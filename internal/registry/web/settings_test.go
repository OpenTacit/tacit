// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/suggest"
)

// signIn equips a server with OIDC and returns a client carrying a valid
// session for the given email — the test's stand-in for a signed-in member.
func signIn(t *testing.T, srv *Server, email string) *http.Client {
	t.Helper()
	srv.OIDC = &oidc.Provider{Secret: []byte("test-session-secret"), TTL: time.Hour}
	tok := srv.OIDC.CreateSession(oidc.Claims{"sub": "u1", "email": email, "name": "T"})
	jar := &staticCookieJar{cookie: &http.Cookie{Name: sessionCookie, Value: tok}}
	return &http.Client{Jar: jar}
}

type staticCookieJar struct{ cookie *http.Cookie }

func (j *staticCookieJar) SetCookies(*url.URL, []*http.Cookie) {}
func (j *staticCookieJar) Cookies(*url.URL) []*http.Cookie     { return []*http.Cookie{j.cookie} }

// extractCSRF pulls the hidden token out of the settings page.
func extractCSRF(t *testing.T, body string) string {
	t.Helper()
	_, after, ok := strings.Cut(body, `name="csrf" value="`)
	if !ok {
		t.Fatal("no csrf token in settings form")
	}
	tok, _, _ := strings.Cut(after, `"`)
	return tok
}

