// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package contracts

import "testing"

// "helped" carries the member's answer, and a no is not a yes.
//
// Five places fold the event log into a funnel and only the merge archive used
// to read Value; the other four counted every helped event as a help, so the
// dashboard's helped rate and the archive's disagreed about the same log. The
// rule lives here now so they cannot drift again.
func TestAHelpedEventThatSaysNoIsNotAHelp(t *testing.T) {
	for _, c := range []struct {
		name  string
		event FeedbackEvent
		want  bool
	}{
		{"an explicit yes", FeedbackEvent{Stage: "helped", Value: true}, true},
		{"an explicit no", FeedbackEvent{Stage: "helped", Value: false}, false},
		// Everything written before the field was populated looks like this, and
		// the stage itself was the answer then.
		{"no answer recorded", FeedbackEvent{Stage: "helped"}, true},
		{"a value that is not the answer", FeedbackEvent{Stage: "helped", Value: "why"}, true},
		{"another stage entirely", FeedbackEvent{Stage: "adopted", Value: true}, false},
		{"a dismissal with a reason", FeedbackEvent{Stage: "dismissed", Value: "not relevant"}, false},
	} {
		if got := c.event.CountsAsHelped(); got != c.want {
			t.Errorf("%s: CountsAsHelped() = %v, want %v", c.name, got, c.want)
		}
	}
}
