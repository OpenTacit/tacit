// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The published snapshot: the same rates, read from one maintained catalogue
// rather than typed by hand.
//
// The table below this one (pricing.go) was hand-kept, and hand-keeping a price
// list has exactly the failure it sounds like — the OpenAI rows carried "a cache
// write is billed as fresh input" for as long as that was true, and went on
// carrying it after OpenAI published a separate cache-write column. Nothing in
// the file could notice. A generated snapshot can be re-read on a schedule, and
// the day it was read is written beside it.
//
// The source is pydantic/genai-prices (MIT), which aggregates Helicone,
// OpenRouter, LiteLLM and simonw/llm-prices, states its rates per million
// tokens in the four classes this package already prices, and keeps dated
// history so a rate that moved can be read as of a day rather than as of now.
//
// Nothing is fetched at run time. `make prices` writes published.json, the file
// is committed, and go:embed compiles it in — the same shape as every other
// asset here, and the only shape an isolated network allows.
//
// Three rules decide what survives the conversion, and all three are refusals:
//
//  1. FIRST-PARTY PROVIDERS ONLY. The catalogue prices the same model under
//     forty providers, and they disagree — gpt-5.6-sol is $2/M input through
//     OpenRouter, $4 direct and $5.50 on Azure EU. The session log records the
//     harness, never who served the turn, so only the maker's own price can be
//     claimed. A reseller's rate is a fact about the reseller.
//  2. BOTH SIDES OR NOTHING. A model missing an input or an output rate prices
//     nothing rather than half of itself.
//  3. THE BASE TIER, NAMED AS SUCH. Several models cost more above a context
//     threshold (OpenAI's at 272k). The snapshot keeps the base rate, because a
//     session total cannot say which of its requests crossed the line — and
//     says so here rather than in a footnote nobody reads. A long-context
//     session is estimated low, and the page has always called this an estimate.
//
// Where a model reports no cache rates at all, the cache classes mirror input.
// That is not a guess: tokens a provider does not bill as a cache class are
// billed as input, and a harness that reports no cache counts never reaches
// those fields anyway.
package pricing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/modelid"
)

//go:embed published.json
var publishedJSON []byte

// Snapshot is the committed price list: what was read, from where, and when.
type Snapshot struct {
	AsOf      string `json:"as_of"`      // the day the catalogue was read
	Source    string `json:"source"`     // the project, for a reader who wants to check
	SourceURL string `json:"source_url"` // and the file it came from
	// TieredAtBase is how many of these rows are the BASE of a tiered price —
	// cheaper below a context threshold, dearer above it, and kept here at the
	// cheaper one. The page prints it, because an estimate that quietly rounds
	// a long-context month down is the kind of number that gets found out.
	TieredAtBase int             `json:"tiered_at_base"`
	Models       map[string]Rate `json:"models"` // keyed by modelid.Key, as rates is
}

// TieredAtBase reports how many priced models are held at their base rate with
// a dearer long-context tier above. Zero means every rate here is the only rate.
func TieredAtBase() int { return published.TieredAtBase }

// published is the parsed snapshot. A snapshot that will not parse leaves an
// empty map, and every lookup falls through to the hand table — a broken
// generated file must not take the prices down with it.
var published = loadPublished()

func loadPublished() Snapshot {
	var s Snapshot
	if err := json.Unmarshal(publishedJSON, &s); err != nil {
		return Snapshot{}
	}
	return s
}

// firstParty are the providers that make the models they price. Every other id
// in the catalogue is a host or a reseller selling somebody else's model at its
// own margin, which is a different number from the one this page claims to show.
var firstParty = map[string]bool{
	"anthropic": true, "openai": true, "google": true, "deepseek": true,
	"mistral": true, "cohere": true, "moonshotai": true, "x-ai": true,
	"zhipuai": true, "zai": true, "minimax": true, "arcee": true,
	"voyageai": true,
}

// catalogue is the shape this reads out of genai-prices v2. Everything else in
// that file — the API patterns, the token extractors, the model descriptions —
// belongs to calculating prices from a live response, which is not this job.
type catalogue []struct {
	ID     string `json:"id"`
	Models []struct {
		ID     string          `json:"id"`
		Prices json.RawMessage `json:"prices"`
	} `json:"models"`
}

// priceSet is one model's rates. A field is either a number or a tiered object,
// so each is decoded through amount.
type priceSet struct {
	In        *amount `json:"input_mtok"`
	CacheWi   *amount `json:"cache_write_mtok"`
	CacheRead *amount `json:"cache_read_mtok"`
	Out       *amount `json:"output_mtok"`
}

// datedPrices is the other spelling of the same field: the rate history, newest
// last, each entry constrained by the day it took effect.
type datedPrices struct {
	Constraint map[string]any `json:"constraint"`
	Prices     priceSet       `json:"prices"`
}

// amount is a rate that may carry tiers. Only the base survives (rule 3).
type amount struct {
	Base  float64
	Tiers bool
}

