// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// fixedClock returns a now() that always yields t — usage timestamps must be
// deterministic in tests (the log stamps ts from the injected clock).
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// The usage log is the member-local resolution of the same collision techniquememory
// solves: the registry can't attribute usage to a person (cohorts, never
// identities), yet a member wants to see their own history. It lives on the
// member's machine, survives reload, and aggregates into the Usage view's
// payload.
func TestUsageLogAppendsAndSurvivesReload(t *testing.T) {
	path := t.TempDir() + "/usage.jsonl"
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	u := loadUsageLog(path, fixedClock(now))
	u.append(usageEvent{Kind: usageQuery, Harness: "claude-code"})
	u.append(usageEvent{Kind: usageShown, Cap: "use-connector", Name: "Use the connector", Harness: "claude-code"})
	u.append(usageEvent{Kind: usageAdopted, Cap: "use-connector"})
	u.append(usageEvent{Kind: usageHelped, Cap: "use-connector"})

	// A fresh agent process on the same machine reads the same history.
	u2 := loadUsageLog(path, fixedClock(now))
	sum := u2.summarize(0)
	if sum.Totals.Queries != 1 || sum.Totals.Shown != 1 || sum.Totals.Adopted != 1 || sum.Totals.Helped != 1 {
		t.Fatalf("totals across reload = %+v", sum.Totals)
	}
	if len(sum.Techniques) != 1 {
		t.Fatalf("want 1 technique, got %d", len(sum.Techniques))
	}
	c := sum.Techniques[0]
	// The name is resolved from the shown event even though adopted/helped
	// carried only the id — the drill-down is labelled without a registry join.
	if c.TechniqueID != "use-connector" || c.Name != "Use the connector" {
		t.Fatalf("technique label not resolved: %+v", c)
	}
	if c.Shown != 1 || c.Adopted != 1 || c.Helped != 1 {
		t.Fatalf("technique funnel = %+v", c)
	}
}

// Events past the retention horizon are dropped on load AND compacted out of
// the file, so the on-disk log can never grow without bound.
func TestUsageLogPrunesOldEventsOnLoad(t *testing.T) {
	path := t.TempDir() + "/usage.jsonl"
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := old.Add(usageRetention + 48*time.Hour)

	seed := loadUsageLog(path, fixedClock(old))
	seed.append(usageEvent{Kind: usageQuery})
	seed.append(usageEvent{Kind: usageShown, Cap: "c1", Name: "One"})

	// A much later process: everything seeded is now beyond retention.
	reloaded := loadUsageLog(path, fixedClock(now))
	if got := reloaded.summarize(0).Totals; got.Queries != 0 || got.Shown != 0 {
		t.Fatalf("stale events survived retention: %+v", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("stale events were not compacted out of the file: %q", raw)
	}
}

// A window scopes both the totals and the drill-down to recent events; older
// ones stay on disk (for a wider window) but don't count.
func TestUsageSummarizeWindow(t *testing.T) {
	path := t.TempDir() + "/usage.jsonl"
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	u := loadUsageLog(path, fixedClock(now.Add(-40*24*time.Hour)))
	u.append(usageEvent{Kind: usageShown, Cap: "old", Name: "Old"}) // 40 days ago
	u.now = fixedClock(now)
	u.append(usageEvent{Kind: usageShown, Cap: "new", Name: "New"}) // today

	if got := u.summarize(7 * 24 * time.Hour).Totals.Shown; got != 1 {
		t.Fatalf("7-day window shown = %d, want 1 (only today's)", got)
	}
	if got := u.summarize(0).Totals.Shown; got != 2 {
		t.Fatalf("all-time shown = %d, want 2", got)
	}
}

// The real proof: the same hook flow that produces registry feedback events
// also fills the member-local usage log, through the agent's actual code paths
// (commitLocked, postFeedback, the UserPromptSubmit query counter). This is
// what makes the Usage view show real numbers rather than always-empty.
func TestUsageLogFillsThroughAgentFlow(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline})
	driveToSuggestion(agent) // one query turn + a shown suggestion

	// The member uses the suggested move -> behavioral adoption.
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "let me query the warehouse connector"}), "claude-code")
	// Keep working so the adoption is promoted to an inferred helped.
	for i := 0; i < HelpedConfirmTurns; i++ {
		agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on with the next step"}), "claude-code")
	}

	sum := agent.UsageSummary(0)
	// Queries: driveToSuggestion submits one prompt and, on a form harness, a
	// second to carry the parked offer; then adoption + confirm turns.
	if sum.Totals.Queries != 3+HelpedConfirmTurns {
		t.Errorf("queries = %d, want %d", sum.Totals.Queries, 2+HelpedConfirmTurns)
	}
	if sum.Totals.Shown != 1 || sum.Totals.Adopted != 1 || sum.Totals.Helped != 1 {
		t.Fatalf("funnel via agent flow = %+v", sum.Totals)
	}
	// The technique is labelled from the shown event, matching the registry feedback.
	if len(sum.Techniques) != 1 || sum.Techniques[0].TechniqueID != "use-internal-data-connector" ||
		sum.Techniques[0].Name != "Query live warehouse data" {
		t.Fatalf("technique drill-down not labelled from shown: %+v", sum.Techniques)
	}
	// Sanity: the local log agrees with what the registry received.
	if len(log.byStage("shown")) != 1 || len(log.byStage("helped")) != 1 {
		t.Fatalf("registry feedback and local log disagree: shown=%d helped=%d",
			len(log.byStage("shown")), len(log.byStage("helped")))
	}
}

