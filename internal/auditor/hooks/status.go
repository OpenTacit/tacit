// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// What the agent reports about itself: the counters behind the status line and
// the E2 funnel, the session review list, the health of the registry and the
// model, and the three background caches (draft count, access mode, cohort
// directory) that keep a stats read off the network.
package hooks

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/product"

	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/llm"
)

// noteRegistry records the outcome of one registry call (retrieval or
// feedback post). Classification is structural: an HTTPStatus() 401/403 means
// the registry answered and said no — a credential fault a human must fix —
// while any other error is an outage, usually transient.
func (a *Agent) noteRegistry(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.regState, a.regConsecErrs, a.regOKAt = "ok", 0, time.Now()
		return
	}
	state := "unreachable"
	var httpErr interface{ HTTPStatus() int }
	if errors.As(err, &httpErr) {
		if code := httpErr.HTTPStatus(); code == 401 || code == 403 {
			state = "unauthorized"
		} else {
			state = "ok" // the registry answered; a 4xx/5xx on one call is not connectivity
		}
	}
	if state == "ok" {
		return // don't overwrite a standing fault with a soft error, or vice versa
	}
	a.regState, a.regConsecErrs, a.regErrorAt = state, a.regConsecErrs+1, time.Now()
}

// RegistryHealth reports the agent's view of its registry: the state, whether
// a human must act (a rejected key always; connectivity only once persistent),
// and the timestamps that let a reader judge staleness.
func (a *Agent) RegistryHealth() (state string, needsOperator bool, lastError, lastOK time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.regState == "" {
		return "unknown", false, a.regErrorAt, a.regOKAt
	}
	needs := a.regState == "unauthorized" ||
		(a.regState == "unreachable" && a.regConsecErrs >= 3)
	return a.regState, needs, a.regErrorAt, a.regOKAt
}

