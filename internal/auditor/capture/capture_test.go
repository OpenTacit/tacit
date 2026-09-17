// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

func ev(name string, fields map[string]any) map[string]any {
	payload := map[string]any{"hook_event_name": name, "session_id": "sess_1"}
	for k, v := range fields {
		payload[k] = v
	}
	return payload
}

func TestHookCaptureFoldsPromptAndToolCall(t *testing.T) {
	c := NewClaudeCodeCapture(contracts.Segment{"team": "revops"})
	c.Apply(ev("SessionStart", map[string]any{"source": "startup", "model": "claude-sonnet-4-6"}))
	c.Apply(ev("UserPromptSubmit", map[string]any{"prompt": "summarize the pipeline"}))
	c.Apply(ev("PostToolUse", map[string]any{
		"tool_name": "Bash", "tool_input": map[string]any{"command": "wc -l pipeline.csv"},
		"tool_response": "42"}))
	rec := c.Record()
	if len(rec.Messages) != 1 || rec.Messages[0].Role != "user" {
		t.Fatalf("messages: %+v", rec.Messages)
	}
	if !strings.Contains(rec.Messages[0].Text, "summarize the pipeline") {
		t.Fatal("prompt lost")
	}
	if len(rec.ToolCalls) != 1 || rec.ToolCalls[0].Name != "Bash" {
		t.Fatalf("tool calls: %+v", rec.ToolCalls)
	}
	if !strings.Contains(rec.ToolCalls[0].Arguments, "wc -l") ||
		!strings.Contains(rec.ToolCalls[0].Output, "42") {
		t.Fatalf("tool io lost: %+v", rec.ToolCalls[0])
	}
}

func TestHookCaptureStampsHarnessSurfaceSegment(t *testing.T) {
	c := NewClaudeCodeCapture(contracts.Segment{"team": "revops"})
	c.Apply(ev("UserPromptSubmit", map[string]any{"prompt": "hi"}))
	rec := c.Record()
	if rec.Harness != "claude-code" || rec.Surface != "cli" {
		t.Fatalf("identity: %s/%s", rec.Harness, rec.Surface)
	}
	if rec.Segment["team"] != "revops" || rec.Segment["harness"] != "claude-code" {
		t.Fatalf("segment: %v", rec.Segment)
	}
}

func TestCodexCaptureReadsAlternateOutputFields(t *testing.T) {
	c := NewCodexCapture(nil)
	c.Apply(ev("PostToolUse", map[string]any{"tool_name": "shell", "tool_input": "ls",
		"tool_result": "files"}))
	rec := c.Record()
	if rec.Harness != "codex" || rec.Source != "codex" {
		t.Fatalf("codex identity: %+v", rec)
	}
	if rec.ToolCalls[0].Output != "files" {
		t.Fatalf("codex tool_result not read: %+v", rec.ToolCalls[0])
	}
}

func TestAmpCaptureIdentityAndOutputFallback(t *testing.T) {
	c := NewAmpCapture(contracts.Segment{"team": "revops"})
	// The Amp plugin adapter forwards tool.result's `output` field verbatim
	// when it hasn't renamed it to tool_output.
	c.Apply(ev("PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": "ls",
		"output": "files"}))
	rec := c.Record()
	if rec.Harness != "amp" || rec.Source != "amp" || rec.Surface != "cli" {
		t.Fatalf("amp identity: %+v", rec)
	}
	if rec.Segment["harness"] != "amp" {
		t.Fatalf("segment stamp: %v", rec.Segment)
	}
	if rec.ToolCalls[0].Output != "files" {
		t.Fatalf("amp output not read: %+v", rec.ToolCalls[0])
	}
}

func TestPiCaptureIdentityAndOutputFallback(t *testing.T) {
	c := NewPiCapture(contracts.Segment{"team": "revops"})
	// The pi extension adapter forwards tool_result content as tool_output;
	// a bare `output` field is tolerated like Amp's.
	c.Apply(ev("PostToolUse", map[string]any{"tool_name": "bash", "tool_input": "ls",
		"output": "files"}))
	rec := c.Record()
	if rec.Harness != "pi" || rec.Source != "pi" || rec.Surface != "cli" {
		t.Fatalf("pi identity: %+v", rec)
	}
	if rec.Segment["harness"] != "pi" {
		t.Fatalf("segment stamp: %v", rec.Segment)
	}
	if rec.ToolCalls[0].Output != "files" {
		t.Fatalf("pi output not read: %+v", rec.ToolCalls[0])
	}
}

