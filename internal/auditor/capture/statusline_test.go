// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import "testing"

// A SHARE IS ROUNDED, NOT TRUNCATED. The published schema's own example carries
// a fraction (23.5), and truncating reports 23 — erring in the one direction a
// figure about an allowance should not. The spend limit comes through the same
// reader and is not clamped at 100: past the cap is a real reading.
func TestAllowanceSharesAreRoundedAndNotClamped(t *testing.T) {
	in := ReadStatusLine(map[string]any{
		"rate_limits": map[string]any{
			"five_hour":   map[string]any{"used_percentage": 23.5, "resets_at": 1789191600},
			"seven_day":   map[string]any{"used_percentage": 41.2, "resets_at": 1789416000},
			"spend_limit": map[string]any{"used_percentage": 103.7, "resets_at": 1790787200},
		},
	})
	if !in.HasFiveHour || in.FiveHourPct != 24 {
		t.Errorf("five hour = %d, want 24 — 23.5 was truncated", in.FiveHourPct)
	}
	if !in.HasSevenDay || in.SevenDayPct != 41 {
		t.Errorf("seven day = %d, want 41", in.SevenDayPct)
	}
	if !in.HasSpend || in.SpendPct != 104 {
		t.Errorf("spend = %d, want 104 — a limit past its cap was clamped or truncated", in.SpendPct)
	}
	if in.SpendAt.IsZero() {
		t.Error("the spend limit came through with no reset")
	}
	// A payload with no spend limit says so rather than reporting nought.
	none := ReadStatusLine(map[string]any{"rate_limits": map[string]any{
		"five_hour": map[string]any{"used_percentage": 5, "resets_at": 1789191600}}})
	if none.HasSpend {
		t.Error("a member with no gateway was given a spend limit of zero")
	}
}
