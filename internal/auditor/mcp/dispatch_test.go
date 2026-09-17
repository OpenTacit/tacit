// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"reflect"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// fullServer wires every seam, so tools/list reports the whole surface.
func fullServer() *Server {
	return &Server{
		Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		Insights: func(window string) (string, string, map[string]any, error) {
			return "<p>funnel</p>", "funnel text", map[string]any{"window": window}, nil
		},
		Org: func(window string) (string, map[string]any, error) {
			return "cohort spread over " + window, map[string]any{"window": window, "cohorts": 3}, nil
		},
		Map: func() (string, string, map[string]any, error) {
			return "<p>map</p>", "map text", map[string]any{"live_techniques": 5}, nil
		},
		Drafts: func() (string, string, map[string]any, error) {
			return "<p>drafts</p>", "drafts text", map[string]any{"count": 1}, nil
		},
		Usage: func(window string) (string, string, map[string]any, error) {
			return "<p>usage</p>", "usage text", map[string]any{"window": window}, nil
		},
		DraftAction: func(id, action string) (string, error) { return action + " " + id, nil },
		Segment:     contracts.Segment{"team": "revops"},
	}
}

// A tool name is a promise: a client that learned tacit_org before
// tacit_metrics arrived must keep getting the same answer, byte for byte, as
// the view that replaced it. One body serves both.
func TestLegacyOrgToolAnswersExactlyTheCohortsView(t *testing.T) {
	s := fullServer()

	legacy := s.Handle(req(t, 1, "tools/call",
		`{"name":"tacit_org","arguments":{"window":"7d"}}`)).Result
	view := s.Handle(req(t, 2, "tools/call",
		`{"name":"tacit_metrics","arguments":{"view":"cohorts","window":"7d"}}`)).Result
	if !reflect.DeepEqual(legacy, view) {
		t.Fatalf("tacit_org and tacit_metrics view=cohorts diverged:\n legacy: %v\n view:   %v",
			legacy, view)
	}
}

// The superseded names are dispatched but never advertised, and the advertised
// order is what a host shows a member — both are pinned here, because the
// dispatch table makes either easy to change by accident.
func TestToolsListOrderAndTheUnadvertisedLegacyNames(t *testing.T) {
	s := fullServer()

	tools := s.Handle(req(t, 1, "tools/list", "")).Result.(map[string]any)["tools"].([]any)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	want := []string{toolName, metricsToolName, draftsToolName, usageToolName, actionToolName}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tools/list = %v, want %v", names, want)
	}

	// Every legacy name still answers a call, and none of them is listed.
	for _, name := range []string{insightsToolName, orgToolName, mapToolName} {
		resp := s.Handle(req(t, 2, "tools/call", `{"name":"`+name+`","arguments":{}}`))
		if resp.Error != nil {
			t.Fatalf("%s should still be dispatched, got error %v", name, resp.Error)
		}
		if res, ok := resp.Result.(map[string]any); !ok || res["isError"].(bool) {
			t.Fatalf("%s returned an error result: %v", name, resp.Result)
		}
	}

	// An unwired legacy name is an unknown tool, not a half-answer.
	bare := &Server{Search: fullServer().Search}
	resp := bare.Handle(req(t, 3, "tools/call", `{"name":"tacit_org","arguments":{}}`))
	if resp.Error == nil || resp.Error.Message != "unknown tool" {
		t.Fatalf("unwired tacit_org = %v, want an unknown tool error", resp)
	}
	// tacit_metrics stays dispatchable on the same bare server: it reports its
	// own missing views rather than vanishing.
	metrics := bare.Handle(req(t, 4, "tools/call",
		`{"name":"tacit_metrics","arguments":{"view":"cohorts"}}`))
	if metrics.Error != nil || !metrics.Result.(map[string]any)["isError"].(bool) {
		t.Fatalf("bare tacit_metrics = %v, want a served result saying the view is missing", metrics)
	}
}
