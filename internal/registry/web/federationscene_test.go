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
)

// A picture with nothing to show says nothing. An empty registry drew a grid
// with a lone plate in it, which reads as a diagram that failed to load; the
// page's own copy is better at "nothing yet, and here is the move".
func TestFederationSceneDrawsNothingWhenThereIsNothing(t *testing.T) {
	if got := federationScene(nil, nil); got != "" {
		t.Errorf("an empty registry still draws a picture: %q", got)
	}
	if federationScene([]fedOut{{Title: "Public", Open: true}}, nil) == "" {
		t.Error("one channel and no feeds should still draw the side that exists")
	}
	if federationScene(nil, []fedIn{{Name: "Peer"}}) == "" {
		t.Error("one feed and no channels should still draw the side that exists")
	}
}

// One item takes the MIDDLE row. Put at the top it reads as the first of three
// that failed to render — the picture would be claiming an absence it has no
// evidence for.
func TestFederationSceneCentresASingleRow(t *testing.T) {
	one := federationScene([]fedOut{{Title: "Public", Open: true}}, nil)
	if !strings.Contains(one, "fed-r2") || strings.Contains(one, "fed-r1") {
		t.Error("a single channel should sit on the middle row, not the top one")
	}
	two := federationScene(nil, []fedIn{{Name: "A"}, {Name: "B"}})
	if !strings.Contains(two, "fed-r1") || !strings.Contains(two, "fed-r3") ||
		strings.Contains(two, "fed-r2") {
		t.Error("two feeds should take the outer rows, so neither reads as the odd one")
	}
}

// THE ONE THING WORTH LOOKING AT. A feed on review trust stops outside the
// boundary until a person accepts it; a feed on auto-accept does not stop at
// all. That was a word in a badge, and it is the most consequential per-feed
// setting on the page.
func TestFederationSceneDrawsTheHoldOnlyWhereSomebodyStillDecides(t *testing.T) {
	held := federationScene(nil, []fedIn{{Name: "Platform", Review: true}})
	if !strings.Contains(held, "fed-hold") {
		t.Error("a feed that waits for review is drawn walking straight in")
	}
	if !strings.Contains(held, "waits for review") {
		t.Error("the label does not say what the hold is")
	}
	straight := federationScene(nil, []fedIn{{Name: "Vendor"}})
	if strings.Contains(straight, "fed-hold") {
		t.Error("an auto-accept feed is drawn stopping at a gate it does not stop at")
	}
	if !strings.Contains(straight, "walks straight in") {
		t.Error("the label does not say the feed is ungated")
	}
}

// Public is the one channel anybody can read. The bar across a port's mouth is
// where that rule is learned from the picture rather than from a document.
func TestFederationSceneBarsOnlyTheGatedChannels(t *testing.T) {
	open := federationScene([]fedOut{{Title: "Public", Count: 12, Open: true}}, nil)
	if strings.Contains(open, "dgm-bars") {
		t.Error("Public is drawn gated; nothing is needed to read it")
	}
	if !strings.Contains(open, "open to anyone") {
		t.Error("the label does not say Public is open")
	}
	gated := federationScene([]fedOut{{Title: "Partners", Count: 3}}, nil)
	if !strings.Contains(gated, "dgm-bars") {
		t.Error("a token-gated channel is drawn open")
	}
}

// The registry is lit when it is offering something and not when it is not. An
// unlit plate is a registry that publishes nothing, which is true and worth
// seeing.
func TestFederationSceneLightsTheRegistryOnlyWhenItPublishes(t *testing.T) {
	if s := federationScene(nil, []fedIn{{Name: "Peer"}}); strings.Contains(s, "dgm-lit") {
		t.Error("a registry that publishes nothing is drawn as though it were serving")
	}
	if s := federationScene([]fedOut{{Title: "Public", Open: true}}, nil); !strings.Contains(s, "dgm-lit") {
		t.Error("a registry that publishes is drawn dark")
	}
}

