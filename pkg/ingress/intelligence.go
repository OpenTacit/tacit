// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
	intel "github.com/opentacit/tacit/pkg/intelligence"
)

// ReporterIdentity binds a signed report identity to the registry identity that
// authenticated the tunnel. Both values pin on the first accepted report.
type ReporterIdentity struct {
	ProviderID  string `json:"provider_id"`
	PublicKey   string `json:"public_key"`
	GeneratedAt string `json:"generated_at,omitempty"`
	ReceivedAt  string `json:"received_at,omitempty"`
}

type storedImport struct {
	Reporter    string       `json:"reporter"`
	ReceivedAt  string       `json:"received_at"`
	GeneratedAt string       `json:"generated_at"`
	Import      intel.Import `json:"import"`
}

type intelligenceData struct {
	Reporters map[string]ReporterIdentity `json:"reporters"`
	Imports   map[string]storedImport     `json:"imports"`
}

// IntelligenceStore keeps the latest signed snapshot per reporter and
// upstream content version. Replays replace nothing and cannot inflate sums.
type IntelligenceStore struct {
	mu   sync.RWMutex
	path string
	data intelligenceData
	now  func() time.Time
}

func OpenIntelligenceStore(dir string) (*IntelligenceStore, error) {
	s := &IntelligenceStore{path: filepath.Join(dir, "intelligence.json"), data: intelligenceData{
		Reporters: map[string]ReporterIdentity{}, Imports: map[string]storedImport{},
	}}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path, err)
	}
	if s.data.Reporters == nil {
		s.data.Reporters = map[string]ReporterIdentity{}
	}
	if s.data.Imports == nil {
		s.data.Imports = map[string]storedImport{}
	}
	return s, nil
}

