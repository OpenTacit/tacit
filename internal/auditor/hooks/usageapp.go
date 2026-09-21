// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The OpenTacit "usage app": a self-contained, graphical view of THIS member's own
// OpenTacit usage over time — queries, suggestions shown/adopted/helped, per-technique
// drill-down — built for embedding as an MCP UI resource (the tacit_usage
// tool). Everything is inlined (CSS + SVG), with no external fetches, so it
// renders inside a sandboxed iframe under a strict CSP.
//
// It lives HERE, next to the usage log, on purpose. Unlike the insights/map/
// drafts apps — whose HTML comes from the registry over HTTP because their data
// is server-side — usage data is member-local and identity-free (usagelog.go),
// so the only place it can be materialised is the member's own machine. The MCP
// server runs there too, so it renders this app directly from the log: no
// registry round-trip, no loopback fetch, no cross-origin. A plain-text summary
// (usageAppText) and a structured object (usageSummaryMap) ride alongside as the
// fallbacks for hosts that don't render UI resources.
package hooks

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/product"

	"github.com/opentacit/tacit/internal/windows"
)

// usageAppWindows are the preset windows the app embeds (all four in one
// document, like the insights app, so the in-frame switcher and a host's
// window argument both resolve without a re-fetch). It reads the keys and
// lengths from internal/windows, so this list cannot drift from the one the
// MCP schema advertises and ParseUsageWindow understands.
var usageAppWindows = func() []struct {
	key string
	dur time.Duration
} {
	out := make([]struct {
		key string
		dur time.Duration
	}, len(windows.Keys))
	for i, k := range windows.Keys {
		out[i].key, out[i].dur = k, windows.Duration(k)
	}
	return out
}()

// RenderUsageApp is the tacit_usage tool's local render: it reads the member's
// usage log and returns the graphical app (all windows, the asked-for one
// selected), a text fallback, and a structured summary for the model. window is
// a preset key ("7d"/"30d"/"90d"/"all"); anything else falls back to 30d.
func RenderUsageApp(path, window string) (htmlDoc, text string, summary map[string]any, err error) {
	return renderUsageAppAt(path, window, nil)
}

// renderUsageAppAt is RenderUsageApp with an injectable clock, so a test can pin
// "now" to the time its fixtures were written.
func renderUsageAppAt(path, window string, now func() time.Time) (htmlDoc, text string, summary map[string]any, err error) {
	selKey := windowLabel(ParseUsageWindow(window))
	durs := make([]time.Duration, len(usageAppWindows))
	for i, w := range usageAppWindows {
		durs[i] = w.dur
	}
	sums := readUsageSummariesAt(path, durs, now)

	var sel UsageSummary
	for _, s := range sums {
		if s.Window == selKey {
			sel = s
			break
		}
	}
	return usageAppHTML(sums, selKey), usageAppText(sel), usageSummaryMap(sel), nil
}

// UsageAppHTML renders the live log this agent already holds as the same
// self-contained document RenderUsageApp builds from a file. It exists because
// the agent is the one process that always has the log open: the MCP path reads
// a path because it may run with no agent at all, and re-reading from disk here
// would answer a question the member asked about the session they are in with
// whatever was last flushed.
//
// The window token is the dashboard's (`w=30d`); the in-frame switcher still
// swaps between all four without a round trip, because every window is in the
// document.
func (a *Agent) UsageAppHTML(window string) string {
	sums := make([]UsageSummary, 0, len(usageAppWindows))
	for _, w := range usageAppWindows {
		sums = append(sums, a.usage.summarize(w.dur))
	}
	return usageAppHTML(sums, windowLabel(ParseUsageWindow(window)))
}

