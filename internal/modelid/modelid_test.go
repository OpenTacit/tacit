// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package modelid

import "testing"

// The table is the point of the package: every row here is a label seen in the
// wild that must land on the same cohort as its siblings, or on a deliberately
// different one.
func TestNormalize(t *testing.T) {
	cases := []struct{ raw, key, vendor, family string }{
		// One model, four labels, one cohort. This is the whole reason the
		// package exists — a dimension that split these would compute every
		// rate over a quarter of the evidence and say nothing about it.
		{"claude-sonnet-5", "anthropic/claude-sonnet-5", "anthropic", "claude-sonnet-5"},
		{"anthropic/claude-sonnet-5", "anthropic/claude-sonnet-5", "anthropic", "claude-sonnet-5"},
		{"us.anthropic.claude-sonnet-5-v1:0", "anthropic/claude-sonnet-5", "anthropic", "claude-sonnet-5"},
		{"claude-sonnet-5-20260101", "anthropic/claude-sonnet-5", "anthropic", "claude-sonnet-5"},

		// Version punctuation is one spelling.
		{"claude-haiku-4.5", "anthropic/claude-haiku-4-5", "anthropic", "claude-haiku-4-5"},
		{"claude-haiku-4-5", "anthropic/claude-haiku-4-5", "anthropic", "claude-haiku-4-5"},
		{"us.anthropic.claude-haiku-4.5", "anthropic/claude-haiku-4-5", "anthropic", "claude-haiku-4-5"},

		// Release tails go; the model's own name stays.
		{"grok-2-latest", "xai/grok-2", "xai", "grok-2"},
		{"mistral-large-latest", "mistral/mistral-large", "mistral", "mistral-large"},
		{"gemini-2.5-pro-preview", "google/gemini-2-5-pro", "google", "gemini-2-5-pro"},
		{"gemma-2-9b-it", "google/gemma-2-9b", "google", "gemma-2-9b"},

		// Routes name the vendor; pass-through hosts do not pretend to.
		{"azure/gpt-4o", "openai/gpt-4o", "openai", "gpt-4o"},
		{"accounts/fireworks/models/llama-v3p3-70b-instruct", "meta/llama-v3p3-70b", "meta", "llama-v3p3-70b"},
		{"meta-llama/Llama-3.3-70B-Instruct-Turbo", "meta/llama-3-3-70b-instruct-turbo", "meta", "llama-3-3-70b-instruct-turbo"},

		// Sizes and tiers are different models, and must stay apart.
		{"gpt-4o", "openai/gpt-4o", "openai", "gpt-4o"},
		{"gpt-4o-mini", "openai/gpt-4o-mini", "openai", "gpt-4o-mini"},
		{"claude-sonnet-4-6", "anthropic/claude-sonnet-4-6", "anthropic", "claude-sonnet-4-6"},

		{"gemini-2.0-flash", "google/gemini-2-0-flash", "google", "gemini-2-0-flash"},
		{"deepseek-chat", "deepseek/deepseek-chat", "deepseek", "deepseek-chat"},
		{"o3-mini", "openai/o3-mini", "openai", "o3-mini"},

		// Unrecognised passes through cleaned but unmapped: a guessed vendor
		// would invent a cohort, and a wrong cohort is worse than a long key.
		{"acme-thinker-3", "acme-thinker-3", "", "acme-thinker-3"},
		{"acme-thinker-3-20260101", "acme-thinker-3", "", "acme-thinker-3"},
	}
	for _, c := range cases {
		got := Normalize(c.raw)
		if got.Key != c.key || got.Vendor != c.vendor || got.Family != c.family {
			t.Errorf("Normalize(%q) = {vendor:%q family:%q key:%q}, want {vendor:%q family:%q key:%q}",
				c.raw, got.Vendor, got.Family, got.Key, c.vendor, c.family, c.key)
		}
		if got.Raw != c.raw {
			t.Errorf("Normalize(%q) lost the raw label: %q", c.raw, got.Raw)
		}
	}
}

