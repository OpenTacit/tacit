// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/jobs"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/store"
	"github.com/opentacit/tacit/internal/registry/suggest"
	"github.com/opentacit/tacit/internal/ui"
	"github.com/opentacit/tacit/pkg/jsonschema"
	"github.com/opentacit/tacit/schemas"
)

func TestTileSparkHelpers(t *testing.T) {
	bkts := []insights.Bucket{
		{Funnel: insights.Funnel{Shown: 2}},              // no adoptions yet
		{Funnel: insights.Funnel{Shown: 1}},              // still none — rate carries
		{Funnel: insights.Funnel{Adopted: 4, Helped: 3}}, // 3/4 = 75%
	}
	if got := bucketCounts(bkts, fShown); got[0] != 2 || got[1] != 1 || got[2] != 0 {
		t.Fatalf("bucketCounts(shown) = %v, want [2 1 0]", got)
	}
	// Cumulative helped/adopted, quiet buckets carrying forward: [0, 0, 75].
	if got := cumulativeRate(bkts, fHelped, fAdopted); got[0] != 0 || got[1] != 0 || got[2] != 75 {
		t.Fatalf("cumulativeRate = %v, want [0 0 75]", got)
	}
	if tileSpark([]int{5}) != "" {
		t.Fatal("tileSpark should suppress a single-point series (nothing to trend)")
	}
	if !strings.Contains(string(tileSpark([]int{1, 2, 3})), "<svg") {
		t.Fatal("tileSpark should render an svg for >=2 points")
	}
}

