// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package techmap

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

func technique(id string, tags []string, tt ...string) models.Technique {
	c := models.Technique{ID: id, Name: id, Status: "stable", Tags: tags}
	if len(tt) > 0 {
		c.TaskTypes = tt
	}
	return c
}

func ev(id, stage string) models.FeedbackEvent {
	return models.FeedbackEvent{TechniqueID: id, Stage: stage}
}

func TestBuildEdgesAndDegrees(t *testing.T) {
	techniques := []models.Technique{
		technique("a", []string{"review", "audit", "feedback"}),
		technique("b", []string{"review", "audit"}),          // shares 2 with a -> drawn edge
		technique("c", []string{"review"}),                   // shares 1 with a and b -> layout only
		technique("d", []string{"setup", "personalization"}), // shares nothing -> isolated
	}
	g := Build(techniques, nil)

	if len(g.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4", len(g.Nodes))
	}
	// a–b share 2; a–c and b–c share 1. d shares nothing.
	got := map[[2]int]int{}
	for _, e := range g.TagEdges {
		got[[2]int{e.A, e.B}] = e.W
	}
	if got[[2]int{0, 1}] != 2 || got[[2]int{0, 2}] != 1 || got[[2]int{1, 2}] != 1 {
		t.Fatalf("edge weights wrong: %v", got)
	}
	if _, ok := got[[2]int{0, 3}]; ok {
		t.Fatal("d shares no tags but got an edge")
	}
	// Degree counts only drawn edges (>=2 shared). a and b each have one; c and d
	// have none.
	deg := map[string]int{}
	for _, n := range g.Nodes {
		deg[n.ID] = n.Deg
	}
	if deg["a"] != 1 || deg["b"] != 1 || deg["c"] != 0 || deg["d"] != 0 {
		t.Fatalf("degrees wrong: %v", deg)
	}
	// c shares a tag with others but not two, and d shares none — both isolated
	// in the DRAWN graph.
	if g.Summary.Isolated != 2 || g.Summary.Connected != 2 || g.Summary.Edges2 != 1 {
		t.Fatalf("summary wrong: %+v", g.Summary)
	}
}

func TestBuildCountsAdoptionAndExcludesNonLive(t *testing.T) {
	techniques := []models.Technique{
		technique("live", []string{"x"}),
		{ID: "draft", Name: "draft", Status: "draft", Tags: []string{"x"}},
		{ID: "retired", Name: "retired", Status: "retired", Tags: []string{"x"}},
	}
	events := []models.FeedbackEvent{
		ev("live", "shown"), ev("live", "shown"), ev("live", "adopted"), ev("live", "helped"),
		ev("draft", "adopted"), // must not count — draft isn't on the map
	}
	g := Build(techniques, events)

	if len(g.Nodes) != 1 || g.Nodes[0].ID != "live" {
		t.Fatalf("map should hold only the live technique, got %d nodes", len(g.Nodes))
	}
	n := g.Nodes[0]
	if n.Shown != 2 || n.Adopt != 1 || n.Helped != 1 {
		t.Fatalf("counts wrong: shown=%d adopt=%d helped=%d", n.Shown, n.Adopt, n.Helped)
	}
	if g.Summary.Adopted != 1 || g.Summary.Helped != 1 {
		t.Fatalf("summary adoption wrong: %+v", g.Summary)
	}
}

// The layout must be DETERMINISTIC: the map can't jump to new positions on every
// reload of the same library.
func TestLayoutIsDeterministic(t *testing.T) {
	techniques := []models.Technique{
		technique("a", []string{"review", "audit"}),
		technique("b", []string{"review", "audit"}),
		technique("c", []string{"setup"}),
	}
	g1 := Build(techniques, nil)
	g2 := Build(techniques, nil)
	for i := range g1.Nodes {
		if g1.Nodes[i].X != g2.Nodes[i].X || g1.Nodes[i].Y != g2.Nodes[i].Y {
			t.Fatalf("node %d moved between builds: (%v,%v) vs (%v,%v)",
				i, g1.Nodes[i].X, g1.Nodes[i].Y, g2.Nodes[i].X, g2.Nodes[i].Y)
		}
	}
	// Positions are normalized into the unit square.
	for _, n := range g1.Nodes {
		if n.X < 0 || n.X > 1 || n.Y < 0 || n.Y > 1 {
			t.Fatalf("position out of the unit square: (%v,%v)", n.X, n.Y)
		}
	}
}

