// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/store"
)

// `tacit init` reads the org's own written conventions into the review queue,
// so the operator's first act is editing their own playbook rather than reading
// a manual beside an empty funnel.
func TestInitFilesTheReposConventionsAsDrafts(t *testing.T) {
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, "CLAUDE.md"), `## Deploys go through the pipeline
Never deploy by hand from a laptop. Push to main and confirm the new revision
answers its health check before you tell anyone it shipped.

## Query the warehouse, don't paste rows
Customer data is in the warehouse behind a role. Point the agent at the
connector rather than pasting a CSV, which is stale the moment you paste it.
`)
	data := t.TempDir()

	filed, already, err := originateFromRepo(data, repo, embedSettings{model: "hashing-v1", dim: 256})
	if err != nil {
		t.Fatalf("originateFromRepo: %v", err)
	}
	if filed != 2 || already != 0 {
		t.Fatalf("filed %d, already %d; want 2 and 0", filed, already)
	}

	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.ListTechniques(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("store holds %d techniques", len(got))
	}
	for _, tech := range got {
		// Held out until a person reads it. That is what makes reading a
		// repository into the playbook safe at all.
		if tech.Status != "draft" {
			t.Errorf("%s has status %q — a convention must not serve unreviewed", tech.ID, tech.Status)
		}
		// The org's own conventions are org-scoped by construction: this is
		// exactly the half of a playbook no public source can supply.
		if tech.Scope != "org" {
			t.Errorf("%s scope = %q, want org", tech.ID, tech.Scope)
		}
		if tech.Provenance != "observed" {
			t.Errorf("%s provenance = %q, want observed", tech.ID, tech.Provenance)
		}
		if !strings.HasPrefix(tech.Source, "CLAUDE.md") {
			t.Errorf("%s source = %q — a reviewer cannot find where it came from", tech.ID, tech.Source)
		}
	}
}

// Re-running init in the same checkout must not file a second copy of every
// convention. An id collision would not catch it: contribute mints slug-2
// rather than refusing, which is right for two members and wrong for one file
// read twice.
func TestInitDoesNotRefileConventionsItAlreadyRead(t *testing.T) {
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, "AGENTS.md"), `## Read the log first
When something deployed misbehaves, read the service log or the health endpoint
before you open any source file.
`)
	data := t.TempDir()
	if filed, _, err := originateFromRepo(data, repo, embedSettings{model: "hashing-v1", dim: 256}); err != nil || filed != 1 {
		t.Fatalf("first pass: filed %d, err %v", filed, err)
	}
	filed, already, err := originateFromRepo(data, repo, embedSettings{model: "hashing-v1", dim: 256})
	if err != nil {
		t.Fatal(err)
	}
	if filed != 0 || already != 1 {
		t.Errorf("second pass filed %d and recognised %d; want 0 and 1", filed, already)
	}
}

// A home directory that happens to be version-controlled is not an
// organization's practice, so auto looks at the working directory and stops.
func TestRepoToOriginate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if got := repoToOriginate("off"); got != "" {
		t.Errorf("off = %q, want nothing", got)
	}
	if got := repoToOriginate("auto"); got != "" {
		t.Errorf("auto in a plain directory = %q, want nothing", got)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := repoToOriginate("auto"); got == "" {
		t.Error("auto in a checkout found nothing")
	}
	if got := repoToOriginate(dir); got != dir {
		t.Errorf("explicit path = %q, want %q", got, dir)
	}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// `tacit init` writes the embedder settings to a file and then, in the same
// run, builds an embedder — in a process that has never seen them. On a machine
// with semantic retrieval configured, that meant the whole conventions pass
// failed with "set TACIT_ONNX_LIB", which is a library path presented as the
// reason your playbook is empty.
func TestOriginateUsesTheSettingsInitJustChose(t *testing.T) {
	t.Setenv("TACIT_ONNX_LIB", "")
	t.Setenv("TACIT_ONNX_MODEL_DIR", "")

	e := embedSettings{model: "hashing-v1", dim: 256,
		onnxLib: "/tmp/libonnxruntime.so", modelDir: "/tmp/model"}
	if _, err := e.embedder(); err != nil {
		t.Fatalf("building the configured embedder: %v", err)
	}
	// And it puts the environment back: a setup command must not rewrite its
	// own process's settings for everything that runs after it.
	if got := os.Getenv("TACIT_ONNX_LIB"); got != "" {
		t.Errorf("TACIT_ONNX_LIB left set to %q", got)
	}
}

// An embedder that cannot be built is not a reason to abandon the drafts.
// Startup re-embeds anything whose stored model differs from the serving one,
// so a draft written in the lexical space is corrected the moment the registry
// runs — and an empty review queue is not.
func TestOriginateFilesDraftsEvenWhenTheEmbedderIsUnavailable(t *testing.T) {
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, "CLAUDE.md"), `## Read the log first
When something deployed misbehaves, read the service log or the health endpoint
before you open any source file at all.
`)
	data := t.TempDir()

	filed, _, err := originateFromRepo(data, repo, embedSettings{model: "onnx/no-such-model", dim: 384})
	if err != nil {
		t.Fatalf("originateFromRepo: %v", err)
	}
	if filed != 1 {
		t.Fatalf("filed %d drafts; want the pass to complete on the fallback embedder", filed)
	}
}
