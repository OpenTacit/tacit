// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package contracts defines OpenTacit's public wire types — the data
// contracts any implementation (registry, hook agent, miner, federation
// provider, third-party tool) exchanges. This package is import-stable and
// dependency-free; the normative, language-neutral definitions live in
// schemas/*.schema.json (JSON Schema 2020-12) and are enforced against these
// types by round-trip tests. See docs/design/api-standard.md.
//
// The registry's internal/registry/models package aliases these types and
// adds server-side validation; the audit layer's internal mirror stays
// loosely-typed by design (it consumes, never authors).
package contracts

import (
	"crypto/sha256"
	"encoding/hex"
)

// Stages a feedback event may carry. shown → adopted → helped is the delivery
// funnel; dismissed is its negative. declined is OFF that funnel: it records
// that a retrieved candidate was fit-checked and the LLM judged it a non-fit
// for that context, so it was never shown. It is retrieval-quality telemetry
// (how often retrieval surfaces a candidate the synthesizer rejects) and must
// never enter the adopted/helped math, which is conditioned on shown.
//
// shadow_shown / shadow_declined are the shadow rung's relevance telemetry
// (docs/learning/validation-without-review.md): a shadow-status technique, judged in
// a real context by the fit-check but NEVER surfaced, was accepted (shadow_shown
// — it would have fit) or rejected (shadow_declined). Like declined they are
// off every funnel; unlike declined they attach to techniques the member cannot see,
// so they can never touch adopted/helped. The rollup ignores them by
// construction (counts.add has no case for them) — that is the fail-safe: any
// aggregate that does not explicitly ask for a shadow stage cannot include it.
var Stages = []string{"shown", "adopted", "helped", "dismissed", "declined", "shadow_shown", "shadow_declined"}

// DeterministicFeedbackEventID derives a stable event id for an outcome tied
// to a specific audit, so one (audit, technique, stage) observation ingests
// exactly once no matter how many times — or via how many producers — it is
// submitted. The motivating double-count: a standalone 'adopted' followed by
// the adopted+helped pair a 'helped' verdict implies inflated the adoption
// sample and misread the helped rate. Events without an audit id keep random
// ids: separate submissions are separate observations. A changed verdict for
// the same (audit, technique, stage) is deliberately NOT an update — the
// log is append-only; record it against a fresh audit id.
func DeterministicFeedbackEventID(auditID, techniqueID, stage string) string {
	sum := sha256.Sum256([]byte(auditID + "\x00" + techniqueID + "\x00" + stage))
	return "evt_" + hex.EncodeToString(sum[:16])
}

// DismissReasons are the allowed `value`s for a dismissed event.
var DismissReasons = []string{"not-relevant", "already-knew", "didnt-work"}

// TechniqueScopes: general (a technique of the model itself) or org (exists only
// with this organization's proprietary tools/data/conventions).
var TechniqueScopes = []string{"general", "org"}

// Confidences a feedback event may carry.
var Confidences = []string{"explicit", "inferred"}

// Segment is one member cohort: a subset of the configured segment
// dimensions (team, role, function, domain, harness, surface) -> value.
// Cohorts, never identities: OpenTacit carries no member identifiers.
type Segment map[string]string

// FederationOrigin records the signed upstream item from which a technique came.
// It is local lineage, not part of the technique's federated content.
type FederationOrigin struct {
	ProviderID  string `json:"provider_id"`
	ProviderKey string `json:"provider_key"`
	EntryID     string `json:"entry_id"`
	ChannelID   string `json:"channel_id"`
	ContentHash string `json:"content_hash"`
	ImportedAt  string `json:"imported_at"`
}

