// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package suggest

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
)

func seed(t *testing.T) (*store.Store, embed.Embedder) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	embedder, _ := embed.New("hashing-v1", 64)
	now := models.Now()
	technique := models.Technique{
		ID: "ask-for-a-diagram", Name: "Ask for a diagram you can keep",
		Description: "diagrams beat prose", Scope: "general", Status: "stable",
		Provenance: "curated", Version: 1, Recipe: "Make a clear, labelled diagram of the thing",
		Tags: []string{"visualization"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	vec := embedder.Embed([]string{embed.TechniqueText(technique)})[0]
	_ = st.SetTechniqueEmbedding(technique.ID, vec, embedder.ModelID(), embedder.Dim())
	events := []map[string]any{
		{"technique_id": "ask-for-a-diagram", "stage": "shown",
			"segment": map[string]any{"harness": "claude-code", "team": "revops"}, "task_type": "code-review"},
		{"technique_id": "ask-for-a-diagram", "stage": "adopted",
			"segment": map[string]any{"harness": "claude-code"}, "task_type": "code-review"},
		{"technique_id": "ask-for-a-diagram", "stage": "dismissed", "value": "not-relevant",
			"segment": map[string]any{"surface": "cli"}},
	}
	for _, body := range events {
		e, err := models.ParseFeedbackEvent(body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.InsertEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	return st, embedder
}

func TestProfileAggregatesUsage(t *testing.T) {
	st, _ := seed(t)
	p, err := BuildProfile(st, 90)
	if err != nil {
		t.Fatal(err)
	}
	if p.Events != 3 || p.Stages["adopted"] != 1 || p.TaskTypes["code-review"] != 2 {
		t.Fatalf("counts wrong: %+v", p)
	}
	if p.Harnesses["claude-code"] != 2 || p.Surfaces["cli"] != 1 || p.Teams["revops"] != 1 {
		t.Fatalf("segments wrong: %+v", p)
	}
	if p.AdoptedTags["visualization"] != 1 || p.DismissReasons["not-relevant"] != 1 {
		t.Fatalf("adoption detail wrong: %+v", p)
	}
	if len(p.AdoptedTechniques) != 1 || !strings.Contains(p.AdoptedTechniques[0], "Ask for a diagram") {
		t.Fatalf("adopted techniques: %v", p.AdoptedTechniques)
	}
	prompt := Prompt(p, 5)
	for _, want := range []string{"Ask for a diagram you can keep", "code-review", "exactly 5", "JSON array"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

type fakeResearcher struct{ drafts []Draft }

func (f fakeResearcher) Research(prompt string) ([]Draft, error) { return f.drafts, nil }

func TestRunLandsDedupedDrafts(t *testing.T) {
	st, embedder := seed(t)
	res := fakeResearcher{drafts: []Draft{
		{Name: "Pin your tool versions in agent instructions", Description: "d",
			Recipe: "State exact versions in CLAUDE.md", AppliesWhen: "a", NotWhen: "n",
			Tags: []string{"setup"}, SourceURL: "https://example.com/pin"},
		// exact rephrase of an existing technique -> must be dropped
		{Name: "Ask for a diagram you can keep", Description: "diagrams beat prose",
			Recipe: "Make a clear, labelled diagram of the thing", Tags: []string{"visualization"}},
		// unusable: no recipe
		{Name: "Empty move", Description: "d"},
		{Name: "Use checkpoints before risky refactors", Description: "d",
			Recipe: "Commit or stash before letting the agent refactor", SourceURL: "https://example.com/ckpt"},
	}}
	created, err := Run(st, embedder, res, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("created %d techniques (want 2: dup + empty dropped): %+v", len(created), created)
	}
	for _, c := range created {
		if c.Status != "draft" || c.Provenance != "suggested" || c.Scope != "general" {
			t.Fatalf("wrong draft shape: %+v", c)
		}
		if len(c.Embedding) == 0 {
			t.Fatal("suggested draft not embedded")
		}
	}
	if created[0].Source != "https://example.com/pin" {
		t.Fatalf("source url lost: %q", created[0].Source)
	}
	// drafts stay out of retrieval until promoted
	cands, _ := st.CandidateTechniques()
	for _, c := range cands {
		if c.Provenance == "suggested" {
			t.Fatal("suggested draft leaked into retrieval")
		}
	}
	// a second identical pass converges to zero
	again, err := Run(st, embedder, res, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("re-run created %d duplicates", len(again))
	}
}

func TestParseDraftsTolerant(t *testing.T) {
	text := "Here are the techniques:\n```json\n[{\"name\":\"X\",\"recipe\":\"r\",\"description\":\"d\"}]\n```"
	drafts, err := parseDrafts(text)
	if err != nil || len(drafts) != 1 || drafts[0].Name != "X" {
		t.Fatalf("parse: %v %+v", err, drafts)
	}
	if _, err := parseDrafts("no json here"); err == nil {
		t.Fatal("garbage accepted")
	}
	// web_search citation markup is scrubbed from every field
	cited := `[{"name":"N","recipe":"r","description":"<cite index=\"19-21,19-22\">Plan first.</cite> Rule: <cite index=\"19-43\">skip when trivial.</cite>"}]`
	drafts, err = parseDrafts(cited)
	if err != nil {
		t.Fatal(err)
	}
	if got := drafts[0].Description; got != "Plan first. Rule: skip when trivial." {
		t.Fatalf("cite tags survived: %q", got)
	}
}

func TestResearcherRetriesRateLimit(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("retry-after", "0")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"[{\"name\":\"X\",\"description\":\"d\",\"recipe\":\"r\"}]"}]}`))
	}))
	defer ts.Close()
	a := &Anthropic{Key: "k", Base: ts.URL, Model: "m", Version: "2023-06-01"}
	drafts, err := a.Research("p")
	if err != nil || len(drafts) != 1 || hits != 2 {
		t.Fatalf("retry path: err=%v drafts=%d hits=%d", err, len(drafts), hits)
	}
	// non-retryable errors surface immediately
	hits = 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(401)
	}))
	defer bad.Close()
	a.Base = bad.URL
	if _, err := a.Research("p"); err == nil || hits != 1 {
		t.Fatalf("401 retried or swallowed: err=%v hits=%d", err, hits)
	}
}

// The suggestion run is the dominant source of new techniques, so it is the dominant
// source of new TAGS. Left unconstrained the model coins fresh ones every pass
// and the vocabulary fills with near-synonyms that nothing ever merges back.
// The prompt must hand it the live vocabulary and tell it to reuse it.
func TestPromptCarriesTheTagVocabulary(t *testing.T) {
	p := Profile{Vocabulary: []string{"prompting", "context", "audit"}}
	got := Prompt(p, 5)
	for _, want := range []string{
		"TAG VOCABULARY",
		"prompting, context, audit",
		"Reuse an existing tag wherever one fits",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
	// An empty vocabulary must not render as an empty "reuse these" list —
	// that reads as "reuse nothing".
	empty := Prompt(Profile{}, 5)
	if !strings.Contains(empty, "none yet") {
		t.Fatal("empty vocabulary should say so, not show an empty list")
	}
}

// The vocabulary is what is LIVE: drafts are proposals (their tags are not yet
// the org's language) and retired techniques have left it. Ordered commonest-first so
// the model sees established language before the long tail.
func TestProfileVocabularyIsLiveAndRanked(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []models.Technique{
		{ID: "a", Name: "A", Status: "stable", Tags: []string{"prompting", "audit"}},
		{ID: "b", Name: "B", Status: "stable", Tags: []string{"prompting"}},
		{ID: "c", Name: "C", Status: "draft", Tags: []string{"draft-only"}},
		{ID: "d", Name: "D", Status: "retired", Tags: []string{"retired-only"}},
	} {
		if err := st.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}
	p, err2 := BuildProfile(st, 0)
	if err2 != nil {
		t.Fatal(err2)
	}
	want := []string{"prompting", "audit"} // prompting(2) outranks audit(1)
	if !reflect.DeepEqual(p.Vocabulary, want) {
		t.Fatalf("vocabulary = %q, want %q (live only, commonest first)", p.Vocabulary, want)
	}
}

// The Public channel decides whether a technique may leave this registry by reading
// its provenance: federation.HumanOrigin says a person wrote it, and anything
// machine-written needs an explicit approval event first. A suggested technique is
// machine-written, so the landing step must set the exact string "suggested" on
// the returned technique and on the stored one.
func TestSuggestedDraftsLandWithMachineProvenance(t *testing.T) {
	st, embedder := seed(t)
	res := fakeResearcher{drafts: []Draft{
		{Name: "Pin your tool versions in agent instructions", Description: "d",
			Recipe: "State exact versions in CLAUDE.md", AppliesWhen: "a", NotWhen: "n",
			Tags: []string{"setup"}, SourceURL: "https://example.com/pin"},
	}}
	created, err := Run(st, embedder, res, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Fatalf("created %d techniques, want 1", len(created))
	}
	stored, ok, err := st.GetTechnique(created[0].ID)
	if err != nil || !ok {
		t.Fatalf("suggested technique not stored: ok=%v err=%v", ok, err)
	}
	for _, c := range []models.Technique{created[0], stored} {
		if c.Provenance != "suggested" {
			t.Fatalf("provenance = %q, want %q", c.Provenance, "suggested")
		}
		if federation.HumanOrigin(c.Provenance) {
			t.Fatalf("provenance %q passes the human-origin gate; a machine wrote this technique", c.Provenance)
		}
	}
}
