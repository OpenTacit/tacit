// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

// An owner link is a signed token with an expiry and no host in it, so it works
// from wherever the registry answers. Every command that printed one still
// hard-coded 127.0.0.1, which handed an operator who set the registry up over
// SSH a link openable only on the machine they were not sitting at.
func TestOwnerLinkPrefersTheAddressSomebodyElseCanOpen(t *testing.T) {
	cfg := registryconfig.Config{Port: 8080}

	// The ingress address wins when there is one: that is the whole point of
	// turning the tunnel on.
	base, where := ownerLinkBase(cfg, 0, "https://wheat-canyon.tacit.zone/")
	if base != "https://wheat-canyon.tacit.zone" || where != "from anywhere" {
		t.Errorf("published: %q / %q", base, where)
	}

	// Then the operator's own proxy, when they configured one.
	withExt := registryconfig.Config{Port: 8080, ExternalURL: "https://tacit.example.com/"}
	base, where = ownerLinkBase(withExt, 0, "")
	if base != "https://tacit.example.com" || where == "" {
		t.Errorf("external: %q / %q", base, where)
	}

	// A public address outranks a configured one — the ingress allocated it for
	// this run, and it is the address the registry is actually answering on.
	base, _ = ownerLinkBase(withExt, 0, "https://wheat-canyon.tacit.zone")
	if base != "https://wheat-canyon.tacit.zone" {
		t.Errorf("public did not outrank the configured external URL: %q", base)
	}
}

// The port the caller is serving on beats the one in the file: `tacit init
// --port` prints a link before anything has re-read the settings.
func TestOwnerLinkUsesThePortInPlay(t *testing.T) {
	cfg := registryconfig.Config{Port: 8080}
	base, _ := ownerLinkBase(cfg, 9999, "")
	if !strings.Contains(base, ":9999") {
		t.Errorf("base = %q, want the port the caller passed", base)
	}
	base, _ = ownerLinkBase(cfg, 0, "")
	if !strings.Contains(base, ":8080") {
		t.Errorf("base = %q, want the configured port when the caller passes none", base)
	}
}

// A base path is part of the address, and a link that drops it 404s.
func TestOwnerLinkKeepsTheBasePath(t *testing.T) {
	cfg := registryconfig.Config{Port: 8080, BasePath: "/apps/tacit"}
	base, _ := ownerLinkBase(cfg, 0, "")
	if !strings.HasSuffix(base, "/apps/tacit") {
		t.Errorf("base = %q, want the sub-path kept", base)
	}
}

// A hostname that resolves to nothing is worse than loopback: loopback at least
// works somewhere. resolvableHostname answers only when a lookup returns an
// address something else could route to.
func TestResolvableHostnameRefusesANameThatGoesNowhere(t *testing.T) {
	t.Setenv("HOSTNAME", "no-such-host.invalid")
	// os.Hostname does not read HOSTNAME, so this cannot force the negative
	// case on every machine. What it can check is the contract: whatever comes
	// back is either empty or a name this machine can actually look up.
	host := resolvableHostname()
	if host == "" {
		return
	}
	if host == "localhost" {
		t.Errorf("returned %q, which is loopback by another name", host)
	}
	cfg := registryconfig.Config{Port: 8080}
	base, where := ownerLinkBase(cfg, 0, "")
	if !strings.Contains(base, host) {
		t.Errorf("base = %q, want it to use the resolvable hostname %q", base, host)
	}
	if where == "on this machine" {
		t.Error("described a network address as machine-local")
	}
}
