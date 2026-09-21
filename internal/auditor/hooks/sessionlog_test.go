// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/contracts"
)

func sessionPaths(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	detail, daily := sessionLogPaths(dir)
	return dir, detail, daily
}

func rec(key string, end time.Time, model, project string, turns int) sessionRecord {
	return sessionRecord{
		Key: key, Start: end.Add(-10 * time.Minute), End: end, Harness: "claude-code",
		Model: model, ModelRaw: model, Project: project, TaskType: "editing",
		Turns: turns, Tools: map[string]int{"Bash": 2, "Edit": 1}, Retries: 1,
	}
}

// The session log is the member-local answer to "how am I working", which the
// registry structurally cannot hold. It has to survive the daemon restarting,
// because a machine that idle-exits many times a day would otherwise remember
// nothing.
func TestSessionLogUpsertsAndSurvivesReload(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	s := loadSessionLog(detail, daily, fixedClock(now))
	s.upsert(rec("sess1", now, "anthropic/claude-sonnet-5", "tacit", 3))
	s.upsert(rec("sess1", now, "anthropic/claude-sonnet-5", "tacit", 7)) // same session, later Stop
	s.upsert(rec("sess2", now, "openai/gpt-4o", "other", 2))

	s2 := loadSessionLog(detail, daily, fixedClock(now))
	sum := s2.summarizeWork(0)
	if sum.Totals.Sessions != 2 {
		t.Fatalf("sessions = %d, want 2 (an upserted session is one session)", sum.Totals.Sessions)
	}
	if sum.Totals.Turns != 9 {
		t.Fatalf("turns = %d, want 9 (7 from the last state of sess1, not 3+7)", sum.Totals.Turns)
	}
	if len(sum.Models) != 2 {
		t.Fatalf("models = %+v, want two", sum.Models)
	}
	if sum.Models[0].Key != "anthropic/claude-sonnet-5" || sum.Models[0].Turns != 7 {
		t.Fatalf("busiest model row wrong: %+v", sum.Models[0])
	}
}

// A broken line is skipped, never fatal: a half-written record from a killed
// process must not cost the member their history.
func TestSessionLogSkipsCorruptLines(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	good, _ := json.Marshal(rec("sess1", now, "anthropic/claude-sonnet-5", "tacit", 4))
	body := "{not json\n" + string(good) + "\n{\"key\":\"\"}\n"
	if err := os.WriteFile(detail, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s := loadSessionLog(detail, daily, fixedClock(now))
	if got := s.summarizeWork(0).Totals.Sessions; got != 1 {
		t.Fatalf("sessions = %d, want 1 — the good line survived and the bad ones went", got)
	}
	// And the rewrite dropped them, so they are not re-parsed for ever.
	raw, _ := os.ReadFile(detail)
	if strings.Contains(string(raw), "not json") {
		t.Fatalf("the corrupt line was kept: %q", raw)
	}
}

// Detail folds into days exactly once. A record that joined a day and then
// joined it again on the next load would inflate a member's history every time
// their daemon restarted.
func TestSessionLogCompactionIsIdempotent(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	old := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	now := old.Add(sessionDetailRetention + 48*time.Hour)

	s := loadSessionLog(detail, daily, fixedClock(old))
	s.upsert(rec("sess1", old, "anthropic/claude-sonnet-5", "tacit", 5))
	s.upsert(rec("sess2", old, "anthropic/claude-sonnet-5", "tacit", 3))

	// A later process finds both past the detail horizon and folds them.
	first := loadSessionLog(detail, daily, fixedClock(now)).summarizeWork(0)
	if first.Totals.Sessions != 2 || first.Totals.Turns != 8 {
		t.Fatalf("after folding = %d sessions / %d turns, want 2 and 8", first.Totals.Sessions, first.Totals.Turns)
	}
	if len(readLines(detail)) != 0 {
		t.Fatalf("folded records stayed in the detail file: %v", readLines(detail))
	}
	// Every subsequent load must agree.
	for i := 0; i < 3; i++ {
		again := loadSessionLog(detail, daily, fixedClock(now)).summarizeWork(0)
		if again.Totals.Sessions != first.Totals.Sessions || again.Totals.Turns != first.Totals.Turns {
			t.Fatalf("reload %d moved the history: %+v -> %+v", i, first.Totals, again.Totals)
		}
	}
}

// Nothing a member typed, nothing a tool printed, and nothing naming their
// disk may reach either file. This is the line the whole local record rests on,
// so it is asserted against the bytes rather than against the struct.
func TestSessionLogHoldsNoFreeText(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	var stats sessionStats
	stats.observe(now, "UserPromptSubmit", map[string]any{
		"prompt": "SECRETPROMPT rewrite the billing module"})
	stats.observe(now, "PostToolUse", map[string]any{
		"tool_name": "Bash", "tool_input": "cat /etc/SECRETFILE", "tool_output": "SECRETOUTPUT"})
	stats.observe(now, "Stop", map[string]any{"transcript_path": "/home/someone/SECRETPATH"})

	s := loadSessionLog(detail, daily, fixedClock(now))
	s.upsert(stats.record("hashed-key", "claude-code", "claude-sonnet-5",
		"/home/someone/Repos/tacit", 0))

	raw, err := os.ReadFile(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SECRETPROMPT", "SECRETFILE", "SECRETOUTPUT",
		"SECRETPATH", "/home/someone", "billing"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("the session record leaked %q: %s", forbidden, raw)
		}
	}
	// The project is the repository's own name and nothing above it.
	if !strings.Contains(string(raw), `"project":"tacit"`) {
		t.Fatalf("the project basename is missing: %s", raw)
	}
	// Tool NAMES are kept: an enumerable vocabulary the session demonstrably
	// used, the same standard ResourcesInPlay holds.
	if !strings.Contains(string(raw), "Bash") {
		t.Fatalf("tool names are meant to be kept: %s", raw)
	}
}