func TestOmpCaptureIdentity(t *testing.T) {
	// omp (oh-my-pi) runs the same harness-aware extension, relaying under "omp".
	c := NewOmpCapture(contracts.Segment{"team": "revops"})
	c.Apply(ev("PostToolUse", map[string]any{"tool_name": "bash", "tool_input": "ls",
		"output": "files"}))
	rec := c.Record()
	if rec.Harness != "omp" || rec.Source != "omp" || rec.Surface != "cli" {
		t.Fatalf("omp identity: %+v", rec)
	}
	if rec.Segment["harness"] != "omp" || rec.ToolCalls[0].Output != "files" {
		t.Fatalf("omp capture: %+v / %+v", rec.Segment, rec.ToolCalls)
	}
}

func TestGeminiTranslationAndCapture(t *testing.T) {
	// Payloads arrive under Gemini's event names and are canonicalized by
	// TranslateGemini before the reader folds them (agent.Handle does this for
	// harness "gemini"); this test drives the same two steps.
	c := NewGeminiCapture(contracts.Segment{"team": "revops"})
	c.Apply(TranslateGemini(ev("SessionStart", map[string]any{"source": "startup"})))
	c.Apply(TranslateGemini(ev("BeforeAgent", map[string]any{"prompt": "summarize the pipeline"})))
	c.Apply(TranslateGemini(ev("AfterTool", map[string]any{
		"tool_name": "run_shell_command", "tool_input": map[string]any{"command": "wc -l pipeline.csv"},
		"tool_response": map[string]any{"llmContent": "42", "returnDisplay": "42 lines"}})))
	c.Apply(TranslateGemini(ev("AfterAgent", map[string]any{
		"prompt":          "summarize the pipeline",
		"prompt_response": "The pipeline has 42 rows.",
		"transcript_path": "/nonexistent/gemini-conversation.json"})))
	rec := c.Record()
	if rec.Harness != "gemini" || rec.Source != "gemini" || rec.Surface != "cli" {
		t.Fatalf("gemini identity: %+v", rec)
	}
	if rec.Segment["harness"] != "gemini" {
		t.Fatalf("segment stamp: %v", rec.Segment)
	}
	if len(rec.ToolCalls) != 1 || rec.ToolCalls[0].Output != "42" {
		t.Fatalf("llmContent not preferred: %+v", rec.ToolCalls)
	}
	var assistant string
	for _, m := range rec.Messages {
		if m.Role == "assistant" {
			assistant = m.Text
		}
	}
	if assistant != "The pipeline has 42 rows." {
		t.Fatalf("prompt_response not ingested inline: %+v", rec.Messages)
	}
}

func TestTranslateGeminiFoldsMCPContextAndPassesUnknownEvents(t *testing.T) {
	p := TranslateGemini(ev("AfterTool", map[string]any{
		"tool_name":   "query",
		"mcp_context": map[string]any{"server_name": "warehouse", "tool_name": "query"},
	}))
	if p["hook_event_name"] != EvPostTool || p["tool_name"] != "mcp__warehouse__query" {
		t.Fatalf("mcp fold: %v", p)
	}
	if rs := ResourcesInPlay("", []contracts.ToolCall{{Name: p["tool_name"].(string)}}); len(rs) != 1 || rs[0] != "mcp:warehouse" {
		t.Fatalf("resource extraction after fold: %v", rs)
	}
	// Events with no canonical counterpart pass through untouched — the
	// agent's default branch no-ops them.
	if p := TranslateGemini(ev("BeforeModel", nil)); p["hook_event_name"] != "BeforeModel" {
		t.Fatalf("unknown event rewritten: %v", p)
	}
}

