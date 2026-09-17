// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package models defines the registry's data contracts and their validation.
//
// Contracts match docs/design/design.md (mirrored from the schemas/ markdown): the
// Technique, the append-only FeedbackEvent, the derived Outcome rollup,
// the Characterization input to retrieval, and the EvidenceBlock the audit
// prompt consumes. The auditor package mirrors the wire shapes independently
// in internal/auditor/contracts (the two layers talk HTTP and deploy
// separately).
package models

import (
	"crypto/rand"

	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/screen"
	"github.com/opentacit/tacit/pkg/contracts"

	"github.com/opentacit/tacit/internal/slug"
)

// The wire types are defined once, publicly, in pkg/contracts (the standard
// any implementation builds against — see docs/design/api-standard.md); this package
// aliases them and adds the registry's server-side validation.

// Stages, DismissReasons, and TechniqueScopes re-export the contract vocabularies.
var (
	Stages          = contracts.Stages
	DismissReasons  = contracts.DismissReasons
	TechniqueScopes = contracts.TechniqueScopes
	LifecycleKinds  = contracts.LifecycleKinds
)

// Aliases to the public wire types (identical types, one definition).
type (
	Segment           = contracts.Segment
	Technique         = contracts.Technique
	FederationOrigin  = contracts.FederationOrigin
	FeedbackEvent     = contracts.FeedbackEvent
	Outcome           = contracts.Outcome
	Characterization  = contracts.Characterization
	OutcomeSummary    = contracts.OutcomeSummary
	Freshness         = contracts.Freshness
	EvidenceCandidate = contracts.EvidenceCandidate
	Cohort            = contracts.Cohort
	EvidenceMeta      = contracts.EvidenceMeta
	Sketch            = contracts.Sketch
	EvidenceBlock     = contracts.EvidenceBlock
	LifecycleEvent    = contracts.LifecycleEvent

	// AuditFactEnrichment IS a wire type (the agent posts it), unlike AuditFact
	// itself, which the registry derives server-side and exchanges with nobody.
	AuditFactEnrichment = contracts.AuditFactEnrichment
)

// ParseAuditFactEnrichment validates the /v1/audit-facts/enrich request body.
func ParseAuditFactEnrichment(body map[string]any) (AuditFactEnrichment, error) {
	id := strings.TrimSpace(str(body, "audit_id"))
	if id == "" {
		return AuditFactEnrichment{}, ValidationErrorf("audit_id is required")
	}
	return AuditFactEnrichment{
		AuditID:     id,
		ToolsAbsent: strList(body, "tools_absent"),
	}, nil
}

// AuditFact records what one interaction WAS, keyed by audit_id — the join to
// the feedback events that same audit later emits, which record how it turned
// OUT (docs/learning/synthesis-design.md, docs/learning/synthesis-plan.md). Retrieval receives
// all of this on every /v1/evidence call and, before this type existed, threw
// it away; most of the learning layer's questions are about the relationship
// between context and outcome, and the context half was being discarded at the
// door.
//
// This is deliberately NOT a wire type. It is derived server-side from a
// Characterization (which is one) and is exchanged with nobody, so it lives
// here rather than in pkg/contracts and pays none of the standard's contract
// tax. If something ever needs to serve it, it graduates then.
//
// THE PRIVACY LINE: there is no SummaryText field, and there must never be one.
// summary_text is the single free-text, transcript-derived field on a
// Characterization; it is used to build a query vector and then dropped. Every
// field below is enumerable and cohort-level, which is what keeps "no
// transcripts persisted" literally true. A struct with no free-text field
// cannot leak free text — that is the point of the shape.
type AuditFact struct {
	AuditID     string   `json:"audit_id"`
	SessionHash string   `json:"session_hash,omitempty"`
	CreatedAt   string   `json:"created_at"`
	Segment     Segment  `json:"segment,omitempty"`
	TaskType    string   `json:"task_type,omitempty"`
	Model       string   `json:"model,omitempty"` // the model that served the interaction
	Domain      string   `json:"domain,omitempty"`
	Harness     string   `json:"harness,omitempty"`
	Surface     string   `json:"surface,omitempty"`
	SkillLevel  string   `json:"skill_level,omitempty"`
	ToolsUsed   []string `json:"tools_used,omitempty"`
	ToolsAbsent []string `json:"tools_absent,omitempty"`
	Resources   []string `json:"resources_in_play,omitempty"`
	// TechniquesOffered is what RETRIEVAL returned, in rank order — NOT what the
	// member saw. The registry cannot know what was shown: the agent fit-checks
	// these candidates afterwards and drops the ones that don't fit (that is what
	// a `declined` event records, and 13 of them exist). Treating this as
	// exposure would overstate it by however many the fit-check rejected, and
	// silently inflate the denominator of every rate derived from it. What was
	// actually shown lives in the event log, as stage=shown.
	TechniquesOffered []string `json:"techniques_offered,omitempty"`
	Thin              bool     `json:"thin"` // the cold-start flag retrieval computed
}