func usageSummaryMap(s UsageSummary) map[string]any {
	techniques := make([]map[string]any, 0, len(s.Techniques))
	for _, c := range s.Techniques {
		techniques = append(techniques, map[string]any{
			"cap": c.TechniqueID, "name": c.Name, "shown": c.Shown,
			"adopted": c.Adopted, "helped": c.Helped, "dismissed": c.Dismissed,
		})
	}
	return map[string]any{
		"window": s.Window,
		"totals": map[string]any{
			"queries": s.Totals.Queries, "shown": s.Totals.Shown,
			"adopted": s.Totals.Adopted, "helped": s.Totals.Helped,
			"dismissed": s.Totals.Dismissed, "offered": s.Totals.Offered,
		},
		"techniques": techniques,
	}
}

func pctStr(n, d int) string {
	if d <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d%%", (n*100+d/2)/d)
}

// usageAppText is the plain-text fallback: the same headline numbers, one
// screen of terminal text, for hosts that can't render the app.
func usageAppText(s UsageSummary) string {
	var b strings.Builder
	label := s.Window
	if label == "all" {
		label = "all time"
	}
	fmt.Fprintf(&b, "Your %s usage (%s), read from this machine — personal and local, never collected by the registry.\n\n", product.Name(), label)
	t := s.Totals
	fmt.Fprintf(&b, "  Queries:    %d\n", t.Queries)
	fmt.Fprintf(&b, "  Shown:      %d\n", t.Shown)
	fmt.Fprintf(&b, "  Adopted:    %d  (%s of shown)\n", t.Adopted, pctStr(t.Adopted, t.Shown))
	fmt.Fprintf(&b, "  Helped:     %d  (%s of adopted)\n", t.Helped, pctStr(t.Helped, t.Adopted))
	fmt.Fprintf(&b, "  Dismissed:  %d\n", t.Dismissed)
	fmt.Fprintf(&b, "  Questions:  %d\n", t.Offered)
	if len(s.Techniques) > 0 {
		b.WriteString("\nBy technique (shown · adopted · helped · dismissed):\n")
		for i, c := range s.Techniques {
			if i >= 10 {
				fmt.Fprintf(&b, "  …and %d more\n", len(s.Techniques)-10)
				break
			}
			name := c.Name
			if name == "" {
				name = c.TechniqueID
			}
			fmt.Fprintf(&b, "  %-40s  %d · %d · %d · %d\n", name, c.Shown, c.Adopted, c.Helped, c.Dismissed)
		}
	}
	if t.Queries == 0 && t.Shown == 0 {
		b.WriteString("\nNothing recorded on this machine in this window yet — as you work with your agent, your usage builds up here.\n")
	}
	return b.String()
}

