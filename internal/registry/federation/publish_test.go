// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
	"github.com/opentacit/tacit/internal/registry/techniques"
	"github.com/opentacit/tacit/pkg/feed"
	"github.com/opentacit/tacit/pkg/jsonschema"
	"github.com/opentacit/tacit/schemas"
)

func newPublisher(t *testing.T) (*Publisher, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, err := feed.LoadOrCreateKey(filepath.Join(t.TempDir(), "feed_key"))
	if err != nil {
		t.Fatal(err)
	}
	return &Publisher{
		Store: st, Key: key,
		ProviderID: "https://techniques.example.org", ProviderName: "Example Org",
		BaseURL: "https://techniques.example.org",
	}, st
}

func seed(t *testing.T, st *store.Store, id string, channels []string, status string) models.Technique {
	t.Helper()
	c := models.Technique{
		ID: id, Name: "Name " + id, Description: "d", Scope: "org", Status: status,
		Provenance: "curated", Version: 1, Recipe: "recipe for " + id,
		Channels:  channels,
		CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}
	if err := st.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDescriptorListsChannels(t *testing.T) {
	p, st := newPublisher(t)
	seed(t, st, "a", []string{"general"}, "stable")
	seed(t, st, "b", []string{"general", "data-tools"}, "stable")
	seed(t, st, "private", nil, "stable")

	d, err := p.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Channels) != 2 || d.Channels[0].ID != "data-tools" || d.Channels[1].ID != "general" {
		t.Fatalf("channels: %+v", d.Channels)
	}
	if !strings.HasPrefix(d.PublicKey, "ed25519:") {
		t.Fatalf("public key: %s", d.PublicKey)
	}
	raw, _ := json.Marshal(d)
	if errs := loadSchema(t, "provider-descriptor.schema.json").ValidateBytes(raw); len(errs) != 0 {
		t.Fatalf("descriptor schema: %v", errs)
	}
}

// A published technique that is pulled back to drafts must stop being served to
// subscribers: it federates as a retraction, like a retired one, not a live technique.
func TestDraftPublishedTechniqueRetracts(t *testing.T) {
	p, st := newPublisher(t)
	seed(t, st, "pulled-technique", []string{"general"}, "draft")
	doc, err := p.Feed("general", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Entries) != 1 {
		t.Fatalf("entries: %d", len(doc.Entries))
	}
	if doc.Entries[0].Kind != feed.KindRetraction {
		t.Fatalf("a technique returned to draft must federate as a retraction, got %q", doc.Entries[0].Kind)
	}
}

func TestFeedEntriesSignedAndVerified(t *testing.T) {
	p, st := newPublisher(t)
	seed(t, st, "live-technique", []string{"general"}, "stable")
	seed(t, st, "dead-technique", []string{"general"}, "retired")
	seed(t, st, "unpublished", nil, "stable")

	doc, err := p.Feed("general", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Entries) != 2 {
		t.Fatalf("entries: %d", len(doc.Entries))
	}
	d, _ := p.Descriptor()
	kinds := map[string]string{}
	for _, e := range doc.Entries {
		if err := feed.Verify(e, d.PublicKey); err != nil {
			t.Fatalf("entry %s: %v", e.ID, err)
		}
		kinds[e.ID] = e.Kind
	}
	if kinds["https://techniques.example.org/techniques/live-technique"] != feed.KindTechnique {
		t.Fatalf("live technique kind: %v", kinds)
	}
	if kinds["https://techniques.example.org/techniques/dead-technique"] != feed.KindRetraction {
		t.Fatalf("retired technique must federate as a retraction: %v", kinds)
	}
	for _, e := range doc.Entries {
		if e.Technique != nil && len(e.Technique.Embedding) != 0 {
			t.Fatal("embedding leaked into the feed")
		}
	}
	raw, _ := json.Marshal(doc)
	if errs := loadSchema(t, "feed.schema.json").ValidateBytes(raw); len(errs) != 0 {
		t.Fatalf("feed schema: %v", errs)
	}
}

