// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Gemini CLI reader + payload translation.
//
// Gemini CLI's hook framework carries the same snake_case input fields as the
// Claude-converged vocabulary (session_id, transcript_path, cwd,
// hook_event_name, prompt, tool_name, tool_input — verified against the
// v0.51.0 source: packages/core createBaseInput), but Google renamed the
// EVENTS: SessionStart/BeforeAgent/BeforeTool/AfterTool/AfterAgent/SessionEnd
// instead of SessionStart/UserPromptSubmit/PreToolUse/PostToolUse/Stop/
// SessionEnd. TranslateGemini maps the names at the agent boundary so the
// accumulator and the agent's event switch stay canonical.
//
// Two more Gemini-shaped differences are folded here rather than taught to the
// shared reader:
//   - AfterAgent delivers the assistant's full response INLINE
//     (prompt_response), and Gemini's transcript is a JSON conversation file,
//     not the JSONL the shared tail parser reads — so the response is mapped
//     to assistant_text and transcript_path is dropped.
//   - MCP tool calls arrive with a bare tool_name plus an mcp_context
//     ({server_name, tool_name, ...}); the name is folded to the canonical
//     mcp__<server>__<tool> so resource extraction sees the org system.

package capture

import "github.com/opentacit/tacit/internal/auditor/contracts"

// NewGeminiCapture returns the Gemini CLI reader. Tool results arrive as
// tool_response{llmContent, returnDisplay, error}; llmContent is what the
// model saw, so it is preferred, with returnDisplay and the whole object as
// fallbacks.
func NewGeminiCapture(segment contracts.Segment) *HookCapture {
	return &HookCapture{
		source: "gemini", harness: "gemini", surface: "cli",
		Segment: cloneSegment(segment),
		toolOutput: func(p map[string]any) any {
			if resp, ok := p["tool_response"].(map[string]any); ok {
				if v := resp["llmContent"]; v != nil {
					return v
				}
				if v := resp["returnDisplay"]; v != nil {
					return v
				}
				return resp
			}
			if v := p["tool_response"]; v != nil {
				return v
			}
			return p["tool_output"]
		},
	}
}

// geminiEvents maps Gemini CLI hook event names onto the canonical vocabulary.
// Events with no canonical counterpart (BeforeModel, AfterModel,
// BeforeToolSelection, Notification, PreCompress) pass through untranslated
// and fall into the agent's no-op default — the plugin config doesn't register
// them, but a member wiring hooks by hand must not break the turn.
var geminiEvents = map[string]string{
	"SessionStart": EvSessionStart,
	"BeforeAgent":  EvUserPrompt,
	"BeforeTool":   EvPreTool,
	"AfterTool":    EvPostTool,
	"AfterAgent":   EvStop,
	"SessionEnd":   EvSessionEnd,
}

// TranslateGemini rewrites one Gemini CLI hook payload into the canonical
// shape, in place. Idempotent: a payload already carrying canonical names
// (SessionStart, SessionEnd) translates to itself.
func TranslateGemini(payload map[string]any) map[string]any {
	event, _ := payload["hook_event_name"].(string)
	mapped, ok := geminiEvents[event]
	if !ok {
		return payload
	}
	payload["hook_event_name"] = mapped
	if mapped == EvStop {
		if t, _ := payload["prompt_response"].(string); t != "" {
			payload["assistant_text"] = t
		}
		// Gemini's transcript is a JSON conversation object, not JSONL — the
		// shared tail parser would read noise. assistant_text carries the turn.
		delete(payload, "transcript_path")
	}
	if mc, ok := payload["mcp_context"].(map[string]any); ok {
		server, _ := mc["server_name"].(string)
		tool, _ := mc["tool_name"].(string)
		if tool == "" {
			tool, _ = payload["tool_name"].(string)
		}
		if server != "" && tool != "" {
			payload["tool_name"] = "mcp__" + server + "__" + tool
		}
	}
	return payload
}
