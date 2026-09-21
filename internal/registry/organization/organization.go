// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package organization composes registry analytics into an aggregate-only view
// of technique spread across declared organizational cohorts.
package organization

import (
	"sort"
	"time"

	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/models"
)

// Report is the complete, pure organization-view payload. It contains no event,
// audit, session, or member identifiers.
type Report struct {
	Overview      insights.Overview
	Dimensions    []Dimension
	Lens          string
	Areas         []Area
	Cohorts       []Cohort
	Cells         []Cell
	Spread        []Spread
	Opportunities []Opportunity
	Proponents    []Proponent
	Health        Health
}

// Dimension is one cohort dimension (team, role, …) with the values observed
// in the window.
type Dimension struct {
	Name   string
	Values []string
}

// Area is the stable, exclusive classification of techniques: each technique aggregates
// under exactly one area, as assigned by the caller's AreaOf.
type Area struct {
	Key, Label, Kind string
	Techniques       int
	Funnel           insights.Funnel
}

// Cohort is one dimension value's aggregate funnel across every area.
type Cohort struct {
	Dimension, Value string
	Funnel           insights.Funnel
}

// Cell is one cohort × area intersection: its funnel for the current and
// previous windows, plus the confidence mix behind the verdicts.
type Cell struct {
	Dimension, Cohort, Area string
	Current, Previous       insights.Funnel
	Explicit, Inferred      int
}

// Spread counts how many cohorts (under the selected lens) adopted an area in
// the current and previous windows, with the adoption totals behind them.
type Spread struct {
	Area              string
	Current, Previous int
	CurrentAdopted    int
	PreviousAdopted   int
}

// Opportunity is emitted only when the same area has demonstrated helped
// outcomes in another cohort and the target has actual exposure but a gap.
type Opportunity struct {
	Dimension, Cohort, Area string
	Target, Peer            insights.Funnel
	OrganizationSpecific    bool
}

// Proponent names an aggregate cohort where an area has established evidence.
type Proponent struct {
	Dimension, Cohort, Area string
	Funnel                  insights.Funnel
	Explicit, Inferred      int
}

// Health is the registry-hygiene summary panel: library counts, techniques
// that never land, the trust mix, and audit coverage.
type Health struct {
	Live, Drafts, New, Decayed int
	OrganizationTechniques     int
	ShownNeverAdopted          int
	Explicit, Inferred         int
	Audit                      AuditCoverage
}

// AuditCoverage reports how completely the window's audit facts are annotated.
type AuditCoverage struct {
	Total, WithSession, WithSegment, WithTaskType, WithContext int
}

var dimensionOrder = []string{"team", "role", "function", "domain", "harness", "surface", "model"}

// AreaOf maps a technique onto the area column it aggregates under in the
// cohort × area matrix. The web layer passes the Playbook map's cluster
// assignment, so every view of the report shares the map's vocabulary.
type AreaOf func(c models.Technique) (key, label, kind string)

