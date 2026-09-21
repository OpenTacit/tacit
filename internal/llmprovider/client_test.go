// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package llmprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// attempt is a stand-in for what every caller writes around the shared rules:
// one request, the raw body on success, and on a failure the wait those rules
// ask for — zero when waiting cannot help.
func attempt(url string, calls *int) func() ([]byte, time.Duration, error) {
	return func() ([]byte, time.Duration, error) {
		*calls++
		req, err := AnthropicRequest(context.Background(), url, "k", "2023-06-01", []byte(`{}`), nil)
		if err != nil {
			return nil, 0, err
		}
		raw, status, header, err := Send(http.DefaultClient, req)
		if err != nil {
			return nil, 0, err
		}
		if status == http.StatusOK {
			return raw, 0, nil
		}
		err = fmt.Errorf("anthropic HTTP %d", status)
		if !Retryable(status) {
			return nil, 0, err
		}
		return nil, PatientWait(header), err
	}
}

func TestRetryWaitsOutARateLimitThenSucceeds(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls == 1 {
			w.Header().Set("Retry-After", "0") // come straight back, in the test
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"}]}`))
	}))
	defer srv.Close()

	raw, err := Retry(context.Background(), time.Now().Add(RetryWindow), attempt(srv.URL, &calls))
	if err != nil {
		t.Fatalf("a 429 with Retry-After must be waited out: %v", err)
	}
	text, _, _ := AnthropicText(raw)
	if text != "hello" || calls != 2 {
		t.Fatalf("text = %q after %d calls", text, calls)
	}
}

func TestRetryRidesOutAnOverload(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(529) // Anthropic's overload
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	if _, err := Retry(context.Background(), time.Now().Add(RetryWindow), attempt(srv.URL, &calls)); err != nil {
		t.Fatalf("529 must be retried: %v", err)
	}
	if calls != 3 {
		t.Fatalf("want two overloads then an answer, got %d calls", calls)
	}
}

func TestRetryStopsOnATerminalStatus(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
	}))
	defer srv.Close()

	_, err := Retry(context.Background(), time.Now().Add(RetryWindow), attempt(srv.URL, &calls))
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("a 400 must come back as it is: %v", err)
	}
	if calls != 1 {
		t.Fatalf("a bad request must not be retried; got %d calls", calls)
	}
	var over *Overrun
	if errors.As(err, &over) {
		t.Fatal("a terminal failure is not an overrun")
	}
}

func TestRetryGivesUpWhenTheWaitPassesTheDeadline(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer srv.Close()

	start := time.Now()
	_, err := Retry(context.Background(), time.Now().Add(time.Second), attempt(srv.URL, &calls))
	var over *Overrun
	if !errors.As(err, &over) {
		t.Fatalf("want an *Overrun the caller can explain, got %v", err)
	}
	if over.Wait != 61*time.Second {
		t.Fatalf("wait = %s, want the 60s the provider asked for plus slack", over.Wait)
	}
	if !strings.Contains(err.Error(), "429") { // the provider's own error survives
		t.Fatalf("overrun lost the provider error: %v", err)
	}
	if calls != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("giving up must be immediate: %d calls in %s", calls, time.Since(start))
	}
}

// The wire format is frozen: a provider must not be able to tell that three
// callers now share one client.
func TestRequestsCarryTheProviderHeaders(t *testing.T) {
	body, err := AnthropicBody("claude-x", 8, "be terse", "hello", map[string]any{"thinking": map[string]any{"type": "disabled"}})
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["model"] != "claude-x" || sent["system"] != "be terse" || sent["max_tokens"] != float64(8) {
		t.Fatalf("messages body = %s", body)
	}
	if th, _ := sent["thinking"].(map[string]any); th["type"] != "disabled" {
		t.Fatalf("extra field dropped: %s", body)
	}

	req, err := AnthropicRequest(context.Background(), "https://api.anthropic.com/", "sk-a", "2023-06-01", body, map[string]string{"X-Title": "Tacit"})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("messages URL = %s", req.URL)
	}
	if req.Header.Get("x-api-key") != "sk-a" || req.Header.Get("anthropic-version") != "2023-06-01" ||
		req.Header.Get("Content-Type") != "application/json" || req.Header.Get("X-Title") != "Tacit" {
		t.Fatalf("messages headers = %v", req.Header)
	}

	// The OpenAI shape folds the system prompt into the messages array.
	body, err = OpenAIBody("gpt-x", 8, "be terse", "hello", map[string]any{"plugins": []map[string]any{{"id": "web"}}})
	if err != nil {
		t.Fatal(err)
	}
	sent = nil
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	msgs, _ := sent["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("want system+user messages: %s", body)
	}
	if first, _ := msgs[0].(map[string]any); first["role"] != "system" || first["content"] != "be terse" {
		t.Fatalf("system message not folded in: %s", body)
	}
	if sent["system"] != nil {
		t.Fatalf("chat/completions has no top-level system field: %s", body)
	}
	req, err = OpenAIRequest(context.Background(), "https://openrouter.ai/api/v1", "sk-o", body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://openrouter.ai/api/v1/chat/completions" {
		t.Fatalf("chat URL = %s", req.URL)
	}
	if req.Header.Get("Authorization") != "Bearer sk-o" {
		t.Fatalf("chat auth = %q", req.Header.Get("Authorization"))
	}

	// No system prompt: the Messages body omits the field, and the chat body
	// sends the user message alone.
	body, _ = AnthropicBody("claude-x", 8, "", "hello", nil)
	if strings.Contains(string(body), "system") {
		t.Fatalf("empty system prompt must not be sent: %s", body)
	}
	body, _ = OpenAIBody("gpt-x", 8, "", "hello", nil)
	sent = nil
	_ = json.Unmarshal(body, &sent)
	if msgs, _ := sent["messages"].([]any); len(msgs) != 1 {
		t.Fatalf("want the user message alone: %s", body)
	}
}

func TestTextExtractionReadsBothWireFormats(t *testing.T) {
	// Thinking blocks are the model's working, not its answer.
	text, stop, err := AnthropicText([]byte(`{"stop_reason":"max_tokens","content":[
		{"type":"thinking","thinking":"hmm"},{"type":"text","text":"one "},{"type":"text","text":"two"}]}`))
	if err != nil || text != "one two" || stop != "max_tokens" {
		t.Fatalf("messages reply = %q / %q / %v", text, stop, err)
	}
	if _, _, err := AnthropicText([]byte("not json")); err == nil {
		t.Fatal("garbage accepted")
	}
	got, err := OpenAIText([]byte(`{"choices":[{"message":{"content":"one "}},{"message":{"content":"two"}}]}`))
	if err != nil || got != "one two" {
		t.Fatalf("chat reply = %q / %v", got, err)
	}
}

// A cancelled context must interrupt the wait between attempts. A backoff that
// slept through cancellation would hold a goroutine — and, in the hook agent,
// the reservation that keeps the daemon from idle-exiting — long after the
// caller stopped caring.
func TestRetryStopsWaitingWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	start := time.Now()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Retry(ctx, time.Time{}, func() (string, time.Duration, error) {
		attempts++
		return "", time.Hour, errors.New("still failing")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Retry waited %v; it should have returned when the context was cancelled", elapsed)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

// An already-cancelled context still lets the first attempt run — the caller
// asked for the work — but stops before the first wait.
func TestRetryMakesOneAttemptUnderACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	if _, err := Retry(ctx, time.Time{}, func() (string, time.Duration, error) {
		attempts++
		return "", time.Second, errors.New("failing")
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}