// area falls back task-type -> first tag -> "—", the same rule the org map uses.
func TestArea(t *testing.T) {
	if a := area(technique("x", []string{"tag1"}, "editing")); a != "editing" {
		t.Fatalf("task-type should win: %q", a)
	}
	if a := area(technique("x", []string{"tag1", "tag2"})); a != "tag1" {
		t.Fatalf("first tag is the fallback: %q", a)
	}
	if a := area(technique("x", nil)); a != "—" {
		t.Fatalf("no tags -> em dash: %q", a)
	}
}

func TestBuildEmpty(t *testing.T) {
	g := Build(nil, nil)
	if len(g.Nodes) != 0 || g.Summary.Total != 0 {
		t.Fatalf("empty registry should make an empty map: %+v", g.Summary)
	}
}

// Cohort edges join techniques adopted by the same cohort, weighted by how
// many cohorts they share. Never-adopted techniques have no cohort, so they
// carry no cohort edge — they are used by no one.
func TestCohortEdges(t *testing.T) {
	techniques := []models.Technique{
		technique("a", []string{"x"}), technique("b", []string{"y"}), technique("c", []string{"z"}),
	}
	seg := func(team, harness string) models.Segment {
		return models.Segment{"team": team, "harness": harness}
	}
	events := []models.FeedbackEvent{
		{TechniqueID: "a", Stage: "adopted", Segment: seg("t1", "claude-code")},
		{TechniqueID: "b", Stage: "adopted", Segment: seg("t1", "opencode")}, // shares team t1 with a
		// c is never adopted -> no cohort, no cohort edge
	}
	g := Build(techniques, events)
	if w := cohortEdgeWeight(g, "a", "b"); w != 1 {
		t.Fatalf("a—b share cohort team:t1, want weight 1, got %d", w)
	}
	if cohortEdgeWeight(g, "a", "c") != 0 {
		t.Fatal("c is never adopted; it must carry no cohort edge")
	}
}

// One team adopting nearly everything makes the cohort graph near-complete —
// flagged so the view can say so instead of drawing a meaningless blob.
func TestCohortDenseFlag(t *testing.T) {
	oneTeam := models.Segment{"team": "solo"}
	var techniques []models.Technique
	var events []models.FeedbackEvent
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		techniques = append(techniques, technique(id, []string{id}))
		events = append(events, models.FeedbackEvent{TechniqueID: id, Stage: "adopted", Segment: oneTeam})
	}
	if g := Build(techniques, events); !g.CohortDense {
		t.Fatal("one team adopting everything should flag the cohort graph as dense")
	}

	// Two teams with disjoint adoption -> sparse, structured, not flagged.
	t1, t2 := models.Segment{"team": "one"}, models.Segment{"team": "two"}
	techniques = nil
	events = nil
	for _, id := range []string{"a", "b", "c"} {
		techniques = append(techniques, technique(id, []string{id}))
		events = append(events, models.FeedbackEvent{TechniqueID: id, Stage: "adopted", Segment: t1})
	}
	for _, id := range []string{"d", "e", "f"} {
		techniques = append(techniques, technique(id, []string{id}))
		events = append(events, models.FeedbackEvent{TechniqueID: id, Stage: "adopted", Segment: t2})
	}
	if g := Build(techniques, events); g.CohortDense {
		t.Fatal("two teams adopting disjoint sets is structured, not dense")
	}
}

func cohortEdgeWeight(g Graph, ai, bi string) int {
	idx := map[string]int{}
	for i, n := range g.Nodes {
		idx[n.ID] = i
	}
	a, b := idx[ai], idx[bi]
	for _, e := range g.CohortEdges {
		if (e.A == a && e.B == b) || (e.A == b && e.B == a) {
			return e.W
		}
	}
	return 0
}

