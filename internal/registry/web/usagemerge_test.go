// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/hooks"
	"github.com/opentacit/tacit/internal/ui"
)

// The cross-machine merge runs in the browser, because the plaintext exists
// only there — the registry cannot add up what it cannot read. That makes the
// merge a second implementation of an aggregation Go already does, and the way
// those two drift is that somebody adds a counted field to the Go summary and
// the page quietly stops summing it: one machine keeps reporting it, two
// machines report the first one's value, and nothing anywhere says so.
//
// So this walks the Go structs and insists every numeric field the summaries
// carry is named in the page's merge lists. A new field fails here until it is
// either summed or deliberately excluded below.
func TestBrowserMergeSumsEveryCountedField(t *testing.T) {
	page := usageJS
	for _, tc := range []struct {
		name    string
		typ     any
		jsList  string
		exclude map[string]bool
	}{
		{"UsageTotals", hooks.UsageTotals{}, "USAGE_TOTALS", nil},
		{"UsageDay", hooks.UsageDay{}, "USAGE_DAY", map[string]bool{"date": true}},
		{"UsageTechnique", hooks.UsageTechnique{}, "USAGE_TECH", map[string]bool{
			"cap": true, "name": true, "last_adopted": true, // carried or spanned, not summed
		}},
		{"UsageModel", hooks.UsageModel{}, "USAGE_MODEL", map[string]bool{
			"model": true, "first": true, "last": true,
		}},
		{"WorkTotals", hooks.WorkTotals{}, "WORK_TOTALS", nil},
		{"DelegationUse", hooks.DelegationUse{}, "DELEGATION_TOTALS", map[string]bool{
			"name": true, "kind": true, // carried, not summed
		}},
		// A tool row is not summed from a list — mergeTools folds it by hand,
		// because its cross-tabs are nested maps. The counts still have to be
		// named somewhere, so they are asserted against that function's own
		// text: a new count added to ToolUse and forgotten in mergeTools would
		// otherwise report one machine's value for two.
		{"ToolUse", hooks.ToolUse{}, "TOOL_TABS", map[string]bool{
			"name": true, "kind": true, "detail_kind": true, "last": true, "detail": true,
			// folded by hand (mergeTools), asserted below
			"calls": true, "offers": true, "calls_offered": true, "sessions": true,
		}},
		// A model row is WorkTotals plus what the model was doing. The counts
		// are summed from the same list; the cross-tab maps are folded by
		// mergeModels (TestModelCrossTabsSurviveTheMachineMerge) and the peak
		// is a level, so it is maxed rather than added.
		{"ModelUse", hooks.ModelUse{}, "WORK_TOTALS", map[string]bool{
			"key": true, "first": true, "last": true,
			"harness": true, "task": true, "project": true,
			"turn_times": true, "variants": true, "effort": true,
			"peak_context": true,
		}},
		// An outcome row is check evidence plus the effort of the same changed
		// sessions. Every number on it adds across machines: a session happens
		// on one machine and is recorded once. The three labels identify the
		// row rather than measuring it.
		{"ModelClientUse", hooks.ModelClientUse{}, "CHECK_TOTALS", map[string]bool{
			"model": true, "client": true, "task": true,
		}},
	} {
		list := jsArray(t, page, tc.jsList)
		for _, field := range jsonFields(tc.typ) {
			if tc.exclude[field] {
				continue
			}
			if !list[field] {
				t.Errorf("%s.%s is in the Go summary but not in the page's %s list — "+
					"two machines would report one machine's value for it",
					tc.name, field, tc.jsList)
			}
		}
	}
}

// jsonFields returns the json tag names of a struct's fields, flattening
// embedded structs the way encoding/json does.
func jsonFields(v any) []string {
	var out []string
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			out = append(out, jsonFields(reflect.New(f.Type).Elem().Interface())...)
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		out = append(out, tag)
	}
	return out
}

// jsArray pulls the string members out of `var NAME=['a','b'];` in the page.
func jsArray(t *testing.T, page, name string) map[string]bool {
	t.Helper()
	start := strings.Index(page, " var "+name+"=[")
	if start < 0 {
		t.Fatalf("the page has no %s list; the browser merge is gone or renamed", name)
	}
	body := page[start:]
	end := strings.Index(body, "];")
	if end < 0 {
		t.Fatalf("%s is not terminated", name)
	}
	out := map[string]bool{}
	for _, part := range strings.Split(body[:end], "'") {
		part = strings.TrimSpace(part)
		if part != "" && !strings.ContainsAny(part, "[],= ") {
			out[part] = true
		}
	}
	return out
}

// mergeTools folds the tool rows by hand, so the field list above cannot police
// it. This does: every count on a tool row has to appear in that function, or a
// member with two machines reads one machine's value for both.
func TestToolCountsAreFoldedByTheToolMerge(t *testing.T) {
	body := usageJS
	start := strings.Index(body, "function mergeTools(")
	if start < 0 {
		t.Fatal("the page has no mergeTools; the tool merge is gone or renamed")
	}
	fn := body[start : start+2000]
	for _, field := range []string{"calls", "offers", "calls_offered", "sessions"} {
		if !strings.Contains(fn, "into."+field+"+=t."+field) {
			t.Errorf("mergeTools does not add %s; two machines would report one machine's value", field)
		}
	}
}

// A level is not a total. Peak context is the fullest the window ever got, so
// merging two machines takes the larger rather than the sum — two machines that
// each half-filled a window did not between them fill it once. This is the one
// field on the page where the wrong arithmetic produces a number that cannot
// physically happen, so it is asserted rather than trusted to review.
func TestPeakContextIsMaxedNeverSummed(t *testing.T) {
	page := usageJS
	if jsArray(t, page, "WORK_TOTALS")["peak_context"] {
		t.Error("peak_context is in the summed field list; a merged peak would exceed any real context")
	}
	if !strings.Contains(page, "out.peak_context=p.peak_context") {
		t.Error("the merge does not carry peak_context at all")
	}
	if !strings.Contains(page, "out.context_reported=out.context_reported||!!p.context_reported") {
		t.Error("context_reported must survive the merge, or a measured window reads as an unmeasured one")
	}
}

// Shown and adopted are drawn touching. Adopted is a subset of shown — one
// measurement read at two depths — and the 2px between grouped bars reads as two
// independent counts that happen to sit side by side. Retries and corrections
// ARE independent, so they keep the gap.
func TestSuggestionBarsTouch(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "legendFor([['Shown','s1'],['Adopted','s2']]),'',0)") {
		t.Error("the suggestions plot should pass gap 0")
	}
	// Retries and corrections pass no gap at all, so they take the default 2px:
	// they are independent counts that happen to sit side by side, which is
	// exactly what the gap says.
	friction := page[strings.Index(page, "legendFor([['Retries','s3'],['Corrections','s7']])"):]
	friction = friction[:200]
	if strings.Contains(friction, "gap:") {
		t.Error("the friction plot should keep the default gap")
	}
	if !strings.Contains(page, "gap=gap===undefined?2:gap") {
		t.Error("the default gap is gone; every other chart depends on it")
	}
}

