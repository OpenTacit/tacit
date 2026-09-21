// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package techmap builds the technique knowledge map: the live techniques as a
// network, laid out so related techniques pull together. It offers two
// relations, each with its own layout:
//
//   - by shared TAGS — what the techniques are about. The clusters are the
//     org's areas of practice; the loose nodes are knowledge nothing connects to.
//   - by shared COHORT — which techniques the same team/role/harness adopts
//     together. This sharpens as an org grows more teams; at one dominant team it
//     degenerates to a near-complete graph, which CohortDense flags so the view
//     can say so instead of drawing a meaningless blob.
//
// Node size encodes adoption; colour encodes source (hue) and helped rate
// (intensity). Both layouts are computed HERE, server-side and deterministically,
// so the map is stable across reloads and the browser ships no physics engine.
package techmap

import (
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
)

// Node is one technique, with a position in each layout.
//
// The JSON tags here are camelCase, unlike the rest of the registry's
// snake_case — deliberate: this payload feeds the JS map renderer directly.
type Node struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Tags    []string `json:"tags"`
	Scope   string   `json:"scope"`
	Source  string   `json:"source"`
	Area    string   `json:"area"`
	Shown   int      `json:"shown"`
	Adopt   int      `json:"adopted"`
	Helped  int      `json:"helped"`
	Deg     int      `json:"deg"`     // tag neighbours at the drawn threshold
	Cluster int      `json:"cluster"` // community id in the tag graph, or -1 if it belongs to none
	// CohortCluster is the index (into Graph.CohortClusters) of the cohort that
	// adopts this technique most — its "home" cohort in the cohort arrangement —
	// or -1 when no cohort in the grouping dimension has adopted it.
	CohortCluster int     `json:"cohortCluster"`
	X             float64 `json:"x"` // arranged by shared tags
	Y             float64 `json:"y"`
	CX            float64 `json:"cx"` // arranged by shared cohort
	CY            float64 `json:"cy"`
	// The same two arrangements embedded in three dimensions, for the WebGL map
	// (layout3.go). Kept beside the flat pair rather than replacing it: the 2-D
	// canvas is still what a browser without WebGL, or a reader who has asked for
	// reduced motion, gets to see, and the two must agree about which techniques
	// belong together even though they disagree about where.
	X3  float64 `json:"x3"`
	Y3  float64 `json:"y3"`
	Z3  float64 `json:"z3"`
	CX3 float64 `json:"cx3"`
	CY3 float64 `json:"cy3"`
	CZ3 float64 `json:"cz3"`
}

// Cluster is a community of techniques the shared-tag graph pulls together — an
// area of practice. Name is derived from the members' dominant tags; Desc is
// filled by the optional LLM naming pass and is empty until then.
type Cluster struct {
	ID      int      `json:"id"`
	Members []int    `json:"members"` // node indices
	Tags    []string `json:"tags"`    // the cluster's most common tags, first the most common
	Name    string   `json:"name"`    // tag-derived default; the LLM pass may replace it
	Desc    string   `json:"desc"`    // one-line description, LLM-filled
	// Ungrouped marks the synthetic bucket holding every technique the
	// detection left outside all communities. It is a home, not an area of
	// practice: the LLM namer skips it, the All list keeps it last, and the
	// heatmap shows it as one column rather than a column per stray technique.
	Ungrouped bool `json:"ungrouped,omitempty"`
}

// Edge joins two techniques; W is the strength (shared tags, or shared cohorts).
type Edge struct {
	A int `json:"a"`
	B int `json:"b"`
	W int `json:"w"`
}

