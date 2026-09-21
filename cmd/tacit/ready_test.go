// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// The report a passing member never sees still has to be readable when it
// fails, and warnings must survive without dragging the whole hop report onto
// a screen that is otherwise one line.
func TestWarnLinesKeepsOnlyTheWarnings(t *testing.T) {
	report := strings.Join([]string{
		"",
		"hook path (codex): wiring → relay → agent → registry",
		"   ok  hook agent up on http://127.0.0.1:8787",
		" warn  agent LLM: no-key — put a key in ~/.tacit-key.env",
		"   ok  agent ↔ registry healthy",
		" warn  release v9 is available",
	}, "\n")
	got := warnLines(report)
	if strings.Count(got, "\n") != 2 {
		t.Errorf("kept %d lines, want the two warnings:\n%s", strings.Count(got, "\n"), got)
	}
	for _, want := range []string{"agent LLM: no-key", "release v9"} {
		if !strings.Contains(got, want) {
			t.Errorf("dropped the warning %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"hook path", "hook agent up", "registry healthy"} {
		if strings.Contains(got, gone) {
			t.Errorf("carried %q through; only warnings belong here:\n%s", gone, got)
		}
	}
}

// An unknown tool is a usage error, not a broken machine: reporting it as a
// failed check would send the member looking for a fault they do not have.
func TestReadyRefusesAnUnknownHarness(t *testing.T) {
	if name, code := resolveReadyHarness("emacs-doctor"); code != exitUsage || name != "" {
		t.Errorf("resolveReadyHarness(unknown) = %q, %d; want \"\", %d", name, code, exitUsage)
	}
	// A known one is taken at its word, with no detection involved — the
	// member said which tool they work in.
	if name, code := resolveReadyHarness("codex"); code != 0 || name != "codex" {
		t.Errorf("resolveReadyHarness(\"codex\") = %q, %d", name, code)
	}
}
