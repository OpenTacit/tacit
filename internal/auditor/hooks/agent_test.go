// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// orgCandidate mirrors the Python suite: the recipe names "@warehouse
// connector" so a later mcp__warehouse__query tool call is detectable as
// adoption.
func orgCandidate() contracts.EvidenceCandidate {
	return contracts.EvidenceCandidate{
		TechniqueID: "use-internal-data-connector",
		Name:        "Query live warehouse data",
		Scope:       "org",
		Recipe:      "Use the @warehouse connector: query revenue.pipeline where quarter = ...",
		AppliesWhen: "The user pasted tabular data that lives in an internal source.",
	}
}

type stubLLM struct{ text string }

func (s stubLLM) Synthesize(_ string, ev contracts.EvidenceBlock, _ bool) (string, error) {
	if s.text != "" {
		return s.text, nil
	}
	top := ev.Candidates[0]
	return "Try: " + top.Name + ". " + top.Recipe, nil
}

type feedbackLog struct {
	mu     sync.Mutex
	events []contracts.FeedbackEventDraft
}

func (f *feedbackLog) sink(events []contracts.FeedbackEventDraft) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, events...)
	return nil
}

func (f *feedbackLog) byStage(stage string) []contracts.FeedbackEventDraft {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.FeedbackEventDraft
	for _, e := range f.events {
		if e.Stage == stage {
			out = append(out, e)
		}
	}
	return out
}

func (f *feedbackLog) all() []contracts.FeedbackEventDraft {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contracts.FeedbackEventDraft, len(f.events))
	copy(out, f.events)
	return out
}

func (f *feedbackLog) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = nil
}

var inline = func(fn func()) { fn() } // deterministic synchronous tests

// seenChars records every characterization the agent sent to retrieval, so a
// test can assert on what the registry would actually have received.
type seenChars struct {
	mu    sync.Mutex
	chars []contracts.Characterization
}

func (s *seenChars) add(ch contracts.Characterization) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chars = append(s.chars, ch)
}

func (s *seenChars) last() (contracts.Characterization, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.chars) == 0 {
		return contracts.Characterization{}, false
	}
	return s.chars[len(s.chars)-1], true
}

func newTestAgent(t *testing.T, cands []contracts.EvidenceCandidate,
	opts Options) (*Agent, *feedbackLog) {
	t.Helper()
	agent, log, _ := newTestAgentSeeing(t, cands, opts)
	return agent, log
}

func newTestAgentSeeing(t *testing.T, cands []contracts.EvidenceCandidate,
	opts Options) (*Agent, *feedbackLog, *seenChars) {
	t.Helper()
	seen := &seenChars{}
	provider := func(ch contracts.Characterization) (contracts.EvidenceBlock, error) {
		seen.add(ch)
		return contracts.EvidenceBlock{Candidates: cands,
			Meta: map[string]any{"thin": true}}, nil
	}
	log := &feedbackLog{}
	if opts.Segment == nil {
		opts.Segment = contracts.Segment{"team": "revops"}
	}
	if opts.RunAsync == nil {
		opts.RunAsync = inline
	}
	agent := NewAgent(provider, stubLLM{}, log.sink, nil, opts)
	return agent, log, seen
}

// The hook agent is where essentially all real evidence requests come from, and
// it has its own audit path (auditAtStop) — it does NOT go through audit.Run.
// The audit id must be minted BEFORE retrieval and travel on the
// characterization, because it is the join key between what the interaction WAS
// (the registry files this as an audit fact) and how it turned OUT (the feedback
// events below carry the same id). It used to be minted after the evidence call,
// so the registry saw every characterization with nothing to file it under.
func TestRetrievalCarriesTheAuditIDThatFeedbackWillUse(t *testing.T) {
	agent, log, seen := newTestAgentSeeing(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{})
	driveToSuggestion(agent)

	ch, ok := seen.last()
	if !ok {
		t.Fatal("the agent never called retrieval")
	}
	if ch.AuditID == "" {
		t.Fatal("characterization reached retrieval with no audit_id: the registry " +
			"cannot key the audit fact, so the context half of this audit is lost")
	}

	shown := log.byStage("shown")
	if len(shown) == 0 {
		t.Fatal("expected a shown event")
	}
	// The join: the id retrieval saw is the id the outcome is reported against.
	// If these ever diverge, audit facts and feedback events describe the same
	// interaction under two different keys and never meet.
	if shown[0].AuditID != ch.AuditID {
		t.Fatalf("audit id split in two: retrieval saw %q, feedback reports %q",
			ch.AuditID, shown[0].AuditID)
	}
}

func TestRetrievalCarriesStableHarnessScopedSessionHash(t *testing.T) {
	agent, _, seen := newTestAgentSeeing(t, nil, Options{SessionSalt: "org-salt", CooldownTurns: -1, CooldownFor: -1})
	driveToSuggestion(agent)
	agent.Handle(ev("Stop", nil), "claude-code")
	if len(seen.chars) != 2 || seen.chars[0].SessionHash == "" || seen.chars[0].SessionHash != seen.chars[1].SessionHash {
		t.Fatalf("same session hashes = %+v", seen.chars)
	}
	agent.Handle(map[string]any{"hook_event_name": "Stop", "session_id": "sess_1"}, "codex")
	if seen.chars[2].SessionHash == seen.chars[0].SessionHash {
		t.Fatal("harness namespace was not included in session hash")
	}
}

func ev(name string, fields map[string]any) map[string]any {
	payload := map[string]any{"hook_event_name": name, "session_id": "sess_1"}
	for k, v := range fields {
		payload[k] = v
	}
	return payload
}

// driveToSuggestion runs SessionStart -> prompt -> Stop on claude-code, and
// then the one further prompt a FORM delivery needs: a form has to be raised by
// the model, and only a model-facing envelope can ask for that, which Stop does
// not carry (formCapable, harness.go). So the suggestion parks at Stop and rides
// the next prompt. Returns whichever response actually carried it.
func driveToSuggestion(agent *Agent) HookResponse {
	return driveToSuggestionOn(agent, "claude-code")
}

// driveToSuggestionOn is the same on a named harness — used by the tests that
// are about the ◆ BLOCK, which still ships everywhere a native question form
// does not exist, and which delivers same-turn at Stop there.
func driveToSuggestionOn(agent *Agent, harness string) HookResponse {
	agent.Handle(ev("SessionStart", map[string]any{"source": "startup"}), harness)
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), harness)
	stop := agent.Handle(ev("Stop", nil), harness)
	if len(stop) > 0 || !formCapable(harness) {
		return stop
	}
	return agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on then"}), harness)
}

// delivered returns the suggestion text out of whichever envelope carried it:
// systemMessage for a ◆ block, additionalContext for a form request. Tests
// about WHAT was recorded use this; tests about HOW it looked read the envelope
// they mean directly.
func delivered(resp HookResponse) string {
	if msg, ok := resp["systemMessage"].(string); ok && msg != "" {
		return msg
	}
	out, _ := resp["hookSpecificOutput"].(map[string]any)
	ctx, _ := out["additionalContext"].(string)
	return ctx
}

func TestSuggestionCarriesTacitSpine(t *testing.T) {
	// Every line of the block must carry the ┃ spine so it reads as one distinct
	// OpenTacit artifact under the harness's "Stop says:" prefix — including the
	// wrapped continuation lines of a long why/try field.
	longWhy := "this is a deliberately long rationale that must wrap across more " +
		"than one line so the continuation clearly still carries the tacit spine marker"
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{})
	// Swap in a stub that returns the long why to force wrapping.
	agent.llm = stubLLM{text: longWhy}

	msg := delivered(driveToSuggestionOn(agent, "codex"))
	if !strings.HasPrefix(msg, "\n") {
		t.Fatalf("block should lead with a blank line, got: %q", msg[:min(20, len(msg))])
	}
	lines := strings.Split(strings.TrimPrefix(msg, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected a multi-line wrapped block, got %d lines:\n%s", len(lines), msg)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, tacitSpine) {
			t.Fatalf("line %d missing the ┃ spine: %q", i, ln)
		}
	}
	if !strings.HasPrefix(lines[0], tacitSpine+Mark()) {
		t.Fatalf("banner line = %q, want the ◆ marker after the spine", lines[0])
	}
}

func TestClassifyReactionMatchesFooterReplies(t *testing.T) {
	// The suggestion footer instructs: reply "helped" / "not relevant". Both
	// bare replies MUST classify — otherwise ambient feedback silently no-ops
	// and the technique's helped/dismissed counts never move.
	for _, tc := range []struct {
		msg   string
		stage string
	}{
		{"helped", "helped"},
		{"Helped", "helped"},
		{"that helped", "helped"},
		{"not relevant", "dismissed"},
	} {
		stage, _, conf, ok := classifyReaction(tc.msg)
		if !ok || stage != tc.stage {
			t.Fatalf("classifyReaction(%q) = (stage=%q, ok=%v), want stage %q", tc.msg, stage, ok, tc.stage)
		}
		if tc.msg == "helped" && conf != "explicit" {
			t.Fatalf("bare %q should be explicit confidence, got %q", tc.msg, conf)
		}
	}
}

func TestBareHelpedReplyRecordsHelped(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent) // shows the suggestion
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "helped"}), "claude-code")
	deadline := time.Now().Add(2 * time.Second)
	for len(log.byStage("helped")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // feedback posts async
	}
	if n := len(log.byStage("helped")); n != 1 {
		t.Fatalf(`bare "helped" reply recorded %d helped events, want 1`, n)
	}
}

func TestBehavioralAdoptionInfersHelpedAfterConfirmWindow(t *testing.T) {
	// The helped denominator must fill from behaviour, not only from members
	// typing "helped": a member who uses a suggested move and keeps working
	// without dismissing it confirms — weakly, at inferred confidence — that it
	// helped.
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline})
	driveToSuggestion(agent) // shows use-internal-data-connector

	// Turn: the member uses the move -> behavioral adoption, no helped yet.
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "let me query the warehouse connector"}), "claude-code")
	if n := len(log.byStage("adopted")); n != 1 {
		t.Fatalf("expected 1 inferred adoption, got %d", n)
	}
	if n := len(log.byStage("helped")); n != 0 {
		t.Fatalf("helped must not fire on the adoption turn, got %d", n)
	}

	// The member keeps working across the confirmation window, never dismissing.
	for i := 0; i < HelpedConfirmTurns; i++ {
		agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on with the next step"}), "claude-code")
	}
	helped := log.byStage("helped")
	if len(helped) != 1 {
		t.Fatalf("expected 1 inferred helped after the confirm window, got %d", len(helped))
	}
	if helped[0].Confidence != "inferred" {
		t.Errorf("passive helped must be inferred confidence, got %q", helped[0].Confidence)
	}
	if helped[0].TechniqueID != "use-internal-data-connector" {
		t.Errorf("helped on the wrong technique: %q", helped[0].TechniqueID)
	}
	if v, _ := helped[0].Value.(bool); !v {
		t.Errorf("inferred helped should carry value=true, got %v", helped[0].Value)
	}
	// The implied adopted must NOT be re-emitted (adoption was already counted):
	// the helped-rate denominator stays 1.
	if n := len(log.byStage("adopted")); n != 1 {
		t.Fatalf("passive helped must not add a second adopted, got %d", n)
	}
}

func TestDismissedAdoptionNeverInfersHelped(t *testing.T) {
	// Signal quality: a technique the member used but then dismissed must never be
	// promoted to helped — an explicit negative verdict supersedes inference.
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline})
	driveToSuggestion(agent)
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "query the warehouse connector"}), "claude-code")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "not relevant"}), "claude-code")
	for i := 0; i < HelpedConfirmTurns+1; i++ {
		agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on"}), "claude-code")
	}
	if n := len(log.byStage("helped")); n != 0 {
		t.Fatalf("a dismissed technique must never infer helped, got %d", n)
	}
	if n := len(log.byStage("dismissed")); n != 1 {
		t.Fatalf("the dismissal should still be recorded once, got %d", n)
	}
}

func TestStopDeliversSuggestionSameTurn(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	resp := driveToSuggestionOn(agent, "codex")
	msg := delivered(resp)
	if !strings.Contains(msg, Mark()) || !strings.Contains(msg, "use-internal-data-connector") {
		t.Fatalf("same-turn delivery missing: %v", resp)
	}
	if _, present := resp["hookSpecificOutput"]; present {
		t.Fatal("Stop delivery must be member-visible only (no hookSpecificOutput)")
	}
	shown := log.byStage("shown")
	if len(shown) != 1 || shown[0].TechniqueID != "use-internal-data-connector" {
		t.Fatalf("shown funnel: %+v", shown)
	}
}

type blockingLLM struct {
	release chan struct{}
	accept  string
}

