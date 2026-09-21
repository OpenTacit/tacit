// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/pkg/feed"
)

// Export writes a channel's complete static federation surface into outDir —
// descriptor, every feed page, and every published technique's canonical
// document — publishable on any static host (GitHub Pages, an S3 bucket, a
// plain nginx). Repeatable: re-export after changes and re-upload.
func (p *Publisher) Export(channel, outDir string) (entries int, err error) {
	d, err := p.Descriptor()
	if err != nil {
		return 0, err
	}
	found := false
	for _, ch := range d.Channels {
		if ch.ID == channel {
			found = true
		}
	}
	if !found {
		return 0, fmt.Errorf("channel %q has no published techniques (set `channels: [%s]` in technique frontmatter or POST /v1/admin/publish)", channel, channel)
	}
	// the exported descriptor lists only the exported channel
	kept := d.Channels[:0]
	for _, ch := range d.Channels {
		if ch.ID == channel {
			kept = append(kept, ch)
		}
	}
	d.Channels = kept

	if err := writeJSON(filepath.Join(outDir, ".well-known", "tacit.json"), d); err != nil {
		return 0, err
	}

	pageCursor := ""
	page := 0
	for {
		doc, err := p.Feed(channel, pageCursor)
		if err != nil {
			return entries, err
		}
		name := "feed.json"
		if page > 0 {
			name = fmt.Sprintf("feed.page-%d.json", page)
		}
		// rewrite next_page to the static naming scheme
		if doc.NextPage != "" {
			next, _ := url.Parse(doc.NextPage)
			cursor := next.Query().Get("page")
			doc.NextPage = strings.TrimRight(p.BaseURL, "/") + "/f/" + channel +
				fmt.Sprintf("/feed.page-%d.json", page+1)
			pageCursor = cursor
		}
		if err := writeJSON(filepath.Join(outDir, "f", channel, name), doc); err != nil {
			return entries, err
		}
		for _, e := range doc.Entries {
			entries++
			if e.Kind != feed.KindTechnique || e.Technique == nil {
				continue
			}
			// The id becomes a file name below, and federated ids are
			// provider-supplied: one carrying a path separator or ".." could
			// write outside outDir. Refuse rather than export a traversal.
			if !safeExportID(e.Technique.ID) {
				return entries, fmt.Errorf("technique id %q is not a safe file name; refusing to export it", e.Technique.ID)
			}
			md := RenderTechniqueMarkdown(*e.Technique)
			path := filepath.Join(outDir, "f", "techniques", e.Technique.ID+".md")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return entries, err
			}
			if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
				return entries, err
			}
		}
		if doc.NextPage == "" {
			return entries, nil
		}
		page++
	}
}

// safeExportID reports whether a technique id can be used as a single path
// element under the export tree: non-empty, no path separators, no "..".
func safeExportID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..")
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsx.WriteJSONAtomic(path, v, 0o644)
}
