// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package retrievaleval compares retrieval rankings against reviewed labels.
package retrievaleval

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/opentacit/tacit/pkg/contracts"
	pkgembed "github.com/opentacit/tacit/pkg/embed"
)

const RRFK = 60

var validSlices = map[string]bool{"exact": true, "semantic": true, "no-match": true}

// Entry is one reviewed retrieval query.
type Entry struct {
	ID               string                     `json:"id"`
	Slice            string                     `json:"slice"`
	Characterization contracts.Characterization `json:"characterization"`
	Relevance        map[string]int             `json:"relevance"`
}

// Fixture is a set of reviewed queries.
type Fixture struct {
	Candidates []Candidate `json:"candidates"`
	Entries    []Entry     `json:"entries"`
}

// Candidate freezes the exact text a reviewer judged.
type Candidate struct {
	ID       string `json:"id"`
	Document string `json:"document"`
}

// Parse reads and validates a fixture.
func Parse(r io.Reader) (Fixture, error) {
	var f Fixture
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return f, fmt.Errorf("decode fixture: %w", err)
	}
	if len(f.Entries) == 0 {
		return f, fmt.Errorf("fixture has no entries")
	}
	if len(f.Candidates) == 0 {
		return f, fmt.Errorf("fixture has no candidates")
	}
	candidates := map[string]bool{}
	for _, candidate := range f.Candidates {
		if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.Document) == "" {
			return f, fmt.Errorf("candidates require id and document")
		}
		if candidates[candidate.ID] {
			return f, fmt.Errorf("duplicate candidate id %q", candidate.ID)
		}
		candidates[candidate.ID] = true
	}
	seen := map[string]bool{}
	for i, e := range f.Entries {
		if strings.TrimSpace(e.ID) == "" {
			return f, fmt.Errorf("entry %d: id is required", i+1)
		}
		if seen[e.ID] {
			return f, fmt.Errorf("duplicate entry id %q", e.ID)
		}
		seen[e.ID] = true
		if !validSlices[e.Slice] {
			return f, fmt.Errorf("entry %q: slice must be exact, semantic, or no-match", e.ID)
		}
		if strings.TrimSpace(e.Characterization.SummaryText) == "" {
			return f, fmt.Errorf("entry %q: characterization.summary_text is required", e.ID)
		}
		if e.Relevance == nil {
			return f, fmt.Errorf("entry %q: relevance labels are required", e.ID)
		}
		hasRelevant := false
		if len(e.Relevance) != len(candidates) {
			return f, fmt.Errorf("entry %q: relevance must grade every candidate", e.ID)
		}
		for id := range candidates {
			grade, ok := e.Relevance[id]
			if !ok {
				return f, fmt.Errorf("entry %q: missing relevance grade for %q", e.ID, id)
			}
			if strings.TrimSpace(id) == "" || grade < 0 || grade > 2 {
				return f, fmt.Errorf("entry %q: relevance grades require a technique id and must be 0, 1, or 2", e.ID)
			}
			hasRelevant = hasRelevant || grade > 0
		}
		if e.Slice == "no-match" && hasRelevant {
			return f, fmt.Errorf("entry %q: no-match must not label a relevant technique", e.ID)
		}
		if e.Slice != "no-match" && !hasRelevant {
			return f, fmt.Errorf("entry %q: missing a positive relevance label", e.ID)
		}
	}
	return f, nil
}

// Ranking holds technique ids from best to worst.
type Ranking []string

// Dense ranks in-memory embeddings by cosine score.
func Dense(query pkgembed.Vector, ids []string, vectors []pkgembed.Vector) Ranking {
	if len(query) == 0 || pkgembed.Dot(query, query) == 0 {
		return nil
	}
	return scored(ids, func(i int) float64 { return pkgembed.Dot(query, vectors[i]) })
}

// DenseAtFloor ranks only candidates whose cosine score clears floor.
func DenseAtFloor(query pkgembed.Vector, ids []string, vectors []pkgembed.Vector, floor float64) Ranking {
	if len(query) == 0 || pkgembed.Dot(query, query) == 0 {
		return nil
	}
	type item struct {
		id    string
		score float64
	}
	items := make([]item, 0, len(ids))
	for i, id := range ids {
		score := pkgembed.Dot(query, vectors[i])
		if score >= floor {
			items = append(items, item{id, score})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].id < items[j].id
		}
		return items[i].score > items[j].score
	})
	out := make(Ranking, len(items))
	for i := range items {
		out[i] = items[i].id
	}
	return out
}