// Technique is one technique (schemas/technique.schema.json).
type Technique struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Scope          string            `json:"scope"`      // general|org
	Status         string            `json:"status"`     // draft|shadow|mined|stable|decayed|retired
	Provenance     string            `json:"provenance"` // curated|mined|contributed|federated|suggested|observed
	Version        int               `json:"version"`
	Recipe         string            `json:"recipe"`
	BeforeAfter    string            `json:"before_after,omitempty"`
	Tags           []string          `json:"tags,omitempty"`
	TaskTypes      []string          `json:"task_types,omitempty"`
	Triggers       []any             `json:"triggers,omitempty"` // strings or {heuristic|llm_judge: ...}
	AppliesWhen    string            `json:"applies_when,omitempty"`
	NotWhen        string            `json:"not_when,omitempty"`
	SupportMatrix  []map[string]any  `json:"support_matrix,omitempty"`
	Channels       []string          `json:"channels,omitempty"` // federation channels this technique is published to
	Origin         *FederationOrigin `json:"origin,omitempty"`
	Shipped        string            `json:"shipped,omitempty"` // YYYY-MM
	DecaySignal    int               `json:"decay_signal"`
	DecayChecked   string            `json:"decay_checked,omitempty"`
	Embedding      []float32         `json:"embedding,omitempty"`
	EmbeddingModel string            `json:"embedding_model,omitempty"`
	EmbeddingDim   int               `json:"embedding_dim,omitempty"`
	Source         string            `json:"source,omitempty"` // git path, 'mined'/'contributed', or federated URI
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`

	// Revision drafts (docs/design/revision-design.md): a draft that proposes changes
	// to an existing technique rides the ordinary drafts lane with Supersedes set
	// to the base technique's id. BaseVersion pins the base's Version at drafting
	// time — promotion refuses to apply a revision whose base has since moved.
	// RevisionNote is the proposer's "why", shown to the reviewer only.
	Supersedes   string `json:"supersedes,omitempty"`
	BaseVersion  int    `json:"base_version,omitempty"`
	RevisionNote string `json:"revision_note,omitempty"`

	// MergedInto names the technique this one was consolidated into: it said the
	// same thing, a reviewer kept the other, and this one was retired
	// (registry/techmerge). It is deliberately not Supersedes — that says "this
	// draft is an edit of that technique" and is applied on promotion, and
	// conflating the two would put a retired duplicate into the revision flow.
	//
	// What it buys is that the survivor is judged on everything the group
	// learned. Folds over the event log redirect through it in memory, so the
	// ledger stays append-only and re-running a fold changes nothing.
	MergedInto string `json:"merged_into,omitempty"`
}

// FeedbackEvent is one append-only outcome event
// (schemas/feedback-event.schema.json).
type FeedbackEvent struct {
	EventID          string  `json:"event_id"`
	AuditID          string  `json:"audit_id"`
	TechniqueID      string  `json:"technique_id"`
	TechniqueVersion int     `json:"technique_version,omitempty"`
	Stage            string  `json:"stage"`           // one of Stages
	Value            any     `json:"value,omitempty"` // bool for helped; reason for dismissed
	Segment          Segment `json:"segment"`
	TaskType         string  `json:"task_type,omitempty"`
	RankShown        int     `json:"rank_shown,omitempty"`
	Confidence       string  `json:"confidence"` // explicit|inferred|verification
	// Delivery records how a shown technique reached the session: "suggested"
	// (surfaced to the member, the default) or "applied" (delivered to the
	// model for silent application in an autonomous session — the
	// evidence-gated autonomy ledger, docs/delivery/agent-delivery-plan.md).
	Delivery string `json:"delivery,omitempty"`
	// Source records WHICH retrieval path produced this event: SourceAmbient
	// (the unprompted fit-check burst) or SourcePull (the member asked). The
	// two have wildly different economics — an ambient burst judges candidates
	// nobody requested and rejects most of them by design, while a pull is
	// already scoped by the member's question — so blending them into one
	// decline rate produces a number that describes neither. Empty on events
	// written before this field existed; see insights.ambient for how those
	// are classified.
	Source string `json:"source,omitempty"`
	// Similarity is the retrieval score that put this technique in front of the
	// fit-check (see EvidenceCandidate.Similarity). Recorded on shown and
	// declined so the score distribution of accepted vs rejected candidates is
	// measurable, which is what config.MinSimilarity must be calibrated on.
	Similarity float64 `json:"similarity,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

