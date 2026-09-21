// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package schemas embeds OpenTacit's normative interface definitions — the
// JSON Schemas (2020-12) for every document contract and the OpenAPI 3.1
// description of the HTTP surface — so the binary can serve them
// (GET /v1/schemas/{name}, GET /v1/openapi.yaml) and tests can enforce them
// without filesystem assumptions. See docs/design/api-standard.md.
package schemas

import (
	"embed"
	"sort"
	"strings"
)

//go:embed *.schema.json openapi.yaml
var fs embed.FS

// OpenAPI returns the OpenAPI 3.1 document.
func OpenAPI() []byte {
	raw, _ := fs.ReadFile("openapi.yaml")
	return raw
}

// Get returns one embedded JSON Schema by filename (e.g.
// "technique.schema.json"); ok=false for anything unknown.
func Get(name string) (raw []byte, ok bool) {
	if !strings.HasSuffix(name, ".schema.json") || strings.Contains(name, "/") {
		return nil, false
	}
	raw, err := fs.ReadFile(name)
	return raw, err == nil
}

// Names lists the embedded schema filenames, sorted.
func Names() []string {
	entries, _ := fs.ReadDir(".")
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".schema.json") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
