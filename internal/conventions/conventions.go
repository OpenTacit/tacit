// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package conventions runs internal/originate backwards.
//
// originate reads a repository's written conventions — CLAUDE.md, AGENTS.md,
// .cursorrules, the copilot instructions — and files each unit as a draft
// technique. Nothing writes them back out, so the moment a member promotes a
// technique it exists in the registry and in none of the files their agents
// actually read. For an organization that gap closes on its own: somebody edits
// the shared CLAUDE.md and everybody has it.
//
// One person has the opposite problem. They have one working style and a dozen
// repositories whose convention files disagree with each other, plus four
// harnesses that each read a different filename. Keeping those in step by hand
// is eight edits per decision, so in practice they are never in step
// (docs/design/single-user-value.md).
//
// So this writes the playbook into every convention file a repository already
// has, inside a managed block, and reports where projects have drifted apart.
//
// Two rules hold the whole thing up:
//
//   - Only inside the markers. Everything outside the managed block is the
//     member's own writing and is copied through untouched. A tool that
//     rewrites a file somebody has been editing for a year gets uninstalled the
//     first time it loses a paragraph.
//   - Only files that already exist, unless told otherwise. A repository with no
//     AGENTS.md has not asked for one, and creating it is a decision about
//     somebody's project rather than a sync.
package conventions

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/pkg/contracts"
)

// The managed-block markers. HTML comments, so they are invisible in every
// renderer a member might read these files through, and stable, because a
// changed marker orphans every block already written.
const (
	BeginMarker = "<!-- tacit:begin -->"
	EndMarker   = "<!-- tacit:end -->"
)

// Target is one convention file a harness reads.
type Target struct {
	Path     string // relative to the repository root
	Harness  string // which harness reads it, for the report
	Fallback bool   // create it under --create even when absent
}

// Targets are the files this writes, in the order a report lists them. It
// mirrors originate's conventionFiles, because the point is that what the
// registry read out of a repository can go back into it.
var Targets = []Target{
	{Path: "CLAUDE.md", Harness: "claude-code", Fallback: true},
	{Path: "AGENTS.md", Harness: "codex, amp, opencode", Fallback: true},
	{Path: "GEMINI.md", Harness: "gemini"},
	{Path: ".cursorrules", Harness: "cursor"},
	{Path: ".github/copilot-instructions.md", Harness: "copilot"},
	{Path: "CONVENTIONS.md", Harness: "aider"},
}

// Render turns the techniques into the body of a managed block.
//
// It writes the recipe, not a link. A convention file is read by a model that
// has no way to follow one, so a line saying "see the registry" does nothing in
// the only place it will ever be read.
func Render(techniques []contracts.Technique, registryURL string) string {
	var b strings.Builder
	b.WriteString(BeginMarker)
	b.WriteString("\n<!-- Written by `tacit conventions --write`. Edit these in the playbook,\n")
	b.WriteString("     not here: anything between these markers is replaced. -->\n\n")
	b.WriteString("## Techniques from the playbook\n\n")
	if len(techniques) == 0 {
		b.WriteString("No techniques are in service yet.\n\n")
	}
	for _, c := range techniques {
		fmt.Fprintf(&b, "### %s\n\n", strings.TrimSpace(c.Name))
		if d := oneLine(c.Description); d != "" {
			fmt.Fprintf(&b, "%s\n\n", d)
		}
		if w := oneLine(c.AppliesWhen); w != "" {
			fmt.Fprintf(&b, "When: %s\n\n", w)
		}
		if w := oneLine(c.NotWhen); w != "" {
			fmt.Fprintf(&b, "Not when: %s\n\n", w)
		}
		if r := strings.TrimSpace(c.Recipe); r != "" {
			fmt.Fprintf(&b, "%s\n\n", r)
		}
	}
	if registryURL != "" {
		fmt.Fprintf(&b, "Playbook: %s\n", strings.TrimRight(registryURL, "/")+"/techniques")
	}
	b.WriteString(EndMarker)
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Apply returns the file with the managed block replaced, or appended when it
// carries none. Everything outside the markers survives byte for byte.
func Apply(existing, block string) string {
	start := strings.Index(existing, BeginMarker)
	end := strings.Index(existing, EndMarker)
	if start >= 0 && end > start {
		return existing[:start] + block + existing[end+len(EndMarker):]
	}
	// A file with one marker and not the other has been edited into a state
	// this cannot safely rewrite, so the block is appended rather than guessed
	// at, and the report says the file is damaged.
	trimmed := strings.TrimRight(existing, "\n")
	if trimmed == "" {
		return block + "\n"
	}
	return trimmed + "\n\n" + block + "\n"
}

// FileState is what one convention file looks like now.
type FileState struct {
	Target
	Exists  bool
	Managed bool   // carries a well-formed managed block
	Damaged bool   // carries one marker and not the other
	Current bool   // its block already equals what would be written
	Block   string // the block it carries, for the drift comparison
}

// RepoState is one repository's convention files.
type RepoState struct {
	Root  string
	Name  string
	Files []FileState
}

// Managed reports whether any of the repository's files carry a block.
func (r RepoState) Managed() bool {
	for _, f := range r.Files {
		if f.Managed {
			return true
		}
	}
	return false
}

// Stale names the files carrying a block that is not the current one — the
// drift this report exists to find.
func (r RepoState) Stale() []string {
	var out []string
	for _, f := range r.Files {
		if f.Managed && !f.Current {
			out = append(out, f.Path)
		}
	}
	return out
}

// Inspect reads a repository's convention files without touching them.
func Inspect(root, block string) RepoState {
	st := RepoState{Root: root, Name: filepath.Base(strings.TrimRight(root, "/"))}
	for _, t := range Targets {
		f := FileState{Target: t}
		raw, err := os.ReadFile(filepath.Join(root, t.Path))
		if err == nil {
			f.Exists = true
			body := string(raw)
			start := strings.Index(body, BeginMarker)
			end := strings.Index(body, EndMarker)
			switch {
			case start >= 0 && end > start:
				f.Managed = true
				f.Block = body[start : end+len(EndMarker)]
				f.Current = f.Block == block
			case start >= 0 || end >= 0:
				f.Damaged = true
			}
		}
		st.Files = append(st.Files, f)
	}
	return st
}

// Sync writes the block into every convention file that already exists. With
// create, the fallback targets are written even when absent — one decision
// about somebody's repository, taken only when they ask for it.
//
// Returns the paths written, relative to root. A file already carrying exactly
// this block is left alone rather than rewritten, so a second sync moves no
// mtimes and leaves no diff to read.
func Sync(root, block string, create bool) ([]string, error) {
	var written []string
	for _, f := range Inspect(root, block).Files {
		if !f.Exists && !(create && f.Fallback) {
			continue
		}
		if f.Current {
			continue
		}
		path := filepath.Join(root, f.Path)
		existing := ""
		if raw, err := os.ReadFile(path); err == nil {
			existing = string(raw)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return written, err
		}
		if err := fsx.WriteFileAtomic(path, []byte(Apply(existing, block)), 0o644); err != nil {
			return written, err
		}
		written = append(written, f.Path)
	}
	return written, nil
}

// FindRepos returns the git repositories at or one level below root, sorted.
// One level, because a member keeps their projects in one directory and a full
// walk of a home directory is a different and much slower program.
func FindRepos(root string) []string {
	var out []string
	if isRepo(root) {
		out = append(out, root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		sort.Strings(out)
		return out
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if dir := filepath.Join(root, e.Name()); isRepo(dir) {
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}

func isRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}
