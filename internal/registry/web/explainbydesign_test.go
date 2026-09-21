// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/ui"
)

// The dashboard used to explain itself in prose: a heading, a paragraph saying
// what the panel was, a table whose columns needed a second paragraph to say
// what their numbers were per, and a third saying what the numbers did not
// prove. Around two hundred of them.
//
// Every one was written because the design left a question open, and a member
// reading a figure does not read the paragraph above it. So the questions moved
// into the marks that raise them — the unit onto the column header, the
// categories onto a legend, the denominator in front of the rates that divide
// by it, the caveats into one closed disclosure — and the paragraphs went.
//
// This file is the ratchet. Each test below fails if a specific paragraph comes
// back, or if the component that replaced it disappears.

// The four components the prose was traded for. If one of these goes, the
// paragraphs will come back, because the question it answers will be open again.
func TestTheComponentsThatReplacedTheProseExist(t *testing.T) {
	// Go side.
	// The name is wrapped so the sort caret can attach to the name LINE rather
	// than to the end of a two-line cell.
	if ui.UH("Cost", "per session") !=
		`<th class="num has-u"><span class="hd">Cost</span><span class="u">per session</span></th>` {
		t.Errorf("UH does not put the unit on the column: %s", ui.UH("Cost", "per session"))
	}
	if got := ui.Sub("Turn times", "over 4,231 turns"); !strings.Contains(got, `class="u"`) {
		t.Errorf("Sub does not carry a unit: %s", got)
	}
	// A unit with no heading of its own is a caption, not an entry in the
	// document outline a screen reader navigates by.
	if got := ui.Sub("", "of adoptions"); strings.Contains(got, "<h3") {
		t.Errorf("a bare unit renders as a heading: %s", got)
	}
	if got := ui.Fine("one", "", "two"); !strings.HasPrefix(got, `<details class="fineprint">`) ||
		strings.Count(got, "<li>") != 2 {
		t.Errorf("Fine is not a closed disclosure over its non-empty items: %s", got)
	}
	if ui.Fine("", "") != "" {
		t.Error("Fine renders an empty disclosure")
	}
	// Browser side, for the views whose data arrives by fetch.
	for _, want := range []string{"function uh(name,unit){", "function sub3(title,unit){",
		"function fine(items){", "function chip(label,on,value){"} {
		if !strings.Contains(usageJS, want) {
			t.Errorf("the Usage page lost %s", want)
		}
	}
}

// The CSS the components rest on. A unit with no style is a paragraph in
// disguise, and a caret that lands under it makes every such header two rows
// taller — which is what pushes somebody to delete the unit and write the
// paragraph again.
func TestTheStylesheetCarriesTheUnitAndTheGroupHeader(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("..", "..", "ui", "assets", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(css)
	for _, want := range []struct{ sel, why string }{
		{"th .u{", "the column unit has no style"},
		{"table.data-table thead th.has-u.sortable .hd::after{", "the sort caret lands under the unit"},
		{`content:"\2195\FE0E"`, "the neutral caret falls through to the colour emoji font"},
		{"tr.grouprow th.span", "one label cannot span the columns that share a unit"},
		{".chart-sub{", "the sub-heading is styled only inside the Usage page"},
		{".chart-sub .u{", "the sub-heading unit has no style"},
		{".chip{", "a source has no chip to be a state on"},
		{"table.list.tight{", "a four-column table still takes the panel's whole width"},
	} {
		if !strings.Contains(body, want.sel) {
			t.Errorf("%s (missing %q)", want.why, want.sel)
		}
	}
	// A two-colour palette forces every background, so a mix bar built out of
	// fills disappears in one. The bar is now the only thing carrying the
	// composition, so the forced-colors branch is load-bearing rather than
	// polish.
	fc := body[strings.Index(body, ".mix-seg.nil{"):]
	if i := strings.Index(fc, "@media (forced-colors:active){"); i < 0 {
		t.Fatal("no forced-colors branch after the mix segment colours")
	} else {
		fc = fc[i:]
		fc = fc[:strings.Index(fc, "\n}")]
	}
	for _, want := range []string{".mix-seg,.lg-swatch{border:1px solid CanvasText",
		"repeating-linear-gradient", ".chip.off i{background:Canvas}"} {
		if !strings.Contains(fc, want) {
			t.Errorf("the forced-colors branch is missing %q, so a two-colour palette loses the mark", want)
		}
	}
}

// A column with nothing orderable in it must not offer a caret, and a group row
// must not be mistaken for the column row. Both are what let a table carry its
// unit in its head instead of in a paragraph above itself.
func TestTheSorterReadsTheColumnRowAndSkipsUnsortableColumns(t *testing.T) {
	js := ui.SortableTableJS
	if !strings.Contains(js, "head.rows[head.rows.length-1].cells") {
		t.Error("the sorter reads the first header row, so a group row breaks sorting")
	}
	if !strings.Contains(js, `th.classList.contains('nosort')`) {
		t.Error("a column whose cell is a bar still offers to sort by it")
	}
}

// The paragraphs themselves. Each of these was a caption saying what its own
// heading had already said, an instruction to click something that looks
// clickable, or a sentence stating the unit of the column beside it.
func TestTheExplanatoryParagraphsAreGone(t *testing.T) {
	gone := []struct{ file, text, replacedBy string }{
		{"usage.go", "click a column to sort", "the sort caret"},
		{"usage.go", "Which models did the work", "the heading, which says Models"},
		{"usage.go", "What your agents actually reached for", "the heading, which says Tools"},
		{"usage.go", "Kinds, not names", "the kind labels on the bars"},
		{"usage.go", "Turns, not sessions", "the sub-heading's unit"},
		{"usage.go", "per changed session</b>, over the same", "the group header over those columns"},
		{"usage.go", "Of it, ", "the prompt-cache mix bar"},
		{"usage.go", "Measured on your own machines", "the page lead, said once"},
		{"usage.go", "Turns by the hour you sent them", "the sub-heading's unit"},
		{"usage.go", "A shell tool is one bar", "the programs table itself"},
		{"outcomes.go", "calculated from this window", "the window control"},
		{"outcomes.go", "how much of what members receive", "the sub-heading's unit"},
		{"insights.go", "Hold the pointer over a bar", "the tooltip"},
		{"insights.go", "click a row for that technique", "the row link"},
		{"organization.go", "Color shows helped rate; the bottom bar", "the heatmap's own legend"},
		{"review.go", "The origin shows where each technique came from", "the origin column"},
		{"techniquemap.go", "Click one to select it", "the cluster being clickable"},
	}
	for _, g := range gone {
		body, err := os.ReadFile(g.file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), g.text) {
			t.Errorf("%s: %q is back — %s is what answers it now", g.file, g.text, g.replacedBy)
		}
	}
}