func (a *amount) UnmarshalJSON(b []byte) error {
	var n float64
	if err := json.Unmarshal(b, &n); err == nil {
		a.Base = n
		return nil
	}
	var tiered struct {
		Base  float64 `json:"base"`
		Tiers []struct {
			Start float64 `json:"start"`
			Price float64 `json:"price"`
		} `json:"tiers"`
	}
	if err := json.Unmarshal(b, &tiered); err != nil {
		return err
	}
	a.Base, a.Tiers = tiered.Base, len(tiered.Tiers) > 0
	return nil
}

// ConvertReport is what a conversion kept and what it turned away, so the
// generator can print a number a reader can check rather than "done".
type ConvertReport struct {
	Providers int // first-party providers seen
	Models    int // models kept
	Tiered    int // kept at their base rate, with a higher tier above a context size
	NoPrice   int // skipped: no input or no output rate
	NotVendor int // skipped: no vendor could be read from the model's own name
	Conflicts int // skipped: a second provider claiming a key already taken
}

// Convert turns a genai-prices v2 catalogue into the snapshot this package
// embeds. asOf is the day of the read: it stamps the snapshot and it chooses
// between dated rates, so re-running the generator on an old file reproduces
// the old answer rather than today's.
func Convert(raw []byte, asOf time.Time, sourceURL string) (Snapshot, ConvertReport, error) {
	var cat catalogue
	if err := json.Unmarshal(raw, &cat); err != nil {
		return Snapshot{}, ConvertReport{}, fmt.Errorf("genai-prices catalogue: %w", err)
	}
	out := Snapshot{
		AsOf:      asOf.UTC().Format("2006-01-02"),
		Source:    "pydantic/genai-prices",
		SourceURL: sourceURL,
		Models:    map[string]Rate{},
	}
	var rep ConvertReport
	from := map[string]string{} // key -> the provider that claimed it
	for _, p := range cat {
		if !firstParty[p.ID] {
			continue
		}
		rep.Providers++
		for _, m := range p.Models {
			key := modelid.Key(m.ID)
			// A key with no vendor would collide across makers — "pro" from two
			// of them is two models — and the session log keys the same way, so
			// a nameless key could never be looked up anyway.
			if !strings.Contains(key, "/") {
				rep.NotVendor++
				continue
			}
			if owner, taken := from[key]; taken {
				if owner != p.ID {
					rep.Conflicts++
				}
				continue
			}
			set, ok := pricesAsOf(m.Prices, asOf)
			if !ok || set.In == nil || set.Out == nil {
				rep.NoPrice++
				continue
			}
			rate := Rate{In: set.In.Base, Out: set.Out.Base,
				CacheWrite: set.In.Base, CacheRead: set.In.Base}
			if set.CacheWi != nil {
				rate.CacheWrite = set.CacheWi.Base
			}
			if set.CacheRead != nil {
				rate.CacheRead = set.CacheRead.Base
			}
			if set.In.Tiers || set.Out.Tiers {
				rep.Tiered++
			}
			out.Models[key] = rate
			from[key] = p.ID
			rep.Models++
		}
	}
	out.TieredAtBase = rep.Tiered
	return out, rep, nil
}

// pricesAsOf picks the rates in force on the given day.
//
// A model prices either one way for all time, or as a list of dated changes.
// The list is walked for the latest change that had already taken effect; a
// constraint this build does not understand — a time-of-day rate, an off-peak
// discount — disqualifies the whole model, because pricing it on the standard
// rate would quietly overcharge every off-peak session.
func pricesAsOf(raw json.RawMessage, asOf time.Time) (priceSet, bool) {
	if len(raw) == 0 {
		return priceSet{}, false
	}
	var one priceSet
	if err := json.Unmarshal(raw, &one); err == nil {
		return one, true
	}
	var history []datedPrices
	if err := json.Unmarshal(raw, &history); err != nil {
		return priceSet{}, false
	}
	day := asOf.UTC().Format("2006-01-02")
	best, found := priceSet{}, ""
	for _, entry := range history {
		start := ""
		for k, v := range entry.Constraint {
			switch k {
			case "start_date":
				s, _ := v.(string)
				start = s
			case "end_date":
				s, _ := v.(string)
				if s != "" && s < day {
					start = "￿" // ended before this day: never the answer
				}
			default:
				return priceSet{}, false // a rule this build cannot honour
			}
		}
		if start > day {
			continue
		}
		if found == "" || start >= found {
			best, found = entry.Prices, start
		}
	}
	return best, len(history) > 0
}

// Encode renders a snapshot as the committed file: stable key order, one model
// per line, so a price change reads as a one-line diff rather than a reshuffle.
func Encode(s Snapshot) ([]byte, error) {
	keys := make([]string, 0, len(s.Models))
	for k := range s.Models {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "{\n  \"as_of\": %q,\n  \"source\": %q,\n  \"source_url\": %q,\n"+
		"  \"tiered_at_base\": %d,\n  \"models\": {\n",
		s.AsOf, s.Source, s.SourceURL, s.TieredAtBase)
	for i, k := range keys {
		r := s.Models[k]
		line, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		comma := ","
		if i == len(keys)-1 {
			comma = ""
		}
		fmt.Fprintf(&b, "    %q: %s%s\n", k, line, comma)
	}
	b.WriteString("  }\n}\n")
	return []byte(b.String()), nil
}