// CountsAsHelped reports whether a helped event is actually a help.
//
// Value carries the answer on this stage (see the field). A member who says the
// technique did not help writes helped:false, and counting the event rather than
// what it says would turn every answer into a yes — which is the one number the
// whole evidence thesis rests on.
//
// An absent or non-boolean Value counts as a help: that is what every event
// written before the field was populated looks like, and the stage itself was
// the answer then.
//
// This lives on the event because five separate places fold the log into a
// funnel — the dashboard, the rollups, the org report, the map and the merge
// archive — and only the archive used to ask. The other four counted a "no" as
// a yes.
func (e FeedbackEvent) CountsAsHelped() bool {
	if e.Stage != "helped" {
		return false
	}
	b, ok := e.Value.(bool)
	return !ok || b
}

// Feedback event sources. The discriminating property is whether the event
// came out of a FIT-CHECK BURST — the only path that judges candidates nobody
// asked for and can therefore emit 'declined'. SourceAmbient marks that path;
// SourcePull is every other way a technique reaches a session (a member asking, or
// an operator running the audit pipeline), none of which decline.
//
// This is what keeps a decline rate honest: its denominator must contain only
// shown events that a decline was possible for.
const (
	SourceAmbient = "ambient" // the unprompted fit-check burst (the suggestion hook)
	SourcePull    = "pull"    // asked for: @tacit, tacit_search, /tacit:*, tacit audit
)

// FeedbackEventDraft is a feedback event as an agent emits it (stage-4
// write-back). The registry assigns event_id/created_at and validates, so the
// draft carries only what the producer knows. A producer MAY set EventID for
// idempotent ingest.
type FeedbackEventDraft struct {
	EventID     string  `json:"event_id,omitempty"`
	AuditID     string  `json:"audit_id,omitempty"`
	TechniqueID string  `json:"technique_id"`
	Stage       string  `json:"stage"` // shown|adopted|helped|dismissed
	Value       any     `json:"value,omitempty"`
	Segment     Segment `json:"segment,omitempty"`
	TaskType    string  `json:"task_type,omitempty"`
	RankShown   int     `json:"rank_shown,omitempty"`
	Confidence  string  `json:"confidence,omitempty"` // explicit|inferred|verification
	Delivery    string  `json:"delivery,omitempty"`   // suggested|applied (see FeedbackEvent)
	Source      string  `json:"source,omitempty"`     // ambient|pull (see FeedbackEvent)
	Similarity  float64 `json:"similarity,omitempty"` // retrieval score (see FeedbackEvent)
	CreatedAt   string  `json:"created_at,omitempty"`
}

// LifecycleKinds enumerate the curation moments the registry logs about a
// technique's OWN journey — the machine acting on the playbook, as distinct from the
// delivery Stages that record how a member's interaction turned out. discovered:
// a technique distilled from observed usage. promoted: a shadow technique graduated to
// serving on its relevance evidence. retired: a technique removed (a stale shadow
// technique that fit too rarely). decayed: a serving technique flagged because its recent
// helped-rate fell below its baseline. approved: a PERSON accepted the technique —
// the one moment in this list the machine did not produce, and the only record
// that distinguishes a reviewer's promotion from the auto-promote gate's.
// published / delisted: the technique entered or left the computed Public channel
// (docs/distribution/global-access-plan.md), which is how the commons keeps its
// membership history and its EnteredAt without a storage object of its own.
// These are the events the Events view renders to answer "what is OpenTacit
// actually doing?" — with the reason, not just the fact.
var LifecycleKinds = []string{"discovered", "promoted", "retired", "decayed", "approved", "published", "delisted"}