// Graph is the whole map: both relations, both layouts, the clusters, and the
// headline counts.
type Graph struct {
	Nodes       []Node    `json:"nodes"`
	TagEdges    []Edge    `json:"tagEdges"`
	CohortEdges []Edge    `json:"cohortEdges"`
	Clusters    []Cluster `json:"clusters"`
	// CohortClusters are the areas of the COHORT arrangement: each groups the
	// techniques a given cohort (e.g. a team) adopts most, named by that cohort.
	// Empty when no segment dimension separates the techniques. CohortDim is the
	// segment dimension they are grouped by ("team", "role", …).
	CohortClusters []Cluster    `json:"cohortClusters"`
	CohortDim      string       `json:"cohortDim"`
	Summary        GraphSummary `json:"summary"`
	// CohortDense is true when the cohort graph is near-complete — one team
	// adopting almost everything links almost everything, so the arrangement
	// carries no structure and the UI should say so rather than draw a hairball.
	CohortDense bool `json:"cohortDense"`
}

// GraphSummary is the map's headline counts (nodes, connectivity, adoption).
type GraphSummary struct {
	Total     int `json:"total"`
	Connected int `json:"connected"`
	Isolated  int `json:"isolated"`
	Adopted   int `json:"adopted"`
	Helped    int `json:"helped"`
	Edges2    int `json:"edges2"`
}

// drawThreshold is the shared-tag count at which a link is drawn and counted
// toward a node's degree. Every pair sharing one tag still informs the layout
// (weaker ties pull, just less); drawing them all is a hairball.
const drawThreshold = 2