func TestAccountAvatarMenu(t *testing.T) {
	// An organization's registry: the menu it renders is the shared set, with no
	// Team entry (that page is the single-member transition; see accountMenu).
	org := &Server{Cfg: config.Config{}}
	got := org.accountHTML(oidc.Claims{
		"name": "Ada Lovelace", "email": "ada@example.com",
		"picture": "https://cdn.example/p.jpg",
	}, true)
	for _, want := range []string{
		`<img class="avatar-img" src="https://cdn.example/p.jpg"`, // the OIDC picture is the avatar
		`aria-haspopup="menu"`,
		// Sign out sits apart from the destinations, below a rule.
		`<hr class="account-sep"><a class="account-item" role="menuitem" href="/auth/logout">Sign out</a>`,
		// TWO destinations, not eight. Registry is a section — People,
		// Settings, Federation and Learning readiness behind one door, reached by
		// the breadcrumb switcher — and so is Documentation.
		`<a class="account-item" role="menuitem" href="/docs/user-guide">Documentation</a>`,
		`Ada Lovelace`, `ada@example.com`, // identity moves into the menu
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("account markup missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Signed in as") {
		t.Fatal("the email should no longer be spelled out in the top bar")
	}

	// No picture claim (or a non-http one): initials stand in, and nothing is
	// smuggled into the <img src>.
	noPic := org.accountHTML(oidc.Claims{"name": "Ada Lovelace", "picture": "javascript:alert(1)"}, true)
	if strings.Contains(noPic, "<img") || !strings.Contains(noPic, `>AL<`) {
		t.Fatalf("expected initials fallback, no img:\n%s", noPic)
	}
	if org.accountHTML(nil, true) != `<a class="signin" href="/auth/login">Sign in</a>` {
		t.Fatal("signed-out bar should offer Sign in")
	}
	// OIDC off: no identity, but Documentation and the Settings section still
	// need a home — a generic menu button carries them.
	open := org.accountHTML(nil, false)
	if !strings.Contains(open, `href="/docs/user-guide">Documentation<`) ||
		!strings.Contains(open, `>Settings<`) ||
		strings.Contains(open, "Sign out") {
		t.Fatalf("OIDC-off menu wrong: %s", open)
	}
	// The initials fallback itself is tested where it now lives
	// (internal/ui), alongside the avatar markup both consoles render.
}

// seedOneEvent gives a registry a pulse.
//
// Outcomes renders its full fourteen panels once anything has happened and a
// single orientation panel before that (pageOutcomes), because fourteen correct
// "nothing here" messages is what a new operator's second screen used to be. A
// test about the full page therefore has to say that something happened; a test
// about the cold start says nothing and gets the cold start.
func seedOneEvent(t *testing.T, ts *httptest.Server) {
	t.Helper()
	resp, body := request(t, "POST", ts.URL+"/v1/feedback", "test-key",
		// No segment: the cohort panels have their own empty state, and a test
		// about those has to be able to reach it.
		`[{"technique_id":"read-images-directly","stage":"shown","audit_id":"aud_seed"}]`)
	if resp.StatusCode >= 300 {
		t.Fatalf("seeding an event: %d %v", resp.StatusCode, body)
	}
}

func newServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	// Hermetic: never read the developer machine's real registry.env — its
	// embedder/key choices must not leak into the suite.
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "no-such.env"))
	cfg := config.Load()
	cfg.APIKey = "test-key"
	cfg.DataDir = t.TempDir()
	techniquesDir, _ := filepath.Abs("../../../techniques")
	docsDir, _ := filepath.Abs("../../../docs")
	cfg.TechniquesDir = techniquesDir
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	embedder, _ := embed.New(cfg.EmbedModel, cfg.EmbedDim)
	if _, _, err := jobs.Startup(st, techniquesDir, embedder); err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, st, embedder, docsDir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func request(t *testing.T, method, url, key string, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Tacit-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestHealthOpenAndAPIKeyRequired(t *testing.T) {
	_, ts := newServer(t)
	resp, body := request(t, "GET", ts.URL+"/v1/health", "", "")
	if resp.StatusCode != 200 || body["ok"] != true {
		t.Fatalf("health: %d %v", resp.StatusCode, body)
	}
	// The serving embedder is advertised so `tacit doctor` can catch the
	// silent onnx->hashing fallback from outside the process.
	if body["embed_model"] != "hashing-v1" {
		t.Fatalf("health embed_model = %v, want hashing-v1", body["embed_model"])
	}
}

func TestBulkDeleteDraftsOnly(t *testing.T) {
	srv, ts := newServer(t)
	for _, n := range []string{"a", "b", "c"} {
		resp, _ := request(t, "POST", ts.URL+"/v1/contribute", "test-key",
			`{"name": "bulk-`+n+`", "description": "d", "recipe": "r", "scope": "org"}`)
		if resp.StatusCode != 201 {
			t.Fatalf("contribute %s: %d", n, resp.StatusCode)
		}
	}
	drafts, _ := srv.Store.ListTechniques([]string{"draft"}, 0)
	if len(drafts) != 3 {
		t.Fatalf("drafts: %d", len(drafts))
	}
	stable, _ := srv.Store.ListTechniques([]string{"stable"}, 1)
	if len(stable) == 0 {
		t.Fatal("need a stable technique for the guard check")
	}

	// The page carries the selection UI.
	_, page := fetchHTML(t, ts.URL+"/review")
	if !strings.Contains(page, `form="bulk-drafts"`) || !strings.Contains(page, "Delete selected") {
		t.Fatal("drafts page lacks the bulk-delete controls")
	}
	// Suggest techniques carries the in-progress feedback, wired to the ids
	// the inline script drives — a run is synchronous and slow, so the click
	// must visibly register.
	for _, want := range []string{`action="/admin/suggest"`, `id="suggest-btn"`, `id="suggest-status"`, `id="suggest-elapsed"`, `class="suggest-bar"`, `id="suggest-fill"`, `data-est="`} {
		if !strings.Contains(page, want) {
			t.Errorf("drafts page missing suggest-feedback markup %q", want)
		}
	}

	// Delete two drafts; smuggle a stable id into the same request — the
	// draft-only rule must hold server-side, not just in the rendered UI.
	form := url.Values{"id": {drafts[0].ID, drafts[1].ID, stable[0].ID}}
	req, _ := http.NewRequest("POST", ts.URL+"/admin/techniques/bulk-delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	after, _ := srv.Store.ListTechniques([]string{"draft"}, 0)
	if len(after) != 1 || after[0].ID != drafts[2].ID {
		t.Fatalf("drafts after bulk delete: %+v", after)
	}
	if _, found, _ := srv.Store.GetTechnique(stable[0].ID); !found {
		t.Fatal("bulk delete removed a stable technique")
	}
}

// Docs pages may embed screenshots; the images are served from the docs tree
// itself, confined to it, and only for recognized asset extensions — every
// other slug still renders as a page.
func TestDocsServesImagesConfined(t *testing.T) {
	srv, ts := newServer(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "user-guide", "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\nfake")
	if err := os.WriteFile(filepath.Join(dir, "user-guide", "images", "map.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "user-guide", "index.md"),
		[]byte("# Guide\n\n![The map](images/map.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.DocsDir = dir

	resp, err := http.Get(ts.URL + "/docs/user-guide/images/map.png")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != string(png) {
		t.Fatalf("image fetch: %d, %d bytes", resp.StatusCode, len(body))
	}
	// This registry has no identity provider, so nothing it serves is public and
	// the asset is bucket B — the classifier's job, not this handler's opinion.
	// (TestUserGuideAssetIsPublicWithOIDC covers the cacheable case.) A short TTL
	// is what replaces the old no-cache once a provider is configured, so a
	// regenerated screenshot still lands on the next reload.
	if cc := resp.Header.Get("Cache-Control"); cc != cachepolicy.PrivateControl {
		t.Fatalf("docs asset Cache-Control = %q, want %q", cc, cachepolicy.PrivateControl)
	}

	// The rendered page rewrites the relative src to the absolute /docs path.
	// With no dark sibling on disk, the image stays a single un-classed img.
	_, page := fetchHTML(t, ts.URL+"/docs/user-guide")
	if !strings.Contains(page, `src="/docs/user-guide/images/map.png?v=`) {
		t.Fatal("embedded image src not rewritten to /docs path")
	}
	if strings.Contains(page, "theme-dark") {
		t.Fatal("dark variant emitted without a sibling on disk")
	}

	// With a -dark sibling, both variants render, classed for the theme CSS.
	if err := os.WriteFile(filepath.Join(dir, "user-guide", "images", "map-dark.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	_, page = fetchHTML(t, ts.URL+"/docs/user-guide")
	for _, want := range []string{
		`<img class="theme-light" src="/docs/user-guide/images/map.png?v=`,
		`<img class="theme-dark" src="/docs/user-guide/images/map-dark.png?v=`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("themed image pair missing %q", want)
		}
	}

	// A traversal attempt must not escape the docs dir.
	req, _ := http.NewRequest("GET", ts.URL+"/docs/ignored", nil)
	req.URL.Path = "/docs/../web_test.go" // bypass client-side cleaning
	req.URL.RawPath = "/docs/%2e%2e/web_test.png"
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode == 200 {
		t.Fatal("traversal escaped the docs dir")
	}
}

// A command copied out of the guide must already name the registry serving
// the guide: the stand-in hostname the markdown carries is rewritten to this
// registry's real address — the configured external URL when there is one,
// else the host the request arrived on. Development docs use the same
// hostname to mean some other deployment and keep it verbatim.
func TestUserGuideNamesThisRegistry(t *testing.T) {
	srv, ts := newServer(t)
	dir := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("user-guide/index.md", "# Guide\n\nJoin at `"+docExampleRegistry+"`.\n")
	write("user-guide/01-join.md", "# Join\n\n```bash\ntacit connect --registry "+docExampleRegistry+"\n```\n")
	write("plan.md", "# Plan\n\nAn operator might publish at `"+docExampleRegistry+"`.\n")
	srv.DocsDir = dir

	// No external URL configured: the address this request arrived on.
	for _, path := range []string{"/docs/user-guide", "/docs/user-guide/01-join"} {
		_, page := fetchHTML(t, ts.URL+path)
		if !strings.Contains(page, ts.URL) {
			t.Fatalf("%s doesn't name the serving registry:\n%s", path, page)
		}
		if strings.Contains(page, docExampleRegistry) {
			t.Fatalf("%s left the stand-in hostname in place", path)
		}
	}

	// Configured (or published) external URL wins — it is what members off
	// this machine can reach, which is who the guide's commands are for.
	srv.Cfg.ExternalURL = "https://tacit.corp.example/"
	_, page := fetchHTML(t, ts.URL+"/docs/user-guide/01-join")
	if !strings.Contains(page, "tacit connect --registry https://tacit.corp.example<") {
		t.Fatalf("external URL not used in the guide:\n%s", page)
	}

	// Development docs are prose about deployments in general, not about this
	// one; a live address must not be substituted into them.
	_, dev := fetchHTML(t, ts.URL+"/docs/plan")
	if !strings.Contains(dev, docExampleRegistry) {
		t.Fatalf("development doc lost its example hostname:\n%s", dev)
	}
}

// The drafts queue shows when each draft arrived and sorts like every other
// table: it opts into the shared data-table sort wiring, which must skip the
// select-all header so ticking it never cycles a sort.
func TestReviewDraftsShowDateAndSort(t *testing.T) {
	_, ts := newServer(t)
	resp, _ := request(t, "POST", ts.URL+"/v1/contribute", "test-key",
		`{"name": "dated-draft", "description": "d", "recipe": "r", "scope": "org"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("contribute: %d", resp.StatusCode)
	}
	_, page := fetchHTML(t, ts.URL+"/review")
	today := time.Now().UTC().Format("2006-01-02")
	for _, want := range []string{
		`class="techniques data-table techniques-drafts"`,
		`<th>added</th>`,
		// The cell is a <time> the reader's browser re-dates into its own zone
		// (ui.LocalTimeScript); the text here is the UTC fallback, and it stays
		// YYYY-MM-DD in both so the header sort keeps sorting it as text.
		`data-lt="ymd">` + today + `</time></td>`,
		`th.querySelector('input')`, // select-all header stays a checkbox, not a sort control
	} {
		if !strings.Contains(page, want) {
			t.Errorf("review drafts page missing %q", want)
		}
	}
}

func TestMembersPagePrefillsURLAndHidesKey(t *testing.T) {
	srv, ts := newServer(t)
	resp, err := http.Get(ts.URL + "/members")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("/members: %d", resp.StatusCode)
	}
	// The Members page's onboarding block pre-fills this registry's address in
	// the join line.
	if !strings.Contains(string(raw), ts.URL+"/join/") {
		t.Errorf("/members does not pre-fill the registry URL %s", ts.URL)
	}
	// The page is the thing an admin pastes into chat: the key must never
	// appear on it.
	if strings.Contains(string(raw), srv.Cfg.APIKey) {
		t.Error("/members leaks the API key")
	}
	resp, _ = request(t, "GET", ts.URL+"/v1/techniques", "", "")
	if resp.StatusCode != 401 {
		t.Fatalf("unkeyed /v1/techniques = %d", resp.StatusCode)
	}
	resp, _ = request(t, "GET", ts.URL+"/v1/techniques", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("keyed /v1/techniques = %d", resp.StatusCode)
	}
}

func TestMapAppEndpoint(t *testing.T) {
	_, ts := newServer(t)
	if resp, _ := request(t, "GET", ts.URL+"/v1/map/app", "", ""); resp.StatusCode != 401 {
		t.Fatalf("unkeyed /v1/map/app = %d, want 401", resp.StatusCode)
	}
	resp, body := request(t, "GET", ts.URL+"/v1/map/app", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("map/app = %d", resp.StatusCode)
	}
	html, _ := body["html"].(string)
	if !strings.Contains(html, "<!doctype html") || !strings.Contains(html, `id="cmap"`) || !strings.Contains(html, "cmap-data") {
		t.Fatalf("map app html missing structure: %.120q", html)
	}
	// Sandbox scope: no set-filter form, no describe-with-AI run (both round-trip).
	// Match the actual controls, not the inlined CSS rule names.
	if strings.Contains(html, `<form class="organization-filters"`) || strings.Contains(html, `id="cmap-describe-form"`) {
		t.Fatal("map app must not carry server-round-trip controls (filter/describe)")
	}
	if text, _ := body["text"].(string); !strings.Contains(text, "playbook map") {
		t.Fatalf("map app text missing header: %q", text)
	}
	if _, ok := body["summary"].(map[string]any); !ok {
		t.Fatalf("map app missing structured summary: %v", body["summary"])
	}
}

func TestInsightsAppEndpoint(t *testing.T) {
	_, ts := newServer(t)
	if resp, _ := request(t, "GET", ts.URL+"/v1/insights/app", "", ""); resp.StatusCode != 401 {
		t.Fatalf("unkeyed /v1/insights/app = %d, want 401", resp.StatusCode)
	}
	request(t, "POST", ts.URL+"/v1/feedback", "test-key",
		`[{"technique_id":"read-images-directly","stage":"shown","audit_id":"aud_s1","segment":{"harness":"claude-code"}},
		  {"technique_id":"read-images-directly","stage":"adopted","audit_id":"aud_s1","segment":{"harness":"claude-code"}}]`)

	resp, body := request(t, "GET", ts.URL+"/v1/insights/app?w=all", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("insights/app = %d", resp.StatusCode)
	}
	html, _ := body["html"].(string)
	if !strings.Contains(html, "<!doctype html") || !strings.Contains(html, `class="tiles"`) {
		t.Fatalf("insights app html missing structure: %.120q", html)
	}
	if text, _ := body["text"].(string); !strings.Contains(text, "Funnel:") {
		t.Fatalf("insights app text missing funnel line: %q", text)
	}
	if _, ok := body["summary"].(map[string]any); !ok {
		t.Fatalf("insights app missing structured summary: %v", body["summary"])
	}
}

// The app endpoints are what a skill curls when the MCP tool is unavailable,
// from an agent whose transcript the reply lands in. `format=text` drops the
// embedded document and keeps the answer.
func TestAppEndpointsServeTextOnly(t *testing.T) {
	_, ts := newServer(t)
	for _, path := range []string{"/v1/insights/app?w=all&format=text", "/v1/map/app?format=text",
		"/v1/organization/app?format=text", "/v1/review/app?format=text"} {
		resp, body := request(t, "GET", ts.URL+path, "test-key", "")
		if resp.StatusCode != 200 {
			t.Fatalf("%s = %d", path, resp.StatusCode)
		}
		if _, present := body["html"]; present {
			t.Errorf("%s still carries html under format=text", path)
		}
		if _, ok := body["text"].(string); !ok {
			t.Errorf("%s dropped the text it exists to serve: %v", path, body)
		}
		if _, ok := body["summary"]; !ok {
			t.Errorf("%s dropped its structured summary", path)
		}
	}
	// Without the parameter the panel is still served — app hosts depend on it.
	if _, body := request(t, "GET", ts.URL+"/v1/insights/app?w=all", "test-key", ""); body["html"] == nil {
		t.Fatal("default response lost its html")
	}
}

func TestEvidenceEndpoint(t *testing.T) {
	_, ts := newServer(t)
	char := `{"summary_text": "user pasted csv rows from the internal warehouse and asked for analysis",
		"tools_absent": ["internal-warehouse-connector"], "harness": "claude-code", "surface": "cli",
		"internal_resources_in_play": ["pipeline.csv (pasted)"], "segment": {"team": "revops"}}`
	resp, body := request(t, "POST", ts.URL+"/v1/evidence", "test-key", char)
	if resp.StatusCode != 200 {
		t.Fatalf("evidence = %d %v", resp.StatusCode, body)
	}
	cands, _ := body["candidates"].([]any)
	if len(cands) == 0 {
		t.Fatal("no candidates for the pasted-rows case")
	}
	meta, _ := body["meta"].(map[string]any)
	if meta["thin"] != true {
		t.Fatal("cold start should be thin")
	}
	resp, _ = request(t, "POST", ts.URL+"/v1/evidence", "test-key", `{"summary_text": "  "}`)
	if resp.StatusCode != 400 {
		t.Fatalf("blank summary = %d", resp.StatusCode)
	}
}

// The evidence call is where the learning layer's substrate is captured: what
// the work WAS, keyed by audit_id so it joins to the events recording how it
// turned OUT (docs/learning/synthesis-plan.md phase 0). Before this, the registry
// received the characterization and threw it away.
func TestEvidenceRecordsAuditFact(t *testing.T) {
	srv, ts := newServer(t)
	char := `{"audit_id": "aud_x1", "summary_text": "user pasted csv rows and asked for analysis",
		"task_type": "data-analysis", "model": "claude-haiku-4-5", "domain": "revenue-operations",
		"tools_used": ["Bash"], "tools_absent": ["internal-warehouse-connector"],
		"internal_resources_in_play": ["pipeline.csv (pasted)"],
		"harness": "claude-code", "surface": "cli", "skill_level": "intermediate",
		"segment": {"team": "revops"}}`
	if resp, body := request(t, "POST", ts.URL+"/v1/evidence", "test-key", char); resp.StatusCode != 200 {
		t.Fatalf("evidence = %d %v", resp.StatusCode, body)
	}

	facts, err := srv.Store.AuditFacts("")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("recorded %d audit facts, want 1", len(facts))
	}
	f := facts[0]
	if f.AuditID != "aud_x1" || f.TaskType != "data-analysis" || f.Harness != "claude-code" {
		t.Fatalf("audit fact lost its identity: %+v", f)
	}
	if f.Model != "claude-haiku-4-5" {
		t.Fatalf("audit fact lost the model — the per-model support matrix runs on this: %+v", f)
	}
	// The context half — the part that was being discarded, and the part every
	// context-vs-outcome finding depends on.
	if len(f.ToolsUsed) != 1 || len(f.ToolsAbsent) != 1 || len(f.Resources) != 1 {
		t.Fatalf("audit fact lost the interaction's context: %+v", f)
	}
	if f.Segment["team"] != "revops" {
		t.Fatalf("audit fact lost the cohort: %+v", f.Segment)
	}
	if len(f.TechniquesOffered) == 0 {
		t.Fatal("audit fact should record which techniques were surfaced")
	}

	// Replay: the hook agent retrying one evidence request must not double-count
	// the interaction, or every rate computed over the fact log inherits it.
	if resp, _ := request(t, "POST", ts.URL+"/v1/evidence", "test-key", char); resp.StatusCode != 200 {
		t.Fatalf("replayed evidence = %d", resp.StatusCode)
	}
	if facts, _ = srv.Store.AuditFacts(""); len(facts) != 1 {
		t.Fatalf("replayed audit recorded twice: %d facts", len(facts))
	}
}

// A producer that sends no audit_id must still retrieve normally — recording is
// bookkeeping and may never break a member's turn.
func TestEvidenceWithoutAuditIDStillRetrieves(t *testing.T) {
	srv, ts := newServer(t)
	resp, body := request(t, "POST", ts.URL+"/v1/evidence", "test-key",
		`{"summary_text": "user pasted csv rows and asked for analysis"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("evidence without audit_id = %d %v", resp.StatusCode, body)
	}
	if cands, _ := body["candidates"].([]any); len(cands) == 0 {
		t.Fatal("retrieval must still work for a producer that sends no audit_id")
	}
	facts, err := srv.Store.AuditFacts("")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("recorded %d facts without a join key; orphans are useless", len(facts))
	}
}

func TestFeedbackAcceptsAndDeduplicates(t *testing.T) {
	_, ts := newServer(t)
	batch := `[{"event_id": "evt_1", "technique_id": "ask-for-a-diagram", "stage": "shown"},
	           {"event_id": "evt_2", "technique_id": "ask-for-a-diagram", "stage": "adopted"}]`
	resp, body := request(t, "POST", ts.URL+"/v1/feedback", "test-key", batch)
	if resp.StatusCode != 202 || body["accepted"] != float64(2) {
		t.Fatalf("batch: %d %v", resp.StatusCode, body)
	}
	// replay: idempotent, counted separately, never a 500
	resp, body = request(t, "POST", ts.URL+"/v1/feedback", "test-key", batch)
	if resp.StatusCode != 202 || body["accepted"] != float64(0) || body["duplicates"] != float64(2) {
		t.Fatalf("replay: %d %v", resp.StatusCode, body)
	}
	resp, _ = request(t, "POST", ts.URL+"/v1/feedback", "test-key",
		`{"technique_id": "x", "stage": "bogus"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("invalid stage = %d", resp.StatusCode)
	}
}

func TestBreadcrumbNavigationAndNestedDrafts(t *testing.T) {
	_, ts := newServer(t)

	// Drafts is no longer a top-nav item — the nav renders links only, and none
	// point at /drafts.
	_, insights := fetchHTML(t, ts.URL+"/outcomes")
	if strings.Contains(insights, `>Drafts</a>`) {
		t.Fatal("Drafts still present as a top-nav item")
	}

	// Definition and outcomes now share one canonical technique view. The legacy
	// outcomes URL renders that same view, with the technique itself as the leaf.
	code, detail := fetchHTML(t, ts.URL+"/outcomes/use-internal-data-connector")
	if code != 200 {
		t.Fatalf("insight detail: %d", code)
	}
	for _, want := range []string{
		`<nav class="crumbs"`,
		`<a href="/techniques/map">Playbook</a>`,
		`<span class="crumb-current" aria-current="page">Query the warehouse instead of pasting rows</span>`,
		`class="technique-page"`,
		`class="technique-detail"`,
		`<h2>Performance</h2>`,
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("unified technique view missing %q", want)
		}
	}
	if strings.Contains(detail, `aria-current="page">Outcomes</span>`) ||
		strings.Contains(detail, `Open the full technique`) || strings.Contains(detail, `Not connected yet?`) {
		t.Fatal("obsolete secondary-view or acquisition chrome remains on the unified technique page")
	}

	// Review is a first-class destination in its own right now — not a
	// sub-view of Techniques.
	code, review := fetchHTML(t, ts.URL+"/review")
	if code != 200 || !strings.Contains(review, `<span class="crumb-current" aria-current="page">Review</span>`) ||
		!strings.Contains(review, `href="/review" class="active"`) {
		t.Fatalf("review breadcrumb/nav: %d", code)
	}

	// A pending draft surfaces its review count as a badge on the Review nav
	// link — the page that holds the work it points at.
	request(t, "POST", ts.URL+"/v1/contribute", "test-key",
		`{"name": "N", "description": "d", "recipe": "r", "scope": "org"}`)
	_, withDraft := fetchHTML(t, ts.URL+"/outcomes")
	if !strings.Contains(withDraft, `nav-badge review`) {
		t.Fatal("draft review badge not shown on the Review nav link")
	}
}

func TestInsightDrillDownPages(t *testing.T) {
	_, ts := newServer(t)
	seedOneEvent(t, ts)

	// The overview wires the shared drill-down affordances: the Suggestions-shown
	// tile links to the retrieval-miss list, and distribution bars carry drill
	// links (scope reuses the /techniques filter; source opens the by-source page).
	_, ov := fetchHTML(t, ts.URL+"/outcomes")
	if !strings.Contains(ov, `href="/review"`) {
		t.Fatal("overview: Suggestions-shown tile not linked to the review queue")
	}
	if !strings.Contains(ov, `class="tile-link"`) {
		t.Fatal("overview: retrieval-miss tile missing the shared tile-link affordance")
	}
	if !strings.Contains(ov, `href="/outcomes/helped-rate`) {
		t.Fatal("overview: Helped-rate tile not linked to the distribution")
	}
	// (The Signal-trust bar links to calibration only when reactions exist — a
	// data-gated LinkedMixBar, like the dismissals bar — so it isn't asserted on
	// the empty seed; the route below verifies the page itself renders.)

	// Each new drill-down renders with a breadcrumb back up to Insights, even
	// when the window has no matching activity (empty state, not an error).
	for _, path := range []string{
		"/outcomes/dismissals/not-relevant",
		"/outcomes/source/curated",
		"/outcomes/cohorts/harness:claude-code",
		"/outcomes/tag/diagram",
		"/outcomes/helped-rate",
		"/outcomes/signal-trust",
	} {
		code, page := fetchHTML(t, ts.URL+path)
		if code != 200 {
			t.Fatalf("%s = %d", path, code)
		}
		if !strings.Contains(page, `<nav class="crumbs"`) || !strings.Contains(page, `<a href="/outcomes`) {
			t.Fatalf("%s missing breadcrumb trail back to Insights", path)
		}
	}

	// The tag-filter note on the techniques list drills into tag performance.
	_, tagged := fetchHTML(t, ts.URL+"/techniques?tag=diagram")
	if !strings.Contains(tagged, `href="/outcomes/tag/diagram"`) {
		t.Fatal("tag filter note missing the tag-performance drill link")
	}

	// A cohort key without a dimension:value shape, and an unknown feed, 404 cleanly.
	if code, _ := fetchHTML(t, ts.URL+"/outcomes/cohorts/nocolon"); code != 404 {
		t.Fatalf("malformed cohort key = %d, want 404", code)
	}
	if code, _ := fetchHTML(t, ts.URL+"/federation/feed/no-such-feed"); code != 404 {
		t.Fatalf("unknown feed = %d, want 404", code)
	}
}

func TestFederationSubscribeForm(t *testing.T) {
	_, ts := newServer(t)

	// The Federation page carries a browser add-feed form.
	_, fed := fetchHTML(t, ts.URL+"/federation")
	if !strings.Contains(fed, `action="/admin/subscriptions"`) || !strings.Contains(fed, `name="feed_url"`) {
		t.Fatal("federation page missing the add-feed form")
	}

	// Submitting it creates a named subscription (session-gated; OIDC off in
	// tests, so open). PostForm follows the redirect back to /federation.
	form := url.Values{"name": {"Platform feed"},
		"feed_url": {"https://example.test/f/general/feed.json"}, "trust": {"review"}}
	resp, err := http.PostForm(ts.URL+"/admin/subscriptions", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("subscribe form = %d", resp.StatusCode)
	}

	// The feed row links by NAME (not the URL), to the detail view.
	_, fed2 := fetchHTML(t, ts.URL+"/federation")
	if !strings.Contains(fed2, `>Platform feed</a>`) {
		t.Fatal("feed row not linked by name")
	}
	m := regexp.MustCompile(`href="/federation/feed/([^"?]+)"`).FindStringSubmatch(fed2)
	if m == nil {
		t.Fatal("no feed detail link on the federation page")
	}
	code, detail := fetchHTML(t, ts.URL+"/federation/feed/"+m[1])
	if code != 200 || !strings.Contains(detail, `>Platform feed</span>`) {
		t.Fatalf("feed detail breadcrumb not using the name: %d", code)
	}

	// A bad feed URL re-renders with an error rather than redirecting.
	bad, err := http.PostForm(ts.URL+"/admin/subscriptions", url.Values{"feed_url": {""}})
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != 400 {
		t.Fatalf("empty feed_url = %d, want 400", bad.StatusCode)
	}
}

func TestContributeAndPromoteFlow(t *testing.T) {
	srv, ts := newServer(t)
	technique := `{"name": "Team warehouse move", "description": "d", "recipe": "use @warehouse", "scope": "org"}`
	resp, body := request(t, "POST", ts.URL+"/v1/contribute", "test-key", technique)
	if resp.StatusCode != 201 || body["status"] != "draft" || body["provenance"] != "contributed" {
		t.Fatalf("contribute: %d %v", resp.StatusCode, body)
	}
	id := body["id"].(string)

	// held out of retrieval while draft
	cands, _ := srv.Store.CandidateTechniques()
	for _, c := range cands {
		if c.ID == id {
			t.Fatal("draft is retrievable")
		}
	}

	resp, body = request(t, "POST", ts.URL+"/v1/admin/promote", "test-key",
		`{"id": "`+id+`"}`)
	if resp.StatusCode != 200 || body["status"] != "stable" {
		t.Fatalf("promote: %d %v", resp.StatusCode, body)
	}
	resp, _ = request(t, "POST", ts.URL+"/v1/admin/promote", "test-key", `{"id": "missing"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("promote missing = %d", resp.StatusCode)
	}
}

func TestSketchIntakeAccumulatesAndFilesOneRevision(t *testing.T) {
	srv, ts := newServer(t)
	// A stable base technique to attach sketches to (contribute + promote).
	resp, body := request(t, "POST", ts.URL+"/v1/contribute", "test-key",
		`{"name": "Warehouse move", "description": "d", "recipe": "use @warehouse", "scope": "org"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("contribute: %d %v", resp.StatusCode, body)
	}
	baseID := body["id"].(string)
	if resp, body = request(t, "POST", ts.URL+"/v1/admin/promote", "test-key", `{"id": "`+baseID+`"}`); resp.StatusCode != 200 {
		t.Fatalf("promote: %d %v", resp.StatusCode, body)
	}

	// No key → 401; Bearer works like X-Tacit-Key.
	if resp, _ = request(t, "POST", ts.URL+"/v1/sketches", "", `{"trigger": "t", "move": "m"}`); resp.StatusCode != 401 {
		t.Fatalf("keyless sketch = %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/v1/sketches", strings.NewReader(
		`{"trigger": "pasting rows by hand", "move": "use @warehouse", "technique_id": "`+baseID+`"}`))
	req.Header.Set("Authorization", "Bearer test-key")
	bresp, err := http.DefaultClient.Do(req)
	if err != nil || bresp.StatusCode != 201 {
		t.Fatalf("bearer sketch: %v %d", err, bresp.StatusCode)
	}
	bresp.Body.Close()

	// Two more distinct adoption contexts reach the threshold (3) and file
	// exactly one revision draft through the review lane.
	resp, body = request(t, "POST", ts.URL+"/v1/sketches", "test-key",
		`{"trigger": "manually exporting csv for the model", "move": "use @warehouse", "technique_id": "`+baseID+`"}`)
	if resp.StatusCode != 201 || body["revision_filed"] != nil {
		t.Fatalf("second sketch: %d %v", resp.StatusCode, body)
	}
	resp, body = request(t, "POST", ts.URL+"/v1/sketches", "test-key",
		`{"trigger": "asking the model to fabricate revenue numbers", "move": "use @warehouse", "technique_id": "`+baseID+`"}`)
	if resp.StatusCode != 201 || body["revision_filed"] == nil {
		t.Fatalf("third sketch should file a revision: %d %v", resp.StatusCode, body)
	}
	draftID := body["revision_filed"].(string)
	draft, ok, _ := srv.Store.GetTechnique(draftID)
	if !ok || draft.Status != "draft" || draft.Supersedes != baseID ||
		!strings.Contains(draft.Description, "Adoption contexts") {
		t.Fatalf("auto revision draft: %+v", draft)
	}

	// A fourth sketch must NOT file a second draft while one is open.
	resp, body = request(t, "POST", ts.URL+"/v1/sketches", "test-key",
		`{"trigger": "yet another context", "move": "use @warehouse", "technique_id": "`+baseID+`"}`)
	if resp.StatusCode != 201 || body["revision_filed"] != nil {
		t.Fatalf("fourth sketch double-filed: %v", body)
	}
}

func TestTechniquesAPIDropsEmbedding(t *testing.T) {
	_, ts := newServer(t)
	resp, body := request(t, "GET", ts.URL+"/v1/techniques/ask-for-a-diagram", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("get technique = %d", resp.StatusCode)
	}
	if _, present := body["embedding"]; present {
		t.Fatal("embedding leaked over the API")
	}
	resp, _ = request(t, "GET", ts.URL+"/v1/techniques/nope", "test-key", "")
	if resp.StatusCode != 404 {
		t.Fatalf("missing technique = %d", resp.StatusCode)
	}
}

func fetchHTML(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, b.String()
}

// With OIDC on and no session, the user guide is public — a first-time member
// reads it before they have a session — while the rest of the docs tree and the
// working dashboard are not. The front door stands in for gated pages.
func TestPublicUserGuideWhenOIDCOn(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{} // enabled, no session presented

	// A gated dashboard page yields the front door, not the page.
	code, front := fetchHTML(t, ts.URL+"/outcomes")
	if code != 200 || !strings.Contains(front, `class="signin-lede"`) {
		t.Fatalf("gated page should render the front door: %d", code)
	}
	// The front door is more than a button: it links into the public guide and
	// carries a connect command addressed to THIS registry.
	for _, want := range []string{
		`href="/docs/user-guide"`,
		"tacit connect --registry " + ts.URL,
	} {
		if !strings.Contains(front, want) {
			t.Fatalf("front door missing %q", want)
		}
	}

	// The user guide itself renders — the real shell, not the front door.
	code, guide := fetchHTML(t, ts.URL+"/docs/user-guide")
	if code != 200 {
		t.Fatalf("user guide status: %d", code)
	}
	if strings.Contains(guide, `class="signin-lede"`) {
		t.Fatal("user guide should render, not the front door")
	}
	if !strings.Contains(guide, "Tacit records which ways of working with AI help") {
		t.Fatal("user guide body not rendered")
	}
	// A guide page reached without a session shows a signed-out shell: the
	// Sign in affordance, and no library totals.
	if !strings.Contains(guide, `class="signin" href="/auth/login"`) {
		t.Fatal("signed-out guide should offer Sign in")
	}
	if strings.Contains(guide, " rollups") {
		t.Fatal("signed-out guide should not expose library totals")
	}

	// A guide image loads without a session; the rest of the tree does not.
	if resp, err := http.Get(ts.URL + "/docs/user-guide/images/hero.svg"); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("guide image should be public: %d", resp.StatusCode)
		}
	}

	// A non-guide doc, and the Development index, stay behind the front door.
	for _, gated := range []string{"/docs/concepts/concept-agent-fleets", "/docs"} {
		code, body := fetchHTML(t, ts.URL+gated)
		if !strings.Contains(body, `class="signin-lede"`) {
			t.Fatalf("%s should be gated (front door), got %d", gated, code)
		}
	}
}

func TestDashboardPages(t *testing.T) {
	_, ts := newServer(t)
	seedOneEvent(t, ts)
	// The landing is Outcomes — the merged funnel-and-cohorts view. The nav is
	// the three questions and nothing else: Federation and Learning are
	// operator concerns and moved to the account menu.
	code, html := fetchHTML(t, ts.URL+"/")
	if code != 200 || !strings.Contains(html, "How "+product.Name()+" helps") ||
		!strings.Contains(html, "Adoption by cohort and area") {
		t.Fatalf("landing page should be the merged Outcomes: %d", code)
	}
	// Just the top-bar nav element — Federation and Learning are still linked,
	// from the account menu, which sits earlier in the markup.
	topNav := html[strings.Index(html, "<nav>"):]
	topNav = topNav[:strings.Index(topNav, "</nav>")]

	// The FIRST nav item must be the landing page. The brand mark links to "/",
	// so if the two disagree, clicking "home" lights up the middle of the bar
	// and the leftmost item is something you never land on. Outcomes is served
	// at "/", so Outcomes leads.
	if !strings.Contains(topNav, `class="active"`) {
		t.Fatal("landing page highlights no nav item")
	}
	if first := topNav[:strings.Index(topNav, "</a>")]; !strings.Contains(first, `class="active"`) {
		t.Fatalf("the landing page is not the first nav item — home would highlight the middle of the bar: %s", first)
	}
	// Order is load-bearing, so assert it rather than mere presence.
	if iOut, iCap, iRev := strings.Index(topNav, ">Outcomes"), strings.Index(topNav, ">Playbook"), strings.Index(topNav, ">Review"); !(iOut < iCap && iCap < iRev) {
		t.Fatal("top bar order should be Outcomes · Techniques · Review")
	}
	for _, want := range []string{`>Playbook`, `>Outcomes`, `>Review`} {
		if !strings.Contains(topNav, want) {
			t.Fatalf("top bar missing %q", want)
		}
	}
	for _, gone := range []string{"Organization", "Federation", "Learning"} {
		if strings.Contains(topNav, gone) {
			t.Fatalf("top bar still carries %q", gone)
		}
	}
	if code, _ := fetchHTML(t, ts.URL+"/outcomes"); code != 200 {
		t.Fatalf("/outcomes overview must keep serving: %d", code)
	}
	// Every view in the section names itself in the trail: Techniques / All,
	// Techniques / Retired, Techniques / Tags.
	code, html = fetchHTML(t, ts.URL+"/techniques")
	if code != 200 || !strings.Contains(html, `<nav class="crumbs"`) ||
		!strings.Contains(html, `<a href="/techniques/map">Playbook</a>`) ||
		!strings.Contains(html, `<summary aria-current="page">All<svg`) {
		t.Fatalf("techniques list breadcrumb: %d", code)
	}
	if !strings.Contains(html, "ask-for-a-diagram") {
		t.Fatal("techniques list missing seed technique")
	}
	// The techniques table carries a source column (provenance), like drafts.
	if !strings.Contains(html, "<th>source</th>") || !strings.Contains(html, ">curated<") {
		t.Fatal("techniques list missing the source column")
	}
	code, html = fetchHTML(t, ts.URL+"/techniques/use-internal-data-connector")
	if code != 200 || !strings.Contains(html, "warehouse") {
		t.Fatalf("technique detail: %d", code)
	}
	code, _ = fetchHTML(t, ts.URL+"/techniques/never-heard-of-it")
	if code != 404 {
		t.Fatalf("missing technique detail = %d", code)
	}
	code, html = fetchHTML(t, ts.URL+"/review")
	if code != 200 || !strings.Contains(html, "Drafts") {
		t.Fatalf("drafts: %d", code)
	}
	code, html = fetchHTML(t, ts.URL+"/docs/user-guide/10-get-started/01-tacit-at-a-glance")
	if code != 200 || !strings.Contains(html, "technique") {
		t.Fatalf("docs view: %d", code)
	}
	code, _ = fetchHTML(t, ts.URL+"/docs/not-a-doc")
	if code != 404 {
		t.Fatalf("missing doc = %d", code)
	}
}

func TestInsightDetailShowsActivityBarChart(t *testing.T) {
	_, ts := newServer(t)
	// Give a seed technique some funnel events so the chart has data to render.
	batch := `[{"technique_id":"ask-for-a-diagram","stage":"shown"},
	           {"technique_id":"ask-for-a-diagram","stage":"adopted"},
	           {"technique_id":"ask-for-a-diagram","stage":"helped"},
	           {"technique_id":"ask-for-a-diagram","stage":"dismissed"}]`
	request(t, "POST", ts.URL+"/v1/feedback", "test-key", batch)

	code, html := fetchHTML(t, ts.URL+"/techniques/ask-for-a-diagram?w=all")
	if code != 200 {
		t.Fatalf("insight detail: %d", code)
	}
	// The decay-watch chart is gone, replaced by the stacked activity bar chart.
	if strings.Contains(html, "Decay watch") {
		t.Fatal("decay-watch chart should be removed from the technique insights page")
	}
	if !strings.Contains(html, `data-series="shown|adopted|helped|dismissed"`) {
		t.Fatal("technique insights page missing the stacked activity bar chart")
	}
	if !strings.Contains(html, `class="vbar `) {
		t.Fatal("activity chart rendered no bars")
	}
}

func TestActivityPanelOnRollupViews(t *testing.T) {
	_, ts := newServer(t)
	// Seed funnel events (carrying a cohort segment) for a technique whose tag and
	// source we know, so the tag, source, and cohort roll-ups all have activity.
	batch := `[{"technique_id":"ask-for-a-diagram","stage":"shown","segment":{"harness":"claude-code"}},
	           {"technique_id":"ask-for-a-diagram","stage":"adopted","segment":{"harness":"claude-code"}},
	           {"technique_id":"ask-for-a-diagram","stage":"helped","segment":{"harness":"claude-code"}},
	           {"technique_id":"ask-for-a-diagram","stage":"dismissed","segment":{"harness":"claude-code"}}]`
	request(t, "POST", ts.URL+"/v1/feedback", "test-key", batch)

	// The same shared Activity stacked-bar panel appears on every roll-up view.
	for _, path := range []string{
		"/outcomes/tag/diagram?w=all",
		"/outcomes/source/curated?w=all",
		"/outcomes/cohorts/harness:claude-code?w=all",
	} {
		code, html := fetchHTML(t, ts.URL+path)
		if code != 200 {
			t.Fatalf("%s = %d", path, code)
		}
		if !strings.Contains(html, `<h2>Activity</h2>`) ||
			!strings.Contains(html, `data-series="shown|adopted|helped|dismissed"`) {
			t.Fatalf("%s missing the shared Activity panel", path)
		}
		if !strings.Contains(html, `class="vbar `) {
			t.Fatalf("%s Activity chart rendered no bars", path)
		}
	}
}

// The review lane is reachable at zero drafts — the state where the nav badge
// is absent. It used to depend on a link in the /techniques sub-line; Review is a
// nav destination in its own right now, so the lane can never go missing.
func TestReviewLaneAlwaysReachable(t *testing.T) {
	srv, ts := newServer(t)
	if d, _ := srv.Store.ListTechniques([]string{"draft"}, 0); len(d) != 0 {
		t.Fatalf("expected 0 drafts, got %d", len(d))
	}
	code, page := fetchHTML(t, ts.URL+"/techniques")
	if code != 200 || !strings.Contains(page, `href="/review"`) {
		t.Fatal("Review is not reachable from the nav at zero drafts")
	}
	// And the queue itself still offers the suggestion run — the exact state
	// where an operator wants to seed drafts.
	code, review := fetchHTML(t, ts.URL+"/review")
	if code != 200 || !strings.Contains(review, "Suggest candidate techniques") {
		t.Fatal("empty review queue drops the suggestion run")
	}
}

func TestTechniquesTagFilter(t *testing.T) {
	_, ts := newServer(t)

	// Tags render as links to each tag's view (/outcomes/tag/<tag>) on the list.
	code, html := fetchHTML(t, ts.URL+"/techniques")
	if code != 200 || !strings.Contains(html, `href="/outcomes/tag/diagram"`) {
		t.Fatal("techniques list missing tag-view links")
	}
	unfiltered := strings.Count(html, `data-href="/techniques/`)

	// Filtering by a tag narrows the list and every remaining row carries it.
	code, filtered := fetchHTML(t, ts.URL+"/techniques?tag=diagram")
	if code != 200 {
		t.Fatalf("tag filter: %d", code)
	}
	kept := strings.Count(filtered, `data-href="/techniques/`)
	if kept == 0 || kept >= unfiltered {
		t.Fatalf("tag filter did not narrow the list: %d of %d", kept, unfiltered)
	}
	if !strings.Contains(filtered, "ask-for-a-diagram") {
		t.Fatal("tag=diagram dropped the diagram technique")
	}
	// The active value is restated as a removable chip, and Clear resets the
	// set — the same affordances Outcomes has, because it is the same control.
	if !strings.Contains(filtered, `<span class="fchip">tag: diagram<a href=`) ||
		!strings.Contains(filtered, `>Clear</a>`) {
		t.Fatal("filtered view missing the active-tag chip / Clear affordance")
	}

	// A tag no technique carries yields the empty state, not a broken table.
	code, none := fetchHTML(t, ts.URL+"/techniques?tag=nonexistent-xyz")
	if code != 200 || !strings.Contains(none, "No reviewed techniques match this filter") {
		t.Fatal("unknown tag missing empty state")
	}
	if strings.Count(none, `data-href="/techniques/`) != 0 {
		t.Fatal("unknown tag still listed techniques")
	}
}

func TestTechniquesScopeFilter(t *testing.T) {
	_, ts := newServer(t)

	// The scope COLUMN is gone — it said "general" on nearly every row. The
	// org-scoped exception keeps its badge on the name, and that badge is
	// still the filter link the cell used to be; the org-scoped count in the
	// sub-line is the other way in.
	code, html := fetchHTML(t, ts.URL+"/techniques")
	if code != 200 || !strings.Contains(html, `href="/techniques?scope=org"`) {
		t.Fatal("techniques list missing the org-scope filter link")
	}
	if strings.Contains(html, "<th>scope</th>") {
		t.Fatal("the constant scope column regrew")
	}
	unfiltered := strings.Count(html, `data-href="/techniques/`)

	// Filtering by scope narrows the list and shows the active-filter affordance.
	code, filtered := fetchHTML(t, ts.URL+"/techniques?scope=general")
	if code != 200 {
		t.Fatalf("scope filter: %d", code)
	}
	kept := strings.Count(filtered, `data-href="/techniques/`)
	if kept == 0 || kept > unfiltered {
		t.Fatalf("scope filter did not narrow the list sensibly: %d of %d", kept, unfiltered)
	}
	if !strings.Contains(filtered, `<span class="fchip">scope: general<a href=`) ||
		!strings.Contains(filtered, `>Clear</a>`) {
		t.Fatal("filtered view missing the active-scope chip / Clear affordance")
	}

	// An unknown scope yields the shared empty state, not a broken table.
	code, none := fetchHTML(t, ts.URL+"/techniques?scope=nonexistent-xyz")
	if code != 200 || !strings.Contains(none, "No reviewed techniques match this filter") {
		t.Fatal("unknown scope missing empty state")
	}
	if strings.Count(none, `data-href="/techniques/`) != 0 {
		t.Fatal("unknown scope still listed techniques")
	}
}

func TestAppleTouchIcon(t *testing.T) {
	_, ts := newServer(t)

	// The shell links the icon and names the home-screen bookmark.
	code, html := fetchHTML(t, ts.URL+"/")
	if code != 200 || !strings.Contains(html, `rel="apple-touch-icon" href="/apple-touch-icon.png"`) {
		t.Fatal("shell missing apple-touch-icon link")
	}
	if !strings.Contains(html, `apple-mobile-web-app-title" content="`+product.Name()+`"`) {
		t.Fatal("shell missing home-screen title")
	}
	if !strings.Contains(html, `aria-label="`+product.Name()+` home"`) {
		t.Fatal("shell missing concise home label")
	}
	resp, err := http.Get(ts.URL + "/manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(manifest), `"description": "Your organization’s measured playbook for work with AI."`) {
		t.Fatal("manifest missing concise description")
	}

	// The icon serves as an opaque PNG at the linked path and the precomposed
	// alias — both public (the sign-in page links it too).
	for _, path := range []string{"/apple-touch-icon.png", "/apple-touch-icon-precomposed.png"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s = %d", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
			t.Fatalf("%s content-type = %q", path, ct)
		}
		// PNG magic — a real raster, not an SVG or empty body (which iOS drops).
		if len(body) < 8 || string(body[:8]) != "\x89PNG\r\n\x1a\n" {
			t.Fatalf("%s is not a PNG (%d bytes)", path, len(body))
		}
	}

	// /favicon.ico is asked for on a cold visit whatever the page declares
	// inline, and the registry had no route for it — a 404 on the way in, on
	// every first visit, and one that reads in a proxy's log as a broken app.
	// It serves the mark, and it is public: the front door links it too.
	resp, err = http.Get(ts.URL + "/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	icon, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/favicon.ico = %d, want the icon rather than a not-found", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("/favicon.ico content-type = %q", ct)
	}
	if !strings.HasPrefix(string(icon), "<svg") {
		t.Errorf("/favicon.ico did not serve an SVG document: %.40q", icon)
	}
}

func TestInsightsPages(t *testing.T) {
	srv, ts := newServer(t)
	// seed a small measured history so charts and leaderboards have content
	batch := `[
	 {"technique_id":"ask-for-a-diagram","stage":"shown","segment":{"team":"revops"}},
	 {"technique_id":"ask-for-a-diagram","stage":"adopted","segment":{"team":"revops"}},
	 {"technique_id":"ask-for-a-diagram","stage":"helped","segment":{"team":"revops"}},
	 {"technique_id":"use-internal-data-connector","stage":"shown"},
	 {"technique_id":"use-internal-data-connector","stage":"dismissed","value":"not-relevant","confidence":"explicit"}]`
	resp, body := request(t, "POST", ts.URL+"/v1/feedback", "test-key", batch)
	if resp.StatusCode != 202 {
		t.Fatalf("seed: %d %v", resp.StatusCode, body)
	}
	_ = srv

	code, html := fetchHTML(t, ts.URL+"/outcomes?w=7d")
	if code != 200 {
		t.Fatalf("insights: %d", code)
	}
	for _, want := range []string{
		"Adoptions", "Helped rate", "How " + product.Name() + " helps", "Most adoptions", "Highest helped rate",
		// One cohort: the map declines to draw and falls back to the
		// cross-dimension ranking, which still names the cohort.
		"Adoption by cohort and area", "The map requires two cohorts",
		"team:revops", "not-relevant", "svg", "Last 7 days",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("insights overview missing %q", want)
		}
	}

	code, html = fetchHTML(t, ts.URL+"/techniques/ask-for-a-diagram?w=7d")
	if code != 200 {
		t.Fatalf("drill-down: %d", code)
	}
	for _, want := range []string{"Cumulative adoption", "<h2>Activity</h2>", `class="vbar `, "n=1"} {
		if !strings.Contains(html, want) {
			t.Fatalf("drill-down missing %q", want)
		}
	}
	code, _ = fetchHTML(t, ts.URL+"/outcomes/never-heard-of-it")
	if code != 404 {
		t.Fatalf("missing technique drill-down = %d", code)
	}
}

func TestHelpedRateLeaderboardMatchesDescendingTableTieOrder(t *testing.T) {
	_, ts := newServer(t)
	batch := `[
	 {"technique_id":"ask-for-a-diagram","stage":"adopted"},
	 {"technique_id":"ask-for-a-diagram","stage":"helped"},
	 {"technique_id":"ask-for-a-diagram","stage":"adopted"},
	 {"technique_id":"ask-for-a-diagram","stage":"helped"},
	 {"technique_id":"use-internal-data-connector","stage":"adopted"},
	 {"technique_id":"use-internal-data-connector","stage":"helped"}
	]`
	resp, body := request(t, "POST", ts.URL+"/v1/feedback", "test-key", batch)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("seed feedback: %d %v", resp.StatusCode, body)
	}
	code, page := fetchHTML(t, ts.URL+"/outcomes/helped-rate?w=all")
	if code != 200 {
		t.Fatalf("helped-rate page = %d", code)
	}
	leaderboardEnd := strings.Index(page, `<h2>All measured techniques</h2>`)
	if leaderboardEnd < 0 {
		t.Fatal("helped-rate page missing measured-techniques table")
	}
	leaderboard := page[:leaderboardEnd]
	largerSample := strings.Index(leaderboard, "Ask for a diagram you can keep")
	smallerSample := strings.Index(leaderboard, "Query the warehouse instead of pasting rows")
	if largerSample < 0 || smallerSample < 0 || largerSample > smallerSample {
		t.Fatalf("equal 100%% rates should rank larger n first: larger=%d smaller=%d", largerSample, smallerSample)
	}
}

func TestBrowserPromoteRejectButtons(t *testing.T) {
	srv, ts := newServer(t)
	_, body := request(t, "POST", ts.URL+"/v1/contribute", "test-key",
		`{"name": "Draft to reject", "description": "d", "recipe": "r"}`)
	id := body["id"].(string)

	// drafts view lists it
	_, html := fetchHTML(t, ts.URL+"/review")
	if !strings.Contains(html, id) {
		t.Fatal("draft not listed")
	}

	// browser reject: form POST, redirects back to /drafts
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/admin/techniques/reject/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("reject = %d", resp.StatusCode)
	}
	got, _, _ := srv.Store.GetTechnique(id)
	if got.Status != "retired" {
		t.Fatalf("status after reject = %s", got.Status)
	}
}

func TestBrowserRouteReviewsDraftsOnly(t *testing.T) {
	// On an open pilot (OIDC off) the browser route must not let a drive-by
	// POST retire a stable curated technique — drafts only.
	srv, ts := newServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/admin/techniques/reject/ask-for-a-diagram", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("reject non-draft = %d", resp.StatusCode)
	}
	got, _, _ := srv.Store.GetTechnique("ask-for-a-diagram")
	if got.Status != "stable" {
		t.Fatalf("stable technique retired via browser route: %s", got.Status)
	}
	// the key-authed admin API retains full lifecycle control
	resp2, body := request(t, "POST", ts.URL+"/v1/admin/promote", "test-key",
		`{"id": "ask-for-a-diagram", "status": "retired"}`)
	if resp2.StatusCode != 200 || body["status"] != "retired" {
		t.Fatalf("admin API retire: %d %v", resp2.StatusCode, body)
	}
}

func TestBrowserReturnToDraft(t *testing.T) {
	srv, ts := newServer(t)
	// Contribute a draft and promote it to stable (a live technique).
	_, body := request(t, "POST", ts.URL+"/v1/contribute", "test-key",
		`{"name": "Live move", "description": "d", "recipe": "r"}`)
	id := body["id"].(string)
	if r, _ := request(t, "POST", ts.URL+"/v1/admin/promote", "test-key",
		`{"id": "`+id+`", "status": "stable"}`); r.StatusCode != 200 {
		t.Fatalf("promote to stable: %d", r.StatusCode)
	}

	// The technique detail offers the Return-to-drafts action for a live technique inline
	// with its metadata, rather than spending a separate panel on one button.
	_, html := fetchHTML(t, ts.URL+"/techniques/"+id)
	footerStart := strings.Index(html, `<footer class="technique-detail-foot">`)
	footerEnd := strings.Index(html, `</footer>`)
	action := `action="/admin/techniques/to-draft/` + id + `"`
	if footerStart < 0 || footerEnd < footerStart || !strings.Contains(html[footerStart:footerEnd], action) {
		t.Fatal("technique metadata footer missing the Return to drafts action")
	}
	if strings.Contains(html, `<h2>Review status</h2>`) {
		t.Fatal("Return to drafts still occupies a separate panel")
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/admin/techniques/to-draft/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("to-draft = %d", resp.StatusCode)
	}
	if got, _, _ := srv.Store.GetTechnique(id); got.Status != "draft" {
		t.Fatalf("status after return to draft = %s, want draft", got.Status)
	}
	// It's back in the review lane and no longer a retrieval candidate.
	if _, listed := fetchHTML(t, ts.URL+"/review"); !strings.Contains(listed, id) {
		t.Fatal("returned technique not shown in the review lane")
	}

	// Guard: to-draft only acts on a stable technique. Re-running now (it's a draft)
	// must be a no-op, not flip it further.
	resp2, _ := client.PostForm(ts.URL+"/admin/techniques/to-draft/"+id, nil)
	resp2.Body.Close()
	if got, _, _ := srv.Store.GetTechnique(id); got.Status != "draft" {
		t.Fatalf("to-draft on a non-stable technique changed status to %s", got.Status)
	}
}

func TestTechniqueHistoryUsesSideBySideWordDiff(t *testing.T) {
	srv, ts := newServer(t)
	technique, ok, err := srv.Store.GetTechnique("ask-for-a-diagram")
	if err != nil || !ok {
		t.Fatalf("seed technique: %v %v", ok, err)
	}
	archived := technique
	archived.Version = 1
	archived.Recipe = "Run npm test before deploy"
	archived.UpdatedAt = "2026-01-01T00:00:00Z"
	if err := srv.Store.ArchiveTechniqueVersion(archived); err != nil {
		t.Fatal(err)
	}
	technique.Version = 2
	technique.Recipe = "Run go test before release"
	technique.UpdatedAt = "2026-02-01T00:00:00Z"
	if err := srv.Store.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}

	code, body := fetchHTML(t, ts.URL+"/techniques/history/ask-for-a-diagram")
	if code != 200 {
		t.Fatalf("history = %d", code)
	}
	for _, want := range []string{
		`class="version-diff panel"`, `v1 → v2`, `v2 · current`,
		`class="version-diff-line diff-removed"`, `class="version-diff-line diff-added"`,
		`<mark class="diff-word-del">npm`, `<mark class="diff-word-add">go`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("side-by-side history missing %q", want)
		}
	}
	if strings.Contains(body, `class="techniqueblock"`) {
		t.Fatal("history still renders archived versions as full techniques")
	}
}

