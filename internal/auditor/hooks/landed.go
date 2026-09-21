// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Did it land: of the lines an agent wrote, how many are still there.
//
// This is the half of the question nobody gives an individual. Every other
// figure in this package is about the writing — turns, tools, cost, lines
// added. None of them says whether the work survived the week, and a member
// who cannot see that has no way to tell a productive month from a busy one.
// GitClear reports churn doubling from a 3.3% pre-AI baseline to 7.1%, and
// duplicated blocks up 81% since 2023; a member who can see their own number
// against their own past is the only person who can do anything about theirs.
//
// The constraint is the interesting part. The session log keeps a project
// BASENAME and never a path — deliberately, and that is not being relaxed — so
// nothing in the log can find the repository later. The way round it is not to
// store the path but to ask the question from inside the repository: the member
// runs `tacit usage --landed` where they are standing, and the working
// directory IS the path. Nothing outside it is read, and nothing about it is
// written down.
//
// Survival is measured exactly rather than estimated. The commits in the window
// are known; blame at the tip says which of today's lines came from which
// commit; the lines still attributed to a window commit are the lines that
// survived. Only the files the window touched are blamed, because no other file
// can hold a line the window wrote.
package hooks

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LandedReport is what the repository says about the window, beside what the
// session log recorded for the same days.
type LandedReport struct {
	Project string    `json:"project"` // the repository's own name, never its path
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Branch  string    `json:"branch,omitempty"`

	Commits int `json:"commits"`
	// Added and Removed are what the window's commits did, from git's own
	// numbers. Files is how many distinct files they touched.
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Files   int `json:"files"`
	// Survived is how many of Added are still in the working tree, by blame at
	// the tip. Blamed is how many of Files could be blamed — a file deleted
	// since keeps none of its lines, and a binary keeps no lines at all.
	Survived int `json:"survived"`
	Blamed   int `json:"blamed"`

	// Recorded is what the session log holds for this project over the same
	// days: what the agent said it wrote. Zero where the status line was not
	// wired, which is why RecordedFrom says whether anything measured it.
	Recorded         int  `json:"recorded"`
	RecordedRemoved  int  `json:"recorded_removed"`
	RecordedSessions int  `json:"recorded_sessions"`
	RecordedReported bool `json:"recorded_reported"`
}

// SurvivalRate is the share of the window's added lines still in the tree. Zero
// added yields zero rather than a division: a rate with no denominator is not a
// small number, it is no number, and the caller says so instead of printing it.
func (r LandedReport) SurvivalRate() float64 {
	if r.Added == 0 {
		return 0
	}
	return float64(r.Survived) / float64(r.Added)
}

// landedMaxWindow bounds how far back the question is asked. Blame is the
// expensive half and it costs one pass per file the window touched; a year of
// a busy repository is every file in it, and a member waiting three minutes for
// a figure will not ask for it twice.
const landedMaxWindow = 120 * 24 * time.Hour

// Landed answers the question from inside dir. Everything it runs is read-only
// and scoped to that repository: a git log, and a blame per file the window
// touched. Nothing outside dir is read and nothing anywhere is written.
func Landed(dir string, window time.Duration, now time.Time, stateDir string) (LandedReport, error) {
	if window <= 0 || window > landedMaxWindow {
		window = landedMaxWindow
	}
	from := now.Add(-window).UTC()
	rep := LandedReport{From: from, To: now.UTC()}

	top, err := gitOut(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return rep, fmt.Errorf("not inside a git repository: %w", err)
	}
	top = strings.TrimSpace(top)
	rep.Project = projectOf(top)
	if b, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		rep.Branch = strings.TrimSpace(b)
	}

	commits, paths, added, removed, err := gitWindow(top, from)
	if err != nil {
		return rep, err
	}
	rep.Commits, rep.Added, rep.Removed, rep.Files = len(commits), added, removed, len(paths)
	if len(commits) == 0 {
		return rep, nil
	}
	rep.Survived, rep.Blamed = blameSurvivors(top, paths, commits)

	// What the sessions said about the same project over the same days. The
	// join is the basename, which is all either side keeps.
	sum := ReadWorkSummary(stateDir, window)
	for _, g := range sum.Projects {
		if g.Key != rep.Project {
			continue
		}
		rep.Recorded, rep.RecordedRemoved = g.LinesAdded, g.LinesRemoved
		rep.RecordedSessions = g.Sessions
		rep.RecordedReported = g.StatusSessions > 0
	}
	return rep, nil
}

