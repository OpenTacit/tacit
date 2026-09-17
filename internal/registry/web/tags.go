// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The tag vocabulary, and the tools to consolidate it.
//
// Tags are a free vocabulary fed by four unbounded sources — curated techniques,
// member contributions, LLM suggestion runs, and federated imports — and until
// now nothing could ever remove or merge one. models.NormalizeTags (called by
// every UpsertTechnique) stops two tags that LOOK the same from becoming two tags.
// This file handles the case it cannot: two tags that MEAN the same thing.
// That is a judgment, so it gets a human surface rather than a rule.
package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/suggest"
	"github.com/opentacit/tacit/internal/registry/tagmerge"
	"github.com/opentacit/tacit/internal/ui"
)

// tagStat is one tag and the live techniques carrying it.
type tagStat struct {
	Tag   string
	Count int
}

// liveTagVocabulary is the tag vocabulary as it actually stands: the tags on
// LIVE techniques, commonest first. Drafts are proposals — their tags are not yet
// the organization's language — and retired techniques have left it. This is the
// single definition of "the vocabulary", used by the Tags page, the draft edit
// form's autocomplete, and (via the suggest profile) the research prompt.
func liveTagVocabulary(techniques []models.Technique) []tagStat {
	use := map[string]int{}
	for _, c := range techniques {
		if c.Status == "draft" || c.Status == "retired" {
			continue
		}
		for _, t := range c.Tags {
			use[t]++
		}
	}
	out := make([]tagStat, 0, len(use))
	for tag, n := range use {
		out = append(out, tagStat{tag, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}

// tagDatalist is the browser-native autocomplete offered wherever a human types
// a tag. The edit form used to be a bare comma-separated text box with the
// vocabulary nowhere in sight, which made coining "agent-setup" beside "setup"
// the path of least resistance. Showing the vocabulary at the point of tagging
// makes reuse the default and invention the deliberate act.
func tagDatalist(id string, vocab []tagStat) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<datalist id="%s">`, html.EscapeString(id))
	for _, t := range vocab {
		fmt.Fprintf(&b, `<option value="%s">%d technique%s</option>`,
			html.EscapeString(t.Tag), t.Count, plural(t.Count))
	}
	b.WriteString(`</datalist>`)
	return b.String()
}

// ---- the Tags page ----

// pageTags is the consolidation surface: every live tag with its use count, the
// singletons flagged, and merge/rename/delete for each. It is a sub-page of
// Techniques, not a fourth nav destination — it is maintenance of the library,
// and the nav stays the three questions a reader arrives with.
func (s *Server) pageTags(r *http.Request, user oidc.Claims) page {
	crumbs := []crumb{{label: "Playbook", href: playbookHome}, {label: "Tags", href: ""}}
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		return storeUnavailablePage("techniques", crumbs, err)
	}
	vocab := liveTagVocabulary(techniques)

	retired := 0
	for _, c := range techniques {
		if c.Status == "retired" {
			retired++
		}
	}

	viewMenu := techniquesViews("tags", liveCount(techniques), len(vocab), retired, "")

	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Tags used by live techniques. Curated techniques, contributions, suggestion runs, and federated imports can add tags. Merge duplicate tags and delete tags that are not useful.</p></div>`)

	if len(vocab) == 0 {
		b.WriteString(`<p class="empty">No live technique carries a tag yet.</p>`)
		return page{active: "techniques", crumbs: crumbs, content: b.String(), viewMenu: viewMenu}
	}

	singles := 0
	for _, t := range vocab {
		if t.Count == 1 {
			singles++
		}
	}
	fmt.Fprintf(&b, `<div class="sub">%d tag%s across %d live technique%s · %d used once</div>`,
		len(vocab), plural(len(vocab)), liveCount(techniques), plural(liveCount(techniques)), singles)

	// A tag used once is not wrong — a genuinely specific tag is fine. It is
	// only a problem when it MEANS the same as another tag, and that is a
	// judgment no rule can make. So singletons are flagged for a human's eye,
	// never pruned automatically.
	if singles > 0 {
		fmt.Fprintf(&b, ui.Sub("", "%d tag%s used by a single technique")+
			ui.Fine(`Review tags used once. Merge any that duplicate an existing tag.`),
			singles, plural(singles))
	}

	// The model reads the vocabulary and proposes; it never applies. The button
	// disables itself on submit — the run is a single short model call, but a
	// double-submit would start a second one and throw the first away.
	b.WriteString(`<div class="toolbar">` +
		`<form method="post" action="/admin/tags/propose" id="merge-form">` +
		`<button type="submit" id="merge-btn" title="Send the tag list to the model and propose merges for review. No merge runs until you accept it.">` +
		`Suggest merges</button></form>` +
		`<span class="sub" id="merge-note"></span></div>` +
		`<script>(function(){var f=document.getElementById('merge-form');if(!f)return;` +
		`f.addEventListener('submit',function(){var b=document.getElementById('merge-btn');` +
		`b.disabled=true;b.textContent='Checking tags…';` +
		`document.getElementById('merge-note').textContent='This may take a few seconds or up to two minutes, depending on the model. Review each proposal before applying it.';});})();</script>`)
	b.WriteString(s.tagProposalPanel())

	rows := make([]tableRow, 0, len(vocab))
	for _, t := range vocab {
		esc := html.EscapeString(t.Tag)
		count := fmt.Sprintf(`<a href="/techniques?tag=%s">%d</a>`, url.QueryEscape(t.Tag), t.Count)
		if t.Count == 1 {
			count = fmt.Sprintf(`<a href="/techniques?tag=%s"><span class="status serious">%d</span></a>`,
				url.QueryEscape(t.Tag), t.Count)
		}
		// Merging INTO an existing tag and renaming to a fresh one are the same
		// operation — rewrite this tag as that one on every technique carrying it —
		// so they are one control, with the vocabulary offered as autocomplete.
		action := fmt.Sprintf(
			`<form class="tag-op" method="post" action="/admin/tags/rename">`+
				`<input type="hidden" name="from" value="%s">`+
				`<input type="text" name="to" list="tag-vocab" placeholder="merge into / rename to…" aria-label="New name for %s" required>`+
				`<button type="submit">Apply</button></form>`+
				`<form class="tag-op" method="post" action="/admin/tags/delete" `+
				`onsubmit="return confirm('Remove the tag \'%s\' from %d technique%s? The techniques are kept; only the tag is removed.')">`+
				`<input type="hidden" name="tag" value="%s">`+
				`<button class="danger" type="submit">Delete</button></form>`,
			esc, esc, esc, t.Count, plural(t.Count), esc)
		rows = append(rows, tableRow{Cells: []string{
			fmt.Sprintf(`<a class="tag" href="/outcomes/tag/%s">%s</a>`, url.PathEscape(t.Tag), esc),
			count, action,
		}})
	}
	b.WriteString(dataTable([]tableCol{
		{Label: "tag"}, {Label: "techniques", Num: true}, {Label: "consolidate"},
	}, rows))
	b.WriteString(tagDatalist("tag-vocab", vocab))
	b.WriteString(`<p class="note">Renaming to a tag that already exists merges the two. Both actions rewrite every technique carrying the tag, and are recorded as ordinary technique edits, so each technique keeps its version history.</p>`)
	return page{active: "techniques", crumbs: crumbs, content: b.String(), viewMenu: viewMenu}
}

func liveCount(techniques []models.Technique) int {
	n := 0
	for _, c := range techniques {
		if c.Status != "draft" && c.Status != "retired" {
			n++
		}
	}
	return n
}

// ---- the operations ----
//
// Every operation below rewrites tags across the whole library, so each one
// carries the browser session gate — same posture as promote/reject. adminOnly
// wraps the routes, but it is a no-op until TACIT_ADMIN_EMAILS is set, and
// these actions must not be open to anyone who can reach the host.

// handleTagRename rewrites one tag as another across every technique carrying it.
// Renaming to a tag that already exists IS the merge — there is no separate
// operation, because "rename x to y" and "merge x into y" are the same rewrite,
// and having two controls for one rewrite is how a UI grows a vocabulary
// problem of its own.
func (s *Server) handleTagRename(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/techniques/tags") {
		return
	}
	_ = r.ParseForm()
	from := models.NormalizeTag(r.PostFormValue("from"))
	// The target is normalized on the SAME rules as any ingested tag, so a
	// human typing "Agent Setup" here cannot create a tag that no write path
	// could otherwise produce.
	to := models.NormalizeTag(r.PostFormValue("to"))
	if from == "" || to == "" || from == to {
		http.Redirect(w, r, "/techniques/tags", http.StatusFound)
		return
	}
	s.rewriteTag(w, r, from, func(tags []string) []string {
		out := make([]string, 0, len(tags))
		for _, t := range tags {
			if t == from {
				out = append(out, to) // NormalizeTags dedupes if `to` is already present
			} else {
				out = append(out, t)
			}
		}
		return out
	})
}