// The whole admin contract: a configured admin sees the edit form, a save
// patches registry.env in place (comments intact), hot-applies, and everyone
// else — signed-in or not — gets neither form nor write access.
func TestAdminSettingsEdit(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte(
		"# operator's own comment — must survive the web editor\n"+
			"TACIT_API_KEY=untouched-by-the-form-0123\n"+
			"TACIT_EXTERNAL_URL=https://old.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ada@example.com"}
	admin := signIn(t, srv, "Ada@Example.com") // case must not matter

	// The admin sees the edit form; the CSRF token rides in it.
	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 512<<10)
	n, _ := resp.Body.Read(raw)
	resp.Body.Close()
	pageHTML := string(raw[:n])
	if !strings.Contains(pageHTML, `action="/settings"`) {
		t.Fatal("admin doesn't see the edit form")
	}
	for _, field := range []string{`name="llm_provider"`, `name="llm_base_url"`, `name="suggest_model"`, `name="tagmerge_model"`} {
		if !strings.Contains(pageHTML, field) {
			t.Fatalf("edit form is missing the model-selection field %s", field)
		}
	}
	// The two model fields are combo boxes sharing a live-populated datalist.
	if !strings.Contains(pageHTML, `list="model-catalog"`) || !strings.Contains(pageHTML, `<datalist id="model-catalog">`) {
		t.Fatal("model fields are not combo boxes (missing datalist wiring)")
	}
	// No duplication. The page used to be split into an editable half and a
	// read-only half, which meant every hot-appliable setting had two homes and
	// could appear in both; the structure is now by subject, so each control
	// exists exactly once and this guard pins that.
	// The administrators list is the one exception, and only in appearance: it is
	// one control per address, so `name="admin_emails"` repeats — what must not
	// repeat is the list it lives in.
	for _, field := range []string{`<select name="llm_provider"`, `name="autonomy"`, `class="set-emails-list"`,
		`name="publish"`, `name="external_url"`} {
		if n := strings.Count(pageHTML, field); n != 1 {
			t.Fatalf("%s appears %d times, want 1 (a setting with two homes)", field, n)
		}
	}
	// The provider dropdown offers the major providers, not just two.
	for _, want := range []string{`value="openrouter"`, `value="groq"`, `>OpenRouter<`, `>Anthropic (Claude)<`} {
		if !strings.Contains(pageHTML, want) {
			t.Fatalf("provider dropdown missing %q", want)
		}
	}
	// Structure: five subjects on three tabs, each appearing exactly once.
	// Editability is a property of the row now, so there is no second hierarchy
	// to search.
	for _, id := range []string{"set-access", "set-automation", "set-deployment"} {
		if n := strings.Count(pageHTML, `id="`+id+`"`); n != 1 {
			t.Fatalf("section %s appears %d times, want exactly 1", id, n)
		}
	}
	// And the old two-halves framing is gone.
	for _, gone := range []string{"Editable now", "Everything else", "Effective configuration"} {
		if strings.Contains(pageHTML, gone) {
			t.Errorf("the page still carries %q from the editable/read-only split", gone)
		}
	}
	// Every section states its state, so the page is scannable without reading
	// each field.
	if n := strings.Count(pageHTML, "set-chip"); n < 5 {
		t.Errorf("only %d state chips; each section should carry one", n)
	}
	// Agent autonomy is a switch in the form — and only a switch. The thresholds
	// it runs on live in registry.env, so the page neither offers them nor
	// rewrites them.
	if !strings.Contains(pageHTML, `name="autonomy"`) {
		t.Fatal("settings form missing the autonomy switch")
	}
	for _, gone := range []string{`name="autonomy_min_rate"`, `name="autonomy_min_n"`,
		`name="auto_promote_min_fit"`, `name="auto_promote_min_judged"`} {
		if strings.Contains(pageHTML, gone) {
			t.Errorf("the page offers %s again; a threshold is not a question this page asks", gone)
		}
	}
	// The toggle's own label says what it does, so it needs no explanatory hint
	// beside it — that is the copy rule this page is built on.
	if !strings.Contains(pageHTML, "Apply proven techniques automatically for agents") {
		t.Error("the autonomy toggle does not say what it does in its label")
	}
	// Provider-specific defaults: each option carries the base/model so the
	// placeholder can follow the dropdown, and the initial placeholder is the
	// current provider's (Anthropic here — no TACIT_LLM_PROVIDER set).
	if !strings.Contains(pageHTML, `data-base="https://openrouter.ai/api/v1"`) {
		t.Fatal("provider options are missing their default base/model data attributes")
	}
	if !strings.Contains(pageHTML, `placeholder="claude-sonnet-5"`) {
		t.Fatal("model field placeholder should default to the current provider's model")
	}
	csrf := extractCSRF(t, pageHTML)

	// Save: change the external URL, set the model key + selection, keep admins.
	form := url.Values{
		"csrf":          {csrf},
		"external_url":  {"https://new.example.com"},
		"admin_emails":  {"ada@example.com, second@example.com"},
		"llm_key":       {"sk-ant-test-000"},
		"llm_provider":  {"openai"},
		"llm_base_url":  {"https://openrouter.ai/api/v1"},
		"suggest_model": {"openai/gpt-4o"},
		"autonomy":      {"on"},
	}
	resp, err = admin.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Query().Get("saved") != "1" {
		t.Fatalf("save didn't land on ?saved=1 (status %d, url %s)", resp.StatusCode, resp.Request.URL)
	}

	// The file: patched in place, comment and untouched key preserved.
	got, _ := os.ReadFile(envPath)
	text := string(got)
	for _, want := range []string{
		"# operator's own comment — must survive the web editor",
		"TACIT_API_KEY=untouched-by-the-form-0123",
		"TACIT_EXTERNAL_URL=https://new.example.com",
		"TACIT_ADMIN_EMAILS=ada@example.com, second@example.com",
		"TACIT_LLM_API_KEY=sk-ant-test-000",
		"TACIT_LLM_PROVIDER=openai",
		"TACIT_LLM_BASE_URL=https://openrouter.ai/api/v1",
		"TACIT_SUGGEST_MODEL=openai/gpt-4o",
		"TACIT_AUTONOMY=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("registry.env missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "old.example.com") {
		t.Fatal("stale external URL left behind")
	}
	// The thresholds are the operator's to set in this file. A save from a page
	// that does not ask about them must not write them — rewriting a line it
	// never showed is how a settings page quietly becomes the owner of a value
	// somebody else set.
	for _, gone := range []string{"TACIT_AUTONOMY_MIN_HELPED_RATE", "TACIT_AUTONOMY_MIN_N",
		"TACIT_AUTO_PROMOTE_MIN_FIT", "TACIT_AUTO_PROMOTE_MIN_JUDGED"} {
		if strings.Contains(text, gone) {
			t.Errorf("the save wrote %s, which the page no longer offers:\n%s", gone, text)
		}
	}

	// Hot-applied without restart.
	if srv.Cfg.ExternalURL != "https://new.example.com" || len(srv.Cfg.AdminEmails) != 2 {
		t.Fatalf("config not hot-applied: %q %v", srv.Cfg.ExternalURL, srv.Cfg.AdminEmails)
	}
	if os.Getenv("TACIT_LLM_API_KEY") != "sk-ant-test-000" {
		t.Fatal("model key not applied")
	}
	if os.Getenv("TACIT_LLM_PROVIDER") != "openai" || os.Getenv("TACIT_SUGGEST_MODEL") != "openai/gpt-4o" {
		t.Fatal("model selection not hot-applied")
	}
	// Agent autonomy hot-applies into s.Cfg (retrieval reads it per request), and
	// the thresholds it runs on are left exactly as this registry loaded them.
	if !srv.Cfg.AutonomyEnabled {
		t.Fatalf("autonomy not hot-applied: %+v", srv.Cfg)
	}
	if srv.Cfg.AutonomyMinHelpedRate != 0.8 || srv.Cfg.AutonomyMinN != 20 {
		t.Errorf("a save moved thresholds the page never showed: %v over n=%d",
			srv.Cfg.AutonomyMinHelpedRate, srv.Cfg.AutonomyMinN)
	}
	for _, k := range []string{"TACIT_LLM_API_KEY", "TACIT_LLM_PROVIDER", "TACIT_LLM_BASE_URL", "TACIT_SUGGEST_MODEL"} {
		t.Setenv(k, "") // don't leak into other tests
	}

	// A signed-in NON-admin: no form, and a write attempt is refused.
	member := signIn(t, srv, "someone-else@example.com")
	srv.Cfg.AdminEmails = []string{"ada@example.com"}
	resp, _ = member.Get(ts.URL + "/settings")
	n, _ = resp.Body.Read(raw)
	resp.Body.Close()
	if strings.Contains(string(raw[:n]), `action="/settings"`) {
		t.Fatal("non-admin sees the edit form")
	}
	resp, _ = member.PostForm(ts.URL+"/settings", form)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin save = %d, want 403", resp.StatusCode)
	}

	// A valid admin session with a wrong CSRF token is refused: the save is
	// bound to the session, not just to being an admin.
	bad := url.Values{"csrf": {"forged"}, "external_url": {"https://evil.example.com"}}
	resp, _ = admin.PostForm(ts.URL+"/settings", bad)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("forged csrf = %d, want 403", resp.StatusCode)
	}

	// A setting the registry no longer reads is swept out of the file rather than
	// left to look meaningful. TACIT_SHARE_FEDERATED_OUTCOMES was the opt-in for
	// reporting outcomes on imported techniques; Global Access covers it now.
	t.Setenv("TACIT_SHARE_FEDERATED_OUTCOMES", "1")
	resp, err = admin.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {csrf}, "admin_emails": {"ada@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	got, _ = os.ReadFile(envPath)
	if strings.Contains(string(got), "TACIT_SHARE_FEDERATED_OUTCOMES=1") {
		t.Fatalf("a retired setting is still in registry.env, still looking live:\n%s", got)
	}
}

