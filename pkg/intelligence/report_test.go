// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package intelligence

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opentacit/tacit/pkg/contracts"
	"github.com/opentacit/tacit/pkg/feed"
	"github.com/opentacit/tacit/pkg/jsonschema"
	"github.com/opentacit/tacit/schemas"
)

const testContentHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func signedFixture(t *testing.T) Report {
	t.Helper()
	key, err := feed.LoadOrCreateKey(t.TempDir() + "/key")
	if err != nil {
		t.Fatal(err)
	}
	originKey, err := feed.LoadOrCreateKey(t.TempDir() + "/origin-key")
	if err != nil {
		t.Fatal(err)
	}
	r := Report{Version: Version, ReporterProviderID: "https://reporter.example", GeneratedAt: "2026-08-25T12:00:00Z",
		Imports: []Import{{Origin: contracts.FederationOrigin{
			ProviderID: "https://origin.example", ProviderKey: feed.PublicKeyString(originKey.Public().(ed25519.PublicKey)),
			EntryID: "https://origin.example/techniques/move", ChannelID: "general", ContentHash: testContentHash,
			ImportedAt: "2026-08-01T00:00:00Z"}, AcceptedAt: "2026-08-02T00:00:00Z",
			Outcome: &Outcome{WindowStart: "2026-08-01T00:00:00Z", WindowEnd: "2026-08-25T12:00:00Z", SampleSize: MinOutcomeSample}}}}
	Sign(&r, key)
	return r
}

func TestReportSignatureCoversPayload(t *testing.T) {
	r := signedFixture(t)
	if err := Verify(r); err != nil {
		t.Fatal(err)
	}
	r.Imports[0].Outcome.Helped++
	if err := Verify(r); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("changed report verified: %v", err)
	}
}

func TestReportMatchesSchema(t *testing.T) {
	r := signedFixture(t)
	raw, _ := json.Marshal(r)
	schemaRaw, ok := schemas.Get("intelligence-report.schema.json")
	if !ok {
		t.Fatal("intelligence report schema is not embedded")
	}
	schema, err := jsonschema.Parse(schemaRaw)
	if err != nil {
		t.Fatal(err)
	}
	if errs := schema.ValidateBytes(raw); len(errs) != 0 {
		t.Fatalf("report violates schema: %v\n%s", errs, raw)
	}
}

func TestReportRejectsThinOutcome(t *testing.T) {
	r := signedFixture(t)
	r.Imports[0].Outcome.SampleSize = MinOutcomeSample - 1
	key, _ := feed.LoadOrCreateKey(t.TempDir() + "/key")
	Sign(&r, key)
	if err := Verify(r); err == nil || !strings.Contains(err.Error(), "privacy floor") {
		t.Fatalf("thin outcome accepted: %v", err)
	}
}
