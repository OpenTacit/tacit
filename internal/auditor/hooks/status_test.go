// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A member who has just connected has attempted no synthesis, so nothing has
// diagnosed anything — and the status line said nothing at all. Silence in the
// first session is where a member concludes OpenTacit has nothing to say, so the
// missing key is reported from the key's absence, before any turn runs.
func TestLLMHealthSaysNoKeyBeforeAnyTurn(t *testing.T) {
	a := &Agent{llm: noKeyLLM{}}
	d, _, _, _ := a.LLMHealth()
	if d.State != "no-key" {
		t.Errorf("state = %q before any synthesis, want no-key", d.State)
	}
	if !d.NeedsOperator {
		t.Error("a missing key does not ask the member to act")
	}
	if d.Remedy == "" {
		t.Error("no remedy offered")
	}

	// With a key, nothing has happened yet and we do not pretend otherwise.
	b := &Agent{llm: keyedLLM{}}
	if d, _, _, _ := b.LLMHealth(); d.State != "unknown" {
		t.Errorf("state = %q with a key and no turn yet, want unknown", d.State)
	}
}

type noKeyLLM struct{ Synthesizer }

func (noKeyLLM) UpgradeAvailable() bool { return true }

type keyedLLM struct{ Synthesizer }

func (keyedLLM) UpgradeAvailable() bool { return false }

// The cohort ask fired as soon as a machine was connected and renewed daily
// until a cohort was set, with no way to answer no. So the first thing a new
// member heard was a request for data cleanup, and a member who did not want a
// cohort was asked every day forever.
func TestCohortAskWaitsForFirstValueAndTakesNoForAnAnswer(t *testing.T) {
	newAgent := func() *Agent {
		return &Agent{
			memory: loadTechniqueMemory(filepath.Join(t.TempDir(), "memory.json")),
			opts: Options{
				RegistryConfigured: true,
				RegistryURL:        "https://reg.test",
				Now:                time.Now,
			},
		}
	}

	// Freshly connected: nothing shown, nothing adopted, so nothing asked.
	a := newAgent()
	if ask := a.segmentAsk("claude-code"); ask != "" {
		t.Errorf("asked for a cohort before the member met anything:\n%s", ask)
	}

	// Once they have met a technique, the ask is due.
	a.memory.NoteAdopted("tech-1", time.Now())
	ask := a.segmentAsk("claude-code")
	if ask == "" {
		t.Fatal("no cohort ask after the member adopted a technique")
	}
	if !strings.Contains(ask, "skip") {
		t.Errorf("the ask offers no way to decline:\n%s", ask)
	}

	// Declining ends it, permanently — not for a day.
	b := newAgent()
	b.memory.NoteAdopted("tech-1", time.Now())
	if err := DeclineSegment(b.memory.path, b.opts.RegistryURL); err != nil {
		t.Fatal(err)
	}
	b.memory = loadTechniqueMemory(b.memory.path)
	b.opts.Now = func() time.Time { return time.Now().Add(30 * 24 * time.Hour) }
	if ask := b.segmentAsk("claude-code"); ask != "" {
		t.Errorf("asked again a month after the member declined:\n%s", ask)
	}
}

// Pause means WATCH NOTHING. A pause the member cannot verify is not a pause,
// so it is checked before the event is even translated: no capture, no
// retrieval, no delivery, no memory of the turn.
func TestPausedAgentObservesNothing(t *testing.T) {
	paused := false
	a, _ := newTestAgent(t, nil, Options{
		RegistryConfigured: true,
		RegistryURL:        "https://reg.test",
		Paused:             func() bool { return paused },
	})
	prompt := func() HookResponse {
		return a.Handle(map[string]any{
			"hook_event_name": "UserPromptSubmit", "session_id": "s1",
			"prompt": "refactor the parser",
		}, "claude-code")
	}

	// Running normally, the turn is observed: the agent keeps session state.
	prompt()
	a.mu.Lock()
	seenWhileRunning := len(a.sessions)
	a.mu.Unlock()
	if seenWhileRunning == 0 {
		t.Fatal("the agent recorded no session while running; this test cannot tell pause from nothing")
	}

	// Paused, a turn leaves no trace and produces no response.
	paused = true
	before := seenWhileRunning
	if resp := prompt(); len(resp) != 0 {
		t.Errorf("a paused agent answered: %v", resp)
	}
	a.Handle(map[string]any{"hook_event_name": "SessionStart", "session_id": "s2"}, "claude-code")
	a.mu.Lock()
	after := len(a.sessions)
	a.mu.Unlock()
	if after != before {
		t.Errorf("a paused agent opened session state (%d -> %d)", before, after)
	}

	// And resuming needs no restart: the next turn is observed again.
	paused = false
	if resp := prompt(); resp == nil && len(a.sessions) == before {
		t.Error("the agent stayed silent after the pause was lifted")
	}
}

// No pull-only client may promise automatic suggestions. Copilot renders no
// hook output at all and Cursor has no visible same-turn channel, and the
// guides described one delivery story for every tool.
func TestCapabilitySaysWhatEachToolCanActuallyDo(t *testing.T) {
	if got := Capability("copilot"); !strings.Contains(got, "cannot interrupt") {
		t.Errorf("copilot renders no hook output, and its capability line says: %q", got)
	}
	if got := Capability("cursor"); !strings.Contains(got, "next prompt") {
		t.Errorf("cursor delivers at the next prompt, and its capability line says: %q", got)
	}
	if got := Capability("claude-code"); strings.Contains(got, "cannot") {
		t.Errorf("claude-code can deliver in session, and its capability line says: %q", got)
	}
	// Every wired harness gets a sentence; an unknown one gets none, so a
	// caller prints nothing rather than a guess.
	for name := range harnessTable {
		if Capability(name) == "" {
			t.Errorf("%s has no capability sentence", name)
		}
	}
	if Capability("emacs-doctor") != "" {
		t.Error("an unknown harness was given a capability sentence")
	}
}
