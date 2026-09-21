// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

func TestAnUngatedRegistryDoesNotReachTheInternet(t *testing.T) {
	// Two settings, set in different places and often months apart. Assembling
	// them into a public, ungated dashboard is the one combination nobody
	// should be able to arrive at by accident.
	ok, why := publishAllowed(registryconfig.Config{GlobalAccess: true})
	if ok {
		t.Fatal("an open registry was allowed to publish")
	}
	for _, want := range []string{"tacit secure", "tacit init --owner", "Global Access off"} {
		if !strings.Contains(why, want) {
			t.Errorf("the refusal does not mention %q: %s", want, why)
		}
	}
}

func TestAGatedRegistryPublishes(t *testing.T) {
	owner := registryconfig.Config{GlobalAccess: true,
		AuthMode: registryconfig.AuthOwner, OwnerSecret: "s"}
	if ok, why := publishAllowed(owner); !ok {
		t.Errorf("owner mode was refused: %s", why)
	}
	idp := registryconfig.Config{GlobalAccess: true,
		OIDCIssuer: "https://idp.example", OIDCClientID: "c",
		OIDCClientSecret: "s", OIDCRedirectURI: "https://x/callback"}
	if ok, why := publishAllowed(idp); !ok {
		t.Errorf("an identity provider was refused: %s", why)
	}
}
