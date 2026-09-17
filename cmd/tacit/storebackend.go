// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/opentacit/tacit/internal/registry/storage"
)

// The Postgres backend is behind the `pg` build tag, so a default build of
// tacit links no database driver at all.
//
// The reason is the claim the README makes. The embedded file store is the
// default and needs nothing; Postgres is the shared multi-instance path and
// most operators never reach it. Importing internal/registry/pgstore from here
// unconditionally linked pgx and its transitive set into every binary — a
// driver the process could not reach unless TACIT_DB_URL named a postgres://
// URL. Gating it costs one flag on the builds that want it (the Makefile and
// the release workflow both pass it) and lets the default build be honestly
// described as the standard library plus modernc.org/sqlite.
//
// openPostgres is nil in an untagged build. storebackend_pg.go sets it.
var openPostgres func(dsn string) (storage.Store, error)

// postgresStore opens the Postgres backend, or explains that this binary was
// not built with it. The error names the fix rather than the missing symbol,
// because the operator who hits this configured a DB URL and got a refusal —
// what they need to know is which build to run, not which package is absent.
//
// The DSN never reaches the message. It carries a password, this error reaches
// a log, and the operator already knows what they set.
func postgresStore(dsn string) (storage.Store, error) {
	if openPostgres == nil {
		return nil, fmt.Errorf("this build has no Postgres support: rebuild tacit with -tags pg, " +
			"or point TACIT_DB_URL at a sqlite: path instead")
	}
	return openPostgres(dsn)
}
