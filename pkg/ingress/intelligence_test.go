// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opentacit/tacit/pkg/contracts"
	"github.com/opentacit/tacit/pkg/feed"
	intel "github.com/opentacit/tacit/pkg/intelligence"
)

const intelligenceContentHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func intelligenceFixture(t *testing.T, reporter int, originKey string) intel.Report {
	t.Helper()
	key, err := feed.LoadOrCreateKey(fmt.Sprintf("%s/reporter-%d", t.TempDir(), reporter))
	if err != nil {
		t.Fatal(err)
	}
	return intelligenceFixtureWithKey(reporter, originKey, key, "2026-08-25T12:00:00Z")
}

func intelligenceFixtureWithKey(reporter int, originKey string, key ed25519.PrivateKey, generatedAt string) intel.Report {
	r := intel.Report{Version: intel.Version, ReporterProviderID: fmt.Sprintf("https://reporter-%d.example", reporter),
		GeneratedAt: generatedAt, Imports: []intel.Import{{
			Origin: contracts.FederationOrigin{ProviderID: "https://origin.example", ProviderKey: originKey,
				EntryID: "https://origin.example/techniques/move", ChannelID: "general", ContentHash: intelligenceContentHash,
				ImportedAt: "2026-08-01T00:00:00Z"}, AcceptedAt: "2026-08-02T00:00:00Z",
			Outcome: &intel.Outcome{WindowStart: "2026-08-01T00:00:00Z", WindowEnd: "2026-08-25T12:00:00Z",
				Shown: 20, Adopted: 12, Helped: 10, Dismissed: 2, WeightedAdopted: 12,
				WeightedHelped: 10, WeightedDismissed: 2, SampleSize: 12},
		}}}
	intel.Sign(&r, key)
	return r
}

func TestIntelligenceStoreDeduplicatesAndAppliesCrossOrgFloor(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenIntelligenceStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := feed.LoadOrCreateKey(t.TempDir() + "/origin")
	originPublic := feed.PublicKeyString(origin.Public().(ed25519.PublicKey))
	for i := 1; i <= intel.CrossOrgMinRegistries; i++ {
		report := intelligenceFixture(t, i, originPublic)
		inserted, err := st.Accept(fmt.Sprintf("instance-%d", i), report)
		if err != nil || inserted != 1 {
			t.Fatalf("accept %d: inserted=%d err=%v", i, inserted, err)
		}
		if i == 1 {
			inserted, err = st.Accept("instance-1", report)
			if err != nil || inserted != 0 {
				t.Fatalf("replay: inserted=%d err=%v", inserted, err)
			}
		}
		below, available := st.Aggregate("https://origin.example", originPublic, "https://origin.example/techniques/move", intelligenceContentHash, 0)
		if available != (i == intel.CrossOrgMinRegistries) {
			t.Fatalf("after %d reports availability=%v", i, available)
		}
		if !available && (below.SampleSize != 0 || below.Helped != 0 || below.ReportingRegistries != 0) {
			t.Fatalf("below-floor outcomes leaked: %+v", below)
		}
	}
	agg, available := st.Aggregate("https://origin.example", originPublic, "https://origin.example/techniques/move", intelligenceContentHash, 0)
	if !available || agg.ImportingRegistries != intel.CrossOrgMinRegistries || agg.SampleSize != 12*intel.CrossOrgMinRegistries {
		t.Fatalf("aggregate: %+v available=%v", agg, available)
	}
	reopened, err := OpenIntelligenceStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reopened.Aggregate("https://origin.example", originPublic, "https://origin.example/techniques/move", intelligenceContentHash, 0); !ok || got.SampleSize != agg.SampleSize {
		t.Fatalf("persisted aggregate: %+v available=%v", got, ok)
	}
	raw, _ := os.ReadFile(dir + "/intelligence.json")
	for _, forbidden := range []string{"audit_id", "session_hash", "segment", "summary_text"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("private field persisted: %s", forbidden)
		}
	}
}

func TestIntelligenceStorePinsReporterAndRejectsSelfReport(t *testing.T) {
	st, _ := OpenIntelligenceStore(t.TempDir())
	origin, _ := feed.LoadOrCreateKey(t.TempDir() + "/origin")
	originPublic := feed.PublicKeyString(origin.Public().(ed25519.PublicKey))
	report := intelligenceFixture(t, 1, originPublic)
	if _, err := st.Accept("instance", report); err != nil {
		t.Fatal(err)
	}
	changed := intelligenceFixture(t, 2, originPublic)
	if _, err := st.Accept("instance", changed); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("changed reporter accepted: %v", err)
	}
	self := intelligenceFixture(t, 3, originPublic)
	self.ReporterProviderID = self.Imports[0].Origin.ProviderID
	key, _ := feed.LoadOrCreateKey(t.TempDir() + "/self")
	intel.Sign(&self, key)
	if _, err := st.Accept("self", self); err == nil || !strings.Contains(err.Error(), "own technique") {
		t.Fatalf("self report accepted: %v", err)
	}
}

