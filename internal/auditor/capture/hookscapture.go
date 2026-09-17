// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Hook-event accumulators: Claude Code, OpenAI Codex CLI, Gemini CLI, Amp,
// and pi readers.
//
// The harnesses deliver hooks one POST at a time (SessionStart,
// UserPromptSubmit, PreToolUse, PostToolUse, Stop — docs/harness/in-harness-hooks.md),
// so these readers are accumulators: fold discrete hook payloads into a
// canonical record. Codex's hook framework converged on Claude Code's — same
// event names, same snake_case input fields, identical camelCase output
// envelope (verified live) — so one accumulator serves both; only the harness
// identity and Codex's PostToolUse output field names differ. Amp and pi have
// no shell hooks at all: their in-harness adapters (plugins/amp/plugins/
// tacit.ts, plugins/pi/extensions/tacit.ts) translate their native extension
// events into this same vocabulary before relaying, so the accumulator serves
// them unchanged. (Amp can't RENDER a message at Stop, but that's the
// plugin's problem, not this reader's: the Stop response still carries the
// suggestion, which the plugin parks on disk and displays at the member's
// next prompt. pi CAN render at Stop — its adapter turns the Stop response
// into a displayed custom message.)

package capture

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// Hook event names, verified against both harnesses' docs.
const (
	EvSessionStart = "SessionStart"
	EvUserPrompt   = "UserPromptSubmit"
	EvPreTool      = "PreToolUse"
	EvPostTool     = "PostToolUse"
	// EvPostToolFail is Claude Code's separate event for a call that failed.
	// It matters more than it looks: a failing call fires PreToolUse and then
	// does NOT reach PostToolUse — measured on a live session, 2026-09-11 —
	// so without this event a failure is invisible and the tool counts silently
	// mean "calls that succeeded".
	EvPostToolFail = "PostToolUseFailure"
	EvStop         = "Stop"
	EvSessionEnd   = "SessionEnd" // Claude Code and Gemini; Codex has no such event
	// EvAssistantText carries a chunk of the assistant's response inline,
	// mid-turn — for harnesses (Cursor's afterAgentResponse) that emit the
	// text as its own event instead of on Stop. Stop then finds the turn's
	// assistant messages already ingested.
	EvAssistantText = "AssistantText"
)