// The Usage view fetches this endpoint from a browser page the registry served
// — a different origin from this loopback agent — so it must answer with the
// aggregated JSON AND the CORS/Private-Network-Access headers that let a
// cross-origin (and possibly HTTPS) page read loopback.
func TestUsageEndpointServesJSONWithCORS(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline})
	driveToSuggestion(agent)
	ts := httptest.NewServer(Handler(agent, "")) // usage is open, like stats
	defer ts.Close()

	// Preflight: the dashboard's origin must be reflected and PNA granted.
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/v1/hooks/usage", nil)
	req.Header.Set("Origin", "https://reg.example.ts.net")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	pre, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pre.Body.Close()
	if got := pre.Header.Get("Access-Control-Allow-Origin"); got != "https://reg.example.ts.net" {
		t.Errorf("preflight Allow-Origin = %q", got)
	}
	if got := pre.Header.Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("preflight Allow-Private-Network = %q, want true", got)
	}

	// GET: reflected origin + the aggregated payload.
	req2, _ := http.NewRequest("GET", ts.URL+"/v1/hooks/usage?window=7d", nil)
	req2.Header.Set("Origin", "https://reg.example.ts.net")
	resp, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://reg.example.ts.net" {
		t.Errorf("GET Allow-Origin = %q", got)
	}
	var out UsageSummary
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Window != "7d" {
		t.Errorf("window echoed = %q, want 7d", out.Window)
	}
	if out.Totals.Shown != 1 {
		t.Fatalf("usage payload shown = %d, want 1: %+v", out.Totals.Shown, out.Totals)
	}
}

