// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"path/filepath"
	"testing"
	"time"
)

// clock is a hand-wound time source: the throttle is entirely about elapsed
// time, and a test that slept for it would take 45 minutes.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)} }

// turns advances both clocks the way a working member does.
func turns(th *throttle, c *clock, n int, each time.Duration) {
	for range n {
		c.add(each)
		th.observeTurn()
	}
}

// A member who has never been shown anything owes nothing: the first relevant
// turn of their first session must not be spent waiting out a cooldown for a
// suggestion that never happened.
func TestFirstSuggestionIsNotMadeToWait(t *testing.T) {
	c := newClock()
	th := newThrottle(defaultCooldownTurns, defaultCooldownFor, defaultMaxPerWindow, defaultWindow, c.now)
	if !th.allow() {
		t.Fatal("the first suggestion was throttled; nothing has been spent yet")
	}
}

// Both clocks have to clear. This is the whole design: either alone goes wrong
// at a different tail of the same distribution, so neither alone may release.
func TestBothClocksMustClear(t *testing.T) {
	for _, tc := range []struct {
		name       string
		turns      int
		each       time.Duration
		wantAllow  bool
		wantReason string
	}{
		{"enough turns, not enough time", 8, 2 * time.Minute, false,
			"8 quick turns is 16 minutes — a burst, which is what the time clock is for"},
		{"enough time, not enough turns", 2, 40 * time.Minute, false,
			"80 minutes but only 2 turns — the turn clock holds a slow session"},
		{"both cleared", 7, 10 * time.Minute, true,
			"7 turns over 70 minutes is ordinary paced work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newClock()
			th := newThrottle(defaultCooldownTurns, defaultCooldownFor, defaultMaxPerWindow, defaultWindow, c.now)
			th.record() // something was shown just now
			turns(th, c, tc.turns, tc.each)
			if got := th.allow(); got != tc.wantAllow {
				t.Fatalf("allow = %v, want %v — %s", got, tc.wantAllow, tc.wantReason)
			}
		})
	}
}

// Spacing alone still lets a long day accumulate. The rolling window is what
// makes the ceiling real for a marathon session — and, unlike the per-session
// cap it replaced, it is a window nothing can reset.
func TestRollingWindowCapsALongDay(t *testing.T) {
	c := newClock()
	th := newThrottle(defaultCooldownTurns, defaultCooldownFor, defaultMaxPerWindow, defaultWindow, c.now)
	shown := 0
	// Twelve hours of steady, well-spaced work: every clock but the window
	// clears each time, so the window is the only thing that can bind.
	for range 12 * 6 {
		turns(th, c, 1, 10*time.Minute)
		if th.allow() {
			th.record()
			shown++
		}
	}
	// 12 hours holds two disjoint 6-hour windows, so the honest ceiling is
	// twice the per-window cap and not one more.
	if shown > 2*defaultMaxPerWindow {
		t.Fatalf("shown %d over 12 hours, want at most %d — the window is not holding",
			shown, 2*defaultMaxPerWindow)
	}
	if shown < defaultMaxPerWindow {
		t.Fatalf("shown %d over 12 hours, want at least %d — the window is over-tight",
			shown, defaultMaxPerWindow)
	}
}

// The window is rolling, not a bucket that empties on a boundary: once the
// oldest suggestion ages out, the budget it held comes back.
func TestWindowReleasesAsItRolls(t *testing.T) {
	c := newClock()
	th := newThrottle(0, 0, 2, time.Hour, c.now) // spacing off; isolate the window
	th.record()
	c.add(10 * time.Minute)
	th.record()
	if th.allow() {
		t.Fatal("two shown inside the hour with a cap of two: the window must be full")
	}
	c.add(51 * time.Minute) // the first ages out, the second has not
	if !th.allow() {
		t.Fatal("the oldest aged out of the window; its budget should be back")
	}
}

