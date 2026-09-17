// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

// discoveryServer stands in for an identity provider's well-known document.
func discoveryServer(t *testing.T, doc string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(doc))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

const fullDiscovery = `{"issuer":"x","authorization_endpoint":"https://idp.example/auth",
"token_endpoint":"https://idp.example/token","userinfo_endpoint":"https://idp.example/userinfo"}`

// writeEnv puts a registry.env in a temp dir and points the config loader at
// it, so a test drives the real command against a real file.
func writeEnv(t *testing.T, content string) string {
	t.Helper()
	// A port nothing answers on: the command asks the local registry for the
	// address it is published under, and a test must not reach a real one.
	if !strings.Contains(content, "TACIT_PORT") {
		content = "TACIT_PORT=8099\n" + content
	}
	path := filepath.Join(t.TempDir(), "registry.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", path)
	return path
}

func readEnv(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestDefaultCallback(t *testing.T) {
	cases := []struct {
		name string
		cfg  registryconfig.Config
		want string
	}{
		{"external URL", registryconfig.Config{ExternalURL: "https://tacit.example.com"},
			"https://tacit.example.com/auth/callback"},
		{"trailing slash trimmed", registryconfig.Config{ExternalURL: "https://tacit.example.com/"},
			"https://tacit.example.com/auth/callback"},
		{"sub-path carried", registryconfig.Config{ExternalURL: "https://demo.example.com/apps/tacit", BasePath: "/apps/tacit"},
			"https://demo.example.com/apps/tacit/auth/callback"},
		{"existing config kept", registryconfig.Config{OIDCRedirectURI: "https://old.example.com/auth/callback"},
			"https://old.example.com/auth/callback"},
		{"nothing configured", registryconfig.Config{Host: "0.0.0.0", Port: 8080},
			"http://localhost:8080/auth/callback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultCallback(tc.cfg); got != tc.want {
				t.Errorf("defaultCallback = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckCallback(t *testing.T) {
	ok := []string{
		"https://tacit.example.com/auth/callback",
		"https://demo.example.com/apps/tacit/auth/callback",
		"http://localhost:8080/auth/callback", // the usual provider exception
	}
	for _, uri := range ok {
		if problem := checkCallback(uri); problem != "" {
			t.Errorf("checkCallback(%q) = %q, want accepted", uri, problem)
		}
	}
	bad := map[string]string{
		"tacit.example.com/auth/callback":        "absolute",
		"https://tacit.example.com":              "/auth/callback",
		"https://tacit.example.com/callback":     "/auth/callback",
		"http://tacit.example.com/auth/callback": "https", // the lockout this prevents
	}
	for uri, want := range bad {
		problem := checkCallback(uri)
		if problem == "" {
			t.Errorf("checkCallback(%q) accepted it, want a refusal about %q", uri, want)
			continue
		}
		if !strings.Contains(problem, want) {
			t.Errorf("checkCallback(%q) = %q, want it to mention %q", uri, problem, want)
		}
	}
}

func TestFetchDiscovery(t *testing.T) {
	ts := discoveryServer(t, fullDiscovery)
	doc, err := fetchDiscovery(ts.URL)
	if err != nil {
		t.Fatalf("fetchDiscovery: %v", err)
	}
	if doc["token_endpoint"] != "https://idp.example/token" {
		t.Errorf("token_endpoint = %v", doc["token_endpoint"])
	}

	// An issuer that answers but names no userinfo endpoint cannot carry this
	// flow — the registry reads identity from userinfo rather than verifying an
	// ID token locally.
	partial := discoveryServer(t, `{"authorization_endpoint":"a","token_endpoint":"b"}`)
	if _, err := fetchDiscovery(partial.URL); err == nil ||
		!strings.Contains(err.Error(), "userinfo_endpoint") {
		t.Errorf("partial discovery err = %v, want it to name userinfo_endpoint", err)
	}

	notAnIssuer := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(notAnIssuer.Close)
	if _, err := fetchDiscovery(notAnIssuer.URL); err == nil {
		t.Error("a 404 discovery document was accepted")
	}
}

func TestCallbackFor(t *testing.T) {
	cases := []struct{ base, basePath, want string }{
		// A proxy-allocated address is an origin: the sub-path is still ours to add.
		{"https://slate-orchard.example.com", "/apps/tacit", "https://slate-orchard.example.com/apps/tacit/auth/callback"},
		// A configured external URL already carries it, and must not get it twice.
		{"https://demo.example.com/apps/tacit", "/apps/tacit", "https://demo.example.com/apps/tacit/auth/callback"},
		{"https://tacit.example.com/", "", "https://tacit.example.com/auth/callback"},
		{"", "/apps/tacit", ""},
	}
	for _, tc := range cases {
		if got := callbackFor(tc.base, tc.basePath); got != tc.want {
			t.Errorf("callbackFor(%q, %q) = %q, want %q", tc.base, tc.basePath, got, tc.want)
		}
	}
}

func TestRegistrySecurityChecks(t *testing.T) {
	full := registryconfig.Config{
		Host: "0.0.0.0", Port: 8080,
		OIDCIssuer: "https://accounts.google.com", OIDCClientID: "id",
		OIDCClientSecret: "sec", OIDCRedirectURI: "https://tacit.example.com/auth/callback",
		ExternalURL: "https://tacit.example.com", SessionSecret: "s", CookieSecure: true,
		AdminEmails: []string{"ops@example.com"},
	}
	cases := []struct {
		name         string
		cfg          func(registryconfig.Config) registryconfig.Config
		live         string
		fails, warns int
	}{
		{"a complete configuration is clean", func(c registryconfig.Config) registryconfig.Config { return c }, "", 0, 0},
		{"three of four is a failure, not a warning", func(c registryconfig.Config) registryconfig.Config {
			c.OIDCClientSecret = ""
			return c
		}, "", 1, 0},
		{"open on every interface", func(c registryconfig.Config) registryconfig.Config {
			c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret, c.OIDCRedirectURI = "", "", "", ""
			return c
		}, "", 0, 1},
		{"open on loopback is a posture, not a fault", func(c registryconfig.Config) registryconfig.Config {
			c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret, c.OIDCRedirectURI = "", "", "", ""
			c.Host = "127.0.0.1"
			return c
		}, "", 0, 0},
		{"no session secret", func(c registryconfig.Config) registryconfig.Config {
			c.SessionSecret = ""
			return c
		}, "", 0, 1},
		{"https callback with plaintext cookies", func(c registryconfig.Config) registryconfig.Config {
			c.CookieSecure = false
			return c
		}, "", 0, 1},
		{"no admins named", func(c registryconfig.Config) registryconfig.Config {
			c.AdminEmails = nil
			return c
		}, "", 0, 1},
		{"callback is not on the external URL", func(c registryconfig.Config) registryconfig.Config {
			c.OIDCRedirectURI = "https://elsewhere.example.com/auth/callback"
			return c
		}, "", 0, 1},
		{"callback is not the callback route", func(c registryconfig.Config) registryconfig.Config {
			c.OIDCRedirectURI = "https://tacit.example.com/"
			return c
		}, "", 1, 1}, // the route failure, plus the external-URL mismatch it implies
		// A published registry sends the proxy address as its redirect_uri,
		// superseding the configured one. That is worth SAYING (both belong at
		// the provider) and is not a fault: the operator chose to publish.
		{"published: the live callback differs from the stored one", func(c registryconfig.Config) registryconfig.Config {
			c.GlobalAccess = true
			return c
		}, "https://slate-orchard.example.com/auth/callback", 0, 0},
		{"published: the live callback is the stored one", func(c registryconfig.Config) registryconfig.Config {
			c.GlobalAccess = true
			return c
		}, "https://tacit.example.com/auth/callback", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &checker{}
			registrySecurityChecks(c, tc.cfg(full), tc.live)
			if c.fails != tc.fails || c.warns != tc.warns {
				t.Errorf("fails=%d warns=%d, want fails=%d warns=%d", c.fails, c.warns, tc.fails, tc.warns)
			}
		})
	}
}

func TestSecureOnWritesTheWholeSet(t *testing.T) {
	ts := discoveryServer(t, fullDiscovery)
	path := writeEnv(t, "# hand-written\nTACIT_API_KEY=abc123\nTACIT_PORT=8080\n")

	code := cmdSecure([]string{
		"--issuer", ts.URL,
		"--client-id", "client-123",
		"--client-secret", "shhh-this-is-the-secret",
		"--callback", "https://tacit.example.com/auth/callback",
		"--admins", "ops@example.com",
		"--yes", "--no-restart",
	})
	if code != 0 {
		t.Fatalf("tacit secure exited %d", code)
	}

	got := readEnv(t, path)
	for _, want := range []string{
		"TACIT_OIDC_ISSUER=" + ts.URL,
		"TACIT_OIDC_CLIENT_ID=client-123",
		"TACIT_OIDC_CLIENT_SECRET=shhh-this-is-the-secret",
		"TACIT_OIDC_REDIRECT_URI=https://tacit.example.com/auth/callback",
		"TACIT_ADMIN_EMAILS=ops@example.com",
		"TACIT_COOKIE_SECURE=1", // implied by an https callback
		"TACIT_SESSION_SECRET=", // minted, because nobody remembers to
		"# hand-written",        // the operator's own file survives
		"TACIT_API_KEY=abc123",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("registry.env missing %q\n---\n%s", want, got)
		}
	}
	if !registryconfig.Load().OIDCEnabled() {
		t.Error("sign-in is still not enabled after tacit secure")
	}
}

func TestSecureKeepsAnExistingSessionSecret(t *testing.T) {
	ts := discoveryServer(t, fullDiscovery)
	path := writeEnv(t, "TACIT_SESSION_SECRET=keep-me\n")
	code := cmdSecure([]string{
		"--issuer", ts.URL, "--client-id", "id", "--client-secret", "sec",
		"--callback", "https://tacit.example.com/auth/callback", "--yes", "--no-restart",
	})
	if code != 0 {
		t.Fatalf("tacit secure exited %d", code)
	}
	if got := readEnv(t, path); !strings.Contains(got, "TACIT_SESSION_SECRET=keep-me") {
		t.Errorf("the existing session secret was replaced — every member would be signed out\n%s", got)
	}
}

func TestSecureRefusesBeforeItWrites(t *testing.T) {
	ts := discoveryServer(t, fullDiscovery)
	cases := []struct {
		name string
		args []string
	}{
		{"a plaintext callback would lock the operator out", []string{
			"--issuer", ts.URL, "--client-id", "id", "--client-secret", "sec",
			"--callback", "http://tacit.example.com/auth/callback"}},
		{"an issuer that serves no discovery document", []string{
			"--issuer", "http://127.0.0.1:1/nope", "--client-id", "id", "--client-secret", "sec",
			"--callback", "https://tacit.example.com/auth/callback"}},
		{"a client ID with no secret", []string{
			"--issuer", ts.URL, "--client-id", "id",
			"--callback", "https://tacit.example.com/auth/callback"}},
		{"an admin entry that is not an email", []string{
			"--issuer", ts.URL, "--client-id", "id", "--client-secret", "sec",
			"--callback", "https://tacit.example.com/auth/callback", "--admins", "ops"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeEnv(t, "TACIT_API_KEY=abc123\n")
			if code := cmdSecure(append(tc.args, "--yes", "--no-restart")); code == 0 {
				t.Error("tacit secure exited 0, want a refusal")
			}
			if got := readEnv(t, path); strings.Contains(got, "TACIT_OIDC") {
				t.Errorf("a refused run still wrote OIDC settings:\n%s", got)
			}
		})
	}
}

func TestSecureDryRunWritesNothing(t *testing.T) {
	ts := discoveryServer(t, fullDiscovery)
	path := writeEnv(t, "TACIT_API_KEY=abc123\n")
	code := cmdSecure([]string{
		"--issuer", ts.URL, "--client-id", "id", "--client-secret", "sec",
		"--callback", "https://tacit.example.com/auth/callback", "--dry-run", "--yes", "--no-restart",
	})
	if code != 0 {
		t.Fatalf("--dry-run exited %d", code)
	}
	if got := readEnv(t, path); strings.Contains(got, "TACIT_OIDC") || strings.Contains(got, "SESSION_SECRET") {
		t.Errorf("--dry-run changed the file:\n%s", got)
	}
}

func TestSecureOffOpensTheDashboardAgain(t *testing.T) {
	path := writeEnv(t, strings.Join([]string{
		"# my registry",
		"TACIT_API_KEY=abc123",
		"TACIT_OIDC_ISSUER=https://accounts.google.com",
		"TACIT_OIDC_CLIENT_ID=id",
		"TACIT_OIDC_CLIENT_SECRET=sec",
		"TACIT_OIDC_REDIRECT_URI=https://tacit.example.com/auth/callback",
		"TACIT_SESSION_SECRET=keep-me",
		"",
	}, "\n"))

	if code := cmdSecure([]string{"--off", "--yes", "--no-restart"}); code != 0 {
		t.Fatalf("tacit secure --off exited %d", code)
	}
	got := readEnv(t, path)
	if strings.Contains(got, "TACIT_OIDC") {
		t.Errorf("--off left OIDC settings behind:\n%s", got)
	}
	for _, want := range []string{"# my registry", "TACIT_API_KEY=abc123", "TACIT_SESSION_SECRET=keep-me"} {
		if !strings.Contains(got, want) {
			t.Errorf("--off removed %q, which is not its business\n%s", want, got)
		}
	}
	if registryconfig.Load().OIDCEnabled() {
		t.Error("sign-in is still enabled after --off")
	}
}

func TestSecureOffWithNothingToRemove(t *testing.T) {
	writeEnv(t, "TACIT_API_KEY=abc123\n")
	if code := cmdSecure([]string{"--off", "--yes", "--no-restart"}); code != 0 {
		t.Fatalf("--off on an open registry exited %d, want 0", code)
	}
}

func TestSecureNeedsARegistry(t *testing.T) {
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "absent.env"))
	if code := cmdSecure([]string{"--off", "--yes"}); code == 0 {
		t.Error("tacit secure exited 0 with no registry.env, want a refusal")
	}
}

