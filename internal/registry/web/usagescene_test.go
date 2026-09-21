// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/ui"
)

// signedInPage fetches one page as a signed-in admin.
func signedInPage(t *testing.T, path string) string {
	t.Helper()
	srv, ts := newServer(t)
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")
	resp, err := admin.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// The picture is SERVER-RENDERED, unlike the rest of this page. The dashboard
// below it is built in the browser, so with no script the page was blank — and
// the claim it makes is the product's trust precondition, which is the last
// thing that should depend on a script arriving.
func TestUsageSceneIsOnThePageBeforeAnyScriptRuns(t *testing.T) {
	page := signedInPage(t, "/usage")
	if !strings.Contains(page, `class="dgm usg-stage"`) {
		t.Fatal("the Usage picture is not in the served HTML")
	}
	root := strings.Index(page, `id="usage-root"`)
	scene := strings.Index(page, `class="dgm usg-stage"`)
	if root < scene {
		t.Error("the picture is inside the script-built root; it must precede it")
	}
}

// THE CLAIM IS STRUCTURAL. Everything personal is inside the boundary and
// exactly one path crosses it, labelled for what it carries. Nothing blocked is
// drawn: nothing is blocked, and a barred arrow would invent a thing that tries
// and fails.
func TestUsageSceneCrossesTheBoundaryExactlyOnce(t *testing.T) {
	s := usageScene()
	if n := strings.Count(s, "dgm-port"); n != 1 {
		t.Errorf("%d ports through the wall; the promise is that there is one", n)
	}
	if strings.Contains(s, "dgm-bars") {
		t.Error("the crossing is drawn gated; nothing is needed to send a count")
	}
	if n := strings.Count(s, "dgm-fence"); n != 1 {
		t.Errorf("%d boundaries; the picture is about one machine", n)
	}
	// The three sessions write INWARD, to the log. A session beam pointing the
	// other way would draw usage leaving.
	if n := strings.Count(s, "dgm-beam-in"); n != 3 {
		t.Errorf("%d inbound writes; the three sessions all write to the log", n)
	}
	if !strings.Contains(s, "aggregate counts without identity") {
		t.Error("the one line that crosses is not labelled for what it carries")
	}
}

// Only one of the two stores wears the mark, and it is the one outside the
// boundary. The mark says whose machine a thing is; the log is the member's.
func TestUsageSceneMarksTheRegistryAndNotTheLog(t *testing.T) {
	s := usageScene()
	if strings.Count(s, ui.MarkSVG) != 1 {
		t.Fatal("the mark should appear exactly once")
	}
	log := s[strings.Index(s, "usg-log"):]
	log = log[:strings.Index(log, "usg-reg")]
	if strings.Contains(log, "dgm-mark") {
		t.Error("the usage log wears the registry's mark; it is the member's, not ours")
	}
	reg := s[strings.Index(s, "usg-reg"):]
	if !strings.Contains(reg, "dgm-mark") {
		t.Error("the registry does not wear its own mark")
	}
}

// Built from the shared diagram vocabulary, setting no colour and no coordinate
// of its own — so a theme or a hairline still has exactly one home.
func TestUsageSceneCarriesNoStylingOfItsOwn(t *testing.T) {
	s := usageScene()
	if strings.Contains(s, "style=") {
		t.Error("the picture sets style inline; geometry and colour live in app.css")
	}
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(s) {
		t.Error("the picture names a colour; colour resolves from a token")
	}
	for _, shared := range []string{"dgm-scene", "dgm-node", "dgm-fence", "dgm-beam", "dgm-port", "dgm-tag"} {
		if !strings.Contains(s, shared) {
			t.Errorf("the picture does not use the shared %s; a copy would drift", shared)
		}
	}
}

// Rule 4: the spectrum is the FIRST panel's alone. Usage builds its panels in
// the browser, into a root div, so the sibling rule that takes the hairline back
// cannot see across it — the page drew it twice the moment a server-rendered
// panel went above them, and then, beside the picture, the note claimed it and
// the picture had none. First is first whether the two are stacked or side by
// side.
func TestUsagePanelsDoNotDrawTheSpectrumTwice(t *testing.T) {
	if !strings.Contains(appCSS, ".usg-band > .panel::before{") {
		t.Error("the picture's plate cannot draw the spectrum")
	}
	if !strings.Contains(appCSS, ".usg-band #usage-root > .panel::before{content:none}") {
		t.Error("a client-built Usage panel can still claim the spectrum beside the picture")
	}
}

// The picture sits BESIDE what the page has to say, not above it with a third of
// a screen of empty board either side. The band is the default in the markup, so
// a page whose script never runs gets it — and a page whose script never runs is
// a page with a note on it.
func TestUsagePictureSitsBesideTheNote(t *testing.T) {
	page := signedInPage(t, "/usage")
	if !strings.Contains(page, `class="usage usg-band"`) {
		t.Fatal("the Usage page is not banded")
	}
	band := page[strings.Index(page, `class="usage usg-band"`):]
	scene, root := strings.Index(band, "usg-stage"), strings.Index(band, `id="usage-root"`)
	if scene < 0 || root < 0 || scene > root {
		t.Error("the picture must be the first half of the band")
	}
	// The dashboard is the exception: a tile row and two charts in half a window
	// is a dashboard nobody can read, so it takes the width back.
	if !strings.Contains(usageJS, "classList.toggle('is-wide'") {
		t.Error("the dashboard cannot reclaim the full width")
	}
	if !strings.Contains(appCSS, ".usg-band.is-wide{grid-template-columns:minmax(0,1fr)}") {
		t.Error("is-wide does not undo the two columns")
	}
}

// An empty div is still a grid item. #usage-head holds the second column for
// the dashboard's figures, and on every other state the script empties it
// rather than removing it — so the cell stayed, the note was pushed onto the
// row below the picture, and the two-column band read as a stacked one at every
// width. The note is the state a member on a tablet actually sees, since the
// registry cannot read their usage.
func TestTheEmptiedHeadReleasesTheNotesColumn(t *testing.T) {
	if !strings.Contains(appCSS, ".usg-band>#usage-head:empty{display:none}") {
		t.Error("an emptied head still holds the column the note belongs in")
	}
	// The tile row and a note give way at different widths: a row of figures
	// needs a desktop, prose holds half a column down to a phone. Collapsing
	// both at 900px is what put the key prompt under the picture on a tablet.
	//
	// The breakpoints themselves are measured in a browser rather than matched
	// in this stylesheet's text — hack/browsercheck opens the page at 1280 and
	// 600 and counts the columns it actually gets. Two media queries written out
	// here passed whether or not the page laid out that way, and broke on any
	// reformat of app.css.
}

// The usage key is a field the registry asks for, so it looks like every other
// field the registry asks for: .inline-form, the same shape Mint a key and the
// join form use. It used to be a bare input with a width in the style
// attribute, which is browser chrome in the middle of a live-data wall.
func TestTheUsageKeyFieldIsTheRegistrysOwnField(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, `<p class="inline-form"><input id="lkey"`) {
		t.Error("the usage key field does not use the shared inline form")
	}
	if strings.Contains(page, `id="lkey"`) && strings.Contains(page, "style=\"width:22rem") {
		t.Error("the usage key field still carries its own width")
	}
	if !strings.Contains(appCSS, ".inline-form input{flex:1;min-width:0;font:13px/1.5 var(--font-mono)") {
		t.Error("the shared field is gone, or no longer machine text")
	}
}

