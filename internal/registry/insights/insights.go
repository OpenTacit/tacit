// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package insights computes the registry's Outcomes data: windowed funnels,
// adoption velocity, freshness, cohort spread, trust and dismissal mixes, and
// time-bucketed series. All derive from the append-only feedback event log and
// remain aggregate-or-cohort only (the registry holds no user identity by
// design).
//
// Pure functions over techniques + events, so every metric is unit-testable
// without a store or a clock.
package insights

import (
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/windows"
	"github.com/opentacit/tacit/pkg/contracts"
)

// Window is the time slice that scopes a whole dashboard view.
type Window struct {
	Key       string        // "7d" | "30d" | "90d" | "all"
	Label     string        // "Last 30 days"
	Start     time.Time     // zero for "all"
	PrevStart time.Time     // start of the previous equal window (velocity baseline)
	Bucket    time.Duration // series bucket size (day or week)
}

// windowLabels and windowBuckets are what the dashboard adds on top of the
// shared preset: a heading and a series bucket size. The keys and their lengths
// come from internal/windows, so this table cannot drift from the MCP schema or
// the member's usage log.
var (
	windowLabels  = map[string]string{"7d": "Last 7 days", "30d": "Last 30 days", "90d": "Last 90 days", "all": "All time"}
	windowBuckets = map[string]time.Duration{"7d": 24 * time.Hour, "30d": 24 * time.Hour, "90d": 7 * 24 * time.Hour, "all": 7 * 24 * time.Hour}
)

// Windows returns the preset windows, resolved against now and the earliest
// known event (which anchors "all").
func Windows(now, earliest time.Time) []Window {
	day := 24 * time.Hour
	allStart := earliest
	if allStart.IsZero() || now.Sub(allStart) < 14*day {
		allStart = now.Add(-90 * day) // young registries still get a readable axis
	}
	out := make([]Window, 0, len(windows.Keys))
	for _, key := range windows.Keys {
		w := Window{Key: key, Label: windowLabels[key], Bucket: windowBuckets[key]}
		// A preset of length d compares against the equal window before it.
		// "all" has no length, so it is its own baseline.
		if d := windows.Duration(key); d > 0 {
			w.Start, w.PrevStart = now.Add(-d), now.Add(-2*d)
		} else {
			w.Start, w.PrevStart = allStart, allStart
		}
		out = append(out, w)
	}
	return out
}

// WindowByKey picks a preset, falling back to the shared default.
func WindowByKey(key string, now, earliest time.Time) Window {
	all := Windows(now, earliest)
	for _, w := range all {
		if w.Key == key {
			return w
		}
	}
	for _, w := range all {
		if w.Key == windows.Default {
			return w
		}
	}
	return all[0]
}

// Funnel is the stage counts for one slice. Declined is off the shown→adopted
// →helped funnel: it counts fit-check rejections (retrieved candidates the
// synthesizer judged a non-fit, never shown), so it drives DeclineRate but
// never the adoption/helped rates.
//
// Mixed receivers are deliberate: only add mutates, so only add takes a
// pointer receiver.
type Funnel struct {
	Shown, Adopted, Helped, Dismissed, Declined int

	// The ambient-only shown/declined pair. DeclineRate is a ratio between two
	// outcomes of ONE mechanism — the unprompted fit-check burst — so it must
	// not see a technique a member pulled up deliberately. Mixing the paths reads as
	// a retrieval-quality collapse when it is really just an ambient funnel
	// (which rejects most of what it judges, by design) averaged with a pull
	// funnel (which rarely rejects anything).
	ambientShown, ambientDeclined int

	// The confidence-weighted verdicts (inferred scaled by
	// config.InferredWeight), feeding shrunk() so the leaderboards score with
	// the same trust discount ranking applies. Display counts stay raw.
	wAdopted, wHelped, wDismissed float64
}

// ambient reports whether an event came out of a fit-check burst.
//
// Events written before FeedbackEvent.Source existed carry no source, so they
// are classified from the signature the historical producers left behind: the
// ambient hook always set task_type (it characterizes the turn before
// retrieving) at inferred confidence, tacit_search wrote batches with neither,
// and the @tacit answer set task_type but marked its shown EXPLICIT because
// the member pulled. Retained rather than dropped so the pre-floor decline
// rate stays comparable with the post-floor one — the baseline is the whole
// point of measuring. New events never reach the fallback.
func ambient(e models.FeedbackEvent) bool {
	switch e.Source {
	case contracts.SourceAmbient:
		return true
	case contracts.SourcePull:
		return false
	}
	return e.TaskType != "" && e.Confidence != "explicit"
}

func (f *Funnel) add(e models.FeedbackEvent) {
	w := 1.0
	switch e.Confidence {
	case "inferred", "verification":
		// Verification verdicts (autonomous sessions, agent-delivery-plan
		// Phase B) score at the inferred discount until the Signal-trust
		// view shows the class has earned more.
		w = config.InferredWeight
	}
	switch e.Stage {
	case "shown":
		f.Shown++
		if ambient(e) {
			f.ambientShown++
		}
	case "adopted":
		f.Adopted++
		f.wAdopted += w
	case "helped":
		// A member answering "no, it did not help" writes helped:false. Counting
		// the event rather than its answer would make every answer a yes.
		if !e.CountsAsHelped() {
			break
		}
		f.Helped++
		f.wHelped += w
	case "dismissed":
		f.Dismissed++
		f.wDismissed += w
	case "declined":
		f.Declined++
		if ambient(e) {
			f.ambientDeclined++
		}
	}
}

// HelpedRate returns helped/adopted, ok=false when unmeasured.
func (f Funnel) HelpedRate() (float64, bool) {
	if f.Adopted == 0 {
		return 0, false
	}
	return float64(f.Helped) / float64(f.Adopted), true
}

