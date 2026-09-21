// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Support rows written from measurement.
//
// support_matrix says which harnesses and models a technique is known to work
// on, dated. Until now every row was typed by a curator, which means the matrix
// records what somebody remembered to check rather than what anybody actually
// ran. Meanwhile the evidence for exactly those rows accumulates in the rollups:
// a member who adopts a technique on a model and reports it helped has verified
// that pairing more directly than a curator reading a changelog.
//
// So the registry proposes the row. It does NOT write it: a proposal lands as an
// ordinary revision draft in a fixed `@support` slot, and a reviewer promotes it
// like anything else. That ordering matters more here than elsewhere, because a
// support row is the one thing a single-member registry can contribute upward
// past the Wilson floors — a row asserts a fact rather than a rate, and a sample
// of one is allowed to assert facts (docs/design/single-user-value.md). What
// it is not allowed to do is assert them unread.
package feedback

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/modelid"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
)

// supportSlot is the fixed id suffix one technique's pending support proposal
// occupies. Fixed, like federation's `@upstream`, so a later pass replaces the
// proposal rather than stacking a second one — and so it can never collide with
// a member's own `@N` revision drafts.
const supportSlot = "@support"

// ProposeSupportRows files, for every technique whose measured outcomes name a
// model its support matrix does not, one revision draft adding the rows. Returns
// how many techniques gained a proposal.
//
// The floors are config.SupportRowMinAdopted adoptions and at least one helped
// verdict on that model. They are deliberately far below MinSample, which
// governs whether a RATE may be trusted: this is not a rate. "It was used here
// and somebody said it worked" is the claim, and thirty adoptions is not what
// makes that claim true — it is what would make it never get made on a registry
// with one member in it.
func ProposeSupportRows(st storage.Store) (int, error) {
	techniques, err := st.ListTechniques([]string{"stable"}, 0)
	if err != nil {
		return 0, err
	}
	measured, err := modelEvidence(st)
	if err != nil {
		return 0, err
	}
	month := time.Now().UTC().Format("2006-01")
	proposed := 0
	for _, technique := range techniques {
		rows := missingSupportRows(technique, measured[technique.ID], month)
		if len(rows) == 0 {
			continue
		}
		if err := fileSupportDraft(st, technique, rows); err != nil {
			return proposed, err
		}
		proposed++
	}
	return proposed, nil
}

// modelFunnel is one (technique, model) pairing's evidence.
type modelFunnel struct{ adopted, helped int }

// modelEvidence counts adoptions and helped verdicts per (technique, model)
// from the event log.
//
// It reads the log rather than the rollups because the store's outcome reads are
// by segment key or by (technique, segment), and neither answers "which models
// does this technique have evidence on" without a scan. The log is the same
// source the rollups are built from and this pass runs beside the recompute, so
// the two cannot disagree.
func modelEvidence(st storage.Store) (map[string]map[string]*modelFunnel, error) {
	events, err := st.AllEvents("")
	if err != nil {
		return nil, err
	}
	factModels := modelsByAudit(st, events)
	out := map[string]map[string]*modelFunnel{}
	for _, e := range events {
		if e.Stage != "adopted" && e.Stage != "helped" {
			continue
		}
		model := e.Segment["model"]
		if model == "" && e.AuditID != "" {
			model = modelid.Key(factModels[e.AuditID])
		}
		if model == "" || e.TechniqueID == "" {
			continue
		}
		byModel := out[e.TechniqueID]
		if byModel == nil {
			byModel = map[string]*modelFunnel{}
			out[e.TechniqueID] = byModel
		}
		f := byModel[model]
		if f == nil {
			f = &modelFunnel{}
			byModel[model] = f
		}
		if e.Stage == "adopted" {
			f.adopted++
		} else {
			f.helped++
		}
	}
	return out, nil
}

// missingSupportRows returns the rows a technique's measured outcomes justify
// and its matrix does not already carry, in a stable order.
func missingSupportRows(technique models.Technique, measured map[string]*modelFunnel, month string) []map[string]any {
	if len(measured) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, e := range technique.SupportMatrix {
		if m := modelKeyOf(e); m != "" {
			known[m] = true
		}
	}
	var keys []string
	for model, f := range measured {
		if known[model] || f.adopted < config.SupportRowMinAdopted || f.helped < 1 {
			continue
		}
		keys = append(keys, model)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	rows := make([]map[string]any, 0, len(keys))
	for _, m := range keys {
		rows = append(rows, map[string]any{
			"model": m, "supported": true, "verified": month, "source": "measured",
		})
	}
	return rows
}

// modelKeyOf reads a support row's model, whatever spelling it was written in.
func modelKeyOf(row map[string]any) string {
	s, _ := row["model"].(string)
	return modelid.Key(s)
}

// fileSupportDraft writes (or replaces) the technique's pending support
// proposal. The draft is the base technique plus the new rows, so a reviewer
// reads the whole technique as it would be rather than a diff fragment.
//
// The embedding is copied rather than recomputed: support_matrix is not part of
// embed.TechniqueText, so the draft's vector is the base's by construction, and
// copying it means the reviewer's similar-techniques view works on the pass that
// files the draft rather than after the next restart.
func fileSupportDraft(st storage.Store, base models.Technique, rows []map[string]any) error {
	draft := base
	draft.ID = base.ID + supportSlot
	draft.Status = "draft"
	draft.Supersedes = base.ID
	draft.BaseVersion = base.Version
	draft.Version = base.Version + 1
	draft.SupportMatrix = append(append([]map[string]any(nil), base.SupportMatrix...), rows...)
	draft.RevisionNote = supportNote(rows)
	// A held-out proposal, not a serving technique: no publication, no decay
	// state, its own timestamps — the same shape federation's `@upstream`
	// proposal takes.
	draft.Channels = nil
	draft.Origin = nil
	draft.DecaySignal, draft.DecayChecked = 0, ""
	now := models.Now()
	draft.CreatedAt, draft.UpdatedAt = now, now
	if err := st.UpsertTechnique(draft); err != nil {
		return err
	}
	if len(base.Embedding) > 0 {
		return st.SetTechniqueEmbedding(draft.ID, base.Embedding, base.EmbeddingModel, base.EmbeddingDim)
	}
	return nil
}

// supportNote is the why a reviewer reads. It names the measurement rather than
// the conclusion, so the reviewer can disagree with it.
func supportNote(rows []map[string]any) string {
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		if m, _ := r["model"].(string); m != "" {
			names = append(names, m)
		}
	}
	return fmt.Sprintf(
		"measured: adopted and reported helped on %s, which the support matrix does not name",
		strings.Join(names, ", "))
}
