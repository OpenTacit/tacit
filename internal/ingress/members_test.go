// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// The proxy's operator needs to know how much each tenant is worth carrying, and
// the registry is the only party entitled to count its own members. So the
// figure travels: the registry reports a number on the control channel, the
// proxy records it against the instance and shows it.
//
// The number is the whole message. Nothing that could distinguish one member
// from another crosses this channel — that is the reason the count is reported
// rather than derived at the proxy, which terminates TLS and could otherwise
// work it out per request.
func TestRegistryReportsItsSizeAndTheProxyShowsIt(t *testing.T) {
	h := newHarness(t)
	c := &Client{
		Addr:  h.tunnelLn.Addr().String(),
		Token: NewKey(),
		Stats: func() Stats { return Stats{Day: 5, Week: 14, Month: 31, Year: 96} },
	}
	c.SetHandler(http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	var once sync.Once
	c.OnWelcome = func(w Welcome) { once.Do(func() { ready <- w.Instance }) }
	go func() { _ = c.Run(ctx) }()

	var name string
	select {
	case name = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the client never connected")
	}

	// Reported on connecting, so a freshly published registry is not blank until
	// the first interval passes.
	var in Instance
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if got, ok := h.srv.Store.Get(name); ok && got.MembersAt.IsZero() == false {
			in = got
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if in.Members != 14 || in.MembersDay != 5 || in.MembersMonth != 31 || in.MembersYear != 96 {
		t.Fatalf("the instance records day/week/month/year = %d/%d/%d/%d, want 5/14/31/96",
			in.MembersDay, in.Members, in.MembersMonth, in.MembersYear)
	}
	// One sample a day, so a trend accumulates from the first report — the only
	// way this can ever have a past, since the registry cannot answer for one.
	if len(in.Days) != 1 || in.Days[0].Week != 14 {
		t.Fatalf("daily samples = %+v, want today's report kept", in.Days)
	}
	if in.MembersAt.IsZero() {
		t.Error("the figure carries no time, so the console cannot say whether it is current")
	}

	// The table carries the month — the "how big is this tenant" number — and
	// the instance page carries the ladder.
	page := h.consoleHTML(t, "/instances")
	if !strings.Contains(page, `<td class="num">31</td>`) {
		t.Errorf("the instances table does not show the monthly figure:\n%s", tableHead(page))
	}
	detail := h.consoleHTML(t, "/instances/"+name)
	for _, want := range []string{"5", "14", "31", "96", "day", "week", "month", "year", "reported"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the instance page does not carry %q from the reported ladder", want)
		}
	}

	// A tenancy figure is what a tenant IS, not something that happened, so it
	// belongs on the record rather than in the operations log.
	for _, op := range h.srv.Ops.Recent(OpFilter{Instance: name, Limit: 20}) {
		if op.Kind == "stats" || strings.Contains(op.Note, "members") {
			t.Errorf("a stats report was written to the operations log: %+v", op)
		}
	}
}

// A registry that says nothing about itself must not be shown as an empty one:
// old clients do not know the message, and a zero is a claim where silence is
// not. Nor does a later silence erase a figure already given.
func TestUnreportedSizeReadsAsUnknownNotZero(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})) // no Stats callback

	// The members cell is the one right after the source cell — matched in place
	// rather than by value, since a zero elsewhere in the row (no requests yet)
	// would satisfy a looser check.
	page := h.consoleHTML(t, "/instances")
	if !strings.Contains(page, `</code></td><td class="num">—</td>`) {
		t.Errorf("a registry that reported nothing is not shown as unknown:\n%s", tableHead(page))
	}
	if detail := h.consoleHTML(t, "/instances/"+in.Name); !strings.Contains(detail, "not reported") {
		t.Error("the instance page does not distinguish 'not reported' from 'none'")
	}

	// And a zero from a registry that HAS reported does not wipe the record.
	h.srv.Store.setStats(in.Name, Stats{Week: 9}, true)
	h.srv.Store.setStats(in.Name, Stats{Week: 0}, true)
	if got, _ := h.srv.Store.Get(in.Name); got.Members != 0 {
		t.Errorf("members = %d; a registry that reports zero is reporting zero", got.Members)
	}
}

