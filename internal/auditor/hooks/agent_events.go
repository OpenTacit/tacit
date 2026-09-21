// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The per-event handlers, in two halves.
//
// A hook event is handled under a.mu exactly once: decideX applies the payload
// to the session, moves the funnel on, and snapshots everything the answer
// depends on. It never releases the lock — handle() takes it and gives it back
// with defer. dispatch then runs the half that must not hold it: registry
// calls, LLM calls, formatting, all of it working from the snapshot alone.
//
// The handlers used to enter locked and unlock themselves before every return,
// one branch at a time. A branch that forgot left the daemon holding a.mu, and
// from there every hook of every session waits forever while the relay drops
// the turns in silence. Splitting the halves means no branch can forget: the
// locked half has no unlock to write.
package hooks

import (
	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/modelid"
)

// branch names what the unlocked half of a turn does.
type branch int

const (
	branchNothing      branch = iota // nothing left to do off the lock
	branchDemo                       // "as a test, …" — fabricate a demonstration
	branchMention                    // "@tacit …" — answer the member
	branchNotice                     // a parked self-test block rides this prompt
	branchProbe                      // the model-facing self-test probe
	branchPending                    // a parked suggestion rides this prompt
	branchAsk                        // nothing to deliver; the cohort ask may be due
	branchStop                       // audit this turn
	branchSessionStart               // session opening notices
)

// decision is what the locked half hands to the unlocked one: the branch to
// run and everything that branch needs, copied while the lock was held. The
// session pointer travels with it for the two calls that take their own lock
// (verifyAdoptionsAsync, mentionResponse) — nothing in dispatch reads session
// fields directly.
type decision struct {
	branch  branch
	harness string
	st      *sessionState
	// adoptions are this prompt's adoption candidates, verified off the lock.
	adoptions []adoptionCandidate
	// prompt-branch payloads.
	demo      string // the demo request
	prompt    string
	record    contracts.CanonicalRecord
	hashKey   string
	lastShown []shownTechnique
	// text is the block this branch delivers: a self-test notice or probe, a
	// parked suggestion, or the self-test block that owns this Stop.
	text   string
	silent bool // a parked suggestion the model applies rather than shows
	// Stop.
	canSuggest bool
	// SessionStart.
	cwd string
}

// dispatch runs the unlocked half and builds the response. a.mu is NOT held.
func (a *Agent) dispatch(d decision) HookResponse {
	// The adoption verifier is an LLM call, so it waits for the lock to be
	// gone — but it stays ahead of every delivery below, as it was when each
	// handler called it right after unlocking.
	a.verifyAdoptionsAsync(d.st, d.adoptions)
	// Amp tells a hook nothing about the model, so a recorded Amp session asks
	// its own thread what ran it. Here for the same reason the verifier is:
	// it wants the lock gone, and it is a subprocess away (ampmodel.go).
	a.resolveAmpModel(d.st, d.harness)
	// A correction the member has now made several times is a standing
	// preference; file it as a draft (repetition.go). Off the response path,
	// once per correction, and only when a real model can name it.
	if d.st != nil && d.st.lastCorrectionCount >= CorrectionThreshold {
		a.raiseRepeatedCorrectionAsync(d.st.lastCorrectionHash, d.st.lastCorrectionCount,
			d.prompt, d.harness)
	}

	switch d.branch {
	case branchDemo:
		return a.deliverDemo(d.demo, d.harness)
	case branchMention:
		if response, ok := a.mentionResponse(d.st, d.harness, d.prompt, d.record, d.hashKey, d.lastShown); ok {
			return response
		}
		return HookResponse{}
	case branchNotice:
		return DeliverNotice(d.text)
	case branchProbe:
		return DeliverSilent(d.text)
	case branchPending:
		if d.silent || formCapable(d.harness) {
			return DeliverSilent(d.text)
		}
		return DeliverContext(d.text)
	case branchAsk:
		if message := a.segmentAsk(d.harness); message != "" {
			return DeliverContext(message)
		}
		return HookResponse{}
	case branchStop:
		// The audit runs on every Stop, including the ones an armed self-test
		// owns — those come through with canSuggest false, so it observes only.
		context := a.auditAtStop(d.st, d.record, d.canSuggest)
		if d.text != "" {
			return DeliverStop(d.text)
		}
		if context != "" {
			return DeliverStop(context)
		}
		return HookResponse{}
	case branchSessionStart:
		if a.opts.RegistryConfigured && len(a.opts.Segment) == 0 {
			a.cohortDirectory()
		}
		if msg := a.repoMarkerNudge(d.cwd); msg != "" {
			return HookResponse{"systemMessage": msg}
		}
		if a.opts.RegistryConfigured && contextAtSessionStart(d.harness) {
			return DeliverSessionContext(playbookNotice())
		}
		return HookResponse{}
	}
	return HookResponse{}
}

