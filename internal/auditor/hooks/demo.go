// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The demonstration trigger: a prompt opening "As a test" is answered with a
// suggestion invented on the spot from the rest of that prompt, delivered
// through the real channel — the form on Claude Code, the ◆ block everywhere
// else.
//
// It exists because showing OpenTacit to someone is otherwise a matter of
// luck. A live suggestion needs a technique that happens to fit the work, a
// fit-check that happens to accept it, and an attention budget that happens to
// have room — measured on a real member log, roughly one arrival every three
// days. That is the correct behaviour for coaching and a hopeless basis for a
// demonstration, where the audience is watching now.
//
// The technique is FABRICATED, and everything here follows from that:
//
//   - Nothing is recorded. No shown, no adopted, no audit fact, no usage-log
//     entry. Inventing a technique and then measuring it would corrupt the one
//     asset the product exists to keep honest.
//   - Nothing is retrieved. The registry is never called, so a demonstration
//     works against an empty registry, a broken one, or none at all.
//   - The technique says it is a demonstration, in the delivery itself. This is the
//     rule that costs something — a demo is more impressive without the label —
//     and it is not optional. OpenTacit's whole claim is that its numbers are
//     measured; a demonstration quietly showing invented ones would be selling
//     the opposite of the product.
//   - The id is namespaced `demo/`, so any event that ever escapes this path is
//     identifiable at a glance in the event log.
//
// The trigger is exempt from the attention budget (throttle.go) and from the
// once-per-session technique dedupe: "every time" is the point. It is member-typed
// and therefore solicited, which is the same reason @tacit is exempt.

package hooks

import (
	"encoding/json"
	"strings"

	"github.com/opentacit/tacit/internal/auditor/contracts"

	"github.com/opentacit/tacit/internal/slug"
)

// demoPhrase opens a prompt that asks for a demonstration. Matched at the START
// of the prompt only, and case-insensitively: mid-sentence it is ordinary
// English ("run it as a test first"), and only the opening position is
// unambiguous enough to hang a behaviour on.
const demoPhrase = "as a test"

// demoIDPrefix namespaces a fabricated technique. Nothing from this path should ever
// reach the registry; if something does, this is what makes it obvious.
const demoIDPrefix = "demo/"

// demoLabel rides the delivery so nobody reads an invented figure as measured.
const demoLabel = "demonstration technique — invented for this request, not from your registry, nothing recorded"

// DemoTechniqueMaker is the optional judgment a real model contributes: invent a
// plausible internal technique answering the request. Asserted on the agent's
// LLM the same way UpgradeReporter is, so the heuristic path simply does not
// implement it and falls back to demoFallback.
type DemoTechniqueMaker interface {
	DemoTechnique(request string) (name, why, recipe, evidence string, err error)
}

// parseDemoRequest splits a demonstration prompt. request is what follows the
// phrase — the thing the invented technique should answer.
func parseDemoRequest(prompt string) (request string, ok bool) {
	trimmed := strings.TrimSpace(prompt)
	if len(trimmed) < len(demoPhrase) ||
		!strings.EqualFold(trimmed[:len(demoPhrase)], demoPhrase) {
		return "", false
	}
	request = strings.TrimSpace(strings.TrimLeft(trimmed[len(demoPhrase):], " ,:;.-—"))
	if request == "" {
		return "", false
	}
	return request, true
}

// demoTechnique builds the fabricated candidate. A real model writes it; without
// one, demoFallback still produces a usable technique, because a demonstration that
// only works with an API key configured is not a demonstration you can give.
func (a *Agent) demoTechnique(request string) (contracts.EvidenceCandidate, string) {
	if maker, ok := a.llm.(DemoTechniqueMaker); ok {
		if name, why, recipe, evidence, err := maker.DemoTechnique(request); err == nil && name != "" {
			return contracts.EvidenceCandidate{
				TechniqueID: demoIDPrefix + slugify(name),
				Name:        name,
				Scope:       "org",
				Recipe:      recipe,
				Outcomes:    demoOutcomes(evidence),
			}, why
		}
	}
	return demoFallback(request), ""
}

// demoOutcomes turns the model's evidence phrase into the outcomes map the
// evidence line renders from. Deliberately ABOVE EvidenceMinN: a demonstration
// should show what a measured technique looks like, and a technique under the floor
// renders "too few outcomes to rate yet", which demonstrates the floor rather
// than the product. The numbers are invented, which is what demoLabel says.
func demoOutcomes(evidence string) map[string]any {
	helped, adopted, n := 0.86, 0.62, 40.0
	var parsed struct {
		Helped  float64 `json:"helped_rate"`
		Adopted float64 `json:"adoption_rate"`
		N       float64 `json:"sample_size"`
	}
	if json.Unmarshal([]byte(evidence), &parsed) == nil {
		if parsed.Helped > 0 && parsed.Helped <= 1 {
			helped = parsed.Helped
		}
		if parsed.Adopted > 0 && parsed.Adopted <= 1 {
			adopted = parsed.Adopted
		}
		if parsed.N >= float64(contracts.EvidenceMinN) {
			n = parsed.N
		}
	}
	return map[string]any{"helped_rate": helped, "adoption_rate": adopted, "sample_size": n}
}

// demoFallback is the no-model fallback: it reflects the request back as an
// org-shaped move. Thin next to what a real model writes, and it keeps the
// demonstration working on a machine with no key.
func demoFallback(request string) contracts.EvidenceCandidate {
	subject := strings.TrimSuffix(strings.TrimSpace(request), ".")
	name := "Go to the internal source for " + subject
	return contracts.EvidenceCandidate{
		TechniqueID: demoIDPrefix + slugify(name),
		Name:        name,
		Scope:       "org",
		Recipe: "Ask the internal system that owns this data directly, rather than " +
			"reconstructing it:\n  1. Find the owning system in the internal catalogue.\n" +
			"  2. Query it for \"" + subject + "\".\n  3. Link the source alongside the answer.",
		Outcomes: demoOutcomes(""),
	}
}

// slugify makes a stable id fragment out of a name.
func slugify(name string) string { return slug.Make(name) }

// deliverDemo answers a demonstration prompt. It runs BEFORE every other
// UserPromptSubmit path and returns the same envelopes a real suggestion uses,
// so what the audience sees is the product's actual delivery and not a mock-up
// of it. Caller holds no lock.
func (a *Agent) deliverDemo(request, harness string) HookResponse {
	technique, why := a.demoTechnique(request)
	if deliverless(harness) {
		return HookResponse{}
	}
	if formCapable(harness) {
		return DeliverSilent(formatForm(why, technique) +
			"\nTell the member, in one line beneath the form, that this is a " + demoLabel + ".\n")
	}
	// Blocks reach the member as visible text; DeliverNotice keeps it out of the
	// model's context, where an invented technique has no business.
	return DeliverNotice(a.format(why, technique, false) + "\n" + tacitSpine + "(" + demoLabel + ")")
}
