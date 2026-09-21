// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

// The operator who has just run `tacit init` has registry.env and no agent.env.
// `tacit invite` — step 3 of the four-step guide on the front page — read only
// agent.env, got the compiled-in dev-key, and returned 401 to the one person
// guaranteed to hold a working key.
func TestAdminCredsFallsBackToThisHostsRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACIT_REGISTRY_URL", "")
	t.Setenv("TACIT_API_KEY", "")
	regEnv := filepath.Join(home, ".config", "tacit", "registry.env")
	if err := os.MkdirAll(filepath.Dir(regEnv), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", regEnv)
	if err := os.WriteFile(regEnv, []byte("TACIT_API_KEY=operatorkey\nTACIT_PORT=9123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	url, key, local := adminCreds(auditorconfig.DefaultRegistryURL, auditorconfig.DefaultRegistryKey)
	if !local {
		t.Fatal("did not fall back to registry.env")
	}
	if key != "operatorkey" {
		t.Errorf("key = %q, want the one in registry.env", key)
	}
	if want := "http://127.0.0.1:9123"; url != want {
		t.Errorf("url = %q, want %q (the port registry.env names)", url, want)
	}
}

// A machine that is a member of an org AND runs a scratch registry means the
// org. The member file is an answer somebody gave; registry.env is only a
// fallback for having no answer at all.
func TestAdminCredsKeepsRealMemberSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	regEnv := filepath.Join(home, ".config", "tacit", "registry.env")
	if err := os.MkdirAll(filepath.Dir(regEnv), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", regEnv)
	if err := os.WriteFile(regEnv, []byte("TACIT_API_KEY=operatorkey\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	url, key, local := adminCreds("https://tacit.example.com", "memberkey")
	if local {
		t.Fatal("overrode a real member setting with this host's registry")
	}
	if url != "https://tacit.example.com" || key != "memberkey" {
		t.Errorf("got %s / %s, want the member's own values", url, key)
	}
}

// With no registry on this host there is nothing to fall back to, and the
// compiled-in default is still the honest answer.
func TestAdminCredsLeavesDefaultsAloneWithNoRegistry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(home, "nothing-here.env"))
	url, key, local := adminCreds(auditorconfig.DefaultRegistryURL, auditorconfig.DefaultRegistryKey)
	if local || url != auditorconfig.DefaultRegistryURL || key != auditorconfig.DefaultRegistryKey {
		t.Errorf("got %s / %s / local=%v, want the compiled-in pair unchanged", url, key, local)
	}
}

func TestLocalRegistryBaseUsesTheConfiguredPort(t *testing.T) {
	if got := localRegistryBase(map[string]string{}); got != "http://127.0.0.1:8080" {
		t.Errorf("default = %q", got)
	}
	if got := localRegistryBase(map[string]string{"TACIT_PORT": "9000"}); got != "http://127.0.0.1:9000" {
		t.Errorf("configured = %q", got)
	}
	// A malformed port is not a reason to build a broken URL.
	if got := localRegistryBase(map[string]string{"TACIT_PORT": "not-a-port"}); got != "http://127.0.0.1:8080" {
		t.Errorf("malformed = %q", got)
	}
	_ = registryconfig.DefaultPort
}

// A join link is a line to paste into chat. `tacit invite` used to mint one
// against 127.0.0.1 without comment, so the operator's first invitation was a
// link that failed for everybody who received it.
func TestReachableJoinBaseRejectsLinksThatOnlyWorkHere(t *testing.T) {
	unreachable := []string{
		"http://127.0.0.1:8080/join/abc",
		"http://localhost:8080/join/abc",
		"http://[::1]:8080/join/abc",
		"http://0.0.0.0:8080/join/abc",
		"http://workshop:8080/join/abc", // the bare machine name `tacit init` prints
	}
	for _, u := range unreachable {
		if reachableJoinBase(u) {
			t.Errorf("%s: called reachable, but it only resolves on this machine", u)
		}
	}
	reachable := []string{
		"https://tacit.example.com/join/abc",
		"https://wheat-canyon.tacit.zone/join/abc",
		"http://10.0.0.4:8080/join/abc", // a LAN address is somebody's real deployment
	}
	for _, u := range reachable {
		if !reachableJoinBase(u) {
			t.Errorf("%s: refused, but colleagues can open it", u)
		}
	}
}
