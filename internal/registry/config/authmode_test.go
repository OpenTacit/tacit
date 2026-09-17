// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func TestOwnerModeNeedsBothTheModeAndTheSecret(t *testing.T) {
	cases := []struct {
		name  string
		cfg   Config
		gated bool
	}{
		{"open by default", Config{}, false},
		{"mode with no secret", Config{AuthMode: AuthOwner}, false},
		{"secret with no mode", Config{OwnerSecret: "s"}, false},
		{"both", Config{AuthMode: AuthOwner, OwnerSecret: "s"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.OwnerEnabled(); got != c.gated {
				t.Errorf("OwnerEnabled = %v, want %v", got, c.gated)
			}
			if got := c.cfg.AuthConfigured(); got != c.gated {
				t.Errorf("AuthConfigured = %v, want %v", got, c.gated)
			}
		})
	}
}

func TestAnOpenRegistryIsStillTheDefault(t *testing.T) {
	// Every install has been open until now, and the whole suite asserts it.
	// Owner mode is something a registry is put into, never something it
	// wakes up in.
	if (Config{}).AuthMode != "" && (Config{}).AuthMode != AuthOpen {
		t.Error("the zero config is not open")
	}
	if (Config{}).AuthConfigured() {
		t.Error("a registry with no settings claims a gate")
	}
}