// The per-technique drill-down opens at five rows and says how many it is
// holding back, matching the length the organization's leaderboards already use.
// The cap is a class on the TABLE and the CSS hides by position, so the shell's
// sorter — which reorders rows in place — leaves the top five of whatever order
// the reader just chose. Hiding per row would leave the five that happened to be
// first before they sorted.
func TestByTechniqueOpensAtFiveAndSortsUnderTheCap(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "var techniqueRowLimit=5") {
		t.Error("the row limit is gone or renamed")
	}
	if !strings.Contains(page, `data-table'+(capped?' capped':'')`) {
		t.Error("the cap should be a class on the table, not a per-row hide")
	}
	if !strings.Contains(page, `'Show '+(live.length-techniqueRowLimit)+' more</button>'`) {
		t.Error("the control does not say how many rows are folded away")
	}
	// Position-based, so a sort re-ranks what the cap shows.
	css := ui.CSS()
	if !strings.Contains(css, ".data-table.capped tbody tr:nth-child(n+6){display:none}") {
		t.Error("the cap is not by row position, so sorting would show the wrong five")
	}
	// And it is wired after render, like the sorter and the charts.
	if !strings.Contains(page, "wireTableMore();") {
		t.Error("the control is never wired up")
	}
}

// The per-technique drill-down reads last on the view that holds it: it is the
// longest thing there and the most specific, and above the rest it pushed
// everything else below the fold.
func TestByTechniqueReadsLastOnWhatCameOfIt(t *testing.T) {
	page := usageJS
	view := page[strings.Index(page, "function resultsView(d,w){"):]
	dormant := strings.Index(view, "html+=dormantPanel(d.techniques);")
	table := strings.Index(view, "if(shownAny)html+=techniquesTable(d.techniques);")
	if dormant < 0 || table < 0 {
		t.Fatal("the render order markers are gone")
	}
	if table < dormant {
		t.Error("By technique is still rendered above what you stopped doing")
	}
}

// A dimension is not a destination, and it is not a table of its own either.
// The page carried a By project table and a By kind of session table built by
// one closure and a By model table built somewhere else, all asking the same
// question of the same rows. One control asks it once.
func TestOneBreakdownReplacesTheSeparateGroupTables(t *testing.T) {
	page := usageJS
	if strings.Contains(page, "function groupTable(") {
		t.Error("the old per-dimension group table is still here beside the control")
	}
	if !strings.Contains(page, "var BREAKDOWNS=[") || !strings.Contains(page, "function breakdownTable(w,cols){") {
		t.Fatal("no break-down-by control")
	}
	// Which way to cut is URL state, like the period and the view: bookmarkable,
	// survives a reload, and works with no JavaScript because the options are
	// links.
	if !strings.Contains(page, "new URLSearchParams(location.search).get('by')") {
		t.Error("the break-down is not read from the URL")
	}
	if !strings.Contains(page, "'&by='+encodeURIComponent(b.key)") {
		t.Error("the control's options do not carry the chosen dimension in the URL")
	}
	// And the period rides along, or changing the cut loses the window.
	if !strings.Contains(page, "'?w='+encodeURIComponent(WINDOW)+'&by='") {
		t.Error("the break-down links drop the period")
	}
	for _, key := range []string{"'project'", "'kind'", "'model'"} {
		if !strings.Contains(page, "{key:"+key+",label:") {
			t.Errorf("no break-down by %s", key)
		}
	}
}

// Trends was an axis pretending to be a destination. Every view carries the
// series it already drew, and the old address moves with its query intact.
func TestTrendsIsAnAxisAndItsAddressStillWorks(t *testing.T) {
	page := usageJS
	if strings.Contains(page, "function trendsView(") {
		t.Error("the trends view is still a page of its own")
	}
	if !strings.Contains(page, "function seriesPanel(w,title,note,plots){") {
		t.Fatal("no series panel for the views to carry")
	}
	_, ts := newServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/usage/trends?w=90d")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("GET /usage/trends = %d, want 301 — a moved URL says so once", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/usage?w=90d" {
		t.Errorf("Location = %q, want /usage?w=90d with the query intact", got)
	}
}

// THE PLATE OPENS THE CLIMB BEHIND IT. A level answers "how much is gone" and
// cannot answer "will it last", which is the question a member has at two in
// the afternoon. The drill-down is the sampled history (hooks/quota.go), one
// panel per provider — a view of its own, reached from the figure that raises
// the question rather than from the menu, the same as Models and Tools. The
// plate opens only where there is history for that provider: a doorway onto a
// chart with nothing in it is the empty grid again.
func TestTheAllowancePlateOpensItsClimb(t *testing.T) {
	if usageView("/usage/allowance") != "allowance" || usagePath("allowance") != "/usage/allowance" {
		t.Fatal("the allowance drill-down has no address")
	}
	// And the address is served: a view the page knows and the mux does not is
	// a plate that opens a 404.
	_, ts := newServer(t)
	if code, _ := fetchHTML(t, ts.URL+"/usage/allowance"); code != 200 {
		t.Errorf("GET /usage/allowance = %d, want 200", code)
	}
	for _, o := range usageViews("now", "30d") {
		if strings.Contains(o.href, "/usage/allowance") {
			t.Error("a drill-down was added to the view menu")
		}
	}
	page := usageJS
	if !strings.Contains(page, "if(!has)return '';") {
		t.Error("the plate opens even where no climb is kept for that provider")
	}
	// The axis is the wall at 100, never the tallest column: scaled to itself a
	// climb from 6% to 8% draws the same picture as one from 6% to 80%.
	if !strings.Contains(page, "var ax=full?{max:full,step:full/4}:niceAxis(dataMax)") {
		t.Error("the allowance chart can be scaled to its own data")
	}
	// One measure at three scales wears one key, and never a funnel key: s1, s2,
	// s4 and s6 are shown, adopted, helped and dismissed wherever they appear.
	if n := strings.Count(page, "{name:'Used',key:'s9',f:'pct'}"); n != 3 {
		t.Errorf("the windows are drawn %d times with the shared key, want 3", n)
	}
	// The chart runs to the reset, not to now: the blank to the right of the
	// climb is the time left, which is half of what the member came to see.
	if !strings.Contains(page, "fiveTo=Date.parse(live.five_hour.resets_at);") {
		t.Error("the five-hour chart stops at now, so the time left is invisible")
	}
	// And the level stops at the last reading rather than being drawn across
	// the hours that have not happened yet.
	if !strings.Contains(page, "if(!any&&seen&&t<=last)b.pct=held;") {
		t.Error("the climb is carried forward into the future of the window")
	}
	// This machine's turns are drawn beside the account's allowance, never as
	// one series with it: the allowance is spent from every machine signed in,
	// and the turn counter cannot say which account paid for it — so the work
	// is its own machine-wide series rather than a field on a provider's.
	if !strings.Contains(page, "Your turns in the same hours") {
		t.Error("the work that went with the climb is missing")
	}
	if !strings.Contains(page, "levelBuckets(w.work_history||[],fiveFrom,fiveTo,FIVE_STEP,clockOf)") {
		t.Error("the work chart reads a provider's own points rather than the machine's")
	}
	if !strings.Contains(page, "and not as one explaining the other") {
		t.Error("the two pictures are offered as cause and effect")
	}
}