// handleTagDelete removes a tag from every technique carrying it. The techniques
// are untouched — only the label goes.
func (s *Server) handleTagDelete(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/techniques/tags") {
		return
	}
	_ = r.ParseForm()
	tag := models.NormalizeTag(r.PostFormValue("tag"))
	if tag == "" {
		http.Redirect(w, r, "/techniques/tags", http.StatusFound)
		return
	}
	s.rewriteTag(w, r, tag, func(tags []string) []string {
		out := make([]string, 0, len(tags))
		for _, t := range tags {
			if t != tag {
				out = append(out, t)
			}
		}
		return out
	})
}

// rewriteTag applies fn to the tags of every technique carrying `tag` and saves it.
// It goes through saveTechniqueEdit — the same path a hand edit takes — so each
// touched technique is archived to its version history and re-embedded. A tag
// consolidation is an edit to the techniques, not a mutation of some separate tag
// table: there isn't one, and inventing one would put the vocabulary and the
// techniques permanently at risk of disagreeing.
func (s *Server) rewriteTag(w http.ResponseWriter, r *http.Request, tag string, fn func([]string) []string) {
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, technique := range techniques {
		if !hasTag(technique.Tags, tag) {
			continue
		}
		technique.Tags = fn(technique.Tags)
		if err := s.saveTechniqueEdit(technique); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, "/techniques/tags", http.StatusFound)
}

