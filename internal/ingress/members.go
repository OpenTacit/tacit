// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"fmt"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/ui"
)

// What all the tenants add up to: the size each registry reports, summed across
// the ones that reported, and the same four figures over time.

// memberTotals is the size of everything this ingress carries, and how much of
// it is actually known.
//
// The coverage fields are not decoration. A total that silently omits the
// registries which have not reported is a smaller number presented as the whole,
// and the operator reading it is deciding what this service costs to run.
type memberTotals struct {
	Stats             // summed across the instances that reported
	Published int     // instances on the route table
	Reported  int     // instances that have reported anything
	Windows   int     // instances that reported all four windows
	Stale     int     // reported instances whose figures are older than staleMembers
	Oldest    float64 // hours since the oldest figure in the total, 0 when all are fresh
}

// memberSummary adds up what the tenants last reported.
//
// Summing across registries is sound in a way that summing within one would not
// be: each registry mints its own member keys, so a machine joined to two of
// them is two keys and counts twice — which is the truthful answer to "how many
// machines does this proxy carry", and the wrong answer to "how many people are
// there". The console says machines for that reason.
//
// A registry that reported only a week (built before the other windows existed)
// contributes to the week alone. Its zeros are not claims and are not summed as
// though they were.
func (s *Server) memberSummary() memberTotals {
	var t memberTotals
	now := time.Now()
	for _, in := range s.Store.List() {
		t.Published++
		if in.MembersAt.IsZero() {
			continue
		}
		t.Reported++
		t.Week += in.Members
		if age := now.Sub(in.MembersAt); age > staleMembers {
			t.Stale++
			if h := age.Hours(); h > t.Oldest {
				t.Oldest = h
			}
		}
		if !in.MembersWindows {
			continue
		}
		t.Windows++
		t.Day += in.MembersDay
		t.Month += in.MembersMonth
		t.Year += in.MembersYear
	}
	return t
}

// memberTrendDays is how far back the overview's sparklines reach. A month is
// enough to show a direction in a figure that moves when people join a team,
// and short enough that the line still has shape at 120 pixels wide.
const memberTrendDays = 30

// memberSeries is the aggregate daily series behind the sparklines: one value
// per day per window, summed across instances.
//
// Each instance contributes its most recent sample AT OR BEFORE each day, and
// nothing at all before its first sample. That carry-forward matters: a registry
// that was offline on Sunday reported nothing on Sunday, and summing only what
// arrived would draw a cliff on every quiet day — a change in reporting read as
// a change in size. Carrying the last known figure says what was known then,
// which is what a trend of size means.
func (s *Server) memberSeries(days int) (day, week, month, year []int) {
	return memberSeriesOf(s.Store.List(), days)
}

// memberSeriesOf is memberSeries over a chosen set of instances — every one for
// the proxy's total, a single one for that tenant's own panel.
// The walk below relies on Instance.Days being oldest first, which is how the
// store keeps it (store.go, setStats): a day's sample is appended, or the last
// one is replaced. That order is what lets one instance be read once for the
// whole series rather than searched again for every day.
func memberSeriesOf(instances []Instance, days int) (day, week, month, year []int) {
	// cursor walks one instance's samples. at is the newest sample at or before
	// the day being summed, and -1 until the instance's first sample is reached.
	type cursor struct {
		days []DaySample
		at   int
	}
	all := make([]*cursor, 0, len(instances))
	// The series starts when the data does. Days before any registry had
	// reported are not zeros to plot — nothing was carrying nobody then, the
	// proxy simply had not been told anything, and a month of flat zero followed
	// by a step is a story about when recording began rather than about size.
	earliest := ""
	for _, in := range instances {
		if len(in.Days) == 0 {
			continue
		}
		all = append(all, &cursor{days: in.Days, at: -1})
		if earliest == "" || in.Days[0].Day < earliest {
			earliest = in.Days[0].Day
		}
	}
	if len(all) == 0 {
		return nil, nil, nil, nil
	}
	today := time.Now().UTC()
	for i := days - 1; i >= 0; i-- {
		date := today.AddDate(0, 0, -i).Format("2006-01-02")
		if date < earliest {
			continue
		}
		var d, w, m, y int
		for _, c := range all {
			// Advance to the newest sample at or before this day. Days are
			// summed oldest first, so the cursor only ever moves forward, and
			// leaving it where it stops is the carry-forward: a registry that
			// reported nothing today keeps yesterday's figure.
			for c.at+1 < len(c.days) && c.days[c.at+1].Day <= date {
				c.at++
			}
			if c.at < 0 {
				continue // this registry did not exist here yet
			}
			s := c.days[c.at]
			d += s.Day1
			w += s.Week
			m += s.Month
			y += s.Year
		}
		day, week = append(day, d), append(week, w)
		month, year = append(month, m), append(year, y)
	}
	return day, week, month, year
}