// A retry is structural — the same tool run again with the same arguments —
// never a reading of what the output said. A detector that decided what
// "failed" means from result text would be wrong often and silently.
func TestSessionStatsCountsRetriesStructurally(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	var s sessionStats
	call := func(name, input string) {
		s.observe(now, "PostToolUse", map[string]any{"tool_name": name, "tool_input": input})
	}
	call("Bash", "go test ./...")
	call("Bash", "go test ./...") // the same thing again: a retry
	call("Bash", "go build ./...")
	call("Edit", "x")
	if s.retries != 1 {
		t.Fatalf("retries = %d, want 1", s.retries)
	}
	if s.tools["Bash"] != 3 || s.tools["Edit"] != 1 {
		t.Fatalf("tool counts wrong: %+v", s.tools)
	}
}

// Tokens and cost are reported by a source or they are not measured at all. A
// zero that reads as a measurement is the "no fake zeros" rule failing in the
// one place a member would believe it.
func TestWorkSummaryDistinguishesUnmeasuredFromZero(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s := loadSessionLog(detail, daily, fixedClock(now))
	s.upsert(rec("sess1", now, "anthropic/claude-sonnet-5", "tacit", 3))
	if sum := s.summarizeWork(0); sum.TokensReported || sum.CostReported {
		t.Fatalf("a source that reported nothing was treated as having measured zero: %+v", sum)
	}
	priced := rec("sess2", now, "anthropic/claude-sonnet-5", "tacit", 2)
	priced.InTokens, priced.OutTokens, priced.CostUSD = 1200, 300, 0.04
	s.upsert(priced)
	sum := s.summarizeWork(0)
	if !sum.TokensReported || !sum.CostReported {
		t.Fatalf("a source that did report was not believed: %+v", sum)
	}
	if sum.Totals.CostUSD != 0.04 {
		t.Fatalf("cost = %v, want 0.04", sum.Totals.CostUSD)
	}
}

func dayRow(date, model string, sessions, turns, retries int) WorkDay {
	return WorkDay{Date: date, Model: model, WorkTotals: WorkTotals{
		Sessions: sessions, Turns: turns, Retries: retries}}
}

// The report fires on a real switch and reads the same measurement either side
// of it, which is the only comparison one member can honestly make.
func TestDetectModelChange(t *testing.T) {
	days := []WorkDay{
		dayRow("2026-08-01", "anthropic/claude-sonnet-4-6", 2, 20, 6),
		dayRow("2026-08-02", "anthropic/claude-sonnet-4-6", 2, 18, 5),
		dayRow("2026-08-03", "anthropic/claude-sonnet-4-6", 1, 12, 4),
		dayRow("2026-08-05", "anthropic/claude-sonnet-5", 2, 10, 1),
		dayRow("2026-08-06", "anthropic/claude-sonnet-5", 2, 12, 2),
		dayRow("2026-08-07", "anthropic/claude-sonnet-5", 1, 8, 0),
	}
	ch := detectModelChange(days)
	if ch == nil {
		t.Fatal("a switch with three days either side was not reported")
	}
	if ch.From != "anthropic/claude-sonnet-4-6" || ch.To != "anthropic/claude-sonnet-5" {
		t.Fatalf("wrong pair: %+v", ch)
	}
	if ch.At != "2026-08-05" {
		t.Fatalf("At = %q, want the first day the new model led", ch.At)
	}
	if ch.Before.Turns != 50 || ch.After.Turns != 30 {
		t.Fatalf("turns = %d before / %d after, want 50 and 30", ch.Before.Turns, ch.After.Turns)
	}
	if ch.Before.Retries != 15 || ch.After.Retries != 3 {
		t.Fatalf("retries = %d before / %d after, want 15 and 3", ch.Before.Retries, ch.After.Retries)
	}
}

// An afternoon spent trying something is not a switch. A report that fired on
// the first session under a new label would cry wolf every time somebody
// experimented, and a member who learns to ignore it has lost the feature.
func TestDetectModelChangeIgnoresAnExperiment(t *testing.T) {
	days := []WorkDay{
		dayRow("2026-08-01", "anthropic/claude-sonnet-5", 2, 20, 3),
		dayRow("2026-08-02", "anthropic/claude-sonnet-5", 2, 18, 2),
		dayRow("2026-08-03", "anthropic/claude-sonnet-5", 2, 16, 2),
		dayRow("2026-08-04", "openai/gpt-4o", 1, 4, 1), // one day of trying it
		dayRow("2026-08-05", "anthropic/claude-sonnet-5", 2, 15, 1),
	}
	if ch := detectModelChange(days); ch != nil {
		t.Fatalf("a one-day experiment was reported as a switch: %+v", ch)
	}
}

// A day where two models both ran belongs to whichever did the most work. A
// member who ran a second model for two turns has not changed anything.
func TestDetectModelChangeUsesTheDaysLeader(t *testing.T) {
	days := []WorkDay{
		dayRow("2026-08-01", "anthropic/claude-sonnet-4-6", 2, 20, 0),
		dayRow("2026-08-01", "openai/gpt-4o", 1, 2, 0),
		dayRow("2026-08-02", "anthropic/claude-sonnet-4-6", 2, 18, 0),
		dayRow("2026-08-03", "anthropic/claude-sonnet-4-6", 2, 18, 0),
		dayRow("2026-08-04", "openai/gpt-4o", 2, 22, 0),
		dayRow("2026-08-04", "anthropic/claude-sonnet-4-6", 1, 3, 0),
		dayRow("2026-08-05", "openai/gpt-4o", 2, 20, 0),
		dayRow("2026-08-06", "openai/gpt-4o", 2, 20, 0),
	}
	ch := detectModelChange(days)
	if ch == nil || ch.At != "2026-08-04" || ch.To != "openai/gpt-4o" {
		t.Fatalf("the day's leader was not used: %+v", ch)
	}
}

