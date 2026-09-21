// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/ui"
)

// The picture states the position the switch is in, from the saved setting,
// before any script runs — a browser with JavaScript off gets the right one.
func TestAccessScenePositionComesFromTheSavedSetting(t *testing.T) {
	for _, c := range []struct {
		on   bool
		want string
	}{{false, `data-on="0"`}, {true, `data-on="1"`}} {
		got := accessScene(c.on)
		if !strings.Contains(got, c.want) {
			t.Errorf("accessScene(%v) = %q, want it to carry %s", c.on, got, c.want)
		}
	}
}

// It is one scene that MOVES between the two positions, not two that are swapped
// — so it is keyed to the switch by state rather than by the hidden/shown pair
// every row on this page uses, and switchScript has to write the answer onto it.
func TestAccessSceneFollowsTheSwitchWithoutAReload(t *testing.T) {
	scene := accessScene(false)
	if !strings.Contains(scene, `data-switch-state="publish"`) {
		t.Fatal("the scene is not keyed to the publish switch")
	}
	if strings.Contains(scene, "data-switch-on") || strings.Contains(scene, "data-switch-off") {
		t.Fatal("the scene belongs to BOTH positions; hiding it in one would swap two pictures")
	}
	if !strings.Contains(switchScript, `[data-switch-state="'+key+'"]`) ||
		!strings.Contains(switchScript, `setAttribute('data-on'`) {
		t.Fatal("switchScript no longer reflects the switch onto the scene")
	}
}

// The Access plate carries it, in both the editable and the read-only view — the
// vacant board it fills is there either way — and the Sign-in plate does not.
func TestAccessSceneIsOnTheAccessPlate(t *testing.T) {
	page := adminSettingsHTML(t)
	if strings.Count(page, `class="dgm acc"`) != 1 {
		t.Fatalf("expected exactly one Access picture on the settings page")
	}
	access := page[strings.Index(page, `<h2>Access</h2>`):strings.Index(page, `<h2>Sign-in</h2>`)]
	if !strings.Contains(access, `class="dgm acc"`) {
		t.Fatal("the Access picture is not on the Access plate")
	}
	// Outside .set-grid, because it takes the plate's leftover height rather than
	// a row's — which is the whole reason it is there.
	if !strings.Contains(access, `</div><div class="dgm acc"`) {
		t.Fatal("the picture must follow the rows, not sit among them")
	}
}

// It is a picture of a setting, not a setting. A control inside it would post,
// and handleSettingsSave writes "0" for every checkbox it does not see.
func TestAccessSceneSubmitsNothing(t *testing.T) {
	scene := accessScene(true)
	for _, tag := range []string{"<input", "<select", "<textarea", "<button", "name="} {
		if strings.Contains(scene, tag) {
			t.Errorf("the Access picture carries %q; it must add nothing to the form", tag)
		}
	}
	// The switch beside it already says which position is in force, and the
	// address row already reads out. A second wordless copy is noise.
	if !strings.Contains(scene, `aria-hidden="true"`) {
		t.Error("the picture repeats the switch, so it must be hidden from a reader")
	}
}

// Every coordinate, angle and colour is in the stylesheet. The Go side emits the
// skeleton, so a theme, a hairline or a token still has exactly one home.
func TestAccessSceneCarriesNoStylingOfItsOwn(t *testing.T) {
	scene := accessScene(true)
	if strings.Contains(scene, "style=") {
		t.Error("the Access picture sets style inline; geometry and colour live in app.css")
	}
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(scene) {
		t.Error("the Access picture names a colour; colour resolves from a token")
	}
}

// The one piece of ambient motion in the registry, so both branches that have to
// survive it must be here: it holds still when asked for less motion, and it is
// drawn in lines when the palette drops to two colours.
func TestAccessSceneSurvivesReducedMotionAndForcedColours(t *testing.T) {
	// The primitives carry a branch each and so does the Access picture; the world
	// only turns here, so its stop is in the Access block.
	shared := appCSS[strings.Index(appCSS, "/* ---- diagram primitives"):]
	if !strings.Contains(shared[:strings.Index(shared, "/* ---- the Access picture")],
		"@media (prefers-reduced-motion:reduce)") {
		t.Error("the shared diagram parts keep moving under prefers-reduced-motion")
	}
	block := appCSS[strings.Index(appCSS, "/* ---- the Access picture"):]
	if !strings.Contains(block, "@media (prefers-reduced-motion:reduce)") {
		t.Error("the Access picture has no reduced-motion branch")
	}
	// The globe is REDRAWN rather than moved, so no rule about transforms can
	// hold it still — its stop is in the script, and it has to keep asking,
	// because the setting can change while the page is open.
	if !strings.Contains(globeScript, "prefers-reduced-motion:reduce") ||
		!strings.Contains(globeScript, "cancelAnimationFrame") {
		t.Error("the world keeps turning under prefers-reduced-motion")
	}
	if !strings.Contains(globeScript, "addEventListener('change'") {
		t.Error("the globe answers prefers-reduced-motion once and never again")
	}
	forced := shared[strings.Index(shared, "@media (forced-colors:active)"):]
	if !strings.Contains(forced[:400], "CanvasText") {
		t.Error("the picture has no forced-colors branch; its fills would vanish")
	}
	// Colour comes from tokens, in the block as everywhere else.
	if regexp.MustCompile(`\.acc[^{]*\{[^}]*#[0-9a-fA-F]{3,8}\b`).MatchString(appCSS) {
		t.Error("a hex literal in the Access picture's CSS; every colour resolves from :root")
	}
}

