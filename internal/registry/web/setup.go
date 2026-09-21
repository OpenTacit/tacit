// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// First-run setup: the interface an operator gets the very first time they run
// the registry, before any registry.env exists.
//
// INTENT — an unconfigured registry is a hazard, not a blank slate: the
// compiled-in defaults are a KNOWN API key ("dev-key") bound to 0.0.0.0. So a
// first run must not quietly serve; it must get an operator to choose real
// settings. But a setup page reachable by anyone on the network is a second
// hazard — whoever finds the port first would own the registry. The resolution
// is the CLAIM CODE: a one-time code printed to the console the operator
// started the process from. Only someone who can read that console — the
// operator, by definition — can complete setup.
//
// The interactions:
//
//	· `tacit serve` with no registry.env and no TACIT_API_KEY → SETUP MODE:
//	  every dashboard page redirects to /setup, every API route answers
//	  503 "setup required" (never dev-key), and the console prints the code.
//	· GET /setup — the form: every configurable aspect, grouped, with the
//	  compiled defaults prefilled and a freshly generated API key offered.
//	  Required: the claim code and an API key. Everything else has a default.
//	· POST /setup — validates the code (constant-time; ten failures lock
//	  setup until restart), validates the fields, writes
//	  ~/.config/tacit/registry.env (0600) — the same file the systemd unit
//	  reads — hot-applies what can apply live (API key, external URL,
//	  federation identity), and shows the success page: the key ONCE, the
//	  member wiring snippet, and exactly which settings need a restart.
//	· After setup (or on an already-configured registry) GET /setup redirects
//	  to /settings — the read-only settings view: every effective value,
//	  secrets masked, with the file path and restart instructions. Ongoing
//	  changes are edits to that file; that page tells you where and what.
//	  /setup is the wizard; /settings is settings — one URL per job.
package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// SetupState is the first-run gate. Present only when cmd/tacit enables it;
// nil on a configured registry (and in every httptest server, so the suite is
// unaffected).
type SetupState struct {
	mu       sync.Mutex
	code     string
	attempts int
	done     bool
}

// setupMaxAttempts locks setup until restart — a claim code that can be
// brute-forced from the network is no claim code at all.
const setupMaxAttempts = 10

// NewSetupCode mints the one-time claim code, formatted for a human reading it
// off a console: XXXX-XXXX.
func NewSetupCode() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	s := strings.ToUpper(hex.EncodeToString(b))
	return s[:4] + "-" + s[4:]
}

// EnableSetup puts the server in setup mode with the given claim code.
func (s *Server) EnableSetup(code string) {
	s.SetupGate = &SetupState{code: code}
}

// setupActive reports whether the first-run gate is still up.
func (s *Server) setupActive() bool {
	if s.SetupGate == nil {
		return false
	}
	s.SetupGate.mu.Lock()
	defer s.SetupGate.mu.Unlock()
	return !s.SetupGate.done
}

// setupGate wraps the whole mux while setup is pending: the operator can reach
// /setup and the static essentials; everything else is parked. The API answers
// 503 rather than authenticating against a key nobody chose.
func (s *Server) setupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.setupActive() {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		switch {
		case p == "/setup",
			strings.HasPrefix(p, "/apple-touch-icon"),
			// The wizard shares the app stylesheet now — park it and setup
			// renders unstyled.
			p == "/assets/app.css",
			p == "/assets/backdrop.js",
			strings.HasPrefix(p, "/assets/fonts/"),
			p == "/manifest.webmanifest":
			next.ServeHTTP(w, r)
		case strings.HasPrefix(p, "/v1/") || p == "/mcp":
			s.sendError(w, http.StatusServiceUnavailable,
				"setup required; open /setup on this registry")
		default:
			http.Redirect(w, r, "/setup", http.StatusFound)
		}
	})
}

