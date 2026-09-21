// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/sessionhash"
	"github.com/opentacit/tacit/pkg/jsonschema"
	"github.com/opentacit/tacit/schemas"
)

type sketchLog struct {
	mu       sync.Mutex
	sketches []contracts.Sketch
}

func (s *sketchLog) sink(sk contracts.Sketch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sketches = append(s.sketches, sk)
}

func (s *sketchLog) all() []contracts.Sketch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.Sketch, len(s.sketches))
	copy(out, s.sketches)
	return out
}

func sketchAgent(t *testing.T) (*Agent, *sketchLog, *feedbackLog) {
	t.Helper()
	log := &sketchLog{}
	agent, fb := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{Sketch: log.sink, SketchSalt: "org-salt"})
	return agent, log, fb
}

func adopt(agent *Agent) {
	driveToSuggestion(agent)
	// behavioral adoption: the recipe's tool shows up in a PreToolUse
	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "mcp__warehouse__query", "tool_input": map[string]any{"q": "select"},
	}), "claude-code")
}

func TestSketchEmittedOnFirstAdoptionOnly(t *testing.T) {
	agent, log, _ := sketchAgent(t)
	adopt(agent)
	if len(log.all()) != 1 {
		t.Fatalf("sketches after adoption: %d", len(log.all()))
	}
	// same technique adopted again: no duplicate sketch
	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "mcp__warehouse__query", "tool_input": map[string]any{}}), "claude-code")
	if len(log.all()) != 1 {
		t.Fatalf("duplicate sketch: %d", len(log.all()))
	}

	sk := log.all()[0]
	if sk.TechniqueID != "use-internal-data-connector" || sk.Harness != "claude-code" {
		t.Fatalf("sketch fields: %+v", sk)
	}
	if sk.SessionHash == "" || strings.Contains(sk.SessionHash, "sess_1") {
		t.Fatalf("session hash leaks the session id: %q", sk.SessionHash)
	}
	if sk.SessionHash != sessionhash.Hash("org-salt", "claude-code:sess_1") {
		t.Fatal("hash not salted deterministically")
	}
	if sk.Trigger == "" || sk.Move == "" {
		t.Fatalf("empty sketch content: %+v", sk)
	}

	raw, _ := json.Marshal(sk)
	schemaRaw, _ := schemas.Get("sketch.schema.json")
	s, err := jsonschema.Parse(schemaRaw)
	if err != nil {
		t.Fatal(err)
	}
	if errs := s.ValidateBytes(raw); len(errs) != 0 {
		t.Fatalf("sketch violates its schema: %v\n%s", errs, raw)
	}
}

func TestNoConsentNoSketches(t *testing.T) {
	agent, fb := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	adopt(agent)
	if n := len(fb.byStage("adopted")); n != 1 {
		t.Fatalf("adoption still records: %d", n)
	}
	// nothing to assert on a nil sink beyond "no panic": consent off is the default
}

func TestSketchContentIsScrubbed(t *testing.T) {
	log := &sketchLog{}
	leaky := orgCandidate()
	leaky.Recipe = "call with api_key = \"9f8e7d6c5b4a39281706aabb\" then query"
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{leaky},
		Options{Sketch: log.sink, SketchSalt: "s"})
	driveToSuggestion(agent)
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "that helped a lot"}), "claude-code")
	sketches := log.all()
	if len(sketches) != 1 {
		t.Fatalf("sketches: %d", len(sketches))
	}
	if strings.Contains(sketches[0].Move, "9f8e7d6c") {
		t.Fatalf("secret in sketch: %s", sketches[0].Move)
	}
	if !strings.Contains(sketches[0].Move, "[redacted:") {
		t.Fatalf("no redaction marker: %s", sketches[0].Move)
	}
}

func TestExplicitApplyEmitsSketch(t *testing.T) {
	agent, log, _ := sketchAgent(t)
	driveToSuggestion(agent)
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "continue"}), "claude-code")
	agent.Handle(askAnswer(optApply), "claude-code")
	if len(log.all()) != 1 {
		t.Fatalf("apply answer sketches: %d", len(log.all()))
	}
}
