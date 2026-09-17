// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/llmprovider"
)

// adminSettingsHTML signs in as an admin and returns the settings page.
func adminSettingsHTML(t *testing.T) string {
	t.Helper()
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")
	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// The copy rule: a settings page is not documentation. Anything the operator
// cannot act on was cut, so these specific phrases must not come back.
func TestSettingsCarriesNoUnactionableCopy(t *testing.T) {
	page := adminSettingsHTML(t)
	for _, gone := range []string{
		"TACIT_LLM_API_KEY",         // an env var name is not an action
		"(a URI)",                   // type=url and a placeholder say this
		"registry-side features",    // the section heading says it
		"provider default if blank", // the placeholder IS the default
		"comma-separated",           // the placeholder shows the shape
		"0–1",                       // min/max on the control say it
		"n behind the rate",         // the label says it
		"These settings apply immediately",
		"no inbound port", // the bargain already says "no open port"
		"import receipts", // the operator cannot act on the wire format
		"Proxy address",   // deployment, not a setting: registry.env and a restart
	} {
		if strings.Contains(page, gone) {
			t.Errorf("settings page still carries unactionable copy: %q", gone)
		}
	}
}

// Defaults and formats belong in placeholders, where they are visible without
// being read as prose.
func TestSettingsPutsDefaultsInPlaceholders(t *testing.T) {
	page := adminSettingsHTML(t)
	for _, want := range []string{
		`placeholder="you@example.com"`,           // that a row holds one address, yours first
		`placeholder="https://tacit.example.com"`, // that an address is a URL
		`placeholder="https://api.anthropic.com"`, // the provider's own base URL
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing placeholder %q — the format has nowhere else to live", want)
		}
	}
}

// A constraint the control cannot express itself stays: these need a restart, and
// nothing on the row would otherwise say so.
func TestSettingsMarksWhatNeedsARestart(t *testing.T) {
	page := adminSettingsHTML(t)
	if n := strings.Count(page, "set-badge"); n < 2 {
		t.Errorf("only %d badges; the restart-only rows should be marked", n)
	}
	if !strings.Contains(page, "restart") {
		t.Error("nothing on the page says which settings need a restart")
	}
	// And the deployment facts are not offered as editable controls.
	for _, notEditable := range []string{`name="host"`, `name="port"`, `name="data_dir"`, `name="embed_model"`} {
		if strings.Contains(page, notEditable) {
			t.Errorf("%s is rendered as a control but cannot be hot-applied", notEditable)
		}
	}
}

// Toggles say what they do, so they need no hint beside them.
func TestSettingsTogglesAreSelfDescribing(t *testing.T) {
	page := adminSettingsHTML(t)
	for _, want := range []string{
		"Apply proven techniques automatically for agents",
		"Evaluate new machine techniques without review",
		"Serve what passes evaluation without review",
		"Discover techniques from usage",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("toggle label %q is missing — the label is what replaced the hint", want)
		}
	}
}

// Every automation switch carries its one line, and BOTH views carry the same
// one: the admin weighing the switch and the member reading the page read the
// same sentence about it.
func TestAutomationSwitchesCarryTheirDescription(t *testing.T) {
	page := adminSettingsHTML(t)
	for _, want := range []string{
		"Autonomous sessions only. A person at a keyboard still sees the suggestion.",
		"Checks new drafts for fit before adding them to the review queue.",
		"Promotes a technique when its fit evidence meets the set limits.",
		"Each recompute cycle, clusters the moves that worked into candidates.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the admin view is missing the line for a switch: %q", want)
		}
	}
	// The read-only view renders facts, not toggles, and says the same things.
	srv := &Server{}
	facts := srv.sectionAutomation(false)
	if !strings.Contains(facts, "Promotes a technique when its fit evidence meets the set limits.") {
		t.Errorf("a member without the controls reads a different page:\n%s", facts)
	}
}

