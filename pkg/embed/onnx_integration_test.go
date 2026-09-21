// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

//go:build onnx

package embed

import (
	"math"
	"os"
	"testing"
)

// TestONNXIntegration exercises the real ONNX embedder against on-disk
// artifacts. It runs only when TACIT_ONNX_LIB and TACIT_ONNX_MODEL_DIR point at
// a real libonnxruntime.so and a model dir (model.onnx + vocab.txt); otherwise
// it skips, so the tagged unit suite still passes without artifacts.
//
//	go test -tags onnx ./pkg/embed/ -run ONNXIntegration -v
func TestONNXIntegration(t *testing.T) {
	if os.Getenv("TACIT_ONNX_LIB") == "" || os.Getenv("TACIT_ONNX_MODEL_DIR") == "" {
		t.Skip("set TACIT_ONNX_LIB and TACIT_ONNX_MODEL_DIR to run the ONNX integration test")
	}
	e, err := New("onnx/all-MiniLM-L6-v2", 384)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if e.Dim() != 384 {
		t.Fatalf("dim = %d, want 384", e.Dim())
	}

	texts := []string{
		"how do I query the data warehouse for sales figures",        // 0
		"run a SQL query against the warehouse database for revenue", // 1 — near 0
		"a haiku about the ocean at sunset",                          // 2 — far from 0
	}
	vecs := e.Embed(texts)
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors, want %d", len(vecs), len(texts))
	}
	for i, v := range vecs {
		if len(v) != 384 {
			t.Fatalf("vec[%d] len = %d, want 384", i, len(v))
		}
		if n := norm(v); math.Abs(n-1) > 1e-3 {
			t.Fatalf("vec[%d] not L2-normalized: |v| = %f", i, n)
		}
	}

	related := Dot(vecs[0], vecs[1])   // warehouse query vs SQL warehouse query
	unrelated := Dot(vecs[0], vecs[2]) // warehouse query vs ocean haiku
	t.Logf("cos(related)=%.3f  cos(unrelated)=%.3f", related, unrelated)
	if related <= unrelated {
		t.Fatalf("semantic ordering broken: related %.3f !> unrelated %.3f", related, unrelated)
	}
	if related < 0.4 {
		t.Fatalf("related pair too weak (%.3f) — check pooling / output name", related)
	}
}

func norm(v Vector) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}
