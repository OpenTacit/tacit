// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The real researcher: the Anthropic Messages API with the server-side
// web_search tool, so the "carefully-crafted searches of the web" happen in
// one round trip — the model plans the searches from the usage profile, reads
// results, and returns the JSON technique proposals. No key -> ErrNoResearcher,
// and the caller says so honestly (the /tacit:suggest skill then falls back
// to researching with the harness's own web access).
package suggest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/llmprovider"
)

// ErrNoResearcher is returned when no model key is available to the registry
// process.
var ErrNoResearcher = errors.New(
	"suggestion research needs TACIT_LLM_API_KEY on the registry service; " +
		"alternatively run /tacit:suggest in an agent session, which researches with the harness's own web access")

// ErrNoSearch is returned when a model provider is configured but has no web
// search (a plain OpenAI-compatible endpoint that is not OpenRouter). The
// registry-side research pass needs live web access; the caller falls back to
// the harness's own.
var ErrNoSearch = errors.New(
	"the configured model provider has no server-side web search; " +
		"run /tacit:suggest in an agent session (researches with the harness's own web access), " +
		"or set TACIT_LLM_PROVIDER=anthropic, or point TACIT_LLM_BASE_URL at OpenRouter")

// Client is a configured research + completion backend. The web-searching
// Research path is provider-specific (Anthropic's server tool vs. OpenRouter's
// web plugin); Complete is plain chat and works on any provider. Callers that
// only need Complete (tagmerge, cluster-describe) accept the narrower interfaces
// this satisfies.
type Client interface {
	// Research runs the brief with web search and returns technique proposals, or
	// ErrNoSearch when the provider has no search.
	Research(prompt string) ([]Draft, error)
	// Complete runs a plain, tool-less prompt and returns the model's text.
	Complete(prompt string, maxTokens int) (string, error)
	// CanSearch reports whether Research can reach the live web on this provider.
	CanSearch() bool
	// WithModel returns a shallow copy configured to use a specific model id,
	// for callers (tagmerge) that override the default for one job.
	WithModel(model string) Client
	// ListModels returns the provider's currently-available models (id, and a
	// name/description where the provider supplies them), for the settings combo
	// box. Best-effort — an error never breaks any flow.
	ListModels() ([]ModelInfo, error)
}

// Anthropic is the web-searching researcher (Messages API + server-side web_search).
type Anthropic struct {
	Key     string
	Base    string // default https://api.anthropic.com
	Model   string // default claude-sonnet-5
	Version string // anthropic-version header
	HTTP    *http.Client
}

// CanSearch reports true: the Anthropic researcher always has server-side
// web_search.
func (a *Anthropic) CanSearch() bool { return true }

// WithModel returns a copy pinned to model.
func (a *Anthropic) WithModel(model string) Client {
	c := *a
	c.Model = model
	return &c
}

// FromEnv builds the researcher from the environment, selecting the provider
// via TACIT_LLM_PROVIDER ("anthropic" default, or "openai" for OpenRouter and
// any OpenAI-compatible base). Returns ErrNoResearcher when no key is set.
func FromEnv() (Client, error) {
	return ClientForProvider(strings.TrimSpace(os.Getenv("TACIT_LLM_PROVIDER")))
}

func (a *Anthropic) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	// research runs several web searches server-side; give it room
	return &http.Client{Timeout: 5 * time.Minute}
}

// Research runs the prompt with web search enabled and parses the JSON array
// of proposals out of the final text blocks.
//
// Rate limits (429) are retried honoring the Retry-After header: a research
// pass accumulates several turns of search context through the model, so a
// small per-minute token allowance is an expected transient, not a failure.
// web_search_20260209 filters search results server-side before they enter
// the context window, which keeps input-token burn down on tight limits.
func (a *Anthropic) Research(prompt string) ([]Draft, error) {
	body, err := llmprovider.AnthropicBody(a.Model, 8192, "", prompt, map[string]any{
		"tools": []map[string]any{{
			"type": a.searchTool(), "name": "web_search",
			"max_uses": 5,
		}},
	})
	if err != nil {
		return nil, err
	}
	drafts, err := llmprovider.Retry(context.Background(), time.Now().Add(llmprovider.RetryWindow),
		func() ([]Draft, time.Duration, error) { return a.once(body) })
	var over *llmprovider.Overrun
	if errors.As(err, &over) {
		return nil, fmt.Errorf("%w. The API requested a retry in %s, which exceeds this pass's time limit. "+
			"Try again after the rate limit resets, or set TACIT_SUGGEST_MODEL to a model with a separate rate-limit bucket, such as claude-haiku-4-5", over.Err, over.Wait.Round(time.Second))
	}
	return drafts, err
}

// searchTool picks the web-search tool variant the model supports: the
// dynamic-filtering web_search_20260209 (filters results before they hit the
// context window) needs Opus 4.6+/Sonnet 4.6/Sonnet 5-class models; Haiku
// and older models take the basic variant.
func (a *Anthropic) searchTool() string {
	for _, older := range []string{"haiku", "sonnet-4-5", "opus-4-5", "opus-4-1", "opus-4-0", "claude-3"} {
		if strings.Contains(a.Model, older) {
			return "web_search_20250305"
		}
	}
	return "web_search_20260209"
}