// Build assembles the graph from the live techniques and the event log, then
// lays out both relations. Drafts and retired techniques are excluded — the map is
// the working library, the set the "All" view and the "Live techniques" tile
// count.
func Build(techniques []models.Technique, events []models.FeedbackEvent) Graph {
	shown, adopt, helped := map[string]int{}, map[string]int{}, map[string]int{}
	// cohorts[capID] = the set of "dim:val" cohorts that ADOPTED it (for edges).
	cohorts := map[string]map[string]bool{}
	// cohortCounts[capID][dim][val] = how many times that cohort adopted it, so a
	// technique's "home" cohort (its dominant adopter) can be found.
	cohortCounts := map[string]map[string]map[string]int{}
	for _, e := range events {
		switch e.Stage {
		case "shown":
			shown[e.TechniqueID]++
		case "adopted":
			adopt[e.TechniqueID]++
			if cohorts[e.TechniqueID] == nil {
				cohorts[e.TechniqueID] = map[string]bool{}
				cohortCounts[e.TechniqueID] = map[string]map[string]int{}
			}
			for dim, val := range e.Segment {
				if val != "" {
					cohorts[e.TechniqueID][dim+":"+val] = true
					if cohortCounts[e.TechniqueID][dim] == nil {
						cohortCounts[e.TechniqueID][dim] = map[string]int{}
					}
					cohortCounts[e.TechniqueID][dim][val]++
				}
			}
		case "helped":
			if !e.CountsAsHelped() {
				break
			}
			helped[e.TechniqueID]++
		}
	}

	var g Graph
	for _, c := range techniques {
		if c.Status == "draft" || c.Status == "retired" {
			continue
		}
		scope := c.Scope
		if scope == "" {
			scope = "general"
		}
		source := c.Provenance
		if source == "" {
			source = "curated"
		}
		g.Nodes = append(g.Nodes, Node{
			ID: c.ID, Name: c.Name, Tags: c.Tags, Scope: scope, Source: source,
			Area:  area(c),
			Shown: shown[c.ID], Adopt: adopt[c.ID], Helped: helped[c.ID],
			Cluster: -1, CohortCluster: -1,
		})
	}

	// Tag edges: pairs sharing a tag, weighted by how many.
	for i := range g.Nodes {
		for j := i + 1; j < len(g.Nodes); j++ {
			if w := overlap(setOf(g.Nodes[i].Tags), g.Nodes[j].Tags); w > 0 {
				g.TagEdges = append(g.TagEdges, Edge{A: i, B: j, W: w})
			}
		}
	}
	// Cohort edges: pairs adopted by the same cohort, weighted by how many shared.
	adoptedNodes := 0
	for i := range g.Nodes {
		if cohorts[g.Nodes[i].ID] != nil {
			adoptedNodes++
		}
		for j := i + 1; j < len(g.Nodes); j++ {
			if w := sharedKeys(cohorts[g.Nodes[i].ID], cohorts[g.Nodes[j].ID]); w > 0 {
				g.CohortEdges = append(g.CohortEdges, Edge{A: i, B: j, W: w})
			}
		}
	}

	// Communities are found BEFORE layout so the synthetic Ungrouped bucket can
	// shape it: every technique belongs to a group, and a group must be a
	// drawable region, so the bucket's members are pulled together by
	// layout-only gather edges instead of scattering across the canvas. The
	// gather ties never render and never count toward degree — "isolated" in
	// the summary still means what it says.
	g.Clusters = detectClusters(g.Nodes, g.TagEdges)
	g.CohortDim, g.CohortClusters = cohortClusters(g.Nodes, cohortCounts)
	tagGather := gatherUngrouped(&g.Clusters, g.Nodes, func(nd *Node) *int { return &nd.Cluster })
	cohortGather := gatherUngrouped(&g.CohortClusters, g.Nodes, func(nd *Node) *int { return &nd.CohortCluster })

	n := len(g.Nodes)
	tagEdgesAll := append(append([]Edge{}, g.TagEdges...), tagGather...)
	cohortEdgesAll := append(append([]Edge{}, g.CohortEdges...), cohortGather...)
	tagPos := layoutPos(n, tagEdgesAll)
	cohortPos := layoutPos(n, cohortEdgesAll)
	// The same two arrangements in three dimensions, for the WebGL map. No
	// de-overlap pass follows this one: two nodes that project to the same point
	// in 3-D are at different depths, and depth ordering plus the fog already
	// tell them apart — nudging them would only distort an embedding that has a
	// whole extra axis to resolve itself in.
	tagPos3 := layout3(n, tagEdgesAll)
	cohortPos3 := layout3(n, cohortEdgesAll)
	// Keep techniques from rendering on top of each other: the force layout can
	// pile a weakly-tied pair into nearly the same spot, and normalize's margin
	// clamp can collapse two of them onto the exact same point. Nudge overlaps
	// apart, sized by how big each node draws (adoption-based, larger for org).
	maxAdopt := 0
	for i := range g.Nodes {
		if g.Nodes[i].Adopt > maxAdopt {
			maxAdopt = g.Nodes[i].Adopt
		}
	}
	sizeTarget := mapSizeTarget(n)
	radii := make([]float64, n)
	for i := range g.Nodes {
		radii[i] = nodeRadius(g.Nodes[i].Adopt, maxAdopt, g.Nodes[i].Scope == "org", sizeTarget)
	}
	separate(tagPos, radii)
	separate(cohortPos, radii)
	for i := range g.Nodes {
		g.Nodes[i].X, g.Nodes[i].Y = tagPos[i][0], tagPos[i][1]
		g.Nodes[i].CX, g.Nodes[i].CY = cohortPos[i][0], cohortPos[i][1]
		g.Nodes[i].X3, g.Nodes[i].Y3, g.Nodes[i].Z3 = tagPos3[i][0], tagPos3[i][1], tagPos3[i][2]
		g.Nodes[i].CX3, g.Nodes[i].CY3, g.Nodes[i].CZ3 = cohortPos3[i][0], cohortPos3[i][1], cohortPos3[i][2]
		for _, e := range g.TagEdges {
			if e.W >= drawThreshold && (e.A == i || e.B == i) {
				g.Nodes[i].Deg++
			}
		}
	}
	// Never leave the edge slices nil: a nil slice marshals to JSON null, and the
	// client does tagEdges.forEach(...), which throws on null and blanks the whole
	// map. A filtered view with no shared tags (e.g. only org-scoped techniques)
	// hits exactly this.
	if g.TagEdges == nil {
		g.TagEdges = []Edge{}
	}
	if g.CohortEdges == nil {
		g.CohortEdges = []Edge{}
	}
	g.Summary = summarize(g)
	// The cohort graph carries structure only if the adopting cohorts differ. If
	// nearly every pair of adopted techniques is linked, one cohort adopts
	// nearly everything and the arrangement says nothing.
	if adoptedNodes >= 3 {
		possible := adoptedNodes * (adoptedNodes - 1) / 2
		g.CohortDense = float64(len(g.CohortEdges)) >= 0.6*float64(possible)
	}
	return g
}

