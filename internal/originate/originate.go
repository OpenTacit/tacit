// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package originate reads the conventions an organization has already written
// down, and turns them into draft techniques.
//
// The cold start is the product's hardest moment. A new registry ships a
// starter set of general moves and no evidence, so the funnel is empty, the
// dashboard has nothing to report, and the first suggestion a member can
// receive is a general technique marked "awaiting measured outcomes". Both
// mechanisms that fill a playbook need something a new registry does not have:
// the research pass profiles ninety days of events, and the miner needs K
// origins across M cohorts before it will propose anything.
//
// But the organization has written its playbook down already. It is in the
// repository: CLAUDE.md, AGENTS.md, .cursorrules, the copilot instructions, the
// skills and commands under .claude/. Those files exist because somebody found
// a move that worked and wanted it repeated — which is the definition of a
// technique — and they are org-scoped by construction, which is the half of the
// playbook no public source can supply.
//
// So this reads them and files each unit as a DRAFT. Nothing serves: a draft is
// held out of retrieval until a reviewer promotes it. The operator's first act
// becomes editing their own playbook, which is the act the whole product is
// organised around, instead of reading a table of contents beside an empty
// funnel.
//
// What this deliberately does not do is judge. There is no model on this path —
// `tacit init` runs before anybody has been asked for a key — so a candidate is
// the org's own words, quoted, with the file and heading it came from. The
// reviewer decides what is a technique. Distilling these into sharper prose is
// what the research pass is for, once there is a key and a queue to work on.
package originate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Source is one file of written convention.
type Source struct {
	Path string // relative to the repo root, as the reviewer will see it
	Body string
}

// Candidate is one unit of that file, shaped like a technique.
type Candidate struct {
	Name        string
	Description string
	Recipe      string
	Source      string // "CLAUDE.md § Interface" — where a reviewer can go and read it
	Tags        []string
	TaskTypes   []string
}

// conventionFiles are the paths an org writes its agent conventions to. Ordered
// by how much of the org's own practice they usually carry, because the cap
// below takes them in order.
var conventionFiles = []string{
	"CLAUDE.md",
	"AGENTS.md",
	"GEMINI.md",
	".cursorrules",
	".github/copilot-instructions.md",
	".claude/CLAUDE.md",
	"CONVENTIONS.md",
}

// conventionGlobs are the directories of small, single-purpose files — a skill
// or a command IS one move, so each file is one candidate rather than being
// split further.
var conventionGlobs = []string{
	".claude/skills/*/SKILL.md",
	".claude/commands/*.md",
	".cursor/rules/*.md",
	".cursor/rules/*.mdc",
	"prompts/*.md",
}

// wholeFileGlobs is the subset of the above where the file IS one move. A
// skill, a command, a rule, a prompt: each was written to be used whole, and
// cutting it at its own headings produces "Output" and "Format skeleton" as
// separate techniques, which is nonsense a reviewer then has to reject one by
// one.
var wholeFileGlobs = []string{
	".claude/skills/", ".claude/commands/", ".cursor/rules/", "prompts/",
}