// handleSetup serves the first-run wizard while the gate is up. Once the
// registry is configured the wizard has no job, so /setup redirects to its
// honest home — /settings — rather than doubling as a second settings URL.
// One page per question: /setup is the wizard, /settings is settings.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.setupActive() {
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}
	locked := false
	s.SetupGate.mu.Lock()
	locked = s.SetupGate.attempts >= setupMaxAttempts
	s.SetupGate.mu.Unlock()
	if locked {
		s.sendHTML(w, http.StatusForbidden, setupShell(
			`<p class="empty">Too many failed claim codes. Setup is locked. Restart the registry to generate a new code.</p>`))
		return
	}
	s.sendHTML(w, 200, setupShell(s.setupForm("", nil)))
}

// handleSettings serves the post-setup Settings view — its own destination now
// (the account menu points here), OIDC-gated like every other page since even
// masked secrets and paths are operator-only. An unconfigured registry has no
// settings to show and sends the caller to the first-run wizard; during the
// first-run gate that redirect is usually moot (the gate parks all pages on
// /setup already), but it keeps /settings honest if reached directly.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if s.setupActive() {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	s.htmlView(s.pageSettings)(w, r)
}

// handleSetupSubmit validates the claim code and the form, writes the settings
// file, hot-applies what it can, and reports what needs a restart.
func (s *Server) handleSetupSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.setupActive() {
		// No settings in the refusal body — this answer goes to anyone.
		s.sendHTML(w, http.StatusConflict, s.renderShell(page{active: "settings",
			crumbs: s.registryCrumbs("General"),
			content: `<p class="empty">This registry is already configured. Settings are in <code>` +
				html.EscapeString(config.RegistryEnvPath()) + `</code>. Edit the file and restart.</p>`},
			s.sessionUser(r)))
		return
	}
	_ = r.ParseForm()

	// The claim code first — nothing else is even read until the caller has
	// proven they can see the operator's console.
	gate := s.SetupGate
	gate.mu.Lock()
	if gate.attempts >= setupMaxAttempts {
		gate.mu.Unlock()
		s.sendHTML(w, http.StatusForbidden, setupShell(
			`<p class="empty">Too many failed claim codes. Setup is locked. Restart the registry to generate a new code.</p>`))
		return
	}
	supplied := strings.ToUpper(strings.TrimSpace(r.PostFormValue("claim_code")))
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(gate.code)) != 1 {
		gate.attempts++
		left := setupMaxAttempts - gate.attempts
		gate.mu.Unlock()
		s.sendHTML(w, http.StatusForbidden, setupShell(s.setupForm(
			fmt.Sprintf("That claim code does not match the code printed on the registry’s console (%d attempts left).", left),
			r.PostForm)))
		return
	}
	gate.mu.Unlock()

	// Collect and validate.
	vals, problems := collectSetupForm(r)
	if len(problems) > 0 {
		s.sendHTML(w, http.StatusBadRequest, setupShell(s.setupForm(strings.Join(problems, " "), r.PostForm)))
		return
	}

	// A registry claimed here and a registry set up by `tacit init` used to end
	// in two different states: init gave the operator an owner sign-in and a
	// gated dashboard, while this form left sign-in entirely off unless all four
	// OIDC fields were filled — so the container path produced a registry
	// anybody who could reach the port could administer, and nothing said so.
	//
	// One door, one end state. With no identity provider configured, the claimer
	// becomes the owner, exactly as on the command line. Configure OIDC here and
	// nothing is added: two gates on one dashboard is a question about which is
	// authoritative that nobody wants at three in the morning.
	vals = withOwnerWhenNoIdP(vals)

	path := config.RegistryEnvPath()
	if err := writeRegistryEnv(path, vals); err != nil {
		s.sendHTML(w, http.StatusInternalServerError, setupShell(s.setupForm(
			"Could not write "+path+": "+err.Error(), r.PostForm)))
		return
	}

	// Hot-apply what applies live; everything read at startup needs a restart.
	restart := s.applySetup(vals)

	gate.mu.Lock()
	gate.done = true
	gate.code = ""
	gate.mu.Unlock()

	s.sendHTML(w, 200, setupShell(setupSuccess(path, vals, restart)))
}