// TWO PROVIDERS ARE TWO PLATES, AND THEY SAY WHOSE. An allowance belongs to an
// account: a member with a Claude session and a Codex session beside it is
// spending two of them, and one plate for both would show whichever harness
// rendered its status line last. Named only where there is more than one —
// mark the exception, stay silent on the rule — so the single-allowance case
// reads exactly as it always did.
func TestEachAllowanceGetsItsOwnPlate(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "return quotaTile(q,named,href)+spendTile(q,named,href);") {
		t.Error("the allowances are not a plate each")
	}
	// A reset days away carries its day. "resets 09:00" on a window that
	// refills next Thursday reads as this morning, and a member acts on it.
	if !strings.Contains(page, "if(ms-Date.now()<20*3600000)return clock(iso);") {
		t.Error("a reset days away is shown as a time of day")
	}
	if strings.Contains(page, "'of the week · resets '+clock(") {
		t.Error("the week's reset is still a bare clock time")
	}
	// A spend limit is money rather than usage, so it keeps its own plate
	// instead of joining the rolling windows in one line — and it is absent
	// unless a gateway reports one, which is most members.
	if !strings.Contains(page, "if(!s)return '';") {
		t.Error("a member with no gateway grows an empty spend plate")
	}
	if !strings.Contains(page, "if(named&&q.source_label)label=q.source_label+' allowance';") {
		t.Error("a lone allowance is labelled with a provider, or two are not")
	}
	// The page holds no list of who the providers might be. Members run
	// whichever subset they run, and one arrives every few weeks: the row
	// carries its own name, and an unrecognised provider gets a plate on the
	// same terms as a known one.
	for _, guess := range []string{"anthropic:'", "openai:'", "'Claude'", "'Gemini'"} {
		if strings.Contains(page, guess) {
			t.Errorf("the page names providers from a fixed list (%s)", guess)
		}
	}
	// Across machines, latest-wins runs PER provider. One machine's Codex
	// reading must not hide another machine's Claude reading.
	if !strings.Contains(page, "if(!have||String(q.at)>String(have.at))out.quotas[q.source||'']=q;") {
		t.Error("the cross-machine merge folds two providers into one reading")
	}
	// The name comes from the ledger row, so the same word appears wherever the
	// allowance does and the browser holds no second vocabulary.
	if strings.Contains(page, "q.source+' allowance'") {
		t.Error("a plate is labelled with a raw key rather than the row's own name")
	}
}

// THE STATE IS PLATES, NOT A PANEL. "Right now" was a heading, a hint paragraph
// and two sentences — a figure and a comparison, said in prose across the full
// width of the page, under a row that had room for four more plates. A panel is
// what you give something with a paragraph's worth to say.
func TestTheStateSitsInTheRowBesideThePicture(t *testing.T) {
	page := usageJS
	if strings.Contains(page, "<h2>Right now</h2>") || strings.Contains(page, "nowPanel(") {
		t.Error("the state is still a panel of its own")
	}
	// An unknown is not a zero: on the sealed-ledger path there is no live agent
	// to ask, and a plate reading 0 would call a machine idle that nobody asked.
	if !strings.Contains(page, "if(typeof w.live_sessions!=='number')return '';") {
		t.Error("a machine nobody could ask is reported as a machine with nothing open")
	}
	// Labelled with the day itself. The last day worked is not always today, and
	// a page left open overnight must not start calling yesterday today.
	if !strings.Contains(page, "return tile(dayLabel(dayMS(last.date)),fmtCount(last.turns||0),") {
		t.Error("the day's plate is not labelled with its day")
	}
	if !strings.Contains(page, "if(days.length<3)return '';") {
		t.Error("two days is being compared against a usual day")
	}
}

// THE CORRECTIONS PLATE OPENS THE CORRECTIONS IT COUNTS. "Things you keep
// saying" is the evidence behind that figure — how often, how far back, and
// which are already drafted as techniques — and it sits three screens into a
// view the plate never names. So the plate is the way in, and it carries the
// panel's own id rather than the top of the page.
//
// Only where there are repeats: every correction made once leaves the panel
// silent, and a doorway onto an empty room is the same lie as an empty grid.
func TestCorrectionsPlateOpensTheCorrectionsItCounts(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, `<section class="panel" id="repeats">`) {
		t.Fatal("the repeats panel has no id, so nothing can land on it")
	}
	if !strings.Contains(page, "tileLink('Corrections',n,APP+'/usage/work?w='+encodeURIComponent(WINDOW)+'#repeats',sub)") {
		t.Error("the Corrections plate does not open the panel that holds them")
	}
	if !strings.Contains(page, "if(!((w.repeats||[]).length))return tile('Corrections',n,sub,null);") {
		t.Error("the plate opens a panel that may have nothing in it")
	}
	// The figure keeps its denominator either way: a plate that dropped the rate
	// on becoming a doorway would ask for a click to get back what it had said.
	if !strings.Contains(page, "(sub?'<span class=\"tile-delta\">'+esc(sub)+'</span>':'')") {
		t.Error("tileLink cannot carry the line under its figure")
	}
	// The page builds its own markup, so the browser's own jump has been and
	// gone before there is anything to jump to.
	if !strings.Contains(page, "if(!jumped&&/^#[\\w-]+$/.test(location.hash)){") {
		t.Error("a fragment naming a panel is never honoured after the render")
	}
	// That the panel then clears the sticky bar is measured in the browser
	// (hack/browsercheck), not matched in the stylesheet's text.
}

