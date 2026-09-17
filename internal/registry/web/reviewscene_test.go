// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/ui"
)

// THE DRAFT GATE NEVER OPENS. The first version of this drawing showed drafts
// flowing into evaluation with a gate between them that automation could open,
// which says two false things: that a draft can progress without a person, and
// that a promoted draft goes on to be evaluated. contribute.EntryStatus decides
// at CREATION — a machine-written technique is filed as a draft or as
// under-evaluation, and that is the whole of what the auto-evaluate switch does.
// A draft a person promotes goes straight into service.
func TestReviewSceneNeverOpensTheDraftGate(t *testing.T) {
	for _, c := range []reviewCounts{
		{}, {AutoShadow: true}, {AutoShadow: true, AutoPromote: true},
	} {
		s := reviewScene(c)
		g1 := s[strings.Index(s, "rev-g1"):]
		g1 = g1[:strings.Index(g1, "</div></div>")]
		if !strings.Contains(g1, "dgm-bars") {
			t.Errorf("with %+v the draft gate is open; a draft always waits for somebody", c)
		}
	}
	// And the label on it never changes either.
	if strings.Count(reviewScene(reviewCounts{AutoShadow: true, AutoPromote: true}), "you decide") != 1 {
		t.Error("the draft gate's label moved with a switch that does not control it")
	}
}

// What the auto-evaluate switch DOES control is which way a new machine-written
// technique goes at the fork — so that is where the picture says it.
func TestReviewSceneForksAtIntake(t *testing.T) {
	for _, part := range []string{"rev-fork-up", "rev-fork-down", "rev-serve-up", "rev-serve-down"} {
		if !strings.Contains(reviewScene(reviewCounts{}), part) {
			t.Errorf("the picture is missing %s; it is a fork, not a lane", part)
		}
	}
	off := reviewScene(reviewCounts{})
	on := reviewScene(reviewCounts{AutoShadow: true})
	if !strings.Contains(off, "machine-written ones as well") {
		t.Error("with auto-evaluate off, the fork does not say where a machine technique lands")
	}
	if !strings.Contains(on, "machine-written ones start under evaluation") {
		t.Error("with auto-evaluate on, the fork does not say where a machine technique lands")
	}
}

// The evaluation gate is the one that opens, and only when the operator has said
// the fit evidence may decide.
func TestReviewSceneOpensOnlyTheEvaluationGate(t *testing.T) {
	shut := reviewScene(reviewCounts{})
	if n := strings.Count(shut, "dgm-bars"); n != 2 {
		t.Errorf("%d barred gates with no automation on; both still need a person", n)
	}
	open := reviewScene(reviewCounts{AutoPromote: true})
	if n := strings.Count(open, "dgm-bars"); n != 1 {
		t.Errorf("%d barred gates with auto-promote on; the draft gate stays shut", n)
	}
	if !strings.Contains(open, "automatic") {
		t.Error("the evaluation gate does not say it is automatic")
	}
}

// The exit is the one lane nobody asks for. It is dashed and inert while nothing
// is leaving, and a live line the moment a technique's evidence turns.
func TestReviewSceneDrawsTheExitOnlyAsLiveAsItIs(t *testing.T) {
	quiet := reviewScene(reviewCounts{Serving: 9})
	if !strings.Contains(quiet, "dgm-beam-pending rev-out") {
		t.Error("nothing is decaying, but the exit is drawn carrying something")
	}
	if !strings.Contains(quiet, "retires automatically when its helped rate declines") {
		t.Error("the exit does not say what it is for")
	}
	busy := reviewScene(reviewCounts{Serving: 9, Decayed: 2})
	if strings.Contains(busy, "dgm-beam-pending rev-out") {
		t.Error("two techniques are retiring and the exit is drawn inert")
	}
	if !strings.Contains(busy, "2 retiring due to declining helped rates") {
		t.Error("the exit does not say how many are leaving")
	}
}

