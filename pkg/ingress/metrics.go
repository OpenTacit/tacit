// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"sync"
	"time"
)

// latencyBuckets are the upper bounds, in milliseconds, of the histogram every
// instance keeps. Percentiles from a fixed histogram are approximate — a p95
// reported as 250 means "between 100 and 250" — which is the right trade for a
// readout whose job is to tell a healthy instance from a struggling one. An
// exact percentile would need every sample kept.
var latencyBuckets = []int{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

// InstanceMetrics is what the console reports for one instance. The counters
// cover whatever the operations log still holds — see Metrics.Covers — not all
// time; Hours is a rolling day, which is what makes a sparkline possible
// without keeping every request.
type InstanceMetrics struct {
	Requests    int64            `json:"requests"`
	ByClass     map[string]int64 `json:"by_class"` // "2xx", "4xx", "5xx", "502" for tunnel failures
	BytesOut    int64            `json:"bytes_out"`
	LatencySum  int64            `json:"latency_sum_ms"`
	LatencyMax  int64            `json:"latency_max_ms"`
	Histogram   []int64          `json:"histogram"`
	Connects    int64            `json:"connects"`
	Disconnects int64            `json:"disconnects"`
	LastRequest time.Time        `json:"last_request,omitzero"`

	// Hours is a 24-slot ring indexed by hour-of-day, each slot stamped with
	// the day it belongs to so a stale slot reads as zero rather than as
	// yesterday's traffic.
	Hours [24]HourBucket `json:"hours"`
}

// HourBucket is one hour of the rolling day.
type HourBucket struct {
	Day      int   `json:"day"` // days since the epoch, to invalidate stale slots
	Requests int64 `json:"requests"`
	Errors   int64 `json:"errors"`
}

// Metrics holds every instance's counters.
//
// It is a VIEW, not a record. Nothing here is persisted: the operations log is
// the durable account of what happened, and everything in this struct is a fold
// over it, rebuilt at startup from the log's tail. That is deliberate. A
// snapshot file beside the log was a second source of truth that could — and
// did — disagree with it: it lost up to a minute on a hard kill, it was dropped
// wholesale when a name was released, and nothing ever reconciled the two. A
// console that reports numbers the log contradicts is worse than a console that
// reports fewer numbers.
type Metrics struct {
	mu  sync.RWMutex
	per map[string]*InstanceMetrics
	// since is the oldest record folded in, so the console can say what its
	// figures cover instead of implying they are lifetime totals.
	since time.Time
}

// NewMetrics returns an empty view.
func NewMetrics() *Metrics {
	return &Metrics{per: map[string]*InstanceMetrics{}}
}

// RebuildFrom replays the log's tail into the counters. Requests and tunnel
// events both land here, which is why tunnel churn no longer needs a file of
// its own to survive a restart.
func (m *Metrics) RebuildFrom(l *OpLog, window time.Duration) {
	var since time.Time
	if window > 0 {
		since = time.Now().Add(-window)
	}
	var oldest time.Time
	l.Scan(since, func(op Op) {
		if oldest.IsZero() || op.TS.Before(oldest) {
			oldest = op.TS
		}
		switch op.Kind {
		case KindTunnelUp:
			m.RecordConnect(op.Instance)
		case KindTunnelDown:
			m.RecordDisconnect(op.Instance)
		default:
			m.recordAt(op.Instance, op.Status, op.Bytes, time.Duration(op.MS)*time.Millisecond, op.TS)
		}
	})
	m.mu.Lock()
	m.since = oldest
	m.mu.Unlock()
}

// Covers is the oldest record behind these figures, zero when there is none.
func (m *Metrics) Covers() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.since
}

func (m *Metrics) forLocked(name string) *InstanceMetrics {
	im, ok := m.per[name]
	if !ok {
		im = &InstanceMetrics{
			ByClass:   map[string]int64{},
			Histogram: make([]int64, len(latencyBuckets)+1),
		}
		m.per[name] = im
	}
	return im
}