// One model all along is not a change, and neither is an empty history.
func TestDetectModelChangeStaysQuiet(t *testing.T) {
	one := []WorkDay{
		dayRow("2026-08-01", "anthropic/claude-sonnet-5", 2, 20, 0),
		dayRow("2026-08-02", "anthropic/claude-sonnet-5", 2, 20, 0),
		dayRow("2026-08-03", "anthropic/claude-sonnet-5", 2, 20, 0),
		dayRow("2026-08-04", "anthropic/claude-sonnet-5", 2, 20, 0),
	}
	if ch := detectModelChange(one); ch != nil {
		t.Fatalf("one model reported as a change: %+v", ch)
	}
	if ch := detectModelChange(nil); ch != nil {
		t.Fatalf("an empty history reported a change: %+v", ch)
	}
}

// Where the turns went. Buckets rather than timings, because a list of
// durations is a record of when a member was at their desk and this file does
// not keep one — so the test asserts the bucketing, which is the part that
// decides whether the count means anything.
func TestTurnsAreCountedByDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "under 30s"},
		{29 * time.Second, "under 30s"},
		{30 * time.Second, "30s–2m"},
		{90 * time.Second, "30s–2m"},
		{5 * time.Minute, "2–10m"},
		{2 * time.Hour, "over 10m"},
	} {
		if got := turnBucket(tc.d); got != tc.want {
			t.Errorf("turnBucket(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// A turn is the span from the member's prompt to the agent stopping. The gap
// AFTER a turn closes is the member reading, or at lunch, and counting it would
// turn every overnight session into one twelve-hour turn.
func TestIdleBetweenTurnsIsNotATurn(t *testing.T) {
	var s sessionStats
	at := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	s.observe(at, capture.EvUserPrompt, nil)
	s.observe(at.Add(20*time.Second), capture.EvStop, map[string]any{})
	// Twelve hours pass with no prompt, then the session ends again.
	s.observe(at.Add(12*time.Hour), capture.EvStop, map[string]any{})
	if got := s.turnBuckets["under 30s"]; got != 1 {
		t.Errorf("the 20-second turn was counted %d times, want 1", got)
	}
	if got := s.turnBuckets["over 10m"]; got != 0 {
		t.Errorf("the idle gap was counted as a turn (%d in 'over 10m')", got)
	}
}

// The tools a judge said were missed are the member's own pattern; the registry
// only ever learns this org-wide. Names counted, nothing else — the same
// standard the tool counts hold.
func TestAbsentToolsAreCountedByName(t *testing.T) {
	var s sessionStats
	s.noteAbsent([]string{"Grep", "Bash", ""})
	s.noteAbsent([]string{"Grep"})
	if s.absentTools["Grep"] != 2 || s.absentTools["Bash"] != 1 {
		t.Errorf("absent tools = %v, want Grep:2 Bash:1", s.absentTools)
	}
	if _, ok := s.absentTools[""]; ok {
		t.Error("an empty tool name was counted")
	}
}

// The tools view exists for the cross-tabs: the registry sees tools org-wide and
// identity-free, so which tool a member reaches for under which harness, model
// and kind of task is answerable only here.
func TestToolsAreCrossTabbedAndPaired(t *testing.T) {
	detail, daily, _ := sessionPaths(t)
	l := loadSessionLog(detail, daily, nil)
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.upsert(sessionRecord{Key: "a", Start: at, End: at, Harness: "claude-code",
		Model: "anthropic/claude-opus-5", Project: "tacit", TaskType: "debug",
		Tools: map[string]int{"Read": 10, "Bash": 4, "mcp__tacit__tacit_search": 1}})
	l.upsert(sessionRecord{Key: "b", Start: at, End: at, Harness: "codex",
		Model: "openai/gpt-5.2", Project: "site", TaskType: "docs",
		Tools: map[string]int{"Read": 6, "Bash": 2}})

	sum := l.summarizeWork(0)
	byName := map[string]ToolUse{}
	for _, tl := range sum.Tools {
		byName[tl.Name] = tl
	}
	read := byName["Read"]
	if read.Calls != 16 || read.Sessions != 2 {
		t.Errorf("Read = %d calls in %d sessions, want 16 in 2", read.Calls, read.Sessions)
	}
	if read.Harness["claude-code"] != 10 || read.Harness["codex"] != 6 {
		t.Errorf("Read by harness = %v", read.Harness)
	}
	if read.Model["openai/gpt-5.2"] != 6 || read.Task["debug"] != 10 || read.Project["site"] != 6 {
		t.Errorf("Read cross-tabs = model %v task %v project %v", read.Model, read.Task, read.Project)
	}
	if byName["mcp__tacit__tacit_search"].Kind != "mcp" {
		t.Errorf("an MCP tool was classed %q", byName["mcp__tacit__tacit_search"].Kind)
	}

	// Read and Bash appeared together twice; the MCP tool only once, and a pair
	// seen once is a coincidence rather than a habit.
	var readBash, withMCP int
	for _, p := range sum.ToolPairs {
		if p.A == "Bash" && p.B == "Read" {
			readBash = p.Sessions
		}
		if p.A == "mcp__tacit__tacit_search" || p.B == "mcp__tacit__tacit_search" {
			withMCP++
		}
	}
	if readBash != 2 {
		t.Errorf("Bash+Read seen in %d sessions, want 2", readBash)
	}
	if withMCP != 0 {
		t.Errorf("%d pairs kept from a single session; one session is a coincidence", withMCP)
	}
}

// The Models view exists for the same reason the tools one does: the registry
// counts model cohorts across the organization and can never say which models
// one member ran, under which client, on which projects, or how long their
// turns took. So the model grouping carries its own cross-tabs, counted in
// sessions, and the labels the harness actually used beside the cohort they
// folded into.
func TestModelsCarryTheirOwnCrossTabs(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	l := loadSessionLog(detail, daily, nil)
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l.upsert(sessionRecord{Key: "a", Start: at, End: at, Harness: "claude-code",
		Model: "anthropic/claude-opus-5", ModelRaw: "claude-opus-5-20260101",
		Project: "tacit", TaskType: "debug", Turns: 6, PeakContext: 90000,
		TurnBuckets: map[string]int{"under 30s": 4, "30s–2m": 2}})
	l.upsert(sessionRecord{Key: "b", Start: at, End: at, Harness: "codex",
		Model: "anthropic/claude-opus-5", ModelRaw: "anthropic/claude-opus-5",
		Project: "site", TaskType: "docs", Turns: 4, PeakContext: 40000,
		TurnBuckets: map[string]int{"under 30s": 4}})
	l.upsert(sessionRecord{Key: "c", Start: at, End: at, Harness: "claude-code",
		Model: "openai/gpt-5.2", ModelRaw: "gpt-5.2", Project: "tacit",
		TaskType: "debug", Turns: 5})

	sum := l.summarizeWork(0)
	byKey := map[string]ModelUse{}
	for _, m := range sum.Models {
		byKey[m.Key] = m
	}
	opus := byKey["anthropic/claude-opus-5"]
	if opus.Sessions != 2 || opus.Turns != 10 {
		t.Errorf("opus = %d sessions, %d turns; want 2 and 10", opus.Sessions, opus.Turns)
	}
	if opus.Harness["claude-code"] != 1 || opus.Harness["codex"] != 1 {
		t.Errorf("by client = %v, want one session each", opus.Harness)
	}
	if opus.Task["debug"] != 1 || opus.Project["tacit"] != 1 || opus.Project["site"] != 1 {
		t.Errorf("cross-tabs = task %v project %v", opus.Task, opus.Project)
	}
	if opus.TurnTimes["under 30s"] != 8 || opus.TurnTimes["30s–2m"] != 2 {
		t.Errorf("turn times = %v, want turns rather than sessions", opus.TurnTimes)
	}
	// Two raw labels, one cohort — which is the whole reason modelid exists,
	// and the only place a member can see what was folded.
	if len(opus.Variants) != 2 || opus.Variants["claude-opus-5-20260101"] != 1 {
		t.Errorf("variants = %v, want both labels", opus.Variants)
	}
	// A peak is a level: the model's is the fullest any one session got, never
	// the sum of two.
	if opus.PeakContext != 90000 {
		t.Errorf("peak context = %d, want the larger of the two", opus.PeakContext)
	}
	// A model that reported none keeps none, rather than an empty object the
	// page has to test twice.
	if byKey["openai/gpt-5.2"].TurnTimes != nil {
		t.Errorf("an unmeasured distribution arrived as %v", byKey["openai/gpt-5.2"].TurnTimes)
	}
}

// Past the detail horizon a session is a day rollup, which keeps the cohort and
// the client and loses the exact label. The cross-tabs have to survive that
// fold, or a longer window reads as a quieter one.
func TestModelCrossTabsSurviveCompaction(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	now := at.Add(sessionDetailRetention + 48*time.Hour)
	l := loadSessionLog(detail, daily, fixedClock(at))
	l.upsert(sessionRecord{Key: "old", Start: at, End: at, Harness: "codex",
		Model: "openai/gpt-5.2", ModelRaw: "gpt-5.2", Project: "site",
		TaskType: "docs", Turns: 3, PeakContext: 12000,
		TurnBuckets: map[string]int{"under 30s": 3}})

	// A later process finds it past the detail horizon and folds it into its day.
	sum := loadSessionLog(detail, daily, fixedClock(now)).summarizeWork(0)
	if len(sum.Models) != 1 {
		t.Fatalf("models after compaction = %d", len(sum.Models))
	}
	m := sum.Models[0]
	if m.Harness["codex"] != 1 || m.Project["site"] != 1 || m.Task["docs"] != 1 {
		t.Errorf("cross-tabs lost in the fold: harness %v project %v task %v", m.Harness, m.Project, m.Task)
	}
	if m.TurnTimes["under 30s"] != 3 || m.PeakContext != 12000 {
		t.Errorf("turn times %v, peak %d", m.TurnTimes, m.PeakContext)
	}
	// The label is the one thing the fold drops, so the view reads variants
	// against DetailFrom rather than pretending the window has them all.
	if len(m.Variants) != 0 {
		t.Errorf("a compacted day kept a raw label: %v", m.Variants)
	}
}

// Every tool that CAN open, opens — and the row says which those are. The
// second level used to exist for shell tools and file tools alone, so on a
// machine that had run neither, Bash was the only row on the tools page that
// opened and nothing said why. The vocabulary a tool keeps now rides on the
// row, recorded through the real hook path.
func TestToolsCarryTheVocabularyBehindThem(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l := loadSessionLog(detail, daily, fixedClock(now))
	var s sessionStats
	call := func(name string, input map[string]any) {
		s.observe(now, capture.EvPostTool, map[string]any{"tool_name": name, "tool_input": any(input)})
	}
	call("Bash", map[string]any{"command": "go test ./..."})
	call("Grep", map[string]any{"pattern": "func main", "glob": "**/*.go"})
	call("Grep", map[string]any{"pattern": "func main"}) // named no type: counted, not detailed
	call("Task", map[string]any{"subagent_type": "Explore", "prompt": "find it"})
	call("Skill", map[string]any{"skill": "tacit:review"})
	call("WebFetch", map[string]any{"url": "https://example.com/x"})
	l.upsert(s.record("k", "claude-code", "anthropic/claude-opus-5", "/home/x/tacit", 0))

	byName := map[string]ToolUse{}
	for _, tl := range l.summarizeWork(0).Tools {
		byName[tl.Name] = tl
	}
	for tool, kind := range map[string]string{
		"Bash": capture.DetailProgram, "Grep": capture.DetailFileType,
		"Task": capture.DetailAgent, "Skill": capture.DetailSkill, "WebFetch": "",
	} {
		if got := byName[tool].DetailKind; got != kind {
			t.Errorf("%s keeps %q, want %q", tool, got, kind)
		}
	}
	if got := byName["Task"].Detail; len(got) != 1 || got[0].Key != "Explore" {
		t.Errorf("Task detail = %v, want the agent it handed to", got)
	}
	if got := byName["Skill"].Detail; len(got) != 1 || got[0].Key != "tacit:review" {
		t.Errorf("Skill detail = %v, want the skill it ran", got)
	}
	// The search that named no file type is counted as a call and detailed as
	// nothing, so the view can say what its second level does not speak for.
	g := byName["Grep"]
	if g.Calls != 2 || len(g.Detail) != 1 || g.Detail[0].Count != 1 {
		t.Errorf("Grep = %d calls with detail %v; want 2 calls and one type", g.Calls, g.Detail)
	}
	if len(byName["WebFetch"].Detail) != 0 {
		t.Errorf("WebFetch kept %v; its input is the member's own text", byName["WebFetch"].Detail)
	}
}

// A session outlives the agent. It idle-exits after fifteen quiet minutes and
// the member carries on working, so the next hook reaches a process that has
// never heard of a session already hours old. That process used to count from
// zero and then REPLACE the fuller record, so a session with a coffee break in
// it kept only what happened after the break — on one real log, four sessions
// holding 57, 3, 7 and 7 tool calls whose transcripts held 437, 145, 595 and
// 190.
func TestSessionSurvivesAnAgentRestart(t *testing.T) {
	dir := t.TempDir()
	opts := func() Options {
		return Options{StateDir: dir, SessionSalt: "org-salt", RunAsync: inline,
			UsageLogPath: filepath.Join(dir, "usage.jsonl"),
			Segment:      contracts.Segment{"team": "revops"}}
	}
	quiet := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, nil
	}
	bash := func(cmd string) map[string]any {
		return ev("PostToolUse", map[string]any{"tool_name": "Bash",
			"tool_input": map[string]any{"command": cmd}})
	}

	first := NewAgent(quiet, stubLLM{}, nil, nil, opts())
	first.Handle(ev("SessionStart", map[string]any{"source": "startup"}), "claude-code")
	first.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "run the tests",
		"model": "claude-opus-5", "cwd": "/home/x/tacit"}), "claude-code")
	for i := 0; i < 5; i++ {
		first.Handle(bash("go test ./..."), "claude-code")
	}
	first.Handle(ev("Stop", nil), "claude-code")

	// Fifteen quiet minutes later: a new process, an empty head, the same
	// session. Its payloads carry no model and no cwd, because the events that
	// named them have already happened.
	second := NewAgent(quiet, stubLLM{}, nil, nil, opts())
	second.Handle(bash("git commit -m x"), "claude-code")
	second.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "now commit it"}), "claude-code")
	second.Handle(ev("Stop", nil), "claude-code")

	detail, daily := sessionLogPaths(dir)
	sum := loadSessionLog(detail, daily, nil).summarizeWork(0)
	if sum.Totals.Sessions != 1 {
		t.Fatalf("sessions = %d, want one session recorded twice rather than two", sum.Totals.Sessions)
	}
	if sum.Totals.Turns != 2 {
		t.Errorf("turns = %d, want both", sum.Totals.Turns)
	}
	if sum.Totals.ToolCalls != 6 {
		t.Errorf("tool calls = %d, want all six", sum.Totals.ToolCalls)
	}
	// The second level survives with them, and so does the label the restarted
	// instance never saw: a resumed session is not unattributed work in no
	// project.
	byName := map[string]ToolUse{}
	for _, tl := range sum.Tools {
		byName[tl.Name] = tl
	}
	keys := map[string]int{}
	for _, d := range byName["Bash"].Detail {
		keys[d.Key] = d.Count
	}
	if keys["go test"] != 5 || keys["git commit"] != 1 {
		t.Errorf("bash detail = %v, want both instances' programs", keys)
	}
	if len(sum.Models) != 1 || sum.Models[0].Key != "anthropic/claude-opus-5" {
		t.Errorf("models = %+v, want the one the first instance saw", sum.Models)
	}
	if len(sum.Projects) != 1 || sum.Projects[0].Key != "tacit" {
		t.Errorf("projects = %+v, want the one the first instance saw", sum.Projects)
	}
}

