// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The command-type hook relay — makes the hook agent on-demand instead of
// always-on. Wired as the harness's hook command (`tacit hook-relay
// <harness>`), it runs once per hook event: read the event JSON on stdin,
// forward it to the local agent, write the agent's JSON response to stdout.
// If the agent isn't running, the FIRST hook of a session auto-starts it
// (detached) and waits for it to come up; the agent idle-exits on its own.
//
// Robustness rule: a relay must NEVER break the member's turn. Any failure
// (agent won't start, transient POST error) returns an empty {} — the harness
// proceeds as if no hook ran.

package hooks

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// RelayConfig carries what the relay needs (kept tiny — it runs per hook).
type RelayConfig struct {
	BaseURL  string // http://127.0.0.1:8787
	APIKey   string // sent as X-Tacit-Key when non-empty (a keyless agent ignores it)
	SpawnLog string // where a relay-spawned agent writes its output
	Consumer string // "human"|"agent" — who this session serves (DetectConsumer)
	Client   string // "terminal"|"bridge" — where the member reads it (DetectClient)
}

// DetectClient classifies WHERE the member is reading this session from.
// Claude Code injects CLAUDE_CODE_BRIDGE_SESSION_ID into the subprocesses of a
// session a web or mobile client has attached to over Remote Control; a purely
// local terminal session has none. Like the consumer classification this must
// travel per event, because the agent daemon outlives any one session.
//
// It matters because the delivery channels differ: a bridged client renders no
// hook output at all, so the ◆ block reaches only a terminal viewer
// (docs/harness/harness-claude-code.md, "Which channels reach a web or mobile
// client").
//
// The signal is confirmed to arrive: Claude Code injects the variable into hook
// subprocesses as well as tool subprocesses (verified 2026-07-25 against a live
// bridged session — the PostToolUse relay reported "bridge" unprompted). Note the
// `claude` process itself carries no CLAUDE_* vars; they are injected per child,
// so reading the parent's environment finds nothing.
//
// OBSERVATION ONLY — nothing routes on this yet, deliberately. What the signal
// cannot tell you is the part that matters for suppressing a delivery: "a bridge
// is attached" is not "the member is reading from the bridge". Both viewers can
// be open at once, as they were when this was measured — the ◆ block rendered in
// the terminal while an iPad was attached. Routing the block away from a terminal
// on this signal alone would hide it from someone watching the terminal. Adding
// a relay for bridged sessions is the safe direction; suppressing the block is
// not, and needs a product decision rather than a better signal.
func DetectClient(getenv func(string) string) string {
	if getenv("CLAUDE_CODE_BRIDGE_SESSION_ID") != "" {
		return "bridge"
	}
	return "terminal"
}

// DetectConsumer classifies the session this relay serves: "agent" for an
// autonomous run (nobody watching at delivery time), "human" otherwise. The
// relay is the right place to look — unlike the long-lived hook agent it runs
// inside the harness's own environment, per event. Deliberately conservative
// (docs/delivery/agent-delivery-plan.md Phase A): autonomy must be proven,
// not assumed, so only an explicit TACIT_CONSUMER or a CI marker classifies
// as agent, and anything unrecognized stays human.
func DetectConsumer(getenv func(string) string) string {
	switch getenv("TACIT_CONSUMER") {
	case "agent":
		return "agent"
	case "human":
		return "human"
	}
	if ci := getenv("CI"); ci != "" && ci != "0" && ci != "false" {
		return "agent"
	}
	return "human"
}

func relayHeaders(apiKey, consumer, client string) map[string]string {
	h := map[string]string{"Content-Type": "application/json"}
	if apiKey != "" {
		// The shared key rides along whenever configured (same env var the
		// agent reads), so a keyed agent — org-managed, or one run by hand —
		// accepts the relay's POSTs instead of silently 401ing every hook.
		h["X-Tacit-Key"] = apiKey
	}
	if consumer != "" {
		// The consumer classification travels per event because the agent
		// daemon outlives any one session: a daemon spawned from a CI run
		// must not paint later interactive sessions as autonomous.
		h["X-Tacit-Consumer"] = consumer
	}
	if client != "" {
		// Same reasoning as the consumer header: per-event, because one daemon
		// serves terminal and bridged sessions at the same time.
		h["X-Tacit-Client"] = client
	}
	return h
}