func (b *blockingLLM) Synthesize(_ string, evidence contracts.EvidenceBlock, _ bool) (string, error) {
	select {
	case <-b.release:
	case <-time.After(5 * time.Second): // safety cap so a bug can't hang the suite
	}
	if b.accept != "" && evidence.Candidates[0].TechniqueID != b.accept {
		return "NONE", nil
	}
	return "Try: Query live warehouse data.", nil
}

// pickyLLM accepts (returns a usable why for) exactly one technique id and
// replies NONE for every other — the fit-check declining a near-miss top technique.
type pickyLLM struct{ accept string }

func (p pickyLLM) Synthesize(_ string, ev contracts.EvidenceBlock, _ bool) (string, error) {
	if ev.Candidates[0].TechniqueID == p.accept {
		return "Try: " + ev.Candidates[0].Name, nil
	}
	return "NONE", nil
}

func TestWalksPastDecliningTopCandidates(t *testing.T) {
	// Top two candidates are declined by the fit-check; a fitting technique sits at
	// rank 3. The audit must surface it same-turn, not fall silent.
	cands := []contracts.EvidenceCandidate{
		{TechniqueID: "top-miss", Name: "Top Miss", Recipe: "no"},
		{TechniqueID: "second-miss", Name: "Second Miss", Recipe: "no"},
		{TechniqueID: "good-fit", Name: "Good Fit", Recipe: "do the thing"},
	}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands, Meta: map[string]any{"thin": true}}, nil
	}
	log := &feedbackLog{}
	agent := NewAgent(provider, pickyLLM{accept: "good-fit"}, log.sink, nil,
		Options{RunAsync: inline, Segment: contracts.Segment{"team": "revops"}})

	resp := driveToSuggestionOn(agent, "codex")
	msg := delivered(resp)
	if !strings.Contains(msg, "good-fit") {
		t.Fatalf("declining the top techniques should not silence the turn — want good-fit, got: %v", resp)
	}
	if strings.Contains(msg, "top-miss") || strings.Contains(msg, "second-miss") {
		t.Fatalf("surfaced a technique the fit-check declined: %v", msg)
	}
	shown := log.byStage("shown")
	if len(shown) != 1 || shown[0].TechniqueID != "good-fit" {
		t.Fatalf("shown funnel should record only good-fit: %+v", shown)
	}
	if shown[0].RankShown != 3 {
		t.Fatalf("shown winner rank = %d, want its fit-check rank 3", shown[0].RankShown)
	}
	// Retrieval-quality telemetry: the two rejected candidates are recorded
	// 'declined' (inferred), the shown one is not — this is what the
	// semantic-embedder trigger measures.
	gotDeclined := map[string]bool{}
	for _, e := range log.byStage("declined") {
		gotDeclined[e.TechniqueID] = true
		if e.Confidence != "inferred" {
			t.Errorf("declined event should be inferred, got %q", e.Confidence)
		}
	}
	if !gotDeclined["top-miss"] || !gotDeclined["second-miss"] || gotDeclined["good-fit"] {
		t.Fatalf("declined telemetry should cover only the two rejected techniques: %+v", gotDeclined)
	}
}

// alwaysNoneLLM declines every candidate — the fit-check rejecting a whole
// retrieval burst, which is the state that used to move no counter at all.
type alwaysNoneLLM struct{}

func (alwaysNoneLLM) Synthesize(_ string, _ contracts.EvidenceBlock, _ bool) (string, error) {
	return "NONE", nil
}

func TestStatsCountDeclinesAlongsideShown(t *testing.T) {
	// Two declines and one show on the same turn: the declines are still counted,
	// because a decline is a decline even when a lower-ranked technique lands, and the
	// ratio is what makes the number worth reading.
	cands := []contracts.EvidenceCandidate{
		{TechniqueID: "top-miss", Name: "Top Miss", Recipe: "no"},
		{TechniqueID: "second-miss", Name: "Second Miss", Recipe: "no"},
		{TechniqueID: "good-fit", Name: "Good Fit", Recipe: "do the thing"},
	}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands, Meta: map[string]any{"thin": true}}, nil
	}
	log := &feedbackLog{}
	agent := NewAgent(provider, pickyLLM{accept: "good-fit"}, log.sink, nil,
		Options{RunAsync: inline, Segment: contracts.Segment{"team": "revops"}})
	driveToSuggestionOn(agent, "codex")

	totals, session := agent.StatsFor("sess_1")
	if totals.Shown != 1 || totals.Declined != 2 {
		t.Fatalf("totals should record 1 shown and 2 declined: %+v", totals)
	}
	// Declines are walked in rank order, so the last one recorded is the
	// lowest-ranked rejection — deterministic despite the concurrent fan-out.
	if session == nil || session.Declined != 2 || session.LastDeclined != "second-miss" {
		t.Fatalf("session should name the last declined technique: %+v", session)
	}
	if totals.LastDeclined != "" {
		t.Errorf("last_declined is per-session only, like client: %+v", totals)
	}
}

func TestStatsDistinguishAllDeclinedFromQuiet(t *testing.T) {
	// The case this counter exists for: retrieval found techniques, the fit-check
	// rejected every one, nothing was shown. Before, that was indistinguishable
	// from a turn where retrieval found nothing — both read `shown: 0`.
	cands := []contracts.EvidenceCandidate{
		{TechniqueID: "miss-one", Name: "Miss One", Recipe: "no"},
		{TechniqueID: "miss-two", Name: "Miss Two", Recipe: "no"},
	}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands, Meta: map[string]any{"thin": true}}, nil
	}
	log := &feedbackLog{}
	agent := NewAgent(provider, alwaysNoneLLM{}, log.sink, nil,
		Options{RunAsync: inline, Segment: contracts.Segment{"team": "revops"}})
	if msg := delivered(driveToSuggestionOn(agent, "codex")); msg != "" {
		t.Fatalf("every candidate was declined; nothing should be shown: %q", msg)
	}
	totals, _ := agent.StatsFor("sess_1")
	if totals.Shown != 0 || totals.Declined != 2 {
		t.Fatalf("a fully-declined burst must be visible as 0 shown / 2 declined: %+v", totals)
	}

	// And a genuinely quiet turn still reads zero on both, so the two states stay
	// distinguishable in the other direction too.
	empty := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, nil
	}
	quiet := NewAgent(empty, alwaysNoneLLM{}, (&feedbackLog{}).sink, nil,
		Options{RunAsync: inline, Segment: contracts.Segment{"team": "revops"}})
	driveToSuggestionOn(quiet, "codex")
	if qt, _ := quiet.StatsFor("sess_1"); qt.Shown != 0 || qt.Declined != 0 {
		t.Fatalf("no candidates means no declines: %+v", qt)
	}
}

// countingLLM records how many fit-checks were fanned out and accepts exactly
// one technique id.
type countingLLM struct {
	mu     sync.Mutex
	calls  int
	accept string
}

func (c *countingLLM) Synthesize(_ string, ev contracts.EvidenceBlock, _ bool) (string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	if ev.Candidates[0].TechniqueID == c.accept {
		return "Try: " + ev.Candidates[0].Name, nil
	}
	return "NONE", nil
}

func (c *countingLLM) count() int { c.mu.Lock(); defer c.mu.Unlock(); return c.calls }

func TestMaxFitChecksCapsFanOut(t *testing.T) {
	// Four candidates, but MaxFitChecks=2: only the top 2 are fit-checked. The
	// fitting technique sits at rank 4, so nothing shows — the burst-bound tradeoff.
	cands := []contracts.EvidenceCandidate{
		{TechniqueID: "r1", Name: "R1", Recipe: "no"},
		{TechniqueID: "r2", Name: "R2", Recipe: "no"},
		{TechniqueID: "r3", Name: "R3", Recipe: "no"},
		{TechniqueID: "r4-fits", Name: "R4", Recipe: "yes"},
	}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands, Meta: map[string]any{"thin": true}}, nil
	}
	llm := &countingLLM{accept: "r4-fits"}
	agent := NewAgent(provider, llm, (&feedbackLog{}).sink, nil,
		Options{RunAsync: inline, MaxFitChecks: 2, Segment: contracts.Segment{"team": "revops"}})

	resp := driveToSuggestion(agent)
	if len(resp) != 0 {
		t.Fatalf("technique beyond the cap should not surface: %v", resp)
	}
	if n := llm.count(); n != 2 {
		t.Fatalf("MaxFitChecks=2 should fan out 2 calls, got %d", n)
	}
}

func TestAllCandidatesDeclinedShowsNothing(t *testing.T) {
	// Precision still holds: if the fit-check declines every candidate, nothing
	// is surfaced (no lowering of the bar just to say something).
	cands := []contracts.EvidenceCandidate{
		{TechniqueID: "miss-1", Name: "Miss 1", Recipe: "no"},
		{TechniqueID: "miss-2", Name: "Miss 2", Recipe: "no"},
	}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands, Meta: map[string]any{"thin": true}}, nil
	}
	log := &feedbackLog{}
	agent := NewAgent(provider, pickyLLM{accept: "none-of-them"}, log.sink, nil,
		Options{RunAsync: inline, Segment: contracts.Segment{"team": "revops"}})

	if resp := driveToSuggestion(agent); len(resp) != 0 {
		t.Fatalf("all-declined should surface nothing, got: %v", resp)
	}
	if n := len(log.byStage("shown")); n != 0 {
		t.Fatalf("nothing should be recorded shown, got %d", n)
	}
	// Even when nothing surfaces, every fit-check rejection is still measured.
	if n := len(log.byStage("declined")); n != 2 {
		t.Fatalf("both declined candidates should be recorded declined, got %d", n)
	}
}

func TestSlowSynthesisParksAndDeliversNextPrompt(t *testing.T) {
	candidates := []contracts.EvidenceCandidate{
		{TechniqueID: "miss-1", Name: "Miss 1", Recipe: "no"},
		{TechniqueID: "miss-2", Name: "Miss 2", Recipe: "no"},
		orgCandidate(),
	}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: candidates}, nil
	}
	log := &feedbackLog{}
	slow := &blockingLLM{release: make(chan struct{}), accept: orgCandidate().TechniqueID}
	agent := NewAgent(provider, slow, log.sink, nil,
		Options{SynthBudget: 50 * time.Millisecond}) // real goroutines
	agent.Handle(ev("SessionStart", nil), "codex")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize pasted rows"}), "codex")
	if resp := agent.Handle(ev("Stop", nil), "codex"); len(resp) != 0 {
		t.Fatalf("over budget should deliver nothing same-turn: %v", resp)
	}
	close(slow.release) // let synthesis finish and park
	deadline := time.Now().Add(2 * time.Second)
	for {
		agent.mu.Lock()
		parked := agent.sessions["codex:sess_1"].pending != nil
		agent.mu.Unlock()
		if parked || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "now what about Q3?"}), "codex")
	hso, _ := resp["hookSpecificOutput"].(map[string]any)
	if hso == nil || hso["hookEventName"] != "UserPromptSubmit" {
		t.Fatalf("parked delivery envelope: %v", resp)
	}
	if resp["systemMessage"] != hso["additionalContext"] {
		t.Fatal("systemMessage and additionalContext must match")
	}
	deadline = time.Now().Add(2 * time.Second)
	for len(log.byStage("shown")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // feedback posts async
	}
	shown := log.byStage("shown")
	if len(shown) == 0 {
		t.Fatal("shown never recorded after parked delivery")
	}
	if shown[0].RankShown != 3 {
		t.Fatalf("parked winner rank = %d, want 3", shown[0].RankShown)
	}
}

// budgetRacingLLM finishes at almost exactly the moment the same-turn budget
// runs out, which is the one interleaving the inline runner used by most tests
// here can never produce: with a real goroutine, the worker's result and the
// Stop timer become ready together, and the two must agree on who owns the
// suggestion.
type budgetRacingLLM struct{ budget time.Duration }

func (b budgetRacingLLM) Synthesize(_ string, ev contracts.EvidenceBlock, _ bool) (string, error) {
	time.Sleep(b.budget)
	return "Try: " + ev.Candidates[0].Name, nil
}

