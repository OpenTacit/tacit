// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"html"
	"strconv"
	"strings"
)

// The project page's three session frames: what a member's terminal held at
// each moment of the flow section (site.go), as text rather than as a
// photograph of it.
//
// They began as PNGs captured from the terminal that ran the session, on the
// argument that a hand-built imitation of another program's chrome would drift
// the first time that program changed. It did buy that, and it cost more than
// it bought: every wording fix — a caption, a column, a figure the demo org
// re-rolled — meant re-running a whole scripted session and re-rendering
// images, so in practice nothing was ever fixed. The frames are text now, and
// editing one is editing a string.
//
// What is load-bearing here is not the chrome, which any harness may restyle
// tomorrow. It is the evidence line, and that is MEASURED, not written. Each
// scenario's is what a registry loaded with that demonstration org actually
// answers /v1/evidence with, for a member in that cohort:
//
//	ai      forgeflow-template-launch    helped 94% · adopted 54% · n=41 · team:pretraining
//	bank    payrail-idempotency-check    helped 90% · adopted 43% · n=40
//	telco   provisionguard-preflight     helped 76% · adopted 35% · n=48
//
// (Two carry no cohort because the registry reports them as __overall__, and
// contracts.EvidenceLine drops the segment when it is that. Do not add one.)
//
// Each technique's name and its applies_when line are that technique's own text.
// hack/sitestills/README.md is how to re-read all of it; change a technique, a
// cohort or a dataset and those numbers must be read again, never adjusted to
// taste. A rate typed into these strings that no registry produced is the one
// thing the house rules forbid.
//
// What the figures do NOT say is that the registry picked this technique for this
// turn. Candidate ranking is by measured impact within the member's cohort —
// RelevanceK keeps 20 of ~30 techniques and MinSimilarity is 0, so at demo-corpus
// size the turn's text barely moves the order, and the same three come back for
// an unrelated prompt. Choosing which candidate FITS the turn is the hook
// agent's fit-check, and in these frames that choice is the composition's.
//
// Around the evidence line the frames are staged, as the captured stills before
// them were: the member's prompts, the agent's prose and the output of internal
// tools that exist only in the demonstration orgs are all composed. That is what
// the section's caption means by "that staged org's" — and why the caption
// claims an interaction, not a recording.
//
// One more thing is not fixed text: the suggestion block's «PRODUCT» heading,
// which takes the configured name like the rest of the page (siteProductText).
// It is the product naming itself inside a picture of the product, and a page
// that called it two things would read as a mistake before it read as
// provenance. Nothing else moves — not a word of the session, not a figure.
//
// Worth knowing while reading that: the heading the member-side hook agent
// actually prints is still a constant in internal/auditor/hooks. That agent runs
// on the member's machine and has no way to hear a registry's PRODUCT_NAME, so
// under a renamed deployment these frames are ahead of what a real session
// shows until the name reaches that surface too.
//
// The frames are held to the terminal they came from: at most termCols columns
// and termRows rows, which TestSiteFramesFitTheTerminal enforces. That is not
// housekeeping. The type is sized from the window's width so 96 columns always
// fit exactly, and a line that runs long would wrap on the page where the
// terminal did not wrap it. termRows is the window's own height — a moment
// taller than it could never be read whole, because the stage scrolls the tape
// to that moment's first line and stops. Both numbers are also the window's
// aspect ratio in app.css; move one, move both.
const (
	termCols = 96 // the terminal's width; the type is sized so exactly this fits
	termRows = 24 // the window's height, and the tallest any one moment may be
)

// The frame markup: {x …} paints its content with one of the terminal's inks,
// and everything else is literal. Deliberately the smallest thing that can
// reproduce a terminal — there is no nesting, no attributes and no way to
// introduce a colour that app.css has not already named.
//
//	{d …}  dim      the harness's own asides: file counts, tool output, hints
//	{b …}  bright   what the harness emphasises: a tool name, a suggestion's body
//	{g …}  green    a completed tool call's bullet
//	{c …}  chip     the reversed label a native form draws for its source
//	{u …}  you      the band the harness paints behind the member's own prompt
//
// A brace that is not followed by one of those letters and a space is text, so
// prose does not have to be escaped.
//
// The band is the one ink with a shape as well as a colour. A harness paints it
// behind the WHOLE prompt, so a prompt that wrapped onto a second line carries the
// band on both, padded with spaces to one right edge — a rectangle, the way a
// terminal fills a highlighted row to the wrap column. Written the obvious way,
// with only the first line marked, it renders as a band that stops mid-sentence
// and a second line hanging outside it, which is how this looked until somebody
// noticed. TestPromptBandIsARectangle holds both halves of that.
var termInk = map[byte]string{
	'd': "term-dim",
	'b': "term-lit",
	'g': "term-ok",
	'c': "term-chip",
	'u': "term-you",
}

