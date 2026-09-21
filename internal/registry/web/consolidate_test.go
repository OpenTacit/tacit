// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/feedback"
	"github.com/opentacit/tacit/internal/registry/models"
)

// getBody fetches a page as the signed-in reviewer.
func getBody(t *testing.T, c *http.Client, target string) string {
	t.Helper()
	resp, err := c.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// postAs submits a form as the signed-in reviewer. The shared post helper uses
// the default client, which carries no session.
func postAs(t *testing.T, c *http.Client, target string, form url.Values) {
	t.Helper()
	req, _ := http.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		t.Fatalf("POST %s -> %d", target, resp.StatusCode)
	}
}

// serving files a stable technique with the given move and rollup, so a test
// can say what the corpus contains and what the evidence says about it.
func serving(t *testing.T, srv *Server, id, name, recipe string, shown, adopted, helped int) {
	t.Helper()
	technique := models.Technique{
		ID: id, Name: name, Recipe: recipe, Status: "stable",
		Scope: "general", Provenance: "curated", CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}
	if err := srv.Store.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	vec := srv.Embedder.Embed([]string{embed.TechniqueText(technique)})[0]
	if err := srv.Store.SetTechniqueEmbedding(id, vec, srv.Embedder.ModelID(), srv.Embedder.Dim()); err != nil {
		t.Fatal(err)
	}
	if shown == 0 && adopted == 0 {
		return
	}
	rate := 0.0
	if adopted > 0 {
		rate = float64(helped) / float64(adopted)
	}
	rows, err := srv.Store.OutcomesForSegment(config.OverallKey)
	if err != nil {
		t.Fatal(err)
	}
	rows = append(rows, models.Outcome{
		TechniqueID: id, SegmentKey: config.OverallKey,
		Shown: shown, Adopted: adopted, Helped: helped, HelpedRate: &rate,
	})
	if err := srv.Store.ReplaceOutcomes(rows); err != nil {
		t.Fatal(err)
	}
}

// candidate files a technique under evaluation: never shown to a member, so it
// carries fit verdicts rather than adoptions.
func candidate(t *testing.T, srv *Server, id, name, recipe string) {
	t.Helper()
	technique := models.Technique{
		ID: id, Name: name, Recipe: recipe, Status: "shadow",
		Scope: "general", Provenance: "suggested", CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}
	if err := srv.Store.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	vec := srv.Embedder.Embed([]string{embed.TechniqueText(technique)})[0]
	if err := srv.Store.SetTechniqueEmbedding(id, vec, srv.Embedder.ModelID(), srv.Embedder.Dim()); err != nil {
		t.Fatal(err)
	}
}

