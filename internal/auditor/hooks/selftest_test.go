// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"encoding/json"
	"github.com/opentacit/tacit/internal/product"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// The point of the self-test: a member who arms it MUST see a block at the end
// of the very next turn, whatever retrieval, the cooldown or the suggestion cap
// would otherwise have decided.
func TestArmedSelfTestAlwaysDeliversAtStop(t *testing.T) {
	agent, log := newTestAgent(t, nil, Options{}) // no candidates: a real suggestion is impossible
	agent.ArmSelfTest("", ModeBlock)

	resp := driveToSuggestion(agent)
	msg, _ := resp["systemMessage"].(string)
	if msg == "" {
		t.Fatal("armed self-test delivered nothing at Stop — the one promise it makes")
	}
	for i, ln := range strings.Split(strings.TrimPrefix(msg, "\n"), "\n") {
		if !strings.HasPrefix(ln, tacitSpine) {
			t.Fatalf("line %d missing the ┃ spine: %q", i, ln)
		}
	}
	if !strings.Contains(msg, Mark()) || !strings.Contains(msg, "self-test") {
		t.Fatalf("block must be recognisable AND labelled a test:\n%s", msg)
	}
	// A test block is not evidence. Nothing may reach the funnel.
	if events := log.all(); len(events) != 0 {
		t.Fatalf("self-test recorded %d feedback events; it must record none: %+v",
			len(events), events)
	}
}

// The relay variant is what a member on a client that renders no hook output
// actually sees, so it must survive a narrow screen (blockquote, no ┃ spine, no
// pre-wrapping) and must say it was relayed — otherwise it would read as proof
// that push works, which is the one thing it does not prove.
func TestSelfTestRelayBlockIsMarkdownAndSaysItWasRelayed(t *testing.T) {
	block := SelfTestRelayBlock("claude-code")

	if strings.Contains(block, tacitSpine) {
		t.Fatalf("relay block must not carry the terminal spine:\n%s", block)
	}
	for i, ln := range strings.Split(block, "\n") {
		if !strings.HasPrefix(ln, ">") {
			t.Fatalf("line %d escapes the blockquote: %q", i, ln)
		}
	}
	if !strings.HasPrefix(block, "> ◆ **"+product.Name()) || !strings.Contains(block, "self-test") {
		t.Fatalf("relay block must be recognisable AND labelled a test:\n%s", block)
	}
	if !strings.Contains(block, "Relayed") {
		t.Fatalf("relay block must disclose that the model carried it:\n%s", block)
	}
	// Fields must stay whole — one quoted line each, separated by blank quoted
	// lines — so the client chooses the wrap width instead of the agent.
	var content int
	for _, ln := range strings.Split(block, "\n") {
		if strings.TrimSpace(strings.TrimPrefix(ln, ">")) != "" {
			content++
		}
	}
	if content != 6 { // heading + name + why + try + measured + provenance
		t.Fatalf("expected 6 whole fields, got %d:\n%s", content, block)
	}
	for _, want := range []string{"why:", "try:", "measured by colleagues:"} {
		if !strings.Contains(block, "> "+want) {
			t.Fatalf("relay block missing the %q field, so it no longer mirrors a real technique:\n%s", want, block)
		}
	}
}

// One block, not two: an armed self-test takes the turn's delivery slot even
// when a real technique would have landed, and it must not strand the gate's
// reservation — the next turn has to be able to suggest normally.
func TestSelfTestTakesTheSlotOnceAndReleasesTheGate(t *testing.T) {
	agent, log := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{CooldownTurns: -1, CooldownFor: -1})
	agent.ArmSelfTest("", ModeBlock)

	first, _ := driveToSuggestion(agent)["systemMessage"].(string)
	if !strings.Contains(first, "self-test") {
		t.Fatalf("expected the self-test to own the first turn, got:\n%s", first)
	}
	if len(log.byStage("shown")) != 0 {
		t.Fatal("the self-test turn recorded a shown event")
	}

	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "summarize these pasted warehouse rows"}), "claude-code")
	if stop := agent.Handle(ev("Stop", nil), "claude-code"); strings.Contains(delivered(stop), "self-test") {
		t.Fatal("the self-test fired twice; it is armed once")
	}
	// claude-code offers a form, so the real suggestion rides the next prompt.
	second := delivered(agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on"}), "claude-code"))
	if !strings.Contains(second, questionHeader) {
		t.Fatalf("the next turn lost its real suggestion — the gate reservation was stranded:\n%s", second)
	}
	if len(log.byStage("shown")) != 1 {
		t.Fatalf("expected exactly the real suggestion's shown event, got %d", len(log.byStage("shown")))
	}
}

