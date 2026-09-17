// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The delivery self-test: proof, on demand, that an OpenTacit suggestion reaches
// the member's eyes in THIS session.
//
// `tacit doctor --harness X` checks four hops — wiring, relay, agent, registry
// — and stops one short of the only one the member actually experiences: does
// the block RENDER? A harness that quietly drops hook output, a client (Claude
// Code on web or mobile) that renders none, a plugin installed at the wrong
// scope: all four hops pass and the member still never sees a suggestion.
//
// So arm the real path and fire it. `tacit hook-selftest` (the /tacit:testfeedback
// command) arms the agent; the next Stop hook of any live session delivers a
// block through the same envelope a real suggestion uses, on the same channel
// this harness would use. Nothing is retrieved, nothing is recorded: the block
// is labelled a self-test and carries no technique, so the funnel never sees it.

package hooks

import (
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/product"
)

// selfTestTTL bounds an armed self-test. Long enough for a member to arm it and
// finish the turn, short enough that a forgotten arming does not surprise them
// an hour later.
const selfTestTTL = 10 * time.Minute

// selfTestState is the one-shot arming, plus what became of the last one — the
// diagnostic split that matters when nothing appeared: the agent delivered and
// the member's client swallowed it, or the Stop hook never arrived at all.
type selfTestState struct {
	armed   bool
	expires time.Time
	harness string // "" = the next Stop on any harness
	mode    string // ModeBlock (member-facing) | ModeContext (model-facing)

	lastAt      time.Time
	lastHarness string
	lastOutcome string // delivered | parked | suppressed | expired | context-delivered
}

// The two channels a self-test can probe. They fail independently and prove
// different things, so they are armed separately.
const (
	// ModeBlock fires the member-facing ◆ block at the next Stop: does a
	// suggestion REACH the member's eyes on this client?
	ModeBlock = "block"
	// ModeContext runs the whole tier-2 loop once, as a self-test: a
	// model-facing additionalContext at the next UserPromptSubmit asks the
	// model to raise one AskUserQuestion, and the member sees whether a
	// suggestion can be PRESENTED as a native form.
	//
	// This is the delivery tier 2 described in docs/harness/in-harness-hooks.md.
	// The form itself validated well; what
	// killed it was the only channel that can reach the model, because hooks
	// cannot call tools — Claude Code began rendering hook context as a
	// visible, sticky "tacit-suggestions" chip, so a directive designed to be
	// unseen was being read. Both halves need one observation nobody has made
	// since: does the form arrive, and does the directive that asked for it
	// stay out of sight.
	ModeContext = "context"
)

// ContextProbeText is what ModeContext sends: the tier-2 directive, reduced to
// a self-test.
//
// It has to satisfy two things that pull against each other. It must be
// harmless if seen — the member may be reading it, which is half the finding —
// so it says what it is and asks for nothing on any real work. And it must
// exercise the REAL path, or it proves nothing: the header is the exact
// constant the PostToolUse parser matches (questionHeader), and the options are
// the funnel's own set, so a form raised here travels the same route a live one
// would.
//
// The placeholders are deliberate and match the block self-test's. A model that
// invented a technique name and an evidence line would be forging the one
// thing no model may author — and the form would then prove that OpenTacit can
// be imitated, not that it can be delivered.
//
// Nothing is recorded: captureQuestionAnswerLocked returns early when the
// session has no shown technique, and a self-test never shows one, so the answer
// reaches the funnel's door and stops there.
var ContextProbeText = "Tacit delivery self-test — the member armed this and is waiting to see it.\n\n" +
	SelfTestFormSpec() +
	"\nRaise the form once, then answer the member's prompt normally."

