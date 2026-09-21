// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"os"
	"path/filepath"
	"testing"
)

// The token fields on the session record were built for "a source that reports
// them", and no hook harness does — the Stop payload carries no usage. The
// transcript has carried it all along, in the shape this fixture copies from a
// real Claude Code session.
func TestTranscriptUsageReadsTheNewestAssistantTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":100,"cache_creation_input_tokens":5}}}
{"type":"user","message":{"role":"user","content":"again"}}
{"type":"assistant","message":{"usage":{"input_tokens":2,"output_tokens":30,"cache_read_input_tokens":384659,"cache_creation_input_tokens":494}}}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	u, ok := TranscriptUsage(path)
	if !ok {
		t.Fatal("no usage found in a transcript that carries it")
	}
	if u.Out != 30 {
		t.Errorf("out = %d, want 30 — the NEWEST turn, not the first", u.Out)
	}
	// Everything the model was handed, not the part that missed the cache. The
	// bare input_tokens here is 2; reporting that as "tokens in" said input was
	// negligible on a session where it was nearly all of it.
	if want := 2 + 494 + 384659; u.In() != want {
		t.Errorf("in = %d, want %d (input + cache creation + cache read)", u.In(), want)
	}
}

// The three classes of input are priced apart — a cache read costs a fraction of
// fresh input, a cache write a premium over it — so they have to survive the
// read as three numbers. They were being added together at the door, which made
// the biggest lever a member has on a bill unreadable from anywhere downstream.
func TestTranscriptUsageKeepsTheThreeClassesApart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	line := `{"type":"assistant","message":{"usage":{"input_tokens":2,"output_tokens":30,` +
		`"cache_read_input_tokens":384659,"cache_creation_input_tokens":494}}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	u, ok := TranscriptUsage(path)
	if !ok {
		t.Fatal("no usage found")
	}
	if u.Fresh != 2 || u.CacheWrite != 494 || u.CacheRead != 384659 {
		t.Errorf("fresh/write/read = %d/%d/%d, want 2/494/384659", u.Fresh, u.CacheWrite, u.CacheRead)
	}
	// And the three still add up to what the model was handed, because every
	// figure already on the page is built on that sum.
	if u.In() != u.Fresh+u.CacheWrite+u.CacheRead {
		t.Error("In() is no longer the sum of its parts")
	}
}

// Capture must never fail a turn. A transcript that is missing, truncated, or
// not JSON at all reports nothing and says so, rather than erroring.
func TestTranscriptUsageIsSilentOnRubbish(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"not json", "this is not a transcript\n"},
		{"no usage", `{"type":"assistant","message":{"content":"hi"}}` + "\n"},
		{"user turns only", `{"type":"user","message":{"usage":{"input_tokens":9}}}` + "\n"},
		{"empty", ""},
	} {
		path := filepath.Join(dir, tc.name+".jsonl")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := TranscriptUsage(path); ok {
			t.Errorf("%s: reported usage it does not have", tc.name)
		}
	}
	if _, ok := TranscriptUsage(filepath.Join(dir, "no-such-file.jsonl")); ok {
		t.Error("a missing transcript reported usage")
	}
}