// noteLLM records the outcome of one synthesis call. Called on every fit-check,
// success or failure, so /v1/hooks/stats can tell an operator what is wrong and
// what to do about it (docs/harness/in-harness-hooks.md).
func (a *Agent) noteLLM(err error) {
	d := llm.Diagnose(err)
	// A missing key is the one degraded state that produces NO error: the client
	// silently answers from the heuristic, which never fails. Counting errors
	// alone would therefore report a perfectly healthy system that is running no
	// fit-check at all — a health signal that lies is worse than none, so ask the
	// client whether it is actually reaching the model.
	if nk, ok := a.noKeyDiagnosis(); ok {
		d = nk
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if d.State == "ok" {
		a.llmState, a.llmOKAt = d, time.Now()
		return
	}
	a.llmState, a.llmErrors, a.llmErrorAt = d, a.llmErrors+1, time.Now()
}

// noKeyDiagnosis is the state of a member who has no model key: reported as a
// mode rather than inferred from an error, because it raises none.
func (a *Agent) noKeyDiagnosis() (llm.Diagnosis, bool) {
	u, ok := a.llm.(interface{ UpgradeAvailable() bool })
	if !ok || !u.UpgradeAvailable() {
		return llm.Diagnosis{}, false
	}
	return llm.Diagnosis{State: "no-key", NeedsOperator: true,
		Detail: "suggestions are retrieved and ranked, but not fit-checked against the turn, because no model key is set",
		Remedy: "Put a TACIT_LLM_API_KEY line in ~/.tacit-key.env to turn fit-checking on. Asking " + product.Name() + " directly works either way."}, true
}

// LLMHealth reports the model's state, with the remedy when a human must act.
//
// A member who has just connected has attempted no synthesis, so nothing has
// diagnosed anything yet — and the honest answer to "what is running" is not
// "unknown" when the key file is plainly empty. Silence in the first session is
// exactly where a member concludes OpenTacit has nothing to say, so the missing key
// is reported from the key's absence rather than waiting for a turn to prove it.
func (a *Agent) LLMHealth() (d llm.Diagnosis, errors int, lastError, lastOK time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.llmState.State == "" {
		if nk, ok := a.noKeyDiagnosis(); ok {
			return nk, 0, a.llmErrorAt, a.llmOKAt
		}
		return llm.Diagnosis{State: "unknown"}, 0, a.llmErrorAt, a.llmOKAt
	}
	return a.llmState, a.llmErrors, a.llmErrorAt, a.llmOKAt
}

// DraftsTTL is how long a cached draft count is served before an async refresh
// is triggered on the next stats read.
const DraftsTTL = 60 * time.Second

// AccessTTL is how long a cached access mode is served before an async refresh.
// Longer than the draft count: an instance changes from private to global once,
// not every minute.
const AccessTTL = 5 * time.Minute

// CohortsTTL is how long a cached cohort directory is served before a
// background refresh. Long: cohort names are the slowest-moving fact in the
// registry, and the ask that reads them fires once a day at most.
const CohortsTTL = 6 * time.Hour

// asyncCached is a registry fact the agent serves from memory: reads never
// wait on the network, a stale value triggers at most one background refresh,
// and a refresh that fails keeps the last known value in service. That last
// part is the point — the alternative is a status line that goes blank every
// time the registry blinks.
//
// It carries its own mutex, so a refresh landing never contends with the hook
// path, and holds it only around the fields: fetch runs on the caller's
// goroutine (run), never under the lock.
type asyncCached[T any] struct {
	ttl   time.Duration
	now   func() time.Time
	run   func(fn func())
	fetch func() (T, error)

	mu         sync.Mutex
	val        T
	at         time.Time // zero until the first refresh lands
	refreshing bool
}

func newAsyncCached[T any](ttl time.Duration, now func() time.Time,
	run func(fn func()), fetch func() (T, error)) *asyncCached[T] {
	return &asyncCached[T]{ttl: ttl, now: now, run: run, fetch: fetch}
}

// Get returns the last known value and whether one has ever landed, starting a
// refresh when the value is stale and none is already in flight. It returns the
// value as it was BEFORE the refresh was started: the answer a caller gets is
// the one already in hand, never one it waited for.
func (c *asyncCached[T]) Get() (T, bool) {
	c.mu.Lock()
	val, at := c.val, c.at
	stale := at.IsZero() || c.now().Sub(at) > c.ttl
	if stale && !c.refreshing {
		c.refreshing = true
		c.mu.Unlock()
		c.run(c.refresh)
	} else {
		c.mu.Unlock()
	}
	return val, !at.IsZero()
}

func (c *asyncCached[T]) refresh() {
	val, err := c.fetch()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	if err != nil {
		return // the source is down: keep serving the last known value
	}
	c.val, c.at = val, c.now()
}

// cohortDirectory returns the last known directory, kicking off a refresh when
// it is stale. Never blocks: the ask runs on the member's prompt, and a
// registry round-trip there would be latency spent on a nudge.
func (a *Agent) cohortDirectory() []CohortDim {
	if a.cohorts == nil {
		return nil // no Cohorts seam
	}
	dims, _ := a.cohorts.Get()
	return dims
}

// ShownSuggestion is one entry in a session's review list: what was
// suggested and what became of it. Status moves shown → adopted → helped,
// or to dismissed; later stages never regress to earlier ones.
type ShownSuggestion struct {
	TechniqueID string `json:"technique_id"`
	Name        string `json:"name"`
	Evidence    string `json:"evidence,omitempty"` // verbatim measured line, "" when cold
	ShownAt     string `json:"shown_at"`
	Status      string `json:"status"` // shown | adopted | helped | dismissed
}

// suggestionRank orders statuses so an update never downgrades a verdict.
var suggestionRank = map[string]int{"shown": 0, "adopted": 1, "helped": 2, "dismissed": 2}

// noteSuggestionLocked appends to the session's review list at delivery time.
func (st *sessionState) noteSuggestionLocked(technique contracts.EvidenceCandidate, now time.Time) {
	ev := evidenceLine(technique.Outcomes) // same verbatim line the nudge shows
	st.sugList = append(st.sugList, &ShownSuggestion{
		TechniqueID: technique.TechniqueID, Name: technique.Name, Evidence: ev,
		ShownAt: now.UTC().Format(time.RFC3339), Status: "shown",
	})
}

// setSuggestionStatusLocked records a verdict on the review list.
func (st *sessionState) setSuggestionStatusLocked(capID, status string) {
	for _, sg := range st.sugList {
		if sg.TechniqueID == capID && suggestionRank[status] > suggestionRank[sg.Status] {
			sg.Status = status
		}
	}
}

// SuggestionsFor returns a session's review list (newest last), matching the
// stats endpoint's suffix rule so callers pass the harness's own session id.
func (a *Agent) SuggestionsFor(sessionID string) []*ShownSuggestion {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sessionID == "" {
		return nil
	}
	for key, st := range a.sessions {
		if strings.HasSuffix(key, ":"+sessionID) {
			out := make([]*ShownSuggestion, len(st.sugList))
			for i, sg := range st.sugList {
				cp := *sg
				out[i] = &cp
			}
			return out
		}
	}
	return nil
}

// Stats are the per-session (or agent-total) suggestion counters that feed
// the status-line segment and the E2 funnel (shown → offered → answered).
type Stats struct {
	Shown   int `json:"shown"`
	Adopted int `json:"adopted"`
	Offered int `json:"offered"` // AskUserQuestion offers observed
	// Declined counts candidates the fit-check rejected (explicit NONE). Read it
	// alongside Shown: 0 shown with 0 declined is a quiet day, 0 shown with 12
	// declined is a fit-check worth looking at. LastDeclined names the most recent
	// one and is per-session only (like Client) — across sessions there is no
	// ordering that would make a single "most recent" meaningful.
	Declined     int    `json:"declined"`
	LastDeclined string `json:"last_declined,omitempty"`
	// Client is per-session only (empty in the totals): "terminal" or "bridge",
	// as the relay classified it. Empty means the relay sent nothing — on Claude
	// Code that is itself the answer to whether hook subprocesses inherit the
	// bridge env var.
	Client string `json:"client,omitempty"`
}

// DraftsCount returns the cached review-queue depth and whether it's known.
// Never blocks a stats read: if the cache is stale (or empty), it kicks off a
// background refresh and returns the last value. false until the first refresh
// lands, so callers can omit the indicator entirely when unknown.
func (a *Agent) DraftsCount() (int, bool) {
	if a.drafts == nil {
		return 0, false // no Drafts seam
	}
	return a.drafts.Get()
}

// AccessMode reports the registry's kind for the status line, refreshing
// asynchronously when stale. ok is false until the first answer arrives, which is
// what keeps the status line silent rather than speculating.
func (a *Agent) AccessMode() (mode, tunnel string, ok bool) {
	if a.access == nil {
		return "", "", false // no Access seam
	}
	kind, ok := a.access.Get()
	return kind.mode, kind.tunnel, ok
}

// accessKind is what the registry says it is: the access mode and, when it is
// reachable from outside, the tunnel hostname. One cached value, because the
// two are read together and answered by one call.
type accessKind struct{ mode, tunnel string }

// StatsFor returns totals across live sessions plus, when sessionID matches a
// live session under any harness, that session's own counters.
func (a *Agent) StatsFor(sessionID string) (totals Stats, session *Stats) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, st := range a.sessions {
		totals.Shown += st.suggestionsMade
		totals.Adopted += len(st.adoptedIDs)
		totals.Offered += st.questionsOffered
		totals.Declined += st.declinedCount
		if sessionID != "" && strings.HasSuffix(key, ":"+sessionID) {
			session = &Stats{Shown: st.suggestionsMade, Adopted: len(st.adoptedIDs),
				Offered: st.questionsOffered, Declined: st.declinedCount,
				LastDeclined: st.lastDeclined, Client: st.client}
		}
	}
	return totals, session
}