// What the member said and what the agent answered, lifted out for the same
// reason the offer's content was: the hero draws this exchange too, and the
// suggestion makes no sense without it. A technique that says "launch from the
// matching template, not a hand-rolled config" is an answer to a question the
// reader has to have seen asked — somebody copied last month's file whole and is
// about to submit it.
//
// workAskLines keeps the terminal's own two lines rather than one sentence,
// padding and all. A harness paints the band behind the WHOLE prompt, so both
// lines carry it out to one right edge; re-wrapping a sentence here would mean
// this file deciding where a terminal broke a line, which is the one thing these
// frames are not allowed to invent. The hero joins them back into prose.
const (
	workCopied = "Copied runs/pretrain-70b-v2.yaml → runs/pretrain-70b-v3.yaml with your two changes"
	workReady  = "Everything else carries over from v2 unchanged. The file is ready to submit."
	offerAsk   = "Good. Submit it to the cluster."
)

var workAskLines = [2]string{
	"Copy the config from last month's 70b-v2 run and set it up for v3. Use the same cluster, ",
	"the data mix to the October blend and give it a longer warmup. Just work from the file.  ",
}

// workAsk is those two lines as the sentence they were before a terminal wrapped
// them: the trailing pad goes, and the join is a single space.
func workAsk() string {
	return strings.TrimSpace(workAskLines[0]) + " " + strings.TrimSpace(workAskLines[1])
}

// labWork is moment one: a member has the agent copy last month's run config
// and edit it for the next run — ordinary, correct-looking work, in which one
// field quietly carries over that should not have. Nothing in the exchange
// mentions Tacit.
var labWork = `{d ❯} {u ` + workAskLines[0] + `}
  {u ` + workAskLines[1] + `}

  {d Read }{b 1}{d  file (ctrl+o to expand)}

● ` + workCopied + `:

  ┌──────────────────┬─────────────────────┬─────────────────────┐
  │      Field       │        70b-v2       │        70b-v3       │
  ├──────────────────┼─────────────────────┼─────────────────────┤
  │ data.mix         │ blend-2025-08       │ blend-2025-10       │
  ├──────────────────┼─────────────────────┼─────────────────────┤
  │ schedule.warmup  │ 2000 steps          │ 6000 steps          │
  ├──────────────────┼─────────────────────┼─────────────────────┤
  │ cluster.topology │ 64 × h100 / 8 nodes │ 64 × h100 / 8 nodes │
  ├──────────────────┼─────────────────────┼─────────────────────┤
  │ checkpoint.every │ 4000 steps          │ 4000 steps          │
  └──────────────────┴─────────────────────┴─────────────────────┘

  ` + workReady + `

{d ✳ Completed in 12s}`

// The offer's content, lifted out of the frame because the page now draws this
// one suggestion twice, in two shapes. The flow section draws the terminal's
// rendering of it: a chip, three lines and a numbered list a keystroke apart.
// The hero draws the other one — the native form a desktop client puts on
// screen, which is what the harness actually asks for (internal/auditor/hooks,
// formatForm) and the only channel that reaches every surface.
//
// They are two pictures of one moment, so they read from one text. The evidence
// line is the reason that matters: it is MEASURED, it is what a registry loaded
// with this demonstration org answers /v1/evidence with, and two copies of a
// figure are two chances for one of them to quietly stop being true.
//
// offerName is the technique's own name and offerFit its applies_when line, both
// that technique's text. offerEvidence is read, never adjusted — the note at the
// head of this file says how to read it again.
const (
	offerName     = "Launch your training run from the matching Forgeflow template, not a hand-rolled config."
	offerEvidence = "Measured by colleagues: helped 94% · adopted 54% · n=41 · team:pretraining"
	offerFit      = "Starting a new training run of meaningful scale (multi-node, >100 GPU-hours)."
)

