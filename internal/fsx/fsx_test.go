// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package fsx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Whatever else concurrent writers do to each other, no reader may ever see a
// file that is neither the old contents nor a whole new set. The fixed
// path+".tmp" name this package replaced failed exactly here.
func TestConcurrentWritesLeaveOneWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	const writers = 16
	bodies := make([][]byte, writers)
	for i := range bodies {
		bodies[i] = bytes.Repeat([]byte(fmt.Sprintf("%02d", i)), 4096)
	}

	var wg sync.WaitGroup
	// A reader running alongside the writers: it must never load a file that
	// mixes two writers' bytes.
	done := make(chan struct{})
	torn := make(chan error, 1)
	go func() {
		defer close(torn)
		for {
			select {
			case <-done:
				return
			default:
			}
			raw, err := os.ReadFile(path)
			if err != nil || len(raw) == 0 {
				continue // not written yet
			}
			if !whole(raw) {
				torn <- fmt.Errorf("read a torn file of %d bytes", len(raw))
				return
			}
		}
	}()

	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := WriteFileAtomic(path, bodies[i], 0o600); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	close(done)
	if err := <-torn; err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !whole(raw) {
		t.Fatalf("final file is a mixture of writers: %q...", raw[:32])
	}

	// Every temp file is gone, whichever writer won.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("temp files left behind: %v", names)
	}
}

// whole reports whether raw is one writer's body and not two spliced together.
func whole(raw []byte) bool {
	if len(raw) != 8192 {
		return false
	}
	return bytes.Equal(raw, bytes.Repeat(raw[:2], 4096))
}

// os.CreateTemp always makes the temp file 0600, so a site asking for a wider
// mode only gets it because of the Chmod.
func TestPermIsApplied(t *testing.T) {
	dir := t.TempDir()
	for _, perm := range []os.FileMode{0o600, 0o644, 0o755} {
		path := filepath.Join(dir, fmt.Sprintf("f-%o", perm))
		if err := WriteFileAtomic(path, []byte("x"), perm); err != nil {
			t.Fatalf("%o: %v", perm, err)
		}
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != perm {
			t.Errorf("mode is %o, want %o", st.Mode().Perm(), perm)
		}
	}
}

// A rewrite of an existing file keeps the mode the caller asks for, not the
// mode the file happened to have.
func TestRewriteResetsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte("old"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode is %o, want 600", st.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new" {
		t.Fatalf("contents are %q", raw)
	}
}

func TestWriteJSONAtomicShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.json")
	type doc struct {
		ID    string   `json:"id"`
		Names []string `json:"names"`
	}
	if err := WriteJSONAtomic(path, doc{ID: "a", Names: []string{"b", "c"}}, 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"id\": \"a\",\n  \"names\": [\n    \"b\",\n    \"c\"\n  ]\n}\n"
	if string(raw) != want {
		t.Fatalf("got %q, want %q", raw, want)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("no trailing newline")
	}
	var back doc
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFailsWhenTheDirectoryIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "f.json")
	if err := WriteFileAtomic(path, []byte("x"), 0o600); err == nil {
		t.Fatal("want an error for a missing directory; callers create it themselves")
	}
}

func TestWriteFileAtomicSyncWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.json")
	if err := WriteFileAtomicSync(path, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Fatalf("contents are %q", raw)
	}
}
