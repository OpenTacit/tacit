// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

func testServer(block contracts.EvidenceBlock, searchErr error) *Server {
	return testServerSeeing(block, searchErr, nil)
}

// testServerSeeing additionally records every characterization the server sends
// to retrieval, so tests can assert on the join key the registry would receive.
func testServerSeeing(block contracts.EvidenceBlock, searchErr error,
	seen *[]contracts.Characterization) *Server {
	return &Server{
		Search: func(ch contracts.Characterization) (contracts.EvidenceBlock, error) {
			if seen != nil {
				*seen = append(*seen, ch)
			}
			return block, searchErr
		},
		Segment: contracts.Segment{"team": "revops"},
	}
}

// insightsServer wires an Insights closure returning a canned app payload.
func insightsServer() *Server {
	return &Server{
		Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		Insights: func(window string) (string, string, map[string]any, error) {
			if window == "" {
				window = "30d"
			}
			return "<!doctype html><title>Tacit</title><div class=\"tiles\">app</div>",
				"◆ Tacit — " + window + "\nFunnel: 10 shown → 4 adopted",
				map[string]any{"window": window, "shown": 10, "adopted": 4}, nil
		},
		Segment: contracts.Segment{"team": "revops"},
	}
}

func TestInsightsToolAdvertisedWhenWired(t *testing.T) {
	s := insightsServer()

	// initialize advertises resources + the UI extension only when Insights is set.
	init := s.Handle(req(t, 1, "initialize", `{"protocolVersion":"2025-03-26"}`))
	caps := init.Result.(map[string]any)["capabilities"].(map[string]any)
	if _, ok := caps["resources"]; !ok {
		t.Fatal("resources capability missing when insights wired")
	}
	ext, ok := caps["extensions"].(map[string]any)
	if !ok || ext[uiExtension] == nil {
		t.Fatalf("UI extension missing: %v", caps["extensions"])
	}

	// tools/list carries search and the consolidated metrics tool; the old
	// tacit_insights/org/map names are no longer advertised (they stay
	// dispatchable for back-compat). Metrics declares the funnel UI resource as
	// its default hint.
	tools := s.Handle(req(t, 2, "tools/list", "")).Result.(map[string]any)["tools"].([]any)
	names := map[string]map[string]any{}
	for _, tl := range tools {
		m := tl.(map[string]any)
		names[m["name"].(string)] = m
	}
	if names[toolName] == nil || names[metricsToolName] == nil {
		t.Fatalf("want both %s and %s, got %v", toolName, metricsToolName, tools)
	}
	if names[insightsToolName] != nil || names[orgToolName] != nil || names[mapToolName] != nil {
		t.Fatalf("old metric tools should no longer be advertised: %v", tools)
	}
	meta := names[metricsToolName]["_meta"].(map[string]any)["ui"].(map[string]any)
	if meta["resourceUri"] != insightsResourceURI {
		t.Fatalf("metrics tool missing default ui.resourceUri: %v", meta)
	}
}