// A PLATE HIDES WHAT RUNS UNDER IT. Every picture in this UI ends its beams at
// the middle of a stack and draws the stack last, so the lines converge out of
// sight and each one comes out from under a rim. The fill was written as two
// percentages summing to 87, which is a colour at 87% alpha: the plates were
// see-through, and every beam ran visibly across the ones it was meant to
// disappear beneath. It is a shared rule, so the Usage picture is only where it
// showed worst.
func TestDiagramPlatesHideWhatRunsUnderThem(t *testing.T) {
	pct := regexp.MustCompile(`(\d+(?:\.\d+)?)%`)
	for _, sel := range []string{".dgm-slab", ".dgm-peer>span"} {
		fill := ""
		for _, decl := range strings.Split(setRule(t, sel), ";") {
			if strings.HasPrefix(strings.TrimSpace(decl), "background:") {
				fill = decl
			}
		}
		if fill == "" {
			t.Errorf("%s no longer has a fill to be opaque", sel)
			continue
		}
		// One percentage is a mix and the rest of the colour; two that do not
		// make 100 is that mix, at their sum as alpha.
		shares := pct.FindAllStringSubmatch(fill, -1)
		if len(shares) < 2 {
			continue
		}
		var sum float64
		for _, s := range shares {
			var v float64
			if _, err := fmt.Sscanf(s[1], "%g", &v); err != nil {
				t.Fatal(err)
			}
			sum += v
		}
		if sum != 100 {
			t.Errorf("%s mixes its fill to %g%%, which is that colour at %g%% alpha — "+
				"a see-through plate hides nothing:%s", sel, sum, sum, fill)
		}
	}
}

