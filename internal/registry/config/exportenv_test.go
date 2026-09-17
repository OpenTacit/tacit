// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Settings writes the model key to registry.env, and the running process sees
// it only because the save also calls os.Setenv. Nothing put the file into the
// environment at startup, so the key vanished on the next start — on a registry
// run by hand. A service never showed it, because systemd's EnvironmentFile=
// had already done this.
func TestTheSettingsFileIsThisProcessesEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.env")
	if err := os.WriteFile(path, []byte(
		"# a comment\nTACIT_LLM_API_KEY=sk-from-the-file\nTACIT_SUGGEST_MODEL=claude-x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", path)
	t.Setenv("TACIT_LLM_API_KEY", "")
	t.Setenv("TACIT_SUGGEST_MODEL", "")

	ExportFileEnv()

	if got := os.Getenv("TACIT_LLM_API_KEY"); got != "sk-from-the-file" {
		t.Errorf("TACIT_LLM_API_KEY = %q, want the file's value", got)
	}
	if got := os.Getenv("TACIT_SUGGEST_MODEL"); got != "claude-x" {
		t.Errorf("TACIT_SUGGEST_MODEL = %q", got)
	}
}

func TestAnExplicitEnvironmentStillWins(t *testing.T) {
	// The settings page says "environment variables override the file", and a
	// deployment that sets one means it.
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.env")
	if err := os.WriteFile(path, []byte("TACIT_LLM_API_KEY=sk-from-the-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", path)
	t.Setenv("TACIT_LLM_API_KEY", "sk-from-the-environment")

	ExportFileEnv()

	if got := os.Getenv("TACIT_LLM_API_KEY"); got != "sk-from-the-environment" {
		t.Errorf("TACIT_LLM_API_KEY = %q, want the environment's value", got)
	}
}

func TestNoSettingsFileIsNotAFailure(t *testing.T) {
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "absent.env"))
	ExportFileEnv() // must not panic
}