// On a harness that renders nothing at a turn's end, the block must ride the
// next prompt — the same route a real suggestion takes there, which is the
// route the member is trying to validate.
func TestSelfTestParksOnParkOnlyHarness(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{})
	agent.ArmSelfTest("", ModeBlock)

	stop := agent.Handle(map[string]any{"hook_event_name": "stop", "conversation_id": "conv_1"}, "cursor")
	if len(stop) != 0 {
		t.Fatalf("cursor renders no stop output; expected a no-op, got %+v", stop)
	}
	next := agent.Handle(map[string]any{"hook_event_name": "beforeSubmitPrompt",
		"conversation_id": "conv_1", "prompt": "next thing"}, "cursor")
	msg, _ := next["user_message"].(string)
	if !strings.Contains(msg, "self-test") {
		t.Fatalf("parked self-test never arrived with the next prompt: %+v", next)
	}
	if strings.Contains(msg, "Stop hook") {
		t.Fatal("the block claimed the Stop channel on a harness that parked it")
	}
}

// Copilot renders no hook output at all. Delivering into the void and calling
// it delivered would tell the member the opposite of the truth.
func TestSelfTestReportsSuppressedOnDeliverlessHarness(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{})
	agent.ArmSelfTest("", ModeBlock)

	resp := agent.Handle(map[string]any{"hook_event_name": "agentStop", "sessionId": "s1"}, "copilot")
	if len(resp) != 0 {
		t.Fatalf("expected a no-op on a deliverless harness, got %+v", resp)
	}
	last, _ := agent.SelfTestStatus()["last"].(map[string]any)
	if last == nil || last["outcome"] != "suppressed" {
		t.Fatalf("status must say the block never rendered, got %+v", agent.SelfTestStatus())
	}
}

// An arming aimed at one harness must not be spent by another's session.
func TestSelfTestHonoursHarnessFilter(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{})
	agent.ArmSelfTest("codex", ModeBlock)

	if resp := agent.Handle(ev("Stop", nil), "claude-code"); len(resp) != 0 {
		t.Fatalf("a claude-code Stop spent an arming meant for codex: %+v", resp)
	}
	resp := agent.Handle(map[string]any{"hook_event_name": "Stop", "session_id": "sess_2"}, "codex")
	if msg, _ := resp["systemMessage"].(string); !strings.Contains(msg, "self-test") {
		t.Fatalf("codex Stop did not deliver its armed block: %+v", resp)
	}
}

// A forgotten arming must not surprise the member an hour later.
func TestSelfTestExpires(t *testing.T) {
	now := time.Now()
	agent, _ := newTestAgent(t, nil, Options{Now: func() time.Time { return now }})
	agent.ArmSelfTest("", ModeBlock)
	now = now.Add(selfTestTTL + time.Minute)

	if resp := agent.Handle(ev("Stop", nil), "claude-code"); len(resp) != 0 {
		t.Fatalf("an expired arming still fired: %+v", resp)
	}
	last, _ := agent.SelfTestStatus()["last"].(map[string]any)
	if last == nil || last["outcome"] != "expired" {
		t.Fatalf("expiry left no record: %+v", agent.SelfTestStatus())
	}
}

// The HTTP seam the CLI and the /tacit:testfeedback command use.
func TestSelfTestEndpointArmsAndReportsThroughStats(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{})
	ts := httptest.NewServer(Handler(agent, "secret"))
	defer ts.Close()

	unauth, err := http.Post(ts.URL+"/v1/hooks/selftest", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("keyless arm = %d, want 401", unauth.StatusCode)
	}

	req, _ := http.NewRequest("POST", ts.URL+"/v1/hooks/selftest", strings.NewReader(`{}`))
	req.Header.Set("X-Tacit-Key", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("arm = %d, want 200", resp.StatusCode)
	}

	stats := func() map[string]any {
		r, err := http.Get(ts.URL + "/v1/hooks/stats")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		st, _ := body["selftest"].(map[string]any)
		return st
	}
	if armed, _ := stats()["armed"].(bool); !armed {
		t.Fatalf("stats did not report the arming: %+v", stats())
	}
	driveToSuggestion(agent)
	after := stats()
	if armed, _ := after["armed"].(bool); armed {
		t.Fatal("stats still reports armed after the block was delivered")
	}
	last, _ := after["last"].(map[string]any)
	if last == nil || last["outcome"] != "delivered" {
		t.Fatalf("stats must record the delivery for `hook-selftest --status`: %+v", after)
	}
}

