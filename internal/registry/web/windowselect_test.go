// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// The three window pickers must keep emitting exactly these bytes. The markup
// is one shared control on three pages, so the only thing that may differ
// between them is the URL each option navigates to.
func TestWindowSelectMarkupIsUnchanged(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	var earliest time.Time
	head := `<div class="window-nav"><select class="window-select" aria-label="Time period">`
	tail := `</select></div>`

	t.Run("page", func(t *testing.T) {
		want := head +
			`<option value="/outcomes/helped-rate?w=7d">Last 7 days</option>` +
			`<option value="/outcomes/helped-rate?w=30d" selected>Last 30 days</option>` +
			`<option value="/outcomes/helped-rate?w=90d">Last 90 days</option>` +
			`<option value="/outcomes/helped-rate?w=all">All time</option>` + tail
		if got := windowSelect("/outcomes/helped-rate", "30d", now, earliest); got != want {
			t.Errorf("windowSelect\n got %s\nwant %s", got, want)
		}
	})

	t.Run("organization", func(t *testing.T) {
		q := url.Values{"dimension": {"tag"}, "scope": {"a&b"}, "w": {"7d"}}
		want := head +
			`<option value="/outcomes?dimension=tag&amp;scope=a%26b&amp;w=7d" selected>Last 7 days</option>` +
			`<option value="/outcomes?dimension=tag&amp;scope=a%26b&amp;w=30d">Last 30 days</option>` +
			`<option value="/outcomes?dimension=tag&amp;scope=a%26b&amp;w=90d">Last 90 days</option>` +
			`<option value="/outcomes?dimension=tag&amp;scope=a%26b&amp;w=all">All time</option>` + tail
		if got := organizationWindowSelect("7d", now, earliest, q); got != want {
			t.Errorf("organizationWindowSelect\n got %s\nwant %s", got, want)
		}
	})

	t.Run("events", func(t *testing.T) {
		q := url.Values{"telemetry": {"1"}, "w": {"90d"}, "team": {"x&y"}}
		want := head +
			`<option value="/outcomes/events?team=x%26y&amp;telemetry=1&amp;w=7d">Last 7 days</option>` +
			`<option value="/outcomes/events?team=x%26y&amp;telemetry=1&amp;w=30d">Last 30 days</option>` +
			`<option value="/outcomes/events?team=x%26y&amp;telemetry=1&amp;w=90d" selected>Last 90 days</option>` +
			`<option value="/outcomes/events?team=x%26y&amp;telemetry=1&amp;w=all">All time</option>` + tail
		if got := eventsWindowSelect("90d", now, earliest, q); got != want {
			t.Errorf("eventsWindowSelect\n got %s\nwant %s", got, want)
		}
	})
}

// Only the keys the page owns survive a window change. A stray query parameter
// must not ride along into the new URL.
func TestWindowSelectDropsForeignQueryKeys(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	var earliest time.Time
	org := organizationWindowSelect("30d", now, earliest, url.Values{"page": {"4"}, "tag": {"go"}})
	if want := `value="/outcomes?tag=go&amp;w=7d"`; !strings.Contains(org, want) {
		t.Errorf("organizationWindowSelect kept a foreign key: %s", org)
	}
	ev := eventsWindowSelect("30d", now, earliest, url.Values{"page": {"4"}, "role": {"ic"}})
	if want := `value="/outcomes/events?role=ic&amp;w=7d"`; !strings.Contains(ev, want) {
		t.Errorf("eventsWindowSelect kept a foreign key: %s", ev)
	}
}
