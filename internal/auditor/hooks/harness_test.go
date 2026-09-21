// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// The /v1/hooks/<harness> paths are a frozen contract with every relay and
// plugin already installed on members' machines, so the routed set is written
// out here by hand and the table must match it. A row nobody routes is dead
// code; a route with no row silently gets the claude-code capture and the
// default traits, which is how a deliverless harness ends up recording 'shown'
// events nobody saw.
func TestHarnessTableMatchesTheRoutedPaths(t *testing.T) {
	routed := []string{"claude-code", "codex", "gemini", "copilot", "cursor",
		"amp", "pi", "omp", "opencode"}

	for _, name := range routed {
		if _, ok := harnessTable[name]; !ok {
			t.Errorf("routed harness %q has no row in harnessTable", name)
		}
	}
	inRoute := map[string]bool{}
	for _, name := range routed {
		inRoute[name] = true
	}
	for name := range harnessTable {
		if !inRoute[name] {
			t.Errorf("harnessTable row %q is not routed", name)
		}
	}
	for name, traits := range harnessTable {
		if traits.NewCapture == nil {
			t.Errorf("harness %q has no capture constructor", name)
		}
	}
}

// Every row is reachable over HTTP, and the path really is the row's key.
func TestEveryHarnessRowAnswersItsRoute(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	ts := httptest.NewServer(Handler(agent, ""))
	defer ts.Close()

	for _, name := range harnessNames() {
		body := `{"hook_event_name":"SessionStart","session_id":"s-` + name + `"}`
		resp, err := http.Post(ts.URL+"/v1/hooks/"+name, "application/json",
			bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("POST /v1/hooks/%s: status %d", name, resp.StatusCode)
		}
	}
}

// The traits each harness carries. The behavior behind them is pinned by the
// per-harness tests; this pins the table itself, so a row edited by hand while
// adding harness #10 does not quietly change how an existing one delivers.
func TestHarnessTraitsAreWhatEachHarnessWasProbedFor(t *testing.T) {
	for _, tc := range []struct {
		harness                                                             string
		deliverless, park, form, sessionContext, mentionBlock, hasTranslate bool
	}{
		{harness: "claude-code", form: true, sessionContext: true, mentionBlock: true},
		{harness: "codex"},
		{harness: "gemini", hasTranslate: true},
		{harness: "copilot", deliverless: true, hasTranslate: true},
		{harness: "cursor", park: true, hasTranslate: true},
		{harness: "amp"},
		{harness: "pi"},
		{harness: "omp"},
		{harness: "opencode"},
	} {
		if got := deliverless(tc.harness); got != tc.deliverless {
			t.Errorf("%s: deliverless = %v, want %v", tc.harness, got, tc.deliverless)
		}
		if got := parkOnly(tc.harness); got != tc.park {
			t.Errorf("%s: parkOnly = %v, want %v", tc.harness, got, tc.park)
		}
		if got := formCapable(tc.harness); got != tc.form {
			t.Errorf("%s: formCapable = %v, want %v", tc.harness, got, tc.form)
		}
		if got := contextAtSessionStart(tc.harness); got != tc.sessionContext {
			t.Errorf("%s: contextAtSessionStart = %v, want %v",
				tc.harness, got, tc.sessionContext)
		}
		if got := mentionBlockCapable(tc.harness); got != tc.mentionBlock {
			t.Errorf("%s: mentionBlockCapable = %v, want %v",
				tc.harness, got, tc.mentionBlock)
		}
		if got := harnessTable[tc.harness].Translate != nil; got != tc.hasTranslate {
			t.Errorf("%s: has payload translator = %v, want %v",
				tc.harness, got, tc.hasTranslate)
		}
	}
}

// An unknown harness reads its payload with the claude-code capture and gets
// the default traits: a ◆ block at Stop, nothing else.
func TestUnknownHarnessFallsBackToClaudeCode(t *testing.T) {
	c := newHarnessCapture("brand-new-cli", contracts.Segment{"team": "revops"})
	if got := c.Harness(); got != "claude-code" {
		t.Fatalf("unknown harness capture = %q, want claude-code", got)
	}
	if deliverless("brand-new-cli") || parkOnly("brand-new-cli") ||
		formCapable("brand-new-cli") || contextAtSessionStart("brand-new-cli") ||
		mentionBlockCapable("brand-new-cli") {
		t.Fatal("unknown harness carries a trait it was never probed for")
	}
	payload := map[string]any{"hook_event_name": "Stop"}
	if got := translateHarnessPayload(payload, "brand-new-cli"); got["hook_event_name"] != "Stop" {
		t.Fatalf("unknown harness payload was translated: %v", got)
	}
}
