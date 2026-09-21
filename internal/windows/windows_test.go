// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package windows

import (
	"strings"
	"testing"
	"time"
)

// The keys are a wire surface: the MCP tool schemas publish them as an enum and
// hosts validate against it. This test is the freeze.
func TestKeysAreFrozen(t *testing.T) {
	want := []string{"7d", "30d", "90d", "all"}
	if len(Keys) != len(want) {
		t.Fatalf("Keys = %v, want %v", Keys, want)
	}
	for i, k := range want {
		if Keys[i] != k {
			t.Fatalf("Keys[%d] = %q, want %q (order is part of the wire surface)", i, Keys[i], k)
		}
	}
	if Default != "30d" {
		t.Fatalf("Default = %q, want 30d", Default)
	}
}

func TestDurationMatchesTheKey(t *testing.T) {
	day := 24 * time.Hour
	for key, want := range map[string]time.Duration{
		"7d": 7 * day, "30d": 30 * day, "90d": 90 * day, "all": 0,
	} {
		if got := Duration(key); got != want {
			t.Errorf("Duration(%q) = %v, want %v", key, got, want)
		}
	}
}

// An unknown token gets the default window rather than a zero one, because a
// zero duration means "all time" here and a typo must not widen the window.
func TestUnknownKeyFallsBackToTheDefault(t *testing.T) {
	for _, key := range []string{"", "6d", "year", "ALLTIME"} {
		if got := Duration(key); got != Duration(Default) {
			t.Errorf("Duration(%q) = %v, want the default %v", key, got, Duration(Default))
		}
		if Valid(key) {
			t.Errorf("Valid(%q) = true", key)
		}
	}
}

func TestKeysAreAcceptedWhateverTheCasingAndSpacing(t *testing.T) {
	for _, key := range []string{"7D", " 30d ", "\tAll\n"} {
		if !Valid(key) {
			t.Errorf("Valid(%q) = false", key)
		}
	}
}

// Every key has to appear in each rendering, or a surface would advertise a
// window it cannot resolve.
func TestEveryKeyReachesEveryRendering(t *testing.T) {
	for _, k := range Keys {
		if !Valid(k) {
			t.Errorf("Valid(%q) = false for a key in Keys", k)
		}
		if !strings.Contains(Help(), k) {
			t.Errorf("Help() = %q, missing %q", Help(), k)
		}
		if !strings.Contains(Prose(), k) {
			t.Errorf("Prose() = %q, missing %q", Prose(), k)
		}
		if !strings.Contains(ProseDefault(), k) {
			t.Errorf("ProseDefault() = %q, missing %q", ProseDefault(), k)
		}
	}
	if got, want := Help(), "7d | 30d | 90d | all"; got != want {
		t.Errorf("Help() = %q, want %q", got, want)
	}
	if got, want := Prose(), "7d, 30d, 90d, or all"; got != want {
		t.Errorf("Prose() = %q, want %q", got, want)
	}
	// The MCP tool descriptions are built from this, so it is a wire surface too.
	if got, want := ProseDefault(), "7d, 30d (default), 90d, or all"; got != want {
		t.Errorf("ProseDefault() = %q, want %q", got, want)
	}
}
