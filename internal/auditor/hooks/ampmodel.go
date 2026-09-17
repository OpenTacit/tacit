// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Amp never tells a hook which model ran the turn.
//
// Every other harness names it somewhere the capture path can read: Claude Code
// and Codex put it on each assistant entry of the transcript the payload points
// at (capture.lastAssistantResponse), and a gateway route carries it on the
// call. Amp's plugin API carries it nowhere — no event payload field, and the
// only model on the typed surface belongs to a CUSTOM agent's definition, which
// is empty for the built-in modes nearly every thread runs under. So an Amp
// session used to land in the session log with no model at all, and the Models
// panel — which groups by model key and drops the blank — showed a member who
// works in two harnesses only the one that names it.
//
// What Amp does have is `amp threads export`, which returns the thread as JSON
// with a `usage` block on every assistant message naming the model that served
// it. That is the same shape the transcript scrape reads, one process away.
//
// Two things follow from Amp routing per REQUEST rather than per session. The
// export names several models in one thread — a main loop, a subagent, a
// classifier — so the session takes the busiest, the label that answers "what
// ran this session". And the answer only exists once the thread has synced, so
// the lookup is best-effort, off the hook path, and tried again on a later turn
// when it comes back empty.
package hooks

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/auditor/sessionhash"
	"github.com/opentacit/tacit/internal/modelid"
	"github.com/opentacit/tacit/internal/pricing"
)

const (
	// ampExportTimeout bounds one export. It is a network read of a thread
	// that can hold thousands of messages; a slow one must not outlive the
	// session it describes, and losing a label costs a grouping, not a turn.
	ampExportTimeout = 20 * time.Second
	// ampExportBytes caps what is parsed. A long thread's export runs to
	// megabytes and an unbounded read is a memory hole at the mercy of another
	// program's output.
	ampExportBytes = 128 << 20
	// ampModelTries bounds how many times one session asks. A thread that has
	// never synced answers nothing however often it is asked, and an unbounded
	// retry would spawn a process per turn for the whole session.
	ampModelTries = 3
	// ampThreadPrefix is the shape of an Amp thread id, which is what the
	// plugin sends as the session id. Anything else is not a thread and is not
	// worth a process.
	ampThreadPrefix = "T-"
)

// ampExport runs the export and returns the thread JSON. A variable so a test
// can answer without Amp installed.
var ampExport = func(ctx context.Context, thread string) ([]byte, error) {
	bin, err := exec.LookPath("amp")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "threads", "export", thread)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(out, ampExportBytes))
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return body, readErr
}

// ampFacts is what one thread's export can tell the session log: which model
// served it, and what it spent. Amp reports no money and no tokens through the
// hook channel, so without this an Amp session is turns and tool calls beside
// eight empty columns.
//
// The counts are a FLOOR, not a total. The export carries the requests the
// thread still holds, and a thread that has been compacted no longer holds them
// all — on the thread this was written against, 136 of the 153 requests Amp's
// own usage report counts. An estimate built on it is low by whatever was
// compacted away, which is the honest direction for a floor to be wrong in, and
// the page calls it an estimate.
type ampFacts struct {
	Model       string
	InTokens    int // every input token, the cache classes included
	OutTokens   int
	CacheWrite  int
	CacheRead   int
	PeakContext int // the fullest one request's input got
}