// memberView is everything the Member machines panel draws. The proxy's total
// and a single registry's page fill it in differently and get the same panel:
// same four windows, same sparklines, same sentence saying what they cover —
// because "how big is this" is one question whether it is asked of one tenant or
// all of them, and two answers shaped differently would invite comparing them
// wrongly.
type memberView struct {
	Stats               // the four figures
	Reported   bool     // anything reported at all
	HasWindows bool     // the day, month and year were reported, not just the week
	Series     [4][]int // the same four over time, oldest first
	Coverage   string   // the line under the heading
	Empty      string   // what to say when nothing has been reported
	Below      string   // markup under the tiles (a tenant's full history)
}

// memberPanelHTML draws the panel. A window nobody reported is a dash, never a
// zero, and a series of fewer than two days draws no line rather than a dot
// pretending to a direction.
func memberPanelHTML(v memberView) string {
	if !v.Reported {
		return ui.Panel("Member machines", "", `<p class="empty">`+ui.Esc(v.Empty)+`</p>`)
	}
	figures := []struct {
		label string
		n     int
		known bool
		i     int
	}{
		{"Today", v.Day, v.HasWindows, 0},
		{"This week", v.Week, true, 1},
		{"This month", v.Month, v.HasWindows, 2},
		{"This year", v.Year, v.HasWindows, 3},
	}
	cells := make([]string, 0, len(figures))
	for _, f := range figures {
		if !f.known {
			cells = append(cells, ui.Tile(f.label, "—", "not reported"))
			continue
		}
		cells = append(cells, ui.SparkTile(f.label, ui.FmtCount(f.n), "", string(ui.Sparkline(v.Series[f.i]))))
	}
	return ui.Panel("Member machines", v.Coverage, ui.Tiles(cells...)+v.Below)
}

// memberPanel is the overview's answer to "how much is this proxy carrying".
//
// Four windows, and a line saying what the total covers: which registries are in
// it, whether any of their figures have gone cold, and that these are machines
// rather than people. A number this size gets quoted; it should arrive with the
// caveats attached rather than acquiring them later.
func (s *Server) memberPanel() string {
	t := s.memberSummary()
	empty := "No registry has reported its size yet."
	if t.Published == 0 {
		empty = "Nothing is published here yet."
	}
	day, week, month, year := s.memberSeries(memberTrendDays)
	return memberPanelHTML(memberView{
		Stats:      t.Stats,
		Reported:   t.Reported > 0,
		HasWindows: t.Windows > 0,
		Series:     [4][]int{day, week, month, year},
		Coverage:   memberCoverage(t, len(day)),
		Empty:      empty,
	})
}

// memberCoverage is the sentence under the totals: what is in them, what is not,
// and what they count.
func memberCoverage(t memberTotals, trendDays int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "machines, not people — a machine joined to two registries counts in both · summed across %d of %d published %s",
		t.Reported, t.Published, plural(t.Published, "registry", "registries"))
	if t.Windows == 0 {
		b.WriteString(" · none of them reports the day, month or year yet, so those have nothing to add up")
	} else if t.Windows < t.Reported {
		fmt.Fprintf(&b, " · %d of them reports the week only, so the day, month and year cover %d",
			t.Reported-t.Windows, t.Windows)
	}
	if t.Stale > 0 {
		fmt.Fprintf(&b, " · %d figure%s over an hour old (the oldest %s)",
			t.Stale, map[bool]string{true: "s"}[t.Stale > 1], humanHours(t.Oldest))
	}
	if trendDays > 1 {
		fmt.Fprintf(&b, " · lines show the last %d days", trendDays)
	}
	return b.String()
}

// plural picks the right noun for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// humanHours says an age in the largest unit that stays honest about it.
func humanHours(h float64) string {
	switch {
	case h >= 48:
		return fmt.Sprintf("%.0f days", h/24)
	case h >= 24:
		return "a day"
	case h < 2:
		return "an hour"
	default:
		return fmt.Sprintf("%.0f hours", h)
	}
}
