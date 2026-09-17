// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

//go:build pg

package pgstore

import _ "embed"

// This file is the migration ledger: each schemaVN below is a historical
// record, applied exactly once per database and never edited after shipping.
// The card/capability names in V1–V13 are the vocabulary the product used
// when those versions shipped; V14 renames that vocabulary to "technique" in
// place, so both fresh and pre-existing databases end at the same shape. Do
// not modernize the terms in the frozen versions — a reworded migration is a
// different migration.

//go:embed schema.sql
var schemaV1 string

// schemaV2: revision-draft fields (docs/design/revision-design.md). Additive; also
// present in schema.sql's CREATE TABLE for fresh databases — the ALTERs here
// carry databases created before the columns existed.
const schemaV2 = `
ALTER TABLE cards ADD COLUMN IF NOT EXISTS supersedes    TEXT    NOT NULL DEFAULT '';
ALTER TABLE cards ADD COLUMN IF NOT EXISTS base_version  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cards ADD COLUMN IF NOT EXISTS revision_note TEXT    NOT NULL DEFAULT '';
`

// schemaV3: prior-version archive (docs/design/revision-design.md) — one snapshot
// row per reviewed change, read on demand for the history view.
const schemaV3 = `
CREATE TABLE IF NOT EXISTS card_versions (
  card_id     TEXT NOT NULL,
  version     INTEGER NOT NULL,
  snapshot    JSONB NOT NULL,
  archived_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_card_versions ON card_versions(card_id, archived_at);
`

// schemaV4: audit facts (docs/learning/synthesis-design.md) — what one interaction WAS,
// keyed by audit_id, so it joins to the feedback events recording how it turned
// OUT. Note there is no summary_text column, and there must never be one: that
// is the free-text, transcript-derived field, and its absence is what keeps "no
// transcripts persisted" literally true. Also present in schema.sql for fresh
// databases.
const schemaV4 = `
CREATE TABLE IF NOT EXISTS audit_facts (
  audit_id     TEXT PRIMARY KEY,
  created_at   TEXT NOT NULL,
  segment      JSONB,
  task_type    TEXT NOT NULL DEFAULT '',
  domain       TEXT NOT NULL DEFAULT '',
  harness      TEXT NOT NULL DEFAULT '',
  surface      TEXT NOT NULL DEFAULT '',
  skill_level  TEXT NOT NULL DEFAULT '',
  tools_used   JSONB,
  tools_absent JSONB,
  resources    JSONB,
  cards_offered  JSONB,
  thin         BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_audit_facts_created ON audit_facts(created_at);
`

// schemaV5: the model that served the interaction (docs/learning/synthesis-plan.md —
// the last can't-backfill capture field; feeds the per-model support matrix
// and org model benchmarking). Additive; also folded into schema.sql.
const schemaV5 = `
ALTER TABLE audit_facts ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT '';
`

const schemaV6 = `
ALTER TABLE audit_facts ADD COLUMN IF NOT EXISTS session_hash TEXT NOT NULL DEFAULT '';
`

// schemaV7: member keys — per-member access credentials
// (docs/design/enterprise-readiness.md; minted by the join flow, managed on
// the Members page). Hash only; the secret is never stored. Also folded into
// schema.sql.
const schemaV7 = `
CREATE TABLE IF NOT EXISTS member_keys (
	id         TEXT PRIMARY KEY,
	label      TEXT NOT NULL DEFAULT '',
	hash       TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT '',
	last_seen  TEXT NOT NULL DEFAULT '',
	revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_member_keys_hash ON member_keys(hash);
`

// schemaV8: the confidence-weighted funnel on rollups — inferred member
// verdicts count below explicit ones (config.InferredWeight;
// docs/delivery/low-intrusion-plan.md guardrail). Additive; also folded into
// schema.sql.
const schemaV8 = `
ALTER TABLE card_outcomes ADD COLUMN IF NOT EXISTS weighted_adopted DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE card_outcomes ADD COLUMN IF NOT EXISTS weighted_helped DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE card_outcomes ADD COLUMN IF NOT EXISTS weighted_dismissed DOUBLE PRECISION NOT NULL DEFAULT 0;
`

// schemaV9: adoption sketches — the registry's first-party intake for the
// zero-effort knowledge inflow (docs/mining/mining-design.md source 2).
// Additive; also folded into schema.sql.
const schemaV9 = `
CREATE TABLE IF NOT EXISTS sketches (
	sketch_id     TEXT PRIMARY KEY,
	capability_id TEXT NOT NULL DEFAULT '',
	session_hash  TEXT NOT NULL DEFAULT '',
	trigger_text  TEXT NOT NULL DEFAULT '',
	move          TEXT NOT NULL DEFAULT '',
	harness       TEXT NOT NULL DEFAULT '',
	task_type     TEXT NOT NULL DEFAULT '',
	segment       JSONB,
	tools         JSONB,
	created_at    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sketches_card ON sketches(capability_id);
`

