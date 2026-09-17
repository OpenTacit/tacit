// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package llm provides the LLM clients behind a small interface.
//
// Client defines the seam: Characterize (stage 1) and Synthesize (stage 3,
// markdown audit). A Real client runs those judgments against any PROVIDER —
// Anthropic's Messages API, or any OpenAI-compatible /chat/completions endpoint
// (OpenRouter, OpenAI, Together, Groq, a local vLLM/Ollama). The provider is the
// only thing that varies, and only below `complete`: the URL, the auth headers
// and the two request shapes come from internal/llmprovider, which every part of
// OpenTacit that calls a model shares. What stays here is the error shape this layer
// diagnoses, and the judgments above `complete` — identical whichever model
// answered. Heuristic runs the whole pipeline offline
// (no API key) — degraded but functional, enough for demos and tests. Auto picks
// real-vs-heuristic per call by resolving the key live, so a key added after a
// long-running agent started takes effect with no restart.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/opentacit/tacit/internal/product"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/llmprovider"
)

// Client is the structural seam every LLM implementation satisfies.
type Client interface {
	Characterize(transcript string, hintSegment contracts.Segment) (contracts.Characterization, error)
	Synthesize(transcript string, evidence contracts.EvidenceBlock, brief bool) (string, error)
}

// Reasoner is the judgment surface only a REAL model has: the advisor answer
// and the two enrichment judgments. The Heuristic cannot make a judgment and so
// does NOT implement this — which is exactly how Auto tells "real model" from
// "can only rank words" apart. Switching on this interface (not a concrete
// provider type) is what lets a second provider (OpenAI) be dispatched as a real
// model rather than silently dropped to the heuristic.
type Reasoner interface {
	Ask(question string, evidence contracts.EvidenceBlock) (string, error)
	InferToolsAbsent(transcript string, toolsUsed []string) ([]string, error)
	VerifyAdoption(message, techniqueName, recipe string) (bool, error)
	InferWorkedMove(transcript string) (trigger, move string, err error)
	InferWorkflow(traceText string) (trigger, move string, err error)
	InferRepeatedCorrection(message string) (trigger, move string, err error)
}

// Config carries the provider settings (see internal/auditor/config).
type Config struct {
	Provider string // "anthropic" (default) | "openai" (covers OpenRouter and any OpenAI-compatible base)
	APIKey   string
	BaseURL  string
	Version  string // anthropic-version header; ignored by the OpenAI provider
	// ExtraHeaders is sent on every request. OpenRouter honors optional
	// HTTP-Referer / X-Title here for request attribution; empty for others.
	ExtraHeaders map[string]string
	CharModel    string
	SynthModel   string
	MaxTokens    int
	PromptsDir   string
	HTTP         *http.Client
}

// BriefAuditPrompt is the in-flow nudge system prompt (hook agent), distinct
// from the full multi-technique audit the CLI path renders. The model's job here is
// deliberately narrow — fit-check plus ONE tailored sentence — because the
// agent composes the visible block itself from the technique's own name and recipe
// (the delivery surface is plain text; model-generated markdown renders as
// literal noise — see docs/harness/in-harness-hooks.md).
//
// The fit-check judges TWO ways a technique can apply, not one, and the sentence it
// writes is coaching rather than commentary.
//
// This prompt used to ask only whether a technique "applies to THIS exchange" and for
// a sentence on why it "fits what they just did". Both halves are retrospective,
// and the second is the one that actually hurt: for a technique about how to frame or
// scope a request — a whole class of them — the only sentence that answers that
// question is praise for the finished turn. Measured against the real model on a
// vague-ask turn, the old wording accepted the technique "Define the problem and
// success criteria before you prompt" and then explained that the agent "surveyed
// actual resources and presented concrete options" — true, and of no use to a
// member deciding what to do next. The nudge is delivered at the end of one turn
// but read at the start of the next, so forward-looking fit is the case it most
// needs to name.
//
// It is NOT the case that the old wording rejected such techniques outright — that was
// checked, and it accepted this one. The reject clauses are kept explicit for the
// same reason: widening the question must not make the fit-check a rubber stamp.
// Verified against the real model: a prompt-shaping technique fits a vague ask, an
// unrelated technique still declines, and the same prompt-shaping technique declines on a
// fully specified request.
func BriefAuditPrompt() string {
	return "You are " + product.Name() + ", an in-flow coaching layer inside a coding agent. " +
		"Given the TRANSCRIPT of what the member just did and the COLLECTIVE EVIDENCE (one candidate " +
		"technique from their organization's playbook, used for work like this), judge whether that technique " +
		"genuinely applies to work like this. A technique can apply in either of two ways: it names " +
		"something that would have gone better in THIS exchange, or it is a move worth using on the " +
		"member's NEXT turn of work like this. Many good techniques are about how to frame, scope or set up " +
		"a request, and those can only ever be acted on going forward — so the exchange being already " +
		"finished is not a reason to reject one. Do reject a technique when the work is unrelated to it, " +
		"when the member already did what it says, or when following it would have changed nothing. " +
		"If it does not apply, output exactly: NONE. If it does, output ONE sentence (plain text, no " +
		"markdown, under 25 words) telling the member why this move fits their work — phrased as " +
		"something to do next when that is how it applies. Be specific to their exchange. " +
		"Output nothing else."
}

