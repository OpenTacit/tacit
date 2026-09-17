// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The feedback funnel: how a shown technique becomes adopted, helped or
// dismissed. Three ways in — the member's natural message, a tool call that
// demonstrably makes the move, and the one-keystroke answer to an OpenTacit form —
// and one way out, postFeedback, which posts to the registry and tees the
// outcome into the member's local usage log.
package hooks

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/auditor/audit"
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/llm"
)

// ReactionWindow is how many turns after a suggestion is shown we still read
// the member's natural message as a possible reaction to it.
const ReactionWindow = 2

// HelpedConfirmTurns is how many further member turns a behaviorally-adopted
// technique must survive — used, then neither dismissed nor reacted to negatively
// across the window — before the agent infers a low-confidence 'helped'. The
// helped denominator is otherwise starved: members adopt but rarely type
// "helped", so the one outcome the whole evidence thesis rests on goes
// unmeasured. This lets it fill from behaviour ("they used the move and kept
// working") while staying deliberately conservative — inferred confidence,
// once per technique, only atop an observed adoption. Tunable; 2 is the pilot
// default.
const HelpedConfirmTurns = 2

// Words too common to be an adoption signal when matching a tool call against
// a suggested technique's text. Adoption is an inferred signal and must be
// calibrated (E1, docs/delivery/low-intrusion-plan.md) before it is ever trusted.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "your": true, "from": true, "when": true, "have": true,
	"into": true, "using": true, "use": true, "query": true, "data": true,
	"them": true, "here": true, "hand": true, "rows": true, "just": true,
}

var tokenSplitRe = regexp.MustCompile(`[^a-z0-9]+`)

// tokens returns distinctive lowercase tokens (len>=4, non-stopword).
func tokens(text string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tokenSplitRe.Split(strings.ToLower(text), -1) {
		if len(t) >= 4 && !stopwords[t] {
			out[t] = true
		}
	}
	return out
}

// Ambient reaction cues, checked (negatives first) against the member's next
// natural message — so feedback needs no dedicated prompt or command.
var reactions = []struct {
	phrases []string
	stage   string
	reason  string
}{
	{[]string{"didn't work", "didnt work", "doesn't work", "that's wrong", "thats wrong",
		"incorrect", "bad tip"}, "dismissed", "didnt-work"},
	{[]string{"already knew", "already know that", "knew that", "i know that", "old news"},
		"dismissed", "already-knew"},
	{[]string{"not relevant", "irrelevant", "not useful", "not helpful", "unhelpful",
		"off topic", "off-topic", "ignore that", "not applicable", "wasn't relevant",
		"wasnt relevant"}, "dismissed", "not-relevant"},
	// "helped"/"helps" must be here: the suggestion footer literally says
	// reply "helped", and bare "helped" contains none of the longer phrases.
	{[]string{"helped", "helps", "helpful", "good tip", "great tip", "nice tip",
		"useful", "that worked", "good suggestion", "good call"}, "helped", ""},
}

// classifyReaction maps a natural message to (stage, reason, confidence), or
// ok=false. Confidence is "explicit" when the message is reaction-dominant and
// "inferred" when the cue is embedded in a longer message — the gradient E1
// calibrates against.
func classifyReaction(text string) (stage, reason, confidence string, ok bool) {
	t := strings.ToLower(text)
	for _, r := range reactions {
		for _, p := range r.phrases {
			if strings.Contains(t, p) {
				residual := 0
				for _, w := range tokenSplitRe.Split(strings.Replace(t, p, " ", 1), -1) {
					if w != "" {
						residual++
					}
				}
				confidence = "inferred"
				if residual <= 2 {
					confidence = "explicit"
				}
				return r.stage, r.reason, confidence, true
			}
		}
	}
	return "", "", "", false
}

// adoptionMinTokens is the lexical trigger's floor: distinct non-stopword
// tokens a message must share with a technique before adoption is even a
// CANDIDATE. A 1-token floor lets vocabulary collisions cascade — a single
// shared four-letter word marks a technique adopted, then two quiet turns
// promote that to helped.
const adoptionMinTokens = 2