// LifecycleEvent is one append-only curation moment in a technique's life. It is the
// org-level counterpart to FeedbackEvent's per-interaction funnel, and is
// cohort-free by construction: a technique's lifecycle belongs to the whole
// organization, so a LifecycleEvent carries NO Segment, audit id, or session —
// nothing that could tie it to a member. Reason is a short human phrase for the
// feed ("fit 82% · n=14 verdicts"); it is model-free, assembled from counts the
// registry already holds.
type LifecycleEvent struct {
	EventID       string `json:"event_id"`
	TechniqueID   string `json:"technique_id"`
	TechniqueName string `json:"technique_name,omitempty"` // snapshot, so the feed reads without a technique join
	Kind          string `json:"kind"`                     // one of LifecycleKinds
	Provenance    string `json:"provenance,omitempty"`
	Reason        string `json:"reason,omitempty"` // why the registry acted, in counts it already has
	CreatedAt     string `json:"created_at"`
}

// DeterministicLifecycleEventID derives a stable id for a curation moment, so a
// recompute cycle that re-observes the same (technique, kind, instant) transition
// ingests it exactly once. Emission is already guarded to fire once per
// transition (a promoted technique leaves the shadow set; a decay event fires only on
// the onset), so createdAt distinguishes genuinely separate moments — a technique
// retired, re-observed, and retired again is two events.
func DeterministicLifecycleEventID(techniqueID, kind, createdAt string) string {
	sum := sha256.Sum256([]byte(techniqueID + "\x00" + kind + "\x00" + createdAt))
	return "lcy_" + hex.EncodeToString(sum[:16])
}

// Outcome is one derived rollup row: (technique, segment_key) -> funnel counts and
// rates.
//
// The integer counts are RAW event counts (what happened, for display and
// audit); the Weighted* fields carry the same funnel with each member-verdict
// event scaled by its confidence (inferred < explicit — the trust guardrail
// docs/delivery/low-intrusion-plan.md requires). Rates and ranking read the
// weighted funnel; the raw counts stay untouched so "n=120" always means 120
// real events.
type Outcome struct {
	TechniqueID       string   `json:"technique_id"`
	SegmentKey        string   `json:"segment_key"` // 'team:payments' | ... | '__overall__'
	Shown             int      `json:"shown"`
	Adopted           int      `json:"adopted"`
	Helped            int      `json:"helped"`
	Dismissed         int      `json:"dismissed"`
	WeightedAdopted   float64  `json:"weighted_adopted,omitempty"`
	WeightedHelped    float64  `json:"weighted_helped,omitempty"`
	WeightedDismissed float64  `json:"weighted_dismissed,omitempty"`
	AdoptionRate      *float64 `json:"adoption_rate"`
	HelpedRate        *float64 `json:"helped_rate"`
	SampleSize        int      `json:"sample_size"` // = floor(weighted adopted) — the trust denominator
	LastUpdated       string   `json:"last_updated"`
}

// Characterization is the audit layer's summary of one interaction — the
// input to /v1/evidence (schemas/characterization.schema.json).
//
// AuditID is optional but load-bearing when present: it is the join key
// between what the work WAS (this characterization, recorded as an audit fact)
// and how it turned OUT (the feedback events the same audit later emits, which
// already carry audit_id). Without it the registry can persist both halves and
// never connect them, which is what the learning layer is built on
// (docs/learning/synthesis-design.md). A producer that omits it still retrieves
// normally — the registry simply records no fact for that audit.
type Characterization struct {
	AuditID     string `json:"audit_id,omitempty"`
	SessionHash string `json:"session_hash,omitempty"`
	SummaryText string `json:"summary_text"`
	// Model is the model that served the audited interaction (e.g.
	// "claude-haiku-4-5"), when the capture source knows it — the transcript
	// names it on Claude Code and Codex. It feeds the per-model support matrix
	// and the org's own model benchmarking (docs/concepts/concepts.md), both of which
	// ran on an empty field until this existed. Like audit_id: optional on the
	// wire, unrecoverable if not captured at the time.
	Model                   string   `json:"model,omitempty"`
	TaskType                string   `json:"task_type,omitempty"`
	Domain                  string   `json:"domain,omitempty"`
	Modalities              []string `json:"modalities,omitempty"`
	ToolsUsed               []string `json:"tools_used,omitempty"`
	ToolsAbsent             []string `json:"tools_absent,omitempty"`
	InternalResourcesInPlay []string `json:"internal_resources_in_play,omitempty"`
	Harness                 string   `json:"harness,omitempty"`
	Surface                 string   `json:"surface,omitempty"`
	SkillLevel              string   `json:"skill_level,omitempty"`
	UsedTechniqueIDs        []string `json:"used_technique_ids,omitempty"`
	Segment                 Segment  `json:"segment,omitempty"`
}

