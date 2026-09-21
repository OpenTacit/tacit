// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sessionhash

import "testing"

func TestHash(t *testing.T) {
	if got := Hash("org-salt", "claude:sess_1"); got != "8c72ff287e99e506b1cfb67ae336e4e2" {
		t.Fatalf("hash = %q", got)
	}
	for _, tc := range [][2]string{{"", "key"}, {"salt", ""}} {
		if got := Hash(tc[0], tc[1]); got != "" {
			t.Fatalf("Hash(%q, %q) = %q, want empty", tc[0], tc[1], got)
		}
	}
}
