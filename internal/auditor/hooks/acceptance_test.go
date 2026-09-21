// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/capture"
)

// Offered and ran are two counts, and the second one must not move.
//
// "tools" has meant the calls that came back for as long as this file has
// existed, and every rate on the Usage page is computed over it. Adding a
// second count is only safe if the first one is untouched: a tool count that
// shifted under a member would move the per-session figures, the shares, the
// cross-tabs and the model comparison all at once, and nothing would say so.
func TestOfferedIsCountedWithoutMovingTheCallCount(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	var s sessionStats
	pre := func(name string) {
		s.observe(now, capture.EvPreTool, map[string]any{"tool_name": name})
	}
	post := func(name string) {
		s.observe(now, capture.EvPostTool, map[string]any{"tool_name": name,
			"tool_input": map[string]any{"command": "go test ./..."}})
	}
	// Three offers, two of which came back. The third was refused, escaped,
	// stopped by a hook, or failed — the payload says nothing more than that.
	pre("Bash")
	post("Bash")
	pre("Bash")
	post("Bash")
	pre("Edit")

	r := s.record("k", "claude-code", "claude-opus-5", "/home/x/tacit", 0)
	if r.Tools["Bash"] != 2 || r.Tools["Edit"] != 0 {
		t.Fatalf("the call count moved: %v", r.Tools)
	}
	if r.Offered["Bash"] != 2 || r.Offered["Edit"] != 1 {
		t.Fatalf("offers = %v, want Bash 2 and Edit 1", r.Offered)
	}
	// A retry is still the same tool run again with the same arguments, read at
	// PostToolUse. Counting offers must not have moved that clock.
	if r.Retries != 1 {
		t.Fatalf("retries = %d, want 1 — the retry clock moved with the offers", r.Retries)
	}
}

// A tool offered and never returned still gets a row. It is the row a member
// most wants: the tool they keep reaching for and keep not getting.
func TestATooLNeverAcceptedStillGetsARow(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l := loadSessionLog(detail, daily, fixedClock(now))
	var s sessionStats
	s.observe(now, capture.EvUserPrompt, map[string]any{})
	for i := 0; i < 4; i++ {
		s.observe(now, capture.EvPreTool, map[string]any{"tool_name": "WebFetch"})
	}
	s.observe(now, capture.EvPreTool, map[string]any{"tool_name": "Bash"})
	s.observe(now, capture.EvPostTool, map[string]any{"tool_name": "Bash", "tool_input": "ls"})
	l.upsert(s.record("k", "claude-code", "m", "/home/x/tacit", 0))

	byName := map[string]ToolUse{}
	sum := l.summarizeWork(0)
	for _, tl := range sum.Tools {
		byName[tl.Name] = tl
	}
	wf := byName["WebFetch"]
	if wf.Offers != 4 || wf.Calls != 0 {
		t.Fatalf("WebFetch = %d offers / %d calls, want 4 and 0", wf.Offers, wf.Calls)
	}
	if sum.Totals.ToolOffers != 5 || sum.Totals.ToolCalls != 1 {
		t.Fatalf("window = %d offers / %d calls, want 5 and 1",
			sum.Totals.ToolOffers, sum.Totals.ToolCalls)
	}
}

// An acceptance rate has to survive a restart. The agent idle-exits and the
// member keeps working; if the second instance counted offers from zero while
// the log held the first instance's calls, the rate would read as a session
// where most calls were refused.
func TestAcceptanceSurvivesAnAgentRestart(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l := loadSessionLog(detail, daily, fixedClock(now))

	var first sessionStats
	first.observe(now, capture.EvUserPrompt, map[string]any{})
	for i := 0; i < 6; i++ {
		first.observe(now, capture.EvPreTool, map[string]any{"tool_name": "Bash"})
		first.observe(now, capture.EvPostTool, map[string]any{"tool_name": "Bash",
			"tool_input": map[string]any{"command": "go test " + string(rune('a'+i))}})
	}
	l.upsert(first.record("k", "claude-code", "m", "/home/x/tacit", 0))

	// A new process, an empty head, the same session.
	prior, ok := l.lookup("k")
	if !ok {
		t.Fatal("the session was not in the log for the second instance to find")
	}
	var second sessionStats
	second.resume(prior)
	second.observe(now, capture.EvPreTool, map[string]any{"tool_name": "Bash"})
	l.upsert(second.record("k", "claude-code", "m", "/home/x/tacit", 0))

	sum := l.summarizeWork(0)
	if sum.Totals.ToolOffers != 7 || sum.Totals.ToolCalls != 6 {
		t.Fatalf("after the restart: %d offers / %d calls, want 7 and 6 — "+
			"a restarted instance reported a session that had shrunk",
			sum.Totals.ToolOffers, sum.Totals.ToolCalls)
	}
}

