// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/models"
)

// A registry that has never shown a technique to anybody drew fourteen panels,
// each correctly reporting that nothing had happened: no suggestions appeared ·
// try a longer window · no cohort dimensions attach to activity · no
// comparison-supported opportunities are measurable · no helped outcomes
// identify an established practice, and nine more. Every sentence true, and the
// page as a whole reading as a product that is broken — on the second screen a
// new operator ever sees.
func TestOutcomesSaysOneThingBeforeThereIsAnythingToSay(t *testing.T) {
	_, ts := newServer(t)
	code, body := fetchHTML(t, ts.URL+"/outcomes")
	if code != 200 {
		t.Fatalf("outcomes: %d", code)
	}
	// It has to ORIENT first. A reader arriving here has not been told what a
	// technique is, and the page below is unreadable without that. The welcome
	// does it, in the page's own voice.
	if !strings.Contains(body, `class="cs-welcome"`) {
		t.Fatal("a registry with no events does not orient the reader")
	}
	// AND IT DRAWS NO FUNNEL. The hero used to open this view as a preview of
	// the picture the reader would keep, fed a literal zero Overview on a page
	// that renders only while nothing has ever happened — so it could report
	// nothing but three noughts, whatever the registry did. The first event
	// swaps this page for the dashboard, where the funnel has something in it.
	for _, gone := range []string{"flow-empty", ">SHOWN<", ">ADOPTED<", ">HELPED<", `id="funnel"`} {
		if strings.Contains(body, gone) {
			t.Errorf("the first screen still draws %q, which can only ever be empty here", gone)
		}
	}
	// The panels that had nothing to say must not be drawn saying it.
	for _, gone := range []string{
		"Adoption by cohort and area",
		"Highest helped rate",
		"No comparison-supported opportunities",
		"Registry and evidence health",
	} {
		if strings.Contains(body, gone) {
			t.Errorf("empty panel %q still drawn on a registry with no events", gone)
		}
	}
	// And every next step is a control, not a sentence about a control.
	for _, want := range []string{`href="/review"`, `href="/members"`,
		`href="/settings?tab=automation"`, "tacit connect"} {
		if !strings.Contains(body, want) {
			t.Errorf("the cold start does not offer %q as a next step", want)
		}
	}
	// The model key is the setting `tacit init` deliberately does not ask for,
	// so this is where an operator is told it exists — and told what it buys,
	// and where to put it if they would rather not use the form.
	for _, want := range []string{"Set up a model", "TACIT_LLM_API_KEY"} {
		if !strings.Contains(body, want) {
			t.Errorf("the cold start does not say %q", want)
		}
	}
	// No invented figure: an absent measurement is not a zero percent, and a
	// step with nothing to count shows a dash rather than a nought.
	if strings.Contains(body, "cs-figure\">0<") {
		t.Error("a step prints 0 where it has nothing to measure")
	}
	// The steps are a to-do list, and the corner flag says which of them still
	// wants the reader. On a registry with no machine wired, no keys and no
	// model that is three of the four; the review queue is empty, so that step
	// carries none.
	if n := strings.Count(body, `class="cs-todo"`); n != 3 {
		t.Errorf("%d steps flagged to do; want the three that are actually outstanding", n)
	}
	// Connecting a machine leads, because until one is wired nothing can be
	// shown and there is nothing to measure. It used to be one line of small
	// print inside the Members panel.
	if !strings.Contains(body, "Connect your tools") {
		t.Error("the cold start does not offer connecting a machine as a step")
	}
	if i, j := strings.Index(body, "Connect your tools"), strings.Index(body, "Approve draft techniques"); i < 0 || j < 0 || i > j {
		t.Error("connecting a machine does not lead the row")
	}
}

// The page opens by telling the reader what OpenTacit does for their
// organization and only then asks them to set it up. On the ground, because a
// plate here would be a readout, and this is the page talking. It sat between
// the funnel and the steps first, where it read as a fifth step that had lost
// its frame.
func TestColdStartWelcomeOpensThePageOnTheGround(t *testing.T) {
	_, ts := newServer(t)
	code, body := fetchHTML(t, ts.URL+"/outcomes")
	if code != 200 {
		t.Fatalf("outcomes: %d", code)
	}
	for _, want := range []string{
		product.Name() + " tracks how your organization uses AI.",
		"measures technique outcomes",
		"stores approved techniques in the organization’s playbook",
		"suggests them to people and agents when relevant",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the first screen does not say %q", want)
		}
	}
	if strings.Contains(body, "panel cs-welcome") || strings.Contains(body, "cs-welcome panel") {
		t.Error("the welcome is drawn on a plate")
	}
	welcome := strings.Index(body, `class="cs-welcome"`)
	steps := strings.Index(body, `class="grid cs-next"`)
	if welcome < 0 || steps < 0 {
		t.Fatalf("the first screen is missing a piece: welcome=%d steps=%d", welcome, steps)
	}
	// The welcome, then the steps it names. Nothing between them and nothing
	// above them.
	if welcome > steps {
		t.Errorf("the welcome does not open the page: welcome=%d steps=%d", welcome, steps)
	}
}

