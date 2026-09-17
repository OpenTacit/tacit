// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"html"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/ingress"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/pkg/intelligence"
)

// Publishing, from the registry's side.
//
// The whole of it is a goroutine inside `tacit serve` that dials the shared
// ingress and answers requests off the connections it opened. There is no
// second process to run, no name to request, and nobody to ask: this registry
// generated an instance key for itself the first time it needed one, and that
// key is its identity for the rest of its life. Present it, and the ingress
// either enrols it and allocates a hostname or recognises it and returns the
// hostname it allocated before.
//
// The setting is a switch because that is all the operator should have to
// decide. Everything the plan calls "claim" happens in the handshake.

// PublishState is what the settings page reports.
type PublishState struct {
	// Enabled is the setting, independent of whether the tunnel is up.
	Enabled bool
	// Connected is whether the ingress is answering right now.
	Connected bool
	// URL is the public address this registry answers on, once the ingress has
	// said what it is. It survives a disconnection, because the name is stable
	// and telling the operator "no address" during a blip would be wrong.
	URL string
	// Ingress is the tunnel address in use.
	Ingress string
	// Since is when the current connection was established.
	Since time.Time
	// LastError is why it is not connected, if it is not.
	LastError string
}

// publisher owns the tunnel goroutine's lifetime.
type publisher struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	state   PublishState
	running bool
}

// PublishState returns a snapshot for the settings page.
func (s *Server) PublishState() PublishState {
	s.pub.mu.Lock()
	defer s.pub.mu.Unlock()
	return s.pub.state
}

