// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

import (
	"testing"
	"time"
)

// A statement must not be able to hold a pooled connection forever.
//
// The store seam carries no context, so a query cannot be cancelled when the
// request that wanted it goes away. Sixteen connections (pgstore.Open) and a
// handful of abandoned scans is all it takes to leave the registry unable to
// answer anything, so every statement runs under a ceiling instead.
func TestEveryStatementRunsUnderACeiling(t *testing.T) {
	if StatementTimeout <= 0 {
		t.Fatal("statements run with no ceiling at all")
	}
	if StatementTimeout > 2*time.Minute {
		t.Errorf("StatementTimeout is %v; a statement that slow has already failed at being a page",
			StatementTimeout)
	}
	ctx, cancel := timeoutCtx()
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("the statement context carries no deadline")
	}
	if d := time.Until(deadline); d > StatementTimeout+time.Second {
		t.Errorf("deadline is %v out, want about %v", d, StatementTimeout)
	}
}

// The rows wrapper exists so the cancel outlives the call that made it: a
// QueryContext's context governs the iteration too, so releasing it on return
// would close the rows before the caller read one.
func TestRowsHoldTheirCancelUntilClose(t *testing.T) {
	ctx, cancel := timeoutCtx()
	r := &rows{cancel: cancel}
	select {
	case <-ctx.Done():
		t.Fatal("the query context was already cancelled before any row was read")
	default:
	}
	r.cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Error("closing the rows did not release the query context")
	}
}

// The single-row helper has the same trap as the multi-row one, and it is the
// one that bit: QueryRowContext does not read anything — Scan does, through the
// rows the context governs. Releasing the context when the helper returned made
// every Scan after it fail with "context canceled", which took out the whole
// SQLite conformance suite.
func TestASingleRowSurvivesUntilItIsScanned(t *testing.T) {
	ctx, cancel := timeoutCtx()
	r := &row{cancel: cancel}
	select {
	case <-ctx.Done():
		t.Fatal("the query context was cancelled before the row was scanned")
	default:
	}
	r.cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Error("scanning the row did not release the query context")
	}
}
