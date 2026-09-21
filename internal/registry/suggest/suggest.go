// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package suggest discovers generally-applicable best-practice techniques
// for this organization and files them as drafts (docs/mining/suggest-design.md).
//
// OpenTacit already sees, in aggregate, what members do with their agents: the
// feedback event log carries task types, harnesses, surfaces, and cohort
// dimensions, and the technique set records what was adopted, what helped, and
// what was dismissed. This package distills that visibility into a usage
// profile, hands it to a researcher that runs carefully-crafted web searches
// (the Anthropic Messages API with the server-side web_search tool), and
// lands up to ten non-duplicative techniques in the ordinary drafts
// lane — provenance "suggested", held out of retrieval until a reviewer
// promotes them, like every other unreviewed technique.
package suggest

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/contribute"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
)

type suggestionStore interface {
	AllEvents(string) ([]models.FeedbackEvent, error)
	AuditFacts(string) ([]models.AuditFact, error)
	GetTechnique(string) (models.Technique, bool, error)
	ListTechniques([]string, int) ([]models.Technique, error)
	SetTechniqueEmbedding(string, []float32, string, int) error
	UpsertTechnique(models.Technique) error
}

// MaxBatch caps one suggestion run — a reviewable screenful, not a flood.
const MaxBatch = 10

// Profile is the aggregate, cohort-only picture of how this org uses its
// agents — the "what we do" half of the research prompt. No identities, no
// transcripts: the same privacy posture as the digest.
type Profile struct {
	WindowDays         int            `json:"window_days"`
	Events             int            `json:"events"`
	Stages             map[string]int `json:"stages,omitempty"`
	TaskTypes          map[string]int `json:"task_types,omitempty"`
	Harnesses          map[string]int `json:"harnesses,omitempty"`
	Surfaces           map[string]int `json:"surfaces,omitempty"`
	Teams              map[string]int `json:"teams,omitempty"`
	Roles              map[string]int `json:"roles,omitempty"`
	AdoptedTags        map[string]int `json:"adopted_tags,omitempty"`
	DismissReasons     map[string]int `json:"dismiss_reasons,omitempty"`
	AdoptedTechniques  []string       `json:"adopted_techniques,omitempty"`
	ExistingTechniques []string       `json:"existing_techniques,omitempty"`
	// The observed-work half (docs/mining/suggest-design.md follow-up): what
	// the org's interactions actually look like, from the audit-fact log —
	// every audited interaction, not just the minority that produced feedback
	// events. This is what lets research target the org's real gaps instead
	// of generic best practice: task types nobody has techniques for, and the
	// internal systems (named, but content-free) work keeps touching.
	ObservedTaskTypes map[string]int `json:"observed_task_types,omitempty"`
	ToolsInPlay       map[string]int `json:"tools_in_play,omitempty"`
	InternalResources map[string]int `json:"internal_resources,omitempty"`
	// Vocabulary is the live tag vocabulary, most-used first. It is in the
	// profile so the prompt can hand the model a CONTROLLED vocabulary to draw
	// from: without it the model coins fresh tags on every run, and since
	// suggestion is the dominant source of new techniques, the vocabulary grows a
	// new near-synonym ("agent-setup" beside "setup") every pass. Tags drive
	// the /techniques filters, the per-tag outcome views, and the column axis of the
	// technique map, so an inflating vocabulary fragments all three.
	Vocabulary []string `json:"tag_vocabulary,omitempty"`
}

