// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The suggestion pipeline: gate the turn, run the audit at Stop, and commit
// or park the one technique that came out of it. Everything here is bounded by
// the delivery-latency contract — the member's turn ends on time whether or
// not a suggestion is ready.
package hooks

import (
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/auditor/audit"
	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/sessionhash"
)

// usable reports whether a brief synthesis is a real suggestion (not empty,
// not the LLM's explicit NONE decline).
func usable(text string) bool {
	if text == "" {
		return false
	}
	return strings.ToUpper(strings.TrimSpace(text)) != "NONE"
}

// gateLocked decides whether to audit this turn and reserves the session
// (synthesizing=true) so a concurrent Stop can't start a second audit.
// Caller holds a.mu.
// gateLocked always returns the record — observation is unconditional — and
// reports whether the SUGGESTION stage may run. The reservation
// (st.synthesizing) is taken only when it may: callers must not clear a
// reservation they never took, or they stomp the in-flight suggestion's.
func (a *Agent) gateLocked(st *sessionState) (contracts.CanonicalRecord, bool) {
	record := st.capture.Record()
	st.capture.EndTurn() // this Stop's audit owns the turn; the next starts clean
	if st.synthesizing || st.pending != nil {
		return record, false // one suggestion in flight at a time
	}
	// One budget for the member, across every live session and across daemon
	// restarts (throttle.go). st.turnsSinceShown is a different clock with a
	// different job — how long to keep listening for a reaction to the technique
	// this session was shown — and is deliberately not consulted here.
	if !a.throttle.allow() {
		return record, false
	}
	st.synthesizing = true // reserved; cleared on skip or after synth
	st.synthesizingSince = a.opts.Now()
	return record, true
}

// noteDeclines records the fit-check's non-fits on the session, so /v1/hooks/stats
// can distinguish "nothing was relevant" from "retrieval found techniques and the
// model rejected them" — the two states that were previously identical from the
// member's seat. Counts every decline in the burst, including those on a turn
// that ultimately showed a lower-ranked technique: a decline is a decline, and the
// ratio is what makes the number worth reading. declined[i] is set only for an
// explicit NONE, never for a synth error, so a broken model does not inflate it.
func (a *Agent) noteDeclines(st *sessionState, fresh []contracts.EvidenceCandidate, declined []bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range declined {
		if declined[i] {
			st.declinedCount++
			st.lastDeclined = fresh[i].TechniqueID
		}
	}
}