// A graph with no shared tags must still marshal edges as [] not null: the
// client does tagEdges.forEach, which throws on null and blanks the map. This
// happens for real when the view is filtered to a set that shares no tags (e.g.
// only org-scoped techniques).
func TestEdgesNeverNil(t *testing.T) {
	g := Build([]models.Technique{
		technique("a", []string{"x"}), technique("b", []string{"y"}), technique("c", []string{"z"}),
	}, nil)
	if g.TagEdges == nil || g.CohortEdges == nil {
		t.Fatal("edge slices must be non-nil so they marshal to [], not null")
	}
	out, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"tagEdges":null`) || strings.Contains(string(out), `"cohortEdges":null`) {
		t.Fatalf("edges marshalled as null, which crashes the client: %s", out)
	}
}

// Two tag-disjoint groups form two communities; a technique sharing nothing
// joins neither. Names come from each community's dominant tag.
func TestDetectClusters(t *testing.T) {
	techniques := []models.Technique{
		technique("a", []string{"review", "audit"}),
		technique("b", []string{"review", "audit"}),
		technique("c", []string{"review"}),
		technique("d", []string{"prompting", "context"}),
		technique("e", []string{"prompting", "context"}),
		technique("f", []string{"prompting"}),
		technique("z", []string{"loner"}), // shares nothing -> no cluster
	}
	g := Build(techniques, nil)
	if len(g.Clusters) != 3 {
		t.Fatalf("want 2 communities + the Ungrouped bucket, got %d: %+v", len(g.Clusters), g.Clusters)
	}
	byID := map[string]int{}
	for i, n := range g.Nodes {
		byID[n.ID] = i
	}
	// a,b,c in one cluster; d,e,f in the other; z in the Ungrouped bucket.
	cl := func(id string) int { return g.Nodes[byID[id]].Cluster }
	if cl("a") != cl("b") || cl("b") != cl("c") {
		t.Fatal("the review group split across clusters")
	}
	if cl("d") != cl("e") || cl("e") != cl("f") {
		t.Fatal("the prompting group split across clusters")
	}
	if cl("a") == cl("d") {
		t.Fatal("the two tag-disjoint groups collapsed into one cluster")
	}
	// The loner is never groupless: it lands in the synthetic bucket, kept last.
	ung := g.Clusters[len(g.Clusters)-1]
	if !ung.Ungrouped || ung.Name != "Ungrouped" {
		t.Fatalf("last cluster should be the Ungrouped bucket, got %+v", ung)
	}
	if cl("z") != ung.ID {
		t.Fatalf("the loner should sit in the Ungrouped bucket %d, got %d", ung.ID, cl("z"))
	}
	// Each real cluster is named from its dominant tag.
	names := map[string]bool{}
	for _, c := range g.Clusters {
		names[c.Name] = true
	}
	if !names["review"] || !names["prompting"] {
		t.Fatalf("clusters not named from dominant tags: %v", names)
	}
	// The gather ties are layout-only: no drawn edge reaches the loner, and it
	// still counts as isolated.
	for _, e := range g.TagEdges {
		if e.A == byID["z"] || e.B == byID["z"] {
			t.Fatal("the loner must not gain real tag edges from the bucket")
		}
	}
	if g.Summary.Isolated == 0 {
		t.Fatal("the loner should still count as isolated in the summary")
	}
}

// TestNoUngroupedBucketWithoutCommunities: with no communities at all there are
// no groups to be outside of — one bucket spanning the whole library would say
// nothing, so none is added.
func TestNoUngroupedBucketWithoutCommunities(t *testing.T) {
	g := Build([]models.Technique{technique("a", []string{"x"}), technique("b", []string{"y"})}, nil)
	if len(g.Clusters) != 0 {
		t.Fatalf("expected no clusters at all, got %+v", g.Clusters)
	}
}

// A disconnected island (the Ungrouped bucket, sharing no tags with any
// community) must NOT drift to the far corner and squash the connected mass into
// the opposite one. Component packing places it beside the mass: the mass keeps
// most of the canvas, and the island sits a short, deliberate gap away — not a
// half-canvas void that reads as a false measure of difference.
func TestUngroupedIslandDoesNotSquashTheMass(t *testing.T) {
	var techniques []models.Technique
	// Three tag-linked communities form one connected mass; two orphan techniques,
	// each with a unique tag, form the Ungrouped island.
	comms := []struct {
		prefix, bridge string
		tags           []string
	}{
		{"eval", "shared-a", []string{"evals", "eval-harness"}},
		{"forge", "shared-a", []string{"forgeflow", "checkpoint"}},
		{"inc", "shared-b", []string{"incident", "runbook"}},
	}
	for _, c := range comms {
		for i := 0; i < 6; i++ {
			tags := append([]string{}, c.tags...)
			if i == 0 {
				tags = append(tags, c.bridge) // a bridge tag ties the communities into one component
			}
			techniques = append(techniques, technique(fmt.Sprintf("%s-%d", c.prefix, i), tags))
		}
	}
	techniques = append(techniques, technique("lone-1", []string{"orphan-x"}), technique("lone-2", []string{"orphan-y"}))

	g := Build(techniques, nil)
	ungID := -1
	for _, cl := range g.Clusters {
		if cl.Ungrouped {
			ungID = cl.ID
		}
	}
	if ungID < 0 {
		t.Fatal("expected an Ungrouped bucket")
	}
	mx0, my0, mx1, my1 := 1e9, 1e9, -1e9, -1e9
	ux0, uy0, ux1, uy1 := 1e9, 1e9, -1e9, -1e9
	for _, nd := range g.Nodes {
		if nd.Cluster == ungID {
			ux0, uy0, ux1, uy1 = math.Min(ux0, nd.X), math.Min(uy0, nd.Y), math.Max(ux1, nd.X), math.Max(uy1, nd.Y)
		} else {
			mx0, my0, mx1, my1 = math.Min(mx0, nd.X), math.Min(my0, nd.Y), math.Max(mx1, nd.X), math.Max(my1, nd.Y)
		}
	}
	// The connected mass fills most of the canvas rather than a squashed corner.
	if area := (mx1 - mx0) * (my1 - my0); area < 0.5 {
		t.Fatalf("connected mass squashed to %.0f%% of the canvas — island still distorts the layout", area*100)
	}
	// The island parks a short gap from the mass, not across a half-canvas void.
	gap := func(lo, hi, olo, ohi float64) float64 {
		if olo > hi {
			return olo - hi
		}
		if lo > ohi {
			return lo - ohi
		}
		return 0
	}
	if gx, gy := gap(mx0, mx1, ux0, ux1), gap(my0, my1, uy0, uy1); gx > 0.25 || gy > 0.25 {
		t.Fatalf("island floated too far from the mass: gap x=%.2f y=%.2f", gx, gy)
	}
}

// TestNodeRadiusRelativeAndBounded checks the map's dynamic node sizing: size
// scales with adoption relative to the busiest node (so the map reads the same
// at any absolute volume), a never-adopted node draws at the floor, org nodes
// draw larger, and the busiest node is bounded so the map cannot overflow.
func TestNodeRadiusRelativeAndBounded(t *testing.T) {
	// sizeTarget shrinks as the map gets more crowded, within [min,max].
	if got := mapSizeTarget(1); got != mapMaxTarget {
		t.Errorf("a nearly-empty map should use the max target, got %.1f", got)
	}
	if got := mapSizeTarget(100000); got != mapMinTarget {
		t.Errorf("a very crowded map should clamp to the min target, got %.1f", got)
	}
	if a, b := mapSizeTarget(10), mapSizeTarget(40); a <= b {
		t.Errorf("fewer nodes should size larger: n=10 %.1f !> n=40 %.1f", a, b)
	}

	st := mapSizeTarget(30)
	// A never-adopted node sits at the floor; the busiest hits the target.
	if r := nodeRadius(0, 100, false, st); r != mapFloorR {
		t.Errorf("zero-adoption node should draw at the floor %.1f, got %.1f", mapFloorR, r)
	}
	if r := nodeRadius(100, 100, false, st); r != st {
		t.Errorf("busiest node should draw at the target %.1f, got %.1f", st, r)
	}
	// RELATIVE: the same adoption count draws the same size only relative to the
	// busiest — a node at half the max is identical whether max is 20 or 20000.
	small := nodeRadius(10, 20, false, st)
	large := nodeRadius(10000, 20000, false, st)
	if small != large {
		t.Errorf("sizing must be relative to the busiest node: %.3f vs %.3f", small, large)
	}
	// More adoption draws larger; org draws a fifth larger than general.
	if nodeRadius(50, 100, false, st) <= nodeRadius(10, 100, false, st) {
		t.Error("more adoption should draw larger")
	}
	if nodeRadius(50, 100, true, st) <= nodeRadius(50, 100, false, st) {
		t.Error("org nodes should draw larger than general")
	}
}

// TestCohortClusters checks the cohort arrangement's areas: techniques group
// by the team that adopts each most, in the first dimension that separates them,
// named for the cohort, with each node pointed at its home cluster.
func TestCohortClusters(t *testing.T) {
	techniques := []models.Technique{
		technique("warehouse-x", []string{"data"}), technique("warehouse-y", []string{"data"}),
		technique("deploy-x", []string{"ops"}), technique("unadopted", []string{"misc"}),
	}
	seg := func(team string) models.Segment { return models.Segment{"team": team} }
	evseg := func(id, stage, team string) models.FeedbackEvent {
		e := ev(id, stage)
		e.Segment = seg(team)
		return e
	}
	events := []models.FeedbackEvent{
		// data-platform adopts both warehouse techniques; platform adopts the deploy technique.
		evseg("warehouse-x", "adopted", "data-platform"), evseg("warehouse-x", "adopted", "data-platform"),
		evseg("warehouse-y", "adopted", "data-platform"),
		evseg("warehouse-y", "adopted", "platform"), // one cross-adoption; data-platform still dominates
		evseg("deploy-x", "adopted", "platform"), evseg("deploy-x", "adopted", "platform"),
	}
	g := Build(techniques, events)

	if g.CohortDim != "team" {
		t.Fatalf("expected grouping dimension team, got %q", g.CohortDim)
	}
	if len(g.CohortClusters) != 3 {
		t.Fatalf("expected 3 cohort areas (Data Platform, Platform, Ungrouped), got %d: %+v", len(g.CohortClusters), g.CohortClusters)
	}
	// Largest cohort first: Data Platform (2 caps) before Platform (1 cap).
	if g.CohortClusters[0].Name != "Data Platform" || len(g.CohortClusters[0].Members) != 2 {
		t.Errorf("cluster 0 = %q with %d members, want Data Platform with 2", g.CohortClusters[0].Name, len(g.CohortClusters[0].Members))
	}
	byID := map[string]Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	if byID["warehouse-x"].CohortCluster != 0 || byID["warehouse-y"].CohortCluster != 0 {
		t.Errorf("warehouse techniques should map to the Data Platform cluster")
	}
	if byID["deploy-x"].CohortCluster == byID["warehouse-x"].CohortCluster {
		t.Error("the deploy technique should be in a different cohort area than the warehouse techniques")
	}
	// A never-adopted technique has no home cohort — but it is never groupless: it
	// lands in the synthetic Ungrouped bucket, kept last.
	ung := g.CohortClusters[len(g.CohortClusters)-1]
	if !ung.Ungrouped || ung.Name != "Ungrouped" {
		t.Fatalf("last cohort area should be the Ungrouped bucket, got %+v", ung)
	}
	if byID["unadopted"].CohortCluster != ung.ID {
		t.Errorf("the never-adopted technique should sit in the Ungrouped bucket %d, got %d", ung.ID, byID["unadopted"].CohortCluster)
	}
}

// The 3-D embedding carries the same promises as the flat one: the same graph
// lands in the same place on every build, everything fits the unit cube, and it
// is a real volume rather than a sheet the renderer would have to fake depth on.
func TestLayout3IsDeterministicAndFillsTheCube(t *testing.T) {
	var techniques []models.Technique
	for _, c := range []struct {
		prefix string
		tags   []string
	}{
		{"eval", []string{"evals", "eval-harness"}},
		{"forge", []string{"forgeflow", "checkpoint"}},
		{"inc", []string{"incident", "runbook"}},
	} {
		for i := 0; i < 6; i++ {
			techniques = append(techniques, technique(fmt.Sprintf("%s-%d", c.prefix, i), c.tags))
		}
	}
	g1, g2 := Build(techniques, nil), Build(techniques, nil)
	for i := range g1.Nodes {
		a, b := g1.Nodes[i], g2.Nodes[i]
		if a.X3 != b.X3 || a.Y3 != b.Y3 || a.Z3 != b.Z3 {
			t.Fatalf("node %d moved between builds: (%v,%v,%v) vs (%v,%v,%v)",
				i, a.X3, a.Y3, a.Z3, b.X3, b.Y3, b.Z3)
		}
	}
	var lo, hi [3]float64
	for ax := range lo {
		lo[ax], hi[ax] = 1e9, -1e9
	}
	for _, n := range g1.Nodes {
		for ax, v := range [3]float64{n.X3, n.Y3, n.Z3} {
			if v < 0 || v > 1 {
				t.Fatalf("position outside the unit cube: %v", v)
			}
			lo[ax] = math.Min(lo[ax], v)
			hi[ax] = math.Max(hi[ax], v)
		}
	}
	// Every axis must be used. A depth that collapsed would leave the WebGL map
	// drawing a flat wall of nodes — the one thing the third dimension is for.
	for ax, name := range []string{"x", "y", "z"} {
		if span := hi[ax] - lo[ax]; span < 0.5 {
			t.Errorf("%s axis spans only %.2f of the cube — the embedding is flat on it", name, span)
		}
	}
	// The flat arrangement is still there and still its own layout.
	for _, n := range g1.Nodes {
		if n.X < 0 || n.X > 1 || n.Y < 0 || n.Y > 1 {
			t.Fatalf("2-D position out of the unit square: (%v,%v)", n.X, n.Y)
		}
	}
}

// Cohort and tag arrangements are separate embeddings in 3-D as they are in 2-D:
// the same node generally sits somewhere else in each, which is what makes
// flipping the arrangement say anything.
func TestLayout3ArrangementsDiffer(t *testing.T) {
	techniques := []models.Technique{
		technique("warehouse-x", []string{"warehouse"}),
		technique("warehouse-y", []string{"warehouse"}),
		technique("deploy-x", []string{"deploy"}),
		technique("deploy-y", []string{"deploy"}),
	}
	seg := func(team string) models.Segment { return models.Segment{"team": team} }
	var events []models.FeedbackEvent
	add := func(id, team string, n int) {
		for i := 0; i < n; i++ {
			events = append(events, models.FeedbackEvent{
				EventID: fmt.Sprintf("%s-%s-%d", id, team, i), TechniqueID: id,
				Stage: "adopted", Segment: seg(team), Confidence: "explicit"})
		}
	}
	// The teams cut ACROSS the tag pairs: tags join warehouse-x to warehouse-y,
	// cohorts join warehouse-x to deploy-x. Same nodes, different neighbours,
	// so the two embeddings have to disagree about where things go.
	add("warehouse-x", "data", 3)
	add("deploy-x", "data", 3)
	add("warehouse-y", "platform", 3)
	add("deploy-y", "platform", 3)

	g := Build(techniques, events)
	moved := false
	for _, n := range g.Nodes {
		if n.X3 != n.CX3 || n.Y3 != n.CY3 || n.Z3 != n.CZ3 {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatal("the cohort arrangement is identical to the tag one in 3-D")
	}
}
