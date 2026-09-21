// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Settings page.
//
// LOOK — a subject is a PLATE and a setting is a ROW, which is how the rest of
// the registry is drawn (see the stylesheet's four rules, and the .set block
// under them). Each plate is a flat hairline-bordered panel with a quiet Title
// Case caption; each row puts its label in one column and its control or its
// value in the next, so a fact and a field are the same shape and the eye finds
// both columns once. Before this the page was the only view in the registry with
// no plate under its content, its second subject was announced by a tracked-out
// uppercase banner, fields stacked their labels while facts set theirs beside,
// and a fact's value was flung to the far right of the window.
//
// STRUCTURE — the page is organized by SUBJECT, and whether a setting is
// editable is a property of the row rather than of the page.
//
// It used to be the other way round: an "Editable now" half with its own group
// headings, then an "Everything else" half with different ones. That split cost
// the operator the ability to find anything without first knowing whether it was
// hot-appliable — and it duplicated subjects under near-identical names
// ("Registry" and "Federation" above, "Registry & federation" below;
// "Automation" in both halves). Five subjects now, each appearing exactly once:
// who can reach it, who administers it, what it does unattended, what it thinks
// with, and how it is deployed. A row is either a control or a fact, and a fact
// says what it takes to change it.
//
// The five subjects live on THREE tabs, because two of the pairs are one
// decision made twice. Reaching the registry and signing in to it are the same
// question asked of the world and of a person — an operator setting up access
// does both in one sitting — and the model is what the automation thinks with,
// so a registry that turns automation on with no key has a fault it can only see
// if the two are on the same screen. Deployment stays alone: it is the only tab
// where nothing is editable, and folding restart-only facts in beside live
// controls would make "can I change this here?" a per-row question on every tab
// instead of one.
//
// COPY — a settings page is not documentation. Anything the operator cannot act
// on has been cut: environment-variable names, "(a URI)", "(registry-side
// features)", "(provider default if blank)" and the rest are gone. What is left
// earns its place by being one of three things — the CONSEQUENCE of a decision at
// the point of making it (what Global Access shares), the REMEDY for a fault
// (register this callback), or a CONSTRAINT the control cannot express itself
// (this one needs a restart). Formats and defaults live in placeholders, where
// they are visible without being read.
//
// The automation switches each carry ONE line under the label, and it is the
// first kind: the consequence, at the point of deciding. A switch that hands a
// decision to the evidence has to say what the evidence then does — starts
// serving a technique to members, leaves a person at a keyboard unaffected —
// because that is the whole of what the operator is weighing.
//
// SCANNABILITY — every tab carries its subject's state, so the answer to "is
// automation on?" is available from the tab strip without opening anything.
// Splitting a page into tabs normally costs the overview; putting the state on
// the tabs is what buys it back.
package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/opentacit/tacit/internal/llmprovider"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// settingsView renders the whole page. Admins get controls; everyone else gets
// the same page with the same sections, read-only.
//
// The subjects are TABS rather than one screen of scroll: the page was long
// enough that the last section was reachable only by scrolling past four others.
// A merged tab renders its two subjects as two captioned plates (plate) and
// carries both subjects' chips, so nothing that used to be visible from the
// strip stops being visible.
//
// The tabs are CSS, driven by radio inputs that precede the panels as siblings.
// That matters for one reason above all: **every panel stays in the DOM and
// inside the form**, so one Save still submits every field. A tab that swapped
// panels server-side, or removed them from the DOM, would post a partial form —
// and handleSettingsSave writes "0" for an absent checkbox, so saving from the
// Access tab would silently switch off every automation toggle. Hidden fields
// still submit; that is what makes this safe. It also means no JavaScript, so the
// page works the same when a script fails to load.
func (s *Server) settingsView(r *http.Request, user oidc.Claims, out settingsOutcome) string {
	admin := s.isAdmin(user)
	tabs := []struct{ id, title, chip, body string }{
		{"access", "Access and sign-in", s.accessChip() + s.adminsChip(),
			plateWith("Access", s.sectionAccess(admin), accessScene(s.cfg().GlobalAccess)) +
				plate("Sign-in", s.sectionAdmins(r, admin, out))},
		{"automation", "Automation and model", s.automationChip() + s.modelChip(),
			plate("Automation", s.sectionAutomation(admin)) + plate("Model", s.sectionModel(admin))},
		{"deployment", "Deployment", chip("restart", "muted"), plate("Deployment", s.sectionDeployment())},
	}

	// WHICH TAB, FROM THE URL. Somewhere else on this registry has to be able to
	// send a reader to a subject rather than to a page — the cold start's model
	// step wants the Model plate, not the top of Settings — and this file's own
	// rule is that a view's state lives in its address. ?tab= seeds the radio
	// that is checked; everything else about the tabs stays CSS, so it still
	// works with no script and the whole form still posts.
	active := 0
	if r != nil {
		for i, t := range tabs {
			if t.id == r.URL.Query().Get("tab") {
				active = i
			}
		}
	}

	var b strings.Builder
	if admin {
		// The action keeps the tab, so a save — and a save that comes back with
		// a problem — lands the reader where they were rather than at the top.
		action := "/settings"
		if active > 0 {
			action += "?tab=" + url.QueryEscape(tabs[active].id)
		}
		fmt.Fprintf(&b, `<form class="set" method="post" action="%s">`, html.EscapeString(action))
		fmt.Fprintf(&b, `<input type="hidden" name="csrf" value="%s">`, html.EscapeString(s.csrfToken(r)))
	} else {
		b.WriteString(`<div class="set">`)
	}

	// The radios come first so the sibling selectors reach both the tab strip and
	// the panels. Named `_tab` — handleSettingsSave reads named fields, so an extra
	// one is inert.
	for i, t := range tabs {
		checked := ""
		if i == active {
			checked = " checked"
		}
		fmt.Fprintf(&b, `<input class="set-tab-in" type="radio" name="_tab" id="tab-%s"%s>`, t.id, checked)
	}

	// The chips ride the tabs, so the state of all five subjects is visible at
	// once even though one panel is — a merged tab wears both of its subjects'
	// chips. Splitting a page into tabs usually costs exactly that overview; here
	// it does not.
	b.WriteString(`<nav class="set-tabs">`)
	for _, t := range tabs {
		fmt.Fprintf(&b, `<label class="set-tab" for="tab-%s">%s%s</label>`, t.id, t.title, t.chip)
	}
	b.WriteString(`</nav>`)

	if !admin {
		b.WriteString(s.readOnlyReason())
	}
	for _, t := range tabs {
		fmt.Fprintf(&b, `<section class="set-panel" id="set-%s">%s</section>`, t.id, t.body)
	}

	if admin {
		// Rendered ENABLED, and disabled by the script a moment later. The other
		// way round would make a page whose script failed to load a page whose
		// settings cannot be saved at all — a much worse failure than a button
		// that is live when nothing has changed.
		b.WriteString(s.saveButton(r, admin, out))
		b.WriteString(switchScript)
		b.WriteString(adminEmailsScript)
		b.WriteString(modelCatalogScript)
		b.WriteString(signInTestScript)
		b.WriteString(saveGuardScript)
		b.WriteString(`</form>`)
	} else {
		b.WriteString(`</div>`)
	}
	return b.String()
}