// The model combo box endpoint serves the selected provider's ids to admins
// and refuses everyone else; a non-admin still sees the settings in the
// read-only list (their only view), so the dedup is admin-only.
func TestSettingsModelsEndpointAndNonAdminView(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.AdminEmails = []string{"ada@example.com"}
	srv.ModelLister = func(provider string) []suggest.ModelInfo {
		if provider == "openai" {
			return []suggest.ModelInfo{
				{ID: "openai/gpt-4o", Name: "GPT-4o", Description: "OpenAI's flagship multimodal model."},
				{ID: "anthropic/claude-haiku-4.5"},
			}
		}
		return []suggest.ModelInfo{{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5"}}
	}

	admin := signIn(t, srv, "ada@example.com")
	resp, err := admin.Get(ts.URL + "/settings/models?provider=openai")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "openai/gpt-4o") {
		t.Fatalf("admin models call = %d %s", resp.StatusCode, body)
	}
	// The per-model name + description ride along so the field can show them.
	if !strings.Contains(string(body), "GPT-4o") || !strings.Contains(string(body), "flagship multimodal") {
		t.Fatalf("model name/description missing from response: %s", body)
	}

	// A non-admin is refused the endpoint, but their read-only settings view
	// still shows the model settings (they have no edit form to dedup against).
	member := signIn(t, srv, "nobody@example.com")
	resp, _ = member.Get(ts.URL + "/settings/models?provider=openai")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin models call = %d, want 403", resp.StatusCode)
	}
	resp, _ = member.Get(ts.URL + "/settings")
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// A non-admin sees the same subjects on the same tabs, read-only — not a
	// differently organized page.
	for _, id := range []string{"set-access", "set-automation", "set-deployment"} {
		if !strings.Contains(string(page), `id="`+id+`"`) {
			t.Errorf("non-admin view is missing section %s", id)
		}
	}
	if !strings.Contains(string(page), "set-fact") {
		t.Error("non-admin view shows no read-only values")
	}
	if strings.Contains(string(page), `action="/settings"`) {
		t.Fatal("non-admin must not see the edit form")
	}
}

