// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"math"
	"strconv"
	"strings"
)

// The globe, projected.
//
// It is an ORTHOGRAPHIC projection of a sphere — the view from far enough away
// that the rays are parallel — and every frame of the turn is one. It was a flat
// map rolling behind a round window, which is cheap and reads as exactly what it
// is: Africa stayed Africa-width at the rim, where a sphere would have squeezed
// it to nothing, and the eye calls that out immediately.
//
// THE HORIZON IS A CLIP, AND THE LIMB CLOSES IT. A ring that runs over the edge
// of the world is cut where it crosses, and the two cut ends are joined by an arc
// of the limb itself — which is what the far side of that coastline actually
// looks like from here.
//
// It was tried the cheap way first: keep every point, and push the hidden ones
// out to the rim they went round. That is four lines, it cannot fold, and it is
// continuous at the horizon — but it is wrong wherever a coastline passes near
// the point opposite the viewer, because the direction to that point is
// undefined and the rim position swings right across the disc. Eurasia going
// round the back threw a green wedge over the Pacific once a turn.
//
// WHICH WAY THE ARC GOES is the whole difficulty, and it is not a local
// question. It was first read off the drawing's own velocity where the ring
// crossed, which is wrong for a reason worth writing down: the projection of a
// straight edge that passes behind the horizon runs out to the rim and back
// along itself, so at the crossing it has no sideways part at all. A coastline
// heading due east over the edge of the world gives no clue in the picture about
// which way it went round.
//
// It comes from the RING'S OWN WINDING instead. These outlines are wound with
// the land on the left — the GeoJSON convention, and a lake inside one is wound
// the other way — so along the limb the land is the side the disc is on, and
// that fixes the direction once for the whole ring. One shoelace sum, no
// sampling, and nothing to go wrong where the geometry is awkward.
//
// ALL OF IT RUNS TWICE — here for the still the server sends, and again in the
// browser for every frame of the turn (globeScript) — because the page has to be
// right before a script arrives and after one does. The two are held together
// from both ends: they take the angle, the tilt and the period from the same
// constants, and the browser's first frame comes out as the same string, to the
// byte, as the still it replaces. Change the projection here and change it
// there.
const (
	globeR    = 90.0 // the disc's radius, in the units worldLandPath is written in
	globeTilt = 16.0 // degrees north of the equator the view is taken from
	// The longitude in the middle of the still the server sends. It faces the
	// Atlantic because that is where two of the three clients are up and both
	// are well clear of the limb: a first paint with one client on it, or with
	// one balanced on the rim, is the picture arriving looking broken.
	globeFace = -60
	globeTurn = 26 // seconds for one revolution
)

// WHERE THE CLIENTS ARE. Five cities, and the reaches on the Access picture end
// on them — so a client is a place on the earth rather than a dot on the plate.
// It turns with the world, and it goes round the back with it.
//
// West to east, which is the order they come round in, and the order the
// stylesheet's .acc-beam1 to .acc-beam5 are in. The list is the count: the scene
// draws one reach per city and the arrivals are spaced by how many there are.
var globeCities = [][2]float64{
	{37.77, -122.42}, // San Francisco
	{-23.55, -46.63}, // São Paulo
	{51.51, -0.13},   // London
	{35.68, 139.69},  // Tokyo
	{-33.87, 151.21}, // Sydney
}

// The scene's own grid, in the em the stylesheet measures the picture in. These
// are the only numbers here that also live in app.css — where the globe sits,
// how big its disc is, and where the reaches start once the switch is on —
// because a client's position is a question about the sphere AND about the
// plate. app.css is where a coordinate belongs and this is where the arithmetic
// is; TestAccessSceneClientsSitOnTheirCities holds the two together.
const (
	sceneGlobeX   = 21.0 // .acc-globe, the middle of the disc: left + half the box
	sceneGlobeY   = 8.0
	sceneGlobeBox = 9.6  // .acc-globe, the side of the box the disc is drawn in
	sceneReachX   = 13.2 // .acc[data-on="1"] .acc-beam, where every reach starts
	sceneReachY   = 8.0
	// The disc fills the viewBox but for the two units of slack the hairline
	// needs on each side, so the radius on the plate is less than half the box.
	sceneGlobeR = sceneGlobeBox * globeR / (2*globeR + 4)
)