// settingsOutcome is what a POST leaves behind for the page it re-renders: the
// provider checks it ran, and whether they passed. A GET carries neither.
type settingsOutcome struct {
	checks       string
	checksPassed bool
}

// saveButton is one button with two jobs, and the sign-in switch decides which.
//
// Moving to Shared makes it **Test configuration**: the settings it would write
// are typed from another system's console, and every way of mistyping them
// produces one symptom — nobody can sign in, at a registry that now needs a
// sign-in. Passing the checks turns it back into Save changes.
//
// The label is decided here as well as in the script, and they must agree: the
// script settles it the moment the page is live, and this is what a browser
// without one sees. Both read the same three facts — the switch's position,
// whether this session may move it, and whether the values in the form have
// been proved.
func (s *Server) saveButton(r *http.Request, admin bool, out settingsOutcome) string {
	label, mode := "Save changes", "save"
	if s.signInConfigurable(admin) && posted(r, "signin_shared") != "" && !out.checksPassed {
		label, mode = "Test configuration", "test"
	}
	// What the last save did, beside the button that did it. At the top of the
	// page it was a line the operator had to scroll back up to find, answering a
	// question they asked at the bottom.
	done := ""
	if r != nil && r.URL.Query().Get("saved") == "1" {
		done = `<span class="set-saved">✓ Save complete. The settings are in effect.</span>`
	}
	return fmt.Sprintf(`<div class="set-save"><button type="submit" name="do" value="%s">%s</button>%s</div>`,
		mode, label, done)
}

// readOnlyReason tells a non-admin what would let them edit — the one thing they
// can act on. Three different answers, because the fixes are different: sign in
// as the owner, ask an administrator, or configure a sign-in at all.
func (s *Server) readOnlyReason() string {
	switch {
	case s.cfg().OwnerEnabled():
		return `<p class="set-lede">Read-only: sign in as this registry’s owner ` +
			`(<code>tacit dashboard</code>) to change these.</p>`
	case s.OIDC == nil:
		return `<p class="set-lede">Read-only: web edits need a configured sign-in.</p>`
	}
	return `<p class="set-lede">Read-only: an administrator can change these.</p>`
}

// ---- sections ---------------------------------------------------------------