func summarize(g Graph) GraphSummary {
	s := GraphSummary{Total: len(g.Nodes)}
	for _, n := range g.Nodes {
		if n.Deg == 0 {
			s.Isolated++
		}
		if n.Adopt > 0 {
			s.Adopted++
		}
		if n.Helped > 0 {
			s.Helped++
		}
	}
	s.Connected = s.Total - s.Isolated
	for _, e := range g.TagEdges {
		if e.W >= drawThreshold {
			s.Edges2++
		}
	}
	return s
}

// gatherUngrouped appends the synthetic "Ungrouped" cluster: every node the
// detection left outside all communities, stamped with the new cluster id via
// field. It returns weak layout-only ties among those members so they gather
// into one drawable region. When there are no real communities the arrangement
// has no groups at all, and one bucket spanning the whole library would say
// nothing — so none is added.
func gatherUngrouped(clusters *[]Cluster, nodes []Node, field func(*Node) *int) []Edge {
	if len(*clusters) == 0 {
		return nil
	}
	var left []int
	for i := range nodes {
		if *field(&nodes[i]) < 0 {
			left = append(left, i)
		}
	}
	if len(left) == 0 {
		return nil
	}
	id := len(*clusters)
	for _, i := range left {
		*field(&nodes[i]) = id
	}
	*clusters = append(*clusters, Cluster{ID: id, Members: left, Name: "Ungrouped", Ungrouped: true})
	edges := make([]Edge, 0, len(left)*(len(left)-1)/2)
	for i := 0; i < len(left); i++ {
		for j := i + 1; j < len(left); j++ {
			edges = append(edges, Edge{A: left[i], B: left[j], W: 1})
		}
	}
	return edges
}

// area is the technique's home region: its first task type, else its first tag,
// else nothing. Same rule the organization technique map uses for its columns.
func area(c models.Technique) string {
	if len(c.TaskTypes) > 0 {
		return c.TaskTypes[0]
	}
	if len(c.Tags) > 0 {
		return c.Tags[0]
	}
	return "—"
}

