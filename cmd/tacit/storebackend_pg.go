// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

//go:build pg

package main

import (
	"github.com/opentacit/tacit/internal/registry/pgstore"
	"github.com/opentacit/tacit/internal/registry/storage"
)

// Wire the Postgres backend into the one place that opens a store. This file
// is the only importer of internal/registry/pgstore, which is what keeps pgx
// out of an untagged build — see storebackend.go for why that matters.
func init() {
	openPostgres = func(dsn string) (storage.Store, error) {
		return pgstore.Open(dsn)
	}
}
