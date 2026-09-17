// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package techmap

import (
	"math"
	"math/rand"
)

// The 3-D embedding, for the WebGL map. Same graph, same forces, same
// determinism as the 2-D layout above — one dimension richer.
//
// It is a parallel path rather than a dimension-generic rewrite of layoutPos on
// purpose. The 2-D layout is tuned, shipped, and still the only thing the canvas
// fallback and the MCP app can draw; making it generic would have changed its
// output in the last bits (math.Hypot is not sqrt(dx²+dy²)) for no gain to the
// reader. What genuinely IS shared — connectedComponents, trimmedRange, the
// force constants — is called, not copied.
//
// The client never runs a layout of its own: positions arrive precomputed and
// world-space, so the same registry draws the same map on every device and
// every reload, and the renderer only has to paint.

// layout3 places n nodes in the unit cube: one component gets the plain
// embedding, several get packed into slots so a distant island cannot squash the
// mass that matters.
func layout3(n int, edges []Edge) [][3]float64 {
	if n <= 1 {
		return simulate3(n, edges) // 0 → empty, 1 → centred; never normalized to a corner
	}
	comps := connectedComponents(n, edges)
	if len(comps) == 1 {
		return normalize3(simulate3(n, edges))
	}
	return pack3(n, comps, edges)
}

// simulate3 is the weighted Fruchterman–Reingold embedding in three dimensions:
// every pair repels, edges attract in proportion to their weight, and a gentle
// pull to the centroid keeps a component's weakly-tied nodes from wandering off.
// Seeded from the same constant as the 2-D sim, so a graph lands in the same
// place on every build — the map must not jump on reload.
//
// k is the ideal edge length, and it is the one constant that has to change with
// the dimension: n nodes spread through a cube have more room between them than
// the same n on a square, so the 2-D k (0.9/√n) would leave the field limp and
// evenly spaced, with no visible clustering. The cube-root form keeps the
// density — and so the clumping the eye reads as areas of practice — the same.
func simulate3(n int, edges []Edge) [][3]float64 {
	pos := make([][3]float64, n)
	if n == 0 {
		return pos
	}
	rng := rand.New(rand.NewSource(1))
	for i := range pos {
		pos[i] = [3]float64{rng.Float64(), rng.Float64(), rng.Float64()}
	}
	if n == 1 {
		pos[0] = [3]float64{0.5, 0.5, 0.5}
		return pos
	}
	k := 0.9 / math.Cbrt(float64(n))
	temp := 0.10
	iters := 400
	if n > 120 {
		iters = 220
	}
	disp := make([][3]float64, n)
	for it := 0; it < iters; it++ {
		for i := range disp {
			disp[i] = [3]float64{}
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx, dy, dz := pos[i][0]-pos[j][0], pos[i][1]-pos[j][1], pos[i][2]-pos[j][2]
				d := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if d < 1e-4 {
					d = 1e-4
				}
				f := k * k / d
				ux, uy, uz := dx/d, dy/d, dz/d
				disp[i][0] += ux * f
				disp[i][1] += uy * f
				disp[i][2] += uz * f
				disp[j][0] -= ux * f
				disp[j][1] -= uy * f
				disp[j][2] -= uz * f
			}
		}
		for _, e := range edges {
			dx := pos[e.A][0] - pos[e.B][0]
			dy := pos[e.A][1] - pos[e.B][1]
			dz := pos[e.A][2] - pos[e.B][2]
			d := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if d < 1e-4 {
				d = 1e-4
			}
			f := d * d / k * (0.5 + 0.5*float64(e.W))
			ux, uy, uz := dx/d, dy/d, dz/d
			disp[e.A][0] -= ux * f
			disp[e.A][1] -= uy * f
			disp[e.A][2] -= uz * f
			disp[e.B][0] += ux * f
			disp[e.B][1] += uy * f
			disp[e.B][2] += uz * f
		}
		var cx, cy, cz float64
		for _, p := range pos {
			cx += p[0]
			cy += p[1]
			cz += p[2]
		}
		cx /= float64(n)
		cy /= float64(n)
		cz /= float64(n)
		for i := 0; i < n; i++ {
			disp[i][0] += (cx - pos[i][0]) * 0.015
			disp[i][1] += (cy - pos[i][1]) * 0.015
			disp[i][2] += (cz - pos[i][2]) * 0.015
			dl := math.Sqrt(disp[i][0]*disp[i][0] + disp[i][1]*disp[i][1] + disp[i][2]*disp[i][2])
			if dl < 1e-4 {
				dl = 1e-4
			}
			step := math.Min(dl, temp)
			pos[i][0] += disp[i][0] / dl * step
			pos[i][1] += disp[i][1] / dl * step
			pos[i][2] += disp[i][2] / dl * step
		}
		if temp > 0.008 {
			temp *= 0.985
		}
	}
	return pos
}

