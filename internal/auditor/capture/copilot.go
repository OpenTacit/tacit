// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// GitHub Copilot CLI reader + payload translation.
//
// Copilot CLI hooks (GA 2026-02) carry the same lifecycle as the canonical
// vocabulary — sessionStart / userPromptSubmitted / preToolUse / postToolUse /
// agentStop / sessionEnd — but in camelCase, with camelCase field names
// (sessionId, toolName, toolArgs, toolResult{resultType, textResultForLlm},
// transcriptPath), and with NO event-name field in the payload at all: the
// hooks config names the event, the payload doesn't repeat it. The relay
// therefore injects hook_event_name from its command line (`tacit hook-relay
// copilot agentStop` — see hooks.RunRelay), and TranslateCopilot maps the
// names and fields at the agent boundary.
//
// Delivery: no Copilot hook output renders to the member (agentStop supports
// only decision:block/allow — a retry loop, not a display channel — and
// userPromptSubmitted documents no output contract at all). Copilot sessions
// are therefore observation-only: the agent's gate refuses to synthesize
// suggestions for this harness (hooks.deliverless) so the shown/helped funnel
// never counts a suggestion nobody saw. The ask path (MCP + skills) is the
// member-visible surface. Revisit when Copilot grows a visible hook output —
// it has shipped Claude-shaped surfaces repeatedly.

package capture

import "github.com/opentacit/tacit/internal/auditor/contracts"

// NewCopilotCapture returns the Copilot CLI reader. Tool results arrive as
// toolResult{resultType, textResultForLlm} (translated to tool_output); the
// LLM-facing text is the signal.
func NewCopilotCapture(segment contracts.Segment) *HookCapture {
	return &HookCapture{
		source: "copilot", harness: "copilot", surface: "cli",
		Segment: cloneSegment(segment),
		toolOutput: func(p map[string]any) any {
			if res, ok := p["tool_output"].(map[string]any); ok {
				if v := res["textResultForLlm"]; v != nil {
					return v
				}
				return res
			}
			return p["tool_output"]
		},
	}
}

// copilotEvents maps Copilot CLI hook event names onto the canonical
// vocabulary. Config-file aliases (the docs accept UserPromptSubmit alongside
// userPromptSubmitted) arrive already-canonical and translate to themselves
// via the canonical constants below. Unmapped events (userPromptTransformed,
// errorOccurred, preCompact, notification, subagent*) pass through to the
// agent's no-op branch.
var copilotEvents = map[string]string{
	"sessionStart":        EvSessionStart,
	"userPromptSubmitted": EvUserPrompt,
	"preToolUse":          EvPreTool,
	"postToolUse":         EvPostTool,
	"agentStop":           EvStop,
	"sessionEnd":          EvSessionEnd,
	// Canonical names are accepted as-is so a member hand-wiring with the
	// documented PascalCase aliases still translates cleanly.
	EvSessionStart: EvSessionStart,
	EvUserPrompt:   EvUserPrompt,
	EvPreTool:      EvPreTool,
	EvPostTool:     EvPostTool,
	EvStop:         EvStop,
	EvSessionEnd:   EvSessionEnd,
}

// copilotFields maps Copilot's camelCase payload fields onto the snake_case
// canonical ones. Only missing canonical keys are filled — a payload that
// already speaks canonical is untouched.
var copilotFields = map[string]string{
	"sessionId":      "session_id",
	"toolName":       "tool_name",
	"toolArgs":       "tool_input",
	"toolResult":     "tool_output",
	"transcriptPath": "transcript_path",
}

// TranslateCopilot rewrites one Copilot CLI hook payload into the canonical
// shape, in place. Idempotent.
func TranslateCopilot(payload map[string]any) map[string]any {
	if event, _ := payload["hook_event_name"].(string); event != "" {
		if mapped, ok := copilotEvents[event]; ok {
			payload["hook_event_name"] = mapped
		}
	}
	for from, to := range copilotFields {
		if v, ok := payload[from]; ok && payload[to] == nil {
			payload[to] = v
		}
	}
	return payload
}