// The model-facing probe rides UserPromptSubmit as additionalContext with no
// systemMessage — the exact envelope tier 2's directive used, because a probe
// on a different envelope would answer a different question.
func TestContextProbeRidesTheModelFacingChannel(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{})
	agent.ArmSelfTest("", ModeContext)

	// It must NOT fire at Stop: that is the member-facing channel.
	agent.Handle(ev("SessionStart", nil), "claude-code")
	if resp := agent.Handle(ev("Stop", nil), "claude-code"); resp["systemMessage"] != nil {
		t.Fatalf("context probe leaked into the member-facing Stop channel: %v", resp)
	}

	resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "carry on"}), "claude-code")
	if _, visible := resp["systemMessage"]; visible {
		t.Errorf("probe put text in front of the member; that is what it is testing FOR: %v", resp)
	}
	out, ok := resp["hookSpecificOutput"].(map[string]any)
	if !ok || out["additionalContext"] != ContextProbeText {
		t.Fatalf("probe did not ride additionalContext: %v", resp)
	}

	// One shot: a probe that repeated would be indistinguishable from a chip
	// that persists, which is half of what the member is being asked to judge.
	again := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "and again"}), "claude-code")
	if _, repeated := again["hookSpecificOutput"]; repeated {
		t.Errorf("probe fired twice: %v", again)
	}
	if status := agent.SelfTestStatus(); status["last"].(map[string]any)["outcome"] != "context-delivered" {
		t.Errorf("outcome not recorded for --status: %v", status["last"])
	}
}

// Arming one mode must not fire the other; they answer different questions and
// a crossed wire would silently produce the wrong evidence.
func TestSelfTestModesDoNotCrossWires(t *testing.T) {
	agent, _ := newTestAgent(t, nil, Options{})
	agent.ArmSelfTest("", ModeBlock)
	agent.Handle(ev("SessionStart", nil), "claude-code")
	if resp := agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "hi"}), "claude-code"); len(resp) != 0 {
		t.Fatalf("block arming fired on the model-facing channel: %v", resp)
	}
	if resp := agent.Handle(ev("Stop", nil), "claude-code"); resp["systemMessage"] == nil {
		t.Fatal("block arming did not fire at Stop")
	}
}

// The probe may be read by the member — that is half the finding — so it has
// to be harmless when seen: labelled a self-test, and carrying no measured
// figure a model could mistake for a real technique's evidence.
func TestContextProbeIsHarmlessIfSeen(t *testing.T) {
	for _, needed := range []string{"self-test", "not one", "do not invent"} {
		if !strings.Contains(strings.ToLower(ContextProbeText), needed) {
			t.Errorf("probe does not label itself to a member who sees it (missing %q)", needed)
		}
	}
	// It describes the shape of an evidence line; it must never contain one.
	// A forged figure is the single thing no model may author, and a probe that
	// carried one would prove OpenTacit can be imitated, not delivered.
	if regexp.MustCompile(`\d+\s*%|n\s*=\s*\d`).MatchString(ContextProbeText) {
		t.Errorf("probe carries a measured-looking figure: %q", ContextProbeText)
	}
}

// The directive and the parser that captures its answer are written in two
// different files and joined only by these strings. Renaming the product broke
// exactly this join once already — the shipped skill said header "OpenTacit" while
// the parser matched "OpenTacit", so forms were raised and never captured, in
// silence. Assert the join instead of trusting it.
func TestContextProbeMatchesWhatTheParserCaptures(t *testing.T) {
	if !strings.Contains(ContextProbeText, questionHeader) {
		t.Errorf("probe asks for a header the parser will not match (%q)", questionHeader)
	}
	for _, opt := range []string{optApply, optShowHow, optNotRelevant, optAlreadyUse} {
		if !strings.Contains(ContextProbeText, opt) {
			t.Errorf("probe omits the funnel option %q, so its answer maps to nothing", opt)
		}
	}

	// And the round trip: a form raised as instructed is seen, and its answer
	// recorded, through the live PreToolUse/PostToolUse route.
	agent, log := newTestAgent(t, nil, Options{})
	agent.ArmSelfTest("", ModeContext)
	agent.Handle(ev("SessionStart", nil), "claude-code")
	agent.Handle(ev("UserPromptSubmit", map[string]any{"prompt": "anything"}), "claude-code")

	question := "Tacit self-test — does this reach you?"
	form := map[string]any{
		"questions": []any{map[string]any{"header": questionHeader, "question": question}},
		"answers":   map[string]any{question: optApply},
	}
	agent.Handle(ev("PreToolUse", map[string]any{
		"tool_name": "AskUserQuestion", "tool_input": form}), "claude-code")
	if got := agent.SelfTestStatus()["last"].(map[string]any)["outcome"]; got != "context-form-offered" {
		t.Errorf("form offer not recorded: %v", got)
	}
	agent.Handle(ev("PostToolUse", map[string]any{
		"tool_name": "AskUserQuestion", "tool_input": form}), "claude-code")
	if got := agent.SelfTestStatus()["last"].(map[string]any)["outcome"]; got != "context-form-answered: "+optApply {
		t.Errorf("form answer not recorded: %v", got)
	}

	// A self-test shows no technique, so its answer must reach the funnel's door and
	// stop: an "adopted" against nothing would be a measurement of nothing.
	if n := len(log.events); n != 0 {
		t.Errorf("self-test answer recorded %d feedback event(s); it must record none", n)
	}
}
