// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `tacit doctor` printed "all checks passed" on a machine where the key was
// good, the local agent was up, and no harness was wired to either. That is
// every machine where `tacit connect` ended in a todo — which is what it does
// for Claude Code whenever the claude CLI is not on PATH. The member's one
// verification step said yes while nothing could deliver anything.
func TestDoctorFailsWhenAnInstalledHarnessIsNotWired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "share"))
	t.Setenv("PATH", filepath.Join(home, "empty-bin"))
	// Codex is present and its config has no relay in it — the shape connect
	// leaves behind when a member declines the edit or edits the file later.
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"), "model = \"gpt-5\"\n")

	var buf bytes.Buffer
	c := &checker{w: &buf}
	doctorAllWiring(c)

	if c.fails == 0 {
		t.Fatalf("no failure reported for an unwired harness; output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "codex") {
		t.Errorf("the report does not name the harness:\n%s", buf.String())
	}
}

// The wired case has to stay quiet, or the check is noise every member learns
// to skip.
func TestDoctorPassesWhenTheHarnessIsWired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "share"))
	t.Setenv("PATH", filepath.Join(home, "empty-bin"))
	bin := filepath.Join(home, "bin", "tacit")
	mustWrite(t, bin, "#!/bin/sh\n")
	mustWrite(t, filepath.Join(home, ".codex", "config.toml"),
		"[hooks]\ncommand = \""+bin+" hook-relay codex\"\n")

	var buf bytes.Buffer
	c := &checker{w: &buf}
	doctorAllWiring(c)

	if c.fails != 0 {
		t.Errorf("%d failure(s) on a wired harness:\n%s", c.fails, buf.String())
	}
}

// A machine with no AI tool on it is a CI box or a server, and the right report
// is a note, not a failure.
func TestDoctorSaysSoWhenThereIsNoHarnessAtAll(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "share"))
	t.Setenv("PATH", filepath.Join(home, "empty-bin"))

	var buf bytes.Buffer
	c := &checker{w: &buf}
	doctorAllWiring(c)

	if c.fails != 0 {
		t.Errorf("failed on a machine with no harness:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "no supported AI tool") {
		t.Errorf("did not say why there is nothing to check:\n%s", buf.String())
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