// SelfTestFormSpec is the form, defined ONCE, in the binary.
//
// Two callers render it and neither may compose it: the hook directive
// (ContextProbeText) and the /tacit:testfeedback skill, which prints this spec
// and tells the model to carry it. The same discipline as SelfTestRelayBlock —
// a model writing its own version would prove OpenTacit can be imitated, which
// is the one thing the test exists to rule out.
//
// The header and the four labels here are the live constants (questionHeader,
// optApply, ...), never copies of them: the skill's form and the parser that
// matches its answers must agree byte-for-byte, so a rename moves both ends
// together or fails a test.
func SelfTestFormSpec() string {
	return "Raise exactly ONE AskUserQuestion, with these fields verbatim:\n\n" +
		"  header:   " + questionHeader + "\n" +
		"  question: " + product.Name() + " self-test — a real suggestion appears here, with its name,\n" +
		"            its recipe, and the outcomes colleagues measured. Apply it?\n" +
		"  options:\n" +
		"    " + optApply + " — a live technique's recipe would be applied to the work in hand\n" +
		"    " + optShowHow + " — explain the move and its evidence first\n" +
		"    " + optNotRelevant + " — records a dismissal\n" +
		"    " + optAlreadyUse + " — records a dismissal of a different kind\n\n" +
		"This is the SHAPE of a suggestion, not one: no technique was retrieved and no outcome is\n" +
		"recorded. Do not invent a technique name, a recipe or a measured figure — the wording\n" +
		"above is the whole content, and inventing one would forge the only thing a model may\n" +
		"never author.\n"
}

// ArmSelfTest arms the next delivery of the given mode. harness "" takes
// whichever session gets there first. Returns the expiry.
func (a *Agent) ArmSelfTest(harness, mode string) time.Time {
	if mode != ModeContext {
		mode = ModeBlock
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.selfTest.armed = true
	a.selfTest.harness = harness
	a.selfTest.mode = mode
	a.selfTest.expires = a.opts.Now().Add(selfTestTTL)
	return a.selfTest.expires
}

// takeContextProbeLocked consumes a ModeContext arming at UserPromptSubmit.
// Caller holds a.mu.
func (a *Agent) takeContextProbeLocked(harness string) bool {
	return a.takeSelfTestLocked(harness, ModeContext)
}

// takeSelfTestLocked consumes the arming if this event is the one it was armed
// for — the right harness AND the right channel. The mode check is what keeps
// the two probes from answering each other's question: a context arming must
// not fire the member-facing block at Stop, and a block arming must not put
// anything on the model-facing channel. Caller holds a.mu.
func (a *Agent) takeSelfTestLocked(harness, mode string) bool {
	s := &a.selfTest
	if !s.armed {
		return false
	}
	if s.mode != mode {
		return false
	}
	if a.opts.Now().After(s.expires) {
		s.armed = false
		s.lastAt, s.lastOutcome, s.lastHarness = a.opts.Now(), "expired", ""
		return false
	}
	if s.harness != "" && s.harness != harness {
		return false
	}
	s.armed = false
	return true
}

// noteSelfTestLocked records how the armed block ended up. Caller holds a.mu.
func (a *Agent) noteSelfTestLocked(harness, outcome string) {
	a.selfTest.lastAt = a.opts.Now()
	a.selfTest.lastHarness = harness
	a.selfTest.lastOutcome = outcome
}

// noteProbeFormLocked advances a context probe's record as the form it asked
// for actually happens: offered when the model raises it, answered when the
// member picks. This is the machine's half of the reading — the member's eyes
// say the form RENDERED, and these say it travelled the live capture route
// (PreToolUse/PostToolUse keyed on questionHeader) rather than merely appearing
// on screen. A form that renders but never reaches the funnel would look
// identical to the member and be useless.
//
// Only ever upgrades a context probe's own record, so an unrelated OpenTacit
// form later in the day cannot rewrite it. Caller holds a.mu.
func (a *Agent) noteProbeFormLocked(harness, outcome string) {
	switch a.selfTest.lastOutcome {
	case "context-delivered", "context-form-offered":
		a.noteSelfTestLocked(harness, outcome)
	}
}

// SelfTestStatus is the /v1/hooks/stats view: armed now, and what the last
// armed test did.
func (a *Agent) SelfTestStatus() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.selfTest
	out := map[string]any{"armed": s.armed && !a.opts.Now().After(s.expires)}
	if s.armed {
		out["expires_at"] = s.expires.UTC().Format(time.RFC3339)
		out["mode"] = s.mode
		if s.harness != "" {
			out["harness"] = s.harness
		}
	}
	if s.lastOutcome != "" {
		last := map[string]any{"outcome": s.lastOutcome,
			"at": s.lastAt.UTC().Format(time.RFC3339)}
		if s.lastHarness != "" {
			last["harness"] = s.lastHarness
		}
		out["last"] = last
	}
	return out
}