// Connecting is finished at a terminal, not in this browser: `tacit connect` on
// the machine holding the tools is the whole of it. The step used to carry an
// "Open Members" button for the sake of the row's shape, which sent a reader who
// wanted to do this to a page that cannot do it.
func TestColdStartConnectCarriesNoButton(t *testing.T) {
	for _, step := range []string{coldStartConnect(0, 0), coldStartConnect(0, 2), coldStartConnect(2, 2)} {
		if strings.Contains(step, `class="btn`) || strings.Contains(step, "btn-row") {
			t.Errorf("the connect step still carries a control:\n%s", step)
		}
	}
	// And the command that does close it is still named where the step is open.
	if !strings.Contains(coldStartConnect(0, 0), "tacit connect") {
		t.Error("the open step does not name the command that finishes it")
	}
}

// A key that exists is an invitation; a key that has authenticated is a machine
// actually wired. Only the second closes this step.
func TestColdStartConnectCountsMachinesThatArrived(t *testing.T) {
	if got := coldStartConnect(0, 0); !strings.Contains(got, "cs-todo") {
		t.Error("no machine connected and the step is not flagged")
	}
	// Invitations minted but none landed: still outstanding, and said so.
	got := coldStartConnect(0, 2)
	if !strings.Contains(got, "cs-todo") {
		t.Error("invitations out but none landed, and the step reads as done")
	}
	if !strings.Contains(got, "This step completes after an invited machine connects") {
		t.Errorf("the step does not say invitations are already out:\n%s", got)
	}
	if got := coldStartConnect(2, 2); strings.Contains(got, "cs-todo") {
		t.Error("two machines are reaching the registry and the step still says to do")
	}
	// No invented figure where there is nothing to count.
	if strings.Contains(coldStartConnect(0, 0), `cs-figure">0<`) {
		t.Error("the step prints 0 where it has nothing to measure")
	}
}

// THE FLAG IS STATE. A step that is done loses it, so the page is a list a
// reader can finish rather than three panels permanently marked as chores.
func TestColdStartFlagsOnlyTheStepsStillOutstanding(t *testing.T) {
	if got := coldStartMembers(0); !strings.Contains(got, "cs-todo") {
		t.Error("nobody invited and the step is not flagged")
	}
	if got := coldStartMembers(3); strings.Contains(got, "cs-todo") {
		t.Error("three member keys are out and the step still says to do")
	}
	// And it reports what it counts: keys, not people.
	if got := coldStartMembers(3); !strings.Contains(got, "3 member keys are out") {
		t.Errorf("the done step does not say what it counted:\n%s", got)
	}
	if got := coldStartDrafts(0); strings.Contains(got, "cs-todo") {
		t.Error("an empty review queue still says to do")
	}
	if got := coldStartDrafts(4); !strings.Contains(got, "cs-todo") {
		t.Error("four drafts waiting and the step is not flagged")
	}
	// The model key is read from the environment the registry runs in.
	t.Setenv("TACIT_LLM_API_KEY", "")
	if got := coldStartModel(); !strings.Contains(got, "cs-todo") {
		t.Error("no model key and the step is not flagged")
	}
	t.Setenv("TACIT_LLM_API_KEY", "sk-test")
	got := coldStartModel()
	if strings.Contains(got, "cs-todo") {
		t.Error("a configured model still says to do")
	}
	if !strings.Contains(got, "Your model") {
		t.Errorf("a configured model is still asked for:\n%s", got)
	}
}