// adoptionCandidate is a textual match awaiting the model's confirmation. The
// attribution fields are captured under the lock at MATCH time, so a slow
// verification still records against the turn and audit that earned it.
type adoptionCandidate struct {
	technique shownTechnique
	message   string
	turn      int
	auditID   string
	segment   contracts.Segment
}

// detectAdoptionLocked matches text against the shown technique. Behavioral
// evidence (toolName != "": a tool call whose input overlaps the recipe)
// records directly — the member, or the model on their behalf, demonstrably
// DID the thing. Textual evidence (the member's next prompt) is returned as
// candidates for model verification instead: words about a move are not the
// move, and the funnel's denominator must not be built on vocabulary
// collisions. Caller holds a.mu; caller fires verifyAdoptionsAsync AFTER
// unlocking.
func (a *Agent) detectAdoptionLocked(st *sessionState, text string,
	toolName string) []adoptionCandidate {
	if len(st.lastShown) == 0 {
		return nil
	}
	used := tokens(text)
	if len(used) == 0 {
		return nil
	}
	behavioral := toolName != ""
	// An MCP tool names the org system it belongs to. Calling that system when
	// the recipe names the same one IS the recommended move being made — the
	// highest-precision behavioral signal there is, and inherently one token
	// (server names are org identifiers, not English that collides by chance).
	mcpServer := ""
	if rest, ok := strings.CutPrefix(toolName, "mcp__"); ok {
		if server, _, found := strings.Cut(rest, "__"); found {
			mcpServer = strings.ToLower(server)
		}
	}
	var pending []adoptionCandidate
	for _, sc := range st.lastShown {
		if st.adoptedIDs[sc.capID] {
			continue
		}
		techniqueTokens := tokens(sc.name)
		for t := range tokens(sc.recipe) {
			techniqueTokens[t] = true
		}
		for t := range tokens(strings.ReplaceAll(sc.capID, "-", " ")) {
			techniqueTokens[t] = true
		}
		hits := 0
		for t := range used {
			if techniqueTokens[t] {
				hits++
			}
		}
		serverMatch := mcpServer != "" && techniqueTokens[mcpServer]
		if hits < adoptionMinTokens && !serverMatch {
			continue
		}
		if behavioral {
			a.recordAdoptionLocked(st, sc, st.turn, st.lastAuditID,
				st.feedbackSegment(a.opts.Segment))
			continue
		}
		pending = append(pending, adoptionCandidate{
			technique: sc, message: text, turn: st.turn,
			auditID: st.lastAuditID, segment: st.feedbackSegment(a.opts.Segment),
		})
	}
	return pending
}

// recordAdoptionLocked is the single place an adoption becomes evidence —
// the direct behavioral path and the verified textual path both land here.
// Caller holds a.mu.
func (a *Agent) recordAdoptionLocked(st *sessionState, sc shownTechnique,
	turn int, auditID string, seg contracts.Segment) {
	if st.adoptedIDs[sc.capID] {
		return // e.g. a behavioral match landed while verification was in flight
	}
	st.adoptedIDs[sc.capID] = true
	st.setSuggestionStatusLocked(sc.capID, "adopted")
	a.memory.NoteAdopted(sc.capID, time.Now())
	// Remember the adoption so a surviving one can become an inferred
	// 'helped' (see inferHelpedLocked) carrying this same audit id + segment.
	st.adoptions[sc.capID] = &adoption{
		turn: turn, auditID: auditID, segment: seg, technique: sc,
	}
	a.emitSketchLocked(st, sc, st.key)
	a.postFeedback(audit.ManualFeedbackEvents(
		sc.capID, "adopted", seg, "", 0, auditID, "", "inferred"))
}

