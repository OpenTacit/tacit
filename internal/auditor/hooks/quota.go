// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The account's rate-limit windows: how much of the five-hour and seven-day
// allowance is gone, and when each one resets.
//
// It lives in its own one-row file rather than on a session record, and the
// reason is the whole design. A session record is history: what happened, when,
// under which model, and it is true for ever. A quota is a level with an
// expiry — true for the next few hours and meaningless after that. Folding one
// into the other would mean rewriting history every time a number that expires
// moved, and reading a month-old session would tell you what the allowance was
// during it, which nobody has ever wanted to know.
//
// So: one row per PROVIDER, overwritten, holding both windows and when they
// reset. The read side drops a window whose reset has passed, because a stale
// quota is worse than no quota — the member acts on it, and the number they
// acted on was for an allowance that has since refilled.
//
// Per provider, because a member runs more than one. It was a single unnamed
// row, which was true while one harness reported and false the moment a second
// did: two allowances took turns in one plate and the timestamp was the only
// tell. Anthropic's allowance and OpenAI's are different allowances, and
// nothing may fold them — not the latest-wins rule that is right for two
// machines spending ONE of them, and not a sum.
//
// Source is the harness status line (cmd/tacit/statusline.go →
// POST /v1/hooks/statusline). Nothing here is uploaded and nothing here names
// anybody: two percentages and two timestamps.
package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/modelid"
)

// QuotaWindow is one allowance window: the share of it already spent, and the
// moment it refills.
type QuotaWindow struct {
	UsedPct  int       `json:"used_pct"`
	ResetsAt time.Time `json:"resets_at"`
}

// Quota is what a harness last said about one account's allowance, and when it
// said it. At is what the view reads back as "as of": a percentage with no
// timestamp beside it invites a member to trust an hour-old reading.
//
// Source is whose allowance this is, taken from the model the status line was
// rendering (modelid.Normalize): its vendor where one is recognised, and the
// model's own family where none is — so a provider nobody has heard of yet
// still keeps its own row rather than sharing one. SourceLabel is that key as a
// heading writes it, filled on the way out.
//
// The set is open on purpose. Vendors arrive faster than any list of them, and
// every member runs a different handful; nothing here may turn on recognising
// one, which is what a fixed list of providers would do.
type Quota struct {
	Source      string       `json:"source,omitempty"`
	SourceLabel string       `json:"source_label,omitempty"`
	At          time.Time    `json:"at"`
	FiveHour    *QuotaWindow `json:"five_hour,omitempty"`
	SevenDay    *QuotaWindow `json:"seven_day,omitempty"`
	// Spend is the third window the harness reports, and the only one that is
	// money rather than usage: it appears behind a Claude apps gateway and it
	// can pass 100. It keeps its own plate rather than joining the rolling
	// windows in one line — a cap on spending and a cap on usage are different
	// claims, and a reader who conflates them acts on the wrong one.
	Spend *QuotaWindow `json:"spend,omitempty"`
}

// quotaStore is the file: one row per source, addressed by it.
type quotaStore struct {
	BySource map[string]Quota `json:"by_source"`
}

func quotaPath(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "quota.json")
}

// quotaFile is the one-row store. A nil receiver is a no-op everywhere, so a
// machine with no state directory simply keeps no quota.
type quotaFile struct {
	mu          sync.Mutex // write is read-modify-write, and status lines arrive per request
	path        string
	historyPath string
	now         func() time.Time
	// What the last sampled point said, so the hot path decides whether one is
	// due without reading a file. In memory only: a restart costs one extra
	// point, which is a point that says the agent restarted.
	lastFiveAt, lastWeekAt, lastSpendAt map[string]time.Time
	lastFive, lastWeek, lastSpend       map[string]int
}

func newQuotaFile(stateDir string, now func() time.Time) *quotaFile {
	if now == nil {
		now = time.Now
	}
	return &quotaFile{path: quotaPath(stateDir), historyPath: quotaHistoryPath(stateDir), now: now,
		lastFiveAt: map[string]time.Time{}, lastWeekAt: map[string]time.Time{},
		lastSpendAt: map[string]time.Time{},
		lastFive:    map[string]int{}, lastWeek: map[string]int{}, lastSpend: map[string]int{}}
}