// Accept verifies and stores one report from an authenticated tunnel. It
// returns how many newer import snapshots landed.
func (s *IntelligenceStore) Accept(reporter string, report intel.Report) (int, error) {
	if err := intel.Verify(report); err != nil {
		return 0, err
	}
	for _, item := range report.Imports {
		if report.ReporterProviderID == item.Origin.ProviderID {
			return 0, errors.New("a provider cannot report outcomes for its own technique")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	generated, _ := time.Parse(time.RFC3339Nano, report.GeneratedAt)
	oldIdentity, hadIdentity := s.data.Reporters[reporter]
	if hadIdentity {
		if oldIdentity.ProviderID != report.ReporterProviderID || oldIdentity.PublicKey != report.ReporterPublicKey {
			return 0, errors.New("reporter provider identity changed")
		}
		if oldGenerated, err := time.Parse(time.RFC3339Nano, oldIdentity.GeneratedAt); err == nil && !generated.After(oldGenerated) {
			return 0, nil
		}
	}

	receivedAt := s.clock().Format(time.RFC3339Nano)
	s.data.Reporters[reporter] = ReporterIdentity{ProviderID: report.ReporterProviderID,
		PublicKey: report.ReporterPublicKey, GeneratedAt: report.GeneratedAt, ReceivedAt: receivedAt}
	previous := map[string]storedImport{}
	for key, item := range s.data.Imports {
		if item.Reporter == reporter {
			previous[key] = item
			delete(s.data.Imports, key)
		}
	}
	for _, item := range report.Imports {
		key := importKey(reporter, item)
		s.data.Imports[key] = storedImport{Reporter: reporter, ReceivedAt: receivedAt,
			GeneratedAt: report.GeneratedAt, Import: item}
	}
	if err := s.save(); err != nil {
		if hadIdentity {
			s.data.Reporters[reporter] = oldIdentity
		} else {
			delete(s.data.Reporters, reporter)
		}
		for key, item := range s.data.Imports {
			if item.Reporter == reporter {
				delete(s.data.Imports, key)
			}
		}
		for key, item := range previous {
			s.data.Imports[key] = item
		}
		return 0, err
	}
	return len(report.Imports), nil
}

func (s *IntelligenceStore) clock() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *IntelligenceStore) save() error {
	return fsx.WriteJSONAtomic(s.path, s.data, 0o600)
}

func importKey(reporter string, item intel.Import) string {
	o := item.Origin
	return reporter + "\x00" + o.ProviderID + "\x00" + o.ProviderKey + "\x00" + o.EntryID + "\x00" + o.ContentHash
}

// IntelligenceAggregate is safe to return only when Available is true.
type IntelligenceAggregate struct {
	ImportingRegistries int
	ReportingRegistries int
	Shown               int
	Adopted             int
	Helped              int
	Dismissed           int
	WeightedAdopted     float64
	WeightedHelped      float64
	WeightedDismissed   float64
	SampleSize          int
}

// IntelligenceSnapshot is the operator-safe view of the intelligence store.
// Reporter identities never cross this boundary. Publisher ids and entry ids
// are public federation identities; importing registries appear only as counts.
type IntelligenceSnapshot struct {
	SharingRegistries   int
	ImportingRegistries int
	ImportLinks         int
	ReadyVersions       int
	Adopted             int
	Helped              int
	SampleSize          int
	LatestReceivedAt    string
	Publishers          []IntelligencePublisher
	Versions            []IntelligenceVersion
}

type IntelligencePublisher struct {
	ProviderID          string
	KeyFingerprint      string
	ImportingRegistries int
	Techniques          int
	Versions            int
	ReadyVersions       int
	Shown               int
	Adopted             int
	Helped              int
	Dismissed           int
	SampleSize          int
}

type IntelligenceVersion struct {
	ProviderID          string
	KeyFingerprint      string
	EntryID             string
	ChannelID           string
	ContentHash         string
	ImportingRegistries int
	OutcomeRegistries   int
	Available           bool
	Shown               int
	Adopted             int
	Helped              int
	Dismissed           int
	SampleSize          int
}

type versionFold struct {
	version     IntelligenceVersion
	providerKey string
	imports     map[string]struct{}
	outcomes    map[string]struct{}
}

type publisherFold struct {
	publisher  IntelligencePublisher
	imports    map[string]struct{}
	techniques map[string]struct{}
}

// Snapshot folds raw reports into counts safe for the console. Outcome values
// are copied only after the exact content version clears the cross-org floor.
func (s *IntelligenceStore) Snapshot() IntelligenceSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := IntelligenceSnapshot{SharingRegistries: len(s.data.Reporters), ImportLinks: len(s.data.Imports)}
	importers := map[string]struct{}{}
	versions := map[string]*versionFold{}
	for _, reporter := range s.data.Reporters {
		if reporter.ReceivedAt > out.LatestReceivedAt {
			out.LatestReceivedAt = reporter.ReceivedAt
		}
	}
	for _, stored := range s.data.Imports {
		item, origin := stored.Import, stored.Import.Origin
		importers[stored.Reporter] = struct{}{}
		key := origin.ProviderID + "\x00" + origin.ProviderKey + "\x00" + origin.EntryID + "\x00" + origin.ContentHash
		fold := versions[key]
		if fold == nil {
			fold = &versionFold{version: IntelligenceVersion{ProviderID: origin.ProviderID,
				KeyFingerprint: FingerprintOf(origin.ProviderKey), EntryID: origin.EntryID,
				ChannelID: origin.ChannelID, ContentHash: origin.ContentHash},
				providerKey: origin.ProviderKey, imports: map[string]struct{}{}, outcomes: map[string]struct{}{}}
			versions[key] = fold
		}
		fold.imports[stored.Reporter] = struct{}{}
		if item.Outcome == nil {
			continue
		}
		fold.outcomes[stored.Reporter] = struct{}{}
		fold.version.Shown += item.Outcome.Shown
		fold.version.Adopted += item.Outcome.Adopted
		fold.version.Helped += item.Outcome.Helped
		fold.version.Dismissed += item.Outcome.Dismissed
		fold.version.SampleSize += item.Outcome.SampleSize
	}
	out.ImportingRegistries = len(importers)

	publishers := map[string]*publisherFold{}
	for _, fold := range versions {
		fold.version.ImportingRegistries = len(fold.imports)
		fold.version.Available = len(fold.outcomes) >= intel.CrossOrgMinRegistries
		if fold.version.Available {
			fold.version.OutcomeRegistries = len(fold.outcomes)
			out.ReadyVersions++
			out.Adopted += fold.version.Adopted
			out.Helped += fold.version.Helped
			out.SampleSize += fold.version.SampleSize
		} else {
			fold.version.Shown = 0
			fold.version.Adopted = 0
			fold.version.Helped = 0
			fold.version.Dismissed = 0
			fold.version.SampleSize = 0
		}
		out.Versions = append(out.Versions, fold.version)

		providerKey := fold.version.ProviderID + "\x00" + fold.providerKey
		publisher := publishers[providerKey]
		if publisher == nil {
			publisher = &publisherFold{publisher: IntelligencePublisher{ProviderID: fold.version.ProviderID,
				KeyFingerprint: fold.version.KeyFingerprint}, imports: map[string]struct{}{}, techniques: map[string]struct{}{}}
			publishers[providerKey] = publisher
		}
		publisher.publisher.Versions++
		publisher.techniques[fold.version.EntryID] = struct{}{}
		for reporter := range fold.imports {
			publisher.imports[reporter] = struct{}{}
		}
		if fold.version.Available {
			publisher.publisher.ReadyVersions++
			publisher.publisher.Shown += fold.version.Shown
			publisher.publisher.Adopted += fold.version.Adopted
			publisher.publisher.Helped += fold.version.Helped
			publisher.publisher.Dismissed += fold.version.Dismissed
			publisher.publisher.SampleSize += fold.version.SampleSize
		}
	}
	for _, fold := range publishers {
		fold.publisher.ImportingRegistries = len(fold.imports)
		fold.publisher.Techniques = len(fold.techniques)
		out.Publishers = append(out.Publishers, fold.publisher)
	}
	sort.Slice(out.Versions, func(i, j int) bool {
		a, b := out.Versions[i], out.Versions[j]
		if a.ImportingRegistries != b.ImportingRegistries {
			return a.ImportingRegistries > b.ImportingRegistries
		}
		if a.ProviderID != b.ProviderID {
			return a.ProviderID < b.ProviderID
		}
		if a.EntryID != b.EntryID {
			return a.EntryID < b.EntryID
		}
		return a.ContentHash < b.ContentHash
	})
	sort.Slice(out.Publishers, func(i, j int) bool {
		a, b := out.Publishers[i], out.Publishers[j]
		if a.ImportingRegistries != b.ImportingRegistries {
			return a.ImportingRegistries > b.ImportingRegistries
		}
		return a.ProviderID < b.ProviderID
	})
	return out
}

