// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// tacit-genprices writes internal/pricing/published.json from the genai-prices
// catalogue. Run it with `make prices`; the file it writes is committed, and
// nothing fetches anything at run time.
//
// The conversion itself lives in internal/pricing (published.go), so the rules
// that decide what survives are tested against a fixture rather than against
// the network.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/opentacit/tacit/internal/pricing"
)

// sourceURL is the catalogue. v2 is the current schema; the frozen v1 file
// beside it is kept for callers who pinned to it, and is not this one.
const sourceURL = "https://raw.githubusercontent.com/pydantic/genai-prices/main/prices/new_data/v2/data.json"

func main() {
	out := flag.String("out", filepath.Join("internal", "pricing", "published.json"),
		"where to write the snapshot")
	from := flag.String("from", sourceURL, "catalogue to read (a local path also works)")
	day := flag.String("as-of", "", "the day to price as of (default today); dated rates are read on it")
	flag.Parse()

	asOf := time.Now().UTC()
	if *day != "" {
		parsed, err := time.Parse("2006-01-02", *day)
		if err != nil {
			fail("--as-of: %v", err)
		}
		asOf = parsed
	}

	raw, err := read(*from)
	if err != nil {
		fail("%v", err)
	}
	snap, rep, err := pricing.Convert(raw, asOf, sourceURL)
	if err != nil {
		fail("%v", err)
	}
	// A conversion that kept almost nothing means the catalogue moved under us:
	// writing it would replace a working price list with an empty one, and the
	// page would quietly stop pricing anything.
	if rep.Models < 50 {
		fail("kept only %d models from %d first-party providers; the catalogue's shape has changed",
			rep.Models, rep.Providers)
	}
	body, err := pricing.Encode(snap)
	if err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil {
		fail("%v", err)
	}
	fmt.Printf("%s: %d models from %d first-party providers, priced as of %s.\n",
		*out, rep.Models, rep.Providers, snap.AsOf)
	fmt.Printf("%d carry a higher tier above a context size and are kept at their base rate.\n", rep.Tiered)
	fmt.Printf("skipped: %d with no input or output rate, %d whose name names no vendor, %d already claimed.\n",
		rep.NoPrice, rep.NotVendor, rep.Conflicts)
}

func read(from string) ([]byte, error) {
	if _, err := os.Stat(from); err == nil {
		return os.ReadFile(from)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, from, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", from, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tacit-genprices: "+format+"\n", args...)
	os.Exit(1)
}