// write overwrites this source's row and leaves every other source alone. Best
// effort: a failed write costs the plate, never a render.
func (q *quotaFile) write(source string, v Quota) {
	if q == nil || q.path == "" {
		return
	}
	if v.FiveHour == nil && v.SevenDay == nil && v.Spend == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	v.Source, v.At = source, q.now().UTC()
	store := q.load()
	if store.BySource == nil {
		store.BySource = map[string]Quota{}
	}
	store.BySource[source] = v
	if body, err := json.Marshal(store); err == nil {
		_ = fsx.WriteFileAtomic(q.path, body, 0o600)
	}
}

// load reads the file as it stands. An absent or unreadable one is an empty
// store rather than an error: this is a level with an expiry, and the next
// status line rewrites it within seconds.
func (q *quotaFile) load() quotaStore {
	var store quotaStore
	raw, err := os.ReadFile(q.path)
	if err != nil {
		return store
	}
	if json.Unmarshal(raw, &store) == nil && store.BySource != nil {
		return store
	}
	// The shape before allowances were kept per provider: one unnamed row. It
	// is read back under the empty source, which renders exactly as it always
	// did, and the next status line replaces it with a named one.
	var one Quota
	if json.Unmarshal(raw, &one) == nil && (one.FiveHour != nil || one.SevenDay != nil) {
		store.BySource = map[string]Quota{"": one}
	}
	return store
}

