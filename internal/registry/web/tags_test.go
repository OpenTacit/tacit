// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

// seedTagTechniques gives the store a small vocabulary with the exact pathology the
// page exists for: a near-synonym pair (setup / agent-setup), a singleton, and
// tags on techniques that are NOT live (a draft, a retired technique) whose tags must not
// count as part of the organization's language.
func seedTagTechniques(t *testing.T, s *Server) {
	t.Helper()
	for _, c := range []models.Technique{
		{ID: "t1", Name: "One", Status: "stable", Tags: []string{"setup", "audit"}},
		{ID: "t2", Name: "Two", Status: "stable", Tags: []string{"agent-setup", "audit"}},
		{ID: "t3", Name: "Three", Status: "stable", Tags: []string{"audit", "singleton"}},
		{ID: "t4", Name: "Draft", Status: "draft", Tags: []string{"draft-only"}},
		{ID: "t5", Name: "Retired", Status: "retired", Tags: []string{"retired-only"}},
	} {
		if err := s.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}
}

func tagsOf(t *testing.T, s *Server, id string) []string {
	t.Helper()
	c, ok, err := s.Store.GetTechnique(id)
	if err != nil || !ok {
		t.Fatalf("get %s: %v ok=%v", id, err, ok)
	}
	return c.Tags
}

func TestTagsPageShowsTheLiveVocabulary(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)

	code, body := fetchHTML(t, ts.URL+"/techniques/tags")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{">audit<", ">setup<", ">agent-setup<", ">singleton<"} {
		if !strings.Contains(body, want) {
			t.Fatalf("tags page missing %q", want)
		}
	}
	// The vocabulary is what is LIVE: a draft's tags are a proposal, and a
	// retired technique's have left the language.
	for _, gone := range []string{"draft-only", "retired-only"} {
		if strings.Contains(body, gone) {
			t.Fatalf("tags page counts %q, which is not on any live technique", gone)
		}
	}
	// Singletons are flagged for a human's eye, never pruned by a rule.
	if !strings.Contains(body, "used once") {
		t.Fatal("tags page does not surface the singletons")
	}
	if !strings.Contains(body, `action="/admin/tags/rename"`) ||
		!strings.Contains(body, `action="/admin/tags/delete"`) {
		t.Fatal("tags page offers no consolidation actions")
	}
}

// Renaming to a tag that ALREADY EXISTS is the merge — one rewrite, one control.
// The technique that carried both must not end up with a duplicate.
func TestTagMergeIntoAnExistingTag(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)
	// Give t1 both, so the merge has a technique where the two collide.
	if err := srv.Store.UpsertTechnique(models.Technique{ID: "t1", Name: "One", Status: "stable",
		Tags: []string{"setup", "agent-setup", "audit"}}); err != nil {
		t.Fatal(err)
	}

	post(t, ts.URL+"/admin/tags/rename", url.Values{"from": {"agent-setup"}, "to": {"setup"}})

	if got := tagsOf(t, srv, "t1"); !equal(got, []string{"setup", "audit"}) {
		t.Fatalf("t1 tags = %q, want [setup audit] — the merge must dedupe, not duplicate", got)
	}
	if got := tagsOf(t, srv, "t2"); !equal(got, []string{"setup", "audit"}) {
		t.Fatalf("t2 tags = %q, want [setup audit]", got)
	}
	_, body := fetchHTML(t, ts.URL+"/techniques/tags")
	if strings.Contains(body, ">agent-setup<") {
		t.Fatal("agent-setup survived the merge")
	}
}

// Renaming to a tag that does NOT exist is a plain rename — the same rewrite.
// The typed target is normalized on the same rules as any ingested tag, so a
// human cannot coin "Agent Setup" here.
func TestTagRenameNormalizesTheTypedTarget(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)

	post(t, ts.URL+"/admin/tags/rename", url.Values{"from": {"singleton"}, "to": {"  Agent Setup  "}})

	if got := tagsOf(t, srv, "t3"); !equal(got, []string{"audit", "agent-setup"}) {
		t.Fatalf("t3 tags = %q, want [audit agent-setup] — the typed target must be normalized", got)
	}
}

