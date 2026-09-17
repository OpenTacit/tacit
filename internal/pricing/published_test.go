// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package pricing

import (
	"encoding/json"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// A cut-down catalogue in the shape genai-prices publishes: a maker, a reseller
// of the same model, a rate that changed on a date, a tiered rate, a model with
// no cache prices, and one priced by a rule this build cannot honour.
const fixture = `[
 {"id":"anthropic","models":[
   {"id":"claude-opus-5","prices":{"input_mtok":5,"cache_write_mtok":6.25,"cache_read_mtok":0.5,"output_mtok":25,"cache_write_1h_mtok":10}}]},
 {"id":"openai","models":[
   {"id":"gpt-5.6-sol","prices":[
     {"prices":{"input_mtok":{"base":5,"tiers":[{"start":271999,"price":10}]},"cache_write_mtok":6.25,"cache_read_mtok":0.5,"output_mtok":30}},
     {"constraint":{"start_date":"2026-08-21"},
      "prices":{"input_mtok":{"base":4,"tiers":[{"start":271999,"price":8}]},"cache_write_mtok":5,"cache_read_mtok":0.4,"output_mtok":20}}]},
   {"id":"gpt-9-nocache","prices":{"input_mtok":3,"output_mtok":9}},
   {"id":"gpt-9-halfpriced","prices":{"input_mtok":3}}]},
 {"id":"deepseek","models":[
   {"id":"deepseek-v4","prices":[
     {"constraint":{"off_peak":true},"prices":{"input_mtok":0.1,"output_mtok":0.5}}]}]},
 {"id":"openrouter","models":[
   {"id":"gpt-5.6-sol","prices":{"input_mtok":2,"cache_write_mtok":2.5,"cache_read_mtok":0.2,"output_mtok":10}}]}
]`

func convert(t *testing.T, on string) (Snapshot, ConvertReport) {
	t.Helper()
	s, rep, err := Convert([]byte(fixture), day(on), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	return s, rep
}

// The same model is sold by forty providers at forty prices, and the session
// log records the harness rather than who served the turn. So only the maker's
// own rate can be claimed: a reseller's margin is a fact about the reseller.
func TestOnlyTheMakersOwnPriceIsKept(t *testing.T) {
	snap, _ := convert(t, "2026-09-13")
	got, ok := snap.Models["openai/gpt-5-6-sol"]
	if !ok {
		t.Fatal("gpt-5.6-sol is not priced at all")
	}
	if got.In != 4 {
		t.Errorf("input = %v, want OpenAI's own 4 rather than a reseller's", got.In)
	}
}

// A rate that changed has a before and an after, and which one is true depends
// on the day. Reading the newest would reprice last month's sessions at this
// month's rates, which is the quiet way a cost history stops being a history.
func TestADatedRateIsReadOnTheDayAsked(t *testing.T) {
	for _, tc := range []struct {
		on      string
		in, out float64
	}{
		{"2026-09-13", 4, 20}, // after the change
		{"2026-08-21", 4, 20}, // the day it took effect
		{"2026-08-20", 5, 30}, // the day before
	} {
		snap, _ := convert(t, tc.on)
		got := snap.Models["openai/gpt-5-6-sol"]
		if got.In != tc.in || got.Out != tc.out {
			t.Errorf("as of %s: in/out = %v/%v, want %v/%v", tc.on, got.In, got.Out, tc.in, tc.out)
		}
	}
}

// A tiered model keeps its base rate, because a session total cannot say which
// of its requests crossed the threshold. The count is reported so the generator
// can say how many rows carry the caveat rather than leaving it unsaid.
func TestATieredRateKeepsItsBaseAndIsCounted(t *testing.T) {
	snap, rep := convert(t, "2026-09-13")
	if got := snap.Models["openai/gpt-5-6-sol"].In; got != 4 {
		t.Errorf("input = %v, want the base rate rather than the long-context one", got)
	}
	if rep.Tiered != 1 {
		t.Errorf("tiered = %d, want the one model that has a higher tier", rep.Tiered)
	}
}

// A model with no cache rates has its cache classes mirror input. That is not a
// guess: a provider that does not bill a cache class bills those tokens as
// input, and a harness reporting no cache counts never reaches the fields.
func TestAModelWithoutCachePricesChargesCacheAsInput(t *testing.T) {
	snap, _ := convert(t, "2026-09-13")
	got, ok := snap.Models["openai/gpt-9-nocache"]
	if !ok {
		t.Fatal("a model with no cache prices was dropped; it is still priceable")
	}
	if got.CacheRead != 3 || got.CacheWrite != 3 {
		t.Errorf("cache rates = %v/%v, want input's 3 rather than an invented discount",
			got.CacheRead, got.CacheWrite)
	}
}

// Half a price is not a cheaper price. A model missing one side of the trade,
// or priced by a rule this build does not implement — an off-peak window, a
// time of day — prices nothing, and the count says so.
func TestHalfAPriceIsNoPrice(t *testing.T) {
	snap, rep := convert(t, "2026-09-13")
	for _, key := range []string{"openai/gpt-9-halfpriced", "deepseek/deepseek-v4"} {
		if r, ok := snap.Models[key]; ok {
			t.Errorf("%s was priced %v; want no row at all", key, r)
		}
	}
	if rep.NoPrice != 2 {
		t.Errorf("skipped %d for want of a price, want both", rep.NoPrice)
	}
}

// The committed snapshot is what ships, so it is checked here rather than
// trusted: a generator run against a moved schema that wrote plausible-looking
// rubbish would otherwise reach the page as confident numbers.
func TestTheCommittedSnapshotIsSane(t *testing.T) {
	if published.AsOf == "" {
		t.Fatal("the committed snapshot has no as_of, so every estimate on the page is undated")
	}
	if _, err := time.Parse("2006-01-02", published.AsOf); err != nil {
		t.Errorf("as_of %q is not a day: %v", published.AsOf, err)
	}
	if len(published.Models) < 50 {
		t.Fatalf("the snapshot prices %d models; that is a failed generation, not a price list",
			len(published.Models))
	}
	for key, r := range published.Models {
		switch {
		case r.In <= 0 || r.Out <= 0:
			t.Errorf("%s prices at %v in / %v out; a free model is a parse failure, not a bargain",
				key, r.In, r.Out)
		case r.CacheRead > r.In:
			t.Errorf("%s reads cache at %v against %v fresh input, which no vendor charges",
				key, r.CacheRead, r.In)
		case r.CacheWrite > r.In*3:
			t.Errorf("%s writes cache at %v against %v input, far past any published premium",
				key, r.CacheWrite, r.In)
		}
	}
	// The four this file was built to get right, read off OpenAI's own pricing
	// page on 2026-09-13. They are here because the hand table had all four
	// wrong in the same place for as long as nobody looked.
	for key, want := range map[string]Rate{
		"openai/gpt-6-astra":   {In: 10, CacheWrite: 12.50, CacheRead: 1, Out: 50},
		"openai/gpt-5-6-sol":   {In: 4, CacheWrite: 5, CacheRead: 0.40, Out: 20},
		"openai/gpt-5-6-terra": {In: 2, CacheWrite: 2.50, CacheRead: 0.20, Out: 12},
		"openai/gpt-5-6-luna":  {In: 0.20, CacheWrite: 0.25, CacheRead: 0.02, Out: 1.20},
	} {
		if got := published.Models[key]; got != want {
			t.Errorf("%s = %+v, want %+v", key, got, want)
		}
	}
}

// The snapshot answers first and the hand table catches what it misses, so a
// model dropped from the catalogue does not silently stop being priced.
func TestTheHandTableAnswersWhatTheSnapshotDoesNot(t *testing.T) {
	prior := published
	t.Cleanup(func() { published = prior })
	published = Snapshot{AsOf: "2026-01-01", Models: map[string]Rate{
		"anthropic/claude-opus-5": {In: 99, CacheWrite: 99, CacheRead: 99, Out: 99},
	}}
	if got, _ := Lookup("claude-opus-5"); got.In != 99 {
		t.Errorf("input = %v, want the snapshot's rate ahead of the fallback's", got.In)
	}
	if _, ok := Lookup("claude-haiku-4-5"); !ok {
		t.Error("a model the snapshot does not carry lost its price instead of falling back")
	}
	if _, ok := Lookup("a-model-nobody-published"); ok {
		t.Error("an unknown model was priced; a miss must stay a miss")
	}
}

// Encode is what the generator writes, and a diff that reshuffled every line
// whenever a price moved would make a price change impossible to review.
func TestTheWrittenFileIsStableAndParsesBack(t *testing.T) {
	snap, _ := convert(t, "2026-09-13")
	first, err := Encode(snap)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encode(snap)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("two encodings of one snapshot differ; the committed file would churn")
	}
	var back Snapshot
	if err := json.Unmarshal(first, &back); err != nil {
		t.Fatalf("the file this writes does not parse: %v", err)
	}
	if back.AsOf != snap.AsOf || len(back.Models) != len(snap.Models) {
		t.Errorf("round trip lost something: %+v", back)
	}
}
