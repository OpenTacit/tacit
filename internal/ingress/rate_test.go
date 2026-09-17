// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/oidc"
)

// The chart's numbers are a fold over the log, so the fold is what has to be
// right: every request in the window lands in the bucket it happened in, tunnel
// events are not requests, and nothing outside the window counts.
func TestRateBucketsRequestsIntoTheirWindow(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenOpLog(dir, 1<<20, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	base := time.Now().UTC().Truncate(time.Hour).Add(-6 * time.Hour)
	add := func(offset time.Duration, instance string, kind string) {
		l.Append(Op{TS: base.Add(offset), Instance: instance, Kind: kind, Status: 200})
	}
	add(-time.Minute, "cedar", KindRequest) // before the window: not counted
	add(5*time.Minute, "cedar", KindRequest)
	add(10*time.Minute, "cedar", KindRequest)
	add(10*time.Minute, "cedar", KindTunnelUp) // a tunnel event is not a request
	add(65*time.Minute, "cedar", KindRequest)
	add(65*time.Minute, "birch", KindRequest)
	add(4*time.Hour, "birch", KindRequest)

	res := l.Rate(base, base.Add(5*time.Hour), time.Hour, false)
	if res.Total != 5 {
		t.Fatalf("total = %d, want the 5 requests inside the window", res.Total)
	}
	if len(res.Buckets) != 5 {
		t.Fatalf("got %d buckets, want 5 hours", len(res.Buckets))
	}
	want := []int{2, 2, 0, 0, 1}
	for i, w := range want {
		if got := res.Buckets[i].Counts[""]; got != w {
			t.Errorf("hour %d holds %d requests, want %d", i, got, w)
		}
	}

	// Split by instance, the same records answer per-registry.
	byInst := l.Rate(base, base.Add(5*time.Hour), time.Hour, true)
	if byInst.Total != 5 {
		t.Fatalf("split total = %d, want 5", byInst.Total)
	}
	if got := byInst.Buckets[1].Counts["cedar"]; got != 1 {
		t.Errorf("cedar's second hour = %d, want 1", got)
	}
	if got := byInst.Buckets[1].Counts["birch"]; got != 1 {
		t.Errorf("birch's second hour = %d, want 1", got)
	}
	// Busiest first, so the legend reads in the order the chart matters in.
	if len(byInst.Names) != 2 || byInst.Names[0] != "cedar" {
		t.Errorf("series order = %v, want cedar (3) before birch (2)", byInst.Names)
	}
}

// Zooming must not be able to ask for a chart with ten thousand points in it:
// the bucket widens with the window, and always to a round unit.
func TestRateBucketKeepsThePointCountBounded(t *testing.T) {
	for _, span := range []time.Duration{
		15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour,
		7 * 24 * time.Hour, 30 * 24 * time.Hour, 90 * 24 * time.Hour,
	} {
		b := rateBucketFor(span)
		if n := span / b; n > ratePoints {
			t.Errorf("a %s window buckets into %d points at %s each, over the %d cap", span, n, b, ratePoints)
		}
		var round bool
		for _, l := range rateLadder {
			if b == l {
				round = true
			}
		}
		if !round {
			t.Errorf("a %s window chose %s, which is not one of the ladder's units", span, b)
		}
	}
}

// The window a request asks for is not the window it gets: the future holds no
// records, and the log holds nothing past retention.
func TestRateViewClampsTheWindow(t *testing.T) {
	s := &Server{}
	now := time.Now()

	// A window that runs into the future slides back to end now, keeping its span.
	q := map[string][]string{
		"from": {ms(now.Add(-time.Hour))},
		"to":   {ms(now.Add(3 * time.Hour))},
	}
	v := s.parseRateView(q)
	if v.to.After(now.Add(time.Minute)) {
		t.Errorf("window ends at %s, in the future", v.to)
	}
	if span := v.to.Sub(v.from); span < 3*time.Hour || span > 5*time.Hour {
		t.Errorf("sliding the window changed its span to %s, want the 4h asked for", span)
	}

	// Too narrow, and too wide.
	v = s.parseRateView(map[string][]string{"from": {ms(now.Add(-time.Second))}, "to": {ms(now)}})
	if got := v.to.Sub(v.from); got < rateMinSpan {
		t.Errorf("span %s is under the %s floor", got, rateMinSpan)
	}
	v = s.parseRateView(map[string][]string{"from": {ms(now.Add(-5000 * 24 * time.Hour))}, "to": {ms(now)}})
	if got := v.to.Sub(v.from); got > rateMaxSpan {
		t.Errorf("span %s is over the %s ceiling", got, rateMaxSpan)
	}
}

// ms is a time as the query carries it: unix milliseconds.
func ms(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

// A line's colour identifies an instance, so it must not depend on who else is
// on the chart. Zooming into a quiet hour drops the other registries from the
// window; the one that remains has to keep the colour it had.
func TestSeriesColourFollowsTheInstanceNotTheRanking(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	alone := h.srv.rateSlots([]string{in.Name})
	crowded := h.srv.rateSlots([]string{"zulu-meadow", in.Name, "alpha-field"})
	if alone[in.Name] != crowded[in.Name] {
		t.Errorf("the published instance is %q alone and %q beside others — a window that "+
			"drops a series repaints the ones that stayed", alone[in.Name], crowded[in.Name])
	}
	// Every slot is distinct, and each is a key the stylesheet draws.
	seen := map[string]bool{}
	for name, key := range crowded {
		if seen[key] {
			t.Errorf("%s reuses the series key %q", name, key)
		}
		seen[key] = true
	}
}

// The overview trades a table that repeated the Operations page for a chart that
// answers a question no table does: what the traffic has been doing.
func TestOverviewChartsRequestsInsteadOfRepeatingTheOpsTable(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	now := time.Now().UTC()
	for i := 0; i < 40; i++ {
		h.srv.Ops.Append(Op{TS: now.Add(-time.Duration(i) * 3 * time.Minute), Instance: in.Name,
			Method: "GET", Path: "/v1/health", Status: 200})
	}

	page := h.consoleHTML(t, "/")
	if strings.Contains(page, "Recent operations") {
		t.Error("the overview still carries the panel that repeated the Operations page")
	}
	for _, want := range []string{
		`<h2>Requests over time</h2>`,
		`id="rate-chart"`,
		`class="btn rate-span" type="button" data-span="86400000" aria-pressed="true"`, // 24h by default
		`class="btn rate-by" type="button" data-by="instance"`,
		`<svg class="viz"`,
		`href="/ops"`, // the records themselves are still one link away
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the overview is missing %s", want)
		}
	}

	// The fetch behind zoom and pan answers with a chart fragment, not a page.
	frag := h.consoleHTML(t, "/overview/chart?by=instance")
	if strings.Contains(frag, "<html") || strings.Contains(frag, "<header") {
		t.Error("the chart endpoint returned a whole document; it is swapped into a panel")
	}
	if !strings.Contains(frag, `<svg class="viz"`) || !strings.Contains(frag, in.Name) {
		t.Errorf("the by-instance fragment does not plot the published instance:\n%s", frag)
	}
	// A window with nothing in it still draws its plot — that plot is the surface
	// the reader drags and scrolls on to get back to where the traffic is.
	old := now.Add(-60 * 24 * time.Hour).UnixMilli()
	empty := h.consoleHTML(t, "/overview/chart?from="+strconv.FormatInt(old, 10)+
		"&to="+strconv.FormatInt(old+3600*1000, 10))
	if !strings.Contains(empty, `<svg class="viz"`) {
		t.Error("an empty window renders no plot, leaving nothing to pan back with")
	}
	if !strings.Contains(empty, "no requests between") {
		t.Errorf("an empty window does not say so in words:\n%s", empty)
	}
}

// The chart endpoint reads the same log the pages do, so it answers to the same
// admission check rather than being an open side door into the record.
func TestChartEndpointIsGatedLikeThePages(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.OIDCIssuer = "https://accounts.example.com"
	h.srv.OIDC = &oidc.Provider{Issuer: "https://accounts.example.com", ClientID: "console",
		Secret: []byte("test-secret"), TTL: time.Hour}

	req, _ := http.NewRequest("GET", h.public.URL+"/overview/chart", nil)
	req.Host = "ingress.test.local"
	resp, err := h.public.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 {
		t.Errorf("the chart served its data to a visitor with no session: %.120q", body)
	}
}