// One event is enough to turn the page on. The threshold is "has anything ever
// happened", not a volume somebody has to reach.
func TestOutcomesReturnsAfterASingleEvent(t *testing.T) {
	_, ts := newServer(t)
	seedOneEvent(t, ts)
	code, body := fetchHTML(t, ts.URL+"/outcomes")
	if code != 200 {
		t.Fatalf("outcomes: %d", code)
	}
	// The welcome, which only the cold start draws. "Nothing has been shown
	// yet" used to stand in for it and no longer can: the hero that carried
	// that sentence is off this view, so its absence would prove nothing.
	if strings.Contains(body, `class="cs-welcome"`) {
		t.Fatal("still showing the cold start after an event arrived")
	}
	if !strings.Contains(body, "Adoption by cohort and area") {
		t.Fatal("the full page did not come back")
	}
}

// A FINISHED STEP SAYS SO. Left bare it reads as a panel that was never part of
// the list rather than as one the reader has closed — which is the whole point
// of drawing the three as a list.
func TestColdStartMarksAStepThatIsDone(t *testing.T) {
	done := coldStartDrafts(0)
	if !strings.Contains(done, "cs-done") || !strings.Contains(done, "Done") {
		t.Errorf("an empty review queue is not marked done:\n%s", done)
	}
	if strings.Contains(coldStartDrafts(4), "cs-done") {
		t.Error("four drafts waiting and the step is marked done")
	}
}

// THE CONTROL IS THE LAST THING IN EVERY STEP, so all three fall to the floor of
// their plates and sit on one line across the row. Followed by the aside, the
// slack landed above the button instead and the three sat at three heights.
func TestColdStartPutsTheControlLast(t *testing.T) {
	t.Setenv("TACIT_LLM_API_KEY", "")
	for _, step := range []string{coldStartDrafts(0), coldStartDrafts(4),
		coldStartMembers(0), coldStartMembers(2), coldStartModel()} {
		btn := strings.Index(step, `class="btn-row"`)
		if btn < 0 {
			t.Errorf("a step offers no control:\n%s", step)
			continue
		}
		if aside := strings.Index(step, "cs-aside"); aside > btn {
			t.Errorf("the aside follows the control, so the button will not reach the floor:\n%s", step)
		}
	}
}

// A settings path is read and retyped, so it is written the way a person writes
// it. Spelled out from the root it was three lines of monospace in a panel whose
// own button is one.
func TestColdStartWritesTheSettingsPathTheWayAPersonWouldSayIt(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	if got := homeRelative(filepath.Join(home, ".config", "tacit", "registry.env")); got != "~/.config/tacit/registry.env" {
		t.Errorf("homeRelative = %q", got)
	}
	// A path that is not under the home directory is left exactly as it is:
	// half a path is worse than a long one.
	if got := homeRelative("/etc/tacit/registry.env"); got != "/etc/tacit/registry.env" {
		t.Errorf("homeRelative rewrote a path outside the home directory: %q", got)
	}
}

// SETTINGS OPENED ON THE SUBJECT. The model rows live under "Automation and
// model"; a button that landed on Access left the reader hunting for the tab
// the sentence above the button had just named.
func TestColdStartOpensSettingsOnTheModelTab(t *testing.T) {
	t.Setenv("TACIT_LLM_API_KEY", "")
	if got := coldStartModel(); !strings.Contains(got, `href="/settings?tab=automation"`) {
		t.Errorf("the model step does not open its own tab:\n%s", got)
	}
	t.Setenv("TACIT_LLM_API_KEY", "sk-test")
	if got := coldStartModel(); !strings.Contains(got, `href="/settings?tab=automation"`) {
		t.Errorf("the configured model step does not open its own tab:\n%s", got)
	}
}

// WHO CAN REACH IT IS ABOVE THE FOLD. Measured on the dashboard at 1440x900,
// the pulse row ends at y=626 and the health panel begins at y=3220 — so a
// member count in the health panel is three screens from the question it
// answers. It rides the sticky top bar, which never scrolls away, and the pulse
// row, which is the last thing above the fold.
func TestTheDashboardSaysWhoCanReachThePlaybook(t *testing.T) {
	_, ts := newServer(t)
	seedOneEvent(t, ts)
	code, body := fetchHTML(t, ts.URL+"/outcomes")
	if code != 200 {
		t.Fatalf("outcomes: %d", code)
	}
	// The bar: a count, and a door to the page that changes it.
	if !strings.Contains(body, "member") {
		t.Error("the top bar does not say who can reach this playbook")
	}
	// The pulse row, above the fold, and FIRST in it: every other figure in the
	// row is a rate over what these people did.
	pulse := strings.Index(body, `class="tiles"`)
	if pulse < 0 {
		t.Fatal("no pulse row")
	}
	row := body[pulse:]
	i, j := strings.Index(row, ">Members<"), strings.Index(row, "Org-scoped share")
	if i < 0 {
		t.Fatal("the pulse row carries no member tile")
	}
	if j < 0 || i > j {
		t.Error("the member tile does not lead the row")
	}
}

