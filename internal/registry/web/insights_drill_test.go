// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"
)

// Every technique-list drill-down renders the same body in the same order: page
// head, time period, KPI tiles, Activity panel, then the technique table. The
// pages differ only in wording, so pin the order once here — it is the contract
// the shared builder has to keep.
func TestInsightDrillBodyOrder(t *testing.T) {
	_, ts := newServer(t)
	batch := `[{"technique_id":"ask-for-a-diagram","stage":"shown","segment":{"harness":"claude-code"}},
	           {"technique_id":"ask-for-a-diagram","stage":"adopted","segment":{"harness":"claude-code"}},
	           {"technique_id":"ask-for-a-diagram","stage":"helped","segment":{"harness":"claude-code"}}]`
	request(t, "POST", ts.URL+"/v1/feedback", "test-key", batch)

	for _, tc := range []struct {
		path  string
		order []string
	}{
		{"/outcomes/tag/diagram?w=all", []string{
			`class="page-head"`, `class="window-select"`, `class="tile-row"`,
			// The landmarks are structural. They used to include the caption
			// telling the reader to click a row, which the row's own hover and
			// link already say -- so the order is now asserted against the
			// panel and the rows themselves.
			`<h2>Activity</h2>`, `<table`,
			`data-href="/techniques/ask-for-a-diagram`,
		}},
		{"/outcomes/source/curated?w=all", []string{
			`class="page-head"`, `class="window-select"`, `<h2>Activity</h2>`, `<table`,
			`data-href="/techniques/ask-for-a-diagram`,
		}},
		{"/outcomes/cohorts/harness:claude-code?w=all", []string{
			`class="page-head"`, `class="window-select"`, `class="tile-row"`,
			`<h2>Activity</h2>`, `<h2>Techniques this cohort engaged</h2>`,
			`data-href="/techniques/ask-for-a-diagram`,
		}},
	} {
		code, body := fetchHTML(t, ts.URL+tc.path)
		if code != 200 {
			t.Fatalf("%s = %d", tc.path, code)
		}
		at := -1
		for _, want := range tc.order {
			i := strings.Index(body, want)
			if i < 0 {
				t.Fatalf("%s is missing %q", tc.path, want)
			}
			if i < at {
				t.Fatalf("%s renders %q out of order", tc.path, want)
			}
			at = i
		}
	}

	// The dismissals drill carries neither tiles nor a chart: a reason list is a
	// count, not a funnel.
	_, dismissals := fetchHTML(t, ts.URL+"/outcomes/dismissals/not-relevant?w=all")
	if strings.Contains(dismissals, `<h2>Activity</h2>`) {
		t.Fatal("dismissals drill grew an Activity panel")
	}

	// An empty slice keeps the head and the time period, and says so in the page's
	// own words instead of listing nothing.
	_, empty := fetchHTML(t, ts.URL+"/outcomes/tag/no-such-tag?w=all")
	if !strings.Contains(empty, `class="window-select"`) ||
		!strings.Contains(empty, `class="empty"`) ||
		strings.Contains(empty, `<h2>Activity</h2>`) {
		t.Fatal("empty tag drill lost its window selector or grew a chart")
	}
}