// usageAppHTML builds the whole self-contained document: every window's view is
// present, the selected one visible; the bridge swaps them for the in-frame
// switcher or a host's window argument.
func usageAppHTML(sums []UsageSummary, selected string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>Your Tacit usage</title><style>`)
	b.WriteString(usageAppCSS)
	b.WriteString(`</style></head><body><div class="app">`)

	b.WriteString(`<div class="app-head"><div><h1>Your Tacit usage</h1>` +
		`<p class="sub">read from this machine — personal and local, never collected by the registry</p></div>` +
		`<div class="win-switch" role="group" aria-label="Time window">`)
	for _, w := range usageAppWindows {
		label := w.key
		if w.key == "all" {
			label = "All"
		}
		fmt.Fprintf(&b, `<button type="button" class="win" data-window="%s" aria-pressed="%t">%s</button>`,
			w.key, w.key == selected, html.EscapeString(label))
	}
	b.WriteString(`</div></div>`)

	for _, s := range sums {
		b.WriteString(usageAppView(s, s.Window == selected))
	}

	b.WriteString(`</div>`)
	b.WriteString(usageAppBridge)
	b.WriteString(`</body></html>`)
	return b.String()
}

func hiddenAttr(hide bool) string {
	if hide {
		return " hidden"
	}
	return ""
}

// usageAppView is one window's worth of the app: tiles, the over-time chart,
// and the by-technique table.
func usageAppView(s UsageSummary, selected bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<section class="view" data-window="%s"%s>`, s.Window, hiddenAttr(!selected))
	t := s.Totals

	b.WriteString(`<div class="tiles">`)
	b.WriteString(usageTile("Queries", fmt.Sprintf("%d", t.Queries), ""))
	b.WriteString(usageTile("Shown", fmt.Sprintf("%d", t.Shown), ""))
	b.WriteString(usageTile("Adopted", fmt.Sprintf("%d", t.Adopted), pctStr(t.Adopted, t.Shown)+" of shown"))
	b.WriteString(usageTile("Helped", fmt.Sprintf("%d", t.Helped), pctStr(t.Helped, t.Adopted)+" of adopted"))
	b.WriteString(usageTile("Questions", fmt.Sprintf("%d", t.Offered), ""))
	b.WriteString(`</div>`)

	if t.Queries == 0 && t.Shown == 0 && t.Offered == 0 {
		b.WriteString(`<p class="empty">No Tacit activity recorded on this machine in this window yet. As you work with your agent, your usage builds up here.</p></section>`)
		return b.String()
	}

	b.WriteString(`<section class="panel"><h2>Over time</h2>`)
	b.WriteString(`<div class="legend"><span><i style="background:var(--s1)"></i>Shown</span><span><i style="background:var(--s2)"></i>Adopted</span></div>`)
	b.WriteString(usageBarsSVG(s.Series))
	b.WriteString(`</section>`)

	if len(s.Techniques) > 0 {
		b.WriteString(`<section class="panel"><h2>By technique</h2>` +
			`<table><thead><tr><th>Technique</th><th class="num">Shown</th><th class="num">Adopted</th><th class="num">Adopt&nbsp;%</th><th class="num">Helped</th><th class="num">Dismissed</th></tr></thead><tbody>`)
		for _, c := range s.Techniques {
			name := c.Name
			if name == "" {
				name = c.TechniqueID
			}
			fmt.Fprintf(&b, `<tr><td>%s</td><td class="num">%d</td><td class="num">%d</td><td class="num">%s</td><td class="num">%d</td><td class="num">%d</td></tr>`,
				html.EscapeString(name), c.Shown, c.Adopted, pctStr(c.Adopted, c.Shown), c.Helped, c.Dismissed)
		}
		b.WriteString(`</tbody></table></section>`)
	}

	b.WriteString(`</section>`)
	return b.String()
}

func usageTile(label, value, delta string) string {
	d := ""
	if delta != "" {
		d = `<span class="tile-delta">` + html.EscapeString(delta) + `</span>`
	}
	return `<div class="tile"><div class="tile-label">` + html.EscapeString(label) +
		`</div><div class="tile-row"><span class="tile-value">` + html.EscapeString(value) + `</span>` + d + `</div></div>`
}

// usageBarsSVG draws paired daily bars (shown, adopted) over the window — the
// same shape as the web view, rendered server-side here.
func usageBarsSVG(series []UsageDay) string {
	if len(series) == 0 {
		return `<p class="empty">No suggestions in this window.</p>`
	}
	const W, H = 760.0, 200.0
	const padL, padR, padT, padB = 8.0, 8.0, 12.0, 26.0
	iw, ih := W-padL-padR, H-padT-padB
	peak := 1
	for _, d := range series {
		if d.Shown > peak {
			peak = d.Shown
		}
		if d.Adopted > peak {
			peak = d.Adopted
		}
	}
	n := float64(len(series))
	bw := iw / n
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %.0f %.0f" role="img" aria-label="Daily suggestions shown and adopted">`, W, H)
	base := padT + ih
	fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="var(--axis)" stroke-width="1"></line>`,
		padL, base, W-padR, base)
	bar := func(x, off, wf float64, v int, color string) {
		if v <= 0 {
			return
		}
		h := ih * float64(v) / float64(peak)
		w := bw*wf - 0.5
		if w < 1 {
			w = 1
		}
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="1" fill="%s"></rect>`,
			x+off, base-h, w, h, color)
	}
	for i, d := range series {
		x := padL + float64(i)*bw
		bar(x, bw*0.10, 0.4, d.Shown, "var(--s1)")
		bar(x, bw*0.50, 0.4, d.Adopted, "var(--s2)")
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" fill="var(--muted)" font-size="11">%s</text>`,
		padL, H-8, html.EscapeString(series[0].Date))
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" fill="var(--muted)" font-size="11" text-anchor="end">%s</text>`,
		W-padR, H-8, html.EscapeString(series[len(series)-1].Date))
	b.WriteString(`</svg>`)
	return b.String()
}