// ---- LLM-proposed merges ----
//
// The model PROPOSES; the human disposes. A tag merge silently rewrites every
// technique carrying the tag, so it is exactly the kind of judgment that must
// not be automated end to end — and "degradation" and "delegation" are a
// standing reminder of how confidently a wrong merge can present itself.
//
// The flow: Suggest merges -> proposals are validated against the real
// vocabulary (tagmerge.validate) -> rendered as a form the reviewer can adjust
// (drop a source tag, retype a target) -> Apply selected, or Discard.

// tagProposal is one round of proposals awaiting a verdict.
type tagProposal struct {
	Items []tagmerge.Proposal
	Err   string // a failed run is shown, not swallowed: "no merges" and "it broke" are different answers
}

// tagMerger resolves the model: the injected one (tests), else this registry's
// Anthropic client.
//
// TACIT_TAGMERGE_MODEL overrides TACIT_SUGGEST_MODEL for this job alone, because
// the two jobs are not alike. A suggestion run makes several web searches and
// burns a lot of tokens, so it is often pointed at a cheap, high-rate-limit model.
// A merge proposal is one short call over a list of words, and it is a pure
// judgment: whether "ethics" and "audit" are one concept or two. Judgment is
// where a weaker model over-merges — it will confidently fold distinctions the
// library depends on — and it costs almost nothing to run this one on a better
// model. Every proposal is reviewed by a human either way; this just wastes less
// of their attention on bad ones.
func (s *Server) tagMerger() (tagmerge.Model, error) {
	a, err := suggest.FromEnv()
	if s.TagMerger != nil {
		return s.TagMerger, nil
	}
	if err != nil {
		return nil, err
	}
	if m := strings.TrimSpace(os.Getenv("TACIT_TAGMERGE_MODEL")); m != "" {
		a = a.WithModel(m)
	}
	return a, nil
}

