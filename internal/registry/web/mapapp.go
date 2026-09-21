// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The OpenTacit "map app": a self-contained, interactive technique knowledge map
// built for embedding as an MCP UI resource (the tacit_map tool), the graphical
// counterpart of the map page. Like the insights app it inlines everything — the
// stylesheet, the canvas renderer, the graph data — so it renders inside a
// sandboxed iframe under a strict CSP with no external fetches. A plain-text
// summary (MapAppText) rides alongside for text-only clients.
//
// The interactive parts are all client-side (hover, node detail, area select +
// zoom, arrange-by, legend), so they work in the frame. The page's server-round-
// trip affordances — the set-filter and the "Describe with AI" naming run — are
// omitted: a sandboxed frame can't reach the registry.
package web

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/techmap"
)

// handleMapApp serves the map app as JSON: the rendered interactive HTML, a text
// fallback, and a compact structured summary. The tacit_map MCP tool relays these.
// Key-authed like the rest of /v1. The map is the whole live library (unfiltered),
// so this endpoint takes no arguments.
func (s *Server) handleMapApp(w http.ResponseWriter, r *http.Request) {
	in, err := s.readAnalyticsInputs(false)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	techniques, events := in.techniques, in.events
	live := make([]models.Technique, 0, len(techniques))
	for _, c := range techniques {
		if c.Status != "draft" && c.Status != "retired" {
			live = append(live, c)
		}
	}
	g := techmap.Build(live, events)
	s.applyClusterLabels(&g)
	s.sendAppJSON(w, r, map[string]any{
		"html":    MapAppHTML(g),
		"text":    MapAppText(g),
		"summary": mapSummary(g),
	})
}

// mapSummary is the model-usable slice of the graph — the headline counts and the
// areas of practice, so the agent can reason over the map even where the graphical
// view doesn't render.
func mapSummary(g techmap.Graph) map[string]any {
	areas := make([]map[string]any, 0, len(g.Clusters))
	for _, c := range g.Clusters {
		a := map[string]any{"name": c.Name, "techniques": len(c.Members)}
		if c.Desc != "" {
			a["description"] = c.Desc
		}
		areas = append(areas, a)
	}
	return map[string]any{
		"live_techniques": g.Summary.Total,
		"clustered":       g.Summary.Connected,
		"isolated":        g.Summary.Isolated,
		"adopted":         g.Summary.Adopted,
		"helped":          g.Summary.Helped,
		"areas":           areas,
	}
}

// mapAppCSS is what the MCP map app ships: the shared registry stylesheet (which
// already carries every .cmap-* rule the map uses) plus only what is unique to
// living in someone else's frame — the header chrome, the full-screen button, and
// the host-sized scroll container.
var mapAppCSS = appCSS + `
/* ---- MCP host chrome (this document only) ---- */
.app{max-width:1200px;margin:0 auto;padding:16px}
.app-head{display:flex;align-items:baseline;justify-content:space-between;flex-wrap:wrap;gap:.4rem}
.app-head h1{font-size:17px;margin:0;letter-spacing:.01em;display:inline-flex;align-items:center;gap:.4rem}
.app-head h1 .mark{color:var(--accent);width:19px;height:19px;flex:none}
.app .sub{color:var(--muted);font-size:12px;margin:.1rem 0 0}
.app .empty{color:var(--muted);font-size:12px;font-style:normal}
:root[data-app="1"]{height:100%;overflow-y:auto}
:root[data-app="1"] body{min-height:100%}
:root[data-mode="fullscreen"] .app{max-width:100%;padding:20px 26px}
.mode-btn{display:none;border:1px solid var(--ring);border-radius:999px;background:var(--surface);
  color:var(--ink-2);font:inherit;font-size:12px;padding:.2rem .65rem;cursor:pointer;white-space:nowrap}
.mode-btn:hover{color:var(--ink);border-color:var(--accent)}
:root[data-can-expand="1"] .mode-btn{display:inline-block}
`