// The picture keeps its height and takes half the width, with the window's
// headline figures in the column beside it and everything after spanning both.
// The band was already a two-column grid — it is what puts the local-only
// explainer beside the picture — so this is the same grid used for the state
// that has data, rather than a second layout.
func TestHeadlineFiguresSitBesideThePicture(t *testing.T) {
	page, css := usagePageSource(), ui.CSS()
	if !strings.Contains(page, `<div id="usage-head"></div>`) {
		t.Fatal("no cell for the headline figures")
	}
	// ONE row of plates, and only the ones this view's question is about. The
	// funnel used to ride here too, twelve plates wrapping three deep, which is
	// the stat-tile row the house style names; it moved to You / Outcomes.
	if !strings.Contains(page, `setHead('<div class="tiles">'+workTiles(w)+'</div>');`) {
		t.Error("the headline figures are not rendered as one row beside the picture")
	}
	if strings.Contains(page, `workTiles(w)+funnelFlow`) || strings.Contains(page, `funnelFlow(t)+workTiles`) {
		t.Error("the funnel is back on Now beside the work figures")
	}
	// The allowance leads, because it is the reason Now exists, and the rest of
	// the state follows it before the window's own figures.
	if !strings.Contains(page, "return quotaTiles(w)+liveTile(w)+todayTile(w)+\n   tile('Sessions'") {
		t.Error("the allowance is not the first figure on Now, or the state is not beside it")
	}
	// Beside the picture these plates are a GRID and not the wrapping row they
	// are everywhere else, because a wrapped flex line stretches its items to
	// the width. Seven of them came out as four at 149px and then three at
	// 204px: two rows sharing a left edge, agreeing about nothing else, at two
	// heights, with the delta beside the figure on one row and under it on the
	// other. The floor decides how many fit; auto-fill decides the rest.
	if !strings.Contains(ui.CSS(), "grid-template-columns:repeat(auto-fill,minmax(8rem,1fr));") {
		t.Error("the plates beside the picture are no longer a grid, so a short last row will stretch again")
	}
	// One height for every plate, so the figures sit on one baseline down each
	// column and the rows do not step.
	if !strings.Contains(ui.CSS(), "grid-auto-rows:1fr}") {
		t.Error("the rows can come out at different heights again")
	}
	// A plate left alone on the last row keeps its column's width. This used to
	// need a cap at half the column — a lone ninth plate became a banner four
	// times the width of its neighbours — and a grid gives it for free, which is
	// why the cap is gone rather than missing.
	if strings.Contains(ui.CSS(), "max-width:calc(50% - 7px)") {
		t.Error("the flex-era stretch cap is back; the grid makes it a contradiction")
	}
	// Stacked, the row is a whole window and the narrow floor puts five across
	// it, where the label breaks before its arrow and sets the height of all of
	// them. Same breakpoint as the band's own.
	if !strings.Contains(ui.CSS(), "@media (max-width:900px){\n  .usg-band>#usage-head>.tiles{grid-template-columns:repeat(auto-fill,minmax(170px,1fr))}\n}") {
		t.Error("the collapsed band keeps the floor meant for half a column")
	}
	// And one shape for the caption. .tile-row wraps, so a short one sat beside
	// its figure and a long one below it — two shapes for one thing in a single
	// row, decided by nothing but string length.
	if !strings.Contains(ui.CSS(), ".usg-band>#usage-head .tile-delta{flex-basis:100%}") {
		t.Error("a plate's caption sits beside or below its figure depending on how long it is")
	}
	if !strings.Contains(page, "function workTiles(w){") {
		t.Error("the work headline figures are still welded into the panel")
	}
	// Only the summary has a picture; the sibling views take the whole width
	// and empty the cell.
	if !strings.Contains(page, "wide(VIEW!=='now');") {
		t.Error("the band should stay two-column only where there is a picture")
	}
	// Everything after the top row runs full width: the tables below need it.
	if !strings.Contains(css, ".usg-band.has-head>#usage-root{grid-column:1/-1}") {
		t.Error("the panels below are not spanning both columns")
	}
	// And on a phone it is one column again, where half a width is no width.
	if !strings.Contains(css, "@media (max-width:900px){.usg-band.has-head>#usage-root{grid-column:auto}}") {
		t.Error("the narrow breakpoint does not release the span")
	}
}

// Every mark type must be able to draw every series the colour block declares.
// A fill with no rule paints transparent, which reads as a layout bug rather
// than as a missing line — it happened twice, once on .vbar and once here.
// The bill is split three ways and the page was showing one number. A cache
// read costs a fraction of fresh input and a cache write a premium over it, so
// two windows with the same tokens in can be two very different invoices — and
// the ratio is the one figure here a member can act on the same afternoon, by
// keeping a session warm instead of starting a new one.
//
// Fresh is the remainder rather than a fourth stored field, so the three always
// add up to the count every other figure on the page is built on.
func TestTheCacheSplitIsReadFromTheTokensNotTheRequests(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "function cacheSplit(w){") {
		t.Fatal("nothing splits the input tokens")
	}
	if !strings.Contains(page, "var fresh=inTok-read-write;") {
		t.Error("fresh is stored rather than computed; a fourth number can disagree with the other three")
	}
	// A window recorded before the split existed carries neither field. Showing
	// it as 100% fresh would be a confident, specific, backwards number.
	if !strings.Contains(page, "if(!read&&!write)return null;") {
		t.Error("a window from before the split would read as all-fresh input")
	}
	// Tokens beat requests for the caption: one says how much, the other how
	// often, and a member deciding whether to keep a session warm wants how much.
	if !strings.Contains(page, "cached=pct(split.read,split.in)+' of it read from cache';") {
		t.Error("the plate does not prefer the token split to the request rate")
	}
	if !strings.Contains(page, "}else if(hasCache(w)){") {
		t.Error("the request rate is gone rather than kept as the fallback")
	}
	// And the full three-way reading lives in the plot note, where a sentence fits.
	// The split is DRAWN, not described: three widths and three names, ordered
	// cheapest to dearest so the bar carries the advice the sentence used to.
	if !strings.Contains(page, "function cacheBar(w){") {
		t.Error("the three-way split is not drawn anywhere")
	}
	for _, part := range []string{"label:'Read from cache'", "label:'Written to cache'", "label:'Fresh'"} {
		if !strings.Contains(page, part) {
			t.Errorf("the cache composition is missing %s", part)
		}
	}
	if strings.Index(page, "label:'Read from cache'") > strings.Index(page, "label:'Fresh'") {
		t.Error("the parts are not ordered cheapest to dearest, so the bar gives no advice")
	}
	if !strings.Contains(page, "html+=cacheBar(w);") {
		t.Error("the cache bar is built but never rendered")
	}
}

