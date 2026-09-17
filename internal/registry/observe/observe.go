// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package observe turns the org's own observed "worked moves" into candidate
// techniques — the discovery producer that feeds the shadow → auto-promote pipeline
// (docs/learning/observed-technique-discovery.md).
//
// Input is technique-less sketches: scrubbed, session-hashed Trigger+Move pairs the
// hook agent distilled from turns that demonstrably worked, not yet tied to any
// technique. This package clusters the repeated ones (>= k distinct sessions across
// >= m distinct cohorts — repetition and k-anonymity in one test), distills each
// qualifying cluster into a technique with a plain LLM completion, runs it through the
// safety screen, dedupes it against the serving set, and files it as
// provenance=observed — entering shadow (like suggest.Run) when auto-shadow is on.
//
// The whole back half (screen, shadow evaluation, evidence-gated promotion, decay)
// is inherited unchanged; the only new logic here is the clustering that the
// external miner used to own and the distillation of a cluster into a technique.
package observe

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/contribute"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/pkg/contracts"
)

type observationStore interface {
	AppendLifecycleEvent(models.LifecycleEvent) (bool, error)
	GetTechnique(string) (models.Technique, bool, error)
	ListTechniques([]string, int) ([]models.Technique, error)
	SetTechniqueEmbedding(string, []float32, string, int) error
	TechniquelessSketches(int) ([]models.Sketch, error)
	UpsertTechnique(models.Technique) error
}

// Completer is the plain, tool-less model the distiller runs against. Distillation
// needs no web search, so suggest.Client satisfies this directly.
type Completer interface {
	Complete(prompt string, maxTokens int) (string, error)
}

// Config bounds one discovery pass. The k/m defaults live in registry config.
type Config struct {
	MinSessions   int     // k: distinct session_hashes a cluster needs (repetition + anonymity)
	MinCohorts    int     // m: distinct cohort values a cluster needs
	SimThreshold  float64 // cosine at/above which two sketches are the same move
	MaxTechniques int     // cap techniques filed per pass
	EnterShadow   bool    // file as shadow (auto-review) vs draft
}

// Cluster is a group of technique-less sketches judged to be the same move, with the
// diversity counts that decide whether it qualifies.
type Cluster struct {
	Sketches []models.Sketch
	Sessions int // distinct session_hash
	Cohorts  int // max distinct values across any one cohort dimension
	centroid embed.Vector
}

func sketchText(s models.Sketch) string {
	return strings.TrimSpace(s.Trigger + "\n" + s.Move)
}

// ClusterSketches groups technique-less sketches by meaning and returns only the
// clusters that clear the k (sessions) and m (cohorts) floors, largest first.
// Greedy single-pass clustering: each sketch joins the first cluster whose
// centroid it is within SimThreshold of, else it seeds a new one.
func ClusterSketches(sketches []models.Sketch, embedder embed.Embedder, cfg Config) []Cluster {
	var clusters []Cluster
	for _, s := range sketches {
		txt := sketchText(s)
		if txt == "" {
			continue
		}
		vec := embedder.Embed([]string{txt})[0]
		placed := false
		for i := range clusters {
			if embed.Dot(vec, clusters[i].centroid) >= cfg.SimThreshold {
				clusters[i].Sketches = append(clusters[i].Sketches, s)
				clusters[i].centroid = meanVec(clusters[i].centroid, vec, len(clusters[i].Sketches))
				placed = true
				break
			}
		}
		if !placed {
			clusters = append(clusters, Cluster{Sketches: []models.Sketch{s}, centroid: vec})
		}
	}
	var qualifying []Cluster
	for _, c := range clusters {
		c.Sessions, c.Cohorts = diversity(c.Sketches)
		if c.Sessions >= cfg.MinSessions && c.Cohorts >= cfg.MinCohorts {
			qualifying = append(qualifying, c)
		}
	}
	// Largest (most-repeated) first — the strongest evidence gets the scarce
	// per-pass technique budget.
	sort.SliceStable(qualifying, func(i, j int) bool {
		return len(qualifying[i].Sketches) > len(qualifying[j].Sketches)
	})
	return qualifying
}

// meanVec folds v into a running centroid of n members (n includes v).
func meanVec(centroid, v embed.Vector, n int) embed.Vector {
	out := make(embed.Vector, len(centroid))
	fn := float32(n)
	for i := range centroid {
		out[i] = (centroid[i]*float32(n-1) + v[i]) / fn
	}
	return out
}

