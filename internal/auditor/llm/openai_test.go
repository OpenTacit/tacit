// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// writePrompts lays down the two prompt files New() reads, so the provider-
// selection path can be exercised end to end.
func writePrompts(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"characterize-system-prompt.md", "audit-system-prompt.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("prompt"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The OpenAI completer must speak /chat/completions: Bearer auth, system folded
// into the messages array, and the answer read from choices[].message.content.
func TestOpenAICompleteSpeaksChatCompletions(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi there"}}]}`))
	}))
	defer srv.Close()

	o := &OpenAI{cfg: Config{APIKey: "sk-x", BaseURL: srv.URL, MaxTokens: 8, HTTP: srv.Client()}}
	out, err := o.complete(context.Background(), "gpt-x", "be terse", "hello")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if out != "hi there" {
		t.Fatalf("content = %q, want %q", out, "hi there")
	}
	if gotAuth != "Bearer sk-x" {
		t.Fatalf("auth header = %q, want Bearer sk-x", gotAuth)
	}
	if !strings.HasSuffix(gotPath, "/chat/completions") {
		t.Fatalf("path = %q, want .../chat/completions", gotPath)
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("want system+user messages, got %v", gotBody["messages"])
	}
	if first, _ := msgs[0].(map[string]any); first["role"] != "system" || first["content"] != "be terse" {
		t.Fatalf("system message not folded in: %v", msgs[0])
	}
}

// A Real client backed by the OpenAI provider produces the same structured
// Characterization the Anthropic path would, given an equivalent reply — the
// point of the provider seam.
func TestRealOverOpenAICharacterize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary_text\":\"did x\",\"harness\":\"codex\"}"}}]}`))
	}))
	defer srv.Close()

	cfg := Config{Provider: "openai", APIKey: "k", BaseURL: srv.URL, CharModel: "m",
		SynthModel: "m", MaxTokens: 8, HTTP: srv.Client(), PromptsDir: writePrompts(t)}
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := r.Characterize("transcript", contracts.Segment{"team": "data"})
	if err != nil {
		t.Fatalf("characterize: %v", err)
	}
	if ch.SummaryText != "did x" {
		t.Fatalf("summary = %q", ch.SummaryText)
	}
	if ch.Segment["team"] != "data" { // hint segment must survive the merge
		t.Fatalf("hint segment lost: %+v", ch.Segment)
	}
}

// Diagnose must phrase a provider-correct remedy: the OpenRouter empty-wallet
// signal is a 402 (Anthropic uses a 400), and the invalid-key remedy must name
// the single model-key var (TACIT_LLM_API_KEY).
func TestDiagnoseIsProviderAware(t *testing.T) {
	credit := &APIError{Provider: "openai", Status: 402, Message: "insufficient credits"}
	if d := Diagnose(credit); d.State != "out-of-credit" || !d.NeedsOperator {
		t.Fatalf("402 → %+v, want out-of-credit/needsOperator", d)
	}
	badKey := &APIError{Provider: "openai", Status: 401, Message: "no auth"}
	d := Diagnose(badKey)
	if d.State != "invalid-key" {
		t.Fatalf("401 state = %q", d.State)
	}
	if !strings.Contains(d.Remedy, "TACIT_LLM_API_KEY") {
		t.Fatalf("openai key remedy should name TACIT_LLM_API_KEY, got: %q", d.Remedy)
	}
	// The Anthropic path (default provider) keeps its original remedy verbatim.
	anthCredit := &APIError{Status: 400, Message: "Your credit balance is too low"}
	if d := Diagnose(anthCredit); !strings.Contains(d.Remedy, "console.anthropic.com") {
		t.Fatalf("anthropic credit remedy regressed: %q", d.Remedy)
	}
}

// The trap this refactor fixes: an OpenAI-backed client under Auto must be
// treated as a real Reasoner (dispatched to the model), not silently dropped to
// the heuristic's ErrCannotJudge. Guards the *Anthropic → Reasoner change.
func TestAutoTreatsOpenAIAsRealReasoner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"yes"}}]}`))
	}))
	defer srv.Close()

	auto := &Auto{
		Resolve: func() string { return "k" },
		Base: Config{Provider: "openai", BaseURL: srv.URL, SynthModel: "m", MaxTokens: 8,
			HTTP: srv.Client(), PromptsDir: writePrompts(t)},
	}
	ok, err := auto.VerifyAdoption("I applied the recipe", "Technique", "do the thing")
	if err != nil {
		t.Fatalf("VerifyAdoption should hit the real model, got err: %v", err)
	}
	if !ok {
		t.Fatal("model replied yes; adoption should be confirmed")
	}
}
