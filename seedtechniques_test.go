// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package tacit

import (
	"os"
	"path/filepath"
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