// sectionAccess is how the world reaches this registry, and what it shares.
//
// The proxy's OWN address is deliberately not here. It is deployment, like the
// host and the port: one shared ingress serves everybody, an operator who runs
// their own edits registry.env and restarts, and a text box offering a choice
// nobody makes cost the tab a field, a validator and a paragraph explaining what
// the box dialled.
// It returns ROWS, not a panel: it shares a tab with the sign-in rows below it
// and the two go in one grid, so a single set of columns runs down the tab.
func (s *Server) sectionAccess(admin bool) string {
	c := s.cfg()
	var rows strings.Builder

	st := s.PublishState()
	if admin {
		rows.WriteString(setSwitch("publish", "Global Access", "Own address", c.GlobalAccess, false,
			publishAddressHTML(st), setExternalURL(c.ExternalURL)))
		// The gate asks about contributing techniques, which is a Global Access
		// question: on Own address there is nothing to consent to.
		rows.WriteString(onlyWhenSwitch("publish", s.globalAccessConfirm(), c.GlobalAccess))
	} else {
		rows.WriteString(setFact("Global Access", onOff(c.GlobalAccess), ""))
		rows.WriteString(setFact("External URL", orDash(s.effectiveExternalURL()), ""))
	}

	// The callback an operator has to go and register. It is about the proxy's
	// address, so it belongs to the Global Access position and moves with it —
	// on Own address the callback it names is not the one in force.
	if admin || c.GlobalAccess {
		if status := publishStatusHTML(st, s.oidcCallbackToRegister(),
			s.PublishedCallbackUsable()); status != "" {
			rows.WriteString(onlyWhenSwitch("publish",
				`<div class="set-wide set-status">`+status+`</div>`, c.GlobalAccess))
		}
	}
	return rows.String()
}

// sectionAdmins is who may change any of this. It shares the Access tab: who can
// reach the registry and who can sign in to administer it are one setup.
//
// Its first row is the Personal/Shared switch, which is why the administrators
// field is no longer here: it belongs to the Shared position and is rendered
// with it (signin.go).
func (s *Server) sectionAdmins(r *http.Request, admin bool, out settingsOutcome) string {
	var rows strings.Builder
	rows.WriteString(s.signInRows(r, admin, out))
	rows.WriteString(setFact("API key", maskSecret(s.cfg().APIKey), restartBadge))
	return rows.String()
}

// automationSetting is one thing the registry does with no one watching: the
// field the save reads, the label BOTH views show, and whether it is on.
//
// One label per setting, not one per view. The same four settings used to carry
// eight strings — a long sentence on the admin's toggle, a short one on
// everybody else's fact — so the page said two different things about each of
// them depending on who was reading.
//
// And one name per concept, which is what "shadow" broke. It is the status
// string in the store; the lane it names is called UNDER EVALUATION everywhere
// a member meets it (review.go), described there as retrieved and fit-checked
// but never shown — evidence at zero exposure. Only this page said "shadow", so
// only this page asked the operator to learn a second word for the thing the
// Review page had already named.
//
// The two middle switches are the two ENDS of one lane, and the labels have to
// say which end. Both remove a person — that is the "without review" they share
// — but the first lets a machine-written technique INTO evaluation, where it is
// fit-checked and shown to nobody, and the second lets one OUT of evaluation to
// members, which is the consequential half. Written as a matched pair
// ("Evaluate... / Promote from evaluation...") they read as one setting said
// twice; the verbs the Review page already uses, evaluate and serve, are what
// tell them apart. Naming only what becomes automatic ("...automatically")
// leaves the other half of the fork unsaid: switched off, these techniques are
// not unevaluated, they are queued for somebody.
type automationSetting struct {
	name, label, desc, badge string
	on                       bool
}

// The description is ONE LINE and says the thing the label had to leave out —
// the scope of what changes, or what decides in a person's place. It is not the
// documentation the guide already carries.
func (s *Server) automationSettings() []automationSetting {
	c := s.cfg()
	return []automationSetting{
		{"autonomy", "Apply proven techniques automatically for agents",
			"Autonomous sessions only. A person at a keyboard still sees the suggestion.",
			"", c.AutonomyEnabled},
		{"auto_shadow", "Evaluate new machine techniques without review",
			"Checks new drafts for fit before adding them to the review queue.",
			"", c.AutoShadow},
		{"auto_promote", "Serve what passes evaluation without review",
			"Promotes a technique when its fit evidence meets the set limits.",
			"", c.AutoPromoteEnabled},
		{"auto_discover", "Discover techniques from usage",
			"Each recompute cycle, clusters the moves that worked into candidates.",
			restartBadge, c.AutoDiscover},
	}
}

// sectionAutomation is what the registry does with no one watching: four
// switches and nothing else.
//
// The thresholds each of them ran on — a helped rate and a sample size, a fit
// rate and a count — are gone from the page. Four number boxes carrying defaults
// nobody had reason to move made the section look like a control panel for a
// question that is really a yes or a no, and each one needed a label, a
// validator and a sentence of its own. They keep working: the values live in
// registry.env, where the rest of what this registry runs on lives, and a
// registry with reason to move one says so there.
func (s *Server) sectionAutomation(admin bool) string {
	var rows strings.Builder
	for _, a := range s.automationSettings() {
		if admin {
			rows.WriteString(setToggle(a.name, a.label, a.desc, a.on, a.badge))
		} else {
			rows.WriteString(setFactDesc(a.label, onOff(a.on), a.desc, a.badge))
		}
	}
	// Auto-promote publishing machine-written techniques to the world is the one
	// combination worth interrupting for, and it is a fact about THIS registry's
	// current settings rather than a description of the feature.
	if s.cfg().AutoPromoteEnabled && s.GlobalAccessServing() {
		rows.WriteString(setWarn(`Automatic promotion does not publish techniques. A person must approve ` +
			`them for the <a href="/federation#public">Public feed</a>.`))
	}
	return rows.String()
}