// The tile reports what it counted, and asks only where there is something to
// ask for.
func TestMemberTileAsksOnlyWhenThereIsNobody(t *testing.T) {
	none := memberPulse(nil, "/members")
	if none.Value != "0" {
		t.Errorf("empty: value=%q", none.Value)
	}
	some := memberPulse([]models.MemberKey{mk("a", true), mk("b", false)}, "/members")
	if some.Value != "2" || some.Delta != "1 joined" {
		t.Errorf("two invited, one joined: value=%q delta=%q", some.Value, some.Delta)
	}
	// The ask is the button, not the delta: a delta slot is for a figure, and
	// an imperative wearing the delta's colour says the wrong thing about a
	// count of zero. With nobody invited there is no figure either, so the
	// line goes.
	if none.Delta != "" {
		t.Errorf("the empty tile still carries a second line: %q", none.Delta)
	}
	// THE DELTA NEVER REPEATS THE VALUE. Where every invitation has been used,
	// "5 joined" beside a 5 was the number twice.
	all := memberPulse([]models.MemberKey{mk("a", true), mk("b", true)}, "/members")
	if all.Value != "2" || all.Delta != "all joined" {
		t.Errorf("everyone joined: value=%q delta=%q", all.Value, all.Delta)
	}
	for _, tile := range []Tile{none, some} {
		if tile.Action.Href == "" || tile.Action.Label == "" {
			t.Errorf("the tile carries no control: %+v", tile.Action)
		}
		// A button inside an anchor is not valid HTML, and reads as two
		// doorways to a keyboard, so a tile with a control is not itself a link.
		if tile.Href != "" {
			t.Error("the tile is both a link and a button")
		}
	}
	row := string(TileRow(false, some))
	if strings.Contains(row, `class="tile-link"`) {
		t.Error("a tile with a control was still wrapped as a doorway")
	}
	if i, j := strings.Index(row, `class="tile-act"`), strings.LastIndex(row, "</div>"); i < 0 || i > j {
		t.Error("the control is not inside the tile it belongs to")
	}
}

// SPARKLINES SIT ON THE FLOOR OF THEIR TILE. Tiles in a row stretch to the
// tallest, and a spark that followed its delta in normal flow landed wherever
// that delta stopped — a delta wrapping to two lines pushed it down, one that
// did not left it high. Measured across the pulse row at 1440x900 the four
// sparks sat at two heights forty-eight pixels apart. They are the same
// measurement drawn the same way, so they belong on one line.
func TestSparklinesSitOnTheFloorOfTheirTile(t *testing.T) {
	if rule := setRule(t, ".spark"); !strings.Contains(rule, "margin-top:auto") {
		t.Errorf(".spark does not fall to the bottom of its tile: %s", rule)
	} else if !strings.Contains(rule, "padding-top") {
		t.Error(".spark has no air above it in a tile that is not stretched")
	}
	// It can only fall if the tile is a column with room to fall through.
	if rule := setRule(t, ".tile"); !strings.Contains(rule, "flex-direction:column") {
		t.Errorf(".tile is not a column, so margin-top:auto has nothing to push against: %s", rule)
	}
}

// A LINKED TILE IS THE SAME HEIGHT AS THE PLAIN ONE BESIDE IT. The anchor
// stretches to the row as any flex item does, but the plate inside it only
// grows to fill if the anchor is a column — so a second a.tile-link rule,
// written for the usage page and scoped by a comment rather than a selector,
// turned it into a block and left every linked tile in the pulse row short of
// its neighbours. One selector, one rule.
func TestLinkedTilesStretchToTheirRow(t *testing.T) {
	if n := strings.Count(appCSS, "a.tile-link{"); n != 1 {
		t.Fatalf("a.tile-link is declared %d times; the later one silently wins", n)
	}
	rule := setRule(t, "a.tile-link")
	if !strings.Contains(rule, "flex-direction:column") {
		t.Errorf("a linked tile is not a column, so its plate cannot fill it: %s", rule)
	}
	if strings.Contains(rule, "display:block") {
		t.Errorf("a linked tile is a block, so it sizes to its content: %s", rule)
	}
	if !strings.Contains(setRule(t, "a.tile-link .tile"), "flex:1 1 auto") {
		t.Error("the plate inside a linked tile does not grow to fill it")
	}
}