// Record adds one served request.
func (m *Metrics) Record(name string, status int, bytes int64, took time.Duration) {
	m.recordAt(name, status, bytes, took, time.Now().UTC())
}

// recordAt is Record with an explicit timestamp, so replaying the log puts each
// request in the hour it actually happened rather than the hour of the replay.
func (m *Metrics) recordAt(name string, status int, bytes int64, took time.Duration, now time.Time) {
	now = now.UTC()
	ms := took.Milliseconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	im := m.forLocked(name)
	if m.since.IsZero() || now.Before(m.since) {
		m.since = now
	}
	im.Requests++
	im.BytesOut += bytes
	im.LatencySum += ms
	if ms > im.LatencyMax {
		im.LatencyMax = ms
	}
	im.Histogram[bucketFor(ms)]++
	im.ByClass[classOf(status)]++
	im.LastRequest = now

	day := int(now.Unix() / 86400)
	slot := &im.Hours[now.Hour()]
	if slot.Day != day {
		*slot = HourBucket{Day: day}
	}
	slot.Requests++
	if status >= 500 {
		slot.Errors++
	}
}

// RecordConnect and RecordDisconnect track tunnel churn, which is the first
// thing to look at when an instance is intermittently unreachable.
func (m *Metrics) RecordConnect(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forLocked(name).Connects++
}

func (m *Metrics) RecordDisconnect(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forLocked(name).Disconnects++
}

// For returns a copy of one instance's metrics.
func (m *Metrics) For(name string) InstanceMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	im, ok := m.per[name]
	if !ok {
		return InstanceMetrics{ByClass: map[string]int64{}, Histogram: make([]int64, len(latencyBuckets)+1)}
	}
	out := *im
	out.ByClass = make(map[string]int64, len(im.ByClass))
	for k, v := range im.ByClass {
		out.ByClass[k] = v
	}
	out.Histogram = append([]int64(nil), im.Histogram...)
	return out
}

// Drop forgets an instance, for when its name is deleted.
func (m *Metrics) Drop(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.per, name)
}

// P95 is the 95th-percentile latency, to the resolution of the histogram.
func (im InstanceMetrics) P95() int { return im.percentile(0.95) }

// P50 is the median, same resolution.
func (im InstanceMetrics) P50() int { return im.percentile(0.50) }

func (im InstanceMetrics) percentile(q float64) int {
	var total int64
	for _, c := range im.Histogram {
		total += c
	}
	if total == 0 {
		return 0
	}
	want := int64(float64(total) * q)
	var seen int64
	for i, c := range im.Histogram {
		seen += c
		if seen >= want {
			if i >= len(latencyBuckets) {
				return latencyBuckets[len(latencyBuckets)-1] * 2 // the overflow bucket
			}
			return latencyBuckets[i]
		}
	}
	return latencyBuckets[len(latencyBuckets)-1]
}

// ErrorRate is the share of requests that failed, 0..1.
func (im InstanceMetrics) ErrorRate() float64 {
	if im.Requests == 0 {
		return 0
	}
	return float64(im.ByClass["5xx"]) / float64(im.Requests)
}

// Day returns the rolling 24 hours oldest-first, ending with the current hour.
func (im InstanceMetrics) Day() []HourBucket {
	now := time.Now().UTC()
	today := int(now.Unix() / 86400)
	out := make([]HourBucket, 0, 24)
	for i := 23; i >= 0; i-- {
		t := now.Add(-time.Duration(i) * time.Hour)
		slot := im.Hours[t.Hour()]
		day := int(t.Unix() / 86400)
		if slot.Day != day || (slot.Day == today && t.Hour() > now.Hour()) {
			slot = HourBucket{}
		}
		out = append(out, slot)
	}
	return out
}

func bucketFor(ms int64) int {
	for i, b := range latencyBuckets {
		if ms <= int64(b) {
			return i
		}
	}
	return len(latencyBuckets)
}

func classOf(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}