// An empty lane is a state, not a measurement of nothing — and the names are the
// ones the rest of the page uses, so the picture invents no third vocabulary.
func TestReviewSceneNamesEmptyLanesAndReusesThePagesWords(t *testing.T) {
	s := reviewScene(reviewCounts{Drafts: 0, Shadow: 3, Serving: 30})
	if strings.Contains(s, "0 draft") {
		t.Error("the picture reports a zero where the lane is simply empty")
	}
	if !strings.Contains(s, "<b>Empty</b>") {
		t.Error("an empty lane does not say so")
	}
	for _, word := range []string{"requires review", "under evaluation", "available in retrieval"} {
		if !strings.Contains(s, word) {
			t.Errorf("the picture does not use the page's own %q", word)
		}
	}
	if strings.Contains(s, "shadow") {
		t.Error("the picture says shadow; the lane is called under evaluation everywhere a member meets it")
	}
}

// Built from the shared vocabulary, setting nothing of its own.
func TestReviewSceneCarriesNoStylingOfItsOwn(t *testing.T) {
	s := reviewScene(reviewCounts{Drafts: 1, Shadow: 1, Serving: 1, Decayed: 1})
	if strings.Contains(s, "style=") {
		t.Error("the picture sets style inline; geometry and colour live in app.css")
	}
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(s) {
		t.Error("the picture names a colour; colour resolves from a token")
	}
	for _, shared := range []string{"dgm-scene", "dgm-node", "dgm-beam", "dgm-port", "dgm-tag"} {
		if !strings.Contains(s, shared) {
			t.Errorf("the picture does not use the shared %s; a copy would drift", shared)
		}
	}
}

// The barred marker has to LOOK blocked. It was one upright with two short
// crossbars, which read as a ‡ floating in the box — the first person to see it
// asked what it was, which is the whole test a wordless marker has to pass. It
// is a MESH now — four uprights and eight crossings — because a few bars still
// read as a thing with gaps between them.
func TestBarredGateLooksBlocked(t *testing.T) {
	rule := setRule(t, ".dgm-mesh")
	if n := strings.Count(rule, "repeating-linear-gradient"); n != 2 {
		t.Errorf("the barred marker has %d axes; a mesh needs both, and one alone reads as gaps", n)
	}
	// THE CELLS ARE SQUARE, so the spacing is two variables rather than two
	// constants: four by eight is right in a port's mouth, which is twice as tall
	// as it is wide, and wrong in anything else.
	if !strings.Contains(rule, "var(--mesh-x)") || !strings.Contains(rule, "var(--mesh-y)") {
		t.Error("the mesh's spacing is fixed; a marker that is not a port's shape gets uneven cells")
	}
	// Each gradient starts at 0, which draws the top and left edges and every
	// line inside — but not the last of either run. Without these two the frame
	// is open on the right and the bottom and reads as a corner, not a panel.
	if !strings.Contains(rule, "box-shadow:inset -1px 0 0 currentColor,inset 0 -1px 0 currentColor") {
		t.Error("the mesh is open on the right or the bottom")
	}
	// Not borders: a border shrinks the padding box the gradients repeat over,
	// and the interior spacing goes out of step with the frame around it.
	if strings.Contains(rule, "border") {
		t.Error("the mesh's frame is a border; it will not line up with its own interior")
	}
	// No crossbars: the pseudo-elements that drew them are what made it a ‡.
	block := appCSS[strings.Index(appCSS, "/* ---- diagram primitives"):]
	block = block[:strings.Index(block, "/* ---- the Access picture")]
	if strings.Contains(block, ".dgm-bars::before") || strings.Contains(block, ".dgm-bars::after") {
		t.Error("the crossbars are back")
	}
	// A gradient is not a border, so forced colours needs something to draw.
	forced := block[strings.Index(block, "@media (forced-colors:active)"):]
	if !strings.Contains(forced, ".dgm-mesh{background:none;box-shadow:none;border:1px solid CanvasText}") {
		t.Error("the mesh vanishes in a two-colour palette; a gradient is not a border")
	}
}

// reviewRule is one rule out of the Review picture's own block, so a test that
// checks where something sits cannot accidentally read a coordinate from
// another scene that happens to share a suffix.
func reviewRule(t *testing.T, sel string) string {
	t.Helper()
	block := appCSS[strings.Index(appCSS, "/* ---- the Review picture"):]
	block = block[:strings.Index(block, "/* ---- the Members picture")]
	i := strings.Index(block, sel+"{")
	if i < 0 {
		t.Fatalf("no %s rule in the Review picture's block", sel)
	}
	rule := block[i:]
	return rule[:strings.Index(rule, "}")+1]
}