// The floor under the resume, for the paths where one cannot happen: a record
// that arrives smaller than the one already stored raises to it rather than
// replacing it. A session's counts only ever grow.
func TestUpsertNeverLosesCounts(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	l := loadSessionLog(detail, daily, fixedClock(now))
	full := sessionRecord{Key: "k", Start: now.Add(-2 * time.Hour), End: now,
		Harness: "claude-code", Model: "anthropic/claude-opus-5", Project: "tacit",
		Turns: 40, Retries: 9, Corrections: 4, PeakContext: 150000,
		Tools:       map[string]int{"Bash": 400, "Read": 30},
		TurnBuckets: map[string]int{"under 30s": 30},
		ToolDetail:  map[string]map[string]int{"Bash": {"go test": 100}},
	}
	l.upsert(full)
	// What a forgetful instance would write.
	l.upsert(sessionRecord{Key: "k", Start: now, End: now.Add(time.Minute),
		Harness: "claude-code", Turns: 2, Tools: map[string]int{"Bash": 3},
		ToolDetail: map[string]map[string]int{"Bash": {"go test": 2}}})

	got, ok := l.lookup("k")
	if !ok {
		t.Fatal("the record is gone")
	}
	if got.Turns != 40 || got.Tools["Bash"] != 400 || got.Tools["Read"] != 30 {
		t.Errorf("counts went backwards: turns %d, tools %v", got.Turns, got.Tools)
	}
	if got.Retries != 9 || got.Corrections != 4 || got.PeakContext != 150000 {
		t.Errorf("a counter was lost: %+v", got)
	}
	if got.ToolDetail["Bash"]["go test"] != 100 || got.TurnBuckets["under 30s"] != 30 {
		t.Errorf("a vocabulary was lost: %v %v", got.ToolDetail, got.TurnBuckets)
	}
	if got.Model != "anthropic/claude-opus-5" || got.Project != "tacit" {
		t.Errorf("a label the newer record did not carry was blanked: %+v", got)
	}
	// The span covers both: the earlier start and the later end.
	if !got.Start.Equal(full.Start) || !got.End.Equal(now.Add(time.Minute)) {
		t.Errorf("span = %s..%s", got.Start, got.End)
	}
}