// sectionModel is the model the registry's own features use — suggestion
// research, cluster descriptions, tag merging. It shares the Automation tab,
// because the automation above it is the biggest thing spending this key: a
// missing key and a switched-on discoverer belong on one screen.
func (s *Server) sectionModel(admin bool) string {
	provider := os.Getenv("TACIT_LLM_PROVIDER")
	if !llmprovider.Valid(provider) {
		provider = llmprovider.Default
	}
	cur := llmprovider.Lookup(provider)

	var rows strings.Builder
	if !admin {
		rows.WriteString(setFact("API key", maskSecret(os.Getenv("TACIT_LLM_API_KEY")), ""))
		rows.WriteString(setFact("Provider", cur.Label, ""))
		rows.WriteString(setFact("Suggest model", orElse(os.Getenv("TACIT_SUGGEST_MODEL"), cur.ResearchModel), ""))
		return rows.String()
	}

	keyHint := "sk-…"
	if os.Getenv("TACIT_LLM_API_KEY") != "" {
		keyHint = "Leave blank to keep the current key"
	}
	rows.WriteString(setPassword("llm_key", "API key", keyHint))
	// Each option carries its provider's defaults so the placeholders below can
	// follow the dropdown before anything is saved.
	var opts strings.Builder
	for _, p := range llmprovider.Known {
		sel := ""
		if p.Key == provider {
			sel = " selected"
		}
		fmt.Fprintf(&opts, `<option value="%s"%s data-base="%s" data-model="%s">%s</option>`,
			p.Key, sel, html.EscapeString(p.BaseURL), html.EscapeString(p.ResearchModel),
			html.EscapeString(p.Label))
	}
	rows.WriteString(setSelect("llm_provider", "Provider", opts.String()))
	// The address in force, as a VALUE — not the operator's override with the
	// provider's default hidden behind it in the placeholder. A placeholder is
	// only visible while the box is empty, so a registry that had ever saved this
	// page showed one address and could be using another: change the provider and
	// the placeholder followed it while the box kept the address of the provider
	// you just left, which is the one that would be saved and called. What the
	// field says is now what llmprovider.BaseURLFor resolves for every caller.
	rows.WriteString(setURL("llm_base_url", "Base URL",
		llmprovider.BaseURLFor(provider, os.Getenv("TACIT_LLM_BASE_URL")), cur.BaseURL, false))
	rows.WriteString(setCombo("suggest_model", "Suggest model",
		os.Getenv("TACIT_SUGGEST_MODEL"), cur.ResearchModel))
	rows.WriteString(setCombo("tagmerge_model", "Tag-merge model",
		os.Getenv("TACIT_TAGMERGE_MODEL"), orElse(os.Getenv("TACIT_SUGGEST_MODEL"), cur.ResearchModel)))
	rows.WriteString(`<datalist id="model-catalog"></datalist>`)
	return rows.String()
}

// sectionDeployment is everything that takes a restart, for everyone. It keeps a
// tab of its own — it is the one place where no row is editable, and it is last
// because it is the section nobody came here to change.
func (s *Server) sectionDeployment() string {
	c := s.cfg()
	var rows strings.Builder
	rows.WriteString(setFact("Serving", fmt.Sprintf("%s:%d%s", c.Host, c.Port, c.BasePath), ""))
	rows.WriteString(setFact("Storage", storageLabel(s.Cfg), ""))
	rows.WriteString(setFact("Techniques directory", c.TechniquesDir, ""))
	rows.WriteString(setFact("Embedder", fmt.Sprintf("%s · dim %d", c.EmbedModel, c.EmbedDim), ""))
	rows.WriteString(setFact("Recompute interval", fmt.Sprintf("%ds", c.RecomputeIntervalSecs), ""))
	rows.WriteString(setNote("Edit <code>" + html.EscapeString(config.RegistryEnvPath()) +
		"</code> and restart. Environment variables override the file."))
	return rows.String()
}

