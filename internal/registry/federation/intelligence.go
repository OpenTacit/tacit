// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"crypto/ed25519"
	"math"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/pkg/intelligence"
)

// IntelligenceReport builds the import receipt and, when it clears the local
// privacy floor, the overall outcome snapshot for each accepted import.
func IntelligenceReport(st storage.Store, reporterID string, key ed25519.PrivateKey, now time.Time) (*intelligence.Report, error) {
	techniques, err := st.ListTechniques([]string{"stable"}, 0)
	if err != nil {
		return nil, err
	}
	r := &intelligence.Report{
		Version: intelligence.Version, ReporterProviderID: reporterID,
		GeneratedAt: now.UTC().Format(time.RFC3339Nano), Imports: []intelligence.Import{},
	}
	for _, technique := range techniques {
		if technique.Provenance != "federated" || technique.Origin == nil {
			continue
		}
		if technique.Origin.ProviderID == reporterID {
			continue // never ask the ingress to count a provider's own evidence
		}
		item := intelligence.Import{Origin: *technique.Origin, AcceptedAt: technique.UpdatedAt}
		outcome, ok, err := st.GetOutcome(technique.ID, config.OverallKey)
		if err != nil {
			return nil, err
		}
		if ok && outcome.SampleSize >= intelligence.MinOutcomeSample {
			item.Outcome = outcomeReport(outcome, technique.Origin.ImportedAt, r.GeneratedAt)
		}
		r.Imports = append(r.Imports, item)
		if len(r.Imports) == intelligence.MaxImportsPerReport {
			break
		}
	}
	intelligence.Sign(r, key)
	return r, nil
}

func outcomeReport(o models.Outcome, start, end string) *intelligence.Outcome {
	return &intelligence.Outcome{
		WindowStart: start, WindowEnd: end,
		Shown: o.Shown, Adopted: o.Adopted, Helped: o.Helped, Dismissed: o.Dismissed,
		WeightedAdopted: roundAggregate(o.WeightedAdopted), WeightedHelped: roundAggregate(o.WeightedHelped),
		WeightedDismissed: roundAggregate(o.WeightedDismissed), SampleSize: o.SampleSize,
	}
}

func roundAggregate(v float64) float64 { return math.Round(v*100) / 100 }
