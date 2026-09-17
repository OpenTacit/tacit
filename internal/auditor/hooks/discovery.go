// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Discovery: what the agent learns from a turn AFTER the member has their
// answer. Enrichment fills the one audit-fact field the structural
// characterizer cannot see; the two distillers turn a demonstrably-good turn,
// and a demonstrably-good session, into technique-less sketches. All of it
// runs off the response path and fails silently.
package hooks

import (
	"errors"
	"strings"

	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/llm"
)

// toolsAbsentInferrer is implemented by a client that can judge what would have
// helped. The heuristic deliberately does not implement it — see llm.ErrCannotJudge.
type toolsAbsentInferrer interface {
	InferToolsAbsent(transcript string, toolsUsed []string) ([]string, error)
}

// workedMoveInferrer distills a reusable move from a succeeded turn — the
// discovery producer's signal (docs/learning/observed-technique-discovery.md). Like
// toolsAbsentInferrer, only a real model implements it.
type workedMoveInferrer interface {
	InferWorkedMove(transcript string) (trigger, move string, err error)
}

// workflowInferrer distills a reusable workflow from a session's phase trace —
// the strategic-pattern producer (docs/learning/workflow-technique-capture.md).
type workflowInferrer interface {
	InferWorkflow(traceText string) (trigger, move string, err error)
}

// workflowMinPhases is the phase-diversity floor: a workflow worth a technique runs
// through at least this many distinct phases (one-shot sessions are not
// workflows). Paired with the verified-good gate.
const workflowMinPhases = 3

// workflowGateReady reports whether a session has become a workflow candidate:
// it reached a verified-good state, ran through enough distinct phases, and has
// not already emitted. Pure, so the gate is testable without a live agent.
func workflowGateReady(phaseSeq []string, verified, alreadyEmitted bool) bool {
	if !verified || alreadyEmitted {
		return false
	}
	distinct := map[string]bool{}
	for _, p := range phaseSeq {
		if p != "" {
			distinct[p] = true
		}
	}
	return len(distinct) >= workflowMinPhases
}

// enrichFactAsync infers the one field the structural characterizer cannot see —
// what would have HELPED and was not used — and posts it back to the registry
// against this audit's id.
//
// It runs in its own goroutine and is NEVER awaited. tools_absent needs a model's
// judgment, and a model call on the same-turn path is exactly what the hook agent
// is designed to avoid; but after the response is out the door, latency is free.
// So the field that was empty because it was too expensive to observe in time is
// now filled at no cost to the member.
//
// Failure is silent by design: this is bookkeeping for the learning layer, and it
// may never cost a member their turn. It is not, however, unreported — a real API
// failure lands in the LLM health signal like any other.
func (a *Agent) enrichFactAsync(st *sessionState, auditID string,
	record contracts.CanonicalRecord, char contracts.Characterization) {
	if a.opts.Enrich == nil || auditID == "" {
		return
	}
	inferrer, ok := a.llm.(toolsAbsentInferrer)
	if !ok {
		return // no model that can judge; do not guess
	}
	// Single-flight per session: observation is un-gated, so every Stop reaches
	// here. One inference at a time bounds the cost; a fact skipped while one is
	// in flight stays unenriched, which is an honest state — never a guessed one.
	a.mu.Lock()
	if st.enriching {
		a.mu.Unlock()
		dbg("tools_absent inference already in flight; skipping for %s", auditID)
		return
	}
	st.enriching = true
	a.mu.Unlock()
	run := a.opts.RunAsync
	if run == nil {
		run = func(fn func()) { go fn() }
	}
	run(func() {
		defer func() {
			a.mu.Lock()
			st.enriching = false
			a.mu.Unlock()
		}()
		absent, err := inferrer.InferToolsAbsent(capture.ToTranscriptText(record), char.ToolsUsed)
		if errors.Is(err, llm.ErrCannotJudge) {
			return // heuristic is answering; already reported as no-key
		}
		a.noteLLM(err)
		if err != nil {
			dbg("tools_absent inference failed for %s: %v", auditID, err)
			return
		}
		if len(absent) == 0 {
			return // nothing was missed — a common, correct, and honest answer
		}
		dbg("tools_absent for %s: %v", auditID, absent)
		// Keep a member-local copy as well as sending it up. The registry
		// learns this org-wide and can never tell one member what THEY keep
		// missing, which is the only form of it that is actionable by the
		// person reading.
		a.mu.Lock()
		st.stats.noteAbsent(absent)
		a.mu.Unlock()
		if err := a.opts.Enrich(contracts.AuditFactEnrichment{
			AuditID: auditID, ToolsAbsent: absent,
		}); err != nil {
			dbg("tools_absent post failed for %s: %v", auditID, err)
		}
	})
}

