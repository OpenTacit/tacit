// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package contracts holds the audit layer's wire types.
//
// The shapes shared with the rest of the system — Segment, the canonical
// record, Characterization, feedback drafts — are aliases of the public
// definitions in pkg/contracts (one definition, one standard). The evidence
// types stay LOCAL and loosely typed on purpose: the audit layer consumes
// evidence from the registry over HTTP and treats outcome/freshness detail
// as opaque maps rather than coupling to the producer's exact shape.
package contracts

import (
	"fmt"

	"github.com/opentacit/tacit/pkg/contracts"
)

// Aliases to the public wire types (identical types, one definition).
type (
	Segment             = contracts.Segment
	Message             = contracts.Message
	ToolCall            = contracts.ToolCall
	AuditFactEnrichment = contracts.AuditFactEnrichment
	CanonicalRecord     = contracts.CanonicalRecord
	Characterization    = contracts.Characterization
	FeedbackEventDraft  = contracts.FeedbackEventDraft
	Sketch              = contracts.Sketch
)

// Feedback event sources, re-exported so emitters in this layer need only the
// one contracts import (see contracts.FeedbackEvent.Source).
const (
	SourceAmbient = contracts.SourceAmbient
	SourcePull    = contracts.SourcePull
)

// EvidenceCandidate is one technique as returned by the registry's /v1/evidence,
// consumed loosely (outcomes/freshness as maps).
type EvidenceCandidate struct {
	TechniqueID string         `json:"technique_id"`
	Name        string         `json:"name"`
	Scope       string         `json:"scope,omitempty"`
	Recipe      string         `json:"recipe,omitempty"`
	AppliesWhen string         `json:"applies_when,omitempty"`
	NotWhen     string         `json:"not_when,omitempty"`
	Support     string         `json:"support,omitempty"`
	Outcomes    map[string]any `json:"outcomes,omitempty"`
	Freshness   map[string]any `json:"freshness,omitempty"`
	// AutonomyEligible: the registry's evidence-gate verdict — this technique may
	// be applied silently in a consumer:agent session
	// (docs/delivery/agent-delivery-plan.md Phase C).
	AutonomyEligible bool `json:"autonomy_eligible,omitempty"`
	// Similarity is the retrieval score that put this technique in the block. The
	// audit layer does not rank on it — that already happened in the registry —
	// it carries the number back onto the shown/declined event it emits, which
	// is the only way the fit-check's verdicts and the retrieval score ever
	// meet in one record.
	Similarity float64 `json:"similarity,omitempty"`
}

// EvidenceBlock is the COLLECTIVE EVIDENCE contract the audit prompt consumes.
type EvidenceBlock struct {
	Candidates []EvidenceCandidate `json:"candidates"`
	Cohort     map[string]any      `json:"cohort,omitempty"`
	Meta       map[string]any      `json:"meta,omitempty"`
	// ShadowCandidates are techniques under evaluation (status=shadow) the registry
	// returned for judge-only fit-checking — never surfaced to the member
	// (docs/learning/validation-without-review.md). The agent may fit-check them
	// on a turn it is already fit-checking and emit shadow_shown/shadow_declined
	// telemetry; they must never be presented.
	ShadowCandidates []EvidenceCandidate `json:"shadow_candidates,omitempty"`
}

// Thin reports the evidence block's cold-start flag.
func (e EvidenceBlock) Thin() bool {
	t, _ := e.Meta["thin"].(bool)
	return t
}

// EvidenceMinN is the sample size a technique needs before this layer will quote a
// RATE for it to a member. It matches federation's AttestationMinN, the floor
// already pre-registered for the same question one boundary out ("may we state
// this rate to a reader who cannot see the events behind it") — reusing it
// rather than inventing a display threshold, per the standing rule against new
// evidence floors.
//
// Below it the honest render is a count, or nothing. `sample_size` reaching a
// member as 0 is not "helped 0%": it is a technique with no measured outcome at all,
// because the count is a decay-WEIGHTED adopted total truncated to an int
// (registry/feedback.go), so any technique with less than one full-weight adoption
// arrives at zero while its rate fields are still populated.
const EvidenceMinN = 5

// EvidenceLine renders a candidate's measured outcomes as the one line the
// whole product's claim rests on — "measured by colleagues: helped 94% ·
// adopted 70% · n=120 · team:revops" — or "" when there is nothing honest to
// say. It is defined ONCE here because both surfaces that show it (the ambient
// hook block and the MCP ask path) must make the same claim from the same
// numbers; they used to render it separately and disagreed.
//
// Two rules, both from the house style — every rate shows its n, and a cold
// start says so rather than inventing a zero:
//
//   - n below EvidenceMinN: no percentage. Report the count if there is one
//     ("tried by 3 colleagues, too few outcomes to rate yet"), else "".
//   - n at or above it: the rate, always with its n.
func EvidenceLine(outcomes map[string]any) string {
	n, hasN := outcomes["sample_size"].(float64)
	hr, hasRate := outcomes["helped_rate"].(float64)
	if !hasRate || !hasN || n <= 0 {
		return ""
	}
	if n < EvidenceMinN {
		colleagues := "colleagues"
		if n < 2 {
			colleagues = "colleague"
		}
		return fmt.Sprintf("tried by %.0f %s — too few outcomes to rate yet", n, colleagues)
	}
	line := fmt.Sprintf("measured by colleagues: helped %.0f%%", hr*100)
	if ar, ok := outcomes["adoption_rate"].(float64); ok {
		line += fmt.Sprintf(" · adopted %.0f%%", ar*100)
	}
	line += fmt.Sprintf(" · n=%.0f", n)
	if seg, ok := outcomes["segment"].(string); ok && seg != "" && seg != "__overall__" {
		line += " · " + seg
	}
	return line
}
