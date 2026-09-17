// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The route table is the only state whose loss renames every tenant: an instance
// that cannot be resolved from its key enrols again and is given a new hostname,
// and every address anybody wrote down stops working. These three tests are the
// three ways it was losable.

func TestACorruptRouteTableIsMovedAsideRatherThanStoppingTheIngress(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instances.json")

	first, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	in, _, err := first.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// A half-written file is what a full disk or a crash mid-rename leaves.
	if err := os.WriteFile(path, []byte(`[{"name":"olive-har`), 0o600); err != nil {
		t.Fatalf("corrupt the table: %v", err)
	}

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("the ingress refused to start over one unreadable file: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, ok := s.Get(in.Name); !ok {
		t.Errorf("instance %s was lost; the spare copy holds it", in.Name)
	}
	if s.Warning() == "" {
		t.Error("nothing was said about the unreadable table; an operator has to be told")
	}
	aside, _ := filepath.Glob(path + ".corrupt-*")
	if len(aside) != 1 {
		t.Errorf("found %d files moved aside, want the bad one kept for inspection", len(aside))
	}
}

func TestHandshakesCoalesceInsteadOfRewritingTheTableEachTime(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	in, _, err := s.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}

	for i := range 20 {
		s.Touch(in.Name, fmt.Sprintf("build-%d", i), "127.0.0.1:9999")
	}
	if v := versionOnDisk(t, dir, in.Name); v != "" {
		t.Errorf("the table was rewritten during the burst (%q on disk); a reconnect storm "+
			"must not serialize one whole-file rewrite per handshake", v)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if v := versionOnDisk(t, dir, in.Name); v != "build-19" {
		t.Errorf("version on disk = %q, want the last handshake's", v)
	}
}

func TestASaveThatFailsLeavesTheTableAsItWas(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only directory anyway")
	}
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	in, _, err := s.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := s.SetOwnerNote(in.Name, "Someone@Example.com", "the finance team"); err == nil {
		t.Fatal("SetOwnerNote reported success on a table it could not write")
	}
	got, _ := s.Get(in.Name)
	if got.Owner != "" || got.Note != "" {
		t.Errorf("owner %q and note %q survived a failed save; memory now disagrees with the file",
			got.Owner, got.Note)
	}
}

// versionOnDisk reads the version the route table FILE records for one instance,
// which is the only way to tell a save that happened from one that was owed.
func versionOnDisk(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "instances.json"))
	if err != nil {
		t.Fatalf("read the table: %v", err)
	}
	var list []Instance
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("parse the table: %v", err)
	}
	for _, in := range list {
		if in.Name == name {
			return in.Version
		}
	}
	t.Fatalf("instance %s is not in the table on disk", name)
	return ""
}