// The fault this whole change exists to fix. The daemon idle-exits after ~15
// minutes and held the old cap in memory, so any pause longer than a coffee
// break refunded it — measurably: 7 suggestions in a day under a rule that
// read "max 3 per session". A restart must come back still owing the cooldown.
func TestBudgetSurvivesADaemonRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	c := newClock()

	// First run: a suggestion is shown, then the member goes quiet and the
	// daemon idle-exits.
	first := loadUsageLog(path, c.now)
	first.append(usageEvent{Kind: usageShown, Cap: "cap-1", Name: "A"})
	c.add(20 * time.Minute) // longer than the 15-minute idle-exit
	first.append(usageEvent{Kind: usageQuery})

	// Second run: a brand-new agent, reading the same member-local log.
	second := loadUsageLog(path, c.now)
	th := newThrottle(defaultCooldownTurns, defaultCooldownFor, defaultMaxPerWindow, defaultWindow, c.now)
	th.seed(second.throttleState(defaultWindow))
	if th.allow() {
		t.Fatal("a restart refilled the budget — this is exactly the per-session bug in new clothes")
	}
	// And it clears on its own terms, not on the restart's.
	turns(th, c, defaultCooldownTurns, 10*time.Minute)
	if !th.allow() {
		t.Fatal("the seeded cooldown never cleared")
	}
}

// Turns are rebuilt from the log too, not just the wall clock: the log records
// one `query` per member turn, so both clocks come back rather than one.
func TestSeedRebuildsTheTurnClock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	c := newClock()
	u := loadUsageLog(path, c.now)
	u.append(usageEvent{Kind: usageQuery}) // turns before the suggestion are not owed
	u.append(usageEvent{Kind: usageShown, Cap: "cap-1"})
	for range 3 {
		c.add(time.Minute)
		u.append(usageEvent{Kind: usageQuery})
	}
	got := loadUsageLog(path, c.now).throttleState(defaultWindow)
	if got.TurnsSince != 3 {
		t.Fatalf("TurnsSince = %d, want 3 (counted from the last shown, not the log head)", got.TurnsSince)
	}
	if len(got.InWindow) != 1 {
		t.Fatalf("InWindow = %d, want 1", len(got.InWindow))
	}
}

// A member with nothing in their history owes nothing — seeding an empty log
// must not invent a cooldown out of the zero time.
func TestSeedingAnEmptyHistoryLeavesTheBudgetUntouched(t *testing.T) {
	c := newClock()
	u := loadUsageLog(filepath.Join(t.TempDir(), "usage.jsonl"), c.now)
	th := newThrottle(defaultCooldownTurns, defaultCooldownFor, defaultMaxPerWindow, defaultWindow, c.now)
	th.seed(u.throttleState(defaultWindow))
	if !th.allow() {
		t.Fatal("an empty history produced a cooldown; a new member owes nothing")
	}
}

// Coaching off is an explicit state, not an arithmetic accident. Observation
// keeps running (the caller's concern); nothing unsolicited is ever shown.
func TestCoachingOffShowsNothing(t *testing.T) {
	c := newClock()
	th := newThrottle(0, 0, -1, defaultWindow, c.now)
	turns(th, c, 100, time.Hour)
	if th.allow() {
		t.Fatal("MaxPerWindow -1 must mean no unsolicited suggestion, ever")
	}
}

// The defaults are a product decision calibrated against a real member log
// (412 turns over 8 days, ~7 turns/hour). They are pinned because drifting
// them silently changes how much of a member's attention OpenTacit spends.
func TestDefaultsAreTheCalibratedOnes(t *testing.T) {
	if defaultCooldownTurns != 6 || defaultCooldownFor != 45*time.Minute {
		t.Errorf("spacing = %d turns / %s, want 6 / 45m — matched so that at ~7 turns/hour "+
			"neither clock is dead weight", defaultCooldownTurns, defaultCooldownFor)
	}
	if defaultMaxPerWindow != 4 || defaultWindow != 6*time.Hour {
		t.Errorf("volume = %d per %s, want 4 per 6h — a worst case near 6-8/day against "+
			"an observed norm of ~1", defaultMaxPerWindow, defaultWindow)
	}
}
