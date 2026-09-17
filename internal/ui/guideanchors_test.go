// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A heading's text is its anchor. So renaming the product in a heading moves
// every link that pointed at it, and the link still looks right — it is a
// plausible slug in a file that exists, and it lands the reader at the top of
// the page instead of the section. Nothing errors.
//
// That is exactly what happened on 2026-09-16: authoring the guide with
// "OpenTacit" rather than "Tacit" moved twelve anchors and broke six links,
// four of them on one page. This walks the guide and holds every intra-guide
// anchor to a heading that actually exists.
func TestGuideAnchorsResolve(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "user-guide")
	anchors := map[string]map[string]bool{}
	var files []string
	if err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		files = append(files, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		set := map[string]bool{}
		for _, m := range headingRe.FindAllStringSubmatch(string(b), -1) {
			set[slugify(m[1])] = true
		}
		anchors[filepath.Clean(p)] = set
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no guide pages under %s", root)
	}

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range anchorLinkRe.FindAllStringSubmatch(string(b), -1) {
			rel, anchor := m[1], m[2]
			target := filepath.Clean(f)
			if rel != "" {
				target = filepath.Clean(filepath.Join(filepath.Dir(f), rel))
			}
			set, ok := anchors[target]
			if !ok {
				continue // a link out of the guide; the dead-link sweep covers those
			}
			if !set[anchor] {
				t.Errorf("%s links to %s#%s, and that page has no such heading",
					f, rel, anchor)
			}
		}
	}
}

var (
	headingRe    = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	anchorLinkRe = regexp.MustCompile(`\]\(([^)]*?)#([a-z0-9-]+)\)`)
	slugStrip    = regexp.MustCompile("[`*_\\[\\]()]")
	slugDrop     = regexp.MustCompile(`[^\w\s-]`)
	slugSpace    = regexp.MustCompile(`\s+`)
)

// slugify derives a heading's anchor the way the renderer does: lowercase, drop
// punctuation and inline markup, spaces become hyphens.
func slugify(text string) string {
	s := strings.ToLower(strings.TrimSpace(text))
	s = slugStrip.ReplaceAllString(s, "")
	s = slugDrop.ReplaceAllString(s, "")
	s = slugSpace.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