func TestBrowserRevertToPriorVersion(t *testing.T) {
	srv, ts := newServer(t)
	technique, ok, err := srv.Store.GetTechnique("ask-for-a-diagram")
	if err != nil || !ok {
		t.Fatalf("seed technique: %v %v", ok, err)
	}
	// Build history: v1 archived with the original recipe, v2 current with a new one.
	archived := technique
	archived.Version = 1
	archived.Recipe = "Run npm test before deploy"
	archived.UpdatedAt = "2026-01-01T00:00:00Z"
	if err := srv.Store.ArchiveTechniqueVersion(archived); err != nil {
		t.Fatal(err)
	}
	technique.Version = 2
	technique.Recipe = "Run go test before release"
	technique.UpdatedAt = "2026-02-01T00:00:00Z"
	if err := srv.Store.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}

	// The history page offers a confirmed revert control for the archived version.
	_, body := fetchHTML(t, ts.URL+"/techniques/history/ask-for-a-diagram")
	for _, want := range []string{
		`action="/admin/techniques/revert/1/ask-for-a-diagram"`,
		`onsubmit="return confirm(`,
		`>Revert to v1</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("history missing revert control %q", want)
		}
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// Guard: a version that was never archived is a no-op redirect, not a 500 and
	// not a mutation — the revert can't be pointed at an arbitrary number.
	respBad, err := client.PostForm(ts.URL+"/admin/techniques/revert/99/ask-for-a-diagram", nil)
	if err != nil {
		t.Fatal(err)
	}
	respBad.Body.Close()
	if respBad.StatusCode != 302 {
		t.Fatalf("revert to a missing version = %d", respBad.StatusCode)
	}
	if got, _, _ := srv.Store.GetTechnique("ask-for-a-diagram"); got.Version != 2 || got.Recipe != "Run go test before release" {
		t.Fatalf("no-op revert changed the technique: v%d %q", got.Version, got.Recipe)
	}

	// Revert to v1: lands a NEW current version (v3) carrying v1's content.
	resp, err := client.PostForm(ts.URL+"/admin/techniques/revert/1/ask-for-a-diagram", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("revert = %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/techniques/history/ask-for-a-diagram" {
		t.Fatalf("revert redirect = %q, want the technique's history", loc)
	}
	got, _, _ := srv.Store.GetTechnique("ask-for-a-diagram")
	if got.Version != 3 {
		t.Fatalf("revert must land a new version, got v%d", got.Version)
	}
	if got.Recipe != "Run npm test before deploy" {
		t.Fatalf("reverted content is not v1's: %q", got.Recipe)
	}
	// History is append-only: v2 (just replaced) and v1 are both archived.
	if vs, _ := srv.Store.TechniqueVersions("ask-for-a-diagram"); len(vs) != 2 || vs[0].Version != 2 || vs[1].Version != 1 {
		t.Fatalf("history after revert not [v2, v1]: %+v", vs)
	}
}

func TestSideBySideDiffAlignsInsertedAndEditedLines(t *testing.T) {
	rows := sideBySideTextDiff("alpha\nbeta", "intro\nalpha changed\nbeta changed")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(rows), rows)
	}
	if rows[0].oldNo != 0 || rows[0].newNo != 1 || rows[0].newClass != "diff-added" {
		t.Fatalf("inserted line not aligned with a gap: %+v", rows[0])
	}
	if rows[1].oldNo != 1 || rows[1].newNo != 2 ||
		!strings.Contains(rows[1].newHTML, `diff-word-add`) {
		t.Fatalf("first edited line misaligned: %+v", rows[1])
	}
	if rows[2].oldNo != 2 || rows[2].newNo != 3 ||
		!strings.Contains(rows[2].newHTML, `diff-word-add`) {
		t.Fatalf("second edited line misaligned: %+v", rows[2])
	}
}

func TestDiffBudgetFallsBackWithoutLosingEscaping(t *testing.T) {
	old := strings.Repeat("old<value> ", 600)
	next := strings.Repeat("new&value ", 600)
	oldHTML, nextHTML := inlineWordDiff(old, next)
	if strings.Contains(oldHTML, "<value>") || strings.Contains(nextHTML, "new&value") {
		t.Fatal("large-diff fallback emitted unescaped content")
	}
	if !strings.Contains(oldHTML, "old&lt;value&gt;") || !strings.Contains(nextHTML, "new&amp;value") {
		t.Fatal("large-diff fallback lost content")
	}
}

// The docs index is a tree mirroring the docs/ directory: folders from
// subdirectories, native <details> groups, and data-search on every row so the
// header filter works — a directory's key covers its descendants so filtering
// never hides a folder that still has a match inside.
func TestDocsIndexTree(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("alpha.md", "# Alpha Note\n\nbody")
	write("dev/00-start.md", "# Start Here\n\nbody")
	write("dev/inner/deep.md", "no heading")
	write("user-guide/01-intro.md", "# Welcome\n\nbody")

	write("index.md", "# All The Docs\n\nthe root folder explains itself")
	write("dev/index.md", "# The Dev Series\n\nwhat lives in dev/")
	write("user-guide/index.md", "# The Guide\n\nthe guide explains itself")

	srv := &Server{DocsDir: dir}
	page := srv.docsFolderHTML(srv.listDocs(), "")
	for _, want := range []string{
		`class="doc-techniques"`,
		`<a class="doc-technique" href="/docs/dev"`,                 // subdirectory as a category technique
		`<span class="doc-technique-name">The Dev Series</span>`,    // labeled by its index title...
		`<span class="doc-technique-sum">what lives in dev/</span>`, // ...summarized by its index's first paragraph
		`<a class="doc-technique" href="/docs/alpha"`,               // top-level doc as a page technique
		`<span class="doc-technique-name">Alpha Note</span><span class="doc-technique-sum">body</span>`,
		"the root folder explains itself", // the root index.md renders above the techniques
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("docs techniques missing %q in:\n%s", want, page)
		}
	}
	// Only immediate children are carded; deeper pages live behind their
	// category technique and the contents menu.
	if strings.Contains(page, `href="/docs/dev/00-start"`) {
		t.Fatal("nested doc carded at the root")
	}
	// The dev technique's search key must include its descendants' titles.
	m := regexp.MustCompile(`<a class="doc-technique" href="/docs/dev" data-search="([^"]*)"`).FindStringSubmatch(page)
	if m == nil || !strings.Contains(m[1], "start here") {
		t.Fatalf("directory search key doesn't cover descendants: %v", m)
	}
	// index.md is the folder's description, not a technique.
	if strings.Contains(page, `href="/docs/index"`) || strings.Contains(page, `href="/docs/dev/index"`) {
		t.Fatal("index.md leaked into the techniques")
	}
	// The root is the Development section: the user guide has its own
	// account-menu entry and stays out of this grid.
	if strings.Contains(page, "user-guide") {
		t.Fatal("user guide leaked into the Development techniques")
	}

	// The guide's own folder page still renders in full.
	guide := srv.docsFolderHTML(srv.listDocs(), "user-guide")
	for _, want := range []string{"the guide explains itself", `href="/docs/user-guide/01-intro"`} {
		if !strings.Contains(guide, want) {
			t.Fatalf("user-guide folder page missing %q", want)
		}
	}

	// A subdirectory's folder page: its own index.md above its own techniques. The
	// inner directory has no index, so its technique falls back to a page count.
	sub := srv.docsFolderHTML(srv.listDocs(), "dev")
	for _, want := range []string{"what lives in dev/", `href="/docs/dev/00-start"`,
		`<a class="doc-technique" href="/docs/dev/inner"`, `<span class="doc-technique-sum">1 page</span>`} {
		if !strings.Contains(sub, want) {
			t.Fatalf("dev folder page missing %q in:\n%s", want, sub)
		}
	}
	if strings.Contains(sub, `href="/docs/alpha"`) {
		t.Fatal("dev folder page techniques documents outside dev/")
	}
	if srv.docsFolderHTML(srv.listDocs(), "no-such-dir") != "" {
		t.Fatal("nonexistent directory rendered a folder page")
	}
}

