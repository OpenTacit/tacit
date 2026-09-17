// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

func TestParseMention(t *testing.T) {
	cases := []struct {
		prompt   string
		question string
		pure     bool
		found    bool
	}{
		{"@tacit do we have a way to do X?", "do we have a way to do X?", true, true},
		{"@Tacit: status", "status", true, true},
		{"refactor this parser. @tacit anything relevant?", "anything relevant?", false, true},
		{"refactor this parser @tacit", "refactor this parser", false, true}, // trailing: points at preceding text
		{"@tacit", "", false, false},                                         // bare address, no question
		{"mail admin@tacitcorp.com about it", "", false, false},              // not an address
		{"the @tacitly named module", "", false, false},
		{"no mention here", "", false, false},
	}
	for _, c := range cases {
		q, pure, found := parseMention(c.prompt)
		if q != c.question || pure != c.pure || found != c.found {
			t.Fatalf("parseMention(%q) = %q,%v,%v; want %q,%v,%v",
				c.prompt, q, pure, found, c.question, c.pure, c.found)
		}
	}
}

// askLLM answers as the advisor; records what it was asked.
type askLLM struct {
	stubLLM
	asked string
}

func (a *askLLM) Ask(question string, evidence contracts.EvidenceBlock) (string, error) {
	a.asked = question
	if len(evidence.Candidates) == 0 {
		return "No validated org move for this yet.", nil
	}
	return "Use " + evidence.Candidates[0].Name + " — colleagues query the warehouse live.", nil
}

func mentionAgent(t *testing.T, cands []contracts.EvidenceCandidate) (*Agent, *feedbackLog, *askLLM) {
	t.Helper()
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: cands}, nil
	}
	log := &feedbackLog{}
	model := &askLLM{}
	agent := NewAgent(provider, model, log.sink, nil,
		Options{RunAsync: inline, MentionBlock: true, Segment: contracts.Segment{"team": "revops"}})
	agent.Handle(ev("SessionStart", nil), "claude-code")
	return agent, log, model
}

func TestPureMentionBlocksAndAnswersOnClaudeCode(t *testing.T) {
	agent, log, model := mentionAgent(t, []contracts.EvidenceCandidate{orgCandidate()})
	resp := agent.Handle(ev("UserPromptSubmit",
		map[string]any{"prompt": "@tacit do we have a validated way to query revenue data?"}), "claude-code")

	if resp["decision"] != "block" {
		t.Fatalf("pure mention on claude-code should block: %v", resp)
	}
	reason, _ := resp["reason"].(string)
	if !strings.Contains(reason, Mark()) || !strings.Contains(reason, "Query live warehouse data") {
		t.Fatalf("block reason should be the advisor answer: %q", reason)
	}
	if !strings.Contains(model.asked, "query revenue data") {
		t.Fatalf("LLM asked %q; want the member's question", model.asked)
	}
	// The primary technique entered the funnel at EXPLICIT confidence.
	shown := log.byStage("shown")
	if len(shown) != 1 || shown[0].TechniqueID != "use-internal-data-connector" ||
		shown[0].Confidence != "explicit" {
		t.Fatalf("mention shown event: %+v", shown)
	}
	// And joined the session slate so ambient coaching won't re-show it —
	// without consuming the unsolicited-suggestion budget.
	agent.mu.Lock()
	st := agent.sessions["claude-code:sess_1"]
	slate, budget := st.shownIDs["use-internal-data-connector"], st.suggestionsMade
	agent.mu.Unlock()
	if !slate || budget != 0 {
		t.Fatalf("slate=%v budget=%d; want true, 0 (mentions are exempt)", slate, budget)
	}
}

func TestMixedMentionPassesThroughWithAnswer(t *testing.T) {
	agent, _, _ := mentionAgent(t, []contracts.EvidenceCandidate{orgCandidate()})
	resp := agent.Handle(ev("UserPromptSubmit",
		map[string]any{"prompt": "summarize the pasted rows below. @tacit anything better for this?"}), "claude-code")

	if resp["decision"] != nil {
		t.Fatalf("mixed mention must never block: %v", resp)
	}
	msg, _ := resp["systemMessage"].(string)
	if !strings.Contains(msg, Mark()) {
		t.Fatalf("member-visible advisor block missing: %v", resp)
	}
	hso, _ := resp["hookSpecificOutput"].(map[string]any)
	note, _ := hso["additionalContext"].(string)
	if !strings.Contains(note, "VERBATIM") || !strings.Contains(note, Mark()) ||
		!strings.Contains(note, "re-answer") {
		t.Fatalf("model note should make the model relay, not re-answer: %q", note)
	}
}

