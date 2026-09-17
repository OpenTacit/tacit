// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The OpenTacit "insights app": a self-contained, graphical view of the registry's
// headline outcomes, built for embedding as an MCP UI resource (the
// tacit_insights tool) so supporting harnesses render it visually rather than
// as text. Everything is inlined — CSS and SVG — with no external fetches, so it
// renders inside a sandboxed iframe under a strict CSP. A plain-text summary
// (InsightsAppText) rides alongside as the fallback for text-only clients.
package web

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/ui"
)

// handleInsightsApp serves the insights app for one window as JSON: the rendered
// graphical HTML, the text fallback, and a compact structured summary. The
// tacit_insights MCP tool relays these (docs — the ask path). Key-authed like the
// rest of /v1.
//
// The HTML carries every preset window with the requested one selected — see
// InsightsAppHTML for why the app can't be a single-window document.
func (s *Server) handleInsightsApp(w http.ResponseWriter, r *http.Request) {
	techniques, events, win, now, err := s.insightsInputs(r)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	var asked insights.Overview
	views := make([]insights.Overview, 0, 4)
	for _, wd := range insights.Windows(now, insights.Earliest(events)) {
		o := insights.Compute(techniques, events, now, wd)
		if wd.Key == win.Key {
			asked = o
		}
		views = append(views, o)
	}
	s.sendAppJSON(w, r, map[string]any{
		"window":  win.Key,
		"html":    InsightsAppHTML(views, win.Key),
		"text":    InsightsAppText(asked),
		"summary": insightsSummary(asked),
	})
}

// insightsSummary is the model-usable slice of the overview — the structured data
// the MCP tool returns alongside the app so the agent can reason over it even
// where the graphical view doesn't render.
func insightsSummary(o insights.Overview) map[string]any {
	m := map[string]any{
		"window":          o.Window.Key,
		"shown":           o.Funnel.Shown,
		"adopted":         o.Funnel.Adopted,
		"helped":          o.Funnel.Helped,
		"declined":        o.Funnel.Declined,
		"live_techniques": o.LiveTechniques,
		"active_cohorts":  o.ActiveCohorts,
	}
	if hr, ok := o.Funnel.HelpedRate(); ok {
		m["helped_rate"] = hr
	}
	if dr, ok := o.Funnel.DeclineRate(); ok {
		m["decline_rate"] = dr
	}
	return m
}

// insightsAppCSS is what the MCP insights app ships. It renders inside a
// sandboxed iframe whose CSP forbids fetching anything (see InsightsAppHTML
// below), so it cannot link /assets/app.css — it INLINES that stylesheet
// verbatim and appends only what is unique to living in someone else's frame:
// the window switcher, the full-screen button, the host-sized scroll container.
var insightsAppCSS = appCSS + `
/* ---- MCP host chrome (this document only) ---- */
.app{max-width:900px;margin:0 auto;padding:18px}
.app-head{display:flex;align-items:baseline;justify-content:space-between;flex-wrap:wrap;gap:.4rem}
.app-head h1{font-size:17px;margin:0;letter-spacing:.01em;
  display:inline-flex;align-items:center;gap:.4rem}
.app-head h1 .mark{color:var(--accent);width:19px;height:19px;flex:none}
.app-actions{display:flex;align-items:baseline;gap:.75rem}
.app .sub{color:var(--muted);font-size:12px;margin:.1rem 0 0}
/* Inside an MCP Apps host the frame is a box the host sizes: scroll within it
   rather than overflowing invisibly. Hosts that never complete the handshake
   keep the plain document flow (the script sizes the frame instead). */
:root[data-app="1"]{height:100%;overflow-y:auto}
:root[data-app="1"] body{min-height:100%}
:root[data-mode="fullscreen"] .app{max-width:1200px;padding:24px 28px}
.mode-btn{display:none;border:1px solid var(--ring);border-radius:999px;background:var(--surface);
  color:var(--ink-2);font:inherit;font-size:12px;padding:.2rem .65rem;cursor:pointer;white-space:nowrap}
.mode-btn:hover{color:var(--ink);border-color:var(--accent)}
:root[data-can-expand="1"] .mode-btn{display:inline-block}
.win-switch{display:inline-flex;border:1px solid var(--ring);border-radius:999px;overflow:hidden}
.win{border:0;border-right:1px solid var(--ring);background:var(--surface);color:var(--muted);
  font:inherit;font-size:12px;padding:.2rem .6rem;cursor:pointer}
.win:last-child{border-right:0}
.win:hover{color:var(--ink)}
.win[aria-pressed="true"]{background:var(--accent);color:var(--on-fill)}
/* This document has no <main>, so its grid and empty state are spelled out. */
.app .grid{grid-template-columns:1fr 1fr}
.app .empty{color:var(--muted);font-size:12px;font-style:normal}
@media (max-width:620px){.app .grid{grid-template-columns:1fr}}
`