// gitWindow reads the window's commits and what they did, in one pass. The
// format line marks a commit and the numstat lines under it are its files.
func gitWindow(top string, from time.Time) (commits map[string]bool, paths []string, added, removed int, err error) {
	out, err := gitOut(top, "log", "--no-merges", "--numstat", "--format=@%H",
		"--since="+from.Format(time.RFC3339))
	if err != nil {
		return nil, nil, 0, 0, err
	}
	commits = map[string]bool{}
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "@") {
			commits[strings.TrimPrefix(line, "@")] = true
			continue
		}
		a, r, path, ok := numstat(line)
		if !ok {
			continue
		}
		added += a
		removed += r
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return commits, paths, added, removed, sc.Err()
}

// numstat reads one "added<TAB>removed<TAB>path" line. A binary file reports
// "-" for both and contributes no lines to either side, which is right: a
// figure about lines has nothing to say about a PNG.
//
// A rename arrives as "old => new", sometimes with a shared prefix or suffix in
// braces. Only the new name can hold a surviving line, so that is the one kept.
func numstat(line string) (added, removed int, path string, ok bool) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 {
		return 0, 0, "", false
	}
	if parts[0] == "-" || parts[1] == "-" {
		return 0, 0, "", false // binary
	}
	a, err1 := strconv.Atoi(parts[0])
	r, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, "", false
	}
	return a, r, renamedTo(parts[2]), true
}

// renamedTo resolves git's rename notation to the name a file has now.
//
//	a.go => b.go                -> b.go
//	dir/{old => new}/x.go       -> dir/new/x.go
//	{old => new}                -> new
func renamedTo(raw string) string {
	raw = strings.TrimSpace(raw)
	open := strings.Index(raw, "{")
	if open >= 0 {
		close := strings.Index(raw[open:], "}")
		if close >= 0 {
			inner := raw[open+1 : open+close]
			if _, after, found := strings.Cut(inner, " => "); found {
				return strings.ReplaceAll(raw[:open]+after+raw[open+close+1:], "//", "/")
			}
		}
	}
	if _, after, found := strings.Cut(raw, " => "); found {
		return strings.TrimSpace(after)
	}
	return raw
}

// blameSurvivors counts the lines in the tree today that a window commit wrote.
//
// Blame at the tip is what makes this exact rather than a guess: every line in
// the file carries the commit that last touched it, so a line still attributed
// to a window commit is a line the window wrote that nothing has rewritten. A
// file that has since been deleted simply fails to blame and keeps none of its
// lines, which is the right answer.
func blameSurvivors(top string, paths []string, commits map[string]bool) (survived, blamed int) {
	for _, p := range paths {
		out, err := gitOut(top, "blame", "--line-porcelain", "HEAD", "--", p)
		if err != nil {
			continue // deleted since, or never blameable: no lines survive
		}
		blamed++
		sc := bufio.NewScanner(strings.NewReader(out))
		sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
		for sc.Scan() {
			line := sc.Text()
			// A porcelain record opens with "<sha> <orig> <final> [count]".
			if len(line) < 40 || line[0] == '\t' || !isHex40(line[:40]) {
				continue
			}
			if commits[line[:40]] {
				survived++
			}
		}
	}
	return survived, blamed
}

func isHex40(s string) bool {
	for i := 0; i < 40; i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// gitOut runs one read-only git command inside dir. -C scopes it: git is never
// invoked anywhere but the repository the member is standing in, and no command
// here writes.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