// anchor reads the point a tag is centred on, in the scene's own em.
func anchor(t *testing.T, sel string) (x, y float64) {
	t.Helper()
	rule := reviewRule(t, sel)
	m := regexp.MustCompile(`left:([0-9.]+)%;top:([0-9.]+)%`).FindStringSubmatch(rule)
	if m == nil {
		t.Fatalf("%s is not anchored on a point: %s", sel, rule)
	}
	l, _ := strconv.ParseFloat(m[1], 64)
	tp, _ := strconv.ParseFloat(m[2], 64)
	cols, rows := revGrid(t)
	return l / 100 * cols, tp / 100 * rows
}

// beamVars reads one beam's origin and heading out of its own rule, in the em
// the scene measures everything in.
func beamVars(t *testing.T, sel string) (x, y, length, ang float64) {
	t.Helper()
	rule := reviewRule(t, sel)
	m := regexp.MustCompile(`--x:(-?[0-9.]+)em;--y:(-?[0-9.]+)em;--len:([0-9.]+)em;--ang:(-?[0-9.]+)deg`).
		FindStringSubmatch(rule)
	if m == nil {
		t.Fatalf("cannot read %s's geometry from %s", sel, rule)
	}
	v := make([]float64, 4)
	for i := range v {
		v[i], _ = strconv.ParseFloat(m[i+1], 64)
	}
	return v[0], v[1], v[2], v[3]
}

// markCentre is where a scene puts the registry's badge.
func markCentre(t *testing.T, sel string) (x, y float64) {
	t.Helper()
	rule := reviewRule(t, sel)
	m := regexp.MustCompile(`left:([0-9.]+)em;top:([0-9.]+)em`).FindStringSubmatch(rule)
	if m == nil {
		t.Fatalf("%s is not placed in scene em: %s", sel, rule)
	}
	x, _ = strconv.ParseFloat(m[1], 64)
	y, _ = strconv.ParseFloat(m[2], 64)
	return x, y
}

// The grid the Review picture declares, and the fork a label has to stay out of.
//
// READ FROM THE STYLESHEET, not copied from it. These were four literals, and
// three of the four were already describing a drawing that had moved on — rows
// had gone 15 → 18 → 17 and the fork had been respread and reangled, while the
// test went on measuring clearances against the old shape and passing. A
// constant that mirrors a stylesheet is a constant that will be wrong, quietly,
// in whichever direction nobody is looking.
func revGrid(t *testing.T) (cols, rows float64) {
	t.Helper()
	rule := reviewRule(t, ".rev-scene")
	m := regexp.MustCompile(`--cols:([0-9.]+);--rows:([0-9.]+)`).FindStringSubmatch(rule)
	if m == nil {
		t.Fatalf("the scene declares no grid: %s", rule)
	}
	cols, _ = strconv.ParseFloat(m[1], 64)
	rows, _ = strconv.ParseFloat(m[2], 64)
	return cols, rows
}

// revFork is where the arrival splits and how steeply each branch leaves it,
// off the upper arm's own rule.
func revFork(t *testing.T) (x, y, ang float64) {
	t.Helper()
	fx, fy, _, fa := beamVars(t, ".rev-fork-up")
	return fx, fy, math.Abs(fa)
}