// globalAccessConfirm is the one-way consent gate a staged registry has to pass,
// and nothing else.
//
// Everything that used to sit above it has gone: a term list explaining the
// arrangement, a sentence restating what the ticked switch already showed, and
// finally the running count of what Global Access would contribute. The count
// read as information and was not: it is on the page whether or not anybody is
// deciding anything, it changes on its own as techniques are written, and an
// operator who wants to know what leaves opens the list. The switch and its
// address are the whole of this tab's first line now.
//
// The gate itself stays, and stays wordy. It asks agreement to something the
// switch the operator already moved did not cover, so it says what that is and
// links the documents it is asking about — consent to publishing N documents is
// only real if the N are one click away.
//
// Its copy used to open "This registry was reachable through the proxy before
// Global Access existed", which reads as a note about an upgrade. Staged is only
// GlobalAccess && !Confirmed, so EVERY registry lands here the first time it
// moves the switch — including one set up yesterday, which was then told a
// history it does not have. It states the position instead: the address works,
// the techniques have not moved.
func (s *Server) globalAccessConfirm() string {
	if !s.GlobalAccessStaged() {
		return ""
	}
	served := s.publicServedCount()

	// Nothing eligible yet is the ordinary state of a new registry — every
	// technique is below the evidence floor until members have used it — and
	// asking somebody to contribute nothing is a question with no content. Say
	// where it stands and leave the box out until there is something in it.
	if served == 0 {
		// Leads with the techniques rather than the address, because the
		// address has its own state a line above — it can be offline or
		// reconnecting — and a panel asserting "the address is live" directly
		// under a red dial error is the page arguing with itself.
		return `<div class="set-row set-wide set-bargain">` +
			`<div class="set-confirm"><p><b>No techniques are eligible for the Public pool.</b> ` +
			`A technique becomes eligible when your colleagues' outcomes meet its evidence minimum. ` +
			`You can approve eligible techniques here.</p></div></div>`
	}

	return fmt.Sprintf(`<div class="set-row set-wide set-bargain">`+
		`<div class="set-confirm"><p><b>Public access is active.</b> `+
		`Approve <a href="/federation#public">%d eligible technique%s</a> to publish them.</p>`+
		`<label class="set-confirm-box"><input type="checkbox" name="global_access_confirm">`+
		`<span>Contribute these techniques to the Public pool</span></label></div></div>`, served, plural(served))
}

// publicServedCount is how many techniques would go to the Public pool if the
// operator confirmed. Zero is the ordinary answer for a registry whose members
// have not produced enough evidence yet.
func (s *Server) publicServedCount() int {
	members, _, _ := s.PublicStatus()
	n := 0
	for _, m := range members {
		if m.Served {
			n++
		}
	}
	return n
}

// ---- state chips ------------------------------------------------------------

// A section's state, so the page answers "what is on?" without being read. Each
// chip reports a fault when there is one, because a fault is the only thing here
// an operator needs to act on immediately.
func (s *Server) accessChip() string {
	if !s.cfg().GlobalAccess {
		return chip("private", "muted")
	}
	st := s.PublishState()
	switch {
	case st.LastError != "" && st.URL == "":
		return chip("offline", "bad")
	case !st.Connected:
		return chip("reconnecting", "warn")
	case s.GlobalAccessStaged() && s.publicServedCount() > 0:
		// Something is waiting on a person. That is worth a warning.
		return chip("unconfirmed", "warn")
	}
	// Staged with nothing eligible is not a fault and not a decision anybody is
	// dodging: it is every new registry, and the address works.
	return chip("global", "good")
}

func (s *Server) adminsChip() string {
	c := s.cfg()
	// Sign-in written but not in force outranks everything else here: the
	// dashboard is open while its own settings say it is not, and the fix is one
	// restart. Both other answers below would describe the file or the process
	// and hide the gap between them.
	if s.signInWritten() {
		return chip("restart", "warn")
	}
	// Owner mode is a sign-in, and the registry has exactly one administrator:
	// whoever can mint a link on the machine it runs on. Reporting that as "no
	// sign-in" told the one person who had just signed in that nobody could.
	if c.OwnerEnabled() {
		return chip("owner", "good")
	}
	if s.OIDC == nil {
		return chip("no sign-in", "warn")
	}
	if len(c.AdminEmails) == 0 {
		return chip("no administrators", "warn")
	}
	return chip(fmt.Sprintf("%d", len(c.AdminEmails)), "muted")
}

func (s *Server) automationChip() string {
	c := s.cfg()
	n := 0
	for _, on := range []bool{c.AutonomyEnabled, c.AutoShadow,
		c.AutoPromoteEnabled, c.AutoDiscover} {
		if on {
			n++
		}
	}
	if n == 0 {
		return chip("off", "muted")
	}
	return chip(fmt.Sprintf("%d on", n), "good")
}

func (s *Server) modelChip() string {
	if os.Getenv("TACIT_LLM_API_KEY") == "" {
		return chip("no key", "warn")
	}
	return chip("configured", "good")
}

// ---- row and section helpers ------------------------------------------------

// restartBadge marks a row the running process will not pick up. It is a
// constraint the control cannot express itself, which is why it survives the cut.
const restartBadge = "restart"

