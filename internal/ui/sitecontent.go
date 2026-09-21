// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

// The project page's words, held as data: the copy site.go renders, and the two
// marks it sets beside it. Everything here is authored text — the rules it is
// written to are in site.go's own note at the head of the page.

import "strings"

// SiteActionLabel is the page's one call to action, at the foot of the column for
// the reader who has scrolled everything. It opens the guide window — the same four
// commands the hero's green light opens — rather than going anywhere.
//
// It used to be a mailto to the pilot address, and the change is the same one the
// hero made: a landing page whose only action is "ask a person" is a page that
// cannot be acted on at the moment somebody wants to act. What remains for reaching
// a human is the close line's contact address, which is a way to reach somebody
// rather than a pitch to be let in.
//
// It is a <button> and not a link, because it goes nowhere. That is also why the
// page's anchor budget dropped by one when this changed (site_test.go).
const SiteActionLabel = "Get started"

// SiteInstallTitle is what the hero's terminal calls itself, in the place its
// title bar puts a window's name. It is the only instruction the page gives, and
// it is two words because the window under it is one line long.
const SiteInstallTitle = "Get started"

// SiteContactHref is the close line's address: a way to reach a person that is
// not the pilot pitch. It is a mailto, not a second destination, and the test
// that keeps this page to one call to action knows it by name.
const SiteContactHref = "mailto:team@opentacit.com"

// SiteContactMailbox is the local part of that address. The domain follows the
// host the page was served from, so a reader who arrived at one of this
// project's domains is given a way to reach it AT that domain rather than at
// another one they have no reason to trust yet.
const SiteContactMailbox = "team"

// Where the masthead's navigation goes. The blog is the project's own, on its
// own host; the rest is the repository, because the repository is still the only
// other place the project publishes anything. The apex serves this page and
// nothing else — 404 to every other path — so there is no docs site and no
// release feed of ours to point at. When one exists, its href replaces the line
// here and nothing else on the page changes.
//
// NOTE the repository links are dead until it is public — github.com/opentacit/tacit
// answers 404 to a stranger today. A published landing page whose navigation is
// six 404s is worse than one with no navigation, so the repository has to go
// public before this page does. The blog does not wait on that: it answers now.
const (
	SiteRepoHref = "https://github.com/opentacit/tacit"
	// The user guide's front page, not the docs tree: /docs is an index of design
	// records, plans and internal strategy with the guide one directory down, and
	// a stranger who follows a link called "User Guide" is looking for the guide.
	SiteDocsHref     = SiteRepoHref + "/blob/main/docs/user-guide/index.md"
	SiteReleasesHref = SiteRepoHref + "/releases"
	// The blog is a static site of ours on its own host, built from a separate
	// repository and served by GitHub Pages. Its own host and not a path here:
	// the apex is this page and the registry, and carving /blog out of it would
	// mean an edge rule splitting one hostname between two origins.
	SiteBlogHref = "https://blog.opentacit.com/"
)

// siteNav is the masthead's list, in the order it is read. It is a slice rather
// than three lines of markup so the test that counts this page's links can name
// the whole set: a link added to the template and not to this list — or the
// reverse — is the way a landing page turns into a menu one commit at a time.
//
// There is deliberately no "Get Started" here. The page has one call to action and
// it is the button; a second entry point in the masthead, wearing the same words
// and going somewhere else, is the page asking a stranger to choose how to start.
//
// "Blog" was taken out once, when the announcement it pointed at stopped being in
// this repository, on the grounds that a masthead link to a document nobody can
// open promises a destination and spends a stranger's click proving there isn't
// one. It is back because there is now a site answering at that address. It goes
// first: the other two are for somebody who has already decided to try this, and
// the blog is for somebody deciding whether to.
var siteNav = []struct{ Label, Href string }{
	{"Blog", SiteBlogHref},
	{"User Guide", SiteDocsHref},
	{"Releases", SiteReleasesHref},
}

// SiteHeadline is the page's one large line, and it is a constant because it is
// used twice: as the <h1> and, after the brand, as the <title>. Written into the
// template twice they drift, and they did — an edit to the headline left the title
// as "OpenTacit — " with nothing after the dash, which is invisible on the page
// and is exactly what a browser tab, a bookmark and a shared link all show.
const (
	siteHeadlineWhat = "what\u00a0works\u00a0with\u00a0AI"
	siteHeadlineHow  = "how\u00a0everyone\u00a0works"
	SiteHeadline     = "Turn " + siteHeadlineWhat + " into " + siteHeadlineHow + ", automatically."
)