// AsText renders a tool input/output (string or JSON object) to text.
func AsText(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

// HookCapture accumulates one session's hook events into a canonical record.
// One instance per session id (the hook agent keys them). Harness identity is
// data, not a subclass: NewClaudeCodeCapture, NewCodexCapture, NewAmpCapture,
// and NewPiCapture configure the same accumulator.
type HookCapture struct {
	source     string
	harness    string
	surface    string
	toolOutput func(payload map[string]any) any

	SessionID string
	Cwd       string
	Model     string
	Segment   contracts.Segment
	// Messages and ToolCalls hold the CURRENT TURN only — EndTurn clears them
	// after each audit consumes its record. Session-scoped accumulation made
	// every audit fact describe the session-so-far rather than the interaction
	// it was keyed to, drifted task_type toward `editing` the moment any turn
	// edited, and re-sent the entire session's text on every fit-check.
	Messages  []contracts.Message
	ToolCalls []contracts.ToolCall
	// resources accumulate for the WHOLE session: which repo and org systems
	// the work touched is genuinely session-scoped context, and trimming it
	// per turn would make later facts forget the warehouse connector used ten
	// minutes ago.
	resSeen  map[string]bool
	resOrder []string
}

// addResource records a session-scoped resource identifier, first-seen order.
func (c *HookCapture) addResource(r string) {
	if r == "" || c.resSeen[r] {
		return
	}
	if c.resSeen == nil {
		c.resSeen = map[string]bool{}
	}
	c.resSeen[r] = true
	c.resOrder = append(c.resOrder, r)
}

// EndTurn closes the turn a just-taken Record described: the next audit starts
// from a clean slate. Session identity, segment, and accumulated resources
// survive; the per-turn content does not.
func (c *HookCapture) EndTurn() {
	c.Messages = nil
	c.ToolCalls = nil
}

// NewClaudeCodeCapture returns the Claude Code reader.
func NewClaudeCodeCapture(segment contracts.Segment) *HookCapture {
	return &HookCapture{
		source: "claude-code", harness: "claude-code", surface: "cli",
		Segment: cloneSegment(segment),
		// tool_response is Claude Code's PostToolUse field; tool_output tolerated.
		toolOutput: func(p map[string]any) any {
			if v := p["tool_response"]; v != nil {
				return v
			}
			return p["tool_output"]
		},
	}
}

// NewCodexCapture returns the Codex CLI reader. Codex PostToolUse can carry
// the result under a few names depending on the tool (shell vs. mcp).
func NewCodexCapture(segment contracts.Segment) *HookCapture {
	return &HookCapture{
		source: "codex", harness: "codex", surface: "cli",
		Segment: cloneSegment(segment),
		toolOutput: func(p map[string]any) any {
			for _, key := range []string{"tool_output", "tool_result", "output", "tool_response"} {
				if v := p[key]; v != nil {
					return v
				}
			}
			return nil
		},
	}
}

// NewAmpCapture returns the Amp reader. Amp's plugin adapter maps the plugin
// events onto the shared hook vocabulary (tool.result carries the result as
// `output`; the adapter forwards it as tool_output), so only the identity
// differs.
func NewAmpCapture(segment contracts.Segment) *HookCapture {
	return &HookCapture{
		source: "amp", harness: "amp", surface: "cli",
		Segment: cloneSegment(segment),
		toolOutput: func(p map[string]any) any {
			if v := p["tool_output"]; v != nil {
				return v
			}
			return p["output"]
		},
	}
}

// NewPiCapture returns the pi reader. pi's extension adapter
// (plugins/pi/extensions/tacit.ts) maps pi's extension events onto the shared
// hook vocabulary and forwards tool results as tool_output, so only the
// identity differs.
func NewPiCapture(segment contracts.Segment) *HookCapture {
	return &HookCapture{
		source: "pi", harness: "pi", surface: "cli",
		Segment: cloneSegment(segment),
		toolOutput: func(p map[string]any) any {
			if v := p["tool_output"]; v != nil {
				return v
			}
			return p["output"]
		},
	}
}

// NewOmpCapture returns the omp (oh-my-pi) reader. omp is a pi-compatible fork
// that runs the very same extension (harness-aware — it relays under "omp"), so
// the reader is identical to pi's apart from the harness identity.
func NewOmpCapture(segment contracts.Segment) *HookCapture {
	return &HookCapture{
		source: "omp", harness: "omp", surface: "cli",
		Segment: cloneSegment(segment),
		toolOutput: func(p map[string]any) any {
			if v := p["tool_output"]; v != nil {
				return v
			}
			return p["output"]
		},
	}
}

// NewOpencodeCapture returns the opencode reader. opencode's plugin adapter
// (plugins/opencode/plugin/tacit.ts) maps the plugin-hook events onto the
// shared vocabulary and forwards tool results as tool_output; assistant text
// arrives inline (assistant_text — accumulated from the plugin's
// text-complete hook), so no transcript tailing is needed.
func NewOpencodeCapture(segment contracts.Segment) *HookCapture {
	return &HookCapture{
		source: "opencode", harness: "opencode", surface: "cli",
		Segment: cloneSegment(segment),
		toolOutput: func(p map[string]any) any {
			if v := p["tool_output"]; v != nil {
				return v
			}
			return p["output"]
		},
	}
}

func cloneSegment(s contracts.Segment) contracts.Segment {
	out := contracts.Segment{}
	for k, v := range s {
		out[k] = v
	}
	return out
}

// Harness returns the configured harness identity.
func (c *HookCapture) Harness() string { return c.harness }

// Apply folds one hook payload; returns its event name.
func (c *HookCapture) Apply(payload map[string]any) string {
	event, _ := payload["hook_event_name"].(string)
	if v, _ := payload["session_id"].(string); v != "" {
		c.SessionID = v
	}
	if v, _ := payload["cwd"].(string); v != "" {
		c.Cwd = v
		if repo := filepath.Base(strings.TrimRight(v, "/")); repo != "" &&
			repo != "." && repo != "/" {
			c.addResource("repo:" + repo)
		}
	}
	if v, _ := payload["model"].(string); v != "" {
		c.Model = v
	}
	// consumer:human|agent (docs/delivery/agent-delivery-plan.md Phase A) —
	// whether anyone is watching this session at delivery time. Sticky: the
	// first event's classification holds for the session, so one late CI
	// sub-step can't repaint an interactive session as autonomous.
	if v, _ := payload["tacit_consumer"].(string); (v == "human" || v == "agent") &&
		c.Segment["consumer"] == "" {
		c.Segment["consumer"] = v
	}

	switch event {
	case EvUserPrompt:
		raw, _ := payload["prompt"].(string)
		if text := strings.TrimSpace(raw); text != "" {
			c.Messages = append(c.Messages, contracts.Message{
				Role: "user", Text: text, Modalities: []string{"text"}})
		}
	case EvPostTool:
		// A completed tool call (name + input + result) — the signal
		// characterization uses.
		name, _ := payload["tool_name"].(string)
		call := contracts.ToolCall{
			Name:      name,
			Arguments: AsText(payload["tool_input"]),
			Output:    AsText(c.toolOutput(payload)),
		}
		c.ToolCalls = append(c.ToolCalls, call)
		for _, r := range ResourcesInPlay("", []contracts.ToolCall{call}) {
			c.addResource(r)
		}
	case EvAssistantText:
		// Inline mid-turn assistant text (Cursor). Same capping as the Stop
		// ingestion; a turn may emit several.
		if t, _ := payload["assistant_text"].(string); strings.TrimSpace(t) != "" {
			c.Messages = append(c.Messages, contracts.Message{
				Role: "assistant", Text: capTail(strings.TrimSpace(t), maxAssistantChars),
				Modalities: []string{"text"}})
		}
	case EvStop:
		// The assistant's half of the turn. Until this existed, NOTHING in
		// capture ever carried an assistant message: Claude Code and Codex
		// deliver the full transcript path at every Stop and no code read it,
		// so the fit-check judged "what did the org know that you missed"
		// while blind to what the model actually said or did.
		c.ingestAssistantText(payload)
	}
	return event
}

// ingestAssistantText appends this turn's assistant response: inline
// (assistant_text — the Amp adapter extracts it from agent.end's messages) or
// from the harness transcript (transcript_path — Claude Code and Codex).
// Best-effort throughout: an unreadable or unparseable transcript yields no
// message, never an error — capture must not break a turn.
func (c *HookCapture) ingestAssistantText(payload map[string]any) {
	if t, _ := payload["assistant_text"].(string); strings.TrimSpace(t) != "" {
		c.Messages = append(c.Messages, contracts.Message{
			Role: "assistant", Text: capTail(strings.TrimSpace(t), maxAssistantChars),
			Modalities: []string{"text"}})
		return
	}
	path, _ := payload["transcript_path"].(string)
	if path == "" {
		return
	}
	text, model := lastAssistantResponse(path)
	if model != "" {
		c.Model = model // the transcript names the model; hooks never do
	}
	if text != "" {
		c.Messages = append(c.Messages, contracts.Message{
			Role: "assistant", Text: text, Modalities: []string{"text"}})
	}
}

const (
	// transcriptTailBytes bounds how much of the (session-long, append-only)
	// transcript is read per Stop — the current turn lives at the end.
	transcriptTailBytes = 256 << 10
	// maxAssistantChars bounds what one assistant response contributes to the
	// record: it feeds LLM calls (fit-check, synthesis, tools_absent), so an
	// enormous response must not multiply their cost. The TAIL is kept —
	// conclusions live at the end of a response.
	maxAssistantChars = 8000
)

// TranscriptUsage reads the newest assistant message's token usage from a
// Claude Code / Codex transcript.
//
// It exists because the token fields on the session record were designed for "a
// source that reports them" and no hook harness does: the Stop payload carries
// no usage, so every record written from hooks had zeroes in it. The transcript
// has carried the numbers all along.
//
// The three classes of input come back apart, because they are priced apart —
// see TokenUsage. In() adds them, and that sum serves twice: SUMMED across turns
// it is the input a session consumed, MAXED it is the fullest the context ever
// got.
//
// Best-effort like everything else on this path: an unreadable or unparseable
// transcript reports nothing rather than failing a turn.
func TranscriptUsage(path string) (u TokenUsage, ok bool) {
	raw, read := readTail(path, transcriptTailBytes)
	if !read {
		return TokenUsage{}, false
	}
	lines := strings.Split(string(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Usage map[string]any `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "assistant" || entry.Message.Usage == nil {
			continue
		}
		m := entry.Message.Usage
		return TokenUsage{
			Fresh:      intField(m, "input_tokens"),
			CacheWrite: intField(m, "cache_creation_input_tokens"),
			CacheRead:  intField(m, "cache_read_input_tokens"),
			Out:        intField(m, "output_tokens"),
		}, true
	}
	return TokenUsage{}, false
}

// TokenUsage is one turn's tokens, split the three ways the bill is.
//
// The three are not one number. A cache read is an order of magnitude cheaper
// than fresh input and a cache write costs a premium over it, so a session that
// is 96% cache reads and one that is 96% fresh input cost wildly different money
// for the same In(). That ratio is the single biggest lever a member has on a
// bill, and it was being added up and thrown away: the reducer summed all three
// into one figure because the only question then was how much context went past
// the model.
type TokenUsage struct {
	Fresh      int // the part that missed the cache entirely
	CacheWrite int // written into the cache, at a premium
	CacheRead  int // served from it, at a discount
	Out        int
}

// In is everything the model was handed this turn.
//
// The bare `input_tokens` field is only the part that missed the cache, which on
// a long session is a handful of tokens against a context of hundreds of
// thousands; a caller reporting it as "tokens in" says input was negligible when
// input was nearly all of it. That was shipped once and looked exactly like a
// chart bug.
func (u TokenUsage) In() int { return u.Fresh + u.CacheWrite + u.CacheRead }

func intField(m map[string]any, key string) int {
	switch n := m[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// lastAssistantResponse extracts the trailing assistant response — the text
// generated since the last human message — from a Claude Code / Codex session
// transcript (JSONL; entries {"type":"assistant"|"user"|..., "message":{...}}).
// Tool results arrive as type=user entries whose content is a tool_result
// block; those are part of the assistant's working, not a human turn, so the
// walk continues past them. Returns the text and the model that produced it.
func lastAssistantResponse(path string) (text, model string) {
	raw, ok := readTail(path, transcriptTailBytes)
	if !ok {
		return "", ""
	}
	lines := strings.Split(string(raw), "\n")
	var parts []string // assistant text, collected newest-first
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Model   string          `json:"model"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // partial first line of the tail window, or noise
		}
		switch entry.Type {
		case "assistant":
			if model == "" {
				model = entry.Message.Model
			}
			if t := textBlocks(entry.Message.Content); t != "" {
				parts = append(parts, t)
			}
		case "user":
			if isHumanContent(entry.Message.Content) {
				// The previous human turn: everything above belongs to it.
				return joinReversedCapped(parts), model
			}
			// tool_result carrier — the assistant's working; keep walking.
		}
	}
	return joinReversedCapped(parts), model
}

// readTail returns up to n bytes from the end of the file.
func readTail(path string, n int64) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false
	}
	off := info.Size() - n
	if off < 0 {
		off = 0
	}
	raw := make([]byte, info.Size()-off)
	if _, err := f.ReadAt(raw, off); err != nil && len(raw) > 0 && err != io.EOF {
		return nil, false
	}
	return raw, true
}