// Every setting the POST handler reads has to exist as a control, or an admin
// cannot set it. This is the guard that a restructure did not silently drop one.
func TestSettingsRendersEverySavedField(t *testing.T) {
	page := adminSettingsHTML(t)
	for _, name := range []string{
		"csrf", "external_url", "publish",
		"admin_emails",
		"autonomy", "auto_shadow", "auto_promote",
		"auto_discover", "llm_key", "llm_provider", "llm_base_url",
		"suggest_model", "tagmerge_model",
	} {
		if !strings.Contains(page, `name="`+name+`"`) {
			t.Errorf("no control for %q, which handleSettingsSave reads", name)
		}
	}
	// And the other direction, which is the one that loses data: a setting the
	// page no longer offers must not still be WRITTEN by the save, or an absent
	// field reads back as "" and clears the operator's line on the next save of
	// anything at all.
	for _, gone := range []string{"publish_ingress", "public_min_n", "feed_provider_name", "feed_provider_id"} {
		if strings.Contains(page, `name="`+gone+`"`) {
			t.Errorf("%q is offered as a control again; it belongs in registry.env", gone)
		}
		if strings.Contains(settingsSaveSource(t), `updates["TACIT_`+strings.ToUpper(gone)+`"] = `) {
			t.Errorf("handleSettingsSave still writes %q, which the page no longer submits", gone)
		}
	}
}

// settingsSaveSource reads the save handler, so the guard above can assert about
// what it writes rather than only about what the page renders. The hazard is
// exactly the gap between those two.
func settingsSaveSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("settings.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Save changes must arrive ENABLED and be switched off by the script, never the
// other way round. A page whose script fails to load is then a page you can
// still save from; the reverse is a settings page that cannot be used at all.
func TestSaveButtonIsLiveWithoutTheScript(t *testing.T) {
	page := adminSettingsHTML(t)
	// It carries which job it is doing (save, or test the sign-in about to be
	// configured) and nothing else: no disabled attribute to undo.
	if !strings.Contains(page, `<button type="submit" name="do" value="save">Save changes</button>`) {
		t.Fatal("the save button is not rendered plain and enabled")
	}
	if strings.Contains(page, "<button type=\"submit\" disabled") {
		t.Error("the save button is rendered disabled, so a page without JavaScript cannot save")
	}
	if !strings.Contains(page, "save.disabled=!dirty") {
		t.Error("nothing switches the save button off once the page is live")
	}
	// "Save complete" answers a question about the state the page was rendered
	// in. Edit anything and it is no longer the answer, so the same dirty test
	// that lights the button clears the message — one check, never two verdicts.
	if !strings.Contains(page, "done.hidden=dirty") {
		t.Error("the saved message survives an edit, so it can contradict the live button")
	}
	// The tab strip is radios in this same form. Looking at another tab is not
	// an edit, and counting it as one would leave Save live on every page load.
	if !strings.Contains(page, `el.name==='_tab'`) {
		t.Error("the change check does not skip the tab radios")
	}
}

// Tabs must not become three screens. Every panel stays in the DOM and inside the
// form, because handleSettingsSave writes "0" for an absent checkbox — so a tab
// that posted only its own fields would silently switch off every toggle on the
// tabs you were not looking at.
func TestSettingsTabsKeepEveryPanelInTheForm(t *testing.T) {
	page := adminSettingsHTML(t)
	for _, id := range []string{"set-access", "set-automation", "set-deployment"} {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("panel %s is absent — its fields would not be submitted", id)
		}
	}
	if n := strings.Count(page, `class="set-panel"`); n != 3 {
		t.Errorf("%d panels rendered, want all 3 present at once", n)
	}
	// One form, one save: the panels are inside it.
	if n := strings.Count(page, `<form class="set"`); n != 1 {
		t.Errorf("%d forms; the tabs must share one so a single Save covers them", n)
	}
	// The tab radios must not shadow a setting the handler reads.
	if strings.Count(page, `name="_tab"`) != 3 {
		t.Error("expected three tab radios driving the panels")
	}
	for _, saved := range []string{"publish", "autonomy", "llm_provider"} {
		if strings.Contains(page, `id="tab-`+saved+`"`) {
			t.Errorf("tab id collides with the saved field %q", saved)
		}
	}
	// Three tabs, and the state rides them so the overview survives being split.
	if n := strings.Count(page, `class="set-tab"`); n != 3 {
		t.Errorf("%d tabs, want 3", n)
	}
	if n := strings.Count(page, "set-chip"); n < 5 {
		t.Errorf("only %d chips on the strip; each subject should carry its state", n)
	}
}