// once makes one Messages API attempt. wait > 0 marks a retryable failure
// (rate limit, overload, server error) and how long to wait.
func (a *Anthropic) once(body []byte) (drafts []Draft, wait time.Duration, err error) {
	raw, wait, err := a.attempt(a.client(), body)
	if err != nil {
		return nil, wait, err
	}
	text, _, err := llmprovider.AnthropicText(raw)
	if err != nil {
		return nil, 0, fmt.Errorf("anthropic response: %w", err)
	}
	drafts, err = parseDrafts(text)
	return drafts, 0, err
}

// attempt sends one Messages request and classifies what came back.
func (a *Anthropic) attempt(c *http.Client, body []byte) (raw []byte, wait time.Duration, err error) {
	req, err := llmprovider.AnthropicRequest(context.Background(), a.Base, a.Key, a.Version, body, nil)
	if err != nil {
		return nil, 0, err
	}
	return call(c, req, "anthropic")
}

// call makes one attempt against a provider and says what to do about the
// answer: the raw body when it succeeded, and on a failure how long to wait
// before trying again — zero when another attempt cannot help. label names the
// provider in the error, which members read.
func call(c *http.Client, req *http.Request, label string) (raw []byte, wait time.Duration, err error) {
	raw, status, header, err := llmprovider.Send(c, req)
	if err != nil {
		return nil, 0, err
	}
	if status < 400 {
		return raw, 0, nil
	}
	err = fmt.Errorf("%s HTTP %d: %s", label, status, truncate(raw, 300))
	if llmprovider.Retryable(status) {
		return nil, llmprovider.PatientWait(header), err
	}
	return nil, 0, err
}

// citeRe matches the inline citation markup the web_search tool trains the
// model to emit (<cite index="…">…</cite>). The indices reference search
// results that don't exist outside the API response; provenance lives in
// source_url, so the markers are scrubbed from every field.
var citeRe = regexp.MustCompile(`</?cite[^>]*>`)

// parseDrafts extracts the JSON array from the model's text — tolerant of a
// stray fence or preamble, strict about the payload — and scrubs citation
// markup out of the field values.
func parseDrafts(text string) ([]Draft, error) {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array in research response (%s)", truncate([]byte(text), 200))
	}
	var drafts []Draft
	if err := json.Unmarshal([]byte(text[start:end+1]), &drafts); err != nil {
		return nil, fmt.Errorf("research response JSON: %w", err)
	}
	for i := range drafts {
		d := &drafts[i]
		for _, f := range []*string{&d.Name, &d.Description, &d.Recipe, &d.AppliesWhen, &d.NotWhen} {
			*f = strings.TrimSpace(citeRe.ReplaceAllString(*f, ""))
		}
	}
	return drafts, nil
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Complete runs a plain, tool-less prompt and returns the model's text. It
// exists so other jobs that need this registry's configured model — the tag
// merge proposer (internal/registry/tagmerge) — reuse this client's key, model
// choice and rate-limit handling rather than growing a second one.
//
// No web_search tool: a tag-merge judgment is made from the vocabulary in the
// prompt, not from the internet.
func (a *Anthropic) Complete(prompt string, maxTokens int) (string, error) {
	body, err := llmprovider.AnthropicBody(a.Model, maxTokens, "", prompt, map[string]any{
		// Thinking OFF, deliberately. A reasoning model spends its thinking from
		// the SAME budget as its answer, and on a judgment over sixty-odd tags it
		// will spend the entire budget deliberating and return no answer at all —
		// which surfaces as an empty response, not as the budget problem it is.
		// It is also slow: three minutes of thinking behind a button a human is
		// waiting on, versus six seconds without. The caller wants a short
		// structured answer, and every answer is reviewed by a human anyway.
		"thinking": map[string]any{"type": "disabled"},
	})
	if err != nil {
		return "", err
	}
	return llmprovider.Retry(context.Background(), time.Now().Add(llmprovider.RetryWindow),
		func() (string, time.Duration, error) { return a.completeOnce(body, maxTokens) })
}

func (a *Anthropic) completeOnce(body []byte, maxTokens int) (string, time.Duration, error) {
	// Seconds is the norm (thinking is disabled above); this is headroom for a
	// slow model or a slow network, not for deliberation.
	c := &http.Client{Timeout: 2 * time.Minute}
	if a.HTTP != nil {
		c = a.HTTP
	}
	raw, wait, err := a.attempt(c, body)
	if err != nil {
		return "", wait, err
	}
	text, stop, err := llmprovider.AnthropicText(raw)
	if err != nil {
		return "", 0, fmt.Errorf("anthropic response: %w", err)
	}
	// A reasoning model's thinking counts against max_tokens, so too small a
	// budget can be spent entirely on thinking and return no answer at all. That
	// is a budget problem, and it must say so — reported as "no JSON in the
	// response" it looks like the model misbehaved, and the fix is invisible.
	if stop == "max_tokens" && strings.TrimSpace(text) == "" {
		return "", 0, fmt.Errorf("the model (%s) used its entire %d-token budget on reasoning and returned no answer; "+
			"raise the budget, or set TACIT_TAGMERGE_MODEL to a model that reasons less", a.Model, maxTokens)
	}
	return text, 0, nil
}