// StartPublishing brings the tunnel up, or does nothing if it is already up.
// Called at startup when the setting is on, and by the settings page the moment
// an operator turns it on — a switch that needed a restart to take effect would
// not be a switch.
func (s *Server) StartPublishing(handler http.Handler) {
	s.pub.mu.Lock()
	if s.pub.running {
		s.pub.mu.Unlock()
		return
	}

	addr := strings.TrimSpace(s.cfg().PublishIngress)
	if addr == "" {
		addr = config.DefaultIngress
	}
	key, err := ingress.LoadOrCreateKey(config.ConfigDir())
	if err != nil {
		s.pub.state = PublishState{Enabled: true, Ingress: addr,
			LastError: "could not read this registry’s instance key: " + err.Error()}
		s.pub.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.pub.cancel, s.pub.running = cancel, true
	s.pub.state = PublishState{
		Enabled: true,
		Ingress: normalizeIngressAddr(addr),
		URL:     s.pub.state.URL, // a known address survives a restart of the tunnel
	}
	s.pub.mu.Unlock()

	client := &ingress.Client{
		Addr:    normalizeIngressAddr(addr),
		Token:   key,
		Version: s.Version,
		// TLS everywhere except a loopback ingress, which is what a laptop test
		// runs and which has no certificate to present.
		TLS: s.cfg().PublishTLS && !isLoopbackAddr(addr),
		Log: func(format string, args ...any) {
			// The tunnel coming up is announced by init, with the link on it.
			// Its failures and retries are not announced by anybody.
			if s.QuietStartup && format == ingress.PublishedFormat {
				return
			}
			log.Printf("[publish] "+format, args...)
		},
		// What this registry tells the proxy's operator about itself: one
		// number, so they can see how much each tenant is worth carrying. It is
		// the figure the Members page shows this organization's own
		// administrator, from the same counter — a tenant and its host should
		// not be looking at two different truths about the tenant.
		//
		// Nothing that identifies a member goes with it. The proxy could count
		// members itself (it terminates TLS, and the key is on every request)
		// and deliberately does not; sending the total is what keeps that line
		// where it is.
		Stats: s.MemberActivity,
	}
	// Import receipts and, past their floors, rounded outcomes on techniques this
	// registry took from OTHERS. Unconditional under Global Access, because
	// confirming Global Access already publishes this registry's own techniques
	// with their measured rates: this is the same class of number about
	// techniques it did not write, floored harder, and a separate opt-in for it
	// could not clear its own cross-org threshold of five reporting registries.
	client.Intelligence = func() *intelligence.Report {
		publisher, err := s.publisher()
		if err != nil {
			log.Printf("[publish] intelligence identity: %v", err)
			return nil
		}
		report, err := federation.IntelligenceReport(s.Store, publisher.ProviderID, publisher.Key, time.Now())
		if err != nil {
			log.Printf("[publish] intelligence report: %v", err)
			return nil
		}
		return report
	}
	// The tunnel serves this registry's own handler rather than looping back
	// through its listener: one hop fewer, and it works whatever address the
	// registry is bound to.
	client.SetHandler(handler)
	client.OnWelcome = func(w ingress.Welcome) {
		s.pub.mu.Lock()
		s.pub.state.Connected = true
		s.pub.state.URL = w.URL
		s.pub.state.Since = time.Now()
		s.pub.state.LastError = ""
		s.pub.mu.Unlock()
	}
	// A dropped session is reported while it is down, not once the client gives
	// up — which it no longer does for a transient refusal. The URL is kept, so
	// Settings shows the address with a "reconnecting" badge and the reason
	// (publishStatusHTML), rather than a green line over an address that is
	// currently answering nothing.
	client.OnDisconnect = func(err error) {
		s.pub.mu.Lock()
		s.pub.state.Connected = false
		if err != nil {
			s.pub.state.LastError = err.Error()
		}
		s.pub.mu.Unlock()
	}

	go func() {
		err := client.Run(ctx)
		s.pub.mu.Lock()
		s.pub.running, s.pub.state.Connected = false, false
		if err != nil && ctx.Err() == nil {
			s.pub.state.LastError = err.Error()
		}
		s.pub.mu.Unlock()
		// Say it out loud as well as recording it. Client.Run only returns when
		// retrying cannot help — a refused key, a closed ingress — and a
		// registry that quietly stopped trying to publish, with the reason
		// visible only to whoever thinks to open Settings, is a registry whose
		// public address silently does not exist.
		if err != nil && ctx.Err() == nil {
			log.Printf("[publish] stopped: %v", err)
		}
	}()
}

// StopPublishing drops the tunnel. The public address stops answering
// immediately; the name stays allocated, so turning the setting back on returns
// the same address.
func (s *Server) StopPublishing() {
	s.pub.mu.Lock()
	cancel := s.pub.cancel
	s.pub.cancel, s.pub.running = nil, false
	s.pub.state.Enabled, s.pub.state.Connected = false, false
	s.pub.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// isLoopbackAddr reports whether an ingress address is on this machine, which
// is the one case where dialling in the clear is right.
func isLoopbackAddr(addr string) bool { return ingress.IsLoopbackAddr(addr) }

// normalizeIngressAddr turns what an operator types into what the tunnel dials.
//
// The setting they are asked for is a hostname — "ingress.tacit.zone" — because
// that is what they were given and what they can check by pasting into a
// browser. A scheme is an implementation detail of how the tunnel reaches it:
// https everywhere except this machine, where a test ingress has no certificate
// and demanding one would make the local case the awkward one.
//
// An explicit URL is honoured as typed, so an unusual deployment stays
// expressible without a new setting.
func normalizeIngressAddr(raw string) string { return ingress.NormalizeAddr(raw) }

// publishAddressHTML is the address the switch gives you, rendered to sit beside
// the switch itself. That is the entire point of the feature from the operator's
// side, and it used to be four rows further down the panel, under two toggles
// and two number fields.
//
// It is rendered whether or not the switch is on, because the script shows it
// the moment the box is ticked. What it must never do is claim a live
// connection that a save has not made yet, so the chip reports the SAVED tunnel:
// "on save" is the honest label for an address that will return when the form is
// submitted, and the name does return — it belongs to this registry's key.
//
// The key's fingerprint used to ride along here. It named the file the address
// depends on without ever saying so, and the one fault it helps with — a restore
// that came up on a different key, under a different name — is a fault the
// changed hostname reports first. `tacit init` still prints it, beside the path
// it is about.
func publishAddressHTML(st PublishState) string {
	switch {
	case st.URL == "" && st.LastError != "":
		return `<span class="set-addr-bad">Not connected: ` + html.EscapeString(st.LastError) + `</span>`
	case st.URL == "" && st.Enabled:
		return `<span class="set-addr-wait">Connecting to ` + html.EscapeString(st.Ingress) + `…</span>`
	case st.URL == "":
		return `<span class="set-addr-wait">Address assigned when you save.</span>`
	}

	var state string
	switch {
	case !st.Enabled:
		state = `<span class="set-chip set-chip-muted">on save</span>`
	case st.Connected:
		state = `<span class="set-chip set-chip-good">ready</span>`
	default:
		state = `<span class="set-chip set-chip-warn">reconnecting</span>`
		if st.LastError != "" {
			state += ` <span class="set-addr-bad">` + html.EscapeString(st.LastError) + `</span>`
		}
	}
	return `<a href="` + html.EscapeString(st.URL) + `">` + html.EscapeString(st.URL) + `</a> ` + state
}

// publishStatusHTML is what is left once the address has moved up beside the
// switch: the callback an operator has to go and register somewhere else, and
// why sign-in is not using it yet. Both are things to DO, which is why they
// survive, and both appear only when they apply.
//
// The standing caveat that the proxy terminates TLS has gone. It is a property
// of the arrangement rather than a fault or a decision, it rendered on every
// load forever, and the guide already carries it — see
// docs/user-guide/10-get-started/02-set-up-a-registry.md.
func publishStatusHTML(st PublishState, callback string, usableCallback bool) string {
	if !st.Enabled || st.URL == "" {
		return ""
	}
	out := ""
	if !usableCallback {
		out += `<p class="hint"><strong>Sign-in still uses the configured callback</strong>, because identity ` +
			`providers refuse a plain HTTP redirect URI. Add a TLS certificate to the proxy so sign-in can use its public address.</p>`
	}
	if callback != "" {
		out += `<p class="lbl">Register this callback with your identity provider</p>` +
			`<pre>` + html.EscapeString(callback) + `</pre>` +
			`<p class="hint">Add it before the next sign-in to avoid a ` +
			`<code>redirect_uri mismatch</code> error.</p>`
	}
	return out
}

// PublishedBase is the public address this registry answers on through the
// shared proxy, or "" when it is not published.
//
// While publishing is on this SUPERSEDES the configured external URL. That is
// the point of the switch: the proxy is the registry's public address, and two
// competing answers to "where am I reachable?" produce join links pointing one
// way and sign-in redirects pointing the other.
//
// It survives a dropped tunnel deliberately. The name belongs to this
// registry's key, so a blip does not change the address, and rewriting every
// link and redirect for the duration of an outage would be worse than serving
// the address that is about to work again.
func (s *Server) PublishedBase() string {
	st := s.PublishState()
	if !st.Enabled {
		return ""
	}
	return strings.TrimRight(st.URL, "/")
}

// OIDCRedirectURI is the callback the identity provider must send members back
// to. Published, it is on the proxy address; otherwise it is whatever the
// operator configured.
//
// Changing it is not free, and the settings page says so: an identity provider
// only redirects to URIs registered with it, so a registry that starts
// publishing must have the new callback registered before anyone signs in
// again. Silently switching and letting the provider produce a
// "redirect_uri mismatch" would be the worst of both.
func (s *Server) OIDCRedirectURI() string {
	// Only an https address can be a callback. Identity providers refuse
	// plaintext redirect URIs outright — Google answers redirect_uri_mismatch —
	// so adopting an http:// published address here would lock the operator out
	// of their own registry the moment they enabled a switch. A proxy without a
	// certificate is a fine place to serve a dashboard from and not a place a
	// sign-in can come back to, and those are separable.
	if base := s.PublishedBase(); strings.HasPrefix(base, "https://") {
		return base + s.cfg().BasePath + "/auth/callback"
	}
	return s.cfg().OIDCRedirectURI
}

// PublishedCallbackUsable reports whether the published address can carry the
// sign-in flow. False when publishing is on but the proxy serves plain HTTP —
// which the settings page says out loud, because the alternative is an operator
// wondering why one setting moved their join links and not their sign-in.
func (s *Server) PublishedCallbackUsable() bool {
	base := s.PublishedBase()
	return base == "" || strings.HasPrefix(base, "https://")
}

// oidcCallbackToRegister is the callback an operator must add at their identity
// provider, or "" when there is nothing to do — no OIDC configured, or the
// callback is the one they already registered.
func (s *Server) oidcCallbackToRegister() string {
	if s.OIDC == nil {
		return ""
	}
	uri := s.OIDCRedirectURI()
	if uri == "" || uri == s.cfg().OIDCRedirectURI {
		return ""
	}
	return uri
}

// orElse is the configured value or the default, for display.
func orElse(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