// Merging two subjects onto one tab must not lose either of them. Every subject
// is its own captioned plate, and BOTH subjects keep their chip on the strip —
// otherwise consolidating the tabs would buy a shorter strip by hiding the state
// it exists to show.
//
// The plate is also what makes this page look like the rest of the registry: a
// subject is a flat hairline-bordered panel with a quiet caption, the same
// object Team and Outcomes are built from. Settings used to be the one view
// whose content sat on the bare plane.
func TestMergedTabsNameBothSubjectsAndKeepBothChips(t *testing.T) {
	page := adminSettingsHTML(t)
	for _, want := range []string{"Access and sign-in", "Automation and model", "Deployment"} {
		if !strings.Contains(page, want) {
			t.Errorf("tab strip is missing %q", want)
		}
	}
	// Every subject is its own captioned plate, so the one the tab title does not
	// name is still named where it starts.
	for _, want := range []string{"Access", "Sign-in", "Automation", "Model", "Deployment"} {
		if !strings.Contains(page, `<section class="panel set-plate"><h2>`+want+`</h2>`) {
			t.Errorf("no plate captioned %q; a subject without a caption is unnamed on its tab", want)
		}
	}
	// The fields of the folded-in subjects are on the tab that absorbed them.
	access := panelHTML(t, page, "set-access")
	if !strings.Contains(access, `name="admin_emails"`) || !strings.Contains(access, `name="publish"`) {
		t.Error("the Access tab should hold both the reach-it and the sign-in controls")
	}
	automation := panelHTML(t, page, "set-automation")
	if !strings.Contains(automation, `name="autonomy"`) || !strings.Contains(automation, `name="llm_provider"`) {
		t.Error("the Automation tab should hold both the toggles and the model")
	}
	// Two subjects on a tab means two chips on it: the strip still answers
	// "is automation on?" and "is there a key?" without opening anything.
	if n := strings.Count(page, "set-chip"); n < 5 {
		t.Errorf("%d chips for five subjects; a merged tab must wear both", n)
	}
}

// panelHTML is the body of one settings panel, so a test can ask which tab a
// field landed on rather than only whether the page has it somewhere.
//
// It counts <section> depth rather than stopping at the first close tag: a tab
// body holds one captioned plate per subject, each a <section> of its own, so
// the first </section> is the end of the first PLATE and taking it as the end of
// the tab hid every subject after the first from these tests.
func panelHTML(t *testing.T, page, id string) string {
	t.Helper()
	start := strings.Index(page, `id="`+id+`"`)
	if start < 0 {
		t.Fatalf("panel %s not in the page", id)
	}
	rest := page[start:]
	depth := 1
	for i := 0; i < len(rest); {
		open, close := strings.Index(rest[i:], "<section"), strings.Index(rest[i:], "</section>")
		if close < 0 {
			t.Fatalf("panel %s is not closed", id)
		}
		if open >= 0 && open < close {
			depth++
			i += open + len("<section")
			continue
		}
		depth--
		if depth == 0 {
			return rest[:i+close]
		}
		i += close + len("</section>")
	}
	t.Fatalf("panel %s is not closed", id)
	return ""
}

