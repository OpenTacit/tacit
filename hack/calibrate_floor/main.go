// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Command calibrate_floor replays retrieval offline against the recorded audit
// facts to find where a similarity floor would sit.
//
// It is a throwaway calibration harness, not part of the service. Note the
// known bias: audit facts do not record ch.SummaryText (they are aggregate-only
// by design), so the reconstructed query text is a SUBSET of what live
// retrieval embeds. Absolute similarities are therefore shifted; what the
// harness is asked for is the SEPARATION between candidates the fit-check
// accepted and candidates it declined, measured under one consistent query
// approximation.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/opentacit/tacit/pkg/contracts"
	"github.com/opentacit/tacit/pkg/embed"
)

type fact struct {
	AuditID           string   `json:"audit_id"`
	TaskType          string   `json:"task_type"`
	ToolsUsed         []string `json:"tools_used"`
	ResourcesInPlay   []string `json:"resources_in_play"`
	Harness           string   `json:"harness"`
	Surface           string   `json:"surface"`
	TechniquesOffered []string `json:"techniques_offered"`
}

type event struct {
	AuditID     string `json:"audit_id"`
	TechniqueID string `json:"technique_id"`
	Stage       string `json:"stage"`
	TaskType    string `json:"task_type"`
	RankShown   int    `json:"rank_shown"`
}

type technique struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Embedding []float32 `json:"embedding"`
}

func readJSONL[T any](path string) ([]T, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []T
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func pct(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	i := int(p / 100 * float64(len(xs)-1))
	return xs[i]
}

func summarize(label string, xs []float64) {
	if len(xs) == 0 {
		fmt.Printf("%-28s n=0\n", label)
		return
	}
	sort.Float64s(xs)
	var sum float64
	for _, x := range xs {
		sum += x
	}
	fmt.Printf("%-28s n=%-5d mean=%.3f  p5=%.3f p25=%.3f p50=%.3f p75=%.3f p95=%.3f\n",
		label, len(xs), sum/float64(len(xs)),
		pct(xs, 5), pct(xs, 25), pct(xs, 50), pct(xs, 75), pct(xs, 95))
}

func main() {
	model := os.Getenv("TACIT_EMBED_MODEL")
	if model == "" {
		model = "onnx/all-MiniLM-L6-v2"
	}
	embedder, err := embed.New(model, 384)
	if err != nil {
		fmt.Fprintf(os.Stderr, "embedder: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("embedder: %s dim=%d\n\n", embedder.ModelID(), embedder.Dim())

	facts, err := readJSONL[fact]("data/audit_facts.jsonl")
	if err != nil {
		fmt.Fprintf(os.Stderr, "facts: %v\n", err)
		os.Exit(1)
	}
	events, err := readJSONL[event]("data/events.jsonl")
	if err != nil {
		fmt.Fprintf(os.Stderr, "events: %v\n", err)
		os.Exit(1)
	}
	cb, err := os.ReadFile("data/techniques.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "techniques: %v\n", err)
		os.Exit(1)
	}
	var techniques []technique
	if err := json.Unmarshal(cb, &techniques); err != nil {
		fmt.Fprintf(os.Stderr, "techniques: %v\n", err)
		os.Exit(1)
	}
	byID := map[string]technique{}
	for _, c := range techniques {
		byID[c.ID] = c
	}

	// verdicts[auditID][techniqueID] = "shown" | "declined"
	verdicts := map[string]map[string]string{}
	for _, e := range events {
		if e.Stage != "shown" && e.Stage != "declined" {
			continue
		}
		if verdicts[e.AuditID] == nil {
			verdicts[e.AuditID] = map[string]string{}
		}
		verdicts[e.AuditID][e.TechniqueID] = e.Stage
	}

	var shownSims, declinedSims []float64
	// perAudit records, for audits that landed a technique, the shown technique's sim —
	// the quantity a floor must not cut.
	var landedShown []float64
	audits := 0

	for _, f := range facts {
		v := verdicts[f.AuditID]
		if len(v) == 0 {
			continue
		}
		audits++
		ch := contracts.Characterization{
			TaskType:                f.TaskType,
			InternalResourcesInPlay: f.ResourcesInPlay,
		}
		qvec := embedder.Embed([]string{embed.QueryText(ch)})[0]

		landed := false
		var thisShown float64
		for capID, stage := range v {
			c, ok := byID[capID]
			if !ok || len(c.Embedding) == 0 {
				continue
			}
			sim := embed.Dot(qvec, c.Embedding)
			if stage == "shown" {
				shownSims = append(shownSims, sim)
				landed = true
				thisShown = sim
			} else {
				declinedSims = append(declinedSims, sim)
			}
		}
		if landed {
			landedShown = append(landedShown, thisShown)
		}
	}

	fmt.Printf("audits with verdicts: %d\n\n", audits)
	summarize("shown (fit-check kept)", shownSims)
	summarize("declined (fit-check cut)", declinedSims)
	fmt.Println()

	// Sensitivity: at each candidate floor, how many declines are suppressed
	// (pure win) versus how many shown techniques are lost (pure cost)?
	fmt.Println("floor   declines-cut   shown-lost   (lower cost is better)")
	sort.Float64s(landedShown)
	for _, floor := range []float64{0.05, 0.10, 0.15, 0.20, 0.25, 0.30, 0.35, 0.40, 0.45, 0.50} {
		cut, lost := 0, 0
		for _, s := range declinedSims {
			if s < floor {
				cut++
			}
		}
		for _, s := range shownSims {
			if s < floor {
				lost++
			}
		}
		fmt.Printf("%.2f    %4d (%4.1f%%)    %3d (%4.1f%%)\n",
			floor, cut, 100*float64(cut)/float64(len(declinedSims)),
			lost, 100*float64(lost)/float64(len(shownSims)))
	}
}
