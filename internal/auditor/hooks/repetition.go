// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Repetition mining: the axis a single member is allowed to have.
//
// The registry's discovery pipeline gates a proposal on ≥K distinct origins
// across ≥M distinct cohorts — the k-threshold, which is what makes a
// discovered technique anonymous (docs/mining/mining-design.md). The threshold
// is right, and it is also unclearable by one person: a member running their
// own registry can never have a technique discovered from their own work, no
// matter how consistently they do it.
//
// Swap the axis. K repetitions by one member over time, counted by the local
// ledger (corrections.go), is the same evidence read down a different
// dimension, and the reason for the cohort threshold does not apply when the
// only origin is the person who will read the result
// (docs/design/single-user-value.md).
//
// What arrives is a DRAFT, in the same review lane every other proposal uses.
// Nothing here promotes anything, and nothing here writes a serving technique.
package hooks

import (
	"errors"
	"strings"

	"github.com/opentacit/tacit/internal/auditor/llm"
	"github.com/opentacit/tacit/pkg/scrub"
)

// repeatedCorrectionInferrer distils a standing preference from one saying of a
// repeated correction. Like every other distillation, only a real model
// implements it — a heuristic guess at what somebody meant is worse than
// nothing here, because it becomes a draft with their name on it.
type repeatedCorrectionInferrer interface {
	InferRepeatedCorrection(message string) (trigger, move string, err error)
}

// correctionMaxChars bounds what one correction contributes to a distillation.
// A correction is a sentence or two; anything longer is a fresh instruction
// that happened to open with "no".
const correctionMaxChars = 600

// raiseRepeatedCorrectionAsync files a draft when a correction crosses the
// repetition threshold for the first time.
//
// The LIVE message is what gets distilled. The ledger keeps only hashes, so the
// past occurrences prove the habit and this one supplies the words — which is
// the whole reason the ledger is allowed to hold as little as it does. Runs off
// the response path, best-effort, silent on failure.
func (a *Agent) raiseRepeatedCorrectionAsync(hash string, count int, message, harness string) {
	if hash == "" || count < CorrectionThreshold || a.contribute == nil {
		return
	}
	if a.corrections.alreadyRaised(hash) {
		return
	}
	inferrer, ok := a.llm.(repeatedCorrectionInferrer)
	if !ok {
		return
	}
	text := strings.TrimSpace(message)
	if r := []rune(text); len(r) > correctionMaxChars {
		text = string(r[:correctionMaxChars])
	}
	// Scrubbed before it reaches the model, not after: this is the one path
	// where a member's raw words leave the process, and a secret pasted into a
	// correction must not ride out with them.
	text, _ = scrub.Redact(text)
	if strings.TrimSpace(text) == "" {
		return
	}
	// Claim the hash BEFORE the model call. A slow or failing distillation must
	// not leave the threshold armed, or the next saying tries again and the
	// member gets the same draft twice.
	a.corrections.markRaised(hash)

	run := a.opts.RunAsync
	if run == nil {
		run = func(fn func()) { go fn() }
	}
	run(func() {
		trigger, move, err := inferrer.InferRepeatedCorrection(text)
		if err != nil {
			if !errors.Is(err, llm.ErrCannotJudge) {
				a.noteLLM(err)
			}
			return
		}
		if move == "" {
			return // nothing a colleague could reuse — the honest, common answer
		}
		a.HandleContribution(map[string]any{
			"name":         firstLine(move),
			"description":  describeRepetition(trigger, count),
			"recipe":       move,
			"applies_when": strings.TrimSpace(trigger),
			"source":       "observed: repeated correction",
			"harness":      harness,
		})
	})
}

// describeRepetition states the evidence as what it is — one person's own
// record, not a measurement of anything wider — so a reviewer weighs it as
// that. The count is the whole claim; there is no rate here and none is
// implied.
func describeRepetition(trigger string, count int) string {
	desc := "You have made this correction " + plural(count, "time") +
		" across separate sessions on this machine, so it reads as a standing " +
		"preference rather than a one-off. Measured by you, on your own work, and by nobody else."
	if t := strings.TrimSpace(trigger); t != "" {
		desc = t + ". " + desc
	}
	return desc
}

func plural(n int, unit string) string {
	s := unit
	if n != 1 {
		s += "s"
	}
	return itoa(n) + " " + s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// firstLine is the technique's name: the first sentence of the move, capped.
// A name is a caption, and the recipe below it carries the rest.
func firstLine(move string) string {
	name := strings.TrimSpace(move)
	if i := strings.IndexAny(name, ".\n"); i > 0 {
		name = name[:i]
	}
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > 80 {
		name = strings.TrimRight(string(r[:79]), " ") + "…"
	}
	return name
}
