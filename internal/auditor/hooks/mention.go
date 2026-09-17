// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The @tacit mention — the addressable half of the advisor
// (docs/harness/advisor-mention-plan.md). The hook agent already LISTENS on
// every turn; this file lets the member ADDRESS it mid-prompt and get a
// direct, registry-grounded answer in OpenTacit's own voice.
//
// Two shapes:
//   - PURE mention: the whole prompt is addressed to OpenTacit ("@tacit do we
//     have a validated way to X?"). On harnesses whose UserPromptSubmit
//     supports block-with-reason (Claude Code, per its hook contract), OpenTacit
//     answers via the block reason and the host model never runs. This is the
//     ONE deliberate exception to the agent's observe-only posture, and it is
//     narrow: the agent may block a prompt if and only if the entire prompt
//     addresses OpenTacit — it is not altering the member's work; it IS the
//     addressee. Everywhere else the pure mention degrades to the mixed shape.
//   - MIXED mention: the prompt carries real work plus a mention ("refactor
//     this — @tacit anything relevant?"). The prompt passes through untouched
//     (hooks cannot rewrite prompts); OpenTacit's answer rides as a visible
//     systemMessage, and additionalContext tells the model the mention was
//     already answered so it incorporates rather than re-answers.
//
// Mentions are member-initiated, so they are EXEMPT from the ambient
// suggestion budget (throttle.go budgets unsolicited attention only, which is
// what lets that ceiling be strict without ever standing between a member and
// an answer they went looking for) — but an answered technique still joins the
// session slate so ambient coaching never re-shows what the advisor just said.

package hooks

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/product"

	"github.com/opentacit/tacit/internal/auditor/audit"
	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/sessionhash"
)

// mentionRe finds the address. Word-boundary so "email@tacitcorp.com" or
// "@tacitly" never trigger; optional trailing punctuation absorbed.
var mentionRe = regexp.MustCompile(`(?i)(^|\s)@tacit\b[:,]?\s*`)

// parseMention splits a prompt on its FIRST @tacit mention. question is what
// the member asked OpenTacit; pure means the entire prompt was the address.
// A trailing mention with nothing after it ("refactor this @tacit") points at
// the preceding text, so that becomes the question.
func parseMention(prompt string) (question string, pure, found bool) {
	loc := mentionRe.FindStringIndex(prompt)
	if loc == nil {
		return "", false, false
	}
	before := strings.TrimSpace(prompt[:loc[0]])
	after := strings.TrimSpace(prompt[loc[1]:])
	question = after
	if question == "" {
		question = before
	}
	if question == "" {
		return "", false, false // a bare "@tacit" with no question at all
	}
	return question, before == "", true
}

// askIntent is the cheap deterministic router (advisor-mention-plan.md A1).
// LLM routing can replace this once mention volume justifies the latency.
type askIntent string

const (
	intentAsk        askIntent = "ask"
	intentStatus     askIntent = "status"
	intentMetrics    askIntent = "metrics"
	intentContribute askIntent = "contribute"
	intentFeedback   askIntent = "feedback"
)

var (
	statusRe     = regexp.MustCompile(`(?i)^(status|health)\??$|^are you (working|there|up|on|alive)\??$`)
	contributeRe = regexp.MustCompile(`(?i)^(remember|capture|save|record) (this|that|what we)`)
	feedbackRe   = regexp.MustCompile(`(?i)^(that |this |it )?(helped|worked|was (helpful|useful)|didn'?t (help|work)|not relevant)`)
	// metricsRe recognises a metrics ASK — anchored so a real question that only
	// mentions "adoption" mid-sentence ("a validated way to improve adoption?")
	// still routes to ask. Only an opening metrics phrasing counts.
	metricsRe = regexp.MustCompile(`(?i)^(metrics|stats|outcomes|the numbers|dashboard|funnel|how (are|'?re) we doing|how'?s (it|tacit) (going|doing)|how is tacit doing)\b`)
)