// The transition this command exists to complete: a registry that was one
// member's becomes an organization's, and says so. Before it wrote the mode,
// the four OIDC settings opened the gate while every question about which
// registry this is — SingleMember, the owner link — still answered "one
// person's".
func TestSecureOnMakesItAnOrganizationsRegistry(t *testing.T) {
	ts := discoveryServer(t, fullDiscovery)
	path := writeEnv(t, strings.Join([]string{
		"TACIT_API_KEY=abc123",
		"TACIT_AUTH_MODE=owner",
		"TACIT_OWNER_SECRET=owner-secret-kept",
		"",
	}, "\n"))

	code := cmdSecure([]string{
		"--issuer", ts.URL, "--client-id", "id", "--client-secret", "sec",
		"--callback", "https://tacit.example.com/auth/callback", "--yes", "--no-restart",
	})
	if code != 0 {
		t.Fatalf("tacit secure exited %d", code)
	}

	got := readEnv(t, path)
	if !strings.Contains(got, "TACIT_AUTH_MODE=oidc") {
		t.Errorf("the mode still does not say this registry has a provider:\n%s", got)
	}
	// Kept, deliberately: `--off` is the way back in, and it needs this.
	if !strings.Contains(got, "TACIT_OWNER_SECRET=owner-secret-kept") {
		t.Errorf("secure removed the owner secret, which is the way back in:\n%s", got)
	}
	cfg := registryconfig.Load()
	if !cfg.OIDCEnabled() {
		t.Error("sign-in is not enabled")
	}
	if cfg.SingleMember() {
		t.Error("a registry with an identity provider still reports as one member's")
	}
	if cfg.OwnerEnabled() {
		t.Error("the owner link still signs its holder in past the provider")
	}
}