func TestPureMentionDefaultsToModelRelay(t *testing.T) {
	// Default (MentionBlock off): even a pure mention passes through with the
	// model instructed to relay the answer verbatim — model output is the one
	// channel every Claude Code client (CLI, web, mobile) actually renders.
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: []contracts.EvidenceCandidate{orgCandidate()}}, nil
	}
	agent := NewAgent(provider, &askLLM{}, nil, nil, Options{RunAsync: inline})
	agent.Handle(ev("SessionStart", nil), "claude-code")
	resp := agent.Handle(ev("UserPromptSubmit",
		map[string]any{"prompt": "@tacit anything for warehouse queries?"}), "claude-code")
	if resp["decision"] != nil {
		t.Fatalf("default must not block: %v", resp)
	}
	if msg, _ := resp["systemMessage"].(string); !strings.Contains(msg, Mark()) {
		t.Fatalf("CLI-rendered block missing: %v", resp)
	}
	hso, _ := resp["hookSpecificOutput"].(map[string]any)
	note, _ := hso["additionalContext"].(string)
	if !strings.Contains(note, "Relay the following answer") || !strings.Contains(note, "Add nothing else") ||
		!strings.Contains(note, "use-internal-data-connector") {
		t.Fatalf("pure-mention relay note: %q", note)
	}
	if strings.Contains(note, tacitSpine) {
		t.Fatalf("relay text must not carry the spine gutter (model output is markdown): %q", note)
	}
}

func TestPureMentionDegradesToVisibleAnswerOffBlockHarnesses(t *testing.T) {
	agent, _, _ := mentionAgent(t, []contracts.EvidenceCandidate{orgCandidate()})
	agent.Handle(ev("SessionStart", nil), "amp")
	resp := agent.Handle(ev("UserPromptSubmit",
		map[string]any{"prompt": "@tacit do we have a validated way to query revenue data?"}), "amp")
	if resp["decision"] != nil {
		t.Fatalf("amp is not block-validated; must not block: %v", resp)
	}
	if msg, _ := resp["systemMessage"].(string); !strings.Contains(msg, Mark()) {
		t.Fatalf("degraded pure mention should still answer visibly: %v", resp)
	}
}

func TestMentionBlockKillSwitch(t *testing.T) {
	provider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{Candidates: []contracts.EvidenceCandidate{orgCandidate()}}, nil
	}
	agent := NewAgent(provider, &askLLM{}, nil, nil, Options{RunAsync: inline}) // MentionBlock false
	agent.Handle(ev("SessionStart", nil), "claude-code")
	resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "@tacit anything for this?"}), "claude-code")
	if resp["decision"] != nil {
		t.Fatalf("kill switch off must not block: %v", resp)
	}
}

func TestMentionAnswersHonestlyOnMissAndOutage(t *testing.T) {
	// No candidates: an honest miss, no technique footer, still an answer.
	agent, log, _ := mentionAgent(t, nil)
	resp := agent.Handle(ev("UserPromptSubmit",
		map[string]any{"prompt": "@tacit validated way to fold proteins?"}), "claude-code")
	reason, _ := resp["reason"].(string)
	if !strings.Contains(reason, "No validated org move") {
		t.Fatalf("miss should be said plainly: %q", reason)
	}
	if n := len(log.byStage("shown")); n != 0 {
		t.Fatalf("a miss must not record shown events, got %d", n)
	}

	// Registry down: named, never silent.
	downProvider := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, statusErr(401)
	}
	down := NewAgent(downProvider, &askLLM{}, nil, nil, Options{RunAsync: inline, MentionBlock: true})
	down.Handle(ev("SessionStart", nil), "claude-code")
	resp = down.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "@tacit anything?"}), "claude-code")
	reason, _ = resp["reason"].(string)
	if !strings.Contains(reason, "key rejected") {
		t.Fatalf("outage answer should name the fault: %q", reason)
	}
}

func TestMentionIntentRouting(t *testing.T) {
	agent, log, _ := mentionAgent(t, []contracts.EvidenceCandidate{orgCandidate()})

	// Status: answered without touching retrieval or the funnel.
	resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "@tacit are you working?"}), "claude-code")
	if reason, _ := resp["reason"].(string); !strings.Contains(reason, "Listening") {
		t.Fatalf("status answer: %v", resp)
	}
	// Metrics: answered from the local funnel + a pointer to tacit_metrics,
	// without touching retrieval.
	resp = agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "@tacit how are we doing?"}), "claude-code")
	if reason, _ := resp["reason"].(string); !strings.Contains(reason, "tacit_metrics") {
		t.Fatalf("metrics answer: %v", resp)
	}
	// But "adoption" mid-question is a real ask, not the metrics intent.
	if got := routeIntent("do we have a validated way to improve adoption?"); got != intentAsk {
		t.Fatalf("mid-question 'adoption' should route to ask, got %q", got)
	}

	// Contribute: pointed at the existing flow.
	resp = agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "@tacit remember this move"}), "claude-code")
	if reason, _ := resp["reason"].(string); !strings.Contains(reason, "/tacit:contribute") {
		t.Fatalf("contribute answer: %v", resp)
	}
	if n := len(log.byStage("shown")); n != 0 {
		t.Fatalf("non-ask intents must not record shown events, got %d", n)
	}

	// Feedback after a real suggestion: acknowledged against the shown technique,
	// and the ambient detector (which runs first) records the verdict.
	driveToSuggestion(agent)
	log.clear()
	resp = agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "@tacit that helped"}), "claude-code")
	if reason, _ := resp["reason"].(string); !strings.Contains(reason, "recorded against") {
		t.Fatalf("feedback acknowledgment: %v", resp)
	}
	if n := len(log.byStage("helped")); n != 1 {
		t.Fatalf("ambient detector should have recorded the helped verdict, got %d", n)
	}
}

