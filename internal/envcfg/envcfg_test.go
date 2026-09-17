// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package envcfg

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestParseFileReadsWhatAnOperatorWrites(t *testing.T) {
	path := writeFile(t, `
# a comment
KEY=value
  SPACED  =  spaced value
QUOTED="quoted"
SINGLE='single'
EMPTY=
WITH_EQUALS=a=b

not a pair
`)
	got := ParseFile(path)
	for key, want := range map[string]string{
		"KEY": "value", "SPACED": "spaced value", "QUOTED": "quoted",
		"SINGLE": "single", "EMPTY": "", "WITH_EQUALS": "a=b",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
	if _, ok := got["# a comment"]; ok {
		t.Error("a comment became a key")
	}
	if len(got) != 6 {
		t.Errorf("parsed %d keys, want 6: %v", len(got), got)
	}
}

func TestParseFileTreatsAMissingFileAsEmpty(t *testing.T) {
	if got := ParseFile(filepath.Join(t.TempDir(), "absent.env")); len(got) != 0 {
		t.Errorf("missing file = %v, want empty", got)
	}
	if got := ParseFile(""); len(got) != 0 {
		t.Errorf("empty path = %v, want empty", got)
	}
}

// The trap this package exists to stop. Exporting a variable as the empty
// string must not erase what the file supplies: a member whose registry.env
// names an OIDC client, running under a unit that exports the same variable
// empty, has to keep their client. "Set" means non-empty.
func TestAnEmptyEnvironmentVariableDoesNotClearTheFileValue(t *testing.T) {
	t.Setenv("TACIT_TEST_OIDC_CLIENT", "")
	l := Lookup{File: map[string]string{"TACIT_TEST_OIDC_CLIENT": "from-the-file"}}
	if got := l.Str("fallback", "TACIT_TEST_OIDC_CLIENT"); got != "from-the-file" {
		t.Fatalf("Str = %q, want the file value — an empty variable cleared it", got)
	}
}

func TestEnvironmentBeatsFileBeatsDefault(t *testing.T) {
	file := map[string]string{"A": "file-a", "B": "file-b"}
	t.Setenv("A", "env-a")
	l := Lookup{File: file}
	if got := l.Str("def", "A"); got != "env-a" {
		t.Errorf("Str(A) = %q, want the environment to win", got)
	}
	if got := l.Str("def", "B"); got != "file-b" {
		t.Errorf("Str(B) = %q, want the file", got)
	}
	if got := l.Str("def", "C"); got != "def" {
		t.Errorf("Str(C) = %q, want the default", got)
	}
}

// Keys are tried in order, so an old name listed after a new one keeps working.
func TestKeysAreTriedInOrder(t *testing.T) {
	t.Setenv("OLD_NAME", "old")
	l := Lookup{}
	if got := l.Str("def", "NEW_NAME", "OLD_NAME"); got != "old" {
		t.Errorf("Str = %q, want the old name to still resolve", got)
	}
	t.Setenv("NEW_NAME", "new")
	if got := l.Str("def", "NEW_NAME", "OLD_NAME"); got != "new" {
		t.Errorf("Str = %q, want the first key to win", got)
	}
}

// A value that will not parse is treated as absent, so a typo falls back to the
// default rather than to zero.
func TestAnUnparsableNumberFallsBackToTheDefault(t *testing.T) {
	t.Setenv("N", "not-a-number")
	t.Setenv("F", "not-a-float")
	l := Lookup{}
	if got := l.Int(7, "N"); got != 7 {
		t.Errorf("Int = %d, want the default 7", got)
	}
	if got := l.Float(1.5, "F"); got != 1.5 {
		t.Errorf("Float = %v, want the default 1.5", got)
	}
	t.Setenv("N", "42")
	if got := l.Int(7, "N"); got != 42 {
		t.Errorf("Int = %d, want 42", got)
	}
}

func TestBoolReadsTheSpellingsOperatorsType(t *testing.T) {
	l := Lookup{}
	for _, tc := range []struct {
		val  string
		want bool
	}{
		{"1", true}, {"true", true}, {"TRUE", true}, {"yes", true}, {"on", true},
		{"0", false}, {"false", false}, {"No", false}, {"off", false},
	} {
		t.Setenv("B", tc.val)
		if got := l.Bool(!tc.want, "B"); got != tc.want {
			t.Errorf("Bool(%q) = %v, want %v", tc.val, got, tc.want)
		}
	}
	// Anything else leaves the default alone rather than reading as false.
	t.Setenv("B", "ture")
	if !l.Bool(true, "B") {
		t.Error("a misspelt value overrode a true default")
	}
	t.Setenv("B", "")
	if !l.Bool(true, "B") {
		t.Error("an unset value overrode a true default")
	}
}

// Only the ingress trims, because it is configured by a unit file where a
// trailing space is a typo rather than a value.
func TestTrimAppliesOnlyWhenAskedFor(t *testing.T) {
	t.Setenv("S", "  ")
	if got := (Lookup{Trim: true}).Str("def", "S"); got != "def" {
		t.Errorf("trimmed lookup = %q, want the whitespace to read as unset", got)
	}
	if got := (Lookup{}).Str("def", "S"); got != "  " {
		t.Errorf("untrimmed lookup = %q, want the value exactly as exported", got)
	}
}