// recordHelpedReactionLocked books a member's own verdict that a technique
// helped, from wherever it was read — an ambient message or a typed answer to
// the question form. helped implies adopted, but only once: a second adopted
// against the same suggestion inflates the denominator and halves the
// technique's helped_rate, the number the whole product is judged on. So the
// implied adopted is dropped when adoption was already recorded, and the whole
// event is dropped when a helped already stands. Caller holds a.mu.
func (a *Agent) recordHelpedReactionLocked(st *sessionState, sc shownTechnique,
	reason, confidence string) {
	events := audit.ManualFeedbackEvents(
		sc.capID, "helped", st.feedbackSegment(a.opts.Segment), reason, 0, st.lastAuditID, "", confidence)
	switch {
	case st.helpedIDs[sc.capID]:
		// A helped already counted this technique — an inferred one, usually,
		// since inference normally trails the reaction window. Drop the
		// duplicate rather than double-count; the log is append-only, so the
		// earlier event stands.
		events = nil
	case st.adoptedIDs[sc.capID]:
		kept := events[:0]
		for _, e := range events {
			if e.Stage != "adopted" {
				kept = append(kept, e)
			}
		}
		events = kept
	default:
		a.emitSketchLocked(st, sc, st.key)
	}
	st.adoptedIDs[sc.capID] = true
	st.helpedIDs[sc.capID] = true
	a.memory.NoteAdopted(sc.capID, time.Now())
	delete(st.adoptions, sc.capID) // an explicit verdict supersedes inference
	a.postFeedback(events)
}

// promoteAdoptionsLocked turns the adoptions still standing into helped at the
// given confidence: the member used the move and nothing since has contradicted
// it. eligible decides which adoptions are ripe (nil means all of them). Emits
// ONLY the helped event — the adoption was recorded when it happened, so the
// denominator is never inflated. An adoption the member has already ruled on
// leaves the map without being promoted: an explicit verdict supersedes
// inference. Caller holds a.mu.
func (a *Agent) promoteAdoptionsLocked(st *sessionState, confidence string,
	eligible func(*adoption) bool) {
	for capID, ad := range st.adoptions {
		if st.helpedIDs[capID] || st.reactedIDs[capID] {
			delete(st.adoptions, capID)
			continue
		}
		if eligible != nil && !eligible(ad) {
			continue
		}
		st.helpedIDs[capID] = true
		st.setSuggestionStatusLocked(capID, "helped")
		delete(st.adoptions, capID)
		a.postFeedback([]contracts.FeedbackEventDraft{{
			AuditID: ad.auditID, TechniqueID: capID, Stage: "helped",
			Value: true, Segment: ad.segment, Confidence: confidence,
		}})
	}
}

// adoptionVerifier is implemented by a client that can judge whether a message
// actually acts on a recommendation. The heuristic deliberately does not.
type adoptionVerifier interface {
	VerifyAdoption(message, techniqueName, recipe string) (bool, error)
}

// verifyAdoptionsAsync confirms textual adoption candidates with the model,
// off the latency path — the same posture as the tools_absent enrichment: a
// judgment is asked of a model or not made at all, never guessed. Except here
// there IS a documented fallback: with no model to ask (heuristic mode), the
// tightened lexical match records directly — that is exactly the signal class
// the corpus already carries, ComputeSignalTrust already audits it, and a
// degraded mode that silently stops counting adoptions would look like the
// product going quiet. Must be called WITHOUT a.mu held.
func (a *Agent) verifyAdoptionsAsync(st *sessionState, cands []adoptionCandidate) {
	if len(cands) == 0 {
		return
	}
	recordDirect := func(c adoptionCandidate) {
		a.mu.Lock()
		a.recordAdoptionLocked(st, c.technique, c.turn, c.auditID, c.segment)
		a.mu.Unlock()
	}
	verifier, ok := a.llm.(adoptionVerifier)
	if !ok {
		for _, c := range cands {
			recordDirect(c)
		}
		return
	}
	run := a.opts.RunAsync
	if run == nil {
		run = func(fn func()) { go fn() }
	}
	for _, c := range cands {
		run(func() {
			confirmed, err := verifier.VerifyAdoption(c.message, c.technique.name, c.technique.recipe)
			if errors.Is(err, llm.ErrCannotJudge) {
				recordDirect(c) // heuristic mode: the documented lexical fallback
				return
			}
			a.noteLLM(err)
			if err != nil {
				// A transient API failure drops the candidate: an adoption missed
				// is honest undercounting; one invented on failure is not.
				dbg("adoption verification failed for %s: %v", c.technique.capID, err)
				return
			}
			if !confirmed {
				dbg("adoption REFUTED for %s: vocabulary collision", c.technique.capID)
				return
			}
			dbg("adoption confirmed for %s", c.technique.capID)
			recordDirect(c)
		})
	}
}