func setOf(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func overlap(set map[string]bool, other []string) int {
	n := 0
	for _, s := range other {
		if set[s] {
			n++
		}
	}
	return n
}

func sharedKeys(a, b map[string]bool) int {
	if a == nil || b == nil {
		return 0
	}
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return n
}

// layoutPos places every node in the unit square. It lays out each CONNECTED
// COMPONENT of the graph on its own, then packs the components side by side —
// rather than embedding the whole graph in one force simulation.
//
// The reason is a structural weakness of force layout on disconnected graphs.
// Between two components there are no edges, so the only force acting across the
// gap is repulsion; the sole restoring pull is the weak centre gravity. A small
// island (most often the synthetic "Ungrouped" bucket, whose members share no
// tags with any community) therefore drifts to the far corner, and normalize
// then squashes the whole main mass into the opposite one — a tiny group of
// orphans commandeering half the canvas. The distance across that gap encodes
// nothing real: with no edges its magnitude is repulsion noise, not a measure
// of how different the island is. Packing removes that misleading distance while
// keeping the distances that DO carry meaning — those live WITHIN a component
// (communities linked by shared tags stay in one component and are laid out
// together, exactly as before). The gaps between packed components are the
// deliberate, uniform signal that they don't connect.
func layoutPos(n int, edges []Edge) [][2]float64 {
	if n <= 1 {
		return simulate(n, edges) // 0 → empty, 1 → centred; never normalized to a corner
	}
	comps := connectedComponents(n, edges)
	if len(comps) == 1 {
		// One component: nothing to pack, so this is byte-for-byte the old path.
		return normalize(simulate(n, edges))
	}
	return packComponents(n, comps, edges)
}

// simulate runs the weighted Fruchterman–Reingold spring embedding and returns
// raw (un-normalized) positions. Seeded from a constant so the same graph always
// lands in the same place — the map must not jump on every reload. Every pair
// repels; edges attract (harder when heavier); a gentle pull to the centre keeps
// a component's own weakly-tied nodes from drifting off.
func simulate(n int, edges []Edge) [][2]float64 {
	pos := make([][2]float64, n)
	if n == 0 {
		return pos
	}
	rng := rand.New(rand.NewSource(1))
	for i := range pos {
		pos[i] = [2]float64{rng.Float64(), rng.Float64()}
	}
	if n == 1 {
		pos[0] = [2]float64{0.5, 0.5}
		return pos
	}
	k := 0.9 / math.Sqrt(float64(n))
	temp := 0.10
	iters := 400
	if n > 120 {
		iters = 220
	}
	disp := make([][2]float64, n)
	for it := 0; it < iters; it++ {
		for i := range disp {
			disp[i] = [2]float64{}
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx, dy := pos[i][0]-pos[j][0], pos[i][1]-pos[j][1]
				d := math.Hypot(dx, dy)
				if d < 1e-4 {
					d = 1e-4
				}
				f := k * k / d
				ux, uy := dx/d, dy/d
				disp[i][0] += ux * f
				disp[i][1] += uy * f
				disp[j][0] -= ux * f
				disp[j][1] -= uy * f
			}
		}
		for _, e := range edges {
			dx, dy := pos[e.A][0]-pos[e.B][0], pos[e.A][1]-pos[e.B][1]
			d := math.Hypot(dx, dy)
			if d < 1e-4 {
				d = 1e-4
			}
			f := d * d / k * (0.5 + 0.5*float64(e.W))
			ux, uy := dx/d, dy/d
			disp[e.A][0] -= ux * f
			disp[e.A][1] -= uy * f
			disp[e.B][0] += ux * f
			disp[e.B][1] += uy * f
		}
		var cx, cy float64
		for _, p := range pos {
			cx += p[0]
			cy += p[1]
		}
		cx /= float64(n)
		cy /= float64(n)
		for i := 0; i < n; i++ {
			disp[i][0] += (cx - pos[i][0]) * 0.015
			disp[i][1] += (cy - pos[i][1]) * 0.015
			dl := math.Hypot(disp[i][0], disp[i][1])
			if dl < 1e-4 {
				dl = 1e-4
			}
			step := math.Min(dl, temp)
			pos[i][0] += disp[i][0] / dl * step
			pos[i][1] += disp[i][1] / dl * step
		}
		if temp > 0.008 {
			temp *= 0.985
		}
	}
	return pos
}

// connectedComponents groups the n nodes by the components of the (undirected)
// edge graph, via union-find. Nodes with no edge are their own singleton
// component. Ordered largest-first, ties broken by smallest member index, so the
// packing below is deterministic and the dominant mass is placed first.
func connectedComponents(n int, edges []Edge) [][]int {
	if n <= 0 {
		return nil
	}
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, e := range edges {
		if e.A < 0 || e.A >= n || e.B < 0 || e.B >= n {
			continue
		}
		if ra, rb := find(e.A), find(e.B); ra != rb {
			parent[ra] = rb
		}
	}
	groups := map[int][]int{}
	for i := 0; i < n; i++ { // ascending i ⇒ each group's members stay sorted
		r := find(i)
		groups[r] = append(groups[r], i)
	}
	comps := make([][]int, 0, len(groups))
	for _, g := range groups {
		comps = append(comps, g)
	}
	sort.Slice(comps, func(i, j int) bool {
		if len(comps[i]) != len(comps[j]) {
			return len(comps[i]) > len(comps[j])
		}
		return comps[i][0] < comps[j][0]
	})
	return comps
}