// offerAnswers are the four replies, in the order both renderings show them.
// They are the harness's own labels (optApply, optShowHow, optNotRelevant,
// optAlreadyUse in internal/auditor/hooks/delivery.go); a member's answer is
// matched against those strings, so a label reworded on this page and nowhere
// else would be a picture of a form that does not exist.
var offerAnswers = [4]struct{ Label, Hint string }{
	{"Apply it now", "apply this to the work in hand"},
	{"Show me how", "explain the technique and its evidence first"},
	{"Not relevant here", "it does not fit here"},
	{"Already use it", "already known"},
}

// termOfferList is the terminal's rendering of those four: a numbered line and a
// dim hint under it, with the prompt caret on the first. The indents are the
// harness's and are why this is built rather than written — a hint that lost a
// space would be invisible in a diff and crooked on the front page.
func termOfferList() string {
	var b strings.Builder
	for i, a := range offerAnswers {
		lead := "  "
		if i == 0 {
			lead = "{d \u276f} "
		}
		b.WriteString(lead + "{d " + strconv.Itoa(i+1) + ".} " + a.Label + "\n")
		b.WriteString("     {d " + a.Hint + "}\n")
	}
	return b.String()
}

// labOffer is moment two: the member asks to submit the job, which is the
// technique's own trigger, and Claude Code draws the suggestion as its native form —
// the evidence line, and four answers a keystroke apart.
var labOffer = `{d ❯} {u ` + offerAsk + `}
{d ──────────────────────────────────────────────────────────────────────────────────────────────}
{c  ◆ «PRODUCT» }

{b ` + offerName + `}
{b ` + offerEvidence + `}
{b ` + offerFit + `}

` + termOfferList() + `  {d 5. Type something.}
{d ──────────────────────────────────────────────────────────────────────────────────────────────}
  {d 6.} Chat about this

{d Enter to select · ↑/↓ to navigate · Esc to cancel}`

// labAdopt is moment three: "Apply it now" fetches the technique, the agent
// launches from the validated template instead, and the field frame one carried
// over is replaced in front of the member. The adoption is recorded by the
// answering — nobody filled in a survey.
var labAdopt = `● User selected an answer:
  {d ⎿  · ` + offerName + `}
     {d ` + offerEvidence + `}
     {d ` + offerFit + `}
     {d → Apply it now}

  {d Called plugin:tacit:tacit (ctrl+o to expand)}

{g ●} {b Bash}(forgeflow templates --family pretrain-70b)
  {d ⎿  pretrain-70b-v3   validated   ckpt 1500   restart auto-resume   64 × h100 / 8 nodes}

{g ●} {b Bash}(forgeflow launch --template pretrain-70b-v3 --overrides runs/70b-v3.overrides.yaml)
  {d ⎿  overrides accepted: data.mix, schedule.warmup}
     {d inherited from the template:}
     {d   checkpoint.every   1500 steps    (your file carried 4000 over from v2)}
     {d   restart.policy     auto-resume   (your file carried none)}
     {d submitted ff-2411 · 64 × h100 / 8 nodes · first checkpoint at step 1500}

● Launched from the template with your two changes as overrides. The checkpoint interval the
  copied file carried over from v2 would have left 4000 steps unprotected on a restart.

{d ✳ Completed in 24s}`

// ---- Techniqueinal Point Financial (financial-institution), team:payments-platform ----
// A positive observability change for payment retries. The technique confirms
// that the key is safe across attempts, then enriches the new trace data with
// charge scope and adds a recovered-timeout view for operations.

const bankWork = `{d ❯} {u settle_charge() already recovers from gateway timeouts, but operations only see the final   }
  {u result. Add trace data for each attempt and a counter for retries that recover.             }

  {d Read }{b 1}{d  file (ctrl+o to expand)}

● Added retry telemetry around the existing gateway call in payments/settle.go:

  ┌───────────────────┬─────────────────────────────────────────────┐
  │      Signal       │                    Value                    │
  ├───────────────────┼─────────────────────────────────────────────┤
  │ Trace fields      │ retry_attempt, retry_delay_ms, outcome      │
  ├───────────────────┼─────────────────────────────────────────────┤
  │ Recovery counter  │ payrail.retry_recovered                     │
  ├───────────────────┼─────────────────────────────────────────────┤
  │ Final result      │ unchanged                                   │
  └───────────────────┴─────────────────────────────────────────────┘

  No retry behavior changed. Operations can now separate recovered timeouts from final failures.

{d ✳ Completed in 9s}`

