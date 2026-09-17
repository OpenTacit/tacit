// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !onnx

package embed

import "fmt"

// In a default (pure-Go) build, an "onnx/..." model id is recognized but not
// runnable — point the operator at the tagged build instead of failing with a
// bare "unknown model". The real constructor lives in onnx.go (//go:build onnx).
func init() {
	onnxFactory = func(model string, _ int) (Embedder, error) {
		return nil, fmt.Errorf("embedder %q needs ONNX support: rebuild tacit with -tags onnx "+
			"(the tagged build links a local ONNX runtime)", model)
	}
}