// withOwnerWhenNoIdP gives a claimed registry the same gate `tacit init` gives
// one set up on the command line: with no identity provider, the person holding
// the console claim code is the owner. Configure OIDC and nothing is added —
// two gates on one dashboard is a question about which is authoritative that
// nobody wants to answer at three in the morning.
func withOwnerWhenNoIdP(vals []setupField) []setupField {
	if setupNames(vals, "TACIT_OIDC_ISSUER") && setupNames(vals, "TACIT_OIDC_CLIENT_ID") {
		return vals
	}
	return append(vals,
		setupField{"TACIT_AUTH_MODE", config.AuthOwner},
		setupField{"TACIT_OWNER_SECRET", NewAPIKey()})
}

// setupNames reports whether the form supplied a non-empty value for key.
func setupNames(vals []setupField, key string) bool {
	for _, f := range vals {
		if f.Key == key {
			return strings.TrimSpace(f.Value) != ""
		}
	}
	return false
}

// setupField is one written setting.
type setupField struct{ Key, Value string }

// collectSetupForm turns the POST into the env-file lines, validating as it
// goes. Only non-empty values are written — the file documents choices, not
// defaults the operator never touched (except host/port/key, always explicit).
func collectSetupForm(r *http.Request) (vals []setupField, problems []string) {
	get := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	add := func(key, val string) {
		if val != "" {
			vals = append(vals, setupField{key, val})
		}
	}

	apiKey := get("api_key")
	if len(apiKey) < 16 {
		problems = append(problems, "API key must be at least 16 characters (use the generated one).")
	}
	add("TACIT_API_KEY", apiKey)

	host := get("host")
	if host == "" {
		host = "0.0.0.0"
	}
	add("TACIT_HOST", host)
	port := get("port")
	if port == "" {
		port = "8080"
	}
	if _, err := strconv.Atoi(port); err != nil {
		problems = append(problems, "Port must be a number.")
	}
	add("TACIT_PORT", port)
	add("TACIT_EXTERNAL_URL", get("external_url"))

	add("TACIT_DATA", get("data_dir"))
	add("TACIT_DB_URL", get("db_url"))
	add("TACIT_TECHNIQUES_DIR", get("techniques_dir"))

	add("TACIT_FEED_PROVIDER_NAME", get("feed_provider_name"))
	add("TACIT_FEED_PROVIDER_ID", get("feed_provider_id"))

	// OIDC is all-or-none: a partial config silently disables sign-in, which
	// reads as "broken" — refuse it instead.
	issuer, clientID, clientSecret, redirect :=
		get("oidc_issuer"), get("oidc_client_id"), get("oidc_client_secret"), get("oidc_redirect_uri")
	oidcSet := 0
	for _, v := range []string{issuer, clientID, clientSecret, redirect} {
		if v != "" {
			oidcSet++
		}
	}
	switch oidcSet {
	case 0: // sign-in off — fine
	case 4:
		add("TACIT_OIDC_ISSUER", issuer)
		add("TACIT_OIDC_CLIENT_ID", clientID)
		add("TACIT_OIDC_CLIENT_SECRET", clientSecret)
		add("TACIT_OIDC_REDIRECT_URI", redirect)
		add("TACIT_OIDC_SCOPES", get("oidc_scopes"))
		secret := get("session_secret")
		if secret == "" {
			secret = newSecret() // stable across restarts, unlike the per-process fallback
		}
		add("TACIT_SESSION_SECRET", secret)
		if get("cookie_secure") == "1" {
			add("TACIT_COOKIE_SECURE", "1")
		}
	default:
		problems = append(problems, "Complete all four OIDC fields: issuer, client ID, client secret, and redirect URI. Leave all four empty to disable OIDC.")
	}

	add("TACIT_EMBED_MODEL", get("embed_model"))
	if d := get("embed_dim"); d != "" {
		if _, err := strconv.Atoi(d); err != nil {
			problems = append(problems, "Embedding dimension must be a number.")
		}
		add("TACIT_EMBED_DIM", d)
	}
	if v := get("recompute_secs"); v != "" {
		if _, err := strconv.Atoi(v); err != nil {
			problems = append(problems, "Recompute interval must be a number of seconds.")
		}
		add("TACIT_RECOMPUTE_SECS", v)
	}
	admins := get("admin_emails")
	for _, e := range strings.Split(admins, ",") {
		if e = strings.TrimSpace(e); e != "" && !strings.Contains(e, "@") {
			problems = append(problems, fmt.Sprintf("Administrator %q is not a valid email address.", e))
		}
	}
	add("TACIT_ADMIN_EMAILS", admins)

	add("TACIT_LLM_API_KEY", get("llm_key"))
	return vals, problems
}