// The four windows are what the data can honestly answer, and the trend is the
// only history there will ever be — a member key holds one last-seen timestamp,
// overwritten on every use, so nothing before the first report can be
// reconstructed by anyone. What the proxy keeps, it keeps from that day on.
func TestReportedWindowsAccumulateADailyTrend(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	// Two reports in one day: the day's sample is the last thing said, not the
	// first, so a registry that starts quiet and gets busy is recorded busy.
	h.srv.Store.setStats(in.Name, Stats{Day: 2, Week: 5, Month: 9, Year: 20}, true)
	h.srv.Store.setStats(in.Name, Stats{Day: 4, Week: 7, Month: 11, Year: 22}, true)
	got, _ := h.srv.Store.Get(in.Name)
	if len(got.Days) != 1 {
		t.Fatalf("%d samples for one day, want the day's figures held once", len(got.Days))
	}
	if got.Days[0].Day1 != 4 || got.Days[0].Month != 11 {
		t.Errorf("the day kept %+v, want the later report", got.Days[0])
	}
	if got.MembersDay != 4 || got.Members != 7 || got.MembersMonth != 11 || got.MembersYear != 22 {
		t.Errorf("current figures = %d/%d/%d/%d, want 4/7/11/22",
			got.MembersDay, got.Members, got.MembersMonth, got.MembersYear)
	}

	// One point is not a trend, and the page says why rather than drawing a dot.
	detail := h.consoleHTML(t, "/instances/"+in.Name)
	if !strings.Contains(detail, "Member machines") {
		t.Fatal("the instance page has no Member machines panel")
	}
	if strings.Contains(detail, `<svg class="viz"`) {
		t.Error("a single sample was plotted as if it were a series")
	}
	if strings.Contains(detail, `class="spark"`) {
		t.Error("a single sample was drawn as a sparkline")
	}
	if !strings.Contains(detail, "before the first report can be recovered") {
		t.Error("the empty trend does not say why there is no history")
	}

	// A second day makes a series, and it plots.
	got.Days = append([]DaySample{{Day: "2026-07-01", Day1: 1, Week: 3, Month: 6, Year: 10}}, got.Days...)
	h.srv.Store.mu.Lock()
	h.srv.Store.byID[in.Name].Days = got.Days
	h.srv.Store.mu.Unlock()
	detail = h.consoleHTML(t, "/instances/"+in.Name)
	if !strings.Contains(detail, `<svg class="viz"`) {
		t.Error("two days of samples did not produce a chart")
	}
	if !strings.Contains(detail, "2 days kept") {
		t.Error("the trend does not say how much history it holds")
	}
	// The same panel the overview draws, asked about one tenant.
	if !strings.Contains(detail, "as this registry reported them") {
		t.Error("the instance panel does not say whose figures these are")
	}
}

