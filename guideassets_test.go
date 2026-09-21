// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package tacit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A single-member registry opens on the user guide, and an installed binary has
// no checkout to read one from — so this is the first screen of the product for
// everybody who followed the install instructions.
func TestGuideMaterializesForABinaryWithNoCheckout(t *testing.T) {
	dir := t.TempDir()
	root, err := MaterializeGuide(dir, "v1")
	if err != nil {
		t.Fatalf("MaterializeGuide: %v", err)
	}
	if !GuideOnDisk(root) {
		t.Fatalf("no user guide under %s after materializing", root)
	}
	index, err := os.ReadFile(filepath.Join(root, "user-guide", "index.md"))
	if err != nil {
		t.Fatalf("reading the index: %v", err)
	}
	if !strings.Contains(string(index), "Where to start") {
		t.Error("the materialized index is not the guide's index")
	}
	// The chapters the guide's own "where to start" sends a new reader to.
	for _, rel := range []string{
		"user-guide/10-get-started/01-tacit-at-a-glance.md",
		"user-guide/10-get-started/02-set-up-a-registry.md",
		"user-guide/10-get-started/03-join-your-organization.md",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	// The screenshots the chapters embed. A guide of broken images is a worse
	// first screen than no guide.
	shots, err := os.ReadDir(filepath.Join(root, "user-guide", "images"))
	if err != nil {
		t.Fatalf("reading images: %v", err)
	}
	if len(shots) < 10 {
		t.Errorf("only %d image(s) materialized", len(shots))
	}
}

// The stamp is what keeps `tacit serve` from rewriting four megabytes on every
// start, and what makes an upgraded binary replace a stale chapter.
func TestGuideRewritesOnlyWhenTheBuildChanges(t *testing.T) {
	dir := t.TempDir()
	root, err := MaterializeGuide(dir, "v1")
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(root, "user-guide", "index.md")
	if err := os.WriteFile(index, []byte("clobbered"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := MaterializeGuide(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(index); string(raw) != "clobbered" {
		t.Error("same build rewrote the tree; the stamp is not being read")
	}

	if _, err := MaterializeGuide(dir, "v2"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(index); string(raw) == "clobbered" {
		t.Error("a new build left the old guide in place")
	}
}

// A checkout and the container image both have the real tree, and it is more
// current than a binary built from it.
func TestGuideOnDiskSeesACheckout(t *testing.T) {
	if !GuideOnDisk("docs") {
		t.Error("the repo's own docs/ was not recognised as holding a guide")
	}
	if GuideOnDisk(t.TempDir()) {
		t.Error("an empty directory was reported as holding a guide")
	}
}