// InsightsAppHTML renders the self-contained graphical dashboard document — KPI
// tiles (with sparklines), the funnel, and the two leaderboards. Links are
// deliberately omitted: the app renders in a sandboxed iframe where navigation
// would go nowhere.
//
// Every preset window is rendered into the document and one is shown, because a
// single-window document cannot honour the member's window. An MCP Apps host
// renders the template it fetched with resources/read — which has no arguments —
// and only afterwards tells the view what was asked for, over
// ui/notifications/tool-input. The frame can't fetch anything (the default CSP
// forbids it), so the data for every window has to already be there for the
// bridge to select. Falling out of that: the member can flip windows in place.
func InsightsAppHTML(views []insights.Overview, selected string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>` + productHTML() + ` insights</title><style>`)
	b.WriteString(insightsAppCSS)
	b.WriteString(`</style></head><body><div class="app">`)

	// Head: the window switcher, and a mode button that stays hidden until the
	// host says it can go full screen.
	b.WriteString(`<div class="app-head">` +
		`<div><h1>` + markSVG + ` ` + productHTML() + `</h1>` +
		`<p class="sub">Technique outcomes from recorded events</p></div>` +
		`<div class="app-actions"><div class="win-switch" role="group" aria-label="Time window">`)
	for _, o := range views {
		fmt.Fprintf(&b, `<button type="button" class="win" data-window="%s" aria-pressed="%t" title="%s">%s</button>`,
			html.EscapeString(o.Window.Key), o.Window.Key == selected,
			html.EscapeString(o.Window.Label), html.EscapeString(windowChip(o.Window)))
	}
	b.WriteString(`</div><button type="button" id="mode-btn" class="mode-btn">Full screen</button></div></div>`)

	for _, o := range views {
		b.WriteString(insightsAppView(o, o.Window.Key == selected))
	}

	b.WriteString(`</div>`)
	b.WriteString(insightsAppBridge)
	b.WriteString(`</body></html>`)
	return b.String()
}

// windowChip is the switcher's short label for a window ("Last 7 days" -> "7d").
func windowChip(w insights.Window) string {
	if w.Key == "all" {
		return "All"
	}
	return w.Key
}

// insightsAppView is one window's worth of dashboard. Only the selected one is
// shown; the bridge swaps them when the host says which window was asked for.
func insightsAppView(o insights.Overview, selected bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<section class="view" data-window="%s"%s>`,
		html.EscapeString(o.Window.Key), hiddenIf(!selected))

	// KPI tiles — the same headline numbers and sparklines as the overview.
	rateStr, measured := "&mdash;", false
	if hr, ok := o.Funnel.HelpedRate(); ok {
		rateStr, measured = fmt.Sprintf("%.0f%%", hr*100), true
	}
	declineStr, declineMeasured := "&mdash;", false
	if dr, ok := o.Funnel.DeclineRate(); ok {
		declineStr, declineMeasured = fmt.Sprintf("%.0f%%", dr*100), true
	}
	b.WriteString(`<div class="tiles">`)
	b.WriteString(string(StatTile("Adoptions", fmtCount(o.Funnel.Adopted), "", true, tileSpark(o.AdoptionSpark))))
	b.WriteString(string(StatTile("Helped rate", rateStr, nIf(measured, fmt.Sprintf("n=%d", o.Funnel.Adopted)), true, tileSpark(o.HelpedRateSpark))))
	b.WriteString(string(StatTile("Suggestions shown", fmtCount(o.Funnel.Shown), "", true, tileSpark(o.ShownSpark))))
	b.WriteString(string(StatTile("Filtered before showing", declineStr, nIf(declineMeasured, fmt.Sprintf("n=%d", o.Funnel.DeclineSample())), true, tileSpark(o.DeclineRateSpark))))
	b.WriteString(`</div>`)

	// Funnel + leaderboards.
	fmt.Fprintf(&b, `<section class="panel" style="margin-top:14px"><h2>Technique outcomes in %s &middot; %s</h2>`+
		"",
		productHTML(), html.EscapeString(o.Window.Label))
	b.WriteString(string(FunnelChart(o.Funnel)))
	b.WriteString(`</section>`)

	b.WriteString(`<div class="grid">`)
	b.WriteString(`<section class="panel"><h2>Highest helped rate</h2>` +
		ui.Sub("", "of adoptions"))
	b.WriteString(string(HBars(bestBars(o.Best), "No helped outcomes in this window")))
	b.WriteString(`</section><section class="panel"><h2>Most adoptions</h2>` +
		ui.Sub("", "this window \u00b7 \u0394 from the previous"))
	b.WriteString(string(HBars(fastestBars(o.Fastest), "No adoptions in this window")))
	b.WriteString(`</section></div></section>`)
	return b.String()
}

