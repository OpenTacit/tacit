// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/ui"
)

// teamPanelHTML renders the invite panel of a single-member registry.
func teamPanelHTML(t *testing.T) string {
	t.Helper()
	srv, _ := newServer(t)
	srv.Cfg.AuthMode = "owner"
	srv.Cfg.OwnerSecret = "s"
	return srv.openToTeamPanel(ownerRequest(srv))
}

// The panel's one irreversible control opens it rather than closing it: on the
// heading line, before the switch, the drawing and the five bullets, and filled
// rather than outlined so it does not read as one more hairline plate.
func TestOpenToTeamButtonLeadsThePanelAndIsFilled(t *testing.T) {
	html := teamPanelHTML(t)
	if !strings.Contains(html, `class="btn btn-primary"`) {
		t.Error("the open control is not the filled button")
	}
	if !strings.Contains(html, `class="section-head head-act"`) {
		t.Error("the open control is not on the heading line")
	}
	btn := strings.Index(html, "Open this registry to my team")
	head := strings.Index(html, "What changes when you invite somebody")
	bullets := strings.Index(html, `<ul class="feed">`)
	if btn < 0 || head < 0 || bullets < 0 {
		t.Fatalf("panel is missing a piece: btn=%d head=%d bullets=%d", btn, head, bullets)
	}
	if !(head < btn && btn < bullets) {
		t.Errorf("the control does not lead the panel: head=%d btn=%d bullets=%d", head, btn, bullets)
	}
}

// THE PLAYBOOK DOES NOT MOVE, and that is the whole argument the page is making
// — "nothing moves", "the address does not change". Not one coordinate under the
// playbook or its label may differ between the two positions; the moment one
// does, the picture is arguing against the sentences beside it.
func TestTeamScenePlaybookIsFixedInBothPositions(t *testing.T) {
	block := appCSS[strings.Index(appCSS, "/* ---- the Team picture"):]
	block = block[:strings.Index(block, "/* ---- prose inside a plate")]
	for _, fixed := range []string{".tm-book", ".tm-tag-book"} {
		if regexp.MustCompile(regexp.QuoteMeta(`.tm-stage[data-on="1"] `) + `[^{]*` +
			regexp.QuoteMeta(fixed)).MatchString(block) {
			t.Errorf("%s moves when the switch does; the page's claim is that it does not", fixed)
		}
	}
	// And the member's own reach is unchanged too: they get at it afterwards
	// exactly as they did before.
	if strings.Contains(block, `.tm-stage[data-on="1"] .tm-you`) {
		t.Error("the member's own line changes; opening up does not change how they reach it")
	}
}

// The four who are not here yet have NO LENGTH until the switch moves, so they
// grow out of the playbook rather than appearing beside it — the order of events
// when somebody accepts an invitation.
func TestTeamSceneGrowsTheTeamRatherThanRevealingIt(t *testing.T) {
	block := appCSS[strings.Index(appCSS, "/* ---- the Team picture"):]
	block = block[:strings.Index(block, "/* ---- prose inside a plate")]
	// The coordinates themselves belong to the picture and move when it is
	// recomposed; what may never change is that the four start with no length
	// and no opacity, so they GROW when the switch is thrown.
	if !regexp.MustCompile(`\.tm-mate\{[^}]*--len:0em;opacity:0\}`).MatchString(block) {
		t.Error("the team is drawn before it exists")
	}
	for i := 1; i <= 4; i++ {
		if !strings.Contains(block, `.tm-stage[data-on="1"] .tm-m`+string(rune('0'+i))+`{--len:`) {
			t.Errorf("mate %d never reaches the playbook", i)
		}
	}
}

// The count is REAL — "your 30 techniques become theirs" is a promise about a
// number the member can check — and there is only one of it. A second number for
// "after" would be inventing the size of a team that does not exist yet.
func TestTeamSceneCarriesTheRealCountAndInventsNoOther(t *testing.T) {
	s := teamScene(30)
	if !strings.Contains(s, "<b>30 techniques</b>") {
		t.Error("the picture does not carry the registry's own technique count")
	}
	if strings.Count(s, "techniques") != 1 {
		t.Error("a second count; the number after is the number before")
	}
	// Nothing to count is not zero techniques — it is a registry that has not
	// been used yet, and the label says so by naming the thing instead.
	if empty := teamScene(0); strings.Contains(empty, "0 technique") {
		t.Error("the picture reports a zero it did not measure")
	}
}

// The switch is a PREVIEW. It sits outside the panel's one form, so neither
// position posts anything: nothing on this page is saved until the button is.
func TestTeamPreviewSwitchSavesNothing(t *testing.T) {
	panel := teamPanelHTML(t)
	// From the form's opening tag to its CLOSING one. Slicing to the end of the
	// panel worked only while the form was the last thing in it; the button
	// leads the panel now, so everything else on the page sat in that slice.
	open := strings.Index(panel, `<form method="post" action="/members/open">`)
	form := panel[open : strings.Index(panel[open:], "</form>")+open]
	if strings.Contains(form, `data-switch="teampreview"`) {
		t.Error("the preview switch is inside the form that opens the registry")
	}
	if !strings.Contains(panel, `data-switch="teampreview"`) {
		t.Fatal("there is no preview switch")
	}
	// And it needs the script, or it is a control that does nothing.
	if !strings.Contains(panel, `[data-switch-state="`) {
		t.Error("the page does not carry switchScript; the switch would be inert")
	}
	// Rendered in today's position, so a page with no script shows what is true
	// rather than a preview of what is not.
	scene := panel[strings.Index(panel, "tm-stage"):]
	if !strings.Contains(scene[:120], `data-on="0"`) {
		t.Error("the picture is served in the team position; today it is not a team")
	}
}

// Built from the shared diagram vocabulary, setting nothing of its own.
func TestTeamSceneCarriesNoStylingOfItsOwn(t *testing.T) {
	s := teamScene(4)
	if strings.Contains(s, "style=") {
		t.Error("the picture sets style inline; geometry and colour live in app.css")
	}
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(s) {
		t.Error("the picture names a colour; colour resolves from a token")
	}
	if !strings.Contains(s, ui.MarkSVG) {
		t.Error("the playbook does not wear the mark; it is the member's own registry")
	}
	for _, shared := range []string{"dgm-scene", "dgm-node", "dgm-fence", "dgm-beam", "dgm-tag"} {
		if !strings.Contains(s, shared) {
			t.Errorf("the picture does not use the shared %s; a copy would drift", shared)
		}
	}
}

// A switch outside a plate must still hide its off half. An author display value
// defeats [hidden]{display:none}, and both halves are inline-flex — inside a
// plate .set-grid catches it, and anywhere else the page showed BOTH answers.
func TestSwitchHidesItsOffHalfWhereverItIsPut(t *testing.T) {
	if !strings.Contains(appCSS, ".set-switch [hidden]{display:none}") {
		t.Error("a switch outside .set-grid will show both of its answers at once")
	}
}

// The contribute form's input carried size="48", which is a floor rather than a
// width: below about 410px it stops shrinking, and /team scrolled sideways on a
// phone — which is exactly where somebody pastes a join link they were sent. The
// class was on the markup and styled nowhere. Measured before and after: 444/390,
// then 390/390.
func TestContributeFormFitsAPhone(t *testing.T) {
	if !strings.Contains(appCSS, ".merge-form input[type=text]{flex:1 1 16rem;min-width:0;") {
		t.Error("the join-link box cannot shrink below its size attribute")
	}
}