func TestSynthesisRacingTheBudgetIsDeliveredOrParked(t *testing.T) {
	// A synthesis that lands on the budget boundary either rides this turn's
	// Stop or parks for the next prompt. It must never do both, and it must
	// never vanish: a member who waited for it and then never saw it has no way
	// to know anything was lost. Run this with -race -count=50.
	const turns = 20
	candidates := make([]contracts.EvidenceCandidate, 0, turns+2)
	for i := 0; i < turns+2; i++ {
		id := fmt.Sprintf("technique-%d", i)
		candidates = append(candidates, contracts.EvidenceCandidate{
			TechniqueID: id, Name: id, Recipe: "do " + id})
	}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: candidates}, nil
	}
	budget := 20 * time.Millisecond
	agent := NewAgent(provider, budgetRacingLLM{budget: budget}, nil, nil,
		Options{SynthBudget: budget, MaxFitChecks: 1, MaxPerWindow: 1000,
			CooldownTurns: -1, CooldownFor: -1}) // real goroutines
	agent.Handle(ev("SessionStart", nil), "codex")

	parked := func() bool {
		agent.mu.Lock()
		defer agent.mu.Unlock()
		return agent.sessions["codex:sess_1"].pending != nil
	}

	wantParkedDelivery := false
	for turn := 0; turn < turns; turn++ {
		prompt := agent.Handle(ev("UserPromptSubmit",
			map[string]any{"prompt": "summarize these pasted warehouse rows"}), "codex")
		if wantParkedDelivery && delivered(prompt) == "" {
			t.Fatalf("turn %d: a parked suggestion was never delivered: %v", turn, prompt)
		}
		stop := agent.Handle(ev("Stop", nil), "codex")
		sameTurn := delivered(stop) != ""

		// The worker clears the reservation exactly once, at its end, so this
		// is the point where the delivery decision is final.
		deadline := time.Now().Add(5 * time.Second)
		for agent.Busy() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if agent.Busy() {
			t.Fatalf("turn %d: synthesis never finished", turn)
		}
		wantParkedDelivery = parked()
		switch {
		case sameTurn && wantParkedDelivery:
			t.Fatalf("turn %d: the same suggestion was both delivered and parked", turn)
		case !sameTurn && !wantParkedDelivery:
			t.Fatalf("turn %d: the synthesized suggestion was lost — neither "+
				"delivered at Stop nor parked for the next prompt", turn)
		}
	}
}

func TestEveryEventReturnsTheLockItTook(t *testing.T) {
	// handle() takes a.mu for every hook event, and a branch that returns
	// without giving it back wedges the whole daemon: every session's next hook
	// waits on a lock nobody will release, and the relay drops those turns in
	// silence. No other test would report it — the suite would simply stop. So
	// drive every event, and every branch of the prompt and Stop handlers,
	// twice in a row on four harnesses, under a watchdog. The second call only
	// returns if the first gave the lock back.
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{SynthBudget: 10 * time.Millisecond, RegistryConfigured: true})
	askQuestion := map[string]any{
		"tool_name": "AskUserQuestion",
		"tool_input": map[string]any{"questions": []any{map[string]any{
			"header": questionHeader, "question": "Apply it?",
			"options": []any{map[string]any{"label": optApply}}}}},
		"tool_response": map[string]any{"answers": map[string]any{"Apply it?": optApply}},
	}
	steps := []struct {
		name   string
		arm    string // self-test mode to arm before the step, "" for none
		event  string
		fields map[string]any
	}{
		{name: "session start", event: "SessionStart", fields: map[string]any{"source": "startup"}},
		{name: "prompt", event: "UserPromptSubmit", fields: map[string]any{"prompt": "summarize these pasted warehouse rows"}},
		{name: "pre tool", event: "PreToolUse", fields: map[string]any{"tool_name": "Edit",
			"tool_input": map[string]any{"file_path": "a.go"}}},
		{name: "pre tool question", event: "PreToolUse", fields: askQuestion},
		{name: "post tool", event: "PostToolUse", fields: map[string]any{"tool_name": "Edit",
			"tool_input": map[string]any{"file_path": "a.go"}}},
		{name: "post tool question", event: "PostToolUse", fields: askQuestion},
		{name: "assistant text", event: "AssistantText", fields: map[string]any{"text": "here you go"}},
		{name: "stop", event: "Stop"},
		{name: "mention prompt", event: "UserPromptSubmit", fields: map[string]any{"prompt": "@tacit what should I try?"}},
		{name: "demo prompt", event: "UserPromptSubmit", fields: map[string]any{"prompt": "as a test, review my pull request"}},
		{name: "self-test at stop", arm: ModeBlock, event: "Stop"},
		{name: "self-test at prompt", arm: ModeContext, event: "UserPromptSubmit",
			fields: map[string]any{"prompt": "carry on then"}},
		{name: "parked delivery", event: "UserPromptSubmit", fields: map[string]any{"prompt": "and Q3?"}},
		{name: "session end", event: "SessionEnd"},
	}

	// Every harness class: the form one, the block one, the park-only one and
	// the one whose output never renders. Stop and the prompt branch on all
	// four.
	drive := func() {
		for _, harness := range []string{"claude-code", "codex", "cursor", "copilot"} {
			for _, s := range steps {
				for call := 0; call < 2; call++ {
					if s.arm != "" {
						agent.ArmSelfTest(harness, s.arm)
					}
					agent.Handle(ev(s.event, s.fields), harness)
				}
			}
		}
	}

	done := make(chan struct{})
	go func() { defer close(done); drive() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("a hook handler never returned — it kept a.mu, and every hook of " +
			"every session is now waiting on it")
	}
}

func TestSlowRetrievalParksAndDeliversNextPrompt(t *testing.T) {
	// Retrieval is network I/O just like synthesis, and it used to run
	// synchronously on the Stop path — a slow-but-alive registry held the
	// member's end-of-turn for the relay timeout, not the budget. Now the whole
	// pipeline is budgeted: a slow evidence call degrades to parked next-turn
	// delivery, and Stop returns within SynthBudget.
	release := make(chan struct{})
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		<-release // a registry that answers, eventually
		return contracts.EvidenceBlock{Candidates: []contracts.EvidenceCandidate{orgCandidate()}}, nil
	}
	log := &feedbackLog{}
	agent := NewAgent(provider, stubLLM{}, log.sink, nil,
		Options{SynthBudget: 50 * time.Millisecond}) // real goroutines
	agent.Handle(ev("SessionStart", nil), "codex")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize pasted rows"}), "codex")
	start := time.Now()
	if resp := agent.Handle(ev("Stop", nil), "codex"); len(resp) != 0 {
		t.Fatalf("slow retrieval should deliver nothing same-turn: %v", resp)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Stop blocked %v on a slow registry; the budget was 50ms", elapsed)
	}
	close(release) // registry answers; the pipeline finishes and parks
	deadline := time.Now().Add(2 * time.Second)
	for {
		agent.mu.Lock()
		parked := agent.sessions["codex:sess_1"].pending != nil
		agent.mu.Unlock()
		if parked || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "now what about Q3?"}), "codex")
	hso, _ := resp["hookSpecificOutput"].(map[string]any)
	if hso == nil || hso["hookEventName"] != "UserPromptSubmit" {
		t.Fatalf("parked delivery envelope after slow retrieval: %v", resp)
	}
}

func TestSlowRetrievalNeverBlocksAnObservationOnlyStop(t *testing.T) {
	// After the coaching budget is spent, Stops are observation-only — and an
	// observation must never wait on the registry at all.
	release := make(chan struct{})
	defer close(release)
	blocked := make(chan struct{}, 8)
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		blocked <- struct{}{}
		<-release
		return contracts.EvidenceBlock{}, nil
	}
	// MaxPerWindow -1 is coaching off, so every Stop is observation-only.
	// A synchronous path would wait the 5s budget.
	agent := NewAgent(provider, stubLLM{}, nil, nil,
		Options{MaxPerWindow: -1, SynthBudget: 5 * time.Second})
	agent.Handle(ev("SessionStart", nil), "codex")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize pasted rows"}), "codex")
	start := time.Now()
	if resp := agent.Handle(ev("Stop", nil), "codex"); len(resp) != 0 {
		t.Fatalf("observation-only Stop must be a no-op: %v", resp)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("observation-only Stop blocked %v on the registry", elapsed)
	}
	<-blocked // the audit fact recording did still reach retrieval, asynchronously
}

func TestAmpStopResponseCarriesTheSuggestion(t *testing.T) {
	// Amp is uniform on the agent side: the Stop response carries the
	// suggestion like any other harness. Amp itself can't RENDER at Stop —
	// the plugin parks this envelope on disk and displays it at the member's
	// next agent.start (validated live; plugins/amp/plugins/tacit.ts).
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	agent.Handle(ev("SessionStart", nil), "amp")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "amp")
	resp := agent.Handle(ev("Stop", nil), "amp")
	msg, _ := resp["systemMessage"].(string)
	if !strings.Contains(msg, Mark()) || !strings.Contains(msg, "use-internal-data-connector") {
		t.Fatalf("amp Stop delivery missing: %v", resp)
	}
	shown := log.byStage("shown")
	if len(shown) != 1 || shown[0].TechniqueID != "use-internal-data-connector" {
		t.Fatalf("shown funnel: %+v", shown)
	}
	// sessions are keyed per harness, so the claude-code namespace is untouched
	agent.mu.Lock()
	_, collided := agent.sessions["claude-code:sess_1"]
	agent.mu.Unlock()
	if collided {
		t.Fatal("amp session leaked into the claude-code namespace")
	}
}

func TestPiStopResponseCarriesTheSuggestion(t *testing.T) {
	// pi renders at Stop natively: its extension adapter turns this envelope
	// into a displayed custom message (plugins/pi/extensions/tacit.ts), so the
	// agent side is identical to the other harnesses.
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	agent.Handle(ev("SessionStart", nil), "pi")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "pi")
	resp := agent.Handle(ev("Stop", nil), "pi")
	msg, _ := resp["systemMessage"].(string)
	if !strings.Contains(msg, Mark()) || !strings.Contains(msg, "use-internal-data-connector") {
		t.Fatalf("pi Stop delivery missing: %v", resp)
	}
	if shown := log.byStage("shown"); len(shown) != 1 {
		t.Fatalf("shown funnel: %+v", shown)
	}
	agent.mu.Lock()
	st := agent.sessions["pi:sess_1"]
	agent.mu.Unlock()
	if st == nil || st.capture.Harness() != "pi" {
		t.Fatal("pi session not keyed under the pi namespace with a pi reader")
	}
}

func TestOpencodeStopCarriesSuggestionAndInlineAssistantText(t *testing.T) {
	// opencode renders at Stop via its plugin adapter (a toast; the parked
	// path injects a synthetic part on the next user message), so the agent
	// side is uniform. Distinctive to opencode: assistant text arrives INLINE
	// on the Stop payload (the plugin accumulates text-complete hooks) — no
	// transcript_path, so ingestion must not depend on one.
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	agent.Handle(ev("SessionStart", nil), "opencode")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "opencode")
	resp := agent.Handle(ev("Stop", map[string]any{
		"assistant_text": "Here is the summary of the pasted rows.",
		"model":          "anthropic/claude-sonnet-5",
	}), "opencode")
	msg, _ := resp["systemMessage"].(string)
	if !strings.Contains(msg, Mark()) || !strings.Contains(msg, "use-internal-data-connector") {
		t.Fatalf("opencode Stop delivery missing: %v", resp)
	}
	if shown := log.byStage("shown"); len(shown) != 1 {
		t.Fatalf("shown funnel: %+v", shown)
	}
	agent.mu.Lock()
	st := agent.sessions["opencode:sess_1"]
	agent.mu.Unlock()
	if st == nil || st.capture.Harness() != "opencode" {
		t.Fatal("opencode session not keyed under its namespace with its reader")
	}
	if st.capture.Model != "anthropic/claude-sonnet-5" {
		t.Fatalf("model not latched from the plugin payload: %q", st.capture.Model)
	}
}

