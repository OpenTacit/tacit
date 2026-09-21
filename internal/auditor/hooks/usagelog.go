// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Member-local usage log: this member's OWN OpenTacit activity over time —
// queries made, suggestions shown, adopted, helped, dismissed, questions
// offered — on the member's machine and nowhere else.
//
// It exists for the same reason techniquememory.go does: "cohorts, never
// identities" forbids the registry from attributing any of this to a person
// (feedback events carry only cohort tags, and the funnel is aggregated
// org-wide). So a member's own "how am I using OpenTacit" overview — the Usage
// view in the dashboard's avatar menu — cannot be materialised server-side.
// It can only be re-materialised HERE, from the very events the registry
// deliberately forgets the "who" of, kept locally and read by the UI just for
// rendering.
//
// Same locality contract as techniquememory.go: written only by the member's own
// hook agent (0600), append-only JSONL, pruned by age, never uploaded. What it
// stores is deliberately thin — a timestamp, an event kind, and (for techniques) a
// technique id + name — no prompts, no transcripts, no identity.
package hooks

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/windows"
)

// usageRetention bounds how far back the personal history reaches. Longer than
// techniqueMemoryTTL because here the history IS the surface — trends over a season
// are the point — but still bounded so the on-disk log can never grow without
// limit.
const usageRetention = 180 * 24 * time.Hour

// Usage event kinds. shown/adopted/helped/dismissed mirror the funnel stages
// (fed straight from the same drafts posted to the registry); query counts
// member turns (a local-only signal the registry never receives); offered
// counts OpenTacit AskUserQuestion prompts.
const (
	usageQuery     = "query"
	usageShown     = "shown"
	usageAdopted   = "adopted"
	usageHelped    = "helped"
	usageDismissed = "dismissed"
	usageOffered   = "offered"
)

type usageEvent struct {
	TS      time.Time `json:"ts"`
	Kind    string    `json:"kind"`
	Cap     string    `json:"cap,omitempty"`     // technique id, for technique-scoped kinds
	Name    string    `json:"name,omitempty"`    // technique name, recorded on `shown`
	Harness string    `json:"harness,omitempty"` // surface the event came from
	// Model is the canonical model cohort (internal/modelid) the event happened
	// under. It costs one field and answers the question a member switching
	// between models cannot otherwise ask: did this technique keep working?
	// The registry can answer it too, now that model is a dimension — but only
	// org-wide, and one member's answer is not visible in an org-wide rate.
	// Here the whole sample is theirs, which is the point of the file.
	Model string `json:"model,omitempty"`
}

type usageLog struct {
	mu   sync.Mutex
	path string // "" -> in-memory only (tests, or no home dir)
	now  func() time.Time
	evs  []usageEvent
}

// loadUsageLog reads the member's on-disk history, tolerating absence and
// corruption alike (a broken line is skipped, never fatal), and compacts it:
// events past the retention horizon are dropped and the file rewritten. A
// missing home dir degrades to in-memory only — the view simply shows nothing
// rather than the agent breaking.
func loadUsageLog(path string, now func() time.Time) *usageLog {
	if now == nil {
		now = time.Now
	}
	u := &usageLog{path: path, now: now}
	if path == "" {
		return u
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return u
	}
	cutoff := now().Add(-usageRetention)
	compact := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev usageEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			compact = true // drop the unparseable line on the next rewrite
			continue
		}
		if ev.TS.Before(cutoff) {
			compact = true
			continue
		}
		u.evs = append(u.evs, ev)
	}
	if compact {
		u.rewriteLocked()
	}
	return u
}