// The page never said what it reads. A member running two tools could not tell
// a quiet week from an unwired one, which is the reading that matters most and
// the one nothing here was answering — ccusage names the eighteen CLIs it reads
// and we named none.
//
// Both halves have to be there. The clients IN the window come from the data;
// the ones that are not come from the agent's own routing table, and without
// that second half "read from claude-code" still leaves the Codex reader
// guessing.
func TestTheWindowSaysWhichClientsFedIt(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "var READS=CFG.harnesses;") {
		t.Fatal("the page does not carry the list of clients the agent can read")
	}
	// Coverage is a row of chips rather than a sentence: a client that ran is a
	// lit chip with its session count, one that could have and did not is a
	// single muted chip with a count. Nobody read the sentence, and the fact it
	// carried that matters most -- an unwired machine reads as a cheap one --
	// was buried in the middle of it.
	if !strings.Contains(page, "function clientChips(w){") {
		t.Error("nothing states the window's coverage")
	}
	if !strings.Contains(page, "if(rest>0)out+=chip(rest+' more readable',false,'');") {
		t.Error("the clients that could have fed the window are not counted")
	}
	// After the figures it qualifies, not before them: provenance is what a
	// reader checks once a number has raised a question.
	if !strings.Contains(page, "html+sourceRow(w)") {
		t.Error("the coverage row is built but never rendered after the figures")
	}
	// One list, from the agent's routing table. A copy in this package is the
	// copy that goes stale the week somebody adds the tenth harness.
	if !strings.Contains(usageGoSource(t), "hooks.HarnessNames()") {
		t.Error("the readable clients are a copy rather than the agent's own list")
	}
	// And the client becomes the fourth way to cut the table, which the
	// restructure named and never built.
	if !strings.Contains(page, "{key:'client',label:'Client',head:'Client',") {
		t.Error("the client is still only readable one model at a time")
	}
	// Rows merge across machines like every other group, or two laptops report
	// one laptop's clients.
	if !strings.Contains(page, "out.clients=mergeRows(") {
		t.Error("client rows do not survive the cross-machine merge")
	}
}

// usageGoSource reads this package's own usage.go, for the assertions that are
// about the SERVER half of the page rather than the script it ships.
func usageGoSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("usage.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A level alone cannot answer the question the allowance page exists for: 40%
// at ten past is comfortable or alarming depending entirely on how fast it got
// there. The history the status line has been filling in says which, so the
// page says it — as an extrapolation, with the rate and the span it was
// measured over, never as a reading.
//
// The silences matter as much as the sentence. A slope from three readings in
// four minutes is noise with a decimal point, and a page that printed it would
// cost the reader their trust in every other figure on it.
func TestTheAllowanceSaysWhereTheClimbIsHeading(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "function burnLine(points,from,to){") {
		t.Fatal("nothing projects the climb")
	}
	for _, guard := range []struct{ src, why string }{
		{"if(rows.length<2)return '';", "one reading is not a rate"},
		{"if(spanMs<20*60000)return '';", "a slope off a few minutes is noise"},
		{"if(climbed<=0||(last.pct||0)>=100)return '';", "a flat or finished window still gets a projection"},
	} {
		if !strings.Contains(page, guard.src) {
			t.Errorf("missing guard — %s", guard.why)
		}
	}
	// The basis travels with the number, like every other rate on this page.
	if !strings.Contains(page, "' Usage increased by '+esc(fixed(perHour,1))+'% an hour over the last '+") {
		t.Error("the projection does not carry the rate and the span it came from")
	}
	// Landing after the reset is the answer a member wants, not a silence.
	if !strings.Contains(page, "return basis+'it resets before it runs out.';") {
		t.Error("a window that resets in time says nothing")
	}
	if !strings.Contains(page, "esc(fmtSpan(Math.round((to-outAt)/1000)))+' short of the reset.'") {
		t.Error("running out early does not say how early")
	}
	// It rides on the five-hour plot's own note rather than taking a plate: a
	// projection is a sentence, and a plate has room for a figure.
	if !strings.Contains(page, "'as the level it held.'+burnLine(h.five,fiveFrom,fiveTo),2,100);") {
		t.Error("the projection is built but never said")
	}
}

// Cost was measured on one of the nine clients the agent routes and absent on
// the other eight — the blank the market scan opened with, moved rather than
// closed. Published rates fill it, and the whole risk of that is confusing an
// estimate with a measurement, so the page keeps them apart in three places.
func TestEstimatedMoneyNeverPassesForMeasuredMoney(t *testing.T) {
	page, src := usagePageSource(), usageGoSource(t)
	// One plate. Two would invite a reader to add them.
	if !strings.Contains(page, "sub+=' \\u00b7 $'+fixed(t.est_cost_usd,2)+' at list';") {
		t.Error("the estimate does not ride under the measured figure")
	}
	if !strings.Contains(page, "html+=tile('Cost, estimated','$'+fixed(t.est_cost_usd,2),'at '+PRICEDAT+' rates',null);") {
		t.Error("a window with nothing measured does not say its cost is an estimate")
	}
	// NO FAKE ZEROS. A client that reports no dollars costs an unknown amount,
	// and $0.00 is the one figure that is certainly false.
	if !strings.Contains(page, "return '<td class=\"'+cls+'\">\\u2014</td>';") {
		t.Error("an unpriced row still renders a zero")
	}
	if !strings.Contains(page, "' est</span></td>';") {
		t.Error("an estimated row is not marked as one")
	}
	// The date the table was read, beside every estimate.
	if !strings.Contains(src, `PriceDate:       pricing.AsOf,`) {
		t.Error("the page never says when its prices were published")
	}
	// And the totals stay two fields all the way through the merge, so two
	// machines cannot silently combine one's measurement with the other's guess.
	if !strings.Contains(page, "'est_cost_usd',") {
		t.Error("the estimate does not survive the cross-machine merge")
	}
}

// Survival was measured exactly and shown nowhere: the command printed it and
// the page could only name the command, because the log keeps a project
// basename and never a path. Filing the reading closes that without moving the
// line — what gets filed is the basename, the branch and counts, which is what
// the command already printed.
//
// The date is the panel's whole defence. Nothing refreshes a survival figure,
// and one taken three weeks ago describes a branch that has moved.
func TestFiledSurvivalReachesThePageWithItsDate(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "function landedPanel(d,w){") {
		t.Fatal("You / Outcomes has no survival panel")
	}
	if !strings.Contains(page, "html+=landedPanel(d,w);") {
		t.Error("the panel is built but never rendered")
	}
	// Every reading names the day it was asked and the window it covered.
	for _, want := range []string{
		"String(r.asked_at||'').slice(0,10)",
		"esc((r.window_days||0)>0?one(r.window_days,'day'):'\\u2014')",
		"Run the command again to refresh a reading",
		// The date is also a figure now, not only a column: the oldest reading
		// in the panel leads the plate row, because a stale survival figure is
		// the one failure this panel has to make visible without being read.
		// The staleness is the reading. A date set at 40px is not it, so the
		// hero is the age and the date rides underneath and in the table.
		"tile('Oldest reading',agoLabel(oldest)",
		"function agoLabel(day){",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q — a survival figure without its date is a claim about a branch that has moved", want)
		}
	}
	// A rate with no denominator is not a small number; it is no number.
	if !strings.Contains(page, "esc(added>0?pct(survived,added):'\\u2014')") {
		t.Error("a window with nothing written still renders a survival rate")
	}
	// The command that fills it is the one the panel names, in both states.
	if strings.Count(page, "tacit usage --landed --publish") < 1 {
		t.Error("the panel does not say how to fill itself")
	}
	// And the cold start says why the page cannot just go and measure — in the
	// disclosure, since the empty state's own job is to name the command.
	if !strings.Contains(page, "Run the command inside the repository you want to check") {
		t.Error("the panel never says why the question has to be asked in the repository")
	}
	if !strings.Contains(page, `'<p class="empty">No repository has been measured yet.</p>'+ask+`) {
		t.Error("the cold start does not lead with the command that fills it")
	}
}

