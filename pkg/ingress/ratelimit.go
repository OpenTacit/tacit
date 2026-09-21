// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import "time"

// The two ceilings the ingress enforces are the same counter twice: requests per
// instance per minute (proxy.go) and new enrolments per source address per hour
// (server.go). Both are fixed windows, and both used to read the clock
// themselves, which made them untestable except by waiting.

// fixedWindow counts events per key inside a window that resets on its own
// boundary. It is deliberately coarse: a burst straddling a boundary counts as
// two smaller bursts, and that is acceptable because these ceilings exist to
// stop a script claiming everything, not to meter anybody accurately.
//
// It carries no lock. Each owner already holds one over the state around it and
// takes it across the call.
type fixedWindow struct {
	every time.Duration
	slot  int64 // which window the counts belong to
	count map[string]int
}

// hit records one event for key and reports how many have landed in the current
// window, this one included. A caller compares that with its own ceiling.
func (w *fixedWindow) hit(now time.Time, key string) int {
	slot := now.UnixNano() / int64(w.every)
	if w.count == nil || w.slot != slot {
		w.slot, w.count = slot, map[string]int{}
	}
	w.count[key]++
	return w.count[key]
}

// clockOf is the clock a component was given, or the real one. A Server built
// by New always has one; a tunnel a test constructs by hand does not.
func clockOf(now func() time.Time) time.Time {
	if now == nil {
		return time.Now()
	}
	return now()
}
