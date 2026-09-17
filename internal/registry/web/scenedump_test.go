// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

// Throwaway: writes every scene, in every state that changes its labels, each
// inside the container its own page puts it in, so a browser can measure the
// labels against the drawing. A scene measured outside its wrapper is a scene
// measured at the wrong size — .usg-stage grows to fill a flex parent, .mbr-hero
// sits in half a band — and the false positives are the ones that eat the time.
//
//	TACIT_SCENE_DUMP=/tmp/scenes/scenes.html go test -run TestDumpScenes ./internal/registry/web
//	cp internal/ui/assets/app.css /tmp/scenes/ && hack/scenecheck.py /tmp/scenes/scenes.html
//
// hack/scenecheck.py is the other half: it renders this page at ten widths and
// asks, in pixels, whether any caption is sitting on any drawing.
func TestDumpScenes(t *testing.T) {
	out := os.Getenv("TACIT_SCENE_DUMP")
	if out == "" {
		t.Skip("no TACIT_SCENE_DUMP")
	}
	panelIn := func(cls, scene string) string {
		return `<section class="panel ` + cls + `">` + scene + `</section>`
	}
	// The Access plate shares a settings tab with a second plate, and the tab is
	// an auto-fit grid — so the plate is half the shell, not all of it.
	setTab := func(plate string) string {
		return `<div class="set"><div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(min(30rem,100%),1fr));gap:14px">` +
			plate + `<section class="panel set-plate"><h2>Sign-in</h2><div class="set-grid"><div class="set-row">b</div></div></section></div></div>`
	}
	tiles := `<div class="lrn-tiles">` + string(TileRow(false,
		Tile{Label: "Audit facts", Value: "912", Good: true},
		Tile{Label: "Days of facts", Value: "6", Delta: "of 14 needed"},
		Tile{Label: "Helped (28d)", Value: "3", Delta: "of 20 needed"},
		Tile{Label: "Cohorts", Value: "2", Delta: "of 3 needed"},
	)) + `</div>`
	scenes := []struct{ name, html string }{
		{"access-off", setTab(plateWith("Access", `<div class="set-row">a</div>`, accessScene(false)))},
		{"access-on", setTab(plateWith("Access", `<div class="set-row">a</div>`, accessScene(true)))},
		{"federation-out", panelIn("fed-hero", federationScene([]fedOut{{Title: "Public playbook", Count: 12, Open: true}}, nil))},
		{"federation-in", panelIn("fed-hero", federationScene(nil, []fedIn{{Name: "Platform", Review: true}, {Name: "Vendor"}}))},
		{"federation-both", panelIn("fed-hero", federationScene([]fedOut{{Title: "Public", Count: 3, Open: true}}, []fedIn{{Name: "Platform"}}))},
		{"usage", `<div class="usage usg-band">` + panelIn("usg-hero", usageScene()) +
			`<div id="usage-root"><section class="panel"><h2>Sessions</h2><p>x</p></section></div></div>`},
		{"team-0", `<section class="panel">` + teamScene(0) + `</section>`},
		{"team-36", `<section class="panel">` + teamScene(36) + `</section>`},
		// The switch's other position: a wider boundary, and four more people.
		{"team-on", `<section class="panel">` +
			strings.Replace(teamScene(36), `class="dgm tm-stage"`, `class="dgm tm-stage" data-on="1"`, 1) + `</section>`},
		{"members-none", `<div class="mbr-band"><div class="mbr-who"><h3>Who is here</h3>` +
			panelIn("mbr-hero", memberScene(nil, 0, false)) + `</div><div class="mbr-add"><h3>Add somebody</h3><p>x</p></div></div>`},
		// An organization's registry: no owner link, and the members have the
		// whole ring rather than an arrangement built round an absent anchor.
		{"members-org", `<div class="mbr-band"><div class="mbr-who"><h3>Who is here</h3>` +
			panelIn("mbr-hero", memberScene([]models.MemberKey{
				{ID: "a", Label: "a", LastSeen: "2026-09-01"}, {ID: "b", Label: "b", LastSeen: "2026-09-02"},
				{ID: "c", Label: "c", LastSeen: "2026-09-03"}, {ID: "d", Label: "d", LastSeen: "2026-09-04"},
				{ID: "e", Label: "e", LastSeen: "2026-09-05"},
			}, 59, false)) +
			`</div><div class="mbr-add"><h3>Add somebody</h3><p>x</p></div></div>`},
		{"members-few", `<div class="mbr-band"><div class="mbr-who"><h3>Who is here</h3>` +
			panelIn("mbr-hero", memberScene([]models.MemberKey{{ID: "a", Label: "alpha"}, {ID: "b", Label: "beta"}}, 12, true)) +
			`</div><div class="mbr-add"><h3>Add somebody</h3><p>x</p></div></div>`},
		{"setup", `<main class="setup-wrap">` + panelIn("stp-hero", setupScene("workshop:8080", "a file store")) + `</main>`},
		{"learning-cold", `<div class="lrn-band">` + panelIn("lrn-hero",
			learningScene(0, []learningGate{{Name: "Findings and detectors", Needs: []learningNeed{{What: "days of accrued facts", Need: 14}}}})) + tiles + `</div>`},
		{"learning-warm", `<div class="lrn-band">` + panelIn("lrn-hero",
			learningScene(912, []learningGate{{Name: "Dynamic cohorts", Needs: []learningNeed{{What: "audit facts", Have: 120, Need: 500}, {What: "distinct cohorts", Have: 2, Need: 3}}}})) + tiles + `</div>`},
		{"review-manual", panelIn("rev-hero", reviewScene(reviewCounts{Drafts: 16, Shadow: 4, Serving: 52}))},
		{"review-auto", panelIn("rev-hero", reviewScene(reviewCounts{Serving: 3, Decayed: 2, AutoShadow: true, AutoPromote: true}))},
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><link rel=stylesheet href=app.css><body class="reg"><div class="wrap">`)
	for _, s := range scenes {
		fmt.Fprintf(&b, `<div data-scene=%q><h3>%s</h3>%s</div>`, s.name, s.name, s.html)
	}
	b.WriteString(`</div></body>`)
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
