// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Cursor reader + payload translation.
//
// Cursor hooks (1.7+) are executables reading JSON on stdin and answering on
// stdout — the relay fits directly — and the payload names its own event
// (hook_event_name), in Cursor's own vocabulary: sessionStart /
// beforeSubmitPrompt / preToolUse / postToolUse / afterAgentResponse / stop /
// sessionEnd (plus permission-gate and tab events OpenTacit doesn't wire). Tool
// fields are already canonical (tool_name, tool_input, tool_output); the
// session key is conversation_id, and the working directory arrives as
// workspace_roots.
//
// Assistant text arrives inline on afterAgentResponse (text), which
// translates to the AssistantText accumulator event — Cursor's
// transcript_path format is unverified, so Stop drops it and relies on the
// inline text (the opencode pattern).
//
// Delivery: no Cursor hook output renders at stop (followup_message drives
// the agent for another loop — a coaching block must never do that), but
// beforeSubmitPrompt's user_message IS member-visible. Cursor therefore runs
// park-only: the gate and synthesis run at stop, the result parks, and the
// next beforeSubmitPrompt delivers it visibly (hooks.parkOnly +
// hooks.TranslateCursorResponse).

package capture

import "github.com/opentacit/tacit/internal/auditor/contracts"

// NewCursorCapture returns the Cursor reader. Cursor is an IDE first — the
// one harness whose surface isn't "cli". Its CLI (cursor-agent) and cloud
// agents fire the same hooks and relay under the same id.
func NewCursorCapture(segment contracts.Segment) *HookCapture {
	return &HookCapture{
		source: "cursor", harness: "cursor", surface: "ide",
		Segment: cloneSegment(segment),
		toolOutput: func(p map[string]any) any {
			return p["tool_output"]
		},
	}
}

// cursorEvents maps Cursor hook event names onto the canonical vocabulary.
// Unmapped events (permission gates, tab hooks, subagent lifecycle,
// afterAgentThought, preCompact, workspaceOpen) pass through to the agent's
// no-op branch.
var cursorEvents = map[string]string{
	"sessionStart":       EvSessionStart,
	"beforeSubmitPrompt": EvUserPrompt,
	"preToolUse":         EvPreTool,
	"postToolUse":        EvPostTool,
	"afterAgentResponse": EvAssistantText,
	"stop":               EvStop,
	"sessionEnd":         EvSessionEnd,
}

// TranslateCursor rewrites one Cursor hook payload into the canonical shape,
// in place. Idempotent.
func TranslateCursor(payload map[string]any) map[string]any {
	event, _ := payload["hook_event_name"].(string)
	mapped, ok := cursorEvents[event]
	if !ok {
		return payload
	}
	payload["hook_event_name"] = mapped
	// conversation_id is the one key present on EVERY hook, so it is the
	// session key — unconditionally. sessionStart also carries its own
	// session_id; letting it win would file that one event under a different
	// session than the rest of the conversation (caught live: two buckets for
	// one session).
	if v, _ := payload["conversation_id"].(string); v != "" {
		payload["session_id"] = v
	}
	if roots, _ := payload["workspace_roots"].([]any); len(roots) > 0 && payload["cwd"] == nil {
		if root, _ := roots[0].(string); root != "" {
			payload["cwd"] = root
		}
	}
	switch mapped {
	case EvAssistantText:
		if t, _ := payload["text"].(string); t != "" {
			payload["assistant_text"] = t
		}
	case EvStop:
		// Assistant text already arrived inline (afterAgentResponse); the
		// transcript format is unverified, so never let the JSONL tail parser
		// chew it.
		delete(payload, "transcript_path")
	}
	return payload
}