// packComponents lays out each component locally, then tiles the components into
// the canvas so none can drift across an empty gap and squash the others. Each
// component gets a square slot whose side ∝ √(node count), so node density is
// even across the map and the busiest component naturally dominates. A uniform
// gap between slots is the "these don't connect" spacing.
func packComponents(n int, comps [][]int, edges []Edge) [][2]float64 {
	type box struct {
		idx   []int
		local [][2]float64
		side  float64
	}
	boxes := make([]box, len(comps))
	var sumSide, area float64
	for ci, members := range comps {
		k := len(members)
		var local [][2]float64
		if k == 1 {
			local = [][2]float64{{0.5, 0.5}}
		} else {
			// Induce the component's own edges under local indices, then run the
			// same sim + normalize it used to get as part of the whole graph.
			localOf := make(map[int]int, k)
			for li, gi := range members {
				localOf[gi] = li
			}
			var le []Edge
			for _, e := range edges {
				a, aok := localOf[e.A]
				b, bok := localOf[e.B]
				if aok && bok {
					le = append(le, Edge{A: a, B: b, W: e.W})
				}
			}
			local = normalize(simulate(k, le))
		}
		side := math.Sqrt(float64(k))
		boxes[ci] = box{idx: members, local: local, side: side}
		sumSide += side
		area += side * side
	}

	// A uniform gap sized off the mean slot, and a target row width that keeps the
	// whole arrangement close to square. The dominant slot always fits its row.
	gap := 0.35 * sumSide / float64(len(boxes))
	rowW := math.Sqrt(area) * 1.4
	if boxes[0].side > rowW {
		rowW = boxes[0].side
	}

	pos := make([][2]float64, n)
	x, y, rowH := 0.0, 0.0, 0.0
	for _, b := range boxes {
		if x > 0 && x+b.side > rowW {
			x = 0
			y += rowH + gap
			rowH = 0
		}
		for li, gi := range b.idx {
			pos[gi] = [2]float64{x + b.local[li][0]*b.side, y + b.local[li][1]*b.side}
		}
		x += b.side + gap
		if b.side > rowH {
			rowH = b.side
		}
	}
	return normalize(pos)
}

// Map node sizing (mirrored verbatim in the client's radius()/outerR() in
// techniquemap.go — keep the two in sync). Node size scales with adoption RELATIVE to
// the busiest node, so the map reads the same whether the org has ten adoptions
// or ten thousand; it is bounded by a count-aware target so N nodes never
// overflow the canvas into an unreadable pile. Absolute counts still live in the
// hover/detail; only the drawn size is normalized.
const (
	mapFloorR    = 5.0     // radius of a never-adopted node (px)
	mapMinTarget = 18.0    // smallest the busiest node may shrink to (crowded maps)
	mapMaxTarget = 32.0    // largest the busiest node may grow to (sparse maps)
	mapAreaK     = 24000.0 // sizeTarget = sqrt(mapAreaK / nodeCount), clamped
)

// mapSizeTarget is the radius the busiest node draws at — larger when the map is
// sparse, smaller when it is crowded, so the total ink stays within the canvas.
func mapSizeTarget(nodeCount int) float64 {
	if nodeCount < 1 {
		nodeCount = 1
	}
	t := math.Sqrt(mapAreaK / float64(nodeCount))
	if t < mapMinTarget {
		t = mapMinTarget
	}
	if t > mapMaxTarget {
		t = mapMaxTarget
	}
	return t
}

// cohortPreference is the order of segment dimensions to group the cohort map
// by; the first that actually separates the techniques wins. Mirrors the
// registry's CohortPreference (team, then role), with function as a last resort.
var cohortPreference = []string{"team", "role", "function"}