// AuditFactFrom derives the fact from a characterization and the evidence the
// registry just built for it. Returns ok=false when the producer sent no
// audit_id: without the join key the fact is unusable, so we record nothing
// rather than accumulate orphans.
func AuditFactFrom(ch Characterization, ev EvidenceBlock, now time.Time) (AuditFact, bool) {
	if strings.TrimSpace(ch.AuditID) == "" {
		return AuditFact{}, false
	}
	offered := make([]string, 0, len(ev.Candidates))
	for _, c := range ev.Candidates {
		offered = append(offered, c.TechniqueID)
	}
	return AuditFact{
		AuditID:           ch.AuditID,
		SessionHash:       ch.SessionHash,
		CreatedAt:         now.UTC().Format(time.RFC3339Nano),
		Segment:           ch.Segment,
		TaskType:          ch.TaskType,
		Model:             ch.Model,
		Domain:            ch.Domain,
		Harness:           ch.Harness,
		Surface:           ch.Surface,
		SkillLevel:        ch.SkillLevel,
		ToolsUsed:         ch.ToolsUsed,
		ToolsAbsent:       ch.ToolsAbsent,
		Resources:         ch.InternalResourcesInPlay,
		TechniquesOffered: offered,
		Thin:              ev.Meta.Thin,
	}, true
}

// ValidationError reports a request body that fails the contract.
type ValidationError struct{ msg string }

// Error implements the error interface.
func (e *ValidationError) Error() string { return e.msg }

// ValidationErrorf builds a ValidationError — used here for contract
// violations, and by callers outside this package (contribute, web) that
// reject a request without a schema violation.
func ValidationErrorf(format string, args ...any) error {
	return &ValidationError{msg: fmt.Sprintf(format, args...)}
}

// ApplyTechniqueEdit applies a partial edit onto a technique. The get callback returns
// (value, present) per field name; absent fields are left alone. This is THE
// reviewable field set — the reviewer edit surfaces and the revision flow
// (docs/design/revision-design.md) share it, so a field is either editable
// everywhere or nowhere.
func ApplyTechniqueEdit(technique *Technique, get func(string) (string, bool)) (changed bool) {
	set := func(dst *string, key string) {
		if v, ok := get(key); ok && strings.TrimSpace(v) != *dst {
			*dst = strings.TrimSpace(v)
			changed = true
		}
	}
	set(&technique.Name, "name")
	set(&technique.Description, "description")
	set(&technique.Recipe, "recipe")
	set(&technique.AppliesWhen, "applies_when")
	set(&technique.NotWhen, "not_when")
	set(&technique.BeforeAfter, "before_after")
	if v, ok := get("tags"); ok {
		var tags []string
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
		// Only a different list counts as a change. The edit form posts the
		// whole tag set back on every save, so treating a present "tags" key
		// as a change made every no-op save archive a new version.
		if !slices.Equal(tags, technique.Tags) {
			technique.Tags = tags
			changed = true
		}
	}
	return changed
}

// ApplyTechniqueEditFromMap is ApplyTechniqueEdit over a decoded JSON body; tags may
// arrive as a JSON array and are normalized to the comma form.
func ApplyTechniqueEditFromMap(technique *Technique, body map[string]any) bool {
	if raw, ok := body["tags"].([]any); ok {
		var parts []string
		for _, t := range raw {
			if s, ok := t.(string); ok {
				parts = append(parts, s)
			}
		}
		body["tags"] = strings.Join(parts, ",")
	}
	return ApplyTechniqueEdit(technique, func(key string) (string, bool) {
		v, present := body[key]
		if !present {
			return "", false
		}
		s, _ := v.(string)
		return s, true
	})
}

