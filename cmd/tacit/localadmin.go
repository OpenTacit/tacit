// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// Admin credentials for THIS machine's registry.
//
// Two files hold a registry address and a key, and they belong to two different
// people. `tacit init` writes registry.env — the operator's file, with the org
// key in it. `tacit connect` writes agent.env — the member's file, with that
// member's own key. Every day-to-day command reads the member's file, which is
// right: a member's machine is a member's machine.
//
// An operator's first machine has only the first file. So `tacit invite`, run
// exactly as the front page teaches it, resolved nothing, fell back to the
// compiled-in http://127.0.0.1:8080 and dev-key, and returned 401 to the person
// who had just been handed a working registry and a real key. The credential was
// on disk, two directories away, in a file the command did not read.
//
// adminCreds closes that: where the member settings are still the compiled-in
// defaults and this host operates a registry, the registry's own address and key
// stand in. A real member setting always wins — a laptop that has both files is
// a member of an org AND runs a scratch registry, and the org is the one it
// meant.

import (
	"fmt"
	"os"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

// adminCreds returns the registry URL and key an admin call should use, and
// says whether it fell back to this host's registry.env. Inputs are the
// member-resolved values, so a caller passes what config.Load gave it.
func adminCreds(memberURL, memberKey string) (url, key string, local bool) {
	if !memberSettingsAreDefaults(memberURL, memberKey) {
		return memberURL, memberKey, false
	}
	vals := auditorconfig.ReadEnvFile(registryconfig.RegistryEnvPath())
	if vals["TACIT_API_KEY"] == "" {
		return memberURL, memberKey, false
	}
	return localRegistryBase(vals), vals["TACIT_API_KEY"], true
}

// memberSettingsAreDefaults reports whether the values came from nothing —
// neither the environment nor agent.env named a registry, so config.Load
// returned the compiled-in pair. Checked by asking where they COULD have come
// from rather than by comparing against the default strings, because an
// operator whose registry genuinely sits on the default address and whose key is
// genuinely "dev-key" still has a real agent.env, and that file is their answer.
func memberSettingsAreDefaults(url, key string) bool {
	if url != auditorconfig.DefaultRegistryURL || key != auditorconfig.DefaultRegistryKey {
		return false
	}
	if os.Getenv("TACIT_REGISTRY_URL") != "" || os.Getenv("TACIT_API_KEY") != "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return true
	}
	agent := auditorconfig.ReadEnvFile(auditorconfig.AgentEnvPath(home))
	return agent["TACIT_REGISTRY_URL"] == "" && agent["TACIT_API_KEY"] == ""
}

// localRegistryBase is where this host's registry answers, from its own
// settings. Loopback rather than the external URL: this is the address for a
// command running on the registry's own machine, and it works before any proxy
// or tunnel is up.
func localRegistryBase(vals map[string]string) string {
	port := registryconfig.DefaultPort
	if v := vals["TACIT_PORT"]; v != "" {
		if n := atoiOr(v, 0); n > 0 {
			port = n
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func atoiOr(s string, def int) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	if n == 0 {
		return def
	}
	return n
}
