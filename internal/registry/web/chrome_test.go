// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The top bar carries no ambient totals and no build stamp.
//
// It held a run of them — techniques, events, rollups, members, and the running
// build's sha — in the one strip of the interface that never scrolls away,
// which is the most expensive place on the page to spend on a constant nobody
// arrives to read. The counts live on the pages that are about them; the build
// is on /v1/health and in what `make deploy` prints.
func TestTheTopBarCarriesNoAmbientCounts(t *testing.T) {
	s, ts := newServer(t)
	s.Version = "abc1234def-dirty"
	resp, err := http.Get(ts.URL + "/outcomes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	page := string(body)
	bar := page[strings.Index(page, `<header class="top">`):strings.Index(page, "</header>")]
	for _, gone := range []string{"rollups", "techniques ·", `title="running build"`, "abc1234"} {
		if strings.Contains(bar, gone) {
			t.Errorf("the top bar still carries %q", gone)
		}
	}
}