const bankOffer = `{d ❯} {u Ship it.}
{d ──────────────────────────────────────────────────────────────────────────────────────────────}
{c  ◆ «PRODUCT» }

{b Add idempotency context to PayRail retry telemetry — measured by}
{b colleagues: helped 90% · adopted 43% · n=40}
{b When adding or improving PayRail retry telemetry.}

{d ❯} {d 1.} Apply it now
     {d apply this to the work in hand}
  {d 2.} Show me how
     {d explain the technique and its evidence first}
  {d 3.} Not relevant here
     {d it does not fit here}
  {d 4.} Already use it
     {d already known}
  {d 5. Type something.}
{d ──────────────────────────────────────────────────────────────────────────────────────────────}
  {d 6.} Chat about this

{d Enter to select · ↑/↓ to navigate · Esc to cancel}`

const bankAdopt = `● User selected an answer:
  {d ⎿  · Add idempotency context to PayRail retry telemetry — measured by}
     {d colleagues: helped 90% · adopted 43% · n=40}
     {d When adding or improving PayRail retry telemetry.}
     {d → Apply it now}

  {d Called plugin:tacit:tacit (ctrl+o to expand)}

{g ●} {b Bash}(payrail idempotency trace --flow settle_charge)
  {d ⎿  key      charge_id}
     {d scope    full charge lifecycle}
     {d window   24h}
     {d ✓ every retry reuses the same key}
     {d trace    accepts retry_attempt metadata without changing the key}

{g ●} {b Bash}(payrail idempotency tests --flow settle_charge)
  {d ⎿  recovered timeout trace covered · counter and final result agree}

● The key is already safe across attempts. Added idempotency_scope=charge to the new trace data
  and a recovered-timeout view so operations can inspect successful retries by gateway.

{d ✳ Completed in 31s}`

// ---- Northbeam Communications (telecommunications-provider), team:network-provisioning ----
// A bulk VLAN changeset that resolves cleanly against inventory and would still
// have taken out one site's out-of-band management path.

const telcoWork = `{d ❯} {u Take the VLAN list in changes/vlan-migration.csv and turn it into a Trellis changeset for}
  {u the edge NFs in region 4. Same shape as the last one.                                    }

  {d Read }{b 2}{d  files (ctrl+o to expand)}

● Built changes/r4-vlan.plan from 38 rows:

  ┌───────────────────┬────────────────────────────────────────┐
  │       Field       │                 Value                  │
  ├───────────────────┼────────────────────────────────────────┤
  │ Network functions │ 38  (edge-r4-001 … edge-r4-038)        │
  ├───────────────────┼────────────────────────────────────────┤
  │ Operation         │ set access VLAN                        │
  ├───────────────────┼────────────────────────────────────────┤
  │ VLAN range        │ 812 – 849                              │
  ├───────────────────┼────────────────────────────────────────┤
  │ Everything else   │ carried over from live inventory       │
  └───────────────────┴────────────────────────────────────────┘

  The plan resolves cleanly against current inventory. Ready for trellis apply.

{d ✳ Completed in 14s}`

const telcoOffer = `{d ❯} {u Push it.}
{d ──────────────────────────────────────────────────────────────────────────────────────────────}
{c  ◆ «PRODUCT» }

{b Run ProvisionGuard preflight before core config pushes — measured by colleagues:}
{b helped 76% · adopted 35% · n=48}
{b Before pushing a configuration change to any provisioned network function.}

{d ❯} {d 1.} Apply it now
     {d apply this to the work in hand}
  {d 2.} Show me how
     {d explain the technique and its evidence first}
  {d 3.} Not relevant here
     {d it does not fit here}
  {d 4.} Already use it
     {d already known}
  {d 5. Type something.}
{d ──────────────────────────────────────────────────────────────────────────────────────────────}
  {d 6.} Chat about this

{d Enter to select · ↑/↓ to navigate · Esc to cancel}`

const telcoAdopt = `● User selected an answer:
  {d ⎿  · Run ProvisionGuard preflight before core config pushes — measured by colleagues:}
     {d helped 76% · adopted 35% · n=48}
     {d Before pushing a configuration change to any provisioned network function.}
     {d → Apply it now}

  {d Called plugin:tacit:tacit (ctrl+o to expand)}

{g ●} {b Bash}(provisionguard check --target edge-r4 --plan changes/r4-vlan.plan)
  {d ⎿  36 pass}
     {d FAIL  edge-r4-017  VLAN 828 already carries this site's out-of-band management path}
     {d WARN  edge-r4-031  VLAN 842 overlaps a compliance-scoped segment}

● Two of the thirty-eight. Held 017 and 031 out of the changeset, filed the VLAN conflict
  against the source list, and left the other 36 ready to apply.

{d ✳ Completed in 22s}`

