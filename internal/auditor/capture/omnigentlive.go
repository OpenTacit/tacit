// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Live capture: subscribe to a running Omnigent session and build the
// canonical record incrementally, invoking a callback at each turn boundary.
//
// Pure net/http — no Omnigent SDK. The stream is live-tail only (no replay),
// so we seed from a snapshot (GET /v1/sessions/{id}) and dedupe streamed items
// by id. Snapshot items are nested ({type, data}); streamed items are flat —
// ReadOmnigentSession accepts both, so a mixed accumulation builds one
// coherent record. ParseSSE is pure over its input, so the whole capture path
// is testable without a socket.

package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// DefaultStreamTimeout exceeds the server's ~15s heartbeat cadence so reads
// don't false-timeout.
const DefaultStreamTimeout = 90 * time.Second

// Event type literals we act on.
const (
	EvItemDone      = "response.output_item.done"
	EvCompleted     = "response.completed"
	EvFailed        = "response.failed"
	EvTurnCompleted = "turn.completed"
	EvSessionUsage  = "session.usage"
)

const sseDone = "[DONE]"

// session metadata keys lifted from the snapshot into the record builder
var metaKeys = []string{"id", "harness", "llm_model", "model_override", "status",
	"labels", "total_cost_usd", "last_total_tokens"}

// SSEEvent is one parsed stream event.
type SSEEvent struct {
	Type    string
	Payload map[string]any
}

// AuthHeaders builds Omnigent auth headers: a bearer token, or (default mode)
// X-Forwarded-Email.
func AuthHeaders(token, email string, acceptSSE bool) map[string]string {
	h := map[string]string{}
	if acceptSSE {
		h["Accept"] = "text/event-stream"
	}
	if token != "" {
		h["Authorization"] = "Bearer " + token
	} else if email != "" {
		h["X-Forwarded-Email"] = email
	}
	return h
}

// ParseSSE parses decoded SSE text into events, calling emit for each.
//
// Framing: an `event: <type>` line, then a `data: <json>` line, then a blank
// line. The sentinel `data: [DONE]` ends the stream. Comment lines,
// heartbeats with unparseable data, and unknown fields are ignored.
func ParseSSE(r io.Reader, emit func(SSEEvent) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	eventType := ""
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(line[5:])
			if data == sseDone {
				return nil
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				eventType = ""
				continue
			}
			et := eventType
			if et == "" {
				et, _ = payload["type"].(string)
			}
			if !emit(SSEEvent{Type: et, Payload: payload}) {
				return nil
			}
			eventType = ""
		case line == "":
			eventType = ""
		}
		// ':' comments and any other line: ignored
	}
	return sc.Err()
}

// LiveCapture accumulates a session's items (snapshot seed + live events).
// Items are keyed by id, so re-delivery is idempotent, and insertion order
// (snapshot first, then stream arrival) gives a chronological transcript.
type LiveCapture struct {
	Meta  map[string]any
	items map[string]map[string]any
	order []string
}

// NewLiveCapture seeds from an optional snapshot.
func NewLiveCapture(snapshot map[string]any) *LiveCapture {
	c := &LiveCapture{Meta: map[string]any{}, items: map[string]map[string]any{}}
	if snapshot != nil {
		c.Seed(snapshot)
	}
	return c
}

// Seed loads snapshot metadata and items.
func (c *LiveCapture) Seed(snapshot map[string]any) {
	c.Meta = map[string]any{}
	for _, k := range metaKeys {
		c.Meta[k] = snapshot[k]
	}
	items, _ := snapshot["items"].([]any)
	for _, raw := range items {
		if it, ok := raw.(map[string]any); ok {
			c.absorb(it)
		}
	}
}

func (c *LiveCapture) absorb(item map[string]any) {
	id, _ := item["id"].(string)
	if id == "" {
		return
	}
	if _, seen := c.items[id]; !seen {
		c.order = append(c.order, id)
	}
	c.items[id] = item
}

// Apply folds one event into state. Returns "turn" at a turn boundary,
// "failed" on failure, "" otherwise.
func (c *LiveCapture) Apply(ev SSEEvent) string {
	switch ev.Type {
	case EvItemDone:
		if item, ok := ev.Payload["item"].(map[string]any); ok {
			c.absorb(item)
		}
	case EvSessionUsage:
		if v, ok := ev.Payload["total_cost_usd"]; ok && v != nil {
			c.Meta["total_cost_usd"] = v
		}
	case EvCompleted, EvTurnCompleted:
		// the completed response carries its full output; fold missed items
		if resp, ok := ev.Payload["response"].(map[string]any); ok {
			if out, ok := resp["output"].([]any); ok {
				for _, raw := range out {
					if it, ok := raw.(map[string]any); ok {
						c.absorb(it)
					}
				}
			}
		}
		return "turn"
	case EvFailed:
		return "failed"
	}
	return ""
}

// Record builds the canonical record from everything accumulated so far.
func (c *LiveCapture) Record() contracts.CanonicalRecord {
	session := map[string]any{}
	for k, v := range c.Meta {
		session[k] = v
	}
	items := make([]any, 0, len(c.order))
	for _, id := range c.order {
		items = append(items, c.items[id])
	}
	session["items"] = items
	return ReadOmnigentSession(session)
}

// --- network I/O (thin; injectable so capture logic stays testable offline) ---

// OmnigentClient talks to one Omnigent server.
type OmnigentClient struct {
	BaseURL string
	Token   string
	Email   string
	HTTP    *http.Client
}

func (o *OmnigentClient) client(timeout time.Duration) *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: timeout}
}

// GetSnapshot fetches GET /v1/sessions/{id}.
func (o *OmnigentClient) GetSnapshot(sessionID string) (map[string]any, error) {
	url := strings.TrimRight(o.BaseURL, "/") + "/v1/sessions/" + sessionID
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range AuthHeaders(o.Token, o.Email, false) {
		req.Header.Set(k, v)
	}
	resp, err := o.client(30 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("snapshot HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var snapshot map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// Monitor streams a live session; onTurn runs at each turn boundary.
// snapshot/events are injectable to drive it offline in tests.
func (o *OmnigentClient) Monitor(sessionID string,
	onTurn func(contracts.CanonicalRecord, *LiveCapture),
	onFailed func(map[string]any, *LiveCapture),
	snapshot map[string]any, events io.Reader, maxTurns int) (*LiveCapture, error) {

	if snapshot == nil {
		var err error
		if snapshot, err = o.GetSnapshot(sessionID); err != nil {
			return nil, err
		}
	}
	lc := NewLiveCapture(snapshot)

	var body io.Reader = events
	if body == nil {
		url := strings.TrimRight(o.BaseURL, "/") + "/v1/sessions/" + sessionID + "/stream?idle=false"
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range AuthHeaders(o.Token, o.Email, true) {
			req.Header.Set(k, v)
		}
		resp, err := (&http.Client{}).Do(req) // no client timeout: SSE is long-lived
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body = resp.Body
	}

	turns := 0
	err := ParseSSE(body, func(ev SSEEvent) bool {
		switch lc.Apply(ev) {
		case "turn":
			onTurn(lc.Record(), lc)
			turns++
			if maxTurns > 0 && turns >= maxTurns {
				return false
			}
		case "failed":
			if onFailed != nil {
				onFailed(ev.Payload, lc)
			}
		}
		return true
	})
	return lc, err
}