// No OIDC means no sessions, so no admins and no web editing — even though
// the dashboard itself is open on such deployments.
func TestNoOIDCMeansReadOnlySettings(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.AdminEmails = []string{"ada@example.com"}
	code, body := fetchHTML(t, ts.URL+"/settings")
	if code != 200 || strings.Contains(body, `action="/settings"`) {
		t.Fatalf("open dashboard must not offer the edit form (code %d)", code)
	}
	resp, err := http.PostForm(ts.URL+"/settings", url.Values{"external_url": {"https://x.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("save without OIDC = %d, want 403", resp.StatusCode)
	}
}

// Once an admin list exists, the dashboard's mutating actions belong to the
// admins; with no list they keep today's any-signed-in-member rule.
func TestAdminGateOnMutatingActions(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.AdminEmails = []string{"ada@example.com"}
	member := signIn(t, srv, "someone-else@example.com")
	resp, err := member.PostForm(ts.URL+"/admin/poll-feeds", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin poll-feeds with admin list set = %d, want 403", resp.StatusCode)
	}

	srv.Cfg.AdminEmails = nil // pilot posture: any signed-in member curates
	resp, err = member.PostForm(ts.URL+"/admin/poll-feeds", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("empty admin list must keep the any-member rule")
	}
}

// The env parser must fold case and trim; config.Load is where the list is born.
func TestAdminEmailsParsing(t *testing.T) {
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "none.env"))
	t.Setenv("TACIT_ADMIN_EMAILS", " Ada@Example.com , second@example.com ,")
	cfg := config.Load()
	if len(cfg.AdminEmails) != 2 || cfg.AdminEmails[0] != "ada@example.com" {
		t.Fatalf("AdminEmails = %v", cfg.AdminEmails)
	}
}