func TestGeminiAfterAgentDeliversSameTurn(t *testing.T) {
	// Gemini CLI events arrive under Google's names (BeforeAgent/AfterAgent —
	// capture/gemini.go) and are canonicalized inside Handle; AfterAgent
	// renders systemMessage in the terminal, so delivery is same-turn like
	// Claude Code, and the assistant response arrives inline as
	// prompt_response (no transcript tailing).
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	agent.Handle(ev("SessionStart", map[string]any{"source": "startup"}), "gemini")
	agent.Handle(ev("BeforeAgent", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "gemini")
	resp := agent.Handle(ev("AfterAgent", map[string]any{
		"prompt":          "summarize these pasted warehouse rows",
		"prompt_response": "Here is the summary of the pasted rows.",
	}), "gemini")
	msg, _ := resp["systemMessage"].(string)
	if !strings.Contains(msg, Mark()) || !strings.Contains(msg, "use-internal-data-connector") {
		t.Fatalf("gemini AfterAgent delivery missing: %v", resp)
	}
	if shown := log.byStage("shown"); len(shown) != 1 {
		t.Fatalf("shown funnel: %+v", shown)
	}
	agent.mu.Lock()
	st := agent.sessions["gemini:sess_1"]
	agent.mu.Unlock()
	if st == nil || st.capture.Harness() != "gemini" {
		t.Fatal("gemini session not keyed under its namespace with its reader")
	}
}

func TestCopilotStopObservesButNeverSuggests(t *testing.T) {
	// Copilot CLI has no member-visible hook output (harness.go deliverless):
	// the same flow that earns a suggestion on every other harness must stay
	// silent here — no synthesis, no shown event — while capture still folds
	// the session under its own reader.
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	agent.Handle(map[string]any{"hook_event_name": "sessionStart", "sessionId": "cop_1", "source": "startup"}, "copilot")
	agent.Handle(map[string]any{"hook_event_name": "userPromptSubmitted", "sessionId": "cop_1",
		"prompt": "summarize these pasted warehouse rows"}, "copilot")
	resp := agent.Handle(map[string]any{"hook_event_name": "agentStop", "sessionId": "cop_1",
		"stopReason": "completed"}, "copilot")
	if len(resp) != 0 {
		t.Fatalf("deliverless harness must answer with a no-op: %v", resp)
	}
	if shown := log.byStage("shown"); len(shown) != 0 {
		t.Fatalf("shown recorded for a suggestion nobody saw: %+v", shown)
	}
	agent.mu.Lock()
	st := agent.sessions["copilot:cop_1"]
	agent.mu.Unlock()
	if st == nil || st.capture.Harness() != "copilot" {
		t.Fatal("copilot session not keyed under its namespace with its reader")
	}
}

func TestCursorParksAtStopAndDeliversVisiblyNextPrompt(t *testing.T) {
	// Cursor has no visible stop channel (followup_message would spin the
	// agent), so an on-budget suggestion PARKS at stop — the stop response is
	// a no-op, no 'shown' yet — and rides the next beforeSubmitPrompt as a
	// member-visible user_message, where 'shown' records.
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	cev := func(ev string, extra map[string]any) map[string]any {
		p := map[string]any{"hook_event_name": ev, "conversation_id": "cur_1"}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	agent.Handle(cev("sessionStart", nil), "cursor")
	agent.Handle(cev("beforeSubmitPrompt", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "cursor")
	stop := agent.Handle(cev("stop", map[string]any{"status": "completed"}), "cursor")
	if len(stop) != 0 {
		t.Fatalf("cursor stop must be a no-op envelope: %v", stop)
	}
	if shown := log.byStage("shown"); len(shown) != 0 {
		t.Fatalf("shown recorded before anything rendered: %+v", shown)
	}
	next := agent.Handle(cev("beforeSubmitPrompt", map[string]any{"prompt": "now the second batch"}), "cursor")
	msg, _ := next["user_message"].(string)
	if !strings.Contains(msg, Mark()) || !strings.Contains(msg, "use-internal-data-connector") {
		t.Fatalf("parked suggestion not delivered as user_message: %v", next)
	}
	if next["systemMessage"] != nil || next["hookSpecificOutput"] != nil {
		t.Fatalf("canonical envelope leaked to cursor: %v", next)
	}
	if cont, _ := next["continue"].(bool); !cont {
		t.Fatalf("continue must be true: %v", next)
	}
	if shown := log.byStage("shown"); len(shown) != 1 {
		t.Fatalf("shown funnel after visible delivery: %+v", shown)
	}
	agent.mu.Lock()
	st := agent.sessions["cursor:cur_1"]
	agent.mu.Unlock()
	if st == nil || st.capture.Harness() != "cursor" {
		t.Fatal("cursor session not keyed under its namespace with its reader")
	}
}

func TestNoCandidatesNoSuggestion(t *testing.T) {
	agent, log := newTestAgent(t, nil, Options{})
	if resp := driveToSuggestion(agent); len(resp) != 0 {
		t.Fatalf("no candidates should be silent: %v", resp)
	}
	if len(log.all()) != 0 {
		t.Fatalf("events recorded with nothing shown: %+v", log.all())
	}
}

func TestRegistryDownNeverBreaksTheTurn(t *testing.T) {
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, http.ErrHandlerTimeout
	}
	agent := NewAgent(provider, stubLLM{}, nil, nil, Options{RunAsync: inline})
	if resp := driveToSuggestion(agent); len(resp) != 0 {
		t.Fatalf("registry error leaked: %v", resp)
	}
	// gate must reopen after the failure
	agent.mu.Lock()
	synthesizing := agent.sessions["claude-code:sess_1"].synthesizing
	agent.mu.Unlock()
	if synthesizing {
		t.Fatal("session stuck synthesizing after registry failure")
	}
}

func TestNotReshownWithinSession(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: 1, CooldownFor: -1})
	if resp := driveToSuggestion(agent); len(resp) == 0 {
		t.Fatal("first suggestion missing")
	}
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "more unrelated work"}), "claude-code")
	agent.Handle(ev("Stop", nil), "claude-code")
	if resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "and more"}), "claude-code"); len(resp) != 0 {
		t.Fatalf("same technique re-shown: %v", resp)
	}
}

func TestCapLimitsSuggestionsPerSession(t *testing.T) {
	cands := []contracts.EvidenceCandidate{orgCandidate()}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands}, nil
	}
	agent := NewAgent(provider, stubLLM{}, nil, nil,
		Options{MaxPerWindow: 1, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline})
	if resp := driveToSuggestion(agent); len(resp) == 0 {
		t.Fatal("first suggestion missing")
	}
	// swap in a fresh technique: still capped
	cands[0].TechniqueID = "another-technique"
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "next"}), "claude-code")
	if resp := agent.Handle(ev("Stop", nil), "codex"); len(resp) != 0 {
		t.Fatalf("cap ignored: %v", resp)
	}
}

func TestCooldownBlocksBackToBack(t *testing.T) {
	cands := []contracts.EvidenceCandidate{orgCandidate()}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands}, nil
	}
	agent := NewAgent(provider, stubLLM{}, nil, nil,
		Options{MaxPerWindow: 5, CooldownTurns: 2, CooldownFor: -1, RunAsync: inline})
	driveToSuggestion(agent)
	cands[0].TechniqueID = "fresh-technique"
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "one turn later"}), "claude-code")
	if resp := agent.Handle(ev("Stop", nil), "codex"); len(resp) != 0 {
		t.Fatalf("cooldown ignored (1 < 2): %v", resp)
	}
}

func TestMatchingToolCallEmitsInferredAdopted(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "mcp__warehouse__query", "tool_input": map[string]any{"q": "revenue.pipeline"}}), "claude-code")
	adopted := log.byStage("adopted")
	if len(adopted) != 1 || adopted[0].TechniqueID != "use-internal-data-connector" {
		t.Fatalf("adopted: %+v", adopted)
	}
	if adopted[0].Confidence != "inferred" {
		t.Fatalf("confidence = %s", adopted[0].Confidence)
	}
}

func TestAdoptedInheritsShownSegment(t *testing.T) {
	// Regression: adopted/helped must carry the SAME cohort tags the suggestion
	// was shown with — including the harness the capture auto-stamps. Before,
	// these stages used only the statically-configured segment, so adoption
	// could never be broken out by harness (the cohorts page showed 0% adopt).
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{Segment: contracts.Segment{"team": "revops"}})
	driveToSuggestion(agent) // harness "claude-code"

	shown := log.byStage("shown")
	if len(shown) == 0 || shown[0].Segment["harness"] != "claude-code" {
		t.Fatalf("shown segment missing auto-stamped harness: %+v", shown)
	}

	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "mcp__warehouse__query", "tool_input": map[string]any{"q": "revenue.pipeline"}}), "claude-code")

	adopted := log.byStage("adopted")
	if len(adopted) != 1 {
		t.Fatalf("adopted: %+v", adopted)
	}
	if adopted[0].Segment["harness"] != "claude-code" {
		t.Fatalf("adopted must inherit harness from shown; got segment %+v", adopted[0].Segment)
	}
	if adopted[0].Segment["team"] != "revops" {
		t.Fatalf("adopted must keep the configured team tag; got %+v", adopted[0].Segment)
	}
}

func TestUnrelatedToolCallIsNotAdoption(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	agent.Handle(ev("PreToolUse", map[string]any{"tool_name": "Read",
		"tool_input": map[string]any{"file_path": "notes.md"}}), "claude-code")
	if len(log.byStage("adopted")) != 0 {
		t.Fatal("unrelated tool call counted as adoption")
	}
}

func TestNextPromptUsingTheMoveIsAdoption(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	agent.Handle(ev("UserPromptSubmit", map[string]any{
		"prompt": "ok, query the warehouse for pipeline instead"}), "claude-code")
	adopted := log.byStage("adopted")
	if len(adopted) != 1 || adopted[0].Confidence != "inferred" {
		t.Fatalf("prompt adoption: %+v", adopted)
	}
}

func TestAdoptThenHelpedCountsAdoptedOnce(t *testing.T) {
	// The happy path: the member USES the move (behavioral adoption), then
	// SAYS it helped. helped implies adopted, but the implied event must be
	// dropped when adoption was already recorded — otherwise adopted=2 halves
	// the technique's helped_rate exactly when the suggestion worked.
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "mcp__warehouse__query", "tool_input": map[string]any{"q": "x"}}), "claude-code")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "that helped"}), "claude-code")
	if n := len(log.byStage("adopted")); n != 1 {
		t.Fatalf("adopted counted %d times (want 1)", n)
	}
	if n := len(log.byStage("helped")); n != 1 {
		t.Fatalf("helped = %d", n)
	}
}

func TestPositiveReactionRecordsHelpedNoCommand(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	log.clear()
	resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "that helped, thanks"}), "claude-code")
	if _, visible := resp["systemMessage"]; visible {
		t.Fatalf("reaction turn showed the member something: %v", resp)
	}
	helped := log.byStage("helped")
	adopted := log.byStage("adopted") // helped implies adopted
	if len(helped) != 1 || len(adopted) != 1 {
		t.Fatalf("events: helped=%d adopted=%d", len(helped), len(adopted))
	}
	if helped[0].Confidence != "explicit" {
		t.Fatalf("reaction-dominant message should be explicit: %s", helped[0].Confidence)
	}
}

func TestNegativeReactionRecordsDismissedWithReason(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "not relevant"}), "claude-code")
	dis := log.byStage("dismissed")
	if len(dis) != 1 || dis[0].Value != "not-relevant" {
		t.Fatalf("dismissed: %+v", dis)
	}
}

func TestEmbeddedCueIsInferredNotExplicit(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	agent.Handle(ev("UserPromptSubmit", map[string]any{
		"prompt": "that helped me understand the pipeline, now break it down by region"}), "claude-code")
	helped := log.byStage("helped")
	if len(helped) != 1 || helped[0].Confidence != "inferred" {
		t.Fatalf("embedded cue: %+v", helped)
	}
}

func TestReactionOnlyWithinWindow(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: 100, CooldownFor: -1}) // block later suggestions
	driveToSuggestion(agent)
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "unrelated one"}), "claude-code")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "unrelated two"}), "claude-code")
	log.clear()
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "that helped"}), "claude-code")
	if n := len(log.all()); n != 0 {
		t.Fatalf("reaction outside window recorded: %+v", log.all())
	}
}

func TestReactionNotDoubleCounted(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	log.clear()
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "that helped"}), "claude-code")
	n := len(log.all())
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "that helped again"}), "claude-code")
	if len(log.all()) != n {
		t.Fatalf("repeated reaction re-counted: %d -> %d", n, len(log.all()))
	}
}

// askAnswer builds a PostToolUse payload for an answered OpenTacit question.
func askAnswer(label string) map[string]any {
	question := "Apply this move from your org?"
	return ev("PostToolUse", map[string]any{
		"tool_name": "AskUserQuestion",
		"tool_input": map[string]any{
			"questions": []any{map[string]any{
				"question": question, "header": "Tacit",
			}},
			"answers":     map[string]any{question: label},
			"annotations": map[string]any{},
		},
	})
}

// The hidden next-turn directive was dropped (it leaked as Claude Code's sticky
// "tacit-suggestions" context chip). After a delivery, the next prompt now
// carries nothing model-facing — only ambient reaction/adoption detection runs.
func TestNoDirectiveAfterDelivery(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "keep going with the analysis"}), "claude-code")
	if len(resp) != 0 {
		t.Fatalf("expected no injected context after delivery, got: %v", resp)
	}
}

func TestQuestionAnswerApplyRecordsExplicitAdoption(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "continue"}), "claude-code") // next turn (no-op)
	log.clear()
	agent.Handle(askAnswer(optApply), "claude-code")
	adopted := log.byStage("adopted")
	if len(adopted) != 1 || adopted[0].Confidence != "explicit" {
		t.Fatalf("apply answer: %+v", adopted)
	}
	// a later "that helped" still records helped, adopted only once
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "that helped"}), "claude-code")
	if n := len(log.byStage("adopted")); n != 1 {
		t.Fatalf("adopted double-counted after apply: %d", n)
	}
	if n := len(log.byStage("helped")); n != 1 {
		t.Fatalf("helped after apply: %d", n)
	}
}

func TestQuestionAnswerDismissalsMapToReasons(t *testing.T) {
	for _, tc := range []struct{ label, reason string }{
		{optNotRelevant, "not-relevant"},
		{optAlreadyUse, "already-knew"},
	} {
		agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
		driveToSuggestion(agent)
		log.clear()
		agent.Handle(askAnswer(tc.label), "claude-code")
		dis := log.byStage("dismissed")
		if len(dis) != 1 || dis[0].Value != tc.reason || dis[0].Confidence != "explicit" {
			t.Fatalf("%s: %+v", tc.label, dis)
		}
		// the ambient path must not double-record afterwards
		agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "not relevant"}), "claude-code")
		if n := len(log.byStage("dismissed")); n != 1 {
			t.Fatalf("%s: dismissal double-counted: %d", tc.label, n)
		}
	}
}

