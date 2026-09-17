// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/ui"
)

// The overview's request-rate chart: how much this ingress carried, over time,
// as one line or one line per instance.
//
// It replaced a "Recent operations" panel that repeated the Operations page's
// newest rows. Two views of the same twelve records answered the same question
// twice and neither answered "is this normal for a Tuesday" — which is the
// question an overview is for.
//
// The chart is rendered SERVER-side, by the same ui.LineChart the registry's
// dashboard draws with, and re-rendered on every zoom, pan or series change. The
// browser's job is the interaction and one fetch; the geometry, the bucketing
// and the labels stay in Go where they are testable and where there is one
// implementation of the house chart. What comes back is a fragment, not JSON, so
// there is no second chart renderer in JavaScript to drift from the first.

// ratePoints is roughly how many buckets a chart should carry. Enough that a
// day has shape, few enough that the SVG stays small and each hover column is
// wide enough to hit.
const ratePoints = 90

// rateLadder is the set of bucket widths a chart may use, coarsest last. The
// narrowest one that keeps the bucket count at or under ratePoints wins, so the
// x axis reads in round units — minutes, hours, days — at every zoom level
// rather than in whatever fraction the window divides into.
var rateLadder = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 7 * 24 * time.Hour,
}

// Zoom bounds. The floor is a window narrow enough to see a single burst; the
// ceiling is the retention window, past which there is nothing to show.
const (
	rateMinSpan = 15 * time.Minute
	rateMaxSpan = 90 * 24 * time.Hour
)

// rateBucketFor picks the bucket width for a window.
func rateBucketFor(span time.Duration) time.Duration {
	for _, b := range rateLadder {
		if span/b <= ratePoints {
			return b
		}
	}
	return rateLadder[len(rateLadder)-1]
}

// rateLabel names a bucket on the x axis and in the tooltip — the no-JS
// fallback text, in UTC. The live page relabels it in the READER's zone
// (ui.LocalTimeScript); this used to render in the server's zone, which is the
// one zone nobody reading the console is guaranteed to be in.
func rateLabel(t time.Time, bucket time.Duration) string {
	return ui.LTFallback(t, rateLabelKind(bucket))
}

// rateLabelKind is how much of the instant a bucket of this size needs shown.
// The unit follows the bucket: a minute-wide bucket needs its clock time, a
// day-wide one does not, and reading "14:05" under a chart of six weeks would
// be false precision.
func rateLabelKind(bucket time.Duration) string {
	switch {
	case bucket >= 24*time.Hour:
		return ui.LTDate
	case bucket >= time.Hour:
		return ui.LTDateHM
	default:
		return ui.LTHM
	}
}

// rateView is what a chart request resolves to.
type rateView struct {
	from, to   time.Time
	byInstance bool
}

// parseRateView reads the window and series choice from a query, clamping both
// to what the log can answer: no future (there is nothing there yet), no window
// narrower than the floor or wider than retention.
func (s *Server) parseRateView(q url.Values) rateView {
	now := time.Now()
	v := rateView{from: now.Add(-24 * time.Hour), to: now, byInstance: q.Get("by") == "instance"}
	if ms, err := strconv.ParseInt(q.Get("to"), 10, 64); err == nil && ms > 0 {
		v.to = time.UnixMilli(ms)
	}
	if ms, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil && ms > 0 {
		v.from = time.UnixMilli(ms)
	}
	if v.to.After(now) {
		v.from, v.to = v.from.Add(now.Sub(v.to)), now
	}
	switch span := v.to.Sub(v.from); {
	case span < rateMinSpan:
		v.from = v.to.Add(-rateMinSpan)
	case span > rateMaxSpan:
		v.from = v.to.Add(-rateMaxSpan)
	}
	return v
}