// Three a side and no more — a diagram of eleven feeds is a hairball. What was
// left out is SAID; a picture that quietly drew nine of eleven would be a lie,
// and this page's whole subject is what reaches whom.
func TestFederationSceneSaysWhatItLeftOut(t *testing.T) {
	var out []fedOut
	for i := 0; i < 5; i++ {
		out = append(out, fedOut{Title: "c"})
	}
	in := []fedIn{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}
	s := federationScene(out, in)
	if n := strings.Count(s, "dgm-port"); n != fedSceneMax {
		t.Errorf("drew %d ports; the cap is %d", n, fedSceneMax)
	}
	if n := strings.Count(s, "dgm-peer"); n != fedSceneMax {
		t.Errorf("drew %d peers; the cap is %d", n, fedSceneMax)
	}
	if !strings.Contains(s, "2 more channels and 1 more feed, listed below") {
		t.Error("the picture drops rows without saying how many")
	}
}

// No count is invented and no zero is faked: a channel nobody has published to
// says so rather than reporting a measurement of nothing.
func TestFederationSceneSaysNoneRatherThanZero(t *testing.T) {
	s := federationScene([]fedOut{{Title: "Partners", Count: 0}}, []fedIn{{Name: "Peer", Count: 0}})
	if strings.Contains(s, "0 technique") || strings.Contains(s, "0 imported") {
		t.Error("the picture reports a zero it did not measure")
	}
	if strings.Count(s, "none yet") != 2 {
		t.Error("an empty channel and an empty feed should both say so")
	}
	// "imported" is already a participle and takes no plural.
	if strings.Contains(federationScene(nil, []fedIn{{Name: "P", Count: 7}}), "importeds") {
		t.Error("7 importeds")
	}
}

// It is built from the shared diagram vocabulary and sets nothing of its own.
// The Go side places and labels; every colour and coordinate is in the
// stylesheet, so a theme or a hairline still has exactly one home.
func TestFederationSceneCarriesNoStylingOfItsOwn(t *testing.T) {
	s := federationScene([]fedOut{{Title: "Public", Count: 4, Open: true}},
		[]fedIn{{Name: "Peer", Count: 2, Review: true}})
	if strings.Contains(s, "style=") {
		t.Error("the picture sets style inline; geometry and colour live in app.css")
	}
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).MatchString(s) {
		t.Error("the picture names a colour; colour resolves from a token")
	}
	for _, shared := range []string{"dgm-scene", "dgm-node", "dgm-fence", "dgm-beam", "dgm-port"} {
		if !strings.Contains(s, shared) {
			t.Errorf("the picture does not use the shared %s; a copy would drift", shared)
		}
	}
	// Two series, because there are two kinds of flow and the page turns on
	// telling them apart. The stylesheet holds which is which.
	block := appCSS[strings.Index(appCSS, "/* ---- the Federation picture"):]
	if !strings.Contains(block, "--hue:var(--s5)") {
		t.Error("what arrives is drawn in the same colour as what leaves")
	}
}

// The hero stays FULL WIDTH and the picture is widened to fill it. Capping the
// plate to the drawing's shape fixed the empty board by leaving a strip of page
// that used none of its width — the same fault moved outward. 47 columns instead
// of 30: the peers move left, the ports and their readers move right, and the
// plate's height is what that aspect asks for. Measured at 1440: the scene fills
// 93% of the plate, against 52% before.
func TestFederationHeroFillsTheFullWidth(t *testing.T) {
	if strings.Contains(appCSS, ".fed-hero{padding:.9rem 1rem;margin:0 auto 14px;max-width:") {
		t.Error("the hero is capped; part of the page then uses none of its width")
	}
	// The literal grid used to be spelled out here, which made the test a copy of
	// the rule rather than a claim about it: the height later came down from 17
	// rows to 15, because the drawing only ever used 14 of them and the slack all
	// sat at the bottom. What matters is the shape it leaves, so that is measured.
	cols, rows := fedNum(t, ".fed-scene", "--cols"), fedNum(t, ".fed-scene", "--rows")
	if cols < 45 {
		t.Errorf("the composition is %.0f columns wide; a full-width plate needs about 47", cols)
	}
	if cols/rows < 2.7 {
		t.Errorf("the grid is %.0f x %.0f, an aspect of %.2f: too tall to fill a full-width plate",
			cols, rows, cols/rows)
	}
}

