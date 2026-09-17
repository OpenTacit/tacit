// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/capture"
)

// Two clocks, and the whole of this phase is keeping them apart.
//
// Every date in this log is UTC, which is right for a rollup. An hour of the
// day is not a rollup — it is a fact about the member, and in UTC it is a
// false one: somebody in California working at six in the evening would be
// told they work at one in the morning. So the hour is counted off the clock
// the turn arrived on, and no timezone is ever stored to do it.
func TestHoursAreCountedOnTheMembersOwnClockNotInUTC(t *testing.T) {
	pacific, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tzdata on this machine")
	}
	// 18:30 in California is 01:30 the next day in UTC — a different hour AND
	// a different date.
	evening := time.Date(2026, 9, 11, 18, 30, 0, 0, pacific)
	if evening.UTC().Hour() != 1 || evening.UTC().Day() != 12 {
		t.Fatalf("the fixture does not straddle the boundary: %v", evening.UTC())
	}

	var s sessionStats
	s.observe(evening, capture.EvUserPrompt, map[string]any{})
	s.observe(evening.Add(20*time.Minute), capture.EvUserPrompt, map[string]any{})
	r := s.record("k", "claude-code", "m", "/home/x/tacit", 0)

	if r.Hours["18"] != 2 {
		t.Fatalf("hours = %v, want two turns at 18 — the member's evening was filed in UTC", r.Hours)
	}
	if r.Hours["01"] != 0 {
		t.Fatalf("a turn was counted at the UTC hour: %v", r.Hours)
	}
	// The record's own dates stay UTC, because everything that rolls up is.
	if r.End.Location() != time.UTC {
		t.Fatalf("the record stopped being UTC: %v", r.End)
	}
	// And nothing about where the member is reaches disk — an hour histogram
	// is not a location and must not become one.
	for _, k := range []string{"zone", "tz", "offset", "location"} {
		if _, present := r.Hours[k]; present {
			t.Fatalf("the hour counter carried %q", k)
		}
	}
}

// The hour counter is a vocabulary of twenty-four keys and nothing else can
// get into it.
func TestHourKeysAreTwoDigitsRoundTheClock(t *testing.T) {
	var s sessionStats
	base := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	for h := 0; h < 24; h++ {
		s.observe(base.Add(time.Duration(h)*time.Hour), capture.EvUserPrompt, map[string]any{})
	}
	r := s.record("k", "claude-code", "m", "/home/x/tacit", 0)
	if len(r.Hours) != 24 {
		t.Fatalf("got %d hours, want 24: %v", len(r.Hours), r.Hours)
	}
	for k, n := range r.Hours {
		if len(k) != 2 || k < "00" || k > "23" || n != 1 {
			t.Fatalf("hour %q = %d is not one of the twenty-four", k, n)
		}
	}
}

// A session's length survives the thirty-day horizon. A day rollup keeps one
// total and loses the sessions inside it, so the distribution has to be
// bucketed on the way in or a season of history has no rhythm in it.
func TestSessionLengthsSurviveCompaction(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	l := loadSessionLog(detail, daily, fixedClock(now))
	for i, span := range []time.Duration{5 * time.Minute, 40 * time.Minute, 2 * time.Hour, 6 * time.Hour} {
		r := rec("old"+string(rune('a'+i)), old, "anthropic/claude-opus-5", "tacit", 3)
		r.Start = old.Add(-span)
		r.Hours = map[string]int{"09": 3}
		l.upsert(r)
	}
	l.mu.Lock()
	l.compactLocked()
	l.mu.Unlock()

	sum := l.summarizeWork(0)
	got := map[string]int{}
	for _, b := range sum.Lengths {
		got[b.Key] = b.Count
	}
	for _, want := range []string{"under 15m", "15m–1h", "1–3h", "over 3h"} {
		if got[want] != 1 {
			t.Fatalf("%q = %d after compaction, want 1: %v", want, got[want], sum.Lengths)
		}
	}
	// Short to long, never busiest-first: the order is the reading.
	if len(sum.Lengths) != 4 || sum.Lengths[0].Key != "under 15m" || sum.Lengths[3].Key != "over 3h" {
		t.Fatalf("the bands came back out of order: %v", sum.Lengths)
	}
	// The hours came through the fold too, or a season of history loses the day.
	if len(sum.Hours) != 1 || sum.Hours[0].Key != "09" || sum.Hours[0].Count != 12 {
		t.Fatalf("hours did not survive compaction: %v", sum.Hours)
	}
}

// A clock that went backwards is not a fifteen-minute session.
func TestASessionWithNoSpanGetsNoBand(t *testing.T) {
	if b := sessionLengthBucket(0); b != "" {
		t.Errorf("a zero span was banded as %q", b)
	}
	if b := sessionLengthBucket(-time.Hour); b != "" {
		t.Errorf("a negative span was banded as %q", b)
	}
	if b := sessionLengthBucket(90 * time.Minute); b != "1–3h" {
		t.Errorf("90m was banded as %q", b)
	}
}
