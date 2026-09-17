// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadTranscriptRawText(t *testing.T) {
	got, err := LoadTranscript("USER: hi", true, "")
	if err != nil || got != "USER: hi" {
		t.Fatalf("%v %q", err, got)
	}
}

func TestFormatTurns(t *testing.T) {
	got := Format([]Turn{{"user", "q"}, {"assistant", "a"}})
	if got != "USER: q\n\nASSISTANT: a" {
		t.Fatalf("%q", got)
	}
}

func TestParseClaudeJSONShapes(t *testing.T) {
	data := map[string]any{"chat_messages": []any{
		map[string]any{"sender": "human", "text": "hello there"},
		map[string]any{"sender": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "hi"},
			"raw string block",
		}},
		map[string]any{"sender": "assistant", "text": "   "}, // blank: skipped
	}}
	turns := ParseClaudeJSON(data)
	if len(turns) != 2 {
		t.Fatalf("turns = %d", len(turns))
	}
	if turns[0].Role != "user" { // human -> user
		t.Fatalf("role mapping: %s", turns[0].Role)
	}
	if !strings.Contains(turns[1].Text, "raw string block") {
		t.Fatalf("string block lost: %q", turns[1].Text)
	}
}

func TestParseChatGPTHTMLExtractsConversation(t *testing.T) {
	// A minimal synthetic RSC-style page: escaped literals near \"mapping\".
	html := `<script>self.__next_f.push("` +
		`\"mapping\":` +
		`\"how do I rebalance a kafka consumer group safely\",` +
		`\"assistant\",` +
		`\"` + strings.Repeat("To rebalance safely you should first drain the consumers ", 5) + `\",` +
		`\"finished_successfully\"` +
		`")</script>`
	turns := ParseChatGPTHTML(html)
	if len(turns) != 2 {
		t.Fatalf("turns = %d: %+v", len(turns), turns)
	}
	if turns[0].Role != "user" || turns[1].Role != "assistant" {
		t.Fatalf("role heuristic: %+v", turns)
	}
	if ParseChatGPTHTML("<html>no payload</html>") != nil {
		t.Fatal("page without mapping should yield nothing")
	}
}

func TestFromFileDispatch(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, "share.json")
	_ = os.WriteFile(claude, []byte(`{"chat_messages": [{"sender": "human", "text": "q"}]}`), 0o644)
	turns, err := FromFile(claude)
	if err != nil || len(turns) != 1 {
		t.Fatalf("claude json: %v %d", err, len(turns))
	}

	cf := filepath.Join(dir, "challenge.html")
	_ = os.WriteFile(cf, []byte("<title>Just a moment...</title>"), 0o644)
	if _, err := FromFile(cf); err == nil {
		t.Fatal("cloudflare challenge accepted as content")
	}

	junk := filepath.Join(dir, "junk.txt")
	_ = os.WriteFile(junk, []byte("plain text"), 0o644)
	if _, err := FromFile(junk); err == nil {
		t.Fatal("unrecognised file accepted")
	}
}
