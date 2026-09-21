// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package demo generates and loads a month of believable, organization-specific
// OpenTacit usage into a running registry — a "demonstration driver".
//
// The design splits cleanly in two:
//
//   - CONTENT (this package's Dataset) is a portable, Tacit-independent JSON
//     document that describes one artificial organization: who it is, its teams
//     and cohorts, the techniques its people discovered, and a compact
//     usage model. It is authored in natural language by an LLM (generate.go)
//     or shipped pre-generated (datasets/*.json), and can be re-loaded any time.
//   - EXPANSION (synth.go) turns that compact model into a month of backdated
//     feedback events deterministically from a seed, so the same Dataset always
//     produces the same month of history. The LLM never authors event streams —
//     that would be slow, costly, and no more realistic than a seeded model.
//
// The loader (load.go) drives a live registry over its public HTTP API exactly
// as a real org would: it contributes and promotes the techniques, posts
// the synthesized funnel events (shown → adopted → helped, plus dismissals),
// and recomputes the rollups. Nothing here reaches into the registry's storage.
package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/slug"
)

// DatasetSchema is the version of the portable dataset format. Bump it on a
// breaking change to the shape so a stale cached file fails loudly.
const DatasetSchema = 1

// Dataset is one artificial organization's complete demonstration content — the
// portable, registry-independent document that generate.go writes and load.go
// reads. It carries no synthesized events: those are derived from Usage by a
// seeded expansion, so the file stays small and re-loads identically.
type Dataset struct {
	Schema      int         `json:"schema"`
	Scenario    string      `json:"scenario"`
	GeneratedBy string      `json:"generated_by,omitempty"` // model id, or "curated"
	Seed        int64       `json:"seed"`
	Org         Org         `json:"org"`
	Window      Window      `json:"window"`
	Teams       []Team      `json:"teams"`
	Roles       []Weighted  `json:"roles"`     // default role mix within a team
	Harnesses   []Weighted  `json:"harnesses"` // AI harness mix across the org
	Techniques  []Technique `json:"techniques"`
	Usage       Usage       `json:"usage"`
}

// Org is the artificial organization's identity — surfaced nowhere in the
// registry itself (which stores no org profile), but it anchors the generated
// content and is printed by the loader so a viewer knows who they're looking at.
type Org struct {
	Name        string `json:"name"`
	Industry    string `json:"industry"`
	Tagline     string `json:"tagline,omitempty"`
	Description string `json:"description,omitempty"`
}

// Window is the month the demo simulates. Events are backdated across it.
type Window struct {
	StartDate string `json:"start_date"` // YYYY-MM-DD, the first day
	Days      int    `json:"days"`
}

// Start parses the window's first day at UTC midnight.
func (w Window) Start() (time.Time, error) {
	return time.Parse("2006-01-02", w.StartDate)
}

// RebaseWindowTo shifts the window so its last simulated day is the day
// before `day`, keeping its length. A dataset's authored start date makes it
// reproducible, but it also rots: a month generated in May is invisible to a
// "last 30 days" dashboard opened in July. Rebasing before synthesis keeps
// every derived event deterministic for a given day while the demo always
// shows a current month. Content is untouched — only the anchor moves.
func (d *Dataset) RebaseWindowTo(day time.Time) {
	d.Window.StartDate = day.UTC().AddDate(0, 0, -d.Window.Days).Format("2006-01-02")
}

// Team is a coherent unit of people: it fixes the function/domain segment
// dimensions so generated members never land in incongruous cohorts
// (e.g. a payments engineer tagged as an ML domain). Size is how many of its
// people use AI harnesses. Maturity (0..1) is the team's propensity to adopt
// early — it shifts the team's members earlier on the org-wide adoption curve.
type Team struct {
	ID        string     `json:"id"` // segment team value, e.g. "payments"
	Label     string     `json:"label"`
	Function  string     `json:"function"` // segment function value
	Domain    string     `json:"domain"`   // segment domain value
	Size      int        `json:"size"`
	Maturity  float64    `json:"maturity"`
	Roles     []Weighted `json:"roles,omitempty"`     // overrides the dataset default mix
	Harnesses []Weighted `json:"harnesses,omitempty"` // overrides the org harness mix
}