func TestIntelligenceSnapshotIsThresholdedAndReplacesReporterSnapshots(t *testing.T) {
	st, _ := OpenIntelligenceStore(t.TempDir())
	origin, _ := feed.LoadOrCreateKey(t.TempDir() + "/origin")
	originPublic := feed.PublicKeyString(origin.Public().(ed25519.PublicKey))
	reporterKeys := make([]ed25519.PrivateKey, intel.CrossOrgMinRegistries)
	for i := range reporterKeys {
		reporterKeys[i], _ = feed.LoadOrCreateKey(fmt.Sprintf("%s/reporter-%d", t.TempDir(), i+1))
		report := intelligenceFixtureWithKey(i+1, originPublic, reporterKeys[i], "2026-08-25T12:00:00Z")
		if _, err := st.Accept(fmt.Sprintf("instance-%d", i+1), report); err != nil {
			t.Fatal(err)
		}
		if i == intel.CrossOrgMinRegistries-2 {
			below := st.Snapshot()
			if len(below.Versions) != 1 || below.Versions[0].Available || below.Versions[0].Adopted != 0 ||
				below.Versions[0].OutcomeRegistries != 0 {
				t.Fatalf("below-floor snapshot leaked outcomes: %+v", below.Versions)
			}
			raw, _ := json.Marshal(below)
			for _, private := range []string{"instance-1", "reporter-1"} {
				if strings.Contains(string(raw), private) {
					t.Fatalf("operator snapshot exposed reporter identity %q: %s", private, raw)
				}
			}
		}
	}

	ready := st.Snapshot()
	if ready.SharingRegistries != intel.CrossOrgMinRegistries || ready.ImportingRegistries != intel.CrossOrgMinRegistries ||
		ready.ReadyVersions != 1 || ready.Adopted != 12*intel.CrossOrgMinRegistries || ready.Helped != 10*intel.CrossOrgMinRegistries {
		t.Fatalf("ready snapshot: %+v", ready)
	}
	if len(ready.Publishers) != 1 || ready.Publishers[0].ReadyVersions != 1 ||
		ready.Publishers[0].ImportingRegistries != intel.CrossOrgMinRegistries {
		t.Fatalf("publisher fold: %+v", ready.Publishers)
	}

	// A report is a full snapshot. When one registry no longer imports the
	// technique, its next report removes the old receipt and the aggregate falls
	// back below the privacy floor.
	empty := intel.Report{Version: intel.Version, ReporterProviderID: "https://reporter-1.example",
		GeneratedAt: "2026-08-25T13:00:00Z", Imports: []intel.Import{}}
	intel.Sign(&empty, reporterKeys[0])
	if inserted, err := st.Accept("instance-1", empty); err != nil || inserted != 0 {
		t.Fatalf("replace with empty snapshot: inserted=%d err=%v", inserted, err)
	}
	after := st.Snapshot()
	if after.SharingRegistries != intel.CrossOrgMinRegistries || after.ImportingRegistries != intel.CrossOrgMinRegistries-1 ||
		len(after.Versions) != 1 || after.Versions[0].ImportingRegistries != intel.CrossOrgMinRegistries-1 ||
		after.Versions[0].Available || after.Adopted != 0 {
		t.Fatalf("removed receipt remained in operator snapshot: %+v", after)
	}
}

func TestRegistryReturnsIntelligenceOnAuthenticatedTunnel(t *testing.T) {
	h := newHarness(t)
	origin, _ := feed.LoadOrCreateKey(t.TempDir() + "/origin")
	originPublic := feed.PublicKeyString(origin.Public().(ed25519.PublicKey))
	report := intelligenceFixture(t, 1, originPublic)
	c := &Client{Addr: h.tunnelLn.Addr().String(), Token: NewKey(),
		Intelligence: func() *intel.Report { copy := report; return &copy }}
	c.SetHandler(http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	var once sync.Once
	c.OnWelcome = func(w Welcome) { once.Do(func() { ready <- w.Instance }) }
	go func() { _ = c.Run(ctx) }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("registry never connected")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := h.srv.Intel.Aggregate("https://origin.example", originPublic,
			"https://origin.example/techniques/move", intelligenceContentHash, 1); ok && got.SampleSize == 12 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("signed intelligence report did not cross the tunnel")
}
