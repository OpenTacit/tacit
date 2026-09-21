// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"sort"
	"time"

	"github.com/opentacit/tacit/pkg/contracts"
)

// Member is one synthesized person: a fixed cohort (the six segment
// dimensions), the day they started using AI at work, and an engagement factor.
// No member identity ever reaches the registry — the member exists only to give
// each event a coherent segment and a plausible activity level.
type Member struct {
	ID                                             string
	Team, Role, Function, Domain, Harness, Surface string
	JoinDay                                        int
	Engagement                                     float64
}

// Segment renders the member's cohort as a registry segment map.
func (m Member) Segment() contracts.Segment {
	return contracts.Segment{
		"team":     m.Team,
		"role":     m.Role,
		"function": m.Function,
		"domain":   m.Domain,
		"harness":  m.Harness,
		"surface":  m.Surface,
	}
}

// SynthResult is a dataset expanded into a concrete month of usage.
type SynthResult struct {
	Members []Member
	Events  []contracts.FeedbackEventDraft
}

// Stats summarizes a synthesis for the loader's report.
type Stats struct {
	Members, Shown, Adopted, Helped, Dismissed, Declined int
	OrgTechniques, GeneralTechniques, Drafts             int
}

// Synthesize expands a dataset into members and backdated feedback events. It is
// fully deterministic in the dataset's seed: the same dataset always yields the
// same history, and audit ids are stable so a re-load is idempotent.
func Synthesize(d *Dataset) (*SynthResult, error) {
	start, err := d.Window.Start()
	if err != nil {
		return nil, err
	}
	rng := rand.New(rand.NewSource(d.Seed))
	members := buildMembers(d, rng)

	// Index techniques that can actually be shown (promoted, i.e. non-draft),
	// keeping the dataset order stable for deterministic weighted selection.
	var shown []*Technique
	for i := range d.Techniques {
		if !d.Techniques[i].Draft {
			shown = append(shown, &d.Techniques[i])
		}
	}

	res := &SynthResult{Members: members}
	u := d.Usage
	lastDay := d.Window.Days - 1

	for _, m := range members {
		for day := m.JoinDay; day < d.Window.Days; day++ {
			date := start.AddDate(0, 0, day)
			prog := 0.0
			if lastDay > 0 {
				prog = float64(day) / float64(lastDay)
			}
			// Activity grows over the month (familiarity) and dips on weekends.
			activity := u.BaseSessionsPerActiveDay * m.Engagement * lerp(1, u.GrowthFactor, prog)
			switch date.Weekday() {
			case time.Saturday, time.Sunday:
				activity *= 0.28
			}
			// Newer members ramp up over their first few days.
			tenure := float64(day-m.JoinDay) + 1
			activity *= clamp(0.45+0.18*tenure, 0.45, 1.0)
			sessions := poisson(rng, activity)

			for s := range sessions {
				if rng.Float64() > u.ShowRate {
					continue // no technique surfaced this session
				}
				tech := pickTechnique(rng, shown, m, day, d.Window.Days)
				if tech == nil {
					continue
				}
				auditID := fmt.Sprintf("aud_demo_%s_d%02d_s%02d", m.ID, day, s)
				res.emitInteraction(rng, d, tech, m, day, prog, date, auditID)
			}
			// Off-funnel retrieval telemetry: a candidate fit-checked and rejected
			// before being shown. Independent of the shown funnel by construction.
			if rng.Float64() < u.DeclineRate && len(shown) > 0 {
				tech := shown[rng.Intn(len(shown))]
				if tech.IntroducedDay <= day {
					res.Events = append(res.Events, contracts.FeedbackEventDraft{
						AuditID:     fmt.Sprintf("aud_demo_%s_d%02d_decl", m.ID, day),
						TechniqueID: tech.ID,
						Stage:       "declined",
						Segment:     m.Segment(),
						TaskType:    taskType(rng, tech),
						Confidence:  "inferred",
						CreatedAt:   stamp(date, rng, 0),
					})
				}
			}
		}
	}
	sort.SliceStable(res.Events, func(i, j int) bool {
		return res.Events[i].CreatedAt < res.Events[j].CreatedAt
	})
	return res, nil
}