// Global Access is not "the world reaches the registry". It is: the registry
// dials OUT to an ingress, and the world reaches the ingress. The picture has to
// carry all three parts, or it draws an open port that does not exist.
func TestAccessSceneRoutesTheWorldThroughTheIngress(t *testing.T) {
	scene := accessScene(true)
	for _, part := range []string{`acc-uplink"`, `acc-ingress"`} {
		if !strings.Contains(scene, part) {
			t.Errorf("the picture is missing %s", part)
		}
	}
	// The two machines are different SHAPES, because they are different things.
	// The ingress is an open tube on edge — two rims and the edges between them —
	// and it is made of none of the registry's plates. Drawn as a smaller plate it
	// read as a lesser registry, which is the one thing it is not.
	ingress := scene[strings.Index(scene, `acc-ingress">`):]
	ingress = ingress[:strings.Index(ingress, "</div>")]
	if strings.Contains(ingress, "dgm-slab") {
		t.Error("the ingress is built from the registry's plates; it must not share its shape")
	}
	for _, part := range []string{"dgm-gate1", "dgm-gate2", "dgm-tube1", "dgm-tube2"} {
		if !strings.Contains(ingress, part) {
			t.Errorf("the ingress is missing %s; unjoined rims read as two cards, not a way through", part)
		}
	}

	block := appCSS[strings.Index(appCSS, "/* ---- diagram primitives"):]
	// The client beams LEAVE the ingress once it exists. Same beams, new origin —
	// which is the fact the picture is being drawn to state.
	if !strings.Contains(block, `.acc-beam{--x:5.5em;--y:8em}`) ||
		!strings.Contains(block, `.acc[data-on="1"] .acc-beam{--x:13.2em;--y:8em}`) {
		t.Error("the client beams no longer move their origin to the ingress")
	}
	// The boundary stays drawn on Global Access — contracted around the registry,
	// not gone. It is what says the ingress is somebody else's machine, so a rule
	// that hid or dissolved it would take the claim out of the picture.
	if !strings.Contains(block, `.acc[data-on="1"] .acc-fence{left:`) {
		t.Error("the boundary must contract around the registry, not vanish")
	}
	if regexp.MustCompile(`\.acc\[data-on="1"\] \.acc-fence\{[^}]*(opacity:0|scale\()`).MatchString(block) {
		t.Error("the boundary is hidden on Global Access; the ingress then sits outside nothing")
	}
	// And the uplink is the one thing running the other way: traffic in on the
	// beams, the connection out on the uplink, because this end is the one that
	// dials. Which way a beam runs is now a class the markup puts on it, so this
	// asks the markup rather than the stylesheet.
	upTag := scene[strings.Index(scene, "acc-uplink"):]
	upTag = scene[strings.LastIndex(scene[:strings.Index(scene, "acc-uplink")], "<span"):]
	upTag = upTag[:strings.Index(upTag, ">")]
	if strings.Contains(upTag, "dgm-beam-in") {
		t.Error("the uplink runs inward; the registry is the end that establishes it")
	}
	if strings.Count(scene, "dgm-beam-in") != len(globeCities) {
		t.Error("every client beam must run in toward the ingress, one for each city")
	}
}

// Which of the two boxes is the operator's own is the question the picture
// otherwise leaves open, so the registry wears the mark — on the top plate, and
// the same drawing the header uses rather than a second copy of it.
func TestAccessSceneMarksTheRegistry(t *testing.T) {
	scene := accessScene(true)
	// The mark follows the node rather than sitting inside its top plate: outside
	// the stack nothing can squash it (see TestTheMarkIsNeverInsideThePlateStack).
	node := strings.Index(scene, "dgm-node")
	mark := strings.Index(scene, "dgm-mark")
	if node < 0 || mark < 0 || mark < node {
		t.Fatal("the registry is not marked, or the mark is drawn before the plate it names")
	}
	if !strings.Contains(scene[mark:], ui.MarkSVG) {
		t.Fatal("the mark is not the header's drawing")
	}
	if strings.Count(scene, "dgm-mark") != 1 {
		t.Error("the mark belongs to the registry alone; the ingress is not the operator's machine")
	}
	// One drawing of the mark, not a traced copy that drifts from the header's.
	// Containing MarkSVG is not enough — the Fatal above already settled that, and
	// a second hand-drawn mark beside it would still pass. Counting the strokes is
	// what catches the copy.
	//
	// The world is the picture's other drawing and is taken out first, which also
	// says it is drawn once: the two copies that make the turn are <use>, not a
	// second path, so the coastlines exist in this file exactly once.
	if !strings.Contains(scene, `data-world="`+worldLandPath+`"`) {
		t.Fatal("the globe is not drawn from worldpath.go")
	}
	rest := regexp.MustCompile(`<path class="acc-land"[^>]*>`).ReplaceAllString(scene, "")
	if got, want := strings.Count(rest, "<path"), strings.Count(ui.MarkSVG, "<path"); got != want {
		t.Errorf("the picture draws %d paths against MarkSVG's %d; something traced the mark", got, want)
	}
}