// The member's own shown → adopted → helped is drawn as the flow the Outcomes
// page draws for the organization, and read against it.
//
// Five flat plates said the same thing and left the reader to work out that
// three of them were one story. The geometry is in the browser for the same
// reason plot() is — on a shared registry this member's usage arrives sealed
// and is opened here, so a server that drew it would first have to be told what
// is in it — but the COMPONENT is the shared one: every class comes from
// app.css, so both themes, forced-colors and the bloom filter follow for free.
func TestTheMembersFunnelIsDrawnAsTheOrgFunnelIs(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "function funnelFlow(t){") {
		t.Fatal("the member's funnel is not drawn")
	}
	// The shared component, not a second palette. o1→o3 is the funnel ramp the
	// house style fixes for these three stages.
	for _, cls := range []string{"flow-svg", "flow-band '+stages[i+1].tint", "flow-bar '+st.tint",
		"flow-name", "flow-count", "flow-conv", "flow-drop"} {
		if !strings.Contains(page, cls) {
			t.Errorf("the flow does not use the shared class %q", cls)
		}
	}
	if !strings.Contains(page, "{name:'Shown',tint:'o1'") ||
		!strings.Contains(page, "{name:'Adopted',tint:'o2'") ||
		!strings.Contains(page, "{name:'Helped',tint:'o3'") {
		t.Error("the stages have been repainted off the fixed funnel ramp")
	}
	// A 760-unit viewBox in a phone column is unreadable, and app.css hides
	// .flow-svg below 640px on the strength of a .flow-phone sibling. Without
	// one the panel would simply empty itself on a phone.
	if !strings.Contains(page, `out+='<div class="flow-phone">'+bars([`) {
		t.Fatal("the flow has no phone rendering; the 640px rule would leave nothing")
	}
	if !strings.Contains(appCSS, "@media (max-width:640px){.flow-svg{display:none}.flow-phone{display:block}}") {
		t.Error("the swap this depends on is gone")
	}
	// Every height is a share of shown, so a zero denominator gets no drawing
	// rather than a flat one.
	if !strings.Contains(page, "if((t.shown||0)>0){") {
		t.Error("the funnel can be drawn from a zero denominator")
	}
	// A stage that never happened has no rate to convert FROM.
	if !strings.Contains(page, "if(stages[j].val<=0)continue;") {
		t.Error("a band can print 0/0")
	}
}

// The comparison is what makes a member's own rate legible: 88% adopted means
// one thing where everybody adopts 68% and another where everybody adopts 90%.
// It travels one way only — org aggregates out, nothing about the member back —
// and it is absent rather than invented where the registry has measured nothing.
func TestTheRegistrysOwnFunnelRidesAlongsideTheMembers(t *testing.T) {
	page, src := usagePageSource(), usageGoSource(t)
	if !strings.Contains(page, "var ORG=CFG.orgFunnel;") {
		t.Fatal("the page carries no registry funnel to compare against")
	}
	if !strings.Contains(src, `OrgFunnel:       json.RawMessage(s.orgFunnelJSON(window)),`) {
		t.Error("nothing fills it")
	}
	// Guarded at both ends: the server sends null where it has shown nothing,
	// and every reader checks before dividing.
	if !strings.Contains(src, "if f.Shown == 0 {") || !strings.Contains(src, `return "null"`) {
		t.Error("a registry that has shown nothing still offers a comparison")
	}
	for _, guard := range []string{"if(ORG&&ORG.shown>0){", "if(oFrom>0)lost+="} {
		if !strings.Contains(page, guard) {
			t.Errorf("missing guard %q", guard)
		}
	}
	// The band carries both the loss and the org rate; the headline carries the
	// org rate under it, where a reader would otherwise invent one.
	if !strings.Contains(page, `registry '+Math.round(oTo/oFrom*100)+'%'`) {
		t.Error("the bands do not carry the registry's rate")
	}
	if !strings.Contains(page, "' across the registry</span></p>';") {
		t.Error("the headline rate stands alone with nothing to read it against")
	}
}

func TestEveryMarkCanDrawEverySeries(t *testing.T) {
	css := ui.CSS()
	for _, key := range []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9"} {
		if !strings.Contains(css, ".vbar."+key+"{fill:var(--"+key+")}") {
			t.Errorf("a chart bar cannot draw %s", key)
		}
		if !strings.Contains(css, ".bar-fill."+key+"{background:var(--"+key+")}") {
			t.Errorf("a leaderboard bar cannot draw %s", key)
		}
	}
}

// The tools view is a third sibling, reached from the Tool calls tile on the
// summary: the number is there and everything behind it is one click away.
func TestToolsViewIsReachableFromTheTile(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "tileLink('Tool calls'") {
		t.Error("the Tool calls tile does not open the tools view")
	}
	if !strings.Contains(page, "function toolsView(w){") {
		t.Error("no tools view")
	}
	// The cross-tabs are the point: a matrix per dimension, and it stays away
	// when there is only one column to show.
	for _, field := range []string{"'harness'", "'model'", "'task'", "'project'"} {
		if !strings.Contains(page, "matrix(tools,"+field) {
			t.Errorf("no breakdown by %s", field)
		}
	}
	if !strings.Contains(page, "if(colOrder.length<2)return ''") {
		t.Error("a one-column matrix should not render — it is the totals column again")
	}
}

// The models view is the fourth sibling, reached from the Models tile on the
// summary: the count is there and everything behind it is one click away. The
// two model panels that used to sit on the summary moved behind it — three
// panels about models on the page a member opens first is the ninth stat-tile
// row the house style warns about.
func TestModelsViewIsReachableFromTheTile(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "tileLink('Models'") {
		t.Error("the Models tile does not open the models view")
	}
	if !strings.Contains(page, "function modelsView(d,w){") || !strings.Contains(page, "function modelDetailView(d,w,key){") {
		t.Error("no models view, or no per-model drill-down behind it")
	}
	// The cross-tabs are the point, and they reuse the tools view's matrix
	// rather than growing a second dense table.
	for _, field := range []string{"'harness'", "'task'", "'project'", "'turn_times'"} {
		if !strings.Contains(page, "matrix(rows,"+field) {
			t.Errorf("no model breakdown by %s", field)
		}
	}
	// One model panel, in one place. The summary keeps the tile.
	now := page[strings.Index(page, "// Now. The picture keeps its height"):]
	if strings.Contains(now, "html+=modelsPanel(") || strings.Contains(now, "html+=changePanel(") {
		t.Error("Now is still drawing the model panels the tile now leads to")
	}
	if !strings.Contains(page, "html+=changePanel(w);") {
		t.Error("the model-change report is not on the models view")
	}
}