// globeClientAt puts one client where its city has turned to: the coastlines'
// own projection, and then the reach that has to arrive there — a length and an
// angle from where the reaches start, in the picture's grid.
//
// A city on the far side has nothing to draw. Its projection lands back inside
// the disc, mirrored, which would be a dot on the wrong ocean, so the reach that
// ends on it is not drawn at all.
func globeClientAt(lat, lon, lon0 float64) (length, angle float64, near bool) {
	cp, sp := math.Cos(globeTilt*math.Pi/180), math.Sin(globeTilt*math.Pi/180)
	c0, s0 := math.Cos(lon0*math.Pi/180), math.Sin(lon0*math.Pi/180)
	rlat, rlon := lat*math.Pi/180, lon*math.Pi/180
	a := math.Cos(rlat) * math.Sin(rlon)
	c := math.Cos(rlat) * math.Cos(rlon)
	u := c*c0 + a*s0
	x := a*c0 - c*s0
	y := cp*math.Sin(rlat) - sp*u
	dx := sceneGlobeX + sceneGlobeR*x - sceneReachX
	dy := sceneGlobeY - sceneGlobeR*y - sceneReachY
	return math.Hypot(dx, dy), math.Atan2(dy, dx) * 180 / math.Pi, sp*math.Sin(rlat)+cp*u >= 0
}

// globeCityJS is the same three cities, handed to the script — one list, read
// twice, the way the tilt and the period are.
func globeCityJS() string {
	var b strings.Builder
	b.WriteByte('[')
	for i, c := range globeCities {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("[" + sceneNum(c[0]) + "," + sceneNum(c[1]) + "]")
	}
	b.WriteByte(']')
	return b.String()
}

// sceneNum writes a number for the script exactly as Go holds it, unrounded:
// these are inputs the two sides share, not output either of them draws.
func sceneNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// worldRings parses worldLandPath into coastlines in degrees — lon, lat. The
// path is the generator's own three commands (hack/worldpath): an absolute M
// opens a ring, relative l steps along it, Z closes it.
func worldRings() [][][2]float64 {
	var rings [][][2]float64
	var ring [][2]float64
	var x, y float64
	d := worldLandPath
	for i := 0; i < len(d); {
		cmd := d[i]
		i++
		if cmd == 'Z' {
			if len(ring) > 2 {
				rings = append(rings, ring)
			}
			ring = nil
			continue
		}
		a, n := worldNum(d, i)
		b, n := worldNum(d, n)
		i = n
		if cmd == 'M' {
			x, y = a, b
		} else {
			x, y = x+a, y+b
		}
		// x is lon+180 and y is 90-lat, one unit to the degree.
		ring = append(ring, [2]float64{x - 180, 90 - y})
	}
	return rings
}

// worldNum reads one number from the path, skipping any separator before it.
//
// ONE DOT PER NUMBER, and it is the whole reason this is written out rather than
// split on spaces: the generator drops a separator wherever the next number
// starts with a sign or a point, so "1.2.3" on the way in is 1.2 and then .3.
// Swallowing both dots gives a number that will not parse, a coastline that
// jumps, and — where the ring is small enough — a shape with no inside for the
// globe to fill.
func worldNum(d string, i int) (float64, int) {
	for i < len(d) && (d[i] == ' ' || d[i] == ',') {
		i++
	}
	j := i
	if j < len(d) && (d[j] == '-' || d[j] == '+') {
		j++
	}
	dot := false
	for j < len(d) {
		switch {
		case d[j] >= '0' && d[j] <= '9':
		case d[j] == '.' && !dot:
			dot = true
		default:
			return parseNum(d[i:j]), j
		}
		j++
	}
	return parseNum(d[i:j]), j
}