func (s *Server) handleTagPropose(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/techniques/tags") {
		return
	}
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	prop := &tagProposal{}
	if m, err := s.tagMerger(); err != nil {
		prop.Err = err.Error()
	} else if items, err := tagmerge.Propose(m, tagVocabForMerge(techniques)); err != nil {
		prop.Err = err.Error()
	} else {
		prop.Items = items
	}
	s.tagPropMu.Lock()
	s.tagProp = prop
	s.tagPropMu.Unlock()
	http.Redirect(w, r, "/techniques/tags", http.StatusFound)
}

// tagVocabForMerge gives the model the vocabulary WITH the names of the
// techniques carrying each tag. A tag is only judgeable in the company it
// keeps: "context" on a prompt-caching technique and "context" on a project-memory
// technique are the same word doing two jobs, and the names are the only way to see it.
func tagVocabForMerge(techniques []models.Technique) []tagmerge.Entry {
	examples := map[string][]string{}
	for _, c := range techniques {
		if c.Status == "draft" || c.Status == "retired" {
			continue
		}
		for _, t := range c.Tags {
			if len(examples[t]) < 4 {
				examples[t] = append(examples[t], c.Name)
			}
		}
	}
	vocab := liveTagVocabulary(techniques)
	out := make([]tagmerge.Entry, 0, len(vocab))
	for _, v := range vocab {
		out = append(out, tagmerge.Entry{Tag: v.Tag, Count: v.Count, Examples: examples[v.Tag]})
	}
	return out
}

func (s *Server) handleTagDiscard(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/techniques/tags") {
		return
	}
	s.tagPropMu.Lock()
	s.tagProp = nil
	s.tagPropMu.Unlock()
	http.Redirect(w, r, "/techniques/tags", http.StatusFound)
}

// handleTagApply applies the reviewer's verdict. It reads the FORM, not the
// stored proposal: the reviewer may have dropped source tags or retyped a
// target, and what they see on screen is what must happen. The stored proposal
// is only the starting point.
func (s *Server) handleTagApply(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/techniques/tags") {
		return
	}
	_ = r.ParseForm()
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// One rewrite map: source tag -> target. Built from the form, normalized on
	// the same rules as any ingested tag, so a reviewer cannot type a tag here
	// that no write path could otherwise produce.
	rewrite := map[string]string{}
	for i := range r.Form["proposal"] {
		into := models.NormalizeTag(r.PostFormValue(fmt.Sprintf("into_%d", i)))
		if into == "" {
			continue
		}
		for _, from := range r.Form[fmt.Sprintf("src_%d", i)] {
			from = models.NormalizeTag(from)
			if from != "" && from != into {
				rewrite[from] = into
			}
		}
	}
	if len(rewrite) > 0 {
		for _, technique := range techniques {
			changed := false
			tags := make([]string, 0, len(technique.Tags))
			for _, t := range technique.Tags {
				if to, ok := rewrite[t]; ok {
					tags = append(tags, to) // NormalizeTags dedupes on write
					changed = true
					continue
				}
				tags = append(tags, t)
			}
			if !changed {
				continue
			}
			technique.Tags = tags
			if err := s.saveTechniqueEdit(technique); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
	}
	s.tagPropMu.Lock()
	s.tagProp = nil
	s.tagPropMu.Unlock()
	http.Redirect(w, r, "/techniques/tags", http.StatusFound)
}