func routeIntent(question string) askIntent {
	q := strings.TrimSpace(question)
	switch {
	case statusRe.MatchString(q):
		return intentStatus
	case metricsRe.MatchString(q):
		return intentMetrics
	case contributeRe.MatchString(q):
		return intentContribute
	case feedbackRe.MatchString(q):
		return intentFeedback
	default:
		return intentAsk
	}
}

// asker is the optional LLM capability the advisor answer needs; the
// heuristic path runs when the model can't (or won't) answer.
type asker interface {
	Ask(question string, evidence contracts.EvidenceBlock) (string, error)
}

// mentionResponse runs the full advisor turn for one mention. Called WITHOUT
// a.mu held (the caller snapshots session state under the lock first); the
// pipeline is budgeted so the harness never waits past AskBudget. The bool
// reports whether a response applies (false = treat the prompt as
// mention-free).
func (a *Agent) mentionResponse(st *sessionState, harness, prompt string,
	record contracts.CanonicalRecord, hashKey string, lastShown []shownTechnique) (HookResponse, bool) {

	question, pure, found := parseMention(prompt)
	if !found {
		return nil, false
	}
	answer, technique := a.advisorAnswer(st, question, record, hashKey, lastShown)
	block := a.advisorBlock(answer, technique)

	if pure && a.opts.MentionBlock && mentionBlockCapable(harness) {
		// Opt-in, terminal-only mode: answer as the addressee; the model
		// never runs. OFF by default since the A0 field result (2026-07-16):
		// the CLI renders a block reason, but Claude Code's web/mobile
		// clients render NO hook output at all — and the agent cannot tell
		// which client is attached. Members who live in a terminal can turn
		// it on (TACIT_HOOKS_MENTION_BLOCK=1) for the token-free answer.
		return HookResponse{"decision": "block", "reason": block}, true
	}

	// Default shape: the MODEL is the renderer. systemMessage carries the
	// spine block for clients that render hook output (the CLI shows it
	// immediately); additionalContext makes the model relay the same answer
	// verbatim, because model output is the ONE channel every client renders.
	// A terminal member sees the answer twice on a mention — the price of
	// never losing it on web/mobile, and it reads as "the advisor spoke, the
	// model carried it".
	relay := relayText(answer, technique)
	note := "(" + product.Name() + ") The member's @tacit mention was answered by " + product.Name() + ", the org's playbook advisor. " +
		"Begin your response with the following answer, quoted VERBATIM under its \"" + Mark() + "\" heading — do not paraphrase it, " +
		"shorten it, or re-answer the mention — then continue with the member's actual request:\n\n" + relay
	if pure {
		note = "(" + product.Name() + ") This prompt was addressed entirely to " + product.Name() + ", the org's playbook advisor, which answered it. " +
			"Relay the following answer to the member VERBATIM (keep the \"" + Mark() + "\" heading). Add nothing else:\n\n" + relay
	}
	return HookResponse{
		"systemMessage": block,
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": note,
		},
	}, true
}

// relayText is the advisor answer shaped for the model to repeat: same
// content as the spine block, without the spine (the model's output is
// markdown-rendered, where gutter glyphs on every line read as noise).
func relayText(answer string, technique contracts.EvidenceCandidate) string {
	answer = mdBoldRe.ReplaceAllString(answer, "$1")
	out := Mark() + "\n" + strings.TrimSpace(answer)
	if technique.TechniqueID != "" {
		out += "\n(reply \"helped\" / \"not relevant\", or just try it · " + technique.TechniqueID + ")"
	}
	return out
}

