// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package tagmerge proposes consolidations of the tag vocabulary.
//
// models.NormalizeTags stops two tags that LOOK the same from becoming two tags.
// It cannot touch the harder case: two tags that MEAN the same thing
// ("agent-setup" beside "setup", "persona" beside "personalization"). That is a
// semantic judgment, which is exactly what a language model is for — and exactly
// what a language model should not be trusted to execute unsupervised.
//
// So this package PROPOSES and never applies. It returns candidate merges with
// reasons; a human accepts, adjusts, or declines them in the UI. The distinction
// is the whole design: an automated tag merge that gets it wrong silently
// rewrites the vocabulary of every technique carrying the tag, and "degradation" and
// "delegation" look far more alike to a naive matcher than they mean.
//
// Everything the model returns is validated against the real vocabulary before a
// human ever sees it (see validate): the model cannot invent a tag, cannot merge
// a tag that does not exist, cannot merge a tag into itself, cannot claim the
// same tag twice, and cannot build a chain (a → b, b → c) whose result would
// depend on the order the merges happened to run in.
package tagmerge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
)

// Entry is one tag in the vocabulary, with the evidence a reader (or a model)
// needs to judge it: how many techniques carry it, and what they are called.
// The names matter — "context" on a technique about prompt caching and "context" on a
// technique about project memory are the same word doing two jobs, and only the names
// reveal that.
type Entry struct {
	Tag      string
	Count    int
	Examples []string // technique names carrying this tag, a few at most
}

// Proposal is one candidate consolidation: fold every tag in From into Into.
// Into is always itself a real tag in the vocabulary — a merge collapses the
// vocabulary, and inventing a new name to collapse into would grow it.
type Proposal struct {
	From []string `json:"from"`
	Into string   `json:"into"`
	Why  string   `json:"why"`
}

// Model is the language model this runs against. An interface so the package is
// testable without a network, and so the caller decides which client to use.
type Model interface {
	Complete(prompt string, maxTokens int) (string, error)
}

// maxTokens is the model's budget. The answer is a short JSON array, so this is
// mostly headroom — but it is deliberately not tight: a reasoning model spends
// its thinking from the SAME budget, and too small a number is spent entirely on
// deliberation, returning nothing. The client disables thinking for exactly that
// reason (suggest.Anthropic.Complete); this is the belt to that pair of braces.
const maxTokens = 8192

// Propose asks the model for candidate merges and returns only those that
// survive validation against the vocabulary.
func Propose(m Model, vocab []Entry) ([]Proposal, error) {
	if len(vocab) < 2 {
		return nil, nil // nothing can merge with nothing
	}
	text, err := m.Complete(Prompt(vocab), maxTokens)
	if err != nil {
		return nil, err
	}
	raw, err := parse(text)
	if err != nil {
		return nil, err
	}
	return validate(raw, vocab), nil
}

// Prompt renders the vocabulary and asks for merges. It states the cost of a
// false positive explicitly, because the model's default instinct on a list of
// similar-looking words is to tidy — and tidying a vocabulary that is doing real
// work destroys distinctions the techniques depend on.
func Prompt(vocab []Entry) string {
	var b strings.Builder
	b.WriteString(`You are consolidating the tag vocabulary of a technique registry — a library of
validated practices for working with AI coding agents. Tags are coined freely by
several sources and are never pruned, so the vocabulary accumulates near-synonyms:
different words for one concept, which fragment the tag filters, the per-tag
outcome reports, and the technique map.

Propose merges of tags that MEAN THE SAME THING in this library.

THE VOCABULARY (tag · number of techniques · example techniques):
`)
	for _, e := range vocab {
		fmt.Fprintf(&b, "- %s · %d · %s\n", e.Tag, e.Count, strings.Join(e.Examples, "; "))
	}
	b.WriteString(`
Rules:
- Merge only genuine synonyms — two words for ONE concept. "setup" and
  "agent-setup" are the same concept. "review" and "verification" are NOT: one is
  a person looking at work, the other is proving something is true. When in
  doubt, do not propose the merge.
- A specific tag used by a single technique is NOT automatically a problem. Only
  propose it if it duplicates another tag's meaning.
- "into" MUST be one of the tags listed above — prefer the more used, more
  general, and more established of the pair. A merge collapses the vocabulary; it
  must never invent a new word.
- Never merge a tag into a tag you are also merging away. Every proposal must
  stand on its own.
- Judge by MEANING, not by spelling. Words that look alike often mean different
  things — "degradation" and "delegation" share most of their letters and nothing
  else. Read the example technique names before you decide.
- Propose nothing at all if the vocabulary has no genuine duplicates. An empty
  array is a valid, and often correct, answer.

A wrong merge is expensive: it silently rewrites every technique carrying the
tag and destroys a distinction the library was relying on. A missed merge costs
nothing — it can be proposed again next time. Be conservative.

Respond with a JSON array and nothing else. Each element:
{"from": ["<tag>", ...], "into": "<tag>", "why": "<one sentence: why these are one concept>"}`)
	return b.String()
}

// parse pulls the JSON array out of the model's text — tolerant of a stray fence
// or preamble, strict about the payload.
func parse(text string) ([]Proposal, error) {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array in the merge response (%.200s)", text)
	}
	var out []Proposal
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("merge response JSON: %w", err)
	}
	return out, nil
}

// validate is the gate between the model and the human. Anything it drops, the
// reviewer never sees — a proposal that cannot be applied coherently is noise in
// a review queue, not a decision.
//
// It enforces, in order: tags are real (both sides exist in the vocabulary, after
// normalization); no self-merge; no tag claimed twice as a source; and no chains
// — a tag being merged away can never be a merge target, because the result would
// then depend on which merge ran first.
func validate(raw []Proposal, vocab []Entry) []Proposal {
	known := make(map[string]bool, len(vocab))
	for _, e := range vocab {
		known[e.Tag] = true
	}

	// Targets are resolved first, so a source can be rejected for pointing at a
	// tag that some other proposal is merging away.
	targets := map[string]bool{}
	for _, p := range raw {
		if into := models.NormalizeTag(p.Into); known[into] {
			targets[into] = true
		}
	}

	claimed := map[string]bool{}
	var out []Proposal
	for _, p := range raw {
		into := models.NormalizeTag(p.Into)
		if !known[into] {
			continue // the model invented a tag; a merge may not grow the vocabulary
		}
		var from []string
		for _, f := range p.From {
			f = models.NormalizeTag(f)
			switch {
			case !known[f], f == into, claimed[f], targets[f]:
				// unknown tag · self-merge · already merged by another
				// proposal · is itself a merge target (a chain, whose result
				// would depend on execution order)
				continue
			}
			claimed[f] = true
			from = append(from, f)
		}
		if len(from) == 0 {
			continue
		}
		out = append(out, Proposal{From: from, Into: into, Why: strings.TrimSpace(p.Why)})
	}
	return out
}