func TestCopilotTranslationAndCapture(t *testing.T) {
	// Copilot CLI payloads are camelCase and the event name arrives from the
	// relay's command line (the payload itself never carries one).
	c := NewCopilotCapture(contracts.Segment{"team": "revops"})
	c.Apply(TranslateCopilot(map[string]any{
		"hook_event_name": "sessionStart", "sessionId": "cop_1", "cwd": "/w/tacit", "source": "startup"}))
	c.Apply(TranslateCopilot(map[string]any{
		"hook_event_name": "userPromptSubmitted", "sessionId": "cop_1", "prompt": "summarize the pipeline"}))
	c.Apply(TranslateCopilot(map[string]any{
		"hook_event_name": "postToolUse", "sessionId": "cop_1",
		"toolName": "bash", "toolArgs": `{"command":"wc -l pipeline.csv"}`,
		"toolResult": map[string]any{"resultType": "success", "textResultForLlm": "42"}}))
	rec := c.Record()
	if rec.Harness != "copilot" || rec.Source != "copilot" || rec.Surface != "cli" {
		t.Fatalf("copilot identity: %+v", rec)
	}
	if c.SessionID != "cop_1" {
		t.Fatalf("sessionId not translated: %q", c.SessionID)
	}
	if len(rec.ToolCalls) != 1 || rec.ToolCalls[0].Name != "bash" ||
		rec.ToolCalls[0].Output != "42" {
		t.Fatalf("textResultForLlm not preferred: %+v", rec.ToolCalls)
	}
	if !strings.Contains(rec.ToolCalls[0].Arguments, "wc -l") {
		t.Fatalf("toolArgs lost: %+v", rec.ToolCalls[0])
	}
	// agentStop → Stop; canonical aliases translate to themselves.
	if p := TranslateCopilot(map[string]any{"hook_event_name": "agentStop"}); p["hook_event_name"] != EvStop {
		t.Fatalf("agentStop: %v", p)
	}
	if p := TranslateCopilot(map[string]any{"hook_event_name": "PreToolUse"}); p["hook_event_name"] != EvPreTool {
		t.Fatalf("alias: %v", p)
	}
}

func TestCursorTranslationAndCapture(t *testing.T) {
	// Cursor names events its own way, keys sessions by conversation_id,
	// carries the workspace as workspace_roots, and emits assistant text
	// inline on afterAgentResponse (not at stop).
	c := NewCursorCapture(contracts.Segment{"team": "revops"})
	base := func(ev string, extra map[string]any) map[string]any {
		p := map[string]any{"hook_event_name": ev, "conversation_id": "conv_1",
			"workspace_roots": []any{"/w/tacit"}}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	c.Apply(TranslateCursor(base("sessionStart", map[string]any{"session_id": nil})))
	c.Apply(TranslateCursor(base("beforeSubmitPrompt", map[string]any{"prompt": "summarize the pipeline"})))
	c.Apply(TranslateCursor(base("postToolUse", map[string]any{
		"tool_name": "Shell", "tool_input": map[string]any{"command": "wc -l x.csv"}, "tool_output": "42"})))
	c.Apply(TranslateCursor(base("afterAgentResponse", map[string]any{"text": "The pipeline has 42 rows."})))
	c.Apply(TranslateCursor(base("stop", map[string]any{
		"status": "completed", "transcript_path": "/nonexistent/cursor.json"})))
	rec := c.Record()
	if rec.Harness != "cursor" || rec.Surface != "ide" {
		t.Fatalf("cursor identity: %+v", rec)
	}
	if c.SessionID != "conv_1" {
		t.Fatalf("conversation_id not adopted as session key: %q", c.SessionID)
	}
	if len(rec.ToolCalls) != 1 || rec.ToolCalls[0].Output != "42" {
		t.Fatalf("tool_output lost: %+v", rec.ToolCalls)
	}
	var assistant []string
	for _, m := range rec.Messages {
		if m.Role == "assistant" {
			assistant = append(assistant, m.Text)
		}
	}
	if len(assistant) != 1 || assistant[0] != "The pipeline has 42 rows." {
		t.Fatalf("afterAgentResponse text not ingested exactly once: %v", assistant)
	}
	if rec.InternalResourcesInPlay[0] != "repo:tacit" {
		t.Fatalf("workspace_roots not adopted as cwd: %v", rec.InternalResourcesInPlay)
	}
}

func TestCharacterizeIsStructuredAndDeterministic(t *testing.T) {
	c := NewClaudeCodeCapture(contracts.Segment{"team": "revops", "domain": "data"})
	c.Apply(ev("UserPromptSubmit", map[string]any{"prompt": "analyze these pasted rows"}))
	c.Apply(ev("PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": "x"}))
	char := Characterize(c.Record())
	if !strings.Contains(char.SummaryText, "pasted rows") {
		t.Fatal("summary missing prompt")
	}
	if len(char.ToolsUsed) != 1 || char.ToolsUsed[0] != "Bash" {
		t.Fatalf("tools: %v", char.ToolsUsed)
	}
	if char.Domain != "data" || char.Harness != "claude-code" {
		t.Fatalf("char: %+v", char)
	}
}