// auditAtStop runs characterize -> retrieve -> gate on candidates ->
// synthesize within the budget. Returns the formatted suggestion to deliver
// on THIS turn, or "". Retrieval and synthesis both run OFF the Stop path, so
// the member-visible wait is bounded by SynthBudget alone; a pipeline that
// exceeds the budget keeps running and PARKS its result for the next prompt —
// a slow registry or model degrades to next-turn delivery, never a long
// stall. (Amp is uniform here: its harness
// can't RENDER at Stop, but the Stop response is still the delivery vehicle
// — the Amp plugin parks it on disk and displays it at the next prompt.)
func (a *Agent) auditAtStop(st *sessionState, record contracts.CanonicalRecord,
	canSuggest bool) string {
	// Mint the audit id BEFORE retrieval, not after: it is the join key between
	// what this interaction WAS (the characterization, which the registry records
	// as an audit fact) and how it turned OUT (the feedback events below, which
	// carry the same id). Minting it later — as this did — meant the registry saw
	// every characterization with no id to file it under, and the context half of
	// every audit was unrecoverable (docs/learning/synthesis-design.md).
	auditID := audit.NewAuditID()
	char := capture.Characterize(record)
	char.AuditID = auditID
	char.SessionHash = sessionhash.Hash(a.opts.SessionSalt, st.hashKey)
	// Techniques this member demonstrably uses, from member-local memory. This
	// is the registry's only personalization input for its "you use N of M
	// techniques common on your team" line; without it, N is always 0.
	char.UsedTechniqueIDs = a.memory.UsedIDs(time.Now())
	a.mu.Lock()
	st.lastSummary = char.SummaryText
	a.mu.Unlock()
	dbg("characterized: audit=%s summary=%q harness=%s canSuggest=%v",
		auditID, char.SummaryText, char.Harness, canSuggest)

	type result struct {
		technique contracts.EvidenceCandidate
		text      string
		rank      int
		ok        bool
	}
	// done carries the worker's result to a Stop path still inside its budget.
	// One sender, capacity one: the send can never block, so the worker may make
	// it while holding a.mu.
	done := make(chan result, 1)
	// timedOut is the delivery decision, and a.mu owns it. The worker takes the
	// lock to hand its result over; the Stop path takes the same lock to stop
	// waiting for it. Whichever arrives first, the other sees it — so a result
	// is delivered on this turn or parked for the next one, never neither and
	// never both.
	timedOut := false

	// EVERYTHING that can wait on the network — retrieval included — runs off
	// the Stop path. The member-visible wait is bounded by SynthBudget alone:
	// a slow registry degrades to parked next-turn delivery exactly like a
	// slow model, and an observation-only turn never waits at all. Retrieval
	// must never run synchronously here — a slow-but-alive registry could hold
	// the member's end-of-turn for the full relay timeout, the one thing this
	// agent must never do.
	a.opts.RunAsync(func() {
		// finish ends the worker's half of the delivery: it releases the
		// reservation once, then either hands the result to a waiting Stop or
		// parks it for the next prompt. Both halves of that decision happen
		// under a.mu, which is what keeps a result from falling between them.
		finish := func(found result) {
			a.mu.Lock()
			defer a.mu.Unlock()
			if canSuggest {
				st.synthesizing = false // release only a reservation we actually took
			}
			if timedOut {
				if found.ok {
					st.pending = &pending{auditID: auditID, text: found.text,
						technique: found.technique, rank: found.rank, char: char,
						silent: silentDelivery(char, found.technique)}
				}
				return
			}
			done <- found
		}
		giveUp := func() { finish(result{}) }
		evidence, err := a.evidence(char)
		// Every Stop retrieves, so this is the freshest read of registry health
		// the agent gets — a rotated key or a dead registry shows up here first.
		a.noteRegistry(err)
		if err != nil {
			// Registry down must never break a turn: no suggestion, no error.
			dbg("evidence error: %v; no suggestion", err)
			giveUp()
			return
		}
		// Every audit recorded a fact, so every audit gets enriched — not just
		// the ones that happened to yield a suggestion.
		a.enrichFactAsync(st, auditID, record, char)
		// And every audit that demonstrably succeeded seeds discovery: distill the
		// worked move and emit it as a technique-less sketch for the registry to cluster.
		a.discoverWorkedMoveAsync(st, record, char)
		// Track the session's phase shape and, once it has demonstrably worked
		// across enough phases, distill the workflow (once per session).
		a.discoverWorkflowAsync(st, record, char)

		// Observation ends here. Everything below is coaching, and only runs
		// when the suggestion gate passed — the member's attention is budgeted;
		// the evidence corpus is not.
		if !canSuggest {
			return
		}

		// Gate: at least one relevant candidate we haven't shown yet. Thin
		// evidence is deliberately allowed — day-one seed techniques are exactly what
		// the in-harness audit exists to surface (graceful degradation).
		if len(evidence.Candidates) == 0 {
			dbg("evidence returned 0 candidates; no suggestion")
			giveUp()
			return
		}
		// Candidates we haven't already shown this session, in rank order. We walk
		// this list and fit-check each until one lands: the top technique is often a
		// near-miss the LLM (correctly) declines, and giving up there would waste a
		// perfectly-fitting technique sitting at rank 2-3. Precision still holds — every
		// shown technique is one the LLM confirmed fits — but a mediocre #1 no longer
		// silences the whole turn.
		a.mu.Lock()
		fresh := make([]contracts.EvidenceCandidate, 0, len(evidence.Candidates))
		now := time.Now()
		for _, c := range evidence.Candidates {
			// shownIDs is this session's slate; memory is the member's standing
			// verdicts — a technique they dismissed last week or already demonstrably
			// use is repetition, and repetition is how the trust budget dies.
			if !st.shownIDs[c.TechniqueID] && !a.memory.Suppressed(c.TechniqueID, now) {
				fresh = append(fresh, c)
			}
		}
		a.mu.Unlock()
		if len(fresh) == 0 {
			giveUp()
			return
		}
		// Burst bound: fit-check at most MaxFitChecks candidates (one LLM call each).
		// Caps the calls a single suggestion can fan out, so a tight per-minute API
		// limit is less likely to 429. Trades away the chance to reach a fitting technique
		// ranked below the cut — lower it only if rate limits bite.
		capped := len(fresh) > a.opts.MaxFitChecks
		if capped {
			fresh = fresh[:a.opts.MaxFitChecks]
		}
		dbg("candidates: %d total, %d fresh (capped=%v, max=%d); walking in rank order",
			len(evidence.Candidates), len(fresh), capped, a.opts.MaxFitChecks)

		transcript := capture.ToTranscriptText(record)

		// Fit-check every fresh candidate CONCURRENTLY, then take the
		// highest-ranked one the LLM accepted. Fanning out (rather than a serial
		// walk) keeps wall-clock at ~one synthesis regardless of how many techniques,
		// so a rank-2/3 hit still lands inside the same-turn budget instead of
		// parking. Each synth is scoped to exactly its technique, so the shown technique
		// always matches what was fit-checked.
		texts := make([]string, len(fresh))
		// declined[i] marks a candidate the LLM fit-checked and rejected (NONE) —
		// distinct from a synth error (which leaves both texts[i] and declined[i]
		// unset, so a transient failure is never miscounted as a non-fit).
		declined := make([]bool, len(fresh))
		// D1 piggyback (docs/learning/validation-without-review.md): judge shadow
		// candidates in the SAME concurrent burst. They are never surfaced — their
		// verdicts feed shadow_shown/shadow_declined telemetry only and can never
		// become the shown technique. Capped like the real burst so a large shadow set
		// can't blow the fan-out. shadowJudged[i] marks a verdict (no synth error);
		// shadowFit[i] marks the synthesizer accepting it.
		shadowFresh := evidence.ShadowCandidates
		if len(shadowFresh) > a.opts.MaxFitChecks {
			shadowFresh = shadowFresh[:a.opts.MaxFitChecks]
		}
		shadowJudged := make([]bool, len(shadowFresh))
		shadowFit := make([]bool, len(shadowFresh))
		var wg sync.WaitGroup
		for i := range fresh {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				briefEvidence := evidence
				briefEvidence.Candidates = []contracts.EvidenceCandidate{fresh[i]}
				text, err := a.llm.Synthesize(transcript, briefEvidence, true)
				a.noteLLM(err) // swallowed below — but never again unreported
				if err != nil {
					dbg("synth error for %s: %v", fresh[i].TechniqueID, err)
					return
				}
				dbg("synth for %s returned: %q (usable=%v)", fresh[i].TechniqueID, text, usable(text))
				if usable(text) {
					texts[i] = text
				} else {
					declined[i] = true
				}
			}(i)
		}
		// Shadow candidates ride the same wg, so the whole burst is still one
		// wall-clock. Each shadow synth is scoped to exactly its technique; the result
		// text is discarded (never shown) — only the fit/decline verdict is kept.
		for i := range shadowFresh {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				briefEvidence := evidence
				briefEvidence.Candidates = []contracts.EvidenceCandidate{shadowFresh[i]}
				briefEvidence.ShadowCandidates = nil
				text, err := a.llm.Synthesize(transcript, briefEvidence, true)
				a.noteLLM(err)
				if err != nil {
					dbg("shadow synth error for %s: %v", shadowFresh[i].TechniqueID, err)
					return
				}
				shadowJudged[i] = true
				shadowFit[i] = usable(text)
			}(i)
		}
		wg.Wait()

		// Retrieval-quality telemetry: each candidate the fit-check rejected is a
		// 'declined' event — surfaced by retrieval but judged a non-fit here, so
		// never shown. Declined events measure retrieval quality and stay off the
		// adopted/helped funnel. Emitted for the whole burst regardless of whether
		// one candidate landed, since a decline is a decline even when a
		// lower-ranked technique is ultimately shown.
		a.postFeedback(audit.DeclinedEvents(auditID, fresh, declined, char.Segment, char.TaskType))
		// The same verdicts, kept locally: the registry gets the telemetry, but the
		// member's own stats endpoint needs them too or a declining fit-check is
		// invisible on the machine where it happened.
		a.noteDeclines(st, fresh, declined)
		// Shadow relevance telemetry: each judged shadow candidate is a
		// shadow_shown (fit-checked a fit — it would have surfaced) or a
		// shadow_declined (a non-fit). Off every funnel; the registry rollup
		// ignores these stages by construction.
		a.postFeedback(audit.ShadowEvents(auditID, shadowFresh, shadowFit, shadowJudged, char.Segment, char.TaskType))

		var found result
		for i := range fresh {
			if texts[i] != "" { // first usable in rank order
				found = result{technique: fresh[i], text: texts[i], rank: i + 1, ok: true}
				break
			}
		}
		finish(found)
	})

	if !canSuggest {
		// Observation-only turn: the fact is recorded asynchronously; there is
		// nothing to deliver, so there is nothing to wait for.
		return ""
	}
	// deliverLocked turns a result the worker handed over inside the budget into
	// this turn's Stop text — or parks it, on the two channels that cannot carry
	// a suggestion at Stop. Caller holds a.mu.
	deliverLocked := func(r result) string {
		if !r.ok {
			return ""
		}
		if silentDelivery(char, r.technique) {
			// Silent application can't ride the Stop envelope (only
			// systemMessage is valid there, and it would render). Park it:
			// the next prompt in the autonomous run delivers
			// additionalContext-only.
			st.pending = &pending{auditID: auditID, text: r.text, technique: r.technique, rank: r.rank, char: char, silent: true}
			return ""
		}
		if parkOnly(st.capture.Harness()) || formCapable(st.capture.Harness()) {
			// Park for the next prompt. Two different reasons, one mechanism:
			// parkOnly has no visible Stop channel at all, and a form has to be
			// RAISED by the model, which only a model-facing envelope can ask
			// for — and Stop carries none. 'shown' records at delivery
			// (commitLocked, from deliverPendingLocked), never here, so a
			// suggestion nobody was offered is never counted as one.
			st.pending = &pending{auditID: auditID, text: r.text, technique: r.technique, rank: r.rank, char: char}
			return ""
		}
		return a.commitLocked(st, auditID, r.text, r.technique, r.rank, char, false)
	}
	select {
	case r := <-done:
		// Finished within budget -> deliver same-turn (always so for the
		// instant heuristic path; and for fast real-LLM calls).
		a.mu.Lock()
		defer a.mu.Unlock()
		return deliverLocked(r)
	case <-time.After(a.opts.SynthBudget):
		a.mu.Lock()
		defer a.mu.Unlock()
		// A result that beat the timer by a hair is still this turn's: take it
		// before giving the delivery to the worker. Only once this drain comes
		// up empty does the worker own it, and it learns that from timedOut
		// under the lock we are holding.
		select {
		case r := <-done:
			return deliverLocked(r)
		default:
			timedOut = true
			return ""
		}
	}
}