// docSummary feeds the page techniques: the first plain paragraph, unwrapped,
// stripped of inline markup, truncated on a word; leading images, lists, and
// headings never summarize.
func TestDocSummary(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	got := docSummary(write("a.md",
		"# Title\n\n![shot](images/x.png)\n\nSee the **map** and\n[the list](x.md) here.\n\nSecond paragraph."))
	if got != "See the map and the list here." {
		t.Fatalf("docSummary = %q", got)
	}
	long := strings.Repeat("word ", 60)
	if got := docSummary(write("b.md", "# T\n\n"+long)); len(got) > 165 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long summary not clipped: %q", got)
	}
	if got := docSummary(write("c.md", "# T\n\n- only\n- a list")); got != "" {
		t.Fatalf("list-only doc should have empty summary, got %q", got)
	}
}

// The left-hand contents menu shows only the current doc's section — the user
// guide's tree on guide pages, everything else on Development pages — with the
// current page highlighted and directories opened along the path to it.
func TestDocsSideNav(t *testing.T) {
	docs := []docEntry{
		{slug: "alpha", title: "Alpha Note"},
		{slug: "dev/00-start", title: "Start Here"},
		{slug: "dev/inner/deep", title: "Deep"},
		{slug: "user-guide/01-intro", title: "Welcome"},
		{slug: "user-guide/index", title: "The Guide"},
	}
	guide := docsSideNav(docs, "user-guide/01-intro")
	for _, want := range []string{
		`class="side-root" href="/docs/user-guide">Overview<`, // the root entry names the guide's overview page

		`class="side-link active" href="/docs/user-guide/01-intro"`,
	} {
		if !strings.Contains(guide, want) {
			t.Fatalf("guide side nav missing %q:\n%s", want, guide)
		}
	}
	// A group directory is labeled by its index.md title, not its slug.
	grouped := docsSideNav(append(docs,
		docEntry{slug: "user-guide/10-get-started/02-setup", title: "Set up"},
		docEntry{slug: "user-guide/10-get-started/index", title: "Get started"},
	), "user-guide/10-get-started/02-setup")
	for _, want := range []string{
		`href="/docs/user-guide/10-get-started">Get started<`,
		`class="side-link active" href="/docs/user-guide/10-get-started/02-setup"`,
	} {
		if !strings.Contains(grouped, want) {
			t.Fatalf("grouped side nav missing %q:\n%s", want, grouped)
		}
	}
	if strings.Contains(guide, "dev/00-start") || strings.Contains(guide, `href="/docs/alpha"`) {
		t.Fatal("guide side nav leaks Development docs")
	}
	if strings.Contains(guide, `href="/docs/user-guide/index"`) {
		t.Fatal("index.md listed as a leaf; the section root already leads to it")
	}

	dev := docsSideNav(docs, "dev/inner/deep")
	for _, want := range []string{
		`class="side-root" href="/docs">Documentation<`,
		`<details open><summary><a href="/docs/dev">dev<`, // ancestors of the current page open
		`class="side-link active" href="/docs/dev/inner/deep"`,
		`href="/docs/alpha"`,
	} {
		if !strings.Contains(dev, want) {
			t.Fatalf("dev side nav missing %q:\n%s", want, dev)
		}
	}
	if strings.Contains(dev, "user-guide") {
		t.Fatal("dev side nav leaks the user guide")
	}
	// On the section landing page the root link itself is the current page.
	if root := docsSideNav(docs, ""); !strings.Contains(root, `class="side-root active" href="/docs">Documentation<`) {
		t.Fatalf("documentation root not active on the landing page:\n%s", root)
	}
}

