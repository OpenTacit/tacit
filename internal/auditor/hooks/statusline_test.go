// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/capture"
)

// A real payload, as Claude Code 2.1.265 sends it. Trimmed of nothing that
// matters and of nothing that should be kept: the paths, the session name and
// the prompt id are in here on purpose, because a reducer is only proved by
// what it throws away.
const statusPayload = `{
 "session_id":"S1",
 "transcript_path":"/home/someone/.claude/projects/SECRETPATH/x.jsonl",
 "cwd":"/home/someone/Repos/tacit",
 "scratchpad_dir":"/tmp/SECRETSCRATCH",
 "prompt_id":"SECRETPROMPTID",
 "session_name":"SECRETNAME",
 "effort":{"level":"high"},
 "model":{"id":"claude-opus-5","display_name":"Opus 5"},
 "workspace":{"current_dir":"/home/someone/Repos/tacit","project_dir":"/home/someone/Repos/tacit",
  "repo":{"host":"github.com","owner":"SECRETOWNER","name":"tacit"}},
 "version":"2.1.265",
 "cost":{"total_cost_usd":0.56,"total_duration_ms":71321,"total_api_duration_ms":32433,
  "total_lines_added":40,"total_lines_removed":12},
 "context_window":{"total_input_tokens":55682,"context_window_size":1000000,"used_percentage":6},
 "prompt_cache":{"warm":true,"requests":10,"misses":1,"hit_ratio":0.95},
 "fast_mode":false,
 "thinking":{"enabled":true},
 "rate_limits":{"five_hour":{"used_percentage":17,"resets_at":%d},
  "seven_day":{"used_percentage":27,"resets_at":%d}}
}`

func statusBody(t *testing.T, five, seven time.Time) map[string]any {
	t.Helper()
	var body map[string]any
	raw := fmt.Sprintf(statusPayload, five.Unix(), seven.Unix())
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("the fixture is not valid JSON: %v", err)
	}
	return body
}

// The status line is handed the member's whole session several times a second.
// What it may contribute is a number or a name from a closed list; everything
// that makes it THIS member's session has to be gone by the time anything
// reaches disk. This is the leak test invariant 1 asks for, on the newest
// source to reach the log.
func TestStatusLineKeepsNumbersAndDropsEverythingElse(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	body := statusBody(t, now.Add(time.Hour), now.Add(72*time.Hour))

	var stats sessionStats
	stats.observe(now, capture.EvUserPrompt, map[string]any{})
	in := capture.ReadStatusLine(body)
	stats.noteStatus(in, in.Measured())

	s := loadSessionLog(detail, daily, fixedClock(now))
	s.upsert(stats.record("hashed-key", "claude-code", "claude-opus-5",
		"/home/someone/Repos/tacit", 0))

	raw, err := os.ReadFile(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SECRETPATH", "SECRETSCRATCH", "SECRETPROMPTID",
		"SECRETNAME", "SECRETOWNER", "/home/someone", "github.com", "2.1.265"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("the status-line reducer leaked %q: %s", forbidden, raw)
		}
	}
	for _, want := range []string{`"lines_added":40`, `"lines_removed":12`,
		`"active_seconds":71`, `"context_pct":6`, `"cache_requests":10`,
		`"cache_misses":1`, `"effort":"high"`, `"status":true`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the record is missing %s: %s", want, raw)
		}
	}
}