// MapAppHTML renders the self-contained interactive map document: the same canvas,
// legend/controls and Areas list the page uses, driven by the same client renderer
// (techniqueMapScript) over the embedded graph, plus the MCP Apps bridge. The set-filter
// and the describe-with-AI run are left out — a sandboxed frame can't reach back.
func MapAppHTML(g techmap.Graph) string {
	data, err := json.Marshal(g)
	if err != nil {
		data = []byte("{}")
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>` + productHTML() + ` playbook map</title><style>`)
	b.WriteString(mapAppCSS)
	b.WriteString(`</style></head><body><div class="app cmap-app">`)

	b.WriteString(`<div class="app-head"><div><h1>` + markSVG + ` ` + productHTML() + `</h1>` +
		`<p class="sub">playbook map</p></div>` +
		`<div class="app-actions"><button type="button" id="mode-btn" class="mode-btn">Full screen</button></div></div>`)

	sm := g.Summary
	fmt.Fprintf(&b, `<div class="sub cmap-sub">%d technique%s · <b>%d</b> clustered · <b>%d</b> isolated · <b>%d</b> adopted · <b>%d</b> measurably helped</div>`,
		sm.Total, plural(sm.Total), sm.Connected, sm.Isolated, sm.Adopted, sm.Helped)

	if len(g.Nodes) == 0 {
		b.WriteString(`<p class="empty">No live techniques to map yet.</p></div></body></html>`)
		return b.String()
	}

	// No filter panel in the sandboxed app, so the arrange-by control gets its own
	// small toolbar.
	b.WriteString(`<div class="cmap-apptools">` + arrangeSeg("tags") + `</div>`)

	cohortPanel := cmapCohortAreasPanel(g.CohortClusters, g.CohortDim)
	aside := ""
	if len(g.Clusters) > 0 || len(g.CohortClusters) > 0 {
		aside = `<div class="cmap-aside">` +
			`<div id="cmap-areas-tags">` + mapAreasAside(g.Clusters) + `</div>` +
			`<div id="cmap-areas-cohort" hidden>` + cohortPanel + `</div>` +
			`</div>`
	}
	b.WriteString(cmapStage(aside))
	b.WriteString(`<script id="cmap-data" type="application/json">` + string(data) + `</script>`)
	// Inlined, not linked: this document renders in a sandboxed MCP Apps frame
	// with no access to our origin, so the renderer has to travel with it.
	b.WriteString(`<script>` + techniqueMapJS + `</script>`)
	b.WriteString(`</div>`) // close .app
	b.WriteString(mapAppBridge)
	b.WriteString(`</body></html>`)
	return b.String()
}

// mapAreasAside is the Areas list for the app: the same clusters the page shows,
// clickable to zoom (techniqueMapScript wires the clicks), minus the "Describe with AI"
// form — that run posts to the registry, which the sandboxed frame can't do.
func mapAreasAside(clusters []techmap.Cluster) string {
	var b strings.Builder
	b.WriteString(`<section class="panel cmap-areas"><p class="cmap-lbl">Areas</p>` +

		`<ul class="cmap-area-list">`)
	for _, c := range clusters {
		fmt.Fprintf(&b, `<li data-cluster="%d"><span class="cmap-area-name">%s</span><span class="cmap-area-n">%d</span>`,
			c.ID, html.EscapeString(c.Name), len(c.Members))
		if c.Desc != "" {
			fmt.Fprintf(&b, `<p class="cmap-area-desc">%s</p>`, html.EscapeString(c.Desc))
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul></section>`)
	return b.String()
}

// MapAppText is the plain-text fallback shown by clients that can't render the app
// — the headline counts and the areas of practice, one screen of terminal text.
func MapAppText(g techmap.Graph) string {
	var b strings.Builder
	b.WriteString("◆ " + product.Name() + " · playbook map\n\n")
	sm := g.Summary
	fmt.Fprintf(&b, "%d live technique%s · %d clustered into areas · %d isolated · %d adopted · %d measurably helped\n",
		sm.Total, plural(sm.Total), sm.Connected, sm.Isolated, sm.Adopted, sm.Helped)
	if len(g.Clusters) > 0 {
		b.WriteString("\nAreas of practice (shared-tag clusters):\n")
		for _, c := range g.Clusters {
			line := fmt.Sprintf(" · %s: %d technique%s", c.Name, len(c.Members), plural(len(c.Members)))
			if c.Desc != "" {
				line += ": " + c.Desc
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// mapAppBridge is the view half of the MCP Apps protocol (ext-apps 2026-01-26):
// JSON-RPC 2.0 over postMessage to the host that framed us. It is the insights
// app's bridge without the window switcher (the map has no windows): it takes the
// host's theme, reports its rendered size so the host can size the frame, and
// offers "Full screen" where the host advertises it. Hosts that never answer the
// handshake still get the pre-spec mcp-ui size message and an explicit document
// height. Inline script is fine — the frame is sandboxed and the MCP Apps default
// CSP permits script-src 'unsafe-inline'; nothing external loads.
const mapAppBridge = `<script>
(function(){
  var root = document.documentElement, host = window.parent;
  if (host === window) return; // opened directly, not framed
  var pending = {}, nextId = 1, ctx = {}, app = false;
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
    var body = document.body; if (!body) return;
    var h = Math.ceil(body.scrollHeight), w = Math.ceil(body.scrollWidth);
    if (!h || (h === lastH && w === lastW)) return;
    lastH = h; lastW = w;
    notify('ui/notifications/size-changed', {width:w, height:h});
    if (!app) { post({type:'ui-size-change', payload:{width:w, height:h}}); try { root.style.height = h + 'px'; } catch (e) {} }
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
    lastW = lastH = 0;
    try { window.dispatchEvent(new Event('resize')); } catch (e) {} // let the canvas re-fit the new box
    reportSize();
  }
  function toggleMode(){
    var want = ctx.displayMode === 'fullscreen' ? 'inline' : 'fullscreen';
    if ((ctx.availableDisplayModes || []).indexOf(want) < 0) return;
    request('ui/request-display-mode', {mode:want}).then(function(res){
      applyContext({displayMode: (res && res.mode) || ctx.displayMode});
    }, function(){});
  }
  window.addEventListener('message', function(ev){
    var m = ev.data;
    if (!m || m.jsonrpc !== '2.0') return;
    if (m.id != null && pending[m.id]) {
      var p = pending[m.id]; delete pending[m.id];
      if (m.error) p.reject(m.error); else p.resolve(m.result);
    } else if (m.method === 'ui/notifications/host-context-changed') {
      applyContext(m.params);
    } else if (m.id != null && (m.method === 'ping' || m.method === 'ui/resource-teardown')) {
      post({jsonrpc:'2.0', id:m.id, result:{}});
    }
  });
  function start(){
    var btn = document.getElementById('mode-btn');
    if (btn) btn.addEventListener('click', toggleMode);
    if (window.ResizeObserver) { try { new ResizeObserver(reportSize).observe(document.body); } catch (e) {} }
    window.addEventListener('load', reportSize);
    reportSize();
    request('ui/initialize', {
      protocolVersion: '2026-01-26',
      clientInfo: {name:'tacit-map', version:'1'},
      appCapabilities: {availableDisplayModes:['inline', 'fullscreen']}
    }).then(function(res){
      app = true;
      root.style.height = '';
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