// tagProposalPanel renders the verdict form: every proposal, its reason, its
// source tags as checkboxes (uncheck to spare one), and its target as an
// editable field. Accept what you like, adjust what you don't, decline the rest.
func (s *Server) tagProposalPanel() string {
	s.tagPropMu.Lock()
	prop := s.tagProp
	s.tagPropMu.Unlock()
	if prop == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(`<section class="panel" id="proposed"><h2>Proposed merges</h2>`)
	switch {
	case prop.Err != "":
		fmt.Fprintf(&b, `<p class="hint">The merge analysis could not run: %s</p>`+
			`<form method="post" action="/admin/tags/discard"><button type="submit">Dismiss</button></form></section>`,
			html.EscapeString(prop.Err))
		return b.String()
	case len(prop.Items) == 0:
		b.WriteString(`<p class="empty">The model found no duplicate tags.</p>` +
			`<form method="post" action="/admin/tags/discard"><button type="submit">Dismiss</button></form></section>`)
		return b.String()
	}

	b.WriteString(`<p class="hint">A language model proposed these merges. None have been applied. Deselect a source tag to exclude it, or edit the target tag. A merge updates every technique with the source tag.</p>`)
	b.WriteString(`<form method="post" action="/admin/tags/apply">`)
	for i, p := range prop.Items {
		fmt.Fprintf(&b, `<div class="merge-row"><input type="hidden" name="proposal" value="%d">`, i)
		b.WriteString(`<div class="merge-tags">`)
		for _, from := range p.From {
			fmt.Fprintf(&b, `<label class="merge-src"><input type="checkbox" name="src_%d" value="%s" checked> <span>%s</span></label>`,
				i, html.EscapeString(from), html.EscapeString(from))
		}
		fmt.Fprintf(&b, `<span class="merge-arrow" aria-hidden="true">→</span>`+
			`<input class="merge-into" type="text" name="into_%d" value="%s" list="tag-vocab" aria-label="Merge into">`,
			i, html.EscapeString(p.Into))
		b.WriteString(`</div>`)
		if p.Why != "" {
			fmt.Fprintf(&b, `<p class="merge-why">%s</p>`, html.EscapeString(p.Why))
		}
		b.WriteString(`</div>`)
	}
	// Both verdicts sit on one row. They were two sibling <form>s, which are
	// block-level, so they stacked — Apply above Discard, each shrink-wrapped to
	// its own label, a ragged little staircase. The discard button lives outside
	// its form and references it by id, the same trick the drafts checkboxes use,
	// so the two can share a row without nesting forms (which HTML forbids).
	//
	// Discard is NOT styled as destructive. It destroys nothing — it throws away
	// suggestions, not techniques — and spending the danger colour on a
	// harmless action is how that colour stops meaning anything when it matters.
	// The genuinely destructive control on this page is Delete, on the tags below.
	b.WriteString(`<div class="merge-actions">` +
		`<button type="submit">Apply selected merges</button>` +
		`<button class="quiet" type="submit" form="tag-discard">Discard all</button>` +
		`</div></form>` +
		`<form id="tag-discard" method="post" action="/admin/tags/discard"></form>`)
	b.WriteString(`</section>`)
	return b.String()
}

// ---- the tag field ----

