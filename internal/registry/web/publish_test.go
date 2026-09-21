// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"github.com/opentacit/tacit/internal/registry/federation"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/pkg/feed"
	"github.com/opentacit/tacit/pkg/ingress"
)

// The operator-facing contract of publishing: one switch, and once it is on the
// page tells them the address. Everything else about the feature — enrolment,
// naming, the tunnel — is deliberately invisible from here.
func TestSettingsCarriesThePublishSwitch(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")

	page := func() string {
		resp, err := admin.Get(ts.URL + "/settings")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	// Off: a switch, its terms, and no promise of an address.
	html := page()
	for _, want := range []string{`id="set-access"`, "Global Access", `name="publish"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("settings form is missing %q", want)
		}
	}
	// Nothing under the switch describes the arrangement any more. The term list
	// that explained it (Reachable / Contributed / Received), the sentence
	// restating what the switch's own position showed, and the running count of
	// what would be contributed have all gone, and must not come back: the count
	// looked like information but is on the page whether or not anyone is
	// deciding anything, and it moves on its own as techniques are written.
	for _, gone := range []string{"Reachable", "no domain, no certificate",
		"cannot turn off the feed on its own", "contributes to the Public pool",
		"will contribute"} {
		if strings.Contains(html, gone) {
			t.Errorf("the switch carries description again: %q", gone)
		}
	}
	// What survives is the one-way consent gate, and only where it applies: this
	// registry is unpublished, so it is not being asked to confirm anything.
	if strings.Contains(html, "global_access_confirm") {
		t.Error("an unstaged registry is shown the confirm gate")
	}
	if strings.Contains(html, "Public address") {
		t.Error("an unpublished registry is offered an address it does not have")
	}
	// The address is no longer the operator's to type — it is registry.env and a
	// restart — but the default still has to name a host that resolves, because
	// it is what every registry dials without being asked.
	//
	// The premise changed on 2026-08-31: the project runs a shared ingress at
	// ingress.tacit.zone, which resolves and answers. The requirement it was
	// guarding has not changed — the prefilled address must name something an
	// operator can actually reach — so what is asserted is that the default is
	// a real host rather than a placeholder, not that it is localhost.
	if config.DefaultIngress == "" || strings.Contains(config.DefaultIngress, "example") {
		t.Errorf("DefaultIngress is %q; it has to name an ingress this machine can reach",
			config.DefaultIngress)
	}
	// The default is a hostname, like anything an operator would type — and it
	// has to resolve to a URL, because the tunnel is an HTTP upgrade on the
	// proxy's ordinary port rather than a raw port of its own.
	if !strings.HasPrefix(normalizeIngressAddr(config.DefaultIngress), "http") {
		t.Errorf("DefaultIngress %q does not resolve to a URL; the tunnel is an HTTP upgrade now",
			config.DefaultIngress)
	}

	// On and serving: the address, plainly, and a chip saying it can be used.
	srv.pub.state = PublishState{
		Enabled: true, Connected: true,
		URL:     "https://cedar-hollow.tacit.zone",
		Ingress: "ingress.tacit.zone:443",
		Since:   time.Now(),
	}
	html = page()
	if !strings.Contains(html, "https://cedar-hollow.tacit.zone") {
		t.Error("the settings page does not show the address the registry was given")
	}
	// "ready", not "connected". The chip is read by somebody asking whether the
	// address works, not whether a socket is open — and "connected" was the
	// tunnel's word for its own state rather than an answer to that question.
	if !strings.Contains(html, `class="set-chip set-chip-good">ready<`) {
		t.Error("a serving registry's address is not marked ready")
	}
	if strings.Contains(html, ">connected<") {
		t.Error("the address still reports the tunnel's own state rather than whether it can be used")
	}
	// The standing caveat that the proxy terminates TLS is NOT here. It is a
	// property of the arrangement rather than a fault or a decision at the point
	// of making one, it rendered on every load forever, and the guide carries it
	// (docs/user-guide/10-get-started/02-set-up-a-registry.md).
	if strings.Contains(html, "terminates TLS") {
		t.Error("the settings page is explaining the proxy again; that belongs in the guide")
	}

	// Turned off after having an address: the address is still on the switch,
	// ready to return, with a chip that does not claim it is live. Showing it
	// beats the sentence that used to describe it, because ticking the box puts
	// it in force in front of the operator.
	srv.pub.state.Enabled, srv.pub.state.Connected = false, false
	html = page()
	if !strings.Contains(html, "https://cedar-hollow.tacit.zone") {
		t.Error("a switched-off registry is not shown the name that is still its own")
	}
	if !strings.Contains(html, "on save") {
		t.Error("the retained address is not marked as one the save brings back")
	}
	if strings.Contains(html, "set-chip-good") {
		t.Error("a switched-off registry is shown as ready")
	}
}

// markedRows returns every OPENING TAG carrying attr. Only what is inside the
// tag counts: switchScript builds the same attribute in a selector string, and
// matching that would count the script as a row.
func markedRows(page, attr string) []string {
	var out []string
	for _, frag := range strings.Split(page, "<") {
		tag := frag
		if i := strings.Index(tag, ">"); i >= 0 {
			tag = tag[:i]
		}
		if strings.Contains(tag, " "+attr) {
			out = append(out, tag)
		}
	}
	return out
}

// The consent gate is all that is left under the switch, so it has to carry the
// whole of what it is asking for: how many documents leave, and a way to see
// which. Consent to publishing N documents is only real if the N are one click
// away, and the running count that used to supply that link is gone.
func TestTheStagedGateNamesWhatItIsAskingFor(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, false
	if !srv.GlobalAccessStaged() {
		t.Fatal("the registry under test is not staged")
	}
	admin := signIn(t, srv, "ops@example.com")

	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)

	// Nothing has cleared the evidence floor on this registry, which is the
	// ordinary state of a new one: every technique is below it until members
	// have used it. Asking somebody to contribute nothing is a question with no
	// content, so the gate states the position and offers no box.
	if strings.Contains(page, `name="global_access_confirm"`) {
		t.Error("asked for consent to contribute nothing")
	}
	if !strings.Contains(page, "No techniques are eligible for the Public pool") {
		t.Error("the gate does not say why it is not asking")
	}

	// With something eligible, the ask is the ask: a count, a link to what it
	// covers, and a box.
	srv.public.mu.Lock()
	srv.public.members = []federation.PublicMember{{TechniqueID: "read-images-directly", Served: true}}
	srv.public.mu.Unlock()
	resp, err = admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page = readBody(t, resp)
	if !strings.Contains(page, `name="global_access_confirm"`) {
		t.Fatal("a staged registry with an eligible technique is not offered the confirmation")
	}
	if !strings.Contains(page, `href="/federation#public"`) {
		t.Error("the gate asks for consent without a way to see what it covers")
	}
	if !strings.Contains(page, "1 eligible technique") {
		t.Error("the gate does not say how many techniques leave")
	}
	// Staged is only GlobalAccess && !Confirmed, so every registry lands here
	// the first time it moves the switch — including one set up yesterday, which
	// this sentence used to tell a history it does not have.
	if strings.Contains(page, "before Global Access existed") {
		t.Error("the gate tells a new registry it was upgraded")
	}
	// It used to say "the N techniques above", which pointed at a count line
	// that no longer exists.
	if strings.Contains(page, "techniques above") {
		t.Error("the gate points at a count that is no longer on the page")
	}
}

// The address is the reason the switch exists, so it sits ON the switch line —
// inside .set-switch, and outside the <label>, because a link inside a label
// toggles the checkbox instead of opening.
func TestThePublicAddressSitsBesideTheSwitch(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	srv.Cfg.GlobalAccess = true
	srv.pub.state = PublishState{Enabled: true, Connected: true,
		URL: "https://cedar-hollow.tacit.zone"}
	admin := signIn(t, srv, "ops@example.com")

	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)

	row := page[strings.Index(page, `class="set-row set-wide set-switch"`):]
	row = row[:strings.Index(row, "</div>")]
	if !strings.Contains(row, `name="publish"`) || !strings.Contains(row, "https://cedar-hollow.tacit.zone") {
		t.Errorf("the switch line does not carry both the checkbox and the address:\n%s", row)
	}
	// The link must not be inside the label, or clicking it toggles the setting.
	label := row[strings.Index(row, "<label"):strings.Index(row, "</label>")]
	if strings.Contains(label, "<a ") {
		t.Error("the address link is inside the toggle's label, so clicking it flips the switch")
	}
	// The address and its state, and nothing else. The instance key's
	// fingerprint used to ride here: it named the file the address depends on
	// without saying so, and the one fault it helps with — a restore on a
	// different key — shows up as a changed hostname first. `tacit init` prints
	// it beside that path, which is where it means something.
	if strings.Contains(row, "key ") {
		t.Error("the switch line carries the instance key's fingerprint again")
	}
	// And the script that swaps it must not restate a field name the handler
	// reads — two homes for one setting is how a form starts posting twice.
	if n := strings.Count(page, `name="publish"`); n != 1 {
		t.Errorf(`name="publish" appears %d times, want 1`, n)
	}

	// The switch NAMES both positions. A lone "Global Access" stated one and left
	// the other to be inferred — ticked meant the proxy, unticked meant something
	// the control never said.
	for _, pos := range []string{">Global Access<", ">Own address<"} {
		if !strings.Contains(row, pos) {
			t.Errorf("the switch does not name the position %q", pos)
		}
	}
	// One checkbox under the paint, so the form contract is the one
	// handleSettingsSave already reads: presence is on, absence is off.
	if n := strings.Count(row, `type="checkbox"`); n != 1 {
		t.Errorf("%d checkboxes in the switch; the two positions are one control", n)
	}
	// Moved off-screen, not display:none — that would take it out of the tab
	// order, and the only way to reach the setting would be the mouse.
	if strings.Contains(page, ".set-seg input{display:none") {
		t.Error("the switch's checkbox is display:none, so it cannot be tabbed to")
	}
	// Two visible words would be read out as one accessible name.
	if !strings.Contains(row, `aria-label="Global Access"`) {
		t.Error("the switch has no accessible name of its own")
	}
}

// One slot beside the switch holds both answers to "where is this registry
// reachable?" — the proxy's address while Global Access is on, the operator's own
// External URL field while it is off. Ticking has to swap them at once, with no
// round trip, so both are in the DOM either way and the script only flips
// `hidden`. Rendering them server-side is what makes the page correct before the
// script runs — and correct if it never does.
func TestTheSwitchSwapsTheExternalURLWithoutASave(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	srv.Cfg.ExternalURL = "https://old.example.com"
	srv.pub.state = PublishState{URL: "https://cedar-hollow.tacit.zone"}
	admin := signIn(t, srv, "ops@example.com")

	get := func() string {
		resp, err := admin.Get(ts.URL + "/settings")
		if err != nil {
			t.Fatal(err)
		}
		return readBody(t, resp)
	}

	// Off: the operator's field holds the slot and the proxy address waits hidden.
	off := get()
	if !strings.Contains(off, `data-switch-on="publish" hidden`) {
		t.Error("the proxy address is not staged hidden for the switch to reveal")
	}
	if strings.Contains(off, `data-switch-off="publish" hidden`) {
		t.Error("Global Access is off but the External URL field is hidden too — the slot is empty")
	}
	if !strings.Contains(off, `data-switch="publish"`) {
		t.Error("the script has no switch to bind to")
	}
	// The switch is generic now — sign-in is a second one on this page — so the
	// script must bind by key rather than to the one switch it was written for.
	if !strings.Contains(off, `data-switch="signin_shared"`) {
		t.Error("the sign-in switch is not on the page for the same script to drive")
	}

	// The rule generalises: ANY row marked as belonging to Global Access is
	// hidden while the switch is on Own address. The address is one; the consent
	// gate and the callback to register are others, and the next one added gets
	// this for free because the script hides every marked row rather than the
	// two it once knew by name.
	for _, tag := range markedRows(off, `data-switch-on="publish"`) {
		if !strings.Contains(tag, "hidden") {
			t.Errorf("Global Access is off but one of its rows is on screen: %q", tag)
		}
	}

	// On: the two swap, and neither has moved out of the switch's row.
	srv.Cfg.GlobalAccess = true
	srv.pub.state.Enabled = true
	on := get()
	for _, tag := range markedRows(on, `data-switch-on="publish"`) {
		if strings.Contains(tag, "hidden") {
			t.Errorf("Global Access is on but one of its rows is still hidden: %q", tag)
		}
	}
	if strings.Contains(on, `data-switch-on="publish" hidden`) {
		t.Error("Global Access is on but its address is still hidden")
	}
	if !strings.Contains(on, `data-switch-off="publish" hidden`) {
		t.Error("Global Access is on and the External URL field is still taking the slot")
	}
	// Either way the field is the operator's own value and stays enabled: a
	// hidden input posts, a disabled one does not, so an edit made in the same
	// save as flipping the switch survives the flip.
	for _, page := range []string{off, on} {
		if !strings.Contains(page, `name="external_url" type="url" value="https://old.example.com"`) {
			t.Error("External URL is not the operator's own editable value")
		}
		row := page[strings.Index(page, `class="set-row set-wide set-switch"`):]
		row = row[:strings.Index(row, "</div>")]
		if !strings.Contains(row, `name="external_url"`) {
			t.Error("the External URL field is not on the switch's own line")
		}
	}
}

// The instance key is the whole of this registry's identity to the ingress, so
// it must be created once and then never move.
func TestInstanceKeyIsStableAndPrivate(t *testing.T) {
	dir := t.TempDir()
	key, err := ingress.LoadOrCreateKey(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(key, ingress.KeyPrefix) {
		t.Errorf("key %q does not carry the prefix that marks it as a secret", key)
	}
	again, err := ingress.LoadOrCreateKey(dir)
	if err != nil || again != key {
		t.Fatalf("second load returned %q (err %v); an identity that changes is not an identity", again, err)
	}
	info, err := os.Stat(ingress.KeyPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("key file mode is %04o, want 0600 — it is a bearer credential", mode)
	}
	if fp := ingress.FingerprintOf(key); strings.Contains(key, fp) {
		t.Error("the fingerprint is a substring of the key; it must not disclose it")
	}
}

// Publishing supersedes the External URL setting. Two answers to "where is this
// registry reachable?" produce join links pointing one way and sign-in
// redirects pointing the other, which is how an operator ends up at a provider
// error with no idea which setting caused it.
func TestPublishingSupersedesTheExternalURL(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\nTACIT_EXTERNAL_URL=https://old.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.ExternalURL = "https://old.example.com"
	srv.Cfg.OIDCRedirectURI = "https://old.example.com/auth/callback"
	srv.OIDC = &oidc.Provider{Secret: []byte("s"), TTL: time.Hour}
	srv.OIDC.RedirectURIFunc = srv.OIDCRedirectURI

	// Unpublished: the configured value stands.
	if got := srv.OIDCRedirectURI(); got != "https://old.example.com/auth/callback" {
		t.Fatalf("unpublished redirect = %q, want the configured one", got)
	}

	srv.pub.state = PublishState{Enabled: true, Connected: true,
		URL: "https://cedar-hollow.tacit.zone"}

	// Published: the proxy address decides, for the callback and for the links
	// a joining machine records.
	if got := srv.OIDCRedirectURI(); got != "https://cedar-hollow.tacit.zone/auth/callback" {
		t.Errorf("published redirect = %q, want it on the proxy address", got)
	}
	req := httptest.NewRequest("GET", "http://internal.local/", nil)
	if got := srv.externalBaseFor(req); got != "https://cedar-hollow.tacit.zone" {
		t.Errorf("join base = %q, want the proxy address", got)
	}
	if got := srv.oauthBaseURL(); got != "https://cedar-hollow.tacit.zone" {
		t.Errorf("MCP OAuth base = %q, want the proxy address", got)
	}

	// The provider itself must send the same value on both legs of the flow, or
	// the token exchange fails after the member has already signed in.
	if got := srv.OIDC.RedirectURIFunc(); got != "https://cedar-hollow.tacit.zone/auth/callback" {
		t.Errorf("the provider resolves %q; authorize and exchange would disagree", got)
	}

	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")
	srv.OIDC.RedirectURIFunc = srv.OIDCRedirectURI // signIn replaced the provider
	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	page := string(body)

	// The proxy's address is what the switch line shows; the operator's own value
	// is still in the form behind it, enabled, so unticking hands it straight back
	// and a save while published cannot wipe it.
	if !strings.Contains(page, "https://cedar-hollow.tacit.zone") {
		t.Error("the address the proxy decided is not the one on the switch")
	}
	if !strings.Contains(page, `value="https://old.example.com"`) {
		t.Error("the operator's own External URL is not held in the form for when the switch goes off")
	}
	if strings.Contains(page, "external_url") && strings.Contains(page, "external_url disabled") {
		t.Error("the External URL field is disabled, so a save would post nothing for it")
	}
	// And the callback they must register is on the page, not left to be
	// discovered as a provider error.
	if !strings.Contains(page, "https://cedar-hollow.tacit.zone/auth/callback") {
		t.Error("the settings page does not show the callback to register with the identity provider")
	}
}

// Saving while published must not wipe the operator's own External URL: a
// disabled input submits nothing, and "" would otherwise read as "clear it".
func TestSavingWhilePublishedKeepsTheConfiguredExternalURL(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\nTACIT_EXTERNAL_URL=https://old.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.ExternalURL = "https://old.example.com"
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	// Posting the form with publish on brings the tunnel up for real. Unset,
	// the address would be DefaultIngress — the proxy the project runs — and
	// this test enrolled against production on every suite run, taking a
	// hostname it never came back for. A port nothing listens on exercises the
	// same path and reaches nobody.
	srv.Cfg.PublishIngress = "127.0.0.1:1"
	srv.pub.state = PublishState{Enabled: true, Connected: true, URL: "https://cedar-hollow.tacit.zone"}
	admin := signIn(t, srv, "ops@example.com")

	page, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()

	// A browser posts the field, because it is rendered and enabled even while
	// the proxy is deciding the public address. An older page that omits it must
	// not read as "clear it" either, so both shapes are checked.
	for _, form := range []url.Values{
		{"csrf": {extractCSRF(t, string(raw))}, "publish": {"on"}, "admin_emails": {"ops@example.com"},
			"external_url": {"https://old.example.com"}},
		{"csrf": {extractCSRF(t, string(raw))}, "publish": {"on"}, "admin_emails": {"ops@example.com"}},
	} {
		resp, err := admin.PostForm(ts.URL+"/settings", form)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()

		saved, err := os.ReadFile(envPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(saved), "TACIT_EXTERNAL_URL=https://old.example.com") {
			t.Errorf("saving while published wiped the operator's External URL:\n%s", saved)
		}
		if srv.Cfg.ExternalURL != "https://old.example.com" {
			t.Errorf("in-memory External URL became %q", srv.Cfg.ExternalURL)
		}
	}
}

// Turning Global Access off and editing the External URL is one thought, so it
// has to be one save. The handler used to decide by whether the proxy was up at
// the time of the request — which it still was — and threw the edit away.
func TestEditingTheExternalURLWhileTurningGlobalAccessOff(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\nTACIT_EXTERNAL_URL=https://old.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.ExternalURL = "https://old.example.com"
	srv.Cfg.GlobalAccess = true
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	srv.pub.state = PublishState{Enabled: true, Connected: true, URL: "https://cedar-hollow.tacit.zone"}
	admin := signIn(t, srv, "ops@example.com")

	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)

	// The switch unticked and a new URL typed, in the same post — "publish" absent
	// is how a browser sends an unchecked box.
	post, err := admin.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {extractCSRF(t, page)}, "admin_emails": {"ops@example.com"},
		"external_url": {"https://new.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = post.Body.Close()

	if srv.Cfg.GlobalAccess {
		t.Error("Global Access is still on after a save that unticked it")
	}
	if srv.Cfg.ExternalURL != "https://new.example.com" {
		t.Errorf("External URL = %q; the edit made in the same save was dropped", srv.Cfg.ExternalURL)
	}
}

// Enabling a switch must not be able to lock the operator out of their own
// registry. An http:// published address cannot be an OIDC callback — providers
// refuse plaintext redirect URIs — so sign-in keeps using the configured one
// until the proxy has a certificate.
func TestPlaintextProxyDoesNotHijackSignIn(t *testing.T) {
	srv, _ := newServer(t)
	srv.Cfg.OIDCRedirectURI = "https://relay.example.ts.net/auth/callback"

	// A proxy without TLS: links follow it, sign-in does not.
	srv.pub.state = PublishState{Enabled: true, Connected: true,
		URL: "http://indigo-island.192.0.2.10.nip.io:8443"}
	if got := srv.OIDCRedirectURI(); got != "https://relay.example.ts.net/auth/callback" {
		t.Errorf("redirect = %q; a plaintext proxy address must not become the callback", got)
	}
	if srv.PublishedCallbackUsable() {
		t.Error("a plaintext published address is reported as a usable callback")
	}
	req := httptest.NewRequest("GET", "http://internal.local/", nil)
	if got := srv.externalBaseFor(req); got != "http://indigo-island.192.0.2.10.nip.io:8443" {
		t.Errorf("join base = %q; those still follow the proxy, which is reachable", got)
	}

	// With TLS, the two converge and the proxy takes the callback.
	srv.pub.state.URL = "https://cedar-hollow.tacit.zone"
	if got := srv.OIDCRedirectURI(); got != "https://cedar-hollow.tacit.zone/auth/callback" {
		t.Errorf("redirect = %q; an https proxy address should carry sign-in", got)
	}
	if !srv.PublishedCallbackUsable() {
		t.Error("an https published address should be usable as a callback")
	}
}

// The operator types the hostname they were given. Everything else about
// reaching it — scheme, port, path — is the tunnel's business, and asking them
// to know it would be asking them to know how the tunnel works.
func TestIngressAddressAcceptsABareHostname(t *testing.T) {
	for typed, want := range map[string]string{
		"ingress.tacit.zone":          "https://ingress.tacit.zone",
		"  ingress.tacit.zone  ":      "https://ingress.tacit.zone", // pasted with whitespace
		"ingress.tacit.zone:8443":     "https://ingress.tacit.zone:8443",
		"localhost:8443":              "http://localhost:8443", // no certificate on this machine
		"127.0.0.1:8443":              "http://127.0.0.1:8443",
		"https://ingress.tacit.zone":  "https://ingress.tacit.zone", // an explicit URL is honoured
		"http://elsewhere:9000":       "http://elsewhere:9000",
		"https://ingress.tacit.zone/": "https://ingress.tacit.zone", // trailing slash trimmed
		"":                            "",
	} {
		if got := normalizeIngressAddr(typed); got != want {
			t.Errorf("normalizeIngressAddr(%q) = %q, want %q", typed, got, want)
		}
	}
}

// The federation surface has to advertise the address the proxy gave this
// registry, because everything it serves is a document full of URLs someone else
// will dial: channel feed URLs in the descriptor, content_url on every entry.
// Getting this wrong is the worst-shaped bug available — every route answers, and
// nothing in the answer is reachable — so it is pinned rather than assumed.
func TestFederationSurfaceAdvertisesTheProxyAddress(t *testing.T) {
	srv, ts := newServer(t)
	seedPublished(t, srv, "technique-a", "general")

	// No external URL configured: the proxy is this registry's only public name,
	// which is the case the old code got wrong by falling back to loopback.
	srv.Cfg.ExternalURL = ""
	// A provider id deliberately UNLIKE the address, so this test can tell the
	// two apart. They were one value before, and a fixture where they coincide
	// passes whether or not the code distinguishes them.
	srv.Cfg.FeedProviderID = "https://identity.example.org"
	srv.pub.state = PublishState{Enabled: true, Connected: true,
		URL: "https://cedar-hollow.tacit.zone"}

	_, desc := request(t, "GET", ts.URL+"/.well-known/tacit.json", "test-key", "")
	channels, _ := desc["channels"].([]any)
	if len(channels) == 0 {
		t.Fatal("descriptor lists no channels; the fixture did not publish")
	}
	first, _ := channels[0].(map[string]any)
	if got, _ := first["feed_url"].(string); got != "https://cedar-hollow.tacit.zone/f/general/feed.json" {
		t.Errorf("descriptor feed_url = %q, want it on the proxy address", got)
	}

	_, doc := request(t, "GET", ts.URL+"/f/general/feed.json", "test-key", "")
	entries, _ := doc["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("feed has no entries")
	}
	entry, _ := entries[0].(map[string]any)
	if got, _ := entry["content_url"].(string); got != "https://cedar-hollow.tacit.zone/f/techniques/technique-a.md" {
		t.Errorf("entry content_url = %q, want it on the proxy address", got)
	}
	for _, field := range []string{"content_url", "id"} {
		if got, _ := entry[field].(string); strings.Contains(got, "127.0.0.1") {
			t.Errorf("entry %s = %q: a subscriber cannot reach this", field, got)
		}
	}
	// Identity stays on the provider id while location follows the proxy: the
	// entry is NAMED by one and FETCHED from the other.
	if got, _ := entry["id"].(string); got != "https://identity.example.org/techniques/technique-a" {
		t.Errorf("entry id = %q, want it namespaced by the provider id", got)
	}
}

// The address arrives AFTER the switch is flipped — the ingress supplies it in
// OnWelcome — so anything that reads the federation surface before the tunnel is
// up must not freeze that answer. A memoised publisher served loopback URLs for
// the rest of the process's life, and only a restart cleared it.
func TestFederationBaseFollowsAnAddressThatArrivesLater(t *testing.T) {
	srv, ts := newServer(t)
	seedPublished(t, srv, "technique-a", "general")
	srv.Cfg.ExternalURL = ""

	// Someone reads the descriptor first: a health check, a crawler, the
	// dashboard's own Federation page while the tunnel is still connecting.
	if _, err := srv.publisher(); err != nil {
		t.Fatal(err)
	}

	srv.pub.state = PublishState{Enabled: true, Connected: true,
		URL: "https://cedar-hollow.tacit.zone"}

	_, desc := request(t, "GET", ts.URL+"/.well-known/tacit.json", "test-key", "")
	channels, _ := desc["channels"].([]any)
	if len(channels) == 0 {
		t.Fatal("descriptor lists no channels")
	}
	first, _ := channels[0].(map[string]any)
	got, _ := first["feed_url"].(string)
	if strings.Contains(got, "127.0.0.1") {
		t.Errorf("feed_url = %q: the publisher froze the address it had before the tunnel came up", got)
	}
	if got != "https://cedar-hollow.tacit.zone/f/general/feed.json" {
		t.Errorf("feed_url = %q, want the address the ingress supplied", got)
	}
}

// The provider id names every entry this registry has ever published, and a
// subscriber keeps it as provenance. If it moved with the address, every
// subscriber would see the whole channel as new techniques rather than updates — the
// feed duplicated in their drafts lane, the old copies orphaned under an id
// nothing will ever retract. So the location may move and the identity may not.
func TestProviderIDIsPinnedWhileTheAddressMoves(t *testing.T) {
	srv, _ := newServer(t)
	srv.Cfg.ExternalURL = ""
	srv.Cfg.FeedProviderID = "" // nothing configured: the pin decides

	srv.pub.state = PublishState{Enabled: true, Connected: true,
		URL: "https://cedar-hollow.tacit.zone"}
	pub, err := srv.publisher()
	if err != nil {
		t.Fatal(err)
	}
	pinned := pub.ProviderID
	if pinned != "https://cedar-hollow.tacit.zone" {
		t.Fatalf("provider id = %q, want the first durable address", pinned)
	}
	firstEntryID := feed.EntryID(pub.ProviderID, "technique-a")

	// A new address: a different proxy, a re-enrolment, an operator who moved to
	// their own domain.
	srv.pub.state.URL = "https://tenant-a.tacit.zone"
	pub2, err := srv.publisher()
	if err != nil {
		t.Fatal(err)
	}
	if pub2.ProviderID != pinned {
		t.Errorf("provider id moved to %q; every subscriber would re-import the whole channel", pub2.ProviderID)
	}
	if feed.EntryID(pub2.ProviderID, "technique-a") != firstEntryID {
		t.Error("entry ids changed with the address")
	}
	if pub2.BaseURL != "https://tenant-a.tacit.zone" {
		t.Errorf("base url = %q, want it to follow the new address", pub2.BaseURL)
	}

	// The pin outlives the process: a restart reads it back rather than adopting
	// whatever address happens to be current.
	srv.pubID, srv.pubKey = "", nil
	pub3, err := srv.publisher()
	if err != nil {
		t.Fatal(err)
	}
	if pub3.ProviderID != pinned {
		t.Errorf("after a restart the provider id is %q, want the pinned %q", pub3.ProviderID, pinned)
	}
}

// A registry that has never been reachable must not pin itself to loopback: that
// name would outlive the first real address it is given.
func TestLoopbackIsNotPinned(t *testing.T) {
	srv, _ := newServer(t)
	srv.Cfg.ExternalURL = ""
	srv.Cfg.FeedProviderID = ""

	pub, err := srv.publisher()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pub.ProviderID, "127.0.0.1") {
		t.Fatalf("unpublished provider id = %q, want the loopback address", pub.ProviderID)
	}
	if _, err := os.Stat(filepath.Join(srv.Cfg.DataDir, "feed_provider_id")); err == nil {
		t.Fatal("a loopback address was pinned; the first real address could never take over")
	}

	srv.pub.state = PublishState{Enabled: true, Connected: true, URL: "https://cedar-hollow.tacit.zone"}
	pub2, err := srv.publisher()
	if err != nil {
		t.Fatal(err)
	}
	if pub2.ProviderID != "https://cedar-hollow.tacit.zone" {
		t.Errorf("provider id = %q, want the first durable address to be adopted and pinned", pub2.ProviderID)
	}
}

// An operator who set TACIT_FEED_PROVIDER_ID has made the decision themselves,
// and it outranks both the pin and the address.
func TestConfiguredProviderIDOutranksThePin(t *testing.T) {
	srv, _ := newServer(t)
	srv.Cfg.FeedProviderID = "https://techniques.example.org"
	srv.pub.state = PublishState{Enabled: true, Connected: true, URL: "https://cedar-hollow.tacit.zone"}

	pub, err := srv.publisher()
	if err != nil {
		t.Fatal(err)
	}
	if pub.ProviderID != "https://techniques.example.org" {
		t.Errorf("provider id = %q, want the configured value", pub.ProviderID)
	}
	if pub.BaseURL != "https://cedar-hollow.tacit.zone" {
		t.Errorf("base url = %q: the configured identity should not pin the location", pub.BaseURL)
	}
}

// seedPublished puts one active technique in a channel, so the descriptor and the
// feed have something to render.
func seedPublished(t *testing.T, srv *Server, id, channel string) {
	t.Helper()
	c := models.Technique{
		ID: id, Name: "Name " + id, Description: "d", Scope: "general", Status: "active",
		Provenance: "curated", Version: 1, Recipe: "recipe for " + id,
		Channels:  []string{channel},
		CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}
	if err := srv.Store.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
}

// A provider id must be the same value on every startup, and the proxy address
// cannot give that: it arrives asynchronously, so whether it is known when the id
// is first needed depends on whether the tunnel connected before the first reader
// of the federation surface. This project's own registry pinned its configured
// Funnel URL for exactly that reason and would have pinned its proxy name on a
// luckier boot. So identity prefers the configured URL even while location
// prefers the proxy, and the two orders of events must agree.
func TestPinnedIdentityDoesNotDependOnStartupTiming(t *testing.T) {
	pin := func(tunnelUpFirst bool) string {
		t.Helper()
		srv, _ := newServer(t)
		srv.Cfg.FeedProviderID = ""
		srv.Cfg.ExternalURL = "https://registry.example.org"

		up := func() {
			srv.pub.state = PublishState{Enabled: true, Connected: true,
				URL: "https://cedar-hollow.tacit.zone"}
		}
		if tunnelUpFirst {
			up()
		}
		pub, err := srv.publisher()
		if err != nil {
			t.Fatal(err)
		}
		if !tunnelUpFirst {
			up()
			if _, err := srv.publisher(); err != nil {
				t.Fatal(err)
			}
		}
		return pub.ProviderID
	}

	early, late := pin(true), pin(false)
	if early != late {
		t.Errorf("provider id depends on tunnel timing: %q when the tunnel came up first, %q when it came up after", early, late)
	}
	if early != "https://registry.example.org" {
		t.Errorf("pinned %q, want the configured external URL — the one value that is the same every boot", early)
	}

	// Location still prefers the proxy: this is the split, not a reversal.
	srv, _ := newServer(t)
	srv.Cfg.ExternalURL = "https://registry.example.org"
	srv.pub.state = PublishState{Enabled: true, Connected: true, URL: "https://cedar-hollow.tacit.zone"}
	if got := srv.federationBase(); got != "https://cedar-hollow.tacit.zone" {
		t.Errorf("federation base = %q, want the proxy address", got)
	}
}

// The self-hoster Global Access exists for: no URL of their own, so the proxy
// address is the only durable name available and identity has to accept it.
func TestProxyAddressIsPinnedWhenThereIsNoConfiguredURL(t *testing.T) {
	srv, _ := newServer(t)
	srv.Cfg.FeedProviderID = ""
	srv.Cfg.ExternalURL = ""
	srv.pub.state = PublishState{Enabled: true, Connected: true, URL: "https://cedar-hollow.tacit.zone"}

	pub, err := srv.publisher()
	if err != nil {
		t.Fatal(err)
	}
	if pub.ProviderID != "https://cedar-hollow.tacit.zone" {
		t.Errorf("provider id = %q, want the proxy address", pub.ProviderID)
	}
	if feed.PinnedProviderID(srv.Cfg.DataDir) != "https://cedar-hollow.tacit.zone" {
		t.Error("the proxy address was not pinned")
	}
}

// The default ingress names the proxy the project actually runs, so a test that
// turns Global Access on and forgets to redirect the address enrols against
// production: it takes a hostname, never comes back, and leaves an instance the
// operator cannot account for. One test in this file did exactly that on every
// suite run before this guard existed.
//
// Declaring such a connection a test (TACIT_PUBLISH_TEST, on by default under
// `go test`) makes it legible and its name reclaimable. It does not make it
// right, and this is the half that stops it happening.
func TestATestMayNotPublishThroughTheDefaultIngress(t *testing.T) {
	srv, _ := newServer(t)
	srv.Cfg.PublishIngress = "" // as an unconfigured registry has it

	srv.StartPublishing(srv.PublishHandler)
	t.Cleanup(srv.StopPublishing)

	state := srv.PublishState()
	if state.Enabled {
		t.Error("a test was allowed to bring the tunnel up against the default ingress")
	}
	if !strings.Contains(state.LastError, config.DefaultIngress) {
		t.Errorf("the refusal does not name what it refused: %q", state.LastError)
	}
	if !strings.Contains(state.LastError, "TACIT_PUBLISH_INGRESS") {
		t.Errorf("the refusal does not say how to point it somewhere harmless: %q", state.LastError)
	}

	// A test that MEANS to exercise publishing says where, and is let through
	// to fail against its own address rather than somebody else's.
	srv.Cfg.PublishIngress = "127.0.0.1:1"
	srv.StartPublishing(srv.PublishHandler)
	if got := srv.PublishState().LastError; strings.Contains(got, config.DefaultIngress) {
		t.Errorf("a redirected test was still refused: %q", got)
	}
}