// Deleting a tag removes the LABEL, never the technique.
func TestTagDeleteKeepsTheTechniques(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)

	post(t, ts.URL+"/admin/tags/delete", url.Values{"tag": {"audit"}})

	for _, id := range []string{"t1", "t2", "t3"} {
		if _, ok, _ := srv.Store.GetTechnique(id); !ok {
			t.Fatalf("deleting a tag deleted technique %s", id)
		}
		for _, tag := range tagsOf(t, srv, id) {
			if tag == "audit" {
				t.Fatalf("%s still carries the deleted tag", id)
			}
		}
	}
	if got := tagsOf(t, srv, "t1"); !equal(got, []string{"setup"}) {
		t.Fatalf("t1 tags = %q, want [setup] — delete must take only its own tag", got)
	}
}

// A rewrite that would do nothing must not churn every technique in the registry.
func TestTagRenameIgnoresNoOps(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)
	before := tagsOf(t, srv, "t1")

	for _, v := range []url.Values{
		{"from": {"audit"}, "to": {"audit"}}, // same tag
		{"from": {"audit"}, "to": {"   "}},   // empty target
		{"from": {""}, "to": {"audit"}},      // empty source
	} {
		post(t, ts.URL+"/admin/tags/rename", v)
	}
	if got := tagsOf(t, srv, "t1"); !equal(got, before) {
		t.Fatalf("a no-op rewrite changed tags: %q -> %q", before, got)
	}
}

// The draft edit form is where a human names a new technique in the org's
// language. It must show them that language — and the control it shows it in has
// to be one a <datalist> can actually serve.
func TestDraftEditFormOffersTheVocabulary(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)

	code, body := fetchHTML(t, ts.URL+"/drafts/t4")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// Existing tags are chips; ONE add-field carries the autocomplete.
	if !strings.Contains(body, `<input type="checkbox" name="tag" value="draft-only" checked>`) {
		t.Fatal("the draft's existing tags are not editable as a set")
	}
	if !strings.Contains(body, `class="tag-add" id="tag-add" type="text" name="add_tags" list="tag-vocab"`) ||
		!strings.Contains(body, `<datalist id="tag-vocab">`) {
		t.Fatal("the add-a-tag field offers no autocomplete against the existing vocabulary")
	}
	if !strings.Contains(body, `<option value="audit">3 techniques</option>`) {
		t.Fatal("the vocabulary offered does not carry use counts")
	}
	// The comma-separated box must never come back. A datalist matches the WHOLE
	// value of its input and REPLACES it when an option is picked, so on that
	// control, choosing a suggestion silently discarded every tag already on the
	// technique — an autocomplete that destroyed data.
	if strings.Contains(body, `name="tags" list=`) {
		t.Fatal("the comma-separated tag box is back; a datalist on it discards the technique's existing tags")
	}
}

// Editing tags as a set: keep some chips, drop others, add new ones — in one save.
func TestDraftEditFormSavesTheTagSet(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)
	if err := srv.Store.UpsertTechnique(models.Technique{ID: "t4", Name: "Draft", Status: "draft",
		Tags: []string{"audit", "setup", "draft-only"}}); err != nil {
		t.Fatal(err)
	}

	// Keep audit and setup (chips survive), drop draft-only (chip unchecked, so
	// the browser posts nothing for it), and add two more.
	post(t, ts.URL+"/admin/techniques/edit/t4", url.Values{
		"name": {"Draft"}, "tag": {"audit", "setup"}, "add_tags": {"Review, agent setup"}})

	got := tagsOf(t, srv, "t4")
	want := []string{"audit", "setup", "review", "agent-setup"}
	if !equal(got, want) {
		t.Fatalf("tags = %q, want %q — kept chips, dropped the unchecked one, added and normalized the new ones", got, want)
	}
}

// Unchecking every chip CLEARS the tags. If "no tag fields posted" were read as
// "the form did not mention tags", the edit would be silently ignored and the
// member could never remove the last tag.
func TestDraftEditFormCanClearEveryTag(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)

	post(t, ts.URL+"/admin/techniques/edit/t4", url.Values{"name": {"Draft"}, "add_tags": {""}})

	if got := tagsOf(t, srv, "t4"); len(got) != 0 {
		t.Fatalf("tags = %q, want none — unchecking every chip must clear them", got)
	}
}