func TestQuestionAnswerFreeTextClassified(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	log.clear()
	agent.Handle(askAnswer("that worked really well"), "claude-code")
	helped := log.byStage("helped")
	if len(helped) != 1 || helped[0].Confidence != "explicit" {
		t.Fatalf("free-text answer: %+v", helped)
	}
}

// A free-text "that worked" can arrive after inference already recorded the
// helped: the member used the move, kept working past the confirm window, and
// only then answered the form in their own words. The explicit answer must not
// add a second helped — the feedback log is append-only, so the earlier event
// stands, and a duplicate counts one outcome twice in the headline number.
func TestFreeTextHelpedAfterInferredHelpedCountsOnce(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline})
	driveToSuggestion(agent)
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "let me query the warehouse connector"}), "claude-code")
	for i := 0; i < HelpedConfirmTurns; i++ {
		agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on with the next step"}), "claude-code")
	}
	if n := len(log.byStage("helped")); n != 1 {
		t.Fatalf("setup: want 1 inferred helped before the answer, got %d", n)
	}

	agent.Handle(askAnswer("that worked really well"), "claude-code")
	if n := len(log.byStage("helped")); n != 1 {
		t.Fatalf("free-text answer after an inferred helped: helped = %d, want 1", n)
	}
	if n := len(log.byStage("adopted")); n != 1 {
		t.Fatalf("adopted = %d, want 1", n)
	}
}

func TestForeignQuestionIgnored(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	log.clear()
	payload := askAnswer(optNotRelevant)
	payload["tool_input"].(map[string]any)["questions"].([]any)[0].(map[string]any)["header"] = "Other"
	agent.Handle(payload, "claude-code")
	if len(log.all()) != 0 {
		t.Fatalf("foreign question captured: %+v", log.all())
	}
}

func TestQuestionOfferedCounter(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	pre := askAnswer(optApply)
	pre["hook_event_name"] = "PreToolUse"
	delete(pre["tool_input"].(map[string]any), "answers")
	agent.Handle(pre, "claude-code")
	totals, _ := agent.StatsFor("")
	if totals.Offered != 1 {
		t.Fatalf("offered = %d", totals.Offered)
	}
}

func TestEvidenceLineRendersMeasuredOutcomes(t *testing.T) {
	measured := orgCandidate()
	measured.Outcomes = map[string]any{
		"helped_rate": 0.94, "adoption_rate": 0.7,
		"sample_size": float64(120), "segment": "team:revops",
	}
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{measured}, Options{})
	resp := driveToSuggestionOn(agent, "codex")
	msg := delivered(resp)
	want := "measured by colleagues: helped 94% · adopted 70% · n=120 · team:revops"
	if !strings.Contains(msg, want) {
		t.Fatalf("evidence line missing:\n%s", msg)
	}
}

func TestColdStartTechniqueHasNoEvidenceLine(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	resp := driveToSuggestionOn(agent, "codex")
	msg := delivered(resp)
	if strings.Contains(msg, "measured by colleagues") {
		t.Fatalf("cold-start technique claims measurements:\n%s", msg)
	}
	if !strings.Contains(msg, "record adoption automatically") {
		t.Fatalf("affordance footer missing:\n%s", msg)
	}
}

func TestStatsForSessionAndTotals(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "mcp__warehouse__query", "tool_input": "q"}), "claude-code")

	totals, session := agent.StatsFor("sess_1")
	if totals.Shown != 1 || totals.Adopted != 1 {
		t.Fatalf("totals: %+v", totals)
	}
	if session == nil || session.Shown != 1 || session.Adopted != 1 {
		t.Fatalf("session: %+v", session)
	}
	if _, unknown := agent.StatsFor("nope"); unknown != nil {
		t.Fatal("unknown session matched")
	}
}

type upgradeLLM struct{ stubLLM }

func (upgradeLLM) UpgradeAvailable() bool { return true }

func TestUpgradeHintShownOncePerSession(t *testing.T) {
	cands := []contracts.EvidenceCandidate{orgCandidate()}
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands}, nil
	}
	agent := NewAgent(provider, upgradeLLM{}, nil, nil,
		Options{MaxPerWindow: 5, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline})
	agent.Handle(ev("SessionStart", nil), "codex")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "pasted rows"}), "codex")
	resp := agent.Handle(ev("Stop", nil), "codex")
	if !strings.Contains(resp["systemMessage"].(string), "running offline") {
		t.Fatal("upgrade hint missing on first suggestion")
	}
	cands[0].TechniqueID = "second-technique"
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "more"}), "codex")
	resp2 := agent.Handle(ev("Stop", nil), "codex")
	if msg, _ := resp2["systemMessage"].(string); msg == "" || strings.Contains(msg, "running offline") {
		t.Fatalf("second suggestion wrong: %q", msg)
	}
}

func TestHandleFeedbackValidatesAndImpliesAdopted(t *testing.T) {
	agent, log := newTestAgent(t, nil, Options{})
	ok, resp := agent.HandleFeedback(map[string]any{"technique_id": "cap", "stage": "helped"})
	if !ok || resp["accepted"] != 2 {
		t.Fatalf("helped feedback: %v %v", ok, resp)
	}
	stages := map[string]bool{}
	for _, e := range log.all() {
		stages[e.Stage] = true
	}
	if !stages["adopted"] || !stages["helped"] {
		t.Fatalf("stages: %v", stages)
	}
	if ok, _ := agent.HandleFeedback(map[string]any{"technique_id": "cap", "stage": "bogus"}); ok {
		t.Fatal("invalid stage accepted")
	}
}

func TestHandleContributionStampsCohortAndForwards(t *testing.T) {
	var forwarded map[string]any
	contribute := func(technique map[string]any) (map[string]any, error) {
		forwarded = technique
		return map[string]any{"id": "x", "status": "draft"}, nil
	}
	agent := NewAgent(
		func(contracts.Characterization) (contracts.EvidenceBlock, error) {
			return contracts.EvidenceBlock{}, nil
		},
		stubLLM{}, nil, contribute,
		Options{Segment: contracts.Segment{"team": "revops"}, RunAsync: inline})
	ok, resp := agent.HandleContribution(map[string]any{
		"name": "N", "description": "D", "recipe": "R"})
	if !ok || resp["status"] != "draft" {
		t.Fatalf("contribution: %v %v", ok, resp)
	}
	seg, _ := forwarded["segment"].(contracts.Segment)
	if seg["team"] != "revops" {
		t.Fatalf("cohort not stamped: %v", forwarded["segment"])
	}
	if ok, _ := agent.HandleContribution(map[string]any{"name": "only"}); ok {
		t.Fatal("missing fields accepted")
	}
}

func TestShouldIdleExit(t *testing.T) {
	now := time.Now()
	if !ShouldIdleExit(now, now.Add(-1000*time.Second), 900, false) {
		t.Fatal("idle long enough should exit")
	}
	if ShouldIdleExit(now, now.Add(-500*time.Second), 900, false) {
		t.Fatal("recently active should stay")
	}
	if ShouldIdleExit(now, now.Add(-100000*time.Second), 900, true) {
		t.Fatal("busy (synthesizing) should stay")
	}
	if ShouldIdleExit(now, now.Add(-100000*time.Second), 0, false) {
		t.Fatal("idleSecs=0 disables idle-exit")
	}
}

func TestHTTPShellAuthAndRoundtrip(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	ts := httptest.NewServer(Handler(agent, "secret"))
	defer ts.Close()

	// health is open
	resp, err := http.Get(ts.URL + "/v1/hooks/health")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health: %v", err)
	}
	resp.Body.Close()

	post := func(path, key, body string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", ts.URL+path, bytes.NewReader([]byte(body)))
		if key != "" {
			req.Header.Set("X-Tacit-Key", key)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}

	if code, _ := post("/v1/hooks/claude-code", "", `{}`); code != 401 {
		t.Fatalf("keyless hook = %d", code)
	}
	if code, _ := post("/v1/hooks/claude-code", "secret",
		`{"hook_event_name": "SessionStart", "session_id": "s"}`); code != 200 {
		t.Fatalf("keyed hook = %d", code)
	}
	post("/v1/hooks/claude-code", "secret",
		`{"hook_event_name": "UserPromptSubmit", "session_id": "s", "prompt": "pasted warehouse rows"}`)
	if code, _ := post("/v1/hooks/claude-code", "secret",
		`{"hook_event_name": "Stop", "session_id": "s"}`); code != 200 {
		t.Fatalf("stop = %d", code)
	}
	code, body := post("/v1/hooks/claude-code", "secret",
		`{"hook_event_name": "UserPromptSubmit", "session_id": "s", "prompt": "carry on"}`)
	if code != 200 {
		t.Fatalf("prompt = %d", code)
	}
	out, _ := body["hookSpecificOutput"].(map[string]any)
	if ctx, _ := out["additionalContext"].(string); !strings.Contains(ctx, questionHeader) {
		t.Fatalf("suggestion not delivered over HTTP: %v", body)
	}
}