func TestAttestationRequiresTheKThreshold(t *testing.T) {
	p, st := newPublisher(t)
	seed(t, st, "measured", []string{"general"}, "stable")
	rate := 0.9137
	thin := 0.5
	if err := st.ReplaceOutcomes([]models.Outcome{
		{TechniqueID: "measured", SegmentKey: "__overall__", Adopted: 23, Helped: 21,
			HelpedRate: &rate, SampleSize: 23, LastUpdated: models.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	doc, _ := p.Feed("general", "")
	if doc.Entries[0].Attestation == nil {
		t.Fatal("measured technique missing attestation")
	}
	if got := doc.Entries[0].Attestation.Outcomes.HelpedRate; got != 0.91 {
		t.Fatalf("rate not rounded: %v", got)
	}

	// below threshold: no attestation at all
	if err := st.ReplaceOutcomes([]models.Outcome{
		{TechniqueID: "measured", SegmentKey: "__overall__", Adopted: 2, Helped: 1,
			HelpedRate: &thin, SampleSize: 2, LastUpdated: models.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	doc, _ = p.Feed("general", "")
	if doc.Entries[0].Attestation != nil {
		t.Fatal("thin evidence attested (k-threshold violated)")
	}
}

func TestCanonicalTechniqueRoundTripsThroughTheParser(t *testing.T) {
	p, st := newPublisher(t)
	c := seed(t, st, "round-trip", []string{"general"}, "stable")
	c.Description = "multi: line, with [chars] and \"quotes\""
	c.Recipe = "line one\nline two"
	c.Tags = []string{"data", "warehouse"}
	c.AppliesWhen = "tabular data pasted by hand"
	if err := st.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}

	md, ok, err := p.CanonicalTechnique("round-trip")
	if err != nil || !ok {
		t.Fatalf("canonical: %v %v", ok, err)
	}
	path := filepath.Join(t.TempDir(), "round-trip.md")
	if err := writeFile(path, md); err != nil {
		t.Fatal(err)
	}
	parsed, err := techniques.ParseFile(path)
	if err != nil {
		t.Fatalf("rendered technique does not re-parse: %v\n%s", err, md)
	}
	// literal block scalars may add a trailing newline; that is the only
	// tolerated difference (the feed's embedded technique JSON, not the .md, is
	// what subscribers import)
	trim := strings.TrimRight
	if parsed.ID != c.ID || trim(parsed.Recipe, "\n") != trim(c.Recipe, "\n") ||
		trim(parsed.Description, "\n") != trim(c.Description, "\n") ||
		len(parsed.Tags) != 2 || trim(parsed.AppliesWhen, "\n") != trim(c.AppliesWhen, "\n") {
		t.Fatalf("round-trip drift:\nwant %+v\ngot  %+v", c, parsed)
	}

	if _, ok, _ := p.CanonicalTechnique("unpublished-id"); ok {
		t.Fatal("unpublished technique served")
	}
}

func TestExportWritesTheStaticSurface(t *testing.T) {
	p, st := newPublisher(t)
	seed(t, st, "a", []string{"general"}, "stable")
	seed(t, st, "b", []string{"general"}, "stable")
	out := t.TempDir()
	n, err := p.Export("general", out)
	if err != nil || n != 2 {
		t.Fatalf("export: %d %v", n, err)
	}
	for _, f := range []string{
		".well-known/tacit.json", "f/general/feed.json", "f/techniques/a.md", "f/techniques/b.md",
	} {
		if _, err := osStat(filepath.Join(out, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	if _, err := p.Export("nope", t.TempDir()); err == nil {
		t.Fatal("export of empty channel succeeded")
	}
}

func TestExportRefusesTraversalIDs(t *testing.T) {
	for id, ok := range map[string]bool{
		"good-id": true, "": false, "a/b": false, `a\b`: false, "..": false, "a..b": false,
	} {
		if got := safeExportID(id); got != ok {
			t.Errorf("safeExportID(%q) = %v, want %v", id, got, ok)
		}
	}

	p, st := newPublisher(t)
	seed(t, st, "../../evil", []string{"general"}, "stable")
	out := t.TempDir()
	if _, err := p.Export("general", out); err == nil {
		t.Fatal("export accepted a technique id that escapes the output directory")
	}
	if _, err := osStat(filepath.Join(filepath.Dir(out), "evil.md")); err == nil {
		t.Fatal("traversal id wrote outside the export tree")
	}
}

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }

func osStat(path string) (os.FileInfo, error) { return os.Stat(path) }

func loadSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	raw, ok := schemas.Get(name)
	if !ok {
		t.Fatalf("schema %s missing", name)
	}
	s, err := jsonschema.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
