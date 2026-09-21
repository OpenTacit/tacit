// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package retrievaleval

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/techniques"
	"github.com/opentacit/tacit/pkg/embed"
)

func TestMeasure(t *testing.T) {
	m := Measure(Ranking{"x", "a", "b", "z"}, map[string]int{"a": 2, "b": 1})
	if m.Recall4 != 1 || m.MRR4 != .5 || m.IrrelevantTop4 != 2 || m.NDCG4 <= .65 || m.NDCG4 >= .7 {
		t.Fatalf("bad metrics: %+v", m)
	}
	n := Measure(Ranking{"x"}, map[string]int{})
	if n.HasRelevant || n.IrrelevantTop4 != 1 {
		t.Fatalf("bad no-match metrics: %+v", n)
	}
}

func TestDenseAtFloor(t *testing.T) {
	got := DenseAtFloor([]float32{1, 0}, []string{"high", "low"},
		[][]float32{{0.8, 0}, {0.1, 0}}, 0.25)
	if len(got) != 1 || got[0] != "high" {
		t.Fatalf("DenseAtFloor = %v", got)
	}
	if got := Dense([]float32{0, 0}, []string{"a"}, [][]float32{{1, 0}}); len(got) != 0 {
		t.Fatalf("zero query ranked candidates: %v", got)
	}
	if got := DenseAtFloor([]float32{0, 0}, []string{"a"}, [][]float32{{1, 0}}, -1); len(got) != 0 {
		t.Fatalf("zero query cleared a floor: %v", got)
	}
}

func TestBM25AndRRFOrdering(t *testing.T) {
	b := BM25("red apple", []string{"b", "a", "c"}, []string{"pear", "red apple", "red"})
	if b[0] != "a" {
		t.Fatalf("BM25 = %v", b)
	}
	r := RRF(Ranking{"b", "a"}, Ranking{"a", "b"})
	if r[0] != "a" {
		t.Fatalf("RRF tie break = %v", r)
	}
	dense := Ranking{"b", "a"}
	lexical := BM25("no overlap", []string{"a", "b"}, []string{"red apple", "green pear"})
	if len(lexical) != 0 {
		t.Fatalf("zero-score BM25 documents ranked: %v", lexical)
	}
	if got := RRF(dense, lexical); !reflect.DeepEqual(got, dense) {
		t.Fatalf("empty lexical rank changed dense order: %v", got)
	}
}

func TestValidateVectors(t *testing.T) {
	if err := ValidateVectors([]string{"a"}, [][]float32{{1, 0}}, 2); err != nil {
		t.Fatal(err)
	}
	if err := ValidateVectors([]string{"a"}, [][]float32{{0, 0}}, 2); err == nil {
		t.Fatal("accepted zero candidate vector")
	}
	if err := ValidateVectors([]string{"a"}, [][]float32{{float32(math.NaN()), 0}}, 2); err == nil {
		t.Fatal("accepted NaN candidate vector")
	}
}

func TestFixtureValidation(t *testing.T) {
	good := `{"candidates":[{"id":"t","document":"text"}],"entries":[{"id":"q","slice":"exact","characterization":{"summary_text":"x"},"relevance":{"t":2}}]}`
	if _, err := Parse(strings.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"entries":[{"id":"q","slice":"exact","characterization":{"summary_text":"x"},"relevance":{"t":2}}]}`,
		`{"candidates":[{"id":"t","document":"text"}],"entries":[{"id":"q","slice":"exact","characterization":{"summary_text":"x"},"relevance":{}}]}`,
		`{"candidates":[{"id":"t","document":"text"}],"entries":[{"id":"q","slice":"no-match","characterization":{"summary_text":"x"}}]}`,
		`{"candidates":[{"id":"t","document":"text"}],"entries":[{"id":"q","slice":"wrong","characterization":{"summary_text":"x"},"relevance":{"t":3}}]}`,
		`{"candidates":[{"id":"t","document":"text"}],"entries":[{"id":"q","slice":"exact","characterization":{"summary_text":"x"},"relevance":{"other":2}}]}`,
		`{"candidates":[{"id":"t","document":"text"},{"id":"t","document":"text"}],"entries":[{"id":"q","slice":"no-match","characterization":{"summary_text":"x"},"relevance":{"t":0}}]}`,
		`{"candidates":[{"id":"t","document":"text"}],"entries":[{"id":"q","slice":"no-match","characterization":{"summary_text":"x"},"relevance":{"t":0}},{"id":"q","slice":"no-match","characterization":{"summary_text":"y"},"relevance":{"t":0}}]}`,
	} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestStarterFixtureFreezesTrackedTechniqueText(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "testdata", "retrieval-eval.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := Parse(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range fixture.Candidates {
		technique, err := techniques.ParseFile(filepath.Join("..", "..", "techniques", candidate.ID+".md"))
		if err != nil {
			t.Fatal(err)
		}
		if want := embed.TechniqueText(technique); candidate.Document != want {
			t.Errorf("frozen document for %s differs from TechniqueText\n got: %q\nwant: %q", candidate.ID, candidate.Document, want)
		}
	}
}