// THE WALL IS CUT WHERE THE PORT STANDS. Drawn straight through it, the port
// read as a box standing behind an unbroken wall — a shape nothing can pass,
// which is the opposite of what it is there to say. The opening is painted in
// the plate's own colour over the boundary and under everything else: after the
// fence, so it cuts it; before the beams and the port, so it hides neither.
func TestUsageSceneOpensTheWallWhereTheCrossingRunsThrough(t *testing.T) {
	s := usageScene()
	if n := strings.Count(s, "usg-gap"); n != 1 {
		t.Fatalf("%d openings in the wall; there is one port and one crossing", n)
	}
	fence, gap := strings.Index(s, "usg-fence"), strings.Index(s, "usg-gap")
	beam, port := strings.Index(s, "dgm-beam"), strings.Index(s, "dgm-port")
	if gap < fence {
		t.Error("the opening is drawn under the boundary, so it cuts nothing")
	}
	if gap > beam || gap > port {
		t.Error("the opening is drawn over the traffic; it must hide the wall and nothing else")
	}
	if body := setRule(t, ".usg-gap"); !strings.Contains(body, "background:var(--surface)") {
		t.Errorf("the opening is not painted in the plate's own colour: %s", body)
	}
}

// ONE LINE CROSSES, so it has to look like one line. It is drawn in two spans —
// to the wall and on from it — and each ran the whole fade, which put a bright
// seam at the port and read as two lines meeting there. The second picks up
// where the first leaves off.
func TestUsageSceneCrossingFadesAsOneLine(t *testing.T) {
	a, b := setRule(t, ".usg-out1"), setRule(t, ".usg-out2")
	end := regexp.MustCompile(`--fade-b:([^;}]+)`).FindStringSubmatch(a)
	start := regexp.MustCompile(`--fade-a:([^;}]+)`).FindStringSubmatch(b)
	if end == nil || start == nil {
		t.Fatalf("the two halves of the crossing do not name where in the fade they sit: %q %q", a, b)
	}
	if end[1] != start[1] {
		t.Errorf("the crossing steps from %s to %s at the port; one reach, one fade", end[1], start[1])
	}
}

