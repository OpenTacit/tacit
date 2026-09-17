// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package embed

import (
	"math"
	"strings"
	"testing"

	"github.com/opentacit/tacit/pkg/contracts"
)

func TestHashingDeterministicAndNormalized(t *testing.T) {
	e := NewHashing(64, "hashing-v1")
	a := e.Embed([]string{"query the warehouse for pipeline data"})[0]
	b := e.Embed([]string{"query the warehouse for pipeline data"})[0]
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("embedding not deterministic")
		}
	}
	var norm float64
	for _, x := range a {
		norm += float64(x) * float64(x)
	}
	if math.Abs(norm-1.0) > 1e-5 {
		t.Fatalf("not L2-normalized: %f", norm)
	}
}

func TestSimilarTextScoresHigherThanUnrelated(t *testing.T) {
	e := NewHashing(256, "hashing-v1")
	vecs := e.Embed([]string{
		"user pasted csv rows from the internal warehouse",
		"pasted tabular warehouse rows into the chat",
		"draw a sequence diagram of the auth flow",
	})
	simNear := Dot(vecs[0], vecs[1])
	simFar := Dot(vecs[0], vecs[2])
	if simNear <= simFar {
		t.Fatalf("lexical similarity inverted: near=%f far=%f", simNear, simFar)
	}
}

func TestTechniqueAndQueryText(t *testing.T) {
	technique := contracts.Technique{
		Name: "N", Description: "D", AppliesWhen: "when pasting",
		Tags: []string{"t1"}, TaskTypes: []string{"data"},
		Triggers: []any{"plain", map[string]any{"heuristic": "pasted rows"}},
	}
	text := TechniqueText(technique)
	for _, want := range []string{"N", "D", "when pasting", "t1", "data", "plain", "pasted rows"} {
		if !contains(text, want) {
			t.Fatalf("technique text missing %q: %s", want, text)
		}
	}
	q := QueryText(contracts.Characterization{
		SummaryText: "sum", TaskType: "tt", ToolsAbsent: []string{"connector"},
		InternalResourcesInPlay: []string{"pipeline.csv"},
	})
	for _, want := range []string{"sum", "tt", "connector", "pipeline.csv"} {
		if !contains(q, want) {
			t.Fatalf("query text missing %q: %s", want, q)
		}
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func TestUnknownModelRejected(t *testing.T) {
	if _, err := New("bert-large", 256); err == nil {
		t.Fatal("unknown embedder accepted")
	}
	e, err := New("hashing-v1", 32)
	if err != nil || e.Dim() != 32 || e.ModelID() != "hashing-v1" {
		t.Fatalf("default embedder wrong: %v", err)
	}
}

func TestValidVector(t *testing.T) {
	if !Valid(Vector{1, 0}, 2) {
		t.Fatal("valid vector rejected")
	}
	for _, vector := range []Vector{{0, 0}, {float32(math.NaN()), 0}, {float32(math.Inf(1)), 0}, {1}} {
		if Valid(vector, 2) {
			t.Fatalf("invalid vector accepted: %v", vector)
		}
	}
}
