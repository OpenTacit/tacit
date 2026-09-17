// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package pkg holds no code. It exists for one test: the README's layout block
// names every public package, and nothing was checking that it still did.
package pkg

import (
	"os"
	"strings"
	"testing"
)

// The README calls pkg/ "the PUBLIC packages (the open standard)" and lists them,
// then promises something about the set a few lines down. A package missing from
// the list is covered by that promise without a reader ever seeing it named —
// which is how three of them ended up unlisted: intelligence for months, then two
// more added during the launch split.
//
// The check is one-directional on purpose. A list entry with no directory would be
// a dead line in a README, caught by reading it; a directory with no entry is the
// silent one, and it is what this catches.
func TestReadmeNamesEveryPublicPackage(t *testing.T) {
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !strings.Contains(string(readme), e.Name()) {
			t.Errorf("pkg/%s is a public package the README's layout never names, "+
				"so the promise below that list covers a package no reader can find in it",
				e.Name())
		}
	}
}
