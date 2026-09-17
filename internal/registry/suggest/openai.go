// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// OpenAI-compatible researcher: any endpoint speaking /chat/completions
// (OpenRouter, OpenAI, Together, Groq, a local vLLM/Ollama). Research uses
// OpenRouter's `web` plugin for live search — the model plans the searches,
// reads results, and returns the JSON technique proposals — mirroring the Anthropic
// path's one-round-trip shape. Complete is plain tool-less chat (tagmerge,
// cluster-describe). A base with no web search reports CanSearch() == false and
// refuses Research with ErrNoSearch, so the caller falls back to researching
// with the harness's own web access rather than dressing a search-less guess as
// research.
package suggest

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/opentacit/tacit/internal/llmprovider"
)

// OpenAI is the OpenAI-compatible researcher. Base is the API root that already
// carries the version segment (OpenRouter https://openrouter.ai/api/v1); this
// appends /chat/completions.
type OpenAI struct {
	Key    string
	Base   string
	Model  string
	Search bool // whether this base offers live web search (OpenRouter, or forced)
	HTTP   *http.Client
}

// CanSearch reports whether Research can reach the live web on this base.
func (o *OpenAI) CanSearch() bool { return o.Search }

// WithModel returns a copy pinned to model.
func (o *OpenAI) WithModel(model string) Client {
	c := *o
	c.Model = model
	return &c
}

func (o *OpenAI) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Research runs the brief with web search and parses the JSON array of
// proposals out of the reply. On a base with no search it refuses honestly
// (ErrNoSearch) — a technique researched with no web access is a parametric guess,
// and a plausible fabricated "org practice" is the worst possible output.
func (o *OpenAI) Research(prompt string) ([]Draft, error) {
	if !o.Search {
		return nil, ErrNoSearch
	}
	body, err := llmprovider.OpenAIBody(o.Model, 8192, "", prompt, map[string]any{
		// OpenRouter's web plugin: the model searches, reads results, and the
		// citations return as message annotations. The output contract already
		// asks the model to put the chosen source in source_url, so parseDrafts
		// reads provenance from the JSON just as on the Anthropic path.
		"plugins": []map[string]any{{"id": "web", "max_results": 5}},
	})
	if err != nil {
		return nil, err
	}
	text, err := llmprovider.Retry(context.Background(), time.Now().Add(llmprovider.RetryWindow),
		func() (string, time.Duration, error) { return o.chat(body) })
	if err != nil {
		return nil, err
	}
	return parseDrafts(text)
}

// Complete runs a plain, tool-less prompt and returns the model's text. Reused
// by the tag-merge proposer and cluster-describe, which need this registry's
// configured model but make a judgment from the prompt, not the internet.
func (o *OpenAI) Complete(prompt string, maxTokens int) (string, error) {
	body, err := llmprovider.OpenAIBody(o.Model, maxTokens, "", prompt, nil)
	if err != nil {
		return "", err
	}
	return llmprovider.Retry(context.Background(), time.Now().Add(llmprovider.RetryWindow),
		func() (string, time.Duration, error) { return o.chat(body) })
}

// chat makes one /chat/completions attempt, joining the text of every choice.
// wait > 0 marks a retryable failure (rate limit, overload, server error) and
// how long to wait.
func (o *OpenAI) chat(body []byte) (text string, wait time.Duration, err error) {
	req, err := llmprovider.OpenAIRequest(context.Background(), o.Base, o.Key, body, nil)
	if err != nil {
		return "", 0, err
	}
	raw, wait, err := call(o.client(), req, "openai")
	if err != nil {
		return "", wait, err
	}
	text, err = llmprovider.OpenAIText(raw)
	if err != nil {
		return "", 0, fmt.Errorf("openai response: %w", err)
	}
	return text, 0, nil
}