func TestTranscriptText(t *testing.T) {
	rec := contracts.CanonicalRecord{
		Messages: []contracts.Message{
			{Role: "user", Text: "hello", Modalities: []string{"text", "image"}},
			{Role: "assistant", Text: "world"},
		},
		ToolCalls: []contracts.ToolCall{{Name: "Bash", Arguments: "ls", Output: "ok"}},
	}
	text := ToTranscriptText(rec)
	if !strings.Contains(text, "USER [+image]: hello") {
		t.Fatalf("modality tag: %s", text)
	}
	if !strings.Contains(text, "ASSISTANT: world") {
		t.Fatalf("roles: %s", text)
	}
	if !strings.Contains(text, "TOOL_CALL Bash(ls) -> ok") {
		t.Fatalf("tool trace: %s", text)
	}
}

func TestReadOmnigentFixture(t *testing.T) {
	path, _ := filepath.Abs("../../../testdata/omnigent-session.json")
	rec, err := ReadOmnigentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Source != "omnigent" {
		t.Fatalf("source = %s", rec.Source)
	}
	if len(rec.Messages) == 0 {
		t.Fatal("fixture produced no messages")
	}
	if len(rec.ToolCalls) > 0 && rec.ToolCalls[0].Name == "" {
		t.Fatalf("tool call missing name: %+v", rec.ToolCalls[0])
	}
	char := Characterize(rec)
	if char.SummaryText == "" || char.SummaryText == "(empty interaction)" {
		t.Fatal("fixture characterization empty")
	}
}

