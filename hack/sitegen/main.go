// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// sitegen writes the project page and its assets to a directory, for any static
// host to serve at a zone's apex.
//
//	go run ./hack/sitegen -o dist/site
//
// The bundle is built, never committed: it is a rendering of internal/ui, and a
// checked-in copy would be one more thing to remember to regenerate. `make site`
// is the entry point. Uploading what it produced is the host's business and is
// not in this repository.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opentacit/tacit/internal/ui"
)

func main() {
	out := flag.String("o", "dist/site", "directory to write the bundle into")
	quiet := flag.Bool("q", false, "print nothing on success")
	flag.Parse()

	files, err := ui.StaticSite()
	if err != nil {
		fail(err)
	}
	// Cleared rather than merged: a file that stopped being part of the bundle —
	// a retired screenshot, a renamed asset — would otherwise stay on disk and be
	// uploaded forever after, and the deploy is a directory upload.
	if err := os.RemoveAll(*out); err != nil {
		fail(err)
	}
	var bytes int
	for _, f := range files {
		dst := filepath.Join(*out, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(dst, f.Data, 0o644); err != nil {
			fail(err)
		}
		bytes += len(f.Data)
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "sitegen: wrote %d files (%d KB) to %s\n",
			len(files), bytes/1024, *out)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sitegen:", err)
	os.Exit(1)
}