// silentDelivery: an autonomy-eligible technique in a consumer:agent session is
// applied by the model, not surfaced. Everything else — any technique in a human
// session, an ineligible technique in an agent session — stays visible.
func silentDelivery(char contracts.Characterization, technique contracts.EvidenceCandidate) bool {
	return char.Segment["consumer"] == "agent" && technique.AutonomyEligible
}

// commitLocked records the shown funnel + per-session delivery state for the
// ONE surfaced technique and formats the compact member-facing nudge. Shared by
// same-turn (Stop) and parked (next-prompt) delivery. Caller holds a.mu.
func (a *Agent) commitLocked(st *sessionState, auditID, text string,
	technique contracts.EvidenceCandidate, rank int, char contracts.Characterization, silent bool) string {

	st.suggestionsMade++ // per-session tally, for /v1/hooks/stats
	st.turnsSinceShown = 0
	a.throttle.record()
	st.lastAuditID = auditID
	st.lastSegment = char.Segment // downstream stages (adopted/helped) inherit it
	st.lastShown = []shownTechnique{{technique.TechniqueID, technique.Name, technique.Recipe}}
	st.shownIDs[technique.TechniqueID] = true
	st.noteSuggestionLocked(technique, a.opts.Now())
	// No tier-2 directive: the member reacts ambiently (their next natural
	// message) or just proceeds — the suggestion's own footer states the
	// affordance. The hidden next-turn directive was dropped because Claude Code
	// surfaces model-facing additionalContext as a visible, sticky
	// "tacit-suggestions" chip, leaking the coaching text.
	result := audit.Result{
		AuditID: auditID, Characterization: char,
		Evidence:            contracts.EvidenceBlock{Candidates: []contracts.EvidenceCandidate{technique}},
		AuditText:           text,
		ShownTechniqueIDs:   []string{technique.TechniqueID},
		ShownTechniqueRanks: []int{rank},
	}
	// The delivery-mode ledger: every shown event states how the technique reached
	// the session, so the funnel can split suggested-and-adopted from
	// silently-applied — the audit trail evidence-gated autonomy stands on.
	// SourceAmbient: both callers of commitLocked deliver the winner of a
	// fit-check burst (same-turn or parked), so this shown event is one the
	// burst could equally have declined — which is exactly the pairing the
	// decline rate is a ratio of. The member-pull paths post their own shown
	// events and never come through here.
	events := audit.ShownEvents(result, char.Segment, contracts.SourceAmbient)
	mode := "suggested"
	if silent {
		mode = "applied"
	}
	for i := range events {
		events[i].Delivery = mode
	}
	a.postFeedback(events)
	// Member-local usage history (usagelog.go): log `shown` here, at the one
	// site with the technique name in hand, so the Usage view's drill-down is
	// labelled without a registry round-trip.
	a.usage.append(usageEvent{Kind: usageShown, Cap: technique.TechniqueID,
		Name: technique.Name, Harness: segHarness(char.Segment),
		Model: char.Segment["model"]})
	if silent {
		return formatSilent(text, technique)
	}
	if formCapable(segHarness(char.Segment)) {
		return formatForm(text, technique)
	}
	return a.format(text, technique, a.takeUpgradeHintLocked(st))
}

// takeUpgradeHintLocked is true at most once per session: heuristic-only
// because no key is configured, so tell the member (once) how to enable
// sharp, fit-checked suggestions.
func (a *Agent) takeUpgradeHintLocked(st *sessionState) bool {
	if st.upgradeHintShown {
		return false
	}
	up, ok := a.llm.(UpgradeReporter)
	if !ok || !up.UpgradeAvailable() {
		return false
	}
	st.upgradeHintShown = true
	return true
}

// deliverPendingLocked delivers a suggestion PARKED by the slow-LLM fallback
// (or parked for silent application — the Stop envelope can't carry
// additionalContext, so autonomous delivery always rides the next prompt).
// Caller holds a.mu.
func (a *Agent) deliverPendingLocked(st *sessionState) (ctx string, silent bool) {
	if st.pending == nil {
		return "", false
	}
	p := *st.pending
	st.pending = nil
	return a.commitLocked(st, p.auditID, p.text, p.technique, p.rank, p.char, p.silent), p.silent
}
