// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

func TestParseDemoRequest(t *testing.T) {
	cases := []struct {
		prompt, want string
		ok           bool
	}{
		{"As a test, I want to view the current rate of adoption",
			"I want to view the current rate of adoption", true},
		{"as a test — show me last quarter's churn", "show me last quarter's churn", true},
		{"AS A TEST: deploy the thing", "deploy the thing", true},
		// Mid-sentence it is ordinary English and must not fire.
		{"run it as a test first", "", false},
		{"I'll do this as a test", "", false},
		{"As a test", "", false}, // nothing to build a technique from
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := parseDemoRequest(tc.prompt)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseDemoRequest(%q) = (%q, %v), want (%q, %v)", tc.prompt, got, ok, tc.want, tc.ok)
		}
	}
}

// The demonstration fires on every qualifying prompt, whatever the budget, the
// session history or the registry would have said. That is the whole point:
// a live suggestion arrives about once every three days, which is no basis for
// showing somebody the product.
func TestDemoFiresEveryTimeRegardlessOfBudget(t *testing.T) {
	// No candidates and a spent budget: nothing could produce a real suggestion.
	agent, log := newTestAgent(t, nil, Options{MaxPerWindow: 0, CooldownTurns: 1 << 20})
	agent.Handle(ev("SessionStart", nil), "claude-code")

	for i := 0; i < 3; i++ {
		resp := agent.Handle(ev("UserPromptSubmit", map[string]any{
			"prompt": "As a test, I want to view the current rate of adoption"}), "claude-code")
		if !strings.Contains(delivered(resp), questionHeader) {
			t.Fatalf("demo %d delivered nothing: %v", i+1, resp)
		}
	}
	// Fabricated techniques must never reach the funnel.
	if events := log.all(); len(events) != 0 {
		t.Fatalf("the demo recorded %d feedback event(s); it must record none: %+v", len(events), events)
	}
	if sum := agent.UsageSummary(0); sum.Totals.Shown != 0 {
		t.Errorf("the demo counted %d shown in the member's own log", sum.Totals.Shown)
	}
}

// A demonstration says it is one. The product's claim is that its numbers are
// measured; showing invented ones without saying so sells the opposite.
func TestDemoDeliverySaysItIsFabricated(t *testing.T) {
	for _, harness := range []string{"claude-code", "codex"} {
		agent, _ := newTestAgent(t, nil, Options{})
		agent.Handle(ev("SessionStart", nil), harness)
		out := delivered(agent.Handle(ev("UserPromptSubmit", map[string]any{
			"prompt": "As a test, I want to view the current rate of adoption"}), harness))
		if !strings.Contains(out, "demonstration") {
			t.Errorf("%s: delivery does not disclose that the technique is invented: %q", harness, out)
		}
		if !strings.Contains(out, "adoption") {
			t.Errorf("%s: the technique ignored what the member asked about: %q", harness, out)
		}
	}
}

// The fabricated id is namespaced, so anything that ever escapes this path is
// identifiable in the event log at a glance.
func TestDemoTechniqueIsNamespacedAndOrgScoped(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{})
	technique, _ := agent.demoTechnique("view the current rate of adoption")
	if !strings.HasPrefix(technique.TechniqueID, demoIDPrefix) {
		t.Errorf("demo id not namespaced: %q", technique.TechniqueID)
	}
	if technique.Scope != "org" {
		t.Errorf("a demo technique must be org-scoped — the point is what a general model cannot know: %q", technique.Scope)
	}
	if technique.Recipe == "" {
		t.Error("a technique with no recipe demonstrates nothing")
	}
}

// The invented evidence must clear the display floor, or the demonstration
// shows the cold-start message instead of the product.
func TestDemoEvidenceRendersAsMeasured(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{})
	technique, _ := agent.demoTechnique("view the current rate of adoption")
	line := contracts.EvidenceLine(technique.Outcomes)
	if !strings.Contains(line, "helped") || !strings.Contains(line, "n=") {
		t.Fatalf("demo evidence does not render as a measured line: %q", line)
	}
}
