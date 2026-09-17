// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package embed re-exports the public embedding package (tacit/pkg/embed)
// for the registry's internal call sites. The implementation moved to pkg/
// so external tools (the miner, third-party providers) can share the exact
// vector space; see docs/design/api-standard.md.
package embed

import "github.com/opentacit/tacit/pkg/embed"

type (
	Vector            = embed.Vector
	Embedder          = embed.Embedder
	HashingEmbedder   = embed.HashingEmbedder
	UnknownModelError = embed.UnknownModelError
)

var (
	New           = embed.New
	NewHashing    = embed.NewHashing
	Dot           = embed.Dot
	Valid         = embed.Valid
	TechniqueText = embed.TechniqueText
	QueryText     = embed.QueryText
)
