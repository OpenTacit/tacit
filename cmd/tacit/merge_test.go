// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"testing"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/merge"
)

// merge.ArchivePath owns the decision and internal/merge tests it. What is
// worth asserting here is the wiring: the command hands it the member's state
// directory, so an archive lands beside the rest of their state rather than at
// the top of their home directory.
func TestMergeArchivesIntoTheStateDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	got := merge.ArchivePath(auditorconfig.Load().StateDir, "")
	want := filepath.Join(home, ".local", "share", "tacit", "archives")
	if filepath.Dir(got) != want {
		t.Fatalf("archive path = %s, want a file under %s", got, want)
	}
}