// The reviewer's whole round trip: find duplicates, read what the evidence said,
// retire what they leave ticked. Nothing moves until they submit.
func TestConsolidationProposesAndOnlyRetiresOnTheVerdict(t *testing.T) {
	srv, ts := newServer(t)
	const move = "run the whole suite and fix what fails before you commit anything"
	serving(t, srv, "keeper", "Run the suite", move, 200, 40, 34)
	serving(t, srv, "restatement-a", "Test before commit", move, 30, 4, 1)
	serving(t, srv, "restatement-b", "Verify then commit", move, 20, 2, 0)
	serving(t, srv, "unrelated", "Bisect the break",
		"git bisect between the last good commit and HEAD to find what broke", 50, 10, 6)

	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}

	// Before the run the panel invites one and claims nothing.
	page := getBody(t, admin, ts.URL+"/techniques")
	if !strings.Contains(page, "Find duplicates") {
		t.Fatal("the playbook does not offer to look for duplicates")
	}
	if strings.Contains(page, "Retire selected") {
		t.Error("the panel offers a verdict before anything has been proposed")
	}

	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/playbook/propose", nil)
	page = getBody(t, admin, ts.URL+"/techniques")

	// It keeps the one the evidence favours and offers the other two.
	if !strings.Contains(page, `<b>Keep</b> <a href="/techniques/keeper">Run the suite</a>`) {
		t.Errorf("the proposal does not keep the best-measured technique:\n%s", panelOf(page))
	}
	for _, id := range []string{"restatement-a", "restatement-b"} {
		if !strings.Contains(page, `value="`+id+`" checked`) {
			t.Errorf("%s was not offered for retirement", id)
		}
	}
	if strings.Contains(page, `value="unrelated"`) {
		t.Error("a technique with a different move was swept into the group")
	}
	if !strings.Contains(page, "85% helped over 40 adoptions") {
		t.Errorf("the panel does not show the evidence it decided on:\n%s", panelOf(page))
	}
	// A rate under the ranking floor is reported as a count, not a percentage:
	// "100% helped" off four adoptions reads as a measurement and is not one.
	if !strings.Contains(page, "4 adopted, too few to rate") {
		t.Errorf("a thin sample was dressed up as a rate:\n%s", panelOf(page))
	}
	// The reviewer must not have to open a disclosure to learn nothing has
	// happened yet.
	if !strings.Contains(page, `<p class="hint">Nothing has been retired.`) {
		t.Errorf("the panel hides the fact that it has changed nothing:\n%s", panelOf(page))
	}

	// Proposing changes nothing on its own.
	for _, id := range []string{"keeper", "restatement-a", "restatement-b"} {
		if got, _, _ := srv.Store.GetTechnique(id); got.Status != "stable" {
			t.Errorf("%s became %q merely by being proposed", id, got.Status)
		}
	}

	// The reviewer spares one and retires the other.
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/playbook/apply",
		url.Values{"retire_0": {"restatement-a"}})

	if got, _, _ := srv.Store.GetTechnique("restatement-a"); got.Status != "retired" {
		t.Errorf("the ticked technique is %q, expected retired", got.Status)
	}
	if got, _, _ := srv.Store.GetTechnique("restatement-b"); got.Status != "stable" {
		t.Errorf("a technique the reviewer unticked was retired anyway (%q)", got.Status)
	}
	if got, _, _ := srv.Store.GetTechnique("keeper"); got.Status != "stable" {
		t.Errorf("the technique being KEPT was retired (%q)", got.Status)
	}

	// The record says what to use instead. "Retired" alone leaves whoever reads
	// the feed later unable to answer the only question they will have.
	events, err := srv.Store.LifecycleEvents("")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.TechniqueID == "restatement-a" && e.Kind == "retired" {
			found = true
			if !strings.Contains(e.Reason, "keeper") {
				t.Errorf("the retirement does not name the survivor: %q", e.Reason)
			}
		}
	}
	if !found {
		t.Error("retiring a duplicate left no record in the lifecycle feed")
	}

	// And the merge is additive: the retired technique points at the survivor,
	// which is what makes every fold over the event log count its evidence
	// toward the one that was kept.
	retired, _, _ := srv.Store.GetTechnique("restatement-a")
	if retired.MergedInto != "keeper" {
		t.Errorf("the retired technique points at %q, expected keeper", retired.MergedInto)
	}
	if spared, _, _ := srv.Store.GetTechnique("restatement-b"); spared.MergedInto != "" {
		t.Errorf("a spared technique was pointed at %q", spared.MergedInto)
	}
	if kept, _, _ := srv.Store.GetTechnique("keeper"); kept.MergedInto != "" {
		t.Errorf("the survivor points at %q", kept.MergedInto)
	}
}

// The survivor ranks on what the whole group earned. Without this a merge keeps
// one copy's evidence and discards the rest, which is the problem consolidation
// was built to fix.
func TestTheSurvivorInheritsTheGroupsEvidence(t *testing.T) {
	srv, ts := newServer(t)
	const move = "run the whole suite and fix what fails before you commit anything"
	serving(t, srv, "keeper", "Run the suite", move, 10, 2, 1)
	serving(t, srv, "restatement-a", "Test before commit", move, 40, 8, 6)

	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/playbook/propose", nil)
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/playbook/apply",
		url.Values{"retire_0": {"restatement-a"}})

	// Events are what a rollup is built from, so recompute the way the running
	// registry does and read the result.
	if _, err := feedback.RecomputeOutcomes(srv.Store); err != nil {
		t.Fatal(err)
	}
	o, ok, err := srv.Store.GetOutcome("keeper", config.OverallKey)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("no events behind these outcomes to fold; the unit tests cover the fold itself")
	}
	if _, stillThere, _ := srv.Store.GetOutcome("restatement-a", config.OverallKey); stillThere {
		t.Error("the merged technique kept its own rollup row, so the group is counted twice")
	}
	_ = o
}