// The invariant holds on the way IN as well: a log written before the resume
// existed can hold a partial line appended after a full one, and reading it
// back must not reinstate the loss it was written under.
func TestLoadRaisesAPartialLineToTheFullerOne(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	full := `{"key":"k","start":"2026-09-12T10:00:00Z","end":"2026-09-12T11:00:00Z","harness":"claude-code",` +
		`"model":"anthropic/claude-opus-5","project":"tacit","turns":9,"tools":{"Bash":437},` +
		`"tool_detail":{"Bash":{"go test":80}}}`
	partial := `{"key":"k","start":"2026-09-12T11:30:00Z","end":"2026-09-12T11:35:00Z","harness":"claude-code",` +
		`"turns":1,"tools":{"Bash":7}}`
	if err := os.WriteFile(detail, []byte(full+"\n"+partial+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := loadSessionLog(detail, daily, fixedClock(now)).lookup("k")
	if !ok {
		t.Fatal("the record is gone")
	}
	if got.Turns != 9 || got.Tools["Bash"] != 437 {
		t.Errorf("the partial line won: turns %d, bash %d", got.Turns, got.Tools["Bash"])
	}
	if got.ToolDetail["Bash"]["go test"] != 80 {
		t.Errorf("the second level was lost: %v", got.ToolDetail)
	}
	if got.Model != "anthropic/claude-opus-5" || got.Project != "tacit" {
		t.Errorf("labels were blanked by the partial line: %+v", got)
	}
}

// --- check evidence ---

// checked builds a record that changed work and left a check in one state.
func checked(key string, end time.Time, model, harness, state string) sessionRecord {
	r := rec(key, end, model, "tacit", 6)
	r.Harness = harness
	r.ChangeObserved = true
	r.ChecksAttempted = 1
	r.CheckState = state
	if state == checkPassed {
		r.ChecksPassed = 1
	}
	r.OutTokens = 1000
	r.CostUSD = 0.50
	r.ActiveSeconds = 300
	r.LinesAdded = 40
	r.Status = true
	return r
}

// A session that edited nothing is not a session that went unchecked. It is
// outside the question, and putting it in the denominator would turn a month of
// reading and searching into a month of unverified work.
func TestCheckCoverageCountsOnlyChangedSessions(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s := loadSessionLog(detail, daily, fixedClock(now))
	s.upsert(checked("a", now, "anthropic/claude-opus-5", "claude-code", checkPassed))
	s.upsert(checked("b", now, "anthropic/claude-opus-5", "claude-code", checkFailed))
	// Changed, and nothing recognised as a check ran.
	unchecked := checked("c", now, "anthropic/claude-opus-5", "claude-code", "")
	unchecked.ChecksAttempted = 0
	s.upsert(unchecked)
	// Read-only work, and a record written before any of this was measured.
	s.upsert(rec("d", now, "anthropic/claude-opus-5", "tacit", 3))

	sum := s.summarizeWork(0)
	if len(sum.ModelClients) != 1 {
		t.Fatalf("rows = %+v, want one", sum.ModelClients)
	}
	got := sum.ModelClients[0]
	if got.Changed != 3 {
		t.Errorf("changed = %d, want 3 — the read-only session is not in the denominator", got.Changed)
	}
	if got.Checked() != 2 {
		t.Errorf("checked = %d, want 2", got.Checked())
	}
	if got.Passed != 1 || got.Failed != 1 {
		t.Errorf("states = %d passed / %d failed, want 1 and 1", got.Passed, got.Failed)
	}
	if sum.Totals.Sessions != 4 {
		t.Errorf("sessions = %d, want 4 — the window's own total is unchanged", sum.Totals.Sessions)
	}
}

// A window with nothing observed to change reports no rows at all, so the view
// can say "no check evidence recorded yet" rather than print a 0%.
func TestNoChangedSessionsReportsNoRows(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s := loadSessionLog(detail, daily, fixedClock(now))
	s.upsert(rec("a", now, "anthropic/claude-opus-5", "tacit", 4))
	if rows := s.summarizeWork(0).ModelClients; rows != nil {
		t.Fatalf("rows = %+v, want none — missing capture must read as missing", rows)
	}
}

// The model is half the comparison and the client is the other half. One model
// under two clients is two arrangements, and folding them would credit or blame
// the wrong one.
func TestOutcomeRowsKeepModelAndClientApart(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s := loadSessionLog(detail, daily, fixedClock(now))
	s.upsert(checked("a", now, "anthropic/claude-opus-5", "claude-code", checkPassed))
	s.upsert(checked("b", now, "anthropic/claude-opus-5", "codex", checkStale))

	rows := s.summarizeWork(0).ModelClients
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two", rows)
	}
	seen := map[string]string{}
	for _, r := range rows {
		if r.Model == "" || r.Client == "" {
			t.Errorf("row names one half only: %+v", r)
		}
		seen[r.Client] = r.Model
	}
	if seen["claude-code"] == "" || seen["codex"] == "" {
		t.Errorf("clients = %+v, want both", seen)
	}
}

// The effort beside a pass rate has to be the effort of exactly those sessions.
// A rate from one set and a cost from another is the arithmetic that makes a
// careless model look careful.
func TestOutcomeEffortComesFromTheSameSessions(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s := loadSessionLog(detail, daily, fixedClock(now))
	s.upsert(checked("a", now, "anthropic/claude-opus-5", "claude-code", checkPassed))
	// Read-only, dear, and outside the question.
	costly := rec("b", now, "anthropic/claude-opus-5", "tacit", 40)
	costly.CostUSD = 90
	costly.OutTokens = 500000
	s.upsert(costly)

	rows := s.summarizeWork(0).ModelClients
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	r := rows[0]
	if r.CostUSD != 0.50 || r.OutTokens != 1000 {
		t.Errorf("effort = $%.2f / %d tokens, want $0.50 and 1000 — the unchanged session's bill is not in it",
			r.CostUSD, r.OutTokens)
	}
	if r.Turns != 6 || r.ToolCalls != 3 {
		t.Errorf("effort = %d turns / %d calls, want 6 and 3", r.Turns, r.ToolCalls)
	}
	if r.StatusSessions != 1 || r.LinesAdded != 40 {
		t.Errorf("status figures = %d sessions / %d lines, want 1 and 40", r.StatusSessions, r.LinesAdded)
	}
}

// A day past the detail horizon must give the same rates as the sessions it was
// made of, or a member's history changes shape on the thirtieth day.
func TestCompactedDayKeepsTheSameRates(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	old := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	s := loadSessionLog(detail, daily, fixedClock(old))
	s.upsert(checked("a", old, "anthropic/claude-opus-5", "claude-code", checkPassed))
	s.upsert(checked("b", old, "anthropic/claude-opus-5", "claude-code", checkStale))
	s.upsert(checked("c", old, "anthropic/claude-opus-5", "claude-code", checkUnknown))
	before := s.summarizeWork(0).ModelClients

	now := old.Add(sessionDetailRetention + 48*time.Hour)
	after := loadSessionLog(detail, daily, fixedClock(now)).summarizeWork(0).ModelClients
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("rows = %+v then %+v, want one each", before, after)
	}
	b, a := before[0], after[0]
	if b.Changed != a.Changed || b.Passed != a.Passed || b.Stale != a.Stale ||
		b.Unknown != a.Unknown || b.Attempted != a.Attempted {
		t.Errorf("compaction moved the counts: %+v -> %+v", b.CheckOutcome, a.CheckOutcome)
	}
	if b.CostUSD != a.CostUSD || b.OutTokens != a.OutTokens || b.Turns != a.Turns ||
		b.ToolCalls != a.ToolCalls || b.ActiveSeconds != a.ActiveSeconds {
		t.Errorf("compaction moved the effort: %+v -> %+v", b.CheckOutcome, a.CheckOutcome)
	}
	if a.Model == "" || a.Client == "" {
		t.Errorf("the compacted row lost half the pair: %+v", a)
	}
}