// Failures are counted by kind and the message never reaches the record. It is
// the same standard the tool inputs hold, on the one field that is most likely
// to be somebody's own work.
func TestFailuresAreCountedByKindAndNeverByMessage(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	var s sessionStats
	s.observe(now, capture.EvUserPrompt, map[string]any{})
	s.observe(now, capture.EvPostToolFail, map[string]any{"tool_name": "Read",
		"tool_response": "Error: /home/SECRETUSER/.ssh/id_rsa: No such file or directory"})
	s.observe(now, capture.EvPostToolFail, map[string]any{"tool_name": "Bash",
		"tool_response": "Exit code 1\nSECRETOUTPUT from the customer's database"})
	// A harness that flags the failure on the success event instead.
	s.observe(now, capture.EvPostTool, map[string]any{"tool_name": "Edit",
		"tool_input": "x", "tool_response": map[string]any{"is_error": true,
			"error": "String to replace not found in SECRETFILE.go"}})

	l := loadSessionLog(detail, daily, fixedClock(now))
	l.upsert(s.record("k", "claude-code", "m", "/home/x/tacit", 0))
	raw := readLines(detail)
	if len(raw) != 1 {
		t.Fatalf("want one record, got %d", len(raw))
	}
	for _, leaked := range []string{"SECRETUSER", "SECRETOUTPUT", "SECRETFILE", "id_rsa", "database"} {
		if strings.Contains(raw[0], leaked) {
			t.Fatalf("a failure message leaked %q into the record: %s", leaked, raw[0])
		}
	}
	sum := l.summarizeWork(0)
	if !sum.FailuresReported {
		t.Fatal("failures were reported and the summary says otherwise")
	}
	got := map[string]int{}
	for _, f := range sum.Failures {
		got[f.Key] = f.Count
	}
	if got[capture.FailNotFound] != 2 || got[capture.FailExit] != 1 {
		t.Fatalf("failures = %v, want two not-found and one non-zero exit", got)
	}
	// The kinds read in the vocabulary's order, not busiest-first: a bad
	// afternoon must not reshuffle the list.
	if sum.Failures[0].Key != capture.FailNotFound {
		t.Fatalf("kinds out of order: %v", sum.Failures)
	}
}

// A window nothing could report a failure in is not a window where nothing
// went wrong. Without the flag, the most flattering reading is also the
// default one.
func TestNoFailuresReportedIsNotZeroFailures(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l := loadSessionLog(detail, daily, fixedClock(now))
	l.upsert(rec("quiet", now, "anthropic/claude-opus-5", "tacit", 3))
	sum := l.summarizeWork(0)
	if sum.FailuresReported || len(sum.Failures) != 0 {
		t.Fatalf("a harness that never reported a failure was read as having measured none: %+v", sum.Failures)
	}
}

// A rate must be divided by a number drawn from the same set as its numerator.
//
// Offers began being counted on a day. A window that straddles that day holds
// sessions with calls and no offers, so dividing every call by every offer
// takes a numerator from a larger set than the denominator — and the answer
// comes out above 100%, which is how this was found: a phone screenshot saying
// "came back clean 100%" on a window with more calls than offers.
func TestTheAcceptanceRateIsDividedBySessionsThatCounted(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l := loadSessionLog(detail, daily, fixedClock(now))

	// An old session: forty calls, and nobody was counting what was offered.
	old := rec("old", now, "anthropic/claude-opus-5", "tacit", 5)
	old.Tools = map[string]int{"Bash": 40}
	old.Offered = nil
	l.upsert(old)

	// A new one: ten offered, eight ran.
	fresh := rec("new", now, "anthropic/claude-opus-5", "tacit", 5)
	fresh.Tools = map[string]int{"Bash": 8}
	fresh.Offered = map[string]int{"Bash": 10}
	l.upsert(fresh)

	sum := l.summarizeWork(0)
	if sum.Totals.ToolCalls != 48 || sum.Totals.ToolOffers != 10 {
		t.Fatalf("totals = %d calls / %d offers, want 48 and 10",
			sum.Totals.ToolCalls, sum.Totals.ToolOffers)
	}
	// The only figure that may be divided by the offers.
	if sum.Totals.ToolCallsOffered != 8 {
		t.Fatalf("calls from sessions that counted offers = %d, want 8",
			sum.Totals.ToolCallsOffered)
	}
	var bash ToolUse
	for _, tl := range sum.Tools {
		if tl.Name == "Bash" {
			bash = tl
		}
	}
	if bash.Calls != 48 || bash.Offers != 10 || bash.CallsOffered != 8 {
		t.Fatalf("Bash = %d calls / %d offers / %d paired, want 48, 10 and 8",
			bash.Calls, bash.Offers, bash.CallsOffered)
	}
}

// And the pairing survives the thirty-day horizon, or the rate goes wrong
// again the moment a window reaches past it.
func TestTheAcceptancePairingSurvivesCompaction(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	l := loadSessionLog(detail, daily, fixedClock(now))

	blind := rec("blind", old, "anthropic/claude-opus-5", "tacit", 5)
	blind.Tools = map[string]int{"Bash": 30}
	blind.Offered = nil
	l.upsert(blind)
	counted := rec("counted", old, "anthropic/claude-opus-5", "tacit", 5)
	counted.Tools = map[string]int{"Bash": 6}
	counted.Offered = map[string]int{"Bash": 9}
	l.upsert(counted)

	l.mu.Lock()
	l.compactLocked()
	l.mu.Unlock()

	sum := l.summarizeWork(0)
	if sum.Totals.ToolCalls != 36 || sum.Totals.ToolOffers != 9 || sum.Totals.ToolCallsOffered != 6 {
		t.Fatalf("after compaction: %d calls / %d offers / %d paired, want 36, 9 and 6",
			sum.Totals.ToolCalls, sum.Totals.ToolOffers, sum.Totals.ToolCallsOffered)
	}
}