// BuildProfile aggregates the event log (windowDays back; <=0 means all) and
// the technique set into a Profile.
func BuildProfile(st suggestionStore, windowDays int) (Profile, error) {
	since := ""
	if windowDays > 0 {
		since = time.Now().UTC().AddDate(0, 0, -windowDays).Format(time.RFC3339Nano)
	}
	events, err := st.AllEvents(since)
	if err != nil {
		return Profile{}, err
	}
	techniques, err := st.ListTechniques(nil, 0)
	if err != nil {
		return Profile{}, err
	}
	byID := map[string]models.Technique{}
	p := Profile{
		WindowDays: windowDays, Events: len(events),
		Stages: map[string]int{}, TaskTypes: map[string]int{},
		Harnesses: map[string]int{}, Surfaces: map[string]int{},
		Teams: map[string]int{}, Roles: map[string]int{},
		AdoptedTags: map[string]int{}, DismissReasons: map[string]int{},
	}
	tagUse := map[string]int{}
	for _, c := range techniques {
		byID[c.ID] = c
		if c.Status != "retired" {
			p.ExistingTechniques = append(p.ExistingTechniques, c.Name)
		}
		if c.Status == "draft" || c.Status == "retired" {
			continue // the vocabulary is what is LIVE, not what was proposed
		}
		for _, tag := range c.Tags {
			tagUse[tag]++
		}
	}
	p.Vocabulary = rankedTags(tagUse)

	// The observed-work half: audit facts record every interaction's shape,
	// so the research prompt can chase the org's actual gaps. Task types and
	// tool/resource NAMES only — the fact log carries no content by design.
	if facts, err := st.AuditFacts(since); err == nil && len(facts) > 0 {
		p.ObservedTaskTypes = map[string]int{}
		p.ToolsInPlay = map[string]int{}
		p.InternalResources = map[string]int{}
		for _, f := range facts {
			if f.TaskType != "" {
				p.ObservedTaskTypes[f.TaskType]++
			}
			for _, tool := range f.ToolsUsed {
				p.ToolsInPlay[tool]++
			}
			for _, res := range f.Resources {
				p.InternalResources[res]++
			}
		}
	}

	adopted := map[string]int{}
	for _, e := range events {
		p.Stages[e.Stage]++
		if e.TaskType != "" {
			p.TaskTypes[e.TaskType]++
		}
		for dim, m := range map[string]map[string]int{
			"harness": p.Harnesses, "surface": p.Surfaces,
			"team": p.Teams, "role": p.Roles,
		} {
			if v := e.Segment[dim]; v != "" {
				m[v]++
			}
		}
		switch e.Stage {
		case "adopted":
			adopted[e.TechniqueID]++
			for _, tag := range byID[e.TechniqueID].Tags {
				p.AdoptedTags[tag]++
			}
		case "dismissed":
			if reason, ok := e.Value.(string); ok && reason != "" {
				p.DismissReasons[reason]++
			}
		}
	}
	type kv struct {
		id string
		n  int
	}
	var tops []kv
	for id, n := range adopted {
		tops = append(tops, kv{id, n})
	}
	sort.Slice(tops, func(i, j int) bool {
		if tops[i].n != tops[j].n {
			return tops[i].n > tops[j].n
		}
		return tops[i].id < tops[j].id
	})
	for i, t := range tops {
		if i == 8 {
			break
		}
		name := byID[t.id].Name
		if name == "" {
			name = t.id
		}
		p.AdoptedTechniques = append(p.AdoptedTechniques, fmt.Sprintf("%s (%d adoptions)", name, t.n))
	}
	sort.Strings(p.ExistingTechniques)
	return p, nil
}