// Every figure the status line brings is a LEVEL: the payload restates a
// cumulative session on every render, so two renders of a session that had
// cost thirty cents did not between them cost sixty. This is the failure
// keepLarger exists to prevent, on the one source that arrives many times a
// second.
func TestStatusLineLevelsAreMaxedNeverSummed(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	var stats sessionStats
	stats.observe(now, capture.EvUserPrompt, map[string]any{})
	render := func(cost float64, lines, active, reqs int) {
		stats.noteStatus(capture.StatusLine{CostUSD: cost, LinesAdded: lines,
			ActiveSeconds: active, CacheRequests: reqs}, true)
	}
	render(0.30, 10, 40, 4)
	render(0.42, 18, 95, 9)
	render(0.42, 18, 95, 9) // the same render again, as a status line does
	r := stats.record("k", "claude-code", "claude-opus-5", "/x/tacit", 0)
	if r.CostUSD != 0.42 {
		t.Fatalf("cost = %v, want 0.42 — levels were added", r.CostUSD)
	}
	if r.LinesAdded != 18 || r.ActiveSeconds != 95 || r.CacheRequests != 9 {
		t.Fatalf("levels were added rather than raised: %+v", r)
	}
}

// A field this harness version does not send must leave its figure absent, not
// zero. The payload is version-gated, and a view built on a zero that means
// "nobody said" is a view that lies on every older harness.
func TestStatusLineAbsentFieldsStayAbsent(t *testing.T) {
	in := capture.ReadStatusLine(map[string]any{"session_id": "S1",
		"cost": map[string]any{"total_cost_usd": 0.2}})
	if in.Measured() != true {
		t.Fatal("a payload with a cost on it measured something")
	}
	if in.LinesAdded != 0 || in.CacheRequests != 0 || in.HasFiveHour {
		t.Fatalf("fields nobody sent arrived anyway: %+v", in)
	}
	var stats sessionStats
	stats.noteStatus(in, in.Measured())
	r := stats.record("k", "claude-code", "m", "/x/tacit", 0)
	// omitempty carries the honesty: an absent figure is absent on the wire,
	// and the view renders nothing rather than a confident 0.
	line, _ := json.Marshal(r)
	for _, unwanted := range []string{"lines_added", "cache_requests", "context_pct"} {
		if strings.Contains(string(line), unwanted) {
			t.Fatalf("%s was written for a harness that never reported it: %s", unwanted, line)
		}
	}
	// A status line that said nothing countable must not mark the session as
	// measured, or the coverage line on the page counts it as a source.
	var quiet sessionStats
	bare := capture.ReadStatusLine(map[string]any{"session_id": "S2"})
	quiet.noteStatus(bare, bare.Measured())
	if quiet.record("k", "claude-code", "m", "/x/tacit", 0).Status {
		t.Fatal("a status line carrying only a session id counted as a source")
	}
}

// Coverage: the page says which sources fed a window, and it needs a
// denominator to say it with. A machine with the status line on some sessions
// and not others must report both numbers.
func TestWorkSummaryCountsTheSessionsAStatusLineFed(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	s := loadSessionLog(detail, daily, fixedClock(now))
	plain := rec("sess1", now, "anthropic/claude-opus-5", "tacit", 3)
	s.upsert(plain)
	fed := rec("sess2", now, "anthropic/claude-opus-5", "tacit", 2)
	fed.Status, fed.LinesAdded, fed.LinesRemoved = true, 40, 12
	fed.CacheRequests, fed.CacheMisses, fed.ContextPct = 10, 1, 62
	s.upsert(fed)

	sum := s.summarizeWork(0)
	if sum.Totals.Sessions != 2 || sum.Totals.StatusSessions != 1 {
		t.Fatalf("coverage = %d of %d, want 1 of 2", sum.Totals.StatusSessions, sum.Totals.Sessions)
	}
	if sum.Totals.LinesAdded != 40 || sum.Totals.LinesRemoved != 12 {
		t.Fatalf("lines did not reach the summary: %+v", sum.Totals)
	}
	if sum.PeakContextPct != 62 {
		t.Fatalf("peak context share = %d, want 62", sum.PeakContextPct)
	}
}