// DeclineRate returns declined/(declined+shown) over the AMBIENT path only: of
// the candidates the unprompted fit-check judged, the fraction it rejected.
// ok=false when no ambient candidate has yet reached a verdict.
//
// Read it as "how often does a turn have nothing worth interrupting for", not
// as retrieval accuracy. While config.MinSimilarity is 0 there is no relevance
// floor in front of the fit-check, so retrieval proposes its top candidates on
// every turn however weakly they match and the fit-check declines whatever
// does not fit — which it should. A high rate here is the expected shape of
// that arrangement, not evidence of a bad embedder; what a bad embedder looks
// like is a high rate at rank 1 specifically (see DeclineRateSpark's callers).
func (f Funnel) DeclineRate() (float64, bool) {
	total := f.ambientDeclined + f.ambientShown
	if total == 0 {
		return 0, false
	}
	return float64(f.ambientDeclined) / float64(total), true
}

// DeclineSample is the ambient shown+declined count DeclineRate is measured
// over — the n a reader needs to know how much to trust it. It is smaller than
// Declined+Shown whenever members pulled techniques up themselves.
func (f Funnel) DeclineSample() int { return f.ambientDeclined + f.ambientShown }

// ShadowFit is one shadow technique's relevance telemetry
// (docs/learning/validation-without-review.md): how often the fit-check, judging
// it in real contexts where it was never surfaced, accepted it as a fit. It is
// the evidence an operator promotes (or rejects) a shadow technique on, gathered with
// zero member exposure.
type ShadowFit struct {
	Shown    int // shadow_shown: judged a fit — would have surfaced
	Declined int // shadow_declined: judged a non-fit
}

// Judged is the number of fit-check verdicts this shadow technique has drawn.
func (s ShadowFit) Judged() int { return s.Shown + s.Declined }

// Rate returns shown/(shown+declined): of the times it was judged in context,
// the fraction it fit. ok=false when it has not been judged yet.
func (s ShadowFit) Rate() (float64, bool) {
	if s.Judged() == 0 {
		return 0, false
	}
	return float64(s.Shown) / float64(s.Judged()), true
}

// ShadowFits aggregates shadow relevance telemetry per technique from the event
// log. These stages are off every funnel, so this is the only place they are
// read — Funnel.add deliberately ignores them.
func ShadowFits(events []models.FeedbackEvent) map[string]ShadowFit {
	out := map[string]ShadowFit{}
	for _, e := range events {
		f := out[e.TechniqueID]
		switch e.Stage {
		case "shadow_shown":
			f.Shown++
		case "shadow_declined":
			f.Declined++
		default:
			continue
		}
		out[e.TechniqueID] = f
	}
	return out
}

// SessionShape is a recurring workflow shape: a collapsed phase sequence and how
// widely it recurs (docs/learning/workflow-technique-capture.md). Reconstructed from
// audit facts — task_types ordered by time — aggregate-only, never content.
type SessionShape struct {
	Phases   []string
	Sessions int // distinct sessions with this shape
	Cohorts  int // widest distinct-value spread over any one cohort dimension
}

// SessionTraces reconstructs each session's ordered phase trace from audit facts
// and returns the shapes recurring in at least minSessions sessions, most common
// first. Consecutive repeats collapse, so a longer session with the same rhythm
// (edit → verify → edit → verify) counts with the shorter one that shares it.
// This is the empirical "how members work" view and the substrate a workflow-technique
// producer clusters over.
func SessionTraces(facts []models.AuditFact, minSessions int) []SessionShape {
	bySession := map[string][]models.AuditFact{}
	for _, f := range facts {
		if f.SessionHash == "" || f.TaskType == "" {
			continue
		}
		bySession[f.SessionHash] = append(bySession[f.SessionHash], f)
	}
	type agg struct {
		sessions map[string]bool
		perDim   map[string]map[string]bool
	}
	shapes := map[string]*agg{}
	for sh, fs := range bySession {
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].CreatedAt < fs[j].CreatedAt })
		var collapsed []string
		for _, f := range fs {
			if len(collapsed) == 0 || collapsed[len(collapsed)-1] != f.TaskType {
				collapsed = append(collapsed, f.TaskType)
			}
		}
		key := strings.Join(collapsed, " → ")
		a := shapes[key]
		if a == nil {
			a = &agg{sessions: map[string]bool{}, perDim: map[string]map[string]bool{}}
			shapes[key] = a
		}
		a.sessions[sh] = true
		for _, dim := range config.SegmentDimensions {
			if v := fs[0].Segment[dim]; v != "" {
				if a.perDim[dim] == nil {
					a.perDim[dim] = map[string]bool{}
				}
				a.perDim[dim][v] = true
			}
		}
	}
	var out []SessionShape
	for key, a := range shapes {
		if len(a.sessions) < minSessions {
			continue
		}
		cohorts := 0
		for _, vals := range a.perDim {
			if len(vals) > cohorts {
				cohorts = len(vals)
			}
		}
		out = append(out, SessionShape{Phases: strings.Split(key, " → "), Sessions: len(a.sessions), Cohorts: cohorts})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return len(out[i].Phases) < len(out[j].Phases)
	})
	return out
}

// Bucket is one time slice of a series.
type Bucket struct {
	Start  time.Time
	Funnel Funnel
}

// TechniqueStat is one technique's windowed standing.
type TechniqueStat struct {
	Technique   models.Technique
	Funnel      Funnel
	PrevAdopted int     // adoptions in the previous equal window
	Score       float64 // Bayesian-shrunk helped rate (window)
	New         bool    // technique created inside the window
	NewlyActive bool    // first-ever event inside the window
	FirstEvent  time.Time
}

// Velocity is the adoption delta vs the previous window.
func (c TechniqueStat) Velocity() int { return c.Funnel.Adopted - c.PrevAdopted }