// RenderUsageApp is what the tacit_usage MCP tool serves: a self-contained
// graphical app (all windows embedded, the asked-for one selected), a text
// fallback, and a structured summary — all from the member-local log, no
// registry.
func TestRenderUsageApp(t *testing.T) {
	path := t.TempDir() + "/usage.jsonl"
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	u := loadUsageLog(path, fixedClock(now))
	u.append(usageEvent{Kind: usageQuery})
	u.append(usageEvent{Kind: usageShown, Cap: "c1", Name: "Scope the blast radius"})
	u.append(usageEvent{Kind: usageAdopted, Cap: "c1"})
	u.append(usageEvent{Kind: usageHelped, Cap: "c1"})

	// Read with the same clock the fixtures were written on. Against the wall
	// clock this test passed for seven days and then failed permanently, because
	// the events aged out of the window it asks for.
	html, text, summary, err := renderUsageAppAt(path, "7d", fixedClock(now))
	if err != nil {
		t.Fatal(err)
	}
	// Self-contained doc with the mcp-ui bridge; the technique name is rendered.
	for _, want := range []string{"<!doctype html", "ui/initialize", "Scope the blast radius", `data-window="7d"`} {
		if !strings.Contains(html, want) {
			t.Errorf("app HTML missing %q", want)
		}
	}
	// All four preset windows are embedded (so the in-frame switcher works).
	for _, key := range []string{"7d", "30d", "90d", "all"} {
		if !strings.Contains(html, `data-window="`+key+`"`) {
			t.Errorf("app HTML missing embedded window %q", key)
		}
	}
	// Text fallback carries the numbers.
	if !strings.Contains(text, "Queries:") || !strings.Contains(text, "Scope the blast radius") {
		t.Errorf("text fallback wrong:\n%s", text)
	}
	// Structured summary is the selected window's aggregate.
	if summary["window"] != "7d" {
		t.Errorf("summary window = %v, want 7d", summary["window"])
	}
	if tot := summary["totals"].(map[string]any); tot["helped"] != 1 || tot["adopted"] != 1 {
		t.Errorf("summary totals wrong: %v", tot)
	}
}

// An empty log renders the app cleanly (the "nothing yet" state), never an error.
func TestRenderUsageAppEmpty(t *testing.T) {
	path := t.TempDir() + "/usage.jsonl"
	html, text, _, err := RenderUsageApp(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "<!doctype html") {
		t.Error("empty app should still be a document")
	}
	if !strings.Contains(text, "Nothing recorded") {
		t.Errorf("empty text fallback should say so:\n%s", text)
	}
}

func TestParseUsageWindow(t *testing.T) {
	day := 24 * time.Hour
	cases := map[string]time.Duration{
		"":     30 * day,
		"30d":  30 * day,
		"7d":   7 * day,
		"12w":  12 * 7 * day,
		"all":  0,
		"0":    0,
		"junk": 30 * day,       // unrecognized -> default
		"999d": usageRetention, // clamped to what's on disk
	}
	for in, want := range cases {
		if got := ParseUsageWindow(in); got != want {
			t.Errorf("ParseUsageWindow(%q) = %v, want %v", in, got, want)
		}
	}
}

// Phase 0 of docs/delivery/out-of-band-plan.md: the org's dashboard cannot
// serve a member their own numbers, so the member's own agent serves the page
// instead — the same document the MCP app renders, at an address a person can
// open. The bare address redirects there, because it is the only thing here a
// member has any reason to look at.
func TestAgentServesUsagePageOnLoopback(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline})
	driveToSuggestion(agent)
	ts := httptest.NewServer(Handler(agent, "")) // open, like the JSON feed beside it
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/usage?w=7d")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /usage = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	// The document IS the data; a cached copy is a stale answer.
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	body, _ := io.ReadAll(resp.Body)
	page := string(body)
	if !strings.HasPrefix(page, "<!doctype html>") {
		t.Fatalf("page is not a whole document: %.60q", page)
	}
	// Every window ships in the one document so the in-frame switcher needs no
	// round trip; the requested one is the visible one.
	for _, w := range []string{"7d", "30d", "90d", "all"} {
		if !strings.Contains(page, `data-window="`+w+`"`) {
			t.Errorf("window %s missing from the page", w)
		}
	}
	if !strings.Contains(page, `data-window="7d" aria-pressed="true"`) {
		t.Error("?w=7d did not select the 7d view")
	}

	// A member who types the agent's address alone lands on the page.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	root, err := noRedirect.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Body.Close()
	if root.StatusCode != http.StatusFound || root.Header.Get("Location") != "/usage" {
		t.Errorf("GET / = %d %q, want 302 /usage", root.StatusCode, root.Header.Get("Location"))
	}
}