// The user-flow section: the sequence of one interaction, told as three
// moments of Claude Code working against a registry loaded with a demonstration
// org (internal/demo), reproduced as the terminal's own text. The frames
// themselves, and what in them is measured rather than staged, are in
// siteframes.go.
//
// It is told three times, in three different industries, and the window's title
// bar picks between them. That is the section's second argument and the reason
// the picker earns a place in the chrome: the first telling shows what the
// product does, and the second and third show that what it knows is not
// generic. A Forgeflow template, a PayRail idempotency key's scope and a
// ProvisionGuard VLAN conflict have nothing in common except that no model
// ships knowing any of them.
//
// What IS staged is the org, and the page says so in the caption: every
// measured figure inside the frames is a demonstration org's, as that registry
// reports it. Those frames are the ONLY place on the page a rate appears at
// all, which is what TestSiteStatesNoRateInItsOwnVoice holds.
//
// On a wide screen with JavaScript the section is a scroll stage: the list on
// the left names the moments, and the window on the right is cut to the
// terminal's height and scrolled along the session as the reader goes, so
// moment two arrives where moment one stopped. Clicking a moment scrolls to it.
//
// Without JavaScript — or on a phone — the window is not cut at all: the three
// moments read as a list, and the whole session sits under them at its own
// length. One rendering, one home for each sentence, nothing for a second copy
// to drift from.

// siteFlowMoments are the three beats, and they are shared by every scenario
// because they ARE the argument: work, a suggestion with evidence, one answer
// that applies it. Only what fills them changes with the picker. Written per
// scenario they would drift, and the section would stop being one story told
// three times and become three stories.
var siteFlowMoments = [3]string{
	"Work with your agent",
	"Review a technique and its evidence",
	"Apply the technique",
}

type siteFlowStep struct {
	Body  string // one sentence under the active moment, in this scenario's terms
	Frame string // the terminal at that moment, in siteframes.go's markup
	Alt   string // the frame, described for a reader who cannot see it
}

// siteFlowScenario is one org's telling. Key is what the picker and the
// data-scen attributes agree on; Label is what the picker calls it.
//
// Label used to be the demonstration catalog's own vertical title
// (internal/demo/scenarios.go) and is no longer. The catalog names an industry a
// dataset was generated for — "AI model vendor", "Financial institution" — and
// this picker names the org in the frame beside it, which is narrower: the second
// telling is a payments platform's idempotency key, not a bank's balance sheet.
// The KEYS still agree with the datasets, which is what hack/sitestills' recipe
// needs; only the words on the picker are the page's own.
type siteFlowScenario struct {
	Key   string
	Label string
	Steps [3]siteFlowStep
}