func TestAskEndpoint(t *testing.T) {
	agent, _, _ := mentionAgent(t, []contracts.EvidenceCandidate{orgCandidate()})
	ts := httptest.NewServer(Handler(agent, ""))
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/v1/hooks/ask", "application/json",
		strings.NewReader(`{"session_id": "sess_1", "harness": "claude-code", "text": "how do we query revenue?"}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("ask endpoint: %v %d", err, resp.StatusCode)
	}
	var out struct {
		Answer      string `json:"answer"`
		Block       string `json:"block"`
		Markdown    string `json:"markdown"`
		Intent      string `json:"intent"`
		TechniqueID string `json:"technique_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if out.Intent != "ask" || out.TechniqueID != "use-internal-data-connector" ||
		!strings.Contains(out.Answer, "warehouse") {
		t.Fatalf("ask response: %+v", out)
	}
	// The rendered block travels alongside the prose: a pull surface on a
	// client that renders no hook output relays this verbatim, so the ◆ spine
	// must arrive from the agent rather than be drawn by the relaying model.
	for _, ln := range strings.Split(strings.TrimPrefix(out.Block, "\n"), "\n") {
		if !strings.HasPrefix(ln, tacitSpine) {
			t.Fatalf("every block line must carry the spine, got %q", ln)
		}
	}
	if !strings.HasPrefix(out.Block, "\n"+tacitSpine+Mark()) {
		t.Fatalf("block must open with the ◆ Tacit heading: %q", out.Block)
	}
	if !strings.Contains(out.Block, out.TechniqueID) {
		t.Fatalf("block should footer the technique id: %q", out.Block)
	}
	// The relay variant must carry NO spine and no pre-wrapping: a phone-width
	// client reflows a blockquote but clips a fixed-width fenced block.
	if strings.Contains(out.Markdown, tacitSpine) {
		t.Fatalf("relay markdown must not carry the terminal spine: %q", out.Markdown)
	}
	if !strings.HasPrefix(out.Markdown, "> "+MarkBold()) {
		t.Fatalf("relay markdown must open with the quoted heading: %q", out.Markdown)
	}
	var paras int
	for _, ln := range strings.Split(out.Markdown, "\n") {
		if !strings.HasPrefix(ln, ">") {
			t.Fatalf("every relay line must stay inside the blockquote, got %q", ln)
		}
		if strings.TrimSpace(strings.TrimPrefix(ln, ">")) != "" {
			paras++
		}
	}
	// One quoted line per source paragraph, plus heading and technique footer: each
	// paragraph must arrive whole so the CLIENT chooses the wrap width. If the
	// agent pre-wrapped, this count would run well ahead of the source.
	var want int
	for _, ln := range strings.Split(out.Answer, "\n") {
		if strings.TrimSpace(ln) != "" {
			want++
		}
	}
	if paras != want+2 {
		t.Fatalf("relay markdown must not re-wrap: %d quoted lines for %d source paragraphs", paras, want)
	}
	if !strings.Contains(out.Markdown, out.TechniqueID) {
		t.Fatalf("relay markdown should footer the technique id: %q", out.Markdown)
	}
}

func TestMentionDoesNotConsumeSuggestionBudget(t *testing.T) {
	// A mention answer, then a Stop: ambient coaching still fires — the
	// member's question must not spend the unsolicited-attention budget.
	second := contracts.EvidenceCandidate{TechniqueID: "second-technique", Name: "Second technique",
		Scope: "org", Recipe: "do the other thing"}
	agent, log, _ := mentionAgent(t, []contracts.EvidenceCandidate{orgCandidate(), second})
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "@tacit anything for warehouse data?"}), "claude-code")
	log.clear()
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "claude-code")
	agent.Handle(ev("Stop", nil), "claude-code")
	resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on"}), "claude-code")
	if !strings.Contains(delivered(resp), "second-technique") {
		t.Fatalf("ambient suggestion after a mention should show the NEXT technique: %v", resp)
	}
}
