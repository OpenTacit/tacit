// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package techniques

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

// repoTechniques points at the real curated techniques shipped in this repo.
func repoTechniques(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../../techniques")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParsesAllRepoTechniques(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join(repoTechniques(t), "*.md"))
	if len(paths) < 5 {
		t.Fatalf("expected the 5 seed techniques, found %d", len(paths))
	}
	for _, p := range paths {
		technique, err := ParseFile(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if technique.ID == "" || technique.Name == "" || technique.Recipe == "" {
			t.Fatalf("%s: required fields empty: %+v", p, technique)
		}
	}
}

// The YAML subset the parser covers, every shape in one file. The fixture is
// local on purpose: this used to read a shipped technique, so editing that
// technique's content broke a test about parsing. What ships is still parsed —
// by TestParsesAllRepoTechniques, which is the test that should care.
func TestParsesEveryFrontmatterShape(t *testing.T) {
	technique, err := ParseFile(filepath.Join("testdata", "every-shape.md"))
	if err != nil {
		t.Fatal(err)
	}
	if technique.Scope != "org" {
		t.Fatalf("scope = %q", technique.Scope)
	}
	if len(technique.Tags) == 0 || technique.Tags[0] != "org-tool" {
		t.Fatalf("inline list tags wrong: %v", technique.Tags)
	}
	if len(technique.Triggers) != 2 {
		t.Fatalf("triggers = %v", technique.Triggers)
	}
	if trig, ok := technique.Triggers[0].(map[string]any); !ok || trig["heuristic"] == "" {
		t.Fatalf("trigger map item wrong: %v", technique.Triggers[0])
	}
	if len(technique.SupportMatrix) != 2 {
		t.Fatalf("support matrix = %v", technique.SupportMatrix)
	}
	if technique.SupportMatrix[0]["harness"] != "claude-code" ||
		technique.SupportMatrix[0]["supported"] != true {
		t.Fatalf("inline map wrong: %v", technique.SupportMatrix[0])
	}
	if !strings.Contains(technique.AppliesWhen, "every shape at once") {
		t.Fatalf("folded scalar wrong: %q", technique.AppliesWhen)
	}
	if !strings.Contains(technique.Recipe, "<with-a-placeholder>") {
		t.Fatalf("literal block wrong: %q", technique.Recipe)
	}
	if technique.Shipped != "2026-03" {
		t.Fatalf("shipped = %q", technique.Shipped)
	}
	if !strings.Contains(technique.BeforeAfter, "Before:") {
		t.Fatalf("markdown body not folded into before_after: %q", technique.BeforeAfter)
	}
}

func TestMissingFrontmatterRejected(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	_ = os.WriteFile(bad, []byte("just a paragraph, no frontmatter"), 0o644)
	if _, err := ParseFile(bad); err == nil {
		t.Fatal("missing frontmatter accepted")
	}
	incomplete := filepath.Join(dir, "inc.md")
	_ = os.WriteFile(incomplete, []byte("---\nid: x\nname: y\n---\nbody"), 0o644)
	if _, err := ParseFile(incomplete); err == nil {
		t.Fatal("missing required fields accepted")
	}
}

func TestSyncDirLoadsEverything(t *testing.T) {
	var got []models.Technique
	n, err := SyncDir(repoTechniques(t), func(c models.Technique) error {
		got = append(got, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != len(got) || n < 5 {
		t.Fatalf("synced %d techniques", n)
	}
}