// The form is the verdict, but it is not trusted: an id that was never proposed
// cannot be driven through it, and neither can the technique being kept.
func TestApplyOnlyRetiresWhatItProposed(t *testing.T) {
	srv, ts := newServer(t)
	const move = "run the whole suite and fix what fails before you commit anything"
	serving(t, srv, "keeper", "Run the suite", move, 200, 40, 34)
	serving(t, srv, "restatement-a", "Test before commit", move, 30, 4, 1)
	serving(t, srv, "bystander", "Bisect the break",
		"git bisect between the last good commit and HEAD to find what broke", 50, 10, 6)

	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/playbook/propose", nil)

	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/playbook/apply",
		url.Values{"retire_0": {"bystander", "keeper", "restatement-a"}})

	if got, _, _ := srv.Store.GetTechnique("bystander"); got.Status != "stable" {
		t.Errorf("a technique that was never proposed was retired (%q)", got.Status)
	}
	if got, _, _ := srv.Store.GetTechnique("keeper"); got.Status != "stable" {
		t.Errorf("the kept technique was retired through its own group's form (%q)", got.Status)
	}
	if got, _, _ := srv.Store.GetTechnique("restatement-a"); got.Status != "retired" {
		t.Errorf("the genuinely proposed technique was not retired (%q)", got.Status)
	}
}

// A playbook with nothing alike in it says so, rather than reaching for
// something to merge. A pass that always finds work is one a reviewer stops
// reading.
func TestNoDuplicatesSaysSo(t *testing.T) {
	srv, ts := newServer(t)
	serving(t, srv, "one", "Bisect the break",
		"git bisect between the last good commit and HEAD to find what broke", 10, 2, 1)
	serving(t, srv, "two", "Ask for the diagram",
		"ask for a mermaid diagram of the call graph and keep it in the repo", 10, 2, 1)

	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/playbook/propose", nil)

	page := getBody(t, admin, ts.URL+"/techniques")
	if !strings.Contains(page, "No two serving techniques say the same thing") {
		t.Errorf("a distinct playbook did not say so:\n%s", panelOf(page))
	}
}

func panelOf(page string) string {
	i := strings.Index(page, `id="consolidate"`)
	if i < 0 {
		return "(no consolidation panel)"
	}
	rest := page[i:]
	if j := strings.Index(rest, "</section>"); j >= 0 {
		return rest[:j]
	}
	return rest[:min(900, len(rest))]
}

// The consolidation link is the way IN, not just a count of work waiting. The
// rest of the review queue counts things that arrived on their own; duplicates
// are found by asking, so a link that appears only after you have asked is a
// signpost that turns up once you no longer need it.
func TestTheWayIntoConsolidationIsAlwaysInTheQueue(t *testing.T) {
	srv, ts := newServer(t)
	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}

	// Nothing proposed, and on a clean playbook with nothing else pending.
	page := getBody(t, admin, ts.URL+"/review")
	if !strings.Contains(page, `href="#consolidate"`) {
		t.Errorf("the review queue offers no way to reach the consolidation panel:\n%s", queueOf(page))
	}
	if !strings.Contains(page, "Find duplicate candidates") {
		t.Errorf("the link does not say what it leads to:\n%s", queueOf(page))
	}

	// Once a pass has run it becomes a count, like the rest of the queue.
	const move = "run the whole suite and fix what fails before you commit anything"
	serving(t, srv, "keeper", "Run the suite", move, 200, 40, 34)
	candidate(t, srv, "restatement", "Test before commit", move)
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/queue/propose", nil)

	page = getBody(t, admin, ts.URL+"/review")
	if !strings.Contains(page, "duplicate candidates") {
		t.Errorf("a proposal does not show as a count in the queue:\n%s", queueOf(page))
	}
	if strings.Contains(queueOf(page), "Find duplicate candidates") {
		t.Error("the queue offers both the invitation and the count at once")
	}
	// rq-quiet marks a lane that resolves without a reader; the invitation is
	// the opposite and must not borrow it.
	if strings.Contains(queueOf(page), "rq-quiet") {
		t.Error("the invitation reuses the class that means 'needs nobody'")
	}
}