// TestMetricsToolViews exercises the consolidated tool's three views: funnel and
// map embed their UI resource, cohorts is text-first with a structured summary.
func TestMetricsToolViews(t *testing.T) {
	s := &Server{
		Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		Insights: func(window string) (string, string, map[string]any, error) {
			if window == "" {
				window = "30d"
			}
			return `<!doctype html><title>Tacit</title><div class="tiles">app</div>`,
				"◆ Tacit — " + window, map[string]any{"window": window, "shown": 10}, nil
		},
		Org: func(window string) (string, map[string]any, error) {
			return "cohort spread text", map[string]any{"cohorts": 3}, nil
		},
		Map: func() (string, string, map[string]any, error) {
			return `<!doctype html><canvas id="cmap"></canvas>`, "map text",
				map[string]any{"live_techniques": 5}, nil
		},
	}
	// funnel (default) embeds the insights app.
	funnel := s.Handle(req(t, 1, "tools/call", `{"name":"tacit_metrics","arguments":{"view":"funnel","window":"7d"}}`)).Result.(map[string]any)
	if funnel["isError"].(bool) || len(funnel["content"].([]any)) != 2 {
		t.Fatalf("funnel view should return text + resource: %v", funnel)
	}
	if funnel["_meta"].(map[string]any)["ui"].(map[string]any)["resourceUri"] != insightsResourceURI {
		t.Fatalf("funnel view should point at the insights resource: %v", funnel["_meta"])
	}
	// map embeds the map app — reachable as text on terminal hosts too.
	mp := s.Handle(req(t, 2, "tools/call", `{"name":"tacit_metrics","arguments":{"view":"map"}}`)).Result.(map[string]any)
	if mp["isError"].(bool) || mp["_meta"].(map[string]any)["ui"].(map[string]any)["resourceUri"] != mapResourceURI {
		t.Fatalf("map view should point at the map resource: %v", mp)
	}
	// cohorts is text-first with a structured summary and no UI resource.
	co := s.Handle(req(t, 3, "tools/call", `{"name":"tacit_metrics","arguments":{"view":"cohorts"}}`)).Result.(map[string]any)
	if co["isError"].(bool) || co["structuredContent"] == nil {
		t.Fatalf("cohorts view should return a structured summary: %v", co)
	}
	if _, hasMeta := co["_meta"]; hasMeta {
		t.Fatalf("cohorts view should carry no UI resource: %v", co)
	}
}

func TestInsightsToolCallReturnsAppAndText(t *testing.T) {
	s := insightsServer()
	resp := s.Handle(req(t, 3, "tools/call", `{"name":"tacit_insights","arguments":{"window":"7d"}}`))
	result := resp.Result.(map[string]any)
	if result["isError"].(bool) {
		t.Fatalf("insights call errored: %v", result)
	}
	content := result["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("want text + resource content, got %d items", len(content))
	}
	// First a text fallback (with the requested window), then the HTML resource.
	txt := content[0].(map[string]any)
	if txt["type"] != "text" || !strings.Contains(txt["text"].(string), "7d") {
		t.Fatalf("text fallback wrong: %v", txt)
	}
	res := content[1].(map[string]any)["resource"].(map[string]any)
	if res["uri"] != insightsResourceURI || res["mimeType"] != appProfileMime ||
		!strings.Contains(res["text"].(string), "<!doctype html") {
		t.Fatalf("ui resource wrong: %v", res)
	}
	if res["_meta"] == nil {
		t.Fatal("inline ui resource missing _meta render config")
	}
	if sc, ok := result["structuredContent"].(map[string]any); !ok || sc["window"] != "7d" {
		t.Fatalf("structuredContent missing/wrong: %v", result["structuredContent"])
	}
}

