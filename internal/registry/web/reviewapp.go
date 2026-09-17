// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The OpenTacit "review app": the drafts queue as a self-contained interactive
// document, built for embedding as an MCP UI resource (the tacit_drafts tool)
// so a member can review drafts — read the full technique, promote, reject —
// without leaving the harness for the dashboard. Like the insights and map
// apps, everything is inlined and nothing external loads; unlike them it ACTS:
// each decision is a tools/call back through the MCP Apps host to
// tacit_draft_action, which is the same registry mutation the dashboard's
// buttons perform. Hosts that can't proxy tool calls degrade to reading — the
// text fallback and the command's native-form tier carry the decisions
// instead (plugins/claude-code/commands/drafts.md).
package web

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/ui"
)

// handleReviewApp serves the drafts review app as JSON: the rendered
// interactive HTML, a text fallback for text-only clients, and a structured
// summary the model can reason over. Key-authed like the rest of /v1; the
// ACTIONS the app takes go through /v1/admin/promote, which stays root-key
// authed — a member key can read the queue but not decide it.
func (s *Server) handleReviewApp(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.Store.ListTechniques([]string{"draft"}, 200)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].CreatedAt > drafts[j].CreatedAt })
	s.sendAppJSON(w, r, map[string]any{
		"html":    ReviewAppHTML(drafts),
		"text":    ReviewAppText(drafts),
		"summary": reviewAppSummary(drafts),
	})
}

// reviewAppSummary is the model-usable slice of the queue: enough for the
// native-form tier to present each draft and act on the member's decision
// without re-fetching.
func reviewAppSummary(drafts []models.Technique) map[string]any {
	list := make([]map[string]any, 0, len(drafts))
	for _, c := range drafts {
		d := map[string]any{
			"id":         c.ID,
			"name":       c.Name,
			"provenance": provenanceOf(c),
			"scope":      scopeOrGeneral(c.Scope),
			"added":      dateOf(c.CreatedAt),
		}
		if c.Supersedes != "" {
			d["supersedes"] = c.Supersedes
			if c.RevisionNote != "" {
				d["revision_note"] = c.RevisionNote
			}
		}
		list = append(list, d)
	}
	return map[string]any{"count": len(drafts), "drafts": list}
}

