// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

func candidates() contracts.EvidenceBlock {
	return contracts.EvidenceBlock{Candidates: []contracts.EvidenceCandidate{{
		TechniqueID: "use-internal-data-connector",
		Name:        "Query live warehouse data",
		Recipe:      "Use the @warehouse connector",
		AppliesWhen: "The user pasted tabular data.",
	}}}
}

func TestHeuristicBriefReturnsOnlyTheWhy(t *testing.T) {
	// Brief mode returns just the one-line "why" — the hook agent composes
	// the visible block from the technique itself (plain-text surface).
	got, err := (Heuristic{}).Synthesize("transcript", candidates(), true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "The user pasted tabular data." {
		t.Fatalf("brief why = %q", got)
	}
	noWhy := candidates()
	noWhy.Candidates[0].AppliesWhen = ""
	fallback, _ := (Heuristic{}).Synthesize("t", noWhy, true)
	if !strings.Contains(fallback, "colleagues") {
		t.Fatalf("fallback why = %q", fallback)
	}
	empty, _ := (Heuristic{}).Synthesize("t", contracts.EvidenceBlock{}, true)
	if empty != "" {
		t.Fatalf("brief with no candidates = %q", empty)
	}
}

func TestHeuristicFullAudit(t *testing.T) {
	got, _ := (Heuristic{}).Synthesize("transcript", candidates(), false)
	if !strings.Contains(got, "**Overall:**") || !strings.Contains(got, "*Applies when:*") {
		t.Fatalf("full audit wrong: %s", got)
	}
	none, _ := (Heuristic{}).Synthesize("t", contracts.EvidenceBlock{}, false)
	if !strings.Contains(none, "zero suggestions") {
		t.Fatalf("empty-evidence audit wrong: %s", none)
	}
}

func TestHeuristicCharacterizeTruncates(t *testing.T) {
	long := strings.Repeat("word ", 400)
	ch, _ := (Heuristic{}).Characterize(long, contracts.Segment{"harness": "codex"})
	if len([]rune(ch.SummaryText)) > 600 {
		t.Fatalf("summary not truncated: %d", len(ch.SummaryText))
	}
	if ch.Harness != "codex" {
		t.Fatalf("hint segment lost: %+v", ch)
	}
}

func TestResolveKeyFromFileLive(t *testing.T) {
	t.Setenv("TACIT_LLM_API_KEY", "")
	keyFile := filepath.Join(t.TempDir(), "key.env")
	if got := ResolveKey(keyFile); got != "" {
		t.Fatalf("missing file resolved %q", got)
	}
	_ = os.WriteFile(keyFile, []byte("# comment\nTACIT_LLM_API_KEY=\"sk-test-123\"\n"), 0o600)
	if got := ResolveKey(keyFile); got != "sk-test-123" {
		t.Fatalf("file key = %q", got)
	}
	t.Setenv("TACIT_LLM_API_KEY", "sk-env-wins")
	if got := ResolveKey(keyFile); got != "sk-env-wins" {
		t.Fatalf("env should win: %q", got)
	}
}

func TestAutoFallsBackAndReportsUpgrade(t *testing.T) {
	auto := &Auto{Resolve: func() string { return "" }}
	if !auto.UpgradeAvailable() {
		t.Fatal("missing key should report upgrade available")
	}
	got, err := auto.Synthesize("t", candidates(), true)
	if err != nil || !strings.Contains(got, "pasted tabular data") {
		t.Fatalf("auto heuristic path: %v %q", err, got)
	}
	offline := &Auto{Resolve: func() string { return "" }, Offline: true}
	if offline.UpgradeAvailable() {
		t.Fatal("explicit offline must not nag about a key")
	}
}

func TestExtractJSONToleratesFences(t *testing.T) {
	obj, err := extractJSON("```json\n{\"task_type\": \"x\"}\n```")
	if err != nil || obj["task_type"] != "x" {
		t.Fatalf("fence extraction: %v %v", err, obj)
	}
	if _, err := extractJSON("no json here"); err == nil {
		t.Fatal("garbage accepted")
	}
}

// stubCompleter answers with a canned reply, so a judgment can be tested with
// no provider behind it.
type stubCompleter struct{ reply string }

func (s stubCompleter) complete(_ context.Context, model, system, user string) (string, error) {
	return s.reply, nil
}
func (s stubCompleter) label() string { return "stub" }

// The two distillation judgments differ only in their prompt, so they share one
// parse — which has to keep tolerating a fence and keep naming the judgment
// that produced a non-JSON answer.
func TestDistillationJudgmentsShareOneParse(t *testing.T) {
	r := &Real{c: stubCompleter{reply: "Here you go:\n```json\n{\"trigger\":\" t \",\"move\":\"m\"}\n```"}}
	for _, tc := range []struct {
		name string
		run  func() (string, string, error)
	}{
		{"worked move", func() (string, string, error) { return r.InferWorkedMove("transcript") }},
		{"workflow", func() (string, string, error) { return r.InferWorkflow("trace") }},
	} {
		trigger, move, err := tc.run()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if trigger != "t" || move != "m" {
			t.Fatalf("%s: trigger=%q move=%q", tc.name, trigger, move)
		}
	}

	bad := &Real{c: stubCompleter{reply: "sorry, no"}}
	if _, _, err := bad.InferWorkedMove("t"); err == nil || !strings.Contains(err.Error(), "worked_move") {
		t.Fatalf("error must name the judgment: %v", err)
	}
	if _, _, err := bad.InferWorkflow("t"); err == nil || !strings.Contains(err.Error(), "workflow") {
		t.Fatalf("error must name the judgment: %v", err)
	}
}

func TestCompleteRetriesOn429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0") // retry immediately in the test
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"}]}`))
	}))
	defer srv.Close()

	a := &Anthropic{cfg: Config{APIKey: "k", Version: "v", SynthModel: "m", MaxTokens: 8,
		BaseURL: srv.URL, HTTP: srv.Client()}}
	out, err := a.complete(context.Background(), "m", "sys", "user")
	if err != nil {
		t.Fatalf("complete after one 429: %v", err)
	}
	if out != "hello" {
		t.Fatalf("got %q, want hello", out)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("expected 1 retry (2 calls), got %d", n)
	}
}

func TestCompleteDoesNotRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	a := &Anthropic{cfg: Config{APIKey: "k", Version: "v", SynthModel: "m", MaxTokens: 8,
		BaseURL: srv.URL, HTTP: srv.Client()}}
	if _, err := a.complete(context.Background(), "m", "sys", "user"); err == nil {
		t.Fatal("expected an error on 400")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("4xx must not be retried; got %d calls", n)
	}
}

// The hook agent must swallow LLM failures — a broken model may never break a
// member's turn. But it swallowed them so completely that nothing anywhere could
// say OpenTacit had stopped working: suggestions simply stopped, which looks exactly
// like a quiet day. Diagnose is what turns a swallowed error into an instruction.
func TestDiagnoseGivesTheOperatorAnInstruction(t *testing.T) {
	credit := newAPIError(400, []byte(`{"type":"error","error":{"type":"invalid_request_error",
		"message":"Your credit balance is too low to access the Anthropic API."}}`))

	for _, tc := range []struct {
		name          string
		err           error
		wantState     string
		needsOperator bool
	}{
		{"healthy", nil, "ok", false},
		{"out of credit", credit, "out-of-credit", true},
		{"bad key", newAPIError(401, []byte(`{"error":{"message":"invalid x-api-key"}}`)), "invalid-key", true},
		{"no model access", newAPIError(403, []byte(`{"error":{"message":"forbidden"}}`)), "forbidden", true},
		{"wrong model id", newAPIError(404, []byte(`{"error":{"message":"model not found"}}`)), "api-error", true},
		// These clear on their own — nagging the operator about them is noise.
		{"rate limited", newAPIError(429, []byte(`{"error":{"message":"rate limit"}}`)), "rate-limited", false},
		{"overloaded", newAPIError(529, []byte(`{"error":{"message":"overloaded"}}`)), "overloaded", false},
		{"network down", errors.New("dial tcp: no route to host"), "unreachable", false},
	} {
		d := Diagnose(tc.err)
		if d.State != tc.wantState {
			t.Errorf("%s: state = %q, want %q", tc.name, d.State, tc.wantState)
		}
		if d.NeedsOperator != tc.needsOperator {
			t.Errorf("%s: needs_operator = %v, want %v", tc.name, d.NeedsOperator, tc.needsOperator)
		}
		if tc.err != nil && d.Remedy == "" {
			t.Errorf("%s: no remedy — a failure with no instruction is the bug we are fixing", tc.name)
		}
	}

	// The remedy must name the actual fix, not merely restate the error.
	if d := Diagnose(credit); !strings.Contains(d.Remedy, "console.anthropic.com") {
		t.Errorf("out-of-credit remedy should say where to top up, got: %q", d.Remedy)
	}
}

// The fit-check prompt has to admit forward-looking fit, and this guards it
// because the regression is quiet rather than loud: a prompt that asks only about
// the FINISHED exchange still accepts techniques about how to frame or scope a request,
// but the only sentence it can write for them is praise for work already done. The
// suggestion keeps arriving and stops being coaching, which no counter detects.
func TestBriefAuditPromptAdmitsForwardLookingFit(t *testing.T) {
	p := BriefAuditPrompt()
	if !strings.Contains(p, "NEXT") {
		t.Error("prompt must let a technique fit the member's NEXT turn, not only the finished one")
	}
	if !strings.Contains(p, "not a reason to reject") {
		t.Error("prompt must say outright that a finished exchange is not grounds to decline")
	}
	// Widening the question must not turn the fit-check into a rubber stamp: the
	// decline path and its grounds both have to survive.
	if !strings.Contains(p, "NONE") {
		t.Error("prompt must keep the explicit NONE decline")
	}
	if !strings.Contains(p, "Do reject") {
		t.Error("prompt must keep explicit grounds for rejecting a technique")
	}
}
