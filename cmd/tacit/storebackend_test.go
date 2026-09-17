// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The default build must not link a Postgres driver. This is the test behind
// the README's dependency claim: pgx is reachable only under -tags pg, so a
// binary built the ordinary way carries none of it. Before the tag existed,
// every build linked the driver and its transitive set for a code path it
// could not reach unless TACIT_DB_URL named a postgres:// URL.
//
// `go list -deps` is the authority rather than a string search over the
// binary, because a linker that drops unreachable symbols would hide the
// import while still forcing the module into every downstream build.
func TestDefaultBuildLinksNoPostgresDriver(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to the go tool")
	}
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, pkg := range strings.Split(string(out), "\n") {
		if strings.Contains(pkg, "jackc/pgx") {
			t.Errorf("default build links %s; the Postgres backend belongs behind -tags pg", pkg)
		}
	}
}

// The counterpart: -tags pg does reach the driver. A gate that silently gated
// everything would pass the test above and ship a binary that cannot talk to
// Postgres at all.
func TestPgTagLinksThePostgresDriver(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to the go tool")
	}
	out, err := exec.Command("go", "list", "-tags", "pg", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	if !strings.Contains(string(out), "jackc/pgx") {
		t.Error("-tags pg does not link pgx; the Postgres backend is unreachable in every build")
	}
}

// An operator who sets a postgres:// URL on a default build gets told which
// build to run. The failure mode this guards is a bare nil-map panic or an
// "unknown driver" error, neither of which names the fix.
func TestUntaggedPostgresOpenNamesTheFix(t *testing.T) {
	if openPostgres != nil {
		t.Skip("built with -tags pg; the backend is wired")
	}
	_, err := postgresStore("postgres://user:hunter2@db.example.com:5432/tacit")
	if err == nil {
		t.Fatal("opening Postgres on an untagged build returned no error")
	}
	if !strings.Contains(err.Error(), "-tags pg") {
		t.Errorf("error does not name the fix: %v", err)
	}
	// The DSN carries a password and this error reaches a log.
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error leaks the DSN: %v", err)
	}
}