// Aggregate combines one exact upstream content version. Outcome values stay
// unavailable until reports from enough distinct registries exist.
func (s *IntelligenceStore) Aggregate(providerID, providerKey, entryID, contentHash string, minRegistries int) (IntelligenceAggregate, bool) {
	if minRegistries <= 0 {
		minRegistries = intel.CrossOrgMinRegistries
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out IntelligenceAggregate
	for _, stored := range s.data.Imports {
		o := stored.Import.Origin
		if o.ProviderID != providerID || o.ProviderKey != providerKey || o.EntryID != entryID || o.ContentHash != contentHash {
			continue
		}
		out.ImportingRegistries++
		if stored.Import.Outcome == nil {
			continue
		}
		r := stored.Import.Outcome
		out.ReportingRegistries++
		out.Shown += r.Shown
		out.Adopted += r.Adopted
		out.Helped += r.Helped
		out.Dismissed += r.Dismissed
		out.WeightedAdopted += r.WeightedAdopted
		out.WeightedHelped += r.WeightedHelped
		out.WeightedDismissed += r.WeightedDismissed
		out.SampleSize += r.SampleSize
	}
	if out.ReportingRegistries < minRegistries {
		return IntelligenceAggregate{ImportingRegistries: out.ImportingRegistries}, false
	}
	return out, true
}