// Two directions, two columns. Side by side, the difference between them is the
// layout rather than a word in a heading.
func TestFederationDirectionsAreTwoColumns(t *testing.T) {
	if !strings.Contains(appCSS, ".fed-cols{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);") {
		t.Error("the two directions are not two columns")
	}
	if !strings.Contains(appCSS, "gap:14px;align-items:stretch}") {
		t.Error("the two direction plates do not take the same height")
	}
	if !strings.Contains(appCSS, "@media (max-width:1100px){.fed-cols{grid-template-columns:minmax(0,1fr)}}") {
		t.Error("the columns do not stack on a narrow window")
	}
}

// The hold wears the SAME MESH a gated channel and a review gate wear, because
// it means the same thing in all three: something is required to pass. It was a
// plain dashed square, which in this vocabulary says "a boundary" and not "a
// person has to accept this" — the first person to see it asked what it was for.
func TestFederationHoldWearsTheSameMeshAsEveryOtherGate(t *testing.T) {
	held := federationScene(nil, []fedIn{{Name: "P", Review: true}})
	if !strings.Contains(held, `class="dgm-mesh fed-hold`) {
		t.Error("the hold is not drawn as a mesh; it will read as another boundary")
	}
	// A mesh says something is required to pass; only the word says what.
	if !strings.Contains(held, "<b>Review</b>") {
		t.Error("the hold is unlabelled")
	}
	if strings.Contains(federationScene(nil, []fedIn{{Name: "V"}}), "<b>Review</b>") {
		t.Error("an auto-accept feed is labelled with a review it does not wait for")
	}
	gated := federationScene([]fedOut{{Title: "c"}}, nil)
	if !strings.Contains(gated, `class="dgm-mesh dgm-bars"`) {
		t.Error("a token-gated channel is not drawn as a mesh")
	}
	// Square marker, square cells: the port's four-by-eight would put the
	// horizontals at twice the density of the uprights.
	if !strings.Contains(appCSS, "--mesh-y:25%") {
		t.Error("the hold keeps a port's row spacing in a square box")
	}
	// AND THEY MUST NOT PILE INTO EACH OTHER. The inbound beams converge on the
	// boundary, so a marker set far enough along its line meets the markers on
	// the other two. This used to be pinned as a literal coordinate, which said
	// where they were and not what was wrong with anywhere else; the holds have
	// since moved to the middle of their own lines, and the thing worth keeping
	// is that three of them still clear each other.
	size := fedNum(t, ".fed-hold", "height")
	var y [4]float64
	for r := 1; r <= 3; r++ {
		y[r] = fedNum(t, fmt.Sprintf(".fed-hold.fed-r%d", r), "top")
	}
	for _, p := range [][2]int{{1, 2}, {2, 3}} {
		if gap := y[p[1]] - y[p[0]]; gap < size {
			t.Errorf("the holds on rows %d and %d are %.2fem apart and %.2fem tall: they overlap",
				p[0], p[1], gap, size)
		}
	}
}

// A FEED THAT IS NOT ARRIVING IS NOT DRAWN ARRIVING. The last poll's error sits
// on the subscription and the picture ignored it, so a feed whose host does not
// resolve still ran traffic down its line — the picture asserting a flow that
// had stopped. The trust only matters to something that arrives, so the label
// says the failure instead of the gate.
func TestFederationSceneDrawsNoTrafficOnAFeedThatIsFailing(t *testing.T) {
	bad := federationScene(nil, []fedIn{{Name: "P", Count: 7, Review: true, Failing: true}})
	if !strings.Contains(bad, "dgm-beam-bare dgm-beam-pending fed-in") {
		t.Error("a failing feed is drawn carrying traffic")
	}
	if !strings.Contains(bad, "7 imported · not arriving") {
		t.Error("the label does not say the feed has stopped")
	}
	if strings.Contains(bad, "waits for review") {
		t.Error("a failing feed is labelled with a gate nothing is reaching")
	}
	ok := federationScene(nil, []fedIn{{Name: "P", Count: 7, Review: true}})
	if strings.Contains(ok, "dgm-beam-pending fed-in") {
		t.Error("a healthy feed is drawn inert")
	}
}

