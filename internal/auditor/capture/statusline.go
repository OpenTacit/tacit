// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"math"
	"strings"
	"time"
)

// StatusLine is the countable part of the harness status-line payload.
//
// The status line is rendered several times a second and is handed the whole
// session as the harness sees it: what it cost so far, how long it has been
// working, how many lines it has written, how full the context is, how the
// prompt cache is doing, how much of the account's allowance is gone, and how
// the session was configured. Until now `tacit statusline` decoded one field
// out of that — the session id — and dropped the rest on the floor.
//
// This is the one place that decides what a status-line payload may
// contribute, for the same reason ToolDetail is the one place that decides
// what a tool input may: there should be one rule about it rather than three
// that drift. What may be kept is a NUMBER or a name from a closed list. What
// is dropped is everything that makes it this member's session — the working
// directory, the transcript path, the scratchpad, the session name, the prompt
// id, the repository owner and host.
//
// Every figure here is cumulative for the session: the payload restates the
// whole session on every render, so these are LEVELS and are kept as maxima.
// Adding two of them would produce a session that cost twice what it cost.
type StatusLine struct {
	SessionID string

	CostUSD       float64
	ActiveSeconds int // time the harness spent working, NOT wall clock
	LinesAdded    int
	LinesRemoved  int

	ContextPct int // share of the window in use at this render

	CacheRequests int // model requests this session
	CacheMisses   int // those that missed the prompt cache

	// Effort is the reasoning level, from a closed list. Model, Project and
	// Version are labels the session log already keeps in the same vocabulary.
	Effort  string
	Model   string
	Project string // the repository's own name, never its path or its owner

	// FiveHourPct, SevenDayPct and SpendPct are the shares of the account's
	// allowances already spent, each with the moment it refills. Levels with an
	// expiry, which is why they are stored apart from the session
	// (hooks/quota.go).
	//
	// The spend limit is the third window the harness reports and the only one
	// that is money rather than usage: it appears behind a Claude apps gateway,
	// and it can pass 100 — which is a real reading and is not clamped.
	FiveHourPct, SevenDayPct, SpendPct int
	FiveHourAt, SevenDayAt, SpendAt    time.Time
	HasFiveHour, HasSevenDay, HasSpend bool
}

// efforts is the closed vocabulary of reasoning levels. An unknown value is
// dropped rather than recorded, on the same standard every other vocabulary
// here holds: a field that can carry anything is a field that will eventually
// carry something that was never meant to be kept.
var efforts = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true, "none": true}

// ReadStatusLine takes the countable shape off a status-line payload. A field
// the harness did not send stays absent rather than arriving as a zero: the
// payload is version-gated, and a figure nothing measured is not a measurement
// of nothing.
func ReadStatusLine(p map[string]any) StatusLine {
	var s StatusLine
	if p == nil {
		return s
	}
	s.SessionID, _ = p["session_id"].(string)
	if cost, ok := p["cost"].(map[string]any); ok {
		s.CostUSD = statusFloat(cost["total_cost_usd"])
		s.ActiveSeconds = statusInt(cost["total_duration_ms"]) / 1000
		s.LinesAdded = statusInt(cost["total_lines_added"])
		s.LinesRemoved = statusInt(cost["total_lines_removed"])
	}
	if ctx, ok := p["context_window"].(map[string]any); ok {
		s.ContextPct = statusInt(ctx["used_percentage"])
	}
	if pc, ok := p["prompt_cache"].(map[string]any); ok {
		s.CacheRequests = statusInt(pc["requests"])
		s.CacheMisses = statusInt(pc["misses"])
	}
	if e, ok := p["effort"].(map[string]any); ok {
		if level, _ := e["level"].(string); efforts[strings.ToLower(level)] {
			s.Effort = strings.ToLower(level)
		}
	}
	if m, ok := p["model"].(map[string]any); ok {
		s.Model, _ = m["id"].(string)
	}
	// The repository's NAME and nothing else off the workspace: the owner and
	// the host say who the member works for, and the two directory fields are
	// paths on their disk.
	if ws, ok := p["workspace"].(map[string]any); ok {
		if repo, ok := ws["repo"].(map[string]any); ok {
			s.Project, _ = repo["name"].(string)
		}
	}
	if rl, ok := p["rate_limits"].(map[string]any); ok {
		s.FiveHourPct, s.FiveHourAt, s.HasFiveHour = statusWindow(rl["five_hour"])
		s.SevenDayPct, s.SevenDayAt, s.HasSevenDay = statusWindow(rl["seven_day"])
		s.SpendPct, s.SpendAt, s.HasSpend = statusWindow(rl["spend_limit"])
	}
	return s
}

// Measured says whether this payload carried anything worth folding in. A
// harness that sends only a session id must not mark the session as one whose
// figures were measured.
func (s StatusLine) Measured() bool {
	return s.CostUSD > 0 || s.ActiveSeconds > 0 || s.LinesAdded > 0 ||
		s.LinesRemoved > 0 || s.ContextPct > 0 || s.CacheRequests > 0
}

// statusWindow reads one allowance window. Reset times arrive as Unix seconds.
func statusWindow(v any) (pct int, at time.Time, ok bool) {
	w, isMap := v.(map[string]any)
	if !isMap {
		return 0, time.Time{}, false
	}
	sec := statusInt(w["resets_at"])
	if sec <= 0 {
		return 0, time.Time{}, false
	}
	return statusPct(w["used_percentage"]), time.Unix(int64(sec), 0).UTC(), true
}

// statusPct rounds where statusInt truncates. A share arrives as a number that
// may carry a fraction — the published schema's own example is 23.5 — and
// truncating it reports 23, which is the one direction a figure about an
// allowance should not err in.
func statusPct(v any) int {
	if f, ok := v.(float64); ok {
		return int(math.Round(f))
	}
	return statusInt(v)
}

func statusInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func statusFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