// Compute builds an organization report for one insights window, grouping
// areas with the caller's AreaOf.
func Compute(techniques []models.Technique, events []models.FeedbackEvent, audits []models.AuditFact, now time.Time, w insights.Window, areaOf AreaOf, requestedLens ...string) Report {
	r := Report{Overview: insights.Compute(techniques, events, now, w)}
	areaByTechnique := make(map[string]string, len(techniques))
	areaIndex := map[string]int{}
	areaOrg := map[string]bool{}
	for _, technique := range techniques {
		switch technique.Status {
		case "draft":
			r.Health.Drafts++
		case "retired":
		default:
			r.Health.Live++
		}
		if technique.DecaySignal != 0 {
			r.Health.Decayed++
		}
		if technique.Status == "draft" || technique.Status == "retired" {
			continue
		}
		if technique.Scope == "org" {
			r.Health.OrganizationTechniques++
		}
		key, label, kind := areaOf(technique)
		areaByTechnique[technique.ID] = key
		if i, ok := areaIndex[key]; ok {
			r.Areas[i].Techniques++
		} else {
			areaIndex[key] = len(r.Areas)
			r.Areas = append(r.Areas, Area{Key: key, Label: label, Kind: kind, Techniques: 1})
		}
		if technique.Scope == "org" {
			areaOrg[key] = true
		}
	}
	r.Health.New = r.Overview.NewTechniques

	type cellKey struct{ dim, val, area string }
	cells := map[cellKey]*Cell{}
	values := map[string]map[string]bool{}
	confidence := map[cellKey][2]int{}
	for _, fact := range audits {
		t, ok := insights.ParseTime(fact.CreatedAt)
		if !ok || !in(t, w.Start, now) {
			continue
		}
		r.Health.Audit.Total++
		if fact.SessionHash != "" {
			r.Health.Audit.WithSession++
		}
		if len(fact.Segment) > 0 {
			r.Health.Audit.WithSegment++
		}
		if fact.TaskType != "" {
			r.Health.Audit.WithTaskType++
		}
		if fact.Domain != "" || fact.Harness != "" || fact.Surface != "" || fact.Model != "" {
			r.Health.Audit.WithContext++
		}
		for dim, val := range fact.Segment {
			if values[dim] == nil {
				values[dim] = map[string]bool{}
			}
			values[dim][val] = true
		}
	}
	for _, event := range events {
		t, ok := insights.ParseTime(event.CreatedAt)
		if !ok {
			continue
		}
		current := in(t, w.Start, now)
		previous := in(t, w.PrevStart, w.Start)
		if !current && !previous {
			continue
		}
		area, known := areaByTechnique[event.TechniqueID]
		if !known {
			continue
		}
		if current {
			i := areaIndex[area]
			add(&r.Areas[i].Funnel, event)
		}
		for dim, val := range event.Segment {
			if current {
				if values[dim] == nil {
					values[dim] = map[string]bool{}
				}
				values[dim][val] = true
			}
			k := cellKey{dim, val, area}
			cell := cells[k]
			if cell == nil {
				cell = &Cell{Dimension: dim, Cohort: val, Area: area}
				cells[k] = cell
			}
			if current {
				add(&cell.Current, event)
				if event.Stage == "adopted" || event.Stage == "helped" || event.Stage == "dismissed" {
					mix := confidence[k]
					if event.Confidence == "explicit" {
						mix[0]++
					} else {
						mix[1]++
					}
					confidence[k] = mix
				}
			} else {
				add(&cell.Previous, event)
			}
		}
	}

	r.Dimensions = makeDimensions(values)
	if len(r.Dimensions) > 0 {
		r.Lens = r.Dimensions[0].Name
		if len(requestedLens) > 0 {
			for _, dim := range r.Dimensions {
				if dim.Name == requestedLens[0] {
					r.Lens = requestedLens[0]
					break
				}
			}
		}
	}
	for _, dim := range r.Dimensions {
		for _, val := range dim.Values {
			cohort := Cohort{Dimension: dim.Name, Value: val}
			for k, cell := range cells {
				if k.dim == dim.Name && k.val == val {
					sum(&cohort.Funnel, cell.Current)
				}
			}
			r.Cohorts = append(r.Cohorts, cohort)
			for _, area := range r.Areas {
				k := cellKey{dim.Name, val, area.Key}
				if cells[k] == nil {
					cells[k] = &Cell{Dimension: dim.Name, Cohort: val, Area: area.Key}
				}
			}
		}
	}
	for _, cell := range cells {
		mix := confidence[cellKey{cell.Dimension, cell.Cohort, cell.Area}]
		cell.Explicit, cell.Inferred = mix[0], mix[1]
		r.Cells = append(r.Cells, *cell)
	}
	sort.Slice(r.Cells, func(i, j int) bool {
		a, b := r.Cells[i], r.Cells[j]
		if a.Dimension != b.Dimension {
			return dimensionLess(a.Dimension, b.Dimension)
		}
		if a.Cohort != b.Cohort {
			return a.Cohort < b.Cohort
		}
		return a.Area < b.Area
	})
	sort.Slice(r.Areas, func(i, j int) bool { return r.Areas[i].Key < r.Areas[j].Key })

	// Recommendations use the selected lens only. Sparse aggregate evidence is
	// shown at full fidelity; denominators let the reader judge its strength.
	for _, area := range r.Areas {
		spread := Spread{Area: area.Key}
		var peer insights.Funnel
		for _, cell := range r.Cells {
			if cell.Dimension != r.Lens || cell.Area != area.Key {
				continue
			}
			if cell.Current.Adopted > 0 {
				spread.Current++
			}
			if cell.Previous.Adopted > 0 {
				spread.Previous++
			}
			spread.CurrentAdopted += cell.Current.Adopted
			spread.PreviousAdopted += cell.Previous.Adopted
			if cell.Current.Adopted > 0 && cell.Current.Helped > 0 {
				sum(&peer, cell.Current)
				mix := confidence[cellKey{cell.Dimension, cell.Cohort, cell.Area}]
				r.Proponents = append(r.Proponents, Proponent{Dimension: cell.Dimension, Cohort: cell.Cohort, Area: cell.Area, Funnel: cell.Current, Explicit: mix[0], Inferred: mix[1]})
			}
		}
		r.Spread = append(r.Spread, spread)
		if peer.Adopted == 0 || peer.Helped == 0 {
			continue
		}
		for _, cell := range r.Cells {
			if cell.Dimension != r.Lens || cell.Area != area.Key || cell.Current.Shown == 0 {
				continue
			}
			if cell.Current.Adopted*peer.Shown >= peer.Adopted*cell.Current.Shown {
				continue
			}
			r.Opportunities = append(r.Opportunities, Opportunity{Dimension: cell.Dimension, Cohort: cell.Cohort, Area: area.Key, Target: cell.Current, Peer: peer, OrganizationSpecific: areaOrg[area.Key]})
		}
	}

	for _, stat := range r.Overview.Techniques {
		if stat.Technique.Status != "draft" && stat.Technique.Status != "retired" && stat.Funnel.Shown > 0 && stat.Funnel.Adopted == 0 {
			r.Health.ShownNeverAdopted++
		}
	}
	r.Health.Explicit, r.Health.Inferred = r.Overview.TrustExplicit, r.Overview.TrustInferred
	return r
}