// inferHelpedLocked promotes surviving behavioral adoptions to a low-confidence
// 'helped': the member used a suggested move and then kept working for
// HelpedConfirmTurns more turns without dismissing it or reacting negatively.
// Deliberately conservative — inferred confidence, once per technique, only atop an
// observed adoption, and never when an explicit verdict already exists — so the
// helped denominator fills from behaviour without drowning the explicit signal.
// Caller holds a.mu.
func (a *Agent) inferHelpedLocked(st *sessionState) {
	a.promoteAdoptionsLocked(st, "inferred", func(ad *adoption) bool {
		return st.turn-ad.turn >= HelpedConfirmTurns
	})
}

// verifyAdoptionsAtStopLocked closes the outcome loop where no member can:
// in a consumer:agent session, an adoption followed in the same turn by
// passing verification becomes a helped verdict of the `verification`
// confidence class — its calibration is tracked separately on the
// Signal-trust view before anything leans on it. Caller holds a.mu;
// postFeedback is async so emitting under the lock is safe.
func (a *Agent) verifyAdoptionsAtStopLocked(st *sessionState, record contracts.CanonicalRecord) {
	if record.Segment["consumer"] != "agent" || len(st.adoptions) == 0 {
		return
	}
	if !verificationPassed(record.ToolCalls) {
		return
	}
	a.promoteAdoptionsLocked(st, "verification", nil)
}

// detectReactionLocked reads the member's natural message for a verdict on
// the suggestion just shown, within ReactionWindow turns. Caller holds a.mu.
func (a *Agent) detectReactionLocked(st *sessionState, text string) {
	if len(st.lastShown) == 0 || st.turnsSinceShown > ReactionWindow {
		return
	}
	stage, reason, confidence, ok := classifyReaction(text)
	if !ok {
		return
	}
	for _, sc := range st.lastShown {
		if st.reactedIDs[sc.capID] {
			continue
		}
		st.reactedIDs[sc.capID] = true
		st.setSuggestionStatusLocked(sc.capID, stage)
		if stage == "dismissed" {
			a.memory.NoteDismissed(sc.capID, time.Now())
		}
		if stage == "helped" {
			a.recordHelpedReactionLocked(st, sc, reason, confidence)
			continue
		}
		a.postFeedback(audit.ManualFeedbackEvents(
			sc.capID, stage, st.feedbackSegment(a.opts.Segment), reason, 0, st.lastAuditID, "", confidence))
	}
}

// hasTacitQuestion reports whether an AskUserQuestion payload contains a
// question tagged with our header.
func hasTacitQuestion(payload map[string]any) bool {
	_, q := tacitQuestion(payload)
	return q != ""
}

// tacitQuestion extracts (tool_input, question text) for the question
// tagged header=="OpenTacit", if present. Payload shape pinned live:
// tool_input carries questions, answers (question text -> chosen label), and
// annotations.
func tacitQuestion(payload map[string]any) (map[string]any, string) {
	ti, _ := payload["tool_input"].(map[string]any)
	if ti == nil {
		return nil, ""
	}
	questions, _ := ti["questions"].([]any)
	for _, raw := range questions {
		q, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if header, _ := q["header"].(string); header == questionHeader {
			text, _ := q["question"].(string)
			return ti, text
		}
	}
	return nil, ""
}

