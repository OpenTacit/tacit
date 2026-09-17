// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/sessionhash"
)

// ampUsage is one request's account of itself, in the shape `amp threads
// export` writes it: the input split three ways, and their sum beside it.
type ampUsage struct {
	Model      string `json:"model"`
	Input      int    `json:"inputTokens"`
	TotalInput int    `json:"totalInputTokens"`
	Output     int    `json:"outputTokens"`
	CacheRead  int    `json:"cacheReadInputTokens"`
	CacheWrite int    `json:"cacheCreationInputTokens"`
}

type ampMessage struct {
	Role  string   `json:"role"`
	Usage ampUsage `json:"usage"`
}

func ampExportOf(messages ...ampUsage) []byte {
	export := struct {
		ID       string       `json:"id"`
		Messages []ampMessage `json:"messages"`
	}{ID: "T-01a097db"}
	for _, u := range messages {
		export.Messages = append(export.Messages, ampMessage{Role: "assistant", Usage: u})
	}
	body, _ := json.Marshal(export)
	return body
}

// ampThread renders an export naming only the model that served each request,
// for the tests that are about which model won rather than what it spent.
func ampThread(models ...string) []byte {
	msgs := make([]ampUsage, 0, len(models))
	for _, m := range models {
		msgs = append(msgs, ampUsage{Model: m})
	}
	return ampExportOf(msgs...)
}

// stubAmpExport answers the export for the length of one test.
func stubAmpExport(t *testing.T, answer func(thread string) ([]byte, error)) {
	t.Helper()
	prior := ampExport
	ampExport = func(_ context.Context, thread string) ([]byte, error) {
		return answer(thread)
	}
	t.Cleanup(func() { ampExport = prior })
}

// Amp routes per request, so one thread holds several models: a main loop, the
// subagents it spawned, a classifier. The session is one row with one label,
// and the honest label is what did most of the work — not whichever model the
// last request happened to reach, and not a map-order coin toss between two.
func TestAmpSessionTakesTheModelThatServedMostOfTheThread(t *testing.T) {
	stubAmpExport(t, func(string) ([]byte, error) {
		return ampThread("gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-sol",
			"gpt-5.6-luna", "gpt-5.6-sol"), nil
	})
	if got, _ := ampThreadFacts(context.Background(), "T-01a097db"); got.Model != "gpt-5.6-sol" {
		t.Errorf("thread model = %q, want the model that served three of five requests", got.Model)
	}
}

// One model, two spellings: Amp reports the same OpenAI model as its family on
// some requests and as a dated build on others. Counting raw labels would split
// that model's five requests into three and two and hand the session to a model
// that served four — so the count is by cohort, and the label is the busiest
// spelling of the cohort that won.
func TestAmpModelCountsCohortsRatherThanSpellings(t *testing.T) {
	stubAmpExport(t, func(string) ([]byte, error) {
		return ampThread(
			"gpt-5.5-2026-04-23", "gpt-5.5-2026-04-23", "gpt-5.5-2026-04-23",
			"gpt-5.5", "gpt-5.5",
			"gpt-6-astra", "gpt-6-astra", "gpt-6-astra", "gpt-6-astra"), nil
	})
	got, _ := ampThreadFacts(context.Background(), "T-01a097db")
	if got.Model != "gpt-5.5-2026-04-23" {
		t.Errorf("thread model = %q, want the busiest spelling of the cohort that served five of nine", got.Model)
	}
}

// A tie has to answer the same way every time it is asked. Ranging a map for
// the winner would report one model on one turn and the other on the next, and
// the member would watch a session change its mind.
func TestAmpModelTieBreaksTheSameWayEveryTime(t *testing.T) {
	stubAmpExport(t, func(string) ([]byte, error) {
		return ampThread("gpt-5.6-terra", "gpt-5.6-sol"), nil
	})
	for i := 0; i < 8; i++ {
		if got, _ := ampThreadFacts(context.Background(), "T-01a097db"); got.Model != "gpt-5.6-sol" {
			t.Fatalf("thread model = %q on ask %d, want the same answer each time", got.Model, i)
		}
	}
}

// No Amp on the PATH, a thread that has not synced, an export that predates the
// usage block. Every one of them is a reason to say nothing: an invented model
// is worse than a blank, because a cohort nobody ran still gets a rate.
func TestAmpModelStaysEmptyWhenTheExportSaysNothing(t *testing.T) {
	for name, answer := range map[string]func(string) ([]byte, error){
		"no amp installed": func(string) ([]byte, error) {
			return nil, errors.New(`exec: "amp": executable file not found in $PATH`)
		},
		"nothing returned":  func(string) ([]byte, error) { return nil, nil },
		"not json":          func(string) ([]byte, error) { return []byte("Error: unknown thread"), nil },
		"no usage recorded": func(string) ([]byte, error) { return ampThread(), nil },
	} {
		t.Run(name, func(t *testing.T) {
			stubAmpExport(t, answer)
			if got, ok := ampThreadFacts(context.Background(), "T-01a097db"); ok || got.Model != "" {
				t.Errorf("thread facts = %+v, want nothing said", got)
			}
		})
	}
}

