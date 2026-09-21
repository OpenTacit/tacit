// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package pkg

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A container registry refuses a repository name with a capital letter in it,
// and GITHUB_REPOSITORY carries the organization's own casing — OpenTacit/tacit.
// Interpolating it straight into an image name builds a tag buildx rejects:
//
//	invalid tag "ghcr.io/OpenTacit/tacit:v0.0.1-rc1": repository name must be lowercase
//
// That is how the first rehearsal tag went: four binaries built, the release
// published and attested, and the image job dead at its first command. The
// failure costs a whole tag, because the workflow runs from the tag's own commit
// — fixing it afterwards means cutting another one.
//
// So the rule is that an image name is never the raw variable. Lowercase it once
// into a shell variable, or into a step output for the attestation to name.
func TestImageNamesAreLowercased(t *testing.T) {
	dir := "../.github/workflows"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no workflows to check: %v", err)
	}
	// The two spellings a workflow can reach the name by: the shell's environment
	// variable, and the expression the runner substitutes before the shell sees it.
	raw := regexp.MustCompile(`ghcr\.io/(\$\{GITHUB_REPOSITORY\}|\$\{\{\s*github\.repository\s*\}\})`)
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for i, line := range strings.Split(string(body), "\n") {
			// A comment showing somebody the verify command is prose, not a tag.
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if raw.MatchString(line) {
				t.Errorf("%s:%d names an image as ghcr.io/<repository> — lowercase it first, "+
					"or the registry rejects the tag:\n    %s", e.Name(), i+1, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no workflow files were read — this guard has lost its subject")
	}
}
