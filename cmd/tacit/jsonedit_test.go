// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// These files belong to the member, and a wrong edit here is their editor
// settings broken by us. The rules the tests hold to: never write a file we
// could not parse, never take an entry the member wrote, keep everything in
// the file that is not ours, and leave no shell of a file behind.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// jsonEqual compares two JSON documents by value, so a test says what the file
// means rather than how it is spaced.
func jsonEqual(t *testing.T, got []byte, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("result is not JSON: %v (%s)", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("test wants invalid JSON: %v", err)
	}
	return reflect.DeepEqual(g, w)
}

func TestAddMCPServer(t *testing.T) {
	const entry = `{"command":"/opt/tacit","args":["mcp"]}`
	cases := []struct {
		name   string
		start  string // file contents before; empty means no file
		key    string
		backup bool
		want   mcpEdit
		file   string // contents after
		bak    bool   // a .bak-pre-tacit copy must be there
	}{
		{
			name: "fresh file",
			key:  "mcpServers",
			want: mcpCreated,
			file: `{"mcpServers":{"tacit":` + entry + `}}`,
		},
		{
			name: "other servers already present",
			start: `{"mcpServers":{"linear":{"command":"linear-mcp"}},
			         "editor.fontSize":13}`,
			key:  "mcpServers",
			want: mcpAdded,
			file: `{"mcpServers":{"linear":{"command":"linear-mcp"},"tacit":` + entry + `},
			        "editor.fontSize":13}`,
		},
		{
			name:  "file with no servers map at all",
			start: `{"editor.fontSize":13}`,
			key:   "mcpServers",
			want:  mcpAdded,
			file:  `{"editor.fontSize":13,"mcpServers":{"tacit":` + entry + `}}`,
		},
		{
			name:  "an existing tacit entry is the member's",
			start: `{"mcpServers":{"tacit":{"command":"/usr/local/bin/tacit","args":["mcp","--verbose"]}}}`,
			want:  mcpPresent,
			key:   "mcpServers",
			file:  `{"mcpServers":{"tacit":{"command":"/usr/local/bin/tacit","args":["mcp","--verbose"]}}}`,
		},
		{
			name:  "amp keeps its own key",
			start: `{"amp.mcpServers":{"linear":{"command":"linear-mcp"}}}`,
			key:   "amp.mcpServers",
			want:  mcpAdded,
			file:  `{"amp.mcpServers":{"linear":{"command":"linear-mcp"},"tacit":` + entry + `}}`,
		},
		{
			name:   "backup holds what was there before",
			start:  `{"editor.fontSize":13}`,
			key:    "mcpServers",
			backup: true,
			want:   mcpAdded,
			file:   `{"editor.fontSize":13,"mcpServers":{"tacit":` + entry + `}}`,
			bak:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			if tc.start != "" {
				if err := os.WriteFile(path, []byte(tc.start), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c := &connectReport{}
			if got := addMCPServer(c, path, tc.key, "/opt/tacit", tc.backup); got != tc.want {
				t.Fatalf("addMCPServer = %v, want %v", got, tc.want)
			}
			if c.fails != 0 {
				t.Errorf("reported %d failure(s)", c.fails)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if !jsonEqual(t, raw, tc.file) {
				t.Errorf("file after edit:\n got %s\nwant %s", raw, tc.file)
			}
			bak := path + ".bak-pre-tacit"
			pre, err := os.ReadFile(bak)
			switch {
			case tc.bak && err != nil:
				t.Errorf("edited a member's file with no backup")
			case tc.bak && string(pre) != tc.start:
				t.Errorf("backup holds %q, want the pre-edit file %q", pre, tc.start)
			case !tc.bak && err == nil:
				t.Errorf("wrote a backup nobody asked for")
			}
		})
	}

	// The safety rule: a file we cannot parse is a file we do not touch. The
	// member's harness may accept comments our parser will not.
	t.Run("unparseable file is never edited", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		start := "// my settings\n{\"mcpServers\": {},}\n"
		if err := os.WriteFile(path, []byte(start), 0o644); err != nil {
			t.Fatal(err)
		}
		c := &connectReport{}
		if got := addMCPServer(c, path, "mcpServers", "/opt/tacit", true); got != mcpUnparseable {
			t.Fatalf("addMCPServer = %v, want mcpUnparseable", got)
		}
		if c.fails != 0 {
			t.Errorf("reported %d failure(s) for a file it simply cannot read", c.fails)
		}
		raw, _ := os.ReadFile(path)
		if string(raw) != start {
			t.Errorf("edited a file it could not parse: %q", raw)
		}
		if _, err := os.Stat(path + ".bak-pre-tacit"); err == nil {
			t.Error("backed up a file it was not going to edit")
		}
	})
}

func TestRemoveMCPServer(t *testing.T) {
	cases := []struct {
		name  string
		start string // file contents before; empty means no file
		key   string
		found bool
		todos int
		file  string // contents after; empty means the file must be gone
	}{
		{
			name:  "nothing to unwind",
			key:   "mcpServers",
			found: false,
		},
		{
			name:  "leaves the file when other servers stay",
			start: `{"mcpServers":{"linear":{"command":"linear-mcp"},"tacit":{"command":"/opt/tacit"}}}`,
			key:   "mcpServers",
			found: true,
			file:  `{"mcpServers":{"linear":{"command":"linear-mcp"}}}`,
		},
		{
			name:  "empty servers map goes, other keys stay",
			start: `{"mcpServers":{"tacit":{"command":"/opt/tacit"}},"editor.fontSize":13}`,
			key:   "mcpServers",
			found: true,
			file:  `{"editor.fontSize":13}`,
		},
		{
			name:  "file holding only our wiring goes",
			start: `{"mcpServers":{"tacit":{"command":"/opt/tacit"}}}`,
			key:   "mcpServers",
			found: true,
		},
		{
			name:  "amp keeps its own key",
			start: `{"amp.mcpServers":{"tacit":{"command":"/opt/tacit"}},"amp.tools.stopTimeout":60}`,
			key:   "amp.mcpServers",
			found: true,
			file:  `{"amp.tools.stopTimeout":60}`,
		},
		{
			name:  "no tacit entry to take",
			start: `{"mcpServers":{"linear":{"command":"linear-mcp"}}}`,
			key:   "mcpServers",
			found: true,
			file:  `{"mcpServers":{"linear":{"command":"linear-mcp"}}}`,
		},
		{
			name:  "unparseable file is never edited",
			start: "// my settings\n{\"mcpServers\": {\"tacit\": {}},}\n",
			key:   "mcpServers",
			found: true,
			todos: 1,
			file:  "// my settings\n{\"mcpServers\": {\"tacit\": {}},}\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			if tc.start != "" {
				if err := os.WriteFile(path, []byte(tc.start), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c := &connectReport{}
			if got := removeMCPServer(c, path, tc.key); got != tc.found {
				t.Fatalf("removeMCPServer = %v, want %v", got, tc.found)
			}
			if c.fails != 0 {
				t.Errorf("reported %d failure(s)", c.fails)
			}
			if c.todos != tc.todos {
				t.Errorf("left %d todo(s), want %d", c.todos, tc.todos)
			}
			raw, err := os.ReadFile(path)
			switch {
			case tc.file == "":
				if err == nil {
					t.Errorf("file still there holding %s", raw)
				}
			case err != nil:
				t.Fatalf("read back: %v", err)
			case tc.todos > 0:
				if string(raw) != tc.file {
					t.Errorf("edited a file it could not parse: %q", raw)
				}
			case !jsonEqual(t, raw, tc.file):
				t.Errorf("file after edit:\n got %s\nwant %s", raw, tc.file)
			}
		})
	}
}

// Connect then disconnect: the member's file comes back to what it was, and a
// file that was not there is not left behind.
func TestMCPServerRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start string
	}{
		{name: "no file to begin with"},
		{name: "settings the member already had", start: `{"editor.fontSize":13,"mcpServers":{"linear":{"command":"linear-mcp"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			if tc.start != "" {
				if err := os.WriteFile(path, []byte(tc.start), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c := &connectReport{}
			addMCPServer(c, path, "mcpServers", "/opt/tacit", true)
			removeMCPServer(c, path, "mcpServers")
			if c.fails != 0 {
				t.Fatalf("reported %d failure(s)", c.fails)
			}
			raw, err := os.ReadFile(path)
			if tc.start == "" {
				if err == nil {
					t.Fatalf("left a file behind holding %s", raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if !jsonEqual(t, raw, tc.start) {
				t.Errorf("round trip changed the file:\n got %s\nwant %s", raw, tc.start)
			}
		})
	}
}