// wholeFile reports whether a path is one of those.
func wholeFile(rel string) bool {
	for _, prefix := range wholeFileGlobs {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// MaxCandidates caps what one pass files. A review queue is a queue somebody
// has to work through, and forty drafts on the first morning is a backlog, not
// a head start.
const MaxCandidates = 25

// minUnitChars is the floor for a unit worth reviewing. A one-line heading with
// nothing under it is a section marker, not a move.
const minUnitChars = 60

// Find reads every convention file a repository carries. A missing file is not
// an error — most repos have one or two of these, and none is a normal answer.
func Find(root string) []Source {
	var out []Source
	seen := map[string]bool{}
	add := func(rel string) {
		if seen[rel] {
			return
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || len(strings.TrimSpace(string(raw))) < minUnitChars {
			return
		}
		seen[rel] = true
		out = append(out, Source{Path: rel, Body: string(raw)})
	}
	for _, rel := range conventionFiles {
		add(rel)
	}
	for _, pattern := range conventionGlobs {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		for _, m := range matches {
			if rel, err := filepath.Rel(root, m); err == nil {
				add(filepath.ToSlash(rel))
			}
		}
	}
	return out
}

// IsRepoRoot reports whether dir looks like the root of a checkout. Used to
// decide whether there is anything to read at all, so `tacit init` run in a home
// directory says nothing rather than searching the disk.
func IsRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

var (
	headingRe = regexp.MustCompile(`(?m)^(#{2,4})\s+(.+?)\s*$`)
	// A top-level list item: "- ", "* ", or "1. " at the start of a line. The
	// user's own conventions are often a numbered list with no headings at all.
	itemRe = regexp.MustCompile(`(?m)^(?:[-*]|\d+\.)\s+(.+)$`)
	// Frontmatter on a skill or command file.
	frontRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)
	// Section headings that are about the repository rather than about how to
	// work in it. Matched on the whole heading, lowercased.
	skipHeadings = regexp.MustCompile(`(?i)^(license|licence|installation|install|contents|table of contents|contributing|changelog|acknowledge?ments|credits|thanks|badges|requirements|prerequisites|layout|structure|api reference|references?|links?|see also|todo|roadmap|faq)\b`)
)

// Candidates splits one source into reviewable units.
//
// Three shapes, in the order they are tried, because a document answers to
// exactly one of them: a file with frontmatter is a single move (a skill, a
// command); a file with headings is one move per section; a file that is a bare
// list is one move per item. A document with none of those is one move.
func Candidates(s Source) []Candidate {
	if m := frontRe.FindStringSubmatch(s.Body); m != nil {
		return singleFileCandidate(s, m[1], s.Body[len(m[0]):])
	}
	if wholeFile(s.Path) {
		return singleFileCandidate(s, "", s.Body)
	}
	if units := headingUnits(s); len(units) > 0 {
		return units
	}
	if units := listUnits(s); len(units) > 0 {
		return units
	}
	body := strings.TrimSpace(s.Body)
	if len(body) < minUnitChars {
		return nil
	}
	return []Candidate{newCandidate(titleFromPath(s.Path), body, s.Path)}
}

// singleFileCandidate handles a skill or command: its frontmatter names it and
// says when to use it, and its body is the recipe.
func singleFileCandidate(s Source, front, body string) []Candidate {
	name := frontField(front, "name")
	if name == "" {
		name = titleFromPath(s.Path)
	}
	desc := frontField(front, "description")
	body = strings.TrimSpace(body)
	if len(body) < minUnitChars && desc == "" {
		return nil
	}
	c := newCandidate(name, body, s.Path)
	if desc != "" {
		c.Description = firstSentences(desc)
	}
	return []Candidate{c}
}

// headingUnits splits at the shallowest heading level the document uses, so a
// document organised in ## sections is not also cut at every ###.
func headingUnits(s Source) []Candidate {
	all := headingRe.FindAllStringSubmatchIndex(s.Body, -1)
	if len(all) == 0 {
		return nil
	}
	top := 99
	for _, m := range all {
		if n := m[3] - m[2]; n < top {
			top = n
		}
	}
	var out []Candidate
	// Rules written before the first heading are still rules — house style is
	// most often a numbered list at the top of the file, above any section.
	if preamble := strings.TrimSpace(s.Body[:all[0][0]]); preamble != "" {
		out = append(out, listUnits(Source{Path: s.Path, Body: preamble})...)
	}
	for i, m := range all {
		if m[3]-m[2] != top {
			continue
		}
		title := strings.TrimSpace(s.Body[m[4]:m[5]])
		end := len(s.Body)
		for _, next := range all[i+1:] {
			if next[3]-next[2] == top {
				end = next[0]
				break
			}
		}
		body := strings.TrimSpace(s.Body[m[1]:end])
		if skipHeadings.MatchString(title) || len(body) < minUnitChars {
			continue
		}
		out = append(out, newCandidate(title, body, s.Path+" § "+title))
	}
	return out
}

// listUnits handles a document that is a bare list of rules — no headings, one
// numbered or bulleted instruction per line, which is how house style is most
// often written down.
func listUnits(s Source) []Candidate {
	items := itemRe.FindAllStringSubmatch(s.Body, -1)
	if len(items) < 3 {
		return nil // a couple of bullets inside prose is not a list of rules
	}
	var out []Candidate
	for _, m := range items {
		text := strings.TrimSpace(m[1])
		if len(text) < minUnitChars {
			continue
		}
		out = append(out, newCandidate(firstSentences(text), text, s.Path))
	}
	return out
}

// newCandidate fills the shape every path produces: a name short enough to read
// in a list, the org's own words as both the description and the recipe, and
// task types guessed from the words themselves so retrieval has something to
// match on before a reviewer touches it.
func newCandidate(name, body, source string) Candidate {
	return Candidate{
		Name:        trimName(name),
		Description: firstSentences(body),
		Recipe:      body,
		Source:      source,
		Tags:        []string{"convention"},
		TaskTypes:   taskTypesFor(name + " " + body),
	}
}

// taskTypesFor guesses the shape of turn a convention applies to, from the
// vocabulary internal/auditor/capture.TaskType emits. A guess is honest here in
// a way it would not be on an event: this labels a DRAFT, which serves nobody
// until a reviewer has read it and can change the label.
func taskTypesFor(text string) []string {
	t := strings.ToLower(text)
	var out []string
	add := func(tt string, words ...string) {
		for _, w := range words {
			if strings.Contains(t, w) {
				out = append(out, tt)
				return
			}
		}
	}
	add("verification", "test", "run ", "build", "verify", "check", "lint", "screenshot", "deploy", "curl")
	add("editing", "edit", "write", "change", "refactor", "implement", "rename", "commit", "fix")
	add("exploration", "read", "search", "grep", "find", "look at", "inspect")
	add("delegation", "subagent", "agent", "delegate", "parallel", "fan out")
	add("research", "web", "docs", "documentation", "look up")
	if len(out) == 0 {
		return []string{"conversation"}
	}
	return out
}

// Dedup drops units whose text a previous unit already carried. Repositories
// routinely hold the same conventions twice — CLAUDE.md and AGENTS.md are
// commonly copies of each other, and one of the two is often a symlink — and
// two identical drafts is two reviews of one decision.
func Dedup(cs []Candidate) []Candidate {
	seen := map[string]bool{}
	out := cs[:0:0]
	for _, c := range cs {
		key := strings.Join(strings.Fields(strings.ToLower(c.Recipe)), " ")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// Cap trims a pass to something a person will actually review, keeping the
// order Find produced — the org's own top-level conventions before the
// per-command detail.
func Cap(cs []Candidate, n int) []Candidate {
	if len(cs) <= n {
		return cs
	}
	return cs[:n]
}

func trimName(s string) string {
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "#*`\"'."))
	s = strings.Join(strings.Fields(s), " ")
	const max = 90
	if len(s) > max {
		if i := strings.LastIndex(s[:max], " "); i > 40 {
			return s[:i]
		}
		return s[:max]
	}
	return s
}

// firstSentences is the summary: enough to judge from a list, not the whole
// section again.
func firstSentences(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	const max = 300
	if len(s) <= max {
		return s
	}
	if i := strings.LastIndexAny(s[:max], ".!?"); i > 60 {
		return s[:i+1]
	}
	return strings.TrimSpace(s[:max]) + "…"
}

func titleFromPath(rel string) string {
	base := filepath.Base(rel)
	if base == "SKILL.md" || base == "index.md" {
		base = filepath.Base(filepath.Dir(rel))
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(base)
	base = strings.TrimSpace(base)
	if base == "" {
		return rel
	}
	return strings.ToUpper(base[:1]) + base[1:]
}

// frontField reads one scalar out of YAML frontmatter. Scalars are all these
// files carry that this package wants, so it does not pull in a parser.
func frontField(front, key string) string {
	for _, line := range strings.Split(front, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), key+":")
		if !ok {
			continue
		}
		return strings.Trim(strings.TrimSpace(rest), `"'`)
	}
	return ""
}