// cohortClusters groups techniques by their "home" cohort — the value, in the
// first grouping dimension that separates them, that adopts each technique most.
// It returns the chosen dimension and the clusters (largest first) and sets each
// node's CohortCluster to its cluster index (left at -1 when the node has no
// adoption in that dimension). Empty when no dimension separates the set (nothing
// adopted, or one cohort adopts everything).
func cohortClusters(nodes []Node, counts map[string]map[string]map[string]int) (string, []Cluster) {
	for _, dim := range cohortPreference {
		dom := make([]string, len(nodes)) // each node's dominant value in this dim
		distinct := map[string]bool{}
		for i := range nodes {
			dom[i] = dominantVal(counts[nodes[i].ID][dim])
			if dom[i] != "" {
				distinct[dom[i]] = true
			}
		}
		if len(distinct) < 2 {
			continue // this dimension does not separate the techniques
		}
		byVal := map[string][]int{}
		for i, v := range dom {
			if v != "" {
				byVal[v] = append(byVal[v], i)
			}
		}
		vals := make([]string, 0, len(byVal))
		for v := range byVal {
			vals = append(vals, v)
		}
		// Largest cohort first; ties by name, so ids and colours are stable.
		sort.Slice(vals, func(a, b int) bool {
			if na, nb := len(byVal[vals[a]]), len(byVal[vals[b]]); na != nb {
				return na > nb
			}
			return vals[a] < vals[b]
		})
		clusters := make([]Cluster, 0, len(vals))
		for id, v := range vals {
			for _, i := range byVal[v] {
				nodes[i].CohortCluster = id
			}
			clusters = append(clusters, Cluster{ID: id, Members: byVal[v], Name: titleizeCohort(v)})
		}
		return dim, clusters
	}
	return "", nil
}

// dominantVal returns the key with the highest count (ties broken by name), or
// "" when the map is empty.
func dominantVal(m map[string]int) string {
	best, bestN := "", -1
	for v, n := range m {
		if n > bestN || (n == bestN && v < best) {
			best, bestN = v, n
		}
	}
	return best
}

// titleizeCohort renders a segment value as an area name: "data-platform" ->
// "Data Platform".
func titleizeCohort(v string) string {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// nodeRadius mirrors the client's radius()/outerR(): a node's radius interpolates
// from the floor (never adopted) up to sizeTarget (as adopted as the busiest
// node), on a sqrt curve, with org-scoped nodes drawing a fifth larger.
func nodeRadius(adopt, maxAdopt int, org bool, sizeTarget float64) float64 {
	if maxAdopt < 1 {
		maxAdopt = 1
	}
	r := mapFloorR + (sizeTarget-mapFloorR)*math.Sqrt(float64(adopt)/float64(maxAdopt))
	if org {
		r *= 1.2
	}
	return r
}

// separate relaxes positions until no two nodes overlap — each pair ends at least
// the sum of their radii (plus a little air) apart. Radii are pixels but positions
// are the [0,1] layout square, so it divides by a reference drawable span; a bigger
// canvas only spreads nodes further, a much smaller one may still touch. It is
// deterministic (fixed iteration order, and a fixed golden-angle direction for
// exactly-coincident nodes) so the map stays stable across reloads.
func separate(pos [][2]float64, radius []float64) {
	n := len(pos)
	if n < 2 {
		return
	}
	const refSpan = 700.0 // approximate drawable width in px
	const gap = 5.0       // air between rims
	sz := make([]float64, n)
	for i := range sz {
		sz[i] = (radius[i] + gap/2) / refSpan
	}
	for it := 0; it < 80; it++ {
		moved := false
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx, dy := pos[j][0]-pos[i][0], pos[j][1]-pos[i][1]
				d := math.Hypot(dx, dy)
				want := sz[i] + sz[j]
				if d >= want {
					continue
				}
				if d < 1e-9 {
					// exactly coincident (e.g. both clamped to the same corner):
					// push along a stable per-pair direction.
					ang := float64(i*7+j) * 2.399963 // golden angle
					dx, dy, d = math.Cos(ang), math.Sin(ang), 1
				}
				push := (want - d) / 2
				ux, uy := dx/d, dy/d
				pos[i][0] -= ux * push
				pos[i][1] -= uy * push
				pos[j][0] += ux * push
				pos[j][1] += uy * push
				moved = true
			}
		}
		for i := 0; i < n; i++ {
			pos[i][0] = clamp01m(pos[i][0])
			pos[i][1] = clamp01m(pos[i][1])
		}
		if !moved {
			break
		}
	}
	for i := 0; i < n; i++ {
		pos[i][0] = math.Round(pos[i][0]*1e4) / 1e4
		pos[i][1] = math.Round(pos[i][1]*1e4) / 1e4
	}
}