// The ingress GROWS out of the registry along the link. It used to SNAP: a
// transform interpolates function by function only when the two lists match or
// one is a prefix of the other, and `rotateY(-38deg)` alone is neither a match
// nor a prefix of `translateX() rotateY() scale()`. The browser fell back to
// interpolating matrices, scale(0) made that matrix singular, it could not
// decompose it, and it swapped the two values outright — so the ingress stood at
// full size from the first frame while only its opacity moved. Nothing in a diff
// shows that; this does.
func TestAccessSceneIngressGrowsRatherThanSnapping(t *testing.T) {
	rule := func(sel string) string {
		i := strings.Index(appCSS, sel+"{")
		if i < 0 {
			t.Fatalf("no rule for %s", sel)
		}
		body := appCSS[i+len(sel)+1:]
		return body[:strings.Index(body, "}")]
	}
	fns := func(body string) []string {
		i := strings.Index(body, "transform:")
		if i < 0 {
			t.Fatalf("no transform in %q", body)
		}
		decl := body[i:]
		if j := strings.IndexAny(decl, ";"); j >= 0 {
			decl = decl[:j]
		}
		return regexp.MustCompile(`([a-zA-Z0-9]+)\(`).FindAllString(decl, -1)
	}
	rest, on := rule(".acc-ingress"), rule(`.acc[data-on="1"] .acc-ingress`)
	if a, b := fns(rest), fns(on); strings.Join(a, "") != strings.Join(b, "") {
		t.Errorf("the ingress transforms are %v at rest and %v in force; "+
			"unmatched lists interpolate as matrices and can give up entirely", a, b)
	}
	// A zero scale is the singular matrix that made it give up.
	for _, body := range []string{rest, on} {
		if regexp.MustCompile(`scale\(0[^.\d]`).MatchString(body) {
			t.Error("scale(0) in the ingress transform; the fallback matrix cannot be decomposed")
		}
	}
	// And it moves on the same clock as everything else in the picture. A part
	// that arrives early or late reads as a second animation, not one scene.
	block := appCSS[strings.Index(appCSS, "/* ---- diagram primitives"):]
	block = block[:strings.Index(block, "@media (prefers-reduced-motion:reduce)")]
	for _, d := range regexp.MustCompile(`transition:[^;}]+`).FindAllString(block, -1) {
		for _, dur := range regexp.MustCompile(`\d*\.?\d+s`).FindAllString(d, -1) {
			if dur != ".36s" {
				t.Errorf("the Access picture transitions at %s as well as .36s: %q", dur, d)
			}
		}
	}
}

// NOTHING IN THE PICTURE MAY BOTH FADE AND HOLD 3D. Opacity below 1 makes a
// browser render an element as a group, and a grouped element's
// transform-style:preserve-3d is treated as flat — so an element carrying both
// is flat for exactly as long as it is fading, and springs into three dimensions
// at the instant opacity reaches 1.
//
// The ingress had both. Its two rims projected concentrically for the whole
// transition — a picture frame seen head-on — and the tube turned oblique in one
// step at the end. Measured in Chromium, the offset between the rims' centres
// went 0, 0, 0 … 0.677 of the near rim's width; it now reads 0.677 in every
// frame. None of that is visible in a diff, and the computed style is no help
// either: transform-style still reports preserve-3d while the browser is
// ignoring it.
func TestAccessSceneKeepsFadingApartFromThe3D(t *testing.T) {
	block := appCSS[strings.Index(appCSS, "/* ---- diagram primitives"):]
	block = block[:strings.Index(block, "@media (prefers-reduced-motion:reduce)")]

	// The block as (selector, body) pairs, keyed by the last class in the
	// selector — which is the element the rule is actually about.
	type rule struct{ sel, body string }
	var rules []rule
	for _, m := range regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(block, -1) {
		rules = append(rules, rule{strings.TrimSpace(m[1]), m[2]})
	}
	subject := func(sel string) string {
		f := strings.Fields(sel)
		last := f[len(f)-1]
		if i := strings.LastIndex(last, "."); i >= 0 {
			last = last[i+1:]
		}
		return strings.SplitN(last, ":", 2)[0]
	}

	holds3D := map[string]bool{}
	for _, r := range rules {
		if strings.Contains(r.body, "transform-style:preserve-3d") {
			holds3D[subject(r.sel)] = true
		}
	}
	if len(holds3D) == 0 {
		t.Fatal("nothing in the Access picture carries preserve-3d any more")
	}
	for _, r := range rules {
		if holds3D[subject(r.sel)] && strings.Contains(r.body, "opacity:") {
			t.Errorf("%s both fades and carries preserve-3d; a faded element is a group, "+
				"and a group is flat — put the fade on a wrapper and the 3D inside it", r.sel)
		}
	}
}

// The ingress comes ROUND as it arrives: edge-on at rest, where it is a line
// with no mouth to enter, turning onto its resting angle as it grows. Holding
// one angle throughout made the arrival a zoom; the turn is what makes the
// opening something that opens.
//
// The turn has to live on the cage, not on the element outside it. Out there the
// picture has already been flattened into a group, so a rotateY would squash a
// flat image instead of rotating a tube — which is the fault this scene had
// once already.
func TestAccessSceneIngressTurnsAsItArrives(t *testing.T) {
	rest, on := setRule(t, ".acc-ingress>.dgm-cage"), setRule(t, `.acc[data-on="1"] .acc-ingress>.dgm-cage`)
	angle := func(body string) string {
		m := regexp.MustCompile(`transform:rotateY\((-?[0-9.]+deg)\)`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("the cage's transform is not a single rotateY: %q", body)
		}
		return m[1]
	}
	if a, b := angle(rest), angle(on); a == b {
		t.Errorf("the ingress holds %s all the way; it should turn onto its angle as it grows", a)
	}
	// The 3D context and the clock belong to the shared cage; only the two angles
	// are the Access picture's own.
	cage := setRule(t, ".dgm-cage")
	if !strings.Contains(cage, "transform-style:preserve-3d") {
		t.Error("the turn is outside the 3D context; it would squash the tube rather than turn it")
	}
	if !strings.Contains(cage, "transition:transform .36s") {
		t.Error("the turn does not run on the picture's clock")
	}
}

