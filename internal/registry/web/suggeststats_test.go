// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSuggestStats(t *testing.T) {
	ss := &suggestStats{path: filepath.Join(t.TempDir(), "suggest_runs.json")}

	// No history -> 0, so the page shows an indeterminate bar.
	if got := ss.estimateSecs(); got != 0 {
		t.Fatalf("empty estimate = %d, want 0", got)
	}

	// Median, not mean: a single slow outlier must not skew the estimate.
	for _, d := range []time.Duration{60 * time.Second, 62 * time.Second, 58 * time.Second, 600 * time.Second} {
		ss.record(d)
	}
	if got := ss.estimateSecs(); got < 58 || got > 62 {
		t.Fatalf("estimate = %d, want ~60 (median resists the 600s outlier)", got)
	}

	// Only the last suggestKeep are retained, and it survives a reopen.
	for i := 0; i < suggestKeep+3; i++ {
		ss.record(90 * time.Second)
	}
	reopened := &suggestStats{path: ss.path}
	if got := reopened.estimateSecs(); got != 90 {
		t.Fatalf("after ring fill, estimate = %d, want 90", got)
	}
	if got := len(reopened.load()); got != suggestKeep {
		t.Fatalf("retained %d runs, want %d", got, suggestKeep)
	}

	// Sub-second runs round up to 1, never 0.
	empty := &suggestStats{path: filepath.Join(t.TempDir(), "s.json")}
	empty.record(200 * time.Millisecond)
	if got := empty.estimateSecs(); got != 1 {
		t.Fatalf("sub-second run estimate = %d, want 1", got)
	}
}