// An absent label is not a cohort called "". Everything downstream keys on Key,
// so an empty one has to mean "don't record a model", not "record the empty
// model" — which would collect every unlabelled session into a fake group.
func TestNormalizeEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t"} {
		if got := Normalize(raw); got.Key != "" || got.Vendor != "" || got.Family != "" {
			t.Errorf("Normalize(%q) = %+v, want the zero ID", raw, got)
		}
	}
	if Key("") != "" {
		t.Errorf("Key(\"\") = %q, want empty", Key(""))
	}
}

// A digit run that is not a date is part of the name. 70b parameters and a
// build id must survive; only a plausible YYYYMMDD goes.
func TestStampStrippingIsNarrow(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-5-20260101": "anthropic/claude-sonnet-5",
		"claude-sonnet-5@20260101": "anthropic/claude-sonnet-5",
		"claude-sonnet-5-19990101": "anthropic/claude-sonnet-5-19990101",
		"llama-3-70000000":         "meta/llama-3-70000000",
	}
	for raw, want := range cases {
		if got := Key(raw); got != want {
			t.Errorf("Key(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The stamp has a second spelling. Amp reports OpenAI models both ways in one
// thread — "gpt-5.5" on six requests and "gpt-5.5-2026-04-23" on seven — and
// the hyphenated one used to keep its date, which made one model two cohorts
// and halved the evidence under each. Narrow for the same reason as the
// compact form: a version that merely looks datelike keeps its name.
func TestHyphenatedDateStampsFoldIntoTheFamily(t *testing.T) {
	cases := map[string]string{
		"gpt-5.5-2026-04-23": "openai/gpt-5-5",
		"gpt-5.5":            "openai/gpt-5-5",
		"gpt-5.5@2026-04-23": "openai/gpt-5-5",
		// Not dates: a thirteenth month, a thirty-second day, a year that is
		// not 20xx, and a model whose name simply ends in numbers.
		"gpt-5.5-2026-13-01":  "openai/gpt-5-5-2026-13-01",
		"gpt-5.5-2026-04-32":  "openai/gpt-5-5-2026-04-32",
		"gpt-5.5-1999-04-23":  "openai/gpt-5-5-1999-04-23",
		"qwen-2.5-72b-14-2-1": "alibaba/qwen-2-5-72b-14-2-1",
		"gpt-6-astra":         "openai/gpt-6-astra",
	}
	for raw, want := range cases {
		if got := Key(raw); got != want {
			t.Errorf("Key(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Idempotence: a key that has already been normalised must normalise to
// itself, or a re-run of the backfill would keep moving the cohort.
func TestNormalizeIsIdempotent(t *testing.T) {
	for _, raw := range []string{
		"claude-sonnet-5", "us.anthropic.claude-haiku-4.5", "gpt-4o-mini",
		"accounts/fireworks/models/llama-v3p3-70b-instruct", "acme-thinker-3",
	} {
		once := Key(raw)
		if twice := Key(once); twice != once {
			t.Errorf("Key(%q) = %q, but Key(%q) = %q", raw, once, once, twice)
		}
	}
}

// THE VENDOR LIST IS NOT A GATE. Providers arrive faster than any list of them,
// and every member runs a different handful of them — so a key this build has
// never seen still comes back as something a heading can carry. The map holds
// casing, never permission.
func TestLabelNamesAVendorItHasNeverSeen(t *testing.T) {
	for key, want := range map[string]string{
		"anthropic":   "Anthropic",
		"openai":      "OpenAI", // a capital alone gets this one wrong
		"xai":         "xAI",
		"mistral":     "Mistral",
		"newprovider": "Newprovider", // never heard of, still named
		"gpt-5-x":     "Gpt-5-x",     // a model family, where no vendor was known
		"":            "",            // nothing to go on stays nothing
	} {
		if got := Label(key); got != want {
			t.Errorf("Label(%q) = %q, want %q", key, got, want)
		}
	}
}
