// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// `tacit init` closes by telling the person what to do next, and the order is
// the behavior under test. It used to offer `tacit invite` in the facts block
// and never mention `tacit connect` at all — recruiting colleagues before the
// person running the command had a single useful moment, and omitting the one
// step that makes their own next session better.
func TestNextStepsPutPersonalValueBeforeRecruiting(t *testing.T) {
	out := captureStdout(t, func() { reportNextSteps(3, true) })

	connect := strings.Index(out, "connect")
	ask := strings.Index(out, "ask ")
	invite := strings.Index(out, "invite")
	for name, i := range map[string]int{"connect": connect, "ask": ask, "invite": invite} {
		if i < 0 {
			t.Fatalf("next steps never mention %q:\n%s", name, out)
		}
	}
	if !(connect < ask && ask < invite) {
		t.Errorf("next steps are out of order — want connect, then ask, then invite:\n%s", out)
	}
	if !strings.Contains(out, "when you want colleagues on it:") {
		t.Errorf("invite is not framed as a later choice:\n%s", out)
	}
}

// The drafts step is offered only when this repository actually produced some.
// Naming an empty review queue as a next step sends the reader to a page with
// nothing on it, which is the cold-start failure the product refuses elsewhere.
func TestNextStepsOmitReviewWhenNoDrafts(t *testing.T) {
	out := captureStdout(t, func() { reportNextSteps(0, true) })
	if strings.Contains(out, "Review holds") {
		t.Errorf("offered the review queue with no drafts read:\n%s", out)
	}
	if strings.Contains(out, "  2. ") && !strings.Contains(out, "ask ") {
		t.Errorf("step numbering skipped rather than closed up:\n%s", out)
	}
}

// One draft is a draft, not a drafts.
func TestNextStepsCountsDraftsInWords(t *testing.T) {
	out := captureStdout(t, func() { reportNextSteps(1, true) })
	if !strings.Contains(out, "1 draft ") {
		t.Errorf("want singular %q in:\n%s", "1 draft", out)
	}
	many := captureStdout(t, func() { reportNextSteps(4, true) })
	if !strings.Contains(many, "4 drafts") {
		t.Errorf("want plural %q in:\n%s", "4 drafts", many)
	}
}

// Without a printed sign-in link there is nothing "above" to follow, so the
// step has to name the destination instead of pointing at it.
func TestNextStepsDoNotPointAtALinkThatWasNotPrinted(t *testing.T) {
	out := captureStdout(t, func() { reportNextSteps(2, false) })
	if strings.Contains(out, "link above") {
		t.Errorf("pointed at a sign-in link that was never printed:\n%s", out)
	}
	if !strings.Contains(out, "open the dashboard") {
		t.Errorf("no destination named for the review step:\n%s", out)
	}
}
