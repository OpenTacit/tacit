// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A scratch repository with known commits, so the arithmetic can be checked
// rather than believed.
type repo struct {
	t   *testing.T
	dir string
	n   int
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "scratch@example.invalid")
	r.git("config", "user.name", "Scratch")
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2026-09-01T12:00:00Z", "GIT_COMMITTER_DATE=2026-09-01T12:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (r *repo) write(name string, lines ...string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) commit(msg string) {
	r.t.Helper()
	r.n++
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
}

func numbered(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = prefix + " line " + string(rune('a'+i%26)) + strings.Repeat("x", i%3)
	}
	return out
}

// Ten lines written, three of them later deleted: seven survive. The point of
// blaming at the tip rather than adding up the diffs is that this is exact.
func TestLandedCountsTheLinesStillInTheTree(t *testing.T) {
	r := newRepo(t)
	r.write("a.go", numbered("a", 10)...)
	r.commit("write ten")
	r.write("a.go", numbered("a", 7)...)
	r.commit("delete three")

	got, err := Landed(r.dir, 30*24*time.Hour, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Commits != 2 {
		t.Fatalf("commits = %d, want 2", got.Commits)
	}
	if got.Survived != 7 {
		t.Fatalf("survived = %d of %d added, want 7", got.Survived, got.Added)
	}
	if got.Project != filepath.Base(r.dir) {
		t.Errorf("project = %q, want the repository's own name", got.Project)
	}
	// The rate carries its denominator or it is not a rate.
	if rate := got.SurvivalRate(); rate <= 0.6 || rate >= 0.8 {
		t.Errorf("survival rate = %.2f, want about 7/10", rate)
	}
}

// A rename must not read as a deletion. Git reports it as "old => new" and
// only the new name can hold a surviving line, so that is the one blamed.
func TestLandedFollowsARename(t *testing.T) {
	r := newRepo(t)
	r.write("old/name.go", numbered("n", 12)...)
	r.commit("write twelve")
	r.git("mv", "old/name.go", "old/other.go")
	r.commit("rename it")

	got, err := Landed(r.dir, 30*24*time.Hour, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	// Every line is still there under its new name, and blame still credits
	// the commit that wrote it.
	if got.Survived != 12 {
		t.Fatalf("survived = %d, want all 12 across the rename", got.Survived)
	}
	if got.Blamed == 0 {
		t.Fatal("nothing was blamed; the renamed path did not resolve")
	}
}

// A deleted file keeps none of its lines, which is the right answer and not an
// error: the blame simply fails and the file contributes nothing.
func TestLandedCountsADeletedFileAsGone(t *testing.T) {
	r := newRepo(t)
	r.write("keep.go", numbered("k", 4)...)
	r.write("drop.go", numbered("d", 9)...)
	r.commit("write both")
	if err := os.Remove(filepath.Join(r.dir, "drop.go")); err != nil {
		t.Fatal(err)
	}
	r.commit("drop one")

	got, err := Landed(r.dir, 30*24*time.Hour, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Survived != 4 {
		t.Fatalf("survived = %d, want the 4 lines of the file that is still there", got.Survived)
	}
}

// A rebase rewrites the commits and their dates. The measure then describes
// the history the member HAS rather than the one they worked in — which is
// exactly what the command's own prose says, and this pins that it does not
// instead produce a silent zero or an error.
func TestLandedSurvivesARebase(t *testing.T) {
	r := newRepo(t)
	r.write("base.go", numbered("b", 3)...)
	r.commit("base")
	r.git("checkout", "-q", "-b", "side")
	r.write("side.go", numbered("s", 8)...)
	r.commit("side work")
	r.git("checkout", "-q", "main")
	r.write("main.go", numbered("m", 5)...)
	r.commit("main work")
	r.git("checkout", "-q", "side")
	r.git("rebase", "-q", "main")

	got, err := Landed(r.dir, 30*24*time.Hour, time.Now(), "")
	if err != nil {
		t.Fatalf("a rebased branch should still answer: %v", err)
	}
	if got.Branch != "side" {
		t.Errorf("branch = %q, want side", got.Branch)
	}
	// The rewritten commit is in the log under its new hash, and its lines are
	// still in the tree under it.
	if got.Survived != 3+8+5 {
		t.Fatalf("survived = %d of %d, want every line of the rebased history",
			got.Survived, got.Added)
	}
}

// Nothing outside the working directory is read. The member hands over a path
// by standing in it, and that is the whole of the permission being granted.
func TestLandedReadsNothingOutsideTheRepository(t *testing.T) {
	outer := t.TempDir()
	secret := filepath.Join(outer, "SECRET.txt")
	if err := os.WriteFile(secret, []byte("not yours\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &repo{t: t, dir: filepath.Join(outer, "inner")}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "s@example.invalid")
	r.git("config", "user.name", "S")
	r.write("in.go", numbered("i", 6)...)
	r.commit("inside only")

	got, err := Landed(r.dir, 30*24*time.Hour, time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Files != 1 || got.Added != 6 {
		t.Fatalf("the report reached beyond the repository: %+v", got)
	}
	// And the report names the repository, never where it sits on the disk.
	body := got.Project + got.Branch
	for _, leaked := range []string{outer, "SECRET", string(filepath.Separator) + "tmp"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("the report carried %q out of the path", leaked)
		}
	}
}

// Outside a repository the command says so rather than reporting zeroes, which
// would read as a project where nothing survived.
func TestLandedOutsideARepositoryIsAnError(t *testing.T) {
	if _, err := Landed(t.TempDir(), time.Hour, time.Now(), ""); err == nil {
		t.Fatal("a directory with no repository in it reported a result")
	}
}

// Git's rename notation has three shapes and all three have to resolve to the
// name the file has now, because only that name can hold a surviving line.
func TestRenameNotationResolvesToTheNewName(t *testing.T) {
	for raw, want := range map[string]string{
		"a.go":                  "a.go",
		"a.go => b.go":          "b.go",
		"dir/{old => new}/x.go": "dir/new/x.go",
		"{old => new}":          "new",
	} {
		if got := renamedTo(raw); got != want {
			t.Errorf("renamedTo(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The join with the session log is the basename, which is all either side
// keeps: the log has no path to match on and this has no identity to match
// with. Without it the command is a git wrapper and not a feedback loop.
func TestLandedJoinsTheSessionLogOnTheProjectName(t *testing.T) {
	r := newRepo(t)
	r.write("x.go", numbered("x", 20)...)
	r.commit("write twenty")

	state := t.TempDir()
	detail, daily := sessionLogPaths(state)
	now := time.Now().UTC()
	l := loadSessionLog(detail, daily, func() time.Time { return now })
	l.upsert(sessionRecord{Key: "k1", Start: now.Add(-time.Hour), End: now,
		Harness: "claude-code", Project: filepath.Base(r.dir), Turns: 9,
		Status: true, LinesAdded: 34, LinesRemoved: 11})
	l.flush()

	got, err := Landed(r.dir, 30*24*time.Hour, now, state)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RecordedReported || got.Recorded != 34 || got.RecordedRemoved != 11 {
		t.Fatalf("the session log did not join: %+v", got)
	}
	if got.RecordedSessions != 1 {
		t.Fatalf("recorded sessions = %d, want 1", got.RecordedSessions)
	}
	// A different project on the same machine must not bleed in.
	l.upsert(sessionRecord{Key: "k2", Start: now.Add(-time.Hour), End: now,
		Harness: "claude-code", Project: "somewhere-else", Turns: 4,
		Status: true, LinesAdded: 900})
	l.flush()
	again, err := Landed(r.dir, 30*24*time.Hour, now, state)
	if err != nil {
		t.Fatal(err)
	}
	if again.Recorded != 34 {
		t.Fatalf("another project's lines reached this report: %d", again.Recorded)
	}
}

// A binary file contributes no lines to either side. A figure about lines has
// nothing to say about a PNG, and "-" is not a number.
func TestBinaryFilesContributeNoLines(t *testing.T) {
	if _, _, _, ok := numstat("-\t-\tlogo.png"); ok {
		t.Error("a binary numstat line was counted")
	}
	a, rm, path, ok := numstat("12\t3\tinternal/x.go")
	if !ok || a != 12 || rm != 3 || path != "internal/x.go" {
		t.Errorf("numstat = %d,%d,%q,%v", a, rm, path, ok)
	}
}

// A filed reading is the only figure on the Usage page that nothing refreshes.
// It exists because a member ran a command in a repository, and it describes a
// branch that keeps moving afterwards — so it has to carry the day it was taken
// and it has to leave on its own.
func TestAFiledSurvivalReadingCarriesItsDateAndExpires(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)

	if got := LandedFilings(dir, now); len(got) != 0 {
		t.Fatalf("an empty state dir filed %d readings", len(got))
	}
	rep := LandedReport{Project: "tacit", Branch: "main", Commits: 12, Added: 900, Survived: 800}
	if err := SaveLanded(dir, rep, 30*24*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	got := LandedFilings(dir, now)
	if len(got) != 1 {
		t.Fatalf("filed %d readings, want 1", len(got))
	}
	if !got[0].AskedAt.Equal(now) {
		t.Errorf("asked at %v, want %v — an undated survival figure is worse than none", got[0].AskedAt, now)
	}
	if got[0].WindowDays != 30 {
		t.Errorf("window = %d days, want 30 — a 7-day survival must never read as a 90-day one", got[0].WindowDays)
	}

	// One row per project, not a history: a series of readings taken whenever
	// somebody happened to run a command is not a trend.
	later := now.Add(48 * time.Hour)
	if err := SaveLanded(dir, LandedReport{Project: "tacit", Added: 1200, Survived: 1000}, 7*24*time.Hour, later); err != nil {
		t.Fatal(err)
	}
	got = LandedFilings(dir, later)
	if len(got) != 1 || got[0].Added != 1200 || got[0].WindowDays != 7 {
		t.Errorf("re-asking did not replace the reading: %+v", got)
	}

	// And it goes on its own. Past the horizon the branch it describes has
	// moved far enough that the figure is a claim about a repository that no
	// longer exists in that shape.
	if stale := LandedFilings(dir, later.Add(landedRetention+time.Hour)); len(stale) != 0 {
		t.Errorf("a reading past the horizon is still on the page: %+v", stale)
	}

	// A project with no name cannot be filed against anything.
	if err := SaveLanded(dir, LandedReport{Added: 5}, time.Hour, later); err != nil {
		t.Fatal(err)
	}
	if got := LandedFilings(dir, later); len(got) != 1 {
		t.Errorf("an unnamed project was filed: %+v", got)
	}
}
