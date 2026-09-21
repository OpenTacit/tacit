// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package product_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/product"
)

// cmd/tacit has had this guard since the rename, scoped to the three command
// directories. internal/ never had one, and it showed: on 2026-09-16 there were
// thirty-three string literals under internal/ spelling the product "Tacit" —
// the ◆ heading on every suggestion a member sees, the @mention relay, the
// nudges, eight MCP tool descriptions and both LLM system prompts. All of them
// named a product that has not been called that since 2026-09-14, and nothing
// renamed them because nothing routed them through Rename.
//
// Compose with product.Name, or send a block of authored prose out through
// product.Rename. Either way the name arrives from one place.
func TestNoInternalPackageHardcodesTheProductName(t *testing.T) {
	// The word on its own. A neighbouring letter, digit, underscore, hyphen or
	// dot binds it to an identifier — tacit_search, X-Tacit-Key, TACIT_API_KEY,
	// tacit.zone — none of which are the mark, and all of which must survive
	// verbatim. rename_test.go holds the same boundary.
	bare := regexp.MustCompile(`(^|[^A-Za-z0-9_.\-/])` + regexp.QuoteMeta(product.Authored) + `([^A-Za-z0-9_.\-]|$)`)

	err := filepath.Walk(filepath.Join("..", ".."), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			// hack/ is developer tooling, not a surface a member reads — and
			// licensecheck legitimately holds "The OpenTacit Authors", which is
			// the copyright holder rather than a label on the product.
			case ".git", "node_modules", "dist", "onnx", "docs", "techniques", "plugins", "testdata", "hack":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// This package defines the name; naming it here is the point.
		if strings.Contains(filepath.ToSlash(path), "internal/product/") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			code := line
			if c := strings.Index(code, "//"); c >= 0 {
				code = code[:c] // a comment may say the old name; prose may not
			}
			if !strings.Contains(code, `"`) && !strings.Contains(code, "`") {
				continue
			}
			if bare.MatchString(code) {
				t.Errorf("%s:%d hardcodes the product name in a string literal.\n"+
					"Compose with product.Name(), or send the block out through product.Rename:\n\t%s",
					path, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