// plate is ONE SUBJECT, rendered as the house plate every other view is built
// from: a flat hairline-bordered panel with a quiet Title Case caption (the
// stylesheet's Rules 1 and 3), holding that subject's rows and nothing else.
//
// It replaces a pair of arrangements that were the page's worst habit. The tab
// body used to be one bare grid on the plane — the only view in the registry
// with no plate under its content — and the second subject on a merged tab was
// introduced by a tracked-out uppercase heading inside that grid, which is the
// banner Rule 3 exists to forbid. Both subjects are plates now, so a tab reads
// as what it is: two objects, each captioned, side by side on the board.
//
// A tab's plates sit in a grid that lays two of them across a wide window and
// stacks them on a narrow one (.set-panel), which is how this page uses the
// width it has rather than leaving a column of white to the right of every
// field.
func plate(title, rows string) string { return plateWith(title, rows, "") }

// plateWith is a plate with something after its rows that is not a row: the
// Access picture, which is not a setting but the setting drawn. It sits outside
// .set-grid because it takes the plate's LEFTOVER height rather than a row's,
// which is the whole reason it is there.
func plateWith(title, rows, tail string) string {
	if rows == "" {
		return ""
	}
	return `<section class="panel set-plate"><h2>` + html.EscapeString(title) + `</h2>` +
		`<div class="set-grid">` + rows + `</div>` + tail + `</section>`
}

func chip(text, tone string) string {
	return `<span class="set-chip set-chip-` + tone + `">` + html.EscapeString(text) + `</span>`
}

// setSwitch asks one question — where is this registry reachable? — as a
// two-position switch that NAMES both answers, with the answer in force beside
// it. Exactly one answer is visible, and publishSwitchScript swaps them the
// instant the switch moves, so the consequence is on screen before anyone
// commits to it.
//
// It was a lone checkbox labelled "Global Access", which stated one position and
// left the other to be inferred: ticked meant the proxy, and unticked meant
// something the control never named. Both positions are written on it now —
// "Own address" on the left, "Global Access" on the right — so the question the
// switch settles is legible without moving it.
//
// The left position is named for the STATE it selects, not for the field it
// reveals. "External URL" was the name of a config setting (TACIT_EXTERNAL_URL),
// which made the two halves different kinds of thing: a capability on one side
// and an input on the other. The right half keeps "Global Access" because that
// is the env var, the guide's section title and the subject of the confirm gate,
// and because the switch is a BUNDLE — a proxy address and membership of the
// Public pool — that no address-shaped label could honestly name.
//
// The control is still ONE CHECKBOX under the paint. The two words are its track,
// the accent behind them is a thumb that slides between the two cells rather
// than a background handed from one word to the other, and the input is visually
// hidden but focusable — which keeps the form contract
// exactly as it was (handleSettingsSave reads presence, and an absent box is
// off) and keeps the keyboard behaviour the browser already gives a checkbox.
// aria-label carries the setting's name, because two visible words would
// otherwise be read out as one. That is also why the words appear TWICE in the
// markup: the thumb carries its own reversed-out copy and clips it, so every
// pixel it covers is reversed text and every pixel outside it is not, at any
// point in the travel. The whole track is aria-hidden, so the duplicate costs a
// reader nothing.
//
// Both answers are SIBLINGS of the <label> rather than children, and that is not
// cosmetic: a link or an input inside a label activates the label, so the
// address could be read but never opened and the field could not be typed in
// without flipping the switch.
//
// The switch is keyed by its own field NAME — data-switch="publish" — and every
// row that belongs to one of its positions carries the same key
// (onlyWhenSwitch). One script drives all of them. That generality is not
// speculative: sign-in is a second two-position question on this same page, and
// a script that knew one switch by name would have been copied to serve it.
//
// locked renders the state without offering the change. It is for a position
// this page cannot move: sign-in, once it is on, is turned off from the console
// (`tacit secure --off`), because a wrong provider is exactly the situation in
// which the dashboard cannot be reached. A disabled checkbox does not post,
// which is the same answer server-side — handleSettingsSave never reads the
// off direction.
func setSwitch(name, label, offLabel string, on, locked bool, whenOn, whenOff string) string {
	checked, hideOn, hideOff := "", " hidden", ""
	if on {
		checked, hideOn, hideOff = " checked", "", " hidden"
	}
	cls, dis := "set-seg", ""
	if locked {
		cls, dis = "set-seg set-seg-locked", " disabled"
	}
	return fmt.Sprintf(`<div class="set-row set-wide set-switch">`+
		`<label class="%s"><input type="checkbox" data-switch="%s" name="%s"%s%s aria-label="%s">`+
		`<span class="set-seg-track" aria-hidden="true">`+
		`<span class="set-seg-opt set-seg-off">%s</span>`+
		`<span class="set-seg-opt set-seg-on">%s</span>`+
		`<span class="set-seg-thumb"><span class="set-seg-mask">`+
		`<span class="set-seg-opt">%s</span><span class="set-seg-opt">%s</span>`+
		`</span></span></span></label>`+
		`<span class="set-addr" data-switch-on="%s"%s>%s</span>`+
		`<span class="set-alt" data-switch-off="%s"%s>%s</span></div>`,
		cls, name, name, checked, dis, html.EscapeString(label),
		html.EscapeString(offLabel), html.EscapeString(label),
		html.EscapeString(offLabel), html.EscapeString(label),
		name, hideOn, whenOn, name, hideOff, whenOff)
}