// Weighted is a segment value with a relative population share.
type Weighted struct {
	Value  string  `json:"value"`
	Label  string  `json:"label,omitempty"`
	Weight float64 `json:"weight"`
}

// Technique is one technique plus the parameters that drive how its
// usage plays out over the month. The technique fields (Name…Shipped) map straight
// onto a /v1/contribute body; the rest govern synthesis and never leave this
// package.
type Technique struct {
	// Technique fields (contributed verbatim).
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scope       string   `json:"scope"` // "org" (the majority) or "general"
	Tags        []string `json:"tags,omitempty"`
	TaskTypes   []string `json:"task_types,omitempty"`
	Triggers    []any    `json:"triggers,omitempty"`
	AppliesWhen string   `json:"applies_when,omitempty"`
	NotWhen     string   `json:"not_when,omitempty"`
	Recipe      string   `json:"recipe"`
	BeforeAfter string   `json:"before_after,omitempty"`
	Shipped     string   `json:"shipped,omitempty"` // YYYY-MM

	// Synthesis parameters.
	Draft         bool     `json:"draft,omitempty"`        // stays a draft (never promoted) → drafts lane
	IntroducedDay int      `json:"introduced_day"`         // first day within the window it is retrieved
	Popularity    float64  `json:"popularity"`             // 0..1 relative show frequency
	StrongTeams   []string `json:"strong_teams,omitempty"` // high adoption + helped here (proponents)
	WeakTeams     []string `json:"weak_teams,omitempty"`   // shown but lagging here (opportunities)
	Roles         []string `json:"roles,omitempty"`        // role affinity, optional
	AdoptionRate  float64  `json:"adoption_rate"`          // base adopted/shown among an affine cohort
	HelpedRate    float64  `json:"helped_rate"`            // base helped/adopted among an affine cohort
	Decays        bool     `json:"decays,omitempty"`       // helped rate collapses in the final fortnight
}

// Usage carries the org-wide knobs that shape volume and the funnel. Defaults
// (applied by normalize) aim at a lively but believable month.
type Usage struct {
	BaseSessionsPerActiveDay float64 `json:"base_sessions_per_active_day"`
	GrowthFactor             float64 `json:"growth_factor"`      // end/start multiplier on activity
	ShowRate                 float64 `json:"show_rate"`          // prob a session surfaces a technique
	DismissRate              float64 `json:"dismiss_rate"`       // of shown-but-not-adopted, the share dismissed
	DeclineRate              float64 `json:"decline_rate"`       // off-funnel retrieval telemetry, per shown
	InferredShare            float64 `json:"inferred_share"`     // share of events with inferred (vs explicit) confidence
	AdoptionMidpoint         float64 `json:"adoption_midpoint"`  // 0..1 of window where half the org has joined
	AdoptionSteepness        float64 `json:"adoption_steepness"` // logistic steepness of the join curve
	MCPShare                 float64 `json:"mcp_share"`          // share of members whose surface is mcp (else cli)
}

// LoadDataset reads and validates a dataset JSON file.
func LoadDataset(path string) (*Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseDataset(raw)
}

