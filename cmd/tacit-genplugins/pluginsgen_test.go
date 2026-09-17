// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFilesMatchSource asserts every committed plugins/** skill and
// command file is exactly what the generator produces from plugins/_src. A
// hand-edit that bypasses _src (or a stale _src that was never regenerated)
// fails here — this is the CI guard that keeps the ~95 harness files in sync
// with their single canonical source.
func TestGeneratedFilesMatchSource(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	files, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("generator produced no files")
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s: not committed (run `make plugins`): %v", rel, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%s: out of sync with plugins/_src — run `make plugins` (do not hand-edit generated files)", rel)
		}
	}
}

// TestFeedbackSkillRemoved guards plan item P0.3: the standalone feedback skill
// must not reappear in any harness.
func TestFeedbackSkillRemoved(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	for _, p := range []string{
		"plugins/amp/skills/tacit-feedback/SKILL.md",
		"plugins/codex/skills/tacit-feedback/SKILL.md",
		"plugins/copilot/skills/tacit-feedback/SKILL.md",
		"plugins/pi/skills/tacit-feedback/SKILL.md",
		"plugins/cursor/commands/tacit-feedback.md",
		"plugins/gemini/commands/tacit/feedback.toml",
		"plugins/opencode/command/tacit-feedback.md",
		"plugins/claude-code/skills/feedback/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Errorf("feedback skill still present: %s (P0.3 deletes it)", p)
		}
	}
}
