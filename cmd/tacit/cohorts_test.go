// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/client"
)

func dir() client.CohortDirectory {
	return client.CohortDirectory{
		Settable: []string{"team", "role", "function", "domain"},
		Dimensions: []client.CohortDimension{
			{Dimension: "team", Values: []client.CohortUse{
				{Value: "payments", Sessions: 12, Shown: 40},
				{Value: "platform", Sessions: 9, Shown: 22},
			}},
			{Dimension: "role", Values: []client.CohortUse{
				{Value: "engineer", Sessions: 18, Shown: 60},
			}},
			{Dimension: "harness", Derived: true, Values: []client.CohortUse{
				{Value: "claude-code", Sessions: 21, Shown: 70},
			}},
		},
	}
}

// TestCohortReportOffersRealValues: the whole point of the report is that the
// command it prints is copy-pasteable and made of cohorts that exist, so a
// member joins one instead of translating a placeholder into a new spelling.
func TestCohortReportOffersRealValues(t *testing.T) {
	out := cohortReport("https://tacit.example.com", dir(), nil)
	if !strings.Contains(out, "tacit connect --segment team=payments,role=engineer") {
		t.Fatalf("want a copy-pasteable command built from the top values:\n%s", out)
	}
	if !strings.Contains(out, "payments 12") || !strings.Contains(out, "platform 9") {
		t.Fatalf("want the values with their session counts:\n%s", out)
	}
	// A dimension nobody uses is still an invitation; its absence would read
	// as "not available here".
	if !strings.Contains(out, "function") || !strings.Contains(out, "nobody has set this yet") {
		t.Fatalf("want unused settable dimensions listed as empty:\n%s", out)
	}
	if !strings.Contains(out, "[set for you]") {
		t.Fatalf("want derived dimensions marked as not-a-choice:\n%s", out)
	}
	if !strings.Contains(out, "Your cohort: not set") {
		t.Fatalf("want the member's own state:\n%s", out)
	}
}

// TestCohortReportFallsBackToSuggestions: a registry can carry feedback events
// without audit facts, and a column of zeroes would read as a broken feature
// rather than a missing log. One unit for the whole report, named in the
// header, never the two mixed down one line.
func TestCohortReportFallsBackToSuggestions(t *testing.T) {
	d := dir()
	for i := range d.Dimensions {
		for j := range d.Dimensions[i].Values {
			d.Dimensions[i].Values[j].Sessions = 0
		}
	}
	out := cohortReport("https://tacit.example.com", d, nil)
	if !strings.Contains(out, "how many suggestions have gone to it") {
		t.Fatalf("want the unit named as suggestions:\n%s", out)
	}
	if !strings.Contains(out, "payments 40") || !strings.Contains(out, "platform 22") {
		t.Fatalf("want the shown counts, not zeroes:\n%s", out)
	}
	// One session anywhere is enough to prefer the better measure.
	d.Dimensions[0].Values[1].Sessions = 3
	out = cohortReport("https://tacit.example.com", d, nil)
	if !strings.Contains(out, "how many sessions") || !strings.Contains(out, "platform 3") {
		t.Fatalf("want sessions once any exist:\n%s", out)
	}
	if strings.Contains(out, "payments 40") {
		t.Fatalf("units must not mix down one line:\n%s", out)
	}
}

// TestCohortReportFlagsALonelyValue: a mistyped cohort is invisible — nothing
// rejects it, the member simply files under a cohort of one. The report is the
// one place the correct spellings are already on screen, so it says so there.
func TestCohortReportFlagsALonelyValue(t *testing.T) {
	out := cohortReport("https://tacit.example.com", dir(), map[string]string{
		"team": "paymnets", "role": "engineer",
	})
	if !strings.Contains(out, "No other session here uses team=paymnets") {
		t.Fatalf("want the typo named:\n%s", out)
	}
	if strings.Contains(out, "role=engineer or") || strings.Contains(out, "or role=engineer") {
		t.Fatalf("a value colleagues DO use must not be flagged:\n%s", out)
	}
	if !strings.Contains(out, "(yours)") {
		t.Fatalf("want the member's own value marked in the list:\n%s", out)
	}
	// The suggestion must be the NEAREST value, never the most popular one:
	// telling someone who typed `paymnets` to join `platform` is confidently
	// wrong about which team they are on.
	if !strings.Contains(out, "The near match is team=payments.") ||
		!strings.Contains(out, "tacit connect --segment team=payments\n") {
		t.Fatalf("want the near match, not the top cohort:\n%s", out)
	}
}