var siteFlowScenarios = [3]siteFlowScenario{{
	Key:   "ai",
	Label: "AI model lab",
	Steps: [3]siteFlowStep{{
		Body: "A researcher tells their agent to copy last month's training config and " +
			"edit it for the next run. The prompt does not refer to «PRODUCT».",
		Frame: labWork,
		Alt: "A Claude Code session: the user tells the agent to copy a 70B " +
			"training run's config for the next run. The agent answers with a " +
			"table of the changes and of the values that stay.",
	}, {
		Body: "They ask to submit it. «PRODUCT» offers a matching playbook technique " +
			"with measured evidence. The user selects an answer.",
		Frame: labOffer,
		Alt: "The same session: «PRODUCT» offers, as a form, the technique “Your training " +
			"run can be launched from a matching Forgeflow template, not a " +
			"hand-rolled config”. " +
			"It shows the measured evidence line and four one-keystroke answers.",
	}, {
		Body: "“Apply it now” uses a validated template. The template replaces " +
			"the old checkpoint interval before submission.",
		Frame: labAdopt,
		Alt: "The same session: the user chose “Apply it now”. The agent starts " +
			"the run from the validated template. The template's checkpoint " +
			"interval replaces the one from the copied file.",
	}},
}, {
	Key:   "bank",
	Label: "Payments processor",
	Steps: [3]siteFlowStep{{
		Body: "A payments engineer asks to make successful gateway retries visible " +
			"to operations. The agent adds trace data and a recovery counter. " +
			"Nothing refers to «PRODUCT».",
		Frame: bankWork,
		Alt: "A Claude Code session: an engineer asks to expose successful payment " +
			"retries to operations. The agent answers with a table of trace fields " +
			"and the recovery counter it added.",
	}, {
		Body: "They ask to ship it. «PRODUCT» shows a matching technique, its evidence, " +
			"and the available actions.",
		Frame: bankOffer,
		Alt: "The same session: «PRODUCT» offers, as a form, the technique “Add " +
			"idempotency context to PayRail retry telemetry”. It shows " +
			"the measured evidence line and four one-keystroke answers.",
	}, {
		Body: "The trace confirms that the idempotency key is safe across attempts. " +
			"The agent enriches the new telemetry with the charge scope and adds a " +
			"recovered-timeout view to the operations dashboard.",
		Frame: bankAdopt,
		Alt: "The same session: the user chose “Apply it now”. The agent traces " +
			"the idempotency key, confirms that its scope covers the full charge, " +
			"then enriches the new telemetry and adds a recovered-timeout view.",
	}},
}, {
	Key:   "telco",
	Label: "Telecommunications provider",
	Steps: [3]siteFlowStep{{
		Body: "A provisioning engineer tells their agent to turn a VLAN list into " +
			"a bulk changeset for thirty-eight sites. It resolves cleanly against " +
			"the inventory.",
		Frame: telcoWork,
		Alt: "A Claude Code session: the engineer tells the agent to build a bulk " +
			"VLAN changeset for thirty-eight edge sites. The agent answers with a " +
			"table that shows the plan.",
	}, {
		Body: "They say push it. The playbook holds a measured technique for this " +
			"moment. «PRODUCT» offers it as a form with the evidence line.",
		Frame: telcoOffer,
		Alt: "The same session: «PRODUCT» offers, as a form, the technique “Run " +
			"ProvisionGuard preflight before core config pushes”. It shows the " +
			"measured evidence line and four one-keystroke answers.",
	}, {
		Body: "Preflight fails two of the thirty-eight sites. One VLAN already " +
			"carries a site's out-of-band management path. The other thirty-six " +
			"still ship.",
		Frame: telcoAdopt,
		Alt: "The same session: the user chose “Apply it now”. Preflight fails " +
			"two of the thirty-eight sites — one VLAN already carries an " +
			"out-of-band management path.",
	}},
}}

// siteFlowHead is the section's title and its honesty caption. It lives with
// the steps in the LEFT column of the stage — the reference layout — so the
// frames can take the full height of the pinned screen; in the stacked
// fallback it simply reads first.
const siteFlowHead = `<div class="site-flow-head"><h2>What «PRODUCT» users see</h2>
<p class="hint">«PRODUCT» recommends playbook techniques in supported CLI, web,
 desktop, mobile, Slack, chat, video, and OpenClaw tools. Each recommendation
 includes evidence from your organization's recorded outcomes.</p>
<p class="hint">Select an example in the window's title bar.</p></div>`

// The privacy section: the four guarantees, set the way the page's other two
// big sections are set — the head and its descriptive prose in a narrow
// column, the artifact beside it. Here the artifact is not a screen but the
// four cards themselves, two by two, each a small plate with the same
// machine-text numeral (.site-index) the page uses everywhere it counts.
//
// The composition takes the member-flow section's side — prose left, artifact
// right — so that this section and the evidence section above it, the two
// quiet siblings in the body column, mirror each other; the earlier form was a
// single panel with a four-across row inside it, which read as a form on a
// page whose every other section is an open split.
type sitePrivCard struct {
	Title string
	Body  string
}

