// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// The whole first-run contract: an unconfigured registry parks everything
// behind /setup, refuses the API outright (never dev-key), demands the
// console-printed claim code, writes the settings file, hot-applies the key,
// and then gets out of the way.
func TestFirstRunSetupFlow(t *testing.T) {
	srv, ts := newServer(t)
	// After newServer: it points TACIT_REGISTRY_ENV at its own hermetic path,
	// and this test needs to observe the file the wizard writes.
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	srv.Cfg.APIKey = "dev-key" // the compiled-in default an operator never chose
	srv.EnableSetup("AAAA-1111")

	// Pages park; the API refuses — even with the default key.
	resp, err := noRedirect().Get(ts.URL + "/outcomes")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/setup" {
		t.Fatalf("dashboard during setup = %d -> %q, want 302 -> /setup", resp.StatusCode, resp.Header.Get("Location"))
	}
	apiResp, _ := request(t, "GET", ts.URL+"/v1/techniques", "dev-key", "")
	if apiResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("API during setup = %d, want 503 — dev-key must never authenticate an unconfigured registry", apiResp.StatusCode)
	}

	// The form renders.
	_, form := fetchHTML(t, ts.URL+"/setup")
	for _, want := range []string{`name="claim_code"`, `name="api_key"`, `name="host"`,
		`name="db_url"`, `name="oidc_issuer"`, `name="embed_model"`, `Claim code`} {
		if !strings.Contains(form, want) {
			t.Errorf("setup form missing %q", want)
		}
	}

	// A wrong claim code is refused and counted.
	bad := url.Values{"claim_code": {"XXXX-0000"}, "api_key": {"0123456789abcdef0123"}}
	resp, err = http.PostForm(ts.URL+"/setup", bad)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong claim code = %d, want 403", resp.StatusCode)
	}

	// The right code with a weak key is a validation error, not a config.
	weak := url.Values{"claim_code": {"AAAA-1111"}, "api_key": {"short"}}
	resp, _ = http.PostForm(ts.URL+"/setup", weak)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("weak key = %d, want 400", resp.StatusCode)
	}

	// The real thing.
	good := url.Values{
		"claim_code": {"aaaa-1111"}, // case-insensitive: humans type codes
		"api_key":    {"0123456789abcdef0123456789abcdef"},
		"host":       {"127.0.0.1"}, "port": {"9191"},
		"data_dir": {"data"}, "techniques_dir": {"techniques"},
		"embed_model": {"hashing-v1"}, "embed_dim": {"256"},
		"external_url": {"https://tacit.example.com"},
	}
	resp, err = http.PostForm(ts.URL+"/setup", good)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("setup submit = %d", resp.StatusCode)
	}

	// The file exists, is private, and round-trips through config.Load.
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal("registry.env not written:", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("registry.env mode = %o, want 600 — it holds the API key", info.Mode().Perm())
	}
	cfg := config.Load()
	if cfg.APIKey != "0123456789abcdef0123456789abcdef" || cfg.Port != 9191 || cfg.FirstRun {
		t.Fatalf("Load after setup: key=%q port=%d firstRun=%v", cfg.APIKey, cfg.Port, cfg.FirstRun)
	}

	// The key is live without a restart; the old default is dead.
	if r, _ := request(t, "GET", ts.URL+"/v1/techniques", "0123456789abcdef0123456789abcdef", ""); r.StatusCode != 200 {
		t.Fatalf("new key = %d, want 200 — hot-apply failed", r.StatusCode)
	}
	if r, _ := request(t, "GET", ts.URL+"/v1/techniques", "dev-key", ""); r.StatusCode != 401 {
		t.Fatalf("dev-key after setup = %d, want 401", r.StatusCode)
	}

	// The gate is down. /setup is the wizard and has no further job, so it now
	// redirects to /settings — its own destination — rather than doubling as a
	// second settings URL (one page per question).
	if r, _ := noRedirect().Get(ts.URL + "/setup"); r.StatusCode != http.StatusFound || r.Header.Get("Location") != "/settings" {
		t.Fatalf("post-setup GET /setup = %d -> %q, want 302 -> /settings", r.StatusCode, r.Header.Get("Location"))
	}
	// Following that redirect (or hitting /settings directly) lands on the
	// read-only settings view.
	code, view := fetchHTML(t, ts.URL+"/settings")
	if code != 200 || !strings.Contains(view, `id="set-deployment"`) {
		t.Fatalf("post-setup /settings: %d — want the settings view", code)
	}
	if strings.Contains(view, "0123456789abcdef0123456789abcdef") {
		t.Fatal("the settings view leaks the full API key — it is shown once, at setup")
	}
	// Re-posting is refused.
	resp, _ = http.PostForm(ts.URL+"/setup", good)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("re-configure = %d, want 409", resp.StatusCode)
	}
}

// Ten wrong codes lock setup until restart — the claim code cannot be
// brute-forced from the network.
func TestSetupLocksAfterTooManyBadCodes(t *testing.T) {
	srv, ts := newServer(t)
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "registry.env"))
	srv.EnableSetup("BBBB-2222")

	for i := 0; i < setupMaxAttempts; i++ {
		r, _ := http.PostForm(ts.URL+"/setup", url.Values{"claim_code": {"NOPE-0000"}})
		r.Body.Close()
	}
	// Even the RIGHT code is refused now.
	r, err := http.PostForm(ts.URL+"/setup", url.Values{
		"claim_code": {"BBBB-2222"}, "api_key": {"0123456789abcdef0123"}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("post-lock correct code = %d, want 403", r.StatusCode)
	}
	if _, err := os.Stat(config.RegistryEnvPath()); !os.IsNotExist(err) {
		t.Fatal("locked setup still wrote a config file")
	}
}

// OIDC is all-or-none: a partial config would silently disable sign-in.
func TestSetupRejectsPartialOIDC(t *testing.T) {
	srv, ts := newServer(t)
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "registry.env"))
	srv.EnableSetup("CCCC-3333")
	r, err := http.PostForm(ts.URL+"/setup", url.Values{
		"claim_code": {"CCCC-3333"}, "api_key": {"0123456789abcdef0123"},
		"oidc_issuer": {"https://accounts.example.com"}, // one of four
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("partial OIDC = %d, want 400", r.StatusCode)
	}
}

// On a sign-in-enabled registry (this one faces the public internet), the
// post-setup settings view is behind the same OIDC gate as every dashboard
// page — even masked secrets are an operator-only sight.
func TestSettingsViewRequiresSignInWhenOIDCOn(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{} // enabled, no session cookie presented
	resp, err := http.Get(ts.URL + "/setup")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := make([]byte, 64<<10)
	n, _ := resp.Body.Read(body)
	if strings.Contains(string(body[:n]), "Effective configuration") {
		t.Fatal("settings view served without a session while OIDC is enabled")
	}
}

// Once configured, the honest name for the page is Settings — /settings
// serves the same view, and the trail says Settings, not Setup.
func TestSettingsRouteAndTitle(t *testing.T) {
	_, ts := newServer(t)
	code, html := fetchHTML(t, ts.URL+"/settings")
	if code != 200 || !strings.Contains(html, `id="set-deployment"`) {
		t.Fatalf("/settings: %d", code)
	}
	if !strings.Contains(html, ">Settings<") || strings.Contains(html, ">Setup<") {
		t.Fatal("configured registry should title the page Settings, not Setup")
	}
}
