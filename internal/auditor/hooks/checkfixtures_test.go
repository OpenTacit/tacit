// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/capture"
)

// One scrubbed PostToolUse payload per supported client, in that client's OWN
// raw shape, put through its own translator and then through the detector.
//
// This is the claim Phase 4 of the plan asks for and the only honest way to
// make it: the detector reads a command off `tool_input` and a result off
// whichever of five keys the client uses, and nine clients name those things
// nine ways. A table that tested one shape would report coverage it had not
// measured.
//
// Every payload here is a real shape with its content replaced: a public
// command, a public output, and no path, prompt or identity. What the detector
// keeps is checked by TestCheckEvidenceKeepsNoCommandOrResult.
var checkFixtures = []struct {
	harness string
	event   string         // the client's own event name, before translation
	payload map[string]any // the client's own field names
	want    string         // the state the detector should reach
}{
	{
		harness: "claude-code",
		event:   capture.EvPostTool,
		payload: map[string]any{
			"tool_name":     "Bash",
			"tool_input":    map[string]any{"command": "go test ./..."},
			"tool_response": map[string]any{"stdout": "ok  \ttacit/internal/registry\t0.42s"},
		},
		want: checkPassed,
	},
	{
		harness: "codex",
		event:   capture.EvPostTool,
		payload: map[string]any{
			"tool_name":   "shell",
			"tool_input":  map[string]any{"command": []any{"bash", "-lc", "pytest -q"}},
			"tool_output": "12 passed in 3.1s",
		},
		want: checkPassed,
	},
	{
		harness: "gemini",
		event:   "AfterTool",
		payload: map[string]any{
			"tool_name":   "run_shell_command",
			"tool_input":  map[string]any{"command": "npm run lint"},
			"tool_output": "\n> lint\n> eslint .\n\n",
		},
		// A linter that printed its own banner and nothing else proved nothing.
		want: checkUnknown,
	},
	{
		harness: "copilot",
		event:   "postToolUse",
		payload: map[string]any{
			"toolName":   "bash",
			"toolArgs":   map[string]any{"command": "make test"},
			"toolResult": "FAIL\tmake: *** [test] Error 1",
		},
		want: checkFailed,
	},
	{
		harness: "cursor",
		event:   "postToolUse",
		payload: map[string]any{
			"conversation_id": "c1",
			"tool_name":       "run_terminal_cmd",
			"tool_input":      map[string]any{"command": "npx tsc --noEmit"},
			"tool_output":     "Found 0 errors.",
		},
		want: checkPassed,
	},
	{
		harness: "amp",
		event:   capture.EvPostTool,
		payload: map[string]any{
			"tool_name":   "Bash",
			"tool_input":  map[string]any{"cmd": "cargo test --all"},
			"tool_output": "test result: ok. 88 passed; 0 failed",
		},
		want: checkPassed,
	},
	{
		harness: "pi",
		event:   capture.EvPostTool,
		payload: map[string]any{
			"tool_name":   "bash",
			"tool_input":  map[string]any{"command": "go vet ./..."},
			"tool_output": "",
		},
		want: checkUnknown,
	},
	{
		harness: "omp",
		event:   capture.EvPostTool,
		payload: map[string]any{
			"tool_name":   "bash",
			"tool_input":  map[string]any{"command": "mypy ."},
			"tool_output": "Success: no issues found in 41 source files",
		},
		want: checkPassed,
	},
	{
		harness: "opencode",
		event:   capture.EvPostTool,
		payload: map[string]any{
			"tool_name":   "bash",
			"tool_input":  map[string]any{"command": "pnpm test"},
			"tool_output": "Tests  3 failed | 40 passed",
		},
		want: checkFailed,
	},
}

// The detector has to read every client the agent routes. A client with no
// fixture is named rather than folded into a pass rate — an unmeasured client
// reported as covered is the one failure this table exists to prevent.
func TestCheckDetectorCoversEverySupportedClient(t *testing.T) {
	covered := map[string]bool{}
	for _, f := range checkFixtures {
		covered[f.harness] = true
	}
	var missing []string
	for _, h := range HarnessNames() {
		if !covered[h] {
			missing = append(missing, h)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("no check fixture for %v — the panel would report coverage it has not measured", missing)
	}
}

func TestCheckDetectorReadsEveryClientsShape(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	for _, f := range checkFixtures {
		t.Run(f.harness, func(t *testing.T) {
			payload := map[string]any{"hook_event_name": f.event}
			for k, v := range f.payload {
				payload[k] = v
			}
			payload = translateHarnessPayload(payload, f.harness)
			var stats sessionStats
			stats.observe(now, capture.EvUserPrompt, map[string]any{})
			event, _ := payload["hook_event_name"].(string)
			stats.observe(now, event, payload)
			if stats.checksAttempted != 1 {
				t.Fatalf("checks attempted = %d, want 1 — the command was not recognised in this client's shape",
					stats.checksAttempted)
			}
			if stats.checkState != f.want {
				t.Errorf("state = %q, want %q", stats.checkState, f.want)
			}
		})
	}
}

// The whole measure is member-local, and the way that stops being true is by
// accident: somebody adds check evidence to an audit event, an org rollup, an
// export or a federated payload because it was to hand. Nothing the registry
// can read may carry any of it, so the field names are asserted against the
// packages that talk to a server.
//
// It is asserted over the SOURCE rather than over a payload, because a payload
// only proves the one shape it was built with — and the failure being guarded
// against is a field somebody adds next year.
func TestCheckEvidenceNeverReachesTheRegistry(t *testing.T) {
	fields := []string{"change_observed", "checks_attempted", "checks_passed",
		"check_state", "model_clients"}
	for _, dir := range []string{
		"../contracts", "../audit", "../../registry/federation", "../../registry/insights",
		"../../registry/store", "../../registry/feedback",
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			for _, f := range fields {
				if strings.Contains(string(body), f) {
					t.Errorf("%s carries %q — task-outcome data must stay on the member's machine",
						path, f)
				}
			}
		}
	}
}
