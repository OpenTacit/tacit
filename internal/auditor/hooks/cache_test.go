// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"errors"
	"testing"
	"time"
)

// The three registry facts the status line reads (draft count, access mode,
// cohort directory) share one cache, so its two promises are tested once here:
// a stale value starts exactly ONE refresh no matter how many readers see it
// stale, and a refresh that fails leaves the last good value in service. The
// second promise is why a registry blip does not blank the status line.
func TestAsyncCachedRefreshesOnceAndKeepsTheLastGoodValue(t *testing.T) {
	clock := time.Now()
	now := func() time.Time { return clock }
	runner := &deferredRunner{} // park refreshes; the test releases them
	calls, answer, fetchErr := 0, 1, error(nil)
	c := newAsyncCached(time.Minute, now, runner.run, func() (int, error) {
		calls++
		return answer, fetchErr
	})

	// Cold: unknown, and the first read starts the one refresh. Further reads
	// while it is in flight must not start a second.
	if v, ok := c.Get(); ok || v != 0 {
		t.Fatalf("cold read = %d, %v; want 0, false", v, ok)
	}
	c.Get()
	c.Get()
	runner.flush()
	if calls != 1 {
		t.Fatalf("cold reads made %d fetches, want exactly 1", calls)
	}
	if v, ok := c.Get(); !ok || v != 1 {
		t.Fatalf("after the refresh = %d, %v; want 1, true", v, ok)
	}

	// Fresh: served from memory, no fetch at all.
	clock = clock.Add(30 * time.Second)
	c.Get()
	runner.flush()
	if calls != 1 {
		t.Fatalf("a fresh value made %d fetches, want 1", calls)
	}

	// Stale: the readers get the old value straight away, and between them they
	// start exactly one refresh.
	clock = clock.Add(time.Minute)
	answer = 2
	for range 3 {
		if v, ok := c.Get(); !ok || v != 1 {
			t.Fatalf("stale read = %d, %v; want the prior 1, true", v, ok)
		}
	}
	runner.flush()
	if calls != 2 {
		t.Fatalf("stale reads made %d fetches in total, want 2", calls)
	}
	if v, _ := c.Get(); v != 2 {
		t.Fatalf("after the second refresh = %d, want 2", v)
	}
	runner.flush()

	// A failed refresh keeps the prior value and its "known" verdict: the
	// caller never sees a zero because the registry was down for a moment.
	clock = clock.Add(2 * time.Minute)
	fetchErr = errors.New("registry down")
	answer = 99
	c.Get()
	runner.flush()
	if v, ok := c.Get(); !ok || v != 2 {
		t.Fatalf("after a failed refresh = %d, %v; want the prior 2, true", v, ok)
	}
	runner.flush()

	// And the failure did not wedge the single-flight guard: the next stale
	// read still refreshes.
	fetchErr = nil
	before := calls
	c.Get()
	runner.flush()
	if calls != before+1 {
		t.Fatalf("after a failure the next read made %d fetches, want 1", calls-before)
	}
	if v, _ := c.Get(); v != 99 {
		t.Fatalf("recovered value = %d, want 99", v)
	}
}