// THE LABELS DO NOT BITE HOLES IN THE BOUNDARY, because they are not inside the
// frame at all. A label carries the plate's colour so it can occlude a beam it
// crosses, which is right until the thing it crosses is the wall — and the wall
// is the whole argument. The frame used to carry seven spare rows to hold them
// off it, which at a wide window was a band of empty board with every caption a
// screen away from what it names. They hang off the top and bottom edges now:
// the two above grow up, the three below grow down, and a caption that wraps to
// a third line moves further from the drawing rather than into it.
func TestUsageSceneLabelsHangOutsideTheFrame(t *testing.T) {
	above := []string{".usg-tag-machine", ".usg-tag-out"}
	below := []string{".usg-tag-sessions", ".usg-tag-log", ".usg-tag-reg"}
	for _, sel := range above {
		body := setRule(t, sel)
		if !strings.Contains(body, "bottom:calc(100% + ") || !strings.Contains(body, "top:auto") {
			t.Errorf("%s is not hung above the frame, so it can reach the drawing: %s", sel, body)
		}
	}
	for _, sel := range below {
		body := setRule(t, sel)
		if !strings.Contains(body, "top:calc(100% + ") || !strings.Contains(body, "bottom:auto") {
			t.Errorf("%s is not hung below the frame, so it can reach the drawing: %s", sel, body)
		}
	}
	// And the frame keeps the room for them, out of the height, before its own
	// unit is struck — or the drawing grows into its captions and the plate
	// clips them.
	scene := setRule(t, ".usg-scene")
	if !strings.Contains(scene, "--cap:") || !strings.Contains(scene, "96cqh - var(--cap)") {
		t.Errorf("the frame reserves no room for the captions hanging off it: %s", scene)
	}
}

// EACH CAPTION IS ON WHAT IT NAMES. A label's own em is its 13px type, not the
// scene's unit, so a coordinate written in em put the caption under the registry
// two thirds of the way back to the log — the drawing's columns have to be
// counted in var(--u). The two stacks are centred on their own middle and the
// caption for the line out on the port it crosses at.
func TestUsageSceneLabelsAreCentredOnWhatTheyName(t *testing.T) {
	port := number(t, setRule(t, ".usg-scene"), "--port-cx", "")
	for _, c := range []struct {
		sel  string
		want float64
	}{
		{".usg-tag-log", 10.2}, // .usg-log, a 4.8em node at 7.8em
		{".usg-tag-reg", 23.4}, // .usg-reg, the same node at 21em
		{".usg-tag-out", port}, // the port the one crossing runs through
	} {
		body := setRule(t, c.sel)
		m := regexp.MustCompile(`left:calc\((var\(--port-cx\)|[\d.]+)\*var\(--u\)\)`).FindStringSubmatch(body)
		if m == nil {
			t.Errorf("%s is not placed in the scene's own unit: %s", c.sel, body)
			continue
		}
		got := port
		if m[1] != "var(--port-cx)" {
			if _, err := fmt.Sscanf(m[1], "%g", &got); err != nil {
				t.Fatal(err)
			}
		}
		if math.Abs(got-c.want) > .05 {
			t.Errorf("%s is centred on column %g, and what it names is at %g", c.sel, got, c.want)
		}
		if !strings.Contains(body, "--ax:-50%") {
			t.Errorf("%s is not centred on its own point: %s", c.sel, body)
		}
	}
}

// usgNarrowestUnit is the smallest px-per-em the wide Usage picture is drawn at
// — a 1000px window, where the band is still two columns and the plate is at its
// narrowest before the phone rules reflow the scene.
const usgNarrowestUnit = 15.2

// em and pctOf read one length out of a rule body, in the unit it is written in.
func em(t *testing.T, body, prop string) float64 {
	t.Helper()
	return number(t, body, prop, "em")
}

func pctOf(t *testing.T, body, prop string) float64 {
	t.Helper()
	return number(t, body, prop, "%") / 100
}

func number(t *testing.T, body, prop, unit string) float64 {
	t.Helper()
	m := regexp.MustCompile(prop + `:(-?[\d.]+)` + regexp.QuoteMeta(unit)).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s in %s of %q", unit, prop, body)
	}
	var v float64
	if _, err := fmt.Sscanf(m[1], "%g", &v); err != nil {
		t.Fatal(err)
	}
	return v
}