// OutcomeSummary is the measured-outcome slice of one evidence candidate.
type OutcomeSummary struct {
	HelpedRate   *float64 `json:"helped_rate"`
	AdoptionRate *float64 `json:"adoption_rate"`
	SampleSize   int      `json:"sample_size"`
	Segment      string   `json:"segment"`
}

// Freshness carries a candidate's shipped date and a recently-shipped flag.
type Freshness struct {
	Shipped         string `json:"shipped,omitempty"`
	RecentlyShipped bool   `json:"recently_shipped"`
}

// EvidenceCandidate is one technique as presented by /v1/evidence.
type EvidenceCandidate struct {
	TechniqueID string          `json:"technique_id"`
	Name        string          `json:"name"`
	Scope       string          `json:"scope"`
	Recipe      string          `json:"recipe"`
	AppliesWhen string          `json:"applies_when,omitempty"`
	NotWhen     string          `json:"not_when,omitempty"`
	Support     string          `json:"support,omitempty"`
	Outcomes    *OutcomeSummary `json:"outcomes"`
	Freshness   Freshness       `json:"freshness"`
	// AutonomyEligible: the technique cleared the operator's evidence bar for
	// silent application in autonomous sessions (registry-computed;
	// docs/delivery/agent-delivery-plan.md Phase C). Absent/false means the
	// technique must be surfaced visibly, whoever the consumer is.
	AutonomyEligible bool `json:"autonomy_eligible,omitempty"`
	// Similarity is the cosine similarity between the query embedding and this
	// technique's embedding — the score that put the technique in the block at all. It
	// rides along so the consumer can record it on the resulting shown/declined
	// event: without it, the fit-check's verdicts cannot be regressed against
	// the retrieval score, and a similarity floor (config.MinSimilarity) has no
	// data to be calibrated from. Omitted when zero.
	Similarity float64 `json:"similarity,omitempty"`
}

// Cohort is the "people on your team" comparison for personalization.
type Cohort struct {
	Segment     string   `json:"segment"`
	Uses        int      `json:"uses"`
	CommonTotal int      `json:"common_total"`
	Examples    []string `json:"examples"`
}

// EvidenceMeta flags thin (cold-start) evidence.
type EvidenceMeta struct {
	Thin bool `json:"thin"`
}

// EvidenceBlock is the COLLECTIVE EVIDENCE contract
// (schemas/evidence-block.schema.json).
type EvidenceBlock struct {
	Candidates []EvidenceCandidate `json:"candidates"`
	Cohort     Cohort              `json:"cohort"`
	Meta       EvidenceMeta        `json:"meta"`
	// ShadowCandidates are techniques under evaluation (status=shadow) that matched
	// this context but must NEVER be surfaced to the member: they carry no
	// serving weight and are held out of Candidates by construction
	// (docs/learning/validation-without-review.md, the shadow rung). A consumer
	// that understands the field MAY fit-check these on turns it is already
	// fit-checking and record shadow relevance telemetry; a consumer that does
	// not simply ignores them, so a shadow technique can never reach an agent's
	// context. They have no measured outcomes and are ordered by similarity only.
	ShadowCandidates []EvidenceCandidate `json:"shadow_candidates,omitempty"`
}