// The whole path, through the real handler: a session with turns on it, a
// status line posting the payload the harness gave it, and the figures showing
// up on the summary the Usage page reads.
func TestStatusLineEndpointFeedsTheSessionAndTheQuota(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	agent, _ := newTestAgent(t, nil, Options{
		StateDir: dir, SessionSalt: "salt", Now: fixedClock(now)})
	ts := httptest.NewServer(Handler(agent, "secret"))
	defer ts.Close()

	// A turn first: the status line renders before the first prompt and long
	// after the last, and a session the hooks have never seen has nothing to
	// attach a cost to.
	agent.Handle(map[string]any{"hook_event_name": "UserPromptSubmit",
		"session_id": "S1", "cwd": "/home/someone/Repos/tacit"}, "claude-code")

	body := statusBody(t, now.Add(3*time.Hour), now.Add(80*time.Hour))
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/hooks/statusline",
		bytes.NewReader(raw))
	req.Header.Set("X-Tacit-Key", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("statusline post: %v %v", err, resp)
	}
	resp.Body.Close()

	sum := agent.WorkSummary(0)
	if sum.Totals.StatusSessions != 1 {
		t.Fatalf("the session was not marked as one the status line fed: %+v", sum.Totals)
	}
	if sum.Totals.LinesAdded != 40 || sum.Totals.LinesRemoved != 12 {
		t.Fatalf("lines = +%d -%d, want +40 -12", sum.Totals.LinesAdded, sum.Totals.LinesRemoved)
	}
	if sum.Totals.CostUSD != 0.56 {
		t.Fatalf("cost = %v, want 0.56 — the status line is the unconditional source", sum.Totals.CostUSD)
	}
	if sum.Totals.CacheRequests != 10 || sum.Totals.CacheMisses != 1 {
		t.Fatalf("cache = %d/%d, want 10/1", sum.Totals.CacheMisses, sum.Totals.CacheRequests)
	}
	if !sum.CostReported {
		t.Fatal("cost arrived and was not believed")
	}
	if len(sum.Quotas) != 1 || sum.Quotas[0].FiveHour == nil || sum.Quotas[0].FiveHour.UsedPct != 17 {
		t.Fatalf("the allowance did not reach the summary: %+v", sum.Quotas)
	}
	// Filed under the vendor of the model the line was rendering, so a second
	// provider's reading lands beside it rather than on top of it.
	if sum.Quotas[0].Source != "anthropic" {
		t.Errorf("the allowance is filed under %q, want anthropic", sum.Quotas[0].Source)
	}
	// The allowance lives apart from the session record, because history must
	// not be rewritten by a number that expires.
	if _, err := os.Stat(filepath.Join(dir, "quota.json")); err != nil {
		t.Fatalf("the quota was not written to its own file: %v", err)
	}

	// Posting it again does not double anything: it is one cumulative session
	// restated, which is what a status line is.
	req2, _ := http.NewRequest("POST", ts.URL+"/v1/hooks/statusline", bytes.NewReader(raw))
	req2.Header.Set("X-Tacit-Key", "secret")
	r2, _ := http.DefaultClient.Do(req2)
	r2.Body.Close()
	if again := agent.WorkSummary(0); again.Totals.CostUSD != 0.56 || again.Totals.LinesAdded != 40 {
		t.Fatalf("a second render of the same session changed the figures: %+v", again.Totals)
	}
}

// A status line for a session no hook ever reported must not conjure one. The
// quota still lands, because it belongs to the account rather than to any
// session.
func TestStatusLineDoesNotInventASession(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	agent, _ := newTestAgent(t, nil, Options{
		StateDir: dir, SessionSalt: "salt", Now: fixedClock(now)})
	agent.HandleStatusLine(statusBody(t, now.Add(time.Hour), now.Add(60*time.Hour)))
	sum := agent.WorkSummary(0)
	if sum.Totals.Sessions != 0 {
		t.Fatalf("a status line created a session with no turns in it: %+v", sum.Totals)
	}
	if len(sum.Quotas) == 0 {
		t.Fatal("the account's allowance was dropped with the session")
	}
}
