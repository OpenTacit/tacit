// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/product"
)

// The CLI is prose a member reads, so it must say what the product is called
// here — not what it was called when the binary was built. Everything the
// registry draws already asks internal/product; the terminal was the last
// surface that did not, and it is the one a member sees before any dashboard.
//
// This walks the source rather than the output because there is no single place
// the CLI prints: several hundred fmt.Print* calls across two dozen files, with
// no writer to wrap. A string literal is where a hardcoded name enters, so a
// string literal is what this refuses.
//
// Two ways to satisfy it, and product.go draws the line between them: compose
// with product.Name(), or — for a block of authored prose like the help text —
// send it out through product.Rename.
func TestNoCommandPrintsAHardcodedProductName(t *testing.T) {
	// The word on its own. A neighbouring letter, digit, underscore, hyphen or
	// dot means it belongs to an identifier — tacit_search, X-Tacit-Key,
	// TACIT_API_KEY, tacit.zone, OpenTacit — none of which are the mark and all
	// of which must survive verbatim. rename_test.go holds the same boundary.
	bare := regexp.MustCompile(`(^|[^A-Za-z0-9_.\-/])` + regexp.QuoteMeta(product.Authored) + `([^A-Za-z0-9_.\-]|$)`)

	roots := []string{".", "../tacit-ingress", "../tacit-genplugins"}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("reading %s: %v", root, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(root, name)
			// Comments are not parsed: they are source a developer reads, and
			// the authored name is the right word for them.
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				// A block handed to product.Rename is authored prose on its way
				// out and is SUPPOSED to carry the authored name — that is the
				// whole mechanism. Skip the subtree rather than the literal, so
				// a concatenation inside the call is covered too.
				if call, ok := n.(*ast.CallExpr); ok && isRenameCall(call) {
					return false
				}
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					s = lit.Value // a raw string with an escape the unquoter dislikes
				}
				if bare.MatchString(s) {
					t.Errorf("%s: a string literal hardcodes %q — compose it with "+
						"product.Name(), or send the block out through product.Rename:\n\t%.120s",
						path, product.Authored, strings.TrimSpace(s))
				}
				return true
			})
		}
	}
}

// isRenameCall reports whether this is product.Rename(...) — matched on the
// selector rather than resolved, which is enough here: nothing else in these
// packages is called Rename, and a false positive would only be another
// package's rename, which wants the same exemption anyway.
func isRenameCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Rename" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "product"
}
