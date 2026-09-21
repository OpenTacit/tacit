// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package conventions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/pkg/contracts"
)

func techniques() []contracts.Technique {
	return []contracts.Technique{
		{ID: "fuser", Name: "Free a port by port", Description: "Kill by port, not by name.",
			AppliesWhen: "A port is still held after a service stopped.",
			Recipe:      "Use `fuser -k PORT/tcp`.", Status: "stable"},
	}
}

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Everything outside the markers is the member's own writing. A tool that loses
// a paragraph of it gets uninstalled the first time, and rightly.
func TestSyncPreservesEverythingOutsideTheBlock(t *testing.T) {
	own := "# My project\n\nAlways run the linter before committing.\n"
	dir := repo(t, map[string]string{"CLAUDE.md": own})
	block := Render(techniques(), "https://example.test")

	if _, err := Sync(dir, block, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	body := string(raw)
	if !strings.Contains(body, "Always run the linter before committing.") {
		t.Fatalf("the member's own writing was lost:\n%s", body)
	}
	if !strings.Contains(body, "fuser -k PORT/tcp") {
		t.Fatalf("the block was not written:\n%s", body)
	}

	// A second technique replaces the block and still leaves the prose alone.
	next := Render(append(techniques(), contracts.Technique{
		ID: "two", Name: "Read the log first", Recipe: "Read the service log.", Status: "stable"}),
		"https://example.test")
	if _, err := Sync(dir, next, false); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	body = string(raw)
	if !strings.Contains(body, "Always run the linter before committing.") {
		t.Fatalf("the second pass lost the member's writing:\n%s", body)
	}
	if strings.Count(body, BeginMarker) != 1 {
		t.Fatalf("the block was appended rather than replaced:\n%s", body)
	}
	if !strings.Contains(body, "Read the service log.") {
		t.Fatalf("the block was not updated:\n%s", body)
	}
}

// A repository with no AGENTS.md has not asked for one. Creating files in
// somebody's project is a decision, not a sync.
func TestSyncOnlyTouchesFilesThatExist(t *testing.T) {
	dir := repo(t, map[string]string{"CLAUDE.md": "# p\n"})
	block := Render(techniques(), "")
	written, err := Sync(dir, block, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0] != "CLAUDE.md" {
		t.Fatalf("wrote %v, want only CLAUDE.md", written)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
		t.Fatal("AGENTS.md was created without being asked for")
	}
	// Asked for, it appears.
	if _, err := Sync(dir, block, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("--create did not write AGENTS.md: %v", err)
	}
}

// A second sync of the same block writes nothing, so a member who runs this in
// a loop has no diff to read and no mtimes moved.
func TestSyncIsIdempotent(t *testing.T) {
	dir := repo(t, map[string]string{"CLAUDE.md": "# p\n"})
	block := Render(techniques(), "")
	if _, err := Sync(dir, block, false); err != nil {
		t.Fatal(err)
	}
	written, err := Sync(dir, block, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("a no-op sync rewrote %v", written)
	}
}

// A file edited into a half-marker state cannot be rewritten safely, so the
// block is appended and the report says the file is damaged rather than the
// tool guessing where the old one ended.
func TestDamagedBlockIsReportedNotGuessedAt(t *testing.T) {
	dir := repo(t, map[string]string{"CLAUDE.md": "# p\n\n" + BeginMarker + "\nhalf a block\n"})
	block := Render(techniques(), "")
	st := Inspect(dir, block)
	if !st.Files[0].Damaged || st.Files[0].Managed {
		t.Fatalf("a half-marker file was not reported as damaged: %+v", st.Files[0])
	}
	if _, err := Sync(dir, block, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(raw), "half a block") {
		t.Fatalf("the damaged content was destroyed:\n%s", raw)
	}
}

// The drift report names only the split cases. A file nobody has is not a gap —
// that is advice about a harness the member does not run.
func TestDriftReportsOnlyTheSplitCases(t *testing.T) {
	block := Render(techniques(), "")
	a := Inspect(repo(t, map[string]string{"CLAUDE.md": "# a\n", "AGENTS.md": "# a\n"}), block)
	a.Name = "alpha"
	b := Inspect(repo(t, map[string]string{"CLAUDE.md": "# b\n"}), block)
	b.Name = "beta"

	d := Drift([]RepoState{a, b})
	if d.Repos != 2 {
		t.Fatalf("repos = %d", d.Repos)
	}
	paths := map[string]Gap{}
	for _, g := range d.Gaps {
		paths[g.Path] = g
	}
	if _, found := paths["CLAUDE.md"]; found {
		t.Fatal("a file both projects have was reported as a gap")
	}
	if _, found := paths["GEMINI.md"]; found {
		t.Fatal("a file neither project has was reported as a gap")
	}
	gap, found := paths["AGENTS.md"]
	if !found {
		t.Fatalf("the real gap was not reported: %+v", d.Gaps)
	}
	if len(gap.Present) != 1 || gap.Present[0] != "alpha" || len(gap.Missing) != 1 || gap.Missing[0] != "beta" {
		t.Fatalf("gap sides wrong: %+v", gap)
	}
	// Neither project carries a block yet.
	if len(d.Unmanaged) != 2 {
		t.Fatalf("unmanaged = %v, want both", d.Unmanaged)
	}
}

// Recipes go into the file, not a link. The only reader is a model that cannot
// follow one.
func TestRenderWritesTheRecipeNotALink(t *testing.T) {
	block := Render(techniques(), "https://example.test")
	if !strings.Contains(block, "fuser -k PORT/tcp") {
		t.Fatalf("the recipe is missing:\n%s", block)
	}
	if !strings.Contains(block, "When: A port is still held after a service stopped.") {
		t.Fatalf("the fit condition is missing:\n%s", block)
	}
	if !strings.HasPrefix(block, BeginMarker) || !strings.HasSuffix(block, EndMarker) {
		t.Fatalf("the block is not marker-delimited:\n%s", block)
	}
}
