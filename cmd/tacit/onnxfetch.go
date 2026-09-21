// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit onnx-fetch — download the pinned semantic-retrieval artifacts into a
// directory. The Docker image build's fetch stage (docs/distribution/
// docker-plan.md) calls this so the image and `tacit init` share one pin
// table (internal/onnxassets) and can never drift. Hidden from usage(): an
// operator wants `tacit init`, which also writes the env wiring.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/opentacit/tacit/internal/onnxassets"
)

func cmdOnnxFetch(args []string) int {
	fs := flag.NewFlagSet("onnx-fetch", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory to fetch into (required)")
	platform := fs.String("platform", runtime.GOOS+"/"+runtime.GOARCH,
		"GOOS/GOARCH of the runtime library to fetch")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "onnx-fetch: set --dir")
		return 2
	}
	goos, goarch, ok := strings.Cut(*platform, "/")
	rt, found := onnxassets.RuntimeLib(goos, goarch)
	if !ok || !found {
		fmt.Fprintf(os.Stderr, "onnx-fetch: no onnxruntime build for %q\n", *platform)
		return 1
	}
	modelDir := filepath.Join(*dir, "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// The library gets a version-free name so the image's TACIT_ONNX_LIB (and
	// any caller's path) survives a pin bump; the file content is still the
	// digest-verified versioned build.
	libName := "libonnxruntime.so"
	if goos == "darwin" {
		libName = "libonnxruntime.dylib"
	}
	if err := onnxassets.Fetch(rt, filepath.Join(*dir, libName)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// The runtime's licence and its third-party notices travel with the
	// library, always, because redistributing the binary without them is a
	// licence violation. They land beside it so that whatever copies the
	// library copies these too.
	for _, n := range onnxassets.RuntimeNotices {
		if err := onnxassets.Fetch(n, filepath.Join(*dir, n.Out)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	for _, m := range onnxassets.ModelFiles {
		if err := onnxassets.Fetch(m, filepath.Join(modelDir, m.Out)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	fmt.Printf("fetched %s runtime (+ its licence and third-party notices) and the %s model into %s\n",
		*platform, onnxassets.ModelName, *dir)
	return 0
}
