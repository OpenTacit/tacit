// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package suggest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The OpenRouter researcher must enable the web plugin and parse technique drafts
// out of the chat-completions reply, filling source_url from the model's JSON.
func TestOpenAIResearchUsesWebPluginAndParses(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"name\":\"Do X\",\"description\":\"d\",\"recipe\":\"r\",\"applies_when\":\"a\",\"source_url\":\"https://ex.com\"}]"}}]}`))
	}))
	defer srv.Close()

	o := &OpenAI{Key: "k", Base: srv.URL, Model: "m", Search: true, HTTP: srv.Client()}
	drafts, err := o.Research("brief")
	if err != nil {
		t.Fatalf("research: %v", err)
	}
	if len(drafts) != 1 || drafts[0].Name != "Do X" || drafts[0].SourceURL != "https://ex.com" {
		t.Fatalf("parsed drafts wrong: %+v", drafts)
	}
	plugins, _ := body["plugins"].([]any)
	if len(plugins) != 1 {
		t.Fatalf("web plugin not requested: %v", body["plugins"])
	}
	if p, _ := plugins[0].(map[string]any); p["id"] != "web" {
		t.Fatalf("plugin id = %v, want web", plugins[0])
	}
}

// A search-less base must refuse Research with ErrNoSearch rather than return a
// parametric guess — the honest-degrade contract.
func TestOpenAIResearchRefusesWithoutSearch(t *testing.T) {
	o := &OpenAI{Key: "k", Base: "https://api.example.com/v1", Model: "m", Search: false}
	if o.CanSearch() {
		t.Fatal("a non-search base must report CanSearch() == false")
	}
	if _, err := o.Research("brief"); !errors.Is(err, ErrNoSearch) {
		t.Fatalf("want ErrNoSearch, got %v", err)
	}
}

// Complete is plain chat and must work on any OpenAI-compatible base, search or
// not — this is the tagmerge / cluster-describe path.
func TestOpenAICompleteWorksWithoutSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"merged"}}]}`))
	}))
	defer srv.Close()

	o := &OpenAI{Key: "k", Base: srv.URL, Model: "m", Search: false, HTTP: srv.Client()}
	got, err := o.Complete("prompt", 64)
	if err != nil || got != "merged" {
		t.Fatalf("complete = %q, %v", got, err)
	}
	// WithModel yields an independent copy pinned to the new model.
	if o.WithModel("other").(*OpenAI).Model != "other" || o.Model != "m" {
		t.Fatal("WithModel must not mutate the original")
	}
}

// FromEnv dispatches on TACIT_LLM_PROVIDER and reads the single model key var.
func TestFromEnvProviderDispatch(t *testing.T) {
	t.Setenv("TACIT_LLM_PROVIDER", "openai")
	t.Setenv("TACIT_LLM_API_KEY", "or-key")
	t.Setenv("TACIT_LLM_BASE_URL", "https://openrouter.ai/api/v1")
	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	o, ok := c.(*OpenAI)
	if !ok {
		t.Fatalf("want *OpenAI, got %T", c)
	}
	if o.Key != "or-key" || !o.CanSearch() {
		t.Fatalf("openrouter client mis-built: %+v", o)
	}
	if !strings.Contains(o.Base, "openrouter") {
		t.Fatalf("base = %q", o.Base)
	}
}