// MixEntry is one labelled count (cohorts, dismissal reasons, trust).
type MixEntry struct {
	Label string
	Count int
}

// Overview is everything the landing page renders.
type Overview struct {
	Window        Window
	Now           time.Time
	Funnel        Funnel
	PrevFunnel    Funnel
	Buckets       []Bucket
	AdoptionSpark []int // fixed 12-point sparkline of adoptions across the window
	ShownSpark    []int // fixed 12-point sparkline of suggestions shown
	// HelpedRateSpark is a fixed 12-point CUMULATIVE helped-rate trend (integer
	// percent): rate = cumulative helped / cumulative adopted up to each point.
	// Cumulative, not per-bucket, because the signal is sparse — per-bucket rates
	// would read as noise dipping to zero on every quiet bucket.
	HelpedRateSpark []int
	// DeclineRateSpark is the same cumulative treatment for the fit-check decline
	// rate: cumulative declined / (declined + shown).
	DeclineRateSpark  []int
	Techniques        []TechniqueStat
	Fastest           []TechniqueStat // adopted>0, by velocity then adoption
	Best              []TechniqueStat // measured, by shrunk score
	Fresh             []TechniqueStat // new or newly-active, most recent first
	Cohorts           []MixEntry      // top segment values by adoptions
	ActiveCohorts     int             // distinct segment values seen in the window
	Dismissals        []MixEntry      // reason mix
	TrustExplicit     int             // explicit-confidence reactions/adoptions
	TrustInferred     int
	TrustVerification int // verification-confidence (autonomous sessions)
	Decayed           []models.Technique
	LiveTechniques    int
	NewTechniques     int

	// The org-scoped share: how much of the registry — and of what is
	// DELIVERED and ADOPTED — is org-specific knowledge, and where it came
	// from. Techniques: live-technique counts. Shown/Adopted: event counts in
	// the window joined to the technique's scope/provenance ("unknown" when
	// the technique no longer exists).
	TechniquesByScope      []MixEntry
	TechniquesByProvenance []MixEntry
	ShownByScope           []MixEntry
	AdoptedByScope         []MixEntry
	AdoptedByProvenance    []MixEntry
}

// OrgShare returns the org-scoped fraction of a mix (0 when unmeasured).
func OrgShare(mix []MixEntry) (share float64, n int) {
	org, total := 0, 0
	for _, m := range mix {
		total += m.Count
		if m.Label == "org" {
			org += m.Count
		}
	}
	if total == 0 {
		return 0, 0
	}
	return float64(org) / float64(total), total
}

// TechniqueDetail is the drill-down payload.
type TechniqueDetail struct {
	Stat       TechniqueStat
	PrevFunnel Funnel
	// RecentFunnel counts the last config.DecayWindowDays regardless of the
	// page window — it's the slice the decay check judges, so the decay figures
	// read the same numbers DetectDecay does.
	RecentFunnel Funnel
	Buckets      []Bucket
	Cumulative   []Bucket // running adoption total per bucket, from first event
	Cohorts      []MixEntry
	Dismissals   []MixEntry
}

// ParseTime parses an event/technique timestamp, accepting RFC3339 with or
// without fractional seconds. Shared with the organization report so both
// views count exactly the same events.
func ParseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Earliest returns the oldest event time (zero if none parse).
func Earliest(events []models.FeedbackEvent) time.Time {
	var earliest time.Time
	for _, e := range events {
		if t, ok := ParseTime(e.CreatedAt); ok && (earliest.IsZero() || t.Before(earliest)) {
			earliest = t
		}
	}
	return earliest
}

func inWindow(t time.Time, start, end time.Time) bool {
	return !t.Before(start) && t.Before(end)
}

func buckets(start, now time.Time, size time.Duration) []Bucket {
	var out []Bucket
	for t := start; t.Before(now); t = t.Add(size) {
		out = append(out, Bucket{Start: t})
	}
	if len(out) == 0 {
		out = []Bucket{{Start: start}}
	}
	return out
}

func bucketIndex(bs []Bucket, t time.Time, size time.Duration) int {
	if len(bs) == 0 || t.Before(bs[0].Start) {
		return -1
	}
	i := int(t.Sub(bs[0].Start) / size)
	if i >= len(bs) {
		i = len(bs) - 1 // clock skew right at "now": clamp into the last bucket
	}
	return i
}

// statSet accumulates every technique's windowed standing while the caller walks
// the event log. Compute folds its overview-only aggregates into the same walk;
// techniqueStats walks on its own. One accumulator, so a drill-down and the
// overview can never disagree about a technique's funnel.
type statSet struct {
	now  time.Time
	w    Window
	byID map[string]*TechniqueStat
}

// newStatSet seeds one stat per technique, flagging the techniques created inside
// the window (the only fact that comes from the technique rather than the events).
func newStatSet(techniques []models.Technique, now time.Time, w Window) *statSet {
	s := &statSet{now: now, w: w, byID: make(map[string]*TechniqueStat, len(techniques))}
	for _, c := range techniques {
		cs := &TechniqueStat{Technique: c}
		if t, ok := ParseTime(c.CreatedAt); ok && inWindow(t, w.Start, now) {
			cs.New = true
		}
		s.byID[c.ID] = cs
	}
	return s
}

// observe folds one event into its technique's stats. It returns that stat (nil
// when the event names a technique the registry no longer holds), the parsed
// event time, and ok=false when the timestamp does not parse — an event the
// caller must skip as well.
func (s *statSet) observe(e models.FeedbackEvent) (cs *TechniqueStat, t time.Time, ok bool) {
	t, ok = ParseTime(e.CreatedAt)
	if !ok {
		return nil, t, false
	}
	cs = s.byID[e.TechniqueID]
	if cs == nil {
		return nil, t, true
	}
	if cs.FirstEvent.IsZero() || t.Before(cs.FirstEvent) {
		cs.FirstEvent = t
	}
	if inWindow(t, s.w.PrevStart, s.w.Start) && e.Stage == "adopted" {
		cs.PrevAdopted++
	}
	if inWindow(t, s.w.Start, s.now) {
		cs.Funnel.add(e)
	}
	return cs, t, true
}