// tagField is the tag editor on the draft edit form. Tags are a SET, and it took
// making the autocomplete work to notice that the old control was not one.
//
// It was a single comma-separated text box with a datalist on it. That does not
// work, and it is worth being precise about why, because it looked like it did:
// an HTML datalist matches against the ENTIRE value of its input and REPLACES
// that value when an option is chosen. So on a draft tagged "audit, review",
// opening the list and picking "setup" silently discards both existing tags —
// and after typing one tag and a comma, the list stops offering anything at all,
// because "audit, se" matches no option. The autocomplete was not merely absent;
// it was a trap.
//
// A datalist can only serve a field holding ONE value. So the set becomes what it
// always was: existing tags are chips you can uncheck to drop, and a single
// add-field — one tag, so the autocomplete works exactly as intended — puts new
// ones in. With JavaScript the add-field turns each entry into a chip and clears
// itself, so several tags can be added before saving; without it, the field is
// still submitted (comma-separated) and everything works, one save at a time.
func tagField(tags []string, vocab []tagStat) string {
	var b strings.Builder
	b.WriteString(`<div class="tag-field"><span class="tag-field-label">Tags</span>`)
	b.WriteString(`<div class="tag-chips" id="tag-chips">`)
	for _, t := range tags {
		b.WriteString(tagChip(t))
	}
	b.WriteString(`</div>`)
	b.WriteString(`<input class="tag-add" id="tag-add" type="text" name="add_tags" list="tag-vocab" ` +
		`placeholder="Add a tag…" autocomplete="off" aria-label="Add a tag">`)
	b.WriteString(tagDatalist("tag-vocab", vocab))
	if len(vocab) > 0 {
		b.WriteString(`<details class="fineprint"><summary>Tag guidance</summary><ul><li>` +
			`Reuse an existing tag when it fits. New tags add separate filter options, outcome views, and map groups. ` +
			`<a href="/techniques/tags">The vocabulary</a>.</li></ul></details>`)
	}
	// Progressive enhancement: turn an entry into a chip and clear the field, so
	// several tags can be added in one edit. Enter must not submit the form —
	// on a form whose other control is "Save edits", that would save the technique
	// when the member meant to add a tag.
	b.WriteString(`<script>(function(){
var add=document.getElementById('tag-add'),chips=document.getElementById('tag-chips');
if(!add||!chips)return;
function norm(v){return v.toLowerCase().trim().replace(/[_\s\/]+/g,'-').replace(/-+/g,'-').replace(/^-|-$/g,'');}
function has(t){return [].some.call(chips.querySelectorAll('input[name="tag"]'),function(i){return i.value===t;});}
function commit(){
  var parts=add.value.split(',');
  var added=false;
  parts.forEach(function(p){var t=norm(p); if(!t||has(t))return;
    var l=document.createElement('label');l.className='tag-chip';
    var i=document.createElement('input');i.type='checkbox';i.name='tag';i.value=t;i.checked=true;
    var s=document.createElement('span');s.textContent=t;
    l.appendChild(i);l.appendChild(s);chips.appendChild(l);added=true;});
  if(added||parts.length)add.value='';
}
add.addEventListener('keydown',function(e){
  if(e.key==='Enter'||e.key===','){e.preventDefault();commit();}});
// A datalist pick fires 'change', not a keystroke.
add.addEventListener('change',function(){if(add.value)commit();});
// Anything still typed when the form is submitted counts — nobody should lose a
// tag for not pressing Enter.
add.form.addEventListener('submit',function(){commit();});
})();</script>`)
	b.WriteString(`</div>`)
	return b.String()
}

func tagChip(tag string) string {
	return fmt.Sprintf(`<label class="tag-chip"><input type="checkbox" name="tag" value="%s" checked><span>%s</span></label>`,
		html.EscapeString(tag), html.EscapeString(tag))
}

// formTags reads the tag SET back off the edit form: the chips that survived
// (repeated "tag"), plus anything left in the add field. Returned as the
// comma-separated string models.ApplyTechniqueEdit already understands, so the JSON
// API path and the browser path stay one code path.
//
// The second return is false only when the form carried no tag control at all —
// so a member who unchecks every chip CLEARS the tags, rather than having the
// edit silently ignored.
func formTags(form url.Values) (string, bool) {
	if !form.Has("tag") && !form.Has("add_tags") {
		return "", false
	}
	parts := append([]string{}, form["tag"]...)
	for _, add := range form["add_tags"] {
		parts = append(parts, strings.Split(add, ",")...)
	}
	return strings.Join(parts, ","), true
}

// ---- the Techniques section nav ----

