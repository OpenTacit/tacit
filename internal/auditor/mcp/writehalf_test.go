// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// A member who reached this registry by adding one /mcp URL to their chat tool
// and signing in could search the playbook and read its metrics, and could add
// nothing to either. An entire population consuming a corpus it cannot feed is
// a corpus that stops being true, so the connector tier now carries the write
// half: contribute a technique, and report what became of one.
func TestConnectorTierCanContributeAndReportOutcomes(t *testing.T) {
	var filed Contribution
	var gotID, gotStage, gotReason string
	s := &Server{
		Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		Contribute: func(c Contribution) (string, error) {
			filed = c
			return "Filed it as a draft.", nil
		},
		Feedback: func(id, stage, reason string) (string, error) {
			gotID, gotStage, gotReason = id, stage, reason
			return "Recorded.", nil
		},
	}

	tools := listedToolNames(t, s)
	for _, want := range []string{"tacit_contribute", "tacit_feedback"} {
		if !tools[want] {
			t.Errorf("%s missing from tools/list", want)
		}
	}

	resp := s.Handle(req(t, 1, "tools/call",
		`{"name":"tacit_contribute","arguments":{"name":"Name the check","description":"d","recipe":"r","scope":"org"}}`))
	if resp.Error != nil {
		t.Fatalf("contribute: %v", resp.Error)
	}
	if filed.Name != "Name the check" || filed.Scope != "org" || filed.Recipe != "r" {
		t.Errorf("the contribution did not arrive intact: %+v", filed)
	}

	resp = s.Handle(req(t, 2, "tools/call",
		`{"name":"tacit_feedback","arguments":{"technique_id":"name-the-check","stage":"helped"}}`))
	if resp.Error != nil {
		t.Fatalf("feedback: %v", resp.Error)
	}
	if gotID != "name-the-check" || gotStage != "helped" {
		t.Errorf("feedback lost its arguments: %s %s %s", gotID, gotStage, gotReason)
	}
}

// Half a technique is not a technique, and a stage the evidence model does not
// know is not evidence. Both refuse before anything is written.
func TestWriteToolsRefuseIncompleteCalls(t *testing.T) {
	called := false
	s := &Server{
		Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		Contribute: func(Contribution) (string, error) { called = true; return "", nil },
		Feedback:   func(string, string, string) (string, error) { called = true; return "", nil },
	}
	for _, args := range []string{
		`{"name":"tacit_contribute","arguments":{"name":"n"}}`,
		`{"name":"tacit_feedback","arguments":{"technique_id":"t","stage":"vibes"}}`,
		`{"name":"tacit_feedback","arguments":{"stage":"helped"}}`,
	} {
		resp := s.Handle(req(t, 1, "tools/call", args))
		result, _ := resp.Result.(map[string]any)
		if isErr, _ := result["isError"].(bool); !isErr {
			t.Errorf("%s was accepted", args)
		}
	}
	if called {
		t.Error("an incomplete call reached the registry")
	}
}

// A registry that does not wire these seams must not advertise them, and must
// answer "unknown tool" rather than pretending.
func TestWriteToolsAreAbsentWhenNotWired(t *testing.T) {
	s := &Server{Search: func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, nil
	}}
	tools := listedToolNames(t, s)
	for _, gone := range []string{"tacit_contribute", "tacit_feedback"} {
		if tools[gone] {
			t.Errorf("%s advertised with nothing behind it", gone)
		}
	}
	if s.Handle(req(t, 1, "tools/call", `{"name":"tacit_contribute","arguments":{}}`)).Error == nil {
		t.Error("an unwired tacit_contribute answered instead of refusing")
	}
}

func listedToolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	resp := s.Handle(req(t, 99, "tools/list", `{}`))
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range out.Tools {
		names[tool.Name] = true
	}
	if len(names) == 0 {
		t.Fatalf("tools/list returned nothing: %s", strings.TrimSpace(string(raw)))
	}
	return names
}
