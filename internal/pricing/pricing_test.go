// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package pricing

import (
	"math"
	"testing"

	"github.com/opentacit/tacit/internal/modelid"
)

// A model nobody published a price for must estimate NOTHING. The failure this
// guards is the one that would discredit the whole figure: a miss returning
// (0, true) prices an unknown model at zero, and a member reads a month of work
// on a new model as a month that cost nothing.
func TestAnUnpricedModelEstimatesNothing(t *testing.T) {
	for _, raw := range []string{"", "some-model-nobody-has-heard-of", "llama-4-70b"} {
		if usd, ok := Estimate(raw, 1000, 1000, 1000, 1000); ok {
			t.Errorf("%q priced at $%v — it has no published rate here", raw, usd)
		}
	}
}

// The tiered models used to be left out for the same reason: their price was a
// range that depended on prompt size, and half a price is not a cheaper price.
// The catalogue resolved that — a tier is now a base rate and a threshold, not
// a range — so they are priced at the base, which is the true rate for every
// request under the line and an understatement above it.
//
// The rule that replaced the refusal is that the understatement must be SAID.
// The snapshot counts these rows and the page's own source line tells the
// member, which is the whole of what the old exclusion was protecting.
func TestATieredModelIsPricedAtItsBaseAndTheCountSaysSo(t *testing.T) {
	r, ok := Lookup("gemini-3-1-pro")
	if !ok {
		t.Fatal("a tiered model prices nothing; it has a published base rate")
	}
	if r.In <= 0 || r.Out <= 0 {
		t.Errorf("tiered model priced at %+v", r)
	}
	if TieredAtBase() == 0 {
		t.Error("no row is flagged as a base-of-tier, so the page cannot tell the member")
	}
	if TieredAtBase() >= Models() {
		t.Errorf("%d of %d models flagged as tiered; that is a counting bug",
			TieredAtBase(), Models())
	}
}

// The same model reaches OpenTacit under four labels, and all four have to find the
// row. This is the whole reason the table is keyed by modelid.Key rather than by
// whatever the harness said.
func TestOneModelIsPricedUnderEveryLabelItArrivesAs(t *testing.T) {
	want, ok := Lookup("claude-opus-5")
	if !ok {
		t.Fatal("the model this was written on has no rate")
	}
	for _, raw := range []string{
		"claude-opus-5",
		"anthropic/claude-opus-5",
		"claude-opus-5-20260101",
		"us.anthropic.claude-opus-5-v1:0",
	} {
		got, ok := Lookup(raw)
		if !ok {
			t.Errorf("%q found no rate (key %q)", raw, modelid.Key(raw))
			continue
		}
		if got != want {
			t.Errorf("%q priced %v, want %v — one model, one rate", raw, got, want)
		}
	}
}

// The published worked example, computed here. Anthropic's own pricing page
// works through a one-hour Opus 5 session: 10,000 uncached input, 40,000 cache
// reads and 15,000 output come to $0.445 in tokens. If this arithmetic ever
// stops matching the vendor's own, the table is wrong or the formula is.
func TestTheArithmeticMatchesTheVendorsOwnWorkedExample(t *testing.T) {
	usd, ok := Estimate("claude-opus-5", 10_000, 0, 40_000, 15_000)
	if !ok {
		t.Fatal("no rate")
	}
	want := 0.05 + 0.02 + 0.375
	if math.Abs(usd-want) > 1e-9 {
		t.Errorf("estimate = $%.6f, want $%.6f", usd, want)
	}
}

// The input side is three prices, not one. A session that is nearly all cache
// reads costs a fraction of the same tokens priced as fresh input, and pricing
// the sum instead of the split would overstate a real bill by an order of
// magnitude — which is exactly the reading the cache split exists to give.
func TestTheSplitIsPricedApartFromTheSum(t *testing.T) {
	const fresh, write, read = 3_100, 21_400, 402_000
	split, ok := Estimate("claude-opus-5", fresh, write, read, 0)
	if !ok {
		t.Fatal("no rate")
	}
	lumped, _ := Estimate("claude-opus-5", fresh+write+read, 0, 0, 0)
	if split >= lumped {
		t.Fatalf("split $%.4f is not cheaper than lumped $%.4f", split, lumped)
	}
	if ratio := lumped / split; ratio < 3 {
		t.Errorf("lumped/split = %.1fx; the cache discount has stopped mattering", ratio)
	}
}

// Every row has to be a whole price. A rate missing its output price, or
// carrying a negative, produces an estimate that is quietly too low forever.
func TestEveryRateIsComplete(t *testing.T) {
	for key, r := range rates {
		for name, v := range map[string]float64{
			"in": r.In, "cache write": r.CacheWrite, "cache read": r.CacheRead, "out": r.Out,
		} {
			if v <= 0 {
				t.Errorf("%s: %s price is %v", key, name, v)
			}
		}
		if r.CacheRead > r.In {
			t.Errorf("%s: a cache read (%v) costs more than fresh input (%v)", key, r.CacheRead, r.In)
		}
		if r.Out < r.In {
			t.Errorf("%s: output (%v) is cheaper than input (%v) — check the row", key, r.Out, r.In)
		}
		if modelid.Key(key) != key {
			t.Errorf("%s is not a canonical key; it would never be found", key)
		}
	}
}