// advisorAnswer routes intent and, for real questions, runs retrieve →
// answer within AskBudget. It NEVER returns silence: the member addressed
// OpenTacit, so a miss, a timeout, or a dead registry each get said plainly.
// Returns the answer text and the primary technique (zero-value when none).
func (a *Agent) advisorAnswer(st *sessionState, question string,
	record contracts.CanonicalRecord, hashKey string, lastShown []shownTechnique) (string, contracts.EvidenceCandidate) {

	var none contracts.EvidenceCandidate
	switch routeIntent(question) {
	case intentStatus:
		return a.statusAnswer(), none
	case intentMetrics:
		return a.metricsAnswer(), none
	case intentContribute:
		return "Happy to. Run /tacit:contribute (or the contribute skill) and I'll draft the technique from this session — it lands in the review queue, nothing goes live unreviewed.", none
	case intentFeedback:
		// The ambient reaction detector already read this prompt (it runs
		// before mention handling), so the verdict is recorded — acknowledge
		// rather than double-record.
		if len(lastShown) > 0 {
			return "Noted — recorded against \"" + lastShown[0].name + "\". Thanks; that's the signal the evidence lines rest on.", none
		}
		return "Noted — though nothing was suggested recently in this session to attach it to. Use /tacit:feedback to pick a specific technique.", none
	}

	type result struct {
		answer    string
		technique contracts.EvidenceCandidate
		ok        bool
	}
	done := make(chan result, 1)
	a.opts.RunAsync(func() {
		auditID := audit.NewAuditID()
		char := capture.Characterize(record)
		char.AuditID = auditID
		char.SessionHash = sessionhash.Hash(a.opts.SessionSalt, hashKey)
		char.UsedTechniqueIDs = a.memory.UsedIDs(time.Now())
		// The mention names intent; the session names context. Both beat
		// either alone, so retrieval sees the question first, then a capped
		// slice of what the session was about.
		q := question
		if ctx := strings.Join(strings.Fields(char.SummaryText), " "); ctx != "" {
			if r := []rune(ctx); len(r) > 200 {
				ctx = string(r[:200])
			}
			q = question + " — session context: " + ctx
		}
		char.SummaryText = q
		evidence, err := a.evidence(char)
		a.noteRegistry(err)
		if err != nil {
			done <- result{answer: "I can't reach the registry right now (" + registryFaultHint(err) + "). Try again in a moment, or run `tacit doctor`.", ok: true}
			return
		}
		var answerer asker
		if ak, ok := a.llm.(asker); ok {
			answerer = ak
		}
		text, aerr := "", error(nil)
		if answerer != nil {
			text, aerr = answerer.Ask(question, evidence)
			a.noteLLM(aerr)
		}
		if answerer == nil || aerr != nil || strings.TrimSpace(text) == "" {
			text = heuristicAskAnswer(evidence)
		}
		r := result{answer: strings.TrimSpace(text), ok: true}
		// Attribute only what the answer actually ENDORSED. The answerer
		// judges fit; when it declares a miss (the instructed "No validated
		// org move…" opener — nearest techniques may still be LISTED, but none
		// fits) the top candidate gets a `declined` event, the same
		// retrieval-quality telemetry the ambient fit-check emits, and no
		// footer, slate entry, or shown — an advisor that says "nothing
		// fits" while the funnel records "shown" is manufacturing evidence.
		// Otherwise the citation test applies: the primary technique's name in
		// the answer means it was the answer.
		miss := missRe.MatchString(r.answer)
		if miss && len(evidence.Candidates) > 0 {
			a.postFeedback([]contracts.FeedbackEventDraft{{
				AuditID: auditID, TechniqueID: evidence.Candidates[0].TechniqueID,
				Stage: "declined", Segment: char.Segment, TaskType: char.TaskType,
				RankShown: 1, Confidence: "inferred",
				Source: contracts.SourcePull, Similarity: evidence.Candidates[0].Similarity,
			}})
		}
		if !miss && len(evidence.Candidates) > 0 &&
			strings.Contains(strings.ToLower(r.answer), strings.ToLower(strings.TrimSpace(evidence.Candidates[0].Name))) {
			r.technique = evidence.Candidates[0]
			// The cited technique enters the funnel and the session slate: shown
			// at EXPLICIT confidence — the member pulled — and the existing
			// ambient reaction/adoption watchers take it from here.
			a.mu.Lock()
			st.lastAuditID = auditID
			st.lastSegment = char.Segment
			st.lastShown = []shownTechnique{{r.technique.TechniqueID, r.technique.Name, r.technique.Recipe}}
			st.shownIDs[r.technique.TechniqueID] = true
			st.noteSuggestionLocked(r.technique, a.opts.Now())
			a.mu.Unlock()
			a.postFeedback([]contracts.FeedbackEventDraft{{
				AuditID: auditID, TechniqueID: r.technique.TechniqueID,
				Stage: "shown", Segment: char.Segment, TaskType: char.TaskType,
				RankShown: 1, Confidence: "explicit",
				Source: contracts.SourcePull, Similarity: r.technique.Similarity,
			}})
		}
		done <- r
	})

	select {
	case r := <-done:
		return r.answer, r.technique
	case <-time.After(a.opts.AskBudget):
		return "Tacit timed out answering (registry or model too slow). Try again, or run `tacit doctor` if this keeps happening.", none
	}
}

