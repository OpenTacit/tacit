// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/conventions"
	"github.com/opentacit/tacit/pkg/client"
	"github.com/opentacit/tacit/pkg/contracts"
)

// cmdConventions writes the playbook back into the files a member's agents
// actually read, and reports where their projects have drifted apart.
//
// `tacit init --from-repo` reads conventions IN. This is the other direction,
// and it exists for the member the team case never has: one person, one working
// style, and a dozen repositories whose convention files were each set up on a
// different afternoon (docs/design/single-user-value.md).
//
// Reporting is the default and writing is a flag, because this edits files in
// somebody's repositories. The report alone is worth running.
func cmdConventions(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("conventions", flag.ContinueOnError)
	registryURL := fs.String("registry", cfg.RegistryURL, "registry URL")
	key := fs.String("key", cfg.RegistryKey, "registry API key")
	root := fs.String("root", ".", "repository, or a directory of repositories")
	write := fs.Bool("write", false, "write the managed block into each project's convention files")
	create := fs.Bool("create", false, "with --write, also create CLAUDE.md / AGENTS.md where absent")
	limit := fs.Int("limit", 20, "most techniques to write (0 for all)")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	reg := &client.Registry{BaseURL: *registryURL, APIKey: *key,
		HTTP: &http.Client{Timeout: 15 * time.Second}}
	techniques, err := reg.Techniques()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tacit conventions: cannot read the playbook at %s: %v\n", *registryURL, err)
		return 1
	}
	serving := servingTechniques(techniques, *limit)
	if len(serving) == 0 {
		fmt.Println("No techniques are in service yet, so there is nothing to write into your projects.")
		fmt.Printf("Promote a draft first: %s/review\n", strings.TrimRight(*registryURL, "/"))
		return 0
	}
	block := conventions.Render(serving, *registryURL)

	abs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit conventions:", err)
		return 1
	}
	repos := conventions.FindRepos(abs)
	if len(repos) == 0 {
		fmt.Printf("No git repository at or under %s.\n", abs)
		fmt.Println("Point --root at a project, or at the directory your projects live in.")
		return 1
	}

	states := make([]conventions.RepoState, 0, len(repos))
	for _, dir := range repos {
		states = append(states, conventions.Inspect(dir, block))
	}

	if *write {
		return writeConventions(states, block, *create, len(serving))
	}
	reportConventions(states, block, len(serving))
	return 0
}

// servingTechniques keeps the techniques in service, best-known first, capped.
// A convention file is read by a model on every turn, so length is a running
// cost — and the cap is what stops the playbook growing into one.
func servingTechniques(all []contracts.Technique, limit int) []contracts.Technique {
	var out []contracts.Technique
	for _, c := range all {
		if c.Status == "stable" {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Org-scoped first: those exist nowhere else, and a model that reads
		// only the first few has read the ones it could not have guessed.
		if (out[i].Scope == "org") != (out[j].Scope == "org") {
			return out[i].Scope == "org"
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func writeConventions(states []conventions.RepoState, block string, create bool, n int) int {
	touched, failed := 0, 0
	for _, st := range states {
		written, err := conventions.Sync(st.Root, block, create)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", st.Name, err)
			failed++
			continue
		}
		if len(written) == 0 {
			continue
		}
		touched++
		fmt.Printf("%s: %s\n", st.Name, strings.Join(written, ", "))
	}
	switch {
	case touched == 0 && failed == 0:
		fmt.Printf("Every convention file already carries these %d techniques. Nothing to do.\n", n)
	default:
		fmt.Printf("\n%d techniques written into %s.\n", n, plural(touched, "project"))
	}
	if !create {
		fmt.Println("Only files that already existed were touched. --create also writes CLAUDE.md and AGENTS.md where a project has neither.")
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func reportConventions(states []conventions.RepoState, block string, n int) {
	fmt.Printf("%d techniques in service, across %s.\n\n", n, plural(len(states), "project"))
	for _, st := range states {
		var marks []string
		for _, f := range st.Files {
			switch {
			case f.Damaged:
				marks = append(marks, f.Path+" (damaged: one marker, not the other)")
			case f.Current:
				marks = append(marks, f.Path+" (current)")
			case f.Managed:
				marks = append(marks, f.Path+" (out of date)")
			case f.Exists:
				marks = append(marks, f.Path+" (unmanaged)")
			}
		}
		if len(marks) == 0 {
			fmt.Printf("  %-24s no convention files — your agents read nothing here\n", st.Name)
			continue
		}
		fmt.Printf("  %-24s %s\n", st.Name, strings.Join(marks, ", "))
	}

	drift := conventions.Drift(states)
	fmt.Println()
	if len(drift.Damaged) > 0 {
		fmt.Printf("Damaged blocks (edited between the markers, or half-deleted): %s\n",
			strings.Join(drift.Damaged, ", "))
		fmt.Println("  --write appends a fresh block rather than guessing; remove the stray marker first.")
	}
	if len(drift.Stale) > 0 {
		fmt.Printf("Out of date: %s\n", strings.Join(drift.Stale, ", "))
	}
	if len(drift.Unmanaged) > 0 {
		fmt.Printf("No playbook at all: %s\n", strings.Join(drift.Unmanaged, ", "))
	}
	for _, g := range drift.Gaps {
		fmt.Printf("%s is in %d of %d projects — %s reads it, and finds nothing in %s\n",
			g.Path, len(g.Present), drift.Repos, g.Harness, strings.Join(g.Missing, ", "))
	}
	if len(drift.Gaps) == 0 && len(drift.Stale) == 0 && len(drift.Unmanaged) == 0 {
		fmt.Println("Your projects agree with each other.")
	}
	fmt.Printf("\nRun with --write to bring them into line.\n")
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