// Each administrator posts its own value now. A row the operator removed comes
// back empty — the script clears and hides it rather than deleting it, so that
// the removal is visible to the save — and empty means gone. A value still
// arriving with commas in it (an older page, the first-run wizard) is read the
// same way, and nobody is listed twice.
func TestRemovedAdministratorRowIsDroppedOnSave(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")
	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	page := string(raw)

	resp, err = admin.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {extractCSRF(t, page)},
		"admin_emails": {
			"ops@example.com",          // kept
			"",                         // removed
			"lead@example.com, ops@ex", // one row, typed the old way
			"OPS@example.com",          // the same person again
			"   ",                      // a blank row nobody typed in
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	got, _ := os.ReadFile(envPath)
	if !strings.Contains(string(got), "TACIT_ADMIN_EMAILS=ops@example.com, lead@example.com, ops@ex\n") {
		t.Fatalf("administrators not read one row at a time:\n%s", got)
	}
	if len(srv.Cfg.AdminEmails) != 3 {
		t.Fatalf("hot-applied administrators: %v", srv.Cfg.AdminEmails)
	}
}

// The one administrators edit the page refuses: your own address leaving the
// list. Whoever else is named, nobody demotes themselves here — and with the
// list empty every member who signs in becomes an administrator, which is worse
// than a lockout.
func TestYouCannotRemoveYourselfAsAdministrator(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")
	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	page := string(raw)
	csrf := extractCSRF(t, page)

	// Your own row is marked, which is what the script reads to leave its ✕ dead.
	if !strings.Contains(page, `<div class="set-email" data-self="1">`) {
		t.Error("the signed-in administrator's own row is not marked")
	}

	// Cleared to nothing: refused, and nothing written.
	resp, err = admin.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {csrf}, "admin_emails": {"", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("removing the only administrator returned %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "cannot remove yourself") {
		t.Error("the refusal does not say why")
	}
	if got, _ := os.ReadFile(envPath); strings.Contains(string(got), "TACIT_ADMIN_EMAILS") {
		t.Fatalf("a refused save wrote to the file:\n%s", got)
	}
	if len(srv.Cfg.AdminEmails) != 1 {
		t.Fatalf("administrators changed anyway: %v", srv.Cfg.AdminEmails)
	}

	// Adding a second administrator buys no way out. This is the sequence that
	// got past the first two guards: add somebody, then remove yourself while
	// they are still named.
	resp, err = admin.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {csrf}, "admin_emails": {"ops@example.com", "lead@example.com", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(srv.Cfg.AdminEmails) != 2 {
		t.Fatalf("the second administrator was not added: %v", srv.Cfg.AdminEmails)
	}
	resp, err = admin.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {csrf}, "admin_emails": {"lead@example.com", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("removing yourself while another administrator is named returned %d, want 400",
			resp.StatusCode)
	}
	if len(srv.Cfg.AdminEmails) != 2 {
		t.Fatalf("administrators changed anyway: %v", srv.Cfg.AdminEmails)
	}

	// Removing SOMEBODY ELSE is an ordinary edit, and goes through.
	resp, err = admin.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {csrf}, "admin_emails": {"ops@example.com", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got, _ := os.ReadFile(envPath); !strings.Contains(string(got), "TACIT_ADMIN_EMAILS=ops@example.com\n") {
		t.Fatalf("removing another administrator was refused:\n%s", got)
	}
}

// An owner session administers by the owner secret, not by being on the list, so
// it has no address of its own to remove and must not be blocked from saving a
// registry that names no administrators yet.
func TestOwnerSessionCanSaveWithNoAdministrators(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AuthMode = config.AuthOwner
	srv.Cfg.OwnerSecret = "an-owner-secret"
	owner := ownerClient(t, ts.URL, srv.Cfg.OwnerSecret)
	resp, err := owner.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	resp, err = owner.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {extractCSRF(t, string(raw))}, "admin_emails": {""},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("the owner's save was refused (%d)", resp.StatusCode)
	}
}

// A post that does not carry the administrators field at all must leave the
// stored list alone. "" reads as "clear it", and clearing the administrators
// makes every member who signs in one — too large a consequence for a field
// that simply was not there.
func TestAbsentAdministratorsFieldLeavesTheListAlone(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_ADMIN_EMAILS=ops@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")
	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	resp, err = admin.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {extractCSRF(t, string(raw))}, "external_url": {"https://tacit.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("a save without the field was refused (%d)", resp.StatusCode)
	}
	if got, _ := os.ReadFile(envPath); !strings.Contains(string(got), "TACIT_ADMIN_EMAILS=ops@example.com") {
		t.Fatalf("the administrators list was cleared by a form that never named it:\n%s", got)
	}
	if len(srv.Cfg.AdminEmails) != 1 {
		t.Fatalf("administrators changed anyway: %v", srv.Cfg.AdminEmails)
	}
}

// THE TAB IS IN THE ADDRESS. Somewhere else on this registry has to be able to
// send a reader to a subject rather than to a page — the cold start's model step
// wants the Model plate — and a link that landed on Access left them hunting for
// the tab the sentence beside the link had just named.
func TestSettingsOpensTheTabTheAddressAsksFor(t *testing.T) {
	s, _ := newServer(t)
	// An owner-gated registry, so the view renders its controls: read-only, the
	// tabs are the same but there is no form to carry the tab.
	s.Cfg.AuthMode = "owner"
	s.Cfg.OwnerSecret = "s"
	admin := oidc.Claims{"owner": true}
	view := func(q string) string {
		r := ownerRequest(s)
		r.URL.RawQuery = q
		return s.settingsView(r, admin, settingsOutcome{})
	}

	// No tab named: the first one, exactly as before.
	if !strings.Contains(view(""), `id="tab-access" checked`) {
		t.Error("a plain /settings does not open on the first tab")
	}
	auto := view("tab=automation")
	if !strings.Contains(auto, `id="tab-automation" checked`) {
		t.Error("?tab=automation does not check the automation radio")
	}
	if strings.Contains(auto, `id="tab-access" checked`) {
		t.Error("two tabs checked at once")
	}
	// The save keeps it, so a save — or one that comes back with a problem —
	// lands where the reader was rather than at the top of the page.
	if !strings.Contains(auto, `action="/settings?tab=automation"`) {
		t.Error("the form drops the tab, so saving jumps the reader to Access")
	}
	// A name nothing answers to is not an error: it is the first tab.
	if !strings.Contains(view("tab=nonsense"), `id="tab-access" checked`) {
		t.Error("an unknown tab name does not fall back to the first")
	}
	// Every panel is still in the DOM and inside the form, whichever tab is
	// checked — that is what makes one Save post every field.
	for _, id := range []string{"set-access", "set-automation", "set-deployment"} {
		if !strings.Contains(auto, `id="`+id+`"`) {
			t.Errorf("panel %s is missing; a partial form silently clears what it does not post", id)
		}
	}
}

// A SAVE ON A STAGED REGISTRY MUST NOT TOUCH THE TUNNEL.
//
// Staged is Global Access on and not yet confirmed — reachable through the
// proxy with the commons withheld, which is what `tacit init` leaves behind and
// so the state an operator is in the first time they open Settings. The save
// decided whether to restart the tunnel by asking GlobalAccessServing(), which
// is on AND confirmed, so it read every staged save as "the switch is on and
// the tunnel is down" and restarted it. For an operator reading this page
// through that tunnel, the restart cut the pipe their own answer was about to
// be written into: the settings were written, the response never arrived, and
// the browser sat loading until the tunnel came back.
func TestSavingOnAStagedRegistryLeavesTheTunnelUp(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	// Reachable, unconfirmed: the tunnel is up, the feed is not.
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, false
	// Nothing may dial out of a test, whichever way this goes.
	srv.Cfg.PublishIngress = "127.0.0.1:1"

	stopped := false
	srv.pub.mu.Lock()
	srv.pub.running, srv.pub.cancel = true, func() { stopped = true }
	srv.pub.mu.Unlock()

	admin := signIn(t, srv, "ops@example.com")
	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 512<<10)
	n, _ := resp.Body.Read(raw)
	_ = resp.Body.Close()
	form := url.Values{
		"csrf":           {extractCSRF(t, string(raw[:n]))},
		"llm_key":        {"sk-ant-test-000"},
		"publish":        {"on"}, // the switch stays where it was
		"admin_emails":   {"ops@example.com"},
		"_tab":           {"automation"},
		"do":             {"save"},
		"autonomy_min_n": {"20"},
	}
	saved, err := admin.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	_ = saved.Body.Close()
	if saved.StatusCode != http.StatusOK && saved.StatusCode != http.StatusSeeOther {
		t.Fatalf("save = %d", saved.StatusCode)
	}
	if stopped {
		t.Error("the save stopped the tunnel it was answering through")
	}
	if !srv.Cfg.GlobalAccess {
		t.Error("the save turned Global Access off")
	}
	// And the key it was asked to write is written.
	if os.Getenv("TACIT_LLM_API_KEY") != "sk-ant-test-000" {
		t.Errorf("model key not applied: %q", os.Getenv("TACIT_LLM_API_KEY"))
	}
}