// The session id is what the export is asked about, so anything that is not a
// thread must not reach a process at all.
func TestOnlyAnAmpThreadIDIsWorthAProcess(t *testing.T) {
	asked := false
	stubAmpExport(t, func(string) ([]byte, error) {
		asked = true
		return ampThread("gpt-5.6-sol"), nil
	})
	if got, ok := ampThreadFacts(context.Background(), "sess_1"); ok || asked {
		t.Errorf("a Claude Code session id produced %+v (export run: %v); want neither", got, asked)
	}
}

// End to end: an Amp session whose payloads never name a model is recorded
// under the model its own thread names. This is the whole point — the Models
// panel groups by model key and drops the blank, so before this an Amp session
// was work that happened under no model at all.
func TestAmpSessionIsRecordedUnderTheModelItsThreadNames(t *testing.T) {
	dir := t.TempDir()
	stubAmpExport(t, func(thread string) ([]byte, error) {
		if thread != "T-01a097db" {
			t.Errorf("export asked about %q, want the session's own thread", thread)
		}
		return ampThread("gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-terra"), nil
	})
	quiet := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, nil
	}
	agent := NewAgent(quiet, stubLLM{}, nil, nil, Options{StateDir: dir,
		SessionSalt: "org-salt", RunAsync: inline,
		UsageLogPath: filepath.Join(dir, "usage.jsonl"),
		Segment:      contracts.Segment{"team": "revops"}})

	amp := func(name string, fields map[string]any) map[string]any {
		payload := map[string]any{"hook_event_name": name, "session_id": "T-01a097db"}
		for k, v := range fields {
			payload[k] = v
		}
		return payload
	}
	agent.Handle(amp("SessionStart", map[string]any{"source": "startup"}), "amp")
	agent.Handle(amp("UserPromptSubmit", map[string]any{"prompt": "audit the docs",
		"cwd": "/home/x/tacit"}), "amp")
	agent.Handle(amp("Stop", nil), "amp")

	detail, daily := sessionLogPaths(dir)
	sum := loadSessionLog(detail, daily, nil).summarizeWork(0)
	if len(sum.Models) != 1 {
		t.Fatalf("models = %v, want the one its thread named", sum.Models)
	}
	if got := sum.Models[0].Key; got != "openai/gpt-5-6-sol" {
		t.Errorf("model key = %q, want the canonical cohort for gpt-5.6-sol", got)
	}
	if got := sum.Models[0].Sessions; got != 1 {
		t.Errorf("sessions under the model = %d, want the one that ran", got)
	}
}

// The thread answers nothing until it has synced, which can be after the turn
// that asked. So a session asks again on a later turn — and stops asking, so a
// thread that never answers cannot spawn a process per turn all session long.
func TestAmpSessionAsksAgainButNotForEver(t *testing.T) {
	dir := t.TempDir()
	asks := 0
	stubAmpExport(t, func(string) ([]byte, error) {
		asks++
		return nil, nil // not synced yet, and never will be
	})
	quiet := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, nil
	}
	agent := NewAgent(quiet, stubLLM{}, nil, nil, Options{StateDir: dir,
		SessionSalt: "org-salt", RunAsync: inline,
		UsageLogPath: filepath.Join(dir, "usage.jsonl"),
		Segment:      contracts.Segment{"team": "revops"}})

	amp := func(name string, fields map[string]any) map[string]any {
		payload := map[string]any{"hook_event_name": name, "session_id": "T-01a097db"}
		for k, v := range fields {
			payload[k] = v
		}
		return payload
	}
	agent.Handle(amp("SessionStart", map[string]any{"source": "startup"}), "amp")
	for i := 0; i < 10; i++ {
		agent.Handle(amp("UserPromptSubmit", map[string]any{"prompt": "keep going"}), "amp")
		agent.Handle(amp("Stop", nil), "amp")
	}
	if asks < 2 {
		t.Errorf("asked %d times over ten turns, want a later turn to try again", asks)
	}
	if asks > ampModelTries {
		t.Errorf("asked %d times, want no more than %d for a thread that never answers",
			asks, ampModelTries)
	}
}