// writeRegistryEnv persists the operator's choices — 0600, atomic, with a
// header saying who writes this file and who reads it.
func writeRegistryEnv(path string, vals []setupField) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# " + product.Name() + " registry settings written by the first-run setup UI.\n")
	b.WriteString("# Read by `tacit serve` and by the systemd unit (EnvironmentFile=).\n")
	b.WriteString("# Environment variables override these. Edit and restart to change.\n")
	for _, f := range vals {
		fmt.Fprintf(&b, "%s=%s\n", f.Key, f.Value)
	}
	return fsx.WriteFileAtomic(path, []byte(b.String()), 0o600)
}

// applySetup hot-applies what can take effect without a restart and returns
// the list of settings that cannot.
func (s *Server) applySetup(vals []setupField) (restart []string) {
	c := s.cfg()
	byKey := map[string]string{}
	for _, f := range vals {
		byKey[f.Key] = f.Value
	}
	s.mutateCfg(func(c *config.Config) {
		c.APIKey = byKey["TACIT_API_KEY"] // agents can authenticate immediately
		if v := byKey["TACIT_EXTERNAL_URL"]; v != "" {
			c.ExternalURL = v
		}
		if v := byKey["TACIT_FEED_PROVIDER_NAME"]; v != "" {
			c.FeedProviderName = v
		}
		if v := byKey["TACIT_FEED_PROVIDER_ID"]; v != "" {
			c.FeedProviderID = v
		}
	})
	// The suggest researcher reads the environment per call, so this one can
	// apply live too.
	if v := byKey["TACIT_LLM_API_KEY"]; v != "" {
		_ = os.Setenv("TACIT_LLM_API_KEY", v)
	}

	needs := func(key, current string) {
		if v, ok := byKey[key]; ok && v != current {
			restart = append(restart, key)
		}
	}
	needs("TACIT_HOST", c.Host)
	needs("TACIT_PORT", strconv.Itoa(c.Port))
	needs("TACIT_DATA", c.DataDir)
	needs("TACIT_DB_URL", c.DBURL)
	needs("TACIT_TECHNIQUES_DIR", c.TechniquesDir)
	needs("TACIT_EMBED_MODEL", c.EmbedModel)
	needs("TACIT_EMBED_DIM", strconv.Itoa(c.EmbedDim))
	needs("TACIT_RECOMPUTE_SECS", strconv.Itoa(c.RecomputeIntervalSecs))
	if _, ok := byKey["TACIT_OIDC_ISSUER"]; ok {
		restart = append(restart, "OIDC sign-in")
	}
	return restart
}

// newSecret mints a 32-byte hex secret (API keys, session secrets).
func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewAPIKey is the generated default offered on the form.
func NewAPIKey() string { return newSecret() }

// ---- rendering -------------------------------------------------------------

