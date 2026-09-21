// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package slug

import "testing"

// The union of the inputs the four separate slugifiers were built around, with
// the output all of them agreed on. This is the evidence the merge was safe:
// each row was checked against the implementation it came from.
func TestMakeMatchesEveryImplementationItReplaced(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// From the registry's own tests.
		{"  Hello,  World! ", "hello-world"},
		// From the demo loader's tests.
		{"Query the warehouse with mq!", "query-the-warehouse-with-mq"},
		{"  Ledger / Billing  ", "ledger-billing"},
		// The shapes all four had to handle.
		{"Already-Kebab", "already-kebab"},
		{"UPPER CASE", "upper-case"},
		{"snake_case_name", "snake-case-name"},
		{"dots.and.dots", "dots-and-dots"},
		{"multiple   spaces", "multiple-spaces"},
		{"--leading and trailing--", "leading-and-trailing"},
		{"digits 123 kept", "digits-123-kept"},
		{"symbols !@#$ collapse", "symbols-collapse"},
		{"", ""},
		{"   ", ""},
		{"!!!", ""},
		{"café crème", "caf-cr-me"},
	} {
		if got := Make(tc.in); got != tc.want {
			t.Errorf("Make(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Only the demo loader capped, and it re-trimmed so a cut never leaves a
// trailing hyphen.
func TestMakeMaxCapsAndNeverEndsOnASeparator(t *testing.T) {
	long := "aaaaaaaaaa bbbbbbbbbb cccccccccc dddddddddd eeeeeeeeee ffffffffff gggggggggg hhhhhhhhhh"
	got := MakeMax(long, 80)
	if len(got) > 80 {
		t.Errorf("MakeMax produced %d characters, want at most 80", len(got))
	}
	if got[len(got)-1] == '-' {
		t.Errorf("MakeMax = %q, want no trailing hyphen", got)
	}
	// A cut landing exactly on a hyphen is the case the re-trim exists for.
	if got, want := MakeMax("ab cd", 3), "ab"; got != want {
		t.Errorf("MakeMax(%q, 3) = %q, want %q", "ab cd", got, want)
	}
	// A max of zero or less means no cap.
	if got := MakeMax(long, 0); got != Make(long) {
		t.Errorf("MakeMax with no cap = %q, want the uncapped slug", got)
	}
}

// A slug is durable: technique ids and federation subscription ids come from
// one, and a subscription's id is re-derived whenever it is saved. Make must
// not cap, or a long feed URL would be renamed out from under an existing
// subscription.
func TestMakeDoesNotCap(t *testing.T) {
	long := ""
	for range 20 {
		long += "segment-"
	}
	if got := Make(long); len(got) <= 80 {
		t.Errorf("Make capped its output at %d characters; it must not cap", len(got))
	}
}
