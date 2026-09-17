// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// testClock is a clock a test moves by hand, so a window boundary is something
// to step over rather than wait for.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	// An arbitrary instant that is not on a minute or hour boundary, so a test
	// that passes only because it started at :00 fails here.
	return &testClock{t: time.Date(2026, 8, 24, 11, 37, 21, 0, time.UTC)}
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// A per-minute ceiling lets the first N through and refuses the rest. With the
// clock pinned this says exactly that, rather than "as long as the test did not
// straddle a minute boundary".
func TestRequestCeilingRefusesTheExcessWithinOneWindow(t *testing.T) {
	h := newHarness(t)
	clk := newTestClock()
	h.srv.now = clk.now // before publish: a tunnel takes the clock at construction
	h.srv.Cfg.RatePerMinute = 3
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	var limited int
	for range 6 {
		if resp, _ := h.get(in.Name, "/v1/health"); resp.StatusCode == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited != 3 {
		t.Errorf("limited %d of 6 requests, want 3 past a ceiling of 3", limited)
	}
}

// The window resets on its own boundary, so an instance that waits gets its
// full allowance again. This is the case the old wall-clock test could not
// state: it would have had to sleep a minute to reach it.
func TestTheRequestCeilingResetsOnTheNextWindow(t *testing.T) {
	h := newHarness(t)
	clk := newTestClock()
	h.srv.now = clk.now
	h.srv.Cfg.RatePerMinute = 2
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	spend := func() (ok, refused int) {
		for range 4 {
			resp, _ := h.get(in.Name, "/v1/health")
			if resp.StatusCode == http.StatusTooManyRequests {
				refused++
			} else {
				ok++
			}
		}
		return ok, refused
	}

	if ok, refused := spend(); ok != 2 || refused != 2 {
		t.Fatalf("first window: %d served, %d refused; want 2 and 2", ok, refused)
	}
	clk.advance(time.Minute)
	if ok, refused := spend(); ok != 2 || refused != 2 {
		t.Fatalf("after the boundary: %d served, %d refused; want the allowance back", ok, refused)
	}
}

// The enrolment ceiling is per source address and hourly. It had no test at all
// before, because reaching the reset meant waiting an hour.
func TestEnrolmentCeilingIsPerAddressAndResetsHourly(t *testing.T) {
	h := newHarness(t)
	clk := newTestClock()
	h.srv.now = clk.now
	h.srv.Cfg.EnrollPerHour = 2

	allowed := func(remote string) int {
		n := 0
		for range 3 {
			if h.srv.enrollAllowed(remote) {
				n++
			}
		}
		return n
	}
	if n := allowed("10.0.0.1:5000"); n != 2 {
		t.Errorf("allowed %d of 3 from one address, want 2", n)
	}
	// A different address has its own count — one script must not close the
	// door on everyone else.
	if n := allowed("10.0.0.2:5000"); n != 2 {
		t.Errorf("allowed %d of 3 from a second address, want 2", n)
	}
	clk.advance(time.Hour)
	if n := allowed("10.0.0.1:5000"); n != 2 {
		t.Errorf("allowed %d of 3 after the hour rolled over, want the allowance back", n)
	}
}

// A ceiling of zero or less is off, not a ban.
func TestAZeroCeilingMeansNoCeiling(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.EnrollPerHour = 0
	for range 50 {
		if !h.srv.enrollAllowed("10.0.0.9:1234") {
			t.Fatal("a zero EnrollPerHour refused an enrolment")
		}
	}
	tn := &tunnel{name: "x"}
	for range 50 {
		if !tn.allow(0) {
			t.Fatal("a zero RatePerMinute refused a request")
		}
	}
}

// The counter is per key and resets whole, which is what makes it coarse: a
// burst straddling a boundary counts as two smaller bursts, and that is the
// deliberate trade.
func TestFixedWindowCountsPerKeyAndResetsWhole(t *testing.T) {
	w := fixedWindow{every: time.Minute}
	base := newTestClock().now()
	if got := w.hit(base, "a"); got != 1 {
		t.Errorf("first hit = %d, want 1", got)
	}
	if got := w.hit(base.Add(30*time.Second), "a"); got != 2 {
		t.Errorf("second hit in the same window = %d, want 2", got)
	}
	if got := w.hit(base, "b"); got != 1 {
		t.Errorf("a different key started at %d, want 1", got)
	}
	if got := w.hit(base.Add(time.Minute), "a"); got != 1 {
		t.Errorf("hit after the boundary = %d, want the count reset to 1", got)
	}
	if got := w.hit(base.Add(time.Minute), "b"); got != 1 {
		t.Errorf("the reset kept a stale count for another key: %d, want 1", got)
	}
}