// ParseDataset decodes and validates a dataset from raw JSON.
func ParseDataset(raw []byte) (*Dataset, error) {
	var d Dataset
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("dataset JSON: %w", err)
	}
	d.normalize()
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// Save writes the dataset as indented JSON, creating parent directories.
func (d *Dataset) Save(path string) error {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// normalize fills defaults so a hand- or LLM-authored file can omit the usage
// knobs and the format version and still load sensibly.
func (d *Dataset) normalize() {
	if d.Schema == 0 {
		d.Schema = DatasetSchema
	}
	if d.Seed == 0 {
		d.Seed = 20260601 // any fixed value: keeps an un-seeded dataset reproducible
	}
	u := &d.Usage
	setf(&u.BaseSessionsPerActiveDay, 3.0)
	setf(&u.GrowthFactor, 2.2)
	setf(&u.ShowRate, 0.55)
	setf(&u.DismissRate, 0.16)
	setf(&u.DeclineRate, 0.05)
	setf(&u.InferredShare, 0.7)
	setf(&u.AdoptionMidpoint, 0.42)
	setf(&u.AdoptionSteepness, 9.0)
	setf(&u.MCPShare, 0.18)
	for i := range d.Techniques {
		c := &d.Techniques[i]
		if c.Scope == "" {
			c.Scope = "org"
		}
		if c.Popularity == 0 {
			c.Popularity = 0.5
		}
		if c.AdoptionRate == 0 {
			c.AdoptionRate = 0.55
		}
		if c.HelpedRate == 0 {
			c.HelpedRate = 0.7
		}
	}
}

func setf(p *float64, def float64) {
	if *p == 0 {
		*p = def
	}
}

// Validate checks the invariants the loader and synth rely on.
func (d *Dataset) Validate() error {
	if d.Schema != DatasetSchema {
		return fmt.Errorf("unsupported dataset schema %d (want %d)", d.Schema, DatasetSchema)
	}
	if strings.TrimSpace(d.Scenario) == "" {
		return fmt.Errorf("scenario is required")
	}
	if _, err := d.Window.Start(); err != nil {
		return fmt.Errorf("window.start_date %q: %w", d.Window.StartDate, err)
	}
	if d.Window.Days < 7 {
		return fmt.Errorf("window.days must be at least 7, got %d", d.Window.Days)
	}
	if len(d.Teams) == 0 {
		return fmt.Errorf("at least one team is required")
	}
	teamIDs := map[string]bool{}
	for _, t := range d.Teams {
		if t.ID == "" || t.Function == "" || t.Domain == "" {
			return fmt.Errorf("team %q needs id, function and domain", t.ID)
		}
		if t.Size <= 0 {
			return fmt.Errorf("team %q needs a positive size", t.ID)
		}
		teamIDs[t.ID] = true
	}
	if len(d.Techniques) == 0 {
		return fmt.Errorf("at least one technique is required")
	}
	ids := map[string]bool{}
	for i := range d.Techniques {
		c := &d.Techniques[i]
		if c.ID == "" {
			c.ID = Slugify(c.Name)
		}
		if !validID(c.ID) {
			return fmt.Errorf("technique id %q must match [a-z0-9][a-z0-9/_@-]*", c.ID)
		}
		if ids[c.ID] {
			return fmt.Errorf("duplicate technique id %q", c.ID)
		}
		ids[c.ID] = true
		if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Recipe) == "" {
			return fmt.Errorf("technique %q needs a name and a recipe", c.ID)
		}
		if c.Scope != "org" && c.Scope != "general" {
			return fmt.Errorf("technique %q scope must be org or general", c.ID)
		}
		if c.IntroducedDay < 0 || c.IntroducedDay >= d.Window.Days {
			return fmt.Errorf("technique %q introduced_day %d out of range 0..%d", c.ID, c.IntroducedDay, d.Window.Days-1)
		}
		for _, tm := range append(append([]string{}, c.StrongTeams...), c.WeakTeams...) {
			if !teamIDs[tm] {
				return fmt.Errorf("technique %q references unknown team %q", c.ID, tm)
			}
		}
	}
	return nil
}

// Slugify turns a name into a technique id: lowercase, non-alphanumerics to hyphens,
// collapsed and trimmed. Matches the id charset the registry accepts.
func Slugify(s string) string { return slug.MakeMax(s, 80) }

func validID(id string) bool {
	if id == "" || len(id) > 80 {
		return false
	}
	for i, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if i > 0 {
			ok = ok || r == '/' || r == '_' || r == '@' || r == '-'
		}
		if !ok {
			return false
		}
	}
	return true
}

// sortedTeamIDs returns the team ids in a stable order (synthesis determinism).
func (d *Dataset) sortedTeamIDs() []string {
	ids := make([]string, 0, len(d.Teams))
	for _, t := range d.Teams {
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	return ids
}