func (a *Agent) decidePromptLocked(st *sessionState, payload map[string]any, harness string) decision {
	prompt, _ := payload["prompt"].(string)
	if prompt != "" {
		st.lastPrompt = prompt
	}
	st.turnsSinceShown++
	a.throttle.observeTurn()
	st.turn++
	a.usage.append(usageEvent{Kind: usageQuery, Harness: harness,
		Model: modelid.Key(st.capture.Model)})
	// A correction is only a correction if the agent has already done
	// something. The first turn of a session opening with "don't use X" is a
	// standing instruction, not a repair, and counting it as one would make
	// every member's habits look like a running argument.
	st.lastCorrectionHash, st.lastCorrectionCount = "", 0
	// Counting it and fingerprinting it are two questions, and they were one.
	// note() returns nothing for a correction that carries fewer than two
	// distinctive words — "no, use 9099", "don't push" — because such a saying
	// cannot be told from any other and has no bucket to go in. Counting off
	// that return made the figure "corrections with a usable fingerprint",
	// which is not what the plate says: a member who corrects their agent in
	// four words all day read as a member who never corrects it.
	//
	// So the count follows the detector, and the ledger keeps its own floor.
	// The repeats panel is unchanged — a saying with no fingerprint still has
	// no bucket, and nothing here invents one for it.
	if st.turn > 1 && isCorrection(prompt) {
		st.correctionsCount++
		if hash, count := a.corrections.note(prompt); hash != "" {
			st.lastCorrectionHash, st.lastCorrectionCount = hash, count
		}
	}
	a.detectReactionLocked(st, prompt)
	d := decision{harness: harness, st: st, prompt: prompt}
	d.adoptions = a.detectAdoptionLocked(st, prompt, "")
	a.inferHelpedLocked(st)

	if request, ok := parseDemoRequest(prompt); ok {
		d.branch, d.demo = branchDemo, request
		return d
	}
	if prompt != "" && mentionRe.MatchString(prompt) && !deliverless(harness) {
		d.branch = branchMention
		d.record = st.capture.Record()
		d.hashKey = st.hashKey
		d.lastShown = append([]shownTechnique(nil), st.lastShown...)
		return d
	}
	if block := st.pendingSelfTest; block != "" {
		st.pendingSelfTest = ""
		d.branch, d.text = branchNotice, block
		return d
	}
	if a.takeContextProbeLocked(harness) {
		a.noteSelfTestLocked(harness, "context-delivered")
		d.branch, d.text = branchProbe, ContextProbeText
		return d
	}
	if context, silent := a.deliverPendingLocked(st); context != "" {
		d.branch, d.text, d.silent = branchPending, context, silent
		return d
	}
	d.branch = branchAsk
	return d
}

func (a *Agent) decidePreToolLocked(st *sessionState, payload map[string]any, harness string) decision {
	name, _ := payload["tool_name"].(string)
	if name == "AskUserQuestion" && hasTacitQuestion(payload) {
		st.questionsOffered++
		a.usage.append(usageEvent{Kind: usageOffered, Harness: harness,
			Model: modelid.Key(st.capture.Model)})
		a.noteProbeFormLocked(harness, "context-form-offered")
	}
	a.detectAdoptionLocked(st, name+" "+capture.AsText(payload["tool_input"]), name)
	return decision{}
}

func (a *Agent) decidePostToolLocked(st *sessionState, payload map[string]any, harness string) decision {
	if name, _ := payload["tool_name"].(string); name == "AskUserQuestion" {
		if ti, question := tacitQuestion(payload); ti != nil {
			if answers, _ := ti["answers"].(map[string]any); answers != nil {
				if label, _ := answers[question].(string); label != "" {
					a.noteProbeFormLocked(harness, "context-form-answered: "+label)
				}
			}
		}
		a.captureQuestionAnswerLocked(st, payload)
	}
	return decision{}
}

func (a *Agent) decideSessionStartLocked(payload map[string]any, harness string) decision {
	cwd, _ := payload["cwd"].(string)
	return decision{branch: branchSessionStart, harness: harness, cwd: cwd}
}

func (a *Agent) decideStopLocked(st *sessionState, harness string) decision {
	// Reserve the session while locked. Characterization and all other slow
	// work start only once handle() has released the lock.
	record, canSuggest := a.gateLocked(st)
	selfTest := a.takeSelfTestLocked(harness, ModeBlock)
	if selfTest && canSuggest {
		st.synthesizing, canSuggest = false, false
	}
	if deliverless(harness) {
		canSuggest = false
	}
	a.verifyAdoptionsAtStopLocked(st, record)

	d := decision{branch: branchStop, harness: harness, st: st, record: record, canSuggest: canSuggest}
	if selfTest {
		block := formatSelfTest(harness)
		outcome := "delivered"
		switch {
		case deliverless(harness):
			block, outcome = "", "suppressed"
		case parkOnly(harness):
			st.pendingSelfTest, block, outcome = block, "", "parked"
		}
		a.noteSelfTestLocked(harness, outcome)
		d.text = block
	}
	return d
}
