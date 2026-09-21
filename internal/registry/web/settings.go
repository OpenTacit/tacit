// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Settings page (/settings, post-setup): read-only for everyone,
// editable for admins — but only the settings that can honestly take effect
// live (the hot-apply set from setup.go). Deployment identity — host, port,
// storage, embedder, OIDC — stays file-edit-and-restart on purpose: a web
// edit that silently does nothing until a restart would lie, and several of
// those settings are takeover-grade on a public registry (DB URL, OIDC
// issuer, ONNX library path).
//
// Admin = signed-in member whose verified OIDC email is in TACIT_ADMIN_EMAILS.
// No OIDC → no web editing at all, even though the dashboard is open: an open
// registry must never hand config-write to the network. Saves are CSRF-bound
// to the session (stateless HMAC token, same posture as the OIDC txn tokens).
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/opentacit/tacit/internal/llmprovider"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// isAdmin reports whether this session may change the registry.
//
// With an identity provider it is the operator's admin list: a verified session
// AND membership in it. With one member and no provider it is the owner, who
// proved they could read the console this registry was started from — the same
// proof the first-run claim code asks for, and the only person there is. A
// single-member registry whose own operator could not open its settings would
// be a dashboard that shows them their configuration and refuses to let them
// touch it.
func (s *Server) isAdmin(user oidc.Claims) bool {
	if user == nil {
		return false
	}
	if s.OIDC == nil {
		return s.cfg().OwnerEnabled() && user["owner"] == true
	}
	email, _ := user["email"].(string)
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, a := range s.cfg().AdminEmails {
		if a == email {
			return true
		}
	}
	return false
}

// namedAdmin reports a session that administers by being ON the administrators
// list — which is what makes its own row one this page will not remove.
//
// An owner session is not one: it administers by the owner secret, so there is
// no row of its own to protect and a registry that names no administrators yet
// is exactly the state it saves from. Neither is a session on a registry whose
// list is already empty — there is nothing there to remove, and every member
// who signs in is already an administrator.
func (s *Server) namedAdmin(user oidc.Claims) bool {
	self := claimEmail(user)
	return self != "" && hasEmail(s.cfg().AdminEmails, self)
}

// claimEmail is the signed-in member's address, lowercased — "" for a session
// that has none, which is what an owner session has.
func claimEmail(user oidc.Claims) string {
	if user == nil {
		return ""
	}
	email, _ := user["email"].(string)
	return strings.ToLower(strings.TrimSpace(email))
}

func hasEmail(list []string, email string) bool {
	for _, e := range list {
		if strings.EqualFold(strings.TrimSpace(e), email) {
			return true
		}
	}
	return false
}

// csrfToken derives a stateless CSRF token from the session cookie: HMAC over
// the session token with the OIDC secret. Bound to the session (a token
// lifted from one member is useless with another's cookie), verifiable
// without server-side state, and rotates when the session does.
func (s *Server) csrfToken(r *http.Request) string {
	secret := s.sessionSecret()
	if len(secret) == 0 {
		return ""
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("settings:" + c.Value))
	return hex.EncodeToString(mac.Sum(nil))
}

// sessionSecret is whatever signs this registry's sessions — the provider's, or
// the owner's where there is no provider. The CSRF token hangs off it, so a
// form on a single-member registry is protected the same way as one on an
// organization's.
func (s *Server) sessionSecret() []byte {
	if s.OIDC != nil {
		return s.OIDC.Secret
	}
	if s.cfg().OwnerEnabled() {
		return []byte(s.cfg().OwnerSecret)
	}
	return nil
}

func (s *Server) checkCSRF(r *http.Request) bool {
	want := s.csrfToken(r)
	return want != "" && hmac.Equal([]byte(want), []byte(r.PostFormValue("csrf")))
}

// What a save does to the tunnel.
const (
	publishNothing = iota
	publishRestart
	publishStop
)

// publishAction decides whether a settings save touches the tunnel.
//
// It used to stop and re-dial on EVERY save, on the reasoning that the address
// may have changed and starting again is cheap. It is not cheap when the save
// arrived THROUGH the tunnel: the response goes back over a connection that has
// just been closed. Measured against a local ingress, that save answered 502
// and the ingress logged "proxy error … EOF, tunnel down"; in a browser it is a
// hang, and then "this registry has no spare connection right now". Saving an
// API key took the dashboard down with it.
//
// The switch is now the only tunnel setting a save can change — the address is
// file-and-restart — so turning it on or off is the whole of it.
func publishAction(wasServing, publishOn bool) int {
	switch {
	case publishOn && !wasServing:
		return publishRestart
	case !publishOn && wasServing:
		return publishStop
	}
	return publishNothing
}