// emitInteraction runs one shown→adopted→helped (or dismissed) funnel for a
// surfaced technique and appends its events, all sharing one audit id.
func (res *SynthResult) emitInteraction(rng *rand.Rand, d *Dataset, tech *Technique, m Member, day int, prog float64, date time.Time, auditID string) {
	u := d.Usage
	seg := m.Segment()
	tt := taskType(rng, tech)
	conf := "explicit"
	if rng.Float64() < u.InferredShare {
		conf = "inferred"
	}
	rank := weightedRank(rng)
	base := contracts.FeedbackEventDraft{
		AuditID:     auditID,
		TechniqueID: tech.ID,
		Segment:     seg,
		TaskType:    tt,
		Confidence:  conf,
		RankShown:   rank,
	}

	shownEv := base
	shownEv.Stage = "shown"
	shownEv.CreatedAt = stamp(date, rng, 0)
	res.Events = append(res.Events, shownEv)

	// Adoption rises with familiarity over the month and with cohort affinity.
	// Affinity drives which techniques a cohort SEES strongly (selection), so
	// its pull on the adoption decision is compressed toward 1 — otherwise a
	// strong-fit cohort would adopt nearly everything shown, which reads as
	// implausibly high adoption.
	adoptMod := adoptBoost(tech, m) * clamp(0.7+0.4*prog, 0.7, 1.1)
	adoptP := clamp(tech.AdoptionRate*adoptMod, 0.02, 0.9)
	if rng.Float64() >= adoptP {
		// Not adopted. Some fraction is an explicit dismissal with a reason.
		if rng.Float64() < u.DismissRate {
			dis := base
			dis.Stage = "dismissed"
			dis.Value = contracts.DismissReasons[rng.Intn(len(contracts.DismissReasons))]
			dis.CreatedAt = stamp(date, rng, 2)
			res.Events = append(res.Events, dis)
		}
		return
	}
	adopt := base
	adopt.Stage = "adopted"
	adopt.CreatedAt = stamp(date, rng, 3)
	res.Events = append(res.Events, adopt)

	helpedP := clamp(tech.HelpedRate*helpedMod(tech, m), 0.05, 0.98)
	// A decaying technique's value collapses while people keep adopting it — the
	// shape the registry's decay detector watches. The collapse spans slightly
	// more than the detector's recent window (config.DecayWindowDays = 14) so the
	// whole recent slice reads as degraded, not a blend with healthy days.
	if tech.Decays && day >= d.Window.Days-15 {
		helpedP *= 0.12
	}
	if rng.Float64() < helpedP {
		helped := base
		helped.Stage = "helped"
		helped.Value = true
		helped.CreatedAt = stamp(date, rng, 6)
		res.Events = append(res.Events, helped)
	}
}

// buildMembers materializes the org's people from its teams, assigning each a
// coherent cohort and a join day drawn from the org-wide adoption S-curve
// (shifted earlier for higher-maturity teams). Deterministic given the seed.
func buildMembers(d *Dataset, rng *rand.Rand) []Member {
	teamByID := map[string]Team{}
	for _, t := range d.Teams {
		teamByID[t.ID] = t
	}
	var members []Member
	for _, id := range d.sortedTeamIDs() {
		t := teamByID[id]
		roles := t.Roles
		if len(roles) == 0 {
			roles = d.Roles
		}
		harnesses := t.Harnesses
		if len(harnesses) == 0 {
			harnesses = d.Harnesses
		}
		mid := clamp(d.Usage.AdoptionMidpoint-(t.Maturity-0.5)*0.5, 0.04, 0.94)
		for i := 0; i < t.Size; i++ {
			u := rng.Float64()
			// Invert the logistic CDF to place this member on the join curve.
			x := mid + math.Log(u/(1-u))/d.Usage.AdoptionSteepness
			join := int(math.Round(clamp(x, 0, 0.98) * float64(d.Window.Days-1)))
			surface := "cli"
			if rng.Float64() < d.Usage.MCPShare {
				surface = "mcp"
			}
			members = append(members, Member{
				ID:         fmt.Sprintf("%s-%02d", t.ID, i+1),
				Team:       t.ID,
				Role:       pickWeighted(rng, roles, "engineer"),
				Function:   t.Function,
				Domain:     t.Domain,
				Harness:    pickWeighted(rng, harnesses, "claude-code"),
				Surface:    surface,
				JoinDay:    join,
				Engagement: clamp(0.6+rng.NormFloat64()*0.25, 0.3, 1.6),
			})
		}
	}
	return members
}

