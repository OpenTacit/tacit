// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
	"github.com/opentacit/tacit/pkg/feed"
	"github.com/opentacit/tacit/pkg/intelligence"
)

func TestIntelligenceReportSendsReceiptThenThresholdedOutcome(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, _ := feed.LoadOrCreateKey(t.TempDir() + "/reporter-key")
	originKey, _ := feed.LoadOrCreateKey(t.TempDir() + "/origin-key")
	importedAt := "2026-08-01T00:00:00Z"
	technique := models.Technique{ID: "ext/provider/move", Name: "Move", Description: "D", Scope: "general",
		Status: "stable", Provenance: "federated", Version: 1, Recipe: "R", CreatedAt: importedAt, UpdatedAt: "2026-08-02T00:00:00Z",
		Origin: &models.FederationOrigin{ProviderID: "https://origin.example", ProviderKey: feed.PublicKeyString(originKey.Public().(ed25519.PublicKey)),
			EntryID: "https://origin.example/techniques/move", ChannelID: "general",
			ContentHash: "sha256:0000000000000000000000000000000000000000000000000000000000000000", ImportedAt: importedAt}}
	if err := st.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	report, err := IntelligenceReport(st, "https://reporter.example", key, now)
	if err != nil || len(report.Imports) != 1 || report.Imports[0].Outcome != nil {
		t.Fatalf("receipt report: %+v err=%v", report, err)
	}

	if err := st.ReplaceOutcomes([]models.Outcome{{TechniqueID: technique.ID, SegmentKey: "__overall__",
		Shown: 21, Adopted: 12, Helped: 10, Dismissed: 2, WeightedAdopted: 10.125,
		WeightedHelped: 8.666, WeightedDismissed: 1.5, SampleSize: intelligence.MinOutcomeSample,
		LastUpdated: models.Now()}}); err != nil {
		t.Fatal(err)
	}
	report, err = IntelligenceReport(st, "https://reporter.example", key, now)
	if err != nil || report.Imports[0].Outcome == nil {
		t.Fatalf("outcome report: %+v err=%v", report, err)
	}
	if got := report.Imports[0].Outcome.WeightedHelped; got != 8.67 {
		t.Fatalf("weighted helped = %v, want rounded 8.67", got)
	}
	if err := intelligence.Verify(*report); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(report)
	for _, forbidden := range []string{"audit-secret", "session-secret", "audit_id", "session_hash", "segment"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("private field crossed report wire: %s", forbidden)
		}
	}
}