func TestStatsEndpoint(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	ts := httptest.NewServer(Handler(agent, "")) // stats is open, like health
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/hooks/stats?session_id=sess_1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Shown   int `json:"shown"`
		Session *struct {
			Shown int `json:"shown"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Shown != 1 || out.Session == nil || out.Session.Shown != 1 {
		t.Fatalf("stats: %+v", out)
	}
}

// The relay's client classification reaches the per-session stats without
// touching delivery: the ◆ block still goes out on a bridged session, because
// "a bridge is attached" is not "the member is reading from it". Recording it
// is what lets a real Claude Code session answer whether hook subprocesses see
// CLAUDE_CODE_BRIDGE_SESSION_ID at all.
func TestClientClassificationReachesStatsWithoutChangingDelivery(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	ts := httptest.NewServer(Handler(agent, ""))
	defer ts.Close()

	post := func(event string, body map[string]any) *http.Response {
		body["hook_event_name"] = event
		body["session_id"] = "sess_1"
		payload, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/v1/hooks/claude-code", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tacit-Client", "bridge")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	post("SessionStart", map[string]any{"source": "startup"}).Body.Close()
	post("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}).Body.Close()
	post("Stop", map[string]any{}).Body.Close()
	next := post("UserPromptSubmit", map[string]any{"prompt": "carry on"})
	var deliveredTo map[string]any
	json.NewDecoder(next.Body).Decode(&deliveredTo)
	next.Body.Close()

	if !strings.Contains(delivered(deliveredTo), questionHeader) {
		t.Fatalf("a bridged session must still be delivered to, got %v", deliveredTo)
	}

	resp, err := http.Get(ts.URL + "/v1/hooks/stats?session_id=sess_1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Session *struct {
			Client string `json:"client"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Session == nil || out.Session.Client != "bridge" {
		t.Fatalf("client classification missing from stats: %+v", out.Session)
	}
}

func TestRepoMarkerNudgesUnconnectedMemberExactlyOnce(t *testing.T) {
	// growth-plan mechanism 2: the repo carries the invitation. An unconnected
	// member opening a marker-carrying repo is nudged once — not per session,
	// once ever — and a connected member never is.
	repo := t.TempDir()
	sub := filepath.Join(repo, "src", "deep")
	if err := os.MkdirAll(filepath.Join(repo, ".tacit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := "# invitation\nregistry = \"https://tacit.example.com\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".tacit", "registry.toml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	agent, _ := newTestAgent(t, nil, Options{}) // RegistryConfigured=false
	start := func(sid string) HookResponse {
		return agent.Handle(map[string]any{
			"hook_event_name": "SessionStart", "session_id": sid, "cwd": sub,
		}, "claude-code")
	}
	resp := start("sess_1")
	msg, _ := resp["systemMessage"].(string)
	if !strings.Contains(msg, "https://tacit.example.com") || !strings.Contains(msg, "join link") {
		t.Fatalf("expected a join nudge, got: %v", resp)
	}
	if again := start("sess_2"); len(again) != 0 {
		t.Fatalf("nudge must fire once ever, got: %v", again)
	}

	// A connected member is never nudged, marker or not. They do get the
	// model-facing playbook notice, which is a different channel and carries
	// nothing member-visible — so assert on what the nudge actually is.
	connected, _ := newTestAgent(t, nil, Options{RegistryConfigured: true})
	if resp := connected.Handle(map[string]any{
		"hook_event_name": "SessionStart", "session_id": "s", "cwd": sub,
	}, "claude-code"); resp["systemMessage"] != nil {
		t.Fatalf("connected member nudged: %v", resp)
	}
}

// TestSegmentAskOffersCohortsAlreadyInUse: asked cold, a member invents a
// second spelling of a cohort that already exists. So the ask carries the
// menu — the values colleagues use, and a command built from the top of it —
// and falls back to the generic wording only when the registry has nothing to
// offer or cannot be reached.
func TestSegmentAskOffersCohortsAlreadyInUse(t *testing.T) {
	clock := time.Now()
	base := func(cohorts func() ([]CohortDim, error)) Options {
		return Options{RegistryConfigured: true, RegistryURL: "https://tacit.example.com",
			Segment: contracts.Segment{}, Cohorts: cohorts,
			Now: func() time.Time { return clock }}
	}
	prompt := func(a *Agent, sid string) string {
		resp := a.Handle(map[string]any{
			"hook_event_name": "UserPromptSubmit", "session_id": sid,
			"prompt": "refactor the parser",
		}, "claude-code")
		msg, _ := resp["systemMessage"].(string)
		return msg
	}

	agent, _ := newTestAgent(t, nil, base(func() ([]CohortDim, error) {
		return []CohortDim{
			{Name: "team", Values: []string{"payments", "platform", "growth"}},
			{Name: "role", Values: []string{"engineer", "analyst"}},
		}, nil
	}))
	// SessionStart warms the directory so the very first prompt — which may be
	// the only one this member ever gets asked on — already has the menu.
	agent.Handle(map[string]any{"hook_event_name": "SessionStart", "session_id": "s1"}, "claude-code")
	metSomething(agent, clock)
	msg := prompt(agent, "s1")
	if !strings.Contains(msg, "team: payments, platform, growth") ||
		!strings.Contains(msg, "role: engineer, analyst") {
		t.Fatalf("expected the cohorts in use, got: %s", msg)
	}
	if !strings.Contains(msg, "tacit connect --segment team=payments,role=engineer") {
		t.Fatalf("expected a command built from the most-used values, got: %s", msg)
	}

	// A registry that cannot answer must never cost the ask: the member still
	// gets the generic version rather than silence.
	broken, _ := newTestAgent(t, nil, base(func() ([]CohortDim, error) {
		return nil, errors.New("registry down")
	}))
	broken.Handle(map[string]any{"hook_event_name": "SessionStart", "session_id": "s"}, "claude-code")
	metSomething(broken, clock)
	if msg := prompt(broken, "s"); !strings.Contains(msg, "--segment") ||
		strings.Contains(msg, "colleagues already use —") {
		t.Fatalf("expected the generic ask when the directory is unavailable, got: %s", msg)
	}

	// An org with no cohorts yet has no menu to draw; the first member is told
	// to invent one, which is genuinely what they must do.
	first, _ := newTestAgent(t, nil, base(func() ([]CohortDim, error) { return nil, nil }))
	metSomething(first, clock)
	first.Handle(map[string]any{"hook_event_name": "SessionStart", "session_id": "s"}, "claude-code")
	if msg := prompt(first, "s"); strings.Contains(msg, "colleagues already use —") {
		t.Fatalf("expected no menu on an untagged registry, got: %s", msg)
	}
}

// TestCohortMenuTruncates: the ask is three lines in a terminal, so a big org's
// tail is counted rather than printed — and never silently dropped.
func TestCohortMenuTruncates(t *testing.T) {
	var vals []string
	for i := range 9 {
		vals = append(vals, string(rune('a'+i))+"-team")
	}
	menu, example := cohortMenu([]CohortDim{{Name: "team", Values: vals}})
	if !strings.Contains(menu, "(+4 more)") || strings.Contains(menu, "f-team") {
		t.Fatalf("menu = %q", menu)
	}
	if example != "team=a-team" {
		t.Fatalf("example = %q", example)
	}
	if menu, _ := cohortMenu([]CohortDim{{Name: "team"}}); menu != "" {
		t.Fatalf("a dimension with no values is not a menu: %q", menu)
	}
}

func TestSegmentAskPromptsConnectedUnsegmentedMemberDaily(t *testing.T) {
	// The inline counterpart of `tacit connect --segment`: a connected member
	// whose machine has no cohort is asked at their prompt, visibly, with the
	// model in the loop so a natural-language answer completes the save. The
	// ask RENEWS — at most once a day per registry — until a cohort is set or
	// the member declines.
	//
	// It does not fire during first use. Every agent below that expects the
	// ask has met a technique first, because a member who has been shown
	// nothing is owed something before they are asked for data cleanup
	// (docs/distribution/first-adoption-plan.md, 1.8).
	clock := time.Now()
	unsegmented := Options{RegistryConfigured: true,
		RegistryURL: "https://tacit.example.com", Segment: contracts.Segment{},
		Now: func() time.Time { return clock }}
	agent, _ := newTestAgent(t, nil, unsegmented)
	metSomething(agent, clock)
	prompt := func(a *Agent, sid, harness string) HookResponse {
		return a.Handle(map[string]any{
			"hook_event_name": "UserPromptSubmit", "session_id": sid,
			"prompt": "refactor the parser",
		}, harness)
	}

	resp := prompt(agent, "s1", "claude-code")
	msg, _ := resp["systemMessage"].(string)
	if !strings.Contains(msg, "--segment") || !strings.Contains(msg, "cohort") {
		t.Fatalf("expected the cohort ask, got: %v", resp)
	}
	if again := prompt(agent, "s2", "claude-code"); len(again) != 0 {
		t.Fatalf("cohort ask must fire at most once a day, got: %v", again)
	}
	clock = clock.Add(25 * time.Hour)
	if tomorrow := prompt(agent, "s3", "claude-code"); tomorrow["systemMessage"] == nil {
		t.Fatalf("cohort ask must renew after a day, got: %v", tomorrow)
	}

	// A member whose cohort IS set is never asked (newTestAgent defaults the
	// segment to team=revops when nil).
	segmented, _ := newTestAgent(t, nil, Options{RegistryConfigured: true,
		RegistryURL: "https://tacit.example.com"})
	if resp := prompt(segmented, "s", "claude-code"); len(resp) != 0 {
		t.Fatalf("segmented member asked: %v", resp)
	}

	// An unconnected member is never asked — the join nudge owns that moment.
	unconnected, _ := newTestAgent(t, nil, Options{
		RegistryURL: "https://tacit.example.com", Segment: contracts.Segment{}})
	if resp := prompt(unconnected, "s", "claude-code"); len(resp) != 0 {
		t.Fatalf("unconnected member asked: %v", resp)
	}

	// A deliverless harness renders no hook output, so the ask is suppressed
	// AND not consumed: the same member's next visible-harness prompt gets it.
	both, _ := newTestAgent(t, nil, unsegmented)
	metSomething(both, clock)
	if resp := prompt(both, "cop", "copilot"); len(resp) != 0 {
		t.Fatalf("ask leaked on a deliverless harness: %v", resp)
	}
	if resp := prompt(both, "cc", "claude-code"); len(resp) == 0 {
		t.Fatal("ask was consumed by the deliverless harness")
	}

	// On Cursor the ask rides the one visible channel: user_message at
	// beforeSubmitPrompt, continue always true.
	cur, _ := newTestAgent(t, nil, unsegmented)
	metSomething(cur, clock)
	resp = cur.Handle(map[string]any{
		"hook_event_name": "beforeSubmitPrompt", "conversation_id": "c1",
		"prompt": "refactor the parser",
	}, "cursor")
	um, _ := resp["user_message"].(string)
	if !strings.Contains(um, "--segment") || resp["continue"] != true {
		t.Fatalf("expected the cursor-visible cohort ask, got: %v", resp)
	}
}

// statusErr mimics the client package's typed HTTP rejection (matched
// structurally — the hooks package must not need the client import).
type statusErr int

func (e statusErr) Error() string   { return fmt.Sprintf("HTTP %d", int(e)) }
func (e statusErr) HTTPStatus() int { return int(e) }

func TestRegistryHealthDistinguishesRotatedKeyFromOutage(t *testing.T) {
	// A rotated key means every retrieval 401s while everything looks quiet.
	// One rejection is enough to demand an operator; connectivity blips need
	// three in a row before they count as a standing fault.
	agent, _ := newTestAgent(t, nil, Options{})

	agent.noteRegistry(statusErr(401))
	state, needs, _, _ := agent.RegistryHealth()
	if state != "unauthorized" || !needs {
		t.Fatalf("401 should be unauthorized+needs-operator, got %s/%v", state, needs)
	}

	// Recovery: one successful call clears the fault entirely.
	agent.noteRegistry(nil)
	if state, needs, _, _ = agent.RegistryHealth(); state != "ok" || needs {
		t.Fatalf("after success: %s/%v; want ok/false", state, needs)
	}

	// Outage: transient until persistent.
	agent.noteRegistry(errors.New("dial tcp: connection refused"))
	agent.noteRegistry(errors.New("dial tcp: connection refused"))
	if state, needs, _, _ = agent.RegistryHealth(); state != "unreachable" || needs {
		t.Fatalf("2 consecutive failures should not yet page anyone: %s/%v", state, needs)
	}
	agent.noteRegistry(errors.New("dial tcp: connection refused"))
	if state, needs, _, _ = agent.RegistryHealth(); state != "unreachable" || !needs {
		t.Fatalf("3rd consecutive failure should demand an operator: %s/%v", state, needs)
	}

	// The stats endpoint wears it, with the remedy.
	ts := httptest.NewServer(Handler(agent, ""))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/hooks/stats")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Registry struct {
			State         string `json:"state"`
			NeedsOperator bool   `json:"needs_operator"`
			Remedy        string `json:"remedy"`
		} `json:"registry"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Registry.State != "unreachable" || !out.Registry.NeedsOperator || out.Registry.Remedy == "" {
		t.Fatalf("stats registry block: %+v", out.Registry)
	}
}

func TestEvidenceCallFeedsRegistryHealth(t *testing.T) {
	// The tracking is wired at the one call every Stop makes: retrieval.
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, statusErr(401)
	}
	agent := NewAgent(provider, stubLLM{}, nil, nil, Options{RunAsync: inline})
	agent.Handle(ev("SessionStart", nil), "claude-code")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "hi"}), "claude-code")
	agent.Handle(ev("Stop", nil), "claude-code")
	if state, needs, _, _ := agent.RegistryHealth(); state != "unauthorized" || !needs {
		t.Fatalf("registry health after 401 retrieval: %s/%v", state, needs)
	}
}

func TestDraftCountCachedAndExposed(t *testing.T) {
	calls := 0
	agent, _ := newTestAgent(t, nil, Options{Drafts: func() (int, error) {
		calls++
		return 7, nil
	}})

	// First read is "unknown" but kicks off the (inline, in tests) refresh.
	if n, ok := agent.DraftsCount(); ok {
		t.Fatalf("first read should be unknown, got %d", n)
	}
	// Now the cached value is available, and stays cached within the TTL.
	if n, ok := agent.DraftsCount(); !ok || n != 7 {
		t.Fatalf("draft count = %d, ok=%v; want 7, true", n, ok)
	}
	agent.DraftsCount()
	if calls != 1 {
		t.Fatalf("draft count should be cached within TTL: %d registry calls", calls)
	}

	// No Drafts seam → always unknown (the indicator is simply omitted).
	bare, _ := newTestAgent(t, nil, Options{})
	if _, ok := bare.DraftsCount(); ok {
		t.Fatal("agent without a Drafts seam should report unknown")
	}

	// The stats endpoint carries drafts once known, and omits it when not.
	ts := httptest.NewServer(Handler(agent, ""))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/hooks/stats")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if d, ok := out["drafts"].(float64); !ok || int(d) != 7 {
		t.Fatalf("stats drafts = %v; want 7", out["drafts"])
	}
}

func TestKeylessAgentServesOpen(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{})
	ts := httptest.NewServer(Handler(agent, ""))
	defer ts.Close()
	for _, harness := range []string{"claude-code", "codex", "amp", "pi", "omp", "opencode"} {
		resp, err := http.Post(ts.URL+"/v1/hooks/"+harness, "application/json",
			bytes.NewReader([]byte(`{"hook_event_name": "SessionStart", "session_id": "s"}`)))
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("keyless-open %s failed: %v", harness, err)
		}
		resp.Body.Close()
	}
}

// The suggestion gate throttles COACHING, not OBSERVATION. It used to block the
// whole audit: once a session had spent its suggestion budget, no evidence call
// ever ran again, so no audit fact was recorded — the learning layer went blind
// for the rest of the session, biased toward session openings, with nothing in
// the data marking the missing turns.
func TestObservationOutlivesTheSuggestionCap(t *testing.T) {
	agent, log, seen := newTestAgentSeeing(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 1, CooldownTurns: 1, CooldownFor: -1})
	driveToSuggestion(agent) // shows the one allowed technique

	// Five more full turns after the cap is exhausted.
	for i := 0; i < 5; i++ {
		agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "keep working on the migration"}), "claude-code")
		agent.Handle(ev("Stop", nil), "claude-code")
	}

	if got := len(log.byStage("shown")); got != 1 {
		t.Fatalf("shown = %d, want 1 — the cap must still hold for coaching", got)
	}
	// Every one of those Stops must still have reached retrieval (= a fact).
	if got := len(seen.chars); got < 6 {
		t.Fatalf("retrieval called %d times for 6 Stops — observation is still "+
			"being censored by the suggestion gate", got)
	}
	// And each observation is its own audit, not a reuse of the last one.
	ids := map[string]bool{}
	for _, ch := range seen.chars {
		if ch.AuditID == "" {
			t.Fatal("post-cap observation reached retrieval without an audit id")
		}
		ids[ch.AuditID] = true
	}
	if len(ids) != len(seen.chars) {
		t.Fatalf("audit ids reused across observations: %d ids for %d audits", len(ids), len(seen.chars))
	}
}

// A Stop while a suggestion is mid-synthesis must not stomp the in-flight
// reservation: the observation still runs, but only the owner of the
// reservation may clear it.
func TestObservationDoesNotStompInFlightReservation(t *testing.T) {
	agent, _, seen := newTestAgentSeeing(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 3, CooldownTurns: 0, CooldownFor: -1})
	agent.Handle(ev("SessionStart", map[string]any{"source": "startup"}), "claude-code")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "first turn"}), "claude-code")

	// Simulate a suggestion mid-flight, exactly as a slow synth would leave it.
	agent.mu.Lock()
	var st *sessionState
	for _, s := range agent.sessions {
		st = s
	}
	st.synthesizing = true
	agent.mu.Unlock()

	agent.Handle(ev("Stop", nil), "claude-code")

	if len(seen.chars) == 0 {
		t.Fatal("observation skipped while a suggestion was in flight")
	}
	agent.mu.Lock()
	stillReserved := st.synthesizing
	agent.mu.Unlock()
	if !stillReserved {
		t.Fatal("the observation-only path cleared a reservation it never took")
	}
}

// End to end through the agent: the characterization the registry receives —
// and files as this audit's fact — describes the turn, not the session-so-far.
func TestEachAuditDescribesItsOwnTurn(t *testing.T) {
	agent, _, seen := newTestAgentSeeing(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: -1}) // observation only; no coaching noise
	agent.Handle(ev("SessionStart", map[string]any{"source": "startup", "cwd": "/home/x/Repos/tacit"}), "claude-code")

	// Turn 1: an edit through the warehouse MCP.
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "fix the rollup"}), "claude-code")
	agent.Handle(ev("PostToolUse", map[string]any{"tool_name": "Edit",
		"tool_input": map[string]any{"file_path": "a.go"}}), "claude-code")
	agent.Handle(ev("PostToolUse", map[string]any{"tool_name": "mcp__warehouse__query",
		"tool_input": map[string]any{"q": "x"}}), "claude-code")
	agent.Handle(ev("Stop", nil), "claude-code")

	// Turn 2: pure conversation.
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "walk me through the design"}), "claude-code")
	agent.Handle(ev("Stop", nil), "claude-code")

	if len(seen.chars) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(seen.chars))
	}
	t1, t2 := seen.chars[0], seen.chars[1]
	if t1.TaskType != "editing" {
		t.Fatalf("turn 1 task_type = %q, want editing", t1.TaskType)
	}
	if t2.TaskType != "conversation" {
		t.Fatalf("turn 2 task_type = %q — turn 1's tools leaked in (the old cumulative drift)", t2.TaskType)
	}
	if len(t2.ToolsUsed) != 0 {
		t.Fatalf("turn 2 tools_used = %v, want none", t2.ToolsUsed)
	}
	// Session-scoped context survives the boundary.
	found := map[string]bool{}
	for _, r := range t2.InternalResourcesInPlay {
		found[r] = true
	}
	if !found["repo:tacit"] || !found["mcp:warehouse"] {
		t.Fatalf("turn 2 lost session resources: %v", t2.InternalResourcesInPlay)
	}
}

// verifyingLLM is a stub that can judge adoption, with a canned verdict.
type verifyingLLM struct {
	stubLLM
	verdict bool
	err     error
	asked   []string // messages it was asked to judge
}

func (v *verifyingLLM) VerifyAdoption(message, techniqueName, recipe string) (bool, error) {
	v.asked = append(v.asked, message)
	return v.verdict, v.err
}

// The core of finding #4: a message that merely SHARES WORDS with a technique is a
// candidate, not an adoption. The model confirms or refutes it; only confirmed
// candidates become evidence. helped_rate's denominator is built here.
func TestTextualAdoptionIsVerifiedNotAssumed(t *testing.T) {
	for _, tc := range []struct {
		name        string
		verdict     bool
		wantAdopted int
	}{
		{"confirmed adoption records", true, 1},
		{"vocabulary collision refuted", false, 0},
	} {
		agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
		v := &verifyingLLM{verdict: tc.verdict}
		agent.llm = v
		driveToSuggestion(agent)
		log.clear() // drop the shown event; count adoptions only

		// Two overlapping tokens with the technique ("warehouse", "connector") — a
		// lexical candidate either way; the verdict decides.
		agent.Handle(ev("UserPromptSubmit",
			map[string]any{"prompt": "ok, switching to the warehouse connector for this"}), "claude-code")

		if len(v.asked) != 1 {
			t.Fatalf("%s: model asked %d times, want 1", tc.name, len(v.asked))
		}
		if got := len(log.byStage("adopted")); got != tc.wantAdopted {
			t.Fatalf("%s: adopted events = %d, want %d", tc.name, got, tc.wantAdopted)
		}
	}
}

// One shared word is no longer an adoption — for text OR generic tool calls.
func TestSingleTokenOverlapNoLongerAdopts(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	v := &verifyingLLM{verdict: true}
	agent.llm = v
	driveToSuggestion(agent)
	log.clear()

	// Textual: "warehouse" alone — not even a candidate; the model isn't asked.
	agent.Handle(ev("UserPromptSubmit",
		map[string]any{"prompt": "the warehouse team pinged me about something else"}), "claude-code")
	if len(v.asked) != 0 {
		t.Fatalf("single-token text became a candidate: asked=%v", v.asked)
	}
	// Behavioral, non-MCP: a Bash call sharing one word with the recipe.
	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "Bash", "tool_input": "grep warehouse ./docs"}), "claude-code")
	if got := len(log.byStage("adopted")); got != 0 {
		t.Fatalf("single-token tool call adopted: %d events", got)
	}
}

// Calling an org system the recipe names IS the move being made: an MCP tool
// whose server matches the technique records directly, even though a server name is
// inherently one token.
func TestMCPServerMatchIsDirectBehavioralAdoption(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	log.clear()

	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "mcp__warehouse__execute", "tool_input": map[string]any{}}), "claude-code")
	if got := len(log.byStage("adopted")); got != 1 {
		t.Fatalf("mcp server match should adopt directly, got %d events", got)
	}
}

// A behavioral adoption landing while a textual verification is in flight must
// not double-record when the verification confirms.
func TestVerifiedAdoptionDoesNotDoubleRecord(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{SynthBudget: time.Millisecond}) // don't wait out the same-turn budget on parked synth
	deferred := &deferredRunner{}
	agent.opts.RunAsync = deferred.run // park async work; release manually
	v := &verifyingLLM{verdict: true}
	agent.llm = v
	driveToSuggestion(agent)
	// The suggestion synth itself ran through the deferred runner — flush it.
	deferred.flush()
	log.clear()

	// Textual candidate: verification parks in the deferred runner.
	agent.Handle(ev("UserPromptSubmit",
		map[string]any{"prompt": "switching to the warehouse connector now"}), "claude-code")
	// Behavioral adoption lands first.
	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "mcp__warehouse__query", "tool_input": map[string]any{}}), "claude-code")
	// Now the verification completes and confirms.
	deferred.flush()

	if got := len(log.byStage("adopted")); got != 1 {
		t.Fatalf("adopted events = %d, want exactly 1 (no double record)", got)
	}
}

// deferredRunner parks async fns so a test controls exactly when they run.
type deferredRunner struct{ fns []func() }

func (d *deferredRunner) run(fn func()) { d.fns = append(d.fns, fn) }
func (d *deferredRunner) flush() {
	fns := d.fns
	d.fns = nil
	for _, fn := range fns {
		fn()
	}
}

// writeFile is a test convenience.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// The two halves of the member-local memory, end to end through the agent:
// an adopted technique feeds used_technique_ids on the NEXT session's audits
// (finding #6 — the "you use N of M" input that was always empty), and a
// dismissed technique is never offered again by a fresh agent on the same machine
// (finding #7 — cross-session repetition).
func TestMemoryPersonalizesAndSuppressesAcrossSessions(t *testing.T) {
	memPath := t.TempDir() + "/memory.json"
	opts := Options{TechniqueMemoryPath: memPath}

	// Session 1: technique shown; the member adopts behaviorally, then dismisses is
	// not possible after adopt — use a second technique? One technique suffices for both
	// assertions: adopt it.
	agent1, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, opts)
	driveToSuggestion(agent1)
	agent1.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "mcp__warehouse__query", "tool_input": map[string]any{}}), "claude-code")

	// Session 2: a fresh agent (same machine). Its very first audit must carry
	// the used id, and must NOT re-offer the technique the member already uses.
	agent2, log2, seen2 := newTestAgentSeeing(t, []contracts.EvidenceCandidate{orgCandidate()}, opts)
	driveToSuggestion(agent2)

	ch, ok := seen2.last()
	if !ok {
		t.Fatal("no observation in session 2")
	}
	if len(ch.UsedTechniqueIDs) != 1 || ch.UsedTechniqueIDs[0] != "use-internal-data-connector" {
		t.Fatalf("used_technique_ids = %v — the personalization input is still unfed", ch.UsedTechniqueIDs)
	}
	if got := len(log2.byStage("shown")); got != 0 {
		t.Fatalf("session 2 re-offered a technique the member demonstrably uses: %d shown", got)
	}
}

// The review list: a shown suggestion is retained with its evidence line and
// its status follows the verdicts — so a persistent surface (the opencode
// sidebar) can show what a transient toast could not.
func TestSuggestionReviewList(t *testing.T) {
	measured := orgCandidate()
	measured.Outcomes = map[string]any{"helped_rate": 0.94, "sample_size": 120.0}
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{measured}, Options{})
	driveToSuggestion(agent)

	list := agent.SuggestionsFor("sess_1")
	if len(list) != 1 {
		t.Fatalf("review list = %+v, want one entry", list)
	}
	sg := list[0]
	if sg.TechniqueID != "use-internal-data-connector" || sg.Status != "shown" {
		t.Fatalf("entry: %+v", sg)
	}
	if !strings.Contains(sg.Evidence, "helped 94%") || !strings.Contains(sg.Evidence, "n=120") {
		t.Fatalf("evidence line should be the verbatim measured line: %+v", sg)
	}
	if sg.ShownAt == "" {
		t.Fatal("shown_at missing")
	}

	// A dismissal verdict lands on the list.
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "not relevant to what I'm doing"}), "claude-code")
	if got := agent.SuggestionsFor("sess_1")[0].Status; got != "dismissed" {
		t.Fatalf("status after dismissal = %q", got)
	}
	// Terminal verdicts never regress.
	agent.mu.Lock()
	st := agent.sessions["claude-code:sess_1"]
	st.setSuggestionStatusLocked("use-internal-data-connector", "adopted")
	agent.mu.Unlock()
	if got := agent.SuggestionsFor("sess_1")[0].Status; got != "dismissed" {
		t.Fatalf("dismissed regressed to %q", got)
	}
	// Unknown session: empty, not someone else's list.
	if agent.SuggestionsFor("other") != nil {
		t.Fatal("foreign session returned a list")
	}
}

// The endpoint that persistent surfaces poll.
func TestSuggestionsEndpoint(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	driveToSuggestion(agent)
	ts := httptest.NewServer(Handler(agent, ""))
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/hooks/suggestions?session_id=sess_1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Suggestions []ShownSuggestion `json:"suggestions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Suggestions) != 1 || body.Suggestions[0].Status != "shown" {
		t.Fatalf("endpoint body: %+v", body)
	}
}

// --- evidence-gated autonomy (docs/delivery/agent-delivery-plan.md) ---------

// evAgent is ev() plus the relay's consumer classification for an autonomous
// session (the server folds the X-Tacit-Consumer header into the payload).
func evAgent(name string, fields map[string]any) map[string]any {
	payload := ev(name, fields)
	payload["tacit_consumer"] = "agent"
	return payload
}

func eligibleCandidate() contracts.EvidenceCandidate {
	c := orgCandidate()
	c.AutonomyEligible = true
	return c
}

// The acceptance test's core: in a consumer:agent session, an
// autonomy-eligible technique is parked at Stop (nothing renders) and delivered on
// the next prompt as additionalContext ONLY — applied, not surfaced — with
// the shown event ledgered delivery=applied under the agent cohort.
func TestAgentSessionAppliesEligibleTechniqueSilently(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{eligibleCandidate()}, Options{})
	agent.Handle(evAgent("SessionStart", map[string]any{"source": "startup"}), "claude-code")
	agent.Handle(evAgent("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "claude-code")
	stop := agent.Handle(evAgent("Stop", nil), "claude-code")
	if _, has := stop["systemMessage"]; has {
		t.Fatalf("eligible technique in an agent session must not render at Stop: %v", stop)
	}

	next := agent.Handle(evAgent("UserPromptSubmit", map[string]any{"prompt": "now the second task"}), "claude-code")
	if _, has := next["systemMessage"]; has {
		t.Fatalf("silent application must not carry a systemMessage: %v", next)
	}
	hso, _ := next["hookSpecificOutput"].(map[string]any)
	ctx, _ := hso["additionalContext"].(string)
	if ctx == "" {
		t.Fatalf("silent application must feed the model via additionalContext: %v", next)
	}
	if !strings.Contains(ctx, "Query live warehouse data") {
		t.Fatalf("applied context should carry the technique: %q", ctx)
	}

	shown := log.byStage("shown")
	if len(shown) != 1 {
		t.Fatalf("want 1 shown event, got %d", len(shown))
	}
	if shown[0].Delivery != "applied" {
		t.Fatalf("ledger: shown delivery = %q, want applied", shown[0].Delivery)
	}
	if shown[0].Segment["consumer"] != "agent" {
		t.Fatalf("shown segment consumer = %q, want agent", shown[0].Segment["consumer"])
	}
}

// An ineligible technique in the same autonomous session stays a visible
// suggestion — policy is earned per technique, never assumed for the session.
func TestAgentSessionSurfacesIneligibleTechniqueVisibly(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	agent.Handle(evAgent("SessionStart", map[string]any{"source": "startup"}), "claude-code")
	agent.Handle(evAgent("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "claude-code")
	agent.Handle(evAgent("Stop", nil), "claude-code")
	offer := agent.Handle(evAgent("UserPromptSubmit", map[string]any{"prompt": "carry on"}), "claude-code")
	if !strings.Contains(delivered(offer), questionHeader) {
		t.Fatalf("ineligible technique must surface to the member, not be applied silently: %v", offer)
	}
	shown := log.byStage("shown")
	if len(shown) != 1 || shown[0].Delivery != "suggested" {
		t.Fatalf("ledger: want 1 shown with delivery=suggested, got %+v", shown)
	}
}

// A human session never gets silent application, however eligible the technique.
func TestHumanSessionAlwaysSeesSuggestions(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{eligibleCandidate()}, Options{})
	resp := driveToSuggestion(agent)
	if !strings.Contains(delivered(resp), questionHeader) {
		t.Fatalf("human session must see the suggestion: %v", resp)
	}
	shown := log.byStage("shown")
	if len(shown) != 1 || shown[0].Delivery != "suggested" {
		t.Fatalf("ledger: want delivery=suggested, got %+v", shown)
	}
	if shown[0].Segment["consumer"] != "human" {
		t.Fatalf("unclassified session must default to consumer=human, got %q",
			shown[0].Segment["consumer"])
	}
}

// In an autonomous session the helped clock (member turns) never ticks;
// passing verification in the turn stands in for the missing reaction and
// closes the loop with the `verification` confidence class.
func TestVerificationStandsInForTheMissingReaction(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	agent.Handle(evAgent("SessionStart", map[string]any{"source": "startup"}), "claude-code")
	agent.Handle(evAgent("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "claude-code")
	agent.Handle(evAgent("Stop", nil), "claude-code")
	// The form request is delivered on the next prompt; lastShown is set there.
	agent.Handle(evAgent("UserPromptSubmit", map[string]any{"prompt": "carry on"}), "claude-code")

	// The agent demonstrably does the recommended move (the MCP server the
	// recipe names) — a behavioral adoption fires at PreToolUse…
	agent.Handle(evAgent("PreToolUse", map[string]any{
		"tool_name":  "mcp__warehouse__query",
		"tool_input": map[string]any{"query": "select * from revenue.pipeline"},
	}), "claude-code")
	if len(log.byStage("adopted")) != 1 {
		t.Fatalf("expected a behavioral adoption, got %+v", log.all())
	}
	// …then verification passes in the same turn (PostToolUse carries the
	// output the capture records).
	agent.Handle(evAgent("PreToolUse", map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "go test ./..."},
	}), "claude-code")
	agent.Handle(evAgent("PostToolUse", map[string]any{
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": "go test ./..."},
		"tool_response": "ok  \ttacit/internal/x\t0.01s\nPASS",
	}), "claude-code")
	agent.Handle(evAgent("Stop", nil), "claude-code")

	helped := log.byStage("helped")
	if len(helped) != 1 {
		t.Fatalf("want 1 verification helped event, got %+v", log.all())
	}
	if helped[0].Confidence != "verification" {
		t.Fatalf("confidence = %q, want verification", helped[0].Confidence)
	}
}

// The ask path is only reachable if the model knows the playbook is there.
// Tool descriptions used to carry that job and stopped being enough once
// harnesses began deferring tool schemas, so SessionStart says it outright.
func TestSessionStartTellsTheModelThePlaybookExists(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{RegistryConfigured: true})
	resp := agent.Handle(ev("SessionStart", map[string]any{"source": "startup"}), "claude-code")

	out, ok := resp["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("no model-facing context at SessionStart: %v", resp)
	}
	if out["hookEventName"] != "SessionStart" {
		t.Errorf("wrong event name: %v", out["hookEventName"])
	}
	ctx, _ := out["additionalContext"].(string)
	if !strings.Contains(ctx, "tacit_search") {
		t.Errorf("context never names the tool: %q", ctx)
	}
	// Model-facing only: the member gets no banner for opening a terminal.
	if _, visible := resp["systemMessage"]; visible {
		t.Errorf("SessionStart put a message in front of the member: %v", resp)
	}
}

// A member with no registry has nothing to search, and a harness that cannot
// carry model-facing context at SessionStart must not be sent any.
func TestSessionStartContextIsWithheldWhenItWouldMislead(t *testing.T) {
	unconfigured, _ := newTestAgent(t, nil, Options{})
	if resp := unconfigured.Handle(ev("SessionStart", nil), "claude-code"); len(resp) != 0 {
		t.Errorf("unconfigured member was told about a playbook they have no access to: %v", resp)
	}

	configured, _ := newTestAgent(t, nil, Options{RegistryConfigured: true})
	for _, harness := range []string{"codex", "cursor", "amp", "pi", "opencode", "gemini", "copilot"} {
		if resp := configured.Handle(ev("SessionStart", nil), harness); len(resp) != 0 {
			t.Errorf("%s: sent SessionStart context on an unverified channel: %v", harness, resp)
		}
	}
}

// The notice states a standing fact about the organization and nothing about
// the turn. That is what separates it from the withdrawn tier-2 directive: it
// reads the same whether the harness shows it to the member or not.
func TestSessionStartContextCarriesNoCoaching(t *testing.T) {
	for _, leak := range []string{"why:", "try:", "recipe", "measured by colleagues", "◆"} {
		if strings.Contains(strings.ToLower(playbookNotice()), strings.ToLower(leak)) {
			t.Errorf("session notice carries turn-specific coaching (%q): %q", leak, playbookNotice())
		}
	}
}

// On a form-capable harness the ambient suggestion is an OFFER, not a printed
// block: it parks at Stop (which carries no model-facing envelope) and rides
// the next prompt as a request for the native question form.
func TestFormHarnessOffersInsteadOfPrinting(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
	agent.Handle(ev("SessionStart", map[string]any{"source": "startup"}), "claude-code")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "claude-code")

	if stop := agent.Handle(ev("Stop", nil), "claude-code"); len(stop) != 0 {
		t.Fatalf("a form harness must not print at Stop: %v", stop)
	}
	// Nothing counted yet: a suggestion nobody has been offered is not shown.
	if n := len(log.byStage("shown")); n != 0 {
		t.Fatalf("shown recorded %d event(s) before the offer reached anyone", n)
	}

	resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on"}), "claude-code")
	if _, visible := resp["systemMessage"]; visible {
		t.Errorf("the ask was echoed to the member as well as the model — the same "+
			"suggestion twice: %v", resp)
	}
	ctx := delivered(resp)
	if !strings.Contains(ctx, questionHeader) {
		t.Fatalf("no form was requested: %q", ctx)
	}
	for _, opt := range []string{optApply, optShowHow, optNotRelevant, optAlreadyUse} {
		if !strings.Contains(ctx, opt) {
			t.Errorf("form request omits %q, so its answer would map to nothing", opt)
		}
	}
	if len(log.byStage("shown")) != 1 {
		t.Errorf("shown should record once, at the offer: %d", len(log.byStage("shown")))
	}
}

// The ask is visible on this channel — confirmed live, and the reason tier 2
// was reverted. What sank tier 2 was not being seen but WHAT was seen: the
// recipe, and instructions on when to press the member. So the request may
// name only what the member is about to be shown anyway.
func TestFormRequestStatesOnlyWhatTheFormWillShow(t *testing.T) {
	technique := orgCandidate()
	technique.Recipe = "@warehouse query <table> where <filter>"
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{technique}, Options{})
	agent.Handle(ev("SessionStart", map[string]any{"source": "startup"}), "claude-code")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "claude-code")
	agent.Handle(ev("Stop", nil), "claude-code")
	ctx := delivered(agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on"}), "claude-code"))

	if !strings.Contains(ctx, technique.Name) || !strings.Contains(ctx, technique.TechniqueID) {
		t.Errorf("the offer must name the technique it is offering: %q", ctx)
	}
	// The recipe is the long field with no business on screen twice; the model
	// fetches it from the registry when the member asks for it.
	if strings.Contains(ctx, technique.Recipe) {
		t.Errorf("the recipe leaked into the visible ask: %q", ctx)
	}
	if !strings.Contains(ctx, "tacit_search") {
		t.Errorf("nothing tells the model where to get the recipe verbatim: %q", ctx)
	}
}