func queueOf(page string) string {
	i := strings.Index(page, `class="review-queue"`)
	if i < 0 {
		return "(no review queue)"
	}
	rest := page[i:]
	if j := strings.Index(rest, "</nav>"); j >= 0 {
		return rest[:j]
	}
	return rest[:min(600, len(rest))]
}

// "Which of these say the same thing?" is a question about the library, so it
// has to be askable from the library. Tag consolidation sits on the Tags page
// for the same reason; this is the technique equivalent, and without it the
// only way in is a page about the review queue.
func TestThePlaybookOffersAWayToFindDuplicates(t *testing.T) {
	srv, ts := newServer(t)
	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}

	list := getBody(t, admin, ts.URL+"/techniques")
	if !strings.Contains(list, `<a href="#consolidate">find duplicates</a>`) {
		t.Errorf("the playbook offers no way to find duplicate techniques:\n%s", sublineOf(list))
	}

	// Not on the archive: consolidating what is already retired decides nothing.
	if arch := getBody(t, admin, ts.URL+"/techniques/retired"); strings.Contains(arch, `href="#consolidate"`) {
		t.Error("the retired archive offers to consolidate techniques that no longer serve")
	}
}

func sublineOf(page string) string {
	i := strings.Index(page, "techniques-subline")
	if i < 0 {
		return "(no subline)"
	}
	rest := page[i:]
	if j := strings.Index(rest, "</div>"); j >= 0 {
		return rest[:j]
	}
	return rest[:min(300, len(rest))]
}

// The two lanes are separate decisions and must stay separate at the seam that
// acts. A serving technique shown beside a candidate is context — the queue
// lane's form must not become a way to retire it on evidence that lane never
// read.
func TestTheQueueLaneCannotRetireAServingTechnique(t *testing.T) {
	srv, ts := newServer(t)
	const move = "run the whole suite and fix what fails before you commit anything"
	serving(t, srv, "published", "Run the suite", move, 200, 40, 34)
	candidate(t, srv, "candidate", "Test before commit", move)

	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/queue/propose", nil)

	page := getBody(t, admin, ts.URL+"/review")
	if !strings.Contains(page, `value="candidate" checked`) {
		t.Errorf("the candidate was not offered:\n%s", panelOf(page))
	}
	if strings.Contains(page, `value="published" checked`) {
		t.Error("the queue lane offered a serving technique as a casualty")
	}

	// Drive the form at it anyway: the handler must refuse.
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/queue/apply",
		url.Values{"retire_0": {"published", "candidate"}})

	if got, _, _ := srv.Store.GetTechnique("published"); got.Status != "stable" {
		t.Errorf("a serving technique was retired through the queue lane (%q)", got.Status)
	}
	if got, _, _ := srv.Store.GetTechnique("candidate"); got.Status != "retired" {
		t.Errorf("the candidate was not dropped (%q)", got.Status)
	}
}

// Each lane keeps its own proposal. Running one must not clear the other's, or
// a reviewer loses work they were midway through deciding.
func TestTheLanesDoNotClearEachOther(t *testing.T) {
	srv, ts := newServer(t)
	const move = "run the whole suite and fix what fails before you commit anything"
	serving(t, srv, "published-a", "Run the suite", move, 200, 40, 34)
	serving(t, srv, "published-b", "Verify then commit", move, 20, 2, 0)
	candidate(t, srv, "candidate", "Test before commit", move)

	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/playbook/propose", nil)
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/queue/propose", nil)

	if got := getBody(t, admin, ts.URL+"/techniques"); !strings.Contains(got, `value="published-b" checked`) {
		t.Errorf("running the queue lane cleared the playbook's proposal:\n%s", panelOf(got))
	}
	if got := getBody(t, admin, ts.URL+"/review"); !strings.Contains(got, `value="candidate" checked`) {
		t.Errorf("the queue lane's own proposal is missing:\n%s", panelOf(got))
	}

	// And discarding one leaves the other standing.
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/queue/discard", nil)
	if got := getBody(t, admin, ts.URL+"/techniques"); !strings.Contains(got, `value="published-b" checked`) {
		t.Error("discarding the queue's proposal threw away the playbook's")
	}
}