// registryFaultHint keeps the member-facing failure line short.
func registryFaultHint(err error) string {
	var httpErr interface{ HTTPStatus() int }
	if errors.As(err, &httpErr) {
		if code := httpErr.HTTPStatus(); code == 401 || code == 403 {
			return "key rejected — it may have been rotated"
		}
		return "registry error"
	}
	return "unreachable"
}

// heuristicAskAnswer is the no-LLM fallback: honest, nearest-techniques-only.
func heuristicAskAnswer(evidence contracts.EvidenceBlock) string {
	if len(evidence.Candidates) == 0 {
		return "No validated org move for this yet — the registry has nothing close. (This miss is itself a signal; recurring ones become suggested techniques.)"
	}
	var b strings.Builder
	b.WriteString("Nearest techniques from your org's playbook (no fit-check available):")
	for i, c := range evidence.Candidates {
		if i >= 3 {
			break
		}
		b.WriteString("\n- " + c.Name)
		if ev := evidenceLine(c.Outcomes); ev != "" {
			b.WriteString(" (" + ev + ")")
		}
	}
	return b.String()
}

// statusAnswer is the "@tacit are you working?" one-liner.
func (a *Agent) statusAnswer() string {
	totals, _ := a.StatsFor("")
	regState, _, _, regOK := a.RegistryHealth()
	llmState, _, _, _ := a.LLMHealth()
	line := fmt.Sprintf("Listening. This agent: %d shown / %d adopted across live sessions · registry %s",
		totals.Shown, totals.Adopted, regState)
	if regState == "ok" && !regOK.IsZero() {
		line += " (last contact " + regOK.UTC().Format("15:04Z") + ")"
	}
	return line + fmt.Sprintf(" · model %s. Details: /tacit:status or `tacit doctor`.", llmState.State)
}

// metricsAnswer handles "@tacit how are we doing?" — the metrics intent, the
// dispatch that makes @tacit a front door to the numbers, not only to search.
// The hook agent holds only THIS machine's local funnel (what it has shown and
// seen adopted in live sessions); the org-wide funnel, cohorts and map are the
// dashboard's and tacit_metrics' job. So it reports what it honestly knows —
// aggregate, no identities — and points at the fuller view rather than
// inventing org numbers it does not have.
func (a *Agent) metricsAnswer() string {
	totals, _ := a.StatsFor("")
	return fmt.Sprintf(
		"On this machine I've shown %d suggestion(s) and seen %d adopted across live sessions. "+
			"For the org-wide picture — the shown→adopted→helped funnel, adoption by cohort, and the "+
			"playbook map — call tacit_metrics (view=funnel, cohorts, or map) or open the Outcomes dashboard.",
		totals.Shown, totals.Adopted)
}

// mdBoldRe strips **emphasis** the model emits despite instructions — the
// spine block is plain text, and enforcement beats hoping the prompt holds.
var mdBoldRe = regexp.MustCompile(`\*\*([^*]+)\*\*`)

