// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package audit is the orchestrator: transcript -> characterize -> evidence
// -> synthesize -> feedback.
//
// The evidence provider is a function value (characterization -> evidence
// block). In normal use it's client.Registry.GetEvidence (HTTP); tests inject
// a function that calls retrieval directly — dependency injection keeps the
// orchestrator testable offline and independent of transport.
package audit

import (
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/llm"
)

// EvidenceProvider is stage-2 retrieval as a function value.
type EvidenceProvider func(contracts.Characterization) (contracts.EvidenceBlock, error)

// Result is one completed audit.
type Result struct {
	AuditID           string
	Characterization  contracts.Characterization
	Evidence          contracts.EvidenceBlock
	AuditText         string
	ShownTechniqueIDs []string
	// ShownTechniqueRanks is parallel to ShownTechniqueIDs and keeps each
	// technique's 1-based position in the fit-check burst. Full audits show the
	// whole ranked list, while the hook agent keeps only the winner; without
	// this field a rank-3 winner is rewritten as rank 1 at delivery.
	ShownTechniqueRanks []int
}

// NewAuditID mints an audit id.
func NewAuditID() string { return newID("aud_") }

// Run executes the audit pipeline. A pre-computed characterization (from the
// structured capture layer, where the managed-runtime record is richer than
// the LLM could infer from text) skips the LLM characterize step.
func Run(transcript string, model llm.Client, provider EvidenceProvider,
	segment contracts.Segment, characterization *contracts.Characterization) (Result, error) {

	auditID := NewAuditID()
	var ch contracts.Characterization
	if characterization != nil {
		ch = *characterization
	} else {
		var err error
		if ch, err = model.Characterize(transcript, segment); err != nil {
			return Result{}, err
		}
	}
	if len(segment) > 0 {
		if ch.Segment == nil {
			ch.Segment = contracts.Segment{}
		}
		for k, v := range segment {
			ch.Segment[k] = v
		}
	}
	// Carry the audit id into retrieval so the registry can key what the work
	// WAS to how it turned OUT — the feedback events below already carry it.
	ch.AuditID = auditID
	evidence, err := provider(ch)
	if err != nil {
		return Result{}, err
	}
	text, err := model.Synthesize(transcript, evidence, false)
	if err != nil {
		return Result{}, err
	}
	shown := make([]string, 0, len(evidence.Candidates))
	ranks := make([]int, 0, len(evidence.Candidates))
	for i, c := range evidence.Candidates {
		shown = append(shown, c.TechniqueID)
		ranks = append(ranks, i+1)
	}
	return Result{
		AuditID:             auditID,
		Characterization:    ch,
		Evidence:            evidence,
		AuditText:           text,
		ShownTechniqueIDs:   shown,
		ShownTechniqueRanks: ranks,
	}, nil
}

// ManualFeedbackEvents builds feedback event(s) for an explicit report.
//
// `helped` emits the implied `adopted` event too — you can't be helped without
// adopting, and without an adopted denominator helped_rate stays undefined.
func ManualFeedbackEvents(techniqueID, stage string, segment contracts.Segment,
	reason string, rank int, auditID, taskType, confidence string) []contracts.FeedbackEventDraft {

	if auditID == "" {
		auditID = NewAuditID()
	}
	if confidence == "" {
		confidence = "explicit"
	}
	base := contracts.FeedbackEventDraft{
		AuditID: auditID, TechniqueID: techniqueID, Segment: segment,
		Confidence: confidence, RankShown: rank, TaskType: taskType,
	}
	switch stage {
	case "helped":
		adopted, helped := base, base
		adopted.Stage = "adopted"
		helped.Stage = "helped"
		helped.Value = true
		return []contracts.FeedbackEventDraft{adopted, helped}
	case "dismissed":
		e := base
		e.Stage = "dismissed"
		if reason != "" {
			e.Value = reason
		}
		return []contracts.FeedbackEventDraft{e}
	default:
		e := base
		e.Stage = stage
		return []contracts.FeedbackEventDraft{e}
	}
}