// The hazard the tabs introduce, tested end to end: a save from the page must
// keep the toggles that live under other tabs.
func TestSavingFromOneTabKeepsOtherTabsSettings(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	srv.Cfg.AutonomyEnabled, srv.Cfg.AutoShadow = true, true
	admin := signIn(t, srv, "ops@example.com")

	page := ""
	if resp, err := admin.Get(ts.URL + "/settings"); err == nil {
		page = readBody(t, resp)
	} else {
		t.Fatal(err)
	}
	csrf := valueOf(t, page, "csrf")

	// Post what a browser would from the Access tab: every field in the form,
	// including the checked toggles sitting in hidden panels.
	form := url.Values{
		"csrf": {csrf}, "_tab": {"access"},
		"external_url": {"https://tacit.example.com"},
		"autonomy":     {"on"}, "auto_shadow": {"on"},
	}
	resp, err := admin.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if !srv.Cfg.AutonomyEnabled {
		t.Error("autonomy was switched off by a save made from another tab")
	}
	if !srv.Cfg.AutoShadow {
		t.Error("auto-shadow was switched off by a save made from another tab")
	}
	if srv.Cfg.ExternalURL != "https://tacit.example.com" {
		t.Errorf("external URL = %q, want the saved value", srv.Cfg.ExternalURL)
	}
}

