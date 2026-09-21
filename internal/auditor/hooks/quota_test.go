// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stale quota is worse than no quota: a member acts on it, and the allowance
// they acted on has since refilled. So a window whose reset has passed is
// dropped on the way out rather than shown with a caveat.
func TestQuotaDropsAWindowThatHasAlreadyReset(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	q := newQuotaFile(dir, fixedClock(now))
	q.write("anthropic", Quota{
		FiveHour: &QuotaWindow{UsedPct: 17, ResetsAt: now.Add(30 * time.Minute)},
		SevenDay: &QuotaWindow{UsedPct: 27, ResetsAt: now.Add(60 * time.Hour)},
	})

	live := newQuotaFile(dir, fixedClock(now.Add(time.Hour))).read() // the five-hour has reset
	if len(live) != 1 || live[0].SevenDay == nil || live[0].SevenDay.UsedPct != 27 {
		t.Fatalf("the week's allowance should have survived: %+v", live)
	}
	if live[0].FiveHour != nil {
		t.Fatalf("an expired window was served: %+v", live[0].FiveHour)
	}
	if gone := newQuotaFile(dir, fixedClock(now.Add(100*time.Hour))).read(); len(gone) != 0 {
		t.Fatalf("a wholly expired reading was served: %+v", gone)
	}
}

// One row PER PROVIDER, overwritten. A session record is history and must not
// be rewritten by a number that expires, which is the whole reason this file
// exists apart from the session log.
func TestQuotaKeepsOneRowAndStampsWhenItWasRead(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		newQuotaFile(dir, fixedClock(now.Add(time.Duration(i)*time.Minute))).write("anthropic", Quota{
			FiveHour: &QuotaWindow{UsedPct: 10 + i, ResetsAt: now.Add(3 * time.Hour)}})
	}
	raw, err := os.ReadFile(filepath.Join(dir, "quota.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "five_hour") != 1 {
		t.Fatalf("the quota file grew rows: %s", raw)
	}
	got := ReadQuota(dir, fixedClock(now.Add(6*time.Minute)))
	if len(got) != 1 || got[0].FiveHour.UsedPct != 14 {
		t.Fatalf("the latest reading did not win: %+v", got)
	}
	// The reading's own timestamp, which is what the page renders as "as of".
	if !got[0].At.Equal(now.Add(4 * time.Minute)) {
		t.Fatalf("the reading is stamped %v, want %v", got[0].At, now.Add(4*time.Minute))
	}
}

// TWO PROVIDERS ARE TWO ALLOWANCES. A member with a Claude session and a Codex
// session beside it is spending both, and the row was unnamed and single: each
// render overwrote the other's reading, so the plate showed whichever harness
// had drawn its status line last and said nothing about whose it was.
func TestEachProviderKeepsItsOwnAllowance(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	q := newQuotaFile(dir, fixedClock(now))
	q.write("anthropic", Quota{FiveHour: &QuotaWindow{UsedPct: 61, ResetsAt: now.Add(2 * time.Hour)}})
	q.write("openai", Quota{FiveHour: &QuotaWindow{UsedPct: 9, ResetsAt: now.Add(2 * time.Hour)}})

	got := ReadQuota(dir, fixedClock(now.Add(time.Minute)))
	if len(got) != 2 {
		t.Fatalf("kept %d allowances, want 2: %+v", len(got), got)
	}
	// Ordered by source, so the plates do not shuffle between renders.
	if got[0].Source != "anthropic" || got[1].Source != "openai" {
		t.Fatalf("the readings are unordered or unnamed: %+v", got)
	}
	if got[0].FiveHour.UsedPct != 61 || got[1].FiveHour.UsedPct != 9 {
		t.Fatalf("one allowance overwrote the other: %+v", got)
	}
	// And each is still one row: a second reading replaces its own, not both.
	q.write("openai", Quota{FiveHour: &QuotaWindow{UsedPct: 12, ResetsAt: now.Add(2 * time.Hour)}})
	got = ReadQuota(dir, fixedClock(now.Add(2*time.Minute)))
	if len(got) != 2 || got[0].FiveHour.UsedPct != 61 || got[1].FiveHour.UsedPct != 12 {
		t.Fatalf("a second reading did not land on its own row: %+v", got)
	}
}