// A model's cross-tabs are nested maps, so the keyed-row merge cannot carry
// them: it sums the named count fields and drops everything else. Two machines
// would then show one machine's breakdown under both machines' totals — the
// same trap tools fell into, and it has the same fix.
func TestModelCrossTabsSurviveTheMachineMerge(t *testing.T) {
	page := usageJS
	tabs := jsArray(t, page, "MODEL_TABS")
	for _, f := range []string{"harness", "task", "project", "turn_times", "variants", "effort"} {
		if !tabs[f] {
			t.Errorf("%s is a model cross-tab the merge does not fold", f)
		}
	}
	if !strings.Contains(page, "out.models=mergeModels(") {
		t.Error("the work merge still folds models as plain keyed rows")
	}
	if !strings.Contains(page, "if((m.peak_context||0)>(into.peak_context||0))into.peak_context=m.peak_context") {
		t.Error("a model's peak context is not maxed across machines")
	}
}

// How you work is ONE row of plates and then readings. It used to open on a
// second row and then a third — cost and tokens, then the turn-time
// distribution as four 40px figures — which is the nested stat-tile row the
// house style names, and a distribution is four lengths rather than four
// heroes. The panel also keeps the wall clock and the kind of session, both of
// which the summary collected and never showed.
func TestHowYouWorkIsOneRowOfPlates(t *testing.T) {
	page := usageJS
	panel := page[strings.Index(page, "function workPanel(w){"):]
	panel = panel[:strings.Index(panel, "function costPanel(w){")]
	if n := strings.Count(panel, `'<div class="tiles">'`); n != 1 {
		t.Errorf("the panel opens %d rows of plates; one is the limit inside a panel", n)
	}
	if !strings.Contains(panel, "bars(w.turn_times.map(") {
		t.Error("the turn-time distribution is not drawn as lengths")
	}
	if !strings.Contains(panel, "tile('Session time'") {
		t.Error("the window's wall clock is measured and never shown")
	}
	// Where the hours went is one control now, not a table per dimension, and
	// the kind of session is one of the ways it cuts.
	if !strings.Contains(panel, "breakdownTable(w,'work')") {
		t.Error("how you work has no break-down at all")
	}
	if !strings.Contains(page, "rows:function(w){return w.task_types||[];}") {
		t.Error("task types are collected by the summary and have no home on the page")
	}
	// Tools that would have helped is a claim about tools: it reads on the
	// tools view, against the tools that were used, and only there.
	if strings.Contains(panel, "absent_tools") {
		t.Error("absent tools are drawn twice — here and on the tools view")
	}
	if !strings.Contains(page, "var absent=(w&&w.absent_tools)||[];") {
		t.Error("absent tools are drawn nowhere at all")
	}
}

// "Why is drill-down only on Bash?" — because a tool opens only where a
// vocabulary was recorded behind it, and the page gave no sign of which tools
// those were. The Inside column names what is behind a tool and links to it; a
// dash is a tool that keeps nothing.
func TestToolsSayWhichOnesOpen(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, `<th class="num">Inside</th>`) {
		t.Error("the tools table has no column saying which rows open")
	}
	if !strings.Contains(page, `'<a href="'+esc(toolHref(t.name))+'">'`) {
		t.Error("the Inside cell is not the link — the only signal is a hover again")
	}
	if !strings.Contains(page, `if(!n)return '<span class="muted">—</span>';`) {
		t.Error("a tool that keeps nothing does not say so")
	}
	// Every vocabulary capture can record needs a noun on the page, or a new
	// kind arrives labelled "levels" and reads as a bug.
	for _, kind := range []string{capture.DetailProgram, capture.DetailFileType,
		capture.DetailAgent, capture.DetailSkill} {
		if !strings.Contains(page, kind+":[") {
			t.Errorf("the page has no noun for the %q vocabulary", kind)
		}
	}
	// The kind travels with the row across machines, or a second machine's
	// tools arrive unlabelled and the column reads "levels" for half of them.
	if !strings.Contains(page, "detail_kind:t.detail_kind") {
		t.Error("the cross-machine merge drops the vocabulary")
	}
	// One key per call for every kind but the programs, where a chained command
	// makes more keys than calls — so only the others may claim coverage.
	if !strings.Contains(page, "var missed=(dk!=='program'&&(t.calls||0)>dcalls)") {
		t.Error("the coverage line is gone, or it now lies about chained commands")
	}
}

// The outcome panel's whole risk is that it reads as a resolution rate. A
// benchmark owns the task, runs it eight times in a sandbox and injects a
// verifier; this sees live work once.
//
// What changed when the panel's four explanatory paragraphs came out: the
// guards moved from prose into marks. So this asserts the MARKS — a reader who
// never reads text has to be un-misled by the default view, and that is a claim
// about structure, not about wording.
func TestCheckEvidenceIsNeverAResolutionRate(t *testing.T) {
	page := usageJS
	start := strings.Index(page, "function checkPanel(w){")
	if start < 0 {
		t.Fatal("the outcome panel is gone or renamed")
	}
	panel := page[start:strings.Index(page, "function checkFineprint(")]
	// Nothing in the DEFAULT view may name a session resolved or successful.
	for _, banned := range []string{"resolved", "pass@1", "success rate", "Resolution"} {
		if strings.Contains(panel, banned) {
			t.Errorf("the panel says %q — check evidence is not a task outcome", banned)
		}
	}
	// The four states are named where they are drawn, not in a paragraph. The
	// legend is the only place the words appear and it sits on the bar.
	if !strings.Contains(page, "function checkLegend(all){") {
		t.Error("the states are not named beside the mark that uses them")
	}
	for _, label := range []string{"label:'Passed'", "label:'Failed'",
		"label:'Edited after'", "label:'No verdict'"} {
		if !strings.Contains(page, label) {
			t.Errorf("the state vocabulary is missing %s", label)
		}
	}
	// "Edited after" and "No verdict" are the words that carry what "stale" and
	// "unknown" needed a sentence to explain. A label that needed the sentence
	// back would be a regression.
	if strings.Contains(page, "reads <b>stale</b>") || strings.Contains(page, "<b>unknown</b> rather than") {
		t.Error("the state paragraph is back; the legend was supposed to retire it")
	}
	// Both halves of the comparison, on every row.
	if !strings.Contains(page, "<tr><th>Model</th><th>Client</th>") {
		t.Error("the comparison does not name the client beside the model")
	}
	// No confidence band, and no rank claim in the default view.
	if strings.Contains(panel, "confidence interval") || strings.Contains(panel, "±") {
		t.Error("the panel draws an interval over self-selected sessions")
	}
	// The claims that must still stand, now in one closed disclosure.
	fine := page[strings.Index(page, "function checkFineprint("):]
	fine = fine[:strings.Index(fine, "</details>")]
	for _, claim := range []string{
		"A passing check means a check ran",
		"does not rank models or clients",
		"No confidence band",
		"neither is kept",
	} {
		if !strings.Contains(fine, claim) {
			t.Errorf("the disclosure dropped the claim %q — it may be collapsed, never deleted", claim)
		}
	}
	if !strings.Contains(page, `<details class="fineprint"><summary>How this is measured</summary>`) {
		t.Error("the caveats are not in a closed disclosure")
	}
}