// setupShell is the standalone page frame for setup mode: the normal shell's
// nav would be a row of links that all bounce back here, so setup gets a
// minimal frame of its own.
func setupShell(content string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>` + productHTML() + ` setup</title>` + faviconLink + themeInitScript + bootHideScript + groundCSS + cssLink + `</head>` +
		`<body><main class="setup-wrap"><h1>` + markSVG + ` ` + productHTML() + ` registry setup</h1>` + content + `</main></body></html>`
}

// setupForm renders the wizard. prior carries resubmitted values so a rejected
// form doesn't lose the operator's typing; generated defaults fill first runs.
func (s *Server) setupForm(errMsg string, prior map[string][]string) string {
	c := s.cfg()
	// What the member typed, or the default. RAW: the value is escaped where it
	// is written out and nowhere else, because a value escaped twice arrives at
	// the reader as its own entities — "a&amp;b" on the screen — and the round
	// trip through an error redraws exactly the values most likely to contain
	// one.
	raw := func(name, def string) string {
		if prior != nil {
			if v, ok := prior[name]; ok && len(v) > 0 && v[0] != "" {
				return v[0]
			}
		}
		return def
	}
	val := func(name, def string) string { return html.EscapeString(raw(name, def)) }
	var b strings.Builder
	b.WriteString(`<p class="hint">Configure this registry to continue. Setup writes your settings to <code>` +
		html.EscapeString(config.RegistryEnvPath()) + `</code>. Most take effect at once; the rest need a restart. ` +
		`Other pages and the API remain closed until setup is complete.</p>`)
	if errMsg != "" {
		b.WriteString(`<p class="error">` + html.EscapeString(errMsg) + `</p>`)
	}
	// What the form is about to stand up, drawn from the values it will write.
	store := "a file store on this machine"
	if v := raw("db_url", ""); v != "" {
		store = "a Postgres database"
	}
	// In a plate, like every other picture: the labels are drawn on the panel's
	// own colour so they occlude the lines they cross, and the wizard's ground is
	// not that colour.
	b.WriteString(`<section class="panel stp-hero">` +
		setupScene(raw("host", c.Host)+":"+raw("port", strconv.Itoa(c.Port)), store) +
		`</section>`)
	b.WriteString(`<div class="claim"><b>Claim code</b><p class="hint">Enter the code printed on the console where <code>tacit serve</code> runs. This limits setup to a person with console access.</p></div>`)
	b.WriteString(`<form class="setup-form" method="post" action="/setup">`)
	fmt.Fprintf(&b, `<label class="full"><span class="lbl">Claim code</span><input name="claim_code" required autofocus placeholder="XXXX-XXXX" value="%s"></label>`, val("claim_code", ""))

	// Everything below this line has a working default, and every one of them
	// is also on the Settings page, under a heading, next to an explanation.
	//
	// They were all on this screen: twenty fields — a Postgres URL, a federation
	// provider ID, an embedding dimension, an OIDC redirect URI — in front of
	// somebody who had not yet seen the product do anything. Reading and
	// dismissing a database URL is not a decision a first run should ask for.
	//
	// A native <details>, per the house rule, so it works with JavaScript off
	// and a server operator who came here to set a port finds it in one click.
	b.WriteString(`<details class="setup-advanced"><summary>Advanced settings` +
		`<span class="hint">optional address, storage, sign-in, federation, and retrieval settings</span></summary>` +
		`<div class="setup-form setup-adv">`)

	b.WriteString(`<h2>Access</h2><p class="sect-hint">The API key authenticates agent, miner, and admin calls through <code>X-Tacit-Key</code>. Keep the generated key safe. It appears one more time after setup.</p>`)
	fmt.Fprintf(&b, `<label class="full"><span class="lbl">API key</span><input name="api_key" required minlength="16" value="%s"></label>`, val("api_key", NewAPIKey()))
	fmt.Fprintf(&b, `<label class="full"><span class="lbl">External URL <span class="hint">(optional; public base URL when a proxy or tunnel serves the registry)</span></span><input name="external_url" type="url" placeholder="https://tacit.example.com" value="%s"></label>`, val("external_url", ""))

	b.WriteString(`<h2>Serving</h2>`)
	fmt.Fprintf(&b, `<label><span class="lbl">Host <span class="hint">(127.0.0.1 = this machine)</span></span><input name="host" value="%s"></label>`, val("host", c.Host))
	fmt.Fprintf(&b, `<label><span class="lbl">Port</span><input name="port" inputmode="numeric" value="%s"></label>`, val("port", strconv.Itoa(c.Port)))

	b.WriteString(`<h2>Storage</h2><p class="sect-hint">The zero-dependency file store is the default. Set a Postgres URL for shared or multi-instance deployments.</p>`)
	fmt.Fprintf(&b, `<label><span class="lbl">Data directory</span><input name="data_dir" value="%s"></label>`, val("data_dir", c.DataDir))
	fmt.Fprintf(&b, `<label><span class="lbl">Curated techniques directory</span><input name="techniques_dir" value="%s"></label>`, val("techniques_dir", c.TechniquesDir))
	fmt.Fprintf(&b, `<label class="full"><span class="lbl">Postgres URL <span class="hint">(optional)</span></span><input name="db_url" placeholder="postgres://…" value="%s"></label>`, val("db_url", ""))

	b.WriteString(`<h2>Sign-in (optional)</h2><p class="sect-hint">OIDC limits dashboard access. Complete all four fields to enable it. When OIDC is off, serve the open HTML pages on loopback.</p>`)
	fmt.Fprintf(&b, `<label><span class="lbl">Issuer</span><input name="oidc_issuer" placeholder="https://accounts.google.com" value="%s"></label>`, val("oidc_issuer", ""))
	fmt.Fprintf(&b, `<label><span class="lbl">Client ID</span><input name="oidc_client_id" value="%s"></label>`, val("oidc_client_id", ""))
	fmt.Fprintf(&b, `<label><span class="lbl">Client secret</span><input name="oidc_client_secret" value="%s"></label>`, val("oidc_client_secret", ""))
	fmt.Fprintf(&b, `<label><span class="lbl">Redirect URI</span><input name="oidc_redirect_uri" placeholder="https://…/auth/callback" value="%s"></label>`, val("oidc_redirect_uri", ""))
	fmt.Fprintf(&b, `<label><span class="lbl">Scopes</span><input name="oidc_scopes" value="%s"></label>`, val("oidc_scopes", "openid email profile"))
	fmt.Fprintf(&b, `<label><span class="lbl">Secure cookies <span class="hint">(1 = require HTTPS)</span></span><input name="cookie_secure" value="%s"></label>`, val("cookie_secure", ""))
	fmt.Fprintf(&b, `<label class="full"><span class="lbl">Administrator emails <span class="hint">(optional; comma-separated list of members who can edit settings)</span></span><input name="admin_emails" value="%s"></label>`, val("admin_emails", ""))

	b.WriteString(`<h2>Federation identity</h2><p class="sect-hint">How this registry names itself on published feeds. Defaults derive from the external URL.</p>`)
	fmt.Fprintf(&b, `<label><span class="lbl">Provider name</span><input name="feed_provider_name" value="%s"></label>`, val("feed_provider_name", c.FeedProviderName))
	fmt.Fprintf(&b, `<label><span class="lbl">Provider ID <span class="hint">(a URI)</span></span><input name="feed_provider_id" placeholder="https://tacit.example.com" value="%s"></label>`, val("feed_provider_id", ""))

	b.WriteString(`<h2>Intelligence</h2><p class="sect-hint">The semantic embedder needs a binary built with -tags onnx. The dependency-free hashing default gives lexical matches. The model API key enables registry-side research (suggest).</p>`)
	fmt.Fprintf(&b, `<label><span class="lbl">Embedding model</span><select name="embed_model">`+
		`<option value="hashing-v1"%s>hashing-v1: dependency-free, lexical</option>`+
		`<option value="onnx/all-MiniLM-L6-v2"%s>onnx/all-MiniLM-L6-v2: semantic (needs -tags onnx)</option>`+
		`</select></label>`,
		sel(val("embed_model", c.EmbedModel) == "hashing-v1"),
		sel(val("embed_model", c.EmbedModel) == "onnx/all-MiniLM-L6-v2"))
	fmt.Fprintf(&b, `<label><span class="lbl">Model API key <span class="hint">(TACIT_LLM_API_KEY, optional)</span></span><input name="llm_key" placeholder="sk-…" value="%s"></label>`, val("llm_key", ""))
	fmt.Fprintf(&b, `<label><span class="lbl">Embedding dimension</span><input name="embed_dim" inputmode="numeric" value="%s"></label>`, val("embed_dim", strconv.Itoa(c.EmbedDim)))
	fmt.Fprintf(&b, `<label><span class="lbl">Recompute interval (s)</span><input name="recompute_secs" inputmode="numeric" value="%s"></label>`, val("recompute_secs", strconv.Itoa(c.RecomputeIntervalSecs)))

	b.WriteString(`</div></details>`)
	b.WriteString(`<div class="full"><button type="submit">Configure registry</button>` +
		`<p class="hint">Every other setting has a default that works, and Settings can change any of them later.</p>` +
		`</div></form>`)
	return b.String()
}

func sel(on bool) string {
	if on {
		return " selected"
	}
	return ""
}

// setupSuccess is shown exactly once: it is the only time the API key is
// displayed, because it is the only moment the operator provably has it from
// the form they just submitted.
func setupSuccess(path string, vals []setupField, restart []string) string {
	byKey := map[string]string{}
	for _, f := range vals {
		byKey[f.Key] = f.Value
	}
	var b strings.Builder
	b.WriteString(`<p><b>Configured.</b> Setup wrote the settings to <code>` + html.EscapeString(path) + `</code>.</p>`)
	b.WriteString(`<h2>Your API key (shown once)</h2>` +
		`<div class="keybox">` + html.EscapeString(byKey["TACIT_API_KEY"]) + `</div>` +
		`<p class="hint">Agents, the miner, and admin calls authenticate with it. Connect a member’s harness with:</p>` +
		`<p><code>tacit connect --registry ` + html.EscapeString(setupBaseURL(byKey)) + ` --key ` + html.EscapeString(byKey["TACIT_API_KEY"]) + `</code></p>`)
	// The dashboard is gated now, so say how to get in. Without this the
	// operator finishes setup and finds a sign-in page they have no credential
	// for, which is a worse first minute than the open dashboard they used to
	// get.
	if secret := byKey["TACIT_OWNER_SECRET"]; secret != "" {
		b.WriteString(`<h2>Signing in</h2>` +
			`<p>This registry has no identity provider. Run <code>tacit dashboard</code> on its console ` +
			`to create an owner sign-in link. The link expires after fifteen minutes; run the command again for a new one.</p>` +
			`<p class="hint">When you are ready for colleagues to sign in with your organization's accounts, ` +
			`<code>tacit secure</code> switches this registry over and keeps its address, data and members.</p>`)
	}
	if len(restart) > 0 {
		b.WriteString(`<h2>Needs a restart</h2><p class="hint">These saved settings take effect at startup:</p><p><code>` +
			html.EscapeString(strings.Join(restart, ", ")) + `</code></p>` +
			`<p class="hint">systemd: <code>systemctl --user restart tacit-registry</code> · manual: stop and rerun <code>tacit serve</code></p>`)
	} else {
		b.WriteString(`<p>Everything applied immediately. <a href="/outcomes">Open the dashboard</a>.</p>`)
	}
	return b.String()
}