// selfTestChannel describes, for the block's own closing line, the route it
// took on this harness. The three delivery tiers (same-turn, park-only,
// deliverless) are the agent's, so the block states which one it rode rather
// than claiming a path it did not take.
func selfTestChannel(harness string) string {
	if parkOnly(harness) {
		return "parked at the turn's end and delivered with this prompt"
	}
	return "delivered by the Stop hook at the turn's end"
}

// SelfTestRelayBlock renders the self-test block for the relay path: where the
// member's client renders no hook output, the model's own reply is the only
// channel left, so `tacit hook-selftest` prints this for the model to reproduce
// verbatim. The agent still authors it — that is the point. A model composing a
// block from a description would prove nothing, because an imitation is exactly
// what the test exists to rule out.
//
// Two deliberate differences from formatSelfTest. It is a markdown blockquote
// rather than the ┃ spine: the spine's fixed-width pre-wrapping is clipped by a
// narrow client instead of reflowed, and the quote's rule is its equivalent in
// markdown. And the closing line says it was relayed — a member must be able to
// tell a block the hook delivered from one the model carried, because only the
// first is evidence that push works here.
func SelfTestRelayBlock(harness string) string {
	if harness == "" {
		harness = "claude-code"
	}
	fields := []string{
		"{the name of a practice your colleagues measured}",
		"why: {one line on why that practice fits the work you just did}",
		"try: {the technique's recipe, verbatim}",
		"measured by colleagues: {helped % · adopted % · n · cohort}",
		"Relayed through the model's reply, because this " + harness + " client renders " +
			"no hook output — the hook's own copy went to the terminal instead. You asked " +
			"for this block, so nothing was retrieved and nothing was recorded.",
	}
	b := strings.Builder{}
	b.WriteString("> ◆ **" + product.Name() + ": delivery self-test — a real suggestion looks like this**")
	for _, f := range fields {
		// A blank quoted line between fields: consecutive lines inside a
		// blockquote collapse into one paragraph, which would run the fields
		// together. Each field stays whole so the CLIENT picks the wrap width.
		b.WriteString("\n>\n> ")
		b.WriteString(f)
	}
	return b.String()
}

// formatSelfTest renders the block. It carries the same spine, the same field
// order and the same wrapping as a real suggestion (format, agent.go), so what
// the member sees here is what they will see when a technique matches — with the
// technique's own text replaced by a description of it. No numbers are invented:
// the evidence line names the figures a real technique puts there.
func formatSelfTest(harness string) string {
	var lines []string
	lines = append(lines, Mark()+": delivery self-test — a real suggestion looks like this")
	lines = append(lines, "<the name of a practice your colleagues measured>")
	lines = appendWrapped(lines, "why: ", "<one line on why that practice fits the work you just did>")
	lines = appendWrapped(lines, "try: ", "<the technique's recipe, verbatim>")
	lines = append(lines, "measured by colleagues: <helped % · adopted % · n · cohort>")
	lines = appendWrapped(lines, "", "You asked for this block, so nothing was retrieved and nothing "+
		"was recorded. It took the real path — hook → local agent → "+harness+", "+
		selfTestChannel(harness)+". Seeing it means suggestions will reach you here.")

	b := strings.Builder{}
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
