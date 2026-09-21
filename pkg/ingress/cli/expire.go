// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opentacit/tacit/pkg/ingress"
)

// expireTestInstances releases the names of test instances that have stopped
// connecting. A registry declares itself a test at the handshake and nothing
// else ever qualifies, so the only names this can reach are ones somebody asked
// to have reclaimed (ingress.Hello.Test, Config.TestRetentionDays).
//
// It names what it released rather than counting it. A sweep that deletes
// hostnames and reports a number leaves the operator unable to answer the one
// question they will be asked, which is whether theirs was in it.
func expireTestInstances(srv *ingress.Server) {
	days := srv.Cfg.TestRetentionDays
	if days <= 0 {
		return
	}
	before := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	dropped, err := srv.Store.ExpireTests(before)
	if err != nil {
		fmt.Fprintln(os.Stderr, "test instance sweep: "+err.Error())
		return
	}
	if len(dropped) == 0 {
		return
	}
	for _, name := range dropped {
		srv.Disconnect(name)
		srv.Metrics.Drop(name)
	}
	srv.Logf("released %d test %s idle for more than %d days: %s",
		len(dropped), plural(len(dropped), "name", "names"), days, strings.Join(dropped, " "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