// ReviewAppText is the text fallback: the queue, compact, with the fields a
// reviewer needs to decide from the conversation alone.
func ReviewAppText(drafts []models.Technique) string {
	if len(drafts) == 0 {
		return "The review queue is empty; no drafts are waiting."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d draft(s) awaiting review (newest first):\n", len(drafts))
	for i, c := range drafts {
		fmt.Fprintf(&b, "\n%d. %s  [%s · %s · %s · added %s]\n",
			i+1, c.Name, c.ID, provenanceOf(c), scopeOrGeneral(c.Scope), dateOf(c.CreatedAt))
		if c.Supersedes != "" {
			fmt.Fprintf(&b, "   revision of %s", c.Supersedes)
			if c.RevisionNote != "" {
				fmt.Fprintf(&b, "; note: %s", strings.TrimSpace(c.RevisionNote))
			}
			b.WriteString("\n")
		}
		if c.Description != "" {
			b.WriteString("   " + strings.TrimSpace(c.Description) + "\n")
		}
		if c.Recipe != "" {
			b.WriteString("   recipe: " + strings.TrimSpace(c.Recipe) + "\n")
		}
		if c.AppliesWhen != "" {
			b.WriteString("   when: " + strings.TrimSpace(c.AppliesWhen) + "\n")
		}
		if c.NotWhen != "" {
			b.WriteString("   not when: " + strings.TrimSpace(c.NotWhen) + "\n")
		}
		if len(c.Tags) > 0 {
			b.WriteString("   tags: " + strings.Join(c.Tags, ", ") + "\n")
		}
	}
	return b.String()
}

func provenanceOf(c models.Technique) string {
	if c.Provenance != "" {
		return c.Provenance
	}
	return "contributed"
}

func scopeOrGeneral(scope string) string {
	if scope == "" {
		return "general"
	}
	return scope
}

func dateOf(rfc3339 string) string {
	if len(rfc3339) >= 10 {
		return rfc3339[:10]
	}
	return rfc3339
}

// reviewAppCSS extends the shared stylesheet with what is unique to this
// document: the decision buttons, the decided state, and the host-sized
// scroll container (same MCP-host chrome as the insights app).
var reviewAppCSS = appCSS + `
/* ---- MCP host chrome (this document only) ---- */
.app{max-width:900px;margin:0 auto;padding:18px}
.app-head h1{font-size:17px;margin:0;letter-spacing:.01em;display:inline-flex;align-items:center;gap:.4rem}
.app-head h1 .mark{color:var(--accent);width:19px;height:19px;flex:none}
.app .sub{color:var(--muted);font-size:12px;margin:.1rem 0 0}
:root[data-app="1"]{height:100%;overflow-y:auto}
:root[data-app="1"] body{min-height:100%}
.draft{margin-top:14px}
.draft .meta-line{color:var(--muted);font-size:12px;margin:.15rem 0 .6rem}
.draft .meta-line code{font-size:11px}
.draft h2{margin:0}
.draft dl{display:grid;grid-template-columns:auto 1fr;gap:.25rem .8rem;margin:.6rem 0;font-size:13px}
.draft dt{color:var(--muted);font-weight:600}
.draft dd{margin:0}
.draft pre{white-space:pre-wrap;overflow-wrap:anywhere;background:color-mix(in srgb,var(--ink) 5%,transparent);
  border-radius:8px;padding:.6rem .75rem;font-size:12.5px;margin:.2rem 0}
.revision{border:1px solid var(--ring);border-left:3px solid var(--accent);border-radius:8px;
  padding:.5rem .75rem;font-size:12.5px;margin:.4rem 0}
.decide{display:flex;align-items:center;gap:.6rem;margin-top:.7rem}
.decide button{border:1px solid var(--ring);border-radius:999px;background:var(--surface);color:var(--ink);
  font:inherit;font-size:13px;padding:.3rem .9rem;cursor:pointer}
.decide button.promote{background:var(--accent);border-color:var(--accent);color:var(--on-fill)}
.decide button:hover{filter:brightness(1.08)}
.decide button:disabled{opacity:.45;cursor:default;filter:none}
.decide .status{font-size:12.5px;color:var(--muted)}
.decide .status.err{color:var(--bad)}
.draft.decided{opacity:.55}
.draft.decided .decide .status{color:var(--ink);font-weight:600}
.app .empty{color:var(--muted);font-size:13px;margin-top:1rem}
`

// ReviewAppHTML renders the drafts queue as the self-contained review
// document. Every draft carries its full technique — the reviewer decides from
// what members would actually be served, not a summary — and two decision
// buttons wired (by the bridge below) to tacit_draft_action through the MCP
// Apps host. The document renders read-only wherever that call path is
// missing, and says so the first time a button is pressed.
func ReviewAppHTML(drafts []models.Technique) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>` + productHTML() + ` review</title><style>`)
	b.WriteString(reviewAppCSS)
	b.WriteString(`</style></head><body><div class="app">`)
	fmt.Fprintf(&b, `<div class="app-head"><h1>%s %s review</h1>`+
		`<p class="sub">%d draft(s) to review · accept makes a technique available in retrieval; reject keeps its record but does not serve it</p></div>`,
		markSVG, productHTML(), len(drafts))

	if len(drafts) == 0 {
		b.WriteString(`<p class="empty">No drafts need review.</p>`)
	}
	for _, c := range drafts {
		b.WriteString(reviewAppDraft(c))
	}
	b.WriteString(`</div>`)
	b.WriteString(ui.LocalTimeScript)
	b.WriteString(reviewAppBridge)
	b.WriteString(`</body></html>`)
	return b.String()
}

// reviewAppDraft is one draft's panel: the technique in full, the revision context
// when it proposes a change to a serving technique, and the decision row.
func reviewAppDraft(c models.Technique) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<section class="panel draft" data-id="%s">`, html.EscapeString(c.ID))
	fmt.Fprintf(&b, `<h2>%s%s</h2>`, orgOnlyBadge(c.Scope), html.EscapeString(c.Name))
	fmt.Fprintf(&b, `<p class="meta-line"><code>%s</code> · %s · added %s</p>`,
		html.EscapeString(c.ID), html.EscapeString(provenanceOf(c)), ui.LocalTimeISO(c.CreatedAt, ui.LTYMD))
	if c.Supersedes != "" {
		fmt.Fprintf(&b, `<div class="revision"><b>Revision of %s</b>`, html.EscapeString(c.Supersedes))
		if c.RevisionNote != "" {
			fmt.Fprintf(&b, `: %s`, html.EscapeString(strings.TrimSpace(c.RevisionNote)))
		}
		b.WriteString(`. Accepting applies this change to the base technique. The base remains unchanged until then.</div>`)
	}
	if c.Description != "" {
		fmt.Fprintf(&b, `<p>%s</p>`, html.EscapeString(strings.TrimSpace(c.Description)))
	}
	b.WriteString(`<dl>`)
	if c.Recipe != "" {
		fmt.Fprintf(&b, `<dt>recipe</dt><dd><pre>%s</pre></dd>`, html.EscapeString(strings.TrimSpace(c.Recipe)))
	}
	if c.AppliesWhen != "" {
		fmt.Fprintf(&b, `<dt>applies when</dt><dd>%s</dd>`, html.EscapeString(strings.TrimSpace(c.AppliesWhen)))
	}
	if c.NotWhen != "" {
		fmt.Fprintf(&b, `<dt>not when</dt><dd>%s</dd>`, html.EscapeString(strings.TrimSpace(c.NotWhen)))
	}
	if len(c.Tags) > 0 {
		fmt.Fprintf(&b, `<dt>tags</dt><dd>%s</dd>`, html.EscapeString(strings.Join(c.Tags, ", ")))
	}
	b.WriteString(`</dl>`)
	b.WriteString(`<div class="decide">` +
		`<button type="button" class="promote" data-action="promote">Accept</button>` +
		`<button type="button" class="reject" data-action="reject">Reject</button>` +
		`<span class="status" role="status"></span></div>`)
	b.WriteString(`</section>`)
	return b.String()
}

