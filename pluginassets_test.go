// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package tacit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializePluginsRewritesBinaryPath(t *testing.T) {
	dir := t.TempDir()
	n, err := MaterializePlugins(dir, "/opt/tacit/bin/tacit")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no plugin files materialized")
	}
	// Dot-directories must survive the embed (manifests live there).
	for _, p := range []string{
		".claude-plugin/marketplace.json",
		"claude-code/.claude-plugin/plugin.json",
		"claude-code/hooks/hooks.json",
		"claude-code/.mcp.json",
		"pi/package.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "claude-code", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), binPlaceholder) {
		t.Errorf("hooks.json still contains the unsubstituted binary placeholder")
	}
	if !strings.Contains(string(raw), "/opt/tacit/bin/tacit hook-relay claude-code") {
		t.Errorf("hooks.json does not reference the substituted binary path:\n%s", raw)
	}
	// No file in the materialized tree may leak the placeholder — every text
	// extension must be covered by substitutable().
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), binPlaceholder) {
			t.Errorf("%s still contains the binary placeholder", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMaterializeSeedTechniquesRefusesExistingDir(t *testing.T) {
	dir := t.TempDir()
	n, err := MaterializeSeedTechniques(filepath.Join(dir, "techniques"))
	if err != nil || n == 0 {
		t.Fatalf("first materialization: n=%d err=%v", n, err)
	}
	// Existing directory = operator state: a re-run must write nothing, so a
	// deleted technique can never resurrect.
	if err := os.Remove(filepath.Join(dir, "techniques", "ask-for-a-diagram.md")); err != nil {
		t.Fatal(err)
	}
	n, err = MaterializeSeedTechniques(filepath.Join(dir, "techniques"))
	if err != nil || n != 0 {
		t.Fatalf("re-run: n=%d err=%v, want 0 nil", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "techniques", "ask-for-a-diagram.md")); !os.IsNotExist(err) {
		t.Error("deleted technique resurrected by re-run")
	}
}

// The version has to move when the binary PATH moves, not only when the
// embedded tree changes.
//
// This is the bug the stamp exists to prevent, one input short of being
// prevented. The path is substituted after the tree is hashed, so two installs
// from one build at two locations carried the same version and different
// content; `claude plugin update` compares versions, found them equal, and went
// on serving a cached plugin whose hooks ran a binary in a mktemp directory
// that install.sh had already deleted. Running `tacit connect` again could not
// fix it, because connect did everything right and the update was a no-op.
func TestPluginTreeVersionMovesWithTheBinaryPath(t *testing.T) {
	home := PluginTreeVersion("/home/someone/go/bin/tacit")
	temp := PluginTreeVersion("/tmp/tmp.nbIg3y1V0i/tacit")
	if home == temp {
		t.Fatalf("two binary paths stamped the same version (%s) — an install that moved the binary can never reach an installed plugin", home)
	}
	// And it is still stable for one path: a version that changed on every run
	// would reinstall the plugin on every connect.
	if again := PluginTreeVersion("/home/someone/go/bin/tacit"); again != home {
		t.Fatalf("the version is not stable for one path: %s then %s", home, again)
	}
	if !strings.HasPrefix(home, "0.1.1-") {
		t.Fatalf("version lost its base: %s", home)
	}
}

// The materialized manifests carry that version, in both places Claude Code
// reads it: the plugin's own manifest, and the marketplace entry `plugin
// update` actually compares.
func TestMaterializedManifestsCarryThePathStampedVersion(t *testing.T) {
	dir := t.TempDir()
	const bin = "/opt/tacit/bin/tacit"
	if _, err := MaterializePlugins(dir, bin); err != nil {
		t.Fatal(err)
	}
	want := PluginTreeVersion(bin)
	for _, p := range []string{
		".claude-plugin/marketplace.json",
		"claude-code/.claude-plugin/plugin.json",
	} {
		raw, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"version": "`+want+`"`) {
			t.Errorf("%s does not carry %s:\n%s", p, want, raw)
		}
	}
	// A second materialization to a different path must declare a different
	// version, or the installed copy keeps the old binary.
	other := t.TempDir()
	if _, err := MaterializePlugins(other, "/tmp/tmp.XXXX/tacit"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(other, ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"version": "`+want+`"`) {
		t.Errorf("a different binary path produced the same declared version:\n%s", raw)
	}
}