// A resumed session is one session. The daemon idle-exits every fifteen quiet
// minutes, so a session with a coffee break in it must not become two — and its
// check evidence must survive the restart, or an edit after the break would
// have no pass left to make stale.
func TestCheckEvidenceSurvivesAResume(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s := loadSessionLog(detail, daily, fixedClock(now))
	first := checked("a", now, "anthropic/claude-opus-5", "claude-code", checkPassed)
	s.upsert(first)

	// A fresh instance joins the session and sees one more check, then an edit.
	prior, ok := s.lookup("a")
	if !ok {
		t.Fatal("the log lost the session it just wrote")
	}
	var stats sessionStats
	stats.resume(prior)
	if stats.checkState != checkPassed || !stats.changeObserved {
		t.Fatalf("resume did not carry the evidence: state %q, changed %v",
			stats.checkState, stats.changeObserved)
	}
	stats.observe(now, capture.EvUserPrompt, map[string]any{})
	stats.observe(now, capture.EvPostTool, map[string]any{
		"tool_name": "Edit", "tool_input": map[string]any{"file_path": "x.go"}})
	s.upsert(stats.record("a", "claude-code", "anthropic/claude-opus-5", "/w/tacit", 0))

	sum := s.summarizeWork(0)
	if sum.Totals.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1 — a resume is not a second session", sum.Totals.Sessions)
	}
	rows := sum.ModelClients
	if len(rows) != 1 || rows[0].Changed != 1 {
		t.Fatalf("rows = %+v, want one changed session", rows)
	}
	if rows[0].Stale != 1 || rows[0].Passed != 0 {
		t.Errorf("states = %+v, want the pass made stale by the edit after the restart", rows[0].CheckOutcome)
	}
	if rows[0].Attempted != 1 {
		t.Errorf("attempted = %d, want 1 — the resumed check is not counted twice", rows[0].Attempted)
	}
}