// schemaV10: the delivery-mode ledger (docs/delivery/agent-delivery-plan.md
// Phase C) — how a shown card reached the session: surfaced to the member
// ("suggested", the historical implicit default, stored as ”) or silently
// applied by an agent ("applied"). Additive; also in schema.sql for fresh
// databases.
const schemaV10 = `
ALTER TABLE feedback_events ADD COLUMN IF NOT EXISTS delivery TEXT NOT NULL DEFAULT '';
`

// schemaV11: the lifecycle-events log — the
// curation moments in a card's own life (discovered/promoted/retired/decayed),
// carrying the reason the registry acted. Cohort-free by construction: no
// member, cohort, or session. Additive; also folded into schema.sql for fresh
// databases.
const schemaV11 = `
CREATE TABLE IF NOT EXISTS lifecycle_events (
  event_id    TEXT PRIMARY KEY,
  card_id     TEXT NOT NULL,
  card_name   TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL,
  provenance  TEXT NOT NULL DEFAULT '',
  reason      TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lifecycle_created ON lifecycle_events(created_at);
`

// schemaV12: retrieval provenance on the funnel — which path produced the
// event (ambient fit-check burst vs member pull) and the similarity that put
// the card in front of the fit-check. Without the pair a decline rate blends
// two paths with different economics, and config.MinSimilarity has nothing to
// be calibrated against. Additive; also in schema.sql for fresh databases.
const schemaV12 = `
ALTER TABLE feedback_events ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback_events ADD COLUMN IF NOT EXISTS similarity DOUBLE PRECISION NOT NULL DEFAULT 0;
`

// schemaV13: federation feed tokens (docs/distribution/global-access-plan.md).
// Every channel except the computed `public` one needs a bearer token, so that a
// registry taking a public address does not thereby serve its private channels
// to the world.
const schemaV13 = `
CREATE TABLE IF NOT EXISTS feed_tokens (
  id         TEXT PRIMARY KEY,
  label      TEXT NOT NULL DEFAULT '',
  hash       TEXT NOT NULL,
  channels   JSONB   NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  last_used  TEXT NOT NULL DEFAULT '',
  revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_feed_tokens_hash ON feed_tokens(hash);
`

// schemaV14: the card→technique vocabulary rename. Tables, columns, and
// indexes move to the names the code has used since the rename; data is
// untouched.
const schemaV14 = `
ALTER TABLE cards RENAME TO techniques;
ALTER TABLE card_versions RENAME TO technique_versions;
ALTER TABLE technique_versions RENAME COLUMN card_id TO technique_id;
ALTER TABLE card_outcomes RENAME TO technique_outcomes;
ALTER TABLE technique_outcomes RENAME COLUMN card_id TO technique_id;
ALTER TABLE feedback_events RENAME COLUMN capability_id TO technique_id;
ALTER TABLE feedback_events RENAME COLUMN capability_version TO technique_version;
ALTER TABLE audit_facts RENAME COLUMN cards_offered TO techniques_offered;
ALTER TABLE sketches RENAME COLUMN capability_id TO technique_id;
ALTER TABLE lifecycle_events RENAME COLUMN card_id TO technique_id;
ALTER TABLE lifecycle_events RENAME COLUMN card_name TO technique_name;
ALTER INDEX IF EXISTS idx_cards_status RENAME TO idx_techniques_status;
ALTER INDEX IF EXISTS idx_cards_scope RENAME TO idx_techniques_scope;
ALTER INDEX IF EXISTS idx_card_versions RENAME TO idx_technique_versions;
ALTER INDEX IF EXISTS idx_events_cap RENAME TO idx_events_technique;
ALTER INDEX IF EXISTS idx_sketches_card RENAME TO idx_sketches_technique;
`

// schemaV15: the rollup table's primary key leads with technique_id, so
// OutcomesForSegment — the read behind every ranked list — had to scan the
// whole table to find one segment's rows. This index is the access path that
// read actually wants.
const schemaV15 = `
CREATE INDEX IF NOT EXISTS idx_outcomes_segment ON technique_outcomes(segment_key);
`

const schemaV16 = `
ALTER TABLE techniques ADD COLUMN IF NOT EXISTS origin JSONB;
`

// migrations, applied in order; schema_migrations records the version.
var migrations = []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV8, schemaV9, schemaV10, schemaV11, schemaV12, schemaV13, schemaV14, schemaV15, schemaV16}