// The evidence line is measured and travels verbatim, whichever channel
// carries it — the claim is the same claim.
func TestFormRequestCarriesTheEvidenceLineVerbatim(t *testing.T) {
	technique := orgCandidate()
	technique.Outcomes = map[string]any{"helped_rate": 0.94, "adoption_rate": 0.7, "sample_size": 120.0}
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{technique}, Options{})
	agent.Handle(ev("SessionStart", map[string]any{"source": "startup"}), "claude-code")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "claude-code")
	agent.Handle(ev("Stop", nil), "claude-code")
	ctx := delivered(agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on"}), "claude-code"))

	want := contracts.EvidenceLine(technique.Outcomes)
	if want == "" || !strings.Contains(ctx, want) {
		t.Fatalf("evidence line %q missing from the offer: %q", want, ctx)
	}
}

// Harnesses with no native question primitive keep the ◆ block. The form is a
// Claude Code delivery, not a replacement of the block everywhere.
func TestBlockStillShipsWhereThereIsNoForm(t *testing.T) {
	for _, harness := range []string{"codex", "amp", "pi", "opencode", "gemini"} {
		agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()}, Options{})
		msg := delivered(driveToSuggestionOn(agent, harness))
		if !strings.Contains(msg, Mark()) {
			t.Errorf("%s: lost its block: %q", harness, msg)
		}
		if strings.Contains(msg, "AskUserQuestion") {
			t.Errorf("%s: asked for a form it has no way to raise: %q", harness, msg)
		}
	}
}