func relayPost(cfg RelayConfig, url string, payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	for k, v := range relayHeaders(cfg.APIKey, cfg.Consumer, cfg.Client) {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func healthy(baseURL string) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(baseURL + "/v1/hooks/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// SpawnAgent launches `tacit serve-hooks` detached (its own session),
// keyless, inheriting our env — so a segment / registry URL / API key present
// in the harness environment carries through. Output goes to a log; we never
// wait on it. A concurrent second spawn simply fails to bind the port and
// exits, leaving the first agent serving (the relays retry).
func SpawnAgent(cfg RelayConfig) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "serve-hooks", "--hooks-key", "")
	if f, err := os.OpenFile(cfg.SpawnLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = f, f
	}
	cmd.Stdin = nil
	detach(cmd) // own session, survives the relay exiting
	return cmd.Start()
}

// EnsureUp is connect-or-spawn: true if the agent is reachable, launching it
// if needed and polling health for up to ~tries*250ms. The health check and
// spawn are injectable for tests.
func EnsureUp(baseURL string, check func(string) bool, spawn func() error,
	sleep func(time.Duration), tries int) bool {
	if check(baseURL) {
		return true
	}
	if err := spawn(); err != nil {
		return false
	}
	for i := 0; i < tries; i++ {
		sleep(250 * time.Millisecond)
		if check(baseURL) {
			return true
		}
	}
	return false
}

// Relay forwards one hook event; any failure returns {} (never block a turn).
func Relay(cfg RelayConfig, harness string, stdin []byte,
	post func(RelayConfig, string, []byte) ([]byte, error),
	ensure func(string) bool) []byte {

	url := cfg.BaseURL + "/v1/hooks/" + harness
	if !ensure(cfg.BaseURL) {
		return []byte("{}") // agent unavailable -> no-op, never block
	}
	out, err := post(cfg, url, stdin)
	if err != nil {
		return []byte("{}") // transient -> no-op
	}
	return out
}

// InjectEventName stamps hook_event_name into a payload that lacks one.
// Copilot CLI names the event only in its hooks config, never in the payload,
// so its relay invocations carry the event as an argument (`tacit hook-relay
// copilot agentStop`). Best-effort: an unparseable payload is forwarded
// untouched — the agent's no-op default handles it.
func InjectEventName(payload []byte, event string) []byte {
	if event == "" || len(payload) == 0 {
		return payload
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil || m == nil {
		return payload
	}
	if v, _ := m["hook_event_name"].(string); v != "" {
		return payload
	}
	m["hook_event_name"] = event
	out, err := json.Marshal(m)
	if err != nil {
		return payload
	}
	return out
}

// RunRelay is the CLI entry: stdin -> agent -> stdout. event is optional —
// harnesses whose payloads name their own event pass "".
func RunRelay(cfg RelayConfig, harness, event string) int {
	data, _ := io.ReadAll(os.Stdin)
	data = InjectEventName(data, event)
	// Spawn-wait budget: 2 tries (~500ms). A healthy agent binds well inside
	// that; one that doesn't gets its event dropped ({}) rather than holding
	// the member's turn — the next hook finds it up. The budget is kept this
	// tight because any longer wait makes the FIRST hook of a session a
	// member-visible stall.
	ensure := func(base string) bool {
		return EnsureUp(base, healthy, func() error { return SpawnAgent(cfg) },
			time.Sleep, 2)
	}
	out := Relay(cfg, harness, data, relayPost, ensure)
	_, _ = os.Stdout.Write(out)
	return 0
}