// ampThreadFacts reads one thread's export. ok is false when it says nothing —
// offline, not yet synced, an Amp too old to carry usage, or no Amp on the PATH
// at all. Every one of those is a reason to stay quiet rather than to guess.
func ampThreadFacts(ctx context.Context, thread string) (ampFacts, bool) {
	var facts ampFacts
	if !strings.HasPrefix(thread, ampThreadPrefix) {
		return facts, false
	}
	body, err := ampExport(ctx, thread)
	if err != nil || len(body) == 0 {
		return facts, false
	}
	var export struct {
		Messages []struct {
			Usage struct {
				Model string `json:"model"`
				// Input is split three ways and Total is their sum, which is
				// the identity the session log stores: InTokens holds every
				// input token and the two cache counts say how it was served.
				Input      int `json:"inputTokens"`
				TotalInput int `json:"totalInputTokens"`
				Output     int `json:"outputTokens"`
				CacheRead  int `json:"cacheReadInputTokens"`
				CacheWrite int `json:"cacheCreationInputTokens"`
			} `json:"usage"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &export); err != nil {
		return facts, false
	}
	// Counted by COHORT rather than by label, because one model reaches the
	// export under more than one spelling — a dated build beside its own
	// family — and a vote split between two spellings of one model could hand
	// the session to a third that did less of the work. The label returned is
	// the busiest spelling of the cohort that won, because the log stores
	// both: the cohort to group by, and what the harness actually said.
	requests := map[string]int{}
	labels := map[string]map[string]int{}
	for _, m := range export.Messages {
		u := m.Usage
		total := u.TotalInput
		if total == 0 {
			total = u.Input + u.CacheRead + u.CacheWrite
		}
		facts.InTokens += total
		facts.OutTokens += u.Output
		facts.CacheRead += u.CacheRead
		facts.CacheWrite += u.CacheWrite
		if total > facts.PeakContext {
			facts.PeakContext = total
		}
		model := strings.TrimSpace(u.Model)
		if model == "" {
			continue
		}
		cohort := modelid.Key(model)
		requests[cohort]++
		if labels[cohort] == nil {
			labels[cohort] = map[string]int{}
		}
		labels[cohort][model]++
	}
	facts.Model = busiest(labels[busiest(requests)])
	return facts, facts.Model != "" || facts.InTokens > 0
}

// busiest returns the key with the most requests. A tie goes to the name that
// sorts first, so the same thread always reports the same model rather than
// flickering between two on map order.
func busiest(requests map[string]int) string {
	names := make([]string, 0, len(requests))
	for name := range requests {
		names = append(names, name)
	}
	sort.Strings(names)
	best := ""
	for _, name := range names {
		if best == "" || requests[name] > requests[best] {
			best = name
		}
	}
	return best
}

// resolveAmpModel asks the thread what ran it and what it spent, once a turn
// has been recorded with no model on it. a.mu is NOT held: this is a subprocess
// and a network read, and the daemon answers hooks while it runs.
//
// Single-flight per session, on the pattern the tools_absent inference uses: a
// Stop arrives every turn, and one export at a time bounds what a session whose
// thread never answers can cost.
func (a *Agent) resolveAmpModel(st *sessionState, harness string) {
	if st == nil || harness != "amp" {
		return
	}
	a.mu.Lock()
	thread := st.capture.SessionID
	if !st.ampModelDue || st.ampModelPending || st.capture.Model != "" ||
		st.ampModelTries >= ampModelTries || !strings.HasPrefix(thread, ampThreadPrefix) {
		a.mu.Unlock()
		return
	}
	st.ampModelDue = false
	st.ampModelPending = true
	st.ampModelTries++
	a.mu.Unlock()

	run := a.opts.RunAsync
	if run == nil {
		run = func(fn func()) { go fn() }
	}
	run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), ampExportTimeout)
		defer cancel()
		facts, ok := ampThreadFacts(ctx, thread)
		a.mu.Lock()
		defer a.mu.Unlock()
		st.ampModelPending = false
		if !ok || st.capture.Model != "" {
			return
		}
		// The whole capture takes the label, not just the log: the model is a
		// cohort on everything this session reports, and an Amp session that
		// answered "which model" only in the member's own log would still be
		// blank in every cohort the organization reads.
		st.capture.Model = facts.Model
		applyAmpTokens(&st.stats, facts)
		dbg("amp thread named %s and spent %d in / %d out; recording this session under it",
			facts.Model, facts.InTokens, facts.OutTokens)
		a.recordSessionLocked(st, harness)
	})
}

// ampThreadDir is where Amp keeps one log file per thread it has run on this
// machine. The file's name is the thread id, which is the only local record of
// which threads were this member's — the session log keeps the hash and never
// the id, so without this there is nothing to ask the export about.
func ampThreadDir(home string) string {
	if cache := os.Getenv("XDG_CACHE_HOME"); cache != "" {
		return filepath.Join(cache, "amp", "logs", "threads")
	}
	return filepath.Join(home, ".cache", "amp", "logs", "threads")
}

// fillAmpModels backfills the model of every Amp session already in the log
// that has none, by hashing this machine's thread ids the way the log is keyed
// and asking the threads that match.
//
// It is the same join the transcript rebuild makes and it exists for the same
// reason: a session recorded before the model could be read is not wrong, it is
// blank, and the source that would fill it is still on this machine. Sessions
// whose thread has been deleted, or whose export says nothing, keep their
// blank.
func fillAmpModels(home, salt string, recs map[string]*sessionRecord) int {
	if salt == "" {
		return 0
	}
	logs, err := filepath.Glob(filepath.Join(ampThreadDir(home), "*.log"))
	if err != nil {
		return 0
	}
	sort.Strings(logs)
	filled := 0
	for _, path := range logs {
		thread := strings.TrimSuffix(filepath.Base(path), ".log")
		r := recs[sessionhash.Hash(salt, "amp:"+thread)]
		// A record filled by an earlier run of this — or by the hook path
		// before the export could report tokens — has the model and not the
		// spend. Both halves come from the same read, so it is worth asking
		// again for either one.
		if r == nil || (r.Model != "" && r.InTokens > 0) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), ampExportTimeout)
		facts, ok := ampThreadFacts(ctx, thread)
		cancel()
		if !ok || facts.Model == "" {
			continue
		}
		if r.Model == "" {
			r.Model, r.ModelRaw = modelid.Key(facts.Model), facts.Model
		}
		raise := func(dst *int, v int) {
			if v > *dst {
				*dst = v
			}
		}
		raise(&r.InTokens, facts.InTokens)
		raise(&r.OutTokens, facts.OutTokens)
		raise(&r.CacheWriteTokens, facts.CacheWrite)
		raise(&r.CacheReadTokens, facts.CacheRead)
		raise(&r.PeakContext, facts.PeakContext)
		// The estimate is derived, and the hook path derives it as each session
		// is recorded (sessionStats.record). A record repaired here never goes
		// back through that, so it is priced here or not at all.
		if fresh := r.InTokens - r.CacheWriteTokens - r.CacheReadTokens; fresh >= 0 && r.InTokens > 0 {
			if est, ok := pricing.Estimate(r.ModelRaw, fresh, r.CacheWriteTokens,
				r.CacheReadTokens, r.OutTokens); ok && est > r.EstCostUSD {
				r.EstCostUSD = est
			}
		}
		filled++
	}
	return filled
}

// applyAmpTokens folds one export's counts into a session's running totals.
//
// Assigned rather than added, and only upwards: the export reports the WHOLE
// thread every time it is read, so adding two reads of one thread would bill
// the member twice for the same work. The record's own merge raises counts the
// same way for the same reason (keepLarger).
func applyAmpTokens(s *sessionStats, facts ampFacts) {
	raise := func(dst *int, v int) {
		if v > *dst {
			*dst = v
		}
	}
	raise(&s.inTokens, facts.InTokens)
	raise(&s.outTokens, facts.OutTokens)
	raise(&s.cacheWriteTokens, facts.CacheWrite)
	raise(&s.cacheReadTokens, facts.CacheRead)
	raise(&s.peakContext, facts.PeakContext)
}