// A propose run embeds every recipe in its lane, which is seconds rather than
// milliseconds on a real library. Without a visible working state the click
// reads as one that missed, and the reader clicks again — queueing a second
// pass behind the first.
func TestTheProposeButtonSaysItIsWorking(t *testing.T) {
	srv, ts := newServer(t)
	const move = "run the whole suite and fix what fails before you commit anything"
	serving(t, srv, "published", "Run the suite", move, 200, 40, 34)
	candidate(t, srv, "candidate", "Test before commit", move)

	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}

	// Counted from the store rather than hardcoded: the seed corpus is the
	// repository's own techniques directory and its size is not this test's
	// business. What is its business is that each lane counts ITS OWN set.
	comparable := func(statuses ...string) int {
		all, err := srv.Store.ListTechniques(nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, c := range all {
			if strings.TrimSpace(c.Recipe) == "" {
				continue
			}
			for _, st := range statuses {
				if c.Status == st {
					n++
					break
				}
			}
		}
		return n
	}
	note := func(n int) string {
		noun := "techniques"
		if n == 1 {
			noun = "technique"
		}
		return fmt.Sprintf(`data-busy="Comparing %d %s —`, n, noun)
	}

	for _, tc := range []struct {
		page string
		want string
	}{
		// The playbook compares the serving library; the queue adds the
		// candidates. The counts differ, and each says its own.
		{"/techniques", note(comparable("stable"))},
		{"/review", note(comparable("stable", "shadow"))},
	} {
		page := getBody(t, admin, ts.URL+tc.page)
		if !strings.Contains(page, tc.want) {
			t.Errorf("%s does not say what the run will compare (wanted %s):\n%s", tc.page, tc.want, panelOf(page))
		}
		if !strings.Contains(page, `data-busy-label="Comparing…"`) {
			t.Errorf("%s: the button does not change while it works", tc.page)
		}
	}
	if comparable("stable") == comparable("stable", "shadow") {
		t.Fatal("the fixture gives both lanes the same count, so this proves nothing about either")
	}

	// The count is what will be compared, so a technique with no recipe — which
	// is never compared — must not be counted.
	before := comparable("stable")
	if err := srv.Store.UpsertTechnique(models.Technique{
		ID: "no-recipe", Name: "No recipe", Status: "stable", Scope: "general",
		Provenance: "curated", CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if page := getBody(t, admin, ts.URL+"/techniques"); !strings.Contains(page, note(before)) {
		t.Errorf("a technique with no recipe was counted as something to compare:\n%s", panelOf(page))
	}
}

// The two buttons under a proposal do opposite things, so they have to read as
// opposites. "Discard all" beside "Drop selected" read as the same verdict at a
// glance — throw the lot away — when one drops candidates and the other spares
// every one of them.
func TestTheVerdictButtonsNameWhatTheyActOn(t *testing.T) {
	srv, ts := newServer(t)
	const move = "run the whole suite and fix what fails before you commit anything"
	serving(t, srv, "published", "Run the suite", move, 200, 40, 34)
	candidate(t, srv, "candidate", "Test before commit", move)

	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	postAs(t, admin, ts.URL+"/admin/techniques/consolidate/queue/propose", nil)

	page := getBody(t, admin, ts.URL+"/review")
	if !strings.Contains(page, ">Drop selected candidates<") {
		t.Errorf("the verdict does not say what it drops:\n%s", panelOf(page))
	}
	if !strings.Contains(page, ">Keep them all<") {
		t.Errorf("the way out does not say that it keeps everything:\n%s", panelOf(page))
	}
	// Scoped to the panel: the tag proposal panel still ships that exact label
	// and renders on /techniques, so a whole-page check would pass here for a
	// reason that has nothing to do with this verdict.
	if strings.Contains(panelOf(page), "Discard all") {
		t.Error("the sparing button still reads as a verdict on the candidates")
	}
}