// Message is one canonical-record message.
type Message struct {
	Role       string   `json:"role"`
	Text       string   `json:"text"`
	Modalities []string `json:"modalities,omitempty"`
}

// ToolCall is one canonical-record tool invocation.
type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

// CanonicalRecord is the source-agnostic interaction record — every capture
// source maps onto this one shape, so characterization and everything
// downstream never depend on which runtime produced the data
// (schemas/canonical-record.schema.json).
type CanonicalRecord struct {
	SchemaVersion           int        `json:"schema_version"`
	Source                  string     `json:"source"`
	SessionID               string     `json:"session_id,omitempty"`
	Harness                 string     `json:"harness,omitempty"`
	Surface                 string     `json:"surface,omitempty"`
	Model                   string     `json:"model,omitempty"`
	CreatedAt               string     `json:"created_at,omitempty"`
	Status                  string     `json:"status,omitempty"`
	Messages                []Message  `json:"messages"`
	ToolCalls               []ToolCall `json:"tool_calls"`
	CostUSD                 *float64   `json:"cost_usd,omitempty"`
	Tokens                  *int       `json:"tokens,omitempty"`
	Segment                 Segment    `json:"segment"`
	Actors                  []string   `json:"actors,omitempty"`
	InternalResourcesInPlay []string   `json:"internal_resources_in_play,omitempty"`
}

// AuditFactEnrichment carries the one thing about an interaction that cannot be
// observed: what would have HELPED and wasn't used.
//
// tools_used is a fact — the turn either called Edit or it didn't. tools_absent
// is a judgment, and the hook agent's characterizer makes none: it runs without
// an LLM by design, to stay off the same-turn latency budget. So the field sat
// empty, and with it the "you did it the hard way" signal that the whole
// diffusion-gap finding rests on (docs/learning/synthesis-design.md).
//
// The fix is not to guess it structurally — that would manufacture the very
// evidence the product exists to measure. It is to infer it with a model AFTER
// the turn is delivered, where latency costs nothing, and post it back keyed by
// audit_id.
//
// IT IS INFERRED, NOT OBSERVED. Consumers must not treat it as they treat
// tools_used: a finding conditioned on it inherits a model's opinion, and the
// three-claim discipline says to say so rather than launder it into a fact.
//
// An absent tools_absent means "no missed opportunity was identified" — either
// the model found none (the common case, and a correct answer) or it could not be
// asked. The two are not distinguished, which is tolerable only because the
// inference is attempted on every audit: a persistent gap means the model is
// down, and the LLM health signal says so.
type AuditFactEnrichment struct {
	AuditID     string   `json:"audit_id"`
	ToolsAbsent []string `json:"tools_absent,omitempty"`
}

// Sketch is one locally-distilled technique sketch — the ONLY artifact the
// opt-in mining channel emits from a member's machine
// (schemas/sketch.schema.json, docs/mining/mining-design.md source 2). It never
// contains transcript text: the hook agent distills, scrubs, and
// pseudonymizes at source. SessionHash is a per-org-salted hash so a miner
// can count distinct occurrences without knowing whose they were.
type Sketch struct {
	SketchID    string   `json:"sketch_id,omitempty"` // assigned at intake when empty
	SessionHash string   `json:"session_hash"`
	Trigger     string   `json:"trigger"` // one sentence: the situation the move fits
	Move        string   `json:"move"`    // the imperative recipe
	Tools       []string `json:"tools,omitempty"`
	TaskType    string   `json:"task_type,omitempty"`
	Harness     string   `json:"harness,omitempty"`
	Segment     Segment  `json:"segment,omitempty"`
	TechniqueID string   `json:"technique_id,omitempty"` // when the move relates to an existing technique
	CreatedAt   string   `json:"created_at,omitempty"`
}