// diversity counts distinct sessions and the widest distinct-cohort spread over
// any single segment dimension — "≥ m cohorts" means m different teams (or roles,
// or …), which is the anonymity guarantee: no technique exists unless several
// independent people, across more than one cohort, did the move.
func diversity(sketches []models.Sketch) (sessions, cohorts int) {
	sess := map[string]bool{}
	perDim := map[string]map[string]bool{}
	for _, s := range sketches {
		if s.SessionHash != "" {
			sess[s.SessionHash] = true
		}
		for _, dim := range config.SegmentDimensions {
			if val := s.Segment[dim]; val != "" {
				if perDim[dim] == nil {
					perDim[dim] = map[string]bool{}
				}
				perDim[dim][val] = true
			}
		}
	}
	for _, vals := range perDim {
		if len(vals) > cohorts {
			cohorts = len(vals)
		}
	}
	return len(sess), cohorts
}

// Distill asks the model to write one technique from a cluster's observed moves. The
// proposal comes back in the same shape a research suggestion does, with Source set
// to the cluster's evidence rather than a URL.
func Distill(model Completer, c Cluster) (contribute.ProposedTechnique, error) {
	var d contribute.ProposedTechnique
	text, err := model.Complete(distillPrompt(c), 1024)
	if err != nil {
		return d, err
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return d, fmt.Errorf("no JSON object in distillation response (%.200s)", text)
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &d); err != nil {
		return contribute.ProposedTechnique{}, fmt.Errorf("distillation JSON: %w", err)
	}
	d.SourceURL = fmt.Sprintf("observed %d times across %d cohorts", len(c.Sketches), c.Cohorts)
	return d, nil
}

func distillPrompt(c Cluster) string {
	var b strings.Builder
	b.WriteString(`Below are real "moves" several people independently used with their AI coding agents in situations where the move demonstrably worked (a test or build passed, or they explicitly confirmed it). Each is a one-line situation and the move they made.

Write ONE reusable technique that captures the shared move. Be concrete and imperative in the recipe. Do not invent detail beyond the observations.

Observed moves:
`)
	for _, s := range c.Sketches {
		fmt.Fprintf(&b, "- when %s: %s\n", strings.TrimSpace(s.Trigger), strings.TrimSpace(s.Move))
	}
	fmt.Fprintf(&b, `
Return ONLY a JSON object:
{"name": "...", "description": "one sentence", "recipe": "the imperative move", "applies_when": "...", "not_when": "...", "tags": ["..."]}
This move was seen %d times across %d cohorts.`, len(c.Sketches), c.Cohorts)
	return b.String()
}

// Run is the discovery pass: cluster technique-less sketches, distill each qualifying
// cluster, screen and dedupe, and file survivors as provenance=observed. Mirrors
// suggest.Run's filing so the shadow entry, screen, and dedup behave identically.
func Run(st observationStore, embedder embed.Embedder, model Completer, cfg Config) ([]models.Technique, error) {
	sketches, err := st.TechniquelessSketches(0)
	if err != nil {
		return nil, err
	}
	clusters := ClusterSketches(sketches, embedder, cfg)
	if len(clusters) == 0 {
		return nil, nil
	}
	existing, err := st.ListTechniques(nil, 0)
	if err != nil {
		return nil, err
	}
	landing := contribute.NewLanding(st, embedder, existing,
		contribute.EntryStatus(cfg.EnterShadow), contribute.ProvenanceObserved)
	var created []models.Technique
	for _, c := range clusters {
		if cfg.MaxTechniques > 0 && len(created) >= cfg.MaxTechniques {
			break
		}
		d, err := Distill(model, c)
		if err != nil {
			continue // a cluster the model couldn't distill is skipped, not fatal
		}
		technique, landed, err := landing.LandDraft(d)
		if err != nil {
			return created, err
		}
		if !landed {
			continue // unusable, screened out, or a technique the registry already has
		}
		// Log the discovery for the Events view.
		// Best-effort telemetry — the technique is already filed, so a log failure must
		// not undo it. Cohort-free: technique.Source is a k/m count, no member or session.
		_, _ = st.AppendLifecycleEvent(models.LifecycleEvent{
			EventID:       contracts.DeterministicLifecycleEventID(technique.ID, "discovered", technique.CreatedAt),
			TechniqueID:   technique.ID,
			TechniqueName: technique.Name,
			Kind:          "discovered",
			Provenance:    technique.Provenance,
			Reason:        "distilled from usage — " + technique.Source,
			CreatedAt:     technique.CreatedAt,
		})
		created = append(created, technique)
	}
	return created, nil
}