// TRAFFIC IS FOR A ROUTE THAT HAS CARRIED SOMETHING.
//
// A dot travelling a line is what makes these pictures read as a network rather
// than a diagram of one, and it is the only thing in them that says which way a
// line runs without a word. What it must not do is claim an arrival on a route
// nothing has come down — so every state that means "nothing has" takes it off
// and draws dashed with a hollow end instead.
//
// This is the guard for that, across the four pictures that have such a state.
func TestDiagramTrafficStopsWhereNothingHasArrived(t *testing.T) {
	if !strings.Contains(setRule(t, ".dgm-beam-pending::after"), "display:none") {
		t.Fatal("a pending reach carries traffic; it is a reach nothing has come down")
	}
	for _, c := range []struct{ name, scene string }{
		{"an invitation nobody has used",
			memberScene([]models.MemberKey{{ID: "a", Label: "a"}}, 3, false)},
		{"a feed that is not arriving",
			federationScene(nil, []fedIn{{Name: "stale", Failing: true}})},
		{"a channel whose token was never used",
			federationScene([]fedOut{{Title: "gated", Count: 2}}, nil)},
		{"a registry with no facts in it",
			learningScene(0, []learningGate{{Name: "A", Needs: []learningNeed{{What: "facts", Need: 5}}}})},
	} {
		if !strings.Contains(c.scene, "dgm-beam-pending") {
			t.Errorf("%s is drawn carrying traffic", c.name)
		}
	}
	// And it holds still for anybody who asked for less motion.
	shared := appCSS[strings.Index(appCSS, "/* ---- diagram primitives"):]
	shared = shared[:strings.Index(shared, "/* ---- the Access picture")]
	still := shared[strings.Index(shared, "@media (prefers-reduced-motion:reduce)"):]
	if !strings.Contains(still[:260], ".dgm-beam:not(.dgm-beam-arrow)::after{display:none}") {
		t.Error("the traffic keeps running under prefers-reduced-motion")
	}
}

// THE UPLINK IS NOT A CHANNEL. Every other reach in this picture is a line
// something travels along, and it wears a dot going round it to say so. This one
// is the registry DIALLING: an act, done once, in the one direction the whole
// drawing exists to state. So it carries an arrowhead at its end instead —
// standing still, because establishing a connection is not traffic — and the
// line stops where the arrowhead is rather than running on under the tube.
func TestAccessUplinkPointsRatherThanFlows(t *testing.T) {
	scene := accessScene(true)
	up := scene[strings.LastIndex(scene[:strings.Index(scene, "acc-uplink")], "<span"):]
	up = up[:strings.Index(up, ">")]
	if !strings.Contains(up, "dgm-beam-arrow") {
		t.Errorf("the uplink does not say which way the connection is opened: %s", up)
	}
	if strings.Contains(up, "dgm-beam-in") {
		t.Error("the uplink runs inward; the registry is the end that dials")
	}
	arrow := setRule(t, ".dgm-beam-arrow::after")
	if !strings.Contains(arrow, "animation:none") {
		t.Errorf("the arrowhead travels the line it is meant to be the end of: %s", arrow)
	}
	if !strings.Contains(arrow, "left:100%") {
		t.Errorf("the arrowhead is not at the end of its reach: %s", arrow)
	}
	// It survives a request for less motion: there is nothing moving to stop.
	shared := appCSS[strings.Index(appCSS, "/* ---- diagram primitives"):]
	shared = shared[:strings.Index(shared, "/* ---- the Access picture")]
	still := shared[strings.Index(shared, "@media (prefers-reduced-motion:reduce)"):]
	if !strings.Contains(still[:260], ".dgm-beam:not(.dgm-beam-arrow)::after{display:none}") {
		t.Error("reduced motion takes the arrowhead off with the traffic")
	}
}

// AND THE LINK ANSWERS EVERY ARRIVAL. What lands on the ingress does not stop
// there — it goes on to the registry — so the reach the registry opened
// brightens as each client's traffic reaches the mouth.
//
// It is timed by ARITHMETIC, not by eye. The client reaches run one flow cycle
// apart in phase, so an arrival lands every cycle-over-clients; the pulse runs on
// exactly that interval and waits one whole one before its first flash, so it
// never fires on an arrival that has not happened. Timing like this drifts
// silently: at a cycle that did not divide, the two would be a millisecond out
// per lap and a quarter-second adrift after a minute, by which point the link is
// flashing at nothing.
//
// AND IT HAS TO RENDER. The old pulse was hung on this element's ::before, which
// .dgm-beam-bare sets to display:none — the rule that gave it a gradient and an
// animation never gave it back its display, so the flash never appeared on any
// page. That is what the display check is for.
func TestAccessUplinkAnswersEveryArrival(t *testing.T) {
	pulse := setRule(t, `.acc[data-on="1"] .acc-uplink::before`)
	if !strings.Contains(pulse, "display:block") {
		t.Fatalf("the pulse is drawn on the end dot's element, which .dgm-beam-bare hides: %s", pulse)
	}
	m := regexp.MustCompile(`animation:acc-arrive ([0-9.]+)s [a-z-]+ ([0-9.]+)s`).FindStringSubmatch(pulse)
	if m == nil {
		t.Fatalf("the uplink has no pulse: %s", pulse)
	}
	dur, _ := strconv.ParseFloat(m[1], 64)
	delay, _ := strconv.ParseFloat(m[2], 64)

	flow := regexp.MustCompile(`animation:dgm-flow ([0-9.]+)s`).FindStringSubmatch(setRule(t, ".dgm-beam::after"))
	if flow == nil {
		t.Fatal("the reaches carry no flow for the pulse to answer")
	}
	cycle, _ := strconv.ParseFloat(flow[1], 64)
	near := func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

	// One arrival per client per cycle, evenly spread.
	if want := cycle / float64(len(globeCities)); !near(dur, want) {
		t.Errorf("the pulse runs every %gs; %d clients on a %gs cycle land one every %gs",
			dur, len(globeCities), cycle, want)
	}
	if !near(delay, dur) {
		t.Errorf("the pulse is delayed %gs against a %gs interval; it should wait exactly one "+
			"arrival, or its first flash answers nothing", delay, dur)
	}
	// The offsets that spread those arrivals, one per client after the first.
	for i := 2; i <= len(globeCities); i++ {
		rule := fmt.Sprintf(".acc-beam%d::after", i)
		if got, want := secs(t, setRule(t, rule), "animation-delay:"), -float64(i-1)*dur; !near(got, want) {
			t.Errorf("%s is offset %gs, not %gs; the arrivals are no longer evenly spaced", rule, got, want)
		}
	}
	// Nothing arrives when nothing travels, so the link has nothing to answer.
	block := appCSS[strings.Index(appCSS, "/* ---- the Access picture"):]
	still := block[strings.Index(block, "@media (prefers-reduced-motion:reduce)"):]
	if !strings.Contains(still[:strings.Index(still, "\n}\n")], `.acc[data-on="1"] .acc-uplink::before{display:none}`) {
		t.Error("the link keeps flashing under prefers-reduced-motion, at traffic that has stopped")
	}
}