// handleSettingsSave applies an admin's edit of the hot-appliable settings:
// validate, patch registry.env in place (preserving the operator's comments
// and every untouched line), hot-apply, redirect back.
func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	user := s.sessionUser(r)
	if !s.isAdmin(user) {
		http.Error(w, "only this registry’s administrator can edit settings—a member of "+
			"TACIT_ADMIN_EMAILS, or its owner where there is no identity provider",
			http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	if !s.checkCSRF(r) {
		http.Error(w, "stale form; reload the settings page and try again", http.StatusForbidden)
		return
	}

	// Test configuration, not Save changes. The button carries which one it was
	// (settings_view.go, saveButton) and this is the whole difference: the same
	// form, asked of the provider instead of written to the file. It is the
	// no-script path too — with a script the fetch goes straight to
	// /settings/signin/test and the page never reloads.
	if strings.TrimSpace(r.PostFormValue("do")) == "test" && s.canConfigureSignIn(user) {
		s.renderSignInTest(w, r, user)
		return
	}

	get := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	updates := map[string]string{}
	var problems []string

	// The operator's own address is theirs, whatever Global Access is currently
	// serving instead — so what decides here is whether the FIELD came back, not
	// whether the proxy is up. It used to be the latter, and that dropped an edit
	// made in the same save as turning Global Access off: the field was editable
	// again by then, and the handler was still ignoring it. An absent field (an
	// older page, a non-admin form) still leaves the stored value alone rather
	// than reading "" as "clear it".
	if r.PostForm.Has("external_url") {
		if v := get("external_url"); v != "" {
			if u, err := url.Parse(v); err != nil || u.Scheme == "" || u.Host == "" {
				problems = append(problems, "Your own address must be an absolute URL.")
			}
		}
		updates["TACIT_EXTERNAL_URL"] = get("external_url")
	}
	// Federation identity is not offered here any more, so it must not be
	// written here either — an absent field reads back as "" and would clear it.
	// The provider ID looks after itself (providerIDLocked pins the first real
	// public address and keeps it), the name is a label peers see, and the
	// first-run wizard asks for both. registry.env and a restart for the rest.

	// Global Access. The switch takes effect now rather than at the next restart:
	// an operator who ticks a box expects an address, not a maintenance window.
	publishOn := get("publish") != ""
	updates["TACIT_GLOBAL_ACCESS"] = map[bool]string{true: "1", false: "0"}[publishOn]
	// Retire keys that no longer decide anything, so the file stops carrying
	// settings the registry does not read. TACIT_SHARE_FEDERATED_OUTCOMES was
	// the opt-in for reporting outcomes on imported techniques, which Global
	// Access now covers.
	for _, retired := range []string{"TACIT_SHARE_FEDERATED_OUTCOMES"} {
		if os.Getenv(retired) != "" {
			updates[retired] = ""
		}
	}
	// Confirming the technique-sharing half is a SEPARATE act from turning the switch
	// on, and it is one-way. An upgraded registry arrives with the switch already
	// on and this unset (the staged state), so a form post that merely saved the
	// switch must not be read as consent to publish techniques.
	//
	// Turning Global Access off clears it: the next person to turn it on is
	// consenting again, and a stored yes from a previous arrangement is not their
	// decision.
	confirmed := s.cfg().GlobalAccessConfirmed
	switch {
	case !publishOn:
		confirmed = false
	case get("global_access_confirm") != "":
		confirmed = true
	}
	updates["TACIT_GLOBAL_ACCESS_CONFIRMED"] = map[bool]string{true: "1", false: "0"}[confirmed]
	// The proxy address is deployment, not a choice: it belongs in registry.env
	// beside the host and the port. The page must not write it either — an absent
	// field reads back as "" and would clear the operator's line. The Public feed
	// evidence floor left the page for the same reason it left the operator's
	// hands: federation/public.go argues the default down rather than up, and a
	// registry deep enough in evidence to want it higher can say so in the file.

	// Sign-in: Personal → Shared, if that is what this save is. It adds its
	// settings to the same patch as everything else on the page, so one Save is
	// one write to the file (signin.go).
	signInOn, signInProblems := s.stageSignIn(r, user, updates)
	problems = append(problems, signInProblems...)

	// One value per row now, and still one comma-separated value from an older
	// page or the first-run wizard — splitEmailList reads both the same way.
	//
	// A form that did not carry the field at all leaves the list alone, for the
	// same reason the operator's own address does: "" would read as "clear it",
	// and clearing the administrators makes every member who signs in one. The
	// page always posts at least the blank row, so a deliberate emptying still
	// arrives as an empty value rather than an absent field.
	var list []string
	if vals, ok := r.PostForm["admin_emails"]; ok {
		list = splitEmailList(vals)
		for _, e := range list {
			if !strings.Contains(e, "@") {
				problems = append(problems, fmt.Sprintf("%q is not a valid email address.", e))
			}
		}
		// The one edit this list refuses: taking your own address out of it.
		// Nobody demotes themselves here, whoever else is named — the settings
		// that decide who administers this registry are not a place to discover
		// you have just signed out of them, and an empty list is worse than a
		// lockout (every member who signs in becomes an administrator). Removing
		// an administrator is somebody else's act: another administrator, or
		// registry.env on the console.
		if self := claimEmail(user); s.namedAdmin(user) && !hasEmail(list, self) {
			problems = append(problems, "You cannot remove yourself as an administrator. "+
				"Another administrator can do it, or edit TACIT_ADMIN_EMAILS in registry.env "+
				"on the registry’s console.")
		}
		updates["TACIT_ADMIN_EMAILS"] = strings.Join(list, ", ")
	}

	// Secrets are never echoed into the form, so empty means "unchanged" —
	// not "clear".
	if v := get("llm_key"); v != "" {
		updates["TACIT_LLM_API_KEY"] = v
	}

	// Model selection for the registry's OWN model features (suggestion
	// research, cluster descriptions, tag-merge). The researcher reads these
	// from the environment per call, so they hot-apply like the key. Empty
	// clears the line and the provider default applies. These do NOT reach the
	// member-side audit layer (each developer sets TACIT_CHAR_MODEL /
	// TACIT_SYNTH_MODEL in their own agent env — the registry never reads them).
	if v := get("llm_provider"); v != "" && !llmprovider.Valid(v) {
		problems = append(problems, "Unknown model provider.")
	}
	updates["TACIT_LLM_PROVIDER"] = get("llm_provider")
	if v := get("llm_base_url"); v != "" {
		if u, err := url.Parse(v); err != nil || u.Scheme == "" || u.Host == "" {
			problems = append(problems, "Model base URL must be an absolute URL.")
		}
	}
	updates["TACIT_LLM_BASE_URL"] = get("llm_base_url")
	updates["TACIT_SUGGEST_MODEL"] = get("suggest_model")
	updates["TACIT_TAGMERGE_MODEL"] = get("tagmerge_model")

	// Automation — evidence-gated autonomy. retrieval.AutonomyGate reads these
	// from s.Cfg per request, so they hot-apply. An unchecked box is "0", not a
	// dropped line, so turning autonomy off is explicit in the file.
	autonomyOn := get("autonomy") != ""
	updates["TACIT_AUTONOMY"] = "0"
	if autonomyOn {
		updates["TACIT_AUTONOMY"] = "1"
	}
	// The thresholds these switches run on are not on the page any more, so they
	// are not written here either — registry.env keeps them, and a save must not
	// rewrite a line it no longer asks about.

	// Automated review (docs/learning/validation-without-review.md): auto-shadow
	// routes screened machine techniques into evaluation; auto-promote graduates a
	// shadow technique to serving on its fit evidence. Both hot-apply (the scheduler
	// reads s.Cfg's gate each cycle; suggest reads AutoShadow per run).
	autoShadowOn := get("auto_shadow") != ""
	updates["TACIT_AUTO_SHADOW"] = "0"
	if autoShadowOn {
		updates["TACIT_AUTO_SHADOW"] = "1"
	}
	autoPromoteOn := get("auto_promote") != ""
	updates["TACIT_AUTO_PROMOTE"] = "0"
	if autoPromoteOn {
		updates["TACIT_AUTO_PROMOTE"] = "1"
	}
	autoDiscoverOn := get("auto_discover") != ""
	updates["TACIT_AUTO_DISCOVER"] = "0"
	if autoDiscoverOn {
		updates["TACIT_AUTO_DISCOVER"] = "1"
	}

	if len(problems) > 0 {
		// The error sits above the tab strip, so it is visible whichever tab the
		// re-render lands on — the offending field may be under a different one.
		s.sendHTML(w, http.StatusBadRequest, s.renderShell(page{active: "settings",
			crumbs: s.registryCrumbs("General"),
			content: `<p class="error">` + html.EscapeString(strings.Join(problems, " ")) +
				`</p>` + s.settingsView(r, user, settingsOutcome{})}, user))
		return
	}

	path := config.RegistryEnvPath()
	if err := config.PatchEnv(path, updates); err != nil {
		s.sendHTML(w, http.StatusInternalServerError, s.renderShell(page{active: "settings",
			crumbs:  s.registryCrumbs("General"),
			content: `<p class="error">Could not write ` + html.EscapeString(path+": "+err.Error()) + `</p>`}, user))
		return
	}

	// Hot-apply — this whole form is the hot-apply set, nothing here needs a
	// restart. One mutateCfg for the lot, so a request reading two of these
	// settings cannot catch the save between them.
	if v, ok := updates["TACIT_LLM_API_KEY"]; ok {
		_ = os.Setenv("TACIT_LLM_API_KEY", v)
	}
	for _, k := range []string{"TACIT_LLM_PROVIDER", "TACIT_LLM_BASE_URL", "TACIT_SUGGEST_MODEL", "TACIT_TAGMERGE_MODEL"} {
		_ = os.Setenv(k, updates[k]) // empty string reads back as unset → provider default
	}
	wasPublishing := s.cfg().GlobalAccess
	wasServing := s.GlobalAccessServing()
	s.mutateCfg(func(c *config.Config) {
		if v, ok := updates["TACIT_EXTERNAL_URL"]; ok {
			c.ExternalURL = v
		}
		if v, ok := updates["TACIT_ADMIN_EMAILS"]; ok {
			c.AdminEmails = splitEmailsCSV(v)
		}
		c.AutonomyEnabled = autonomyOn
		c.AutoShadow = autoShadowOn
		c.AutoPromoteEnabled = autoPromoteOn
		c.AutoDiscover = autoDiscoverOn
		c.GlobalAccess = publishOn
		c.GlobalAccessConfirmed = confirmed
	})

	// Global Access is the one setting with a process behind it, so applying it
	// means starting or stopping that process rather than storing a value.
	//
	// WHAT THE TUNNEL RUNS ON IS THE SETTING, NOT THE FEED. This asked
	// GlobalAccessServing(), which is GlobalAccess AND confirmed — a different
	// question, and false on every STAGED registry: reachable through the proxy
	// with the commons withheld, which is what `tacit init` leaves behind and
	// what an operator is looking at the first time they open this page. So
	// every save there read as "the tunnel is down and the switch is on" and
	// restarted it — including, for an operator reading this page through that
	// tunnel, the pipe their own response was about to be written into. The
	// save worked; the answer never arrived, and the browser sat loading until
	// the tunnel came back. Startup starts the tunnel on cfg.GlobalAccess
	// alone (cmd/tacit/main.go), and so does this.
	// Touch the tunnel only when the tunnel's own setting changed — and only
	// once this response is on the wire, for the same reason. An operator who
	// reaches Settings through the tunnel is reading it down the pipe this
	// touches.
	tunnel := publishAction(wasPublishing, publishOn)
	applyTunnel := func() {
		if tunnel == publishNothing {
			return
		}
		flush(w)
		s.StopPublishing()
		if tunnel == publishRestart {
			s.StartPublishing(s.PublishHandler)
		}
	}
	// Re-rank now rather than at the next scheduler tick. Confirming the commons
	// and then seeing an empty channel for an hour would read as a broken feature;
	// and a changed floor is a changed channel, which the operator wants to see
	// the moment they save. Turning the switch off clears the membership.
	if wasServing != s.GlobalAccessServing() || publishOn {
		s.RecomputePublicChannel()
	}

	email, _ := user["email"].(string)
	log.Printf("[settings] %s updated %s", email, strings.Join(config.SortedKeys(updates), " "))

	// Turning sign-in on is the one save this process does not survive: the
	// settings are read at startup, so the registry restarts. A redirect would
	// send the browser back to a port that is going away mid-flight, so this save
	// answers with a page that says what happened and waits for the registry to
	// come back — see signInDone.
	if signInOn {
		restarting := s.serviceRestarts(path)
		s.sendHTML(w, 200, s.renderShell(page{active: "settings",
			crumbs: []crumb{{label: "Settings", href: "/settings"}, {label: "Sign-in", href: ""}},
			content: signInDone(updates["TACIT_OIDC_ISSUER"], updates["TACIT_OIDC_REDIRECT_URI"],
				strings.Join(list, ", "), restarting)}, user))
		applyTunnel()
		if restarting {
			flush(w)
			s.restartForSignIn()
		}
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
	applyTunnel()
}

func splitEmailsCSV(v string) []string {
	var out []string
	for _, e := range strings.Split(v, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}
