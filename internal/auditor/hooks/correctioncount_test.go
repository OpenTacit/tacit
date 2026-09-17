// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"path/filepath"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// A CORRECTION IS COUNTED IN BOTH PLACES THAT CLAIM TO COUNT IT.
//
// Two stores hold corrections and they are written by different paths: the
// ledger (corrections.go) takes the shape of the saying as it is said, and the
// session record (sessionlog.go) takes the count at the next Stop. They can
// disagree, and when they do the page shows a zero beside a panel that knows
// better — which is the fake zero the house rules forbid, wearing the clothes
// of a measurement.
//
// They HAVE disagreed. A machine with no org salt hashed every session key to
// "" and upsert refused the record, so a correction reached the ledger and
// nothing else; the local salt fixed that (localkey_test.go) and left this path
// uncovered. This is the path: a member says it, and the figure moves.
func TestACorrectionReachesTheFigureThatCountsIt(t *testing.T) {
	agent, _ := countingAgent(t)
	// The first turn is never a correction: a session opening with "don't use X"
	// is a standing instruction, not a repair.
	turn(agent, "don't use the live registry for this")
	turn(agent, "no, use the scratch registry instead")

	sum := agent.WorkSummary(0)
	if sum.Totals.Corrections != 1 {
		t.Errorf("corrections = %d, want 1 — the opening instruction counts, or the repair does not",
			sum.Totals.Corrections)
	}
	if sum.Totals.Turns != 2 {
		t.Fatalf("turns = %d, want 2", sum.Totals.Turns)
	}
}

// A SHORT CORRECTION IS STILL A CORRECTION. The ledger needs two distinctive
// words to fingerprint a saying, and it should: one word cannot be told from
// any other, and a bucket like that means nothing. The COUNT had been taken off
// that same return, so "no, use 9099" was recognised and then not counted — and
// a member who corrects their agent in four words all day read as a member who
// never corrects it at all.
func TestAShortCorrectionStillCounts(t *testing.T) {
	agent, _ := countingAgent(t)
	turn(agent, "run the tests")
	turn(agent, "no, use 9099")

	sum := agent.WorkSummary(0)
	if sum.Totals.Corrections != 1 {
		t.Errorf("corrections = %d, want 1 — a saying with one distinctive word went uncounted",
			sum.Totals.Corrections)
	}
	// The ledger keeps its own floor: there is still nothing to cluster on, so
	// saying it twice makes no repeat and the panel stays silent.
	turn(agent, "no, use 9099")
	if n := len(agent.WorkSummary(0).Repeats); n != 0 {
		t.Errorf("repeats = %d — a saying with no fingerprint was given a bucket anyway", n)
	}
	if got := agent.WorkSummary(0).Totals.Corrections; got != 2 {
		t.Errorf("corrections = %d, want 2", got)
	}
}

// And it survives the agent going away. The agent idle-exits after fifteen
// quiet minutes while the member carries on working, so the instance that
// writes a session's last record is routinely not the one that saw its first
// correction. It reads the count back off the record it is about to replace;
// without that the session keeps only what was said after the restart.
func TestCorrectionsSurviveTheAgentRestarting(t *testing.T) {
	agent, opts := countingAgent(t)
	turn(agent, "run the tests")
	turn(agent, "no, run them with the onnx build tag")

	// A second agent over the same state directory: a new process, the same
	// machine, the same session still open.
	next := NewAgent(quietEvidence, stubLLM{}, nil, nil, opts)
	turn(next, "no, never use the plain install")

	if got := next.WorkSummary(0).Totals.Corrections; got != 2 {
		t.Errorf("corrections = %d, want 2 — the restart dropped what came before it", got)
	}
}

// The same correction twice is what "Things you keep saying" is made of, and
// it is what the Corrections plate on Now opens (usage.go). One saying is not a
// repeat: the panel stays silent, and the plate stays a plate.
func TestTheSameCorrectionTwiceBecomesARepeat(t *testing.T) {
	agent, _ := countingAgent(t)
	turn(agent, "run the tests")
	turn(agent, "no, use the scratch registry instead")
	if n := len(agent.WorkSummary(0).Repeats); n != 0 {
		t.Fatalf("repeats = %d after one saying, want 0", n)
	}
	turn(agent, "no, use the scratch registry instead")
	repeats := agent.WorkSummary(0).Repeats
	if len(repeats) != 1 {
		t.Fatalf("repeats = %d after saying it twice, want 1", len(repeats))
	}
	if repeats[0].Count != 2 {
		t.Errorf("the repeat was said %d times, want 2", repeats[0].Count)
	}
}

// countingAgent and turn are this file's own: the draft path next door needs a
// distilling model and no state directory, and this one needs a state directory
// and no model.
func quietEvidence(contracts.Characterization) (contracts.EvidenceBlock, error) {
	return contracts.EvidenceBlock{}, nil
}

// countingAgent is an agent over its own state directory, and the options to
// build a second one over the same directory.
func countingAgent(t *testing.T) (*Agent, Options) {
	t.Helper()
	dir := t.TempDir()
	opts := Options{StateDir: dir, RunAsync: inline,
		UsageLogPath: filepath.Join(dir, "usage.jsonl")}
	return NewAgent(quietEvidence, stubLLM{}, nil, nil, opts), opts
}

// turn is one member turn all the way through: the message, then the Stop that
// ends it and writes the session record. (repetition_test.go's own say drives
// the prompt alone, which is all the draft path needs.)
func turn(a *Agent, prompt string) {
	a.Handle(ev("UserPromptSubmit", map[string]any{
		"prompt": prompt, "model": "claude-opus-5", "cwd": "/home/x/tacit"}), "claude-code")
	a.Handle(ev("Stop", nil), "claude-code")
}
