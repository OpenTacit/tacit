// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Member-local session record: the countable shape of THIS member's own
// sessions — how many turns, which tools, how often something had to be done
// twice, under which model, in which project — on the member's machine and
// nowhere else.
//
// Same reason as usagelog.go and techniquememory.go, one step further. The
// usage log answers "how am I using OpenTacit". This answers "how am I working",
// which is the question a member switching models actually has and which no
// registry can answer for them: "cohorts, never identities" means the org-wide
// funnel is the only thing the server may hold, and one person is not visible
// in an org-wide rate (docs/design/single-user-value.md).
//
// The line this file must not cross, restated because widening the record is
// exactly where it would be crossed by accident: no prompt text, no
// completions, no file contents, no transcripts, no identity. Everything here
// is derived and countable — counts, durations, tool names, a project
// basename, a model label. The session key is hashed, as it is everywhere
// else. 0600, pruned by age, and never uploaded: the loopback endpoint that
// serves it is gated, and nothing posts it anywhere.
//
// Two horizons, because detail and history want different things. Full records
// live for a month, which is long enough to answer "what happened last week".
// Past that they fold into one row per day per (harness, model, project, task
// type), which is what a season of history costs when the file must stay
// bounded. Folding is one-way and idempotent: a record leaves the detail map at
// the moment it joins a day.
package hooks

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/modelid"
	"github.com/opentacit/tacit/internal/pricing"
)

const (
	// sessionDetailRetention is how long a full session record survives before
	// it folds into its day.
	sessionDetailRetention = 30 * 24 * time.Hour
	// sessionDailyRetention bounds the compacted history. Longer than the usage
	// log's, because the model-change report reads across a model switch and
	// switches are months apart.
	sessionDailyRetention = 400 * 24 * time.Hour
	// sessionRewriteSlack is how many stale appended lines are tolerated before
	// the file is rewritten. A session is upserted once per Stop, so a long
	// session writes its key many times; without this the file would grow with
	// turns rather than with sessions.
	sessionRewriteSlack = 3
)

// sessionRecord is one session, as counts.
type sessionRecord struct {
	Key      string    `json:"key"` // hashed session key: a local join, never an identity
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Harness  string    `json:"harness,omitempty"`
	Model    string    `json:"model,omitempty"`     // canonical cohort (internal/modelid)
	ModelRaw string    `json:"model_raw,omitempty"` // what the harness actually said
	Project  string    `json:"project,omitempty"`   // repository basename, never a path
	TaskType string    `json:"task_type,omitempty"`
	Turns    int       `json:"turns"`
	// Tools counts calls by tool name. Names are an enumerable vocabulary the
	// session demonstrably used — the same standard ResourcesInPlay holds, and
	// the reason arguments are not here.
	Tools map[string]int `json:"tools,omitempty"`
	// Retries counts a tool called again with the same arguments it just ran
	// with. Structural, never a reading of the output: a heuristic that decided
	// what "failed" means from result text would be wrong often and silently.
	// Offered counts the calls a tool was ASKED to make, by name: every tool
	// call reaches PreToolUse, and only the ones that went ahead reach
	// PostToolUse. Tools above is what RAN and does not change — every rate on
	// the page is computed over it — so the difference between the two is the
	// rejection, and it is the one measure this market most often misreads as
	// productivity. It travels with its denominator or not at all.
	Offered map[string]int `json:"offered,omitempty"`
	// Failures counts the calls that failed, by the KIND of failure
	// (capture.ToolFailure) — never the message, which is the member's own
	// work. Absent on a harness that does not report failures at all, which is
	// a different thing from a harness that reported none.
	Failures map[string]int `json:"failures,omitempty"`
	// Hours counts this session's turns by the hour of the member's OWN clock,
	// "00" to "23". The dates everywhere else here are UTC, which is right for
	// a rollup and wrong for a person: somebody working at six in the evening
	// is not working at one in the morning, and an hour chart in UTC would tell
	// them they were. Counted at the turn, so no timezone is ever stored.
	Hours   map[string]int `json:"hours,omitempty"`
	Retries int            `json:"retries"`
	// Corrections counts turns the member opened by correcting the agent
	// (corrections.go). The shape of each one lives in the ledger; only the
	// count lives here.
	Corrections int `json:"corrections"`
	// Token and cost fields are filled only by a source that reports them — a
	// gateway, or a harness that puts usage on the payload. A source that says
	// nothing leaves them zero, and the read side omits a total nothing
	// measured rather than printing a confident 0.
	InTokens  int `json:"in_tokens,omitempty"`
	OutTokens int `json:"out_tokens,omitempty"`
	// CacheWriteTokens and CacheReadTokens split InTokens the way the bill
	// splits it: a cache read costs a fraction of fresh input and a cache write
	// costs a premium over it, so the same InTokens can be two very different
	// invoices. What is left after subtracting both is the part that missed the
	// cache entirely, which is why there is no third field.
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	// EstCostUSD is what the window's tokens come to at published rates
	// (internal/pricing), for the harnesses that report no money of their own.
	// It is kept APART from CostUSD and never added to it: an estimate and a
	// measurement are different claims, and a total that quietly mixed them
	// would be the most expensive kind of wrong number here — specific,
	// plausible, and impossible to attribute. Zero where the model has no
	// published rate, which is not a model that cost nothing.
	EstCostUSD float64 `json:"est_cost_usd,omitempty"`
	// PeakContext is the fullest the model's context got in this session. It is
	// a level, not a total: it is carried as a maximum through compaction and
	// through every rollup, and adding two of them together would mean nothing.
	PeakContext int     `json:"peak_context,omitempty"`
	CostUSD     float64 `json:"cost_usd,omitempty"`
	// TurnBuckets counts this session's turns by how long they took, and
	// AbsentTools counts the tools a judge said would have helped here and were
	// not used (discovery.go). Both are counted vocabularies, like Tools.
	TurnBuckets map[string]int `json:"turn_buckets,omitempty"`
	AbsentTools map[string]int `json:"absent_tools,omitempty"`
	// ToolDetail is what each tool was doing, one level finer than its name:
	// programs and their subcommands for a shell tool, kinds of file for a file
	// tool. Vocabularies only — the same standard Tools holds, and the reason no
	// command line and no path is here.
	ToolDetail map[string]map[string]int `json:"tool_detail,omitempty"`
	// Everything below comes from the harness status line
	// (capture.ReadStatusLine), which restates the whole session on every
	// render. They are LEVELS, kept as maxima within a session exactly as
	// PeakContext is, and summed only ACROSS sessions. Status says the status
	// line reported at all, which is what lets the page name the sources that
	// fed a window rather than letting an unwired machine read as a cheap one.
	Status        bool   `json:"status,omitempty"`
	LinesAdded    int    `json:"lines_added,omitempty"`
	LinesRemoved  int    `json:"lines_removed,omitempty"`
	ActiveSeconds int    `json:"active_seconds,omitempty"`
	ContextPct    int    `json:"context_pct,omitempty"`
	CacheRequests int    `json:"cache_requests,omitempty"`
	CacheMisses   int    `json:"cache_misses,omitempty"`
	Effort        string `json:"effort,omitempty"`
	// Check evidence: whether this session changed anything, and whether the
	// agent found out if the change held up (verification.go).
	//
	// These four are the nearest honest counterpart to a benchmark's
	// resolution rate, and the distance is the point. A benchmark owns the
	// task, runs it eight times in a sandbox and injects a verifier it trusts.
	// OpenTacit sees live work once. So there is no `resolved` here and never will
	// be: what a session can prove is that a recognised check ran and what its
	// output said, which is a smaller claim carried by a name that says so
	// (docs/design/real-work-usage-analysis-plan.md).
	//
	// ChangeObserved is the denominator everything else divides by: an edit
	// tool ran, or the status line reported lines added or removed. It does not
	// claim every shell-side change was seen — a session that only ever wrote
	// files through `sed` is not in it, and the panel says so.
	ChangeObserved bool `json:"change_observed,omitempty"`
	// ChecksAttempted counts recognised checks; ChecksPassed those whose output
	// carried an explicit pass marker and no fail marker. Counts, so they only
	// ever rise.
	ChecksAttempted int `json:"checks_attempted,omitempty"`
	ChecksPassed    int `json:"checks_passed,omitempty"`
	// CheckState is where the LATEST check left the session: passed, failed,
	// unknown, or stale — passed, and then something was edited. Absent when no
	// check was seen at all, which is a different fact from a check that proved
	// nothing, and the reason it is a string rather than a bool.
	CheckState string `json:"check_state,omitempty"`
}

// noteDetail records the second level for one tool call, where the tool has
// one. Which tools have one, and what the keys are made of, is capture's
// answer alone (ToolDetail): the log records what it is handed, and the view
// labels it from the same kind, so there is one rule about what may be kept
// rather than three that drift.
func (s *sessionStats) noteDetail(tool string, input any) {
	keys := capture.ToolDetail(tool, input)
	if len(keys) == 0 {
		return
	}
	if s.toolDetail == nil {
		s.toolDetail = map[string]map[string]int{}
	}
	if s.toolDetail[tool] == nil {
		s.toolDetail[tool] = map[string]int{}
	}
	for _, k := range keys {
		s.toolDetail[tool][k]++
	}
}

// addCounts folds src into dst, creating dst when it is the first contribution.
func addCounts(dst, src map[string]int) map[string]int {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]int{}
	}
	for k, v := range src {
		dst[k] += v
	}
	return dst
}

