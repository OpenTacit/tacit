// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package tacit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The starter set is the whole playbook on day one, and on day one it decides
// whether the product has anything to say.
//
// It shipped five techniques, all about talking to a chat model, tagged with
// task types — concept-explanation, learning, data-extraction, reporting — from
// a vocabulary the hook agent does not use. The agent labels a turn from the
// tools it called (capture.TaskType): editing, verification, exploration,
// delegation, research, tool-use, conversation. Those labels go into the query
// embedding and the technique embedding both, so a starter set outside the
// vocabulary is a starter set that scores badly against every real coding turn.
//
// This does not police what the techniques say. It checks the one thing a
// stranger's first week depends on: that the shipped playbook speaks the
// language the capture layer speaks.
func TestStarterTechniquesUseTheTaskTypesTheAgentEmits(t *testing.T) {
	// capture.TaskType's closed vocabulary. Duplicated deliberately rather than
	// imported: internal/ is not importable from the repo root package, and a
	// second copy that has to be edited alongside the first is the point —
	// changing the vocabulary should make somebody look at the starter set.
	emitted := map[string]bool{
		"editing": true, "delegation": true, "research": true, "verification": true,
		"exploration": true, "tool-use": true, "conversation": true,
	}

	files, err := filepath.Glob(filepath.Join("techniques", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no starter techniques found: %v", err)
	}
	covered := map[string]int{}
	matching := 0
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		types := taskTypesOf(string(raw))
		if len(types) == 0 {
			t.Errorf("%s declares no task_types — it can only ever match on prose", filepath.Base(path))
			continue
		}
		hit := false
		for _, tt := range types {
			if emitted[tt] {
				covered[tt]++
				hit = true
			}
		}
		if hit {
			matching++
		}
	}

	// Every label the agent can emit needs at least one technique behind it, or
	// there is a whole shape of turn the playbook is silent on.
	for tt := range emitted {
		if tt == "tool-use" || tt == "conversation" {
			continue // the two fallbacks; a turn with no shape needs no move
		}
		if covered[tt] == 0 {
			t.Errorf("no starter technique is tagged %q — turns of that shape retrieve nothing relevant", tt)
		}
	}
	// And the set as a whole has to be mostly about the work, not mostly about
	// chatting. Half is a floor, not a target.
	if matching*2 < len(files) {
		t.Errorf("only %d of %d starter techniques speak the capture vocabulary", matching, len(files))
	}
}

// taskTypesOf pulls the task_types list out of a technique's frontmatter. The
// registry has a real loader; this is a test reading one line, and a dependency
// on internal/ from here is not available.
func taskTypesOf(doc string) []string {
	for _, line := range strings.Split(doc, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "task_types:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		rest = strings.TrimPrefix(rest, "[")
		rest = strings.TrimSuffix(rest, "]")
		var out []string
		for _, part := range strings.Split(rest, ",") {
			if v := strings.TrimSpace(part); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return nil
}

// A registry can be renamed: the product name is one setting, and every surface
// takes it from there (internal/product). A starter technique that writes the
// name into its own text puts it back on the page — under a name the operator
// chose to replace. The techniques have nothing to gain from saying it either;
// they talk about the playbook, the registry, and a suggestion, all of which
// survive a rename.
//
// The name is spelt out here rather than imported so that changing it is not
// enough to make this pass quietly, which is the same reason the task-type
// vocabulary above is duplicated.
func TestStarterTechniquesDoNotWriteTheProductName(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("techniques", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no starter techniques found: %v", err)
	}
	// The lowercase command name is not the product name: `tacit pause` and
	// `@tacit` keep working whatever the registry is called.
	banned := regexp.MustCompile(`OpenTacit|\bTacit\b`)
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if hit := banned.FindString(string(raw)); hit != "" {
			t.Errorf("%s writes the product name %q — say the playbook, the registry, or a suggestion instead",
				filepath.Base(path), hit)
		}
	}
}

// A starter technique's recipe is the one place the shipped playbook tells a
// stranger what to type, and it is prose: nothing compiles it, so a renamed
// command goes on being suggested long after it stops working. The command
// names are the half of that a test can hold; what a command DOES still needs
// a reader.
func TestStarterTechniquesNameCommandsThatExist(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("techniques", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no starter techniques found: %v", err)
	}
	main, err := os.ReadFile(filepath.Join("cmd", "tacit", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	// `tacit <verb>` at the start of a recipe line or inside backticks. Neither
	// shape matches the "@tacit do we have…" of an advisor question, which is a
	// prompt rather than a command.
	verbs := regexp.MustCompile("(?m)(?:^\\s*|`)tacit ([a-z][a-z-]*)")
	slash := regexp.MustCompile(`/tacit:([a-z]+)`)
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)
		for _, m := range verbs.FindAllStringSubmatch(string(raw), -1) {
			if !strings.Contains(string(main), `case "`+m[1]+`"`) {
				t.Errorf("%s says `tacit %s`, which is not a subcommand", name, m[1])
			}
		}
		for _, m := range slash.FindAllStringSubmatch(string(raw), -1) {
			command := filepath.Join("plugins", "claude-code", "commands", m[1]+".md")
			skill := filepath.Join("plugins", "claude-code", "skills", m[1], "SKILL.md")
			if !exists(command) && !exists(skill) {
				t.Errorf("%s says /tacit:%s, which is neither a command nor a skill", name, m[1])
			}
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A technique whose recipe is a tacit command depends on a tool the
// organization runs, which is what org scope means (schemas/technique.md).
// Scope is not cosmetic: federation exports the general ones
// (internal/registry/federation/public.go), so a general technique about this
// registry would travel to organizations as advice about a tool the reader may
// not have.
func TestStarterTechniquesAboutTheToolAreOrgScoped(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("techniques", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no starter techniques found: %v", err)
	}
	names := regexp.MustCompile("(?m)(?:^\\s*|`)tacit [a-z]|/tacit:[a-z]|@tacit")
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !names.MatchString(string(raw)) {
			continue
		}
		if !regexp.MustCompile(`(?m)^scope: org$`).MatchString(string(raw)) {
			t.Errorf("%s tells the member to run a tacit command but is not scope: org",
				filepath.Base(path))
		}
	}
}

// Nothing in the bundled set is allowed to be invented, and two halves of that
// rule are mechanical. A rate or a sample size in a technique's own text reads
// as evidence, and the bundled set has no outcomes — every registry starts at
// "awaiting measured outcomes". A `verified:` date is a claim that somebody ran
// a check on a named harness on a named month, and no such check is recorded
// anywhere for a technique that ships with the binary.
//
// The half that stays a reader's job: an invented tool in a recipe, or a
// before/after narrating a session nobody had.
func TestStarterTechniquesInventNoEvidence(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("techniques", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no starter techniques found: %v", err)
	}
	for _, claim := range []struct {
		what    string
		pattern *regexp.Regexp
	}{
		{"an outcome rate", regexp.MustCompile(`[0-9]+\s*%`)},
		{"a sample size", regexp.MustCompile(`\bn\s*=\s*[0-9]`)},
		{"a verification date", regexp.MustCompile(`verified:`)},
	} {
		for _, path := range files {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if hit := claim.pattern.FindString(string(raw)); hit != "" {
				t.Errorf("%s states %s (%q) that nothing measured",
					filepath.Base(path), claim.what, hit)
			}
		}
	}
}