// Guide chapters end with Previous/Next links walking the guide in reading
// order, crossing group boundaries; edges get only the link that exists, and
// nothing outside the guide (or its folder indexes) joins the sequence.
func TestGuidePager(t *testing.T) {
	docs := []docEntry{
		{slug: "dev/guide", title: "Dev Doc"},
		{slug: "user-guide/10-a/01-one", title: "One"},
		{slug: "user-guide/10-a/02-two", title: "Two"},
		{slug: "user-guide/10-a/index", title: "Group A"},
		{slug: "user-guide/20-b/03-three", title: "Three"},
		{slug: "user-guide/index", title: "The Guide"},
	}
	mid := guidePager(docs, "user-guide/10-a/02-two")
	for _, want := range []string{
		`class="pager-prev" href="/docs/user-guide/10-a/01-one"`, `« One<`,
		`class="pager-next" href="/docs/user-guide/20-b/03-three"`, `>Three »<`, // Next crosses the group boundary
	} {
		if !strings.Contains(mid, want) {
			t.Fatalf("pager missing %q:\n%s", want, mid)
		}
	}
	if strings.Contains(mid, "index") || strings.Contains(mid, "dev/guide") {
		t.Fatal("pager sequence includes non-chapters")
	}
	if first := guidePager(docs, "user-guide/10-a/01-one"); strings.Contains(first, "pager-prev") ||
		!strings.Contains(first, `pager-next" href="/docs/user-guide/10-a/02-two"`) {
		t.Fatalf("first chapter should have only Next:\n%s", first)
	}
	if last := guidePager(docs, "user-guide/20-b/03-three"); strings.Contains(last, "pager-next") ||
		!strings.Contains(last, `pager-prev" href="/docs/user-guide/10-a/02-two"`) {
		t.Fatalf("last chapter should have only Previous:\n%s", last)
	}
	if got := guidePager(docs, "dev/guide"); got != "" {
		t.Fatalf("development docs should have no pager, got:\n%s", got)
	}
}

// The on-page index: h2/h3 headings gain anchor ids (deduplicated), the nav
// links to them with depth classes, and a page with fewer than two sections
// gets no index at all.
func TestDocTOC(t *testing.T) {
	body, toc := docTOC(`<h2>Alpha &amp; One</h2><p>x</p><h3>Beta <code>x</code></h3><h2>Alpha &amp; One</h2>`)
	for _, want := range []string{
		`<h2 id="alpha-one">Alpha &amp; One</h2>`,
		`<h3 id="beta-x">Beta <code>x</code></h3>`, // inline markup survives; the id is text-only
		`<h2 id="alpha-one-1">`,                    // duplicate heading, distinct anchor
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rewritten doc missing %q:\n%s", want, body)
		}
	}
	for _, want := range []string{
		`aria-label="On this page"`,
		`<li class="toc-l2"><a href="#alpha-one">Alpha &amp; One</a></li>`,
		`<li class="toc-l3"><a href="#beta-x">Beta x</a></li>`,
		`href="#alpha-one-1"`,
	} {
		if !strings.Contains(toc, want) {
			t.Fatalf("toc missing %q:\n%s", want, toc)
		}
	}
	if _, toc := docTOC(`<h2>Only Section</h2><p>x</p>`); toc != "" {
		t.Fatalf("single-section page should have no index, got:\n%s", toc)
	}
	if got := headingID("¿Qué? — 42"); got != "qué-42" {
		t.Fatalf("headingID = %q", got)
	}
}

// Doc breadcrumbs trail back to Documentation, whichever tree the page is in.
func TestDocCrumbsSections(t *testing.T) {
	got := docCrumbs("dev/guide", "The Guide", nil)
	want := []crumb{{label: "Documentation", href: "/docs"}, {label: "dev", href: "/docs/dev"}, {label: "The Guide", href: ""}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dev crumbs = %v, want %v", got, want)
	}
	got = docCrumbs("user-guide/01-intro", "Welcome", nil)
	want = []crumb{{label: "Documentation", href: "/docs/user-guide"}, {label: "Welcome", href: ""}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("guide crumbs = %v, want %v", got, want)
	}
	// The guide's landing page is its section root — a single current crumb.
	if got := docCrumbs("user-guide", "user-guide", nil); !reflect.DeepEqual(got, []crumb{{label: "Documentation", href: ""}}) {
		t.Fatalf("guide root crumbs = %v", got)
	}
	// A directory with an index title wears it in the trail; the numeric
	// prefix on the directory name orders the tree without showing.
	titles := map[string]string{"user-guide/10-get-started": "Get started"}
	got = docCrumbs("user-guide/10-get-started/01-intro", "Welcome", titles)
	want = []crumb{{label: "Documentation", href: "/docs/user-guide"},
		{label: "Get started", href: "/docs/user-guide/10-get-started"}, {label: "Welcome", href: ""}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grouped guide crumbs = %v, want %v", got, want)
	}
}

