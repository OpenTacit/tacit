// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package contracts

import "testing"

// The evidence line is the one claim the product makes that no model can fake,
// so it has to be true at the bottom of the sample range as well as the top.
// Each case here failed against the two renderers this replaced.
func TestEvidenceLine(t *testing.T) {
	cases := []struct {
		name     string
		outcomes map[string]any
		want     string
	}{{
		name: "measured",
		outcomes: map[string]any{"helped_rate": 0.94, "adoption_rate": 0.70,
			"sample_size": 120.0, "segment": "team:revops"},
		want: "measured by colleagues: helped 94% · adopted 70% · n=120 · team:revops",
	}, {
		// The live failure: a technique nobody has ever tried, whose decay-weighted
		// adopted count truncates to zero while its rate fields stay populated.
		// Both old renderers printed "measured by colleagues: helped 0%" — a
		// technique reading as proven useless when it is merely unmeasured.
		name:     "no sample is not a zero rate",
		outcomes: map[string]any{"helped_rate": 0.0, "adoption_rate": 0.5, "sample_size": 0.0},
		want:     "",
	}, {
		// A third from a sample of one is not a ratio anyone measured; it is a
		// decayed weight. Report the count and refuse the percentage.
		name:     "below the floor reports a count, never a rate",
		outcomes: map[string]any{"helped_rate": 0.333, "sample_size": 1.0},
		want:     "tried by 1 colleague — too few outcomes to rate yet",
	}, {
		name:     "below the floor, plural",
		outcomes: map[string]any{"helped_rate": 0.8, "sample_size": 2.0},
		want:     "tried by 2 colleagues — too few outcomes to rate yet",
	}, {
		name:     "at the floor the rate is quotable, with its n",
		outcomes: map[string]any{"helped_rate": 0.6, "sample_size": 5.0},
		want:     "measured by colleagues: helped 60% · n=5",
	}, {
		name:     "the overall segment is not a cohort label",
		outcomes: map[string]any{"helped_rate": 0.5, "sample_size": 10.0, "segment": "__overall__"},
		want:     "measured by colleagues: helped 50% · n=10",
	}, {
		name:     "no outcomes at all",
		outcomes: nil,
		want:     "",
	}, {
		name:     "a sample with no rate says nothing",
		outcomes: map[string]any{"sample_size": 40.0},
		want:     "",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EvidenceLine(tc.outcomes); got != tc.want {
				t.Errorf("EvidenceLine() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Every rate that reaches a member carries its denominator. Stated as a
// property rather than a case list so a future field cannot quietly drop it.
func TestEvidenceLineAlwaysShowsN(t *testing.T) {
	for _, n := range []float64{5, 6, 30, 120, 4000} {
		got := EvidenceLine(map[string]any{"helped_rate": 0.5, "sample_size": n})
		if !contains(got, "n=") {
			t.Errorf("n=%v rendered %q, which quotes a rate with no sample size", n, got)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
