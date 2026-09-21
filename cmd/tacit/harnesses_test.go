// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The names and their order are what members read in `--harness` help, in the
// unknown-harness error, and in the order connect wires and disconnect
// unwires. Deriving them from the table must not change a byte of either list.
func TestHarnessNamesMatchTheShippedList(t *testing.T) {
	const pipes = "claude-code|codex|gemini|copilot|cursor|amp|pi|omp|opencode"
	const commas = "claude-code, codex, gemini, copilot, cursor, amp, pi, omp, opencode"
	if got := harnessNames(); got != pipes {
		t.Errorf("harnessNames() = %q, want %q", got, pipes)
	}
	if got := harnessNamesProse(); got != commas {
		t.Errorf("harnessNamesProse() = %q, want %q", got, commas)
	}
}

// A harness missing one of its four roles connects but cannot be unwired or
// doctored — the failure the table exists to make impossible.
func TestEveryHarnessHasAllFourRoles(t *testing.T) {
	seen := map[string]bool{}
	for _, h := range harnesses {
		if h.name == "" {
			t.Fatal("a harness has no name")
		}
		if seen[h.name] {
			t.Errorf("%s appears twice", h.name)
		}
		seen[h.name] = true
		if h.installed == nil {
			t.Errorf("%s has no installed check", h.name)
		}
		if h.connect == nil {
			t.Errorf("%s has no connect step", h.name)
		}
		if h.undo == nil {
			t.Errorf("%s has no undo step", h.name)
		}
		if h.wiring == nil {
			t.Errorf("%s has no doctor wiring check", h.name)
		}
		if _, ok := findHarness(h.name); !ok {
			t.Errorf("findHarness(%q) does not find it", h.name)
		}
	}
	if _, ok := findHarness("nosuchharness"); ok {
		t.Error("findHarness found a harness that does not exist")
	}
}

// Hop 1 of `tacit doctor --harness cursor` on a machine that ran connect: the
// hooks file names the relay, and the binary it names is there.
func TestDoctorWiringReportsWiredCursor(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin", "tacit")
	writeRelayHooks(t, home, bin)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &checker{}
	out := captureStdout(t, func() { doctorWiring(c, "cursor", home, filepath.Join(home, "plugins")) })
	for _, want := range []string{"cursor hooks wired", "relay binary exists", "park-only"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q\ngot:\n%s", want, out)
		}
	}
	if c.fails != 0 {
		t.Errorf("wired cursor reported %d failure(s):\n%s", c.fails, out)
	}
}

// The moved-binary fault: the wiring is still there, the binary it runs is
// not, and no harness reports an error of its own. Doctor is the only place
// that notices.
func TestDoctorWiringReportsAMissingRelayBinary(t *testing.T) {
	home := t.TempDir()
	writeRelayHooks(t, home, filepath.Join(home, "moved", "tacit"))

	c := &checker{}
	out := captureStdout(t, func() { doctorWiring(c, "cursor", home, filepath.Join(home, "plugins")) })
	if !strings.Contains(out, "no longer exists") {
		t.Errorf("doctor output missing the dangling-binary line\ngot:\n%s", out)
	}
	if c.fails != 1 {
		t.Errorf("dangling relay path reported %d failure(s), want 1:\n%s", c.fails, out)
	}
}

// A name no harness claims checks nothing and says nothing here; doctorHarness
// rejects it before this point.
func TestDoctorWiringIgnoresAnUnknownHarness(t *testing.T) {
	c := &checker{}
	out := captureStdout(t, func() { doctorWiring(c, "nosuchharness", t.TempDir(), t.TempDir()) })
	if out != "" || c.fails != 0 || c.warns != 0 {
		t.Errorf("unknown harness produced output %q (%d fail, %d warn)", out, c.fails, c.warns)
	}
}