func TestParseSSE(t *testing.T) {
	stream := strings.Join([]string{
		": heartbeat comment",
		"event: response.output_item.done",
		`data: {"item": {"id": "i1", "type": "message", "role": "user",` +
			` "content": [{"type": "input_text", "text": "hi"}]}}`,
		"",
		`data: {"type": "response.completed", "response": {"output": []}}`,
		"",
		"data: not-json-heartbeat",
		"",
		"data: [DONE]",
		"event: never-delivered",
	}, "\n")
	var events []SSEEvent
	err := ParseSSE(strings.NewReader(stream), func(e SSEEvent) bool {
		events = append(events, e)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d (want 2: item + completed)", len(events))
	}
	if events[0].Type != EvItemDone || events[1].Type != EvCompleted {
		t.Fatalf("types: %s %s", events[0].Type, events[1].Type)
	}
}

func TestLiveCaptureDedupesAndSignalsTurn(t *testing.T) {
	snapshot := map[string]any{
		"id": "conv_1", "harness": "claude-code",
		"items": []any{
			map[string]any{"id": "m1", "type": "message",
				"data": map[string]any{"role": "user",
					"content": []any{map[string]any{"type": "input_text", "text": "start"}}}},
		},
	}
	cap := NewLiveCapture(snapshot)
	// re-delivery of the snapshot item (flat shape this time) is idempotent
	sig := cap.Apply(SSEEvent{Type: EvItemDone, Payload: map[string]any{
		"item": map[string]any{"id": "m1", "type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "start"}}}}})
	if sig != "" {
		t.Fatalf("item-done signaled %q", sig)
	}
	cap.Apply(SSEEvent{Type: EvItemDone, Payload: map[string]any{
		"item": map[string]any{"id": "m2", "type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "input_text", "text": "reply"}}}}})
	if sig := cap.Apply(SSEEvent{Type: EvTurnCompleted, Payload: map[string]any{}}); sig != "turn" {
		t.Fatalf("turn boundary signaled %q", sig)
	}
	if sig := cap.Apply(SSEEvent{Type: EvFailed, Payload: map[string]any{}}); sig != "failed" {
		t.Fatalf("failure signaled %q", sig)
	}
	rec := cap.Record()
	if len(rec.Messages) != 2 {
		t.Fatalf("messages = %d (dedupe broken?)", len(rec.Messages))
	}
	if rec.SessionID != "conv_1" {
		t.Fatalf("session id = %s", rec.SessionID)
	}
}

func TestMonitorOffline(t *testing.T) {
	snapshot := map[string]any{"id": "conv_2", "items": []any{}}
	stream := strings.Join([]string{
		"event: response.output_item.done",
		`data: {"item": {"id": "m1", "type": "message", "role": "user",` +
			` "content": [{"type": "input_text", "text": "q"}]}}`,
		"",
		`data: {"type": "turn.completed"}`,
		"",
		`data: {"type": "turn.completed"}`,
		"",
	}, "\n")
	turns := 0
	client := &OmnigentClient{}
	_, err := client.Monitor("conv_2", func(rec contracts.CanonicalRecord, _ *LiveCapture) {
		turns++
		if len(rec.Messages) != 1 {
			t.Fatalf("turn record: %+v", rec.Messages)
		}
	}, nil, snapshot, strings.NewReader(stream), 1) // maxTurns=1 stops early
	if err != nil {
		t.Fatal(err)
	}
	if turns != 1 {
		t.Fatalf("turns = %d (maxTurns ignored)", turns)
	}
}

// task_type was absent on every one of the 135 events in the corpus, because
// nothing on the hook path produced it — so every finding conditioned on it was
// dead on arrival. It reports the SHAPE of the turn from the tools it used, not
// a guess at intent.
func TestTaskTypeFromToolSignature(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools []string
		want  string
	}{
		{"changed something", []string{"Read", "Grep", "Edit"}, "editing"},
		{"edit beats the reads around it", []string{"Read", "Bash", "Write"}, "editing"},
		{"handed off to an agent", []string{"Read", "Task"}, "delegation"},
		{"reached outside", []string{"WebSearch"}, "research"},
		{"ran something", []string{"Bash"}, "verification"},
		{"looked, changed nothing", []string{"Read", "Grep", "Glob"}, "exploration"},
		{"unknown tool", []string{"SomeNewTool"}, "tool-use"},
		{"pure dialogue", nil, "conversation"},
	} {
		if got := TaskType(tc.tools); got != tc.want {
			t.Errorf("%s: TaskType(%v) = %q, want %q", tc.name, tc.tools, got, tc.want)
		}
	}
}

// resources_in_play names the org's own systems a turn touched — the repo, and
// the MCP servers whose tools were actually called. It is the basis of an
// `enabler` finding, and nothing was populating it.
func TestResourcesInPlayNamesReposAndMCPServers(t *testing.T) {
	got := ResourcesInPlay("/home/x/Repos/tacit", []contracts.ToolCall{
		{Name: "Read"},
		{Name: "mcp__tacit__tacit_search"},
		{Name: "mcp__tacit__tacit_insights"}, // same server, named once
		{Name: "mcp__github__create_pr"},
	})
	want := []string{"repo:tacit", "mcp:tacit", "mcp:github"}
	if len(got) != len(want) {
		t.Fatalf("ResourcesInPlay = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ResourcesInPlay = %v, want %v", got, want)
		}
	}
}

// THE PRIVACY LINE: audit facts hold enumerable identifiers, never content. A
// tool's arguments and the files it touched carry the member's work; a server
// name and a repo name do not. If this ever starts leaking paths or arguments,
// the "no transcripts persisted" guarantee is gone and nobody will notice.
func TestResourcesInPlayLeaksNoContent(t *testing.T) {
	got := ResourcesInPlay("/home/x/Repos/tacit", []contracts.ToolCall{
		{Name: "Read", Arguments: `{"file_path":"/home/x/secrets/salaries.csv"}`,
			Output: "alice,220000\nbob,195000"},
		{Name: "Bash", Arguments: `{"command":"psql -c 'select * from customers'"}`},
	})
	for _, r := range got {
		if strings.Contains(r, "salaries") || strings.Contains(r, "alice") ||
			strings.Contains(r, "psql") || strings.Contains(r, "customers") ||
			strings.Contains(r, "secrets") {
			t.Fatalf("resource %q carries tool content — audit facts must hold "+
				"identifiers only", r)
		}
	}
	if len(got) != 1 || got[0] != "repo:tacit" {
		t.Fatalf("ResourcesInPlay = %v, want just [repo:tacit]", got)
	}
}

// Audits describe the interaction they are keyed to, not the session-so-far.
// The record used to accumulate forever: task_type drifted toward `editing`
// the moment any turn edited, and every fact after the first described a
// superset of the previous one.
func TestRecordIsTurnScopedButResourcesAreSessionScoped(t *testing.T) {
	c := NewClaudeCodeCapture(contracts.Segment{"team": "tacit"})
	c.Apply(map[string]any{"hook_event_name": "SessionStart", "session_id": "s1",
		"cwd": "/home/x/Repos/tacit"})
	c.Apply(map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "wire the fix in"})
	c.Apply(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": "a.go"}})
	c.Apply(map[string]any{"hook_event_name": "PostToolUse",
		"tool_name": "mcp__warehouse__query", "tool_input": map[string]any{"q": "x"}})

	turn1 := c.Record()
	if len(turn1.Messages) != 1 || len(turn1.ToolCalls) != 2 {
		t.Fatalf("turn 1: %d messages / %d calls, want 1/2", len(turn1.Messages), len(turn1.ToolCalls))
	}
	c.EndTurn()

	// Turn 2: pure conversation, no tools.
	c.Apply(map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "now explain the design"})
	turn2 := c.Record()
	if len(turn2.Messages) != 1 || turn2.Messages[0].Text != "now explain the design" {
		t.Fatalf("turn 2 messages leaked from turn 1: %+v", turn2.Messages)
	}
	if len(turn2.ToolCalls) != 0 {
		t.Fatalf("turn 2 inherited turn 1's tool calls: %+v", turn2.ToolCalls)
	}
	if got := TaskType(toolNames(turn2.ToolCalls)); got != "conversation" {
		t.Fatalf("turn 2 task_type = %q — the old cumulative drift back toward editing", got)
	}
	// Resources are the exception: which repo and org systems the SESSION
	// touched is context every later fact should keep.
	want := []string{"repo:tacit", "mcp:warehouse"}
	if len(turn2.InternalResourcesInPlay) != 2 ||
		turn2.InternalResourcesInPlay[0] != want[0] || turn2.InternalResourcesInPlay[1] != want[1] {
		t.Fatalf("session resources lost at turn boundary: %v, want %v",
			turn2.InternalResourcesInPlay, want)
	}
}

func toolNames(calls []contracts.ToolCall) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.Name)
	}
	return out
}

// The assistant's half of the turn, read from the transcript Claude Code has
// been delivering at every Stop all along. Fixture entries use the REAL
// transcript shapes (verified against a live session file): assistant entries
// with text blocks, user entries with string content (human) and with
// tool_result blocks (the assistant's working — not a human turn), plus the
// noise types a real file carries.
func TestStopIngestsTheTrailingAssistantResponse(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/session.jsonl"
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"earlier human turn"}}`,
		`{"type":"assistant","message":{"model":"claude-haiku-4-5","content":[{"type":"text","text":"EARLIER answer — belongs to the previous turn"}]}}`,
		`{"type":"user","message":{"role":"user","content":"total the pipeline by region"}}`,
		`{"type":"ai-title","title":"noise"}`,
		`{"type":"assistant","message":{"model":"claude-haiku-4-5","content":[{"type":"text","text":"I'll query the data."},{"type":"tool_use","name":"Bash"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"rows..."}]}}`,
		`{"type":"assistant","message":{"model":"claude-haiku-4-5","content":[{"type":"text","text":"North totals 120k; South 98k."}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewClaudeCodeCapture(nil)
	c.Apply(map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "total the pipeline by region"})
	c.Apply(map[string]any{"hook_event_name": "Stop", "transcript_path": path})

	rec := c.Record()
	if len(rec.Messages) != 2 {
		t.Fatalf("want user+assistant, got %d messages: %+v", len(rec.Messages), rec.Messages)
	}
	got := rec.Messages[1]
	if got.Role != "assistant" {
		t.Fatalf("second message role = %q", got.Role)
	}
	if !strings.Contains(got.Text, "I'll query the data.") ||
		!strings.Contains(got.Text, "North totals 120k") {
		t.Fatalf("assistant text incomplete: %q", got.Text)
	}
	if strings.Contains(got.Text, "EARLIER answer") {
		t.Fatal("walked past the human turn boundary into the previous response")
	}
	// The transcript names the model; hook payloads never do. This feeds the
	// per-model support matrix that has been running on an empty field.
	if rec.Model != "claude-haiku-4-5" {
		t.Fatalf("model not latched from transcript: %q", rec.Model)
	}
}

// Capture must never break a turn: garbage in, nothing out.
func TestStopTranscriptIngestionIsBestEffort(t *testing.T) {
	c := NewClaudeCodeCapture(nil)
	c.Apply(map[string]any{"hook_event_name": "Stop", "transcript_path": "/nonexistent/x.jsonl"})
	if n := len(c.Messages); n != 0 {
		t.Fatalf("missing transcript produced %d messages", n)
	}
	dir := t.TempDir()
	bad := dir + "/bad.jsonl"
	os.WriteFile(bad, []byte("not json at all\n{\"type\":\"assistant\"}\n"), 0o644)
	c.Apply(map[string]any{"hook_event_name": "Stop", "transcript_path": bad})
	if n := len(c.Messages); n != 0 {
		t.Fatalf("garbage transcript produced %d messages", n)
	}
	// The Amp inline path: assistant_text on the Stop payload.
	c.Apply(map[string]any{"hook_event_name": "Stop", "assistant_text": "  inline answer  "})
	if len(c.Messages) != 1 || c.Messages[0].Text != "inline answer" {
		t.Fatalf("inline assistant_text not ingested: %+v", c.Messages)
	}
}

// Every downstream stage inherits the characterization's segment, so this is
// where the model becomes a cohort. The raw label stays on the
// characterization for the support row and the change report; the segment
// carries the canonical key, because a cohort that splits one model across its
// release labels computes each rate over a fraction of the evidence.
func TestCharacterizeStampsTheModelCohort(t *testing.T) {
	c := NewClaudeCodeCapture(contracts.Segment{"team": "revops"})
	c.Apply(ev("UserPromptSubmit", map[string]any{"prompt": "hello"}))
	c.Apply(ev("Stop", map[string]any{"model": "claude-sonnet-5-20260101"}))
	char := Characterize(c.Record())
	if got := char.Segment["model"]; got != "anthropic/claude-sonnet-5" {
		t.Fatalf("segment model = %q, want anthropic/claude-sonnet-5", got)
	}
	if char.Model != "claude-sonnet-5-20260101" {
		t.Fatalf("raw label lost: %q", char.Model)
	}
	if char.Segment["team"] != "revops" {
		t.Fatalf("the stamp disturbed the rest of the segment: %+v", char.Segment)
	}
}

// A session with no model reported is not a cohort called "". Collecting every
// unlabelled session into one group would invent a habit nobody has.
func TestCharacterizeLeavesTheModelAloneWhenNoneIsReported(t *testing.T) {
	c := NewClaudeCodeCapture(nil)
	c.Apply(ev("UserPromptSubmit", map[string]any{"prompt": "hello"}))
	char := Characterize(c.Record())
	if _, present := char.Segment["model"]; present {
		t.Fatalf("an absent model became a cohort: %+v", char.Segment)
	}
}