// UsageSummary aggregates this member's local activity history (usagelog.go)
// over the window, for the Usage view. Member-local data only — nothing
// server-side, no identity — so a member sees their own usage without the
// registry ever attributing anything to them. window<=0 is the full retained
// history.
func (a *Agent) UsageSummary(window time.Duration) UsageSummary {
	return a.usage.summarize(window)
}

// WorkSummary aggregates this member's local session log over the window and
// folds in the recurring corrections (sessionlog.go, corrections.go). Same
// contract as UsageSummary: member-local, counts only, loopback-only.
func (a *Agent) WorkSummary(window time.Duration) WorkSummary {
	sum := a.sessionLog.summarizeWork(window)
	// Read live rather than aggregated, and read every time: the allowance is
	// a level with an expiry, so a copy taken when the window was summarized
	// would be the one figure on the page that goes wrong by sitting still.
	sum.Quotas = a.quota.read()
	sum.QuotaHistory = ReadQuotaHistory(a.opts.StateDir, a.opts.Now)
	sum.WorkHistory = ReadWorkHistory(a.opts.StateDir, a.opts.Now)
	// Filed by hand, never by the agent: a survival reading exists because a
	// member ran the command in a repository, and the agent does not know where
	// any repository is.
	sum.Landed = LandedFilings(a.opts.StateDir, nowOr(a.opts.Now))
	// What is open right now, from the agent's own head. The log is history;
	// this is the only figure on the page that is not. Through SessionCount
	// rather than off the map directly, because this method holds no lock.
	live := a.SessionCount()
	sum.LiveSessions = &live
	for _, e := range a.corrections.repeated(2) {
		sum.Repeats = append(sum.Repeats, CorrectionRepeat{
			Hash: e.Hash, Count: e.Count, First: e.First, Last: e.Last, Raised: e.Raised})
	}
	return sum
}
