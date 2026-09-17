// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package embed provides the embedding seam and the dependency-free default.
//
// Embedder is the swappable interface (docs/design/design.md). The default
// HashingEmbedder needs no model and no external call: it hashes tokens into a
// fixed-dim, L2-normalized vector — purely lexical, good enough to validate
// the pipeline at pilot scale. A semantic model or hosted API can swap in
// behind the same interface; the rest of the service is unchanged because both
// techniques and queries go through the same instance.
//
// Determinism matters (techniques embedded in one process, queries in another), so
// we use crypto/md5 — identical hashing to the Python implementation, giving
// bit-for-bit identical vectors for the same text.
package embed

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/opentacit/tacit/pkg/contracts"
)

// Vector is one embedding.
type Vector = []float32

// Embedder is the structural interface every embedder satisfies.
type Embedder interface {
	ModelID() string
	Dim() int
	// Embed maps each text to an L2-normalized vector of Dim() floats.
	Embed(texts []string) []Vector
}

var tokenRe = regexp.MustCompile(`[a-z0-9]+`)

func tokens(text string) []string {
	return tokenRe.FindAllString(strings.ToLower(text), -1)
}

func normalize(vec Vector) Vector {
	var sum float64
	for _, x := range vec {
		sum += float64(x) * float64(x)
	}
	norm := math.Sqrt(sum)
	if norm > 0 {
		for i, x := range vec {
			vec[i] = float32(float64(x) / norm)
		}
	}
	return vec
}

// HashingEmbedder is the deterministic, dependency-free bag-of-tokens default.
type HashingEmbedder struct {
	dim     int
	modelID string
}

// NewHashing returns the default embedder.
func NewHashing(dim int, modelID string) *HashingEmbedder {
	if modelID == "" {
		modelID = "hashing-v1"
	}
	return &HashingEmbedder{dim: dim, modelID: modelID}
}

// ModelID implements Embedder.
func (e *HashingEmbedder) ModelID() string { return e.modelID }

// Dim implements Embedder.
func (e *HashingEmbedder) Dim() int { return e.dim }

func (e *HashingEmbedder) embedOne(text string) Vector {
	vec := make(Vector, e.dim)
	for _, tok := range tokens(text) {
		h := md5.Sum([]byte(tok))
		idx := int(binary.BigEndian.Uint32(h[:4])) % e.dim
		if idx < 0 {
			idx += e.dim
		}
		sign := float32(-1)
		if h[4]&1 == 1 { // signed hashing reduces collisions
			sign = 1
		}
		vec[idx] += sign
	}
	return normalize(vec)
}

// Embed implements Embedder.
func (e *HashingEmbedder) Embed(texts []string) []Vector {
	out := make([]Vector, len(texts))
	for i, t := range texts {
		out[i] = e.embedOne(t)
	}
	return out
}

// Dot is the cosine similarity of two L2-normalized vectors.
func Dot(a, b Vector) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// Valid reports whether vec has the expected size and a finite, non-zero norm.
func Valid(vec Vector, dim int) bool {
	if dim <= 0 || len(vec) != dim {
		return false
	}
	var sum float64
	for _, x := range vec {
		v := float64(x)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
		sum += v * v
	}
	return sum > 0 && !math.IsNaN(sum) && !math.IsInf(sum, 0)
}

// --- text composition (what gets embedded) --------------------------------

func triggerTexts(triggers []any) []string {
	var out []string
	for _, t := range triggers {
		switch v := t.(type) {
		case map[string]any:
			for _, val := range v {
				out = append(out, fmt.Sprint(val))
			}
		default:
			out = append(out, fmt.Sprint(v))
		}
	}
	return out
}

// TechniqueText composes the text that indexes a technique.
func TechniqueText(technique contracts.Technique) string {
	parts := []string{technique.Name, technique.Description}
	parts = append(parts, triggerTexts(technique.Triggers)...)
	if technique.AppliesWhen != "" { // positive applicability aids similarity
		parts = append(parts, technique.AppliesWhen)
	}
	parts = append(parts, technique.TaskTypes...)
	parts = append(parts, technique.Tags...)
	return joinNonEmpty(parts)
}

// QueryText composes the text that embeds a characterization as the query.
func QueryText(ch contracts.Characterization) string {
	parts := []string{ch.SummaryText}
	for _, v := range []string{ch.TaskType, ch.Domain, ch.SkillLevel} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	parts = append(parts, ch.Modalities...)
	parts = append(parts, ch.ToolsAbsent...)
	// proprietary signal: internal data/tools in play help surface scope=org techniques
	parts = append(parts, ch.InternalResourcesInPlay...)
	return joinNonEmpty(parts)
}

func joinNonEmpty(parts []string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, ". ")
}

// onnxFactory builds an "onnx/<name>" embedder. It is nil in a default build
// and set by init() in onnx.go when compiled with -tags onnx; the !onnx stub
// sets it to a constructor that returns a helpful "rebuild with -tags onnx"
// error. Keeping the seam here lets the pure-Go build stay dependency-free.
var onnxFactory func(model string, dim int) (Embedder, error)

// New returns the configured embedder. hashing-v1 is the built-in default; an
// "onnx/<model>" id routes to the in-process ONNX embedder (docs/design/embedder-onnx.md),
// available only in a build tagged onnx. This is the swap point for further
// semantic models or hosted APIs.
func New(model string, dim int) (Embedder, error) {
	switch {
	case model == "hashing-v1":
		return NewHashing(dim, model), nil
	case strings.HasPrefix(model, "onnx/"):
		if onnxFactory == nil {
			return nil, &UnknownModelError{Model: model}
		}
		return onnxFactory(model, dim)
	}
	return nil, &UnknownModelError{Model: model}
}

// UnknownModelError reports an unconfigured embedder model id.
type UnknownModelError struct{ Model string }

func (e *UnknownModelError) Error() string {
	return "unknown embedder model: " + e.Model + " (install/configure a richer embedder here)"
}