func formatForAudit(transcript string, evidence contracts.EvidenceBlock) string {
	raw, _ := json.MarshalIndent(evidence, "", "  ")
	return "TRANSCRIPT:\n" + transcript + "\n\nCOLLECTIVE EVIDENCE:\n" + string(raw)
}

// sliceJSON narrows a model reply to the outermost open..end pair, so a code
// fence or a stray preamble around the payload is tolerated. Without a pair it
// reports false and hands back the trimmed text, which the caller may still try
// to decode.
func sliceJSON(text string, open, end byte) (string, bool) {
	text = strings.TrimSpace(text)
	i, j := strings.IndexByte(text, open), strings.LastIndexByte(text, end)
	if i < 0 || j <= i {
		return text, false
	}
	return text[i : j+1], true
}

// extractJSON pulls the first JSON object out of a model reply (tolerating
// code fences).
func extractJSON(text string) (map[string]any, error) {
	body, ok := sliceJSON(text, '{', '}')
	if !ok {
		return nil, errors.New("no JSON object in model output")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// extractJSONInto decodes a model reply into dst, naming the judgment in the
// error so a non-JSON answer says which one produced it and what it said.
func extractJSONInto(judgment, text string, open, end byte, dst any) error {
	body, _ := sliceJSON(text, open, end)
	if err := json.Unmarshal([]byte(body), dst); err != nil {
		return fmt.Errorf("%s: model returned non-JSON %q: %w", judgment, body, err)
	}
	return nil
}

// --- provider seam ----------------------------------------------------------
//
// A completer is the wire-format shaper: the ONLY thing that differs between
// providers. Everything above it (Characterize/Synthesize/Ask/...) is
// provider-agnostic.

type completer interface {
	complete(ctx context.Context, model, system, user string) (string, error)
	// label names the provider for the diagnosis remedies ("anthropic"|"openai").
	label() string
}

// Anthropic is the Messages-API completer.
type Anthropic struct {
	cfg Config
}

func (a *Anthropic) label() string { return "anthropic" }

func (a *Anthropic) complete(ctx context.Context, model, system, user string) (string, error) {
	body, err := llmprovider.AnthropicBody(model, a.cfg.MaxTokens, system, user, nil)
	if err != nil {
		return "", err
	}
	raw, err := doRetry(ctx, httpClient(a.cfg), func() (*http.Request, error) {
		return llmprovider.AnthropicRequest(ctx, a.cfg.BaseURL, a.cfg.APIKey, a.cfg.Version, body, a.cfg.ExtraHeaders)
	}, "anthropic")
	if err != nil {
		return "", err
	}
	text, _, err := llmprovider.AnthropicText(raw)
	return text, err
}

// OpenAI is the OpenAI-compatible /chat/completions completer. The BaseURL is
// the API root that already carries the version segment (OpenRouter
// https://openrouter.ai/api/v1, OpenAI https://api.openai.com/v1); this appends
// /chat/completions. System is folded into the messages array as a system-role
// message — the one structural difference from the Messages shape.
type OpenAI struct {
	cfg Config
}

func (o *OpenAI) label() string { return "openai" }

func (o *OpenAI) complete(ctx context.Context, model, system, user string) (string, error) {
	body, err := llmprovider.OpenAIBody(model, o.cfg.MaxTokens, system, user, nil)
	if err != nil {
		return "", err
	}
	raw, err := doRetry(ctx, httpClient(o.cfg), func() (*http.Request, error) {
		return llmprovider.OpenAIRequest(ctx, o.cfg.BaseURL, o.cfg.APIKey, body, o.cfg.ExtraHeaders)
	}, "openai")
	if err != nil {
		return "", err
	}
	return llmprovider.OpenAIText(raw)
}

func httpClient(cfg Config) *http.Client {
	if cfg.HTTP != nil {
		return cfg.HTTP
	}
	return &http.Client{Timeout: 120 * time.Second}
}

// Retry/backoff for transient provider failures (rate limits, overload). The
// hook agent fans out several fit-checks per suggestion, so a low per-minute
// limit yields 429s; a short bounded retry converts most into successes. Kept
// small so a synth stays inside (or barely over) the hook's same-turn budget —
// if it does spill over, the caller parks the result for the next turn.
const (
	llmMaxRetries   = 2
	llmBaseRetry    = 500 * time.Millisecond
	llmMaxRetryWait = 4 * time.Second
)

// doRetry posts the request (rebuilt each attempt, so the body reader is fresh)
// and returns the raw 200 body, or a structured APIError stamped with the
// provider so Diagnose can phrase a provider-correct remedy. Shared by both
// completers: the retry/backoff behaviour is identical across providers.
//
// The bound here is a count of attempts, not a stretch of time — this runs on
// the hook path, where a member is waiting on the turn. The registry's research
// and the demo generator share the same loop but wait out a rate limit for
// minutes, which they can afford and this cannot.
func doRetry(ctx context.Context, client *http.Client, newReq func() (*http.Request, error), provider string) ([]byte, error) {
	attempt := 0
	return llmprovider.Retry(ctx, time.Time{}, func() ([]byte, time.Duration, error) {
		attempt++
		req, err := newReq()
		if err != nil { // malformed request: retrying cannot help
			return nil, 0, err
		}
		raw, status, header, err := llmprovider.Send(client, req)
		if err == nil && status == http.StatusOK {
			return raw, 0, nil
		}
		wait := time.Duration(0)
		if attempt <= llmMaxRetries {
			wait = retryWait(attempt, header)
		}
		if err != nil { // network error: retry
			return nil, wait, err
		}
		e := newAPIError(status, raw)
		e.Provider = provider
		if !llmprovider.Retryable(status) {
			return nil, 0, e // 4xx (bad request, auth): retrying won't help
		}
		return nil, wait, e
	})
}

// retryWait honors a Retry-After header (seconds) when present, capped; else
// exponential backoff with jitter. attempt is 1-based. Never zero: the shared
// loop reads a zero wait as "another attempt cannot help", and a provider that
// asks for zero seconds is asking to be tried again at once.
func retryWait(attempt int, h http.Header) time.Duration {
	if secs, ok := llmprovider.RetryAfter(h); ok {
		return max(min(secs, llmMaxRetryWait), time.Millisecond)
	}
	d := llmBaseRetry << (attempt - 1)
	if d > llmMaxRetryWait {
		d = llmMaxRetryWait
	}
	return d + time.Duration(rand.Int64N(int64(d)/2+1)) // +0–50% jitter
}

// APIError is a non-2xx response from a model provider, kept STRUCTURED rather
// than flattened into a string. The hook agent is required to swallow LLM
// failures — a broken model must never break a member's turn — but swallowing a
// failure and being unable to say what it was are different things: the second
// leaves the layer silently degraded with nothing anywhere reporting it.
type APIError struct {
	Provider string // "anthropic" | "openai" — which backend rejected the call
	Status   int
	Type     string // the provider's error.type
	Message  string // the provider's error.message, verbatim
}

// Error renders the provider, status, and the provider's own error text.
func (e *APIError) Error() string {
	who := e.Provider
	if who == "" {
		who = "anthropic"
	}
	if e.Message != "" {
		return fmt.Sprintf("%s HTTP %d (%s): %s", who, e.Status, e.Type, e.Message)
	}
	return fmt.Sprintf("%s HTTP %d", who, e.Status)
}

// newAPIError parses the provider's error body. Anthropic and OpenAI/OpenRouter
// share the {error:{type,message}} shape, so one parse serves both.
func newAPIError(status int, raw []byte) *APIError {
	e := &APIError{Status: status}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err == nil {
		e.Type, e.Message = body.Error.Type, body.Error.Message
	}
	if e.Message == "" {
		e.Message = string(bytes.TrimSpace(raw))
	}
	return e
}

// Diagnosis says what is wrong with the LLM and — the part that matters — what
// the operator must DO about it. A counter that only says "12 errors" tells
// whoever is on call nothing; the remedy is the product.
type Diagnosis struct {
	State  string `json:"state"`            // ok | no-key | out-of-credit | invalid-key | forbidden | rate-limited | overloaded | unreachable | api-error
	Detail string `json:"detail,omitempty"` // what the API said
	Remedy string `json:"remedy,omitempty"` // what a human must do
	// NeedsOperator is true when the fault will NOT clear on its own. Rate limits
	// and overloads resolve themselves; an empty wallet does not.
	NeedsOperator bool `json:"needs_operator"`
}

// OK is the healthy diagnosis.
var OK = Diagnosis{State: "ok"}

// keyRemedy names the fix for a rejected/absent key.
func keyRemedy() string {
	return "The model API key was rejected. Fix TACIT_LLM_API_KEY in ~/.tacit-key.env."
}

// Diagnose classifies an LLM failure into an actionable state. err == nil is OK.
func Diagnose(err error) Diagnosis {
	if err == nil {
		return OK
	}
	var api *APIError
	if !errors.As(err, &api) {
		return Diagnosis{State: "unreachable", Detail: err.Error(),
			Remedy: "The model API could not be reached. Check network/proxy egress from this host."}
	}
	provider := api.Provider
	if provider == "" {
		provider = "anthropic"
	}
	lower := strings.ToLower(api.Message)
	switch {
	// OpenRouter signals an empty wallet with 402; Anthropic uses a 400 whose
	// message mentions the credit balance.
	case api.Status == 402 || (api.Status == 400 && strings.Contains(lower, "credit balance")):
		remedy := "The Anthropic account is out of credit. Top it up at " +
			"https://console.anthropic.com/settings/billing. Suggestion delivery resumes when credit is available."
		if provider == "openai" {
			remedy = "The model provider account is out of credit. Top up your OpenRouter/OpenAI balance. " +
				"Delivery resumes when credit is available."
		}
		return Diagnosis{State: "out-of-credit", Detail: api.Message, NeedsOperator: true, Remedy: remedy}
	case api.Status == 401:
		return Diagnosis{State: "invalid-key", Detail: api.Message, NeedsOperator: true, Remedy: keyRemedy()}
	case api.Status == 403:
		return Diagnosis{State: "forbidden", Detail: api.Message, NeedsOperator: true,
			Remedy: "The API key lacks access to the configured model. Check the key's permissions, " +
				"or set TACIT_SYNTH_MODEL to a model it can use."}
	case api.Status == 404:
		return Diagnosis{State: "api-error", Detail: api.Message, NeedsOperator: true,
			Remedy: "The configured model was not found. Check TACIT_SYNTH_MODEL against the current model ids " +
				"(model names are provider-scoped — an Anthropic id will not exist on an OpenAI-compatible endpoint)."}
	case api.Status == 429:
		return Diagnosis{State: "rate-limited", Detail: api.Message,
			Remedy: "Rate-limited. This clears on its own; if it persists, lower the audit rate " +
				"or move TACIT_SYNTH_MODEL to a model with a separate limit bucket."}
	case api.Status >= 500:
		return Diagnosis{State: "overloaded", Detail: api.Message,
			Remedy: "The API is overloaded or erroring. This normally clears on its own."}
	default:
		return Diagnosis{State: "api-error", Detail: api.Message, NeedsOperator: true,
			Remedy: "The model API rejected the request. See the detail above."}
	}
}

// --- the real reasoner ------------------------------------------------------

// Real is a real LLM reasoner over any provider. The completer is the only
// thing that varies; the judgments below are identical whichever backend
// answered.
type Real struct {
	c           completer
	cfg         Config
	charPrompt  string
	auditPrompt string

	// Ctx bounds every model call this client makes. It is the daemon's
	// lifetime: cancelling it interrupts an in-flight request and the backoff
	// between attempts, so a provider that has stopped answering cannot hold a
	// goroutine — or the reservation that keeps the hook agent from idle-exiting
	// — past shutdown. Nil means context.Background.
	Ctx context.Context
}

// ctx is Real's lifetime context, or Background when none was set.
func (r *Real) ctx() context.Context {
	if r.Ctx != nil {
		return r.Ctx
	}
	return context.Background()
}

// New builds a real client for the configured provider, loading the prompt
// files and validating the key.
func New(cfg Config) (*Real, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("no API key set for provider %q", providerOf(cfg))
	}
	// Both files are authored prose that names the product, and they go to the
	// model verbatim — so they take the same rename as every other sentence that
	// does, and for the same reason: a deployment that set PRODUCT_NAME should
	// not have its coach introduce itself as somebody else's product. The two
	// prompts built in this file already compose the name; reading these from
	// disk was the one path that did not.
	charPrompt, err := os.ReadFile(filepath.Join(cfg.PromptsDir, "characterize-system-prompt.md"))
	if err != nil {
		return nil, err
	}
	auditPrompt, err := os.ReadFile(filepath.Join(cfg.PromptsDir, "audit-system-prompt.md"))
	if err != nil {
		return nil, err
	}
	var c completer
	switch providerOf(cfg) {
	case "openai":
		c = &OpenAI{cfg: cfg}
	default:
		c = &Anthropic{cfg: cfg}
	}
	return &Real{c: c, cfg: cfg,
		charPrompt:  product.Rename(string(charPrompt)),
		auditPrompt: product.Rename(string(auditPrompt))}, nil
}

// providerOf resolves the configured provider name to its wire format — the
// only distinction this package makes (Messages vs chat/completions).
func providerOf(cfg Config) string {
	return llmprovider.Lookup(cfg.Provider).Wire
}

// Characterize implements Client (stage 1, via the LLM).
func (r *Real) Characterize(transcript string, hintSegment contracts.Segment) (contracts.Characterization, error) {
	user := transcript
	if len(hintSegment) > 0 {
		hint, _ := json.Marshal(hintSegment)
		user += "\n\n(Known context: " + string(hint) + ")"
	}
	reply, err := r.c.complete(r.ctx(), r.cfg.CharModel, r.charPrompt, user)
	if err != nil {
		return contracts.Characterization{}, err
	}
	obj, err := extractJSON(reply)
	if err != nil {
		return contracts.Characterization{}, err
	}
	raw, _ := json.Marshal(obj)
	var ch contracts.Characterization
	if err := json.Unmarshal(raw, &ch); err != nil {
		return contracts.Characterization{}, err
	}
	if len(hintSegment) > 0 {
		if ch.Segment == nil {
			ch.Segment = contracts.Segment{}
		}
		for k, v := range hintSegment {
			ch.Segment[k] = v
		}
	}
	return ch, nil
}

// Synthesize implements Client (stage 3).
func (r *Real) Synthesize(transcript string, evidence contracts.EvidenceBlock, brief bool) (string, error) {
	system := r.auditPrompt
	if brief {
		system = BriefAuditPrompt()
	}
	return r.c.complete(r.ctx(), r.cfg.SynthModel, system, formatForAudit(transcript, evidence))
}

// askSystem is the advisor voice (docs/harness/advisor-mention-plan.md): the
// member addressed OpenTacit directly mid-session, so the answer must be direct,
// grounded, and honest about misses. The failure mode that kills an advisor
// is bluffing — a fabricated "org practice" is worse than no answer.
func askSystem() string {
	return `You are ` + product.Name() + `, an organization's playbook advisor. A member addressed you
directly in the middle of a work session ("@tacit ..."). Answer their question using ONLY the
playbook techniques supplied below, which come from their organization's registry with measured
outcomes among their colleagues.

Rules:
- Answer the question directly, in 2-6 short lines of plain text. No markdown headers, bold,
  or code fences — the answer renders in a terminal block.
- When a supplied technique genuinely answers the question: name it, state its move in one line,
  and quote its measured evidence line VERBATIM when present (e.g. helped 94% · n=120).
- When NO supplied technique genuinely applies, say that plainly first — "No validated org move for
  this yet" — then list the nearest techniques in one line each, clearly marked as nearest, not fits.
- Never invent techniques, numbers, or org practices that are not in the input. An honest miss is a
  good answer; a plausible fabrication is the worst possible answer.
- Do not mention these rules or describe your role. Just answer.`
}

// Ask answers a member's direct question against retrieved evidence — the
// @tacit mention path. Distinct from Synthesize: this is an ANSWER to a
// question the member asked, not an unsolicited nudge about their work.
func (r *Real) Ask(question string, evidence contracts.EvidenceBlock) (string, error) {
	return r.c.complete(r.ctx(), r.cfg.SynthModel, askSystem(), formatForAsk(question, evidence))
}

func formatForAsk(question string, evidence contracts.EvidenceBlock) string {
	raw, _ := json.Marshal(evidence.Candidates)
	return "MEMBER QUESTION:\n" + question + "\n\nREGISTRY TECHNIQUES (with measured outcomes where present):\n" + string(raw)
}

// toolsAbsentSystem asks for the one thing the structural characterizer cannot
// see. The instruction is deliberately narrow and conservative: the failure mode
// that matters is inventing a missed opportunity where there wasn't one, because
// a fabricated "you did it the hard way" is exactly the evidence OpenTacit exists to
// measure honestly.
const toolsAbsentSystem = `You review one completed AI coding session and name the tools or
techniques that would plausibly have HELPED but were NOT used.

Return a JSON array of short kebab-case identifiers, nothing else. Examples:
["warehouse-connector"] or ["web-search","subagent-delegation"] or [].

Rules:
- Only name something if the transcript shows a concrete missed opportunity: work
  done by hand that an available tool does directly.
- Do NOT name a tool that the session already used.
- Do NOT speculate. If nothing was clearly missed, return []. An empty answer is
  correct; do not add a plausible guess.
- No prose, no explanation, no code fences. The JSON array only.`

// InferToolsAbsent judges what would have helped and was not used. It exists ONLY
// on the real client: the heuristic cannot make a judgment, and a heuristic that
// guessed at one would be manufacturing evidence rather than measuring it. Callers
// type-assert for this method and simply skip enrichment when it is absent.
func (r *Real) InferToolsAbsent(transcript string, toolsUsed []string) ([]string, error) {
	user := "Tools the session already used: " + strings.Join(toolsUsed, ", ") + "\n\nTranscript:\n" + transcript
	out, err := r.c.complete(r.ctx(), r.cfg.SynthModel, toolsAbsentSystem, user)
	if err != nil {
		return nil, err
	}
	var absent []string
	if err := extractJSONInto("tools_absent", out, '[', ']', &absent); err != nil {
		return nil, err
	}
	// Never report a tool the session demonstrably used — that would contradict an
	// observed fact with an inferred one, and the observed fact wins.
	used := make(map[string]bool, len(toolsUsed))
	for _, t := range toolsUsed {
		used[strings.ToLower(t)] = true
	}
	var clean []string
	for _, t := range absent {
		if t = strings.TrimSpace(t); t != "" && !used[strings.ToLower(t)] {
			clean = append(clean, t)
		}
	}
	return clean, nil
}

// workedMoveSystem distills a reusable move from a session that already
// succeeded. The success is established BEFORE this call (a verification pass);
// the model only names the move, and is told to stay silent rather than
// manufacture a generic one — the same discipline as tools_absent.
const workedMoveSystem = `You review one completed AI coding session that DEMONSTRABLY SUCCEEDED (a test or build ran and passed this turn). Extract the single reusable "move" the person made that a colleague in the same situation could repeat.

Return ONLY a JSON object: {"trigger": "the one-sentence situation", "move": "the imperative move"}.
If there is no crisp, reusable, generally-useful move — the work was routine, one-off, or specific to this one codebase — return {"trigger":"","move":""}. Prefer silence over a generic platitude.`

// InferWorkedMove distills a reusable trigger+move from a succeeded session. Like
// InferToolsAbsent it exists only on the real client and runs only after
// delivery; empty strings mean "no reusable move here", a common honest answer.
func (r *Real) InferWorkedMove(transcript string) (trigger, move string, err error) {
	return r.inferTriggerMove("worked_move", workedMoveSystem, "Transcript:\n"+transcript)
}

// inferTriggerMove runs one distillation judgment: a system prompt over one
// piece of user text, answered as {"trigger":…,"move":…}. The two callers
// differ only in that prompt and in the name the error carries.
func (r *Real) inferTriggerMove(judgment, system, user string) (trigger, move string, err error) {
	out, err := r.c.complete(r.ctx(), r.cfg.SynthModel, system, user)
	if err != nil {
		return "", "", err
	}
	var parsed struct {
		Trigger string `json:"trigger"`
		Move    string `json:"move"`
	}
	if err := extractJSONInto(judgment, out, '{', '}', &parsed); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(parsed.Trigger), strings.TrimSpace(parsed.Move), nil
}

// repeatedCorrectionSystem distils a standing preference from a correction the
// member has now made several times.
//
// The repetition is established BEFORE this call, by the local ledger
// (internal/auditor/hooks/corrections.go). The model is not asked whether
// something is a habit — it is told that it is, and asked only to name it. That
// is the same discipline worked-move distillation follows, and for the same
// reason: a model asked to judge significance will find some.
const repeatedCorrectionSystem = `You are given ONE message in which somebody corrected an AI coding agent. They have now made this same correction several times over weeks, so it is a standing preference rather than a one-off.

Write it as a reusable technique a colleague could follow. Return ONLY a JSON object: {"trigger": "the one-sentence situation this applies in", "move": "the imperative rule, in one or two sentences"}.

Rules:
- The move must be actionable without knowing this person's project. Rewrite "use our deploy script" as the general form, or stay silent.
- Say what to DO, not what to avoid, wherever the correction allows it.
- If the correction is about one file, one bug, or one moment — nothing a colleague could reuse — return {"trigger":"","move":""}. Prefer silence over a platitude.`

// InferRepeatedCorrection distils a standing preference from one saying of a
// correction the member has repeated. Like the other distillations it exists
// only on the real client; empty strings mean "nothing reusable here", which is
// a common and honest answer.
func (r *Real) InferRepeatedCorrection(message string) (trigger, move string, err error) {
	return r.inferTriggerMove("repeated_correction", repeatedCorrectionSystem, "Message:\n"+message)
}

// workflowSystem distills a reusable WORKFLOW (the shape of the work) from a
// session that reached a verified-good state, given only the ordered sequence of
// its phases — never transcript content, which turn-scoped capture does not
// retain. Structural by necessity, and told to stay silent unless the shape
// carries a genuine, transferable approach.
const workflowSystem = `You review the PHASE SEQUENCE of one AI coding session that reached a verified-good state (a test or build passed). Each phase is one of: exploration, research, delegation, editing, verification, tool-use, conversation. You are given the ordered sequence and a one-line context, never the transcript.

If the sequence reflects a reusable WORKFLOW a colleague could adopt (e.g. "explore before editing, verify after each change"), name it. Return ONLY a JSON object: {"trigger": "the kind of task this workflow suits", "move": "the imperative workflow, as steps"}.
If the sequence is trivial or carries no transferable approach, return {"trigger":"","move":""}. Prefer silence over a platitude.`

// InferWorkflow distils a workflow technique from a session's phase trace. Like the
// other enrichment judgments it exists only on the real client and runs after
// delivery; empty strings mean "no reusable workflow here".
func (r *Real) InferWorkflow(traceText string) (trigger, move string, err error) {
	return r.inferTriggerMove("workflow", workflowSystem, traceText)
}

// verifyAdoptionSystem asks the one question the lexical trigger cannot answer:
// did the member actually act on the recommendation, or merely use words that
// collide with it? Written against the failure that matters — a fabricated
// adoption inflates the denominator of helped_rate, the number the whole
// evidence thesis rests on — so ambiguity resolves to no.
const verifyAdoptionSystem = `You judge whether a member's message shows them ACTING ON a specific
recommendation they were given: applying it, committing to apply it, or reporting having
applied it.

Answer with exactly one word: yes or no.

Rules:
- "yes" only when the message clearly engages with THIS recommendation by doing what its
  recipe says, saying they will, or reporting they did.
- Sharing vocabulary is NOT adoption. A member discussing config files has not adopted a
  technique about config verification. Coincidental word overlap is the reason you are being
  asked; treat it as "no".
- Talking about unrelated work, asking questions, or continuing a different task: no.
- If ambiguous, answer no. A missed adoption costs little; an invented one poisons the
  helped-rate every colleague sees.`

// VerifyAdoption exists ONLY on the real client: it is a judgment, and the
// heuristic cannot make one (see ErrCannotJudge on Auto).
func (r *Real) VerifyAdoption(message, techniqueName, recipe string) (bool, error) {
	user := "Recommendation: " + techniqueName + "\nRecipe: " + recipe +
		"\n\nMember's message:\n" + message
	out, err := r.c.complete(r.ctx(), r.cfg.SynthModel, verifyAdoptionSystem, user)
	if err != nil {
		return false, err
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(out)), "yes"), nil
}