// Now returns the current UTC time in the ISO-8601 form used throughout.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// NewID returns a random hex id with the given prefix (evt_/aud_...).
func NewID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

var (
	sessionHashRe = regexp.MustCompile(`^[a-f0-9]{16,64}$`)
)

// Slugify converts free text into a kebab-case technique id.
func Slugify(text string) string {
	if s := slug.Make(text); s != "" {
		return s
	}
	// A technique id is never empty: a name of nothing but punctuation still
	// has to land somewhere, and the dedupe loop suffixes from here.
	return "technique"
}

func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func strList(m map[string]any, key string) []string {
	out := []string{}
	if vs, ok := m[key].([]any); ok {
		for _, v := range vs {
			out = append(out, fmt.Sprint(v))
		}
	}
	return out
}

// firstList returns the first key that carries a non-empty list — for wire
// fields whose key was renamed but whose old-producer traffic still arrives.
func firstList(m map[string]any, keys ...string) []string {
	for _, key := range keys {
		if out := strList(m, key); len(out) > 0 {
			return out
		}
	}
	return []string{}
}

func segMap(v any) (Segment, bool) {
	if v == nil {
		return Segment{}, true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	seg := Segment{}
	for k, val := range m {
		seg[k] = fmt.Sprint(val)
	}
	return seg, true
}

// ParseTechniqueContribution validates a member-contributed technique
// (POST /v1/contribute). Contributions always enter as status=draft,
// provenance=contributed — held out of retrieval until a reviewer promotes
// them, so one member's draft can't affect colleagues before review. The
// contributor's cohort is preserved in Source for provenance.
//
// Two steps: parse the body into a Technique, then put that Technique through
// the safety screen. Splitting them keeps the parse pure and makes the screen
// a step you can see.
func ParseTechniqueContribution(body map[string]any) (Technique, error) {
	technique, err := parseTechniqueContribution(body)
	if err != nil {
		return Technique{}, err
	}
	if err := screenTechnique(technique); err != nil {
		return Technique{}, err
	}
	return technique, nil
}

// screenTechnique runs the automated safety screen
// (docs/learning/validation-without-review.md, D3): the deterministic gate that
// lets contributions enter without a human reading each one. Contributed
// techniques are the adversary-plausible lane (one member's technique reaches
// every colleague's agent), so a High finding — prompt-injection,
// exfiltration, an embedded secret — hard-rejects here rather than landing a
// hostile draft for a reviewer to spot.
func screenTechnique(technique Technique) error {
	fs := screen.Scan(screen.Input{
		Name:        technique.Name,
		Description: technique.Description,
		Recipe:      technique.Recipe,
		Extra:       technique.AppliesWhen + "\n" + technique.NotWhen + "\n" + technique.BeforeAfter,
	})
	if screen.Blocks(fs) {
		return ValidationErrorf("rejected by the safety screen: %s", screen.Summary(fs))
	}
	return nil
}

// parseTechniqueContribution builds the Technique the body asks for: required
// fields, vocabulary checks, defaults, and the derived id and Source. It reads
// the body and nothing else.
func parseTechniqueContribution(body map[string]any) (Technique, error) {
	for _, field := range []string{"name", "description", "recipe"} {
		if strings.TrimSpace(str(body, field)) == "" {
			return Technique{}, ValidationErrorf("%s is required", field)
		}
	}
	scope := str(body, "scope")
	if scope == "" {
		scope = "general"
	}
	if !slices.Contains(TechniqueScopes, scope) {
		return Technique{}, ValidationErrorf("scope must be one of %v", TechniqueScopes)
	}
	seg, ok := segMap(body["segment"])
	if !ok {
		return Technique{}, ValidationErrorf("segment must be an object")
	}
	segParts := make([]string, 0, len(seg))
	for _, k := range sortedKeys(seg) {
		segParts = append(segParts, k+"="+seg[k])
	}
	// Provenance defaults to contributed. Two others are allowed so the paths
	// that are not a member typing into a form label their drafts honestly:
	// "suggested" for the research pass (agent researches with its own web
	// access, submits through /v1/contribute), and "observed" for a convention
	// read out of the organization's own repository by `tacit init`. Both carry
	// a source the reviewer can go and read.
	provenance := "contributed"
	switch p := str(body, "provenance"); p {
	case "suggested", "observed":
		provenance = p
	}
	source := provenance
	if len(segParts) > 0 {
		source += " (" + strings.Join(segParts, ", ") + ")"
	}
	if src := strings.TrimSpace(str(body, "source_url")); src != "" && provenance != "contributed" {
		source = src
	}
	id := str(body, "id")
	if id == "" {
		id = Slugify(str(body, "name"))
	}
	if len(id) > 80 {
		id = id[:80]
	}
	now := Now()
	triggers, _ := body["triggers"].([]any)
	var sm []map[string]any
	if vs, ok := body["support_matrix"].([]any); ok {
		for _, v := range vs {
			if m, ok := v.(map[string]any); ok {
				sm = append(sm, m)
			}
		}
	}
	return Technique{
		ID:            id,
		Name:          strings.TrimSpace(str(body, "name")),
		Description:   strings.TrimSpace(str(body, "description")),
		Scope:         scope,
		Status:        "draft",
		Provenance:    provenance,
		Version:       1,
		Recipe:        strings.TrimSpace(str(body, "recipe")),
		BeforeAfter:   str(body, "before_after"),
		Tags:          strList(body, "tags"),
		TaskTypes:     strList(body, "task_types"),
		Triggers:      triggers,
		AppliesWhen:   str(body, "applies_when"),
		NotWhen:       str(body, "not_when"),
		SupportMatrix: sm,
		Shipped:       str(body, "shipped"),
		Source:        source,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// ParseCharacterization validates the /v1/evidence request body.
func ParseCharacterization(body map[string]any) (Characterization, error) {
	summary, _ := body["summary_text"].(string)
	if strings.TrimSpace(summary) == "" {
		return Characterization{}, ValidationErrorf("summary_text is required")
	}
	sessionHash := str(body, "session_hash")
	if sessionHash != "" && !sessionHashRe.MatchString(sessionHash) {
		return Characterization{}, ValidationErrorf("session_hash must be 16-64 lowercase hex characters")
	}
	seg, ok := segMap(body["segment"])
	if !ok {
		return Characterization{}, ValidationErrorf("segment must be an object")
	}
	return Characterization{
		AuditID:                 str(body, "audit_id"),
		SessionHash:             sessionHash,
		SummaryText:             summary,
		Model:                   str(body, "model"),
		TaskType:                str(body, "task_type"),
		Domain:                  str(body, "domain"),
		Modalities:              strList(body, "modalities"),
		ToolsUsed:               strList(body, "tools_used"),
		ToolsAbsent:             strList(body, "tools_absent"),
		InternalResourcesInPlay: strList(body, "internal_resources_in_play"),
		Harness:                 str(body, "harness"),
		Surface:                 str(body, "surface"),
		SkillLevel:              str(body, "skill_level"),
		UsedTechniqueIDs:        firstList(body, "used_technique_ids", "used_capability_ids"),
		Segment:                 seg,
	}, nil
}

// ParseFeedbackEvent validates one /v1/feedback event body.
func ParseFeedbackEvent(body map[string]any) (FeedbackEvent, error) {
	cap := str(body, "technique_id")
	if cap == "" {
		// Producers built before the technique rename still post the old key;
		// their events are as real as anyone's.
		cap = str(body, "capability_id")
	}
	if cap == "" {
		return FeedbackEvent{}, ValidationErrorf("technique_id is required")
	}
	stage := str(body, "stage")
	if !slices.Contains(Stages, stage) {
		return FeedbackEvent{}, ValidationErrorf("stage must be one of %v", Stages)
	}
	if stage == "dismissed" {
		if reason, ok := body["value"].(string); ok && reason != "" && !slices.Contains(DismissReasons, reason) {
			return FeedbackEvent{}, ValidationErrorf("dismissed value must be one of %v", DismissReasons)
		}
	}
	seg, ok := segMap(body["segment"])
	if !ok {
		return FeedbackEvent{}, ValidationErrorf("segment must be an object")
	}
	eventID := str(body, "event_id")
	auditID := str(body, "audit_id")
	if eventID == "" && auditID != "" {
		// Audit-linked outcome: derive the id so replays and overlapping
		// stage pairs (a standalone 'adopted' + the pair 'helped' implies)
		// ingest once (contracts.DeterministicFeedbackEventID).
		eventID = contracts.DeterministicFeedbackEventID(auditID, cap, stage)
	}
	if eventID == "" {
		eventID = NewID("evt_")
	}
	if auditID == "" {
		auditID = NewID("aud_")
	}
	confidence := str(body, "confidence")
	if confidence == "" {
		confidence = "explicit"
	}
	createdAt := str(body, "created_at")
	if createdAt == "" {
		createdAt = Now()
	}
	rank := 0
	if r, ok := body["rank_shown"].(float64); ok {
		rank = int(r)
	}
	capVer := 0
	if v, ok := body["technique_version"].(float64); ok {
		capVer = int(v)
	} else if v, ok := body["capability_version"].(float64); ok {
		capVer = int(v)
	}
	delivery := str(body, "delivery")
	if delivery != "" && delivery != "suggested" && delivery != "applied" {
		return FeedbackEvent{}, ValidationErrorf("delivery must be suggested or applied")
	}
	source := str(body, "source")
	if source != "" && source != contracts.SourceAmbient && source != contracts.SourcePull {
		return FeedbackEvent{}, ValidationErrorf("source must be %s or %s",
			contracts.SourceAmbient, contracts.SourcePull)
	}
	// Similarity is advisory telemetry, not a funnel input: a producer that
	// omits it (or sends junk) costs the calibration one sample and must not
	// lose the member's event, so it is read leniently and never validated.
	similarity, _ := body["similarity"].(float64)
	return FeedbackEvent{
		EventID:          eventID,
		AuditID:          auditID,
		TechniqueID:      cap,
		TechniqueVersion: capVer,
		Stage:            stage,
		Value:            body["value"],
		Segment:          seg,
		TaskType:         str(body, "task_type"),
		RankShown:        rank,
		Confidence:       confidence,
		Delivery:         delivery,
		Source:           source,
		Similarity:       similarity,
		CreatedAt:        createdAt,
	}, nil
}

func sortedKeys(m Segment) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MemberKey is one minted access credential — a member's machine, named at
// join time. It is the unit of access management (list, revoke), NOT of
// analytics: feedback events are never attributed to a key, so "who has
// access" stays answerable while "how is each person performing" stays
// unaskable by construction. Only the secret's hash is stored; the secret
// itself is shown once at mint time and never again.
type MemberKey struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Hash      string `json:"hash"` // hex sha256 of the secret
	CreatedAt string `json:"created_at"`
	LastSeen  string `json:"last_seen,omitempty"`
	RevokedAt string `json:"revoked_at,omitempty"` // non-empty = disallowed
}

// FeedToken is one consumer's credential for reading a non-public federation
// channel (docs/distribution/global-access-plan.md). It follows MemberKey
// exactly — hash only, shown once at mint, listed and revocable — and adds the
// channel scope, so one token can cover a consortium reading several channels.
//
// It is deliberately a SEPARATE namespace from MemberKey and the org root key.
// keyOK accepts the root key or any minted member key, which is the credential
// every member's machine carries and which opens the whole JSON API: handing that
// to a peer organization so it can read one channel is the wrong blast radius by
// an order of magnitude. So a feed token authenticates nothing but the federation
// surface, and a member key authenticates no gated feed.
//
// Like MemberKey it is the unit of access management — list, revoke, "is this
// still in use?" — and NOT of analytics. A per-consumer token is precisely the
// object someone will later be tempted to build per-consumer usage reporting on,
// and the identity boundary says no: LastUsed exists so an operator can retire a
// dead credential, not so anyone can profile a peer.
type FeedToken struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`    // who it was minted for
	Hash      string   `json:"hash"`     // hex sha256 of the secret
	Channels  []string `json:"channels"` // channels this token may read
	CreatedAt string   `json:"created_at"`
	LastUsed  string   `json:"last_used,omitempty"`
	RevokedAt string   `json:"revoked_at,omitempty"` // non-empty = disallowed
}

// Allows reports whether this token may read a channel.
func (t FeedToken) Allows(channel string) bool {
	if t.RevokedAt != "" {
		return false
	}
	for _, ch := range t.Channels {
		if ch == channel {
			return true
		}
	}
	return false
}
