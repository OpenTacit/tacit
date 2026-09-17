// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"testing"
	"time"
)

// A synthesis that never returns must not hold the daemon open for ever. The
// reservation still stands — the worker may yet park a result — but past the
// ceiling it stops counting as busy, so idle exit can proceed and the process
// reclaims the stuck goroutine.
func TestAStuckSynthesisStopsHoldingTheDaemonOpen(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	a := &Agent{
		opts:     Options{SynthBudget: 4 * time.Second, Now: func() time.Time { return now }},
		sessions: map[string]*sessionState{},
	}
	st := &sessionState{key: "claude-code:s1"}
	a.sessions[st.key] = st

	if a.Busy() {
		t.Fatal("an idle agent reported busy")
	}

	st.synthesizing = true
	st.synthesizingSince = now
	if !a.Busy() {
		t.Fatal("a fresh reservation did not report busy")
	}

	// Inside the ceiling the daemon still waits: a slow pipeline is expected to
	// park its result for the next prompt.
	now = now.Add(3 * a.opts.SynthBudget)
	if !a.Busy() {
		t.Fatal("a reservation inside the ceiling stopped reporting busy")
	}

	// Past it, the daemon has waited long enough.
	now = now.Add(2 * a.opts.SynthBudget)
	if a.Busy() {
		t.Fatal("a stuck reservation still reported busy past the ceiling")
	}
	if !st.synthesizing {
		t.Error("the ceiling cleared the reservation; it must only stop counting it")
	}

	// And that is what lets the daemon go.
	if !ShouldIdleExit(now, now.Add(-time.Hour), 900, a.Busy()) {
		t.Error("idle exit still blocked past the ceiling")
	}
}

// A reservation with no timestamp is treated as live, so an agent built by an
// older path cannot idle-exit out from under real work.
func TestAnUnstampedReservationCountsAsBusy(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	a := &Agent{
		opts:     Options{SynthBudget: 4 * time.Second, Now: func() time.Time { return now }},
		sessions: map[string]*sessionState{"k": {key: "k", synthesizing: true}},
	}
	if !a.Busy() {
		t.Error("an unstamped reservation did not report busy")
	}
}
