// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package modelid canonicalises the model labels harnesses report.
//
// One model reaches OpenTacit under several names. A Claude Code hook payload says
// `claude-sonnet-5`, a gateway route says `anthropic/claude-sonnet-5`, Bedrock
// says `us.anthropic.claude-sonnet-5-v1:0`, and a dated release says
// `claude-sonnet-5-20260101`. All four are one cohort, and a cohort dimension
// that splits them is worse than no dimension at all: every rate it carries is
// computed over a quarter of the evidence, and nothing says so.
//
// So the label is split into the parts different consumers need. `Key` is the
// cohort — what goes on a segment, keys a rollup and groups a chart. `Raw` is
// what the harness actually said — what a support_matrix row records and what
// the model-change report names, because "your default moved from
// claude-sonnet-5-20260101 to claude-sonnet-5-20260415" is the sentence the
// member needs and the family alone cannot say it.
//
// The vendor table is deliberately small and the unrecognised case passes
// through cleaned but unmapped. Guessing a vendor from an unknown prefix would
// invent a cohort; leaving it alone costs only a slightly longer key.
//
// This package sits outside both internal/auditor and internal/registry
// because both ends need the same answer — the capture path to write the
// segment, the registry to read a label back for display and backfill — and a
// second implementation would drift.
package modelid

import (
	"strings"
)

// ID is one model label, split.
type ID struct {
	Raw    string // exactly what the harness reported, untouched
	Vendor string // "anthropic", "openai", … ; "" when unrecognised
	Family string // "claude-sonnet-5" — version kept, punctuation and stamps normalised
	Key    string // "anthropic/claude-sonnet-5" — the cohort value; "" for an empty label
}

// vendors maps a route or dotted prefix to the vendor name OpenTacit uses. Keys are
// what appears in a label; values are what a cohort is called.
var vendors = map[string]string{
	"anthropic":   "anthropic",
	"openai":      "openai",
	"azure":       "openai",
	"google":      "google",
	"googleai":    "google",
	"vertex_ai":   "google",
	"vertexai":    "google",
	"meta":        "meta",
	"meta-llama":  "meta",
	"mistral":     "mistral",
	"mistralai":   "mistral",
	"xai":         "xai",
	"x-ai":        "xai",
	"deepseek":    "deepseek",
	"cohere":      "cohere",
	"alibaba":     "alibaba",
	"qwen":        "alibaba",
	"moonshotai":  "moonshot",
	"zhipuai":     "zhipu",
	"amazon":      "amazon",
	"microsoft":   "microsoft",
	"perplexity":  "perplexity",
	"together":    "",
	"openrouter":  "",
	"bedrock":     "",
	"fireworks":   "",
	"groq":        "",
	"litellm":     "",
	"accounts":    "",
	"models":      "",
	"replicate":   "",
	"ollama":      "",
	"lm_studio":   "",
	"deepinfra":   "",
	"anyscale":    "",
	"cloudflare":  "",
	"nebius":      "",
	"hyperbolic":  "",
	"novita":      "",
	"sambanova":   "",
	"cerebras":    "",
	"baseten":     "",
	"aiml":        "",
	"nvidia":      "",
	"watsonx":     "",
	"sagemaker":   "",
	"databricks":  "",
	"snowflake":   "",
	"github":      "",
	"copilot":     "",
	"inference":   "",
	"maas":        "",
	"publishers":  "",
	"projects":    "",
	"locations":   "",
	"deployments": "",
}

// families maps a model-name prefix to its vendor, for labels that arrive with
// no route at all — the common case, since most harnesses report the bare name.
// Longest prefix wins, so "gpt-oss" can differ from "gpt".
var families = []struct{ prefix, vendor string }{
	{"claude", "anthropic"},
	{"gpt-oss", "openai"},
	{"gpt", "openai"},
	{"chatgpt", "openai"},
	{"o1", "openai"},
	{"o3", "openai"},
	{"o4", "openai"},
	{"text-davinci", "openai"},
	{"codex", "openai"},
	{"gemini", "google"},
	{"gemma", "google"},
	{"palm", "google"},
	{"llama", "meta"},
	{"codellama", "meta"},
	{"mistral", "mistral"},
	{"mixtral", "mistral"},
	{"codestral", "mistral"},
	{"magistral", "mistral"},
	{"devstral", "mistral"},
	{"ministral", "mistral"},
	{"grok", "xai"},
	{"deepseek", "deepseek"},
	{"qwen", "alibaba"},
	{"qwq", "alibaba"},
	{"command", "cohere"},
	{"phi", "microsoft"},
	{"nova", "amazon"},
	{"titan", "amazon"},
	{"glm", "zhipu"},
	{"kimi", "moonshot"},
	{"sonar", "perplexity"},
	{"jamba", "ai21"},
	{"granite", "ibm"},
}

// dropSuffix are the tails that name a release rather than a model. Stripping
// them is what merges a dated build with its own family; keeping them would
// split one cohort per release, which is the failure this package exists to
// prevent.
var dropSuffix = []string{"-latest", "-preview", "-exp", "-experimental", "-instruct", "-it"}

