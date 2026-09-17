-- Registry schema, migration v1. Frozen: see migrations.go.
-- Same tables and semantics as the file store and the original Python
-- schema.sql: cards (with packed-float32 embeddings), an append-only feedback
-- event log (the source of truth for outcomes), and derived per-(card,
-- segment) rollups. Timestamps are ISO-8601 TEXT to stay byte-equivalent with
-- the wire contract and the file backend.

CREATE TABLE IF NOT EXISTS cards (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  description     TEXT NOT NULL,
  scope           TEXT NOT NULL DEFAULT 'general',  -- general|org
  status          TEXT NOT NULL,                    -- draft|mined|stable|decayed|retired
  provenance      TEXT NOT NULL,                    -- curated|mined|contributed
  version         INTEGER NOT NULL DEFAULT 1,
  recipe          TEXT NOT NULL,
  before_after    TEXT NOT NULL DEFAULT '',
  tags            JSONB,
  task_types      JSONB,
  triggers        JSONB,
  applies_when    TEXT NOT NULL DEFAULT '',
  not_when        TEXT NOT NULL DEFAULT '',
  support_matrix  JSONB,
  shipped         TEXT NOT NULL DEFAULT '',
  decay_signal    INTEGER NOT NULL DEFAULT 0,
  decay_checked   TEXT NOT NULL DEFAULT '',
  embedding       BYTEA,                            -- packed little-endian float32
  embedding_model TEXT NOT NULL DEFAULT '',
  embedding_dim   INTEGER NOT NULL DEFAULT 0,
  channels        JSONB,                            -- federation publication (null = unpublished)
  source          TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  supersedes      TEXT NOT NULL DEFAULT '',         -- revision draft: base card id (v2)
  base_version    INTEGER NOT NULL DEFAULT 0,       -- revision draft: base version pin (v2)
  revision_note   TEXT NOT NULL DEFAULT '',         -- revision draft: proposer's why (v2)
  origin          JSONB                             -- local federation lineage
);
ALTER TABLE cards ADD COLUMN IF NOT EXISTS channels JSONB;
ALTER TABLE cards ADD COLUMN IF NOT EXISTS origin JSONB;
CREATE INDEX IF NOT EXISTS idx_cards_status ON cards(status);
CREATE INDEX IF NOT EXISTS idx_cards_scope  ON cards(scope);

CREATE TABLE IF NOT EXISTS feedback_events (
  event_id            TEXT PRIMARY KEY,
  audit_id            TEXT NOT NULL,
  capability_id       TEXT NOT NULL,
  capability_version  INTEGER NOT NULL DEFAULT 0,
  stage               TEXT NOT NULL,                -- shown|adopted|helped|dismissed
  value               JSONB,
  segment             JSONB,
  task_type           TEXT NOT NULL DEFAULT '',
  rank_shown          INTEGER NOT NULL DEFAULT 0,
  confidence          TEXT NOT NULL DEFAULT '',     -- explicit|inferred|verification
  delivery            TEXT NOT NULL DEFAULT '',     -- ''(suggested)|suggested|applied
  source              TEXT NOT NULL DEFAULT '',     -- ambient|pull ('' predates the field)
  similarity          REAL NOT NULL DEFAULT 0,      -- retrieval score behind this candidate
  created_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_cap     ON feedback_events(capability_id, stage);
CREATE INDEX IF NOT EXISTS idx_events_created ON feedback_events(created_at);

CREATE TABLE IF NOT EXISTS card_outcomes (
  card_id        TEXT NOT NULL,
  segment_key    TEXT NOT NULL,                     -- 'team:payments' | ... | '__overall__'
  shown          INTEGER NOT NULL DEFAULT 0,
  adopted        INTEGER NOT NULL DEFAULT 0,
  helped         INTEGER NOT NULL DEFAULT 0,
  dismissed      INTEGER NOT NULL DEFAULT 0,
  weighted_adopted   DOUBLE PRECISION NOT NULL DEFAULT 0,  -- confidence-weighted funnel:
  weighted_helped    DOUBLE PRECISION NOT NULL DEFAULT 0,  -- inferred verdicts count below
  weighted_dismissed DOUBLE PRECISION NOT NULL DEFAULT 0,  -- explicit (config.InferredWeight)
  adoption_rate  DOUBLE PRECISION,
  helped_rate    DOUBLE PRECISION,
  sample_size    INTEGER NOT NULL DEFAULT 0,        -- = floor(weighted_adopted)
  last_updated   TEXT NOT NULL,
  PRIMARY KEY (card_id, segment_key)
);

-- What one interaction WAS, keyed by audit_id — the join to the feedback events
-- that record how it turned OUT (docs/synthesis-design.md). Retrieval receives
-- all of this on every /v1/evidence call; before this table it was discarded.
--
-- There is no summary_text column, and there must never be one: that is the
-- single free-text, transcript-derived field on a characterization, and its
-- absence here is what keeps "no transcripts persisted" literally true.
CREATE TABLE IF NOT EXISTS audit_facts (
  audit_id     TEXT PRIMARY KEY,
  session_hash TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  segment      JSONB,                               -- the static cohort dims
  task_type    TEXT NOT NULL DEFAULT '',
  domain       TEXT NOT NULL DEFAULT '',
  harness      TEXT NOT NULL DEFAULT '',
  surface      TEXT NOT NULL DEFAULT '',
  skill_level  TEXT NOT NULL DEFAULT '',
  tools_used   JSONB,                               -- enumerable ids, never free text
  tools_absent JSONB,
  resources    JSONB,                               -- the org's internal systems in play
  cards_offered  JSONB,                               -- capability ids, in rank order
  thin         BOOLEAN NOT NULL DEFAULT FALSE,      -- the cold-start flag retrieval computed
  model        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_facts_created ON audit_facts(created_at);

-- member keys: per-member access credentials (hash only; never the secret)
CREATE TABLE IF NOT EXISTS member_keys (
	id         TEXT PRIMARY KEY,
	label      TEXT NOT NULL DEFAULT '',
	hash       TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT '',
	last_seen  TEXT NOT NULL DEFAULT '',
	revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_member_keys_hash ON member_keys(hash);

-- adoption sketches: scrubbed situation + move at first-adoption moments,
-- the registry's first-party mining intake (docs/mining/mining-design.md source 2)
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

-- lifecycle events: the curation moments in a card's own life — discovered,
-- promoted, retired, decayed. Cohort-free by
-- construction: a card's lifecycle is org-level, carrying no member or session.
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
