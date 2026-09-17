// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAgentEnvFallback: `tacit setup` writes agent.env; Load falls back to it
// for anything the environment doesn't set, and the environment still wins.
func TestAgentEnvFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACIT_REGISTRY_URL", "")
	t.Setenv("TACIT_API_KEY", "")

	path := AgentEnvPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "# written by tacit setup\nTACIT_REGISTRY_URL=http://reg:8080\nTACIT_API_KEY=\"k1\"\n\nbogus line\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg.RegistryURL != "http://reg:8080" || cfg.RegistryKey != "k1" {
		t.Fatalf("file fallback not applied: url=%q key=%q", cfg.RegistryURL, cfg.RegistryKey)
	}

	t.Setenv("TACIT_REGISTRY_URL", "http://env:1")
	if got := Load().RegistryURL; got != "http://env:1" {
		t.Fatalf("environment must override the file: %q", got)
	}

	// no file -> defaults, not an error
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TACIT_REGISTRY_URL", "")
	if got := Load().RegistryURL; got != "http://127.0.0.1:8080" {
		t.Fatalf("default lost without file: %q", got)
	}
}

func TestSessionSaltPrecedenceAndSketchFallback(t *testing.T) {
	for _, key := range []string{"TACIT_SESSION_SALT", "TACIT_SKETCH_SALT"} {
		t.Setenv(key, "")
	}
	t.Setenv("HOME", t.TempDir())
	if cfg := Load(); cfg.SessionSalt != "" || cfg.SketchSalt != "tacit" {
		t.Fatalf("unset salts: session=%q sketch=%q", cfg.SessionSalt, cfg.SketchSalt)
	}
	t.Setenv("TACIT_SKETCH_SALT", "old-tacit")
	if cfg := Load(); cfg.SessionSalt != "old-tacit" || cfg.SketchSalt != "old-tacit" {
		t.Fatalf("sketch-salt fallback: %+v", cfg)
	}
	t.Setenv("TACIT_SESSION_SALT", "new-tacit")
	if cfg := Load(); cfg.SessionSalt != "new-tacit" || cfg.SketchSalt != "new-tacit" {
		t.Fatalf("canonical precedence: %+v", cfg)
	}
}

// The state directory is where member-local state goes, and it collects what
// an earlier build scattered in $HOME on the way past. A member who already
// has a session history keeps it; nobody starts over for a decision they had
// no part in.
func TestDefaultStateDirAdoptsStrayHomeFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	for _, name := range strayStateFiles {
		if err := os.WriteFile(filepath.Join(home, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := defaultStateDir()
	if dir == "" {
		t.Fatal("no state directory")
	}
	for _, name := range strayStateFiles {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s did not move into the state directory: %v", name, err)
		}
		if string(raw) != name+"\n" {
			t.Fatalf("%s arrived with the wrong content: %q", name, raw)
		}
		if _, err := os.Stat(filepath.Join(home, name)); err == nil {
			t.Errorf("%s is still in the home directory", name)
		}
	}
	// Running again is a no-op, and the second pass must not clobber a live
	// file with a stray one that reappeared.
	if err := os.WriteFile(filepath.Join(home, "sessions.jsonl"), []byte("stray\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defaultStateDir()
	raw, _ := os.ReadFile(filepath.Join(dir, "sessions.jsonl"))
	if string(raw) != "sessions.jsonl\n" {
		t.Fatalf("a second pass overwrote the live file: %q", raw)
	}
}

// The usage log and the technique memory stay where they have always been.
// Moving them would be tidier and would orphan every member's history for the
// sake of files `ls` does not show.
func TestEstablishedDotfilesStayInPlace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := defaultUsageLogPath(); got != filepath.Join(home, ".tacit-usage-log.jsonl") {
		t.Errorf("usage log moved to %s", got)
	}
	if got := defaultTechniqueMemoryPath(); got != filepath.Join(home, ".tacit-technique-memory.json") {
		t.Errorf("technique memory moved to %s", got)
	}
}
