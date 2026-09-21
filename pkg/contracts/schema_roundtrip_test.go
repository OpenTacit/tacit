// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package contracts_test

// The round-trip enforcement from docs/design/api-standard.md: every wire type
// marshals a representative fixture and must validate against its normative
// JSON Schema, and the real seed techniques must validate too. Spec drift — in
// either direction — is a test failure, not a discovery by a third party.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/techniques"
	"github.com/opentacit/tacit/pkg/contracts"
	"github.com/opentacit/tacit/pkg/jsonschema"
	"github.com/opentacit/tacit/schemas"
)

func schema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	raw, ok := schemas.Get(name)
	if !ok {
		t.Fatalf("schema %s not embedded", name)
	}
	s, err := jsonschema.Parse(raw)
	if err != nil {
		t.Fatalf("schema %s: %v", name, err)
	}
	return s
}

func mustValidate(t *testing.T, s *jsonschema.Schema, v any, label string) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal: %v", label, err)
	}
	if errs := s.ValidateBytes(raw); len(errs) != 0 {
		t.Fatalf("%s violates its schema:\n%v\ninstance: %s", label, errs, raw)
	}
}

func rate(v float64) *float64 { return &v }

func TestTechniqueFixtureRoundTrip(t *testing.T) {
	s := schema(t, "technique.schema.json")
	mustValidate(t, s, contracts.Technique{
		ID: "use-internal-data-connector", Name: "Query the warehouse",
		Description: "Live queries beat pasted rows.", Scope: "org", Status: "stable",
		Provenance: "curated", Version: 3, Recipe: "@warehouse query <table>",
		Tags: []string{"data"}, TaskTypes: []string{"analysis"},
		Triggers:    []any{"pasted csv", map[string]any{"heuristic": "tabular_paste"}},
		AppliesWhen: "pasted tabular data", NotWhen: "ad-hoc data",
		Channels: []string{"general"}, Shipped: "2026-05",
		Origin: &contracts.FederationOrigin{ProviderID: "https://provider.example", ProviderKey: "ed25519:key",
			EntryID: "https://provider.example/techniques/query", ChannelID: "general",
			ContentHash: "sha256:0000000000000000000000000000000000000000000000000000000000000000", ImportedAt: "2026-07-06T00:30:00Z"},
		Embedding: []float32{0.1, -0.2}, EmbeddingModel: "hashing-v1", EmbeddingDim: 2,
		Source:    "techniques/use-internal-data-connector.md",
		CreatedAt: "2026-07-06T00:00:00Z", UpdatedAt: "2026-07-06T01:00:00Z",
	}, "full technique fixture")

	// minimal technique: only the always-emitted fields
	mustValidate(t, s, contracts.Technique{
		ID: "x1", Name: "n", Scope: "general", Status: "draft", Provenance: "contributed",
		Version: 1, CreatedAt: "2026-07-06T00:00:00Z", UpdatedAt: "2026-07-06T00:00:00Z",
	}, "minimal technique fixture")
}

func TestSeedTechniquesSatisfyTheSchema(t *testing.T) {
	s := schema(t, "technique.schema.json")
	dir := filepath.Join("..", "..", "techniques")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("seed techniques missing: %v", err)
	}
	n := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		technique, err := techniques.ParseFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		mustValidate(t, s, technique, "seed technique "+e.Name())
		n++
	}
	if n == 0 {
		t.Fatal("no seed techniques validated")
	}
}

func TestFeedbackEventRoundTrip(t *testing.T) {
	s := schema(t, "feedback-event.schema.json")
	mustValidate(t, s, contracts.FeedbackEvent{
		EventID: "evt_1", AuditID: "aud_1", TechniqueID: "c", TechniqueVersion: 2,
		Stage: "dismissed", Value: "not-relevant",
		Segment: contracts.Segment{"team": "revops"}, TaskType: "analysis",
		RankShown: 1, Confidence: "explicit", CreatedAt: "2026-07-06T00:00:00Z",
	}, "dismissed event")
	mustValidate(t, s, contracts.FeedbackEvent{
		EventID: "evt_2", AuditID: "aud_1", TechniqueID: "c", Stage: "helped",
		Value: true, Segment: contracts.Segment{}, Confidence: "inferred",
		CreatedAt: "2026-07-06T00:00:00Z",
	}, "helped event")
}