// post submits a browser form and follows the redirect back to the page.
func post(t *testing.T, target string, form url.Values) {
	t.Helper()
	req, _ := http.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("POST %s -> %d", target, resp.StatusCode)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fakeMerger stands in for the language model.
type fakeMerger struct {
	reply string
	err   error
	calls int
}

func (f *fakeMerger) Complete(_ string, _ int) (string, error) {
	f.calls++
	return f.reply, f.err
}

// The load-bearing guarantee: PROPOSING MUST NOT WRITE. A tag merge rewrites
// every technique carrying the tag, so the model's opinion may never reach the
// store without a human passing through Apply.
func TestProposingMergesChangesNothing(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)
	srv.TagMerger = &fakeMerger{reply: `[{"from":["agent-setup"],"into":"setup","why":"same concept"}]`}
	before := tagsOf(t, srv, "t2")

	post(t, ts.URL+"/admin/tags/propose", url.Values{})

	if got := tagsOf(t, srv, "t2"); !equal(got, before) {
		t.Fatalf("proposing merges rewrote the techniques: %q -> %q", before, got)
	}
	// It is shown for review, with the model's reason.
	_, body := fetchHTML(t, ts.URL+"/techniques/tags")
	for _, want := range []string{"Proposed merges", "same concept",
		`name="src_0" value="agent-setup" checked`, `name="into_0" value="setup"`,
		`action="/admin/tags/apply"`, `action="/admin/tags/discard"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("proposal panel missing %q", want)
		}
	}
	// The two verdicts share a row, which means Discard's button sits OUTSIDE
	// its form and references it by id — HTML forbids nesting forms. If that
	// empty form ever goes missing the button silently does nothing, so the
	// wiring is asserted, not assumed.
	if !strings.Contains(body, `type="submit" form="tag-discard"`) ||
		!strings.Contains(body, `<form id="tag-discard"`) {
		t.Fatal("the Discard button is not wired to its form — it would do nothing when clicked")
	}
	// Discarding suggestions destroys nothing; the danger colour on this page
	// belongs to Delete, the one control that actually removes something.
	if strings.Contains(body, `class="danger" type="submit" form="tag-discard"`) {
		t.Fatal("Discard is styled as destructive, but it throws away suggestions, not techniques")
	}
}

// Accept: the reviewer applies what the model proposed.
func TestApplyProposedMerge(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)
	srv.TagMerger = &fakeMerger{reply: `[{"from":["agent-setup"],"into":"setup","why":"x"}]`}
	post(t, ts.URL+"/admin/tags/propose", url.Values{})

	post(t, ts.URL+"/admin/tags/apply", url.Values{
		"proposal": {"0"}, "src_0": {"agent-setup"}, "into_0": {"setup"}})

	if got := tagsOf(t, srv, "t2"); !equal(got, []string{"setup", "audit"}) {
		t.Fatalf("t2 tags = %q, want [setup audit]", got)
	}
	// The verdict is spent: the panel does not linger after it is applied.
	_, body := fetchHTML(t, ts.URL+"/techniques/tags")
	if strings.Contains(body, "Proposed merges") {
		t.Fatal("the proposal panel survived being applied")
	}
}

// Adjust: the form is the source of truth, not the stored proposal. A reviewer
// who unchecks a source tag and retypes the target must get what they see.
func TestApplyHonoursTheReviewersAdjustments(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)
	srv.TagMerger = &fakeMerger{reply: `[{"from":["agent-setup","singleton"],"into":"setup","why":"x"}]`}
	post(t, ts.URL+"/admin/tags/propose", url.Values{})

	// Spare "singleton" (omitted from src_0), and send the merge to "audit"
	// instead of the proposed "setup".
	post(t, ts.URL+"/admin/tags/apply", url.Values{
		"proposal": {"0"}, "src_0": {"agent-setup"}, "into_0": {"audit"}})

	if got := tagsOf(t, srv, "t2"); !equal(got, []string{"audit"}) {
		t.Fatalf("t2 tags = %q, want [audit] — the reviewer's target must win over the model's", got)
	}
	if got := tagsOf(t, srv, "t3"); !equal(got, []string{"audit", "singleton"}) {
		t.Fatalf("t3 tags = %q — the spared tag was merged anyway", got)
	}
}

// Decline: discard leaves the vocabulary exactly as it was.
func TestDiscardProposedMerges(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)
	srv.TagMerger = &fakeMerger{reply: `[{"from":["agent-setup"],"into":"setup","why":"x"}]`}
	post(t, ts.URL+"/admin/tags/propose", url.Values{})
	before := tagsOf(t, srv, "t2")

	post(t, ts.URL+"/admin/tags/discard", url.Values{})

	if got := tagsOf(t, srv, "t2"); !equal(got, before) {
		t.Fatalf("discard rewrote the techniques: %q -> %q", before, got)
	}
	_, body := fetchHTML(t, ts.URL+"/techniques/tags")
	if strings.Contains(body, "Proposed merges") {
		t.Fatal("discard did not clear the proposals")
	}
}

// A failed run and "nothing to merge" are different answers, and neither may be
// reported as the other.
func TestProposeDistinguishesFailureFromNoMerges(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)

	srv.TagMerger = &fakeMerger{reply: `[]`}
	post(t, ts.URL+"/admin/tags/propose", url.Values{})
	_, body := fetchHTML(t, ts.URL+"/techniques/tags")
	if !strings.Contains(body, "The model found no duplicate tags") {
		t.Fatal("an empty answer should be reported as an answer")
	}

	srv.TagMerger = &fakeMerger{err: errors.New("model unreachable")}
	post(t, ts.URL+"/admin/tags/propose", url.Values{})
	_, body = fetchHTML(t, ts.URL+"/techniques/tags")
	if !strings.Contains(body, "could not run") || !strings.Contains(body, "model unreachable") {
		t.Fatal("a failed run must say so, not read as 'no merges found'")
	}
}

// Techniques is no longer one page: it is the library, the vocabulary that
// library is described in, and the archive. They are sibling views of one thing
// and they get a section nav, so the shape of the section is legible from any of
// its pages — the Tags page used to be reachable only from a link buried in a
// run-on count line on /techniques, and unreachable from anywhere else at all.
func TestTechniquesSectionNav(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)

	// From the list: Tags is a visible destination, and the list is current.
	_, list := fetchHTML(t, ts.URL+"/techniques")
	if !strings.Contains(list, `<a href="/techniques/tags">Tags<span>`) {
		t.Fatal("the list does not offer Tags as a destination")
	}
	// "All", not "Techniques": the tab sits under a heading that already says
	// Techniques, and repeating the section's name for one of its views makes
	// that view look like the whole section.
	if !strings.Contains(list, `<a href="/techniques" class="active" aria-current="true">All`) {
		t.Fatal("the list does not mark itself current in the section nav")
	}

	// From Tags: the nav is the same, and Tags is current — so there is a way back.
	_, tags := fetchHTML(t, ts.URL+"/techniques/tags")
	if !strings.Contains(tags, `<a href="/techniques/tags" class="active" aria-current="true">Tags`) {
		t.Fatal("the Tags page does not mark itself current")
	}
	if !strings.Contains(tags, `<a href="/techniques">All<span>`) {
		t.Fatal("the Tags page offers no way back to the library")
	}

	// The retired archive marks itself current too, rather than leaving the
	// reader on a page no nav item claims.
	_, archive := fetchHTML(t, ts.URL+"/techniques/retired")
	if !strings.Contains(archive, `class="active" aria-current="true">Retired`) {
		t.Fatal("the archive does not mark itself current")
	}

	// Order: All · Retired · Tags — the two views of the techniques, then the
	// vocabulary they are described in. Asserted, because it is a decision.
	nav := list[strings.Index(list, `class="crumb-menu-pop"`):]
	nav = nav[:strings.Index(nav, "</details>")]
	iAll, iRet, iTags := strings.Index(nav, ">All"), strings.Index(nav, ">Retired"), strings.Index(nav, ">Tags")
	if !(iAll >= 0 && iAll < iRet && iRet < iTags) {
		t.Fatalf("section nav order should be All · Retired · Tags: %s", nav)
	}

	// All three items are ROUTES. A nav whose items behave differently from one
	// another is not a nav — the archive used to be a query flag on the page you
	// were already on, while its neighbour navigated somewhere else.
	for _, href := range []string{`href="/techniques"`, `href="/techniques/retired"`, `href="/techniques/tags"`} {
		if !strings.Contains(nav, href) {
			t.Fatalf("section nav item is not its own route: missing %s", href)
		}
	}
	if strings.Contains(nav, "status=retired") {
		t.Fatal("the archive is a filter again, not a view")
	}
}

// A door is only worth showing once there is something behind it.
func TestSectionNavHidesAnEmptyArchive(t *testing.T) {
	srv, ts := newServer(t)
	if err := srv.Store.UpsertTechnique(models.Technique{ID: "live", Name: "Live", Status: "stable"}); err != nil {
		t.Fatal(err)
	}
	_, body := fetchHTML(t, ts.URL+"/techniques")
	if strings.Contains(body, ">Retired<") {
		t.Fatal("the section nav offers an archive with nothing in it")
	}
}
