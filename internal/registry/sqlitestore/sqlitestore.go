// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package sqlitestore is the SQLite backend for the registry
// (docs/design/sqlite-plan.md) — the single-machine growth path between the
// embedded file store (pilot) and Postgres (shared / multi-instance). It
// implements exactly the storage.Store contract the other backends implement
// (enforced by the shared conformance suite, which runs against this backend
// on every ungated `go test ./...`).
//
// The statements themselves live in internal/registry/sqlstore, shared with
// pgstore and written in this backend's `?` placeholder form; what stays here
// is what is genuinely SQLite: the driver and pool, the migration ledger and
// schema, and the database-URL parsing.
//
// The driver is modernc.org/sqlite — pure Go, no cgo — so CGO_ENABLED=0
// cross-compilation and the static single binary survive unchanged (the
// dependency decision is recorded in docs/design/sqlite-plan.md; don't
// re-litigate it here). Like pgx, it is unreachable unless configured: the
// backend activates only for a sqlite: TACIT_DB_URL.
//
// Deliberately not implemented: storage.RecomputeLocker. SQLite is a
// single-machine engine; a second registry instance is the Postgres trigger.
package sqlitestore

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // database/sql driver ("sqlite")

	"github.com/opentacit/tacit/internal/registry/sqlstore"
	"github.com/opentacit/tacit/internal/registry/storage"
)

// Store satisfies storage.Store (checked at compile time).
var _ storage.Store = (*Store)(nil)

// Store is the SQLite-backed registry state. Everything but the pool's own
// lifecycle comes from the embedded shared implementation.
type Store struct {
	*sqlstore.Store
	db *sql.DB
}

// PathFromURL extracts the database path from a sqlite: URL —
// "sqlite:///abs/path/tacit.db", "sqlite:tacit.db", "sqlite://tacit.db".
// ok is false for anything else (the caller falls through to Postgres).
// Explicit scheme only: a bare path is never guessed into a database.
func PathFromURL(dbURL string) (path string, ok bool) {
	rest, found := strings.CutPrefix(dbURL, "sqlite:")
	if !found {
		return "", false
	}
	// sqlite:///abs/path -> ///abs/path -> /abs/path; sqlite://rel -> rel.
	rest = strings.TrimPrefix(rest, "//")
	if unescaped, err := url.PathUnescape(rest); err == nil {
		rest = unescaped
	}
	return rest, rest != ""
}

// Open opens (or creates) the database file and applies pending migrations.
// WAL keeps readers concurrent under the registry's single writer process;
// the one-connection pool serializes writes instead of surfacing SQLITE_BUSY.
func Open(path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)
	// No Rebind: `?` is this driver's own placeholder syntax, so the shared
	// SQL runs verbatim. No PreReplaceOutcomes either — see sqlstore's
	// ReplaceOutcomes for why the one-connection pool is lock enough.
	if err := sqlstore.RunMigrations(db, migrations, nil); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitestore migrate: %w", err)
	}
	return &Store{Store: sqlstore.New(db, sqlstore.Dialect{Rebind: sqlstore.RebindQuestion}), db: db}, nil
}

// Close releases the connection.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for backend-specific helpers (tests).
func (s *Store) DB() *sql.DB { return s.db }
