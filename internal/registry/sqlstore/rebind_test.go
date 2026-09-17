// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

import "testing"

// The rewrite renumbers placeholders, and a `?` is only a placeholder outside a
// literal, an identifier or a comment.
//
// Postgres spells three jsonb operators with a question mark — ? ?| ?& — and a
// string may simply contain one. Counting every `?` in the text renumbered the
// arguments around them: SQLite reads the canonical form and never notices,
// Postgres gets $2 where $1 was meant, and nothing says so until a query runs.
func TestRebindOnlyRenumbersRealPlaceholders(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"plain placeholders",
			`SELECT a FROM t WHERE b = ? AND c = ?`,
			`SELECT a FROM t WHERE b = $1 AND c = $2`},
		{"a question mark inside a string literal",
			`SELECT ? FROM t WHERE label = 'why? because' AND id = ?`,
			`SELECT $1 FROM t WHERE label = 'why? because' AND id = $2`},
		// A bare `?` cannot be told apart from a placeholder by reading, so an
		// operator is written `??` — and comes out the other side as itself.
		{"the jsonb has-key operator, escaped",
			`SELECT ? FROM t WHERE segment ?? 'model' AND id = ?`,
			`SELECT $1 FROM t WHERE segment ? 'model' AND id = $2`},
		{"a quoted identifier",
			`SELECT "odd?name" FROM t WHERE id = ?`,
			`SELECT "odd?name" FROM t WHERE id = $1`},
		{"a line comment",
			"SELECT a -- is this it?\nFROM t WHERE id = ?",
			"SELECT a -- is this it?\nFROM t WHERE id = $1"},
		{"a block comment",
			`SELECT a /* really? */ FROM t WHERE id = ?`,
			`SELECT a /* really? */ FROM t WHERE id = $1`},
		{"a doubled quote inside a literal",
			`SELECT ? FROM t WHERE s = 'it''s here? yes' AND id = ?`,
			`SELECT $1 FROM t WHERE s = 'it''s here? yes' AND id = $2`},
		{"nothing to do", `SELECT 1`, `SELECT 1`},
	} {
		if got := RebindDollar(c.in); got != c.want {
			t.Errorf("%s:\n  got  %s\n  want %s", c.name, got, c.want)
		}
	}
}

// Every statement this package ships must still survive the rewrite unchanged in
// meaning: the canonical text is SQLite's, and Postgres only ever sees the
// rewritten form.
func TestRebindIsStableOnASecondPass(t *testing.T) {
	q := `SELECT a FROM t WHERE b = ? AND c = 'x?y' AND d = ?`
	once := RebindDollar(q)
	if twice := RebindDollar(once); twice != once {
		t.Errorf("rebinding an already-rebound query changed it:\n  %s\n  %s", once, twice)
	}
}

// SQLite reads `?` as a placeholder already, so its rebind exists only to spend
// the escape: a statement carrying `??` must reach the driver with one.
func TestSQLiteRebindSpendsTheEscape(t *testing.T) {
	got := RebindQuestion(`SELECT ? FROM t WHERE segment ?? 'model' AND id = ?`)
	want := `SELECT ? FROM t WHERE segment ? 'model' AND id = ?`
	if got != want {
		t.Errorf("\n  got  %s\n  want %s", got, want)
	}
	if q := `SELECT a FROM t WHERE id = ?`; RebindQuestion(q) != q {
		t.Error("a query with no escape in it was rewritten")
	}
}