func TestInsightsResourceReadAndListing(t *testing.T) {
	s := insightsServer()
	list := s.Handle(req(t, 4, "resources/list", "")).Result.(map[string]any)["resources"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["uri"] != insightsResourceURI {
		t.Fatalf("resources/list = %v", list)
	}
	if list[0].(map[string]any)["_meta"] == nil {
		t.Fatalf("listed resource missing _meta render config: %v", list[0])
	}
	read := s.Handle(req(t, 5, "resources/read", `{"uri":"`+insightsResourceURI+`"}`))
	contents := read.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
	if contents["mimeType"] != appProfileMime || !strings.Contains(contents["text"].(string), "tiles") {
		t.Fatalf("resources/read wrong: %v", contents)
	}
	// The host reads its render config (border, initial frame box) off the resource.
	meta := contents["_meta"].(map[string]any)
	if _, ok := meta["ui"].(map[string]any)["prefersBorder"]; !ok {
		t.Fatalf("resource _meta.ui missing render config: %v", meta)
	}
	if meta["mcpui.dev/ui-preferred-frame-size"] == nil {
		t.Fatalf("resource _meta missing preferred frame size: %v", meta)
	}
	// An unknown resource is an error.
	if s.Handle(req(t, 6, "resources/read", `{"uri":"ui://nope"}`)).Error == nil {
		t.Fatal("unknown resource should error")
	}
}

// Without Insights wired, the insights tool and resources stay hidden.
func TestInsightsHiddenWhenUnwired(t *testing.T) {
	s := testServer(contracts.EvidenceBlock{}, nil)
	tools := s.Handle(req(t, 1, "tools/list", "")).Result.(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("insights tool should be hidden without Insights, got %d tools", len(tools))
	}
	if s.Handle(req(t, 2, "tools/call", `{"name":"tacit_insights","arguments":{}}`)).Error == nil {
		t.Fatal("tacit_insights should be unknown when unwired")
	}
	if list := s.Handle(req(t, 3, "resources/list", "")).Result.(map[string]any)["resources"].([]any); len(list) != 0 {
		t.Fatalf("no resources without Insights, got %v", list)
	}
}

// mapServer wires a Map closure returning a canned app payload.
func mapServer() *Server {
	return &Server{
		Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		Map: func() (string, string, map[string]any, error) {
			return `<!doctype html><title>Tacit map</title><canvas id="cmap"></canvas>`,
				"◆ Tacit · playbook map\n5 live techniques",
				map[string]any{"live_techniques": 5, "areas": []any{map[string]any{"name": "review", "techniques": 3}}}, nil
		},
	}
}

func TestMapToolAdvertisedAndCall(t *testing.T) {
	s := mapServer()

	// initialize advertises the resources capability + UI extension for the map app.
	caps := s.Handle(req(t, 1, "initialize", `{"protocolVersion":"2025-03-26"}`)).Result.(map[string]any)["capabilities"].(map[string]any)
	if _, ok := caps["resources"]; !ok {
		t.Fatal("resources capability missing when map wired")
	}

	// tools/list carries the consolidated metrics tool (Map alone is enough to
	// list it); the bare tacit_map name is no longer advertised.
	tools := s.Handle(req(t, 2, "tools/list", "")).Result.(map[string]any)["tools"].([]any)
	var metricsTool map[string]any
	for _, tl := range tools {
		m := tl.(map[string]any)
		if m["name"] == mapToolName {
			t.Fatalf("tacit_map should no longer be advertised: %v", tools)
		}
		if m["name"] == metricsToolName {
			metricsTool = m
		}
	}
	if metricsTool == nil {
		t.Fatalf("tacit_metrics missing from tools/list: %v", tools)
	}

	// The map view embeds the map UI resource — and the old tacit_map name still
	// dispatches for back-compat. Both must return the same resource.
	for _, call := range []string{
		`{"name":"tacit_metrics","arguments":{"view":"map"}}`,
		`{"name":"tacit_map","arguments":{}}`,
	} {
		resp := s.Handle(req(t, 3, "tools/call", call)).Result.(map[string]any)
		if resp["isError"].(bool) {
			t.Fatalf("map call %s errored: %v", call, resp)
		}
		res := resp["content"].([]any)[1].(map[string]any)["resource"].(map[string]any)
		if res["uri"] != mapResourceURI || !strings.Contains(res["text"].(string), "cmap") {
			t.Fatalf("map ui resource wrong for %s: %v", call, res)
		}
	}

	// tools/call returns the text fallback, the inline UI resource, and the summary.
	resp := s.Handle(req(t, 3, "tools/call", `{"name":"tacit_map","arguments":{}}`)).Result.(map[string]any)
	if resp["isError"].(bool) {
		t.Fatalf("map call errored: %v", resp)
	}
	res := resp["content"].([]any)[1].(map[string]any)["resource"].(map[string]any)
	if res["uri"] != mapResourceURI || res["mimeType"] != appProfileMime || !strings.Contains(res["text"].(string), "cmap") {
		t.Fatalf("map ui resource wrong: %v", res)
	}
	if sc, ok := resp["structuredContent"].(map[string]any); !ok || sc["live_techniques"] != 5 {
		t.Fatalf("structuredContent wrong: %v", resp["structuredContent"])
	}

	// resources/list + read include the map template.
	list := s.Handle(req(t, 4, "resources/list", "")).Result.(map[string]any)["resources"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["uri"] != mapResourceURI {
		t.Fatalf("resources/list = %v", list)
	}
	read := s.Handle(req(t, 5, "resources/read", `{"uri":"`+mapResourceURI+`"}`)).Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
	if !strings.Contains(read["text"].(string), "cmap") {
		t.Fatalf("resources/read wrong: %v", read)
	}
}

// Without Map wired, the map tool and its resource stay hidden.
func TestMapHiddenWhenUnwired(t *testing.T) {
	s := testServer(contracts.EvidenceBlock{}, nil)
	for _, tl := range s.Handle(req(t, 1, "tools/list", "")).Result.(map[string]any)["tools"].([]any) {
		if tl.(map[string]any)["name"] == mapToolName {
			t.Fatal("tacit_map should be hidden without Map wired")
		}
	}
	if s.Handle(req(t, 2, "tools/call", `{"name":"tacit_map","arguments":{}}`)).Error == nil {
		t.Fatal("tacit_map should be unknown when unwired")
	}
}

func req(t *testing.T, id int, method, params string) request {
	t.Helper()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q`, id, method)
	if params != "" {
		body += `,"params":` + params
	}
	body += `}`
	var r request
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLifecycle(t *testing.T) {
	s := testServer(contracts.EvidenceBlock{}, nil)

	init := s.Handle(req(t, 1, "initialize", `{"protocolVersion":"2025-03-26"}`))
	result := init.Result.(map[string]any)
	if result["protocolVersion"] != "2025-03-26" {
		t.Fatalf("version echo: %v", result)
	}
	caps := result["capabilities"].(map[string]any)
	if _, hasTools := caps["tools"]; !hasTools {
		t.Fatal("tools capability missing")
	}

	// notifications get no response
	var note request
	_ = json.Unmarshal([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), &note)
	if s.Handle(note) != nil {
		t.Fatal("notification answered")
	}

	list := s.Handle(req(t, 2, "tools/list", ""))
	tools := list.Result.(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != toolName {
		t.Fatalf("tools: %v", tools)
	}

	if s.Handle(req(t, 3, "ping", "")).Result == nil {
		t.Fatal("ping unanswered")
	}
	if s.Handle(req(t, 4, "bogus/method", "")).Error == nil {
		t.Fatal("unknown method accepted")
	}
}

func TestServeHTTPStreamable(t *testing.T) {
	s := insightsServer()

	// A request is answered with a single application/json JSON-RPC response.
	post := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}

	w := post(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("initialize status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if resp["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatalf("initialize result: %v", resp["result"])
	}

	// tools/call over HTTP returns the same payload as the stdio path.
	w = post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","arguments":{},"params":{"name":"tacit_insights","arguments":{"window":"7d"}}}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "structuredContent") {
		t.Fatalf("tools/call over HTTP: %d %s", w.Code, w.Body.String())
	}

	// A notification (no id) is acknowledged with 202 and an empty body.
	w = post(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if w.Code != http.StatusAccepted || w.Body.Len() != 0 {
		t.Fatalf("notification: status=%d body=%q", w.Code, w.Body.String())
	}

	// Malformed JSON is a 400; non-POST is a 405.
	if w := post(t, "not json"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", w.Code)
	}
}

func candidate() contracts.EvidenceCandidate {
	return contracts.EvidenceCandidate{
		TechniqueID: "use-internal-data-connector",
		Name:        "Query live warehouse data",
		Scope:       "org",
		Recipe:      "@warehouse query <table>",
		AppliesWhen: "pasted tabular data from an internal source",
		NotWhen:     "ad-hoc data",
		Outcomes: map[string]any{
			"helped_rate": 0.94, "sample_size": float64(120), "segment": "team:revops",
		},
	}
}

func callSearch(t *testing.T, s *Server, query string) (string, bool) {
	t.Helper()
	resp := s.Handle(req(t, 5, "tools/call",
		`{"name":"tacit_search","arguments":{"query":"`+query+`"}}`))
	result := resp.Result.(map[string]any)
	content := result["content"].([]any)[0].(map[string]any)
	return content["text"].(string), result["isError"].(bool)
}

func TestSearchRendersEvidence(t *testing.T) {
	s := testServer(contracts.EvidenceBlock{
		Candidates: []contracts.EvidenceCandidate{candidate()}}, nil)
	text, isErr := callSearch(t, s, "analyze warehouse rows")
	if isErr {
		t.Fatalf("search errored: %s", text)
	}
	for _, want := range []string{
		"use-internal-data-connector", "org",
		"measured by colleagues: helped 94% · n=120 · team:revops",
		"when: pasted tabular data", "not when: ad-hoc data", "recipe: @warehouse query",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	// A pull records NO funnel event: `shown` is member-facing only, so an agent
	// browsing the playbook can't inflate the adoption/helped denominators. That's
	// now a compile-time guarantee — the MCP server has no feedback writer — so
	// there is nothing to assert beyond the rendering.
}

func TestSearchColdStartAndEmpty(t *testing.T) {
	cold := candidate()
	cold.Outcomes = nil
	s := testServer(contracts.EvidenceBlock{
		Candidates: []contracts.EvidenceCandidate{cold}}, nil)
	text, _ := callSearch(t, s, "x")
	if !strings.Contains(text, "awaiting measured outcomes") {
		t.Fatalf("cold-start honesty missing:\n%s", text)
	}

	empty := testServer(contracts.EvidenceBlock{}, nil)
	text, isErr := callSearch(t, empty, "x")
	if isErr || text != "Playbook search returned zero matching techniques." {
		t.Fatalf("unexpected empty-search result:\n%s", text)
	}
}

func TestSearchErrors(t *testing.T) {
	s := testServer(contracts.EvidenceBlock{}, errors.New("connection refused"))
	text, isErr := callSearch(t, s, "x")
	if !isErr || !strings.Contains(text, "registry unavailable") {
		t.Fatalf("registry error not surfaced: %v %s", isErr, text)
	}
	sOK := testServer(contracts.EvidenceBlock{}, nil)
	if _, isErr := callSearch(t, sOK, ""); !isErr {
		t.Fatal("blank query accepted")
	}
}

func TestRunOverStdioFraming(t *testing.T) {
	s := testServer(contracts.EvidenceBlock{}, nil)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		"not even json",
	}, "\n")
	var out strings.Builder
	if err := s.Run(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 { // two responses: initialize + tools/list
		t.Fatalf("responses: %d\n%s", len(lines), out.String())
	}
	for _, line := range lines {
		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("response not one-line JSON: %q", line)
		}
	}
}

// The ask-path audit id must be minted BEFORE retrieval and travel on the
// characterization, so the registry records a fact for this pull — surface="mcp",
// which distinguishes a pull/search from a member-facing push in the audit log.
// A pull emits no funnel event, but the fact still needs the join key so the
// pull is legible.
func TestSearchCarriesTheAuditIDToRetrieval(t *testing.T) {
	var seen []contracts.Characterization
	s := testServerSeeing(contracts.EvidenceBlock{
		Candidates: []contracts.EvidenceCandidate{{TechniqueID: "c1", Name: "N", Recipe: "r"}},
	}, nil, &seen)

	if _, isErr := s.search("warehouse queries"); isErr {
		t.Fatal("search failed")
	}
	if len(seen) != 1 {
		t.Fatalf("retrieval called %d times, want 1", len(seen))
	}
	ch := seen[0]
	if ch.AuditID == "" {
		t.Fatal("characterization reached retrieval with no audit_id: the registry " +
			"cannot record a fact for this pull")
	}
	if ch.Surface != "mcp" {
		t.Fatalf("ask-path surface = %q, want mcp — pull and push audits must be distinguishable", ch.Surface)
	}
}

// draftsServer wires Drafts + DraftAction closures with a canned queue.
func draftsServer(actions *[]string) *Server {
	return &Server{
		Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		Drafts: func() (string, string, map[string]any, error) {
			return `<!doctype html><title>Tacit review</title><div class="draft" data-id="d1">tacit_draft_action</div>`,
				"1 draft(s) awaiting review",
				map[string]any{"count": 1, "drafts": []any{map[string]any{"id": "d1"}}}, nil
		},
		DraftAction: func(id, action string) (string, error) {
			*actions = append(*actions, action+":"+id)
			if id == "missing" {
				return "", errors.New("technique not found")
			}
			return "Promoted " + id, nil
		},
		Segment: contracts.Segment{},
	}
}

func TestDraftsToolAndActionCall(t *testing.T) {
	var actions []string
	s := draftsServer(&actions)

	// Both tools advertised; the drafts tool declares its UI resource.
	tools := s.Handle(req(t, 1, "tools/list", "")).Result.(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	if !names[draftsToolName] || !names[actionToolName] {
		t.Fatalf("drafts tools missing from tools/list: %v", names)
	}

	// tacit_drafts returns text + the inline review app + the structured queue.
	resp := s.Handle(req(t, 2, "tools/call", `{"name":"tacit_drafts","arguments":{}}`)).Result.(map[string]any)
	if resp["isError"].(bool) {
		t.Fatalf("drafts call errored: %v", resp)
	}
	res := resp["content"].([]any)[1].(map[string]any)["resource"].(map[string]any)
	if res["uri"] != draftsResourceURI || !strings.Contains(res["text"].(string), "tacit_draft_action") {
		t.Fatalf("drafts ui resource wrong: %v", res)
	}
	if sc := resp["structuredContent"].(map[string]any); sc["count"] != 1 {
		t.Fatalf("structuredContent wrong: %v", resp["structuredContent"])
	}

	// The action tool validates input, then decides through the closure.
	bad := s.Handle(req(t, 3, "tools/call", `{"name":"tacit_draft_action","arguments":{"id":"d1","action":"eject"}}`)).Result.(map[string]any)
	if !bad["isError"].(bool) {
		t.Fatal("invalid action accepted")
	}
	good := s.Handle(req(t, 4, "tools/call", `{"name":"tacit_draft_action","arguments":{"id":"d1","action":"promote"}}`)).Result.(map[string]any)
	if good["isError"].(bool) || !strings.Contains(good["content"].([]any)[0].(map[string]any)["text"].(string), "Promoted d1") {
		t.Fatalf("promote result wrong: %v", good)
	}
	failed := s.Handle(req(t, 5, "tools/call", `{"name":"tacit_draft_action","arguments":{"id":"missing","action":"reject"}}`)).Result.(map[string]any)
	if !failed["isError"].(bool) {
		t.Fatal("closure error not surfaced as tool error")
	}
	if len(actions) != 2 || actions[0] != "promote:d1" || actions[1] != "reject:missing" {
		t.Fatalf("actions = %v", actions)
	}

	// resources/read serves the review app template.
	read := s.Handle(req(t, 6, "resources/read", `{"uri":"`+draftsResourceURI+`"}`)).Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
	if !strings.Contains(read["text"].(string), "Tacit review") {
		t.Fatalf("resources/read wrong: %v", read)
	}
}

// Without Drafts/DraftAction wired, both tools stay hidden.
func TestDraftsHiddenWhenUnwired(t *testing.T) {
	s := testServer(contracts.EvidenceBlock{}, nil)
	for _, tl := range s.Handle(req(t, 1, "tools/list", "")).Result.(map[string]any)["tools"].([]any) {
		n := tl.(map[string]any)["name"]
		if n == draftsToolName || n == actionToolName {
			t.Fatalf("%v should be hidden when unwired", n)
		}
	}
	if s.Handle(req(t, 2, "tools/call", `{"name":"tacit_draft_action","arguments":{"id":"x","action":"promote"}}`)).Error == nil {
		t.Fatal("tacit_draft_action should be unknown when unwired")
	}
}

// bigPanelServer serves an app whose HTML is larger than a tool result may
// carry — the real registry's insights panel is ~180KB, because each app
// embeds the whole stylesheet.
func bigPanelServer() *Server {
	html := "<!doctype html><style>" + strings.Repeat("x", maxInlineAppBytes) + "</style>"
	return &Server{
		Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		Insights: func(window string) (string, string, map[string]any, error) {
			return html, "◆ Tacit — the text fallback", map[string]any{"shown": 10}, nil
		},
	}
}

// A panel too large to inline must not take the whole result down with it. The
// text fallback and the structured summary are the parts a terminal host can
// actually use, and they were being lost inside an over-limit error.
func TestOversizedPanelTravelsAsALink(t *testing.T) {
	s := bigPanelServer()
	result := s.Handle(req(t, 1, "tools/call",
		`{"name":"tacit_metrics","arguments":{"view":"funnel"}}`)).Result.(map[string]any)

	content := result["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("want text + link, got %d items", len(content))
	}
	txt := content[0].(map[string]any)
	if txt["type"] != "text" || !strings.Contains(txt["text"].(string), "text fallback") {
		t.Fatalf("text fallback missing: %v", txt)
	}
	link := content[1].(map[string]any)
	if link["type"] != "resource_link" {
		t.Fatalf("want a resource_link for an oversized panel, got %v", link["type"])
	}
	if link["uri"] != insightsResourceURI {
		t.Fatalf("link points at %v, want %v", link["uri"], insightsResourceURI)
	}
	if _, inlined := link["text"]; inlined {
		t.Fatal("oversized panel was inlined anyway")
	}
	// App hosts still render it: the _meta pointer and resources/read are the
	// path they take, and neither depends on the inline copy.
	meta := result["_meta"].(map[string]any)["ui"].(map[string]any)
	if meta["resourceUri"] != insightsResourceURI {
		t.Fatalf("_meta.ui lost its resource pointer: %v", meta)
	}
	read := s.Handle(req(t, 2, "resources/read",
		`{"uri":"`+insightsResourceURI+`"}`)).Result.(map[string]any)
	if len(read["contents"].([]any)) != 1 {
		t.Fatal("resources/read no longer serves the panel")
	}

	// The whole result has to fit a tool-result budget with room to spare.
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxInlineAppBytes {
		t.Fatalf("result is %d bytes; the point was to stop shipping the panel", len(raw))
	}
}

// A panel small enough to be free still rides along inline, so mcp-ui hosts
// render it without a second round trip.
func TestSmallPanelStaysInline(t *testing.T) {
	result := insightsServer().Handle(req(t, 1, "tools/call",
		`{"name":"tacit_metrics","arguments":{"view":"funnel"}}`)).Result.(map[string]any)
	res := result["content"].([]any)[1].(map[string]any)
	if res["type"] != "resource" {
		t.Fatalf("small panel should inline, got %v", res["type"])
	}
	if !strings.Contains(res["resource"].(map[string]any)["text"].(string), "doctype") {
		t.Fatal("inline panel lost its HTML")
	}
}
