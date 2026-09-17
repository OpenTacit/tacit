// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package tacit

// The user guide ships inside the binary.
//
// A single-member registry opens on the guide — the right landing page for
// somebody who set the thing up ninety seconds ago and has no data yet. The
// dashboard reads it from a directory, defaulting to ./docs, and the release
// tarball contains exactly one file: the binary. So the first screen after
// `curl | sh` and `tacit init` was "Document not found", for every operator who
// did not happen to be standing in a checkout. Only the container shipped the
// guide (Dockerfile: COPY docs /opt/tacit/docs), which is why it survived being
// tested.
//
// Embedded rather than downloaded, for the reason everything else here is: the
// registry fetches nothing at runtime, and a guide that needs the internet is
// not a guide on an isolated network.
//
// Materialized to disk rather than served from the embed, because the docs
// handler is a filesystem reader — path confinement, image pairing, the section
// walk — and re-pointing it at a directory this package wrote costs nothing and
// changes no behaviour. The copy is ours, not the operator's, so a version
// stamp lets an upgraded binary replace it without asking.

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:docs/user-guide
var guideAssets embed.FS

// guideStamp records which build wrote the materialized copy. A binary whose
// stamp does not match rewrites the tree; a matching one does nothing, so
// `tacit serve` is not copying four megabytes on every start.
const guideStamp = ".tacit-guide-version"

// MaterializeGuide writes the embedded guide under dir as docs/user-guide,
// returning the docs root to serve (dir/docs). version stamps the copy.
//
// Existing files are overwritten. The guide is not operator state — nobody
// edits their local copy of the manual — and a stale chapter after an upgrade
// is a support question nobody can answer from what they can see.
func MaterializeGuide(dir, version string) (string, error) {
	root := filepath.Join(dir, "docs")
	stamp := filepath.Join(root, guideStamp)
	if raw, err := os.ReadFile(stamp); err == nil && string(raw) == version {
		return root, nil
	}
	err := fs.WalkDir(guideAssets, "docs/user-guide", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		raw, err := guideAssets.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o644)
	})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(stamp, []byte(version), 0o644); err != nil {
		return "", err
	}
	return root, nil
}

// GuideOnDisk reports whether a docs directory already holds a user guide — the
// checkout the developer works in, or the tree the container image copies. Both
// are more current than anything embedded in a binary built from them, so both
// win.
func GuideOnDisk(docsDir string) bool {
	_, err := os.Stat(filepath.Join(docsDir, "user-guide", "index.md"))
	return err == nil
}