// discoverWorkedMoveAsync distills a reusable move from a turn that DEMONSTRABLY
// succeeded and emits it as a technique-less sketch — the observation-driven discovery
// producer's input (docs/learning/observed-technique-discovery.md). The gate is strict
// and non-model: only a verification pass (a test/build that ran and passed this
// turn) qualifies, so the model never decides "this worked" — it only names the
// move on a turn we already know did. Runs off the response path, best-effort,
// silent on failure, and only when a sketch sink is configured (the consent
// signal). No worked-move judgment without a real model.
func (a *Agent) discoverWorkedMoveAsync(st *sessionState, record contracts.CanonicalRecord, char contracts.Characterization) {
	if a.opts.Sketch == nil || !verificationPassed(record.ToolCalls) {
		return
	}
	wm, ok := a.llm.(workedMoveInferrer)
	if !ok {
		return
	}
	sessionKey, taskType := st.key, char.TaskType
	run := a.opts.RunAsync
	if run == nil {
		run = func(fn func()) { go fn() }
	}
	run(func() {
		trigger, move, err := wm.InferWorkedMove(capture.ToTranscriptText(record))
		if err != nil {
			if !errors.Is(err, llm.ErrCannotJudge) {
				a.noteLLM(err)
			}
			return
		}
		if move == "" {
			return // no crisp reusable move — the honest, common answer
		}
		a.emitWorkedMoveSketch(sessionKey, trigger, move, taskType)
	})
}

// discoverWorkflowAsync accumulates the session's phase trace and, once the
// session has reached a verified-good state across enough distinct phases,
// distils the WORKFLOW (the shape of the work) into a technique-less sketch — the
// strategic-pattern producer (docs/learning/workflow-technique-capture.md). At most
// one per session. The distillation sees only the phase sequence and a one-line
// context, never transcript content (turn-scoped capture retains none), so a
// workflow technique is structural by construction — and privacy-safe by the same.
func (a *Agent) discoverWorkflowAsync(st *sessionState, record contracts.CanonicalRecord, char contracts.Characterization) {
	if a.opts.Sketch == nil {
		return
	}
	wf, ok := a.llm.(workflowInferrer)
	if !ok {
		return
	}
	a.mu.Lock()
	if char.TaskType != "" {
		st.phaseSeq = append(st.phaseSeq, char.TaskType)
	}
	if verificationPassed(record.ToolCalls) {
		st.verifiedSession = true
	}
	ready := workflowGateReady(st.phaseSeq, st.verifiedSession, st.workflowEmitted)
	if ready {
		st.workflowEmitted = true // claim before releasing: at most one per session
	}
	sessionKey, taskType := st.key, char.TaskType
	traceText := workflowTraceText(st.phaseSeq, st.lastSummary)
	a.mu.Unlock()
	if !ready {
		return
	}
	run := a.opts.RunAsync
	if run == nil {
		run = func(fn func()) { go fn() }
	}
	run(func() {
		trigger, move, err := wf.InferWorkflow(traceText)
		if err != nil {
			if !errors.Is(err, llm.ErrCannotJudge) {
				a.noteLLM(err)
			}
			return
		}
		if move == "" {
			return
		}
		a.emitWorkflowSketch(sessionKey, trigger, move, taskType)
	})
}

// workflowTraceText renders a session's phase sequence and one-line context for
// the distiller. Only task_types (structural) and the already-scrubbed summary.
func workflowTraceText(phaseSeq []string, summary string) string {
	return "Phase sequence: " + strings.Join(phaseSeq, " → ") + "\nContext: " + summary
}