// --- offline heuristic ------------------------------------------------------

// Heuristic is the offline fallback: no API key, no network. Degraded but
// exercises the full loop.
type Heuristic struct{}

// ModelID identifies the heuristic in logs.
func (Heuristic) ModelID() string { return "heuristic-offline" }

// Characterize implements Client.
func (Heuristic) Characterize(transcript string, hintSegment contracts.Segment) (contracts.Characterization, error) {
	summary := strings.Join(strings.Fields(strings.TrimSpace(transcript)), " ")
	if r := []rune(summary); len(r) > 600 {
		summary = string(r[:600])
	}
	if summary == "" {
		summary = "empty conversation"
	}
	seg := contracts.Segment{}
	for k, v := range hintSegment {
		seg[k] = v
	}
	harness := seg["harness"]
	if harness == "" {
		harness = "unknown"
	}
	surface := seg["surface"]
	if surface == "" {
		surface = "unknown"
	}
	return contracts.Characterization{
		SummaryText: summary,
		Domain:      seg["domain"],
		Modalities:  []string{"text"},
		Harness:     harness,
		Surface:     surface,
		SkillLevel:  "unknown",
		Segment:     seg,
	}, nil
}

// Synthesize implements Client.
func (Heuristic) Synthesize(transcript string, evidence contracts.EvidenceBlock, brief bool) (string, error) {
	cands := evidence.Candidates
	if len(cands) == 0 {
		if brief {
			return "", nil
		}
		return "**Overall:** Registry search returned zero suggestions for this conversation.", nil
	}
	if brief {
		// In-flow nudge: return only the one-line "why" (the agent composes
		// the visible block from the technique itself). No fit-check offline (the
		// hashing embedder ranks it), so precision rides on a real LLM.
		c := cands[0]
		if why := strings.TrimSpace(c.AppliesWhen); why != "" {
			return why, nil
		}
		return "colleagues use this for work like yours", nil
	}
	var b strings.Builder
	b.WriteString("**Overall:** Offline heuristic results. These retrieved moves may apply to this conversation:\n\n")
	for i, c := range cands {
		if i >= 3 {
			break
		}
		fmt.Fprintf(&b, "**%d. %s**\n", i+1, c.Name)
		if aw := strings.TrimSpace(c.AppliesWhen); aw != "" {
			fmt.Fprintf(&b, "*Applies when:* %s\n", aw)
		}
		fmt.Fprintf(&b, "*Suggested recipe:*\n```\n%s\n```\n\n", strings.TrimSpace(c.Recipe))
	}
	b.WriteString("_Applicability is unverified in heuristic mode. Set TACIT_LLM_API_KEY " +
		"for a fit-checked audit._")
	return b.String(), nil
}

