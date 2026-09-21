// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package sqlstore is the SQL implementation of storage.Store, shared by the
// SQLite backend (internal/registry/sqlitestore) and the Postgres backend
// (internal/registry/pgstore).
//
// Both backends run the same statements against the same table shapes, so the
// statements live here once. Every query is written in SQLite's positional
// `?` form — that text is canonical — and Postgres rewrites it through
// Dialect.Rebind (RebindDollar), which renumbers the i-th `?` as `$i`. The
// rewrite is textual but not naive: it skips string literals, quoted
// identifiers and comments, and `??` is the escape for a literal `?` — which is
// how a statement writes Postgres's jsonb operators, spelled the same as a
// placeholder and indistinguishable from one without a real parser.
//
// What is genuinely per-backend stays in the backend package: opening and
// configuring the pool, the migration ledger and its schema.sql, Postgres's
// advisory recompute lock, and SQLite's database-URL parsing. The one
// behavioral difference inside a shared method is Postgres's EXCLUSIVE table
// lock in ReplaceOutcomes, carried here as Dialect.PreReplaceOutcomes.
package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Dialect is everything a backend changes about the shared SQL.
type Dialect struct {
	// Rebind rewrites a canonical `?` query into the driver's placeholder
	// syntax. nil means the driver reads `?` as-is (SQLite).
	Rebind func(query string) string
	// PreReplaceOutcomes is a statement run inside ReplaceOutcomes'
	// transaction before the swap; "" runs none.
	PreReplaceOutcomes string
}

// Store implements every storage.Store method except Close, which each
// backend keeps alongside the pool it owns.
type Store struct {
	db *sql.DB
	d  Dialect
}

// New wraps an already-open, already-migrated pool.
func New(db *sql.DB, d Dialect) *Store { return &Store{db: db, d: d} }

// rebind applies the dialect's placeholder rewrite. Every statement in this
// package goes through one of the five helpers below, so a query can never
// reach a driver un-rebound.
func (s *Store) rebind(query string) string {
	if s.d.Rebind == nil {
		return query
	}
	return s.d.Rebind(query)
}

// StatementTimeout caps how long any one statement may run.
//
// The seam this package sits behind carries no context — the default backend is
// a map behind a mutex, where there would be nothing to cancel — so a statement
// cannot be cut short when the browser that asked for it goes away. The pool is
// sixteen connections wide (pgstore.Open), which is all it takes for a handful
// of abandoned scans to leave the registry with none.
//
// This is a ceiling, not a deadline anybody chose: a statement that reaches it
// has already failed at being a page. Generous enough that no healthy query is
// near it, short enough that a stuck one gives its connection back.
const StatementTimeout = 30 * time.Second

func timeoutCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), StatementTimeout)
}

func (s *Store) exec(query string, args ...any) (sql.Result, error) {
	ctx, cancel := timeoutCtx()
	defer cancel()
	return s.db.ExecContext(ctx, s.rebind(query), args...)
}

// rows carries its query's cancel until Close. A QueryContext's context governs
// the row iteration as well as the statement, so cancelling it when this
// function returns would close the rows before the caller had read one.
type rows struct {
	*sql.Rows
	cancel context.CancelFunc
}

func (r *rows) Close() error {
	err := r.Rows.Close()
	r.cancel()
	return err
}

func (s *Store) query(query string, args ...any) (*rows, error) {
	ctx, cancel := timeoutCtx()
	rs, err := s.db.QueryContext(ctx, s.rebind(query), args...)
	if err != nil {
		cancel()
		return nil, err
	}
	return &rows{Rows: rs, cancel: cancel}, nil
}

// row carries its query's cancel until Scan, for the same reason rows does.
// QueryRowContext does not read the row — Scan does, and it reads it through the
// rows the context governs, so releasing the context on the way out of this
// function makes every Scan that follows fail with "context canceled".
type row struct {
	*sql.Row
	cancel context.CancelFunc
}

func (r *row) Scan(dest ...any) error {
	err := r.Row.Scan(dest...)
	r.cancel()
	return err
}

func (s *Store) queryRow(query string, args ...any) *row {
	ctx, cancel := timeoutCtx()
	return &row{Row: s.db.QueryRowContext(ctx, s.rebind(query), args...), cancel: cancel}
}

func (s *Store) txExec(tx *sql.Tx, query string, args ...any) (sql.Result, error) {
	ctx, cancel := timeoutCtx()
	defer cancel()
	return tx.ExecContext(ctx, s.rebind(query), args...)
}

// txPrepare's context covers the preparation only; the statement it returns
// outlives it, which is what the caller needs.
func (s *Store) txPrepare(tx *sql.Tx, query string) (*sql.Stmt, error) {
	ctx, cancel := timeoutCtx()
	defer cancel()
	return tx.PrepareContext(ctx, s.rebind(query))
}

// RebindDollar rewrites the i-th `?` placeholder as `$i`, the Postgres
// placeholder syntax.
//
// It skips string literals, quoted identifiers and comments, so a `?` inside one
// is left alone. That matters because `?`, `?|` and `?&` are Postgres's jsonb
// operators, and `'a?b'` is an ordinary string: a rewrite that counted every
// question mark in the text would renumber the placeholders around either one
// and produce a query that works on SQLite and is nonsense on Postgres, with
// nothing to see at compile time.
func RebindDollar(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	arg := 0
	for i := 0; i < len(query); {
		switch c := query[i]; {
		case c == '\'' || c == '"':
			// A literal or a quoted identifier, ending at the next unescaped
			// quote of the same kind. Both double an inner quote to escape it,
			// which needs no special case: the closing quote ends this run and
			// the doubled one opens the next.
			j := i + 1
			for j < len(query) && query[j] != c {
				j++
			}
			if j < len(query) {
				j++
			}
			b.WriteString(query[i:j])
			i = j
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			j := strings.IndexByte(query[i:], '\n')
			if j < 0 {
				b.WriteString(query[i:])
				i = len(query)
			} else {
				b.WriteString(query[i : i+j+1])
				i += j + 1
			}
		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			j := strings.Index(query[i+2:], "*/")
			if j < 0 {
				b.WriteString(query[i:])
				i = len(query)
			} else {
				b.WriteString(query[i : i+2+j+2])
				i += 2 + j + 2
			}
		case c == '?' && i+1 < len(query) && query[i+1] == '?':
			// `??` is an escaped literal `?` — how a statement writes Postgres's
			// jsonb operators (? ?| ?&), which are spelled the same as a
			// placeholder and cannot be told apart from one by reading.
			b.WriteByte('?')
			i += 2
		case c == '?':
			arg++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(arg))
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// RebindQuestion is SQLite's rebind: the placeholders are already `?`, so the
// only work is collapsing the `??` escape into the literal `?` it stands for.
// Without it a statement carrying an escaped operator would reach SQLite with
// both characters still in it.
func RebindQuestion(query string) string {
	if !strings.Contains(query, "??") {
		return query
	}
	return strings.ReplaceAll(query, "??", "?")
}

// RunMigrations applies a backend's pending migrations in order, recording
// each in schema_migrations so it runs exactly once per database. The ledger
// stays in the backend package: migrations are append-only history, and the
// two backends have different histories written in different SQL.
func RunMigrations(db *sql.DB, migrations []string, rebind func(string) string) error {
	if _, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var current sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	record := `INSERT INTO schema_migrations (version) VALUES (?)`
	if rebind != nil {
		record = rebind(record)
	}
	for v := int(current.Int64); v < len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(record, v+1); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