// textBlocks joins the text blocks of a message content value (string, or a
// list of typed blocks).
func textBlocks(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n")
}

// isHumanContent reports whether a user entry is a real human turn (string
// content, or a block list containing text) rather than a tool_result carrier.
func isHumanContent(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s) != ""
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" {
			return true
		}
	}
	return false
}

func joinReversedCapped(parts []string) string {
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return capTail(strings.Join(parts, "\n"), maxAssistantChars)
}

// capTail keeps the LAST n runes — a response's conclusion outranks its preamble.
func capTail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "[…truncated…] " + string(r[len(r)-n:])
}

// Record builds the canonical record of the CURRENT TURN — the messages and
// tool calls since the last EndTurn — over session-scoped identity and
// accumulated resources.
func (c *HookCapture) Record() contracts.CanonicalRecord {
	seg := cloneSegment(c.Segment)
	if seg["harness"] == "" {
		seg["harness"] = c.harness // mirror the omnigent reader's harness stamp
	}
	if seg["consumer"] == "" {
		// Conservative default: autonomy must be proven (a relay
		// classification), never assumed.
		seg["consumer"] = "human"
	}
	return contracts.CanonicalRecord{
		SchemaVersion:           SchemaVersion,
		Source:                  c.source,
		SessionID:               c.SessionID,
		Harness:                 c.harness,
		Surface:                 c.surface,
		Model:                   c.Model,
		Messages:                c.Messages,
		ToolCalls:               c.ToolCalls,
		Segment:                 seg,
		InternalResourcesInPlay: append([]string(nil), c.resOrder...),
	}
}

// ResourcesInPlay names the org's own systems this turn actually touched: the
// repository the work happened in, and the MCP servers whose tools were called.
// It is what populates the org-systems signal on every audit — the signal that
// tells us which repo or internal service a technique depends on, and the whole
// basis of an `enabler` finding (docs/learning/synthesis-design.md).
//
// Strictly observational: a repo name and an MCP server name are enumerable
// identifiers the turn demonstrably used. Nothing here is inferred, and nothing
// free-text is emitted — file paths and tool arguments are deliberately NOT
// included, because those carry content and would breach the line audit facts
// hold (docs/learning/synthesis-design.md § the privacy line).
func ResourcesInPlay(cwd string, calls []contracts.ToolCall) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if repo := filepath.Base(strings.TrimRight(cwd, "/")); repo != "" &&
		repo != "." && repo != "/" {
		add("repo:" + repo)
	}
	for _, t := range calls {
		// Claude Code / Codex namespace MCP tools as mcp__<server>__<tool>; the
		// server is the org system, the tool is just how it was poked.
		if rest, ok := strings.CutPrefix(t.Name, "mcp__"); ok {
			if server, _, found := strings.Cut(rest, "__"); found && server != "" {
				add("mcp:" + server)
			}
		}
	}
	return out
}