// append records one event: in memory always, and appended as one JSONL line
// to the on-disk log when persisted. Best-effort — a write failure costs a
// line of local history, never a turn (a nil receiver is a no-op so callers
// need not guard). The timestamp is stamped here from the agent's injectable
// clock so tests stay deterministic.
func (u *usageLog) append(ev usageEvent) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	ev.TS = u.now()
	u.evs = append(u.evs, ev)
	if u.path == "" {
		return
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f, err := os.OpenFile(u.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// rewriteLocked persists the in-memory events atomically, compacting away
// anything append-only growth or pruning left behind. A failure loses the
// compaction, not the log, so it is not worth failing a turn over — but it is
// worth saying, because a log that stops shrinking is otherwise silent. Caller
// holds u.mu.
func (u *usageLog) rewriteLocked() {
	if u.path == "" {
		return
	}
	var b strings.Builder
	for _, ev := range u.evs {
		line, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := fsx.WriteFileAtomic(u.path, []byte(b.String()), 0o600); err != nil {
		log.Printf("[tacit-hooks] usage log not compacted into %s: %v", u.path, err)
	}
}

// --- read side: aggregation for the Usage view ---

// UsageTotals is the headline funnel over the window.
type UsageTotals struct {
	Queries   int `json:"queries"`
	Shown     int `json:"shown"`
	Adopted   int `json:"adopted"`
	Helped    int `json:"helped"`
	Dismissed int `json:"dismissed"`
	Offered   int `json:"offered"`
}

// UsageDay is one calendar day's counts, for the trend chart.
type UsageDay struct {
	Date    string `json:"date"` // YYYY-MM-DD (UTC)
	Queries int    `json:"queries"`
	Shown   int    `json:"shown"`
	Adopted int    `json:"adopted"`
	Helped  int    `json:"helped"`
}

// UsageTechnique is one technique's funnel, for the drill-down table.
type UsageTechnique struct {
	TechniqueID string `json:"cap"` // wire/on-disk key frozen as "cap"
	Name        string `json:"name,omitempty"`
	Shown       int    `json:"shown"`
	Adopted     int    `json:"adopted"`
	Helped      int    `json:"helped"`
	Dismissed   int    `json:"dismissed"`
	// PrevAdopted counts adoptions in the PREVIOUS equal window, and
	// LastAdopted is when the member last used the technique at all. Together
	// they are the difference between a technique that never caught on and one
	// that did and then stopped — which is either decay or forgetting, and the
	// member is the only one who can tell which.
	PrevAdopted int       `json:"prev_adopted"`
	LastAdopted time.Time `json:"last_adopted,omitempty"`
}

// UsageModel is one model's funnel over the window — the breakdown the
// registry cannot give a member, because org-wide rates say nothing about one
// person and this file's whole sample is one person.
type UsageModel struct {
	Model     string    `json:"model"`
	Queries   int       `json:"queries"`
	Shown     int       `json:"shown"`
	Adopted   int       `json:"adopted"`
	Helped    int       `json:"helped"`
	Dismissed int       `json:"dismissed"`
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
}

// UsageSummary is the whole payload the local endpoint returns and the Usage
// view renders. Aggregated on read so the on-disk log stays a plain event
// stream and never has to hold derived state.
type UsageSummary struct {
	Window     string           `json:"window"`
	From       time.Time        `json:"from"`
	To         time.Time        `json:"to"`
	Totals     UsageTotals      `json:"totals"`
	Series     []UsageDay       `json:"series"`
	Techniques []UsageTechnique `json:"techniques"`
	// Models is present only when the log holds more than one — a single-model
	// breakdown is a table with one row that repeats the totals above it, and a
	// panel with nothing to say should say nothing rather than draw a grid.
	Models []UsageModel `json:"models,omitempty"`
}

// summarize aggregates the events within [now-window, now] into the payload.
// Technique names are resolved from `shown` events in the log itself, so the
// drill-down is labelled without any registry round-trip. window<=0 means the
// full retained history.
func (u *usageLog) summarize(window time.Duration) UsageSummary {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.now()
	from := time.Time{}
	if window > 0 {
		from = now.Add(-window)
	}
	sum := UsageSummary{
		Window: windowLabel(window), From: from.UTC(), To: now.UTC(),
	}
	// The previous equal window, for the one comparison a single member can
	// honestly make: themselves, earlier. An "all" window has no previous, and
	// says so by leaving the comparison empty rather than inventing a baseline.
	prevFrom := time.Time{}
	if window > 0 {
		prevFrom = from.Add(-window)
	}
	days := map[string]*UsageDay{}
	techniques := map[string]*UsageTechnique{}
	models := map[string]*UsageModel{}
	names := map[string]string{}
	// First pass: names from every shown event (survives across the window so a
	// technique adopted today but shown yesterday is still labelled).
	for _, ev := range u.evs {
		if ev.Kind == usageShown && ev.Name != "" {
			names[ev.Cap] = ev.Name
		}
	}
	technique := func(id string) *UsageTechnique {
		c := techniques[id]
		if c == nil {
			c = &UsageTechnique{TechniqueID: id, Name: names[id]}
			techniques[id] = c
		}
		return c
	}
	model := func(key string, at time.Time) *UsageModel {
		if key == "" {
			return &UsageModel{} // a scratch row: an unlabelled turn is not a model
		}
		m := models[key]
		if m == nil {
			m = &UsageModel{Model: key, First: at}
			models[key] = m
		}
		if at.Before(m.First) {
			m.First = at
		}
		if at.After(m.Last) {
			m.Last = at
		}
		return m
	}
	day := func(t time.Time) *UsageDay {
		key := t.UTC().Format("2006-01-02")
		d := days[key]
		if d == nil {
			d = &UsageDay{Date: key}
			days[key] = d
		}
		return d
	}
	for _, ev := range u.evs {
		if !from.IsZero() && ev.TS.Before(from) {
			// Still in scope for the previous-window comparison and for "when
			// did I last use this", both of which read behind the window.
			if ev.Kind == usageAdopted && ev.Cap != "" {
				c := technique(ev.Cap)
				if ev.TS.After(c.LastAdopted) {
					c.LastAdopted = ev.TS.UTC()
				}
				if !prevFrom.IsZero() && !ev.TS.Before(prevFrom) {
					c.PrevAdopted++
				}
			}
			continue
		}
		d := day(ev.TS)
		m := model(ev.Model, ev.TS.UTC())
		switch ev.Kind {
		case usageQuery:
			sum.Totals.Queries++
			d.Queries++
			m.Queries++
		case usageShown:
			sum.Totals.Shown++
			d.Shown++
			technique(ev.Cap).Shown++
			m.Shown++
		case usageAdopted:
			sum.Totals.Adopted++
			d.Adopted++
			c := technique(ev.Cap)
			c.Adopted++
			if ev.TS.After(c.LastAdopted) {
				c.LastAdopted = ev.TS.UTC()
			}
			m.Adopted++
		case usageHelped:
			sum.Totals.Helped++
			d.Helped++
			technique(ev.Cap).Helped++
			m.Helped++
		case usageDismissed:
			sum.Totals.Dismissed++
			technique(ev.Cap).Dismissed++
			m.Dismissed++
		case usageOffered:
			sum.Totals.Offered++
		}
	}
	// Series: sorted ascending by date.
	sum.Series = make([]UsageDay, 0, len(days))
	for _, d := range days {
		sum.Series = append(sum.Series, *d)
	}
	sort.Slice(sum.Series, func(i, j int) bool { return sum.Series[i].Date < sum.Series[j].Date })
	// Techniques: most-shown first, then adopted, then name — the drill-down order.
	sum.Techniques = make([]UsageTechnique, 0, len(techniques))
	for _, c := range techniques {
		sum.Techniques = append(sum.Techniques, *c)
	}
	sort.Slice(sum.Techniques, func(i, j int) bool {
		a, b := sum.Techniques[i], sum.Techniques[j]
		if a.Shown != b.Shown {
			return a.Shown > b.Shown
		}
		if a.Adopted != b.Adopted {
			return a.Adopted > b.Adopted
		}
		return a.Name < b.Name
	})
	// Models: busiest first. Held back entirely below two, because one row
	// restating the totals above it is a grid pretending to be a comparison.
	if len(models) > 1 {
		sum.Models = make([]UsageModel, 0, len(models))
		for _, m := range models {
			sum.Models = append(sum.Models, *m)
		}
		sort.Slice(sum.Models, func(i, j int) bool {
			a, b := sum.Models[i], sum.Models[j]
			if a.Queries != b.Queries {
				return a.Queries > b.Queries
			}
			return a.Model < b.Model
		})
	}
	return sum
}

// ReadUsageSummaries loads the member-local usage log once and aggregates it
// over each window — the read path for surfaces outside the live hook agent
// (the tacit_usage MCP app renders from these). It reads the same on-disk log
// the agent writes, so a member sees identical numbers whether the agent is up
// or idle; the wall clock is fine here, the data being the member's own.
func ReadUsageSummaries(path string, windows []time.Duration) []UsageSummary {
	return readUsageSummariesAt(path, windows, nil)
}

// readUsageSummariesAt is ReadUsageSummaries with an injectable clock. The
// windows are relative to "now", so a test that writes events at a fixed time
// and reads them through the wall clock is a test with an expiry date — it
// passes until the fixture ages out of the window, then fails forever. Reading
// and writing have to agree on what time it is.
func readUsageSummariesAt(path string, windows []time.Duration, now func() time.Time) []UsageSummary {
	u := loadUsageLog(path, now)
	out := make([]UsageSummary, 0, len(windows))
	for _, w := range windows {
		out = append(out, u.summarize(w))
	}
	return out
}

// ParseUsageWindow maps a compact token to a duration for UsageSummary. The
// presets come from internal/windows, the list every OpenTacit surface offers; this
// path also takes any "<n>d"/"<n>w" a member types ("12w") and the aliases
// "max"/"0". Empty or unrecognized gives the default preset; "all" means the
// full retained history. A window is clamped to the retention horizon —
// nothing older is on disk to show.
func ParseUsageWindow(s string) time.Duration {
	s = strings.TrimSpace(strings.ToLower(s))
	switch {
	case s == "":
		return clampUsageWindow(windows.Duration(windows.Default))
	case s == "max" || s == "0":
		return 0
	case windows.Valid(s):
		return clampUsageWindow(windows.Duration(s))
	}
	unit := time.Duration(0)
	switch s[len(s)-1] {
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	default:
		return clampUsageWindow(windows.Duration(windows.Default))
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return clampUsageWindow(windows.Duration(windows.Default))
	}
	return clampUsageWindow(time.Duration(n) * unit)
}

// clampUsageWindow holds a window to what the log still keeps. 0 ("all") is
// already the whole history and passes through.
func clampUsageWindow(d time.Duration) time.Duration {
	if d > usageRetention {
		return usageRetention
	}
	return d
}

// windowLabel renders a duration as the compact token the query used.
func windowLabel(window time.Duration) string {
	if window <= 0 {
		return "all"
	}
	return strconv.Itoa(int(window/(24*time.Hour))) + "d"
}

// usageThrottleState is the suggestion budget as the log remembers it: when
// the member was last shown something, how many of their turns have passed
// since, and the timestamps still inside the rolling window.
//
// It exists so the throttle survives the daemon. The hook agent idle-exits
// after ~15 minutes and holds its state in memory, so anything counted only
// there is refunded by any pause longer than a coffee break — which is exactly
// how the per-session cap this replaced came to permit seven suggestions in a
// day while claiming a limit of three.
//
// Both clocks are reconstructible here because the log already records what
// each needs: `shown` for the suggestions and `query` for the member's turns.
// That is the whole reason the throttle reads from this file rather than
// keeping a state file of its own — no second on-disk artifact, no second
// privacy surface, and no way for the two to disagree.
type usageThrottleState struct {
	LastShown  time.Time
	TurnsSince int
	InWindow   []time.Time
}

// throttleState replays the retained history into a budget. Events are held in
// arrival order, which is timestamp order for a log only ever appended to by
// one agent at a time.
func (u *usageLog) throttleState(window time.Duration) usageThrottleState {
	if u == nil {
		return usageThrottleState{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	st := usageThrottleState{TurnsSince: 1 << 20}
	cut := u.now().Add(-window)
	for _, ev := range u.evs {
		switch ev.Kind {
		case usageShown:
			st.LastShown = ev.TS
			st.TurnsSince = 0 // turns are counted from the LAST one shown
			if ev.TS.After(cut) {
				st.InWindow = append(st.InWindow, ev.TS)
			}
		case usageQuery:
			// Turns before the first suggestion are not owed against anything,
			// so the sentinel is left alone until something has been shown.
			if st.TurnsSince != 1<<20 {
				st.TurnsSince++
			}
		}
	}
	return st
}
