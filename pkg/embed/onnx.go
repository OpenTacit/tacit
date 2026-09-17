// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

//go:build onnx

// In-process ONNX embedder — a semantic sentence encoder that runs entirely on
// this machine with no external service and no per-call network. It loads the
// ONNX Runtime shared library at runtime (dlopen) and runs a sentence-transformer
// model (default all-MiniLM-L6-v2, 384-dim) over WordPiece tokens, mean-pooling
// the hidden states. Build with `-tags onnx`; see docs/design/embedder-onnx.md.
//
// Runtime inputs (all resolved from the environment so the binary stays
// artifact-free):
//   - TACIT_ONNX_LIB        path to libonnxruntime.so (required)
//   - TACIT_ONNX_MODEL_DIR  dir holding model.onnx and vocab.txt (required)
//   - TACIT_ONNX_INPUTS     model input names, in order (default
//     "input_ids,attention_mask,token_type_ids")
//   - TACIT_ONNX_OUTPUT     hidden-state output name (default "last_hidden_state")
//   - TACIT_ONNX_MAXLEN     max sequence length (default 256)
package embed

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

func init() { onnxFactory = newONNX }

// The ONNX Runtime environment is process-global; initialize it once.
var (
	ortOnce sync.Once
	ortErr  error
)

type onnxEmbedder struct {
	mu      sync.Mutex // serializes Run; also guards session lifetime
	session *ort.DynamicAdvancedSession
	tok     *wordPiece
	dim     int
	maxLen  int
	modelID string
	inNames []string
	outName string
}

func newONNX(model string, dim int) (Embedder, error) {
	libPath := os.Getenv("TACIT_ONNX_LIB")
	if libPath == "" {
		return nil, fmt.Errorf("onnx embedder %q: set TACIT_ONNX_LIB to libonnxruntime.so "+
			"(the tagged build links a local ONNX runtime)", model)
	}
	modelDir := os.Getenv("TACIT_ONNX_MODEL_DIR")
	if modelDir == "" {
		return nil, fmt.Errorf("onnx embedder %q: set TACIT_ONNX_MODEL_DIR "+
			"(must contain model.onnx and vocab.txt)", model)
	}
	tok, err := loadWordPiece(filepath.Join(modelDir, "vocab.txt"))
	if err != nil {
		return nil, fmt.Errorf("onnx embedder: %w", err)
	}
	ortOnce.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		ortErr = ort.InitializeEnvironment()
	})
	if ortErr != nil {
		return nil, fmt.Errorf("onnx embedder: initialize runtime (%s): %w", libPath, ortErr)
	}
	inNames := splitEnv("TACIT_ONNX_INPUTS", "input_ids,attention_mask,token_type_ids")
	outName := envDefault("TACIT_ONNX_OUTPUT", "last_hidden_state")
	maxLen := atoiDefault("TACIT_ONNX_MAXLEN", 256)

	modelPath := filepath.Join(modelDir, "model.onnx")
	session, err := ort.NewDynamicAdvancedSession(modelPath, inNames, []string{outName}, nil)
	if err != nil {
		return nil, fmt.Errorf("onnx embedder: open session %s: %w", modelPath, err)
	}
	return &onnxEmbedder{
		session: session, tok: tok, dim: dim, maxLen: maxLen,
		modelID: model, inNames: inNames, outName: outName,
	}, nil
}

func (e *onnxEmbedder) ModelID() string { return e.modelID }
func (e *onnxEmbedder) Dim() int        { return e.dim }

// Embed runs the model once per text (batch of 1, no padding). A failed embed
// degrades to a zero vector rather than crashing the caller — that text simply
// won't match well, and indexing/queries keep working.
func (e *onnxEmbedder) Embed(texts []string) []Vector {
	out := make([]Vector, len(texts))
	for i, t := range texts {
		v, err := e.embedOne(t)
		if err != nil {
			v = make(Vector, e.dim)
		}
		out[i] = v
	}
	return out
}

func (e *onnxEmbedder) embedOne(text string) (Vector, error) {
	ids, mask := e.tok.encode(text, e.maxLen)
	seq := int64(len(ids))
	tokType := make([]int64, len(ids)) // single-sequence: all zeros
	shape := ort.NewShape(1, seq)

	inputs := make([]ort.Value, 0, len(e.inNames))
	cleanup := func() {
		for _, v := range inputs {
			v.Destroy()
		}
	}
	for _, name := range e.inNames {
		var data []int64
		switch name {
		case "input_ids":
			data = ids
		case "attention_mask":
			data = mask
		case "token_type_ids":
			data = tokType
		default:
			cleanup()
			return nil, fmt.Errorf("unsupported onnx input %q (TACIT_ONNX_INPUTS)", name)
		}
		t, err := ort.NewTensor(shape, data)
		if err != nil {
			cleanup()
			return nil, err
		}
		inputs = append(inputs, t)
	}
	defer cleanup()

	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, seq, int64(e.dim)))
	if err != nil {
		return nil, err
	}
	defer output.Destroy()

	e.mu.Lock()
	err = e.session.Run(inputs, []ort.Value{output})
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return normalize(meanPool(output.GetData(), mask, e.dim)), nil
}

func envDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func splitEnv(key, def string) []string {
	raw := envDefault(key, def)
	parts := strings.Split(raw, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func atoiDefault(key string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && v > 0 {
		return v
	}
	return def
}