// reviewAppBridge is the view half of the MCP Apps protocol for this app. The
// handshake, sizing, and theme handling mirror the insights app's bridge (see
// insightsAppBridge for the why of each move); what is new is the decision
// path: a button click becomes a tools/call request to the HOST, which proxies
// it to the tacit MCP server's tacit_draft_action tool. The host is free to
// ask the member to confirm — that is its prerogative — and a host that can't
// proxy tool calls rejects the request, which the app reports in place with
// the chat fallback ("ask in chat: promote <id>"). The app never talks to the
// registry directly: the frame's CSP forbids network, and the tool path is
// what keeps one auth story (the member's key, checked server-side).
const reviewAppBridge = `<script>
(function(){
  var root = document.documentElement, host = window.parent;
  var pending = {}, nextId = 1, app = false;
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
    if (!app) {
      post({type:'ui-size-change', payload:{width:w, height:h}});
      try { root.style.height = h + 'px'; } catch (e) {}
    }
  }

  function applyContext(ctx){
    if (!ctx) return;
    if (ctx.theme === 'dark' || ctx.theme === 'light') root.setAttribute('data-theme', ctx.theme);
    lastW = lastH = 0;
    reportSize();
  }

  function decide(section, action){
    var id = section.getAttribute('data-id');
    var btns = section.querySelectorAll('.decide button');
    var status = section.querySelector('.decide .status');
    for (var i = 0; i < btns.length; i++) btns[i].disabled = true;
    status.classList.remove('err');
    status.textContent = action === 'promote' ? 'Promoting…' : 'Rejecting…';
    request('tools/call', {name:'tacit_draft_action', arguments:{id:id, action:action}}).then(function(res){
      var failed = res && res.isError;
      if (failed) {
        var msg = '';
        try { msg = res.content[0].text; } catch (e) {}
        status.classList.add('err');
        status.textContent = msg || 'The action failed; ask in chat: ' + action + ' ' + id;
        for (var i = 0; i < btns.length; i++) btns[i].disabled = false;
        return;
      }
      section.classList.add('decided');
      status.textContent = action === 'promote' ? 'Promoted; now in service' : 'Rejected';
      lastW = lastH = 0;
      reportSize();
    }, function(){
      status.classList.add('err');
      status.textContent = 'This view can’t act here; ask in chat: ' + action + ' ' + id;
      for (var i = 0; i < btns.length; i++) btns[i].disabled = false;
    });
  }

  if (host !== window) window.addEventListener('message', function(ev){
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
    var sections = document.querySelectorAll('.draft');
    for (var i = 0; i < sections.length; i++) (function(section){
      var btns = section.querySelectorAll('.decide button');
      for (var j = 0; j < btns.length; j++) (function(btn){
        btn.addEventListener('click', function(){ decide(section, btn.getAttribute('data-action')); });
      })(btns[j]);
    })(sections[i]);
    if (host === window) return; // opened directly: read-only document, no host to talk to
    if (window.ResizeObserver) { try { new ResizeObserver(reportSize).observe(document.body); } catch (e) {} }
    window.addEventListener('load', reportSize);
    reportSize();
    request('ui/initialize', {
      protocolVersion: '2026-01-26',
      clientInfo: {name:'tacit-review', version:'1'},
      appCapabilities: {availableDisplayModes:['inline']}
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