const usageAppCSS = `
*{box-sizing:border-box}
:root{--plane:#f9f9f7;--surface:#fcfcfb;--ink:#0b0b0b;--ink2:#52514e;--muted:#898781;--grid:#e1e0d9;--axis:#c3c2b7;--ring:rgba(11,11,11,.10);--s1:#2a78d6;--s2:#1baf7a}
@media (prefers-color-scheme:dark){:root{--plane:#191917;--surface:#201f1d;--ink:#f2f1ee;--ink2:#b9b7b1;--muted:#8f8d87;--grid:#2c2c2a;--axis:#383835;--ring:rgba(255,255,255,.10);--s1:#3987e5;--s2:#199e70}}
:root[data-theme="dark"]{--plane:#191917;--surface:#201f1d;--ink:#f2f1ee;--ink2:#b9b7b1;--muted:#8f8d87;--grid:#2c2c2a;--axis:#383835;--ring:rgba(255,255,255,.10);--s1:#3987e5;--s2:#199e70}
:root[data-theme="light"]{--plane:#f9f9f7;--surface:#fcfcfb;--ink:#0b0b0b;--ink2:#52514e;--muted:#898781;--grid:#e1e0d9;--axis:#c3c2b7;--ring:rgba(11,11,11,.10);--s1:#2a78d6;--s2:#1baf7a}
html,body{margin:0}
body{background:var(--plane);color:var(--ink);font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;-webkit-font-smoothing:antialiased}
.app{padding:16px;max-width:900px;margin:0 auto}
.app-head{display:flex;justify-content:space-between;align-items:flex-start;gap:12px;flex-wrap:wrap;margin-bottom:14px}
h1{font-size:20px;margin:0}
.sub{color:var(--ink2);margin:.2rem 0 0;font-size:13px}
.win-switch{display:inline-flex;gap:4px;background:var(--surface);border:1px solid var(--ring);border-radius:8px;padding:3px}
.win{border:0;background:transparent;color:var(--ink2);font:inherit;font-size:13px;padding:.25rem .6rem;border-radius:6px;cursor:pointer}
.win[aria-pressed="true"]{background:var(--plane);color:var(--ink);font-weight:600;box-shadow:0 1px 2px var(--ring)}
.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:10px}
.tile{background:var(--surface);border:1px solid var(--ring);border-radius:10px;padding:.7rem .85rem}
.tile-label{color:var(--ink2);font-size:12px}
.tile-row{display:flex;align-items:baseline;gap:.5rem;margin-top:.15rem}
.tile-value{font-size:26px;font-weight:700;font-variant-numeric:tabular-nums}
.tile-delta{color:var(--s2);font-size:12px;font-weight:600}
.panel{background:var(--surface);border:1px solid var(--ring);border-radius:12px;padding:14px;margin-top:14px}
.panel h2{font-size:15px;margin:0 0 .1rem}
.legend{display:flex;gap:1rem;margin:.25rem 0 .6rem;color:var(--ink2);font-size:12px}
.legend span{display:inline-flex;align-items:center;gap:.4rem}
.legend i{width:.8rem;height:.8rem;border-radius:2px;display:inline-block}
.chart{width:100%;height:auto;display:block}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{text-align:left;padding:.45rem .3rem;border-bottom:1px solid var(--ring)}
th{color:var(--muted);font-weight:600}
td.num,th.num{text-align:right;font-variant-numeric:tabular-nums;white-space:nowrap}
tbody tr:last-child td{border-bottom:0}
.empty{color:var(--ink2);padding:1rem 0}
`