// fitLine is the enforcement behind the offer's promise, so it is tested on its
// own: the guarantee must not depend on the model choosing to keep it.
func TestFitLineDropsAWhyThatQuotesTheRecipe(t *testing.T) {
	recipe := "@warehouse query <table> where <filter>"
	cases := []struct{ name, why, want string }{
		{"a genuine why survives",
			"the rows came from an internal warehouse; a live query skips the copy-paste",
			"the rows came from an internal warehouse; a live query skips the copy-paste"},
		{"a why that reproduces the recipe is dropped",
			"Try this: @warehouse query <table> where <filter>", ""},
		{"case and whitespace do not smuggle it through",
			"try   @WAREHOUSE   QUERY <table>   where <filter>  now", ""},
		{"whitespace is normalised", "  spread   over    lines  ", "spread over lines"},
		{"an empty why stays empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fitLine(tc.why, recipe); got != tc.want {
				t.Errorf("fitLine() = %q, want %q", got, tc.want)
			}
		})
	}
	// A short recipe line must not suppress every why by chance collision.
	if got := fitLine("this fits the work in hand", "run it"); got == "" {
		t.Error("a two-word recipe suppressed an unrelated why")
	}
}

// metSomething marks this member as having actually met the product: the
// cohort ask waits for it, so a test that wants the ask has to grant it.
func metSomething(a *Agent, at time.Time) {
	a.memory.NoteAdopted("tech-met", at)
}