// missRe recognizes the instructed miss opener (askSystem requires it lead
// the answer when no technique fits).
var missRe = regexp.MustCompile(`(?i)^no validated org move`)

// advisorBlock renders the answer in OpenTacit's visual voice — same spine the
// coaching nudge uses, but headed as an ANSWER, because it is one.
func (a *Agent) advisorBlock(answer string, technique contracts.EvidenceCandidate) string {
	answer = mdBoldRe.ReplaceAllString(answer, "$1")
	var lines []string
	lines = append(lines, Mark())
	for _, ln := range strings.Split(answer, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		lines = appendWrapped(lines, "", strings.TrimRight(ln, " "))
	}
	if technique.TechniqueID != "" {
		lines = append(lines, `reply "helped" / "not relevant", or just try it · `+technique.TechniqueID)
	}
	var b strings.Builder
	b.WriteString("\n")
	for i, ln := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(tacitSpine)
		b.WriteString(ln)
	}
	return b.String()
}

// advisorMarkdown renders the answer for the relay path: clients that render
// no hook output (Claude Code web and mobile) only ever see a suggestion if the
// model reproduces it in its own reply. The ┃ spine cannot travel there — it is
// a terminal affordance, and its fixed-width pre-wrapping gets clipped inside a
// phone-width code block rather than reflowed. A blockquote is the same visual
// unit in markdown, and the client wraps it to whatever width it has.
func (a *Agent) advisorMarkdown(answer string, technique contracts.EvidenceCandidate) string {
	answer = mdBoldRe.ReplaceAllString(answer, "$1")
	var b strings.Builder
	b.WriteString("> " + MarkBold())
	for _, ln := range strings.Split(answer, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		b.WriteString("\n>\n> ")
		b.WriteString(strings.TrimSpace(ln))
	}
	if technique.TechniqueID != "" {
		b.WriteString("\n>\n> reply \"helped\" / \"not relevant\", or just try it · `")
		b.WriteString(technique.TechniqueID)
		b.WriteString("`")
	}
	return b.String()
}

// HandleAsk is the HTTP seam for the same engine (POST /v1/hooks/ask) — the
// consolidation point advisor-mention-plan.md A1 names: skills and other
// pull surfaces route here so ranking, funnel recording, and voice stay one
// thing. Body: {session_id, harness, text}.
func (a *Agent) HandleAsk(body map[string]any) (bool, map[string]any) {
	text, _ := body["text"].(string)
	if strings.TrimSpace(text) == "" {
		return false, map[string]any{"error": "text required"}
	}
	harness, _ := body["harness"].(string)
	if harness == "" {
		harness = "claude-code"
	}
	sid, _ := body["session_id"].(string)
	if sid == "" {
		sid = "ask"
	}
	a.Touch()
	key := harness + ":" + sid

	a.mu.Lock()
	st := a.sessions[key]
	if st == nil {
		st = newSessionState(capture.NewClaudeCodeCapture(a.opts.Segment))
		st.key, st.hashKey = key, key
		a.sessions[key] = st
	}
	record := st.capture.Record()
	hashKey := st.hashKey
	lastShown := append([]shownTechnique(nil), st.lastShown...)
	a.mu.Unlock()

	answer, technique := a.advisorAnswer(st, text, record, hashKey, lastShown)
	// "block" is the answer already rendered in OpenTacit's visual voice. Pull
	// surfaces that reach a client rendering no hook output (Claude Code web
	// and mobile) relay THIS verbatim: the ◆ spine has to come from the agent,
	// because a model drawing it from the prose would be forging the one thing
	// the member is meant to recognize as not-model-authored.
	resp := map[string]any{
		"answer":   answer,
		"block":    a.advisorBlock(answer, technique),
		"markdown": a.advisorMarkdown(answer, technique),
		"intent":   string(routeIntent(text)),
	}
	if technique.TechniqueID != "" {
		resp["technique_id"] = technique.TechniqueID
	}
	return true, resp
}
