// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/ingress"
)

// `tacit init` gives every new registry a public address now, so somebody who
// tries the product and tears it down is the ordinary case — and until this,
// each one kept its hostname allocated on the shared proxy for good. Only
// `tacit merge` ever released a name.
func TestTeardownReleasesThePublicAddress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envPath := filepath.Join(home, ".config", "tacit", "registry.env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", envPath)

	// A registry that never published has no name to hand back, and must not
	// spend a twenty-second dial finding that out.
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { releaseIngressName(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("dialled an ingress for a registry that never published")
	}

	// One that did publish tries, and says so when it cannot — the name staying
	// allocated is worth a line, not a silent success.
	if err := os.WriteFile(envPath, []byte(
		"TACIT_API_KEY=k\nTACIT_GLOBAL_ACCESS=1\nTACIT_PUBLISH_INGRESS=ingress.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ingress.LoadOrCreateKey(filepath.Dir(envPath)); err != nil {
		t.Fatal(err)
	}
	done = make(chan struct{})
	go func() { releaseIngressName(); close(done) }()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		t.Fatal("release did not give up on an unreachable ingress")
	}
	// The key survives a failed release: it is the credential for a name that
	// still exists, and deleting it would strand the allocation.
	if _, err := os.Stat(ingress.KeyPath(filepath.Dir(envPath))); err != nil {
		t.Errorf("instance key removed after a failed release: %v", err)
	}
}