var sitePrivCards = [4]sitePrivCard{{
	Title: "Open source",
	// The license answers lock-in and says nothing about data loss: keeping
	// your data is the next card's promise, and it is the deployment that
	// keeps it, not the licence. A guarantee we cannot enforce is worth less
	// than the one we can.
	Body: "«PRODUCT» uses the Apache 2.0 license. You can read, run, and fork the source.",
}, {
	Title: "Full data control",
	Body: "Your data stays on your site. «PRODUCT» does not store or share it " +
		"without your permission. You can operate «PRODUCT» with no connection " +
		"to a shared service, even in an air-gapped network.",
}, {
	Title: "Network access",
	Body: "An optional hosted reverse proxy provides remote access. Use it to share AI " +
		"techniques with other organizations, and to access the techniques they " +
		"share.",
}, {
	Title: "Tool support",
	Body: "Use «PRODUCT» with your usual AI agents: Amp, Claude Code, Codex, " +
		"Copilot CLI, Cursor, Gemini CLI, opencode and pi. MCP support adds " +
		"other compatible tools.",
}}

// The visibility section: the dashboard, shown rather than described. The page
// states no rate in its own voice (TestSiteStatesNoRateInItsOwnVoice), so the
// claim "you can see whether it works" is carried by the screen that shows it
// — the user guide's own screenshot pair, embedded (assets.go), with the prose
// beside it confined to WHAT is measured and never to a figure.
//
// The composition is the member-flow section's, on purpose: a narrow column of
// prose on the left, and the big artifact — there a terminal, here the
// dashboard — as a window on the right. The page shows two screens, and they
// should read as siblings, not as a window and a pasted-in picture. What this
// section does NOT take from that one is the stage: one image needs no scroll
// travel, no picker, and no script.
//
// The dashboard's three destinations, in the order its own top bar carries them.
// One slice and two tellings, the way the member-flow section works: the track's
// slides and the column's descriptions are both rendered from this, so a view
// cannot gain a screenshot and lose the sentence that explains it.
//
// Each shot is the user guide's own light/dark pair, held byte-identical to the
// guide's copy (siteshot_test.go) and embedded by assets.go. One alt per view
// and not per file: the pair is the same picture in two schemes and the
// stylesheet shows exactly one, so a screen reader should meet it once.
//
// The three are captured at one viewport (hack/guideshots) and so share a size,
// which is what lets the track hold a single height without the shorter screens
// floating in a band of empty page. The size is still written per view rather
// than as one constant: it is the intrinsic size of a particular file, and a
// view that is one day captured differently should say so here rather than
// inherit a number that stopped being true.
// Each view brings its whole column with it: a title, the deck under it, and the
// paragraph below that. The section used to have one fixed heading, "See how
// OpenTacit helps", over a column that changed underneath it — which made the
// heading a label for the section and not for the screen, and left the reader
// swiping between three pictures under a sentence that could only be about one
// of them.
var siteSeeViews = []struct {
	Key, Label      string
	Shot, Dark      string
	W, H            int
	Alt             string
	Title, Subtitle string
	Desc            string
}{{
	Key: "outcomes", Label: "Outcomes",
	Shot: "outcomes.png", Dark: "outcomes-dark.png", W: 2560, H: 2000,
	Alt: "The «PRODUCT» dashboard: the top bar with Outcomes, Playbook, and Review, " +
		"above the suggestion funnel and its pulse tiles",
	Title:    "Measure technique outcomes",
	Subtitle: "«PRODUCT» records when techniques are shown, adopted, and reported as helpful.",
	Desc: "The dashboard shows helped rates, use by cohort and area, and techniques " +
		"that a cohort has seen but not adopted. It uses session events and does not track individuals.",
}, {
	Key: "playbook", Label: "Playbook",
	Shot: "technique-map.png", Dark: "technique-map-dark.png", W: 2560, H: 2000,
	Alt: "The playbook map shows techniques as a network in a depth field, in " +
		"clusters with labels, and the Areas overlay is open adjacent to the map",
	Title:    "Browse the playbook",
	Subtitle: "View the organization's techniques in one place.",
	Desc: "The playbook is what your colleagues use to work with AI. Techniques " +
		"are grouped by area of practice and linked by shared use.",
}, {
	Key: "review", Label: "Review",
	Shot: "review-queue.png", Dark: "review-queue-dark.png", W: 2560, H: 2000,
	Alt: "The Review page shows the queue counts, and the drafts table with its " +
		"selection checkboxes and the Suggest candidate techniques button",
	Title:    "Review draft techniques",
	Subtitle: "A reviewer can edit, promote, or reject each draft.",
	Desc: "«PRODUCT» creates draft candidates from sessions and adds them to the review queue. " +
		"A person reviews each draft by default. Organizations can also automate review.",
}}