// Two sessions are two sessions, whatever their keys look like.
func TestTwoSessionsAreNotAResume(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	s := loadSessionLog(detail, daily, fixedClock(now))
	s.upsert(checked("a", now, "anthropic/claude-opus-5", "claude-code", checkPassed))
	s.upsert(checked("b", now, "anthropic/claude-opus-5", "claude-code", checkPassed))
	rows := s.summarizeWork(0).ModelClients
	if len(rows) != 1 || rows[0].Changed != 2 || rows[0].Passed != 2 {
		t.Fatalf("rows = %+v, want one row of two changed and two passed", rows)
	}
}

// A fuller record must never be replaced by a thinner one — the floor under
// resume, for the paths where a resume could not happen. Counts rise; the state
// is the later reading and wins where the newer line has one.
func TestKeepLargerHoldsCheckEvidence(t *testing.T) {
	prior := checked("a", time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		"anthropic/claude-opus-5", "claude-code", checkPassed)
	prior.ChecksAttempted = 4
	prior.ChecksPassed = 3

	// A forgetful instance writes a partial line.
	thin := rec("a", prior.End, "anthropic/claude-opus-5", "tacit", 2)
	keepLarger(&thin, &prior)
	if !thin.ChangeObserved || thin.ChecksAttempted != 4 || thin.ChecksPassed != 3 {
		t.Errorf("the thin line lost the evidence: %+v", thin)
	}
	if thin.CheckState != checkPassed {
		t.Errorf("state = %q, want %q inherited", thin.CheckState, checkPassed)
	}

	// And a later line that DOES state a state keeps its own.
	later := rec("a", prior.End, "anthropic/claude-opus-5", "tacit", 2)
	later.ChangeObserved = true
	later.CheckState = checkFailed
	keepLarger(&later, &prior)
	if later.CheckState != checkFailed {
		t.Errorf("state = %q, want %q — the newer line is the later reading", later.CheckState, checkFailed)
	}
}

