// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"strings"
	"testing"
	"time"
)

// The Overview's tile and the sentence under its chart count the same requests
// over the same window. They must therefore write the number the same way: a
// reader should not have to work out that "1.5k" and "1,500" are one figure
// said twice.
func TestOverviewWritesTheSameCountTheSameWay(t *testing.T) {
	h := newHarness(t)
	in, _, err := h.srv.Store.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < 1500; i++ {
		h.srv.Ops.Append(Op{TS: now.Add(-time.Duration(i) * time.Second), Instance: in.Name,
			Method: "GET", Path: "/v1/health", Status: 200})
	}
	h.srv.Metrics = NewMetrics()
	h.srv.Metrics.RebuildFrom(h.srv.Ops, MetricsWindow)

	page := h.consoleHTML(t, "/")
	for _, want := range []string{
		`<span class="tile-value">1,500</span>`, // Requests served
		`1,500 in this window`,                  // under the chart
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the overview does not say %q; the two counters disagree about how to write a number", want)
		}
	}
}