// secs reads one duration out of a rule body.
func secs(t *testing.T, body, prop string) float64 {
	t.Helper()
	m := regexp.MustCompile(prop + `[^;}]*?(-?[0-9.]+)s`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s in %q", prop, body)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// The picture draws with --draw, not with --axis.
//
// --axis is a CHART AXIS, pitched to be read beside its own tick label, and on
// the wall it is deliberately faint: #283243 sits about twelve steps of
// lightness off the plate it is drawn on, against the light board's
// twenty-three. An axis can afford that. A line drawing cannot — at that weight
// the globe and the boundary round the registry were barely there in the dark,
// which is what the operator reported. --draw is the same hairline at matched
// apparent weight in both themes.
//
// Both halves are asserted. A token declared but not used leaves the picture
// faint; a token used but not declared leaves it with no colour at all.
func TestAccessSceneDrawsWithItsOwnHairline(t *testing.T) {
	block := appCSS[strings.Index(appCSS, "/* ---- diagram primitives"):]
	block = block[:strings.Index(block, "/* ---- prose inside a plate")]
	if strings.Contains(block, "var(--axis)") {
		t.Error("the picture is drawn in --axis, a chart axis; on the wall it disappears")
	}
	if !strings.Contains(block, "var(--draw)") {
		t.Error("the picture's hairlines no longer resolve from --draw")
	}
	// Declared once per palette, the way every other token in this file is:
	// light, the OS-preference dark, and the forced data-theme dark.
	if n := strings.Count(appCSS, "--draw:"); n != 3 {
		t.Errorf("--draw is declared %d times; it needs one value per palette (light, dark, forced dark)", n)
	}
	vals := map[string]bool{}
	for _, m := range regexp.MustCompile(`--draw:(#[0-9a-f]{6})`).FindAllStringSubmatch(appCSS, -1) {
		vals[m[1]] = true
	}
	if len(vals) != 2 {
		t.Errorf("--draw takes %d distinct values; the whole point is that the two boards "+
			"need different ink to read the same", len(vals))
	}
}

// THE WORLD IS THE WORLD. It was six meridians and three parallels — a drawing
// of a sphere, and of nowhere. What Global Access reaches is a place, and the
// picture now says which one: coastlines, land against sea.
func TestAccessSceneDrawsTheRealWorld(t *testing.T) {
	scene := accessScene(true)
	for _, part := range []string{`class="acc-land"`, `class="acc-sea"`, `class="acc-limb"`} {
		if !strings.Contains(scene, part) {
			t.Errorf("the globe is missing %s", part)
		}
	}
	for _, gone := range []string{"acc-mer", "acc-par", "acc-sphere"} {
		if strings.Contains(scene, gone) || strings.Contains(appCSS, gone) {
			t.Errorf("%s is still here; there is one globe, and it is drawn not moved", gone)
		}
	}
	// Land and sea are the only two colours in the chrome that are not the
	// accent, and they are tokens like every other: one value per palette —
	// light, the OS-preference dark, and the forced data-theme dark.
	for _, token := range []string{"--land:", "--sea:"} {
		if n := strings.Count(appCSS, token); n != 3 {
			t.Errorf("%s is declared %d times; it needs one value per palette", token, n)
		}
	}
	for _, use := range []string{"fill:var(--land)", "fill:var(--sea)"} {
		if !strings.Contains(appCSS, use) {
			t.Errorf("the globe does not take its colour from %s", use)
		}
	}
}

// A SPHERE, NOT A MAP. The globe drew the coastlines on a flat map rolling
// behind a round window, and a continent at the rim kept its full width where a
// sphere squeezes it away — which is what a reader sees and names, without
// knowing the word for it. The projection is orthographic now, and this is the
// property that says so: the same span of longitude is WIDE in the middle of the
// disc and narrow at its edge.
func TestGlobeSquashesTheSameSpanAtTheLimb(t *testing.T) {
	span := func(lon, lon0 float64) float64 {
		d := globeAt([][][2]float64{{{lon, 0}, {lon + 10, 0}, {lon + 10, 5}}}, lon0)
		x1, x2 := nthX(t, d, 0), nthX(t, d, 1)
		return math.Abs(x2 - x1)
	}
	middle, edge := span(0, 0), span(75, 0)
	if middle <= 0 {
		t.Fatal("ten degrees of longitude has no width in the middle of the disc")
	}
	if edge > middle/3 {
		t.Errorf("ten degrees is %.1f units in the middle and %.1f at the limb; "+
			"on a sphere the edge one is a fraction of the middle, and this reads as a flat map",
			middle, edge)
	}
}

// NOTHING LEAVES THE DISC, at any angle. The far side is pinned to the rim
// rather than clipped, which is what lets the drawing carry no clip of its own —
// and it is only true while the pinning is: a projection that let a hidden point
// keep its distance would throw the far hemisphere across the near one.
func TestGlobeKeepsEveryPointOnItsFace(t *testing.T) {
	rings := worldRings()
	if len(rings) < 20 {
		t.Fatalf("%d coastlines parsed out of worldpath.go; the path is not being read", len(rings))
	}
	// A tenth of a unit of slack: the path is written to one decimal, so a point
	// pinned to the rim can be rounded that far off the circle it sits on.
	for _, lon0 := range []float64{-180, -20, 0, 37, 90, 179} {
		for _, xy := range coords(t, globeAt(rings, lon0)) {
			if r := math.Hypot(xy[0]-globeR, xy[1]-globeR); r > globeR+.1 {
				t.Fatalf("at %g the drawing reaches %.2f from the middle of a %g disc", lon0, r, globeR)
			}
		}
	}
}

// THE LAND IS THE LAND, AT EVERY ANGLE. This is the test that earns its place:
// clipping a coastline at the horizon and closing it along the limb is easy to
// get subtly, spectacularly wrong, and both ways it went wrong painted a plate
// of solid green that no unit of arithmetic would have caught.
//
// Twice: an arc that went the long way round enclosed the whole disc, and arcs
// joined in the order the ring was walked sailed past other crossings and took
// an extra lap of the world each. Both leave the drawing covering its own disc,
// so that is what this measures — the painted area, at angles all the way round.
// Land is a bit over a quarter of the earth, and a hemisphere can be most ocean
// or most continent, so the honest window is wide. It is the failures that are
// not close.
func TestGlobePaintsPlausibleLandAtEveryAngle(t *testing.T) {
	rings := worldRings()
	disc := math.Pi * globeR * globeR
	for lon0 := -180.0; lon0 < 180; lon0 += 5 {
		got := math.Abs(painted(t, globeAt(rings, lon0)))
		if share := got / disc; share < .05 || share > .6 {
			t.Errorf("at %g the globe paints %.0f%% of its face; the earth has no such "+
				"hemisphere, so the clip has folded or gone round twice", lon0, share*100)
		}
	}
}

// painted is the area the path fills: each subpath on its own, with the arcs
// walked, so a hole subtracts and a lap of the disc cannot hide inside a sum.
func painted(t *testing.T, d string) float64 {
	t.Helper()
	var total float64
	for _, sub := range strings.Split(d, "M")[1:] {
		poly := walk(t, "M"+sub)
		var a float64
		for i := range poly {
			j := (i + 1) % len(poly)
			a += poly[i][0]*poly[j][1] - poly[j][0]*poly[i][1]
		}
		total += a / 2
	}
	return total
}

// walk turns one subpath into a polygon, sampling every arc of the limb.
func walk(t *testing.T, d string) [][2]float64 {
	t.Helper()
	var out [][2]float64
	var at [2]float64
	for _, m := range regexp.MustCompile(`([MLAZ])([^MLAZ]*)`).FindAllStringSubmatch(d, -1) {
		if m[1] == "Z" {
			continue
		}
		nums := regexp.MustCompile(`-?(?:\d+\.?\d*|\.\d+)`).FindAllString(m[2], -1)
		step := 2
		if m[1] == "A" {
			step = 7
		}
		for i := 0; i+step <= len(nums); i += step {
			x := mustFloat(t, nums[i+step-2])
			y := mustFloat(t, nums[i+step-1])
			if m[1] == "A" {
				from := math.Atan2(at[1]-globeR, at[0]-globeR)
				to := math.Atan2(y-globeR, x-globeR)
				sweep := nums[i+4] == "1"
				da := to - from
				for sweep && da < 0 {
					da += 2 * math.Pi
				}
				for !sweep && da > 0 {
					da -= 2 * math.Pi
				}
				for k := 1; k <= 48; k++ {
					a := from + da*float64(k)/48
					out = append(out, [2]float64{globeR + globeR*math.Cos(a), globeR + globeR*math.Sin(a)})
				}
			} else {
				out = append(out, [2]float64{x, y})
			}
			at = [2]float64{x, y}
		}
	}
	return out
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// THE STILL AND THE TURN ARE THE SAME PROJECTION. The page arrives with a globe
// the server drew and the script takes it over, so the two run the same
// arithmetic from the same numbers — and the first frame has to land on the
// still, or the picture jumps the moment the script loads.
func TestGlobeStillAndScriptShareTheirNumbers(t *testing.T) {
	for _, want := range []string{
		"TILT=" + globeNum(globeTilt),
		"FACE=" + globeNum(globeFace),
		"TURN=" + globeNum(globeTurn*1000),
	} {
		if !strings.Contains(globeScript, want) {
			t.Errorf("the script does not carry %s; it would draw a different globe from the still", want)
		}
	}
	scene := accessScene(true)
	if !strings.Contains(scene, `d="`+globeAt(worldRings(), globeFace)+`"`) {
		t.Error("the still in the page is not the projection at the angle the script starts from")
	}
}

// THE COASTLINES ARE GEOGRAPHY, not a picture of it — degrees, in a 360 by 180
// box — because the view is a number the projection takes and the shape that
// ships has to be the earth. Regenerating with the wrong flags is how that stops
// being true (hack/worldpath/README.md).
func TestWorldPathIsGeographyInDegrees(t *testing.T) {
	minX, minY, maxX, maxY := worldBounds(t, worldLandPath)
	const slack = .05
	if minX < -slack || maxX > 360+slack || minY < -slack || maxY > 180+slack {
		t.Errorf("the coastlines run from (%g,%g) to (%g,%g), outside the 360x180 degree box",
			minX, minY, maxX, maxY)
	}
	if minX > 1 || maxX < 359 {
		t.Errorf("the coastlines span x %g to %g; a world that stops short of a "+
			"meridian is a world with a piece missing", minX, maxX)
	}
	// 90-lat, so the top of the box is the north pole. Land reaches into the
	// Arctic and stops at the Antarctic coast, which is the bottom.
	if minY > 15 || maxY < 179 {
		t.Errorf("the coastlines span y %g to %g; that is not both poles", minY, maxY)
	}
}

// coords and nthX read a projected path back — the commands globe.go writes: an
// absolute M, a run of L, an A along the limb, and Z. Only the point each one
// ends on is a place on the drawing; an arc's first five numbers are its shape.
func coords(t *testing.T, d string) [][2]float64 {
	t.Helper()
	var out [][2]float64
	for _, m := range regexp.MustCompile(`([MLAZ])([^MLAZ]*)`).FindAllStringSubmatch(d, -1) {
		if m[1] == "Z" {
			continue
		}
		nums := regexp.MustCompile(`-?(?:\d+\.?\d*|\.\d+)`).FindAllString(m[2], -1)
		step := 2
		if m[1] == "A" {
			step = 7
		}
		if len(nums) == 0 || len(nums)%step != 0 {
			t.Fatalf("%s holds %d numbers, which is not whole %s", m[1], len(nums), m[1])
		}
		for i := step - 2; i < len(nums); i += step {
			x, err := strconv.ParseFloat(nums[i], 64)
			if err != nil {
				t.Fatal(err)
			}
			y, err := strconv.ParseFloat(nums[i+1], 64)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, [2]float64{x, y})
		}
	}
	return out
}

func nthX(t *testing.T, d string, n int) float64 {
	t.Helper()
	c := coords(t, d)
	if len(c) <= n {
		t.Fatalf("the path draws %d points, not %d", len(c), n+1)
	}
	return c[n][0]
}

// worldBounds walks the path. It understands the three commands the generator
// emits — absolute M, relative l, Z — and fails on anything else rather than
// quietly measuring half a world.
func worldBounds(t *testing.T, d string) (minX, minY, maxX, maxY float64) {
	t.Helper()
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	var x, y float64
	for _, m := range regexp.MustCompile(`([MlZ])([^MlZ]*)`).FindAllStringSubmatch(d, -1) {
		if m[1] == "Z" {
			continue
		}
		nums := regexp.MustCompile(`-?(?:\d+\.?\d*|\.\d+)`).FindAllString(m[2], -1)
		if len(nums) != 2 {
			t.Fatalf("%s takes two numbers, got %q", m[1], m[2])
		}
		var v [2]float64
		for i, n := range nums {
			f, err := strconv.ParseFloat(n, 64)
			if err != nil {
				t.Fatal(err)
			}
			v[i] = f
		}
		if m[1] == "M" {
			x, y = v[0], v[1]
		} else {
			x, y = x+v[0], y+v[1]
		}
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	if math.IsInf(minX, 1) {
		t.Fatal("the path draws nothing")
	}
	return minX, minY, maxX, maxY
}

// A CLIENT IS A PLACE, and it stays on it while the world turns. The city is on
// the sphere, so it turns with the sphere: at the angle where its own meridian
// faces us it sits on the middle line of the disc, half a revolution later it is
// behind the world, and it never leaves the face of the globe on the way.
func TestGlobeClientsStayOnTheirCities(t *testing.T) {
	for i, city := range globeCities {
		lat, lon := city[0], city[1]
		// Its own meridian in the middle: nothing left or right of centre, and the
		// latitude alone says how far up or down the disc it sits.
		length, angle, near := globeClientAt(lat, lon, lon)
		x, y := clientPoint(length, angle)
		if !near {
			t.Errorf("city %d is behind the world at its own meridian", i+1)
		}
		if math.Abs(x-sceneGlobeX) > .001 {
			t.Errorf("city %d sits %.3fem off the middle of the disc when its own "+
				"meridian faces us", i+1, x-sceneGlobeX)
		}
		if lat > globeTilt && y >= sceneGlobeY || lat < -globeTilt && y <= sceneGlobeY {
			t.Errorf("city %d is at %g north and comes out on the wrong side of the "+
				"equator", i+1, lat)
		}
		// Half a turn later it is round the back, and every angle between keeps it
		// on the face of the globe rather than out on the board.
		if _, _, back := globeClientAt(lat, lon, lon+180); back {
			t.Errorf("city %d is still drawn from the far side of the world", i+1)
		}
		for lon0 := -180.0; lon0 < 180; lon0++ {
			length, angle, _ = globeClientAt(lat, lon, lon0)
			x, y = clientPoint(length, angle)
			if r := math.Hypot(x-sceneGlobeX, y-sceneGlobeY); r > sceneGlobeR+.001 {
				t.Fatalf("city %d is %.3fem from the middle of a %.3fem disc at %g",
					i+1, r, sceneGlobeR, lon0)
			}
		}
	}
}

// THE STILL AND THE TURN PUT THE CLIENTS IN THE SAME PLACE. Where a city has
// turned to is arithmetic, so it lives in globe.go and reaches the page as
// --g-len, --g-ang and --g-vis; the stylesheet carries the same positions
// at the still's angle, and the still is what a browser with no script keeps. If
// the two ever part company the clients jump the moment the script loads.
func TestAccessSceneClientsSitOnTheirCities(t *testing.T) {
	// The globe's box and where the reaches start are the picture's own geometry,
	// so app.css owns them and globe.go has to agree with what it finds there.
	box := cssNums(t, `\.acc-globe\{position:absolute;left:([\d.]+)em;top:([\d.]+)em;`+
		`width:([\d.]+)em;height:([\d.]+)em;`)
	if x, y := box[0]+box[2]/2, box[1]+box[3]/2; x != sceneGlobeX || y != sceneGlobeY {
		t.Errorf("app.css puts the middle of the globe at %gem,%gem; globe.go has %gem,%gem",
			x, y, sceneGlobeX, sceneGlobeY)
	}
	if box[2] != sceneGlobeBox {
		t.Errorf("app.css draws the globe in a %gem box; globe.go has %gem", box[2], sceneGlobeBox)
	}
	reach := cssNums(t, `\.acc\[data-on="1"\] \.acc-beam\{--x:([\d.]+)em;--y:([\d.]+)em\}`)
	if reach[0] != sceneReachX || reach[1] != sceneReachY {
		t.Errorf("app.css starts the reaches at %gem,%gem; globe.go has %gem,%gem",
			reach[0], reach[1], sceneReachX, sceneReachY)
	}

	for i, city := range globeCities {
		got := cssNums(t, fmt.Sprintf(`\.acc\[data-on="1"\] \.acc-beam%d\{`+
			`--len:var\(--g-len,([\d.]+)em\);--ang:var\(--g-ang,(-?[\d.]+)deg\);\s*`+
			`opacity:var\(--g-vis,([01])\)\}`, i+1))
		length, angle, near := globeClientAt(city[0], city[1], globeFace)
		vis := 0.0
		if near {
			vis = 1
		}
		if math.Abs(got[0]-length) > .005 || math.Abs(got[1]-angle) > .05 || got[2] != vis {
			t.Errorf("app.css puts client %d at %gem %gdeg (shown %g) for the still; "+
				"globe.go has %.2fem %.1fdeg (shown %g)",
				i+1, got[0], got[1], got[2], length, angle, vis)
		}
	}

	// And the turn works from the same numbers, not from a copy of them.
	for _, want := range []string{
		"GX=" + sceneNum(sceneGlobeX), "GY=" + sceneNum(sceneGlobeY),
		"GR=" + sceneNum(sceneGlobeR), "RX=" + sceneNum(sceneReachX),
		"RY=" + sceneNum(sceneReachY), "CITY=" + globeCityJS(),
	} {
		if !strings.Contains(globeScript, want) {
			t.Errorf("the script does not carry %s; it would put the clients somewhere "+
				"other than the still does", want)
		}
	}
	// A reach is rewritten forty times a second, and a 360ms ease on top of that
	// leaves every client trailing its own city. The ease belongs to the switch
	// moving, so it comes off for the turn and goes back on for the next move.
	if !strings.Contains(appCSS, `.acc-tracking[data-on="1"] .acc-beam{transition-property:opacity}`) {
		t.Error("nothing takes the ease off the reaches while the world turns")
	}
	for _, want := range []string{"classList.add('acc-tracking')", "classList.remove('acc-tracking')"} {
		if !strings.Contains(globeScript, want) {
			t.Errorf("the script never does %s, so the ease is on at the wrong time", want)
		}
	}
}

// clientPoint is the far end of a reach, back in the picture's grid.
func clientPoint(length, angle float64) (x, y float64) {
	r := angle * math.Pi / 180
	return sceneReachX + length*math.Cos(r), sceneReachY + length*math.Sin(r)
}

// cssNums pulls one rule's numbers out of app.css, and fails rather than
// guessing: a pattern that no longer matches means the rule moved, and a test
// that quietly passed on nothing would be worse than the drift it is watching.
func cssNums(t *testing.T, pattern string) []float64 {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(appCSS)
	if m == nil {
		t.Fatalf("app.css has no rule matching %s", pattern)
	}
	out := make([]float64, len(m)-1)
	for i, s := range m[1:] {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("app.css: %q is not a number", s)
		}
		out[i] = v
	}
	return out
}

// A SWITCH IS NOT A TUNNEL. Flipping it draws the whole Global Access picture —
// ingress, uplink, the world lit — before anything has been saved, let alone
// dialled. The registry's actual setting is what the server rendered, so the
// scene carries both: data-saved never moves without a save, data-on follows the
// control, and while they differ the picture says it is a preview.
//
// It is a claim about the FORM, not about the connection. Nothing here reports
// that a tunnel is up; that would need the backend to say so.
func TestAccessScenePreviewIsNotAConnection(t *testing.T) {
	for _, on := range []bool{false, true} {
		scene := accessScene(on)
		state := "0"
		if on {
			state = "1"
		}
		if !strings.Contains(scene, `data-on="`+state+`" data-saved="`+state+`"`) {
			t.Errorf("accessScene(%v) does not render the saved setting beside the switch's", on)
		}
	}
	if !strings.Contains(accessScene(true), "Preview — not saved") {
		t.Error("the picture has no word for a switch that has moved and not been saved")
	}
	// Only the switch moves data-on. A save is what moves the other one, and the
	// page reloads with the server's answer.
	if strings.Contains(switchScript, "data-saved") {
		t.Error("the switch writes the saved state; then a preview could never be told from a save")
	}
	// Shown only where the two disagree, which is the whole of what it means.
	for _, want := range []string{
		`.acc[data-on="1"][data-saved="0"] .acc-tag-preview`,
		`.acc[data-on="0"][data-saved="1"] .acc-tag-preview`,
	} {
		if !strings.Contains(appCSS, want) {
			t.Errorf("the preview word is not keyed to %s", want)
		}
	}
	if rule := setRule(t, ".acc-tag-preview"); !strings.Contains(rule, "opacity:0") {
		t.Errorf("the preview word shows when the switch agrees with the setting: %s", rule)
	}
}