// THE INTAKE LABEL LEFT THE SPLIT WHEN THE TYPE GREW. It used to sit in the
// mouth of the fork, which a four-point caption fits and a reading-size one does
// not: the wedge opens at about 0.62em of clear air per em out from the split,
// and three lines of 13px type need more than that anywhere the label would
// still be left of the drafts stack. Pushed right until the wedge was deep
// enough, it landed on the stack instead.
//
// So it hangs from the top-left corner of the scene and names the branch point
// from above it. The corner is the whole of the placement — a label anchored to
// an edge cannot be clipped by that edge, whatever height the frame is given —
// and what this checks is that it stays out of the upper arm, which is the one
// thing above the fork it could still cross.
func TestReviewIntakeLabelClearsTheUpperArm(t *testing.T) {
	rule := reviewRule(t, ".rev-tag-in")
	for _, want := range []string{"left:0", "top:0", "--ax:0", "--ay:0"} {
		if !strings.Contains(rule, want) {
			t.Fatalf("the intake label is no longer hung from the corner (%s missing): %s", want, rule)
		}
	}
	m := regexp.MustCompile(`--tag-w:([0-9.]+)`).FindStringSubmatch(rule)
	if m == nil {
		t.Fatalf("the intake label states no width to wrap at: %s", rule)
	}
	w, _ := strconv.ParseFloat(m[1], 64)

	// Four lines of type — a heading that wraps to two, and two of secondary —
	// which is what the longest of the two intake phrases comes to at this width.
	// In scene em it depends on how big the scene is drawn, so the test takes the
	// SMALLEST unit the wide picture is ever drawn at: below that the phone rules
	// take over and the label is somewhere else entirely.
	const lines = 18 + 18 + 16 + 16 + 4
	h := lines / revNarrowestUnit

	// Hung from the corner, the label runs from the scene's origin.
	forkX, _, _ := revFork(t)
	if w > forkX+wedgeHalfInv(t, h) {
		t.Errorf("the intake label is %.1fem wide and %.2fem tall; at that width its "+
			"lower-right corner is inside the fork's upper arm", w, h)
	}
}

// revNarrowestUnit is the smallest px-per-em the wide Review picture is drawn at
// — a 761px window, the last one before the narrow rules reflow it into the
// vertical drawing — measured in a browser across the widths this scene is
// checked at. It was 16.3 when that handover happened at 520px, and the extra
// two pixels per em are the whole reason the captions stopped crossing the
// lines: the type is absolute px, so a bigger unit is a smaller caption.
const revNarrowestUnit = 18.0

// wedgeHalfInv is the distance out from the split at which the fork opens h of
// clear air on each side. The wedge is nothing at the split and widens from
// there, so the corner of a label nearest the split is the one that has to fit.
func wedgeHalfInv(t *testing.T, h float64) float64 {
	_, _, forkAng := revFork(t)
	return h / math.Tan(forkAng*math.Pi/180)
}

// THE PICTURE IS SIZED BY WHICHEVER OF ITS TWO DIMENSIONS RUNS OUT FIRST, and
// for a long time that was always the rows. A left-to-right flow with a fork and
// a merge was drawn on a 30x18 grid — a shape half again as wide as it is tall —
// inside a panel three and a half times as wide as it is tall, so the unit came
// off the height and four hundred pixels of empty plate sat down each side.
//
// The cure was not more height. It was admitting the grid was the wrong shape:
// spread along the axis the flow actually travels, and stop holding three rows
// of air for captions that no longer need it. Both numbers matter and neither
// is arbitrary, so both are asserted — a later hand tempted to "tidy" the grid
// back toward square will take the width with it.
func TestReviewPictureIsShapedLikeThePanelItIsDrawnIn(t *testing.T) {
	cols, rows := revGrid(t)
	if got := cols / rows; got < 2.0 {
		t.Errorf("the grid is %.0fx%.0f (%.2f:1); a flow this wide drawn that square "+
			"sizes itself off the rows and leaves the panel's width empty", cols, rows, got)
	}
	// The drawing has to REACH the far side of its own grid, or spreading the
	// grid just buys more empty plate. The exit is the last thing on the right.
	x, _, _, _ := beamVars(t, ".rev-out")
	if x < cols*0.8 {
		t.Errorf("the flow ends at %.1fem of %.0f columns; the grid is wider than the drawing", x, cols)
	}
}

