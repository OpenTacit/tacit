// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// usageServer wires a Usage closure returning a canned member-local app payload
// — the tacit_usage tool renders from the machine's log (via hooks.RenderUsageApp
// in production), so unlike the other apps it needs no registry.
func usageServer() *Server {
	return &Server{
		Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		Usage: func(window string) (string, string, map[string]any, error) {
			if window == "" {
				window = "30d"
			}
			return "<!doctype html><title>Your Tacit usage</title><div class=\"tiles\">app</div>",
				"Your Tacit usage (" + window + ")\n  Queries: 5",
				map[string]any{"window": window, "totals": map[string]any{"queries": 5}}, nil
		},
	}
}

func TestUsageToolAdvertisedWhenWired(t *testing.T) {
	s := usageServer()
	caps := s.Handle(req(t, 1, "initialize", `{"protocolVersion":"2025-03-26"}`)).Result.(map[string]any)["capabilities"].(map[string]any)
	if caps["resources"] == nil || caps["extensions"] == nil {
		t.Fatalf("Usage wired but app capabilities not advertised: %v", caps)
	}
	tools := s.Handle(req(t, 2, "tools/list", "")).Result.(map[string]any)["tools"].([]any)
	found := false
	for _, tl := range tools {
		if tl.(map[string]any)["name"] == usageToolName {
			found = true
		}
	}
	if !found {
		t.Fatalf("tacit_usage missing from tools/list: %v", tools)
	}
}

func TestUsageToolCallReturnsAppAndText(t *testing.T) {
	s := usageServer()
	resp := s.Handle(req(t, 3, "tools/call", `{"name":"tacit_usage","arguments":{"window":"7d"}}`))
	result := resp.Result.(map[string]any)
	if result["isError"].(bool) {
		t.Fatalf("usage call errored: %v", result)
	}
	content := result["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("want text + resource content, got %d items", len(content))
	}
	txt := content[0].(map[string]any)
	if txt["type"] != "text" || !strings.Contains(txt["text"].(string), "7d") {
		t.Fatalf("text fallback wrong: %v", txt)
	}
	res := content[1].(map[string]any)["resource"].(map[string]any)
	if res["uri"] != usageResourceURI || res["mimeType"] != appProfileMime ||
		!strings.Contains(res["text"].(string), "<!doctype html") {
		t.Fatalf("ui resource wrong: %v", res)
	}
	if res["_meta"] == nil {
		t.Fatal("inline ui resource missing _meta render config")
	}
	if sc, ok := result["structuredContent"].(map[string]any); !ok || sc["window"] != "7d" {
		t.Fatalf("structuredContent missing/wrong: %v", result["structuredContent"])
	}
	if result["_meta"].(map[string]any)["ui"].(map[string]any)["resourceUri"] != usageResourceURI {
		t.Fatalf("_meta.ui.resourceUri wrong: %v", result["_meta"])
	}
}

func TestUsageResourceReadAndListing(t *testing.T) {
	s := usageServer()
	list := s.Handle(req(t, 4, "resources/list", "")).Result.(map[string]any)["resources"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["uri"] != usageResourceURI {
		t.Fatalf("resources/list = %v", list)
	}
	read := s.Handle(req(t, 5, "resources/read", `{"uri":"`+usageResourceURI+`"}`))
	contents := read.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
	if contents["mimeType"] != appProfileMime || !strings.Contains(contents["text"].(string), "tiles") {
		t.Fatalf("resources/read wrong: %v", contents)
	}
}

// Without Usage wired, the tool and its resource stay hidden.
func TestUsageHiddenWhenUnwired(t *testing.T) {
	s := testServer(contracts.EvidenceBlock{}, nil)
	tools := s.Handle(req(t, 1, "tools/list", "")).Result.(map[string]any)["tools"].([]any)
	for _, tl := range tools {
		if tl.(map[string]any)["name"] == usageToolName {
			t.Fatal("tacit_usage should be hidden without Usage wired")
		}
	}
	if s.Handle(req(t, 2, "tools/call", `{"name":"tacit_usage","arguments":{}}`)).Error == nil {
		t.Fatal("calling an unwired tacit_usage should error")
	}
}