// BM25 ranks documents with the standard Okapi BM25 formula.
func BM25(query string, ids, documents []string) Ranking {
	toks := make([][]string, len(documents))
	df := map[string]int{}
	avg := 0.0
	for i, doc := range documents {
		toks[i] = words(doc)
		avg += float64(len(toks[i]))
		seen := map[string]bool{}
		for _, w := range toks[i] {
			if !seen[w] {
				df[w]++
				seen[w] = true
			}
		}
	}
	if len(documents) > 0 {
		avg /= float64(len(documents))
	}
	q := words(query)
	if len(q) == 0 {
		return nil
	}
	scores := make([]float64, len(ids))
	for i := range ids {
		tf := map[string]int{}
		for _, w := range toks[i] {
			tf[w]++
		}
		var score float64
		for _, w := range q {
			if tf[w] == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(documents)-df[w])+0.5)/(float64(df[w])+0.5))
			den := float64(tf[w]) + 1.2*(1-0.75+0.75*float64(len(toks[i]))/math.Max(avg, 1))
			score += idf * float64(tf[w]) * 2.2 / den
		}
		scores[i] = score
	}
	matched := make([]string, 0, len(ids))
	matchedScores := make([]float64, 0, len(ids))
	for i, score := range scores {
		if score > 0 {
			matched = append(matched, ids[i])
			matchedScores = append(matchedScores, score)
		}
	}
	return scored(matched, func(i int) float64 { return matchedScores[i] })
}

// ValidateVectors rejects partial embedding failures before they become an
// ID-sorted benchmark result.
func ValidateVectors(ids []string, vectors []pkgembed.Vector, dim int) error {
	if len(vectors) != len(ids) {
		return fmt.Errorf("embedder returned %d candidate vectors for %d candidates", len(vectors), len(ids))
	}
	for i, vector := range vectors {
		if !pkgembed.Valid(vector, dim) {
			return fmt.Errorf("candidate %q has an invalid embedding", ids[i])
		}
	}
	return nil
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') })
}

func scored(ids []string, score func(int) float64) Ranking {
	type item struct {
		id    string
		score float64
	}
	a := make([]item, len(ids))
	for i, id := range ids {
		a[i] = item{id, score(i)}
	}
	sort.Slice(a, func(i, j int) bool {
		if a[i].score == a[j].score {
			return a[i].id < a[j].id
		}
		return a[i].score > a[j].score
	})
	out := make(Ranking, len(a))
	for i := range a {
		out[i] = a[i].id
	}
	return out
}

// RRF fuses rankings by reciprocal rank with a fixed k of 60.
func RRF(rankings ...Ranking) Ranking {
	scores := map[string]float64{}
	for _, ranking := range rankings {
		for i, id := range ranking {
			scores[id] += 1 / float64(RRFK+i+1)
		}
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	return scored(ids, func(i int) float64 { return scores[ids[i]] })
}

// Metrics contains query-level values. Ranking metrics are undefined for no-match.
type Metrics struct {
	HasRelevant                    bool
	Recall20, Recall4, MRR4, NDCG4 float64
	IrrelevantTop4                 float64
}

// Measure scores one ranking.
func Measure(r Ranking, relevance map[string]int) Metrics {
	m := Metrics{}
	var relevant int
	for _, g := range relevance {
		if g > 0 {
			relevant++
		}
	}
	m.HasRelevant = relevant > 0
	for i, id := range r {
		g := relevance[id]
		if i < 20 && g > 0 {
			m.Recall20 += 1
		}
		if i < 4 {
			if g > 0 {
				m.Recall4++
				if m.MRR4 == 0 {
					m.MRR4 = 1 / float64(i+1)
				}
			} else {
				m.IrrelevantTop4++
			}
			m.NDCG4 += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i)+2)
		}
	}
	if relevant > 0 {
		m.Recall20 /= float64(relevant)
		m.Recall4 /= float64(relevant)
		grades := make([]int, 0, len(relevance))
		for _, g := range relevance {
			if g > 0 {
				grades = append(grades, g)
			}
		}
		sort.Sort(sort.Reverse(sort.IntSlice(grades)))
		var ideal float64
		for i, g := range grades {
			if i == 4 {
				break
			}
			ideal += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i)+2)
		}
		if ideal > 0 {
			m.NDCG4 /= ideal
		}
	}
	return m
}