// DeclinedEvents builds 'declined' events for candidates the fit-check LLM
// judged a non-fit for this context — retrieved and considered, but never
// shown. Off the delivery funnel (see contracts.Stages): the signal is
// retrieval quality, so confidence is 'inferred' (the synthesizer's verdict,
// not the member's). candidates is the rank-ordered fit-check burst and
// declined is a parallel mask (declined[i] == true → the LLM rejected
// candidates[i]); RankShown is the 1-based position in that order, which is
// what a decline rate is measured against.
func DeclinedEvents(auditID string, candidates []contracts.EvidenceCandidate,
	declined []bool, segment contracts.Segment, taskType string) []contracts.FeedbackEventDraft {

	out := make([]contracts.FeedbackEventDraft, 0, len(candidates))
	for i, c := range candidates {
		if i >= len(declined) || !declined[i] {
			continue
		}
		out = append(out, contracts.FeedbackEventDraft{
			AuditID: auditID, TechniqueID: c.TechniqueID, Stage: "declined",
			Segment: segment, RankShown: i + 1,
			TaskType: taskType, Confidence: "inferred",
			Source: contracts.SourceAmbient, Similarity: c.Similarity,
		})
	}
	return out
}

// ShadowEvents builds shadow relevance telemetry for shadow candidates the
// fit-check judged this turn (D1 piggyback, docs/learning/validation-without-review.md).
// A judged candidate becomes shadow_shown when the synthesizer accepted it (a
// fit that WOULD have surfaced had the technique been serving) or shadow_declined
// when it rejected it. fit and judged are parallel masks over candidates:
// judged[i]==false is a synth error (no verdict), emitted as nothing so a
// transient failure is never miscounted. These stages stay OFF every funnel —
// the registry rollup does not aggregate them — because a shadow technique is never
// shown, so it can have no adopted/helped. Confidence is 'inferred': it is the
// synthesizer's verdict, not the member's.
func ShadowEvents(auditID string, candidates []contracts.EvidenceCandidate,
	fit, judged []bool, segment contracts.Segment, taskType string) []contracts.FeedbackEventDraft {

	out := make([]contracts.FeedbackEventDraft, 0, len(candidates))
	for i, c := range candidates {
		if i >= len(judged) || !judged[i] {
			continue
		}
		stage := "shadow_declined"
		if i < len(fit) && fit[i] {
			stage = "shadow_shown"
		}
		out = append(out, contracts.FeedbackEventDraft{
			AuditID: auditID, TechniqueID: c.TechniqueID, Stage: stage,
			Segment: segment, RankShown: i + 1,
			TaskType: taskType, Confidence: "inferred",
			Source: contracts.SourceAmbient, Similarity: c.Similarity,
		})
	}
	return out
}

// ShownEvents builds stage-4 'shown' events for the candidates presented.
// source is contracts.SourceAmbient only when these techniques came out of a
// fit-check burst that could equally have declined them; every other caller
// passes SourcePull, which keeps them out of the decline-rate denominator.
func ShownEvents(result Result, segment contracts.Segment, source string) []contracts.FeedbackEventDraft {
	seg := segment
	if len(seg) == 0 {
		seg = result.Characterization.Segment
	}
	// The similarity that earned each technique its place is on the evidence block,
	// not on the id list, so recover it by id.
	sim := make(map[string]float64, len(result.Evidence.Candidates))
	for _, c := range result.Evidence.Candidates {
		sim[c.TechniqueID] = c.Similarity
	}
	out := make([]contracts.FeedbackEventDraft, 0, len(result.ShownTechniqueIDs))
	for i, capID := range result.ShownTechniqueIDs {
		rank := i + 1
		if i < len(result.ShownTechniqueRanks) && result.ShownTechniqueRanks[i] > 0 {
			rank = result.ShownTechniqueRanks[i]
		}
		out = append(out, contracts.FeedbackEventDraft{
			AuditID: result.AuditID, TechniqueID: capID, Stage: "shown",
			Segment: seg, RankShown: rank,
			TaskType: result.Characterization.TaskType, Confidence: "inferred",
			Source: source, Similarity: sim[capID],
		})
	}
	return out
}
