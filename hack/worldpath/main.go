// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// worldpath turns Natural Earth's land polygons into the one path the Access
// picture's globe is drawn from. See README.md for where the input comes from
// and what to do with the output.
//
//	go run ./hack/worldpath -in ne_110m_land.json
//
// The output is PLAIN GEOGRAPHY, not a picture: x = lon+180, y = 90-lat, one
// unit to the degree. The globe projects it — in Go for the still the server
// sends, and in the browser once a frame as it turns — so the shape that ships
// is the earth rather than one view of it.
//
// Simplification runs on the UNIT SPHERE, in three dimensions, which is the only
// place a tolerance means the same thing everywhere: a degree of longitude is a
// degree wide at the equator and nothing at the pole, so a tolerance in degrees
// keeps detail around the Arctic that no screen can show and spends bytes on it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

type collection struct {
	Features []struct {
		Geometry struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		} `json:"geometry"`
	} `json:"features"`
}

type pt struct{ x, y float64 }

func main() {
	in := flag.String("in", "", "Natural Earth land GeoJSON (110m or 50m)")
	tol := flag.Float64("tol", .35, "simplification tolerance, in output units")
	min := flag.Float64("min", 3, "drop islands smaller than this, in square output units")
	flag.Parse()

	raw, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var fc collection
	if err := json.Unmarshal(raw, &fc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var rings [][]pt
	for _, f := range fc.Features {
		switch f.Geometry.Type {
		case "Polygon":
			var poly [][][2]float64
			must(json.Unmarshal(f.Geometry.Coordinates, &poly))
			rings = append(rings, project(poly)...)
		case "MultiPolygon":
			var multi [][][][2]float64
			must(json.Unmarshal(f.Geometry.Coordinates, &multi))
			for _, poly := range multi {
				rings = append(rings, project(poly)...)
			}
		}
	}

	var kept [][]pt
	var points int
	for _, r := range rings {
		r = simplify(r, *tol)
		if len(r) < 4 || math.Abs(area(r)) < *min {
			continue
		}
		kept = append(kept, r)
		points += len(r)
	}

	fmt.Println(path(kept))
	fmt.Fprintf(os.Stderr, "%d rings, %d points, %d bytes\n", len(kept), points, len(path(kept)))
}

// project takes a polygon's rings — the outer one and any holes — and returns
// each in the 360x180 degree box. Holes are kept: an inland sea is sea.
//
// IT ALSO FIXES THE WINDING, which the globe depends on and the source does not
// promise. A ring that runs over the edge of the world is closed with an arc of
// the limb, and which way that arc goes cannot be read off the picture — the
// projection of an edge crossing the horizon runs out to the rim and back along
// itself. It comes from the winding instead: land on the left, a lake the other
// way. GeoJSON says the outer ring comes first, so the roles are known here and
// nowhere later; a ring wound against its role is reversed on the way past.
func project(poly [][][2]float64) [][]pt {
	var out [][]pt
	for i, ring := range poly {
		r := make([]pt, 0, len(ring))
		for _, c := range ring {
			r = append(r, pt{c[0] + 180, 90 - c[1]})
		}
		if outer := i == 0; outer != (winding(r) > 0) {
			for a, b := 0, len(r)-1; a < b; a, b = a+1, b-1 {
				r[a], r[b] = r[b], r[a]
			}
		}
		out = append(out, r)
	}
	return out
}

// winding is the ring's signed area in lon and lat — positive counter-clockwise
// seen from outside the sphere, which is a coastline with its land on the left.
// The stored y counts DOWN from the north pole, so the sign flips coming out.
func winding(r []pt) float64 {
	var a float64
	for i := range r {
		j := (i + 1) % len(r)
		a += r[i].x*r[j].y - r[j].x*r[i].y
	}
	return -a / 2
}

// globe turns a point in the degree box into one on the unit sphere, which is
// where both the tolerance and the area mean what they say.
func globe(p pt) [3]float64 {
	lon, lat := (p.x-180)*math.Pi/180, (90-p.y)*math.Pi/180
	return [3]float64{math.Cos(lat) * math.Sin(lon), math.Sin(lat), math.Cos(lat) * math.Cos(lon)}
}

// simplify is Douglas-Peucker on the unit sphere, with the tolerance given in
// the globe's own units — the radius is 90 of them, so the distance it allows is
// the distance you would measure on the drawing.
func simplify(r []pt, tol float64) []pt {
	if len(r) < 3 {
		return r
	}
	keep := make([]bool, len(r))
	keep[0], keep[len(r)-1] = true, true
	var walk func(a, b int)
	walk = func(a, b int) {
		if b-a < 2 {
			return
		}
		far, worst := a, tol/90
		for i := a + 1; i < b; i++ {
			if d := dist(globe(r[i]), globe(r[a]), globe(r[b])); d > worst {
				far, worst = i, d
			}
		}
		if far == a {
			return
		}
		keep[far] = true
		walk(a, far)
		walk(far, b)
	}
	walk(0, len(r)-1)
	out := r[:0:0]
	for i, k := range keep {
		if k {
			out = append(out, r[i])
		}
	}
	return out
}

// dist is the distance from p to the segment ab, in three dimensions.
func dist(p, a, b [3]float64) float64 {
	var d, l2, t float64
	for i := 0; i < 3; i++ {
		d = b[i] - a[i]
		l2 += d * d
		t += (p[i] - a[i]) * (b[i] - a[i])
	}
	if l2 == 0 {
		return length(p, a)
	}
	t = math.Max(0, math.Min(1, t/l2))
	var q [3]float64
	for i := 0; i < 3; i++ {
		q[i] = a[i] + t*(b[i]-a[i])
	}
	return length(p, q)
}

func length(a, b [3]float64) float64 {
	var sum float64
	for i := 0; i < 3; i++ {
		sum += (a[i] - b[i]) * (a[i] - b[i])
	}
	return math.Sqrt(sum)
}

// area is the ring's area FACE ON, in square drawing units: the sphere points
// turned so the ring's own middle faces the viewer, then measured. An island
// keeps or loses its place by the size it has when the globe is showing it,
// which is the only size that decides whether anybody can see it.
func area(r []pt) float64 {
	var cx, cy float64
	for _, p := range r {
		cx, cy = cx+p.x, cy+p.y
	}
	lon0, lat0 := (cx/float64(len(r))-180)*math.Pi/180, (90-cy/float64(len(r)))*math.Pi/180
	flat := make([][2]float64, len(r))
	for i, p := range r {
		lon, lat := (p.x-180)*math.Pi/180, (90-p.y)*math.Pi/180
		flat[i] = [2]float64{
			90 * math.Cos(lat) * math.Sin(lon-lon0),
			90 * (math.Cos(lat0)*math.Sin(lat) - math.Sin(lat0)*math.Cos(lat)*math.Cos(lon-lon0)),
		}
	}
	var a float64
	for i := range flat {
		j := (i + 1) % len(flat)
		a += flat[i][0]*flat[j][1] - flat[j][0]*flat[i][1]
	}
	return a / 2
}

// path writes the rings as one path. Coordinates are rounded to a tenth of a
// unit — a twentieth of a pixel at the size the globe is drawn — and then the
// deltas are taken from the ROUNDED positions, so nothing drifts along a ring.
func path(rings [][]pt) string {
	var b strings.Builder
	for _, r := range rings {
		var cx, cy float64
		for i, p := range r {
			x, y := round(p.x), round(p.y)
			if i == 0 {
				b.WriteString("M" + pair(x, y))
			} else if x != cx || y != cy {
				b.WriteString("l" + pair(round(x-cx), round(y-cy)))
			}
			cx, cy = x, y
		}
		b.WriteString("Z")
	}
	return b.String()
}

func round(v float64) float64 { return math.Round(v*10) / 10 }

// pair writes two numbers with a separator only where one is needed. A minus
// sign always ends the number before it. A leading dot only does so when that
// number already has one — "2" and ".2" run together read as the single number
// 2.2, which is a coastline in the wrong place.
func pair(x, y float64) string {
	a, b := num(x), num(y)
	if strings.HasPrefix(b, "-") || (strings.HasPrefix(b, ".") && strings.Contains(a, ".")) {
		return a + b
	}
	return a + " " + b
}

// num writes a number as short as it goes: no trailing zero, and no leading
// zero on a fraction, where the dot is separator enough.
func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	if strings.HasPrefix(s, "0.") {
		return s[1:]
	}
	if strings.HasPrefix(s, "-0.") {
		return "-" + s[2:]
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