func TestSecureOffGivesTheOwnerTheirRegistryBack(t *testing.T) {
	path := writeEnv(t, strings.Join([]string{
		"TACIT_AUTH_MODE=oidc",
		"TACIT_OWNER_SECRET=owner-secret-kept",
		"TACIT_OIDC_ISSUER=https://accounts.google.com",
		"TACIT_OIDC_CLIENT_ID=id",
		"TACIT_OIDC_CLIENT_SECRET=sec",
		"TACIT_OIDC_REDIRECT_URI=https://tacit.example.com/auth/callback",
		"",
	}, "\n"))

	if code := cmdSecure([]string{"--off", "--yes", "--no-restart"}); code != 0 {
		t.Fatalf("tacit secure --off exited %d", code)
	}
	if got := readEnv(t, path); !strings.Contains(got, "TACIT_AUTH_MODE=owner") {
		t.Errorf("--off left the mode claiming a provider that is gone:\n%s", got)
	}
	if cfg := registryconfig.Load(); !cfg.OwnerEnabled() || !cfg.SingleMember() {
		t.Error("--off removed the provider without giving the owner their way in")
	}
}

// A registry that never had an owner has no mode to go back to: writing one
// would name a gate that cannot let anybody in.
func TestSecureOffOnARegistryWithNoOwnerNamesNoMode(t *testing.T) {
	path := writeEnv(t, strings.Join([]string{
		"TACIT_AUTH_MODE=oidc",
		"TACIT_OIDC_ISSUER=https://accounts.google.com",
		"TACIT_OIDC_CLIENT_ID=id",
		"TACIT_OIDC_CLIENT_SECRET=sec",
		"TACIT_OIDC_REDIRECT_URI=https://tacit.example.com/auth/callback",
		"",
	}, "\n"))

	if code := cmdSecure([]string{"--off", "--yes", "--no-restart"}); code != 0 {
		t.Fatalf("tacit secure --off exited %d", code)
	}
	if got := readEnv(t, path); strings.Contains(got, "TACIT_AUTH_MODE") {
		t.Errorf("--off named a mode for a registry with no gate:\n%s", got)
	}
	if registryconfig.Load().AuthConfigured() {
		t.Error("an open registry reports a gate")
	}
}

// What turning sign-in on does about the process serving this registry. The
// case that matters is the last one: a registry somebody is running in a
// terminal must not be left behind by settings it will never read, and this
// command cannot stop a process it did not start.
func TestServiceActionForTurningSignInOn(t *testing.T) {
	cases := []struct {
		name                        string
		managed, answering, canMana bool
		want                        serviceAction
	}{
		{"a unit is already running it", true, true, true, serviceRestart},
		{"nothing is running it", false, false, true, serviceInstall},
		{"a terminal is running it", false, true, true, serviceBlocked},
		{"a container, where services are not ours to enrol", false, true, false, serviceManual},
		{"no service manager and nothing running", false, false, false, serviceManual},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceActionFor(tc.managed, tc.answering, tc.canMana); got != tc.want {
				t.Errorf("serviceActionFor(%v,%v,%v) = %v, want %v",
					tc.managed, tc.answering, tc.canMana, got, tc.want)
			}
		})
	}
}