// A budget, not a ban. Prose is right for an empty state, for a finding, and
// for a command somebody has to type — and wrong as a caption over a figure.
// This counts the hint paragraphs per file so the next caption has to be a
// deliberate choice rather than a reflex.
func TestProseStaysWithinBudget(t *testing.T) {
	budget := map[string]int{
		// What remains in each: loading and error states, empty states, the
		// findings, the ledger key flow, and one cross-link.
		"usage.go":        30,
		"review.go":       6,
		"insights.go":     2,
		"organization.go": 3,
		"outcomes.go":     1,
		"events.go":       3,
		"learning.go":     4,
		"tags.go":         3,
		"techniquemap.go": 2,
	}
	re := regexp.MustCompile(`class="hint"`)
	for file, max := range budget {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(re.FindAllString(string(body), -1)); n > max {
			t.Errorf("%s has %d hint paragraphs, over its budget of %d. "+
				"A caption over a figure is a question the design left open: put the unit on the "+
				"column, the categories in a legend, the caveat in ui.Fine — and raise this "+
				"number only for an empty state, a finding, or a command.", file, n, max)
		}
	}
}

// The layout the components sit in. Each of these was a visible fault on a real
// board and invisible in a headless render or a diff: a caret that fell through
// to the colour emoji font, a bar list whose track started across a void, a
// marker inline in a numeric column, a date set at 40px, a table hard against
// the figures above it. They are asserted here because the next person to add a
// column or a bar list will otherwise meet them again.
func TestTheLayoutOfTheReplacements(t *testing.T) {
	page := usageJS
	css, err := os.ReadFile(filepath.Join("..", "..", "ui", "assets", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(css)

	// .bars sizes its label track for a technique title. Everything whose label
	// is a word takes the short-label variant, which already existed and was
	// used on exactly one chart.
	if !strings.Contains(page, "function tbars(rows,empty){") {
		t.Fatal("there is no short-label bars helper, so the next word-labelled list will strand")
	}
	if n := strings.Count(page, "tbars("); n < 10 {
		t.Errorf("only %d bar lists take the short-label track; the word-labelled ones all should", n)
	}
	if !strings.Contains(body, ".bars-tight-label .bars{grid-template-columns:max-content") {
		t.Error("the short-label variant is gone")
	}

	// A figure a reader judges staleness by is an age, not a date: 40px of
	// "2026-09-12" is the weakest hero on the page.
	if !strings.Contains(page, "function agoLabel(day){") {
		t.Error("nothing renders an age, so a date will be set at 40px again")
	}
	for _, want := range []string{"tile('Oldest reading',agoLabel(oldest)", "tile('Last used',agoLabel(t.last)"} {
		if !strings.Contains(page, want) {
			t.Errorf("%s does not lead with the age", want)
		}
	}

	// Spacing between the blocks a panel is made of. Without these a table sat
	// on the figures above it and a chip row sat on the last bar.
	for _, want := range []string{
		".tiles+.table-wrap,.tiles+.chips{margin-top:",
		".table-wrap+p.hint,.table-wrap+p.empty{margin-top:",
		".bars+.chips,.bars-tight-label+.chips",
		".tiles.tiles-lead>*{flex:0 1 auto",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the stylesheet is missing %q", want)
		}
	}

	// A composition bar needs a track, or one state filling it draws as a rule
	// across the panel rather than as a measure that is full.
	for _, want := range []string{".mix.mix-hero{", ".mix.mix-cell{"} {
		i := strings.Index(body, want)
		if i < 0 {
			t.Errorf("%s is gone", want)
			continue
		}
		if rule := body[i : i+200]; !strings.Contains(rule, "box-shadow:inset 0 0 0 1px var(--ring)") {
			t.Errorf("%s has no track", want)
		}
	}

	// The plural that bites: three repositories, not three repositorys.
	if !strings.Contains(page, `/[^aeiou]y$/.test(unit)`) {
		t.Error("the pluraliser appends s to a consonant-y word")
	}
}
