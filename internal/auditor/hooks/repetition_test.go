// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"strings"
	"sync"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// correctionLLM is stubLLM plus the one distillation this feature needs.
type correctionLLM struct {
	stubLLM
	mu   sync.Mutex
	saw  []string
	move string
}

func (c *correctionLLM) InferRepeatedCorrection(message string) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.saw = append(c.saw, message)
	if c.move == "" {
		return "", "", nil
	}
	return "Freeing a port that is still held", c.move, nil
}

func (c *correctionLLM) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.saw...)
}

type contributionLog struct {
	mu   sync.Mutex
	sent []map[string]any
}

func (l *contributionLog) sink(body map[string]any) (map[string]any, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent = append(l.sent, body)
	return map[string]any{"id": "draft_1"}, nil
}

func (l *contributionLog) all() []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]map[string]any(nil), l.sent...)
}

func correctionAgent(t *testing.T, move string) (*Agent, *correctionLLM, *contributionLog) {
	t.Helper()
	llm := &correctionLLM{move: move}
	contribs := &contributionLog{}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, nil
	}
	agent := NewAgent(provider, llm, func([]contracts.FeedbackEventDraft) error { return nil },
		contribs.sink, Options{
			Segment: contracts.Segment{"team": "revops"}, RunAsync: inline,
			UsageLogPath: t.TempDir() + "/usage.jsonl", SessionSalt: "salt",
		})
	return agent, llm, contribs
}

// say drives one member turn through the real hook path.
func say(agent *Agent, session, prompt string) {
	agent.Handle(map[string]any{
		"hook_event_name": "UserPromptSubmit", "session_id": session, "prompt": prompt,
	}, "claude-code")
}

// The whole feature: a correction repeated across sessions becomes a draft, and
// the registry's cohort threshold — which one person can never clear — is
// replaced by a repetition threshold they can.
func TestRepeatedCorrectionRaisesADraft(t *testing.T) {
	agent, llm, contribs := correctionAgent(t,
		"Free the port with fuser -k PORT/tcp rather than killing processes by name.")

	sayings := []string{
		"no, use fuser -k on the port rather than pkill",
		"I said use fuser -k on the port, not pkill",
		"stop using pkill — fuser -k on the port",
		"instead of pkill, use fuser -k on the port",
	}
	for i, s := range sayings {
		session := "sess" + string(rune('a'+i))
		say(agent, session, "start the work") // turn 1: a correction needs something to correct
		say(agent, session, s)
	}

	sent := contribs.all()
	if len(sent) != 1 {
		t.Fatalf("filed %d drafts, want exactly 1: %+v", len(sent), sent)
	}
	draft := sent[0]
	if !strings.Contains(draft["recipe"].(string), "fuser -k") {
		t.Fatalf("the recipe is not the distilled move: %+v", draft)
	}
	// The description states the evidence as one person's own record.
	desc := draft["description"].(string)
	if !strings.Contains(desc, "4 times") {
		t.Fatalf("the count is not stated: %q", desc)
	}
	if !strings.Contains(desc, "by nobody else") {
		t.Fatalf("the sample is not owned up to: %q", desc)
	}
	// The model saw the LIVE saying, which is the reason the ledger may keep
	// only hashes of the rest.
	if seen := llm.seen(); len(seen) != 1 || !strings.Contains(seen[0], "fuser") {
		t.Fatalf("the distiller saw %v", seen)
	}
}

// Below the threshold nothing is raised and no model is called. A member who
// said something twice has not established anything.
func TestRepeatedCorrectionWaitsForTheThreshold(t *testing.T) {
	agent, llm, contribs := correctionAgent(t, "Some move.")
	for i := 0; i < CorrectionThreshold-1; i++ {
		session := "sess" + string(rune('a'+i))
		say(agent, session, "start the work")
		say(agent, session, "no, use fuser -k on the port rather than pkill")
	}
	if got := contribs.all(); len(got) != 0 {
		t.Fatalf("filed a draft below the threshold: %+v", got)
	}
	if got := llm.seen(); len(got) != 0 {
		t.Fatalf("called the model below the threshold: %v", got)
	}
}

// Once raised, never again. A member who keeps saying it — because the draft is
// still sitting in review — must not collect a queue of identical drafts.
func TestRepeatedCorrectionRaisesOnce(t *testing.T) {
	agent, _, contribs := correctionAgent(t, "Some move.")
	for i := 0; i < CorrectionThreshold+4; i++ {
		session := "sess" + string(rune('a'+i))
		say(agent, session, "start the work")
		say(agent, session, "no, use fuser -k on the port rather than pkill")
	}
	if got := contribs.all(); len(got) != 1 {
		t.Fatalf("filed %d drafts, want 1", len(got))
	}
}

// A distiller that finds nothing reusable files nothing. Silence is the common
// and correct answer, and a draft written anyway would be a platitude with the
// member's own name on it.
func TestRepeatedCorrectionStaysSilentWithNoMove(t *testing.T) {
	agent, _, contribs := correctionAgent(t, "")
	for i := 0; i < CorrectionThreshold+1; i++ {
		session := "sess" + string(rune('a'+i))
		say(agent, session, "start the work")
		say(agent, session, "no, use fuser -k on the port rather than pkill")
	}
	if got := contribs.all(); len(got) != 0 {
		t.Fatalf("filed a draft with nothing to say: %+v", got)
	}
}

// The first turn of a session is a standing instruction, not a repair. Counting
// it would make every member's habits look like a running argument.
func TestFirstTurnIsNotACorrection(t *testing.T) {
	agent, _, contribs := correctionAgent(t, "Some move.")
	for i := 0; i < CorrectionThreshold+2; i++ {
		say(agent, "sess"+string(rune('a'+i)), "don't use pkill, use fuser -k on the port")
	}
	if got := contribs.all(); len(got) != 0 {
		t.Fatalf("an opening instruction was mined as a correction: %+v", got)
	}
}