// setExternalURL is the operator's OWN address: always theirs, always editable,
// and always submitted even while it is out of sight, so that moving the switch
// back hands it straight over.
//
// It used to be a row of its own further down the panel, first as a disabled box
// showing the proxy's address and then as a field wearing a badge. Both put two
// rows on the page for one question. It is now what the switch's "Own address"
// position shows — and it carries no label of its own, because the position it
// belongs to is already named on the switch beside it.
func setExternalURL(value string) string {
	return fmt.Sprintf(`<input name="external_url" type="url" value="%s" `+
		`placeholder="https://tacit.example.com" aria-label="Own address">`,
		html.EscapeString(value))
}

// switchScript makes every two-position switch answer immediately: move it and
// the rows belonging to the position it left go, the rows belonging to the
// position it reached arrive. Both sides are rendered server-side and toggled
// with `hidden`, so the script builds no markup and the page is correct before
// it runs — and still correct if it never runs.
//
// A third kind of thing belongs to BOTH positions and only wants to know which
// one is in force — the Access picture, which moves between them rather than
// being swapped. Those carry data-switch-state and get the answer written onto
// them as data-on, which is what the stylesheet transitions against. Hiding is
// no use to something that has to travel.
//
// The fields it hides stay ENABLED, because a hidden input still posts and a
// disabled one does not: an operator who edits the address and then ticks the
// switch in one save must not lose the edit to the tick, and a save from the
// position that hides a toggle must not read as switching that toggle off.
const switchScript = `<script>(function(){
Array.prototype.forEach.call(document.querySelectorAll('[data-switch]'),function(cb){
  var key=cb.getAttribute('data-switch');
  var on=document.querySelectorAll('[data-switch-on="'+key+'"]');
  var off=document.querySelectorAll('[data-switch-off="'+key+'"]');
  var both=document.querySelectorAll('[data-switch-state="'+key+'"]');
  function sync(){
    Array.prototype.forEach.call(on,function(el){el.hidden=!cb.checked;});
    Array.prototype.forEach.call(off,function(el){el.hidden=cb.checked;});
    Array.prototype.forEach.call(both,function(el){
      el.setAttribute('data-on',cb.checked?'1':'0');});
  }
  cb.addEventListener('change',sync);sync();
});
})();</script>`

// onlyWhenSwitch marks a row as belonging to the ON half of the named switch. It
// is rendered either way and shown only while the switch is there, which is what
// lets switchScript reveal it without a round trip — the same arrangement the
// published address and the Own address field already use.
//
// Hidden, not disabled: a hidden checkbox still posts, and handleSettingsSave
// writes "0" for an absent one. A row that vanished from the form would switch
// its own setting off every time somebody saved from the other position.
func onlyWhenSwitch(name, row string, on bool) string {
	if row == "" {
		return ""
	}
	attr := ` data-switch-on="` + name + `"`
	if !on {
		attr += " hidden"
	}
	return strings.Replace(row, `class="`, attr+` class="`, 1)
}

// saveGuardScript keeps Save changes dead until something is actually different.
//
// One Save covers every tab, so "different" means different anywhere in the
// form — which is also the only honest reading: an unchanged save posts every
// field on every tab and rewrites registry.env with what is already in it.
//
// It compares each control against its OWN rendered value (defaultValue,
// defaultChecked) rather than against a snapshot taken on load. The two differ
// where it matters: modelCatalogScript fills a datalist and rewrites
// placeholders as the page settles, and a browser restoring form state after a
// back-navigation hands the page values the server never rendered. Comparing to
// the rendered value gets both right, and gets "typed it back to what it was"
// right for free.
//
// `_tab` is skipped. The tab strip is radio buttons inside this same form, so
// looking at another tab would otherwise read as an edit.
//
// So are BUTTONS, and that one is not cosmetic. The save button carries a name
// now (`do`, which says whether the press is a save or a test), and a button has
// no defaultValue at all — so the value comparison below read `'save' !==
// undefined` and reported the button itself as an edit, on every page, from the
// moment it loaded. The guard was dead until this line existed.
const saveGuardScript = `<script>(function(){
var form=document.querySelector('form.set');
var save=form&&form.querySelector('.set-save button');
if(!form||!save)return;
// What the LAST save did stops being the answer the moment anything differs
// from it. It goes while the form is dirty and comes back if the operator puts
// the value back — the same test the button runs, so the two never disagree.
var done=form.querySelector('.set-saved');
function changed(el){
  if(!el.name||el.name==='_tab')return false;
  if(el.type==='submit'||el.type==='button'||el.type==='reset')return false;
  if(el.type==='checkbox'||el.type==='radio')return el.checked!==el.defaultChecked;
  if(el.tagName==='SELECT'){
    for(var i=0;i<el.options.length;i++){
      if(el.options[i].selected!==el.options[i].defaultSelected)return true;
    }
    return false;
  }
  return el.value!==el.defaultValue;
}
function sync(){
  var dirty=Array.prototype.some.call(form.elements,changed);
  save.disabled=!dirty;
  if(done)done.hidden=dirty;
}
form.addEventListener('input',sync);
form.addEventListener('change',sync);
sync();
})();</script>`

