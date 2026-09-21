// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The wire layer: how OpenTacit talks to a model provider once the table above has
// said which format that provider speaks. Three callers share it — the
// auditor's per-turn client, the registry's suggestion researcher, and the demo
// authoring tool. Each keeps its own prompts, its own tool payloads and its own
// error wording. What they share is the request shape, the rules for what is
// worth another attempt, and how an answer is read back out.

package llmprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryWindow is how long a latency-tolerant job — suggestion research, demo
// dataset generation — keeps waiting out a rate limit before it gives up. Long
// enough to ride out a per-minute token allowance, short enough that whoever
// asked for the work is not left waiting all afternoon.
const RetryWindow = 4 * time.Minute

// defaultWait is what to wait after a retryable failure that named no
// Retry-After.
const defaultWait = 20 * time.Second

// AnthropicBody builds a Messages request: one user message, the system prompt
// when there is one, and any extra top-level fields the caller needs — a tool
// definition, a thinking setting.
func AnthropicBody(model string, maxTokens int, system, user string, extra map[string]any) ([]byte, error) {
	req := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   []map[string]any{{"role": "user", "content": user}},
	}
	if strings.TrimSpace(system) != "" {
		req["system"] = system
	}
	for k, v := range extra {
		req[k] = v
	}
	return json.Marshal(req)
}

// OpenAIBody builds a /chat/completions request. The system prompt goes in as
// the leading system-role message, which is the one structural difference from
// the Messages shape.
func OpenAIBody(model string, maxTokens int, system, user string, extra map[string]any) ([]byte, error) {
	msgs := make([]map[string]any, 0, 2)
	if strings.TrimSpace(system) != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": system})
	}
	msgs = append(msgs, map[string]any{"role": "user", "content": user})
	req := map[string]any{"model": model, "max_tokens": maxTokens, "messages": msgs}
	for k, v := range extra {
		req[k] = v
	}
	return json.Marshal(req)
}

// AnthropicRequest posts a body to the Messages endpoint with the two headers
// the API identifies a caller by. base is the API root; this appends
// /v1/messages. extraHeaders is sent as given (OpenRouter reads HTTP-Referer and
// X-Title there for attribution).
func AnthropicRequest(ctx context.Context, base, key, version string, body []byte, extraHeaders map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", version)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}

// OpenAIRequest posts a body to /chat/completions with bearer auth. base is the
// API root that already carries the version segment (OpenRouter
// https://openrouter.ai/api/v1).
func OpenAIRequest(ctx context.Context, base, key string, body []byte, extraHeaders map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}

// Send makes one attempt and reads the whole response. A transport failure —
// the request never got an answer — comes back as err with status 0. Any answer
// at all, a 500 included, comes back with a nil error: what to do about a status
// is the caller's rule, not the wire's.
func Send(c *http.Client, req *http.Request) (raw []byte, status int, header http.Header, err error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(resp.Body)
	return raw, resp.StatusCode, resp.Header, nil
}

// Retryable reports whether a status is worth another attempt: a rate limit
// (429), Anthropic's overload (529), and any other 5xx — the gateways in front
// of a provider return several of those and they all clear on their own.
func Retryable(status int) bool { return status == 429 || status >= 500 }

// RetryAfter reads the Retry-After header as a whole number of seconds. ok is
// false when the header is missing or is not a number, and the caller applies
// its own default.
func RetryAfter(h http.Header) (time.Duration, bool) {
	secs, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

// PatientWait is how long a latency-tolerant job waits before its next attempt:
// what the provider asked for plus a second of slack, or twenty seconds when it
// asked for nothing. The slack earns its keep — coming back at the exact second
// the provider named tends to earn a second rate limit.
func PatientWait(h http.Header) time.Duration {
	if d, ok := RetryAfter(h); ok {
		return d + time.Second
	}
	return defaultWait
}

// Overrun reports that the provider asked to be retried, but not soon enough:
// the wait would have run past the deadline. A caller with something useful to
// say about giving up — which model has its own rate-limit bucket, when to try
// again — reads the wait and the provider's own error back out with errors.As.
type Overrun struct {
	Wait time.Duration
	Err  error
}

// Error reports the provider's error unchanged.
func (o *Overrun) Error() string { return o.Err.Error() }

// Unwrap hands the provider's error to errors.Is and errors.As.
func (o *Overrun) Unwrap() error { return o.Err }

// Retry runs one attempt after another until one succeeds or a failure stops
// being worth retrying.
//
// once returns the attempt's value, how long to wait before trying again, and
// the error. A zero wait means the failure is terminal and Retry returns the
// error as it stands, so the attempt function sets its own bounds: by a count
// of attempts, or by which statuses it treats as transient.
//
// A zero deadline means no time bound. Otherwise, when the next wait would run
// past the deadline, Retry stops and returns an *Overrun around the last error.
func Retry[T any](ctx context.Context, deadline time.Time, once func() (T, time.Duration, error)) (T, error) {
	for {
		v, wait, err := once()
		if err == nil {
			return v, nil
		}
		var zero T
		if wait <= 0 {
			return zero, err
		}
		if !deadline.IsZero() && time.Now().Add(wait).After(deadline) {
			return zero, &Overrun{Wait: wait, Err: err}
		}
		// The wait is interruptible. A backoff that slept through cancellation
		// would hold a goroutine — and, in the hook agent, the reservation that
		// keeps the daemon from idle-exiting — for minutes after the caller
		// stopped caring.
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		}
	}
}

// AnthropicText concatenates the text blocks of a Messages response and returns
// the stop_reason with them. Only text blocks are the answer: a reasoning model
// also returns thinking blocks, which are its working, not its output.
func AnthropicText(raw []byte) (text, stopReason string, err error) {
	var out struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", err
	}
	var b strings.Builder
	for _, block := range out.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String(), out.StopReason, nil
}

// OpenAIText joins the content of every choice in a /chat/completions response.
func OpenAIText(raw []byte) (string, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, ch := range out.Choices {
		b.WriteString(ch.Message.Content)
	}
	return b.String(), nil
}
