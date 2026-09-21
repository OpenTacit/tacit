// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// The marker says which kind of instance the session is talking to, and stays
// silent when the registry has not said — a status line must never speculate.
func TestAccessMarker(t *testing.T) {
	cases := []struct{ mode, tunnel, want string }{
		{"private", "", "⌂ private"},
		{"global", "up", "⇄ proxied"},
		{"global", "", "⇄ proxied"},
		{"global", "down", "⇄ proxied·down"},
		{"staged", "up", "⇄ proxied (commons unconfirmed)"},
		{"", "", ""},         // registry has not answered, or predates the field
		{"nonsense", "", ""}, // an unknown mode is not guessed at
	}
	for _, c := range cases {
		if got := accessMarker(c.mode, c.tunnel); got != c.want {
			t.Errorf("accessMarker(%q, %q) = %q, want %q", c.mode, c.tunnel, got, c.want)
		}
	}
}

// A dropped tunnel must be distinguishable from a healthy proxy: locally
// everything looks fine while every colleague gets a 502.
func TestProxiedDownIsDistinguishable(t *testing.T) {
	if accessMarker("global", "down") == accessMarker("global", "up") {
		t.Error("a dropped tunnel reads identically to a healthy one")
	}
}
