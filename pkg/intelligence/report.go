// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package intelligence defines the privacy-safe report a registry sends back
// after accepting and using a federated technique.
package intelligence

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/opentacit/tacit/pkg/contracts"
	"github.com/opentacit/tacit/pkg/feed"
)

const (
	Version               = "tacit-intelligence/v1"
	MinOutcomeSample      = 10
	CrossOrgMinRegistries = 5
	MaxImportsPerReport   = 1000
)

// Outcome is an all-org aggregate. It has no member, session, audit, event,
// task-text, or cohort field by design.
type Outcome struct {
	WindowStart       string  `json:"window_start"`
	WindowEnd         string  `json:"window_end"`
	Shown             int     `json:"shown"`
	Adopted           int     `json:"adopted"`
	Helped            int     `json:"helped"`
	Dismissed         int     `json:"dismissed"`
	WeightedAdopted   float64 `json:"weighted_adopted"`
	WeightedHelped    float64 `json:"weighted_helped"`
	WeightedDismissed float64 `json:"weighted_dismissed"`
	SampleSize        int     `json:"sample_size"`
}

// Import records that one registry accepted one signed upstream item. Outcome
// is absent until the local evidence clears MinOutcomeSample.
type Import struct {
	Origin     contracts.FederationOrigin `json:"origin"`
	AcceptedAt string                     `json:"accepted_at"`
	Outcome    *Outcome                   `json:"outcome,omitempty"`
}

// Report is one signed snapshot from an authenticated registry.
type Report struct {
	Version            string   `json:"version"`
	ReporterProviderID string   `json:"reporter_provider_id"`
	ReporterPublicKey  string   `json:"reporter_public_key"`
	GeneratedAt        string   `json:"generated_at"`
	Imports            []Import `json:"imports"`
	Signature          string   `json:"signature,omitempty"`
}

func signingBase(r Report) []byte {
	r.Signature = ""
	raw, _ := json.Marshal(r)
	return raw
}

// Sign fills the reporter key and signs every field in the report.
func Sign(r *Report, key ed25519.PrivateKey) {
	r.ReporterPublicKey = feed.PublicKeyString(key.Public().(ed25519.PublicKey))
	r.Signature = "ed25519:" + base64.StdEncoding.EncodeToString(ed25519.Sign(key, signingBase(*r)))
}

// Verify checks the wire shape and signature. The receiver still binds the
// reporter provider ID and key to the authenticated tunnel identity.
func Verify(r Report) error {
	if r.Version != Version {
		return fmt.Errorf("unsupported intelligence version %q", r.Version)
	}
	if strings.TrimSpace(r.ReporterProviderID) == "" {
		return errors.New("reporter provider id is empty")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.GeneratedAt); err != nil {
		return errors.New("generated_at is not an RFC3339 timestamp")
	}
	if len(r.Imports) > MaxImportsPerReport {
		return fmt.Errorf("report has %d imports; maximum is %d", len(r.Imports), MaxImportsPerReport)
	}
	pub, err := feed.ParsePublicKey(r.ReporterPublicKey)
	if err != nil {
		return err
	}
	raw, ok := strings.CutPrefix(r.Signature, "ed25519:")
	if !ok {
		return errors.New("report signature missing or not ed25519")
	}
	sig, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || !ed25519.Verify(pub, signingBase(r), sig) {
		return errors.New("report signature invalid")
	}
	seen := map[string]bool{}
	for _, item := range r.Imports {
		o := item.Origin
		if o.ProviderID == "" || o.ProviderKey == "" || o.EntryID == "" || o.ContentHash == "" || o.ImportedAt == "" {
			return errors.New("import origin is incomplete")
		}
		if !strings.HasPrefix(o.EntryID, strings.TrimRight(o.ProviderID, "/")+"/techniques/") {
			return errors.New("import entry id is outside its provider namespace")
		}
		hash, ok := strings.CutPrefix(o.ContentHash, "sha256:")
		if !ok || len(hash) != 64 {
			return errors.New("import content hash is not sha256")
		}
		if _, err := hex.DecodeString(hash); err != nil {
			return errors.New("import content hash is not sha256")
		}
		if _, err := feed.ParsePublicKey(o.ProviderKey); err != nil {
			return fmt.Errorf("origin provider key: %w", err)
		}
		if _, err := time.Parse(time.RFC3339Nano, o.ImportedAt); err != nil {
			return errors.New("origin imported_at is not an RFC3339 timestamp")
		}
		if _, err := time.Parse(time.RFC3339Nano, item.AcceptedAt); err != nil {
			return errors.New("accepted_at is not an RFC3339 timestamp")
		}
		key := o.ProviderID + "\x00" + o.EntryID + "\x00" + o.ContentHash
		if seen[key] {
			return errors.New("report contains a duplicate import")
		}
		seen[key] = true
		if item.Outcome != nil {
			outcome := item.Outcome
			if outcome.SampleSize < MinOutcomeSample {
				return fmt.Errorf("outcome sample %d is below privacy floor %d", outcome.SampleSize, MinOutcomeSample)
			}
			if outcome.Shown < 0 || outcome.Adopted < 0 || outcome.Helped < 0 || outcome.Dismissed < 0 ||
				invalidAggregate(outcome.WeightedAdopted) || invalidAggregate(outcome.WeightedHelped) || invalidAggregate(outcome.WeightedDismissed) {
				return errors.New("outcome contains an invalid aggregate")
			}
			start, err := time.Parse(time.RFC3339Nano, outcome.WindowStart)
			if err != nil {
				return errors.New("outcome window_start is not an RFC3339 timestamp")
			}
			end, err := time.Parse(time.RFC3339Nano, outcome.WindowEnd)
			if err != nil {
				return errors.New("outcome window_end is not an RFC3339 timestamp")
			}
			if end.Before(start) {
				return errors.New("outcome window ends before it starts")
			}
		}
	}
	return nil
}

func invalidAggregate(v float64) bool { return v < 0 || math.IsNaN(v) || math.IsInf(v, 0) }
