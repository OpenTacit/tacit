// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

// suggestStats remembers how long recent "Suggest techniques" research runs
// took, so the Drafts page can fill a progress bar toward the expected
// duration instead of showing a bare elapsed counter. This is operational
// telemetry, not registry data — a per-instance sidecar JSON file next to the
// file store, deliberately NOT in the Store interface (no backend churn for a
// UX nicety; per-instance history is fine for a rare operator action).

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
)

const suggestKeep = 5 // recent runs kept; the estimate is their median

type suggestStats struct {
	mu   sync.Mutex
	path string
}

func (ss *suggestStats) load() []int {
	raw, err := os.ReadFile(ss.path)
	if err != nil {
		return nil
	}
	var secs []int
	if json.Unmarshal(raw, &secs) != nil {
		return nil
	}
	return secs
}

// record appends a completed run's duration (whole seconds), keeping the last
// suggestKeep. Best-effort: a telemetry write must never break a run.
func (ss *suggestStats) record(d time.Duration) {
	if ss == nil {
		return
	}
	sec := int(d.Round(time.Second).Seconds())
	if sec < 1 {
		sec = 1
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	secs := append(ss.load(), sec)
	if len(secs) > suggestKeep {
		secs = secs[len(secs)-suggestKeep:]
	}
	raw, err := json.Marshal(secs)
	if err != nil {
		log.Printf("[suggest] run history not encoded: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(ss.path), 0o755); err != nil {
		log.Printf("[suggest] run history directory not created: %v", err)
		return
	}
	// Best-effort still says so: a silent failure here shows up much later as a
	// progress bar that never learns how long a run takes.
	if err := fsx.WriteFileAtomic(ss.path, raw, 0o644); err != nil {
		log.Printf("[suggest] run history not saved to %s: %v", ss.path, err)
	}
}

// estimateSecs returns the median of recent run durations, or 0 when there is
// no history yet (the page then shows an indeterminate bar). Median rather
// than mean so one anomalous run — a rate-limited or timed-out pass — doesn't
// skew the estimate.
func (ss *suggestStats) estimateSecs() int {
	if ss == nil {
		return 0
	}
	ss.mu.Lock()
	secs := ss.load()
	ss.mu.Unlock()
	if len(secs) == 0 {
		return 0
	}
	sort.Ints(secs)
	return secs[len(secs)/2]
}