func hiddenIf(hide bool) string {
	if hide {
		return " hidden"
	}
	return ""
}

// insightsAppBridge is the view half of the MCP Apps protocol (ext-apps
// 2026-01-26): JSON-RPC 2.0 over postMessage to the host that framed us.
//
// It exists because the frame is not sized by its content — the host owns the
// box, and only learns what the app needs if the app says so. The handshake is
// ui/initialize (where the view declares that it can also run full screen),
// then ui/notifications/size-changed carrying the rendered height, which the
// host is required to apply to flexible frames. Without it the panel sits at
// the host's default height with the rest of the dashboard clipped, which is
// exactly what a static document gets today.
//
// Alongside that it takes the host's theme (a sandboxed frame can't see the
// host's colour scheme), scrolls inside the frame once the host owns the box,
// and offers "Full screen" only where the host advertises the mode. Hosts that
// never answer the handshake still get the pre-spec mcp-ui size message and an
// explicit document height, which is what Claude's iframe measures
// (anthropics/claude-ai-mcp#69) — so nothing regresses for them.
//
// Inline script is fine here: the frame is sandboxed allow-scripts and the MCP
// Apps default CSP permits script-src 'unsafe-inline'. Nothing external loads.
const insightsAppBridge = `<script>
(function(){
  var root = document.documentElement, host = window.parent;
  if (host === window) return; // opened directly, not framed: nothing to talk to
  var pending = {}, nextId = 1;
  var ctx = {};    // last HostContext the host gave us
  var app = false; // true once the host has answered ui/initialize

  function post(m){ try { host.postMessage(m, '*'); } catch (e) {} }
  function notify(method, params){ post({jsonrpc:'2.0', method:method, params:params||{}}); }
  function request(method, params){
    var id = nextId++;
    return new Promise(function(resolve, reject){
      pending[id] = {resolve:resolve, reject:reject};
      post({jsonrpc:'2.0', id:id, method:method, params:params||{}});
    });
  }

  var lastW = 0, lastH = 0;
  function reportSize(){
    var body = document.body;
    if (!body) return;
    var h = Math.ceil(body.scrollHeight), w = Math.ceil(body.scrollWidth);
    if (!h || (h === lastH && w === lastW)) return;
    lastH = h; lastW = w;
    notify('ui/notifications/size-changed', {width:w, height:h});
    if (!app) { // pre-spec hosts: the mcp-ui message, and the height Claude measures
      post({type:'ui-size-change', payload:{width:w, height:h}});
      try { root.style.height = h + 'px'; } catch (e) {}
    }
  }

  // The host renders the template — which carries no arguments — and only then
  // says which window the member asked for. Every window is in the document, so
  // honouring the ask is a matter of showing the right one.
  function selectWindow(key){
    if (!key) return;
    var views = document.querySelectorAll('.view'), target = null;
    for (var i = 0; i < views.length; i++) {
      if (views[i].getAttribute('data-window') === key) target = views[i];
    }
    if (!target) return; // unknown window: leave the one we have up
    for (var i = 0; i < views.length; i++) views[i].hidden = views[i] !== target;
    var btns = document.querySelectorAll('.win');
    for (var j = 0; j < btns.length; j++) {
      btns[j].setAttribute('aria-pressed', btns[j].getAttribute('data-window') === key ? 'true' : 'false');
    }
    lastW = lastH = 0; // another window is another height
    reportSize();
  }

  function applyContext(next){
    if (!next) return;
    for (var k in next) { if (Object.prototype.hasOwnProperty.call(next, k)) ctx[k] = next[k]; }
    if (ctx.theme === 'dark' || ctx.theme === 'light') root.setAttribute('data-theme', ctx.theme);
    root.setAttribute('data-mode', ctx.displayMode || 'inline');
    var modes = ctx.availableDisplayModes || [];
    root.setAttribute('data-can-expand', modes.indexOf('fullscreen') >= 0 ? '1' : '0');
    var btn = document.getElementById('mode-btn');
    if (btn) btn.textContent = ctx.displayMode === 'fullscreen' ? 'Exit full screen' : 'Full screen';
    lastW = lastH = 0; // the box changed: re-report against it
    reportSize();
  }

  function toggleMode(){
    var want = ctx.displayMode === 'fullscreen' ? 'inline' : 'fullscreen';
    if ((ctx.availableDisplayModes || []).indexOf(want) < 0) return; // host doesn't offer it
    request('ui/request-display-mode', {mode:want}).then(function(res){
      applyContext({displayMode: (res && res.mode) || ctx.displayMode}); // host has the last word
    }, function(){});
  }

  window.addEventListener('message', function(ev){
    var m = ev.data;
    if (!m || m.jsonrpc !== '2.0') return;
    if (m.id != null && pending[m.id]) {
      var p = pending[m.id]; delete pending[m.id];
      if (m.error) p.reject(m.error); else p.resolve(m.result);
    } else if (m.method === 'ui/notifications/host-context-changed') {
      applyContext(m.params); // partial: theme flip, mode change, host resize
    } else if (m.method === 'ui/notifications/tool-input' || m.method === 'ui/notifications/tool-input-partial') {
      selectWindow(m.params && m.params.arguments && m.params.arguments.window); // the window that was asked for
    } else if (m.method === 'ui/notifications/tool-result') {
      selectWindow(m.params && m.params.structuredContent && m.params.structuredContent.window);
    } else if (m.id != null && (m.method === 'ping' || m.method === 'ui/resource-teardown')) {
      post({jsonrpc:'2.0', id:m.id, result:{}}); // nothing to tear down: we hold no state
    }
  });

  function start(){
    var btn = document.getElementById('mode-btn');
    if (btn) btn.addEventListener('click', toggleMode);
    var wins = document.querySelectorAll('.win');
    for (var i = 0; i < wins.length; i++) {
      wins[i].addEventListener('click', function(){ selectWindow(this.getAttribute('data-window')); });
    }
    if (window.ResizeObserver) { try { new ResizeObserver(reportSize).observe(document.body); } catch (e) {} }
    window.addEventListener('load', reportSize);
    reportSize();
    request('ui/initialize', {
      protocolVersion: '2026-01-26',
      clientInfo: {name:'tacit-insights', version:'1'},
      appCapabilities: {availableDisplayModes:['inline', 'fullscreen']}
    }).then(function(res){
      app = true;
      root.style.height = ''; // the host sizes the frame now; we scroll inside it
      root.setAttribute('data-app', '1');
      applyContext(res && res.hostContext);
      notify('ui/notifications/initialized', {});
      reportSize();
    }, function(){});
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();
</script>`