// rateSlots assigns each instance the CSS series class it draws in.
//
// The slot follows the ENTITY, not its rank in the window: an instance's
// position in a stable alphabetical list, so zooming or panning — which changes
// which instances have traffic, and how much — never repaints the ones that
// stayed. Ranking would swap two lines' colours the moment one overtook the
// other, which is the fastest way to make a chart lie to the person reading it.
//
// The enrolled instances come first, so a registry that is still published keeps
// its colour as others come and go; names that appear only in the log take the
// remaining slots. A released name still has history worth plotting, and the log
// outlives the route table by the log's retention window (OpLogRetentionDays).
//
// The palette has eight hues and a ninth is never invented: anything past the
// eighth folds into one muted "other" line, and the legend says how many.
func (s *Server) rateSlots(present []string) map[string]string {
	seen, order := map[string]bool{}, []string{}
	enrolled := make([]string, 0, 16)
	for _, in := range s.Store.List() {
		enrolled = append(enrolled, in.Name)
	}
	sort.Strings(enrolled)
	extra := append([]string{}, present...)
	sort.Strings(extra)
	for _, n := range append(enrolled, extra...) {
		if n != "" && !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	slots := map[string]string{}
	for i, n := range order {
		if i >= len(ui.LineSeriesKeys) {
			break
		}
		slots[n] = ui.LineSeriesKeys[i]
	}
	return slots
}

// rateChartHTML renders the chart itself plus the line that says what is on
// screen. Both change together on every interaction, so both live in the
// fragment the browser swaps in.
func (s *Server) rateChartHTML(v rateView) string {
	bucket := rateBucketFor(v.to.Sub(v.from))
	res := s.Ops.Rate(v.from, v.to, bucket, v.byInstance)

	// Bucket starts are real instants, so the axis carries them and the reader's
	// browser dates them in the reader's own zone (ui.LocalTimeScript). The
	// server-rendered text below is the no-JS fallback.
	axis := ui.Axis{
		Labels: make([]string, len(res.Buckets)),
		ISO:    make([]string, len(res.Buckets)),
		Kind:   rateLabelKind(bucket),
	}
	for i, bk := range res.Buckets {
		axis.Labels[i] = rateLabel(bk.Start, bucket)
		axis.ISO[i] = bk.Start.UTC().Format(time.RFC3339)
	}

	var series []ui.Series
	var folded int
	if !v.byInstance {
		vals := make([]int, len(res.Buckets))
		for i, bk := range res.Buckets {
			vals[i] = bk.Counts[""]
		}
		series = append(series, ui.Series{Name: "requests", Key: "s1", Values: vals})
	} else {
		slots := s.rateSlots(res.Names)
		other := make([]int, len(res.Buckets))
		for _, name := range res.Names {
			vals := make([]int, len(res.Buckets))
			for i, bk := range res.Buckets {
				vals[i] = bk.Counts[name]
			}
			key, ok := slots[name]
			if !ok {
				folded++
				for i, n := range vals {
					other[i] += n
				}
				continue
			}
			series = append(series, ui.Series{Name: name, Key: key, Values: vals})
		}
		if folded > 0 {
			series = append(series, ui.Series{
				Name: fmt.Sprintf("%d other", folded), Key: "other", Values: other})
		}
	}
	// An empty window still draws its plot. A flat line on the baseline is the
	// truth — nothing was served then — and, more practically, the plot is the
	// surface you drag and scroll on: replacing it with a sentence would strand
	// a reader who panned somewhere quiet with nothing left to pan back with.
	// The words above the chart say the same thing for anyone not reading the
	// line, so the empty case is never mute.
	if len(series) == 0 {
		series = append(series, ui.Series{Name: "requests", Key: "s1", Values: make([]int, len(res.Buckets))})
	}

	var b strings.Builder
	// rateHint is markup, not text: the window's ends are <time> elements. Every
	// other part of the sentence is generated here, so there is nothing in it to
	// escape.
	fmt.Fprintf(&b, `<p class="hint">%s</p>`, rateHint(v, bucket, res))
	b.WriteString(string(ui.LineChart(axis, series, 240)))
	return b.String()
}

// rateHint is the sentence under the heading: what window, in what unit, adding
// to what — and, when the read hit its ceiling, that the total counts the tail
// of the log rather than all of it.
func rateHint(v rateView, bucket time.Duration, res RateSeries) string {
	span := fmt.Sprintf("%s — %s", ui.LocalTime(v.from, ui.LTDateHM), ui.LocalTime(v.to, ui.LTDateHM))
	if res.Total == 0 {
		return "no requests between " + span
	}
	hint := fmt.Sprintf("requests per %s · %s · %s in this window",
		bucketNoun(bucket), span, ui.FmtCount(res.Total))
	if res.Truncated {
		hint += " (within the newest " + formatBytes(maxScan) + " of the log)"
	}
	return hint
}

// bucketNoun names a bucket width in words, for the sentence above the chart.
func bucketNoun(d time.Duration) string {
	switch {
	case d >= 7*24*time.Hour:
		return "week"
	case d >= 24*time.Hour:
		return "day"
	case d >= time.Hour:
		if h := int(d / time.Hour); h > 1 {
			return strconv.Itoa(h) + " hours"
		}
		return "hour"
	default:
		if m := int(d / time.Minute); m > 1 {
			return strconv.Itoa(m) + " minutes"
		}
		return "minute"
	}
}

// ratePanel is the whole panel: the controls, then the chart the controls act
// on. The controls sit in one row above the plot (never beside it), and the
// container carries the window as data attributes so the script has the same
// state the server just rendered from.
func (s *Server) ratePanel(v rateView) string {
	press := func(on bool) string {
		if on {
			return ` aria-pressed="true"`
		}
		return ` aria-pressed="false"`
	}
	span := v.to.Sub(v.from)
	var b strings.Builder
	b.WriteString(`<section class="panel chart-panel"><h2>Requests over time</h2>`)

	b.WriteString(`<div class="chart-controls">`)
	b.WriteString(`<div class="btn-row" role="group" aria-label="Time range">`)
	for _, r := range []struct {
		label string
		d     time.Duration
	}{{"1h", time.Hour}, {"6h", 6 * time.Hour}, {"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour}, {"30d", 30 * 24 * time.Hour}} {
		// A preset reads as chosen when the window is within a few percent of
		// it — panning keeps the span, so "24h" should stay lit as it moves.
		on := span > r.d*95/100 && span < r.d*105/100
		fmt.Fprintf(&b, `<button class="btn rate-span" type="button" data-span="%d"%s>%s</button>`,
			int64(r.d/time.Millisecond), press(on), r.label)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<div class="btn-row" role="group" aria-label="Series">`)
	fmt.Fprintf(&b, `<button class="btn rate-by" type="button" data-by=""%s>Total</button>`, press(!v.byInstance))
	fmt.Fprintf(&b, `<button class="btn rate-by" type="button" data-by="instance"%s>By instance</button>`, press(v.byInstance))
	b.WriteString(`</div>`)
	b.WriteString(`<div class="btn-row" role="group" aria-label="Zoom">` +
		`<button class="btn rate-zoom" type="button" data-zoom="out" title="Zoom out" aria-label="Zoom out">−</button>` +
		`<button class="btn rate-zoom" type="button" data-zoom="in" title="Zoom in" aria-label="Zoom in">+</button>` +
		`<button class="btn rate-now" type="button" title="Jump to now">Now</button>` +
		`</div>`)
	b.WriteString(`</div>`)

	fmt.Fprintf(&b, `<div id="rate-chart" data-from="%d" data-to="%d" data-by="%s">`,
		v.from.UnixMilli(), v.to.UnixMilli(), map[bool]string{true: "instance"}[v.byInstance])
	b.WriteString(s.rateChartHTML(v))
	b.WriteString(`</div>`)
	b.WriteString(`<p class="hint">Drag to pan · scroll to zoom · ` +
		`<a href="/ops">every request, one by one, on Operations</a></p>`)
	b.WriteString(`</section>`)
	return b.String()
}

// handleRateChart serves one rendered chart for a window. It answers the
// browser's fetch on every zoom, pan and series change — the same function the
// page render calls, so what arrives by fetch and what arrives with the document
// can never be two different charts.
func (s *Server) handleRateChart(w http.ResponseWriter, r *http.Request) {
	v := s.parseRateView(r.URL.Query())
	cachepolicy.MarkPrivate(w, r, "operations chart")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(s.rateChartHTML(v)))
}

// RateChartScript drives the chart: it owns the window (from, to) and the series
// choice, and re-fetches a rendered chart whenever they change.
//
// Zoom is anchored at the pointer, so the moment under the cursor stays under
// the cursor — a chart that zoomed to the centre would walk the thing you were
// looking at off the screen. Panning is a pointer drag with capture, so it keeps
// tracking outside the plot. Neither can go past now, and both are clamped to
// the same span limits the server enforces, so the buttons and the URL cannot
// disagree.
//
// Fetches are debounced and serialized: a wheel gesture is dozens of events, and
// what matters is the window it lands on. The old chart stays on screen until
// the new one arrives rather than blanking on every step.
const RateChartScript = `<script>
(function(){
 var box=document.getElementById('rate-chart');
 if(!box)return;
 var MIN=15*60*1000, MAX=90*24*3600*1000;
 var timer=null, inflight=false, pending=false;
 function state(){return {from:+box.dataset.from, to:+box.dataset.to, by:box.dataset.by||''};}
 function clamp(s){
  var now=Date.now();
  if(s.to>now){s.from-=s.to-now;s.to=now;}
  var span=s.to-s.from;
  if(span<MIN)s.from=s.to-MIN;
  if(span>MAX)s.from=s.to-MAX;
  return s;
 }
 function put(s){box.dataset.from=Math.round(s.from);box.dataset.to=Math.round(s.to);box.dataset.by=s.by;}
 function draw(){
  if(inflight){pending=true;return;}
  inflight=true;
  var s=state();
  fetch('/overview/chart?from='+s.from+'&to='+s.to+'&by='+encodeURIComponent(s.by),
        {headers:{'Accept':'text/html'},credentials:'same-origin'})
   .then(function(r){return r.ok?r.text():Promise.reject(r.status);})
   .then(function(html){
     box.innerHTML=html;
     // The plot is new markup, so it needs the shared hover layer wired to it —
     // and its labels put into the reader's timezone, which the page-load pass
     // could not have reached.
     if(window.tacitLocalTime)window.tacitLocalTime(box);
     box.querySelectorAll('svg.viz').forEach(function(svg){
       if(window.tacitWireViz)window.tacitWireViz(svg);});
   })
   .catch(function(){/* keep the chart that is already on screen */})
   .then(function(){inflight=false;if(pending){pending=false;draw();}});
 }
 function apply(s){put(clamp(s));marks();clearTimeout(timer);timer=setTimeout(draw,120);}
 // The pressed states are the server's answer on load; after that the script
 // keeps them in step without a round trip, so a control never lags its effect.
 function marks(){
  var s=state(), span=s.to-s.from;
  box.parentNode.querySelectorAll('.rate-span').forEach(function(btn){
    var d=+btn.dataset.span;
    btn.setAttribute('aria-pressed', span>d*0.95&&span<d*1.05?'true':'false');});
  box.parentNode.querySelectorAll('.rate-by').forEach(function(btn){
    btn.setAttribute('aria-pressed',(btn.dataset.by||'')===s.by?'true':'false');});
 }
 box.parentNode.querySelectorAll('.rate-span').forEach(function(btn){
  btn.addEventListener('click',function(){var s=state();s.from=s.to-(+btn.dataset.span);apply(s);});});
 box.parentNode.querySelectorAll('.rate-by').forEach(function(btn){
  btn.addEventListener('click',function(){var s=state();s.by=btn.dataset.by||'';apply(s);});});
 box.parentNode.querySelectorAll('.rate-zoom').forEach(function(btn){
  btn.addEventListener('click',function(){
    var s=state(),mid=(s.from+s.to)/2,half=(s.to-s.from)/2*(btn.dataset.zoom==='in'?0.5:2);
    s.from=mid-half;s.to=mid+half;apply(s);});});
 var nowBtn=box.parentNode.querySelector('.rate-now');
 if(nowBtn)nowBtn.addEventListener('click',function(){
   var s=state(),span=s.to-s.from;s.to=Date.now();s.from=s.to-span;apply(s);});

 // Zoom at the pointer: the fraction of the plot under the cursor is the
 // fraction of the window that must not move.
 box.addEventListener('wheel',function(e){
  var plot=box.querySelector('.vchart-plot');
  if(!plot)return;
  e.preventDefault();
  var r=plot.getBoundingClientRect(), f=Math.min(1,Math.max(0,(e.clientX-r.left)/r.width));
  var s=state(), span=s.to-s.from, at=s.from+span*f;
  var k=e.deltaY>0?1.25:0.8;
  s.from=at-span*f*k; s.to=at+span*(1-f)*k;
  apply(s);
 },{passive:false});

 // Drag to pan. Pointer capture keeps the gesture alive outside the plot, and
 // .chart-dragging takes the hit columns out of the way so the tooltip does not
 // flicker along with the drag.
 var drag=null;
 box.addEventListener('pointerdown',function(e){
  var plot=box.querySelector('.vchart-plot');
  if(!plot||!plot.contains(e.target))return;
  var s=state();
  drag={x:e.clientX,w:plot.getBoundingClientRect().width,from:s.from,to:s.to,id:e.pointerId};
  box.classList.add('chart-dragging');
  box.setPointerCapture(e.pointerId);
 });
 box.addEventListener('pointermove',function(e){
  if(!drag)return;
  var s=state(), span=drag.to-drag.from, dx=(e.clientX-drag.x)/drag.w;
  s.from=drag.from-span*dx; s.to=drag.to-span*dx;
  apply(s);
 });
 function endDrag(e){
  if(!drag)return;
  box.classList.remove('chart-dragging');
  try{box.releasePointerCapture(drag.id);}catch(err){}
  drag=null;
 }
 box.addEventListener('pointerup',endDrag);
 box.addEventListener('pointercancel',endDrag);
})();
</script>`