// pack3 lays out each component on its own, then arranges the components in
// space: the largest at the centre, the rest on shells around it, spread by the
// golden angle so no two share a bearing.
//
// The 2-D packer tiles components into a grid of square slots, and that is right
// on a plane — rows read as rows. Tried in the cube it was badly wrong: a
// registry's long tail is dozens of single-technique components, and a regular
// lattice of them, seen in perspective, becomes a chain of evenly spaced dots
// marching to the horizon. The eye reads that as structure, and there is none —
// those techniques have nothing to do with each other, which is the whole reason they
// are singletons. On shells they read as what they are: loose material around a
// core, with no order implied by where they sit.
func pack3(n int, comps [][]int, edges []Edge) [][3]float64 {
	type box struct {
		idx   []int
		local [][3]float64
		side  float64
	}
	boxes := make([]box, len(comps))
	var sumSide float64
	for ci, members := range comps {
		k := len(members)
		var local [][3]float64
		if k == 1 {
			local = [][3]float64{{0.5, 0.5, 0.5}}
		} else {
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
			local = normalize3(simulate3(k, le))
		}
		side := math.Cbrt(float64(k))
		boxes[ci] = box{idx: members, local: local, side: side}
		sumSide += side
	}

	gap := 0.35 * sumSide / float64(len(boxes))

	// place writes a box's local cube centred on c.
	pos := make([][3]float64, n)
	place := func(b box, c [3]float64) {
		for li, gi := range b.idx {
			pos[gi] = [3]float64{
				c[0] + (b.local[li][0]-0.5)*b.side,
				c[1] + (b.local[li][1]-0.5)*b.side,
				c[2] + (b.local[li][2]-0.5)*b.side,
			}
		}
	}
	place(boxes[0], [3]float64{0, 0, 0})
	core := boxes[0].side * 0.5

	// Satellites on a Fibonacci sphere: the golden angle in longitude and an
	// even sweep in latitude, which is the standard way to scatter points on a
	// sphere without them banding at the poles or lining up in columns. Shells of
	// twelve, each further out, so a long tail keeps its distance from the mass
	// instead of burying it.
	const perShell = 12
	ga := math.Pi * (3 - math.Sqrt(5))
	for si, b := range boxes[1:] {
		shell := si / perShell
		k := si % perShell
		// -1..1 across the shell, offset by half a step off the exact poles.
		u := 1 - 2*(float64(k)+0.5)/float64(perShell)
		rxy := math.Sqrt(math.Max(0, 1-u*u))
		th := ga * float64(si)
		dir := [3]float64{math.Cos(th) * rxy, u, math.Sin(th) * rxy}
		r := core + gap + b.side*0.7 + float64(shell)*(gap+b.side*0.9)
		place(b, [3]float64{dir[0] * r, dir[1] * r, dir[2] * r})
	}
	return normalize3(pos)
}

// normalize3 fits the embedding into the unit cube on the 2-D normalize's terms:
// bounds trimmed so one runaway node cannot squash everything else into a
// corner, and out-of-band outliers soft-clamped into the margin band rather than
// pinned to it, which keeps distinct nodes distinct.
func normalize3(pos [][3]float64) [][3]float64 {
	n := len(pos)
	out := make([][3]float64, n)
	if n == 0 {
		return out
	}
	var lo, hi, span [3]float64
	for ax := 0; ax < 3; ax++ {
		v := make([]float64, n)
		for i, p := range pos {
			v[i] = p[ax]
		}
		lo[ax], hi[ax] = trimmedRange(v)
		span[ax] = hi[ax] - lo[ax]
		if span[ax] < 1e-9 {
			span[ax] = 1
		}
	}
	const m = 0.05
	const soften = 0.12
	soft := func(t float64) float64 {
		v := t
		if t < m {
			e := m - t
			v = m * soften / (e + soften)
		} else if t > 1-m {
			e := t - (1 - m)
			v = (1 - m) + m*e/(e+soften)
		}
		return math.Round(v*1e4) / 1e4
	}
	for i, p := range pos {
		for ax := 0; ax < 3; ax++ {
			out[i][ax] = soft(m + (p[ax]-lo[ax])/span[ax]*(1-2*m))
		}
	}
	return out
}
