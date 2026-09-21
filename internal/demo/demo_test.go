// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/opentacit/tacit/pkg/contracts"
)

// TestShippedDatasetValidates guards the bundled dataset: it must parse and
// satisfy every invariant the loader relies on, or the demo breaks silently.
func TestShippedDatasetValidates(t *testing.T) {
	d, found, err := ShippedDataset("software-vendor")
	if err != nil {
		t.Fatalf("shipped dataset: %v", err)
	}
	if !found {
		t.Fatal("software-vendor should ship a dataset")
	}
	orgs := 0
	for _, c := range d.Techniques {
		if c.Scope == "org" {
			orgs++
		}
	}
	if orgs*2 < len(d.Techniques) {
		t.Errorf("expected an org-heavy dataset, got %d org of %d", orgs, len(d.Techniques))
	}
}

func TestSynthesizeDeterministic(t *testing.T) {
	d, _, err := ShippedDataset("software-vendor")
	if err != nil {
		t.Fatal(err)
	}
	a, err := Synthesize(d)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Synthesize(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Events) != len(b.Events) {
		t.Fatalf("event count not stable: %d vs %d", len(a.Events), len(b.Events))
	}
	for i := range a.Events {
		if !reflect.DeepEqual(a.Events[i], b.Events[i]) {
			t.Fatalf("event %d differs between runs: %+v vs %+v", i, a.Events[i], b.Events[i])
		}
	}
}

// TestSynthesizeShapesOutcomes checks that expansion produces a well-formed,
// demonstrable funnel: every event carries a full six-dimension segment and a
// valid stage, drafts are never shown, and shown > adopted > helped holds.
func TestSynthesizeShapesOutcomes(t *testing.T) {
	d, _, err := ShippedDataset("software-vendor")
	if err != nil {
		t.Fatal(err)
	}
	drafts := map[string]bool{}
	for _, c := range d.Techniques {
		if c.Draft {
			drafts[c.ID] = true
		}
	}
	res, err := Synthesize(d)
	if err != nil {
		t.Fatal(err)
	}
	var stats Stats
	tally(&stats, res)
	if stats.Members == 0 || stats.Shown == 0 {
		t.Fatal("no members or no shown events synthesized")
	}
	if !(stats.Shown > stats.Adopted && stats.Adopted > stats.Helped) {
		t.Errorf("funnel not monotone: shown=%d adopted=%d helped=%d", stats.Shown, stats.Adopted, stats.Helped)
	}
	dims := []string{"team", "role", "function", "domain", "harness", "surface"}
	for _, e := range res.Events {
		if !slices.Contains(contracts.Stages, e.Stage) {
			t.Fatalf("invalid stage %q", e.Stage)
		}
		if drafts[e.TechniqueID] {
			t.Fatalf("draft %q must never be shown", e.TechniqueID)
		}
		for _, dim := range dims {
			if e.Segment[dim] == "" {
				t.Fatalf("event for %q missing segment dim %q", e.TechniqueID, dim)
			}
		}
	}
}

// TestRebaseWindowTo pins the anchor arithmetic: the rebased window keeps its
// length and its last simulated day is the day before the given one — so a
// "last 30 days" dashboard always contains the month, however old the dataset.
func TestRebaseWindowTo(t *testing.T) {
	d := &Dataset{Window: Window{StartDate: "2026-05-01", Days: 30}}
	d.RebaseWindowTo(time.Date(2026, 7, 17, 15, 4, 0, 0, time.UTC))
	if d.Window.StartDate != "2026-06-17" {
		t.Fatalf("rebased start = %s, want 2026-06-17", d.Window.StartDate)
	}
	if d.Window.Days != 30 {
		t.Fatalf("rebase must not change the length, got %d", d.Window.Days)
	}
	start, err := d.Window.Start()
	if err != nil {
		t.Fatal(err)
	}
	// Last simulated day is start + days - 1 = July 16, the day before the anchor.
	if last := start.AddDate(0, 0, d.Window.Days-1); last.Format("2006-01-02") != "2026-07-16" {
		t.Fatalf("window should end the day before the anchor, ends %s", last.Format("2006-01-02"))
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Query the warehouse with mq!": "query-the-warehouse-with-mq",
		"  Ledger / Billing  ":         "ledger-billing",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripTrailingCommas(t *testing.T) {
	cases := map[string]string{
		`{"a":1,}`:               `{"a":1}`,
		`{"a":[1,2,],}`:          `{"a":[1,2]}`,
		`{"a":1,\n  "b":2\n}`:    `{"a":1,\n  "b":2\n}`,   // no trailing comma: unchanged
		`{"a":"has, } inside",}`: `{"a":"has, } inside"}`, // comma inside a string is preserved
		`{"a":"esc \" ,",}`:      `{"a":"esc \" ,"}`,      // escaped quote handled
	}
	for in, want := range cases {
		if got := string(stripTrailingCommas([]byte(in))); got != want {
			t.Errorf("stripTrailingCommas(%q) = %q, want %q", in, got, want)
		}
	}
}
