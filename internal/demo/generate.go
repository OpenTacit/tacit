// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/llmprovider"
)

// GenerateOptions configure an LLM generation run. This authoring tool speaks
// the Anthropic Messages wire format, so the key must be Claude-capable (a
// direct Anthropic key, or an OpenRouter key with TACIT_LLM_BASE_URL pointed at
// an Anthropic-compatible endpoint).
type GenerateOptions struct {
	APIKey    string // model key; falls back to TACIT_LLM_API_KEY
	Model     string // falls back to TACIT_DEMO_MODEL, then a sane default
	BaseURL   string // falls back to TACIT_LLM_BASE_URL, then api.anthropic.com
	Seed      int64  // dataset seed (0 → dataset default)
	Days      int    // window length (0 → 30)
	MaxTokens int    // model output budget (0 → TACIT_DEMO_MAX_TOKENS, then default)
	Progress  io.Writer
}

// anthropic is the default provider's entry in the shared provider table, so
// the demo tool's default model and endpoint cannot drift from the rest of the
// product. This authoring tool speaks the Anthropic Messages wire specifically,
// so the key must be Claude-capable whatever the registry's own provider
// setting says; TACIT_DEMO_MODEL and TACIT_LLM_BASE_URL still override.
var anthropic = llmprovider.Lookup(llmprovider.Default)

// defaultDemoMaxTokens is the output budget for one dataset. A ~25-technique
// document runs well past a small cap; too low and the JSON is truncated
// mid-document and fails to parse. Kept generous because generation is a
// one-shot, latency-tolerant, cached step.
const defaultDemoMaxTokens = 32000

// Generate authors a fresh dataset for a scenario by asking an LLM to write the
// organization's content in natural language, then validating it against the
// portable schema. The event stream is NOT generated here — only the compact
// content model — so a single call produces a whole month of reproducible usage
// once synth expands it.
func Generate(sc Scenario, opts GenerateOptions) (*Dataset, error) {
	key := firstNonEmpty(opts.APIKey, os.Getenv("TACIT_LLM_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("TACIT_LLM_API_KEY is not set — generation needs a Claude-capable key. "+
			"To run without a key, load the pre-generated dataset instead (tacit demo load --scenario %s)", sc.Key)
	}
	model := firstNonEmpty(opts.Model, os.Getenv("TACIT_DEMO_MODEL"), anthropic.ResearchModel)
	base := firstNonEmpty(opts.BaseURL, os.Getenv("TACIT_LLM_BASE_URL"), anthropic.BaseURL)
	days := opts.Days
	if days == 0 {
		days = 30
	}
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = envInt("TACIT_DEMO_MAX_TOKENS", defaultDemoMaxTokens)
	}
	if opts.Progress != nil {
		fmt.Fprintf(opts.Progress, "Generating %q with %s (this authors ~25 techniques; usually 1–3 min)…\n", sc.Key, model)
	}

	user := fmt.Sprintf("SCENARIO KEY: %s\nORG BRIEF: %s\n\nWrite the dataset for a %d-day window. "+
		"Produce roughly 22–28 techniques, the large majority scope:\"org\" and specific to THIS organization's "+
		"own named internal tools and processes (invent believable product names), plus a few scope:\"general\" ones. "+
		"Include 1–2 techniques with \"draft\": true (awaiting review) and 1–2 promoted techniques that plainly "+
		"underperform (a low helped_rate, ~0.35–0.45) so the outcome views surface what ISN'T working. Stagger "+
		"introduced_day so several techniques appear partway through the month. Return ONLY the JSON document.",
		sc.Key, sc.Prompt, days)

	reply, stop, err := anthropicComplete(base, key, model, generateSystemPrompt, user, maxTokens, opts.Progress)
	if err != nil {
		return nil, err
	}
	if stop == "max_tokens" {
		return nil, fmt.Errorf("the model hit its %d-token output limit and the dataset was cut off. "+
			"Raise it with --max-tokens or TACIT_DEMO_MAX_TOKENS, or ask for fewer techniques", maxTokens)
	}
	raw, err := extractJSONDoc(reply)
	if err != nil {
		return nil, fmt.Errorf("model did not return a JSON document: %w", err)
	}
	raw = stripTrailingCommas(raw) // repair the most common LLM JSON slip
	d, err := ParseDataset(raw)
	if err != nil {
		return nil, fmt.Errorf("generated dataset failed validation: %w", err)
	}
	d.Scenario = sc.Key
	d.GeneratedBy = model
	if opts.Seed != 0 {
		d.Seed = opts.Seed
	}
	return d, nil
}