// Every rate carries its sample, and after the prose came out it carries it as
// a FRACTION rather than a sentence: "104 / 110" needs no reading and "of 110
// changed sessions" did. The floor is the product's own (config.MinSample).
func TestCheckRatesCarryTheirSample(t *testing.T) {
	page, src := usagePageSource(), usageGoSource(t)
	if !strings.Contains(page, " var MINSAMPLE=CFG.minSample;") {
		t.Fatal("the page carries no sample floor")
	}
	if !strings.Contains(src, `MinSample:       config.MinSample,`) {
		t.Error("the floor is a copy rather than the registry's own")
	}
	// The denominator is its own tile, and it comes FIRST — the order is what
	// explains which number the rates below divide by.
	changed := strings.Index(page, "tile('Changed sessions',fmtCount(all.changed)")
	ran := strings.Index(page, "tile('Ran a check',pct(checked(all),all.changed)")
	passed := strings.Index(page, "tile('Latest check passed',pct(all.passed,all.changed)")
	if changed < 0 || ran < 0 || passed < 0 {
		t.Fatal("the panel's three headline figures are gone or renamed")
	}
	if !(changed < ran && ran < passed) {
		t.Error("the denominator does not precede the rates that divide by it")
	}
	// And each rate shows its own fraction rather than describing it.
	if !strings.Contains(page, "fmtCount(checked(all))+' / '+fmtCount(all.changed)") ||
		!strings.Contains(page, "fmtCount(all.passed)+' / '+fmtCount(all.changed)") {
		t.Error("a rate states its denominator in prose rather than showing the fraction")
	}
	// A thin row keeps its count and loses its rate.
	if !strings.Contains(page, "var lean=n<MINSAMPLE;") {
		t.Error("a thin row is not marked")
	}
	if !strings.Contains(page, `class="thin-mark" title="under '+MINSAMPLE+`) {
		t.Error("the thin marker carries no meaning of its own")
	}
	// And it sits in the row's NAME cell, not in one of its figures: inline in a
	// right-aligned numeric column it pushed a two-digit count left of a
	// three-digit one and the column stopped lining up.
	if strings.Contains(page, `'<td class="num">'+esc(fmtCount(n))+
     (lean?`) {
		t.Error("the sample marker is back inside a numeric cell")
	}
	// And a thin task type is left out rather than pooled.
	if !strings.Contains(page, "return (t.changed||0)>=MINSAMPLE;") {
		t.Error("the task split does not apply the sample rule")
	}
}

// Missing capture reads as missing, and the units read off the column headers
// rather than off a paragraph above the table.
func TestNoCheckEvidenceReadsAsAbsentNotZero(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "No check data in this window.") {
		t.Error("an empty panel does not say the source is absent")
	}
	// The rates and the effort come off the same rows, so they cannot be drawn
	// from two different sets of sessions.
	if !strings.Contains(page, "var pairs=foldPairs(rows),all=foldOutcome(rows);") {
		t.Error("the panel's rates and effort are not folded from the same rows")
	}
	// "per session" lives in the header now. This is the assertion that stops
	// the paragraph coming back.
	if !strings.Contains(page, "function uh(name,unit){") {
		t.Error("there is no column-header unit, so a paragraph will be needed again")
	}
	for _, h := range []string{"uh('Changed','sessions')", "uh('Passed','of changed')"} {
		if !strings.Contains(page, h) {
			t.Errorf("column %s does not carry its unit", h)
		}
	}
	// The five columns that share one unit take a GROUP header rather than the
	// same two words five times, which is the shape that let the paragraph go.
	if !strings.Contains(page, `class="span num">Per changed session</th>`) {
		t.Error("the per-session columns have no group header")
	}
	if !strings.Contains(page, `class="span">What happened</th>`) {
		t.Error("nothing separates what happened from what it took")
	}
	// A column whose cell is a bar has nothing to order, so it offers no caret.
	if !strings.Contains(page, `'<th class="nosort">Checks</th>'`) {
		t.Error("the state-bar column still offers to sort by a bar")
	}
	if strings.Contains(page, "<b>per changed session</b>, over the same") {
		t.Error("the per-session paragraph is back; the headers were supposed to retire it")
	}
	// What fed the figures, as chips with a state rather than as a sentence.
	if !strings.Contains(page, "function sourceChips(w,all){") {
		t.Error("the panel never states its sources")
	}
	if !strings.Contains(page, `class="chip'+(on?'':' off')+'"><i></i>'`) {
		t.Error("a source that reported nothing is not distinguishable from one that did")
	}
	// The bar carries its own reading in words, for a screen reader and for a
	// two-colour palette where four hues cannot separate four states.
	if !strings.Contains(page, "function stateWords(c){") ||
		!strings.Contains(page, `role="img" aria-label="'+esc(stateWords(c))`) {
		t.Error("the state bar is colour alone")
	}
}

// The outcome rows survive the cross-machine merge, keyed on all three labels
// so a pair one machine never ran stays present rather than arriving as a zero.
func TestOutcomeRowsSurviveTheMachineMerge(t *testing.T) {
	page := usageJS
	if !strings.Contains(page, "out.model_clients=mergeRows(") {
		t.Fatal("outcome rows do not survive the cross-machine merge")
	}
	if !strings.Contains(page, "(r.model||'')+SEP+(r.client||'')+SEP+(r.task||'')") {
		t.Error("the merge key drops one of the three labels, folding rows that are not the same row")
	}
	// The pair fold and the merge share one separator, so they cannot key the
	// same row two ways.
	if !strings.Contains(page, " var SEP='\\u0000';") {
		t.Error("the composite keys use no shared separator")
	}
	if !strings.Contains(page, "var k=(r.model||'')+SEP+(r.client||'');") {
		t.Error("the pair fold keys on something other than the shared separator")
	}
}