func setupBaseURL(byKey map[string]string) string {
	if v := byKey["TACIT_EXTERNAL_URL"]; v != "" {
		return v
	}
	host := byKey["TACIT_HOST"]
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	port := byKey["TACIT_PORT"]
	if port == "" {
		port = "8080"
	}
	return "http://" + host + ":" + port + config.NormalizeBasePath(byKey["TACIT_BASE_PATH"])
}

// pageSettings is the post-setup /settings page — OIDC-gated
// like every other dashboard view. Read-only reference for everyone; admins
// (TACIT_ADMIN_EMAILS) additionally get the hot-apply edit form
// (settings.go).
func (s *Server) pageSettings(r *http.Request, user oidc.Claims) page {
	// The "saved" answer rides the save bar (saveButton), not the top of the page.
	return page{active: "settings", crumbs: s.registryCrumbs("General"),
		viewMenu: s.registryViews("settings"),
		content:  s.settingsView(r, user, settingsOutcome{})}
}

func orDash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

func storageLabel(c config.Config) string {
	if c.DBURL != "" {
		return "Postgres"
	}
	return "file store · " + c.DataDir
}

func oidcLabel(c config.Config) string {
	if c.OIDCEnabled() {
		return "enabled · " + c.OIDCIssuer
	}
	return "off; dashboard open"
}