// generateSystemPrompt teaches the model the portable dataset schema and what
// makes the content good — organization-specific, evidence-shaped techniques.
var generateSystemPrompt = `You author demonstration datasets for Tacit, an organization's measured playbook for working with AI: it watches how an
organization uses AI coding agents and coaches people with techniques their colleagues found useful.

Your job: given an organization brief, invent a believable company and write a portable JSON dataset describing a
month of its AI usage. You write ONLY the content model — teams, cohorts, and playbook techniques with a few tuning
parameters. You do NOT write individual usage events; those are synthesized deterministically from your model.

Output a single JSON object with EXACTLY these fields (no others; unknown fields are rejected):

{
  "schema": 1,
  "scenario": "<the scenario key>",
  "seed": 20260601,
  "org": { "name": "...", "industry": "...", "tagline": "...", "description": "..." },
  "window": { "start_date": "YYYY-MM-DD", "days": <int> },
  "roles":     [ { "value": "engineer", "weight": 0.5 }, ... ],   // default seniority mix within a team
  "harnesses": [ { "value": "claude-code", "weight": 0.55 }, { "value": "codex", "weight": 0.18 },
                 { "value": "amp", "weight": 0.12 }, { "value": "pi", "weight": 0.09 },
                 { "value": "opencode", "weight": 0.06 } ],       // AI harness mix; use ONLY these five values
  "teams": [
    { "id": "kebab-id", "label": "Human Label", "function": "product-eng", "domain": "backend",
      "size": <people using AI, 4–14>, "maturity": <0..1 early-adopter propensity>,
      "roles": [ ... ], "harnesses": [ ... ] }   // roles/harnesses optional per-team overrides
  ],
  "usage": {   // OPTIONAL — omit to accept sensible defaults
    "base_sessions_per_active_day": 3.5, "growth_factor": 2.4, "show_rate": 0.55, "dismiss_rate": 0.16,
    "decline_rate": 0.05, "inferred_share": 0.7, "adoption_midpoint": 0.4, "adoption_steepness": 8.0, "mcp_share": 0.16
  },
  "techniques": [
    {
      "id": "kebab-id",                          // [a-z0-9][a-z0-9/_@-]*, unique
      "name": "Imperative, specific move",
      "description": "Why the naive approach falls short and what this does instead (2–3 sentences).",
      "scope": "org",                            // "org" for most; "general" for model-agnostic habits
      "tags": ["...", "internal-tool"],
      "task_types": ["debugging", "editing", ...],
      "triggers": [ { "heuristic": "one sentence: the situation the move fits" } ],  // optional
      "applies_when": "When this technique fits.",
      "not_when": "When it does not.",
      "recipe": "Copy-pasteable instructions naming the internal tool/command.",
      "before_after": "Before: ... After: ...",  // optional but nice
      "shipped": "YYYY-MM",                       // when the internal tool/practice shipped
      "introduced_day": <0..days-1>,              // when it starts being suggested in the window
      "popularity": <0..1>,                       // relative show frequency
      "strong_teams": ["team-id", ...],           // where adoption + helped run high (proponents)
      "weak_teams": ["team-id", ...],             // shown but lagging (opportunities to spread)
      "roles": ["senior-engineer", ...],          // optional role affinity
      "adoption_rate": <0..1>,                    // base adopted/shown among an affine cohort
      "helped_rate": <0..1>,                      // base helped/adopted among an affine cohort
      "draft": false,                             // true → stays in the review lane, unpromoted
      "decays": false                             // true → helped rate collapses in the final fortnight
    }
  ]
}

RULES
- segment dimensions are fixed: team, role, function, domain, harness, surface. Put specialty in team/function/domain;
  put seniority in role. Common function values: product-eng, infrastructure, data-eng, security, devex, ml. Common
  domain values: backend, frontend, mobile, data, infra, ml. Keep each team's function/domain coherent.
- The MAJORITY of techniques must be scope:"org" and reference the organization's OWN named internal tools,
  services, CLIs, and processes — not generic "how to prompt better" advice. Invent believable product names and use
  them consistently across techniques (e.g. a build CLI, a flag service, a service catalog, an incident stack).
- Make strong_teams/weak_teams choices that tell a story: a technique proven on one team that another team hasn't
  picked up yet is exactly the "opportunity" the product surfaces.
- Vary introduced_day so the month shows new techniques appearing, not everything on day 0.
- helped_rate and adoption_rate are fractions in 0..1. popularity and maturity are 0..1.
- Emit STRICT JSON: double-quoted keys and strings, NO trailing commas before } or ], no comments.
- Return ONLY the JSON object. No markdown, no code fence, no commentary.`