// valueOf pulls a hidden input's value out of rendered HTML.
func valueOf(t *testing.T, page, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("no %s field in the page", name)
	}
	rest := page[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

// Settings uses the width the same way every other view does: the standard
// 1320px wrap, filled. It used to narrow the wrap (wrap-narrow) and centre the
// result, which put its title and its right-hand edge somewhere no other view's
// were. Both halves are asserted — the page's wrap, and the column inside it,
// since capping .set would left-align the page without widening it.
func TestSettingsUsesTheFullWidthLikeOtherViews(t *testing.T) {
	_, ts := newServer(t)
	_, page := fetchHTML(t, ts.URL+"/settings")
	if strings.Contains(page, "wrap-narrow") {
		t.Error("the settings page narrows its wrap, so its title misaligns with every other view")
	}
	_, fed := fetchHTML(t, ts.URL+"/federation")
	if strings.Contains(fed, "wrap-narrow") {
		t.Error("federation should keep the wide wrap")
	}
	_, css := fetchHTML(t, ts.URL+"/assets/app.css")
	i := strings.Index(css, ".set{")
	if i < 0 {
		t.Fatal("no .set rule in the stylesheet")
	}
	if rule := css[i : i+strings.Index(css[i:], "}")]; strings.Contains(rule, "max-width") {
		t.Errorf("the settings column caps its own width (%s), so the page does not fill the wrap", rule)
	}
}

// The administrators field is a list, and a list punctuated inside one text box
// is a format the operator has to guess at. One row per address, each with its
// own Remove, and a blank row at the end so the list can be added to with no
// script at all.
func TestAdministratorsAreOneFieldPerAddress(t *testing.T) {
	page := adminSettingsHTML(t) // one administrator: ops@example.com
	if n := strings.Count(page, `name="admin_emails"`); n != 2 {
		t.Errorf("%d administrator fields, want 2 (the address, and a blank row to add another)", n)
	}
	if !strings.Contains(page, `value="ops@example.com"`) {
		t.Error("the stored administrator is not in a field of its own")
	}
	if n := strings.Count(page, `class="set-email-del"`); n != 2 {
		t.Errorf("%d Remove buttons, want one per row", n)
	}
	// Rendered hidden: without a script, Remove would be a button that does
	// nothing, and clearing the box is what removes an address instead.
	if !strings.Contains(page, `class="set-email-del" hidden`) {
		t.Error("Remove is offered before the script that makes it work")
	}
	// The comma list is gone from the placeholder too — it was the only place the
	// old format was ever explained.
	if strings.Contains(page, "you@example.com, lead@example.com") {
		t.Error("the comma-separated format is still being taught")
	}
}

// saveGuardScript keeps Save dead until something differs from what was
// rendered, and it walks the form's controls to decide. The save button is one
// of those controls — it carries a name — and a button has no defaultValue, so
// without this skip the guard reported the button as an edit and Save was live
// on every page from the moment it loaded.
func TestSaveGuardIgnoresItsOwnButton(t *testing.T) {
	if !strings.Contains(saveGuardScript, "el.type==='submit'") {
		t.Fatal("the dirty check does not skip buttons, so Save is never disabled")
	}
	page := adminSettingsHTML(t)
	if !strings.Contains(page, `<button type="submit" name="do"`) {
		t.Fatal("the save button no longer carries a name; re-check what the guard must skip")
	}
}

// The Base URL field carries the address IN FORCE, not the operator's override
// with the real answer hidden in a placeholder.
//
// A placeholder is visible only while the box is empty, so the two drifted apart
// the moment anything was saved: the box kept whatever was stored — including
// the default of a provider the registry had since moved off — while the
// placeholder showed the current provider's, and it was the box that got saved
// and called. What the field shows is now what llmprovider.BaseURLFor resolves
// for the researcher and the auditor.
func TestBaseURLFieldShowsTheAddressInForce(t *testing.T) {
	t.Setenv("TACIT_LLM_PROVIDER", "openai")
	t.Setenv("TACIT_LLM_BASE_URL", "") // nothing configured: the provider's default
	want := llmprovider.Lookup("openai").BaseURL
	page := adminSettingsHTML(t)
	field := fieldHTML(t, page, "llm_base_url")
	if !strings.Contains(field, `value="`+want+`"`) {
		t.Errorf("Base URL field does not carry the address in force (%s): %s", want, field)
	}

	// An operator's own gateway still wins, and is still what the field shows.
	t.Setenv("TACIT_LLM_BASE_URL", "https://gateway.internal/v1")
	field = fieldHTML(t, adminSettingsHTML(t), "llm_base_url")
	if !strings.Contains(field, `value="https://gateway.internal/v1"`) {
		t.Errorf("a configured base must be what the field shows: %s", field)
	}
}

// fieldHTML is one <input> tag from the rendered page, by field name.
func fieldHTML(t *testing.T, page, name string) string {
	t.Helper()
	i := strings.Index(page, `<input name="`+name+`"`)
	if i < 0 {
		t.Fatalf("no %s field in the page", name)
	}
	return page[i : i+strings.Index(page[i:], ">")+1]
}

// The plates have to shrink to a phone. A grid track of minmax(30rem,1fr) never
// goes below 30rem, so on a 390px screen the plates stood 480px wide and the
// whole page scrolled sideways — the one thing the house rules say a page must
// never do. min(30rem,100%) is what makes the "one plate across" case real.
func TestSettingsPlatesShrinkToAPhone(t *testing.T) {
	_, ts := newServer(t)
	_, css := fetchHTML(t, ts.URL+"/assets/app.css")
	i := strings.Index(css, "#tab-access:checked ~ #set-access")
	if i < 0 {
		t.Fatal("no rule showing the access tab's plates")
	}
	rule := css[i : i+strings.Index(css[i:], "}")]
	if !strings.Contains(rule, "minmax(min(") {
		t.Errorf("the plate grid cannot shrink below its track minimum, so a phone scrolls sideways: %s", rule)
	}
}

// setRule returns one rule's body from the served stylesheet.
func setRule(t *testing.T, sel string) string {
	t.Helper()
	i := strings.Index(appCSS, sel+"{")
	if i < 0 {
		t.Fatalf("the stylesheet has no rule for %s", sel)
	}
	body := appCSS[i+len(sel)+1:]
	return body[:strings.Index(body, "}")]
}

// The one line under a setting saying what it does is the QUIETEST thing in its
// row. It had no rule of its own at all, so it inherited the body's 14px in full
// --ink and came out louder than the 12.5px --muted label it explains: on the
// Automation plate the consequence read before the setting.
func TestSettingsDescriptionIsQuieterThanTheLabelItExplains(t *testing.T) {
	desc := setRule(t, ".set-desc")
	label := setRule(t, ".set-label")
	for _, want := range []string{"font-size:12px", "color:var(--muted)"} {
		if !strings.Contains(desc, want) {
			t.Errorf(".set-desc is missing %s, so it out-shouts the label above it", want)
		}
	}
	// The label is 12.5px --ink-2; the line under it must not be bigger or darker.
	if !strings.Contains(label, "12.5px") || !strings.Contains(label, "var(--ink-2)") {
		t.Fatal("the label's size or colour moved; .set-desc was set relative to it")
	}
}

// A fact whose label is a sentence keeps its narrow value column on a WIDE
// window only. The rule is two selectors deep and .set-row is one, and a media
// query adds no specificity — so the phone's stacking rule never won it, and the
// four automation facts held a 5rem value column at 390px, wrapping their labels
// mid-phrase beside a three-character "off". That is the squeeze the rule was
// written to fix, reproduced one breakpoint down.
func TestSettingsFactWithADescriptionStacksOnAPhone(t *testing.T) {
	// EVERY block at this breakpoint, not the first one. The stylesheet has
	// carried more than one for a while — the Review picture hands its narrow
	// column to the vertical drawing here too — and reading whichever happened
	// to come first made this test a statement about file order.
	const marker = "@media (max-width:760px){"
	found := false
	for i := strings.Index(appCSS, marker); i >= 0; {
		block := appCSS[i+len(marker):]
		if end := strings.Index(block, "\n}"); end >= 0 {
			block = block[:end]
		}
		if strings.Contains(block, ".set-fact:has(.set-desc){grid-template-columns:minmax(0,1fr)}") {
			found = true
			break
		}
		next := strings.Index(appCSS[i+len(marker):], marker)
		if next < 0 {
			break
		}
		i += len(marker) + next
	}
	if !found {
		t.Error("no phone block restates .set-fact:has(.set-desc); " +
			"the wide-window rule outranks .set-row and keeps a 5rem value column at 390px")
	}
}

// A class the page no longer emits still reads as live to whoever edits the
// block next. Four rules for .set-sub outlived the thresholds they styled, and
// one of them was ADDED after the last emitter had gone.
func TestSettingsStylesheetStylesNothingThePageStoppedDrawing(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	// A class may be built by concatenation — chip() writes `set-chip-` and adds
	// the tone — so a prefix that ends a string literal counts as emitting it.
	emitted := func(cls string) bool {
		if strings.Contains(src.String(), cls) {
			return true
		}
		for i := len(cls) - 1; i > 0; i-- {
			if cls[i] == '-' && (strings.Contains(src.String(), cls[:i+1]+"`") ||
				strings.Contains(src.String(), cls[:i+1]+`"`)) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`\.(set-[a-z0-9-]+)`).FindAllStringSubmatch(appCSS, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		if !emitted(m[1]) {
			t.Errorf("the stylesheet styles .%s, which nothing in this package draws", m[1])
		}
	}
}

// THE SAVE BAR ENDS THE PAGE. Its own padding is even, so the button sits square
// in it — but under that came the form's rem and the 4rem of ground every other
// page leaves below its last plate, and scrolled to the foot of a long page the
// bar lifted off the bottom of the window and left the button in five times the
// air it had above it.
//
// The page ends flush under the bar instead, which also makes the two states one
// state: stuck to the foot of the window the bar shows this padding under the
// button, and now so does the end of the page. Scoped by the bar being there —
// a reader who cannot save has no bar, and their page keeps its ground.
func TestSettingsSaveBarEndsThePage(t *testing.T) {
	if bar := setRule(t, ".set-save"); !strings.Contains(bar, "padding:.85rem 0") {
		t.Errorf("the bar's padding is no longer even, so the button sits off-centre in it: %s", bar)
	}
	if !strings.Contains(appCSS, ".wrap:has(.set-save),.set:has(.set-save){padding-bottom:0}") {
		t.Error("the page still leaves its own ground under the save bar")
	}
	// Every other page keeps it: the bar is what makes this one different, and a
	// page that ends flush under its last plate is a page ending in a hairline.
	if !strings.Contains(setRule(t, ".wrap"), "4rem") {
		t.Error("the ground under the last plate is gone from every page, not just the one with a save bar")
	}
}
