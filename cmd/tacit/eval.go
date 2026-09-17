// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/retrievaleval"
)

func cmdEval(args []string) int {
	if len(args) == 0 || args[0] != "retrieval" {
		fmt.Fprintln(os.Stderr, "usage: tacit eval retrieval --set <json> [--model id] [--dim n]")
		return 2
	}
	return cmdEvalRetrieval(args[1:])
}

func cmdEvalRetrieval(args []string) int {
	cfg := registryconfig.Load()
	fs := flag.NewFlagSet("eval retrieval", flag.ContinueOnError)
	setPath := fs.String("set", "", "reviewed JSON fixture")
	model := fs.String("model", cfg.EmbedModel, "embedding model id")
	dim := fs.Int("dim", cfg.EmbedDim, "embedding dimension")
	floorsArg := fs.String("floors", "0.20,0.25,0.30", "comma-separated cosine floors to compare; empty disables")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dim <= 0 {
		fmt.Fprintln(os.Stderr, "--dim must be greater than zero")
		return 2
	}
	if *setPath == "" {
		fmt.Fprintln(os.Stderr, "--set is required")
		return 2
	}
	f, err := os.Open(*setPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open set: %v\n", err)
		return 1
	}
	fixture, err := retrievaleval.Parse(f)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	floors, err := parseEvalFloors(*floorsArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	embedder, err := embed.New(*model, *dim)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	sort.Slice(fixture.Candidates, func(i, j int) bool { return fixture.Candidates[i].ID < fixture.Candidates[j].ID })
	ids, docs := make([]string, len(fixture.Candidates)), make([]string, len(fixture.Candidates))
	for i, candidate := range fixture.Candidates {
		ids[i], docs[i] = candidate.ID, candidate.Document
	}
	vectors := embedder.Embed(docs)
	if err := retrievaleval.ValidateVectors(ids, vectors, embedder.Dim()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	type sums struct {
		n, relevant int
		rankers     map[string]*[5]float64
	}
	groups := map[string]*sums{"overall": {rankers: map[string]*[5]float64{}}}
	misses := map[string][]string{}
	for _, e := range fixture.Entries {
		qtext := embed.QueryText(e.Characterization)
		queryVectors := embedder.Embed([]string{qtext})
		if len(queryVectors) != 1 || !embed.Valid(queryVectors[0], embedder.Dim()) {
			fmt.Fprintf(os.Stderr, "query %q has an invalid embedding\n", e.ID)
			return 1
		}
		qvec := queryVectors[0]
		dense := retrievaleval.Dense(qvec, ids, vectors)
		hybrid := retrievaleval.RRF(dense, retrievaleval.BM25(qtext, ids, docs))
		rankings := map[string]retrievaleval.Ranking{"dense": dense, "hybrid": hybrid}
		for _, floor := range floors {
			rankings[floorRankerName(floor)] = retrievaleval.DenseAtFloor(qvec, ids, vectors, floor)
		}
		for name, ranking := range rankings {
			m := retrievaleval.Measure(ranking, e.Relevance)
			if m.HasRelevant && m.Recall4 < 1 {
				misses[name] = append(misses[name], e.ID)
			}
		}
		for _, key := range []string{"overall", e.Slice} {
			g := groups[key]
			if g == nil {
				g = &sums{rankers: map[string]*[5]float64{}}
				groups[key] = g
			}
			g.n++
			if retrievaleval.Measure(dense, e.Relevance).HasRelevant {
				g.relevant++
			}
			for name, ranking := range rankings {
				m := retrievaleval.Measure(ranking, e.Relevance)
				if g.rankers[name] == nil {
					g.rankers[name] = &[5]float64{}
				}
				addMetrics(g.rankers[name], m, m.HasRelevant)
				g.rankers[name][4] += m.IrrelevantTop4
			}
		}
	}
	fmt.Printf("retrieval evaluation: model=%s dim=%d candidates=%d queries=%d\n", embedder.ModelID(), embedder.Dim(), len(fixture.Candidates), len(fixture.Entries))
	fmt.Println("slice      ranker       Recall@20 Recall@4 MRR@4 nDCG@4 irrelevant@4")
	rankerNames := []string{"dense", "hybrid"}
	for _, floor := range floors {
		rankerNames = append(rankerNames, floorRankerName(floor))
	}
	for _, key := range []string{"overall", "exact", "semantic", "no-match"} {
		g := groups[key]
		if g == nil {
			continue
		}
		for _, name := range rankerNames {
			printEvalRow(key, name, *g.rankers[name], g.relevant, g.n)
		}
	}
	for _, name := range rankerNames {
		if len(misses[name]) > 0 {
			fmt.Printf("top-4 misses (%s): %s\n", name, strings.Join(misses[name], ", "))
		}
	}
	return 0
}

func parseEvalFloors(s string) ([]float64, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var floors []float64
	for _, raw := range strings.Split(s, ",") {
		floor, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || math.IsNaN(floor) || math.IsInf(floor, 0) || floor < -1 || floor > 1 {
			return nil, fmt.Errorf("--floors values must be numbers from -1 to 1 (got %q)", raw)
		}
		duplicate := false
		for _, prior := range floors {
			duplicate = duplicate || prior == floor
		}
		if !duplicate {
			floors = append(floors, floor)
		}
	}
	return floors, nil
}

func floorRankerName(floor float64) string {
	return "dense@" + strconv.FormatFloat(floor, 'g', -1, 64)
}

func addMetrics(dst *[5]float64, m retrievaleval.Metrics, ranking bool) {
	if ranking {
		dst[0] += m.Recall20
		dst[1] += m.Recall4
		dst[2] += m.MRR4
		dst[3] += m.NDCG4
	}
}

func printEvalRow(slice, ranker string, v [5]float64, relevant, total int) {
	if relevant == 0 {
		fmt.Printf("%-10s %-12s %-9s %-8s %-5s %-6s %.3f (n=%d)\n", slice, ranker, "n/a", "n/a", "n/a", "n/a", v[4]/float64(total), total)
		return
	}
	fmt.Printf("%-10s %-12s %.3f     %.3f    %.3f %.3f  %.3f (rank n=%d, all n=%d)\n", slice, ranker, v[0]/float64(relevant), v[1]/float64(relevant), v[2]/float64(relevant), v[3]/float64(relevant), v[4]/float64(total), relevant, total)
}
