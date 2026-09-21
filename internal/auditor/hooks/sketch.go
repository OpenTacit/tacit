// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Technique sketches — the opt-in mining channel (docs/mining/mining-design.md
// source 2). At the moment a suggestion is adopted, the agent emits a small,
// factual sketch: the situation (scrubbed characterization summary), the move
// (the adopted technique's recipe), and a per-org-salted session hash so a miner
// can count distinct occurrences without identity. The transcript itself
// never leaves the machine, with or without consent.
//
// Consent is the presence of a sink: no sketch URL configured, no sketches —
// and the wiring lives here, in the open core, where its transparency is the
// trust argument.
package hooks

import (
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/sessionhash"
	"github.com/opentacit/tacit/pkg/scrub"
)

// SketchSink receives one sketch (posted to the miner intake, async).
// A nil sink means the member has not opted in.
type SketchSink func(contracts.Sketch)

const triggerMaxLen = 300

// emitSketchLocked builds and dispatches a sketch for a first adoption.
// Caller holds a.mu; the sink call is dispatched via RunAsync so a slow miner
// can never stall a hook response.
func (a *Agent) emitSketchLocked(st *sessionState, sc shownTechnique, sessionKey string) {
	if a.opts.Sketch == nil {
		return
	}
	trigger := st.lastSummary
	if strings.TrimSpace(trigger) == "" {
		trigger = st.lastPrompt
	}
	trigger = strings.Join(strings.Fields(trigger), " ")
	if r := []rune(trigger); len(r) > triggerMaxLen {
		trigger = string(r[:triggerMaxLen])
	}
	trigger, _ = scrub.Redact(trigger)
	move, _ := scrub.Redact(sc.recipe)
	if strings.TrimSpace(trigger) == "" || strings.TrimSpace(move) == "" {
		return
	}

	harness := sessionKey
	if i := strings.Index(harness, ":"); i >= 0 {
		harness = harness[:i]
	}
	sk := contracts.Sketch{
		SessionHash: sessionhash.Hash(a.opts.SketchSalt, sessionKey),
		Trigger:     trigger,
		Move:        move,
		Harness:     harness,
		Segment:     a.opts.Segment,
		TechniqueID: sc.capID,
		CreatedAt:   a.opts.Now().UTC().Format(time.RFC3339Nano),
	}
	sink := a.opts.Sketch
	a.opts.RunAsync(func() { sink(sk) })
}

// emitWorkedMoveSketch dispatches a TECHNIQUE-LESS sketch — a distilled "worked move"
// observed on a turn that demonstrably succeeded, tied to no existing technique. It is
// the observation-driven discovery producer's input
// (docs/learning/observed-technique-discovery.md): the registry clusters these into
// new candidate techniques. Same scrub + pseudonymization as an adoption sketch; the
// only difference is an empty TechniqueID. Best-effort, off the response path;
// trigger and move are already the model's distillation, so no session state is
// read and no lock is needed.
func (a *Agent) emitWorkedMoveSketch(sessionKey, trigger, move, taskType string) {
	sink := a.opts.Sketch
	if sink == nil {
		return
	}
	trigger = strings.Join(strings.Fields(trigger), " ")
	if r := []rune(trigger); len(r) > triggerMaxLen {
		trigger = string(r[:triggerMaxLen])
	}
	trigger, _ = scrub.Redact(trigger)
	move, _ = scrub.Redact(move)
	if strings.TrimSpace(trigger) == "" || strings.TrimSpace(move) == "" {
		return
	}
	harness := sessionKey
	if i := strings.Index(harness, ":"); i >= 0 {
		harness = harness[:i]
	}
	sk := contracts.Sketch{
		SessionHash: sessionhash.Hash(a.opts.SketchSalt, sessionKey),
		Trigger:     trigger,
		Move:        move,
		Harness:     harness,
		TaskType:    taskType,
		Segment:     a.opts.Segment,
		// TechniqueID intentionally empty: a novel move, not tied to a technique.
		CreatedAt: a.opts.Now().UTC().Format(time.RFC3339Nano),
	}
	a.opts.RunAsync(func() { sink(sk) })
}

// emitWorkflowSketch dispatches a technique-less WORKFLOW sketch — a distilled
// approach the whole session followed, tied to no technique
// (docs/learning/workflow-technique-capture.md). Structurally identical to a
// worked-move sketch and clustered by the same registry pipeline; it differs only
// in what it describes (a multi-turn approach, not one move), which the sketch
// text carries and clustering separates on its own. Best-effort, off the path.
func (a *Agent) emitWorkflowSketch(sessionKey, trigger, move, taskType string) {
	a.emitWorkedMoveSketch(sessionKey, trigger, move, taskType)
}