// zoomGlyphSVG is what the green light shows under the pointer: the two opposed
// corners a macOS window puts there to say "this one opens". Drawn rather than a
// character because the glyph has to be tiny, centred and the same on every
// platform, and it inherits currentColor so the stylesheet decides its ink.
const zoomGlyphSVG = `<svg viewBox="0 0 10 10" fill="currentColor" aria-hidden="true">` +
	`<path d="M1.4 1.4h3.4L1.4 4.8z"/><path d="M8.6 8.6H5.2l3.4-3.4z"/></svg>`

// The guide window: the hero's install command is one line of a chapter, and the
// green light opens the rest of it. What is in here is the user guide's "Set up a
// registry" compressed to the four commands it turns on — install, bootstrap,
// invite, check — in this page's own voice and at this page's length.
//
// It is a retelling and not a copy, for the reason the whole page is short: a
// stranger who wants the chapter has the User Guide link in the masthead, and a
// landing page that pastes a chapter into a modal has become documentation with a
// close button. What must not drift is the COMMANDS: every one of them is checked
// against the guide's own file by TestGuideWindowTeachesWhatTheGuideTeaches.
//
// No links in here, deliberately. The page's link budget is fixed and named
// (site_test.go), and a modal is exactly where an unnoticed sixth destination
// would appear. The masthead's User Guide is the way out to the full chapter.
const (
	// The window's accessible name — on the <dialog> and on the control that opens
	// it — and, as SiteGuideHeading, what the window calls itself once it is open.
	// They differ by one word on purpose: the name is spoken about a window on a
	// page that has already said whose product this is, and the heading is the
	// first line of a document that has not.
	//
	// The title BAR is not this. It carries SiteInstallTitle, the same two words
	// the little window in the hero carries, because the big one grows out of the
	// little one and a title that changes mid-flight is the seam showing.
	SiteGuideTitle   = "Set up a registry"
	SiteGuideHeading = "Set up your «PRODUCT» registry"
	// The commands the window teaches, in the order it teaches them. Named so
	// the test that ties this window to the guide has one list to walk rather
	// than a regexp over rendered HTML.
	siteGuideInit   = "tacit init"
	siteGuideInvite = "tacit invite"
	siteGuideDoctor = "tacit doctor"
)

// siteGuideCommands is every command this window puts in front of a reader. The
// guide has to contain each of them verbatim, or the page and the documentation
// are teaching different things.
var siteGuideCommands = []string{InstallCommand, siteGuideInit, siteGuideInvite, siteGuideDoctor}

// githubMarkSVG is the Octocat silhouette, GitHub's own mark, drawn at 16 units
// and painted with currentColor so it takes the masthead's ink in both themes —
// no hex anywhere, which is the rule this page is held to and the reason the
// icon is a path rather than a downloaded PNG.
//
// It is GitHub's trademark and is used the way their guidelines allow: as the
// link to a repository, unmodified, in one colour, and never as part of this
// project's own mark.
const githubMarkSVG = `<svg width="17" height="17" viewBox="0 0 16 16" fill="currentColor" ` +
	`aria-hidden="true"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 ` +
	`0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 ` +
	`1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 ` +
	`0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 ` +
	`2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 ` +
	`0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg>`

// siteHarnesses is the marquee's content: the tools OpenTacit works inside. It is
// the page's one piece of motion and it earns it by being information — the
// question a stranger asks after "what is it" is "does it work where I work", and
// the answer is a list too long to set as prose. Doubled in the template because a
// marquee needs a second copy to scroll into — and the separator has to sit
// BETWEEN the copies as well as between the items, or the loop reads
// "OmnigentClaude Code" at the seam.
//
// It is also the hero's scroll cue, which is why the stage above it no longer
// fills the viewport: a band of tool names crossing the fold says there is more
// page far better than the nodding chevron that used to sit there, and it says
// something while doing it.
var siteHarnesses = strings.Join([]string{
	"Claude Code", "Codex", "Cursor", "Gemini CLI", "Copilot CLI", "Amp",
	"opencode", "pi", "omp", "Omnigent",
}, `<i>◆</i>`)