// AND A CHANNEL NOBODY HAS READ IS NOT DRAWN BEING READ. "Is the peer we set
// this up for still fetching?" is the question the channel's meta line exists to
// answer, and the picture answers it at a glance.
func TestFederationSceneShowsWhetherAChannelIsActuallyRead(t *testing.T) {
	unread := federationScene([]fedOut{{Title: "Partners", Count: 2}}, nil)
	if !strings.Contains(unread, "dgm-beam dgm-beam-pending fed-serve") {
		t.Error("a channel nobody has fetched is drawn being read")
	}
	if !strings.Contains(unread, "no reader yet") {
		t.Error("the label does not say the channel has no reader")
	}
	read := federationScene([]fedOut{{Title: "Partners", Count: 2, LastRead: "2026-09-01T10:00:00Z"}}, nil)
	if strings.Contains(read, "dgm-beam-pending fed-serve") {
		t.Error("a channel somebody fetched is drawn inert")
	}
	if !strings.Contains(read, "read ") {
		t.Error("the label does not say when the channel was last read")
	}
	// Public is fetched anonymously by design, so an empty timestamp there means
	// "we cannot see", not "nobody came" — and the picture must not claim it.
	pub := federationScene([]fedOut{{Title: "Public", Count: 9, Open: true}}, nil)
	if strings.Contains(pub, "dgm-beam-pending fed-serve") || strings.Contains(pub, "no reader yet") {
		t.Error("Public is drawn unread; anonymous fetches leave no trace to read")
	}
}

// fedRule is one rule out of the Federation picture's own block.
func fedRule(t *testing.T, sel string) string {
	t.Helper()
	block := appCSS[strings.Index(appCSS, "/* ---- the Federation picture"):]
	block = block[:strings.Index(block, "/* ---- the Usage picture")]
	i := strings.Index(block, sel+"{")
	if i < 0 {
		t.Fatalf("no %s rule in the Federation picture's block", sel)
	}
	rule := block[i:]
	return rule[:strings.Index(rule, "}")+1]
}

// fedNum pulls one em value out of a rule.
func fedNum(t *testing.T, sel, prop string) float64 {
	t.Helper()
	rule := fedRule(t, sel)
	m := regexp.MustCompile(prop + `:(-?[0-9.]+)(?:em|deg|%)?`).FindStringSubmatch(rule)
	if m == nil {
		t.Fatalf("no %s in %s", prop, rule)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("%s in %s: %v", prop, rule, err)
	}
	return v
}

// EVERY LINE IN THIS PICTURE HANGS OFF THE BOUNDARY'S MIDDLE, and for a long
// time the row ladder did not: it ran 4.6/9.3/14 while the fence's centre was
// 8.3, so the middle rung sat a full em below the thing every beam starts or
// ends at. One number, and it bent the whole drawing — the single inbound feed
// arrived on a slope instead of level, the review gate sat off the middle of its
// own line, and the outbound fan dropped 5.7em on one side while rising 3.7 on
// the other.
//
// So the rows are checked against the boundary rather than against themselves,
// and the two directions are checked for the mirror they are supposed to be.
func TestFederationSceneIsSymmetricAboutItsBoundary(t *testing.T) {
	axis := fedNum(t, ".fed-fence", "top") + fedNum(t, ".fed-fence", "height")/2
	left := fedNum(t, ".fed-fence", "left")
	right := left + fedNum(t, ".fed-fence", "width")

	ry := map[int]float64{}
	for r := 1; r <= 3; r++ {
		ry[r] = fedNum(t, fmt.Sprintf(".fed-r%d", r), "--ry")
	}
	if math.Abs(ry[2]-axis) > 0.01 {
		t.Errorf("the middle row is at %.2fem but the boundary's centre is %.2fem; "+
			"a single feed will arrive on a slope", ry[2], axis)
	}
	if up, down := axis-ry[1], ry[3]-axis; math.Abs(up-down) > 0.01 {
		t.Errorf("the rows sit %.2fem above the boundary and %.2fem below it; "+
			"the fan cannot be symmetric", up, down)
	}

	// What arrives: every feed lands on the boundary's left edge, at its middle.
	for r := 1; r <= 3; r++ {
		sel := fmt.Sprintf(".fed-in.fed-r%d", r)
		x, y := fedNum(t, sel, "--x"), fedNum(t, sel, "--y")
		l, a := fedNum(t, sel, "--len"), fedNum(t, sel, "--ang")*math.Pi/180
		ex, ey := x+l*math.Cos(a), y+l*math.Sin(a)
		if math.Abs(ex-left) > 0.02 || math.Abs(ey-axis) > 0.02 {
			t.Errorf("%s ends at (%.2f, %.2f) em, not on the boundary at (%.2f, %.2f)",
				sel, ex, ey, left, axis)
		}
		// And the gate on it stands at its middle, which is the whole point of a
		// gate you can see: it is on the line, halfway along.
		hold := fmt.Sprintf(".fed-hold.fed-r%d", r)
		hx, hy := fedNum(t, hold, "left"), fedNum(t, hold, "top")
		if math.Abs(hx-(x+ex)/2) > 0.02 || math.Abs(hy-(y+ey)/2) > 0.02 {
			t.Errorf("%s sits at (%.2f, %.2f) em; the middle of its line is (%.2f, %.2f)",
				hold, hx, hy, (x+ex)/2, (y+ey)/2)
		}
	}

	// What leaves: every channel starts at the boundary's right edge, at its
	// middle, and the outer rows mirror each other.
	var lens, angs [4]float64
	for r := 1; r <= 3; r++ {
		sel := fmt.Sprintf(".fed-lead.fed-r%d", r)
		x, y := fedNum(t, ".fed-lead", "--x"), fedNum(t, ".fed-lead", "--y")
		if math.Abs(x-right) > 0.02 || math.Abs(y-axis) > 0.02 {
			t.Fatalf("the outbound fan leaves (%.2f, %.2f) em, not the boundary at (%.2f, %.2f)",
				x, y, right, axis)
		}
		lens[r], angs[r] = fedNum(t, sel, "--len"), fedNum(t, sel, "--ang")
	}
	if math.Abs(angs[2]) > 0.01 {
		t.Errorf("the middle channel leaves at %.2f degrees; it is level with the boundary", angs[2])
	}
	if math.Abs(lens[1]-lens[3]) > 0.02 || math.Abs(angs[1]+angs[3]) > 0.02 {
		t.Errorf("the fan rises %.2fem at %.2f degrees and falls %.2fem at %.2f: it is lopsided",
			lens[1], angs[1], lens[3], angs[3])
	}
}