// "Nobody was active yesterday" and "this registry never told us" are different
// answers, and the console has to be able to tell them apart — otherwise the
// commonest true zero in the ladder (a quiet day) reads as missing data to the
// one person the figures exist for.
func TestAReportedZeroIsNotTheSameAsSilence(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	// A current registry, reporting a genuinely quiet day: a figure of 0, not a
	// dash, and nothing calling it unreported.
	h.srv.Store.setStats(in.Name, Stats{Day: 0, Week: 3, Month: 7, Year: 12}, true)
	detail := h.consoleHTML(t, "/instances/"+in.Name)
	panel := between(detail, "Member machines", "</section>")
	if strings.Contains(panel, "not reported") {
		t.Errorf("a reported zero is shown as though nothing was reported:\n%s", panel)
	}
	if !strings.Contains(panel, `<span class="tile-value">0</span>`) {
		t.Errorf("the quiet day is not shown as none:\n%s", panel)
	}

	// A registry from before the windows existed: only the week is a claim.
	h.srv.Store.setStats(in.Name, Stats{Week: 3}, false)
	detail = h.consoleHTML(t, "/instances/"+in.Name)
	panel = between(detail, "Member machines", "</section>")
	if n := strings.Count(panel, "not reported"); n != 3 {
		t.Errorf("%d windows marked unreported, want the day, month and year:\n%s", n, panel)
	}
	if !strings.Contains(panel, `<span class="tile-value">3</span>`) {
		t.Error("the one window it does report was dropped with the others")
	}
	if !strings.Contains(panel, "reports the week only") {
		t.Error("the panel does not say why three of its windows are blank")
	}
}

// The overview adds the tenants up. A total is the number that gets quoted, so
// what it covers travels with it: which registries are in it, which report only
// part of the ladder, and whether any of the figures have gone cold.
func TestOverviewTotalsSayWhatTheyCover(t *testing.T) {
	h := newHarness(t)
	// Three registries: one current, one that reports the week only (built
	// before the other windows existed), one that has never said anything.
	current, _, err := h.srv.Store.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatal(err)
	}
	weekOnly, _, err := h.srv.Store.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.srv.Store.Enroll(NewKey(), 0); err != nil {
		t.Fatal(err)
	}
	h.srv.Store.setStats(current.Name, Stats{Day: 3, Week: 8, Month: 20, Year: 44}, true)
	h.srv.Store.setStats(weekOnly.Name, Stats{Week: 5}, false)

	got := h.srv.memberSummary()
	if got.Published != 3 || got.Reported != 2 || got.Windows != 1 {
		t.Fatalf("coverage = %d published / %d reported / %d with windows, want 3/2/1",
			got.Published, got.Reported, got.Windows)
	}
	// The week takes both registries; the other windows take only the one that
	// reported them. A registry's absent window is not a zero to add.
	if got.Week != 13 {
		t.Errorf("week total = %d, want 8+5", got.Week)
	}
	if got.Day != 3 || got.Month != 20 || got.Year != 44 {
		t.Errorf("day/month/year = %d/%d/%d, want only the registry that reported them",
			got.Day, got.Month, got.Year)
	}

	page := h.consoleHTML(t, "/")
	for _, want := range []string{
		"Member machines", "TODAY", "THIS WEEK", "THIS MONTH", "THIS YEAR",
		"summed across 2 of 3 published registries",
		"reports the week only", // and which windows that leaves short
		"machines, not people",  // one machine in two registries counts twice
	} {
		if !strings.Contains(strings.ToUpper(page), strings.ToUpper(want)) {
			t.Errorf("the overview total does not say %q", want)
		}
	}

	// A figure that has gone cold is still counted, and said to be cold: dropping
	// it would quietly shrink the total instead of qualifying it.
	h.srv.Store.mu.Lock()
	h.srv.Store.byID[current.Name].MembersAt = time.Now().Add(-30 * time.Hour)
	h.srv.Store.mu.Unlock()
	cold := h.srv.memberSummary()
	if cold.Stale != 1 || cold.Week != 13 {
		t.Errorf("stale = %d, week = %d; a cold figure should count and be flagged", cold.Stale, cold.Week)
	}
	if page = h.consoleHTML(t, "/"); !strings.Contains(page, "over an hour old") {
		t.Error("the overview does not say that a figure in the total has gone cold")
	}
}