func TestCharacterizationAndEvidenceRoundTrip(t *testing.T) {
	cs := schema(t, "characterization.schema.json")
	mustValidate(t, cs, contracts.Characterization{
		AuditID:     "aud_1",
		SessionHash: "0123456789abcdef0123456789abcdef",
		Model:       "claude-haiku-4-5",
		SummaryText: "pasted CSV rows", TaskType: "analysis",
		Modalities: []string{"text"}, ToolsUsed: []string{"Bash"},
		Segment: contracts.Segment{"team": "revops"},
	}, "characterization")

	// audit_id is optional: a producer that predates it still validates, and
	// the registry just records no audit fact for that audit.
	mustValidate(t, cs, contracts.Characterization{
		SummaryText: "pasted CSV rows",
	}, "characterization without audit_id")

	// The enrichment: the inferred half of an audit fact, posted after the turn.
	es := schema(t, "audit-fact-enrichment.schema.json")
	mustValidate(t, es, contracts.AuditFactEnrichment{
		AuditID: "aud_1", ToolsAbsent: []string{"warehouse-connector"},
	}, "audit fact enrichment")
	// "nothing was missed" is a correct and common answer, and must round-trip.
	mustValidate(t, es, contracts.AuditFactEnrichment{AuditID: "aud_1"},
		"audit fact enrichment with nothing missed")

	s := schema(t, "evidence-block.schema.json")
	mustValidate(t, s, contracts.EvidenceBlock{
		Candidates: []contracts.EvidenceCandidate{{
			TechniqueID: "c", Name: "N", Scope: "org", Recipe: "r",
			AppliesWhen: "w",
			Outcomes: &contracts.OutcomeSummary{
				HelpedRate: rate(0.94), AdoptionRate: rate(0.7), SampleSize: 120, Segment: "team:revops"},
			Freshness: contracts.Freshness{Shipped: "2026-05", RecentlyShipped: true},
		}},
		Cohort: contracts.Cohort{Segment: "team:revops", Uses: 12, CommonTotal: 40,
			Examples: []string{"c"}},
		Meta: contracts.EvidenceMeta{Thin: false},
	}, "measured evidence block")

	// cold start: nil outcomes, nil examples — the wire allows both
	mustValidate(t, s, contracts.EvidenceBlock{
		Candidates: []contracts.EvidenceCandidate{{TechniqueID: "c", Name: "N",
			Scope: "general", Recipe: "r", Outcomes: nil}},
		Cohort: contracts.Cohort{Segment: "__overall__"},
		Meta:   contracts.EvidenceMeta{Thin: true},
	}, "cold-start evidence block")

	// shadow candidates: judge-only techniques, never surfaced (validation-without-review)
	mustValidate(t, s, contracts.EvidenceBlock{
		Candidates: []contracts.EvidenceCandidate{},
		ShadowCandidates: []contracts.EvidenceCandidate{{TechniqueID: "sh", Name: "S",
			Scope: "general", Recipe: "r", Outcomes: nil}},
		Cohort: contracts.Cohort{Segment: "__overall__"},
		Meta:   contracts.EvidenceMeta{Thin: true},
	}, "evidence block with shadow candidates")
}

func TestCanonicalRecordAndSketchRoundTrip(t *testing.T) {
	tokens := 1200
	cost := 0.42
	mustValidate(t, schema(t, "canonical-record.schema.json"), contracts.CanonicalRecord{
		SchemaVersion: 1, Source: "claude-code", SessionID: "s", Harness: "claude-code",
		Messages:  []contracts.Message{{Role: "user", Text: "hi", Modalities: []string{"text"}}},
		ToolCalls: []contracts.ToolCall{{Name: "Bash", Arguments: "ls"}},
		CostUSD:   &cost, Tokens: &tokens, Segment: contracts.Segment{"team": "revops"},
	}, "canonical record")

	mustValidate(t, schema(t, "sketch.schema.json"), contracts.Sketch{
		SketchID: "sk_1", SessionHash: "a3f4b2c1d0e9f8a7",
		Trigger: "member pasted warehouse rows", Move: "query the connector instead",
		Tools: []string{"mcp__warehouse__query"}, TaskType: "analysis",
		Harness: "claude-code", Segment: contracts.Segment{"team": "revops"},
		TechniqueID: "use-internal-data-connector", CreatedAt: "2026-07-06T00:00:00Z",
	}, "sketch")
}

func TestSchemasThemselvesStayInTheValidatorSubset(t *testing.T) {
	// every embedded schema must parse, and every $ref must resolve
	for _, name := range schemas.Names() {
		raw, _ := schemas.Get(name)
		s, err := jsonschema.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(raw), `"$ref": "./`) || strings.Contains(string(raw), `"$ref": "http`) {
			t.Fatalf("%s uses a non-local $ref (validator subset is local-only)", name)
		}
		_ = s
	}
	if len(schemas.Names()) < 7 {
		t.Fatalf("expected the full schema set, got %v", schemas.Names())
	}
}