// BELOW 760px THE WIDE DRAWING GIVES WAY TO THE VERTICAL ONE, which is 240px
// earlier than it used to. A tag's type is absolute px while the scene scales
// with its own unit, so every column the window loses is a line another caption
// gains — and a caption that gains a line grows down into the drawing, where its
// own background cuts the line it crosses. The wide picture is clear of its
// captions down to a 700-pixel plate and not below it. The vertical layout was
// always the right answer in that band; it was just starting too late.
func TestReviewHandsTheNarrowBandToTheVerticalDrawing(t *testing.T) {
	i := strings.Index(appCSS, ".rev-tag-out{")
	if i < 0 {
		t.Fatal("the wide layout is gone")
	}
	rest := appCSS[i:]
	j := strings.Index(rest, "@media (max-width:")
	if j < 0 {
		t.Fatal("the wide layout has no narrow counterpart")
	}
	m := regexp.MustCompile(`@media \(max-width:([0-9]+)px\)\{`).FindStringSubmatch(rest[j:])
	if m == nil {
		t.Fatal("cannot read the handover breakpoint")
	}
	px, _ := strconv.Atoi(m[1])
	if px < 700 {
		t.Errorf("the wide drawing is kept down to %dpx; below about 700 its captions "+
			"wrap into the fork and onto the stacks", px)
	}
	// And the vertical drawing is what takes over there.
	if !strings.Contains(rest[j:j+900], ".rev-scene{--cols:14") {
		t.Error("the narrow block does not reflow the scene into its vertical grid")
	}
}

// And the exit caption is the longest string in the picture, so it is the one
// that runs off the right edge. Centred on a point it did exactly that. It hangs
// from the corner it was overflowing instead: an edge-hung label cannot be
// clipped by that edge whatever it says, at any size the frame is drawn.
func TestReviewExitCaptionStaysInsideTheScene(t *testing.T) {
	rule := reviewRule(t, ".rev-tag-out")
	for _, want := range []string{"right:0", "bottom:0", "--ax:0", "--ay:0"} {
		if !strings.Contains(rule, want) {
			t.Errorf("the exit caption is not hung from the bottom-right corner (%s missing): %s", want, rule)
		}
	}
	if !strings.Contains(rule, "--tag-w:") {
		t.Error("the exit caption states no width to wrap at, so the longest phrase sets the picture's width")
	}
}

// A NEW TECHNIQUE COMES FROM SOMEWHERE, AND THE PICTURE SAYS WHERE. It used to
// open on a bare beam arriving from off-scene, which says something turns up
// without saying what makes it. The registry's own mark stands at the branch
// point instead — the same badge it wears in every other picture.
//
// Which only reads as a source if the two branches actually leave it, so that is
// what this measures: extrapolate both beams back and they must meet on the
// mark, not somewhere near it.
func TestReviewSceneMarksWhereTechniquesComeFrom(t *testing.T) {
	scene := reviewScene(reviewCounts{Drafts: 2, Shadow: 1, Serving: 9})
	if !strings.Contains(scene, `class="dgm-mark rev-mark"`) {
		t.Fatal("the Review picture has no mark, so its fork begins at nothing")
	}
	if !strings.Contains(scene, ui.MarkSVG) {
		t.Error("the mark is not the shared one; a second drawing of it is a second brand")
	}

	ux, uy, _, ua := beamVars(t, ".rev-fork-up")
	dx, dy, _, da := beamVars(t, ".rev-fork-down")
	// Both run right and away from the same point, so walking each back by the
	// distance that closes the gap between them lands on it.
	tanU, tanD := math.Tan(ua*math.Pi/180), math.Tan(da*math.Pi/180)
	if tanU == tanD {
		t.Fatal("the two branches are parallel; there is no fork")
	}
	// y = uy + (x-ux)*tanU and y = dy + (x-dx)*tanD, solved for x.
	x := (dy - uy + ux*tanU - dx*tanD) / (tanU - tanD)
	y := uy + (x-ux)*tanU

	mx, my := markCentre(t, ".rev-mark")
	if math.Abs(x-mx) > 0.05 || math.Abs(y-my) > 0.05 {
		t.Errorf("the branches meet at (%.2f, %.2f) em but the mark stands at (%.2f, %.2f): "+
			"the fork does not come out of it", x, y, mx, my)
	}
	// And the label must not sit on the mark it stands above. Hung from the top
	// corner it grows downward, so what matters is how far down it reaches at the
	// smallest unit the wide picture is drawn at.
	const markHalf = 1.1 // .dgm-mark is 2.2em square, centred on its point
	const lines = 18 + 18 + 16 + 16 + 4
	if h := lines / revNarrowestUnit; h > my-markHalf {
		t.Errorf("the intake label reaches %.2fem down the scene, onto the mark that starts at %.2fem",
			h, my-markHalf)
	}
}