// writeRelayHooks lays down the cursor hooks file connect merges, pointing at
// the given binary.
func writeRelayHooks(t *testing.T, home, bin string) {
	t.Helper()
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"version":1,"hooks":{"beforeSubmitPrompt":[{"command":"` + bin + ` hook-relay cursor"}]}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeClaudePlugin lays down a Claude Code plugin cache entry: a versioned
// directory with a hooks.json running bin, and the manifest naming which
// version is installed.
func writeClaudePlugin(t *testing.T, home string, versions map[string]string, installed string) {
	t.Helper()
	for version, bin := range versions {
		dir := filepath.Join(home, ".claude", "plugins", "cache", "tacit", "tacit", version, "hooks")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"` +
			bin + ` hook-relay claude-code"}]}]}}`
		if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"version":2,"plugins":{"tacit@tacit":[{"scope":"user","installPath":"` +
		filepath.Join(home, ".claude", "plugins", "cache", "tacit", "tacit", installed) +
		`","version":"` + installed + `"}]}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		[]byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Claude Code keeps every version it has ever installed and serves exactly
// one. Doctor has to read the manifest to know which — searching the tree
// reported whichever abandoned copy sorted first, and told a member with a
// healthy plugin to run `tacit connect` again, which cannot delete a directory
// Claude Code owns and no longer uses.
func TestDoctorIgnoresAbandonedPluginVersions(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin", "tacit")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The stale copy sorts FIRST, which is what made the old walk find it.
	writeClaudePlugin(t, home, map[string]string{
		"0.1.1-000000000000": "/tmp/tmp.deleted/tacit",
		"0.1.1-zzzzzzzzzzzz": bin,
	}, "0.1.1-zzzzzzzzzzzz")

	c := &checker{}
	out := captureStdout(t, func() {
		doctorWiring(c, "claude-code", home, filepath.Join(home, "plugins"))
	})
	if c.fails != 0 {
		t.Errorf("a healthy plugin reported %d failure(s):\n%s", c.fails, out)
	}
	if strings.Contains(out, "tmp.deleted") {
		t.Errorf("doctor read an abandoned cached version:\n%s", out)
	}
	if !strings.Contains(out, "relay binary exists") {
		t.Errorf("doctor did not check the installed copy:\n%s", out)
	}
}

// And the fault still reports when it is the INSTALLED copy that dangles.
func TestDoctorReportsAMissingBinaryInTheInstalledPlugin(t *testing.T) {
	home := t.TempDir()
	writeClaudePlugin(t, home, map[string]string{
		"0.1.1-000000000000": "/tmp/tmp.deleted/tacit",
	}, "0.1.1-000000000000")

	c := &checker{}
	out := captureStdout(t, func() {
		doctorWiring(c, "claude-code", home, filepath.Join(home, "plugins"))
	})
	if c.fails != 1 || !strings.Contains(out, "no longer exists") {
		t.Errorf("a dangling installed plugin reported %d failure(s):\n%s", c.fails, out)
	}
}

// The TypeScript plugins describe themselves in prose and resolve the binary
// at run time. Doctor used to read the prose — capturing back through an
// opening backtick — and report a binary called "`tacit" that had gone
// missing, on a plugin that was working.
//
// PATH decides which of doctor's two lines this prints, so the test sets PATH
// and checks both. It cannot ask whether a backtick appears anywhere in the
// output: doctor's own remedy reads "run `tacit connect` again", which that
// question answers yes to. What it asks instead is that the line names the
// binary as plain tacit, and that a PATH lookup is never judged as a file.
func TestDoctorReadsPastProseAndPathLookups(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode", "plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "// nothing is listening, fall back to spawning `tacit hook-relay opencode`,\n" +
		"const TACIT_BIN = env([\"TACIT_BIN\"], \"tacit\")\n" +
		"await runProcess(TACIT_BIN, [\"hook-relay\", HARNESS], body)\n"
	if err := os.WriteFile(filepath.Join(dir, "tacit.ts"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	onPath := filepath.Join(home, "bin")
	if err := os.MkdirAll(onPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(onPath, "tacit"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		path  string
		want  string
		fails int
	}{
		{"a developer machine has tacit on PATH", onPath, "relay binary on PATH (tacit → ", 0},
		{"a bare CI container does not", filepath.Join(home, "nothing"), "runs tacit, which is not on PATH", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", tc.path)
			c := &checker{}
			out := captureStdout(t, func() {
				doctorWiring(c, "opencode", home, filepath.Join(home, "plugins"))
			})
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q in doctor's report:\n%s", tc.want, out)
			}
			if c.fails != tc.fails {
				t.Errorf("reported %d failure(s), want %d:\n%s", c.fails, tc.fails, out)
			}
			if strings.Contains(out, "no longer exists") {
				t.Errorf("a PATH lookup was checked as a file path:\n%s", out)
			}
		})
	}
}