// read returns every live allowance, ordered by source so the plates do not
// shuffle between renders, with every expired window dropped. An absent,
// unreadable or wholly expired file yields nothing at all, which the view
// renders as no plate rather than as a confident zero.
func (q *quotaFile) read() []Quota {
	if q == nil || q.path == "" {
		return nil
	}
	q.mu.Lock()
	store := q.load()
	q.mu.Unlock()
	now := q.now().UTC()
	var out []Quota
	named := false
	for source, v := range store.BySource {
		v.Source, v.SourceLabel = source, modelid.Label(source)
		if live := v.fresh(now); live != nil {
			out = append(out, *live)
			named = named || source != ""
		}
	}
	// An unnamed reading is dropped once a named one exists. It is either the
	// row written before allowances were kept per provider — the same
	// allowance, about to be restated under its own name — or a reading whose
	// model label nothing recognised. Either way a second unattributed plate
	// beside a named one tells a member nothing except that one of them might
	// be double-counting the other.
	if named {
		kept := out[:0]
		for _, v := range out {
			if v.Source != "" {
				kept = append(kept, v)
			}
		}
		out = kept
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// fresh drops the windows whose reset has already passed. Expired means the
// allowance has refilled and this reading no longer describes it — so it is
// not shown, rather than shown with a caveat nobody reads.
func (v Quota) fresh(now time.Time) *Quota {
	expired := func(w *QuotaWindow) bool {
		return w == nil || w.ResetsAt.IsZero() || !w.ResetsAt.After(now)
	}
	if expired(v.FiveHour) {
		v.FiveHour = nil
	}
	if expired(v.SevenDay) {
		v.SevenDay = nil
	}
	if expired(v.Spend) {
		v.Spend = nil
	}
	if v.FiveHour == nil && v.SevenDay == nil && v.Spend == nil {
		return nil
	}
	return &v
}

// ---- the sampled history ------------------------------------------------
//
// A level with an expiry answers "how much is left". It cannot answer "will it
// last", which is the question a member actually has at 2pm — and that one
// needs the shape of the climb, which nothing was keeping.
//
// So the same status line that overwrites the current row also drops a point
// into a log of its own, thinned two ways: at most one point per interval, and
// one whenever the percentage moves. A window that sat still for an hour costs
// twelve points; one being spent costs a point per whole percent, which is the
// resolution the curve is worth.
//
// The machine's own work is sampled on the same beat, as a series of its own
// rather than a field on each provider's points. It is machine-wide — a turn
// counter cannot say which account paid for it — so hanging it off one
// provider would split this machine's work between them by whichever happened
// to sample. It is still the only honest account of what went with the climb:
// the allowance belongs to an ACCOUNT and is spent from every machine and
// surface signed into it, while this log is one machine. The page says so
// rather than implying the two explain each other.
const (
	quotaFiveInterval  = 5 * time.Minute
	quotaWeekInterval  = 30 * time.Minute
	quotaSpendInterval = 2 * time.Hour
	quotaFiveHorizon   = 6 * time.Hour
	quotaWeekHorizon   = 8 * 24 * time.Hour
	// A spend period runs to a month, so it is sampled a tenth as often and
	// kept ten times as long: about four hundred points, the same order as the
	// week's, for a window twelve times the length.
	quotaSpendHorizon = 32 * 24 * time.Hour
)

// QuotaPoint is one reading of one window.
type QuotaPoint struct {
	At  time.Time `json:"at"`
	Pct int       `json:"pct"`
}

// WorkPoint is what this machine did between two readings.
type WorkPoint struct {
	At        time.Time `json:"at"`
	Turns     int       `json:"turns,omitempty"`
	ToolCalls int       `json:"tool_calls,omitempty"`
}

// QuotaSeries is one provider's history, one series per window.
type QuotaSeries struct {
	Source      string       `json:"source,omitempty"`
	SourceLabel string       `json:"source_label,omitempty"`
	Five        []QuotaPoint `json:"five,omitempty"`
	Week        []QuotaPoint `json:"week,omitempty"`
	Spend       []QuotaPoint `json:"spend,omitempty"`
}

type quotaHistory struct {
	BySource map[string]QuotaSeries `json:"by_source"`
	// Work is this machine's own, beside every provider rather than inside one.
	Work []WorkPoint `json:"work,omitempty"`
	// The running totals the last point was differenced against, kept here so a
	// restart picks up where the file left off — and the agent restarts every
	// fifteen quiet minutes. Seeded says they mean it: the first sample a
	// machine ever takes has nothing to difference against, and without it every
	// turn the log has ever held would be filed as work done in one bucket.
	LastTurns int  `json:"last_turns,omitempty"`
	LastCalls int  `json:"last_calls,omitempty"`
	Seeded    bool `json:"seeded,omitempty"`
}

func quotaHistoryPath(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "quota-history.json")
}

// sample records this reading where it says something the last one did not.
//
// work returns the machine's running totals, and it is a function because the
// answer costs a walk of the session log: a status line renders several times a
// second and almost none of them are due a point.
func (q *quotaFile) sample(source string, v Quota, work func() (turns, calls int)) {
	if q == nil || q.historyPath == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now().UTC()
	fiveDue := q.due(source, v.FiveHour, q.lastFive, q.lastFiveAt, quotaFiveInterval, now)
	weekDue := q.due(source, v.SevenDay, q.lastWeek, q.lastWeekAt, quotaWeekInterval, now)
	spendDue := q.due(source, v.Spend, q.lastSpend, q.lastSpendAt, quotaSpendInterval, now)
	if !fiveDue && !weekDue && !spendDue {
		return
	}
	hist := q.loadHistory()
	if work != nil {
		turns, calls := work()
		// The DELTA since the last point, so a chart adds them per bucket
		// without having to reason about a total that can also shrink (the
		// session log folds its oldest records into days and its running sum
		// goes down with them). A total that went backwards contributes nothing
		// rather than a negative column.
		dt, dc := turns-hist.LastTurns, calls-hist.LastCalls
		if !hist.Seeded {
			dt, dc = 0, 0
		}
		if dt < 0 {
			dt = 0
		}
		if dc < 0 {
			dc = 0
		}
		hist.LastTurns, hist.LastCalls, hist.Seeded = turns, calls, true
		hist.Work = trimWork(hist.Work, now.Add(-quotaFiveHorizon))
		if dt > 0 || dc > 0 {
			hist.Work = append(hist.Work, WorkPoint{At: now, Turns: dt, ToolCalls: dc})
		}
	}
	series := hist.BySource[source]
	if fiveDue {
		series.Five = append(trim(series.Five, now.Add(-quotaFiveHorizon)),
			QuotaPoint{At: now, Pct: v.FiveHour.UsedPct})
		q.lastFive[source], q.lastFiveAt[source] = v.FiveHour.UsedPct, now
	}
	if weekDue {
		series.Week = append(trim(series.Week, now.Add(-quotaWeekHorizon)),
			QuotaPoint{At: now, Pct: v.SevenDay.UsedPct})
		q.lastWeek[source], q.lastWeekAt[source] = v.SevenDay.UsedPct, now
	}
	if spendDue {
		series.Spend = append(trim(series.Spend, now.Add(-quotaSpendHorizon)),
			QuotaPoint{At: now, Pct: v.Spend.UsedPct})
		q.lastSpend[source], q.lastSpendAt[source] = v.Spend.UsedPct, now
	}
	if hist.BySource == nil {
		hist.BySource = map[string]QuotaSeries{}
	}
	hist.BySource[source] = series
	if body, err := json.Marshal(hist); err == nil {
		_ = fsx.WriteFileAtomic(q.historyPath, body, 0o600)
	}
}

// due says whether one window has earned a point: one it has never had, one
// whose level has moved, or one the interval has come round for. A window the
// harness did not report has nothing to say and is not invented.
func (q *quotaFile) due(source string, w *QuotaWindow, pcts map[string]int,
	ats map[string]time.Time, every time.Duration, now time.Time) bool {
	if w == nil {
		return false
	}
	if pct, seen := pcts[source]; !seen || pct != w.UsedPct {
		return true
	}
	at, ok := ats[source]
	return !ok || !now.Before(at.Add(every))
}

func trimWork(points []WorkPoint, from time.Time) []WorkPoint {
	keep := points[:0]
	for _, p := range points {
		if p.At.After(from) {
			keep = append(keep, p)
		}
	}
	return keep
}

func trim(points []QuotaPoint, from time.Time) []QuotaPoint {
	keep := points[:0]
	for _, p := range points {
		if p.At.After(from) {
			keep = append(keep, p)
		}
	}
	return keep
}

// ReadWorkHistory is this machine's own work on the same beat as the readings,
// for the chart that runs under them.
func ReadWorkHistory(stateDir string, now func() time.Time) []WorkPoint {
	q := newQuotaFile(stateDir, now)
	if q.historyPath == "" {
		return nil
	}
	q.mu.Lock()
	hist := q.loadHistory()
	q.mu.Unlock()
	return trimWork(hist.Work, q.now().UTC().Add(-quotaFiveHorizon))
}

// ReadQuotaHistory is the sampled history, ordered by source like the readings
// themselves. Every point that is still inside its window's horizon.
func ReadQuotaHistory(stateDir string, now func() time.Time) []QuotaSeries {
	q := newQuotaFile(stateDir, now)
	if q.historyPath == "" {
		return nil
	}
	q.mu.Lock()
	hist := q.loadHistory()
	q.mu.Unlock()
	cut := q.now().UTC()
	var out []QuotaSeries
	for source, series := range hist.BySource {
		series.Source, series.SourceLabel = source, modelid.Label(source)
		series.Five = trim(series.Five, cut.Add(-quotaFiveHorizon))
		series.Week = trim(series.Week, cut.Add(-quotaWeekHorizon))
		series.Spend = trim(series.Spend, cut.Add(-quotaSpendHorizon))
		if len(series.Five) == 0 && len(series.Week) == 0 && len(series.Spend) == 0 {
			continue
		}
		out = append(out, series)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

func (q *quotaFile) loadHistory() quotaHistory {
	var hist quotaHistory
	raw, err := os.ReadFile(q.historyPath)
	if err != nil {
		return hist
	}
	_ = json.Unmarshal(raw, &hist)
	return hist
}

// ReadQuota answers from the file alone, for the surfaces that read the
// member-local state without a running agent (FileSummarizer, `tacit usage`).
func ReadQuota(stateDir string, now func() time.Time) []Quota {
	return newQuotaFile(stateDir, now).read()
}