// all closes the walk: it scores each technique, marks the ones whose first-ever
// event fell inside the window, and returns them ordered by id.
func (s *statSet) all() []TechniqueStat {
	out := make([]TechniqueStat, 0, len(s.byID))
	for _, cs := range s.byID {
		if !cs.FirstEvent.IsZero() && inWindow(cs.FirstEvent, s.w.Start, s.now) {
			cs.NewlyActive = true
		}
		cs.Score = shrunk(cs.Funnel)
		out = append(out, *cs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Technique.ID < out[j].Technique.ID })
	return out
}

// techniqueStats returns every technique's windowed standing in one pass over the
// event log. It is the shared core: the overview composes its aggregates on top,
// and the drill-downs filter and sort it without paying for any of them.
func techniqueStats(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window) []TechniqueStat {
	s := newStatSet(techniques, now, w)
	for _, e := range events {
		s.observe(e)
	}
	return s.all()
}

// Compute builds the overview for a window.
func Compute(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window) Overview {
	o := Overview{Window: w, Now: now, Buckets: buckets(w.Start, now, w.Bucket),
		AdoptionSpark: make([]int, 12), ShownSpark: make([]int, 12)}
	helpedSpark := make([]int, 12)       // per-bucket helped, folded into the cumulative rate below
	declinedSpark := make([]int, 12)     // per-bucket ambient declined, likewise
	ambientShownSpark := make([]int, 12) // per-bucket ambient shown: DeclineRateSpark's denominator

	// The per-technique stats ride along in the event walk below; the counts here
	// are the ones that read the technique list alone.
	stats := newStatSet(techniques, now, w)
	for _, c := range techniques {
		if c.Status != "draft" && c.Status != "retired" {
			o.LiveTechniques++
			o.TechniquesByScope = upsertMix(o.TechniquesByScope, OrLabel(c.Scope, "general"))
			o.TechniquesByProvenance = upsertMix(o.TechniquesByProvenance, OrLabel(c.Provenance, "curated"))
		}
		if c.DecaySignal != 0 {
			o.Decayed = append(o.Decayed, c)
		}
	}

	sparkSize := now.Sub(w.Start) / 12
	if sparkSize <= 0 {
		sparkSize = time.Hour
	}
	cohorts := map[string]int{}
	active := map[string]bool{}

	for _, e := range events {
		cs, t, ok := stats.observe(e)
		if !ok {
			continue
		}

		if inWindow(t, w.PrevStart, w.Start) {
			o.PrevFunnel.add(e)
		}
		if !inWindow(t, w.Start, now) {
			continue
		}

		o.Funnel.add(e)
		scope, provenance := "unknown", "unknown"
		if cs != nil {
			scope = OrLabel(cs.Technique.Scope, "general")
			provenance = OrLabel(cs.Technique.Provenance, "curated")
		}
		switch e.Stage {
		case "shown":
			o.ShownByScope = upsertMix(o.ShownByScope, scope)
		case "adopted":
			o.AdoptedByScope = upsertMix(o.AdoptedByScope, scope)
			o.AdoptedByProvenance = upsertMix(o.AdoptedByProvenance, provenance)
		}
		if i := bucketIndex(o.Buckets, t, w.Bucket); i >= 0 {
			o.Buckets[i].Funnel.add(e)
		}
		if i := int(t.Sub(w.Start) / sparkSize); i >= 0 {
			if i > 11 {
				i = 11
			}
			switch e.Stage {
			case "adopted":
				o.AdoptionSpark[i]++
			case "shown":
				o.ShownSpark[i]++
				// ShownSpark is every shown technique — that trend is about reach.
				// The decline-rate trend needs the ambient subset, for the same
				// reason DeclineRate does: it is a ratio within one mechanism.
				if ambient(e) {
					ambientShownSpark[i]++
				}
			case "helped":
				helpedSpark[i]++
			case "declined":
				if ambient(e) {
					declinedSpark[i]++
				}
			}
		}
		for dim, val := range e.Segment {
			key := dim + ":" + val
			active[key] = true
			if e.Stage == "adopted" {
				cohorts[key]++
			}
		}
		switch e.Stage {
		case "dismissed":
			reason, _ := e.Value.(string)
			if reason == "" {
				reason = "unspecified"
			}
			o.Dismissals = upsertMix(o.Dismissals, reason)
			countTrust(&o, e.Confidence)
		case "adopted", "helped":
			countTrust(&o, e.Confidence)
		}
	}
	o.ActiveCohorts = len(active)

	// Fold the per-bucket helped/adopted counts into a cumulative helped-rate
	// trend so a single quiet bucket doesn't read as a crash to 0%. A bucket
	// with no adoptions yet carries the running rate forward (0 until the first
	// adoption).
	o.HelpedRateSpark = make([]int, 12)
	o.DeclineRateSpark = make([]int, 12)
	cumHelped, cumAdopted := 0, 0
	cumDeclined, cumShown := 0, 0
	for i := range o.HelpedRateSpark {
		cumHelped += helpedSpark[i]
		cumAdopted += o.AdoptionSpark[i]
		switch {
		case cumAdopted > 0:
			o.HelpedRateSpark[i] = (cumHelped*100 + cumAdopted/2) / cumAdopted // rounded percent
		case i > 0:
			o.HelpedRateSpark[i] = o.HelpedRateSpark[i-1]
		}
		cumDeclined += declinedSpark[i]
		cumShown += ambientShownSpark[i]
		switch tot := cumDeclined + cumShown; {
		case tot > 0:
			o.DeclineRateSpark[i] = (cumDeclined*100 + tot/2) / tot
		case i > 0:
			o.DeclineRateSpark[i] = o.DeclineRateSpark[i-1]
		}
	}

	o.Techniques = stats.all()
	for _, cs := range o.Techniques {
		if cs.New {
			o.NewTechniques++
		}
	}

	o.Fastest = pick(o.Techniques, 5,
		func(c TechniqueStat) bool { return c.Funnel.Adopted > 0 },
		func(a, b TechniqueStat) bool {
			if a.Velocity() != b.Velocity() {
				return a.Velocity() > b.Velocity()
			}
			return a.Funnel.Adopted > b.Funnel.Adopted
		})
	o.Best = pick(o.Techniques, 5,
		// "working best" requires evidence of WORKING: adopted-but-never-
		// helped techniques are unproven, and a list of 0% rates is noise
		func(c TechniqueStat) bool { return c.Funnel.Helped > 0 },
		func(a, b TechniqueStat) bool { return a.Score > b.Score })
	o.Fresh = pick(o.Techniques, 5,
		func(c TechniqueStat) bool { return c.New || c.NewlyActive },
		func(a, b TechniqueStat) bool {
			at, bt := freshTime(a), freshTime(b)
			return at.After(bt)
		})

	for key, n := range cohorts {
		o.Cohorts = append(o.Cohorts, MixEntry{Label: key, Count: n})
	}
	sort.Slice(o.Cohorts, func(i, j int) bool {
		if o.Cohorts[i].Count != o.Cohorts[j].Count {
			return o.Cohorts[i].Count > o.Cohorts[j].Count
		}
		return o.Cohorts[i].Label < o.Cohorts[j].Label
	})
	if len(o.Cohorts) > 6 {
		o.Cohorts = o.Cohorts[:6]
	}
	sort.Slice(o.Dismissals, func(i, j int) bool { return o.Dismissals[i].Count > o.Dismissals[j].Count })
	for _, mix := range [][]MixEntry{o.TechniquesByScope, o.TechniquesByProvenance,
		o.ShownByScope, o.AdoptedByScope, o.AdoptedByProvenance} {
		sort.Slice(mix, func(i, j int) bool {
			if mix[i].Count != mix[j].Count {
				return mix[i].Count > mix[j].Count
			}
			return mix[i].Label < mix[j].Label
		})
	}
	return o
}