// bestBars / fastestBars mirror the overview leaderboards, minus the drill-down
// hrefs (dead in a sandboxed frame).
func bestBars(techniques []insights.TechniqueStat) []BarRow {
	out := make([]BarRow, 0, len(techniques))
	for _, c := range techniques {
		rate, _ := c.Funnel.HelpedRate()
		out = append(out, BarRow{Label: c.Technique.Name, Value: int(rate * 100), Max: 100, Key: "s4",
			Sub: fmt.Sprintf("n=%d", c.Funnel.Adopted)})
	}
	return out
}

func fastestBars(techniques []insights.TechniqueStat) []BarRow {
	maxVel := 1
	for _, c := range techniques {
		if c.Funnel.Adopted > maxVel {
			maxVel = c.Funnel.Adopted
		}
	}
	out := make([]BarRow, 0, len(techniques))
	for _, c := range techniques {
		out = append(out, BarRow{Label: c.Technique.Name, Value: c.Funnel.Adopted, Max: maxVel, Key: "s2",
			Sub: fmt.Sprintf("%+d", c.Velocity())})
	}
	return out
}

// InsightsAppText is the plain-text fallback shown by clients that can't render the
// app — the same numbers, one screen of terminal text.
func InsightsAppText(o insights.Overview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "◆ %s · %s (technique outcomes from recorded events)\n\n", product.Name(), o.Window.Label)

	adoptRate := "—"
	if o.Funnel.Shown > 0 {
		adoptRate = fmt.Sprintf("%.0f%%", float64(o.Funnel.Adopted)/float64(o.Funnel.Shown)*100)
	}
	fmt.Fprintf(&b, "Funnel: %d shown → %d adopted (%s) → %d helped\n",
		o.Funnel.Shown, o.Funnel.Adopted, adoptRate, o.Funnel.Helped)

	helped := "no adopted suggestions"
	if hr, ok := o.Funnel.HelpedRate(); ok {
		helped = fmt.Sprintf("%.0f%% (n=%d)", hr*100, o.Funnel.Adopted)
	}
	decline := "no filter verdicts"
	if dr, ok := o.Funnel.DeclineRate(); ok {
		decline = fmt.Sprintf("%.0f%% (n=%d)", dr*100, o.Funnel.DeclineSample())
	}
	fmt.Fprintf(&b, "Helped rate: %s   ·   Filtered before showing: %s\n", helped, decline)
	fmt.Fprintf(&b, "Live techniques: %d   ·   Active cohorts: %d\n", o.LiveTechniques, o.ActiveCohorts)

	if len(o.Best) > 0 {
		b.WriteString("\nHighest helped rate:\n")
		for i, c := range o.Best {
			rate, _ := c.Funnel.HelpedRate()
			fmt.Fprintf(&b, " %d. %s: helped %.0f%% (n=%d)\n", i+1, c.Technique.Name, rate*100, c.Funnel.Adopted)
		}
	}
	if len(o.Fastest) > 0 {
		b.WriteString("\nMost adoptions this window:\n")
		for i, c := range o.Fastest {
			fmt.Fprintf(&b, " %d. %s: %d adopted (%+d from the previous window)\n", i+1, c.Technique.Name, c.Funnel.Adopted, c.Velocity())
		}
	}
	return b.String()
}