// rankedTags orders the vocabulary by use, commonest first, so the model sees
// the org's established language before its long tail. Ties break
// alphabetically, so the prompt is stable across runs.
func rankedTags(use map[string]int) []string {
	out := make([]string, 0, len(use))
	for tag := range use {
		out = append(out, tag)
	}
	sort.Slice(out, func(i, j int) bool {
		if use[out[i]] != use[out[j]] {
			return use[out[i]] > use[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// vocabLine renders the vocabulary for the prompt. With no live techniques there is
// no vocabulary yet, and the model is told so rather than shown an empty list —
// an empty "reuse these" instruction reads as "reuse nothing".
func vocabLine(tags []string) string {
	if len(tags) == 0 {
		return "(none yet — this registry has no live tags, so coin what the techniques need, lowercase and hyphenated)"
	}
	return strings.Join(tags, ", ")
}

// Draft is one researched technique proposal, pre-validation. It is the shared
// proposal shape, named for the lane that produces it.
type Draft = contribute.ProposedTechnique

// Researcher produces technique drafts for a research prompt. The real one
// (Anthropic + web_search) lives in anthropic.go; tests inject fakes.
type Researcher interface {
	Research(prompt string) ([]Draft, error)
}

// Prompt renders the research brief: the usage profile, the do-not-duplicate
// list, and instructions for the web searches and the output contract.
func Prompt(p Profile, n int) string {
	profJSON, _ := json.MarshalIndent(p, "", " ")
	return fmt.Sprintf(`Research practices for working with AI agents and coding assistants.

Below is an aggregate, cohort-only usage profile from one organization's %s
registry (what its members do with their agents, which techniques they
adopted, what they dismissed). Identify the main usage patterns, then search
for recent, well-supported practices that fit them. Prefer primary sources,
including vendor documentation and engineering posts from tool authors. Use
material published within the last year when available.

PRIORITIZE THE ORG'S OBSERVED GAPS over generic best practice: the profile's
observed_task_types / tools_in_play / internal_resources fields describe what
this organization's interactions actually look like (every audited
interaction, not just those with feedback). Weight your proposals toward the
heaviest observed task types and tools that the existing techniques do not
already cover, and where the observed work repeatedly touches a named
internal system, anchor the technique's applies_when to that kind of work so it
retrieves for it — the goal is techniques this org will actually hit, not a
reading list.

USAGE PROFILE:
%s

EXISTING TECHNIQUES. Do not duplicate or trivially rephrase them:
%s

TAG VOCABULARY. This organization already uses these tags, commonest first:
%s
Tag each technique from this vocabulary. Reuse an existing tag wherever one fits —
prefer a slightly loose fit over a new tag. Coin a new tag ONLY when the technique is
about something the vocabulary genuinely cannot express, and then coin at most
one. New tags must be lowercase and hyphenated. Every new tag is a permanent
cost: it fragments the tag filters, the per-tag outcome views, and the playbook
map, and a near-synonym of an existing tag ("agent-setup" beside "setup") is the
most expensive thing you can produce here.

Propose exactly %d techniques. Every technique must be:
- applicable without organization-specific tools or paths,
- actionable as a reusable recipe,
- distinct from the existing techniques and each other.

Respond with a JSON array without prose or a code fence. Use plain-text field
values without citation markers, <cite> tags, or Markdown. Put the source in
source_url. Each element:
{"name": "<short imperative title>",
 "description": "<1-2 sentences describing the practice and expected benefit>",
 "recipe": "<paste-ready steps or prompt text>",
 "applies_when": "<when this is the right move>",
 "not_when": "<specific conditions where it does not apply>",
 "tags": ["<tag>", ...],
 "source_url": "<the most relevant primary source>"}`,
		product.Name(), profJSON, "- "+strings.Join(p.ExistingTechniques, "\n- "), vocabLine(p.Vocabulary), n)
}

// Run executes one suggestion pass — profile -> research (up to n proposals)
// -> validate -> dedupe -> land the survivors as drafts — and returns the
// techniques actually created. Each proposal is run through the deterministic
// safety screen (a machine proposal is the very thing that screen exists to
// gate); a High finding drops it. When enterShadow is set, survivors enter
// shadow evaluation instead of the draft lane — the machine-lane entry of the
// automated-review loop (docs/learning/validation-without-review.md):
// screened, then judged for relevance with zero exposure, then auto-graduated
// on evidence.
func Run(st suggestionStore, embedder embed.Embedder, r Researcher, n int, enterShadow bool) ([]models.Technique, error) {
	if n <= 0 || n > MaxBatch {
		n = MaxBatch
	}
	profile, err := BuildProfile(st, 90)
	if err != nil {
		return nil, err
	}
	drafts, err := r.Research(Prompt(profile, n))
	if err != nil {
		return nil, err
	}
	existing, err := st.ListTechniques(nil, 0)
	if err != nil {
		return nil, err
	}
	landing := contribute.NewLanding(st, embedder, existing,
		contribute.EntryStatus(enterShadow), contribute.ProvenanceSuggested)
	var created []models.Technique
	for _, d := range drafts {
		if len(created) == n {
			break
		}
		technique, landed, err := landing.LandDraft(d)
		if err != nil {
			return created, err
		}
		if landed {
			created = append(created, technique)
		}
	}
	// A research pass that keeps proposing techniques the registry already has
	// is telling you the prompt is mis-scoped, and only the count says so.
	if n := len(landing.Duplicates); n > 0 {
		log.Printf("[suggest] %d of %d proposals were techniques the playbook already has",
			n, len(drafts))
	}
	return created, nil
}
