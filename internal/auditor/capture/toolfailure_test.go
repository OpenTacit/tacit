// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"strings"
	"testing"
)

// The fixtures are real failures, taken off this machine's own transcripts
// rather than invented. A vocabulary tested against imagined errors classifies
// imagined errors.
var realFailures = []struct {
	kind string
	text string
}{
	{FailNotFound, "Exit code 2\nsed: can't read internal/registry/web/assets/app.css: No such file or directory"},
	{FailNotFound, "Exit code 2\nugrep: warning: internal/registry/web/assets/app.css: No such file or directory"},
	{FailNotFound, `<tool_use_error>Attachment "/tmp/scratch/members-light.png" does not exist.</tool_use_error>`},
	{FailNotFound, "Exit code 2\nls: cannot access '/definitely/not/a/real/path': No such file or directory"},
	{FailDenied, "git push is blocked in this repository. Commit and stop - the maintainer pushes."},
	{FailDenied, "<tool_use_error>Blocked: sleep 45 followed by: ls ~/archive-*.json</tool_use_error>"},
	{FailDenied, "The user doesn't want to proceed with this tool use. The tool use was rejected"},
	{FailDenied, "Permission for this action was denied by the Claude Code auto mode classifier. Reason: Blocked by classifier."},
	{FailParse, "Exit code 2\ninternal/auditor/capture/command_test.go:256:23: string literal not terminated"},
	{FailExit, "Exit code 1\nTraceback (most recent call last):\n  File \"<string>\", line 3\nModuleNotFoundError: No module named 'numpy'"},
	{FailExit, "Exit code 144"},
	{FailTimedOut, "Command timed out after 2m0.0s"},
	{FailOther, "Streamable HTTP error: Error POSTing to endpoint"},
}

func TestFailureVocabularyAgainstRealFailures(t *testing.T) {
	for _, tc := range realFailures {
		if got := ToolFailure(true, tc.text); got != tc.kind {
			t.Errorf("%q\n  classified as %q, want %q", oneLine(tc.text), got, tc.kind)
		}
	}
}

// The whole point of the vocabulary is that it is a WORD and never the
// message. A tool's output is the member's own work — file contents, a diff, a
// stack trace, somebody's customer list — and the kind of failure is the only
// thing about it that may be kept.
func TestFailureKeepsTheKindAndNoneOfTheMessage(t *testing.T) {
	secret := "Exit code 1: could not open /home/someone/.ssh/id_rsa for CUSTOMER Ltd: No such file or directory"
	got := ToolFailure(true, secret)
	if got != FailNotFound {
		t.Fatalf("kind = %q, want %q", got, FailNotFound)
	}
	for _, leaked := range []string{"someone", "id_rsa", "CUSTOMER", "/home", ".ssh"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("the kind carried %q out of the message: %q", leaked, got)
		}
	}
	// Every answer is one of the fixed words, whatever it was handed.
	for _, text := range []string{"", "\x00\xff", strings.Repeat("boom ", 5000)} {
		if k := ToolFailure(true, text); !known(k) {
			t.Fatalf("ToolFailure invented the kind %q", k)
		}
	}
}

// A call the harness did not call a failure is not a failure, however its
// output reads. Inferring from text alone would file a grep that printed "no
// such file or directory" as a failed grep, and the page would be confidently
// wrong about the one thing it exists to measure.
func TestSuccessIsNeverGuessedIntoAFailure(t *testing.T) {
	for _, text := range []string{
		"internal/x.go:3: No such file or directory", // a grep's own output
		"Exit code 0",
		"the tests pass",
	} {
		if got := ToolFailure(false, text); got != "" {
			t.Errorf("a successful call was classified as %q from its text alone: %q", got, oneLine(text))
		}
	}
}

// What the harness says, never what the text suggests.
func TestFailedPayloadReadsTheHarnessNotTheText(t *testing.T) {
	if !FailedPayload(map[string]any{"hook_event_name": EvPostToolFail}) {
		t.Error("the failure event is the harness saying so")
	}
	if !FailedPayload(map[string]any{"tool_response": map[string]any{"is_error": true}}) {
		t.Error("an is_error flag on the response is the harness saying so")
	}
	if FailedPayload(map[string]any{"hook_event_name": EvPostTool,
		"tool_response": "error: No such file or directory"}) {
		t.Error("a success payload whose TEXT mentions an error is not a failure")
	}
	if FailedPayload(nil) {
		t.Error("no payload is not a failure")
	}
}

func known(k string) bool {
	for _, v := range FailureKinds {
		if v == k {
			return true
		}
	}
	return false
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 70 {
		return s[:70] + "…"
	}
	return s
}
