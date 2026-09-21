// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Command magnets asks which part of a technique's text makes it a magnet — a technique
// retrieval proposes for queries it has nothing to do with.
//
// The test is mean similarity across DELIBERATELY UNRELATED queries. A technique
// that genuinely matches one topic should score high on that topic and low
// everywhere else; a technique that scores moderately high on everything is sitting
// near the centre of the embedding space, where it beats better-targeted techniques
// on queries it has no business being retrieved for. Ranking is relative, so a
// technique that is everyone's 0.3 is a rank-1 candidate whenever nothing scores 0.4.
//
// Each technique is embedded three ways (embed.TechniqueText's composition, name only,
// and everything except the name) so the contribution of the title is separable
// from the rest of the technique.
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

// probes are intentionally scattered across unrelated domains. Nothing here
// should match many techniques well; a technique with a high mean over this set is
// matching the shape of "text about working" rather than any actual topic.
var probes = []string{
	"debugging a segfault in a C++ ring buffer",
	"choosing a CSS grid layout for a marketing page",
	"writing a SQL migration to add a nullable column",
	"drafting a performance review for a direct report",
	"configuring DNS records for a new subdomain",
	"summarising a PDF of quarterly financial results",
	"fixing a flaky integration test that times out",
	"planning a week of meals within a budget",
	"renaming a Go package across a large repository",
	"explaining recursion to someone new to programming",
	"tuning a Postgres query that does a sequential scan",
	"setting up a CI pipeline that runs on pull requests",
}

type technique struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	AppliesWhen string   `json:"applies_when"`
	Status      string   `json:"status"`
	Tags        []string `json:"tags"`
	TaskTypes   []string `json:"task_types"`
	Triggers    []any    `json:"triggers"`
}

func (c technique) contract() contracts.Technique {
	return contracts.Technique{
		Name: c.Name, Description: c.Description, AppliesWhen: c.AppliesWhen,
		Tags: c.Tags, TaskTypes: c.TaskTypes, Triggers: c.Triggers,
	}
}

// withoutName is TechniqueText with the title stripped, to isolate what the rest of
// the technique contributes on its own.
func (c technique) withoutName() string {
	d := c.contract()
	d.Name = ""
	return embed.TechniqueText(d)
}

type row struct {
	id                       string
	full, nameOnly, bodyOnly float64
	fullMax                  float64
	textLen, nameLen         int
}

func meanMax(e embed.Embedder, text string, qvecs []embed.Vector) (mean, max float64) {
	if strings.TrimSpace(text) == "" {
		return 0, 0
	}
	v := e.Embed([]string{text})[0]
	for _, q := range qvecs {
		s := embed.Dot(q, v)
		mean += s
		if s > max {
			max = s
		}
	}
	return mean / float64(len(qvecs)), max
}

func main() {
	model := os.Getenv("TACIT_EMBED_MODEL")
	if model == "" {
		model = "onnx/all-MiniLM-L6-v2"
	}
	e, err := embed.New(model, 384)
	if err != nil {
		fmt.Fprintln(os.Stderr, "embedder:", err)
		os.Exit(1)
	}
	cb, err := os.ReadFile("data/techniques.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "techniques:", err)
		os.Exit(1)
	}
	var techniques []technique
	if err := json.Unmarshal(cb, &techniques); err != nil {
		fmt.Fprintln(os.Stderr, "techniques:", err)
		os.Exit(1)
	}

	qvecs := make([]embed.Vector, len(probes))
	for i, p := range probes {
		qvecs[i] = e.Embed([]string{p})[0]
	}

	var rows []row
	for _, c := range techniques {
		if c.Status == "retired" {
			continue
		}
		full := embed.TechniqueText(c.contract())
		fm, fmax := meanMax(e, full, qvecs)
		nm, _ := meanMax(e, c.Name, qvecs)
		bm, _ := meanMax(e, c.withoutName(), qvecs)
		rows = append(rows, row{c.ID, fm, nm, bm, fmax, len(full), len(c.Name)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].full > rows[j].full })

	fmt.Printf("mean similarity to %d unrelated probe queries, per technique\n", len(probes))
	fmt.Printf("(high mean = matches everything = crowds the top of the ranking)\n\n")
	fmt.Printf("%-52s %6s %6s %6s %6s %5s\n", "technique", "full", "name", "body", "max", "chars")
	show := func(rs []row) {
		for _, r := range rs {
			fmt.Printf("%-52s %6.3f %6.3f %6.3f %6.3f %5d\n",
				r.id[:min(52, len(r.id))], r.full, r.nameOnly, r.bodyOnly, r.fullMax, r.textLen)
		}
	}
	fmt.Println("--- top 12 (the magnets) ---")
	show(rows[:min(12, len(rows))])
	fmt.Println("\n--- bottom 8 (well-targeted) ---")
	show(rows[max(0, len(rows)-8):])

	// Does the name or the body drive the full-text mean? Report which is
	// closer to it, and whether name-vs-body differ systematically at all.
	var nameCloser, bodyCloser int
	var sumName, sumBody, sumFull float64
	for _, r := range rows {
		if abs(r.nameOnly-r.full) < abs(r.bodyOnly-r.full) {
			nameCloser++
		} else {
			bodyCloser++
		}
		sumName += r.nameOnly
		sumBody += r.bodyOnly
		sumFull += r.full
	}
	n := float64(len(rows))
	fmt.Printf("\ntechniques: %d\n", len(rows))
	fmt.Printf("mean over all techniques:  full=%.3f  name-only=%.3f  body-only=%.3f\n",
		sumFull/n, sumName/n, sumBody/n)
	fmt.Printf("full-text mean tracks the NAME for %d techniques, the BODY for %d\n", nameCloser, bodyCloser)

	// Length effect: does more text mean a higher floor against everything?
	sort.Slice(rows, func(i, j int) bool { return rows[i].textLen < rows[j].textLen })
	q := len(rows) / 4
	var shortMean, longMean float64
	for _, r := range rows[:q] {
		shortMean += r.full
	}
	for _, r := range rows[len(rows)-q:] {
		longMean += r.full
	}
	fmt.Printf("shortest quartile mean=%.3f   longest quartile mean=%.3f\n",
		shortMean/float64(q), longMean/float64(q))
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
