// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package tacit holds the repo-root assets that ship inside the binary.
//
// The starter techniques live in techniques/ so the repo checkout serves them directly
// (the dev loop and the original deployment read that directory), and are
// embedded here so an installed binary — which has no checkout — can seed a
// fresh registry with the same set.
package tacit

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed techniques/*.md
var seedTechniques embed.FS

// MaterializeSeedTechniques writes the embedded starter techniques into dir, creating
// it. It refuses to touch a directory that already exists: techniques are operator
// state once served (edited, deleted, added to), and a re-run must never
// resurrect a technique the operator removed.
func MaterializeSeedTechniques(dir string) (int, error) {
	if _, err := os.Stat(dir); err == nil {
		return 0, nil
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	entries, err := fs.ReadDir(seedTechniques, "techniques")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		raw, err := fs.ReadFile(seedTechniques, "techniques/"+e.Name())
		if err != nil {
			return n, err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), raw, 0o644); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