// clamp01m keeps a coordinate on-canvas during separation. Its margin is smaller
// than normalize's so it does not pull soft-clamped outliers (which sit in the
// [0,0.05]/[0.95,1] bands) back onto the 0.05/0.95 line and re-collapse them.
func clamp01m(v float64) float64 {
	const edge = 0.02
	if v < edge {
		return edge
	}
	if v > 1-edge {
		return 1 - edge
	}
	return v
}

func normalize(pos [][2]float64) [][2]float64 {
	n := len(pos)
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i, p := range pos {
		xs[i], ys[i] = p[0], p[1]
	}
	// Robust bounds: fit the box to the bulk, not to the extremes. A disconnected
	// node feels only repulsion, so it can drift far from the mass; with raw
	// min/max that single outlier defines the box and squashes every other node
	// into a corner. Trim the most extreme few per axis instead — anything past the
	// trimmed range clamps to the margin, so a runaway node sits at the edge rather
	// than collapsing the whole layout.
	minX, maxX := trimmedRange(xs)
	minY, maxY := trimmedRange(ys)
	sx, sy := maxX-minX, maxY-minY
	if sx < 1e-9 {
		sx = 1
	}
	if sy < 1e-9 {
		sy = 1
	}
	// A small margin so no node sits flush against the canvas edge. Values past the
	// trimmed range are SOFT-clamped: compressed monotonically into the thin margin
	// band rather than pinned to the edge. A hard clamp maps every runaway to the
	// exact same corner point, so two distinct outliers collapse onto one another
	// (and no de-overlap can tell them apart); the soft clamp stays injective, so
	// distinct nodes stay distinct and the later separation only has to nudge, not
	// unpile.
	const m = 0.05
	const soften = 0.12 // compression scale for out-of-band outliers
	soft := func(t float64) float64 {
		v := t
		if t < m {
			e := m - t
			v = m * soften / (e + soften) // (0, m), order-preserving
		} else if t > 1-m {
			e := t - (1 - m)
			v = (1 - m) + m*e/(e+soften) // (1-m, 1), order-preserving
		}
		return math.Round(v*1e4) / 1e4
	}
	out := make([][2]float64, n)
	for i, p := range pos {
		out[i] = [2]float64{
			soft(m + (p[0]-minX)/sx*(1-2*m)),
			soft(m + (p[1]-minY)/sy*(1-2*m)),
		}
	}
	return out
}

// trimmedRange returns the min and max of v after dropping the most extreme value
// at each end (a few more when there are many nodes), so lone runaway nodes don't
// set the bounds. Falls back to the full range if trimming would collapse it.
func trimmedRange(v []float64) (lo, hi float64) {
	n := len(v)
	if n == 0 {
		return 0, 1
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if n < 5 {
		return s[0], s[n-1]
	}
	t := n / 20 // ~5% each end
	if t < 1 {
		t = 1
	}
	lo, hi = s[t], s[n-1-t]
	if hi-lo < 1e-9 {
		lo, hi = s[0], s[n-1]
	}
	return lo, hi
}
