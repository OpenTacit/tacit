// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html"
	"net/url"
	"strings"

	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
)

// --- HTML dashboard --------------------------------------------------------------

type page struct {
	status    int
	active    string
	activeDoc string
	content   string
	// crumbs is the breadcrumb trail rendered in place of the page title. When
	// nil, crumbsFor derives a single crumb from active; deep pages (a technique, its
	// history, its insights) set the full trail so every parent is one click back.
	crumbs []crumb
	// viewMenu, when set, turns the LAST crumb into a dropdown of sibling views —
	// how the Techniques section (All / Map / Retired / Tags) switches without a
	// separate tab row.
	viewMenu []crumbOpt
	// narrow constrains the whole column — title and content together — and
	// centres it. A page whose content has its own fixed width would otherwise
	// sit at the left edge of a 1320px wrap with its title, which reads as a
	// mistake on a wide window.
	narrow bool
}

// crumb is one breadcrumb step. An empty href marks the current page (rendered
// as plain text, not a link); every ancestor carries an href so it's clickable.
//
// menu turns THIS step into a dropdown of what else sits at its level. It used
// to be settable only on the last step (page.viewMenu), which meant a trail
// could say where you were and never that there was anything underneath: a
// member on You / Now had no way to learn that You / Now / Allowance existed,
// because the only menu on the line was the one offering the four destinations.
//
// So the caret is now the trail's whole vocabulary for depth, and it reads both
// ways. A step with a caret is a level holding more than one page; a step
// without one is a level holding exactly one, and the absence is information —
// it says there is nothing below here to find.
type crumb struct {
	label, href string
	menu        []crumbOpt
}

// crumbOpt is one option in the breadcrumb view-switcher (page.viewMenu). It
// turns the last crumb into a dropdown of sibling views — the Techniques
// section's All / Map / Retired / Tags, which used to be a separate tab row
// costing its own vertical space. count < 0 shows no count.
type crumbOpt struct {
	label, href string
	count       int
	active      bool
}

// WHAT GOES IN A CARET, AND WHAT DOES NOT.
//
// A step's dropdown holds the pages at its level when they are a fixed set with
// names: the four destinations of You, its Allowance and Tools and Models, a
// technique's History. A member cannot be expected to find those any other way,
// so the trail is where they live.
//
// It does NOT hold a per-item family — a page per tag, per cohort, per
// technique. Those are not a menu's worth of things, they are a list's, and the
// list is already on the page above them. A caret that tried to hold every tag
// would be a table in a popover.
//
// So the rule both ways: a caret means "a handful of named pages sit here", and
// no caret on a step whose children are a list means "the list is upstairs".

// crumbsFor returns the page's breadcrumb trail, defaulting to a single crumb
// named for the active nav section when the page didn't set an explicit one.
func crumbsFor(p page) []crumb {
	if p.crumbs != nil {
		return p.crumbs
	}
	switch p.active {
	case "outcomes":
		return []crumb{{label: "Outcomes", href: ""}}
	case "usage":
		return []crumb{{label: sectionYou, href: ""}}
	// "events" sets its own trail (Outcomes › Events) in pageEvents, so it needs
	// no default here.
	case "techniques":
		return []crumb{{label: "Playbook", href: ""}}
	case "review":
		return []crumb{{label: "Review", href: ""}}
	case "learning":
		return []crumb{{label: "Learning readiness", href: ""}}
	case "settings":
		return []crumb{{label: "Settings", href: ""}}
	case "members":
		return []crumb{{label: "Members", href: ""}}
	case "federation":
		return []crumb{{label: "Federation", href: ""}}
	case "docs":
		return []crumb{{label: "Documentation", href: ""}}
	}
	return nil
}

