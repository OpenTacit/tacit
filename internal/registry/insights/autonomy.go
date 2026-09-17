// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package insights

// Evidence-gated autonomy analytics (docs/delivery/agent-delivery-plan.md
// Phases C–D): which techniques clear the operator's bar for silent agent
// application, and how silently-applied deliveries perform against suggested
// ones — the comparison that proves or falsifies the graduation rule.

import (
	"sort"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
)

// AutonomyTechnique is one stable technique's standing against the gate, measured over
// the technique's whole history (the gate is about accumulated evidence, not the
// page's viewing window).
type AutonomyTechnique struct {
	Technique  models.Technique
	HelpedRate float64 // all-time helped/adopted
	N          int     // all-time adoptions (the sample behind the rate)
	Eligible   bool
}

// Autonomy is the governance view: eligibility standings plus the window's
// funnel split by delivery mode. Applied vs Suggested are joined through the
// shown event's audit id, so an adoption/helped verdict lands in the funnel of
// the delivery that earned it.
type Autonomy struct {
	Techniques []AutonomyTechnique // stable measured techniques, eligible first
	Eligible   int
	Applied    Funnel // deliveries marked delivery=applied (silent application)
	Suggested  Funnel // visible deliveries (delivery=suggested or historical '')
}

// ComputeAutonomy evaluates the gate over all history and splits the window's
// funnel by delivery mode.
func ComputeAutonomy(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window,
	minHelpedRate float64, minN int) Autonomy {

	type tally struct{ adopted, helped int }
	allTime := map[string]*tally{}
	deliveryByAudit := map[string]string{}
	for _, e := range events {
		switch e.Stage {
		case "adopted", "helped":
			t := allTime[e.TechniqueID]
			if t == nil {
				t = &tally{}
				allTime[e.TechniqueID] = t
			}
			if e.Stage == "adopted" {
				t.adopted++
			} else {
				t.helped++
			}
		case "shown":
			if e.AuditID != "" {
				mode := e.Delivery
				if mode == "" {
					mode = "suggested"
				}
				deliveryByAudit[e.AuditID] = mode
			}
		}
	}

	var a Autonomy
	for _, c := range techniques {
		if c.Status != "stable" {
			continue
		}
		t := allTime[c.ID]
		if t == nil || t.adopted == 0 {
			continue // unmeasured techniques have no standing to show
		}
		rate := float64(t.helped) / float64(t.adopted)
		ac := AutonomyTechnique{Technique: c, HelpedRate: rate, N: t.adopted,
			Eligible: rate >= minHelpedRate && t.adopted >= minN}
		if ac.Eligible {
			a.Eligible++
		}
		a.Techniques = append(a.Techniques, ac)
	}
	sort.Slice(a.Techniques, func(i, j int) bool {
		if a.Techniques[i].Eligible != a.Techniques[j].Eligible {
			return a.Techniques[i].Eligible
		}
		if a.Techniques[i].N != a.Techniques[j].N {
			return a.Techniques[i].N > a.Techniques[j].N
		}
		return a.Techniques[i].Technique.ID < a.Techniques[j].Technique.ID
	})

	for _, e := range events {
		t, ok := ParseTime(e.CreatedAt)
		if !ok || !inWindow(t, w.Start, now) {
			continue
		}
		mode := deliveryByAudit[e.AuditID]
		if e.Stage == "shown" {
			mode = e.Delivery
			if mode == "" {
				mode = "suggested"
			}
		}
		switch mode {
		case "applied":
			a.Applied.add(e)
		case "suggested":
			a.Suggested.add(e)
		}
	}
	return a
}