// AN OPEN CHANNEL HAS NO READER TO DRAW. Anonymous readership is not observed —
// a public feed is fetched by whoever has the URL, and this registry never sees
// them — so the line leaves the port and stops. It used to end in the same solid
// dot a token-holding reader gets, which is a person the registry has never met.
//
// The other two ends still say what they know: a hollow end where a token was
// minted and nobody has used it, a solid one where somebody actually fetched.
func TestFederationDrawsNoReaderItCannotSee(t *testing.T) {
	open := federationScene([]fedOut{{Title: "public", Count: 4, Open: true}}, nil)
	if !strings.Contains(open, `class="dgm-beam dgm-beam-bare fed-serve`) {
		t.Error("an open channel still ends in a reader; nothing here observes one")
	}
	if !strings.Contains(open, "open to anyone") {
		t.Error("the open channel does not say who may read it")
	}
	unread := federationScene([]fedOut{{Title: "gated", Count: 4}}, nil)
	if !strings.Contains(unread, `class="dgm-beam dgm-beam-pending fed-serve`) {
		t.Error("a channel whose token was never used is drawn being read")
	}
	read := federationScene([]fedOut{{Title: "gated", Count: 4, LastRead: "2026-09-01T00:00:00Z"}}, nil)
	if !strings.Contains(read, `class="dgm-beam fed-serve`) {
		t.Error("a channel somebody has actually fetched draws no reader")
	}
}

// A FAILING FEED'S PROBLEM IS THE FETCH. It drew the review gate as well, which
// is a barred shape with nothing beside it to say what it bars: the label on a
// feed that is not arriving says exactly that, in place of the gate's own word.
func TestFederationDrawsNoGateOnAFeedThatIsNotArriving(t *testing.T) {
	failing := federationScene(nil, []fedIn{{Name: "stale", Review: true, Failing: true}})
	if strings.Contains(failing, "fed-hold") {
		t.Error("a feed that is not arriving still draws the gate its arrivals would wait at")
	}
	if !strings.Contains(failing, "not arriving") {
		t.Error("the failing feed does not say so")
	}
	arriving := federationScene(nil, []fedIn{{Name: "live", Count: 3, Review: true}})
	if !strings.Contains(arriving, "fed-hold") || !strings.Contains(arriving, "waits for review") {
		t.Error("a feed that arrives under review draws no gate")
	}
}
