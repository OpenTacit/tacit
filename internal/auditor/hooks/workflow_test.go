// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import "testing"

func TestWorkflowGateReady(t *testing.T) {
	cases := []struct {
		name     string
		phases   []string
		verified bool
		emitted  bool
		want     bool
	}{
		{"multi-phase verified", []string{"research", "editing", "verification"}, true, false, true},
		{"not verified", []string{"research", "editing", "verification"}, false, false, false},
		{"already emitted", []string{"research", "editing", "verification"}, true, true, false},
		{"too few distinct phases", []string{"editing", "editing", "verification"}, true, false, false},
		{"repeats collapse to distinct", []string{"research", "research", "editing", "editing", "verification"}, true, false, true},
		{"empty phases ignored", []string{"", "editing", "", "verification"}, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflowGateReady(tc.phases, tc.verified, tc.emitted); got != tc.want {
				t.Fatalf("workflowGateReady = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkflowTraceText(t *testing.T) {
	got := workflowTraceText([]string{"research", "editing", "verification"}, "built a feature")
	want := "Phase sequence: research → editing → verification\nContext: built a feature"
	if got != want {
		t.Fatalf("trace text = %q, want %q", got, want)
	}
}
