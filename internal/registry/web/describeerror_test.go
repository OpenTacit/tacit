// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"
)

// Four different failures used to redirect to one message: "could not reach the
// model. Check TACIT_LLM_API_KEY". Two of them have nothing to do with the key,
// and an operator who had just set one correctly was sent to fix the one thing
// that was already right.
func TestADescribeFailureSaysWhichFailureItWas(t *testing.T) {
	cases := []struct{ code, want string }{
		{"nokey", "No model API key is set"},
		{"model", "The model request failed"},
		{"nodata", "could not read the map data"},
		{"error", "did not finish"},
	}
	for _, c := range cases {
		got := describeProblem(c.code)
		if !strings.Contains(got, c.want) {
			t.Errorf("describeProblem(%q) = %q, want it to mention %q", c.code, got, c.want)
		}
		if strings.Contains(got, "TACIT_LLM_API_KEY") {
			t.Errorf("describeProblem(%q) still points at the environment variable", c.code)
		}
	}
	if describeProblem("") != "" || describeProblem("nonsense") != "" {
		t.Error("an unknown code should say nothing rather than invent a failure")
	}
}
