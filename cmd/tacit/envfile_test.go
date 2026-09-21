// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Both env files are read by a shell (systemd's EnvironmentFile=, the launchd
// plist) and by people, and both have been on disk since the first release. The
// bytes are the contract, so this test spells them out in full rather than
// checking properties of them. The two headers differ on purpose: each file
// says which command wrote it.
func TestWriteSortedEnvFileGolden(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name   string
		header string
		vals   map[string]string
		want   string
	}{
		{
			name:   "registry.env",
			header: "# written by `tacit init`; environment variables override these",
			vals: map[string]string{
				"TACIT_PORT":     "8080",
				"TACIT_API_KEY":  "k-secret",
				"TACIT_DATA":     "/var/lib/tacit",
				"TACIT_LLM_MODE": "off",
			},
			want: "# written by `tacit init`; environment variables override these\n" +
				"TACIT_API_KEY=k-secret\n" +
				"TACIT_DATA=/var/lib/tacit\n" +
				"TACIT_LLM_MODE=off\n" +
				"TACIT_PORT=8080\n",
		},
		{
			name:   "agent.env",
			header: "# written by `tacit connect`; environment variables override these",
			vals: map[string]string{
				"TACIT_REGISTRY_URL": "https://reg.example",
				"TACIT_API_KEY":      "k-secret",
				"TACIT_SKETCH_URL":   "", // an empty value is a decision; the line stays
			},
			want: "# written by `tacit connect`; environment variables override these\n" +
				"TACIT_API_KEY=k-secret\n" +
				"TACIT_REGISTRY_URL=https://reg.example\n" +
				"TACIT_SKETCH_URL=\n",
		},
		{
			name:   "no settings at all",
			header: "# written by `tacit init`; environment variables override these",
			vals:   map[string]string{},
			want:   "# written by `tacit init`; environment variables override these\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name, "env")
			if err := writeSortedEnvFile(path, tc.header, tc.vals); err != nil {
				t.Fatalf("write: %v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Errorf("file is\n%q\nwant\n%q", raw, tc.want)
			}
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != 0o600 {
				t.Errorf("mode is %o, want 600 — these files carry the API key", st.Mode().Perm())
			}
		})
	}
}

// A second write replaces the file rather than appending to or truncating it,
// and leaves no temp file behind for an operator to find and wonder about.
func TestWriteSortedEnvFileReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.env")
	header := "# written by `tacit connect`; environment variables override these"

	if err := writeSortedEnvFile(path, header, map[string]string{"A": "1", "B": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := writeSortedEnvFile(path, header, map[string]string{"A": "9"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != header+"\nA=9\n" {
		t.Fatalf("file is %q", raw)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d files, want only agent.env", len(entries))
	}
}