func setToggle(name, label, desc string, on bool, badge string) string {
	checked := ""
	if on {
		checked = " checked"
	}
	return fmt.Sprintf(`<label class="set-row set-wide set-toggle">`+
		`<input type="checkbox" name="%s"%s>%s</label>`,
		name, checked, labelCell(label, desc, badge))
}

// labelCell is a label, what it takes to change it, and one line saying what it
// does — one grid cell, so the line sits under the label rather than in the
// column where the values are.
func labelCell(label, desc, badge string) string {
	out := `<span class="set-text"><span class="set-label">` +
		html.EscapeString(label) + badgeHTML(badge) + `</span>`
	if desc != "" {
		out += `<span class="set-desc">` + html.EscapeString(desc) + `</span>`
	}
	return out + `</span>`
}

func setText(name, label, value, placeholder string, wide bool) string {
	return inputRow(name, label, "text", value, placeholder, wide, "")
}

func setURL(name, label, value, placeholder string, wide bool) string {
	return inputRow(name, label, "url", value, placeholder, wide, "")
}

func setPassword(name, label, placeholder string) string {
	return fmt.Sprintf(`<label class="set-row set-wide"><span class="set-label">%s</span>`+
		`<input name="%s" type="password" placeholder="%s" autocomplete="off"></label>`,
		html.EscapeString(label), name, html.EscapeString(placeholder))
}

func setSelect(name, label, optionsHTML string) string {
	return fmt.Sprintf(`<label class="set-row"><span class="set-label">%s</span>`+
		`<select name="%s">%s</select></label>`, html.EscapeString(label), name, optionsHTML)
}

// setCombo is free text with the provider's live catalogue offered: model ids
// drift, so a custom one has to stay typable.
func setCombo(name, label, value, placeholder string) string {
	return fmt.Sprintf(`<label class="set-row"><span class="set-label">%s</span>`+
		`<input name="%s" list="model-catalog" value="%s" placeholder="%s">`+
		`<small class="set-note model-desc"></small></label>`,
		html.EscapeString(label), name, html.EscapeString(value), html.EscapeString(placeholder))
}

func inputRow(name, label, kind, value, placeholder string, wide bool, badge string) string {
	cls := "set-row"
	if wide {
		cls += " set-wide"
	}
	return fmt.Sprintf(`<label class="%s"><span class="set-label">%s%s</span>`+
		`<input name="%s" type="%s" value="%s" placeholder="%s"></label>`,
		cls, html.EscapeString(label), badgeHTML(badge), name, kind,
		html.EscapeString(value), html.EscapeString(placeholder))
}

// setDisabled is a value another setting is deciding: readable, not editable, and
// saying which setting decided it.
func setDisabled(label, value, why string) string {
	return fmt.Sprintf(`<div class="set-row set-wide"><span class="set-label">%s%s</span>`+
		`<input type="text" value="%s" disabled></div>`,
		html.EscapeString(label), badgeHTML(why), html.EscapeString(value))
}

// setFact is a read-only row: the label, the effective value, and what it takes
// to change it.
func setFact(label, value, badge string) string {
	return setFactDesc(label, value, "", badge)
}

// setFactDesc is a fact that carries the same one-line description its editable
// twin does, so a member without the controls reads the same page.
func setFactDesc(label, value, desc, badge string) string {
	return fmt.Sprintf(`<div class="set-row set-fact">%s<span class="set-value">%s</span></div>`,
		labelCell(label, desc, badge), html.EscapeString(value))
}

func setNote(htmlText string) string {
	return `<p class="set-row set-wide set-note">` + htmlText + `</p>`
}

func setWarn(htmlText string) string {
	return `<p class="set-row set-wide set-warn">` + htmlText + `</p>`
}

func badgeHTML(badge string) string {
	if badge == "" {
		return ""
	}
	cls := "set-badge"
	// A badge that carries a value rather than a label keeps its own casing.
	if strings.ContainsAny(badge, "0123456789") {
		cls += " set-badge-val"
	}
	return ` <span class="` + cls + `">` + html.EscapeString(badge) + `</span>`
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// maskSecret shows enough of a secret to recognize it and not enough to use it.
func maskSecret(v string) string {
	switch {
	case v == "":
		return "—"
	case len(v) > 6:
		return v[:4] + "…"
	default:
		return "•••"
	}
}

// effectiveExternalURL is the address actually in force, which is the proxy's
// while Global Access is on.
func (s *Server) effectiveExternalURL() string {
	if base := s.PublishedBase(); base != "" {
		return base
	}
	return s.cfg().ExternalURL
}