// A session recorded before this change has no model and cannot get one from
// the hook path — that session is over. Its thread is still on this machine,
// and the log is keyed by the hash of the same id the thread file is named
// after, so the rebuild can join the two and fill what is blank.
func TestRebuildFillsTheModelOfAmpSessionsAlreadyRecorded(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	threads := filepath.Join(home, ".cache", "amp", "logs", "threads")
	if err := os.MkdirAll(threads, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, thread := range []string{"T-01a097db", "T-01a069c0"} {
		if err := os.WriteFile(filepath.Join(threads, thread+".log"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stubAmpExport(t, func(thread string) ([]byte, error) {
		if thread == "T-01a069c0" {
			return nil, nil // never synced: it keeps its blank
		}
		return ampThread("gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-terra"), nil
	})

	detail, _ := sessionLogPaths(state)
	lines := ""
	for _, thread := range []string{"T-01a097db", "T-01a069c0"} {
		r := sessionRecord{Key: sessionhash.Hash("org-salt", "amp:"+thread),
			Harness: "amp", Turns: 3, Start: time.Now().Add(-time.Hour), End: time.Now()}
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines += string(line) + "\n"
	}
	if err := os.WriteFile(detail, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	rep, err := RebuildDetail(state, home, "org-salt", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.AmpFilled != 1 {
		t.Errorf("filled %d sessions, want the one thread that answered", rep.AmpFilled)
	}
	raw, err := os.ReadFile(detail)
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r sessionRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		models[r.Key] = r.Model
	}
	if got := models[sessionhash.Hash("org-salt", "amp:T-01a097db")]; got != "openai/gpt-5-6-sol" {
		t.Errorf("the synced thread's session = %q, want the model its export named", got)
	}
	if got := models[sessionhash.Hash("org-salt", "amp:T-01a069c0")]; got != "" {
		t.Errorf("the thread that answered nothing = %q, want a blank rather than a guess", got)
	}
}

// The export carries what each request spent, which is the other half of what
// Amp tells a hook nothing about. Without it an Amp session is turns and tool
// calls beside eight empty columns, and its cost reads as zero rather than as
// unmeasured.
func TestAmpTokensComeFromTheThreadsOwnAccount(t *testing.T) {
	stubAmpExport(t, func(string) ([]byte, error) {
		return ampExportOf(
			ampUsage{Model: "gpt-5.6-sol", Input: 100, CacheWrite: 900, CacheRead: 0, TotalInput: 1000, Output: 50},
			ampUsage{Model: "gpt-5.6-sol", Input: 10, CacheWrite: 0, CacheRead: 4000, TotalInput: 4010, Output: 70},
		), nil
	})
	got, ok := ampThreadFacts(context.Background(), "T-01a097db")
	if !ok {
		t.Fatal("an export with usage on every message said nothing")
	}
	// InTokens holds EVERY input token, the cache classes included, because
	// that is the identity the session log stores and prices against.
	if got.InTokens != 5010 || got.OutTokens != 120 {
		t.Errorf("in/out = %d/%d, want 5010/120", got.InTokens, got.OutTokens)
	}
	if got.CacheWrite != 900 || got.CacheRead != 4000 {
		t.Errorf("cache write/read = %d/%d, want 900/4000", got.CacheWrite, got.CacheRead)
	}
	// Peak context is a level, so it is the fullest ONE request got — never the
	// sum, which would report a context nobody ever held.
	if got.PeakContext != 4010 {
		t.Errorf("peak context = %d, want the fullest single request", got.PeakContext)
	}
}

// An older export states the three parts and not their sum. The sum is an
// identity, so it is computed rather than treated as a missing figure.
func TestAmpTokensAddUpWhenTheTotalIsAbsent(t *testing.T) {
	stubAmpExport(t, func(string) ([]byte, error) {
		return ampExportOf(ampUsage{Model: "gpt-5.6-sol", Input: 100, CacheWrite: 900, CacheRead: 2000, Output: 5}), nil
	})
	got, _ := ampThreadFacts(context.Background(), "T-01a097db")
	if got.InTokens != 3000 {
		t.Errorf("in tokens = %d, want the three parts added", got.InTokens)
	}
}

// The export reports the WHOLE thread every time it is read, so a session that
// asks twice must not end up billed twice. Counts are raised to the reading,
// never added to it.
func TestAmpTokensDoNotDoubleWhenTheThreadIsReadTwice(t *testing.T) {
	facts := ampFacts{InTokens: 5010, OutTokens: 120, CacheWrite: 900, CacheRead: 4000, PeakContext: 4010}
	var s sessionStats
	applyAmpTokens(&s, facts)
	applyAmpTokens(&s, facts)
	if s.inTokens != 5010 || s.outTokens != 120 || s.cacheReadTokens != 4000 {
		t.Errorf("after two reads: in %d out %d cache-read %d, want one thread's worth",
			s.inTokens, s.outTokens, s.cacheReadTokens)
	}
	// And a shorter reading never lowers what a longer one already recorded: a
	// compacted thread reports a floor, and a session's tokens must not fall.
	applyAmpTokens(&s, ampFacts{InTokens: 10, OutTokens: 1})
	if s.inTokens != 5010 {
		t.Errorf("in tokens fell to %d on a thinner reading", s.inTokens)
	}
}

// End to end, and the point of all of it: an Amp session whose payloads carry
// no model, no tokens and no money ends up with a priced estimate, from its own
// thread and the published rates.
func TestAnAmpSessionEndsUpWithAPricedEstimate(t *testing.T) {
	dir := t.TempDir()
	stubAmpExport(t, func(string) ([]byte, error) {
		return ampExportOf(
			ampUsage{Model: "gpt-5.6-sol", Input: 1_000_000, TotalInput: 1_000_000, Output: 100_000},
		), nil
	})
	quiet := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, nil
	}
	agent := NewAgent(quiet, stubLLM{}, nil, nil, Options{StateDir: dir,
		SessionSalt: "org-salt", RunAsync: inline,
		UsageLogPath: filepath.Join(dir, "usage.jsonl"),
		Segment:      contracts.Segment{"team": "revops"}})
	amp := func(name string, fields map[string]any) map[string]any {
		payload := map[string]any{"hook_event_name": name, "session_id": "T-01a097db"}
		for k, v := range fields {
			payload[k] = v
		}
		return payload
	}
	agent.Handle(amp("SessionStart", map[string]any{"source": "startup"}), "amp")
	agent.Handle(amp("UserPromptSubmit", map[string]any{"prompt": "audit the docs"}), "amp")
	agent.Handle(amp("Stop", nil), "amp")

	detail, daily := sessionLogPaths(dir)
	sum := loadSessionLog(detail, daily, nil).summarizeWork(0)
	if len(sum.Models) != 1 {
		t.Fatalf("models = %v, want the one its thread named", sum.Models)
	}
	m := sum.Models[0]
	if m.InTokens != 1_000_000 || m.OutTokens != 100_000 {
		t.Errorf("tokens = %d in / %d out, want the thread's own account", m.InTokens, m.OutTokens)
	}
	// A million fresh input at $4 and 100k output at $20 is $6.00 at the rates
	// this was written against. The figure is checked loosely — the rates are
	// refreshed by `make prices` and will move — but it must be REAL money, not
	// a zero standing in for a measurement nobody made.
	if m.EstCostUSD <= 0 {
		t.Fatalf("estimated cost = %v; the session priced nothing despite naming a model with a published rate",
			m.EstCostUSD)
	}
	if m.CostUSD != 0 {
		t.Errorf("measured cost = %v, want nothing: Amp reports no money and an estimate is not a measurement",
			m.CostUSD)
	}
}

// The first version of the backfill filled the model and stopped there, so a
// session it had already labelled could never gain the tokens the same read was
// carrying. Both halves come from one export; one without the other is a row
// that names a model and prices nothing.
func TestRebuildFillsTokensOnASessionItAlreadyLabelled(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	threads := filepath.Join(home, ".cache", "amp", "logs", "threads")
	if err := os.MkdirAll(threads, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(threads, "T-01a097db.log"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubAmpExport(t, func(string) ([]byte, error) {
		return ampExportOf(ampUsage{Model: "gpt-5.6-sol", Input: 1_000_000,
			TotalInput: 1_000_000, Output: 100_000}), nil
	})

	detail, _ := sessionLogPaths(state)
	r := sessionRecord{Key: sessionhash.Hash("org-salt", "amp:T-01a097db"),
		Harness: "amp", Model: "openai/gpt-5-6-sol", ModelRaw: "gpt-5.6-sol",
		Turns: 3, Start: time.Now().Add(-time.Hour), End: time.Now()}
	line, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(detail, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	rep, err := RebuildDetail(state, home, "org-salt", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.AmpFilled != 1 {
		t.Fatalf("filled %d, want the session that had a model and no tokens", rep.AmpFilled)
	}
	raw, err := os.ReadFile(detail)
	if err != nil {
		t.Fatal(err)
	}
	var back sessionRecord
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.InTokens != 1_000_000 || back.OutTokens != 100_000 {
		t.Errorf("tokens = %d/%d, want the thread's own account", back.InTokens, back.OutTokens)
	}
	if back.EstCostUSD <= 0 {
		t.Error("the repaired record prices nothing; the hook path that would have priced it is long gone")
	}
}