// usageAppBridge does two jobs. The window switcher is wired UNCONDITIONALLY, so
// it works when the doc is rendered standalone (the `tacit usage --html` output
// opened as a file/artifact in Claude Code's viewer) as well as framed. The MCP
// Apps host bridge (ext-apps 2026-01-26 — report height, apply theme, honour a
// host-supplied window) only engages when actually framed by an mcp-ui host.
const usageAppBridge = `<script>
(function(){
  var root=document.documentElement, host=window.parent, framed=(host!==window);
  var app=false, lastH=0, lastW=0;
  function post(m){try{host.postMessage(m,'*');}catch(e){}}
  function notify(method,params){post({jsonrpc:'2.0',method:method,params:params||{}});}
  function reportSize(){
    if(!framed)return; // standalone: the viewer sizes us
    var body=document.body; if(!body)return;
    var h=Math.ceil(body.scrollHeight), w=Math.ceil(body.scrollWidth);
    if(!h||(h===lastH&&w===lastW))return;
    lastH=h; lastW=w;
    notify('ui/notifications/size-changed',{width:w,height:h});
    if(!app){post({type:'ui-size-change',payload:{width:w,height:h}});try{root.style.height=h+'px';}catch(e){}}
  }
  function selectWindow(key){
    if(!key)return;
    var views=document.querySelectorAll('.view'), target=null;
    for(var i=0;i<views.length;i++){if(views[i].getAttribute('data-window')===key)target=views[i];}
    if(!target)return;
    for(var i=0;i<views.length;i++)views[i].hidden=views[i]!==target;
    var btns=document.querySelectorAll('.win');
    for(var j=0;j<btns.length;j++)btns[j].setAttribute('aria-pressed',btns[j].getAttribute('data-window')===key?'true':'false');
    lastH=lastW=0; reportSize();
  }
  function applyContext(next){
    if(!next)return;
    if(next.theme==='dark'||next.theme==='light')root.setAttribute('data-theme',next.theme);
    lastH=lastW=0; reportSize();
  }
  function wire(){ // the switcher — standalone and framed alike
    var wins=document.querySelectorAll('.win');
    for(var i=0;i<wins.length;i++)wins[i].addEventListener('click',function(){selectWindow(this.getAttribute('data-window'));});
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',wire);else wire();
  if(!framed)return; // opened as a file/artifact: switcher is enough, no host bridge
  window.addEventListener('message',function(ev){
    var m=ev.data;
    if(!m||m.jsonrpc!=='2.0')return;
    if(m.method==='ui/notifications/host-context-changed')applyContext(m.params);
    else if(m.method==='ui/notifications/tool-input'||m.method==='ui/notifications/tool-input-partial')selectWindow(m.params&&m.params.arguments&&m.params.arguments.window);
    else if(m.method==='ui/notifications/tool-result')selectWindow(m.params&&m.params.structuredContent&&m.params.structuredContent.window);
    else if(m.id!=null&&(m.method==='ping'||m.method==='ui/resource-teardown'))post({jsonrpc:'2.0',id:m.id,result:{}});
    else if(m.jsonrpc==='2.0'&&m.id===1&&!m.method){app=true;try{root.style.height='';}catch(e){}root.setAttribute('data-app','1');if(m.result&&m.result.hostContext)applyContext(m.result.hostContext);notify('ui/notifications/initialized',{});reportSize();}
  });
  function start(){
    if(window.ResizeObserver){try{new ResizeObserver(reportSize).observe(document.body);}catch(e){}}
    window.addEventListener('load',reportSize);
    reportSize();
    post({jsonrpc:'2.0',id:1,method:'ui/initialize',params:{protocolVersion:'2026-01-26',clientInfo:{name:'tacit-usage',version:'1'},appCapabilities:{}}});
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',start);
  else start();
})();
</script>`
