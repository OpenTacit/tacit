// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// The narrow reading technique evidence rests on, pinned. Widening the check
// vocabulary for the member's own record must not move this: a technique
// credited with a `helped` because a linter was happy is a verdict nobody
// earned.
func TestVerificationPassedStaysNarrow(t *testing.T) {
	cases := []struct {
		name string
		args string
		out  string
		want bool
	}{
		{"tests passed", `{"command":"go test ./..."}`, "ok  	tacit/internal	0.4s", true},
		{"tests failed", `{"command":"go test ./..."}`, "--- FAIL: TestX\nFAIL", false},
		{"tests said nothing", `{"command":"go test ./..."}`, "", false},
		{"passed and failed together", `{"command":"make test"}`, "3 passed, 1 failed", false},
		// A runner counting its failures at zero is the clearest pass it prints.
		{"none failed", `{"command":"cargo test"}`, "test result: ok. 88 passed; 0 failed", true},
		// A successful shell command is not a passing check. This is the
		// distinction the whole reading turns on.
		{"plain shell command", `{"command":"ls -la"}`, "ok", false},
		// A linter is a check for the member's record and NOT verification.
		{"linter happy", `{"command":"golangci-lint run"}`, "0 issues", false},
		{"build happy", `{"command":"go build ./..."}`, "build succeeded", false},
	}
	for _, c := range cases {
		got := verificationPassed([]contracts.ToolCall{{Arguments: c.args, Output: c.out}})
		if got != c.want {
			t.Errorf("%s: verificationPassed = %v, want %v", c.name, got, c.want)
		}
	}
}

// The wide reading, which the session record counts. Four states, and the
// difference between them is the whole measure: an unreadable result is not a
// failure, and a successful shell command is not a check at all.
func TestCheckOutcomeStates(t *testing.T) {
	cases := []struct {
		name    string
		command string
		result  string
		failed  bool
		state   string
		isCheck bool
	}{
		{"tests passed", `{"command":"go test ./..."}`, "ok  	tacit	0.4s", false, checkPassed, true},
		{"tests failed", `{"command":"pytest"}`, "1 failed, 2 passed", false, checkFailed, true},
		{"harness said it failed", `{"command":"go test ./..."}`, "", true, checkFailed, true},
		{"build proved nothing", `{"command":"go build ./..."}`, "", false, checkUnknown, true},
		{"type check clean", `{"command":"npx tsc --noEmit"}`, "Found 0 errors.", false, checkPassed, true},
		{"linter clean", `{"command":"mypy ."}`, "Success: no issues found in 12 files", false, checkPassed, true},
		{"lint broke", `{"command":"eslint src"}`, "exit status 1", false, checkFailed, true},
		// Not a check, whatever its output says.
		{"a listing", `{"command":"ls"}`, "ok", false, "", false},
		{"a commit", `{"command":"git commit -m done"}`, "1 file changed", false, "", false},
		{"reading a file", `{"file_path":"/x/y.go"}`, "package y", false, "", false},
	}
	for _, c := range cases {
		state, isCheck := checkOutcome(c.command, c.result, c.failed)
		if isCheck != c.isCheck || state != c.state {
			t.Errorf("%s: checkOutcome = (%q, %v), want (%q, %v)",
				c.name, state, isCheck, c.state, c.isCheck)
		}
	}
}

// Fail-then-pass is a session that got there. Pass-then-edit is not, and the
// difference is the one thing a bare "the tests passed" cannot say.
func TestCheckStateFollowsEventOrder(t *testing.T) {
	var fail sessionStats
	fail.noteCheck(checkFailed)
	fail.noteCheck(checkPassed)
	if fail.checkState != checkPassed {
		t.Errorf("fail then pass = %q, want %q", fail.checkState, checkPassed)
	}
	if fail.checksAttempted != 2 || fail.checksPassed != 1 {
		t.Errorf("counts = %d attempted / %d passed, want 2 and 1",
			fail.checksAttempted, fail.checksPassed)
	}

	var stale sessionStats
	stale.noteCheck(checkPassed)
	stale.noteChange(true)
	if stale.checkState != checkStale {
		t.Errorf("pass then edit = %q, want %q", stale.checkState, checkStale)
	}
	// And it is stale until another check passes.
	stale.noteCheck(checkPassed)
	if stale.checkState != checkPassed {
		t.Errorf("pass after an edit = %q, want %q", stale.checkState, checkPassed)
	}

	// A status line reports lines added without saying WHEN, so it says work
	// changed and cannot age a pass.
	var unordered sessionStats
	unordered.noteCheck(checkPassed)
	unordered.noteChange(false)
	if unordered.checkState != checkPassed || !unordered.changeObserved {
		t.Errorf("unordered change = %q / observed %v, want %q and true",
			unordered.checkState, unordered.changeObserved, checkPassed)
	}
}

// An unreadable result is counted and does not overwrite a clear one. Reading
// it as a failure would make the quietest tools look like the worst; letting it
// erase a pass would lose the only clear evidence the session has.
func TestUnknownCheckYieldsToAClearResult(t *testing.T) {
	var s sessionStats
	s.noteCheck(checkPassed)
	s.noteCheck(checkUnknown)
	if s.checkState != checkPassed {
		t.Errorf("pass then unknown = %q, want %q", s.checkState, checkPassed)
	}
	if s.checksAttempted != 2 {
		t.Errorf("attempted = %d, want 2 — an unknown check still ran", s.checksAttempted)
	}
	var first sessionStats
	first.noteCheck(checkUnknown)
	if first.checkState != checkUnknown {
		t.Errorf("unknown alone = %q, want %q", first.checkState, checkUnknown)
	}
}