// Nothing reported yet is a sentence, not four zeros: an ingress carrying
// registries that have not spoken is not an ingress carrying nobody.
func TestOverviewTotalsSayNothingRatherThanZero(t *testing.T) {
	h := newHarness(t)
	h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})) // no Stats callback

	page := h.consoleHTML(t, "/")
	if !strings.Contains(page, "No registry has reported its size yet") {
		t.Errorf("the overview shows totals for figures nobody sent:\n%s",
			between(page, "Member machines", "</section>"))
	}
	if strings.Contains(page, `class="tile-label">Today`) {
		t.Error("empty totals were rendered as tiles of zero")
	}
}

// The sparklines are the totals over the last month, and the way they are
// assembled decides whether they tell the truth on a quiet day.
func TestAggregateSparklinesCarryQuietDaysForward(t *testing.T) {
	h := newHarness(t)
	a, _, err := h.srv.Store.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := h.srv.Store.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatal(err)
	}
	day := func(ago int) string { return time.Now().UTC().AddDate(0, 0, -ago).Format("2006-01-02") }

	// A reports every day; B reports, then goes quiet for a day, then returns.
	// Both have reported at some point, which is what puts them in the totals.
	h.srv.Store.setStats(a.Name, Stats{Day: 3, Week: 6, Month: 13, Year: 23}, true)
	h.srv.Store.setStats(b.Name, Stats{Day: 1, Week: 2, Month: 5, Year: 7}, true)
	h.srv.Store.mu.Lock()
	h.srv.Store.byID[a.Name].Days = []DaySample{
		{Day: day(3), Day1: 1, Week: 4, Month: 10, Year: 20},
		{Day: day(2), Day1: 2, Week: 5, Month: 11, Year: 21},
		{Day: day(1), Day1: 2, Week: 5, Month: 12, Year: 22},
		{Day: day(0), Day1: 3, Week: 6, Month: 13, Year: 23},
	}
	h.srv.Store.byID[b.Name].Days = []DaySample{
		{Day: day(2), Day1: 1, Week: 2, Month: 4, Year: 6},
		// nothing for day(1) — B was offline, which is not B shrinking to zero
		{Day: day(0), Day1: 1, Week: 2, Month: 5, Year: 7},
	}
	h.srv.Store.mu.Unlock()

	_, week, month, _ := h.srv.memberSeries(4)
	if len(week) != 4 {
		t.Fatalf("series has %d points, want one per day asked for", len(week))
	}
	// Day 3: only A existed here. Day 2: both. Day 1: B silent, carried
	// forward, so the total holds rather than dipping. Day 0: both again.
	want := []int{4, 7, 7, 8}
	for i, w := range want {
		if week[i] != w {
			t.Errorf("week series = %v, want %v — a silent day should carry, not collapse", week, want)
			break
		}
	}
	if month[0] != 10 {
		t.Errorf("the first day counts only the registry that had reported by then, got %d", month[0])
	}

	// And the panel draws them, saying what span the lines cover.
	page := h.consoleHTML(t, "/")
	if n := strings.Count(page, `class="spark"`); n != 4 {
		t.Errorf("%d sparklines in the totals panel, want one per window", n)
	}
	// It says the span it actually has, not the span it asks for: four days of
	// history is four days, and promising a month would be a promise about data
	// nobody kept.
	if !strings.Contains(page, "lines show the last 4 days") {
		t.Errorf("the panel does not say what span the sparklines cover:\n%s",
			between(page, "Member machines", "</section>"))
	}
}

// One day of history is not a trend, and a lone point placed by dividing by the
// number of gaps between points is not even arithmetic.
func TestNoSparklineUntilThereIsATrend(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.srv.Store.setStats(in.Name, Stats{Day: 2, Week: 4, Month: 6, Year: 9}, true)

	page := h.consoleHTML(t, "/")
	if strings.Contains(page, `class="spark"`) {
		t.Error("a single day of samples was drawn as a trend line")
	}
	if !strings.Contains(page, `class="tile-value">6`) {
		t.Error("the figures themselves went missing with the sparklines")
	}
	if strings.Contains(page, "lines show the last") {
		t.Error("the panel promises lines it did not draw")
	}
}
