// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"fmt"
	"io"
	"strings"

	"github.com/opentacit/tacit/pkg/client"
)

// FeedbackBatch is how many events go in one /v1/feedback POST. The endpoint
// accepts an array; a few hundred per request keeps the load to a handful of
// round-trips without oversized bodies.
const FeedbackBatch = 400

// LoadOptions tune a load run.
type LoadOptions struct {
	// Force loads even when the target already holds substantial content. The
	// guard exists because the demo must never be pointed at a real registry and
	// silently bury its data among synthetic events.
	Force bool
	// Progress receives human-readable progress lines. nil discards them.
	Progress io.Writer
}

// LoadResult reports what a load did.
type LoadResult struct {
	Stats                 Stats
	TechniquesContributed int
	TechniquesPromoted    int
	TechniquesSkipped     int // already present on the target
	EventsAccepted        int
	EventsDuplicate       int
	OutcomesRecomputed    int
}

// Load populates a running registry with a dataset's month of usage over its
// public HTTP API: it contributes and promotes the techniques, posts the
// synthesized backdated funnel events, and recomputes the rollups. It is
// idempotent — techniques already present are skipped and events carry stable audit
// ids — so a re-run against the same instance converges rather than duplicating.
func Load(reg *client.Registry, d *Dataset, opts LoadOptions) (*LoadResult, error) {
	logf := func(format string, a ...any) {
		if opts.Progress != nil {
			fmt.Fprintf(opts.Progress, format+"\n", a...)
		}
	}

	health, err := reg.Health()
	if err != nil {
		return nil, fmt.Errorf("registry health check failed (is it running, and is the key the org root key?): %w", err)
	}
	existingEvents := asInt(health["events"])
	existingTechniques := asInt(health["techniques"])
	if !opts.Force && existingEvents > 200 {
		return nil, fmt.Errorf("target registry already holds %d events and %d techniques — this looks like a real instance, not a scratch one. "+
			"Point --registry at a fresh demo instance, or pass --force to load anyway", existingEvents, existingTechniques)
	}
	logf("Target: %v — %d techniques, %d events, embedder %v", strTrim(health["external_url"], reg.BaseURL), existingTechniques, existingEvents, health["embed_model"])

	res := &LoadResult{}

	// Which technique ids already exist? Contribute uniquifies a taken id
	// (slug-2, …), which would break our event references, so we skip those and
	// reuse the existing technique. On a fresh instance this set is just the starter
	// techniques.
	present := map[string]bool{}
	if techniques, err := reg.Techniques(); err == nil {
		for _, c := range techniques {
			present[c.ID] = true
		}
	}

	logf("Contributing %d techniques…", len(d.Techniques))
	for i := range d.Techniques {
		c := &d.Techniques[i]
		switch c.Scope {
		case "org":
			res.Stats.OrgTechniques++
		default:
			res.Stats.GeneralTechniques++
		}
		if c.Draft {
			res.Stats.Drafts++
		}
		if present[c.ID] {
			res.TechniquesSkipped++
			continue
		}
		if _, err := reg.Contribute(c.contributeBody()); err != nil {
			return nil, fmt.Errorf("contribute %q: %w", c.ID, err)
		}
		res.TechniquesContributed++
		if c.Draft {
			continue // drafts stay in the review lane, deliberately unpromoted
		}
		if _, err := reg.Promote(c.ID, "stable"); err != nil {
			return nil, fmt.Errorf("promote %q: %w", c.ID, err)
		}
		res.TechniquesPromoted++
	}
	logf("  contributed %d, promoted %d, left %d as drafts, skipped %d already present",
		res.TechniquesContributed, res.TechniquesPromoted, res.Stats.Drafts, res.TechniquesSkipped)

	logf("Synthesizing a %d-day month of usage…", d.Window.Days)
	syn, err := Synthesize(d)
	if err != nil {
		return nil, err
	}
	tally(&res.Stats, syn)
	logf("  %d members, %d events (%d shown · %d adopted · %d helped · %d dismissed · %d declined)",
		res.Stats.Members, len(syn.Events), res.Stats.Shown, res.Stats.Adopted, res.Stats.Helped, res.Stats.Dismissed, res.Stats.Declined)

	logf("Posting events in batches of %d…", FeedbackBatch)
	for start := 0; start < len(syn.Events); start += FeedbackBatch {
		end := min(start+FeedbackBatch, len(syn.Events))
		acc, dup, err := reg.PostFeedback(syn.Events[start:end])
		if err != nil {
			return nil, fmt.Errorf("post feedback [%d:%d]: %w", start, end, err)
		}
		res.EventsAccepted += acc
		res.EventsDuplicate += dup
	}
	logf("  accepted %d events (%d duplicates ignored)", res.EventsAccepted, res.EventsDuplicate)

	logf("Recomputing outcome rollups…")
	n, err := reg.Recompute()
	if err != nil {
		return nil, fmt.Errorf("recompute: %w", err)
	}
	res.OutcomesRecomputed = n
	return res, nil
}

// contributeBody builds a /v1/contribute request body from a technique.
func (c *Technique) contributeBody() map[string]any {
	body := map[string]any{
		"id":          c.ID,
		"name":        c.Name,
		"description": c.Description,
		"scope":       c.Scope,
		"recipe":      c.Recipe,
	}
	if len(c.Tags) > 0 {
		body["tags"] = c.Tags
	}
	if len(c.TaskTypes) > 0 {
		body["task_types"] = c.TaskTypes
	}
	if len(c.Triggers) > 0 {
		body["triggers"] = c.Triggers
	}
	if c.AppliesWhen != "" {
		body["applies_when"] = c.AppliesWhen
	}
	if c.NotWhen != "" {
		body["not_when"] = c.NotWhen
	}
	if c.BeforeAfter != "" {
		body["before_after"] = c.BeforeAfter
	}
	if c.Shipped != "" {
		body["shipped"] = c.Shipped
	}
	return body
}

// ViewURLs returns the human dashboard pages worth opening after a load.
func ViewURLs(base string) map[string]string {
	base = strings.TrimRight(base, "/")
	return map[string]string{
		"Outcomes & insights": base + "/outcomes",
		"Cohorts":             base + "/outcomes/cohorts",
		"Playbook map":        base + "/techniques/map",
		"All techniques":      base + "/techniques",
		"Drafts to review":    base + "/review",
	}
}

func tally(s *Stats, syn *SynthResult) {
	s.Members = len(syn.Members)
	for _, e := range syn.Events {
		switch e.Stage {
		case "shown":
			s.Shown++
		case "adopted":
			s.Adopted++
		case "helped":
			s.Helped++
		case "dismissed":
			s.Dismissed++
		case "declined":
			s.Declined++
		}
	}
}

func asInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

func strTrim(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}