func copyCounts(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// CheckOutcome is check evidence over a set of sessions, with what those same
// sessions took.
//
// The two halves travel together on purpose. A pass rate read off one set of
// sessions beside a cost read off another is the arithmetic that makes a cheap
// model look careful and a careful one look dear, so both are counted over the
// sessions in Changed and nothing else. Every rate divides by Changed, and
// Changed is stated wherever a rate is shown.
//
// What it will not carry: a confidence band, a rank, or a verdict. The sessions
// here are self-selected and their tasks change from one to the next, so a
// binomial interval would assume trials nobody ran, and a ranking would report
// a result on a comparison nobody controlled.
type CheckOutcome struct {
	// Changed is sessions where work was observed to change — the denominator.
	Changed int `json:"changed,omitempty"`
	// The four states the latest check left a changed session in. They add up
	// to the checked sessions, which is why there is no fifth field for that:
	// one stored total is one number that can disagree with its parts.
	Passed  int `json:"passed,omitempty"`
	Failed  int `json:"failed,omitempty"`
	Stale   int `json:"stale,omitempty"`
	Unknown int `json:"unknown,omitempty"`
	// Attempted is every recognised check these sessions ran, which is a
	// different count from the sessions that ran one: a session that ran the
	// tests nine times is one checked session.
	Attempted int `json:"attempted,omitempty"`
	// Effort over the same changed sessions. Seconds is first event to last;
	// ActiveSeconds is what the harness says it spent working, and
	// StatusSessions is how many of Changed reported a status line at all —
	// the denominator the lines and the active time carry.
	Turns          int     `json:"turns,omitempty"`
	ToolCalls      int     `json:"tool_calls,omitempty"`
	OutTokens      int     `json:"out_tokens,omitempty"`
	Seconds        int     `json:"seconds,omitempty"`
	ActiveSeconds  int     `json:"active_seconds,omitempty"`
	LinesAdded     int     `json:"lines_added,omitempty"`
	LinesRemoved   int     `json:"lines_removed,omitempty"`
	StatusSessions int     `json:"status_sessions,omitempty"`
	CostUSD        float64 `json:"cost_usd,omitempty"`
	// EstCostUSD is the same tokens at published rates, never added to CostUSD:
	// an estimate and a measurement are different claims.
	EstCostUSD float64 `json:"est_cost_usd,omitempty"`
}

// Checked is the changed sessions that ran a recognised check, derived from the
// four states so it cannot come to disagree with them.
func (c CheckOutcome) Checked() int { return c.Passed + c.Failed + c.Stale + c.Unknown }

func (c *CheckOutcome) add(o CheckOutcome) {
	c.Changed += o.Changed
	c.Passed += o.Passed
	c.Failed += o.Failed
	c.Stale += o.Stale
	c.Unknown += o.Unknown
	c.Attempted += o.Attempted
	c.Turns += o.Turns
	c.ToolCalls += o.ToolCalls
	c.OutTokens += o.OutTokens
	c.Seconds += o.Seconds
	c.ActiveSeconds += o.ActiveSeconds
	c.LinesAdded += o.LinesAdded
	c.LinesRemoved += o.LinesRemoved
	c.StatusSessions += o.StatusSessions
	c.CostUSD += o.CostUSD
	c.EstCostUSD += o.EstCostUSD
}

// checkOutcomeOf reads one session record as its own contribution. A record
// that changed nothing contributes nothing at all — not a zero, which would put
// a session that never edited anything into the denominator of a rate about
// edited work. A record written before any of this was measured has no
// ChangeObserved and lands on the same branch, which is the honest place for
// it: absent, rather than counted as work nobody checked.
func checkOutcomeOf(r sessionRecord, calls, seconds int) CheckOutcome {
	if !r.ChangeObserved {
		return CheckOutcome{}
	}
	c := CheckOutcome{Changed: 1, Attempted: r.ChecksAttempted,
		Turns: r.Turns, ToolCalls: calls, OutTokens: r.OutTokens,
		Seconds: seconds, ActiveSeconds: r.ActiveSeconds,
		LinesAdded: r.LinesAdded, LinesRemoved: r.LinesRemoved,
		StatusSessions: boolCount(r.Status), CostUSD: r.CostUSD, EstCostUSD: r.EstCostUSD}
	switch r.CheckState {
	case checkPassed:
		c.Passed = 1
	case checkFailed:
		c.Failed = 1
	case checkStale:
		c.Stale = 1
	case checkUnknown:
		c.Unknown = 1
	}
	return c
}

// sessionDay is one day of sessions sharing a harness, model, project and task
// type — what a record becomes when it passes the detail horizon.
type sessionDay struct {
	Date             string         `json:"date"` // YYYY-MM-DD (UTC)
	Harness          string         `json:"harness,omitempty"`
	Model            string         `json:"model,omitempty"`
	Project          string         `json:"project,omitempty"`
	TaskType         string         `json:"task_type,omitempty"`
	Sessions         int            `json:"sessions"`
	Turns            int            `json:"turns"`
	ToolCalls        int            `json:"tool_calls"`
	ToolOffers       int            `json:"tool_offers,omitempty"`
	ToolCallsOffered int            `json:"tool_calls_offered,omitempty"`
	Retries          int            `json:"retries"`
	Corrections      int            `json:"corrections"`
	Seconds          int            `json:"seconds"`
	InTokens         int            `json:"in_tokens,omitempty"`
	OutTokens        int            `json:"out_tokens,omitempty"`
	CacheWriteTokens int            `json:"cache_write_tokens,omitempty"`
	CacheReadTokens  int            `json:"cache_read_tokens,omitempty"`
	EstCostUSD       float64        `json:"est_cost_usd,omitempty"`
	PeakContext      int            `json:"peak_context,omitempty"`
	CostUSD          float64        `json:"cost_usd,omitempty"`
	TurnBuckets      map[string]int `json:"turn_buckets,omitempty"`
	AbsentTools      map[string]int `json:"absent_tools,omitempty"`
	Failures         map[string]int `json:"failures,omitempty"`
	Hours            map[string]int `json:"hours,omitempty"`
	// Lengths counts the day's sessions by how long each one ran. A day rollup
	// keeps one total and loses the sessions inside it, so the distribution has
	// to be bucketed on the way in or it does not survive the horizon.
	Lengths map[string]int `json:"lengths,omitempty"`
	// From the status line. Counts add across the day's sessions; ContextPct
	// is the fullest any one of them got, and is maxed like PeakContext.
	StatusSessions int `json:"status_sessions,omitempty"`
	LinesAdded     int `json:"lines_added,omitempty"`
	LinesRemoved   int `json:"lines_removed,omitempty"`
	ActiveSeconds  int `json:"active_seconds,omitempty"`
	CacheRequests  int `json:"cache_requests,omitempty"`
	CacheMisses    int `json:"cache_misses,omitempty"`
	ContextPct     int `json:"context_pct,omitempty"`
	// Checks is the day's check evidence, over the day's CHANGED sessions only.
	// It survives the detail horizon because the day key already carries the
	// model and the client, which are the two things the comparison is keyed
	// by — so a pair's rate reads the same either side of the thirty-day line
	// with no new join. A day folded before any of this was measured has a zero
	// Changed, and a zero denominator is not a rate.
	Checks CheckOutcome `json:"checks,omitempty"`
}

func (d sessionDay) bucket() string {
	return strings.Join([]string{d.Date, d.Harness, d.Model, d.Project, d.TaskType}, "\x00")
}

// sessionLog is the pair of on-disk files and their in-memory state.
type sessionLog struct {
	mu        sync.Mutex
	path      string // "" -> in-memory only (tests, or no home dir)
	dayPath   string
	now       func() time.Time
	recs      map[string]*sessionRecord
	days      map[string]*sessionDay
	extra     int  // appended lines beyond one per record
	daysDirty bool // a day changed and has not been rewritten
}

// sessionLogPaths names both files inside the member's state directory.
//
// It used to take the directory of the usage log, on the reasoning that one
// place for everything local is easier to find and easier to delete. The usage
// log lives at the top of $HOME, so "one place" turned out to be the home
// directory itself and the files landed there undotted. An empty dir means
// in-memory only, as everywhere else.
func sessionLogPaths(stateDir string) (detail, daily string) {
	if stateDir == "" {
		return "", ""
	}
	return filepath.Join(stateDir, "sessions.jsonl"), filepath.Join(stateDir, "session-days.jsonl")
}

// loadSessionLog reads both files, tolerating absence and corruption alike (a
// broken line is skipped, never fatal), folds anything past the detail horizon
// into its day, and drops days past the long horizon. A missing home dir
// degrades to in-memory only.
func loadSessionLog(detail, daily string, now func() time.Time) *sessionLog {
	if now == nil {
		now = time.Now
	}
	s := &sessionLog{path: detail, dayPath: daily, now: now,
		recs: map[string]*sessionRecord{}, days: map[string]*sessionDay{}}
	if detail == "" {
		return s
	}
	lines := 0
	for _, line := range readLines(detail) {
		var r sessionRecord
		if json.Unmarshal([]byte(line), &r) != nil || r.Key == "" {
			s.extra++
			continue
		}
		lines++
		// The last line for a key wins its LABELS — a session is upserted per
		// Stop, and the newest line is the best statement of what it was. Its
		// counts are raised to the largest any line holds, on the same
		// invariant upsert keeps: a session's counts only ever grow. That also
		// heals a file written before resume existed, where a forgetful
		// instance appended a partial line after a full one.
		keepLarger(&r, s.recs[r.Key])
		s.recs[r.Key] = &r
	}
	s.extra += lines - len(s.recs)
	for _, line := range readLines(daily) {
		var d sessionDay
		if json.Unmarshal([]byte(line), &d) != nil || d.Date == "" {
			s.daysDirty = true
			continue
		}
		if existing := s.days[d.bucket()]; existing != nil {
			addDay(existing, d)
			s.daysDirty = true
			continue
		}
		copied := d
		s.days[d.bucket()] = &copied
	}
	s.compactLocked()
	return s
}

func readLines(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// upsert records a session's current state: in memory always, and appended as
// one JSONL line when persisted. Best-effort — a write failure costs a line of
// local history, never a turn, and a nil receiver is a no-op so callers need
// not guard.
func (s *sessionLog) upsert(r sessionRecord) {
	if s == nil || r.Key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, seen := s.recs[r.Key]; seen {
		s.extra++
		// A session's counts only ever grow. Resuming (resume, above) is what
		// keeps them growing across a restart; this is the floor under it, for
		// the paths where a resume could not happen — an unreadable log, or a
		// record rebuilt from the transcript while an agent still holds the
		// session. Without it a partial instance replaces a fuller record and
		// the loss reads as a quiet day.
		keepLarger(&r, prior)
	}
	copied := r
	s.recs[r.Key] = &copied
	if s.path == "" {
		return
	}
	if s.extra > sessionRewriteSlack*(len(s.recs)+1) {
		s.rewriteLocked()
		return
	}
	if line, err := json.Marshal(copied); err == nil {
		appendLine(s.path, line)
	}
}

// lookup returns what the log holds for one session, which is how a fresh agent
// instance finds the session it is joining halfway through.
func (s *sessionLog) lookup(key string) (sessionRecord, bool) {
	if s == nil || key == "" {
		return sessionRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recs[key]
	if !ok {
		return sessionRecord{}, false
	}
	return *r, true
}

// counts is what this machine has done, as the sampled quota history needs it:
// a running total it can difference. It walks the records rather than keeping a
// counter, because a counter would have to survive a restart, the fold into
// days, and every upsert — and this is asked for once every few minutes.
func (s *sessionLog) counts() (turns, calls int) {
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.recs {
		turns += r.Turns
		for _, n := range r.Tools {
			calls += n
		}
	}
	for _, d := range s.days {
		turns += d.Turns
		calls += d.ToolCalls
	}
	return turns, calls
}

// keepLarger raises every count in r to the larger of r and prior, leaving the
// labels — model, project, task type, harness — as the newer record states
// them. A count is the thing that cannot honestly go down; a label is the thing
// that can honestly change.
func keepLarger(r *sessionRecord, prior *sessionRecord) {
	if prior == nil {
		return
	}
	if !prior.Start.IsZero() && (r.Start.IsZero() || prior.Start.Before(r.Start)) {
		r.Start = prior.Start
	}
	if prior.End.After(r.End) {
		r.End = prior.End
	}
	raise := func(a *int, b int) {
		if b > *a {
			*a = b
		}
	}
	raise(&r.Turns, prior.Turns)
	raise(&r.Retries, prior.Retries)
	raise(&r.Corrections, prior.Corrections)
	raise(&r.InTokens, prior.InTokens)
	raise(&r.OutTokens, prior.OutTokens)
	raise(&r.CacheWriteTokens, prior.CacheWriteTokens)
	raise(&r.CacheReadTokens, prior.CacheReadTokens)
	raise(&r.PeakContext, prior.PeakContext)
	raise(&r.LinesAdded, prior.LinesAdded)
	raise(&r.LinesRemoved, prior.LinesRemoved)
	raise(&r.ActiveSeconds, prior.ActiveSeconds)
	raise(&r.ContextPct, prior.ContextPct)
	raise(&r.CacheRequests, prior.CacheRequests)
	raise(&r.CacheMisses, prior.CacheMisses)
	r.Status = r.Status || prior.Status
	if r.Effort == "" {
		r.Effort = prior.Effort
	}
	if prior.CostUSD > r.CostUSD {
		r.CostUSD = prior.CostUSD
	}
	// The estimate is derived from token counts that keepLarger has already
	// raised, so it can only go up with them — but a rebuilt record that lost
	// its model would price nothing, and a session's cost must never fall.
	if prior.EstCostUSD > r.EstCostUSD {
		r.EstCostUSD = prior.EstCostUSD
	}
	r.Tools = maxCounts(r.Tools, prior.Tools)
	r.Offered = maxCounts(r.Offered, prior.Offered)
	r.Failures = maxCounts(r.Failures, prior.Failures)
	r.Hours = maxCounts(r.Hours, prior.Hours)
	r.TurnBuckets = maxCounts(r.TurnBuckets, prior.TurnBuckets)
	r.AbsentTools = maxCounts(r.AbsentTools, prior.AbsentTools)
	for tool, d := range prior.ToolDetail {
		if r.ToolDetail == nil {
			r.ToolDetail = map[string]map[string]int{}
		}
		r.ToolDetail[tool] = maxCounts(r.ToolDetail[tool], d)
	}
	if r.Model == "" {
		r.Model, r.ModelRaw = prior.Model, prior.ModelRaw
	}
	if r.Project == "" {
		r.Project = prior.Project
	}
	// Check evidence follows the same split the rest of this function does, for
	// the same reason: the counts cannot honestly go down, and the STATE can
	// honestly change. r is the newer line, so its state is the later reading
	// of event order and wins — except where it has none, which means this
	// instance saw no check and must not erase what an earlier one did.
	raise(&r.ChecksAttempted, prior.ChecksAttempted)
	raise(&r.ChecksPassed, prior.ChecksPassed)
	r.ChangeObserved = r.ChangeObserved || prior.ChangeObserved
	if r.CheckState == "" {
		r.CheckState = prior.CheckState
	}
}

// maxCounts raises dst to the larger of the two counts per key. Addition would
// be wrong here: the same instance upserts the same cumulative session at every
// Stop, and adding those would count one session many times over.
func maxCounts(dst, src map[string]int) map[string]int {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]int{}
	}
	for k, v := range src {
		if v > dst[k] {
			dst[k] = v
		}
	}
	return dst
}

func appendLine(path string, line []byte) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// compactLocked folds records past the detail horizon into their day and drops
// days past the long one. One-way and idempotent: a record joins a day exactly
// once, because it leaves the detail map in the same step.
func (s *sessionLog) compactLocked() {
	now := s.now().UTC()
	detailCutoff := now.Add(-sessionDetailRetention)
	dayCutoff := now.Add(-sessionDailyRetention).Format("2006-01-02")
	changed := false
	for key, r := range s.recs {
		if !r.End.Before(detailCutoff) {
			continue
		}
		s.foldLocked(*r)
		delete(s.recs, key)
		changed = true
	}
	for bucket, d := range s.days {
		if d.Date < dayCutoff {
			delete(s.days, bucket)
			changed = true
		}
	}
	if changed || s.extra > 0 {
		s.rewriteLocked()
	}
	if s.daysDirty {
		s.rewriteDaysLocked()
	}
}

func (s *sessionLog) foldLocked(r sessionRecord) {
	d := sessionDay{
		Date: r.End.UTC().Format("2006-01-02"), Harness: r.Harness, Model: r.Model,
		Project: r.Project, TaskType: r.TaskType,
	}
	into := s.days[d.bucket()]
	if into == nil {
		into = &d
		s.days[d.bucket()] = into
	}
	into.Sessions++
	into.Turns += r.Turns
	for _, n := range r.Tools {
		into.ToolCalls += n
	}
	if len(r.Offered) > 0 {
		for _, n := range r.Offered {
			into.ToolOffers += n
		}
		for _, n := range r.Tools {
			into.ToolCallsOffered += n
		}
	}
	into.Retries += r.Retries
	into.Corrections += r.Corrections
	if sec := int(r.End.Sub(r.Start).Seconds()); sec > 0 {
		into.Seconds += sec
	}
	into.InTokens += r.InTokens
	into.OutTokens += r.OutTokens
	into.CacheWriteTokens += r.CacheWriteTokens
	into.CacheReadTokens += r.CacheReadTokens
	into.EstCostUSD += r.EstCostUSD
	into.LinesAdded += r.LinesAdded
	into.LinesRemoved += r.LinesRemoved
	into.ActiveSeconds += r.ActiveSeconds
	into.CacheRequests += r.CacheRequests
	into.CacheMisses += r.CacheMisses
	if r.Status {
		into.StatusSessions++
	}
	if r.ContextPct > into.ContextPct {
		into.ContextPct = r.ContextPct
	}
	// Max, never sum: two sessions that each half-filled the window did not
	// between them fill it once.
	if r.PeakContext > into.PeakContext {
		into.PeakContext = r.PeakContext
	}
	into.TurnBuckets = addCounts(into.TurnBuckets, r.TurnBuckets)
	into.AbsentTools = addCounts(into.AbsentTools, r.AbsentTools)
	into.Failures = addCounts(into.Failures, r.Failures)
	into.Hours = addCounts(into.Hours, r.Hours)
	if b := sessionLengthBucket(r.End.Sub(r.Start)); b != "" {
		into.Lengths = addCounts(into.Lengths, map[string]int{b: 1})
	}
	into.CostUSD += r.CostUSD
	// The same session's calls and span the totals above were built from, so
	// the pair rows and the window totals cannot come to disagree about what a
	// changed session cost.
	calls := 0
	for _, n := range r.Tools {
		calls += n
	}
	seconds := int(r.End.Sub(r.Start).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	into.Checks.add(checkOutcomeOf(r, calls, seconds))
	s.daysDirty = true
}

func addDay(into *sessionDay, from sessionDay) {
	into.Sessions += from.Sessions
	into.Turns += from.Turns
	into.ToolCalls += from.ToolCalls
	into.ToolOffers += from.ToolOffers
	into.ToolCallsOffered += from.ToolCallsOffered
	into.Retries += from.Retries
	into.Corrections += from.Corrections
	into.Seconds += from.Seconds
	if from.PeakContext > into.PeakContext {
		into.PeakContext = from.PeakContext
	}
	into.TurnBuckets = addCounts(into.TurnBuckets, from.TurnBuckets)
	into.AbsentTools = addCounts(into.AbsentTools, from.AbsentTools)
	into.Failures = addCounts(into.Failures, from.Failures)
	into.Hours = addCounts(into.Hours, from.Hours)
	into.Lengths = addCounts(into.Lengths, from.Lengths)
	into.InTokens += from.InTokens
	into.OutTokens += from.OutTokens
	into.CacheWriteTokens += from.CacheWriteTokens
	into.CacheReadTokens += from.CacheReadTokens
	into.EstCostUSD += from.EstCostUSD
	into.CostUSD += from.CostUSD
	into.StatusSessions += from.StatusSessions
	into.LinesAdded += from.LinesAdded
	into.LinesRemoved += from.LinesRemoved
	into.ActiveSeconds += from.ActiveSeconds
	into.CacheRequests += from.CacheRequests
	into.CacheMisses += from.CacheMisses
	if from.ContextPct > into.ContextPct {
		into.ContextPct = from.ContextPct
	}
	into.Checks.add(from.Checks)
}

// rewriteLocked persists the detail map atomically, one line per session. A
// failure loses the compaction, not the log — but it is worth saying, because a
// file that stops shrinking is otherwise silent. Caller holds s.mu.
func (s *sessionLog) rewriteLocked() {
	if s.path == "" {
		return
	}
	keys := make([]string, 0, len(s.recs))
	for k := range s.recs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if line, err := json.Marshal(s.recs[k]); err == nil {
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	if err := fsx.WriteFileAtomic(s.path, []byte(b.String()), 0o600); err != nil {
		log.Printf("[tacit-hooks] session log not compacted into %s: %v", s.path, err)
		return
	}
	s.extra = 0
}

func (s *sessionLog) rewriteDaysLocked() {
	if s.dayPath == "" {
		return
	}
	buckets := make([]string, 0, len(s.days))
	for k := range s.days {
		buckets = append(buckets, k)
	}
	sort.Strings(buckets)
	var b strings.Builder
	for _, k := range buckets {
		if line, err := json.Marshal(s.days[k]); err == nil {
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	if err := fsx.WriteFileAtomic(s.dayPath, []byte(b.String()), 0o600); err != nil {
		log.Printf("[tacit-hooks] session days not written to %s: %v", s.dayPath, err)
		return
	}
	s.daysDirty = false
}

// flush compacts and persists everything. Called when the agent idles out, so
// a daemon that exits does not leave a month of appended duplicates behind.
func (s *sessionLog) flush() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactLocked()
	if s.daysDirty {
		s.rewriteDaysLocked()
	}
}

// --- the accumulator ---

// sessionStats is one live session's running counts. It lives on sessionState
// and is folded into a sessionRecord at every Stop, so a session that never
// ends cleanly — Codex has no SessionEnd, and a laptop lid closes — is still
// recorded up to its last completed turn.
type sessionStats struct {
	start time.Time
	end   time.Time
	turns int
	tools map[string]int
	// offered counts what each tool was ASKED to do, from PreToolUse. Kept
	// apart from tools, which stays what RAN: moving that count would move
	// every rate on the page at once.
	offered     map[string]int
	failures    map[string]int
	hours       map[string]int
	retries     int
	corrections int
	inTokens    int
	outTokens   int
	// cacheWriteTokens and cacheReadTokens split inTokens the way the bill is
	// split. Sums, like inTokens: a share of one turn says nothing about a
	// session, so the page divides the totals.
	cacheWriteTokens int
	cacheReadTokens  int
	// peakContext is the fullest the model's context got during this session —
	// a LEVEL, so it is kept as a maximum and never added to anything. It
	// answers the question a member has after a long session went badly:
	// was I running the window out.
	peakContext int
	costUSD     float64
	// status holds what the harness status line last said about this session
	// (capture.ReadStatusLine). Every figure in it is cumulative for the
	// session, so it is REPLACED with the larger reading rather than added to,
	// and statusSeen says the status line reported at all.
	status     capture.StatusLine
	statusSeen bool
	lastTool   string
	lastArgs   string
	// lastTurnAt is when the member's current turn began, for the duration
	// buckets. Cleared as each turn closes so an idle session between turns
	// cannot be counted as one very long one.
	lastTurnAt  time.Time
	turnBuckets map[string]int
	absentTools map[string]int
	// toolDetail is the second level: for each tool, what it was actually doing.
	// A shell tool's detail is the programs it ran and their subcommands ("git
	// commit"); a file tool's is the kind of file it touched (".go"). Both are
	// vocabularies derived from an input that is then dropped — never a command
	// line and never a path.
	toolDetail map[string]map[string]int
	// Check evidence, as verification.go reads it. checkState holds where the
	// LATEST check left the session, so it follows event order rather than
	// growing: a pass can be replaced by a later failure, and by staleness the
	// moment an edit lands after it.
	changeObserved  bool
	checksAttempted int
	checksPassed    int
	checkState      string
	// The prior fields are what a RESUMED session already knew about itself and
	// this instance may never see again: the model and the project are read off
	// the payloads of events that have already happened, so an agent joining a
	// session halfway through would otherwise file the rest of it as
	// unattributed work in no project. Empty for a session that started here.
	priorModel, priorModelRaw, priorProject string
}

// resume primes a fresh instance with what the log already holds for this
// session.
//
// The agent idle-exits after fifteen quiet minutes and the member keeps
// working; the next hook starts a new process with an empty head. Before this,
// that process counted from zero and its record REPLACED the fuller one, so a
// session with a coffee break in it kept only what happened after the break.
// Measured on one machine's log: four sessions holding 57, 3, 7 and 7 tool
// calls whose transcripts held 437, 145, 595 and 190.
//
// Counts stay cumulative per session, so what this instance writes at its next
// Stop is the whole session rather than its own share of it.
func (s *sessionStats) resume(r sessionRecord) {
	if !r.Start.IsZero() && (s.start.IsZero() || r.Start.Before(s.start)) {
		s.start = r.Start
	}
	if r.End.After(s.end) {
		s.end = r.End
	}
	s.turns += r.Turns
	s.retries += r.Retries
	// Corrections are counted on the session STATE rather than here
	// (agent_events.go), so the resuming agent seeds that from the same record.
	s.inTokens += r.InTokens
	s.outTokens += r.OutTokens
	s.cacheWriteTokens += r.CacheWriteTokens
	s.cacheReadTokens += r.CacheReadTokens
	s.costUSD += r.CostUSD
	if r.PeakContext > s.peakContext {
		s.peakContext = r.PeakContext // a level, never a sum
	}
	// The status-line levels come back whole on the next render, but only if
	// the member has the status line wired and the session is still open. A
	// resumed session seeds them so a record written before the next render
	// cannot report a session that shrank.
	s.noteStatus(capture.StatusLine{
		CostUSD: r.CostUSD, ActiveSeconds: r.ActiveSeconds,
		LinesAdded: r.LinesAdded, LinesRemoved: r.LinesRemoved,
		ContextPct: r.ContextPct, CacheRequests: r.CacheRequests,
		CacheMisses: r.CacheMisses, Effort: r.Effort,
	}, r.Status)
	s.tools = addCounts(s.tools, r.Tools)
	s.offered = addCounts(s.offered, r.Offered)
	s.failures = addCounts(s.failures, r.Failures)
	s.hours = addCounts(s.hours, r.Hours)
	s.turnBuckets = addCounts(s.turnBuckets, r.TurnBuckets)
	s.absentTools = addCounts(s.absentTools, r.AbsentTools)
	for tool, d := range r.ToolDetail {
		if s.toolDetail == nil {
			s.toolDetail = map[string]map[string]int{}
		}
		s.toolDetail[tool] = addCounts(s.toolDetail[tool], d)
	}
	s.priorModel, s.priorModelRaw, s.priorProject = r.Model, r.ModelRaw, r.Project
	// The check evidence comes back too. Counts add, like every other count
	// here. The state is where the session was left, and this instance inherits
	// it rather than starting from "no check was ever seen": a session that
	// passed its tests before the daemon idled out did pass them, and an edit
	// after the restart still has a pass to make stale.
	s.changeObserved = s.changeObserved || r.ChangeObserved
	s.checksAttempted += r.ChecksAttempted
	s.checksPassed += r.ChecksPassed
	if s.checkState == "" {
		s.checkState = r.CheckState
	}
}

// noteChange records that this session changed something, and ages any passing
// check that came before it.
//
// The staleness rule is the whole reason the state is not a bool. An agent that
// ran the tests, saw them pass, and then edited four more files has not shown
// that the finished work passes anything — it has shown that an earlier version
// did. Only a recognised EDIT ages a pass: the status line restates the whole
// session on every render and carries no ordering, so it can say that work
// changed and cannot say when.
func (s *sessionStats) noteChange(ordered bool) {
	s.changeObserved = true
	if ordered && s.checkState == checkPassed {
		s.checkState = checkStale
	}
}

// noteCheck folds one recognised check into the running evidence.
//
// A clear result replaces an earlier one; an unclear result does not. A check
// whose output proved nothing is worth counting and is not worth overwriting a
// pass or a failure with — which is why unknown is the one state that yields.
func (s *sessionStats) noteCheck(state string) {
	s.checksAttempted++
	if state == checkPassed {
		s.checksPassed++
	}
	if state == checkUnknown && s.checkState != "" {
		return
	}
	s.checkState = state
}

// TurnBuckets are the durations a turn is counted under, coarsest form that
// still answers "where does the time go": a turn that came back while you were
// reading, one you waited through, and one you went and did something else
// during. Buckets rather than timings, because a list of durations is a log of
// when a member was at their desk and this file does not keep one.
var TurnBuckets = []struct {
	Key   string
	Under time.Duration
}{
	{"under 30s", 30 * time.Second},
	{"30s–2m", 2 * time.Minute},
	{"2–10m", 10 * time.Minute},
	{"over 10m", 1<<63 - 1},
}

// SessionLengths are the lengths a session is counted under: one you dipped
// into, one that took the morning, and one that took the day. The same move
// TurnBuckets makes one level down, and for the same reason — a list of
// durations is a log of when somebody was at their desk, and this file keeps
// counts instead.
var SessionLengths = []struct {
	Key   string
	Under time.Duration
}{
	{"under 15m", 15 * time.Minute},
	{"15m–1h", time.Hour},
	{"1–3h", 3 * time.Hour},
	{"over 3h", 1<<63 - 1},
}

// sessionLengthBucket names the band a session's wall clock falls in. A
// negative or zero span is no band at all rather than the shortest one: a
// clock that went backwards is not a fifteen-minute session.
func sessionLengthBucket(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	for _, b := range SessionLengths {
		if d < b.Under {
			return b.Key
		}
	}
	return SessionLengths[len(SessionLengths)-1].Key
}

func turnBucket(d time.Duration) string {
	for _, b := range TurnBuckets {
		if d < b.Under {
			return b.Key
		}
	}
	return TurnBuckets[len(TurnBuckets)-1].Key
}

// noteAbsent records tools a judge decided would have helped and were not used
// (discovery.go). Names only, counted — the same standard the tool counts hold,
// and the reason no argument or reasoning text comes with them.
func (s *sessionStats) noteAbsent(tools []string) {
	if s.absentTools == nil {
		s.absentTools = map[string]int{}
	}
	for _, t := range tools {
		if t != "" {
			s.absentTools[t]++
		}
	}
}

// noteStatus folds one status-line reading into the running counts. The
// payload restates the whole session on every render, so each figure is raised
// to the larger of the two and never added: two renders of a session that had
// cost thirty cents did not between them cost sixty.
//
// seen is passed separately from the reading because a resumed session seeds
// these levels from its own record, and that is not the status line reporting.
func (s *sessionStats) noteStatus(in capture.StatusLine, seen bool) {
	raise := func(a *int, b int) {
		if b > *a {
			*a = b
		}
	}
	if in.CostUSD > s.status.CostUSD {
		s.status.CostUSD = in.CostUSD
	}
	raise(&s.status.ActiveSeconds, in.ActiveSeconds)
	raise(&s.status.LinesAdded, in.LinesAdded)
	raise(&s.status.LinesRemoved, in.LinesRemoved)
	raise(&s.status.ContextPct, in.ContextPct)
	raise(&s.status.CacheRequests, in.CacheRequests)
	raise(&s.status.CacheMisses, in.CacheMisses)
	if in.Effort != "" {
		s.status.Effort = in.Effort
	}
	// The harness's own count of its edits is the second source of "this
	// session changed something", and on a client whose agent edits through the
	// shell it is the only one. Unordered, so it cannot age a passing check.
	if in.LinesAdded > 0 || in.LinesRemoved > 0 {
		s.noteChange(false)
	}
	s.statusSeen = s.statusSeen || seen
}

// observe folds one hook payload into the running counts. It reads only the
// countable shape of the payload: an event name, a tool name, the fingerprint
// of a tool's arguments, and usage numbers when a source reports them. Nothing
// it reads is kept.
func (s *sessionStats) observe(now time.Time, event string, payload map[string]any) {
	if s.start.IsZero() {
		s.start = now
	}
	s.end = now
	switch event {
	case capture.EvUserPrompt:
		s.turns++
		s.lastTurnAt = now
		// The member's own clock, not UTC: this is the one figure here that is
		// about them rather than about the work.
		if s.hours == nil {
			s.hours = map[string]int{}
		}
		s.hours[fmt.Sprintf("%02d", now.Hour())]++
	case capture.EvPreTool:
		// A tool call that reaches here was offered; one that also reaches
		// PostToolUse ran. The difference is a permission refused, an escape,
		// or a hook that blocked it — the same measure Claude Code publishes
		// as a suggestion accept rate, and it needs no source we do not read.
		if name, _ := payload["tool_name"].(string); name != "" {
			if s.offered == nil {
				s.offered = map[string]int{}
			}
			s.offered[name]++
		}
	case capture.EvPostToolFail:
		// A call that failed. It arrives as its own event rather than as a flag
		// on PostToolUse — measured on a live session: a failing Bash fires
		// PreToolUse and then nothing else — so this is the only place a
		// failure is visible at all, and "tools" above stays what SUCCEEDED.
		if kind := capture.ToolFailure(true, capture.ToolResponse(payload)); kind != "" {
			if s.failures == nil {
				s.failures = map[string]int{}
			}
			s.failures[kind]++
		}
		// A check that the harness says failed is a failed check, whatever its
		// output reads like. This is the one event where a failing shell command
		// is visible at all, so without it a session whose tests broke would be
		// recorded as a session with no check evidence.
		s.noteToolCheck(payload, true)
	case capture.EvPostTool:
		name, _ := payload["tool_name"].(string)
		// A harness that reports failures on the success event instead — a flag
		// on the payload rather than an event of its own — is read here, so one
		// rule covers both and neither is inferred from the text.
		if capture.FailedPayload(payload) {
			if kind := capture.ToolFailure(true, capture.ToolResponse(payload)); kind != "" {
				if s.failures == nil {
					s.failures = map[string]int{}
				}
				s.failures[kind]++
			}
		}
		if name == "" {
			return
		}
		if s.tools == nil {
			s.tools = map[string]int{}
		}
		s.tools[name]++
		s.noteDetail(name, payload["tool_input"])
		// Did this session change anything, and did it find out whether the
		// change held up. Both read the same event and neither keeps what it
		// read (verification.go).
		if capture.ToolKind(name) == "edit" {
			s.noteChange(true)
		}
		s.noteToolCheck(payload, capture.FailedPayload(payload))
		args := capture.AsText(payload["tool_input"])
		if name == s.lastTool && args == s.lastArgs && args != "" {
			s.retries++
		}
		s.lastTool, s.lastArgs = name, args
	case capture.EvStop:
		if !s.lastTurnAt.IsZero() {
			if s.turnBuckets == nil {
				s.turnBuckets = map[string]int{}
			}
			s.turnBuckets[turnBucket(now.Sub(s.lastTurnAt))]++
			s.lastTurnAt = time.Time{}
		}
		s.readUsage(payload)
		// No hook harness puts usage on the Stop payload, so for Claude Code
		// and Codex the transcript is the only source — and it has carried the
		// numbers all along (capture.TranscriptUsage).
		if path, _ := payload["transcript_path"].(string); path != "" {
			if u, ok := capture.TranscriptUsage(path); ok {
				// One number, two aggregations: summed it is the input this
				// session consumed, maxed it is the fullest the context got.
				in := u.In()
				s.inTokens += in
				s.outTokens += u.Out
				// And the three classes apart, because they are priced apart.
				// Summed only — a share of a turn's input says nothing about
				// the session, so the page divides the sums instead.
				s.cacheWriteTokens += u.CacheWrite
				s.cacheReadTokens += u.CacheRead
				if in > s.peakContext {
					s.peakContext = in
				}
			}
		}
	}
}

// noteToolCheck reads one tool call's command and result as check evidence.
// Both texts are matched here and dropped here: what leaves this function is a
// count and a one-word state.
func (s *sessionStats) noteToolCheck(payload map[string]any, failed bool) {
	command := capture.AsText(payload["tool_input"])
	if command == "" {
		return
	}
	state, isCheck := checkOutcome(command, capture.AsText(capture.ToolResponse(payload)), failed)
	if !isCheck {
		return
	}
	s.noteCheck(state)
}

// readUsage takes token and cost numbers off a payload that carries them. Most
// harnesses do not, and a source that says nothing must leave the totals alone
// rather than write a zero that reads as a measurement.
func (s *sessionStats) readUsage(payload map[string]any) {
	usage, _ := payload["usage"].(map[string]any)
	if usage == nil {
		usage = payload
	}
	s.inTokens += intOf(usage["input_tokens"]) + intOf(usage["prompt_tokens"])
	s.outTokens += intOf(usage["output_tokens"]) + intOf(usage["completion_tokens"])
	s.costUSD += floatOf(usage["cost_usd"]) + floatOf(usage["total_cost_usd"])
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func floatOf(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

// record renders the running counts as the row the log stores.
func (s *sessionStats) record(key, harness, model, cwd string, corrections int) sessionRecord {
	names := make([]string, 0, len(s.tools))
	tools := make(map[string]int, len(s.tools))
	for n, c := range s.tools {
		names = append(names, n)
		tools[n] = c
	}
	sort.Strings(names)
	r := sessionRecord{
		Key: key, Start: s.start.UTC(), End: s.end.UTC(), Harness: harness,
		Model: modelid.Key(model), ModelRaw: model, Project: projectOf(cwd),
		TaskType: capture.TaskType(names), Turns: s.turns, Retries: s.retries,
		Corrections: corrections, InTokens: s.inTokens, OutTokens: s.outTokens,
		CacheWriteTokens: s.cacheWriteTokens, CacheReadTokens: s.cacheReadTokens,
		PeakContext: s.peakContext, CostUSD: s.costUSD,
		Status: s.statusSeen, LinesAdded: s.status.LinesAdded,
		LinesRemoved: s.status.LinesRemoved, ActiveSeconds: s.status.ActiveSeconds,
		ContextPct: s.status.ContextPct, CacheRequests: s.status.CacheRequests,
		CacheMisses: s.status.CacheMisses, Effort: s.status.Effort,
	}
	// Two sources can report cost — a gateway, which reports it per call and
	// is summed, and the status line, which reports the session's running
	// total. The larger is the honest answer and the sum is not: adding them
	// would count the same dollars twice on a machine that has both.
	if s.status.CostUSD > r.CostUSD {
		r.CostUSD = s.status.CostUSD
	}
	// And what the tokens come to at published rates, for the eight routed
	// harnesses that report no money at all. Computed for EVERY session rather
	// than only the unmeasured ones, because a member with both wants to know
	// whether the two agree — and the page can only offer that comparison if
	// the estimate exists beside the measurement rather than instead of it.
	//
	// Fresh input is the remainder: the transcript reports cache creation and
	// cache reads apart, and what is left of InTokens is what missed the cache.
	if fresh := r.InTokens - r.CacheWriteTokens - r.CacheReadTokens; r.InTokens > 0 {
		if fresh < 0 {
			fresh = 0
		}
		if est, ok := pricing.Estimate(r.ModelRaw, fresh, r.CacheWriteTokens, r.CacheReadTokens, r.OutTokens); ok {
			r.EstCostUSD = est
		}
	}
	// A resumed instance reads the model and the project off events that have
	// already happened, so where it saw neither it keeps what the session was
	// already filed under rather than blanking it.
	if r.Model == "" {
		r.Model, r.ModelRaw = s.priorModel, s.priorModelRaw
	}
	if r.Project == "" {
		r.Project = s.priorProject
	}
	if len(s.turnBuckets) > 0 {
		r.TurnBuckets = copyCounts(s.turnBuckets)
	}
	if len(s.absentTools) > 0 {
		r.AbsentTools = copyCounts(s.absentTools)
	}
	if len(s.toolDetail) > 0 {
		r.ToolDetail = make(map[string]map[string]int, len(s.toolDetail))
		for tool, d := range s.toolDetail {
			r.ToolDetail[tool] = copyCounts(d)
		}
	}
	if len(tools) > 0 {
		r.Tools = tools
	}
	if len(s.offered) > 0 {
		r.Offered = copyCounts(s.offered)
	}
	if len(s.failures) > 0 {
		r.Failures = copyCounts(s.failures)
	}
	if len(s.hours) > 0 {
		r.Hours = copyCounts(s.hours)
	}
	r.ChangeObserved = s.changeObserved
	r.ChecksAttempted = s.checksAttempted
	r.ChecksPassed = s.checksPassed
	r.CheckState = s.checkState
	return r
}

// projectOf reduces a working directory to the repository's own name. The
// basename is what distinguishes one project from another; the path above it is
// a fact about the member's disk and has no business in a record.
func projectOf(cwd string) string {
	cwd = strings.TrimRight(strings.TrimSpace(cwd), "/")
	if cwd == "" || cwd == "/" {
		return ""
	}
	base := filepath.Base(cwd)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

// --- read side ---

// WorkTotals is the countable shape of a set of sessions.
type WorkTotals struct {
	Sessions  int `json:"sessions"`
	Turns     int `json:"turns"`
	ToolCalls int `json:"tool_calls"`
	// ToolOffers is what was ASKED of the tools and ToolCallsOffered is what
	// ran IN THE SAME SESSIONS. The second one exists because the first is
	// otherwise divided by the wrong number: a session recorded before offers
	// were counted contributes calls and no offers, so ToolCalls over a window
	// that straddles that change is a denominator drawn from a larger set than
	// the numerator, and the rate comes out over 100% — which is the tell.
	ToolOffers       int `json:"tool_offers,omitempty"`
	ToolCallsOffered int `json:"tool_calls_offered,omitempty"`
	Retries          int `json:"retries"`
	Corrections      int `json:"corrections"`
	Seconds          int `json:"seconds"`
	InTokens         int `json:"in_tokens,omitempty"`
	OutTokens        int `json:"out_tokens,omitempty"`
	// The three classes of input the bill is split into. Fresh input is what is
	// left of InTokens after both, so it is computed rather than carried.
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	CacheReadTokens  int     `json:"cache_read_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	// EstCostUSD is the same tokens at published rates, never added to CostUSD.
	EstCostUSD float64 `json:"est_cost_usd,omitempty"`
	// From the harness status line. StatusSessions is how many of Sessions
	// reported it — the denominator every figure below carries, and the reason
	// a member with one wired machine out of two does not read the other one
	// as a machine that wrote no code.
	StatusSessions int `json:"status_sessions,omitempty"`
	LinesAdded     int `json:"lines_added,omitempty"`
	LinesRemoved   int `json:"lines_removed,omitempty"`
	ActiveSeconds  int `json:"active_seconds,omitempty"`
	CacheRequests  int `json:"cache_requests,omitempty"`
	CacheMisses    int `json:"cache_misses,omitempty"`
}

func (t *WorkTotals) add(o WorkTotals) {
	t.Sessions += o.Sessions
	t.Turns += o.Turns
	t.ToolCalls += o.ToolCalls
	t.ToolOffers += o.ToolOffers
	t.ToolCallsOffered += o.ToolCallsOffered
	t.Retries += o.Retries
	t.Corrections += o.Corrections
	t.Seconds += o.Seconds
	t.InTokens += o.InTokens
	t.OutTokens += o.OutTokens
	t.CacheWriteTokens += o.CacheWriteTokens
	t.CacheReadTokens += o.CacheReadTokens
	t.CostUSD += o.CostUSD
	t.EstCostUSD += o.EstCostUSD
	t.StatusSessions += o.StatusSessions
	t.LinesAdded += o.LinesAdded
	t.LinesRemoved += o.LinesRemoved
	t.ActiveSeconds += o.ActiveSeconds
	t.CacheRequests += o.CacheRequests
	t.CacheMisses += o.CacheMisses
}

// TurnsPerSession is the headline the member actually compares across models:
// how much back-and-forth one piece of work took. Zero sessions yields zero
// rather than a division — a rate with no denominator is not a small number,
// it is no number, and the view says so instead of printing it.
func (t WorkTotals) TurnsPerSession() float64 {
	if t.Sessions == 0 {
		return 0
	}
	return float64(t.Turns) / float64(t.Sessions)
}

// WorkGroup is one value of a grouping — a model, a project, a task type —
// with its totals and the span it covers.
type WorkGroup struct {
	Key   string    `json:"key"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
	WorkTotals
}

// ModelUse is one model's account of itself over the window: the totals every
// grouping carries, and beside them what the model was actually doing — under
// which client, on which projects, at which kind of work, how long its turns
// ran, how full its context got, and which exact labels folded into the cohort.
//
// Only the model grouping carries these. A project and a task type are facts
// about the work; the model is the one thing a member CHANGES, so it is the one
// grouping where "and what happened under it" is a question worth a table.
//
// Harness, Task and Project count SESSIONS — a session runs under one of each,
// so a call count would say the same thing louder. TurnTimes counts turns, in
// the vocabulary TurnBuckets defines. Variants counts sessions too.
type ModelUse struct {
	WorkGroup
	Harness   map[string]int `json:"harness,omitempty"`
	Task      map[string]int `json:"task,omitempty"`
	Project   map[string]int `json:"project,omitempty"`
	TurnTimes map[string]int `json:"turn_times,omitempty"`
	// Variants are the labels the harnesses actually said, which modelid folded
	// into this one cohort: a pinned date stamp, a gateway route, a Bedrock arn.
	// They come from the full records only — a compacted day keeps the cohort
	// and loses the label — so the view reads them against DetailFrom.
	Variants map[string]int `json:"variants,omitempty"`
	// PeakContext is the fullest this model's context got, a level like every
	// other peak here: carried as a maximum, never added to another one.
	PeakContext int `json:"peak_context,omitempty"`
	// Effort counts sessions by the reasoning level they ran at. It sits with
	// the model because it is the other half of the same choice: what a member
	// changes when they change how a session is configured.
	Effort map[string]int `json:"effort,omitempty"`
}

// ModelClientUse is one row of the outcome comparison: a model, the client it
// ran under, the kind of work it was, and the check evidence and effort of the
// sessions that matched all three.
//
// The client is on the row because it is half the thing being compared. A model
// does not choose its tools, write its prompts or decide when to stop — the
// client does, and the same weights under two clients are two different
// arrangements. Naming only the model would credit or blame the wrong half.
//
// Model and Client are separate fields rather than one display string so
// nothing downstream has to split them back apart to group by either, and so
// the cross-machine merge keys on the pair rather than on a label somebody
// might reformat.
//
// Task is the observed task type, which describes what the session's TOOLS
// were, not what the member asked for. It is here so a split is possible where
// the sample supports one, and it is not a claim that a model is good at a kind
// of task. The pair's own row is the sum of its task rows.
type ModelClientUse struct {
	Model  string `json:"model,omitempty"`
	Client string `json:"client,omitempty"`
	Task   string `json:"task,omitempty"`
	CheckOutcome
}

// WorkDay is one day under one model. The model is on the row because the
// model-change report reads across a switch, and a day series that averaged
// two models together would hide the very step it exists to show.
type WorkDay struct {
	Date  string `json:"date"`
	Model string `json:"model,omitempty"`
	// PeakContext rides beside WorkTotals rather than inside it, like everywhere
	// else it travels: it is a level, and every field in WorkTotals is summed by
	// something. A day's peak is the fullest any one session got that day.
	PeakContext int `json:"peak_context,omitempty"`
	WorkTotals
}

// ToolUse is one tool's whole account of itself over the window: how often it
// was called, how many sessions reached for it, and under which harness, model
// and kind of task. The cross-tabs are what the registry can never hold — it
// sees tools org-wide and identity-free — and they are the point of the view
// that reads this.
type ToolUse struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Calls int    `json:"calls"`
	// Offers is how often this tool was asked to run, from PreToolUse. Calls
	// is how often it did. A tool with more offers than calls was refused,
	// escaped, or blocked by a hook, and the difference is the only honest
	// reading of "accepted" anywhere on this page.
	Offers int `json:"offers,omitempty"`
	// CallsOffered is Calls restricted to the sessions that counted offers,
	// which is the only denominator Offers may be divided by.
	CallsOffered int            `json:"calls_offered,omitempty"`
	Sessions     int            `json:"sessions"`
	Last         time.Time      `json:"last"`
	Harness      map[string]int `json:"harness,omitempty"`
	Model        map[string]int `json:"model,omitempty"`
	Task         map[string]int `json:"task,omitempty"`
	Project      map[string]int `json:"project,omitempty"`
	Days         map[string]int `json:"days,omitempty"`
	// DetailKind is the vocabulary behind this tool's name — programs, file
	// types, agents, skills — or empty for a tool that keeps none. It rides on
	// the row so the view can say WHICH tools open before a member clicks one,
	// and so a tool that keeps a vocabulary but recorded none of it this window
	// is distinguishable from one that never could.
	DetailKind string `json:"detail_kind,omitempty"`
	// Detail is the second level for this tool, busiest first, in whatever
	// vocabulary DetailKind names: the programs and subcommands a shell ran,
	// the kinds of file a reader or a search touched, the agents a delegation
	// handed to, the skills a member invoked. Empty for a tool that keeps none,
	// and empty for one whose calls all named none.
	Detail []WorkCount `json:"detail,omitempty"`
	// detail accumulates before it is sorted into Detail; unexported so it never
	// reaches the wire as a map whose order changes between reads.
	detail map[string]int
}

// DelegationUse is one thing a session handed work to — a subagent, a skill,
// an MCP server — and what the sessions that used it cost.
//
// The last part is stated carefully because the honest version is weaker than
// the one a reader wants. Cost and tokens are reported per SESSION, so nothing
// here knows what a single delegation cost. What it knows is which sessions
// reached for a thing and what those sessions came to, which is a real
// measurement and a different one: a session that called a subagent once and
// then worked for an hour is not an hour of subagent.
//
// It is still the finding. "The sessions where I delegate cost four times the
// ones where I don't" is something a member acts on the same day, and it is
// the natural end of the drill-down: a tool opens into what it ran, and a
// delegation opens into what its sessions came to.
type DelegationUse struct {
	Name string `json:"name"`
	// Kind is "agent", "skill" or "mcp" — the vocabulary capture already names
	// (DetailAgent, DetailSkill) plus the servers behind the MCP tools.
	Kind     string `json:"kind"`
	Calls    int    `json:"calls"`
	Sessions int    `json:"sessions"`
	// SessionCost and SessionTokens are the totals of the sessions that used
	// it, not of the delegation. The field names say "session" for that reason
	// and the view repeats it in words.
	SessionCost   float64 `json:"session_cost,omitempty"`
	SessionTokens int     `json:"session_tokens,omitempty"`
	SessionTurns  int     `json:"session_turns,omitempty"`
}

// ToolPair is two tools that appeared in the same session, and how often. It
// answers a question nothing else here can: which moves travel together. A pair
// is unordered and stored with the names sorted, so A-then-B and B-then-A are
// one row rather than two halves of one.
type ToolPair struct {
	A        string `json:"a"`
	B        string `json:"b"`
	Sessions int    `json:"sessions"`
}

// CommandUse is one program the shell ran, and how often. The kind comes from
// capture.ProgramKind so a member reads twenty program names as eight kinds of
// act — the same move the tool kinds make one level up.
type CommandUse struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Calls int    `json:"calls"`
}

// WorkCount is one counted name — a turn-duration bucket, a tool that was
// missed. The shape the merge already knows how to add up.
type WorkCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// WorkSummary is the payload /v1/hooks/sessions returns.
type WorkSummary struct {
	Window    string      `json:"window"`
	From      time.Time   `json:"from"`
	To        time.Time   `json:"to"`
	Totals    WorkTotals  `json:"totals"`
	Models    []ModelUse  `json:"models,omitempty"`
	Projects  []WorkGroup `json:"projects,omitempty"`
	TaskTypes []WorkGroup `json:"task_types,omitempty"`
	// Clients are the harnesses that fed this window, by wire name. The page
	// reads them twice: as a way to cut the table, and as the coverage
	// statement — a client absent from this list ran no sessions, which is a
	// different fact from a client OpenTacit cannot read.
	Clients []WorkGroup `json:"clients,omitempty"`
	// ModelClients is the outcome comparison, one row per (model, client,
	// observed task type). Empty where no session in the window was observed to
	// change anything, which the view reports as no evidence rather than as a
	// zero — the two read very differently and only one of them is a
	// measurement.
	ModelClients []ModelClientUse `json:"model_clients,omitempty"`
	// Landed is what `tacit usage --landed --publish` filed: of the lines an
	// agent wrote in a repository, how many are still in the branch. It is not
	// windowed with everything else here — each reading carries the window it
	// was taken over and the day it was asked for — because nothing refreshes
	// it on its own and a stale survival figure is worse than none.
	Landed []FiledLanded `json:"landed,omitempty"`
	Days   []WorkDay     `json:"days,omitempty"`
	// TokensReported and CostReported say whether any source actually reported
	// these. Without them a member reads a total of zero as a measurement of
	// zero, when it means nothing was measured at all.
	TokensReported bool `json:"tokens_reported"`
	CostReported   bool `json:"cost_reported"`
	// PeakContext is the fullest the model's context got in this window, and
	// ContextReported says whether anything measured it — without the flag a
	// zero reads as an empty window rather than as a harness that never said.
	// It is a maximum everywhere it travels: through the day rollups, through
	// the window summary, and through the browser's cross-machine merge.
	PeakContext     int  `json:"peak_context,omitempty"`
	ContextReported bool `json:"context_reported"`
	// PeakContextPct is the same level as a share of the window, which peak
	// tokens alone cannot give: the window size belongs to the model and the
	// harness, and only the status line says what it was.
	PeakContextPct int `json:"peak_context_pct,omitempty"`
	// LiveSessions is what the agent has open right now, which the log cannot
	// know: it records what has happened and not what is still happening. Only
	// a running agent fills it — on the sealed-ledger path there is nobody to
	// ask, and the view says nothing rather than claiming a zero.
	LiveSessions *int `json:"live_sessions,omitempty"`
	// QuotaHistory is the sampled climb of those same allowances (quota.go),
	// one series per provider per window. It rides with the summary rather than
	// being fetched on its own, because the page is read from a sealed ledger
	// as often as from a live agent and a drill-down that worked on one and not
	// the other would be a drill-down that mostly does not work.
	QuotaHistory []QuotaSeries `json:"quota_history,omitempty"`
	// WorkHistory is this machine's own turns and calls on the same beat, for
	// the chart that runs under the climb. Machine-wide, because a turn counter
	// cannot say which account paid for it.
	WorkHistory []WorkPoint `json:"work_history,omitempty"`
	// Quotas is one allowance per provider, read live rather than aggregated:
	// each is a level with an expiry and they live in their own file
	// (quota.go). Empty when nothing reported one, or when every window they
	// held has since reset. A member running Claude and Codex together has two,
	// and they are two allowances rather than one figure.
	Quotas []Quota `json:"quotas,omitempty"`
	// TurnTimes is the turn-duration distribution in TurnBuckets order, and
	// AbsentTools the tools a judge said were missed, busiest first. Both go out
	// as ordered lists rather than maps so the browser's cross-machine merge
	// combines them the way it combines every other keyed row.
	TurnTimes   []WorkCount `json:"turn_times,omitempty"`
	AbsentTools []WorkCount `json:"absent_tools,omitempty"`
	// Failures are the calls that failed, by kind, in FailureKinds order.
	// FailuresReported says whether any harness in this window reports failures
	// at all — without it, a harness that never says reads as a month where
	// nothing went wrong, which is the most flattering lie this page could tell.
	Failures         []WorkCount `json:"failures,omitempty"`
	FailuresReported bool        `json:"failures_reported"`
	// Hours is the window's turns by hour of the member's own clock, "00" to
	// "23" in order, and Lengths its sessions by how long each ran. The dates
	// everywhere else here are UTC; these two are not, and the page says so.
	Hours   []WorkCount `json:"hours,omitempty"`
	Lengths []WorkCount `json:"lengths,omitempty"`
	// Tools and ToolPairs come from the DETAIL records only: a session past the
	// thirty-day horizon is folded into a day row that keeps counts and loses
	// the names. DetailFrom above says where that line falls, and the tools view
	// says so in place rather than letting an older window look like a quiet one.
	Tools     []ToolUse  `json:"tools,omitempty"`
	ToolPairs []ToolPair `json:"tool_pairs,omitempty"`
	// Commands is what the shell tools actually ran, busiest first, each with
	// the kind of act it is. It turns one bar called Bash into the dozen things
	// Bash was.
	Commands []CommandUse `json:"commands,omitempty"`
	// Delegations are the subagents, skills and MCP servers the window reached
	// for, with the totals of the sessions that used each. From the full
	// records only, like Tools and for the same reason.
	Delegations []DelegationUse `json:"delegations,omitempty"`
	// Detail is how far back full records reach; everything older is day
	// rollups. Naming it is what stops "no sessions in March" being read as a
	// quiet March rather than a compacted one.
	DetailFrom time.Time `json:"detail_from"`
	// Change is the most recent switch of default model the history shows, with
	// the same measurements either side of it. Nil when nothing switched, or
	// when neither side has enough days to be worth comparing.
	Change *ModelChange `json:"change,omitempty"`
	// Repeats are the corrections this member has made more than once
	// (corrections.go). Counts and dates only — the ledger holds no wording,
	// so neither does this.
	Repeats []CorrectionRepeat `json:"repeats,omitempty"`
}

// CorrectionRepeat is one recurring correction, as counts. It deliberately
// carries no text: what the member said is not kept, only that they said the
// same thing again.
type CorrectionRepeat struct {
	Hash   string    `json:"hash"`
	Count  int       `json:"count"`
	First  time.Time `json:"first"`
	Last   time.Time `json:"last"`
	Raised bool      `json:"raised"`
}

// summarizeWork aggregates detail records and day rollups over the window.
// window<=0 means the full retained history.
func (s *sessionLog) summarizeWork(window time.Duration) WorkSummary {
	if s == nil {
		return WorkSummary{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	from := time.Time{}
	if window > 0 {
		from = now.Add(-window)
	}
	out := WorkSummary{
		Window: windowLabel(window), From: from, To: now,
		DetailFrom: now.Add(-sessionDetailRetention),
	}
	byModel := map[string]*ModelUse{}
	byProject, byTask := map[string]*WorkGroup{}, map[string]*WorkGroup{}
	// The client is the fourth dimension the restructure named and the only one
	// that never got a row of its own: it lived inside ModelUse, where it could
	// only be read a model at a time. It is also the one a member needs to read
	// the window's COVERAGE — which of their tools fed this, and therefore
	// whether a quiet week was quiet or merely unwired.
	byClient := map[string]*WorkGroup{}
	// The outcome comparison, keyed by all three of model, client and task
	// type. One session has exactly one of each, so a row is a disjoint set of
	// sessions and the pair's figures are its task rows added up — which is why
	// there is no second map for the pair.
	byPair := map[string]*ModelClientUse{}
	byDay := map[string]*WorkDay{}
	group := func(m map[string]*WorkGroup, key string, at time.Time) *WorkGroup {
		if key == "" {
			return &WorkGroup{} // a scratch row: unlabelled is not a group
		}
		g := m[key]
		if g == nil {
			g = &WorkGroup{Key: key, First: at, Last: at}
			m[key] = g
		}
		if at.Before(g.First) {
			g.First = at
		}
		if at.After(g.Last) {
			g.Last = at
		}
		return g
	}
	// A model row, with its cross-tabs ready. The unlabelled case returns a
	// scratch row rather than a nil one: every caller writes into the maps, and
	// "this session did not say which model" is not a model.
	modelRow := func(key string, at time.Time) *ModelUse {
		blank := func() *ModelUse {
			return &ModelUse{Harness: map[string]int{}, Task: map[string]int{},
				Project: map[string]int{}, TurnTimes: map[string]int{},
				Variants: map[string]int{}, Effort: map[string]int{}}
		}
		if key == "" {
			return blank()
		}
		m := byModel[key]
		if m == nil {
			m = blank()
			m.WorkGroup = WorkGroup{Key: key, First: at, Last: at}
			byModel[key] = m
		}
		if at.Before(m.First) {
			m.First = at
		}
		if at.After(m.Last) {
			m.Last = at
		}
		return m
	}
	// notePair files one set's check evidence under its (model, client, task).
	// A set that changed nothing files nothing: a row exists because work was
	// observed to change under that pair, and an empty row would be a
	// denominator made of sessions the question does not apply to.
	notePair := func(model, client, task string, c CheckOutcome) {
		if c.Changed == 0 {
			return
		}
		key := model + "\x00" + client + "\x00" + task
		p := byPair[key]
		if p == nil {
			p = &ModelClientUse{Model: model, Client: client, Task: task}
			byPair[key] = p
		}
		p.CheckOutcome.add(c)
	}
	notePeakDay := func(date, model string, peak int) {
		if peak <= 0 {
			return
		}
		if d := byDay[date+"\x00"+model]; d != nil && peak > d.PeakContext {
			d.PeakContext = peak
		}
	}
	day := func(date, model string, at time.Time) *WorkDay {
		key := date + "\x00" + model
		d := byDay[key]
		if d == nil {
			d = &WorkDay{Date: date, Model: model}
			byDay[key] = d
		}
		return d
	}
	// fold returns the model row it just added to, so the caller can hand it the
	// things that are not totals — a peak, a turn-time distribution, the label
	// the harness actually used.
	fold := func(date, model, harness, project, task string, at time.Time, t WorkTotals) *ModelUse {
		out.Totals.add(t)
		m := modelRow(model, at)
		m.add(t)
		bump(m.Harness, harness, t.Sessions)
		bump(m.Task, task, t.Sessions)
		bump(m.Project, project, t.Sessions)
		group(byProject, project, at).add(t)
		group(byTask, task, at).add(t)
		group(byClient, harness, at).add(t)
		day(date, model, at).add(t)
		if t.InTokens > 0 || t.OutTokens > 0 {
			out.TokensReported = true
		}
		if t.CostUSD > 0 {
			out.CostReported = true
		}
		return m
	}
	// Context is a level, so it rides beside fold rather than through it: the
	// summary keeps the fullest the window ever got, and nothing adds two of
	// them together.
	turnTimes, absent := map[string]int{}, map[string]int{}
	failures := map[string]int{}
	hours, lengths := map[string]int{}, map[string]int{}
	// Tools, cross-tabbed. Only the detail records carry tool names, so this
	// walks them alone; the day rollups below add nothing here and the view says
	// where the names stop.
	byTool := map[string]*ToolUse{}
	byDelegate := map[string]*DelegationUse{}
	pairs := map[string]int{}
	programs := map[string]int{}
	// Offers are counted over the same rows as calls, so a tool that was only
	// ever refused still gets a row — it is the row a member most wants.
	noteOffers := func(r *sessionRecord) {
		if len(r.Offered) == 0 {
			return
		}
		// The calls of THIS session, against its offers: the only pairing that
		// can be divided.
		for name, n := range r.Tools {
			if n <= 0 {
				continue
			}
			t := byTool[name]
			if t == nil {
				t = newToolRow(name)
				byTool[name] = t
			}
			t.CallsOffered += n
		}
		for name, n := range r.Offered {
			if name == "" || n <= 0 {
				continue
			}
			t := byTool[name]
			if t == nil {
				t = newToolRow(name)
				byTool[name] = t
			}
			t.Offers += n
		}
	}
	noteTools := func(r *sessionRecord) {
		day := r.End.UTC().Format("2006-01-02")
		names := make([]string, 0, len(r.Tools))
		for name, n := range r.Tools {
			if name == "" || n <= 0 {
				continue
			}
			names = append(names, name)
			t := byTool[name]
			if t == nil {
				t = newToolRow(name)
				byTool[name] = t
			}
			t.Calls += n
			t.Sessions++
			if r.End.After(t.Last) {
				t.Last = r.End.UTC()
			}
			bump(t.Harness, r.Harness, n)
			bump(t.Model, r.Model, n)
			bump(t.Task, r.TaskType, n)
			bump(t.Project, r.Project, n)
			bump(t.Days, day, n)
			for k, dn := range r.ToolDetail[name] {
				bump(t.detail, k, dn)
			}
		}
		// Every unordered pair in this session, counted once. Sorted first so a
		// pair is one row rather than two halves of one.
		sort.Strings(names)
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				pairs[names[i]+"\x00"+names[j]]++
			}
		}
	}
	// The spend join: what a session handed work to, against what that session
	// came to. Walks the detail records only — a compacted day keeps the counts
	// and loses the names, which is the same horizon the tool names have.
	noteDelegations := func(r *sessionRecord, t WorkTotals) {
		add := func(kind, name string, calls int) {
			if name == "" || calls <= 0 {
				return
			}
			key := kind + "\x00" + name
			d := byDelegate[key]
			if d == nil {
				d = &DelegationUse{Name: name, Kind: kind}
				byDelegate[key] = d
			}
			d.Calls += calls
			d.Sessions++
			d.SessionCost += t.CostUSD
			d.SessionTokens += t.InTokens + t.OutTokens
			d.SessionTurns += t.Turns
		}
		for tool, detail := range r.ToolDetail {
			switch capture.ToolDetailKind(tool) {
			case capture.DetailAgent:
				for name, n := range detail {
					add("agent", name, n)
				}
			case capture.DetailSkill:
				for name, n := range detail {
					add("skill", name, n)
				}
			}
		}
		// An MCP server is not a tool detail — it is the first half of the tool
		// NAME, which is all any harness gives. Counted per server because that
		// is the unit a member wired.
		byServer := map[string]int{}
		for tool, n := range r.Tools {
			if server := capture.MCPServer(tool); server != "" && n > 0 {
				byServer[server] += n
			}
		}
		for server, n := range byServer {
			add("mcp", server, n)
		}
	}
	notePeak := func(peak int) {
		if peak <= 0 {
			return
		}
		out.ContextReported = true
		if peak > out.PeakContext {
			out.PeakContext = peak
		}
	}
	// The share is a second level over the same question, and it arrives from
	// a different source — the status line, which knows the window size this
	// file never sees.
	notePeakPct := func(pct int) {
		if pct > out.PeakContextPct {
			out.PeakContextPct = pct
		}
	}
	for _, r := range s.recs {
		if !from.IsZero() && r.End.Before(from) {
			continue
		}
		calls, offers, callsOffered := 0, 0, 0
		for _, n := range r.Tools {
			calls += n
		}
		for _, n := range r.Offered {
			offers += n
		}
		if offers > 0 {
			callsOffered = calls
		}
		seconds := int(r.End.Sub(r.Start).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		// This session's own totals, held in a variable because two things need
		// them: the rollups fold them in, and the delegation join reads them as
		// they are. Reading them back off the model row instead would hand each
		// delegation the whole model's running total, which is the kind of
		// arithmetic that produces a session costing more than the window.
		mine := WorkTotals{Sessions: 1, Turns: r.Turns, ToolCalls: calls, ToolOffers: offers,
			ToolCallsOffered: callsOffered, Retries: r.Retries, Corrections: r.Corrections, Seconds: seconds,
			InTokens: r.InTokens, OutTokens: r.OutTokens,
			CacheWriteTokens: r.CacheWriteTokens, CacheReadTokens: r.CacheReadTokens,
			CostUSD: r.CostUSD, EstCostUSD: r.EstCostUSD,
			StatusSessions: boolCount(r.Status), LinesAdded: r.LinesAdded,
			LinesRemoved: r.LinesRemoved, ActiveSeconds: r.ActiveSeconds,
			CacheRequests: r.CacheRequests, CacheMisses: r.CacheMisses}
		m := fold(r.End.UTC().Format("2006-01-02"), r.Model, r.Harness, r.Project, r.TaskType,
			r.End.UTC(), mine)
		m.TurnTimes = addCounts(m.TurnTimes, r.TurnBuckets)
		bump(m.Variants, r.ModelRaw, 1)
		bump(m.Effort, r.Effort, 1)
		if r.PeakContext > m.PeakContext {
			m.PeakContext = r.PeakContext
		}
		notePeak(r.PeakContext)
		notePeakPct(r.ContextPct)
		noteOffers(r)
		noteDelegations(r, mine)
		// Built from the same calls and span this session's own totals were
		// built from, a line above, so the effort beside a pass rate is the
		// effort of exactly those sessions.
		notePair(r.Model, r.Harness, r.TaskType, checkOutcomeOf(*r, calls, seconds))
		notePeakDay(r.End.UTC().Format("2006-01-02"), r.Model, r.PeakContext)
		turnTimes = addCounts(turnTimes, r.TurnBuckets)
		absent = addCounts(absent, r.AbsentTools)
		failures = addCounts(failures, r.Failures)
		hours = addCounts(hours, r.Hours)
		if b := sessionLengthBucket(r.End.Sub(r.Start)); b != "" {
			lengths[b]++
		}
		noteTools(r)
	}
	for _, d := range s.days {
		at, err := time.Parse("2006-01-02", d.Date)
		if err != nil || (!from.IsZero() && at.Before(from.Truncate(24*time.Hour))) {
			continue
		}
		md := fold(d.Date, d.Model, d.Harness, d.Project, d.TaskType, at.UTC(),
			WorkTotals{Sessions: d.Sessions, Turns: d.Turns, ToolCalls: d.ToolCalls, ToolOffers: d.ToolOffers,
				ToolCallsOffered: d.ToolCallsOffered,
				Retries:          d.Retries, Corrections: d.Corrections, Seconds: d.Seconds,
				InTokens: d.InTokens, OutTokens: d.OutTokens,
				CacheWriteTokens: d.CacheWriteTokens, CacheReadTokens: d.CacheReadTokens,
				CostUSD: d.CostUSD, EstCostUSD: d.EstCostUSD,
				StatusSessions: d.StatusSessions, LinesAdded: d.LinesAdded,
				LinesRemoved: d.LinesRemoved, ActiveSeconds: d.ActiveSeconds,
				CacheRequests: d.CacheRequests, CacheMisses: d.CacheMisses})
		md.TurnTimes = addCounts(md.TurnTimes, d.TurnBuckets)
		if d.PeakContext > md.PeakContext {
			md.PeakContext = d.PeakContext
		}
		notePeak(d.PeakContext)
		notePeakPct(d.ContextPct)
		notePeakDay(d.Date, d.Model, d.PeakContext)
		notePair(d.Model, d.Harness, d.TaskType, d.Checks)
		turnTimes = addCounts(turnTimes, d.TurnBuckets)
		absent = addCounts(absent, d.AbsentTools)
		failures = addCounts(failures, d.Failures)
		hours = addCounts(hours, d.Hours)
		lengths = addCounts(lengths, d.Lengths)
	}
	out.Models = sortedModels(byModel)
	out.Projects = sortedGroups(byProject)
	out.TaskTypes = sortedGroups(byTask)
	out.Clients = sortedGroups(byClient)
	out.ModelClients = sortedOutcomeRows(byPair)
	out.Days = make([]WorkDay, 0, len(byDay))
	for _, d := range byDay {
		out.Days = append(out.Days, *d)
	}
	sort.Slice(out.Days, func(i, j int) bool {
		if out.Days[i].Date != out.Days[j].Date {
			return out.Days[i].Date < out.Days[j].Date
		}
		return out.Days[i].Model < out.Days[j].Model
	})
	// Turn times keep the bucket order, because "under 30s" before "over 10m"
	// is the only order that reads; absent tools go busiest first, like every
	// other counted vocabulary here.
	for _, b := range TurnBuckets {
		if n := turnTimes[b.Key]; n > 0 {
			out.TurnTimes = append(out.TurnTimes, WorkCount{Key: b.Key, Count: n})
		}
	}
	// The kinds keep their own order, like the turn buckets: "not found" before
	// "other" is the order that reads, and busiest-first would reshuffle the
	// list every time a bad afternoon moved one kind above another.
	for _, kind := range capture.FailureKinds {
		if n := failures[kind]; n > 0 {
			out.Failures = append(out.Failures, WorkCount{Key: kind, Count: n})
			out.FailuresReported = true
		}
	}
	// Hours run round the clock and lengths run short to long. Both keep their
	// own order rather than going busiest-first: a chart of the day that put
	// two in the afternoon next to nine in the morning would not be a day.
	for h := 0; h < 24; h++ {
		key := fmt.Sprintf("%02d", h)
		if n := hours[key]; n > 0 {
			out.Hours = append(out.Hours, WorkCount{Key: key, Count: n})
		}
	}
	for _, b := range SessionLengths {
		if n := lengths[b.Key]; n > 0 {
			out.Lengths = append(out.Lengths, WorkCount{Key: b.Key, Count: n})
		}
	}
	out.AbsentTools = sortedCounts(absent)
	out.Tools = sortedTools(byTool)
	// "Inside the shell" is the per-tool detail of every shell tool, grouped
	// back to the program. Derived rather than stored twice, so the two cannot
	// come to disagree about how often git ran.
	for _, t := range out.Tools {
		if !capture.ShellTool(t.Name) {
			continue
		}
		for _, d := range t.Detail {
			programs[capture.ProgramOf(d.Key)] += d.Count
		}
	}
	for _, c := range sortedCounts(programs) {
		out.Commands = append(out.Commands, CommandUse{
			Name: c.Key, Kind: capture.ProgramKind(c.Key), Calls: c.Count})
	}
	out.Delegations = sortedDelegations(byDelegate)
	out.ToolPairs = sortedPairs(pairs)
	out.Change = detectModelChange(out.Days)
	return out
}

// boolCount is 1 for a session that reported and 0 for one that did not, so a
// coverage denominator adds up the same way every other count here does.
func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

// newToolRow is one tool's blank account of itself. Two callers fill it — the
// calls that ran and the calls that were only offered — and a tool that was
// refused every time must get the same row shape as one that never was.
func newToolRow(name string) *ToolUse {
	return &ToolUse{Name: name, Kind: capture.ToolKind(name),
		DetailKind: capture.ToolDetailKind(name),
		Harness:    map[string]int{}, Model: map[string]int{},
		Task: map[string]int{}, Project: map[string]int{}, Days: map[string]int{},
		detail: map[string]int{}}
}

// bump adds to a counted map, skipping the empty key: a session that reported
// no project should not put its tools under a heading called "".
func bump(m map[string]int, key string, n int) {
	if key != "" {
		m[key] += n
	}
}

// sortedTools renders the tools busiest-first, ties by name so the order is
// stable between reads.
func sortedTools(m map[string]*ToolUse) []ToolUse {
	out := make([]ToolUse, 0, len(m))
	for _, t := range m {
		t.Detail = sortedCounts(t.detail)
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// sortedDelegations renders them by what the sessions using them came to,
// dearest first where anything measured a cost and busiest first where nothing
// did — which is the order that answers the question being asked, and the
// honest fallback when the question cannot be asked at all.
func sortedDelegations(m map[string]*DelegationUse) []DelegationUse {
	if len(m) == 0 {
		return nil
	}
	out := make([]DelegationUse, 0, len(m))
	priced := false
	for _, d := range m {
		if d.SessionCost > 0 {
			priced = true
		}
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if priced && out[i].SessionCost != out[j].SessionCost {
			return out[i].SessionCost > out[j].SessionCost
		}
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// sortedPairs renders co-occurrence commonest-first. Pairs seen in a single
// session are dropped: one session is a coincidence, and a list of them would
// bury the handful that are a habit.
func sortedPairs(m map[string]int) []ToolPair {
	out := make([]ToolPair, 0, len(m))
	for k, n := range m {
		if n < 2 {
			continue
		}
		a, b, _ := strings.Cut(k, "\x00")
		out = append(out, ToolPair{A: a, B: b, Sessions: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

// sortedCounts renders a counted vocabulary busiest-first, ties by name so the
// order is stable between reads.
func sortedCounts(m map[string]int) []WorkCount {
	out := make([]WorkCount, 0, len(m))
	for k, v := range m {
		out = append(out, WorkCount{Key: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// dropEmpty turns a map nothing was ever put in back into no map, so an absent
// dimension arrives absent rather than as an empty object.
func dropEmpty(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	return m
}

// sortedModels renders the models busiest-first, on the same rule sortedGroups
// uses, and drops the cross-tab maps that stayed empty so an absent dimension
// arrives absent rather than as an empty object the page has to test twice.
func sortedModels(m map[string]*ModelUse) []ModelUse {
	if len(m) == 0 {
		return nil
	}
	out := make([]ModelUse, 0, len(m))
	for _, g := range m {
		u := *g
		u.Harness = dropEmpty(u.Harness)
		u.Task = dropEmpty(u.Task)
		u.Project = dropEmpty(u.Project)
		u.TurnTimes = dropEmpty(u.TurnTimes)
		u.Variants = dropEmpty(u.Variants)
		u.Effort = dropEmpty(u.Effort)
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// sortedGroups renders a grouping busiest-first. Groups of one session are kept
// — a project touched once is a true fact about the window, and dropping it
// would quietly rewrite the total the rows are supposed to add up to.
func sortedGroups(m map[string]*WorkGroup) []WorkGroup {
	if len(m) == 0 {
		return nil
	}
	out := make([]WorkGroup, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// sortedOutcomeRows renders the outcome rows busiest-first by CHANGED SESSIONS, which
// is the same rule every other table here follows and is deliberately not a
// rank: it orders by how much evidence a row rests on, never by how well it
// did. Task mix, project mix and the member's own choice of when to reach for
// which tool are none of them controlled, so nothing here may put a model or a
// client above another one.
func sortedOutcomeRows(m map[string]*ModelClientUse) []ModelClientUse {
	if len(m) == 0 {
		return nil
	}
	out := make([]ModelClientUse, 0, len(m))
	for _, p := range m {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Changed != out[j].Changed {
			return out[i].Changed > out[j].Changed
		}
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		if out[i].Client != out[j].Client {
			return out[i].Client < out[j].Client
		}
		return out[i].Task < out[j].Task
	})
	return out
}

// --- the model-change report ---

// ModelChange is a switch of default model, with what changed across it.
//
// It exists because the event nobody announces is the one that invalidates a
// member's working habits: the default model moves, and part of what they had
// learned quietly stops paying. An organization can afford to notice this
// slowly, across a cohort. One person has no cohort, so the comparison has to
// be against their own record, which is exactly what this file holds.
type ModelChange struct {
	At         string     `json:"at"` // first day the new model led (YYYY-MM-DD)
	From       string     `json:"from"`
	To         string     `json:"to"`
	BeforeDays int        `json:"before_days"`
	AfterDays  int        `json:"after_days"`
	Before     WorkTotals `json:"before"`
	After      WorkTotals `json:"after"`
}

// modelChangeMinDays is how many days each side must cover before the two are
// worth comparing. Below it the "change" is a day of trying something out, and
// reporting a difference computed over one day either side would be a rate with
// no sample dressed as a finding.
const modelChangeMinDays = 3

// detectModelChange finds the most recent day the leading model changed and has
// enough history either side of it.
//
// Leading, not only: a member trying a second model for an afternoon has not
// switched, and a report that fired on the first session under a new label
// would cry wolf every time somebody experimented. The day's leader is the
// model that took the most turns that day.
func detectModelChange(days []WorkDay) *ModelChange {
	type dayLead struct {
		date  string
		model string
	}
	byDate := map[string][]WorkDay{}
	var order []string
	for _, d := range days {
		if d.Model == "" {
			continue
		}
		if _, seen := byDate[d.Date]; !seen {
			order = append(order, d.Date)
		}
		byDate[d.Date] = append(byDate[d.Date], d)
	}
	sort.Strings(order)
	leads := make([]dayLead, 0, len(order))
	for _, date := range order {
		best := WorkDay{}
		for _, d := range byDate[date] {
			if d.Turns > best.Turns || (d.Turns == best.Turns && d.Model < best.Model) {
				best = d
			}
		}
		if best.Model != "" {
			leads = append(leads, dayLead{date: date, model: best.Model})
		}
	}
	// Walk back from the end for the most recent transition with room either
	// side. The most recent is the one the member is living with.
	for i := len(leads) - 1; i > 0; i-- {
		if leads[i].model == leads[i-1].model {
			continue
		}
		before, after := leads[i-1].model, leads[i].model
		beforeDays, afterDays := 0, 0
		for j := i - 1; j >= 0 && leads[j].model == before; j-- {
			beforeDays++
		}
		for j := i; j < len(leads) && leads[j].model == after; j++ {
			afterDays++
		}
		if beforeDays < modelChangeMinDays || afterDays < modelChangeMinDays {
			continue
		}
		ch := &ModelChange{At: leads[i].date, From: before, To: after,
			BeforeDays: beforeDays, AfterDays: afterDays}
		for _, d := range days {
			switch {
			case d.Date < leads[i].date && d.Model == before:
				ch.Before.add(d.WorkTotals)
			case d.Date >= leads[i].date && d.Model == after:
				ch.After.add(d.WorkTotals)
			}
		}
		return ch
	}
	return nil
}

// ReadWorkSummary loads the member-local session log once and aggregates it —
// the read path for surfaces outside the live hook agent, matching
// ReadUsageSummaries. It reads the same files the agent writes, so the numbers
// agree whether the agent is up or not.
func ReadWorkSummary(stateDir string, window time.Duration) WorkSummary {
	detail, daily := sessionLogPaths(stateDir)
	sum := loadSessionLog(detail, daily, nil).summarizeWork(window)
	sum.Quotas = ReadQuota(stateDir, nil)
	sum.QuotaHistory = ReadQuotaHistory(stateDir, nil)
	sum.WorkHistory = ReadWorkHistory(stateDir, nil)
	sum.Landed = LandedFilings(stateDir, time.Now())
	return sum
}
