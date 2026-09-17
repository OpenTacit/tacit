// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The unsolicited-suggestion throttle: how much of the member's attention
// ambient coaching may spend, and how often.
//
// This replaced a per-session cap (3 suggestions, 2-turn cooldown) that could
// not do the job it was named for. Three faults, and the first is fatal:
//
//   - It was scoped to something that evaporates. suggestionsMade lived in the
//     in-memory sessionState, and the hook daemon idle-exits after 15 minutes
//     (HooksIdleExitSecs). Real inter-turn gaps are routinely longer than
//     that, so the budget reset on any coffee break and a day could carry far
//     more suggestions than the per-session rule's name promised.
//   - It was per SESSION, and a member with two terminals open is one member
//     with one attention span, not two budgets.
//   - A 2-turn cooldown is no spacing at all when turns are quick: successive
//     suggestions can land minutes apart.
//
// So the budget is now the MEMBER's, held on the Agent rather than a session,
// rebuilt from the member-local usage log at startup so a daemon restart
// cannot refill it, and spent against two clocks at once.
//
// Two clocks, ANDed, because either alone is wrong at a different tail of the
// same distribution. Turns and wall-clock look interchangeable at the median —
// ~7 turns an hour of active work, a turn every ~8.5 minutes — but the spread
// is wide (p25 3.7 min, p95 90 min). A time-only rule goes slack exactly when
// turns are slow: at p95 an hour clears on every single turn. A turn-only rule
// goes rigid exactly when the member comes back from a break: three hours away
// and they still owe N turns before anything can surface, which is the moment
// a fresh piece of org context is most worth having. Requiring both to clear
// means whichever axis is currently the binding one does the work.
//
// A rolling window sits over the top, because spacing alone still lets a long
// day accumulate: at one an hour, a ten-hour day is ten interruptions. The
// window is what makes the ceiling real for a marathon session — and, unlike
// "per session", it is a window nothing can reset.
//
// None of this governs advice the member ASKS for. @tacit mentions (mention.go),
// the MCP tools and the skills are all exempt: this budgets unsolicited
// attention only, so a strict ceiling never stands between a member and an
// answer they went looking for.
package hooks

import "time"

// The defaults, calibrated against observed member usage (~7 turns/hour of
// active work).
//
// 6 turns and 45 minutes are deliberately matched rather than stacked: at the
// observed pace six turns is about 51 minutes, so in ordinary work the two
// clocks come due together and neither is dead weight. Fast bursts are held by
// the 45 minutes, slow sessions by the six turns.
//
// 4 in 6 hours puts the worst case near 6–8 a day against an observed norm of
// about one — headroom enough that a genuinely busy day is not clipped, tight
// enough that a session where everything happens to look relevant cannot
// become a stream.
const (
	defaultCooldownTurns = 6
	defaultCooldownFor   = 45 * time.Minute
	defaultMaxPerWindow  = 4
	defaultWindow        = 6 * time.Hour
)

// throttle is the member's ambient-suggestion budget. It is not safe for
// concurrent use on its own: every method is called with the Agent's mu held,
// which is also what makes it one budget across every live session.
type throttle struct {
	cooldownTurns int
	cooldownFor   time.Duration
	maxPerWindow  int
	window        time.Duration
	now           func() time.Time

	// turnsSince counts member turns since the last suggestion. It starts
	// large so a member who has never been shown anything is not made to wait
	// out a cooldown for a suggestion that never happened.
	turnsSince int
	lastShown  time.Time
	// shown holds the timestamps inside the rolling window, oldest first.
	// Bounded by maxPerWindow + the pruning in allow(), so it cannot grow.
	shown []time.Time
}

func newThrottle(cooldownTurns int, cooldownFor time.Duration,
	maxPerWindow int, window time.Duration, now func() time.Time) *throttle {

	if cooldownTurns < 0 {
		cooldownTurns = 0
	}
	if cooldownFor < 0 {
		cooldownFor = 0
	}
	// Negative is "coaching off": observation still runs and facts are still
	// recorded, but nothing unsolicited is ever shown. An explicit state, not
	// an arithmetic accident — the rule it replaced expressed this as a cap of
	// -1 that 0 suggestions could never be below.
	if maxPerWindow < 0 {
		maxPerWindow = 0
	} else if maxPerWindow == 0 {
		maxPerWindow = defaultMaxPerWindow
	}
	if window <= 0 {
		window = defaultWindow
	}
	if now == nil {
		now = time.Now
	}
	return &throttle{
		cooldownTurns: cooldownTurns, cooldownFor: cooldownFor,
		maxPerWindow: maxPerWindow, window: window, now: now,
		turnsSince: 1 << 20,
	}
}

// seed restores the budget from the member-local usage log, so a daemon that
// idle-exited mid-cooldown comes back still owing it. Without this the whole
// mechanism is the old per-session cap wearing different numbers.
func (t *throttle) seed(s usageThrottleState) {
	if t == nil {
		return
	}
	if s.LastShown.IsZero() {
		return // nothing shown in retained history: the budget is untouched
	}
	t.lastShown = s.LastShown
	t.turnsSince = s.TurnsSince
	t.shown = append(t.shown[:0], s.InWindow...)
}

// observeTurn advances the turn clock. Called once per member turn, on the
// prompt — not on Stop, which can fire more than once for a single turn.
func (t *throttle) observeTurn() {
	if t == nil {
		return
	}
	t.turnsSince++
}

// allow reports whether an unsolicited suggestion may be shown now. It prunes
// the window as a side effect, which is the only place the slice shrinks.
func (t *throttle) allow() bool {
	if t == nil {
		return true
	}
	if t.maxPerWindow == 0 {
		return false // coaching off
	}
	now := t.now()
	cut := now.Add(-t.window)
	kept := t.shown[:0]
	for _, at := range t.shown {
		if at.After(cut) {
			kept = append(kept, at)
		}
	}
	t.shown = kept

	if len(t.shown) >= t.maxPerWindow {
		return false
	}
	if t.turnsSince < t.cooldownTurns {
		return false
	}
	// A zero lastShown is a member who has never been shown anything, not one
	// shown at the zero time — they owe nothing.
	if !t.lastShown.IsZero() && now.Sub(t.lastShown) < t.cooldownFor {
		return false
	}
	return true
}

// record books a shown suggestion against the budget. The usage log records
// the same event independently (that is what seed reads back), so this does
// not write: the two are kept in step by commitLocked calling throttle.record
// and the usage append back to back.
func (t *throttle) record() {
	if t == nil {
		return
	}
	now := t.now()
	t.lastShown = now
	t.turnsSince = 0
	t.shown = append(t.shown, now)
}