// breadcrumbs renders the trail: ancestors as links, the last item as the
// current-page label, separated by a slash. Same size as the old page title.
// When menu is non-empty, the LAST crumb becomes a dropdown switching between
// those sibling views.
func breadcrumbs(crumbs []crumb, menu []crumbOpt) string {
	if len(crumbs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<nav class="crumbs" aria-label="Breadcrumb">`)
	for i, c := range crumbs {
		if i > 0 {
			b.WriteString(`<span class="crumb-sep" aria-hidden="true">/</span>`)
		}
		last := i == len(crumbs)-1
		// page.viewMenu is the shorthand for "a dropdown on the last step",
		// which is what most sections need and all of them used to.
		opts := c.menu
		if last && len(opts) == 0 {
			opts = menu
		}
		switch {
		case len(opts) > 0:
			// An ancestor that is a page of its own keeps its link and the
			// caret becomes a control beside it: "up one level" is the commonest
			// move on a trail, and folding the label into a <summary> would have
			// cost it a click. An ancestor that is only a CATEGORY — a level
			// with a name and no page, like the destination above Overview —
			// has no href, so its label is the summary and the caret is all it
			// is for.
			b.WriteString(crumbMenu(c.label, c.href, opts, last))
		case c.href != "" && !last:
			fmt.Fprintf(&b, `<a href="%s">%s</a>`, html.EscapeString(c.href), html.EscapeString(c.label))
		default:
			fmt.Fprintf(&b, `<span class="crumb-current" aria-current="page">%s</span>`, html.EscapeString(c.label))
		}
	}
	b.WriteString(`</nav>`)
	return b.String()
}

// crumbMenu renders one crumb as a dropdown of what sits at its level: the
// crumb's own label is the summary, the rest are options with their counts.
// Native <details>, so it works without JavaScript.
//
// here says whether this crumb is the page being read. Only that one is
// aria-current: a dropdown on an ANCESTOR is a way down into a level the reader
// is above, and announcing it as the current page would be a lie to anybody
// navigating by landmark.
func crumbMenu(current, href string, opts []crumbOpt, here bool) string {
	var b strings.Builder
	// A real SVG chevron, not a text ▾ — the glyph's font metrics sit it off-centre
	// in its pill; the path centres exactly and recolours with the summary text.
	const caret = `<svg class="crumb-caret" viewBox="0 0 10 6" aria-hidden="true"><path d="M1 1l4 4 4-4"/></svg>`
	if href != "" {
		// Label first as a link, then the caret on its own. The label is where
		// this step goes; the caret is what else is at its level.
		// Wrapped, so the label and its caret are ONE step of the trail: the
		// nav's own gap is the width of a separator, and leaving the caret to
		// sit in it made it read as belonging to neither neighbour.
		fmt.Fprintf(&b, `<span class="crumb-step"><a href="%s">%s</a>`+
			`<details class="crumb-menu caret-only">`+
			`<summary aria-label="Pages beside %s">%s</summary><div class="crumb-menu-pop">`,
			html.EscapeString(href), html.EscapeString(current),
			html.EscapeString(current), caret)
	} else {
		cur := ""
		if here {
			cur = ` aria-current="page"`
		}
		fmt.Fprintf(&b, `<details class="crumb-menu"><summary%s>%s%s</summary><div class="crumb-menu-pop">`,
			cur, html.EscapeString(current), caret)
	}
	for _, o := range opts {
		cls := ""
		if o.active {
			// "true", not "page": an option can be the current item in ITS set
			// without being the page being read — on a leaf, the destination
			// option is the category the reader is under, and the page is two
			// steps along. The crumb that IS the page says so itself, and
			// exactly one step on the line may.
			cls = ` class="active" aria-current="true"`
		}
		count := ""
		if o.count >= 0 {
			count = fmt.Sprintf(`<span>%d</span>`, o.count)
		}
		fmt.Fprintf(&b, `<a href="%s"%s>%s%s</a>`, html.EscapeString(o.href), cls, html.EscapeString(o.label), count)
	}
	b.WriteString(`</div></details>`)
	if href != "" {
		b.WriteString(`</span>`)
	}
	return b.String()
}

func (s *Server) renderShell(p page, user oidc.Claims) string {
	account := s.accountHTML(user, s.authRequired())
	nav := s.navHTML(p.active, p.activeDoc)
	// A visitor without a session reaches only what's public (the user guide),
	// and the working nav leads to gated pages — so it collapses to a single
	// Documentation link.
	if s.authRequired() && user == nil {
		nav = `<a href="/docs/user-guide" class="active">Documentation</a>`
	}
	// The bar carried a run of ambient totals here — techniques, events,
	// rollups, members, and the running build's sha. None of them was a thing
	// anybody arrives to read, and the strip that never scrolls away is the
	// most expensive place on the page to spend on a constant. The counts are
	// on the pages that are about them; the build is on /v1/health and in what
	// `make deploy` prints.
	return strings.NewReplacer(
		"«BASE»", s.cfg().BasePath,
		"«ACCOUNT»", account,
		"«DEMO»", s.demoHTML(),
		"«CRUMBS»", breadcrumbs(crumbsFor(p), p.viewMenu),
		"«NAV»", nav,
		"«CONTENT»", p.content,
		"«WRAPMOD»", map[bool]string{true: " wrap-narrow"}[p.narrow],
		"«PRODUCT»", productHTML(),
	).Replace(shellTemplate)
}

// accountHTML renders the signed-in member in the top bar: their OIDC profile
// picture as an avatar button that opens a menu naming them and offering Sign
// out. The name/email live in the menu rather than the bar, so identity costs
// one 32px circle instead of a line of text. Providers that return no picture
// (or a picture that fails to load — the shell's script drops it) fall back to
// the initials underneath. With no session it's a plain Sign in link.
// accountMenuItems is the avatar menu's link list, and it is SHORT on purpose.
//
// It had eight entries and a Sign out. Four of them — Members, Federation,
// Learning readiness and Settings — were one subject asked four ways: how this
// registry is configured and who reaches it. Three more were documentation.
// Eight destinations in a menu is a menu somebody reads rather than uses.
//
// Two now, and each is a SECTION rather than a page. Settings lands on General
// and carries the breadcrumb view-switcher every other section already uses
// (registryViews); Documentation lands on the guide and does the same
// Every page keeps its own URL, so nothing 301s, no bookmark
// breaks, and the switcher is a select that navigates — it works with no script,
// exactly as the period control does.
//
// General puts two levels of navigation on that one page. They are different
// KINDS and look it: the switcher is a
// breadcrumb dropdown choosing a page, and the tabs below choose a subject
// inside one form with one Save (settings_view.go). Splitting those tabs into
// pages instead would mean three Saves on the page where a wrong setting locks
// the operator out, which is a worse trade than a second row of navigation.
//
// Usage is NOT here any more. It was the one entry in this list that was not an
// operator's — a member's own numbers, read from their own machine — and a
// question somebody arrives with does not belong behind an avatar. It is the
// fourth item in the top bar (navItems).
//
// "Connect your tools" was a deep link into the guide. It is on the guide's own
// front page, which is where somebody looking for it goes.
// Settings leads. Documentation is last, nearest Sign out, where the wiring
// instructions used to sit.
const accountMenuItems = `<a class="account-item" role="menuitem" href="/docs/user-guide">Documentation</a>`

// registryViews is the Settings section's switcher: what this registry is
// configured to do, who can reach it, what crosses its boundary, and what its
// evidence can support.
//
// People renames itself the way the section's landing page does — "Team" on a
// registry that is one member's or has been opened to one, "Members" on an
// organization's — because those are the same subject under the name each kind
// of registry uses for it.
func (s *Server) registryViews(active string) []crumbOpt {
	opt := func(key, href, label string) crumbOpt {
		return crumbOpt{label: label, href: href, count: -1, active: key == active}
	}
	people := "Members"
	if s.membersHome() == "/team" {
		people = "Team"
	}
	// The section's own name is Settings, so the page that holds the settings
	// form cannot be called that too — it is General, and it leads, because it
	// is where the section lands. Then who is here, then the two an operator
	// visits occasionally.
	return []crumbOpt{
		opt("settings", "/settings", "General"),
		opt("people", s.membersHome(), people),
		opt("federation", "/federation", "Federation"),
		opt("learning", "/learning", "Learning readiness"),
	}
}

// registryCrumbs is the trail above that switcher. The section's own name leads
// to General, which is the page an operator opening "Settings" wants first.
func (s *Server) registryCrumbs(here string) []crumb {
	return []crumb{{label: "Settings", href: "/settings"}, {label: here, href: ""}}
}

// learningCrumbs is registryCrumbs with the level under Learning readiness
// opened out. Workflows used to render the SAME trail as the readiness page, so
// a reader could not tell they had moved — never mind that there was anywhere
// to move to.
func (s *Server) learningCrumbs(here string) []crumb {
	opt := func(label, href string, active bool) crumbOpt {
		return crumbOpt{label: label, href: href, count: -1, active: active}
	}
	kids := []crumbOpt{
		opt("Overview", "/learning", here == "Overview"),
		opt("How members work", "/learning/workflows", here == "How members work"),
	}
	return []crumb{
		{label: "Settings", href: "/settings"},
		{label: "Learning readiness", menu: s.registryViews("learning")},
		{label: here, menu: kids},
	}
}

// docsSectionCrumbs is the trail on a documentation page. The section
// leads to the guide, which is the half a member wants.
// There is one documentation section now, so the trail is one crumb and there
// is no switcher. The guide used to sit beside a Development section holding
// design records and contributor guides; those are kept outside the repository,
// and a dropdown offering a choice of one is a dropdown offering nothing.
func docsSectionCrumbs() []crumb {
	return []crumb{{label: "Documentation", href: ""}}
}

// accountHTML renders the signed-in member in the top bar. The avatar, its
// menu and the initials fallback live in internal/ui, because the ingress
// console shows the same thing and a second implementation would drift.
func (s *Server) accountHTML(user oidc.Claims, oidcOn bool) string {
	return ui.Account(user, oidcOn, s.accountMenu())
}

// accountMenu is the avatar menu for THIS registry.
//
// One entry for everything an operator configures. It used to be named after
// the thing rather than the act — "Registry", landing on the people page — and
// a reader looking for a switch had to guess that the noun held it. The people
// page is still in the section, one click down, under the name this registry
// uses for it: "Members" on an organization's, "Team" on one that is a single
// member's or has been opened to their team.
func (s *Server) accountMenu() string {
	return `<a class="account-item" role="menuitem" href="/settings">Settings</a>` + accountMenuItems
}

// navItems is the top bar: one destination per question a reader arrives with
// — is it working (Outcomes), what does my organization know (Techniques),
// what needs me (Review), how am I using this (Usage). Federation, Learning,
// Members and Settings are operator concerns and live in the account menu:
// giving a once-in-a-lifetime task (Federation) the same prominence as the
// daily one (Review, which had no nav entry at all) inverted the bar against
// how it is actually used.
//
// THE FOURTH QUESTION IS THE ONLY PERSONAL ONE, AND ITS NAME SAYS SO. The first
// three are about the organization's playbook; this one is one member's own
// machine, and the one view this registry cannot collect. It sat in the avatar
// menu, where the comment on that list already flagged it as the entry that is
// not an operator's — which is a note about the wrong home rather than a reason
// for it. A question a reader arrives with belongs in the row of questions.
//
// It is called "You" rather than "Usage" because "usage" says what is measured
// and leaves out the part that matters: whose. The label is also what makes the
// section's own Outcomes view legible — Outcomes is the organization's, You /
// Outcomes is yours, and the same measure at two scales is disambiguated by
// where it sits rather than by two names for one thing.
//
// The ROUTE does not follow the label. /usage is what members have bookmarked
// and what `tacit usage` prints, and a name is not a reason to move an address.
//
// ORDER: Outcomes leads because Outcomes is the landing page ("GET /{$}"), and
// the leftmost item must BE the front door — the brand mark links to "/", so any
// other order has "home" light up the middle of the bar and leaves a first item
// that isn't home. Review trails the three org questions: it carries the
// attention badge, and a badged queue reads as a notification, which belongs on
// the right of the group it belongs to. Usage follows all three rather than
// splitting them — Outcomes, Playbook, Review is a reading as well as a list,
// and a personal view dropped into the middle of it breaks the sentence to save
// a badge two inches of travel.
// playbookHome is where "Playbook" goes — the top-bar item, and the section
// crumb on every page under it (views, technique detail, history, publication). One
// constant because they are one affordance: a reader who clicks "Playbook" in
// the bar and a reader who clicks it in the trail must land on the same page,
// or the section appears to have two different front doors.
const playbookHome = "/techniques/map"

// sectionYou is what the personal section is CALLED, in the bar and at the head
// of every trail under it. One constant because a section with two names has
// two front doors: a reader who clicks it in the bar and one who clicks it in a
// breadcrumb have to arrive at the same place, under the same word.
const sectionYou = "You"

type navItem struct{ key, href, label string }

var navItems = []navItem{
	{"outcomes", "/outcomes", "Outcomes"},
	// Playbook opens on the MAP: "what does my organization know" is a question
	// about shape — which areas of practice exist, how they relate — and the map
	// answers it at a glance where a list makes you read it row by row. The list
	// (All), archive and tag vocabulary are one click away in the breadcrumb
	// view-switcher, which leads with Map for the same reason.
	{"techniques", playbookHome, "Playbook"},
	{"review", "/review", "Review"},
	// It opens on NOW, the way Playbook opens on the map: the state — what is
	// left of the allowance, what is open, what the window came to — is the
	// question with the shortest half-life here, and the one a member comes back
	// to. Work, Cost and Outcomes are one click away in the
	// breadcrumb switcher, which is the same affordance the Playbook's views use.
	{"usage", "/usage", sectionYou},
}

// navFor is this registry's top-level destinations: four questions, the same
// four for everybody.
//
// A single-member registry used to get a fourth, the user guide, first — and
// "/" redirected to it. That was the right answer to a bad first screen: the
// owner's alternative was a funnel with nothing in it, drawn as fourteen empty
// panels. Outcomes opens on what the registry has and what to do next now
// (outcomes.go), which is what the guide was standing in for. So the nav is one
// nav, and the guide stays where every other registry keeps it — the account
// menu, and a link from the first screen an owner sees.
func (s *Server) navFor() []navItem { return navItems }

func (s *Server) navHTML(active, activeDoc string) string {
	badges := s.navBadges()
	var b strings.Builder
	for _, item := range s.navFor() {
		cls := ""
		if item.key == active {
			cls = "active"
		}
		lbl := html.EscapeString(item.label)
		for _, badge := range badges[item.key] {
			lbl += fmt.Sprintf(`<span class="nav-badge %s" title="%s">%d</span>`,
				badge.kind, html.EscapeString(badge.title), badge.count)
		}
		fmt.Fprintf(&b, `<a href="%s" class="%s">%s</a>`, item.href, cls, lbl)
	}
	return b.String()
}

type navBadge struct {
	count int
	title string
	kind  string // "review" (drafts, accent) | "warn" (decayed)
}

// navBadges computes the attention indicators shown on the top navigation:
// work that is waiting on a human, not ambient totals (those live in the
// footer counts). Both ride on Review — the page that now holds the work they
// point at: drafts to decide, and decayed techniques needing re-validation.
func (s *Server) navBadges() map[string][]navBadge {
	badges := map[string][]navBadge{}
	if drafts, err := s.Store.ListTechniques([]string{"draft"}, 0); err == nil && len(drafts) > 0 {
		badges["review"] = append(badges["review"], navBadge{len(drafts),
			fmt.Sprintf("%d draft%s in the review queue", len(drafts), plural(len(drafts))), "review"})
	}
	if decayed, err := s.Store.ListTechniques([]string{"decayed"}, 0); err == nil && len(decayed) > 0 {
		badges["review"] = append(badges["review"], navBadge{len(decayed),
			fmt.Sprintf("%d decayed technique%s; revalidation needed", len(decayed), plural(len(decayed))), "warn"})
	}
	return badges
}

// orgOnlyBadge marks the exception and stays silent on the rule. "general" is
// the overwhelming default — badging every row with it spends the reader's
// attention on a constant, and buries the org-scoped techniques that are the
// whole point of having a registry. Returns "" for general scope, so it can be
// prefixed to a name unconditionally.
func orgOnlyBadge(scope string) string {
	if scope == "org" {
		return "<span class='org'>org</span> "
	}
	return ""
}

// techniquesFilterHref builds a /techniques URL carrying the given tag and/or scope
// filters. Empty values are omitted; with neither set it returns the bare list.
func techniquesFilterHref(tag, scope string) string {
	q := url.Values{}
	if tag != "" {
		q.Set("tag", tag)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	if len(q) == 0 {
		return "/techniques"
	}
	return "/techniques?" + q.Encode()
}

// scopeLink renders a technique's scope badge as a filter link to /techniques?scope=…,
// preserving any active tag filter so scope and tag filters stack. The badge
// keeps its org/gen look — no active-state highlight, since a scope-filtered
// list is entirely one scope and the filter note already names it. activeTag may
// be empty (e.g. on the detail page, where clicking just jumps to the list).
func scopeLink(scope, activeTag string) string {
	class := "gen"
	if scope == "org" {
		class = "org"
	}
	return fmt.Sprintf(`<a class="scope-link %s" href="%s">%s</a>`,
		class, html.EscapeString(techniquesFilterHref(activeTag, scope)), html.EscapeString(scope))
}