// OrLabel returns v, or def when v is empty — the shared fallback for
// unlabeled scope/provenance values (also used by the digest).
func OrLabel(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ComputeTechnique builds the drill-down for one technique.
func ComputeTechnique(technique models.Technique, events []models.FeedbackEvent, now time.Time, w Window) TechniqueDetail {
	d := TechniqueDetail{Stat: TechniqueStat{Technique: technique}, Buckets: buckets(w.Start, now, w.Bucket)}
	cohorts := map[string]int{}

	// cumulative curve spans the technique's whole life at the window's granularity
	first := now
	var relevant []models.FeedbackEvent
	for _, e := range events {
		if e.TechniqueID != technique.ID {
			continue
		}
		relevant = append(relevant, e)
		if t, ok := ParseTime(e.CreatedAt); ok && t.Before(first) {
			first = t
			d.Stat.FirstEvent = t
		}
	}
	d.Cumulative = buckets(first, now, w.Bucket)

	running := 0
	decaySince := now.AddDate(0, 0, -config.DecayWindowDays)
	// events are stored append-ordered but not guaranteed time-ordered; sort.
	sort.Slice(relevant, func(i, j int) bool { return relevant[i].CreatedAt < relevant[j].CreatedAt })
	for _, e := range relevant {
		t, ok := ParseTime(e.CreatedAt)
		if !ok {
			continue
		}
		if !t.Before(decaySince) {
			d.RecentFunnel.add(e)
		}
		if e.Stage == "adopted" {
			running++
		}
		if i := bucketIndex(d.Cumulative, t, w.Bucket); i >= 0 {
			d.Cumulative[i].Funnel.Adopted = running
		}
		if inWindow(t, w.PrevStart, w.Start) {
			d.PrevFunnel.add(e)
			if e.Stage == "adopted" {
				d.Stat.PrevAdopted++
			}
		}
		if !inWindow(t, w.Start, now) {
			continue
		}
		d.Stat.Funnel.add(e)
		if i := bucketIndex(d.Buckets, t, w.Bucket); i >= 0 {
			d.Buckets[i].Funnel.add(e)
		}
		for dim, val := range e.Segment {
			if e.Stage == "adopted" {
				cohorts[dim+":"+val]++
			}
		}
		if e.Stage == "dismissed" {
			reason, _ := e.Value.(string)
			if reason == "" {
				reason = "unspecified"
			}
			d.Dismissals = upsertMix(d.Dismissals, reason)
		}
	}
	// carry the running total across empty buckets
	last := 0
	for i := range d.Cumulative {
		if d.Cumulative[i].Funnel.Adopted < last {
			d.Cumulative[i].Funnel.Adopted = last
		}
		last = d.Cumulative[i].Funnel.Adopted
	}
	d.Stat.Score = shrunk(d.Stat.Funnel)
	if t, ok := ParseTime(technique.CreatedAt); ok && inWindow(t, w.Start, now) {
		d.Stat.New = true
	}
	for key, n := range cohorts {
		d.Cohorts = append(d.Cohorts, MixEntry{Label: key, Count: n})
	}
	sort.Slice(d.Cohorts, func(i, j int) bool {
		if d.Cohorts[i].Count != d.Cohorts[j].Count {
			return d.Cohorts[i].Count > d.Cohorts[j].Count
		}
		return d.Cohorts[i].Label < d.Cohorts[j].Label
	})
	sort.Slice(d.Dismissals, func(i, j int) bool { return d.Dismissals[i].Count > d.Dismissals[j].Count })
	return d
}

// CohortStat is one segment value's windowed funnel — e.g. team:payments.
type CohortStat struct {
	Dim    string
	Val    string
	Funnel Funnel
}

// AdoptionRate is adopted/shown (ok=false when nothing was shown).
func (c CohortStat) AdoptionRate() (float64, bool) {
	if c.Funnel.Shown == 0 {
		return 0, false
	}
	return float64(c.Funnel.Adopted) / float64(c.Funnel.Shown), true
}

// CohortDimension groups the values observed for one segment dimension (team,
// role, …) with the dimension's aggregate funnel.
type CohortDimension struct {
	Name    string
	Cohorts []CohortStat // values, most-shown first
	Funnel  Funnel       // sum across the dimension's values
}

// CohortReport is the cohort-breakdown page payload: an org baseline plus a
// per-dimension breakdown of how each segment moves through the funnel.
type CohortReport struct {
	Window   Window
	Now      time.Time
	Overall  Funnel // every in-window event (tagged or not) — for coverage context
	Tagged   Funnel // in-window events carrying a segment — the cohort baseline
	Distinct int    // distinct dim:val pairs in the window (== Overview.ActiveCohorts)
	// Dimensions breaks the tagged events down by segment dimension and value.
	Dimensions []CohortDimension
}

// cohortDimOrder is the canonical display order; unlisted dimensions follow,
// alphabetically. Mirrors the segment dimensions in pkg/contracts.
var cohortDimOrder = []string{"team", "role", "function", "domain", "harness", "surface", "model"}

// ComputeCohorts breaks the window's events down by segment dimension and value,
// so the reader can see which cohorts exist and how their OpenTacit usage differs.
// Cohort-only: the raw events carry no identity, just these group labels.
func ComputeCohorts(events []models.FeedbackEvent, now time.Time, w Window) CohortReport {
	rep := CohortReport{Window: w, Now: now}
	byDim := map[string]map[string]*Funnel{}
	distinct := map[string]bool{}

	for _, e := range events {
		t, ok := ParseTime(e.CreatedAt)
		if !ok || !inWindow(t, w.Start, now) {
			continue
		}
		rep.Overall.add(e)
		if len(e.Segment) > 0 {
			rep.Tagged.add(e)
		}
		for dim, val := range e.Segment {
			distinct[dim+":"+val] = true
			vals := byDim[dim]
			if vals == nil {
				vals = map[string]*Funnel{}
				byDim[dim] = vals
			}
			f := vals[val]
			if f == nil {
				f = &Funnel{}
				vals[val] = f
			}
			f.add(e)
		}
	}
	rep.Distinct = len(distinct)

	emit := func(dim string) {
		vals := byDim[dim]
		if len(vals) == 0 {
			return
		}
		cd := CohortDimension{Name: dim}
		for val, f := range vals {
			cd.Cohorts = append(cd.Cohorts, CohortStat{Dim: dim, Val: val, Funnel: *f})
			cd.Funnel.Shown += f.Shown
			cd.Funnel.Adopted += f.Adopted
			cd.Funnel.Helped += f.Helped
			cd.Funnel.Dismissed += f.Dismissed
		}
		sort.Slice(cd.Cohorts, func(i, j int) bool {
			a, b := cd.Cohorts[i].Funnel, cd.Cohorts[j].Funnel
			if a.Shown != b.Shown {
				return a.Shown > b.Shown
			}
			if a.Adopted != b.Adopted {
				return a.Adopted > b.Adopted
			}
			return cd.Cohorts[i].Val < cd.Cohorts[j].Val
		})
		rep.Dimensions = append(rep.Dimensions, cd)
	}

	seen := map[string]bool{}
	for _, dim := range cohortDimOrder {
		seen[dim] = true
		emit(dim)
	}
	var extra []string
	for dim := range byDim {
		if !seen[dim] {
			extra = append(extra, dim)
		}
	}
	sort.Strings(extra)
	for _, dim := range extra {
		emit(dim)
	}
	return rep
}

// DismissalsFor returns the techniques dismissed with the given reason in the
// window, most-dismissed first (Label = technique id, Count = dismissals). The
// caller resolves ids to names. Reason "unspecified" matches events that
// carried no reason.
func DismissalsFor(events []models.FeedbackEvent, now time.Time, w Window, reason string) []MixEntry {
	byTechnique := map[string]int{}
	for _, e := range events {
		if e.Stage != "dismissed" {
			continue
		}
		t, ok := ParseTime(e.CreatedAt)
		if !ok || !inWindow(t, w.Start, now) {
			continue
		}
		r, _ := e.Value.(string)
		if r == "" {
			r = "unspecified"
		}
		if r != reason {
			continue
		}
		byTechnique[e.TechniqueID]++
	}
	out := make([]MixEntry, 0, len(byTechnique))
	for id, n := range byTechnique {
		out = append(out, MixEntry{Label: id, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// liveMeasuredTechniques returns the reviewed techniques' windowed TechniqueStats
// that keep accepts — the shared base for every technique-list drill-down. It
// reads the same one-pass core the overview reads, and none of the overview's
// sparklines, mixes, cohorts or leaderboards. Drafts and retired techniques are
// excluded (they aren't part of what members see); the caller orders the result.
func liveMeasuredTechniques(techniques []models.Technique, events []models.FeedbackEvent,
	now time.Time, w Window, keep func(TechniqueStat) bool) []TechniqueStat {
	var out []TechniqueStat
	for _, c := range techniqueStats(techniques, events, now, w) {
		if c.Technique.Status == "draft" || c.Technique.Status == "retired" {
			continue
		}
		if keep != nil && !keep(c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// sortByAdoption orders a technique list most-adopted first, breaking ties on
// impressions and then on id — the one ranking every "how is this slice doing?"
// table uses, so the same techniques always list in the same order.
func sortByAdoption(list []TechniqueStat) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Funnel.Adopted != list[j].Funnel.Adopted {
			return list[i].Funnel.Adopted > list[j].Funnel.Adopted
		}
		if list[i].Funnel.Shown != list[j].Funnel.Shown {
			return list[i].Funnel.Shown > list[j].Funnel.Shown
		}
		return list[i].Technique.ID < list[j].Technique.ID
	})
}

// hasLabel reports whether list holds want, ignoring case — the tag and
// task-type facets both match that way.
func hasLabel(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// RetrievalMisses returns reviewed techniques that were shown but under-adopted
// in the window, ranked by wasted impressions (shown − adopted) — the retrieval
// tuning worklist. Techniques never shown are omitted (nothing to miss).
func RetrievalMisses(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window) []TechniqueStat {
	out := liveMeasuredTechniques(techniques, events, now, w,
		func(c TechniqueStat) bool { return c.Funnel.Shown > 0 })
	sort.Slice(out, func(i, j int) bool {
		wi := out[i].Funnel.Shown - out[i].Funnel.Adopted
		wj := out[j].Funnel.Shown - out[j].Funnel.Adopted
		if wi != wj {
			return wi > wj
		}
		return out[i].Funnel.Shown > out[j].Funnel.Shown
	})
	return out
}

// SourceTechniques returns the reviewed techniques of one provenance
// (curated|contributed|suggested|federated|mined) with their windowed funnels,
// most-adopted first — the "is this source earning its keep?" drill-down.
func SourceTechniques(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window, provenance string) []TechniqueStat {
	out := liveMeasuredTechniques(techniques, events, now, w,
		func(c TechniqueStat) bool { return OrLabel(c.Technique.Provenance, "curated") == provenance })
	sortByAdoption(out)
	return out
}

// MeasuredTechniques returns reviewed techniques with a measured helped-rate
// (adopted > 0) in the window, worst rate first — the distribution and laggard
// view behind the helped-rate KPI (complements "Working best", which shows only
// the top).
func MeasuredTechniques(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window) []TechniqueStat {
	out := liveMeasuredTechniques(techniques, events, now, w,
		func(c TechniqueStat) bool { return c.Funnel.Adopted > 0 })
	sort.Slice(out, func(i, j int) bool {
		ri, _ := out[i].Funnel.HelpedRate()
		rj, _ := out[j].Funnel.HelpedRate()
		if ri != rj {
			return ri < rj // worst first
		}
		if out[i].Funnel.Adopted != out[j].Funnel.Adopted {
			return out[i].Funnel.Adopted > out[j].Funnel.Adopted
		}
		if out[i].Technique.Name != out[j].Technique.Name {
			return out[i].Technique.Name < out[j].Technique.Name
		}
		return out[i].Technique.ID < out[j].Technique.ID
	})
	return out
}

// SignalClass is the reaction/adoption tally for one confidence level.
type SignalClass struct {
	Helped, Dismissed, Adopted int
}

// Reactions is the graded feedback count (helped + dismissed) — the denominator
// for calibration; adoptions are behavioral, not a verdict.
func (c SignalClass) Reactions() int { return c.Helped + c.Dismissed }

// PositiveRate is helped / reactions (ok=false when there were no reactions).
func (c SignalClass) PositiveRate() (float64, bool) {
	n := c.Reactions()
	if n == 0 {
		return 0, false
	}
	return float64(c.Helped) / float64(n), true
}

// SignalTechnique is one technique's explicit-vs-inferred split.
type SignalTechnique struct {
	Technique                        models.Technique
	Explicit, Inferred, Verification SignalClass
}

// SignalTrust compares inferred vs explicit feedback in the window — the E1
// calibration question: do the low-weight inferred signals (read from behavior
// and ambient reactions) agree with the deliberate explicit ones? If their
// positive rates match, inferred is earning trust.
type SignalTrust struct {
	Explicit, Inferred SignalClass
	// Verification is the autonomous-session class (agent-delivery-plan
	// Phase B): helped verdicts stood in by passing verification commands
	// where no member could react. Split out so its calibration is readable
	// before evidence-gated autonomy leans on it.
	Verification SignalClass
	Techniques   []SignalTechnique // per-technique split, most reactions first
}

// ComputeSignalTrust rolls the window's reactions/adoptions up by confidence,
// overall and per technique (techniques with at least one graded reaction).
func ComputeSignalTrust(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window) SignalTrust {
	techniqueByID := map[string]models.Technique{}
	for _, c := range techniques {
		techniqueByID[c.ID] = c
	}
	bump := func(sc *SignalClass, stage string) {
		switch stage {
		case "helped":
			sc.Helped++
		case "dismissed":
			sc.Dismissed++
		case "adopted":
			sc.Adopted++
		}
	}
	per := map[string]*SignalTechnique{}
	var st SignalTrust
	for _, e := range events {
		if e.Stage != "helped" && e.Stage != "dismissed" && e.Stage != "adopted" {
			continue
		}
		t, ok := ParseTime(e.CreatedAt)
		if !ok || !inWindow(t, w.Start, now) {
			continue
		}
		sc := per[e.TechniqueID]
		if sc == nil {
			technique, ok := techniqueByID[e.TechniqueID]
			if !ok {
				technique = models.Technique{ID: e.TechniqueID, Name: e.TechniqueID}
			}
			sc = &SignalTechnique{Technique: technique}
			per[e.TechniqueID] = sc
		}
		switch e.Confidence {
		case "explicit":
			bump(&st.Explicit, e.Stage)
			bump(&sc.Explicit, e.Stage)
		case "verification":
			bump(&st.Verification, e.Stage)
			bump(&sc.Verification, e.Stage)
		default:
			bump(&st.Inferred, e.Stage)
			bump(&sc.Inferred, e.Stage)
		}
	}
	for _, sc := range per {
		if sc.Explicit.Reactions()+sc.Inferred.Reactions()+sc.Verification.Reactions() == 0 {
			continue // only techniques with a graded verdict inform calibration
		}
		st.Techniques = append(st.Techniques, *sc)
	}
	sort.Slice(st.Techniques, func(i, j int) bool {
		ri := st.Techniques[i].Explicit.Reactions() + st.Techniques[i].Inferred.Reactions() + st.Techniques[i].Verification.Reactions()
		rj := st.Techniques[j].Explicit.Reactions() + st.Techniques[j].Inferred.Reactions() + st.Techniques[j].Verification.Reactions()
		if ri != rj {
			return ri > rj
		}
		return st.Techniques[i].Technique.ID < st.Techniques[j].Technique.ID
	})
	return st
}

// TagTechniques returns the reviewed techniques carrying tag, with their windowed
// funnels, most-adopted first — the tag performance drill-down.
func TagTechniques(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window, tag string) []TechniqueStat {
	out := liveMeasuredTechniques(techniques, events, now, w,
		func(c TechniqueStat) bool { return hasLabel(c.Technique.Tags, tag) })
	sortByAdoption(out)
	return out
}

// TaskTypeTechniques is TagTechniques for the task-type facet: every reviewed,
// window-active technique declaring the task type, most-adopted first. The
// Organization view's areas are keyed by first task type, so this is that
// page's drill-down.
func TaskTypeTechniques(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window, taskType string) []TechniqueStat {
	out := liveMeasuredTechniques(techniques, events, now, w,
		func(c TechniqueStat) bool { return hasLabel(c.Technique.TaskTypes, taskType) })
	sortByAdoption(out)
	return out
}

// CohortDetail is one cohort's (dimension:value) windowed standing: its overall
// funnel plus a per-technique breakdown.
type CohortDetail struct {
	Funnel     Funnel
	Techniques []TechniqueStat // techniques this cohort touched, most-adopted first
}

// ComputeCohortDetail restricts the window's events to those tagged dim=val and
// rolls them up overall and per technique — the drill-down behind one row of
// the cohorts table.
func ComputeCohortDetail(techniques []models.Technique, events []models.FeedbackEvent, now time.Time, w Window, dim, val string) CohortDetail {
	techniqueByID := map[string]models.Technique{}
	for _, c := range techniques {
		techniqueByID[c.ID] = c
	}
	perTechnique := map[string]*Funnel{}
	var res CohortDetail
	for _, e := range events {
		if e.Segment[dim] != val {
			continue
		}
		t, ok := ParseTime(e.CreatedAt)
		if !ok || !inWindow(t, w.Start, now) {
			continue
		}
		res.Funnel.add(e)
		f := perTechnique[e.TechniqueID]
		if f == nil {
			f = &Funnel{}
			perTechnique[e.TechniqueID] = f
		}
		f.add(e)
	}
	for id, f := range perTechnique {
		technique, ok := techniqueByID[id]
		if !ok {
			technique = models.Technique{ID: id, Name: id}
		}
		res.Techniques = append(res.Techniques, TechniqueStat{Technique: technique, Funnel: *f})
	}
	sortByAdoption(res.Techniques)
	return res
}

// ActivityBuckets time-buckets the events the matcher accepts across the window,
// tallying each stage into the bucket's funnel. It powers the shared Activity
// stacked-bar panel on the tag, source, and cohort insight views — pass a
// matcher that selects the events belonging to that slice.
func ActivityBuckets(events []models.FeedbackEvent, now time.Time, w Window, match func(models.FeedbackEvent) bool) []Bucket {
	out := buckets(w.Start, now, w.Bucket)
	for _, e := range events {
		if !match(e) {
			continue
		}
		t, ok := ParseTime(e.CreatedAt)
		if !ok || !inWindow(t, w.Start, now) {
			continue
		}
		if i := bucketIndex(out, t, w.Bucket); i >= 0 {
			out[i].Funnel.add(e)
		}
	}
	return out
}

// shrunk mirrors retrieval.Shrink — dismissals in the denominator, verdicts
// confidence-weighted — so the leaderboards can never praise a technique that
// ranking is busy burying. Funnels built by literal (tests, callers outside
// the event loop) carry no weights; fall back to the raw counts there.
func shrunk(f Funnel) float64 {
	h, a, d := f.wHelped, f.wAdopted, f.wDismissed
	if a == 0 && h == 0 && d == 0 {
		h, a, d = float64(f.Helped), float64(f.Adopted), float64(f.Dismissed)
	}
	return (h + config.Prior*config.ShrinkK) / (a + d + config.ShrinkK)
}

func countTrust(o *Overview, confidence string) {
	switch confidence {
	case "explicit":
		o.TrustExplicit++
	case "verification":
		o.TrustVerification++
	default:
		o.TrustInferred++
	}
}

func upsertMix(mix []MixEntry, label string) []MixEntry {
	for i := range mix {
		if mix[i].Label == label {
			mix[i].Count++
			return mix
		}
	}
	return append(mix, MixEntry{Label: label, Count: 1})
}

func freshTime(c TechniqueStat) time.Time {
	if t, ok := ParseTime(c.Technique.CreatedAt); ok && t.After(c.FirstEvent) {
		return t
	}
	return c.FirstEvent
}

func pick(all []TechniqueStat, n int, keep func(TechniqueStat) bool, less func(a, b TechniqueStat) bool) []TechniqueStat {
	var out []TechniqueStat
	for _, c := range all {
		if keep(c) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}