// A PROVIDER NOBODY HAS HEARD OF KEEPS ITS OWN ALLOWANCE. The vendor list is a
// vocabulary, not a gate: a status line rendering a model this build cannot
// place files under the model's own family, so its allowance is a row and a
// plate on the same terms as any other. Keying it under "" would have put every
// unrecognised provider in one row — the same bug one level down.
func TestAnUnknownProviderKeepsItsOwnRow(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	q := newQuotaFile(dir, fixedClock(now))
	q.write("anthropic", Quota{FiveHour: &QuotaWindow{UsedPct: 61, ResetsAt: now.Add(2 * time.Hour)}})
	q.write("newprovider", Quota{FiveHour: &QuotaWindow{UsedPct: 4, ResetsAt: now.Add(2 * time.Hour)}})
	q.write("someone-else", Quota{FiveHour: &QuotaWindow{UsedPct: 44, ResetsAt: now.Add(2 * time.Hour)}})

	got := ReadQuota(dir, fixedClock(now.Add(time.Minute)))
	if len(got) != 3 {
		t.Fatalf("kept %d allowances, want 3: %+v", len(got), got)
	}
	for _, v := range got {
		if v.SourceLabel == "" {
			t.Errorf("%q came back with no name for a heading to carry", v.Source)
		}
	}
	if got[1].SourceLabel != "Newprovider" {
		t.Errorf("the unknown provider is named %q", got[1].SourceLabel)
	}
}

