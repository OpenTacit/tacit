// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package feed

import (
	"crypto/ed25519"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/opentacit/tacit/pkg/contracts"
	"github.com/opentacit/tacit/pkg/jsonschema"
	"github.com/opentacit/tacit/schemas"
)

func load(t *testing.T, name string) *jsonschema.Schema {
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

func TestFeedDocumentRoundTrip(t *testing.T) {
	key, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "k"))
	technique := contracts.Technique{
		ID: "use-internal-data-connector", Name: "Query the warehouse",
		Description: "d", Scope: "org", Status: "stable", Provenance: "curated",
		Version: 1, Recipe: "@warehouse query",
		CreatedAt: "2026-07-06T00:00:00Z", UpdatedAt: "2026-07-06T00:00:00Z",
	}
	entry := Entry{
		ID: EntryID("https://techniques.example.org", technique.ID), Kind: KindTechnique,
		Updated: "2026-07-06T10:00:00Z", Title: technique.Name,
		ContentURL:  "https://techniques.example.org/f/techniques/use-internal-data-connector.md",
		ContentHash: TechniqueHash(technique), Technique: &technique,
		Attestation: &Attestation{
			Outcomes:    AttestedOutcomes{HelpedRate: 0.91, SampleSize: 120, WindowDays: 180},
			Granularity: "org",
		},
	}
	Sign(&entry, key)
	retraction := Entry{
		ID: "https://techniques.example.org/techniques/old-move", Kind: KindRetraction,
		Updated: "2026-07-01T08:00:00Z", Reason: "decayed",
	}
	Sign(&retraction, key)

	doc := Feed{
		Version: Version,
		Channel: ChannelMeta{ID: "general", Provider: "https://techniques.example.org", Title: "T"},
		Updated: "2026-07-06T10:00:00Z",
		Entries: []Entry{entry, retraction},
	}
	raw, _ := json.Marshal(doc)
	if errs := load(t, "feed.schema.json").ValidateBytes(raw); len(errs) != 0 {
		t.Fatalf("feed violates schema: %v\n%s", errs, raw)
	}

	// the embedded technique must also satisfy the full technique schema
	techniqueRaw, _ := json.Marshal(technique)
	if errs := load(t, "technique.schema.json").ValidateBytes(techniqueRaw); len(errs) != 0 {
		t.Fatalf("embedded technique violates technique schema: %v", errs)
	}
}

func TestDescriptorRoundTrip(t *testing.T) {
	key, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "k"))
	d := Descriptor{
		Federation: DescriptorVersion,
		Provider:   ProviderInfo{ID: "https://techniques.example.org", Name: "Example Org"},
		PublicKey:  PublicKeyString(key.Public().(ed25519.PublicKey)),
		Channels: []ChannelInfo{{ID: "general", Title: "Engineering techniques",
			FeedURL: "https://techniques.example.org/f/general/feed.json"}},
	}
	raw, _ := json.Marshal(d)
	if errs := load(t, "provider-descriptor.schema.json").ValidateBytes(raw); len(errs) != 0 {
		t.Fatalf("descriptor violates schema: %v\n%s", errs, raw)
	}
}