// captureQuestionAnswerLocked maps the member's one-keystroke answer to the
// agent's question form onto the feedback funnel with explicit confidence —
// the ground truth E1 calibrates against. Caller holds a.mu.
func (a *Agent) captureQuestionAnswerLocked(st *sessionState, payload map[string]any) {
	ti, qText := tacitQuestion(payload)
	if ti == nil || len(st.lastShown) == 0 {
		return
	}
	answers, _ := ti["answers"].(map[string]any)
	label, _ := answers[qText].(string)
	if label == "" {
		return
	}
	sc := st.lastShown[0] // the directive offers exactly the last-shown technique

	switch label {
	case optApply, optShowHow:
		// An explicit request to use the move — adoption, not a "reaction"
		// (a later "that helped" should still record helped; the
		// helped-implies-adopted dedupe handles the overlap).
		if !st.adoptedIDs[sc.capID] {
			st.adoptedIDs[sc.capID] = true
			a.memory.NoteAdopted(sc.capID, time.Now())
			a.emitSketchLocked(st, sc, st.key)
			a.postFeedback(audit.ManualFeedbackEvents(
				sc.capID, "adopted", st.feedbackSegment(a.opts.Segment), "", 0, st.lastAuditID, "", "explicit"))
		}
	case optNotRelevant:
		a.recordQuestionDismissalLocked(st, sc, "not-relevant")
	case optAlreadyUse:
		a.recordQuestionDismissalLocked(st, sc, "already-knew")
	default:
		// The member picked "Other" and typed free text — classify it like an
		// ambient reaction, but with the deliberate-answer context.
		if stage, reason, _, ok := classifyReaction(label); ok && !st.reactedIDs[sc.capID] {
			st.reactedIDs[sc.capID] = true
			if stage == "helped" {
				a.recordHelpedReactionLocked(st, sc, reason, "explicit")
				return
			}
			a.postFeedback(audit.ManualFeedbackEvents(
				sc.capID, stage, st.feedbackSegment(a.opts.Segment), reason, 0, st.lastAuditID, "", "explicit"))
		}
	}
}

func (a *Agent) recordQuestionDismissalLocked(st *sessionState, sc shownTechnique, reason string) {
	if st.reactedIDs[sc.capID] {
		return
	}
	st.reactedIDs[sc.capID] = true
	a.memory.NoteDismissed(sc.capID, time.Now())
	a.postFeedback(audit.ManualFeedbackEvents(
		sc.capID, "dismissed", st.feedbackSegment(a.opts.Segment), reason, 0, st.lastAuditID, "", "explicit"))
}

// postFeedback is best-effort AND off the hook path: callers hold a.mu, so
// the registry POST is dispatched via RunAsync — a slow or hung registry must
// never stall a hook response. Tests inject an inline runner.
func (a *Agent) postFeedback(events []contracts.FeedbackEventDraft) {
	if len(events) == 0 {
		return
	}
	// Tee the funnel OUTCOMES into the member-local usage log (usagelog.go):
	// the same drafts the registry receives, kept locally so the Usage view can
	// show this member their own adoption/helped/dismissed history — which the
	// registry, by "cohorts never identities", structurally cannot. `shown` is
	// logged at its source (commitLocked) where the technique name is in hand;
	// `declined` is off-funnel (never seen by the member) and has no place in a
	// personal usage view.
	for _, ev := range events {
		switch ev.Stage {
		case usageAdopted, usageHelped, usageDismissed:
			a.usage.append(usageEvent{Kind: ev.Stage, Cap: ev.TechniqueID,
				Harness: segHarness(ev.Segment), Model: ev.Segment["model"]})
		}
	}
	a.opts.RunAsync(func() {
		_ = a.feedback(events) // feedback is best-effort
	})
}

// segHarness pulls the surface label off a segment for the usage log's
// per-harness drill-down; "" when the segment carries none.
func segHarness(seg contracts.Segment) string {
	if h := seg["harness"]; h != "" {
		return h
	}
	return seg["surface"]
}

// HandleFeedback is the explicit-feedback route (the manual-fallback skill).
func (a *Agent) HandleFeedback(body map[string]any) (bool, map[string]any) {
	a.Touch()
	capID, _ := body["technique_id"].(string)
	stage, _ := body["stage"].(string)
	valid := map[string]bool{"shown": true, "adopted": true, "helped": true, "dismissed": true}
	if capID == "" || !valid[stage] {
		return false, map[string]any{"error": "technique_id and a valid stage are required"}
	}
	reason, _ := body["reason"].(string)
	events := audit.ManualFeedbackEvents(capID, stage, a.opts.Segment, reason, 0, "", "", "explicit")
	a.postFeedback(events)
	return true, map[string]any{"accepted": len(events)}
}