// The detector reads the command and the result while the event is being
// handled and keeps neither. The whole local record rests on this line, so it
// is asserted against the bytes.
func TestCheckEvidenceKeepsNoCommandOrResult(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	var stats sessionStats
	stats.observe(now, capture.EvUserPrompt, map[string]any{})
	stats.observe(now, capture.EvPostTool, map[string]any{
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": "go test ./SECRETPKG/..."},
		"tool_response": "ok  	SECRETPKG	0.2s",
	})
	r := stats.record("k", "claude-code", "anthropic/claude-opus-5", "/w/tacit", 0)
	if r.ChecksAttempted != 1 || r.CheckState != checkPassed {
		t.Fatalf("the check was not read: %+v", r)
	}
	blob, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	// The program and its subcommand are a vocabulary the log has always kept
	// (ToolDetail). The path it ran against and the output it printed are not.
	if strings.Contains(string(blob), "SECRETPKG") {
		t.Fatalf("the record kept what it read: %s", blob)
	}
}

// An edit tool is a change observed; a failing check is a failed check. Both
// read the live event shapes rather than a hand-built record.
func TestObserveReadsChangeAndCheckOffEvents(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	var stats sessionStats
	stats.observe(now, capture.EvUserPrompt, map[string]any{})
	stats.observe(now, capture.EvPostTool, map[string]any{
		"tool_name": "Write", "tool_input": map[string]any{"file_path": "a.go"}})
	if !stats.changeObserved {
		t.Error("an edit tool did not register as a change")
	}
	// A failing shell command reaches the failure event and nothing else.
	stats.observe(now, capture.EvPostToolFail, map[string]any{
		"tool_name": "Bash", "tool_input": map[string]any{"command": "make test"},
		"tool_response": "make: *** [test] Error 1"})
	if stats.checkState != checkFailed || stats.checksAttempted != 1 {
		t.Errorf("failed check = %q / %d attempted, want %q and 1",
			stats.checkState, stats.checksAttempted, checkFailed)
	}
	if stats.checksPassed != 0 {
		t.Errorf("passed = %d, want 0", stats.checksPassed)
	}
}

// A panel row against its source, end to end: drive real hook events in a known
// order through the accumulator, write the records, and check the row the view
// reads is the arithmetic of those sessions and nothing else.
//
// This is the comparison the plan asks for before the measure ships. Every
// figure below can be traced to an event above it.
func TestOutcomeRowMatchesItsSourceSessions(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	log := loadSessionLog(detail, daily, fixedClock(now))

	// Session one: edit, then the tests pass. A changed session that passed.
	one := &sessionStats{}
	one.observe(now, capture.EvUserPrompt, map[string]any{})
	one.observe(now, capture.EvPostTool, map[string]any{
		"tool_name": "Edit", "tool_input": map[string]any{"file_path": "a.go"}})
	one.observe(now.Add(time.Minute), capture.EvPostTool, map[string]any{
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": "go test ./..."},
		"tool_response": "ok  \tpkg\t0.3s"})
	log.upsert(one.record("one", "claude-code", "anthropic/claude-opus-5", "/w/tacit", 0))

	// Session two: the tests pass, and THEN an edit lands. Stale.
	two := &sessionStats{}
	two.observe(now, capture.EvUserPrompt, map[string]any{})
	two.observe(now, capture.EvPostTool, map[string]any{
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": "go test ./..."},
		"tool_response": "PASS"})
	two.observe(now.Add(time.Minute), capture.EvPostTool, map[string]any{
		"tool_name": "Write", "tool_input": map[string]any{"file_path": "b.go"}})
	log.upsert(two.record("two", "claude-code", "anthropic/claude-opus-5", "/w/tacit", 0))

	// Session three: reading only, and a passing check. Not a changed session,
	// so it is in neither the numerator nor the denominator.
	three := &sessionStats{}
	three.observe(now, capture.EvUserPrompt, map[string]any{})
	three.observe(now, capture.EvPostTool, map[string]any{
		"tool_name": "Read", "tool_input": map[string]any{"file_path": "c.go"}})
	three.observe(now, capture.EvPostTool, map[string]any{
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": "make test"},
		"tool_response": "ok"})
	log.upsert(three.record("three", "claude-code", "anthropic/claude-opus-5", "/w/tacit", 0))

	// Session four: a different client, an edit, and a build that printed
	// nothing. Changed, checked, and proving neither way.
	four := &sessionStats{}
	four.observe(now, capture.EvUserPrompt, map[string]any{})
	four.observe(now, capture.EvPostTool, map[string]any{
		"tool_name": "apply_patch", "tool_input": map[string]any{"file_path": "d.rs"}})
	four.observe(now, capture.EvPostTool, map[string]any{
		"tool_name": "shell", "tool_input": map[string]any{"command": "cargo build"}})
	log.upsert(four.record("four", "codex", "openai/gpt-5-codex", "/w/tacit", 0))

	rows := log.summarizeWork(0).ModelClients
	got := map[string]ModelClientUse{}
	for _, r := range rows {
		got[r.Model+" "+r.Client] = r
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two pairs", rows)
	}
	opus := got["anthropic/claude-opus-5 claude-code"]
	if opus.Changed != 2 {
		t.Errorf("opus changed = %d, want 2 — the read-only session is outside the question", opus.Changed)
	}
	if opus.Passed != 1 || opus.Stale != 1 || opus.Checked() != 2 {
		t.Errorf("opus states = %+v, want one passed and one stale", opus.CheckOutcome)
	}
	if opus.Attempted != 2 {
		t.Errorf("opus attempted = %d, want 2 — the read-only session's check is not counted either",
			opus.Attempted)
	}
	if opus.Turns != 2 || opus.ToolCalls != 4 {
		t.Errorf("opus effort = %d turns / %d calls, want 2 and 4", opus.Turns, opus.ToolCalls)
	}
	codex := got["openai/gpt-5-codex codex"]
	if codex.Changed != 1 || codex.Unknown != 1 || codex.Passed != 0 {
		t.Errorf("codex states = %+v, want one changed session proving neither way", codex.CheckOutcome)
	}
	// And the window's own session total is untouched by any of it.
	if n := log.summarizeWork(0).Totals.Sessions; n != 4 {
		t.Errorf("sessions = %d, want 4", n)
	}
}
