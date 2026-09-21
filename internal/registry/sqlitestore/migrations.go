// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlitestore

import _ "embed"

// This file is the migration ledger: each schemaVN below is a historical
// record, applied exactly once per database and never edited after shipping.
// The card/capability names in V1–V4 are the vocabulary the product used when
// those versions shipped; V5 renames that vocabulary to "technique" in place,
// so both fresh and pre-existing databases end at the same shape. Do not
// modernize the terms in the frozen versions — a reworded migration is a
// different migration.

//go:embed schema.sql
var schemaV1 string

// schemaV2: the lifecycle-events log —
// carries databases created before it existed. Also folded into schema.sql for
// fresh databases. CREATE TABLE IF NOT EXISTS is idempotent, so this is safe to
// re-run even where schema.sql already created it.
const schemaV2 = `
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

// schemaV3: retrieval provenance on the funnel — which path produced the event
// (source) and the similarity that put the card in front of the fit-check.
// Without the pair, a decline rate blends the ambient burst with member pulls
// and no threshold for config.MinSimilarity can be calibrated from the log.
// Existing rows keep source=” and similarity=0, which the rollup reads as
// "predates the field" rather than as a measurement.
const schemaV3 = `
ALTER TABLE feedback_events ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback_events ADD COLUMN similarity REAL NOT NULL DEFAULT 0;
`

// schemaV4: federation feed tokens (docs/distribution/global-access-plan.md).
// Every channel except the computed `public` one needs a bearer token, so that a
// registry taking a public address does not thereby serve its private channels
// to the world. Separate table from member_keys on purpose: the two credentials
// authorize disjoint surfaces.
const schemaV4 = `
CREATE TABLE IF NOT EXISTS feed_tokens (
  id         TEXT PRIMARY KEY,
  label      TEXT NOT NULL DEFAULT '',
  hash       TEXT NOT NULL,
  channels   TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  last_used  TEXT NOT NULL DEFAULT '',
  revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_feed_tokens_hash ON feed_tokens(hash);
`

// schemaV5: the card→technique vocabulary rename. Tables, columns, and
// indexes move to the names the code has used since the rename; data is
// untouched. SQLite ≥3.25 (modernc tracks well past it) supports RENAME
// COLUMN. Index renames are drop-and-recreate — SQLite has no ALTER INDEX.
const schemaV5 = `
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
DROP INDEX IF EXISTS idx_cards_status;
DROP INDEX IF EXISTS idx_cards_scope;
DROP INDEX IF EXISTS idx_card_versions;
DROP INDEX IF EXISTS idx_events_cap;
DROP INDEX IF EXISTS idx_sketches_card;
CREATE INDEX IF NOT EXISTS idx_techniques_status ON techniques(status);
CREATE INDEX IF NOT EXISTS idx_techniques_scope  ON techniques(scope);
CREATE INDEX IF NOT EXISTS idx_technique_versions ON technique_versions(technique_id, archived_at);
CREATE INDEX IF NOT EXISTS idx_events_technique ON feedback_events(technique_id, stage);
CREATE INDEX IF NOT EXISTS idx_sketches_technique ON sketches(technique_id);
`

// schemaV6: the rollup table's primary key leads with technique_id, so
// OutcomesForSegment — the read behind every ranked list — had to scan the
// whole table to find one segment's rows. This index is the access path that
// read actually wants.
const schemaV6 = `
CREATE INDEX IF NOT EXISTS idx_outcomes_segment ON technique_outcomes(segment_key);
`

const schemaV7 = `
ALTER TABLE techniques ADD COLUMN origin TEXT;
`

// schemaV8: a technique retired into another one records which
// (registry/techmerge). Folds over the event log redirect through it, so the
// survivor of a consolidation is judged on what the whole group learned.
const schemaV8 = `
ALTER TABLE techniques ADD COLUMN merged_into TEXT NOT NULL DEFAULT '';
`

// migrations, applied in order; schema_migrations records the version. Each
// entry runs exactly once per database, so future ALTERs here can be plain
// ADD COLUMN — SQLite has no IF NOT EXISTS for columns, and with versioned
// application it doesn't need one.
var migrations = []string{schemaV1, schemaV2, schemaV3, schemaV4, schemaV5, schemaV6, schemaV7, schemaV8}
