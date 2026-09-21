// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

// RebindDollar is the whole safety of the one-text-two-dialects arrangement:
// every shared statement is written once in SQLite's `?` form and reaches
// Postgres only through this rewrite. If it ever miscounts, the SQL still
// parses and the arguments land in the wrong columns — so it is pinned here
// directly rather than only through the backends.

import (
	"strconv"
	"strings"
	"testing"
)

func TestRebindDollar(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// No placeholders: returned untouched, including the literal $ a
		// Postgres-only statement may already carry.
		{`SELECT COUNT(*) FROM techniques`, `SELECT COUNT(*) FROM techniques`},
		{``, ``},
		{`SELECT COUNT(*) FROM techniques WHERE status = 'draft'`,
			`SELECT COUNT(*) FROM techniques WHERE status = 'draft'`},
		// One, then several — numbering is positional, left to right.
		{`DELETE FROM techniques WHERE id=?`, `DELETE FROM techniques WHERE id=$1`},
		{`UPDATE member_keys SET revoked_at = ? WHERE id = ?`,
			`UPDATE member_keys SET revoked_at = $1 WHERE id = $2`},
		{`VALUES (?,?,?)`, `VALUES ($1,$2,$3)`},
		// The same value bound twice is two placeholders and two arguments —
		// the shape UpsertTechnique's status CASE relies on.
		{`SET status=CASE WHEN ? = '' THEN techniques.status ELSE ? END`,
			`SET status=CASE WHEN $1 = '' THEN techniques.status ELSE $2 END`},
		// Past $9: two-digit numbering must not truncate.
		{strings.Repeat("?,", 11), `$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,`},
	} {
		if got := RebindDollar(tc.in); got != tc.want {
			t.Errorf("RebindDollar(%q) = %q (want %q)", tc.in, got, tc.want)
		}
	}
}

// TestRebindDollarThirtyArgs is UpsertTechnique's scale: the widest statement in
// the package binds 30 placeholders across an INSERT and its ON CONFLICT
// UPDATE. Every one must come back distinctly numbered, in order.
func TestRebindDollarThirtyArgs(t *testing.T) {
	const n = 30
	var b strings.Builder
	b.WriteString("INSERT INTO t VALUES (")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
	}
	b.WriteByte(')')
	in := b.String()

	got := RebindDollar(in)
	if strings.Contains(got, "?") {
		t.Fatalf("placeholder left unbound: %s", got)
	}
	if strings.Count(got, "$") != n {
		t.Fatalf("bound %d placeholders (want %d): %s", strings.Count(got, "$"), n, got)
	}
	for i := 1; i <= n; i++ {
		want := "$" + strconv.Itoa(i)
		// A bare Contains would let $3 match inside $30, so check the
		// argument list itself, split on commas.
		found := false
		for _, field := range strings.Split(strings.TrimSuffix(
			strings.TrimPrefix(got, "INSERT INTO t VALUES ("), ")"), ",") {
			if field == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}
