// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package tacit

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The harness integration packages ship inside the binary for the same reason
// the starter techniques do: an installed binary has no checkout to copy them
// from. `tacit connect` materializes this tree and wires each harness to it.
//
//go:embed all:plugins
var pluginAssets embed.FS

// InstallScript is install.sh, embedded so the registry can serve a
// personalized join script (GET /join/{token} appends a `tacit join` step to
// exactly what `curl | sh` would have run).
//
//go:embed install.sh
var InstallScript string

// binPlaceholder is the token baked into the plugin sources wherever the
// tacit binary must be named absolutely (hooks run under an isolated PATH).
// Materialization rewrites it to the installing member's binary — the
// checked-in tree is a template, never usable as a marketplace directly.
const binPlaceholder = "{{TACIT_BIN}}"

// substitutable reports whether a plugin file is text that may reference the
// tacit binary by path (everything in the tree except icons/etc., which the
// tree currently has none of — the extension list is the allowlist anyway).
func substitutable(name string) bool {
	switch filepath.Ext(name) {
	case ".json", ".jsonc", ".ts", ".toml", ".md", ".example", ".txt":
		return true
	}
	return false
}

// PluginTreeVersion derives the version stamped into the materialized
// .claude-plugin/plugin.json: a patch bump on the checked-in base plus a
// content hash of the whole embedded tree. Claude Code's `plugin update`
// compares manifest versions and re-copies only when they differ — with the
// checked-in static version, a new binary's plugin content could NEVER reach
// an installed plugin. The content hash makes "the tree changed" and "the
// version changed" the same fact.
//
// binPath is part of the hash, and has to be. It is not in the embedded tree —
// it is substituted for the placeholder on the way out — so hashing the tree
// alone made two installs from the same build but DIFFERENT binary locations
// carry the same version and different content. `plugin update` then compared
// versions, found them equal, and kept serving the cached copy. That is how a
// member ends up with an installed plugin whose hooks run a binary in a
// mktemp directory that install.sh deleted minutes later, and why running
// `tacit connect` again could not fix it: connect did everything right, and
// the update was a no-op every time. The path is what changed, so the path
// belongs in the fact that says something changed.
func PluginTreeVersion(binPath string) string {
	var paths []string
	_ = fs.WalkDir(pluginAssets, "plugins", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		raw, _ := fs.ReadFile(pluginAssets, p)
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(raw))
		h.Write(raw)
	}
	fmt.Fprintf(h, "bin\x00%s\x00", binPath)
	return "0.1.1-" + hex.EncodeToString(h.Sum(nil))[:12]
}

// MaterializePlugins writes the embedded plugin packages under dir, rewriting
// the baked-in binary path to binPath and stamping the plugin manifest with
// the content-derived version (PluginTreeVersion). Unlike techniques, this tree is
// tacit-owned, versioned with the binary, and holds no operator state — the
// destination is removed and rewritten on every call so renamed or deleted
// files never linger.
func MaterializePlugins(dir, binPath string) (int, error) {
	if err := os.RemoveAll(dir); err != nil {
		return 0, err
	}
	version := PluginTreeVersion(binPath)
	n := 0
	err := fs.WalkDir(pluginAssets, "plugins", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("plugins", path)
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		raw, err := fs.ReadFile(pluginAssets, path)
		if err != nil {
			return err
		}
		if binPath != "" && substitutable(path) {
			raw = []byte(strings.ReplaceAll(string(raw), binPlaceholder, binPath))
		}
		// Both manifests carry a version and BOTH must move with the content:
		// the plugin's own plugin.json, and the marketplace.json entries —
		// `claude plugin update` compares the MARKETPLACE's declared version,
		// so a static version there means updates never flow.
		if strings.Contains(path, ".claude-plugin") &&
			(d.Name() == "plugin.json" || d.Name() == "marketplace.json") {
			raw = []byte(strings.ReplaceAll(string(raw), `"version": "0.1.0"`, `"version": "`+version+`"`))
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}