// termMomentHTML renders one moment as a <pre> in the window's tape (site.go
// draws the window; the tape is all three moments of a session, one after the
// other, and the stage scrolls it).
//
// No padding to termRows. On a tape that would put two dozen blank lines
// between moments and the scroll would spend most of its travel crossing them;
// what makes this read as one session is that moment two starts where moment
// one stopped. termRows is the WINDOW's height now, and the ceiling any one
// moment has to fit under, which TestSiteFramesFitTheTerminal holds.
//
// The PRE is role="img" with the step's description as its name, which is what
// the stills' alt text used to do. A reader announcing two dozen lines of box
// drawing, tool output and column rules would be reading the evidence, not the
// argument; the description is the argument, and it is the same sentence either
// rendering shows.
func termMomentHTML(frame, label string) string {
	// Resolved here, before any of it is marked up: past this point the name's
	// own characters are escaped and each non-ASCII one is boxed, which is what
	// the frame needs and what a later substitution could no longer do.
	frame, label = siteProductText(frame), siteProductText(label)
	var b strings.Builder
	b.WriteString(`<pre class="site-term" role="img" aria-label="` +
		html.EscapeString(label) + `">`)
	for i, line := range strings.Split(frame, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(termLineHTML(line))
	}
	b.WriteString(`</pre>`)
	return b.String()
}

// termLineHTML paints one line: the ink spans become <span class>, everything
// else is escaped text. The frames carry ampersands and redirections from real
// shell commands, so escaping is not optional here.
func termLineHTML(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] == '{' && i+2 < len(line) && line[i+2] == ' ' {
			if cls, ok := termInk[line[i+1]]; ok {
				if end := strings.IndexByte(line[i+3:], '}'); end >= 0 {
					b.WriteString(`<span class="` + cls + `">`)
					termText(&b, line[i+3:i+3+end])
					b.WriteString(`</span>`)
					i += 3 + end + 1
					continue
				}
			}
		}
		// Not an opener: emit up to the next brace and look again there.
		next := strings.IndexByte(line[i+1:], '{')
		if next < 0 {
			termText(&b, line[i:])
			break
		}
		termText(&b, line[i:i+1+next])
		i += 1 + next
	}
	return b.String()
}

// termText writes a run of a line's characters, escaped, with every character
// outside ASCII boxed into one terminal cell.
//
// That box is not decoration, it is the grid. The embedded IBM Plex Mono is a
// Latin subset, so the box drawing, the bullets, the arrows and the harness's
// own ⎿ all come from whatever monospace the reader's machine falls back to —
// and a fallback's advance is its own business. Measured here they ran from 4%
// to 75% wider than a cell, which over a sixty-three character table rule is
// two and a half columns of drift, and which is a different amount of drift on
// every machine. A frame whose whole claim is "this is one screen of a terminal"
// cannot have its columns depend on what fonts the reader happens to own.
//
// One character, one cell, and the glyph is centred in it and allowed to spill:
// clipping instead would leave a hairline between consecutive rule characters,
// which is the artefact this is here to remove. The element is a bare <i> so
// the cost is ten bytes a character rather than an attribute a character —
// there is a lot of box drawing in frame one.
func termText(b *strings.Builder, s string) {
	for _, r := range s {
		if r < 0x80 {
			switch r {
			case '&':
				b.WriteString("&amp;")
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			case '"':
				b.WriteString("&#34;")
			default:
				b.WriteRune(r)
			}
			continue
		}
		b.WriteString("<i>")
		b.WriteRune(r)
		b.WriteString("</i>")
	}
}

// termPlain is a frame with its markup removed: the characters a terminal
// actually held. The width and height checks measure this, not the source.
func termPlain(frame string) string {
	frame = siteProductText(frame)
	var b strings.Builder
	for i := 0; i < len(frame); {
		if frame[i] == '{' && i+2 < len(frame) && frame[i+2] == ' ' {
			if _, ok := termInk[frame[i+1]]; ok {
				if end := strings.IndexByte(frame[i+3:], '}'); end >= 0 {
					b.WriteString(frame[i+3 : i+3+end])
					i += 3 + end + 1
					continue
				}
			}
		}
		next := strings.IndexByte(frame[i+1:], '{')
		if next < 0 {
			b.WriteString(frame[i:])
			break
		}
		b.WriteString(frame[i : i+1+next])
		i += 1 + next
	}
	return b.String()
}
