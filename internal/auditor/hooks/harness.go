// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// What the agent knows about each harness: which channels it can carry a
// suggestion on, how to read its payloads, and how to build its capture. One
// table, one row per harness, so adding the next one is a single edit and a
// trait left off a row is visible on the row rather than silent.
package hooks

import (
	"sort"

	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// harnessTraits is one harness's row. Every field is opt-in: the zero row is a
// harness that renders a ◆ block at Stop and nothing else, which is the right
// default for a harness nobody has probed yet.
type harnessTraits struct {
	// Deliverless: hook output never reaches the member, so the session is
	// observation and ask-path only.
	Deliverless bool
	// ParkOnly: no visible same-turn channel, but a visible next-turn one.
	ParkOnly bool
	// FormCapable: an ambient suggestion is offered as a native form rather
	// than printed as a ◆ block.
	FormCapable bool
	// ContextAtSessionStart: carries a model-facing additionalContext at
	// SessionStart.
	ContextAtSessionStart bool
	// MentionBlockCapable: UserPromptSubmit supports decision:"block" with a
	// member-visible reason.
	MentionBlockCapable bool
	// NewCapture builds this harness's capture. A payload from a harness with
	// no row falls back to the claude-code capture.
	NewCapture func(contracts.Segment) *capture.HookCapture
	// Translate maps this harness's raw hook payload onto the event shape the
	// agent reads. Nil where the payload already arrives in that shape.
	Translate func(map[string]any) map[string]any
}

// harnessTable is the whole set. The keys are the frozen /v1/hooks/<harness>
// path segments, and server.go routes exactly these.
var harnessTable = map[string]harnessTraits{
	// FormCapable: an ambient suggestion is OFFERED AS A FORM rather than
	// printed as a ◆ block: the model raises a native question with the
	// technique and the four funnel answers, and the member replies with one
	// keystroke.
	//
	// Chosen 2026-07-28 after the form was driven live on both a terminal and a
	// web/mobile client and rendered on each. It settles three things the block
	// could not. It reaches every surface, where hook output is dropped by web
	// and mobile — the split P3 could not close. Every delivery collects an
	// explicit answer, against a funnel whose reaction capture had recorded a
	// 100% helped rate and not one dismissal. And it cannot double-deliver,
	// because the block does not fire here at all.
	//
	// What it costs is the ignorability the ambient nudge was designed around
	// (docs/delivery/low-intrusion-plan.md): a form interrupts where a dim line
	// did not. Two things pay for it — the throttle already holds unsolicited
	// suggestions to roughly one per member per few days, and the form dismisses
	// with a single Esc. Harnesses without a native question primitive keep the
	// block, which is still the right delivery there.
	//
	// ContextAtSessionStart and MentionBlockCapable: both verified in a live
	// session, not read off a table. Codex shares the hook engine and Cursor's
	// sessionStart documents an additional_context field, so both are
	// candidates for the first; Codex is a candidate for the second
	// (advisor-mention-plan.md A0 item 2). Neither is set until somebody
	// watches it arrive in a real session.
	"claude-code": {
		FormCapable:           true,
		ContextAtSessionStart: true,
		MentionBlockCapable:   true,
		NewCapture:            capture.NewClaudeCodeCapture,
	},
	"codex": {NewCapture: capture.NewCodexCapture},
	"gemini": {
		NewCapture: capture.NewGeminiCapture,
		Translate:  capture.TranslateGemini,
	},
	// Deliverless: Copilot CLI's hook outputs never render to the member —
	// agentStop supports only decision:block/allow, and userPromptSubmitted
	// documents no output contract. Its sessions are observation + ask-path
	// only: the gate never synthesizes, the funnel never records a 'shown'
	// nobody saw, and the @-mention path — whose answer would be invisible — is
	// left to the skills. Revisit as the hook surface grows.
	"copilot": {
		Deliverless: true,
		NewCapture:  capture.NewCopilotCapture,
		Translate:   capture.TranslateCopilot,
	},
	// ParkOnly: Cursor has NO visible same-turn channel but a visible next-turn
	// one — stop output can only spin the agent via followup_message, while
	// beforeSubmitPrompt's user_message renders to the member. Its suggestions
	// always park at Stop, even when synthesis beats the budget, and deliver at
	// the next prompt; 'shown' records at that delivery, so the funnel stays
	// honest.
	"cursor": {
		ParkOnly:   true,
		NewCapture: capture.NewCursorCapture,
		Translate:  capture.TranslateCursor,
	},
	"amp":      {NewCapture: capture.NewAmpCapture},
	"pi":       {NewCapture: capture.NewPiCapture},
	"omp":      {NewCapture: capture.NewOmpCapture},
	"opencode": {NewCapture: capture.NewOpencodeCapture},
}

// HarnessNames lists the harnesses whose sessions this agent can read, in a
// stable order. It is the coverage statement the Usage page owes its reader:
// ccusage names the eighteen CLIs it reads, and a member who cannot see which
// tools count here cannot tell a quiet week from an unwired one.
//
// One list, because the set was already spelled twice — this table and the CLI's
// own (cmd/tacit/harnesses.go) — and a third copy in the web package would be
// the copy that goes stale.
func HarnessNames() []string { return harnessNames() }

// harnessNames lists the routed harnesses in a stable order.
func harnessNames() []string {
	names := make([]string, 0, len(harnessTable))
	for name := range harnessTable {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// deliverless reports whether this harness's hook output reaches the member.
func deliverless(harness string) bool { return harnessTable[harness].Deliverless }

// Capability is the one sentence a member is owed about the tool they just
// wired: what this harness can actually do with a suggestion.
//
// The guides described one delivery story — a block appears mid-session — and
// the table has said otherwise for as long as it has existed. Copilot renders
// no hook output at all, so it can only ever answer when asked; Cursor has no
// visible same-turn channel, so a suggestion lands at the next prompt. Wiring
// either one and then waiting for an interruption that the transport cannot
// carry is a failure the member has no way to diagnose
// (docs/distribution/first-adoption-plan.md, Phase 2).
//
// Empty for a harness nothing is known about, so a caller prints nothing
// rather than a guess.
func Capability(harness string) string {
	h, known := harnessTable[harness]
	switch {
	case !known:
		return ""
	case h.Deliverless:
		return "answers when you ask it, and cannot interrupt with a suggestion — this tool renders no hook output"
	case h.ParkOnly:
		return "suggestions arrive at your next prompt rather than the same turn, and you can ask any time"
	case h.FormCapable:
		return "suggestions arrive in the session as a form you dismiss with Esc, and you can ask any time"
	default:
		return "suggestions arrive in the session, and you can ask any time"
	}
}

// parkOnly reports whether this harness can only deliver on the next turn.
func parkOnly(harness string) bool { return harnessTable[harness].ParkOnly }

// formCapable reports whether an ambient suggestion is offered as a form here.
func formCapable(harness string) bool { return harnessTable[harness].FormCapable }

// contextAtSessionStart reports whether SessionStart carries model-facing text.
func contextAtSessionStart(harness string) bool {
	return harnessTable[harness].ContextAtSessionStart
}

// mentionBlockCapable reports whether UserPromptSubmit can block with a
// member-visible reason.
func mentionBlockCapable(harness string) bool {
	return harnessTable[harness].MentionBlockCapable
}

// translateHarnessPayload maps each harness onto the hook event shape used by
// the agent. Translators edit the given payload when they can.
func translateHarnessPayload(payload map[string]any, harness string) map[string]any {
	if t := harnessTable[harness].Translate; t != nil {
		return t(payload)
	}
	return payload
}

// newHarnessCapture builds the capture for a harness. An unknown harness gets
// the claude-code capture: its hook shape is the one the agent was written
// against, so it is the safest thing to read an unrecognized payload with.
func newHarnessCapture(harness string, segment contracts.Segment) *capture.HookCapture {
	if n := harnessTable[harness].NewCapture; n != nil {
		return n(segment)
	}
	return capture.NewClaudeCodeCapture(segment)
}
