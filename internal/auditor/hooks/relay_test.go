// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEnsureUpAlreadyHealthyDoesNotSpawn(t *testing.T) {
	spawned := 0
	ok := EnsureUp("b", func(string) bool { return true },
		func() error { spawned++; return nil }, func(time.Duration) {}, 10)
	if !ok || spawned != 0 {
		t.Fatalf("ok=%v spawned=%d", ok, spawned)
	}
}

func TestEnsureUpSpawnsOnceThenConnects(t *testing.T) {
	spawned, checks := 0, 0
	ok := EnsureUp("b", func(string) bool { checks++; return checks >= 3 },
		func() error { spawned++; return nil }, func(time.Duration) {}, 10)
	if !ok || spawned != 1 {
		t.Fatalf("ok=%v spawned=%d", ok, spawned)
	}
}

func TestEnsureUpGivesUp(t *testing.T) {
	ok := EnsureUp("b", func(string) bool { return false },
		func() error { return nil }, func(time.Duration) {}, 3)
	if ok {
		t.Fatal("gave up should be false")
	}
}

func TestRelayNeverBreaksTheTurn(t *testing.T) {
	cfg := RelayConfig{BaseURL: "http://x"}
	// agent unavailable -> {}
	out := Relay(cfg, "claude-code", []byte(`{"hook_event_name":"Stop"}`),
		func(RelayConfig, string, []byte) ([]byte, error) {
			t.Fatal("must not POST when the agent is unavailable")
			return nil, nil
		},
		func(string) bool { return false })
	if string(out) != "{}" {
		t.Fatalf("unavailable: %s", out)
	}
	// transient POST error -> {}
	out = Relay(cfg, "claude-code", []byte(`{}`),
		func(RelayConfig, string, []byte) ([]byte, error) { return nil, errors.New("refused") },
		func(string) bool { return true })
	if string(out) != "{}" {
		t.Fatalf("transient: %s", out)
	}
	// healthy -> forwards the agent's response and targets the harness route
	var gotURL string
	out = Relay(cfg, "codex", []byte(`{}`),
		func(_ RelayConfig, url string, _ []byte) ([]byte, error) {
			gotURL = url
			return []byte(`{"ok":1}`), nil
		},
		func(string) bool { return true })
	if string(out) != `{"ok":1}` || gotURL != "http://x/v1/hooks/codex" {
		t.Fatalf("forward: %s -> %s", gotURL, out)
	}
}

func TestRelayHeadersCarryTheSharedKey(t *testing.T) {
	h := relayHeaders("k1", "", "")
	if h["X-Tacit-Key"] != "k1" {
		t.Fatalf("key header missing: %v", h)
	}
	if _, present := relayHeaders("", "", "")["X-Tacit-Key"]; present {
		t.Fatal("empty key must not send a header")
	}
}

func TestRelayHeadersCarryTheConsumer(t *testing.T) {
	if h := relayHeaders("", "agent", ""); h["X-Tacit-Consumer"] != "agent" {
		t.Fatalf("consumer header missing: %v", h)
	}
	if _, present := relayHeaders("", "", "")["X-Tacit-Consumer"]; present {
		t.Fatal("empty consumer must not send a header")
	}
}

func TestRelayHeadersCarryTheClient(t *testing.T) {
	if h := relayHeaders("", "", "bridge"); h["X-Tacit-Client"] != "bridge" {
		t.Fatalf("client header missing: %v", h)
	}
	if _, present := relayHeaders("", "", "")["X-Tacit-Client"]; present {
		t.Fatal("empty client must not send a header")
	}
}

// DetectClient reads the env var Claude Code injects into the subprocesses of a
// session a web/mobile client has attached to. Absent => a local terminal.
func TestDetectClient(t *testing.T) {
	bridged := func(k string) string {
		if k == "CLAUDE_CODE_BRIDGE_SESSION_ID" {
			return "session_01ABC"
		}
		return ""
	}
	if got := DetectClient(bridged); got != "bridge" {
		t.Fatalf("bridged session should read as bridge, got %q", got)
	}
	if got := DetectClient(func(string) string { return "" }); got != "terminal" {
		t.Fatalf("plain session should read as terminal, got %q", got)
	}
}

func TestInjectEventName(t *testing.T) {
	// Copilot CLI payloads carry no event name; the relay stamps the one its
	// command line names. Payloads that already name an event, empty events,
	// and unparseable payloads all pass through untouched.
	out := InjectEventName([]byte(`{"sessionId":"s1"}`), "agentStop")
	if string(out) != `{"hook_event_name":"agentStop","sessionId":"s1"}` &&
		!(strings.Contains(string(out), `"hook_event_name":"agentStop"`) && strings.Contains(string(out), `"sessionId":"s1"`)) {
		t.Fatalf("not injected: %s", out)
	}
	in := []byte(`{"hook_event_name":"Stop"}`)
	if out := InjectEventName(in, "agentStop"); string(out) != string(in) {
		t.Fatalf("existing event overwritten: %s", out)
	}
	if out := InjectEventName(in, ""); string(out) != string(in) {
		t.Fatalf("empty event mutated payload: %s", out)
	}
	if out := InjectEventName([]byte("not json"), "agentStop"); string(out) != "not json" {
		t.Fatalf("unparseable payload mutated: %s", out)
	}
}

func TestDetectConsumer(t *testing.T) {
	env := func(vals map[string]string) func(string) string {
		return func(k string) string { return vals[k] }
	}
	for _, tc := range []struct {
		vals map[string]string
		want string
	}{
		{map[string]string{}, "human"},
		{map[string]string{"CI": "true"}, "agent"},
		{map[string]string{"CI": "1"}, "agent"},
		{map[string]string{"CI": "false"}, "human"},
		{map[string]string{"CI": "0"}, "human"},
		// The explicit override wins in both directions: a fleet operator
		// marks scheduled runs agent; a person debugging inside CI marks
		// their session human.
		{map[string]string{"TACIT_CONSUMER": "agent"}, "agent"},
		{map[string]string{"TACIT_CONSUMER": "human", "CI": "true"}, "human"},
		{map[string]string{"TACIT_CONSUMER": "gibberish", "CI": "true"}, "agent"},
	} {
		if got := DetectConsumer(env(tc.vals)); got != tc.want {
			t.Errorf("DetectConsumer(%v) = %q, want %q", tc.vals, got, tc.want)
		}
	}
}