// techniquesNav is the section's own navigation. Techniques is no longer one
// page: it is the library (the list), the vocabulary that library is described in
// (Tags), and the archive (Retired). Those are sibling VIEWS of one thing, and
// they were reachable only as links buried in a run-on sub-line — "43 reviewed
// techniques · 3 org-scoped · 66 tags · 4 retired · click a row for detail" —
// where a destination reads as trivia, indistinguishable from the counts around it.
//
// They fold into the breadcrumb instead of a separate nav row: the last crumb
// ("Techniques / X") becomes a dropdown of the sibling views, so the section's
// shape stays reachable from any of its pages without spending a row of vertical
// space on tabs. The top nav stays the three questions; this is one level down.
//
// activeLabel is the label of the current view, which the caller also uses as the
// last breadcrumb crumb so the trail and the dropdown summary agree.
// group ("cohort" or "") is the current grouping lens; when set, it rides on the
// Map and All hrefs so the choice follows across those two views through the URL
// — the single grouping state, no localStorage.
func techniquesViews(active string, live, tags, retired int, group string) []crumbOpt {
	opt := func(key, href, label string, n int) crumbOpt {
		return crumbOpt{label: label, href: href, count: n, active: key == active}
	}
	allHref, mapHref := "/techniques", "/techniques/map"
	// "tags" is the server default, so it needs no param; anything else rides on
	// both hrefs. That includes "none", which only the list can honour — the map
	// draws itself arranged by tags regardless, and carrying the param anyway is
	// what hands the reader their ungrouped list back on the way home.
	if group == "cohort" || group == "none" {
		allHref += "?group=" + group
		mapHref += "?group=" + group
	}
	// count < 0 omits the number — used by Map, which is a second view of the SAME
	// live set as All, so restating "43" beside it would be noise.
	//
	// Map leads: it is the section's default view (Playbook's nav target), the
	// shape of the library at a glance. Then All (the same live set as a scannable,
	// groupable list), Retired (the archive), and Tags (the vocabulary the techniques
	// are described in). "All" — not "Playbook": the view sits under a crumb that
	// already says Playbook, so repeating the section's name for one of its views
	// would make that view look like the whole section.
	views := []crumbOpt{
		opt("map", mapHref, "Map", -1),
		opt("all", allHref, "All", live),
	}
	// The archive is only a door worth showing once something is behind it — but
	// keep it while it is the active view, so leaving it is possible.
	//
	// A negative count is a caller that does not KNOW how many are retired: a
	// technique's own trail has no list to hand. The door still belongs in that
	// switcher, because the archive is a view of this section wherever the
	// reader is in it — so only an explicit zero closes it.
	if retired != 0 || active == "retired" {
		views = append(views, opt("retired", "/techniques/retired", "Retired", retired))
	}
	views = append(views, opt("tags", "/techniques/tags", "Tags", tags))
	return views
}

// techniquesViewLabel is the display label for a Techniques view key, used as
// the last breadcrumb crumb and the dropdown summary.
// techniqueCrumbs is the trail above one technique: the section, the list it is
// on, the technique, and the level under the technique.
//
// It used to read Playbook / <name> — which skipped the list entirely, so a
// reader on a technique could not reach the Map or the archive, and nothing on
// the line said a technique HAS anything under it. History is a named page, so
// it belongs in the caret (the rule in chrome.go); the techniques themselves
// are a per-item family and stay in the list above.
func techniqueCrumbs(id, name, status, here string, versions int) []crumb {
	view := "all"
	if status == "retired" {
		view = "retired"
	}
	// Counts are the list's business, not the trail's: this switcher is here to
	// move, and a number beside "Retired" that the page cannot see is worse
	// than none.
	views := techniquesViews(view, -1, -1, -1, "")
	trail := []crumb{
		{label: "Playbook", href: playbookHome},
		{label: techniquesViewLabel(view), menu: views},
	}
	step := crumb{label: name}
	// The caret only where there is something under it. One prior version is
	// the first thing worth a History page; none means the technique is all
	// there is, and a caret onto a single page would be a promise of depth
	// that is not there.
	if versions > 0 {
		opt := func(label, href string, active bool) crumbOpt {
			return crumbOpt{label: label, href: href, count: -1, active: active}
		}
		step.menu = []crumbOpt{
			opt("Overview", "/techniques/"+url.PathEscape(id), here == "Overview"),
			opt("History", "/techniques/history/"+url.PathEscape(id), here == "History"),
		}
	}
	if here != "Overview" {
		step.href = "/techniques/" + url.PathEscape(id)
		return append(trail, step, crumb{label: here})
	}
	return append(trail, step)
}

func techniquesViewLabel(active string) string {
	switch active {
	case "map":
		return "Map"
	case "retired":
		return "Retired"
	case "tags":
		return "Tags"
	default:
		return "All"
	}
}
