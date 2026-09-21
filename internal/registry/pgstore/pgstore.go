// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

//go:build pg

// Package pgstore is the Postgres backend for the registry
// (docs/design/postgres-plan.md) — the shared / multi-instance path. It implements
// exactly the storage.Store contract the file store implements (enforced by
// the shared conformance suite): upsert preserves runtime state, event ingest
// is idempotent on event_id, decay flips status only between stable/decayed.
//
// The statements themselves live in internal/registry/sqlstore, shared with
// sqlitestore and written in SQLite's `?` placeholder form; RebindDollar
// renumbers them into Postgres's $N on the way to the driver. What stays here
// is what is genuinely Postgres: the driver and pool, the migration ledger and
// schema, the EXCLUSIVE table lock ReplaceOutcomes takes, and the advisory
// lock that elects one rollup runner across instances.
//
// This package is behind the `pg` build tag, and it is the only importer of
// pgx. An untagged build of tacit therefore links no Postgres driver at all —
// cmd/tacit/storebackend.go is the seam, and cmd/tacit/storebackend_test.go
// holds the claim. Released binaries and `make install` pass the tag; a
// default `go build` does not, and refuses a postgres:// URL by name.
//
// The file store remains the zero-dependency default; this backend activates
// when TACIT_DB_URL is set (legacy aliases still accepted) on a tagged build.
package pgstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver ("pgx")

	"github.com/opentacit/tacit/internal/registry/sqlstore"
	"github.com/opentacit/tacit/internal/registry/storage"
)

// Store satisfies storage.Store (checked at compile time).
var _ storage.Store = (*Store)(nil)

// Store satisfies storage.RecomputeLocker too — the shared backend is the one
// that needs cross-instance election.
var _ storage.RecomputeLocker = (*Store)(nil)

// Store is the Postgres-backed registry state. Everything but the pool's own
// lifecycle and this backend's two locks comes from the embedded shared
// implementation.
type Store struct {
	*sqlstore.Store
	db *sql.DB
}

// dialect is what this backend changes about the shared SQL: $N placeholders,
// and the EXCLUSIVE table lock ReplaceOutcomes takes before the swap.
var dialect = sqlstore.Dialect{
	Rebind:             sqlstore.RebindDollar,
	PreReplaceOutcomes: `LOCK TABLE technique_outcomes IN EXCLUSIVE MODE`,
}

// Open connects, configures the pool, and applies pending migrations.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := sqlstore.RunMigrations(db, migrations, dialect.Rebind); err != nil {
		db.Close()
		return nil, fmt.Errorf("pgstore migrate: %w", err)
	}
	return &Store{Store: sqlstore.New(db, dialect), db: db}, nil
}

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the pool for backend-specific helpers (tests, migrate-store).
func (s *Store) DB() *sql.DB { return s.db }

// --- multi-instance coordination ----------------------------------------------

// recomputeLockKey namespaces the advisory lock tacit uses to elect a single
// rollup runner across instances (docs/design/postgres-plan.md, open question 1).
// The value is "oreach" + 1, from the pre-rename project name — FROZEN:
// changing the key would let two instances (one old, one new) recompute
// concurrently during a rolling upgrade.
const recomputeLockKey = 0x6f726561636801 // "oreach" + 1

// TryRecomputeLock implements storage.RecomputeLocker: at most one instance
// holds it at a time. Advisory locks are per-connection, so the lock pins a
// pooled connection until release.
func (s *Store) TryRecomputeLock() (release func(), ok bool, err error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	var got bool
	if err := conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock($1)`, recomputeLockKey).Scan(&got); err != nil {
		conn.Close()
		return nil, false, err
	}
	if !got {
		conn.Close()
		return nil, false, nil
	}
	return func() {
		_, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, recomputeLockKey)
		conn.Close()
	}, true, nil
}