func makeDimensions(values map[string]map[string]bool) []Dimension {
	var out []Dimension
	for name, set := range values {
		d := Dimension{Name: name}
		for value := range set {
			d.Values = append(d.Values, value)
		}
		sort.Strings(d.Values)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return dimensionLess(out[i].Name, out[j].Name) })
	return out
}

func dimensionLess(a, b string) bool {
	rank := func(s string) int {
		for i, v := range dimensionOrder {
			if s == v {
				return i
			}
		}
		return len(dimensionOrder)
	}
	ra, rb := rank(a), rank(b)
	if ra != rb {
		return ra < rb
	}
	return a < b
}

func in(t, start, end time.Time) bool { return !t.Before(start) && t.Before(end) }

// add and sum deliberately count raw stages, bypassing insights.Funnel's
// confidence weighting: the org report shows literal event counts, and the
// denominators beside them are what lets a reader judge the evidence.
func add(f *insights.Funnel, e models.FeedbackEvent) {
	switch e.Stage {
	case "shown":
		f.Shown++
	case "adopted":
		f.Adopted++
	case "helped":
		// The answer, not the event: see FeedbackEvent.CountsAsHelped.
		if e.CountsAsHelped() {
			f.Helped++
		}
	case "dismissed":
		f.Dismissed++
	case "declined":
		f.Declined++
	}
}
func sum(dst *insights.Funnel, src insights.Funnel) {
	dst.Shown += src.Shown
	dst.Adopted += src.Adopted
	dst.Helped += src.Helped
	dst.Dismissed += src.Dismissed
	dst.Declined += src.Declined
}