// Nested docs are servable through the wildcard route, .md-suffixed links
// resolve (docs cross-link relatively), and the directory shows in the trail.
func TestDocsNestedRoute(t *testing.T) {
	srv, ts := newServer(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev", "guide.md"),
		[]byte("# The Guide\n\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "user-guide"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "user-guide", "01-intro.md"),
		[]byte("# Chapter Title\n\nguide body"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.DocsDir = dir

	for _, url := range []string{"/docs/dev/guide", "/docs/dev/guide.md"} {
		code, html := fetchHTML(t, ts.URL+url)
		if code != 200 || !strings.Contains(html, "The Guide") {
			t.Fatalf("%s: %d", url, code)
		}
	}
	// The directory crumb is a link to the folder page.
	code, html := fetchHTML(t, ts.URL+"/docs/dev/guide")
	if !strings.Contains(html, `href="/docs/dev"`) {
		t.Fatalf("nested doc crumbs don't link to the folder: %d", code)
	}
	// The page ships the docusaurus-style panes: the layout grid and the
	// left-hand contents menu with the current page highlighted.
	if !strings.Contains(html, `class="docs-layout`) ||
		!strings.Contains(html, `class="side-link active" href="/docs/dev/guide"`) {
		t.Fatal("doc page missing the contents-menu layout")
	}
	// Development pages keep their title heading in the content...
	if !strings.Contains(html, "<h1>The Guide</h1>") {
		t.Fatal("development doc lost its h1")
	}
	// ...but guide pages don't repeat it: the breadcrumb already names them.
	code, html = fetchHTML(t, ts.URL+"/docs/user-guide/01-intro")
	if code != 200 || strings.Contains(html, "<h1>") || !strings.Contains(html, "guide body") {
		t.Fatalf("guide page should render without its h1: %d", code)
	}
	if !strings.Contains(html, `aria-current="page">Chapter Title<`) {
		t.Fatal("guide page title missing from the breadcrumb")
	}
	// The folder page itself serves over HTTP.
	code, html = fetchHTML(t, ts.URL+"/docs/dev")
	if code != 200 || !strings.Contains(html, `href="/docs/dev/guide"`) {
		t.Fatalf("folder page: %d", code)
	}
	// A link to a folder's index.md lands on the folder page — techniques and a
	// single folder crumb — not on the index as a bare document.
	for _, u := range []string{"/docs/dev/index", "/docs/dev/index.md"} {
		code, html = fetchHTML(t, ts.URL+u)
		if code != 200 || !strings.Contains(html, `class="doc-technique" href="/docs/dev/guide"`) {
			t.Fatalf("%s should render the folder page with techniques: %d", u, code)
		}
	}
	// /docs and /docs/index are the guide now: there is one documentation
	// section, so the root address is the thing it documents rather than a
	// listing whose only useful entry is the guide.
	for _, u := range []string{"/docs", "/docs/index"} {
		code, html = fetchHTML(t, ts.URL+u)
		if code != 200 || !strings.Contains(html, "Chapter Title") {
			t.Fatalf("%s should render the guide: %d", u, code)
		}
	}
}

// TestLiveResponsesSatisfyTheSchemas is the API-side of the round-trip
// discipline (docs/design/api-standard.md): real server responses must validate
// against the normative schemas.
func TestLiveResponsesSatisfyTheSchemas(t *testing.T) {
	_, ts := newServer(t)
	load := func(name string) *jsonschema.Schema {
		raw, ok := schemas.Get(name)
		if !ok {
			t.Fatalf("schema %s missing", name)
		}
		s, err := jsonschema.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	resp, body := request(t, "POST", ts.URL+"/v1/evidence", "test-key",
		`{"summary_text": "pasted csv rows from the warehouse"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("evidence: %d", resp.StatusCode)
	}
	raw, _ := json.Marshal(body)
	if errs := load("evidence-block.schema.json").ValidateBytes(raw); len(errs) != 0 {
		t.Fatalf("live evidence violates schema: %v", errs)
	}

	resp, _ = request(t, "POST", ts.URL+"/v1/feedback", "test-key",
		`{"technique_id":"ask-for-a-diagram","stage":"shown"}`)
	if resp.StatusCode != 202 {
		t.Fatalf("feedback: %d", resp.StatusCode)
	}
	resp, page := request(t, "GET", ts.URL+"/v1/events", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("events: %d", resp.StatusCode)
	}
	events, _ := page["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events page: %v", page)
	}
	eraw, _ := json.Marshal(events[0])
	if errs := load("feedback-event.schema.json").ValidateBytes(eraw); len(errs) != 0 {
		t.Fatalf("live event violates schema: %v\n%s", errs, eraw)
	}

	// the specs themselves are served
	specResp, err := http.Get(ts.URL + "/v1/openapi.yaml")
	if err != nil || specResp.StatusCode != 200 {
		t.Fatalf("openapi serving: %v %v", specResp, err)
	}
	specResp.Body.Close()
	schemaResp, err := http.Get(ts.URL + "/v1/schemas/technique.schema.json")
	if err != nil || schemaResp.StatusCode != 200 {
		t.Fatalf("schema serving: %v %v", schemaResp, err)
	}
	schemaResp.Body.Close()
}

func TestFederationPublishSurface(t *testing.T) {
	_, ts := newServer(t)
	// nothing published yet: descriptor serves with zero channels
	// The root key authorizes throughout: what is under test is the federation
	// document shape, and a non-public channel needs authorization now
	// (feedtokens.go).
	resp, desc := request(t, "GET", ts.URL+"/.well-known/tacit.json", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("descriptor: %d", resp.StatusCode)
	}
	if chs, _ := desc["channels"].([]any); len(chs) != 0 {
		t.Fatalf("unexpected channels: %v", desc)
	}

	// publish a technique via the admin endpoint
	resp, body := request(t, "POST", ts.URL+"/v1/admin/publish", "test-key",
		`{"technique_id":"ask-for-a-diagram","channels":["general"]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("publish: %d %v", resp.StatusCode, body)
	}
	resp, _ = request(t, "POST", ts.URL+"/v1/admin/publish", "", `{}`)
	if resp.StatusCode != 401 {
		t.Fatalf("publish without key: %d", resp.StatusCode)
	}

	// descriptor now lists the channel; feed serves and verifies
	_, desc = request(t, "GET", ts.URL+"/.well-known/tacit.json", "test-key", "")
	chs, _ := desc["channels"].([]any)
	if len(chs) != 1 {
		t.Fatalf("channels after publish: %v", desc)
	}
	resp, feedDoc := request(t, "GET", ts.URL+"/f/general/feed.json", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("feed: %d", resp.StatusCode)
	}
	entries, _ := feedDoc["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("feed entries: %v", feedDoc)
	}
	entry := entries[0].(map[string]any)
	if entry["kind"] != "technique" || entry["signature"] == nil {
		t.Fatalf("entry: %v", entry)
	}
	if _, hasEmbedding := entry["technique"].(map[string]any)["embedding"]; hasEmbedding {
		t.Fatal("embedding leaked through the feed route")
	}

	// canonical technique serves as markdown; unpublished 404s. Authorized, because
	// the technique path is gated by its technique's channels now.
	mdReq, _ := http.NewRequest("GET", ts.URL+"/f/techniques/ask-for-a-diagram.md", nil)
	mdReq.Header.Set("X-Tacit-Key", "test-key")
	mdResp, err := http.DefaultClient.Do(mdReq)
	if err != nil || mdResp.StatusCode != 200 {
		t.Fatalf("canonical technique: %v %v", mdResp, err)
	}
	mdResp.Body.Close()
	otherReq, _ := http.NewRequest("GET", ts.URL+"/f/techniques/use-internal-data-connector.md", nil)
	otherReq.Header.Set("X-Tacit-Key", "test-key")
	otherResp, _ := http.DefaultClient.Do(otherReq)
	if otherResp.StatusCode != 404 {
		t.Fatalf("unpublished technique served: %d", otherResp.StatusCode)
	}
	otherResp.Body.Close()

	// unknown channel 404s
	resp, _ = request(t, "GET", ts.URL+"/f/nope/feed.json", "", "")
	if resp.StatusCode != 404 {
		t.Fatalf("unknown channel: %d", resp.StatusCode)
	}
}

func TestFederationSubscriptionAdminAndPage(t *testing.T) {
	_, ts := newServer(t)
	resp, body := request(t, "POST", ts.URL+"/v1/admin/subscriptions", "test-key",
		`{"feed_url": "https://peer.example.org/f/general/feed.json", "trust": "review"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("subscribe: %d %v", resp.StatusCode, body)
	}
	if body["prefix"] != "ext/peer-example-org" {
		t.Fatalf("prefix: %v", body)
	}
	resp, list := request(t, "GET", ts.URL+"/v1/admin/subscriptions", "test-key", "")
	subs, _ := list["subscriptions"].([]any)
	if resp.StatusCode != 200 || len(subs) != 1 {
		t.Fatalf("list: %d %v", resp.StatusCode, list)
	}

	code, html := fetchHTML(t, ts.URL+"/federation")
	// The page is organised by DIRECTION now: "Publishing" was one of six sibling
	// headings, and what leaves has a plate of its own.
	if code != 200 || !strings.Contains(html, "peer.example.org") ||
		!strings.Contains(html, "What leaves this registry") ||
		!strings.Contains(html, "What arrives from others") {
		t.Fatalf("federation page: %d", code)
	}

	id, _ := body["id"].(string)
	req, _ := http.NewRequest("DELETE", ts.URL+"/v1/admin/subscriptions/"+id, nil)
	req.Header.Set("X-Tacit-Key", "test-key")
	delResp, err := http.DefaultClient.Do(req)
	if err != nil || delResp.StatusCode != 200 {
		t.Fatalf("delete: %v %v", delResp, err)
	}
	delResp.Body.Close()

	resp, _ = request(t, "POST", ts.URL+"/v1/admin/poll-feeds", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("poll-feeds: %d", resp.StatusCode)
	}
}

// TestSlashedTechniqueIDsResolveEverywhere is the regression for federated/mined
// imports whose local ids contain slashes (mined/<fingerprint>): every
// dashboard surface must link and resolve them.
func TestSlashedTechniqueIDsResolveEverywhere(t *testing.T) {
	srv, ts := newServer(t)
	// a draft with a slashed id, as a federation import would create
	resp, body := request(t, "POST", ts.URL+"/v1/contribute", "test-key",
		`{"name": "Mined move", "recipe": "do the thing", "description": "d"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("contribute: %d %v", resp.StatusCode, body)
	}
	technique, ok, _ := srv.Store.GetTechnique(body["id"].(string))
	if !ok {
		t.Fatal("draft missing")
	}
	technique.ID = "mined/deploy-checklist-move"
	technique.Provenance = "federated"
	if err := srv.Store.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}

	code, html := fetchHTML(t, ts.URL+"/review")
	if code != 200 || !strings.Contains(html, `data-href="/drafts/mined/deploy-checklist-move"`) {
		t.Fatalf("drafts list link: %d", code)
	}
	code, html = fetchHTML(t, ts.URL+"/drafts/mined/deploy-checklist-move")
	if code != 200 || !strings.Contains(html, "Mined move") {
		t.Fatalf("draft detail: %d", code)
	}
	if !strings.Contains(html, `action="/admin/techniques/promote/mined/deploy-checklist-move"`) {
		t.Fatal("promote form URL wrong")
	}
	code, _ = fetchHTML(t, ts.URL+"/outcomes/mined/deploy-checklist-move")
	if code != 200 {
		t.Fatalf("insights drill-down: %d", code)
	}
	code, _ = fetchHTML(t, ts.URL+"/techniques/mined/deploy-checklist-move")
	if code != 200 {
		t.Fatalf("technique detail: %d", code)
	}

	// the browser reject button works on the slashed id
	resp2, err := http.DefaultClient.PostForm(ts.URL+"/admin/techniques/reject/mined/deploy-checklist-move", nil)
	if err != nil || resp2.StatusCode != 200 { // redirect followed to /drafts
		t.Fatalf("reject: %v %v", resp2, err)
	}
	resp2.Body.Close()
	got, _, _ := srv.Store.GetTechnique("mined/deploy-checklist-move")
	if got.Status != "retired" {
		t.Fatalf("reject did not apply: %s", got.Status)
	}

	// federation canonical URL with a slashed id. What is under test here is id
	// ROUTING, so the request carries authorization: a non-public channel needs a
	// feed token now (feedtokens.go), and an anonymous 404 would look like a
	// routing failure while actually being the gate working.
	technique.Channels = []string{"general"}
	technique.Status = "stable"
	_ = srv.Store.UpsertTechnique(technique)
	mdReq, _ := http.NewRequest("GET", ts.URL+"/f/techniques/mined/deploy-checklist-move.md", nil)
	mdReq.Header.Set("X-Tacit-Key", srv.Cfg.APIKey)
	mdResp, err := http.DefaultClient.Do(mdReq)
	if err != nil || mdResp.StatusCode != 200 {
		t.Fatalf("canonical slashed technique: %v %v", mdResp, err)
	}
	mdResp.Body.Close()

	// the API detail route too
	resp, _ = request(t, "GET", ts.URL+"/v1/techniques/mined/deploy-checklist-move", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("api technique: %d", resp.StatusCode)
	}
}

func TestTechniquesPageExplainsTheDraftSplit(t *testing.T) {
	srv, ts := newServer(t)
	resp, body := request(t, "POST", ts.URL+"/v1/contribute", "test-key",
		`{"name": "Pending move", "recipe": "r", "description": "d"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("contribute: %d %v", resp.StatusCode, body)
	}
	code, html := fetchHTML(t, ts.URL+"/techniques")
	if code != 200 {
		t.Fatalf("techniques: %d", code)
	}
	// The count is the starter set, whatever the starter set currently is:
	// hardcoding it broke this test the day a technique was added to
	// techniques/, which is a change the page is supposed to survive.
	live, err := srv.Store.ListTechniques([]string{"stable"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	reviewed := len(live)
	if want := fmt.Sprintf("%d reviewed techniques", reviewed); !strings.Contains(html, want) {
		t.Fatalf("reviewed count missing: want %q", want)
	}
	// The draft count is stated once, on the Review nav badge — not restated
	// in the /techniques sub-line as well.
	if !strings.Contains(html, `nav-badge review`) {
		t.Fatal("draft count missing from the Review nav badge")
	}
	if strings.Contains(html, "draft awaiting review</a>") {
		t.Fatal("/techniques sub-line still restates the draft count")
	}
	if strings.Contains(html, "Pending move") {
		t.Fatal("draft leaked into the techniques list")
	}
}

func TestReviewerEditAffordance(t *testing.T) {
	srv, ts := newServer(t)
	resp, body := request(t, "POST", ts.URL+"/v1/contribute", "test-key",
		`{"name": "Rough draft", "recipe": "raw payload", "description": "d"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("contribute: %d", resp.StatusCode)
	}
	id := body["id"].(string)
	before, _, _ := srv.Store.GetTechnique(id)

	// the draft page carries the edit form
	code, html := fetchHTML(t, ts.URL+"/drafts/"+id)
	if code != 200 || !strings.Contains(html, `action="/admin/techniques/edit/`+id+`"`) {
		t.Fatalf("edit form missing: %d", code)
	}

	// API partial edit: only the provided fields change, and it re-embeds
	resp, out := request(t, "POST", ts.URL+"/v1/admin/techniques/"+id, "test-key",
		`{"applies_when": "setting up a new repository", "not_when": "daily work", "tags": ["setup"]}`)
	if resp.StatusCode != 200 || out["changed"] != true {
		t.Fatalf("edit: %d %v", resp.StatusCode, out)
	}
	after, _, _ := srv.Store.GetTechnique(id)
	if after.AppliesWhen != "setting up a new repository" || after.NotWhen != "daily work" ||
		len(after.Tags) != 1 || after.Tags[0] != "setup" {
		t.Fatalf("edit not applied: %+v", after)
	}
	if after.Name != "Rough draft" || after.Recipe != "raw payload" {
		t.Fatalf("unprovided fields changed: %+v", after)
	}
	if len(after.Embedding) == 0 || vecEqual(before.Embedding, after.Embedding) {
		t.Fatal("edit did not re-embed")
	}

	// browser form edit (drafts only)
	form := url.Values{"name": {"Refined draft"}, "applies_when": {"first-time setup"}}
	postResp, err := http.DefaultClient.PostForm(ts.URL+"/admin/techniques/edit/"+id, form)
	if err != nil || postResp.StatusCode != 200 { // redirect followed
		t.Fatalf("form edit: %v %v", postResp, err)
	}
	postResp.Body.Close()
	after, _, _ = srv.Store.GetTechnique(id)
	if after.Name != "Refined draft" || after.AppliesWhen != "first-time setup" {
		t.Fatalf("form edit not applied: %+v", after)
	}

	// stable techniques are not editable through the browser form...
	_, _, _ = contributePromote(srv, id)
	form = url.Values{"name": {"Vandalized"}}
	postResp, _ = http.DefaultClient.PostForm(ts.URL+"/admin/techniques/edit/"+id, form)
	postResp.Body.Close()
	after, _, _ = srv.Store.GetTechnique(id)
	if after.Name == "Vandalized" {
		t.Fatal("browser edit touched a non-draft technique")
	}
	// ...but the key-authed API can refine any technique
	resp, _ = request(t, "POST", ts.URL+"/v1/admin/techniques/"+id, "test-key", `{"name": "Post-promotion refinement"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("api edit of stable technique: %d", resp.StatusCode)
	}
}

func vecEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contributePromote(srv *Server, id string) (any, any, error) {
	technique, _, _ := srv.Store.GetTechnique(id)
	technique.Status = "stable"
	return nil, nil, srv.Store.UpsertTechnique(technique)
}

// TestBrowserPublishUnpublish drives the technique page's Federation disclosure:
// publish to channels via form POST, see it on the federation page, then
// unpublish and confirm the technique left the feed.
func TestBrowserPublishUnpublish(t *testing.T) {
	_, ts := newServer(t)
	_, techniqueHTML := fetchHTML(t, ts.URL+"/techniques/ask-for-a-diagram")
	if !strings.Contains(techniqueHTML, `<details class="fineprint technique-federation">`) ||
		!strings.Contains(techniqueHTML, `<span class="technique-federation-state">Not published</span>`) ||
		strings.Contains(techniqueHTML, `<section class="panel technique-support-panel"><h2>Federation</h2>`) {
		t.Fatal("technique federation control is not folded into the definition panel")
	}
	form := func(path, body string) *http.Response {
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	resp := form("/admin/techniques/channels/ask-for-a-diagram", "do=publish&channels=general%2C+partners")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("publish: %d", resp.StatusCode)
	}
	r2, body := request(t, "GET", ts.URL+"/v1/techniques/ask-for-a-diagram", "test-key", "")
	chs, _ := body["channels"].([]any)
	if r2.StatusCode != 200 || len(chs) != 2 {
		t.Fatalf("channels after publish: %v", body["channels"])
	}

	// invalid channel name -> 400, nothing changes
	if resp := form("/admin/techniques/channels/ask-for-a-diagram", "do=publish&channels=Not+Valid"); resp.StatusCode != 400 {
		t.Fatalf("invalid channel accepted: %d", resp.StatusCode)
	}

	if resp := form("/admin/techniques/channels/ask-for-a-diagram", "next=%2Ffederation"); resp.StatusCode != http.StatusFound {
		t.Fatalf("unpublish: %d", resp.StatusCode)
	} else if loc := resp.Header.Get("Location"); loc != "/federation" {
		t.Fatalf("next redirect: %q", loc)
	}
	_, body = request(t, "GET", ts.URL+"/v1/techniques/ask-for-a-diagram", "test-key", "")
	if chs, _ := body["channels"].([]any); len(chs) != 0 {
		t.Fatalf("channels after unpublish: %v", body["channels"])
	}
}

type fakeResearcher struct{ drafts []suggest.Draft }

func (f fakeResearcher) Research(prompt string) ([]suggest.Draft, error) {
	return f.drafts, nil
}

// TestSuggestEndpointAndButton drives the discovery surface: the key-authed
// run creates suggested drafts, the usage profile is served, and the Drafts
// page renders the "Suggest candidate techniques" button.
func TestSuggestEndpointAndButton(t *testing.T) {
	srv, ts := newServer(t)
	srv.Suggest = fakeResearcher{drafts: []suggest.Draft{{
		Name: "Pin tool versions in agent instructions", Description: "d",
		Recipe: "state exact versions", AppliesWhen: "a", NotWhen: "n",
		Tags: []string{"setup"}, SourceURL: "https://example.com/pin"}}}

	resp, body := request(t, "POST", ts.URL+"/v1/admin/suggest", "test-key", `{"count": 5}`)
	created, _ := body["created"].([]any)
	if resp.StatusCode != 200 || len(created) != 1 {
		t.Fatalf("suggest: %d %v", resp.StatusCode, body)
	}
	_, technique := request(t, "GET", ts.URL+"/v1/techniques/"+created[0].(string), "test-key", "")
	if technique["provenance"] != "suggested" || technique["status"] != "draft" {
		t.Fatalf("suggested technique shape: %v", technique)
	}

	resp, body = request(t, "GET", ts.URL+"/v1/admin/usage-profile", "test-key", "")
	if resp.StatusCode != 200 || body["window_days"] != float64(90) {
		t.Fatalf("usage profile: %d %v", resp.StatusCode, body)
	}

	code, page := fetchHTML(t, ts.URL+"/review")
	if code != 200 || !strings.Contains(page, `action="/admin/suggest"`) ||
		!strings.Contains(page, "Suggest candidate techniques") {
		t.Fatalf("drafts page missing suggest button: %d", code)
	}
}

// Docs cross-link relatively (valid on GitHub); the server rewrites those to
// absolute /docs/ paths so resolution never depends on whether the current
// URL carries a trailing slash (folder pages don't).
func TestAbsDocLinks(t *testing.T) {
	in := `<a href="design.md">x</a> <a href="../dev/09-testing.md#sec">y</a>` +
		` <a href="https://example.com/a.md">ext</a> <a href="/docs/learning">abs</a>`
	got := absDocLinks(in, "design")
	for _, want := range []string{
		`href="/docs/design/design"`,
		`href="/docs/dev/09-testing#sec"`,
		`href="https://example.com/a.md"`, // external untouched
		`href="/docs/learning"`,           // already absolute untouched
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("absDocLinks missing %q in %q", want, got)
		}
	}
	if got := absDocLinks(`<a href="concepts.md">c</a>`, ""); !strings.Contains(got, `href="/docs/concepts"`) {
		t.Fatalf("root-dir resolution wrong: %q", got)
	}
}

// Health advertises the operator's external URL so member-side tools can
// print dashboard links a browser anywhere can open — while absent config
// stays absent (no empty-string key for clients to trip on).
func TestHealthAdvertisesExternalURL(t *testing.T) {
	srv, ts := newServer(t)
	resp, body := request(t, "GET", ts.URL+"/v1/health", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("health = %d", resp.StatusCode)
	}
	if _, present := body["external_url"]; present {
		t.Fatal("external_url present with no ExternalURL configured")
	}
	srv.Cfg.ExternalURL = "https://tacit.example.com/"
	_, body = request(t, "GET", ts.URL+"/v1/health", "", "")
	if body["external_url"] != "https://tacit.example.com" {
		t.Fatalf("external_url = %v, want trailing slash trimmed", body["external_url"])
	}
}

// Docs left the top bar for the avatar menu — the nav must not regrow it,
// and /docs itself must keep serving.
func TestDocsOutOfTopBar(t *testing.T) {
	_, ts := newServer(t)
	code, html := fetchHTML(t, ts.URL+"/outcomes")
	if code != 200 {
		t.Fatalf("insights: %d", code)
	}
	if strings.Contains(html, `<a href="/docs" class="">Docs</a>`) || strings.Contains(html, `>Docs</a>`) {
		t.Fatal("Docs still renders in the top navigation")
	}
	// One Documentation entry in the menu; the guide and Development are the two
	// halves of its breadcrumb switcher.
	if !strings.Contains(html, `href="/docs/user-guide">Documentation<`) {
		t.Fatal("Documentation missing from the account menu")
	}
	if strings.Contains(html, `role="menuitem" href="/docs">`) {
		t.Fatal("Development is still a menu entry of its own")
	}
	if code, _ := fetchHTML(t, ts.URL+"/docs"); code != 200 {
		t.Fatalf("/docs = %d", code)
	}
	if code, _ := fetchHTML(t, ts.URL+"/docs/user-guide"); code != 200 {
		t.Fatalf("/docs/user-guide = %d", code)
	}
}

// The default /techniques view is the LIVE set — its headline count must match the
// Outcomes tile that links to it. Retired techniques live at /techniques/retired, a view
// of their own beside Tags in the section nav, one click away, never silently
// gone. It used to be ?status=retired — a query flag, so one nav item filtered
// the page you were on while its neighbour navigated to a different one.
func TestTechniquesDefaultViewIsLiveSet(t *testing.T) {
	srv, ts := newServer(t)
	if err := srv.Store.UpsertTechnique(models.Technique{ID: "retired-move", Name: "Old retired move", Status: "retired"}); err != nil {
		t.Fatal(err)
	}
	_, body := fetchHTML(t, ts.URL+"/techniques")
	if strings.Contains(body, "Old retired move") {
		t.Fatal("retired technique in the default live list")
	}
	// The archive is a sibling VIEW of the library: its own route, in the section
	// nav, behaving exactly as Tags does.
	if !strings.Contains(body, `<a href="/techniques/retired">Retired<span>1</span></a>`) {
		t.Fatal("no route to the retired archive in the section nav")
	}
	_, archive := fetchHTML(t, ts.URL+"/techniques/retired")
	if !strings.Contains(archive, "Old retired move") || !strings.Contains(archive, "1 reviewed technique") {
		t.Fatal("archive view missing the retired technique")
	}
	if strings.Contains(archive, "Old stable move") {
		t.Fatal("live techniques leaked into the retired archive")
	}
	if !strings.Contains(archive, `<summary aria-current="page">Retired<svg`) {
		t.Fatal("the archive does not name itself in the breadcrumb")
	}
	// The retired query flag is gone with the legacy redirects: the archive is
	// only its own route, and the flag no longer filters the live list.
	_, flagged := fetchHTML(t, ts.URL+"/techniques?status=retired")
	if strings.Contains(flagged, "Old retired move") {
		t.Fatal("?status=retired should no longer filter the live list")
	}
	if !strings.Contains(archive, `action="/admin/techniques/restore/retired-move"`) {
		t.Fatal("archive rows missing the Restore action")
	}
}

// The All view groups into areas of practice by default: the same communities the
// Map finds, with each area's adoption rolled up, and techniques in no community
// kept in an Ungrouped band rather than dropped. ?group=none opts out into a plain
// flat list (TestTechniquesGroupingNoneIsFlat).
func TestTechniquesGroupByArea(t *testing.T) {
	srv, ts := newServer(t)
	// Start from a known set so the areas are exactly what we seed.
	all, _ := srv.Store.ListTechniques(nil, 0)
	for _, c := range all {
		_, _ = srv.Store.DeleteTechnique(c.ID)
	}
	for _, c := range []models.Technique{
		{ID: "ga-1", Name: "GA One", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "ga-2", Name: "GA Two", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "ga-3", Name: "GA Three", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "ga-solo", Name: "GA Solo", Status: "stable", Tags: []string{"solo"}},
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}

	_, area := fetchHTML(t, ts.URL+"/techniques")
	// Grouping is the segmented control in the filter bar, not the old toggle.
	if strings.Contains(area, `class="seg-toggle"`) {
		t.Fatal("the old arrangement toggle should be gone — grouping rides the filter bar")
	}
	// The three tag-sharing techniques roll up into one area band.
	if !strings.Contains(area, `class="techniques-group`) || !strings.Contains(area, `3 technique`) {
		t.Fatal("the three shared-tag techniques did not group into one area")
	}
	// The isolated technique is kept in an Ungrouped band, not dropped.
	if !strings.Contains(area, `techniques-group-name">Ungrouped`) {
		t.Fatal("the isolated technique has no Ungrouped home")
	}
	// Every technique still appears once.
	for _, id := range []string{"ga-1", "ga-2", "ga-3", "ga-solo"} {
		if !strings.Contains(area, `data-href="/techniques/`+id+`"`) {
			t.Fatalf("technique %s missing from the grouped view", id)
		}
	}
}

// A grouping that finds nothing to group by says so. While one team does all the
// adopting, every technique's dominant cohort is the same value, so no dimension
// separates them — and the list used to render that as a lone "Ungrouped" heading
// over the whole library, which reads as a clustering failure rather than as
// "there is only one cohort here".
func TestTechniquesCohortGroupingSaysWhenItCannotGroup(t *testing.T) {
	srv, ts := newServer(t)
	all, _ := srv.Store.ListTechniques(nil, 0)
	for _, c := range all {
		_, _ = srv.Store.DeleteTechnique(c.ID)
	}
	now := time.Now().UTC()
	for _, id := range []string{"sc-1", "sc-2", "sc-3"} {
		if err := srv.Store.UpsertTechnique(models.Technique{ID: id, Name: id, Status: "stable",
			Tags: []string{"review", "audit"}, CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
		// Every adoption from the one team, exactly the shape of a young registry.
		for i := 0; i < 3; i++ {
			e := models.FeedbackEvent{EventID: fmt.Sprintf("%s-%d", id, i), TechniqueID: id, Stage: "adopted",
				Segment: models.Segment{"team": "tacit", "role": "maintainer"}, Confidence: "explicit",
				CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}
			if _, err := srv.Store.InsertEvent(e); err != nil {
				t.Fatal(err)
			}
		}
	}

	_, coh := fetchHTML(t, ts.URL+"/techniques?group=cohort")
	if strings.Contains(coh, `techniques-group-name">Ungrouped`) {
		t.Fatal("a single-cohort registry should not render one Ungrouped band over the whole library")
	}
	if !strings.Contains(coh, "Cohort groups require adoption by another team or role") {
		t.Fatal("cohort grouping with nothing to group by must explain itself")
	}
	// The reader still gets the techniques — the note replaces the band, not the list.
	for _, id := range []string{"sc-1", "sc-2", "sc-3"} {
		if n := strings.Count(coh, `data-href="/techniques/`+id+`"`); n != 1 {
			t.Fatalf("technique %s appears %d times under cohort grouping, want 1", id, n)
		}
	}
	if strings.Contains(coh, `class="techniques-member`) {
		t.Fatal("rows should not be hidden as collapsed members when there are no bands")
	}
	// Tag grouping still bands these three (they share tags), and says nothing.
	_, tags := fetchHTML(t, ts.URL+"/techniques?group=tags")
	if !strings.Contains(tags, `class="techniques-group`) {
		t.Fatal("tag grouping should still band the shared-tag techniques")
	}
	if strings.Contains(tags, "Not enough cohorts") {
		t.Fatal("the cohort note leaked into tag grouping")
	}
}

// Grouping "None" is the third choice, and it leads the control: no area bands,
// no cohort bands, no collapsed rows — every technique in one plain list, which is
// what the view's own name promises.
func TestTechniquesGroupingNoneIsFlat(t *testing.T) {
	srv, ts := newServer(t)
	all, _ := srv.Store.ListTechniques(nil, 0)
	for _, c := range all {
		_, _ = srv.Store.DeleteTechnique(c.ID)
	}
	for _, c := range []models.Technique{
		{ID: "fl-1", Name: "Flat One", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "fl-2", Name: "Flat Two", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "fl-3", Name: "Flat Three", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "fl-solo", Name: "Flat Solo", Status: "stable", Tags: []string{"solo"}},
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}

	_, flat := fetchHTML(t, ts.URL+"/techniques?group=none")
	if strings.Contains(flat, `class="techniques-group`) {
		t.Fatal("?group=none still banded the rows into areas")
	}
	if strings.Contains(flat, `class="techniques-member`) {
		t.Fatal("?group=none still marked rows as collapsible area members")
	}
	if strings.Contains(flat, `tr.techniques-group`) {
		t.Fatal("?group=none should not ship the collapse script")
	}
	// The control reports the choice, labels it None, and offers the other two.
	if !strings.Contains(flat, `data-group="none"`) {
		t.Fatal("grouping control not set to none")
	}
	if !strings.Contains(flat, ">None</a>") {
		t.Fatal("the ungrouped choice should be labelled None")
	}
	for _, href := range []string{"group=tags", "group=cohort"} {
		if !strings.Contains(flat, href) {
			t.Fatalf("grouping control missing the %s choice", href)
		}
	}
	// Every technique is present, exactly once.
	for _, id := range []string{"fl-1", "fl-2", "fl-3", "fl-solo"} {
		if n := strings.Count(flat, `data-href="/techniques/`+id+`"`); n != 1 {
			t.Fatalf("technique %s appears %d times in the flat list, want 1", id, n)
		}
	}
	// The choice rides the view-switcher, so a trip to the Map and back keeps it.
	if !strings.Contains(flat, `href="/techniques/map?group=none"`) {
		t.Fatal("view-switcher must carry group=none to the Map")
	}
	// The Map cannot arrange by "none"; it falls back to tags rather than breaking.
	_, mp := fetchHTML(t, ts.URL+"/techniques/map?group=none")
	if !strings.Contains(mp, `data-mode="tags" class="active"`) {
		t.Fatal("map should fall back to the tags arrangement under group=none")
	}
	if !strings.Contains(mp, `href="/techniques?group=none"`) {
		t.Fatal("map's view-switcher must hand group=none back to the All view")
	}
}

// Restore mirrors a draft's Promote: one button, pinned server-side to the
// retired state — a drive-by POST can't flip a stable technique, and a restored
// technique serves again immediately.
func TestRestoreFromRetirement(t *testing.T) {
	srv, ts := newServer(t)
	if err := srv.Store.UpsertTechnique(models.Technique{ID: "resurrect-me", Name: "Resurrect move", Status: "retired"}); err != nil {
		t.Fatal(err)
	}
	// Restore on a STABLE technique is a no-op (the guard, not the happy path).
	resp, err := http.PostForm(ts.URL+"/admin/techniques/restore/ask-for-a-diagram", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if c, _, _ := srv.Store.GetTechnique("ask-for-a-diagram"); c.Status != "stable" {
		t.Fatalf("guard failed: stable technique became %q", c.Status)
	}
	// The real restore.
	resp, err = http.PostForm(ts.URL+"/admin/techniques/restore/resurrect-me", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/techniques" {
		t.Fatalf("restore should land on the live list, got %s", resp.Request.URL.Path)
	}
	if c, _, _ := srv.Store.GetTechnique("resurrect-me"); c.Status != "stable" {
		t.Fatalf("restore left status %q", c.Status)
	}
	_, body := fetchHTML(t, ts.URL+"/techniques")
	if !strings.Contains(body, "Resurrect move") {
		t.Fatal("restored technique missing from the live list")
	}
}

// The legacy redirects (pre-merge /insights and /organization trees, /drafts,
// the decay and retrieval worklists, /invite and /connect) are retired: the
// old URLs are genuinely gone, not silently forwarded.
func TestLegacyURLsAreGone(t *testing.T) {
	_, ts := newServer(t)
	// (/drafts itself is absent here: the /drafts/{id} detail route keeps the
	// path prefix alive, so the mux answers the bare path with its own
	// trailing-slash redirect rather than a 404.)
	for _, old := range []string{
		"/insights",
		"/insights/tag/diagram?w=7d",
		"/organization",
		"/organization/insights/helped-rate",
		"/outcomes/decay",
		"/outcomes/retrieval-misses",
		"/invite",
		"/connect",
	} {
		resp, err := noRedirect().Get(ts.URL + old)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s -> %d, want 404 (legacy redirects removed)", old, resp.StatusCode)
		}
	}
}

// The organization app endpoint: key-gated, text-first, cohort framing
// intact — the machine counterpart of the Organization view for tacit_org.
func TestOrganizationAppEndpoint(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	if err := srv.Store.UpsertTechnique(models.Technique{ID: "oa-move", Name: "OA move", Status: "stable", TaskTypes: []string{"oa-area"}}); err != nil {
		t.Fatal(err)
	}
	for i, stage := range []string{"shown", "adopted", "helped"} {
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: fmt.Sprintf("oa-%d", i), TechniqueID: "oa-move", Stage: stage,
			Segment: models.Segment{"team": "oa-team"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	if resp, _ := request(t, "GET", ts.URL+"/v1/organization/app?w=30d", "", ""); resp.StatusCode != 401 {
		t.Fatalf("unkeyed organization app = %d, want 401", resp.StatusCode)
	}
	resp, body := request(t, "GET", ts.URL+"/v1/organization/app?w=30d", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("organization app = %d", resp.StatusCode)
	}
	text, _ := body["text"].(string)
	for _, want := range []string{"grouped by team", "oa-team", "no individual data", "tacit_insights"} {
		if !strings.Contains(text, want) {
			t.Fatalf("organization text missing %q:\n%s", want, text)
		}
	}
	summary, _ := body["summary"].(map[string]any)
	if summary["lens"] != "team" || summary["active_breadth"] != float64(1) {
		t.Fatalf("summary: %v", summary)
	}
	if html, _ := body["html"].(string); html != "" {
		t.Fatal("org app is text-first; html should be empty")
	}
}

// A Go format-string bug renders as "%!(EXTRA …)" or "%!s(MISSING)" INSIDE the
// page and swallows an argument — in the case that prompted this test, the
// entire <tbody> of the techniques table, which then spilled out of the table
// as raw text. Every existing assertion still passed, because they all looked
// for things that were still there.
//
// Nothing else in the suite would notice, so this does: no page may contain the
// marker Go prints when the verbs and the arguments disagree.
func TestNoPageRendersAFormatError(t *testing.T) {
	srv, ts := newServer(t)
	// Content on every surface: a live technique, an org-scoped one, a draft, a
	// retired one, tags, and events — a bare registry renders too few branches
	// for this to be worth much.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, c := range []models.Technique{
		{ID: "fmt-live", Name: "Live", Status: "stable", Tags: []string{"audit", "setup"}, CreatedAt: now},
		{ID: "fmt-org", Name: "Org", Status: "stable", Scope: "org", Tags: []string{"audit"}, CreatedAt: now},
		{ID: "fmt-draft", Name: "Draft", Status: "draft", Tags: []string{"audit"}, CreatedAt: now},
		{ID: "fmt-retired", Name: "Retired", Status: "retired", Tags: []string{"audit"}, CreatedAt: now},
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}
	for i, stage := range []string{"shown", "adopted", "helped"} {
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{
			EventID: fmt.Sprintf("fmt-%d", i), TechniqueID: "fmt-live", Stage: stage,
			Segment: models.Segment{"team": "fmt"}, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	for _, path := range []string{
		"/", "/outcomes", "/outcomes?w=all", "/techniques", "/techniques?tag=audit", "/techniques?scope=org",
		"/techniques/retired", "/techniques/tags", "/techniques/fmt-live", "/review", "/drafts/fmt-draft",
		"/outcomes/cohorts", "/outcomes/helped-rate", "/outcomes/signal-trust",
		"/outcomes/tag/audit", "/outcomes/fmt-live", "/learning", "/federation", "/members", "/docs",
	} {
		code, body := fetchHTML(t, ts.URL+path)
		if code != 200 {
			t.Fatalf("%s -> %d", path, code)
		}
		if i := strings.Index(body, "%!"); i >= 0 {
			end := min(i+90, len(body))
			t.Fatalf("%s renders a Go format error — the verbs and arguments disagree, "+
				"and an argument has been swallowed: %q", path, body[i:end])
		}
	}
}

// A fragment jump scrolls its target flush to the top of the viewport — and the
// top bar is sticky, so the target lands underneath it and the reader arrives
// just past the thing they clicked to see. Every in-page anchor on the dashboard
// had this: Group by (#cohorts), the Review queue's counts (#drafts, #decay,
// #adoption), the Active cohorts tile.
//
// scroll-margin-top holds the target clear of the bar. The symptom is invisible
// to every other test: the markup is perfectly correct, and the page is simply
// scrolled to the wrong place.
//
// It is checked in hack/browsercheck, which measures the bar and the margin and
// compares them. Matching "[id]{scroll-margin-top:" in the stylesheet's text
// proved a rule existed, never that it cleared anything — a bar that grew past
// the margin would have passed.

// An anchor that points at an id no page renders is a link that does nothing.
// Nothing else would notice: the markup is valid and the page still loads.
func TestNoAnchorPointsAtNothing(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, c := range []models.Technique{
		{ID: "anc-live", Name: "Live", Status: "stable", Tags: []string{"audit"}, CreatedAt: now},
		{ID: "anc-draft", Name: "Draft", Status: "draft", Tags: []string{"audit"}, CreatedAt: now},
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}
	for i, stage := range []string{"shown", "adopted", "helped"} {
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{
			EventID: fmt.Sprintf("anc-%d", i), TechniqueID: "anc-live", Stage: stage,
			Segment: models.Segment{"team": "anc"}, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	// Same-page anchors only. A cross-page one (/review?w=30d#drafts from the
	// Outcomes health tile) resolves on ITS page, which the loop below reaches
	// in turn — so the link's PATH decides, not the presence of a query. An
	// earlier form of this matcher keyed off "?" and so misread any windowed
	// cross-page anchor as a same-page one.
	anchored := regexp.MustCompile(`href="([^"#]*)#([a-z-]+)"`)
	for _, path := range []string{"/outcomes", "/review", "/techniques"} {
		_, body := fetchHTML(t, ts.URL+path)
		for _, m := range anchored.FindAllStringSubmatch(body, -1) {
			id := m[2]
			if target, _, _ := strings.Cut(m[1], "?"); target != "" && target != path {
				continue // another page's anchor; that page gets its own turn
			}
			if id == "" {
				continue
			}
			if !strings.Contains(body, `id="`+id+`"`) {
				t.Fatalf("%s links to #%s, which nothing on the page has an id for — the link does nothing", path, id)
			}
		}
	}
}

// A page's subtitle sets its posture. "Whether the techniques OpenTacit suggests
// are being adopted" opens by asking if OpenTacit works at all; "How the techniques
// are being adopted" reports on adoption it presumes. The dashboard is OpenTacit's
// own face — it should state what it measures, not put the product's worth up for
// question in its own headers. This guards the page-head subtitles against the
// doubt-framing creeping back; "whether" is fine anywhere in body copy, where it
// is describing a real either/or and not framing the product.
func TestPageSubtitlesDoNotQuestionTheProduct(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := srv.Store.AppendAuditFact(models.AuditFact{AuditID: "sub1", CreatedAt: now,
		TaskType: "editing", Segment: models.Segment{"team": "t"}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/outcomes", "/learning", "/techniques", "/techniques/tags", "/review"} {
		_, body := fetchHTML(t, ts.URL+path)
		if strings.Contains(body, `<p class="sub">Whether`) {
			t.Fatalf("%s opens its subtitle with 'Whether' — that frames the product as an open question, not a thing being measured", path)
		}
	}
}

// The review app is the drafts queue as an interactive MCP Apps document:
// full techniques, decision buttons wired to tacit_draft_action, and text +
// structured fallbacks for hosts that render neither.
func TestReviewAppServesDraftsWithActions(t *testing.T) {
	_, ts := newServer(t)
	resp, _ := request(t, "POST", ts.URL+"/v1/contribute", "test-key",
		`{"name": "review-app-draft", "description": "d", "recipe": "r", "scope": "org"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("contribute: %d", resp.StatusCode)
	}
	r2, body := request(t, "GET", ts.URL+"/v1/review/app", "test-key", "")
	if r2.StatusCode != 200 {
		t.Fatalf("review app: %d", r2.StatusCode)
	}
	htmlDoc, _ := body["html"].(string)
	text, _ := body["text"].(string)
	summary, _ := body["summary"].(map[string]any)
	for _, want := range []string{"review-app-draft", "tacit_draft_action",
		`data-action="promote"`, `data-action="reject"`, "ui/initialize"} {
		if !strings.Contains(htmlDoc, want) {
			t.Errorf("review app html missing %q", want)
		}
	}
	if !strings.Contains(text, "review-app-draft") {
		t.Error("text fallback missing the draft")
	}
	if n, _ := summary["count"].(float64); n < 1 {
		t.Errorf("summary count = %v, want >= 1", summary["count"])
	}
	// No key, no queue.
	if r3, _ := request(t, "GET", ts.URL+"/v1/review/app", "", ""); r3.StatusCode != 401 {
		t.Fatalf("unauthenticated review app: %d, want 401", r3.StatusCode)
	}
}

func TestCohortSpreadDimensionFilter(t *testing.T) {
	cohorts := []insights.MixEntry{
		{Label: "surface:cli", Count: 5}, {Label: "surface:mcp", Count: 3},
		{Label: "team:growth", Count: 2}, {Label: "untagged", Count: 1},
	}
	if got := cohortDims(cohorts); !reflect.DeepEqual(got, []string{"surface", "team"}) {
		t.Fatalf("cohortDims = %v, want [surface team]", got)
	}
	head := cohortSpreadHead(cohorts)
	for _, want := range []string{
		`<option value="">all cohorts</option>`,
		`<option value="surface">surface</option>`,
		`<option value="team">team</option>`,
		`class="dim-select"`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("cohortSpreadHead missing %s in %s", want, head)
		}
	}
	if s := cohortSpreadScript(cohorts); !strings.Contains(s, "cohort-dim") {
		t.Errorf("cohortSpreadScript should wire the dropdown, got %s", s)
	}
	// One dimension (or none) renders the plain heading — a filter with a
	// single choice would be chrome without information.
	if h := cohortSpreadHead(cohorts[:2]); strings.Contains(h, "select") {
		t.Errorf("single-dimension head should have no dropdown: %s", h)
	}
	if s := cohortSpreadScript(cohorts[:2]); s != "" {
		t.Errorf("single-dimension script should be empty, got %s", s)
	}
}

// The front door renders the ambient scene, which now lives in internal/ui and is
// shared with the project page at the zone's apex. Both find their canvas by
// attribute; this is what notices if the registry's markup loses it, since a
// missing attribute costs nothing but a page that has quietly stopped moving.
func TestFrontDoorStillRendersTheSharedBackdrop(t *testing.T) {
	if !strings.Contains(signinTemplate, "data-backdrop") {
		t.Error("the front door's canvas carries no data-backdrop, so the scene will not start")
	}
	if !strings.Contains(signinScript, ui.BackdropURL()) {
		t.Error("the front door no longer loads the scene at the shared fingerprinted URL")
	}
}

// A shared form must not floor at its inputs' intrinsic width. `1fr` means
// `minmax(auto,1fr)`, and auto's minimum is min-content — for an <input> that is
// its default twenty-odd characters, so each column refused to go below about
// 195px and the Add-a-feed form pushed a 390px phone out to 428. Measured in
// Chromium before and after: 428/390, then 390/390.
func TestFormGridDoesNotFloorAtItsInputsWidth(t *testing.T) {
	rule := appCSS[strings.Index(appCSS, ".form-grid{"):]
	rule = rule[:strings.Index(rule, "}")]
	if !strings.Contains(rule, "minmax(0,1fr)") {
		t.Error(".form-grid columns floor at min-content; a phone scrolls sideways")
	}
	// And one column where two would be two fields nobody can type in.
	if !strings.Contains(appCSS, "@media (max-width:560px){.form-grid{grid-template-columns:minmax(0,1fr)}}") {
		t.Error("the form keeps two columns on a narrow window")
	}
}

// The avatar menu is TWO destinations, and each is a section reached by the
// breadcrumb switcher rather than a page listed in the menu. Eight entries plus
// Sign out was a menu somebody read rather than used.
//
// Every page keeps its own URL, so nothing 301s and no bookmark breaks — the
// switcher is a select that navigates, working with no script exactly as the
// period control does.
func TestAvatarMenuIsTwoSections(t *testing.T) {
	org, _ := newServer(t)
	menu := org.accountMenu()
	if n := strings.Count(menu, `class="account-item"`); n != 2 {
		t.Errorf("the avatar menu has %d destinations; it should have two", n)
	}
	for _, want := range []string{`>Settings<`, `>Documentation<`} {
		if !strings.Contains(menu, want) {
			t.Errorf("the menu is missing %s", want)
		}
	}
	// Everything else that was ever in here is reached from its own section —
	// and Usage from the top bar, because a member's own numbers are a question
	// somebody arrives with rather than an account setting.
	for _, gone := range []string{">Members<", ">Team<", ">Federation<", ">Learning readiness<",
		">User Guide<", ">Development<", ">Connect your tools<", ">Usage<"} {
		if strings.Contains(menu, gone) {
			t.Errorf("%s is still its own menu entry", gone)
		}
	}
	// Settings lands on General, the first view in its own switcher.
	if !strings.Contains(menu, `href="/settings">Settings<`) {
		t.Error("Settings does not land on the General page")
	}
}

// The Settings switcher offers the four pages behind that one entry. General
// leads, because it is where the section lands and because the section's own
// name is Settings — a page inside it cannot carry that name too. The people
// page is named the way its own page is: Team on a registry that is one
// member's, Members on an organization's.
func TestRegistrySwitcherCarriesTheFoldedPages(t *testing.T) {
	org, _ := newServer(t)
	var labels, hrefs []string
	for _, o := range org.registryViews("people") {
		labels = append(labels, o.label)
		hrefs = append(hrefs, o.href)
	}
	if len(labels) != 4 {
		t.Fatalf("the switcher offers %v; it should offer four views", labels)
	}
	if labels[0] != "General" || hrefs[0] != "/settings" {
		t.Errorf("General does not lead the switcher: %v at %v", labels, hrefs)
	}
	if labels[2] != "Federation" || labels[3] != "Learning readiness" {
		t.Errorf("the switcher offers %v", labels)
	}
	if labels[1] != "Members" || hrefs[1] != "/members" {
		t.Errorf("an organization's registry calls its people page %q at %q", labels[1], hrefs[1])
	}
	if !org.registryViews("people")[1].active {
		t.Error("the switcher does not mark the view it is on")
	}
	own, _ := newServer(t)
	own.Cfg.AuthMode = "owner"
	own.Cfg.OwnerSecret = "s"
	if got := own.registryViews("people")[1].label; got != "Team" {
		t.Errorf("a single-member registry calls its people page %q", got)
	}
}

// `tacit init` serves in the foreground and polls its own /v1/health to learn
// when the registry is up and what address the ingress gave it. Every poll was
// logged, so the product's own waiting appeared in the middle of the sentences
// it was waiting to print: two `GET /v1/health` lines cutting the owner's
// sign-in link in half, on the screen a new operator reads most carefully.
func TestSelfPollIsNotLoggedButOtherProbesAre(t *testing.T) {
	req := func(method, path, remote string) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = remote
		return r
	}
	for _, c := range []struct {
		name  string
		r     *http.Request
		base  string
		quiet bool
	}{
		{"loopback health poll", req("GET", "/v1/health", "127.0.0.1:54321"), "", true},
		{"loopback v6 health poll", req("GET", "/v1/health", "[::1]:54321"), "", true},
		{"health poll from elsewhere", req("GET", "/v1/health", "10.0.0.9:54321"), "", false},
		{"another loopback path", req("GET", "/v1/techniques", "127.0.0.1:54321"), "", false},
		{"a write to health", req("POST", "/v1/health", "127.0.0.1:54321"), "", false},
		{"under a base path", req("GET", "/apps/tacit/v1/health", "127.0.0.1:1"), "/apps/tacit", true},
		{"base path mismatch", req("GET", "/v1/health", "127.0.0.1:1"), "/apps/tacit", false},
	} {
		if got := selfPoll(c.r, c.base); got != c.quiet {
			t.Errorf("%s: selfPoll = %v, want %v", c.name, got, c.quiet)
		}
	}
}