// The file written before allowances were kept per provider still reads. It is
// one unnamed row, which is what it was, and the next status line replaces it
// with a named one — so nothing is lost and nothing is mislabelled.
func TestAnUnnamedQuotaFileStillReads(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	old := `{"at":"2026-09-11T11:59:00Z","five_hour":{"used_pct":17,"resets_at":"2026-09-11T14:00:00Z"}}`
	if err := os.WriteFile(filepath.Join(dir, "quota.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ReadQuota(dir, fixedClock(now))
	if len(got) != 1 || got[0].FiveHour == nil || got[0].FiveHour.UsedPct != 17 {
		t.Fatalf("the reading did not survive the shape change: %+v", got)
	}
	if got[0].Source != "" {
		t.Errorf("an unnamed reading was given a provider: %q", got[0].Source)
	}

	// And it steps aside the moment a named reading lands. The old row is the
	// same allowance under no name, so leaving it beside the new one would put
	// two plates on the page for one account.
	newQuotaFile(dir, fixedClock(now)).write("anthropic",
		Quota{FiveHour: &QuotaWindow{UsedPct: 18, ResetsAt: now.Add(2 * time.Hour)}})
	got = ReadQuota(dir, fixedClock(now.Add(time.Minute)))
	if len(got) != 1 || got[0].Source != "anthropic" || got[0].FiveHour.UsedPct != 18 {
		t.Fatalf("the unnamed row was served beside the named one: %+v", got)
	}
}

// No state directory is a machine that keeps no quota, not a crash.
func TestQuotaWithoutAHomeIsQuiet(t *testing.T) {
	q := newQuotaFile("", nil)
	q.write("anthropic", Quota{FiveHour: &QuotaWindow{UsedPct: 5, ResetsAt: time.Now().Add(time.Hour)}})
	if got := q.read(); len(got) != 0 {
		t.Fatalf("a homeless agent served a quota: %+v", got)
	}
	if got := ReadQuota("", nil); len(got) != 0 {
		t.Fatalf("ReadQuota invented one: %+v", got)
	}
}

// THE CLIMB IS SAMPLED, NOT RECORDED WHOLE. A status line renders several times
// a second and almost none of those renders say anything new, so a point is
// kept when the level MOVES or when the interval comes round — and never
// otherwise. A window that sat still for an hour costs twelve points.
func TestQuotaHistorySamplesTheClimb(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	at := start
	clock := func() time.Time { return at }
	q := newQuotaFile(dir, clock)
	work := func() (int, int) { return 0, 0 }

	five := func(pct int) Quota {
		return Quota{FiveHour: &QuotaWindow{UsedPct: pct, ResetsAt: start.Add(4 * time.Hour)}}
	}
	// A hundred renders in one minute, all saying 10%: one point.
	for i := 0; i < 100; i++ {
		at = start.Add(time.Duration(i) * 600 * time.Millisecond)
		q.sample("anthropic", five(10), work)
	}
	if got := len(ReadQuotaHistory(dir, clock)[0].Five); got != 1 {
		t.Fatalf("%d points for one unmoved reading, want 1", got)
	}
	// The level moves: a point, whatever the clock says.
	at = start.Add(time.Minute)
	q.sample("anthropic", five(11), work)
	// It sits still for four minutes: nothing.
	for i := 0; i < 4; i++ {
		at = start.Add(time.Duration(2+i) * time.Minute)
		q.sample("anthropic", five(11), work)
	}
	if got := len(ReadQuotaHistory(dir, clock)[0].Five); got != 2 {
		t.Fatalf("%d points, want 2 — a still level was sampled anyway", got)
	}
	// Past the interval it takes one anyway, so a flat stretch is still drawn
	// as a stretch rather than as a gap.
	at = start.Add(7 * time.Minute)
	q.sample("anthropic", five(11), work)
	if got := len(ReadQuotaHistory(dir, clock)[0].Five); got != 3 {
		t.Fatalf("%d points, want 3 — the interval never came round", got)
	}
}

// The machine's own work is sampled as a DELTA on its own series, so a chart
// adds it per bucket. It is machine-wide: a turn counter cannot say which
// account paid for it, so it sits beside the providers rather than inside one.
//
// Two things it must not do. Count the whole log as one bucket's work the first
// time a machine ever samples — there is nothing to difference against yet — and
// go negative when the session log folds its oldest records into days and its
// running total falls.
func TestWorkHistoryCarriesTheWorkSinceTheLastPoint(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	at := start
	clock := func() time.Time { return at }
	q := newQuotaFile(dir, clock)
	turns, calls := 900, 4000 // a machine with a long history behind it
	work := func() (int, int) { return turns, calls }
	five := func(pct int) Quota {
		return Quota{FiveHour: &QuotaWindow{UsedPct: pct, ResetsAt: start.Add(4 * time.Hour)}}
	}
	q.sample("anthropic", five(1), work)
	if got := ReadWorkHistory(dir, clock); len(got) != 0 {
		t.Fatalf("the first sample filed the whole log as one bucket: %+v", got)
	}
	turns, calls = 912, 4040
	at = start.Add(time.Minute)
	q.sample("anthropic", five(2), work)
	turns, calls = 3, 4 // the log folded: the running total fell
	at = start.Add(2 * time.Minute)
	q.sample("anthropic", five(3), work)

	got := ReadWorkHistory(dir, clock)
	if len(got) != 1 {
		t.Fatalf("%d work points, want 1 — a fall in the total drew a column", len(got))
	}
	if got[0].Turns != 12 || got[0].ToolCalls != 40 {
		t.Errorf("the work since the last point is %d turns %d calls, want 12 and 40",
			got[0].Turns, got[0].ToolCalls)
	}
	// And the readings themselves carry no work at all.
	if pts := ReadQuotaHistory(dir, clock)[0].Five; len(pts) != 3 {
		t.Fatalf("%d readings, want 3", len(pts))
	}
}

// Old points go. The five-hour series is about the window being spent and the
// week's about the week, so each is trimmed to its own horizon rather than to
// one shared with the other.
func TestQuotaHistoryKeepsOnlyItsOwnHorizons(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	at := start
	clock := func() time.Time { return at }
	q := newQuotaFile(dir, clock)
	work := func() (int, int) { return 0, 0 }
	for i := 0; i < 40; i++ {
		at = start.Add(time.Duration(i) * time.Hour)
		q.sample("anthropic", Quota{
			FiveHour: &QuotaWindow{UsedPct: i, ResetsAt: at.Add(time.Hour)},
			SevenDay: &QuotaWindow{UsedPct: i, ResetsAt: at.Add(48 * time.Hour)},
		}, work)
	}
	got := ReadQuotaHistory(dir, clock)
	if len(got) != 1 {
		t.Fatalf("kept %d series, want 1", len(got))
	}
	// 40 hourly points: six hours of them survive on the five-hour series, and
	// all forty on the week's.
	if n := len(got[0].Five); n > 7 {
		t.Errorf("the five-hour series kept %d points, well past its six hours", n)
	}
	if n := len(got[0].Week); n != 40 {
		t.Errorf("the week's series kept %d points of 40", n)
	}
}

// THE SPEND LIMIT IS THE THIRD WINDOW, AND IT IS NOT A USAGE WINDOW. It appears
// behind a gateway, it is money rather than usage, and it can pass 100 — which
// is a real reading and is not clamped on its way through. It keeps its own
// series on its own beat, because a spend period runs to a month and sampling
// it like the five hours would keep a month of five-minute points.
func TestSpendLimitIsKeptBesideTheUsageWindows(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	at := start
	clock := func() time.Time { return at }
	q := newQuotaFile(dir, clock)
	work := func() (int, int) { return 0, 0 }
	reading := func(spend int) Quota {
		return Quota{
			FiveHour: &QuotaWindow{UsedPct: 10, ResetsAt: start.Add(4 * time.Hour)},
			Spend:    &QuotaWindow{UsedPct: spend, ResetsAt: start.Add(20 * 24 * time.Hour)},
		}
	}
	q.write("anthropic", reading(103))
	q.sample("anthropic", reading(103), work)

	live := ReadQuota(dir, clock)
	if len(live) != 1 || live[0].Spend == nil {
		t.Fatalf("the spend limit did not survive the store: %+v", live)
	}
	if live[0].Spend.UsedPct != 103 {
		t.Errorf("a spend limit past its cap was clamped to %d", live[0].Spend.UsedPct)
	}
	hist := ReadQuotaHistory(dir, clock)
	if len(hist) != 1 || len(hist[0].Spend) != 1 {
		t.Fatalf("the spend limit has no series of its own: %+v", hist)
	}
	// Its own beat: a five-minute tick moves the five-hour series and leaves
	// the spend series alone, because a month-long window has not moved.
	at = start.Add(6 * time.Minute)
	q.sample("anthropic", reading(103), work)
	hist = ReadQuotaHistory(dir, clock)
	if len(hist[0].Five) != 2 {
		t.Errorf("the five-hour series has %d points after two ticks, want 2", len(hist[0].Five))
	}
	if len(hist[0].Spend) != 1 {
		t.Errorf("the spend series has %d points, want 1 — it is being sampled like the five hours",
			len(hist[0].Spend))
	}
	// A quota with nothing but a spend limit is still a quota.
	only := t.TempDir()
	newQuotaFile(only, clock).write("anthropic",
		Quota{Spend: &QuotaWindow{UsedPct: 12, ResetsAt: start.Add(20 * 24 * time.Hour)}})
	if got := ReadQuota(only, clock); len(got) != 1 || got[0].Spend == nil {
		t.Errorf("a gateway's spend limit alone was dropped: %+v", got)
	}
}