// TestCohortReportNewCohortIsNotATypo: a value unlike anything on the registry
// is a new team, and telling them to join the nearest string would be worse
// than silence.
func TestCohortReportNewCohortIsNotATypo(t *testing.T) {
	out := cohortReport("https://tacit.example.com", dir(), map[string]string{"team": "identity"})
	if strings.Contains(out, "The near match is") {
		t.Fatalf("nothing here is close to `identity`:\n%s", out)
	}
	if !strings.Contains(out, "If it is a new cohort, nothing to do") {
		t.Fatalf("want the new-cohort wording:\n%s", out)
	}
}

// TestNearestScalesToTheWord: two typos in `sre` is a different cohort; two in
// `data-platform` is a slip.
func TestNearestScalesToTheWord(t *testing.T) {
	d := client.CohortDirectory{
		Settable: []string{"team"},
		Dimensions: []client.CohortDimension{{Dimension: "team", Values: []client.CohortUse{
			{Value: "sre"}, {Value: "data-platform"},
		}}},
	}
	if got := nearest(d, "team", "dat-platfrm"); got != "data-platform" {
		t.Fatalf("nearest(dat-platfrm) = %q", got)
	}
	if got := nearest(d, "team", "ops"); got != "" {
		t.Fatalf("nearest(ops) = %q, want no suggestion", got)
	}
	if got := nearest(d, "team", "sr"); got != "sre" {
		t.Fatalf("nearest(sr) = %q", got)
	}
}

// TestCohortReportCaseCollision: `Payments` and `payments` are two cohorts to
// every rollup in the registry, which is exactly the collision worth catching
// — so the lonely check folds case even though the values do not.
func TestCohortReportCaseCollision(t *testing.T) {
	out := cohortReport("https://tacit.example.com", dir(), map[string]string{"team": "Payments"})
	if strings.Contains(out, "No other session here uses") {
		t.Fatalf("a case variant is a collision, not a new cohort:\n%s", out)
	}
}

// TestCohortReportFirstMember: an empty registry must not draw an empty menu.
// The first joiner genuinely has to invent a name, and is told why it matters.
func TestCohortReportFirstMember(t *testing.T) {
	empty := client.CohortDirectory{Settable: []string{"team", "role", "function", "domain"}}
	out := cohortReport("https://tacit.example.com", empty, nil)
	if !strings.Contains(out, "you are the first") {
		t.Fatalf("want the cold-start wording:\n%s", out)
	}
	if !strings.Contains(out, "team=<your-team>") {
		t.Fatalf("want a placeholder command when there is nothing to copy:\n%s", out)
	}
	if strings.Contains(out, "nobody has set this yet") {
		t.Fatalf("cold start says it once, not once per dimension:\n%s", out)
	}
}

// TestCohortLineTruncates: forty teams is a readable line plus a count, never
// a dropped tail.
func TestCohortLineTruncates(t *testing.T) {
	var vals []client.CohortUse
	for i := range 12 {
		vals = append(vals, client.CohortUse{Value: string(rune('a'+i)) + "-team", Sessions: 12 - i})
	}
	line := cohortLine("team", vals, false, "", func(u client.CohortUse) int { return u.Sessions })
	if !strings.Contains(line, "+4 more") {
		t.Fatalf("want the tail counted, got: %s", line)
	}
	if strings.Contains(line, "l-team") {
		t.Fatalf("want the tail elided, got: %s", line)
	}
}

// TestFormatSegmentOrder: a member reads team before role before the rest,
// whichever order the map iterates in.
func TestFormatSegmentOrder(t *testing.T) {
	got := formatSegment(map[string]string{"role": "engineer", "zebra": "z", "team": "payments"})
	if got != "team=payments,role=engineer,zebra=z" {
		t.Fatalf("got %q", got)
	}
	if got := formatSegment(nil); got != "not set" {
		t.Fatalf("got %q", got)
	}
}
