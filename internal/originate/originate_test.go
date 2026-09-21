// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package originate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The common shape: a CLAUDE.md of ## sections, each one a rule somebody wrote
// down because they wanted it repeated.
func TestSectionsBecomeCandidates(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", `# How we work

## Deploys go through the pipeline
Never deploy by hand from a laptop. Push to main, watch the pipeline, and confirm
the new revision answers before you say it shipped.

## Migrations are two deploys
Add the column and ship it. Backfill and ship that. Only then read it. A migration
that ships with the code reading it cannot be rolled back.

## License
Proprietary. Do not redistribute this repository or any part of it to anyone.
`)
	got := Candidates(Find(root)[0])
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want the two rules: %+v", len(got), names(got))
	}
	if got[0].Name != "Deploys go through the pipeline" {
		t.Errorf("name = %q", got[0].Name)
	}
	if !strings.HasPrefix(got[0].Source, "CLAUDE.md § ") {
		t.Errorf("source = %q, want the file and section a reviewer can go and read", got[0].Source)
	}
	if !strings.Contains(got[0].Recipe, "Never deploy by hand") {
		t.Error("the recipe is not the section's own words")
	}
	// A licence notice is not a technique, and a reviewer should not have to
	// reject one.
	for _, c := range got {
		if strings.Contains(strings.ToLower(c.Name), "license") {
			t.Error("a boilerplate section was filed as a candidate")
		}
	}
}

// House style is as often a bare numbered list with no headings at all.
func TestABareListOfRulesBecomesOneCandidateEach(t *testing.T) {
	root := t.TempDir()
	write(t, root, "AGENTS.md", `1. Never use a long word where a short one will do, in comments or in prose.
2. Run the tests before you say a change works; a build passing is not a test passing.
3. When you change behaviour, add or update a test named for that specific behaviour.
`)
	got := Candidates(Find(root)[0])
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want one per rule: %+v", len(got), names(got))
	}
	if !strings.Contains(got[1].Name, "Run the tests") {
		t.Errorf("name = %q", got[1].Name)
	}
}

// Rules written above the first heading are still rules.
func TestThePreambleListIsNotLost(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", `# House style

1. Never use a metaphor or figure of speech you are used to seeing in print.
2. If it is possible to cut a word out, always cut it out of the sentence.
3. Never use the passive voice where you can use the active voice instead.

## Interface
The chrome is nearly monochrome and the colour is the data. Panels are flat
plates with one hairline border and no shadow anywhere on them.
`)
	got := Candidates(Find(root)[0])
	if len(got) != 4 {
		t.Fatalf("got %d candidates, want three rules and one section: %+v", len(got), names(got))
	}
}

// A skill or a command was written to be used whole. Splitting one at its own
// headings produces "Output" and "Format skeleton" as separate techniques.
func TestASkillIsOneMove(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/skills/deploy/SKILL.md", `---
name: Ship a service
description: Take a merged change through the release pipeline to production
---
## Steps
Run the plan first and read what it will change.

## Rollback
Rolling back is safe and takes about forty seconds to complete fully.
`)
	got := Candidates(Find(root)[0])
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want one: %+v", len(got), names(got))
	}
	if got[0].Name != "Ship a service" {
		t.Errorf("name = %q, want the frontmatter's", got[0].Name)
	}
	if !strings.Contains(got[0].Description, "release pipeline") {
		t.Errorf("description = %q, want the frontmatter's", got[0].Description)
	}
}

// CLAUDE.md and AGENTS.md are commonly copies of each other, and one is often a
// symlink. Two identical drafts is two reviews of one decision.
func TestDuplicateConventionFilesFileOnce(t *testing.T) {
	root := t.TempDir()
	body := `## Read the log before the source
When something deployed misbehaves, read the service log or the health endpoint
before you open any source file at all.
`
	write(t, root, "CLAUDE.md", body)
	write(t, root, "AGENTS.md", body)
	var all []Candidate
	for _, s := range Find(root) {
		all = append(all, Candidates(s)...)
	}
	if len(all) != 2 {
		t.Fatalf("expected both files to be read, got %d", len(all))
	}
	if got := Dedup(all); len(got) != 1 {
		t.Fatalf("after dedup: %d, want 1: %+v", len(got), names(got))
	}
}

// The task types have to come from the vocabulary capture.TaskType emits, or a
// draft scores badly against every real turn once it is promoted.
func TestTaskTypesComeFromTheCaptureVocabulary(t *testing.T) {
	emitted := map[string]bool{
		"editing": true, "delegation": true, "research": true, "verification": true,
		"exploration": true, "tool-use": true, "conversation": true,
	}
	for _, text := range []string{
		"Run the tests before you say it works",
		"Refactor the middleware and commit it",
		"Read the log before opening any source file",
		"anything at all with no signal in it whatsoever",
	} {
		for _, tt := range taskTypesFor(text) {
			if !emitted[tt] {
				t.Errorf("%q produced task type %q, which the capture layer never emits", text, tt)
			}
		}
	}
	if got := taskTypesFor("Run the tests before you say it works"); got[0] != "verification" {
		t.Errorf("task types = %v, want verification first", got)
	}
}

// A repository with nothing written down produces nothing, and says so by
// producing nothing rather than by inventing a candidate.
func TestARepoWithNoConventionsProducesNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "README.md", "# A project\n\nIt does a thing.\n")
	if got := Find(root); len(got) != 0 {
		t.Errorf("found %d source(s) in a repo with no conventions", len(got))
	}
}

func TestIsRepoRoot(t *testing.T) {
	root := t.TempDir()
	if IsRepoRoot(root) {
		t.Error("a plain directory was called a checkout")
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsRepoRoot(root) {
		t.Error("a checkout was not recognised")
	}
}

func TestCapKeepsTheQueueReviewable(t *testing.T) {
	cs := make([]Candidate, 40)
	if got := Cap(cs, MaxCandidates); len(got) != MaxCandidates {
		t.Errorf("cap = %d, want %d", len(got), MaxCandidates)
	}
	if got := Cap(cs[:3], MaxCandidates); len(got) != 3 {
		t.Errorf("a short list was trimmed: %d", len(got))
	}
}

func names(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}
