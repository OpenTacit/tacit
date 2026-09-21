// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package product

import "testing"

// The rename must be able to tell the product from the things named after it.
// Every case below appears in the user guide, and getting one wrong publishes
// documentation for a system nobody is running: a header no client sends, a
// variable no operator can set, a command that is not on the path.
func TestRenameChangesTheProductAndNothingNamedAfterIt(t *testing.T) {
	t.Setenv(EnvKey, "Sagesse")
	for _, tc := range []struct{ in, want string }{
		{"OpenTacit", "Sagesse"},
		{"Set up OpenTacit today.", "Set up Sagesse today."},
		{"OpenTacit's playbook", "Sagesse's playbook"},
		{"(OpenTacit)", "(Sagesse)"},
		{"OpenTacit, OpenTacit and OpenTacit", "Sagesse, Sagesse and Sagesse"},

		// Identifiers, all of which contain the name and none of which are it.
		{"the `X-Tacit-Key` header", "the `X-Tacit-Key` header"},
		{"TACIT_API_KEY", "TACIT_API_KEY"},
		{"run tacit connect", "run tacit connect"},
		{"at tacit.zone", "at tacit.zone"},
		{"github.com/opentacit/tacit", "github.com/opentacit/tacit"},
		{"OpenTacit-adjacent", "OpenTacit-adjacent"},
		{"OpenTacit_registry", "OpenTacit_registry"},

		// The bare old name is now just a word. It was the authored name until
		// 2026-09-16, so anything still carrying it is prose nobody updated —
		// and renaming it would be renaming something this product is not called.
		{"Tacit", "Tacit"},

		// The word inside a sentence that also carries an identifier.
		{"OpenTacit sends X-Tacit-Key on every OpenTacit call.", "Sagesse sends X-Tacit-Key on every Sagesse call."},
	} {
		if got := Rename(tc.in); got != tc.want {
			t.Errorf("Rename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A deployment that kept the name gets its prose back byte for byte — the common
// case, and the one where a rename has nothing to do.
func TestRenameIsANoOpWhenTheNameIsTheAuthoredOne(t *testing.T) {
	t.Setenv(EnvKey, Authored)
	const prose = "OpenTacit records what helped. OpenTacit's playbook grows from it."
	if got := Rename(prose); got != prose {
		t.Errorf("Rename rewrote prose it had no reason to touch: %q", got)
	}
}
