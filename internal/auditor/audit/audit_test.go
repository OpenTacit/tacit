// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/llm"
)

func provider(cands ...contracts.EvidenceCandidate) EvidenceProvider {
	return func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands}, nil
	}
}

func TestRunAuditOfflineGlue(t *testing.T) {
	cand := contracts.EvidenceCandidate{TechniqueID: "cap-1", Name: "Move", Recipe: "do it"}
	result, err := Run("USER: hello", llm.Heuristic{}, provider(cand), contracts.Segment{"team": "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.AuditID, "aud_") {
		t.Fatalf("audit id = %s", result.AuditID)
	}
	if len(result.ShownTechniqueIDs) != 1 || result.ShownTechniqueIDs[0] != "cap-1" {
		t.Fatalf("shown = %v", result.ShownTechniqueIDs)
	}
	if result.Characterization.Segment["team"] != "t" {
		t.Fatal("segment not merged into characterization")
	}
	if result.AuditText == "" {
		t.Fatal("no audit text")
	}
}

func TestRunAuditWithPrecomputedCharacterization(t *testing.T) {
	pre := contracts.Characterization{SummaryText: "structured", Harness: "omnigent"}
	result, err := Run("ignored", llm.Heuristic{}, provider(), nil, &pre)
	if err != nil {
		t.Fatal(err)
	}
	if result.Characterization.SummaryText != "structured" {
		t.Fatal("pre-computed characterization not used")
	}
}

func TestHelpedImpliesAdopted(t *testing.T) {
	events := ManualFeedbackEvents("cap", "helped", nil, "", 0, "", "", "")
	if len(events) != 2 || events[0].Stage != "adopted" || events[1].Stage != "helped" {
		t.Fatalf("helped events = %+v", events)
	}
	if events[1].Value != true {
		t.Fatal("helped value not true")
	}
	if events[0].AuditID == "" || events[0].AuditID != events[1].AuditID {
		t.Fatal("audit ids not shared")
	}
	if events[0].Confidence != "explicit" {
		t.Fatalf("default confidence = %s", events[0].Confidence)
	}
}

func TestDismissedCarriesReason(t *testing.T) {
	events := ManualFeedbackEvents("cap", "dismissed", nil, "not-relevant", 0, "aud_x", "", "explicit")
	if len(events) != 1 || events[0].Value != "not-relevant" || events[0].AuditID != "aud_x" {
		t.Fatalf("dismissed = %+v", events)
	}
}

func TestShownEventsRankAndConfidence(t *testing.T) {
	result := Result{
		AuditID:          "aud_1",
		Characterization: contracts.Characterization{TaskType: "tt", Segment: contracts.Segment{"team": "t"}},
		Evidence: contracts.EvidenceBlock{Candidates: []contracts.EvidenceCandidate{
			{TechniqueID: "a", Similarity: 0.42},
			{TechniqueID: "b", Similarity: 0.31},
		}},
		ShownTechniqueIDs: []string{"a", "b"},
	}
	events := ShownEvents(result, nil, contracts.SourceAmbient)
	if len(events) != 2 || events[0].RankShown != 1 || events[1].RankShown != 2 {
		t.Fatalf("ranks: %+v", events)
	}
	if events[0].Confidence != "inferred" || events[0].TaskType != "tt" {
		t.Fatalf("event: %+v", events[0])
	}
	if events[0].Segment["team"] != "t" {
		t.Fatal("segment fallback to characterization failed")
	}
	// The similarity that earned each technique its slot rides onto its event,
	// matched by technique id rather than by position — it is the only record
	// tying a fit-check verdict back to the retrieval score behind it.
	if events[0].Source != contracts.SourceAmbient || events[0].Similarity != 0.42 {
		t.Fatalf("source/similarity: %+v", events[0])
	}
	if events[1].Similarity != 0.31 {
		t.Fatalf("second candidate similarity: %+v", events[1])
	}
}

func TestShownEventsKeepFitCheckRank(t *testing.T) {
	result := Result{
		AuditID:             "aud_rank",
		Characterization:    contracts.Characterization{TaskType: "tt"},
		Evidence:            contracts.EvidenceBlock{Candidates: []contracts.EvidenceCandidate{{TechniqueID: "winner"}}},
		ShownTechniqueIDs:   []string{"winner"},
		ShownTechniqueRanks: []int{3},
	}
	events := ShownEvents(result, nil, contracts.SourceAmbient)
	if len(events) != 1 || events[0].RankShown != 3 {
		t.Fatalf("winner's fit-check rank was not kept: %+v", events)
	}
}

// A shown technique the member pulled up must not be attributed to the ambient
// burst, or it lands in a decline-rate denominator no decline could pair with.
func TestShownEventsCarryPullSource(t *testing.T) {
	result := Result{
		AuditID:           "aud_2",
		Characterization:  contracts.Characterization{TaskType: "tt"},
		Evidence:          contracts.EvidenceBlock{Candidates: []contracts.EvidenceCandidate{{TechniqueID: "a"}}},
		ShownTechniqueIDs: []string{"a"},
	}
	events := ShownEvents(result, nil, contracts.SourcePull)
	if len(events) != 1 || events[0].Source != contracts.SourcePull {
		t.Fatalf("source: %+v", events)
	}
}

// DeclinedEvents is the other half of the pair: every rejection carries the
// score that proposed it, which is what a similarity floor is calibrated on.
func TestDeclinedEventsCarrySourceAndSimilarity(t *testing.T) {
	cands := []contracts.EvidenceCandidate{
		{TechniqueID: "kept", Similarity: 0.5},
		{TechniqueID: "cut", Similarity: 0.11},
	}
	events := DeclinedEvents("aud_3", cands, []bool{false, true}, nil, "tt")
	if len(events) != 1 {
		t.Fatalf("want only the declined candidate: %+v", events)
	}
	if events[0].TechniqueID != "cut" || events[0].Similarity != 0.11 {
		t.Fatalf("similarity: %+v", events[0])
	}
	if events[0].Source != contracts.SourceAmbient {
		t.Fatalf("declines only come from the ambient burst: %+v", events[0])
	}
	if events[0].RankShown != 2 {
		t.Fatalf("rank is the burst position: %+v", events[0])
	}
}

func TestShadowEventsFitAndDecline(t *testing.T) {
	cands := []contracts.EvidenceCandidate{
		{TechniqueID: "fit"}, {TechniqueID: "nonfit"}, {TechniqueID: "errored"},
	}
	// fit judged a match, nonfit judged a non-match, errored never got a verdict.
	fit := []bool{true, false, false}
	judged := []bool{true, true, false}
	events := ShadowEvents("aud_1", cands, fit, judged, contracts.Segment{"team": "t"}, "tt")

	// The synth-errored candidate emits nothing — a transient failure is not a
	// verdict.
	if len(events) != 2 {
		t.Fatalf("want 2 events (errored one dropped), got %d: %+v", len(events), events)
	}
	if events[0].TechniqueID != "fit" || events[0].Stage != "shadow_shown" {
		t.Fatalf("fit -> shadow_shown expected, got %+v", events[0])
	}
	if events[1].TechniqueID != "nonfit" || events[1].Stage != "shadow_declined" {
		t.Fatalf("nonfit -> shadow_declined expected, got %+v", events[1])
	}
	// Inferred (the synthesizer's verdict) and carries rank + task type.
	if events[0].Confidence != "inferred" || events[0].RankShown != 1 || events[0].TaskType != "tt" {
		t.Fatalf("metadata: %+v", events[0])
	}
	if events[0].Segment["team"] != "t" {
		t.Fatal("segment not carried")
	}
}