// pickTechnique chooses a surfaced technique for this member on this day,
// weighted by popularity and cohort affinity, among those already introduced.
func pickTechnique(rng *rand.Rand, techs []*Technique, m Member, day, days int) *Technique {
	var (
		total   float64
		elig    []*Technique
		weights []float64
	)
	for _, tech := range techs {
		if tech.IntroducedDay > day {
			continue
		}
		// Popularity drives selection with a super-linear curve (no flat floor), so
		// a few hero techniques dominate the shows and the rest form a long tail
		// of small nodes — the skewed distribution a real registry has, and the one
		// the technique map renders readably (node size = adoption).
		w := math.Pow(tech.Popularity, 1.4) * affinity(tech, m) * decayVolume(tech, day, days)
		if w <= 0 {
			continue
		}
		elig = append(elig, tech)
		weights = append(weights, w)
		total += w
	}
	if total == 0 {
		return nil
	}
	r := rng.Float64() * total
	for i, tech := range elig {
		r -= weights[i]
		if r <= 0 {
			return tech
		}
	}
	return elig[len(elig)-1]
}

// affinity is how strongly this member's cohort is drawn to a technique:
// strong teams pull harder, weak teams lag (the seed of the org view's
// "opportunity" signal), and a stated role affinity narrows it further.
func affinity(tech *Technique, m Member) float64 {
	a := 1.0
	if slices.Contains(tech.StrongTeams, m.Team) {
		a *= 1.9
	}
	if slices.Contains(tech.WeakTeams, m.Team) {
		a *= 0.55
	}
	if len(tech.Roles) > 0 {
		if slices.Contains(tech.Roles, m.Role) {
			a *= 1.5
		} else {
			a *= 0.5
		}
	}
	return a
}

// decayVolume ramps a deprecated (decaying) technique's exposure down over the
// back half of the window. A deprecated practice is not just less helpful but
// less reached-for, so its late volume shrinks — which keeps its healthy early
// outcomes dominant in the all-time baseline the decay detector compares against,
// so the late collapse in helped rate actually trips the signal.
func decayVolume(tech *Technique, day, days int) float64 {
	if !tech.Decays || days < 2 {
		return 1
	}
	prog := float64(day) / float64(days-1)
	if prog < 0.5 {
		return 1
	}
	return clamp(1-1.6*(prog-0.5), 0.2, 1)
}

// adoptBoost is affinity's compressed influence on the adoption decision — the
// same direction as selection affinity but a third of the swing, so strong-fit
// cohorts adopt more without adopting nearly everything they're shown.
func adoptBoost(tech *Technique, m Member) float64 {
	return 1 + (affinity(tech, m)-1)*0.35
}

// helpedMod nudges the helped rate up where a technique is a strong fit.
func helpedMod(tech *Technique, m Member) float64 {
	if slices.Contains(tech.StrongTeams, m.Team) {
		return 1.12
	}
	if slices.Contains(tech.WeakTeams, m.Team) {
		return 0.9
	}
	return 1.0
}

func taskType(rng *rand.Rand, tech *Technique) string {
	if len(tech.TaskTypes) == 0 {
		return ""
	}
	return tech.TaskTypes[rng.Intn(len(tech.TaskTypes))]
}

// weightedRank returns a shown rank 1..4 skewed toward the top slot.
func weightedRank(rng *rand.Rand) int {
	switch r := rng.Float64(); {
	case r < 0.5:
		return 1
	case r < 0.78:
		return 2
	case r < 0.92:
		return 3
	default:
		return 4
	}
}

// stamp places an event within a working day, offsetMin minutes after the base
// moment so a funnel's stages stay ordered (shown < adopted < helped).
func stamp(date time.Time, rng *rand.Rand, offsetMin int) string {
	base := time.Date(date.Year(), date.Month(), date.Day(), 8, 0, 0, 0, time.UTC)
	withinDay := time.Duration(rng.Intn(11*60)) * time.Minute // 08:00–19:00
	t := base.Add(withinDay).Add(time.Duration(offsetMin)*time.Minute + time.Duration(rng.Intn(60))*time.Second)
	return t.UTC().Format(time.RFC3339)
}

// poisson draws a non-negative session count with mean lambda (Knuth).
func poisson(rng *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	l := math.Exp(-lambda)
	k, p := 0, 1.0
	for {
		k++
		p *= rng.Float64()
		if p <= l {
			return k - 1
		}
	}
}

func pickWeighted(rng *rand.Rand, ws []Weighted, fallback string) string {
	if len(ws) == 0 {
		return fallback
	}
	var total float64
	for _, w := range ws {
		total += w.Weight
	}
	if total <= 0 {
		return ws[rng.Intn(len(ws))].Value
	}
	r := rng.Float64() * total
	for _, w := range ws {
		r -= w.Weight
		if r <= 0 {
			return w.Value
		}
	}
	return ws[len(ws)-1].Value
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
