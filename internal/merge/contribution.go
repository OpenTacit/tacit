// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package merge folds a personal registry into an organization's: what of a
// technique crosses the border, what is left behind, and what has to be
// remembered so a second run does not send everything twice.
//
// The design is docs/design/registry-first-personal-tier.md (M3). The
// short version is that almost nothing here is a rule — the destination
// enforces the lane, screens the contribution and embeds it on arrival, so the
// sender's whole job is deciding what to leave OUT.
package merge

import (
	"fmt"
	"strings"

	"github.com/opentacit/tacit/pkg/contracts"
)

// Contribution is the body to POST to /v1/contribute for one local technique.
//
// It is built by naming the fields that travel rather than by copying the
// technique and deleting things: a field added to Technique later must not
// start crossing this border because nobody remembered to exclude it.
//
// What is deliberately absent, and why:
//
//   - id — the destination slugs one from the name and makes it unique. Sending
//     ours would ask for a specific id and get "-2" appended when it collides,
//     which reads like a duplicate of somebody else's work.
//   - status, provenance, version — the destination sets all three (draft,
//     contributed, 1). A client that could send them could post itself straight
//     into retrieval.
//   - embedding — computed on arrival, in the destination's model's space. Ours
//     is meaningless there and might be from a different model entirely.
//   - outcomes, events, cohorts, source, origin, channels, created_at — the
//     personal registry's own history and place in the world, none of which is
//     true of the copy that lands in somebody else's review queue.
//
// standing, when non-empty, is appended to the description as the contributor's
// own claim about how the technique has fared. See Standing.
func Contribution(t contracts.Technique, standing string) map[string]any {
	description := strings.TrimSpace(t.Description)
	if standing != "" {
		description = strings.TrimSpace(description + "\n\n" + standing)
	}
	body := map[string]any{
		"name":        t.Name,
		"description": description,
		"recipe":      t.Recipe,
		"scope":       t.Scope,
	}
	// Optional fields go in only when they carry something, so a reviewer's
	// diff is not full of empty keys.
	for k, v := range map[string]string{
		"before_after": t.BeforeAfter,
		"applies_when": t.AppliesWhen,
		"not_when":     t.NotWhen,
		"shipped":      t.Shipped,
	} {
		if strings.TrimSpace(v) != "" {
			body[k] = v
		}
	}
	if len(t.Tags) > 0 {
		body["tags"] = t.Tags
	}
	if len(t.TaskTypes) > 0 {
		body["task_types"] = t.TaskTypes
	}
	if len(t.Triggers) > 0 {
		body["triggers"] = t.Triggers
	}
	if len(t.SupportMatrix) > 0 {
		body["support_matrix"] = t.SupportMatrix
	}
	return body
}

// StandingNote is the sentence that carries a technique's personal evidence across
// a border the schema has no field for.
//
// The numbers must not enter the organization's outcome storage: its promotion
// floors and its public channel are Wilson lower bounds over cohorts, and one
// member's measurements would be a sample of one inflating them. But a reviewer
// deciding whether to promote a stranger's technique is better off knowing it
// has been used thirty times than not, so the count travels as prose, framed as
// the contributor's claim and disclaimed in the same breath.
//
// The precedent is parseTechniqueContribution's own Source field, which packs
// the contributor's cohort into a string because the schema has nowhere else to
// put it. This is the same move, one field along.
//
// An unused technique gets no sentence: "0 shown, 0 adopted" is not evidence of
// anything and reads like a warning the contributor did not intend.
func StandingNote(o contracts.Outcome, since string) string {
	if o.Shown == 0 {
		return ""
	}
	kept := "Kept on the contributor's personal registry"
	if since != "" {
		kept += " since " + since
	}
	return fmt.Sprintf("%s. They report %d shown, %d adopted and %d helped there. "+
		"Not measured by this organization.", kept, o.Shown, o.Adopted, o.Helped)
}