func parseNum(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// globeAt draws the coastlines as they stand with lon0 in the middle of the
// disc: the near side as it is, the horizon as a cut, and the limb between one
// cut and the next.
func globeAt(rings [][][2]float64, lon0 float64) string {
	cp, sp := math.Cos(globeTilt*math.Pi/180), math.Sin(globeTilt*math.Pi/180)
	c0, s0 := math.Cos(lon0*math.Pi/180), math.Sin(lon0*math.Pi/180)
	var b strings.Builder
	var x, y, z []float64
	for _, ring := range rings {
		x, y, z = x[:0], y[:0], z[:0]
		near, far := false, false
		for _, p := range ring {
			lat, lon := p[1]*math.Pi/180, p[0]*math.Pi/180
			a := math.Cos(lat) * math.Sin(lon)
			c := math.Cos(lat) * math.Cos(lon)
			u := c*c0 + a*s0
			x = append(x, a*c0-c*s0)
			y = append(y, cp*math.Sin(lat)-sp*u)
			zi := sp*math.Sin(lat) + cp*u
			z = append(z, zi)
			if zi >= 0 {
				near = true
			} else {
				far = true
			}
		}
		switch {
		case !near:
			continue
		case !far:
			b.WriteString("M" + point(x[0], y[0]))
			for i := 1; i < len(x); i++ {
				b.WriteString("L" + point(x[i], y[i]))
			}
			b.WriteString("Z")
		default:
			b.WriteString(clipped(x, y, z, !windsLeft(ring)))
		}
	}
	return b.String()
}

// clipped walks a ring that crosses the horizon, and rejoins what is left.
//
// The walk is the easy half: start where the ring first comes INTO view, and it
// falls into runs of visible coastline with a stretch of far side between each.
// Each run starts and ends on the limb.
//
// REJOINING THEM IS THE HALF THAT MATTERS. A run is not closed by the run that
// follows it round the ring — it is closed by whichever run STARTS FIRST along
// the limb, going the way the winding says. Joined in the order they were
// walked, the arcs sail straight past other crossings and each one takes another
// lap of the disc with it: Eurasia came out wound twice round the world, which a
// nonzero fill paints as a solid green plate. Nearest-first also splits the
// result into more than one piece when that is the truth, which it is whenever a
// landmass shows up on both sides of the world at once.
func clipped(x, y, z []float64, sweep bool) string {
	n := len(z)
	start := -1
	for i := 0; i < n; i++ {
		if z[i] >= 0 && z[(i-1+n)%n] < 0 {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	type run struct {
		inX, inY   float64
		pts        []string
		outX, outY float64
	}
	var runs []run
	for k := 0; k < n; k++ {
		i := (start + k) % n
		p := (i - 1 + n) % n
		switch {
		case z[i] >= 0 && z[p] < 0:
			hx, hy := horizon(x, y, z, p, i)
			runs = append(runs, run{inX: hx, inY: hy, pts: []string{point(x[i], y[i])}})
		case z[i] >= 0:
			runs[len(runs)-1].pts = append(runs[len(runs)-1].pts, point(x[i], y[i]))
		case z[p] >= 0:
			runs[len(runs)-1].outX, runs[len(runs)-1].outY = horizon(x, y, z, p, i)
		}
	}

	// Which run each one hands over to: the first entry along the limb from where
	// this one left.
	next := make([]int, len(runs))
	for i := range runs {
		best, from := -1, math.Atan2(-runs[i].outY, runs[i].outX)
		gap := math.Inf(1)
		for j := range runs {
			d := math.Atan2(-runs[j].inY, runs[j].inX) - from
			if !sweep {
				d = -d
			}
			for d <= 1e-9 {
				d += 2 * math.Pi
			}
			if d < gap {
				best, gap = j, d
			}
		}
		next[i] = best
	}

	var b strings.Builder
	drawn := make([]bool, len(runs))
	for s := range runs {
		if drawn[s] {
			continue
		}
		b.WriteString("M" + point(runs[s].inX, runs[s].inY))
		for i := s; ; {
			drawn[i] = true
			for _, p := range runs[i].pts {
				b.WriteString("L" + p)
			}
			b.WriteString("L" + point(runs[i].outX, runs[i].outY))
			j := next[i]
			b.WriteString(limb(runs[i].outX, runs[i].outY, runs[j].inX, runs[j].inY, sweep))
			if j == s || drawn[j] {
				break
			}
			i = j
		}
		b.WriteString("Z")
	}
	return b.String()
}

// horizon is where the edge from a to b crosses it: the depth is linear along
// the edge, so the crossing is one division — and the point is put back on the
// rim, which is where a point at zero depth belongs.
func horizon(x, y, z []float64, a, b int) (float64, float64) {
	t := z[a] / (z[a] - z[b])
	hx, hy := x[a]+t*(x[b]-x[a]), y[a]+t*(y[b]-y[a])
	if r := math.Hypot(hx, hy); r > 0 {
		hx, hy = hx/r, hy/r
	}
	return hx, hy
}

// windsLeft is true when the ring is wound with its inside on the left, seen
// from outside the sphere — a coastline. A lake or an inland sea is wound the
// other way, and takes the other way round the limb with it.
func windsLeft(ring [][2]float64) bool {
	var a float64
	for i := range ring {
		j := (i + 1) % len(ring)
		a += ring[i][0]*ring[j][1] - ring[j][0]*ring[i][1]
	}
	return a > 0
}

// limb draws the arc between two points on it. The long way round is the long
// way round: a sweep is only ever half a circle unless it is told otherwise.
func limb(fromX, fromY, toX, toY float64, sweep bool) string {
	// Screen angles, so y counts downward, like the flag.
	a := math.Atan2(-fromY, fromX)
	c := math.Atan2(-toY, toX)
	d := c - a
	for sweep && d < 0 {
		d += 2 * math.Pi
	}
	for !sweep && d > 0 {
		d -= 2 * math.Pi
	}
	large, dir := "0", "0"
	if math.Abs(d) > math.Pi {
		large = "1"
	}
	if sweep {
		dir = "1"
	}
	return "A" + globeNum(globeR) + " " + globeNum(globeR) + " 0 " + large + " " + dir +
		" " + point(toX, toY)
}

// point puts a projected point on the drawing.
func point(x, y float64) string {
	return globeNum(globeR+globeR*x) + " " + globeNum(globeR-globeR*y)
}

// globeNum matches the browser's toFixed(1), so the still the server sends and
// the first frame the script draws are the same string.
func globeNum(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// globeScript turns it.
//
// The projection cannot live in CSS — a transform moves a drawing, and this
// redraws one — so the globe carries the one script in the picture. It is small
// enough to read in a sitting: parse the coastlines once, precompute the three
// terms of each point that do not depend on the angle, and rewrite the path.
//
// It holds still for anybody who asked for less motion, and stops entirely when
// the picture is not on screen — the Access plate sits behind a tab, and a globe
// turning in a hidden panel is a phone getting warm for nothing.
//
// A page with no script keeps the still the server drew, which is the same
// projection at one angle: the picture says what it says either way.
var globeScript = `<script>(function(){
var path=document.querySelector('.acc-land');
if(!path||!path.getAttribute('data-world')||!window.requestAnimationFrame)return;
var TILT=` + globeNum(globeTilt) + `*Math.PI/180,FACE=` + globeNum(globeFace) + `,TURN=` + globeNum(globeTurn*1000) + `;
var CP=Math.cos(TILT),SP=Math.sin(TILT);
// THE CLIENTS ARE PLACES: three cities, turning with the world and going round
// the back with it. What that costs is here — the globe's box and where the
// reaches start, from the same constants the still uses, and the three terms of
// each city that do not turn. Every length is in the picture's own em, so
// nothing below has to know how big the plate was drawn.
var GX=` + sceneNum(sceneGlobeX) + `,GY=` + sceneNum(sceneGlobeY) + `,GR=` + sceneNum(sceneGlobeR) +
	`,RX=` + sceneNum(sceneReachX) + `,RY=` + sceneNum(sceneReachY) + `;
var acc=path.closest?path.closest('.acc'):null;
var beams=acc?acc.querySelectorAll('.acc-beam'):[];
var on=!!acc&&acc.getAttribute('data-on')==='1',settle=0;
var CITY=` + globeCityJS() + `;
for(var ci=0;ci<CITY.length;ci++){
  var cla=CITY[ci][0]*Math.PI/180,clo=CITY[ci][1]*Math.PI/180,ccos=Math.cos(cla);
  CITY[ci]=[ccos*Math.sin(clo),ccos*Math.cos(clo),Math.sin(cla)];
}
// The coastlines, once: lon and lat become the three terms of the projection
// that do not turn, so a frame is six multiplications a point.
// The winding rides along with the points: it is what says which way an arc of
// the limb goes, and it is a shoelace over lon and lat, taken once.
var rings=[],ring=null,px=0,py=0,fx=0,fy=0,turn=0,x=0,y=0,d=path.getAttribute('data-world');
d.replace(/([MlZ])([^MlZ]*)/g,function(_,cmd,args){
  if(cmd==='Z'){
    // The closing edge counts, and the path does not write it: the ring's last
    // point is not its first, so a shoelace that stopped there would be a sum
    // over an open line and could come out either sign.
    if(ring&&ring.p.length>=9){ring.cw=turn+px*fy-fx*py<=0;rings.push(ring);}
    ring=null;return '';
  }
  var n=args.match(/-?(?:\d+\.?\d*|\.\d+)/g)||[];
  if(cmd==='M'){x=+n[0];y=+n[1];ring={p:[],cw:false};turn=0;fx=x-180;fy=90-y;}else{x+=+n[0];y+=+n[1];}
  if(!ring)return '';
  var lat=90-y,lon=x-180;
  if(ring.p.length)turn+=px*lat-lon*py;
  px=lon;py=lat;
  lat*=Math.PI/180;lon*=Math.PI/180;
  var cos=Math.cos(lat);
  ring.p.push(cos*Math.sin(lon),cos*Math.cos(lon),Math.sin(lat));
  return '';
});
function face(lon0){
  var c0=Math.cos(lon0*Math.PI/180),s0=Math.sin(lon0*Math.PI/180),out='';
  for(var i=0;i<rings.length;i++){
    var r=rings[i],n=r.p.length/3,X=[],Y=[],Z=[],near=false,far=false;
    for(var j=0;j<r.p.length;j+=3){
      var a=r.p[j],c=r.p[j+1],s=r.p[j+2],u=c*c0+a*s0,z=SP*s+CP*u;
      X.push(a*c0-c*s0);Y.push(CP*s-SP*u);Z.push(z);
      if(z>=0)near=true;else far=true;
    }
    if(!near)continue;
    if(!far){
      out+='M'+pt(X[0],Y[0]);
      for(var k=1;k<n;k++)out+='L'+pt(X[k],Y[k]);
      out+='Z';continue;
    }
    out+=clip(X,Y,Z,n,r.cw);
  }
  return out;
}
// The same walk and the same rejoin as globe.go: into view, along the coast,
// over the edge — then each run closed by whichever one starts first along the
// limb, going the way the winding says.
function clip(X,Y,Z,n,sweep){
  var start=-1,i,p,h;
  for(i=0;i<n;i++)if(Z[i]>=0&&Z[(i-1+n)%n]<0){start=i;break;}
  if(start<0)return '';
  var runs=[],r;
  for(var k=0;k<n;k++){
    i=(start+k)%n;p=(i-1+n)%n;
    if(Z[i]>=0){
      if(Z[p]<0){h=horizon(X,Y,Z,p,i);runs.push({ix:h[0],iy:h[1],pts:[],ox:0,oy:0});}
      runs[runs.length-1].pts.push(pt(X[i],Y[i]));
    }else if(Z[p]>=0){
      h=horizon(X,Y,Z,p,i);r=runs[runs.length-1];r.ox=h[0];r.oy=h[1];
    }
  }
  var next=[],T=2*Math.PI;
  for(i=0;i<runs.length;i++){
    var from=Math.atan2(-runs[i].oy,runs[i].ox),gap=Infinity,best=-1;
    for(var j=0;j<runs.length;j++){
      var d=Math.atan2(-runs[j].iy,runs[j].ix)-from;
      if(!sweep)d=-d;
      while(d<=1e-9)d+=T;
      if(d<gap){gap=d;best=j;}
    }
    next[i]=best;
  }
  var out='',drawn=[];
  for(var s=0;s<runs.length;s++){
    if(drawn[s])continue;
    out+='M'+pt(runs[s].ix,runs[s].iy);
    for(i=s;;){
      drawn[i]=true;
      out+='L'+runs[i].pts.join('L')+'L'+pt(runs[i].ox,runs[i].oy);
      var m=next[i];
      out+=limb(runs[i].ox,runs[i].oy,runs[m].ix,runs[m].iy,sweep);
      if(m===s||drawn[m])break;
      i=m;
    }
    out+='Z';
  }
  return out;
}
function horizon(X,Y,Z,a,b){
  var t=Z[a]/(Z[a]-Z[b]),hx=X[a]+t*(X[b]-X[a]),hy=Y[a]+t*(Y[b]-Y[a]);
  var r=Math.sqrt(hx*hx+hy*hy);
  return r>0?[hx/r,hy/r]:[hx,hy];
}
function limb(fx,fy,tx,ty,sweep){
  var d=Math.atan2(-ty,tx)-Math.atan2(-fy,fx),T=2*Math.PI;
  while(sweep&&d<0)d+=T;
  while(!sweep&&d>0)d-=T;
  return 'A90 90 0 '+(Math.abs(d)>Math.PI?1:0)+' '+(sweep?1:0)+' '+pt(tx,ty);
}
function pt(x,y){return (90+90*x).toFixed(1)+' '+(90-90*y).toFixed(1);}
// A reach ends where its city has turned to. It is not drawn at all while that
// city is round the back: the projection puts a far point back inside the disc,
// mirrored, and a client dot on the wrong ocean is worse than no client dot.
function place(lon0){
  var c0=Math.cos(lon0*Math.PI/180),s0=Math.sin(lon0*Math.PI/180);
  for(var i=0;i<beams.length&&i<CITY.length;i++){
    var p=CITY[i],u=p[1]*c0+p[0]*s0,
      dx=GX+GR*(p[0]*c0-p[1]*s0)-RX,dy=GY-GR*(CP*p[2]-SP*u)-RY,st=beams[i].style;
    st.setProperty('--g-len',Math.sqrt(dx*dx+dy*dy).toFixed(2)+'em');
    st.setProperty('--g-ang',(Math.atan2(dy,dx)*180/Math.PI).toFixed(1)+'deg');
    st.setProperty('--g-vis',SP*p[2]+CP*u>=0?'1':'0');
  }
}
// Redrawn on the frame, but only once the angle has actually moved: at a
// revolution every twenty-six seconds a quarter of a degree is about forty
// times a second, and the eye cannot spend more than that.
var still=window.matchMedia?matchMedia('(prefers-reduced-motion:reduce)'):{matches:false};
var raf=0,start=0,drawn=FACE,onScreen=true;
function frame(now){
  raf=requestAnimationFrame(frame);
  if(!start)start=now;
  var lon0=FACE-360*((now-start)%TURN)/TURN;
  if(Math.abs(lon0-drawn)<.25)return;
  drawn=lon0;path.setAttribute('d',face(lon0));
  if(on)place(lon0);
}
function run(){if(!raf&&onScreen&&!still.matches)raf=requestAnimationFrame(frame);}
function stop(){if(raf)cancelAnimationFrame(raf);raf=0;start=0;}
if(still.addEventListener)still.addEventListener('change',function(){still.matches?stop():run();});
// The reaches EASE between the two positions of the switch, which is what makes
// the clients travel out to the world rather than jump there — and it is wrong
// forty times a second, where an ease leaves every client trailing its own city.
// So the ease comes off once the scene has settled, and goes back on the moment
// the switch moves again.
if(acc&&window.MutationObserver){
  if(on)acc.classList.add('acc-tracking');
  new MutationObserver(function(){
    var now=acc.getAttribute('data-on')==='1';
    if(now===on)return;
    on=now;clearTimeout(settle);acc.classList.remove('acc-tracking');
    if(!on)return;
    place(drawn);
    settle=setTimeout(function(){acc.classList.add('acc-tracking');},400);
  }).observe(acc,{attributes:true,attributeFilter:['data-on']});
}
if(window.IntersectionObserver){
  new IntersectionObserver(function(e){onScreen=e[0].isIntersecting;onScreen?run():stop();}).observe(path);
}else run();
})();</script>`