// modelReply is one answer from the model: the concatenated text and the
// stop_reason ("end_turn", "max_tokens", …) that tells a complete document from
// a truncated one.
type modelReply struct{ text, stop string }

// anthropicComplete calls the Anthropic Messages API, waiting out a rate limit
// for as long as the provider asks and the retry window allows. Generation is a
// one-shot, latency-tolerant step, so waiting beats failing a run that has
// already spent a minute or two.
func anthropicComplete(baseURL, key, model, system, user string, maxTokens int, progress io.Writer) (string, string, error) {
	body, err := llmprovider.AnthropicBody(model, maxTokens, system, user, nil)
	if err != nil {
		return "", "", err
	}
	client := &http.Client{Timeout: 300 * time.Second}
	attempts := 0
	got, err := llmprovider.Retry(context.Background(), time.Now().Add(llmprovider.RetryWindow), func() (modelReply, time.Duration, error) {
		if attempts++; attempts > 1 && progress != nil {
			fmt.Fprintf(progress, "  retrying (%d)…\n", attempts-1)
		}
		req, err := llmprovider.AnthropicRequest(context.Background(), baseURL, key, "2023-06-01", body, nil)
		if err != nil {
			return modelReply{}, 0, err
		}
		raw, status, header, err := llmprovider.Send(client, req)
		if err != nil { // the request never got an answer; try again
			return modelReply{}, llmprovider.PatientWait(nil), err
		}
		if status != http.StatusOK {
			err := fmt.Errorf("anthropic HTTP %d: %s", status, strings.TrimSpace(string(raw)))
			if !llmprovider.Retryable(status) {
				return modelReply{}, 0, err // client errors won't improve on retry
			}
			return modelReply{}, llmprovider.PatientWait(header), err
		}
		text, stop, err := llmprovider.AnthropicText(raw)
		return modelReply{text, stop}, 0, err
	})
	return got.text, got.stop, err
}

// envInt reads a positive integer environment variable, or returns def.
func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// stripTrailingCommas removes any comma that immediately precedes a } or ] —
// invalid in strict JSON but a common LLM slip that Go's decoder rejects. It is
// string-aware (tracking escapes) so a comma inside a string value is never
// touched.
func stripTrailingCommas(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	inStr, esc := false, false
	for i := range len(raw) {
		c := raw[i]
		if inStr {
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(raw) && (raw[j] == ' ' || raw[j] == '\t' || raw[j] == '\n' || raw[j] == '\r') {
				j++
			}
			if j < len(raw) && (raw[j] == '}' || raw[j] == ']') {
				continue // drop the trailing comma
			}
		}
		out = append(out, c)
	}
	return out
}

// extractJSONDoc pulls the outermost JSON object from a model reply, tolerating
// an accidental code fence or stray prose around it.
func extractJSONDoc(text string) ([]byte, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON object found")
	}
	return []byte(text[start : end+1]), nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