// Ask implements the advisor answer offline: no judgment, just the nearest
// retrieved techniques, honestly labelled. Degraded but never silent — the member
// addressed OpenTacit and gets an answer.
func (Heuristic) Ask(question string, evidence contracts.EvidenceBlock) (string, error) {
	cands := evidence.Candidates
	if len(cands) == 0 {
		return "No validated org move found for this yet. (Offline heuristic — a registry answer with fit-checking needs an API key.)", nil
	}
	var b strings.Builder
	b.WriteString("Nearest techniques from your org's playbook (offline heuristic — unverified fit):\n")
	for i, c := range cands {
		if i >= 3 {
			break
		}
		line := strings.TrimSpace(strings.SplitN(c.Recipe, "\n", 2)[0])
		fmt.Fprintf(&b, "- %s — %s\n", c.Name, line)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// --- live key resolution ----------------------------------------------------

// KeyEnv is the single model-key variable, used for every provider — both as an
// environment variable and as the matching KEY=value line in the key file.
const KeyEnv = "TACIT_LLM_API_KEY"

// ResolveKey returns the model key resolved live: environment first, then a
// KEY=value env file (default ~/.tacit-key.env). Read on every call so it
// reflects the current file, not launch state — the member writes the key to
// the file themselves, so the secret never passes through a transcript.
func ResolveKey(keyFile string) string {
	if v := strings.TrimSpace(os.Getenv(KeyEnv)); v != "" {
		return v
	}
	f, err := os.Open(keyFile)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, KeyEnv+"="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// --- Auto: real-vs-heuristic per call ---------------------------------------

// Auto chooses the real vs. heuristic client per call, resolving the key live.
// UpgradeAvailable lets the hook agent tell the member that adding a key would
// sharpen suggestions — but only when heuristic is due to a missing key, not
// an explicit offline flag (then the fallback is intentional, so no nag).
type Auto struct {
	Resolve func() string // key resolver, called per synthesis
	Offline bool
	KeyFile string
	Base    Config // template for the real client (key filled per call)

	mu        sync.Mutex
	client    *Real
	clientKey string
}

func (a *Auto) active() Client {
	if a.Offline {
		return Heuristic{}
	}
	key := a.Resolve()
	if key == "" {
		return Heuristic{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.clientKey != key { // first key, or a rotated one
		cfg := a.Base
		cfg.APIKey = key
		c, err := New(cfg)
		if err != nil {
			return Heuristic{}
		}
		a.client, a.clientKey = c, key
	}
	return a.client
}

// Characterize implements Client.
func (a *Auto) Characterize(transcript string, hintSegment contracts.Segment) (contracts.Characterization, error) {
	return a.active().Characterize(transcript, hintSegment)
}

// Synthesize implements Client.
func (a *Auto) Synthesize(transcript string, evidence contracts.EvidenceBlock, brief bool) (string, error) {
	return a.active().Synthesize(transcript, evidence, brief)
}

// Ask implements the advisor answer, real or heuristic per call.
func (a *Auto) Ask(question string, evidence contracts.EvidenceBlock) (string, error) {
	if r, ok := a.active().(Reasoner); ok {
		return r.Ask(question, evidence)
	}
	return Heuristic{}.Ask(question, evidence)
}

// UpgradeAvailable reports that a key would upgrade suggestions.
func (a *Auto) UpgradeAvailable() bool { return !a.Offline && a.Resolve() == "" }

// ErrCannotJudge means no model is available to form a judgment — the heuristic
// is answering. Callers must SKIP the inference, never substitute a guess: a
// fabricated "you did it the hard way" is precisely the evidence this product
// exists to measure honestly.
var ErrCannotJudge = errors.New("no model available to judge")

// InferToolsAbsent forwards to the real client, or refuses. Auto deliberately
// does NOT fall back to the heuristic here: the heuristic can rank words, not
// judge what would have helped.
func (a *Auto) InferToolsAbsent(transcript string, toolsUsed []string) ([]string, error) {
	rr, ok := a.active().(Reasoner)
	if !ok {
		return nil, ErrCannotJudge
	}
	return rr.InferToolsAbsent(transcript, toolsUsed)
}

// InferWorkedMove forwards to the real client, or refuses — same contract as
// InferToolsAbsent: no model, no judgment, never a guess dressed as one.
func (a *Auto) InferWorkedMove(transcript string) (string, string, error) {
	rr, ok := a.active().(Reasoner)
	if !ok {
		return "", "", ErrCannotJudge
	}
	return rr.InferWorkedMove(transcript)
}

// InferWorkflow forwards to the real client, or refuses — same contract as the
// other enrichment judgments: no model, no judgment, never a guess.
func (a *Auto) InferWorkflow(traceText string) (string, string, error) {
	rr, ok := a.active().(Reasoner)
	if !ok {
		return "", "", ErrCannotJudge
	}
	return rr.InferWorkflow(traceText)
}

// InferRepeatedCorrection forwards to the real client, or refuses — the same
// contract as the other distillations: no model, no judgment, never a guess.
func (a *Auto) InferRepeatedCorrection(message string) (string, string, error) {
	rr, ok := a.active().(Reasoner)
	if !ok {
		return "", "", ErrCannotJudge
	}
	return rr.InferRepeatedCorrection(message)
}

// VerifyAdoption forwards to the real client, or refuses — the same contract as
// InferToolsAbsent: no model, no judgment, never a guess dressed as one.
func (a *Auto) VerifyAdoption(message, techniqueName, recipe string) (bool, error) {
	rr, ok := a.active().(Reasoner)
	if !ok {
		return false, ErrCannotJudge
	}
	return rr.VerifyAdoption(message, techniqueName, recipe)
}

// demoTechniqueSystem invents a technique for the demonstration trigger
// (hooks/demo.go). It is the ONE prompt in this package that asks the model to
// make something up, and it is fenced accordingly: the caller labels every
// delivery a demonstration, records nothing, and never lets the result near the
// registry.
//
// What it must produce is a technique that looks like the org's own — naming
// internal systems, not public ones — because the point being demonstrated is
// that a playbook holds moves a general model could not know. A technique that
// suggested a public tool would demonstrate nothing.
const demoTechniqueSystem = `You invent ONE plausible internal technique for an organization,
answering a request the member just made. This is a DEMONSTRATION: the technique is fiction,
and the caller labels it as such.

Return JSON only, no prose:
{"name": "...", "why": "...", "recipe": "...", "helped_rate": 0.0-1.0, "adoption_rate": 0.0-1.0, "sample_size": 5-500}

Rules:
- The technique must be ORG-SPECIFIC: invent plausible internal system, dataset, dashboard or
  channel names and use them. A technique naming only public tools demonstrates nothing, because
  a general model already knows those.
- name: the move, imperative and short (under 60 characters). Not a system name on its own.
- why: ONE sentence tying the move to what the member asked for. No preamble.
- recipe: how to do it, concretely — the systems to go to and what to ask them for. Two to
  five short lines, or numbered steps. Never repeat the name back as the recipe.
- The rates should look like a real, useful-but-not-perfect technique: helped 0.6-0.95.
- Invent nothing about the MEMBER, only about the organization's systems.`

// DemoTechnique implements hooks.DemoTechniqueMaker: one fabricated technique for the
// demonstration trigger.
func (r *Real) DemoTechnique(request string) (name, why, recipe, evidence string, err error) {
	raw, err := r.c.complete(r.ctx(), r.cfg.SynthModel, demoTechniqueSystem, "MEMBER REQUEST:\n"+request)
	if err != nil {
		return "", "", "", "", err
	}
	obj, err := extractJSON(raw)
	if err != nil {
		return "", "", "", "", err
	}
	str := func(k string) string { v, _ := obj[k].(string); return v }
	rates := map[string]float64{}
	for _, k := range []string{"helped_rate", "adoption_rate", "sample_size"} {
		if v, ok := obj[k].(float64); ok {
			rates[k] = v
		}
	}
	encoded, _ := json.Marshal(rates)
	return str("name"), str("why"), str("recipe"), string(encoded), nil
}

// DemoTechnique delegates to the real model; the heuristic cannot invent a technique, and
// the caller falls back to its own when this reports it cannot.
func (a *Auto) DemoTechnique(request string) (name, why, recipe, evidence string, err error) {
	rr, ok := a.active().(interface {
		DemoTechnique(string) (string, string, string, string, error)
	})
	if !ok {
		return "", "", "", "", ErrCannotJudge
	}
	return rr.DemoTechnique(request)
}