// Normalize splits a raw model label. An empty or whitespace-only label yields
// the zero ID, which every caller treats as "no model known" — never as a
// cohort called "".
func Normalize(raw string) ID {
	id := ID{Raw: strings.TrimSpace(raw)}
	if id.Raw == "" {
		return id
	}
	label := strings.ToLower(id.Raw)

	// Bedrock-style dotted routing: us.anthropic.claude-… . Only strip when a
	// dot segment names a vendor, because a dot is also a version separator
	// (claude-haiku-4.5) and eating that would be worse than keeping a prefix.
	if parts := strings.Split(label, "."); len(parts) > 1 {
		for i, p := range parts {
			if v, ok := vendors[p]; ok && v != "" {
				id.Vendor = v
				label = strings.Join(parts[i+1:], ".")
				break
			}
		}
	}

	// Route-style prefixes: anthropic/claude-…, accounts/fireworks/models/llama-… .
	if segs := strings.Split(label, "/"); len(segs) > 1 {
		for _, s := range segs[:len(segs)-1] {
			if v, ok := vendors[s]; ok && v != "" && id.Vendor == "" {
				id.Vendor = v
			}
		}
		label = segs[len(segs)-1]
	}

	label = cleanLabel(label)
	if id.Vendor == "" {
		id.Vendor = vendorOf(label)
	}
	id.Family = label
	if label == "" {
		// A label that was nothing but a route and a stamp tells us nothing.
		return ID{Raw: id.Raw}
	}
	id.Key = label
	if id.Vendor != "" {
		id.Key = id.Vendor + "/" + label
	}
	return id
}

// Key is Normalize(raw).Key, for the many callers that want only the cohort.
func Key(raw string) string { return Normalize(raw).Key }

// cleanLabel strips the parts of a name that identify a release rather than a
// model, and settles punctuation so 4.5 and 4-5 are one version.
func cleanLabel(label string) string {
	label = strings.TrimSpace(label)
	// Bedrock version tail: -v1:0, :0, :free.
	if i := strings.IndexByte(label, ':'); i >= 0 {
		label = label[:i]
	}
	label = strings.TrimSuffix(label, "-v1")
	label = strings.TrimSuffix(label, "-v2")

	// A date stamp, however it is joined: @20260101, -20260101, _20260101.
	label = dropStamp(label)

	for {
		trimmed := label
		for _, s := range dropSuffix {
			trimmed = strings.TrimSuffix(trimmed, s)
		}
		if trimmed == label {
			break
		}
		label = trimmed
	}
	label = dropStamp(label)

	// 4.5 -> 4-5, so one version has one spelling. Only between digits: a dot
	// elsewhere is part of the name.
	b := []byte(label)
	for i := 1; i < len(b)-1; i++ {
		if b[i] == '.' && isDigit(b[i-1]) && isDigit(b[i+1]) {
			b[i] = '-'
		}
	}
	label = string(b)
	label = strings.ReplaceAll(label, "_", "-")
	return strings.Trim(label, "-")
}

// dropStamp removes one trailing date stamp, joined by -, _ or @. Vendors
// spell the stamp two ways — Anthropic's compact 20260101 and OpenAI's
// hyphenated 2026-04-23 — and a model whose dated build keeps its stamp is a
// second cohort of the same model, which is the split this package exists to
// prevent.
func dropStamp(label string) string {
	if out, ok := dropCompactStamp(label); ok {
		return out
	}
	if out, ok := dropDashedStamp(label); ok {
		return out
	}
	return label
}

// dropCompactStamp removes one trailing YYYYMMDD.
func dropCompactStamp(label string) (string, bool) {
	if len(label) < 9 || !isJoin(label[len(label)-9]) {
		return label, false
	}
	tail := label[len(label)-8:]
	for i := 0; i < len(tail); i++ {
		if !isDigit(tail[i]) {
			return label, false
		}
	}
	// 20xx only: a bare 8-digit run that is not a plausible date is part of
	// the name (a parameter count, a build id) and stays.
	if tail[0] != '2' || tail[1] != '0' {
		return label, false
	}
	return label[:len(label)-9], true
}

// dropDashedStamp removes one trailing YYYY-MM-DD. Narrower than the compact
// form on purpose: a version like "gpt-6-astra" must survive, so the month and
// the day have to be real ones rather than any four digits.
func dropDashedStamp(label string) (string, bool) {
	if len(label) < 11 || !isJoin(label[len(label)-11]) {
		return label, false
	}
	tail := label[len(label)-10:]
	if !isJoin(tail[4]) || !isJoin(tail[7]) {
		return label, false
	}
	for _, i := range []int{0, 1, 2, 3, 5, 6, 8, 9} {
		if !isDigit(tail[i]) {
			return label, false
		}
	}
	if tail[0] != '2' || tail[1] != '0' {
		return label, false
	}
	month := int(tail[5]-'0')*10 + int(tail[6]-'0')
	day := int(tail[8]-'0')*10 + int(tail[9]-'0')
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return label, false
	}
	return label[:len(label)-11], true
}

// isJoin reports whether c is one of the separators a stamp is joined by.
func isJoin(c byte) bool { return c == '-' || c == '_' || c == '@' }

func vendorOf(label string) string {
	best, bestLen := "", 0
	for _, f := range families {
		if strings.HasPrefix(label, f.prefix) && len(f.prefix) > bestLen {
			best, bestLen = f.vendor, len(f.prefix)
		}
	}
	return best
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Label is a vendor or family key as a heading writes it: "anthropic" reads
// "Anthropic", "openai" reads "OpenAI".
//
// The vocabulary is open. Vendors arrive faster than any list of them is
// updated, and a member runs whichever subset they run, so this never decides
// WHETHER a key can be shown — only how it looks. Anything unknown is the key
// with its first letter raised, which is right for most of them and legible for
// the rest; the map holds the few whose casing a capital alone gets wrong.
//
// It is deliberately not a table of product names. "Claude" for anthropic would
// be a second vocabulary beside the cohort key every other surface here prints,
// and it would have to be guessed again for every vendor that ever ships.
var labelCasing = map[string]string{
	"openai":     "OpenAI",
	"xai":        "xAI",
	"deepseek":   "DeepSeek",
	"openrouter": "OpenRouter",
}

func Label(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if s, ok := labelCasing[strings.ToLower(key)]; ok {
		return s
	}
	return strings.ToUpper(key[:1]) + key[1:]
}
