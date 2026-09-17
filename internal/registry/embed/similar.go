// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package embed

import (
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
)

// TooSimilar reports whether vec is a near-duplicate (config.DupThreshold
// cosine or above) of any existing technique's embedding — the shared novelty
// gate for machine-proposed techniques (suggest, observe).
func TooSimilar(vec Vector, techniques []models.Technique) bool {
	for _, c := range techniques {
		if len(c.Embedding) != len(vec) || len(vec) == 0 {
			continue
		}
		if Dot(vec, c.Embedding) >= config.DupThreshold {
			return true
		}
	}
	return false
}
