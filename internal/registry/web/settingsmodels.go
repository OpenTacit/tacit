// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The settings model combo box is populated from the provider's live /models
// endpoint. The fetch is cached (short TTL) and best-effort: any failure yields
// an empty list and the field stays a plain free-text combo, so a missing key
// or an offline provider never degrades the settings page.
package web

import (
	"net/http"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/llmprovider"
	"github.com/opentacit/tacit/internal/registry/suggest"
)

var (
	modelCacheMu sync.Mutex
	modelCache   = map[string]modelCacheEntry{}
)

type modelCacheEntry struct {
	models []suggest.ModelInfo
	at     time.Time
}

const (
	modelCacheTTL     = 10 * time.Minute // a good list is stable
	modelCacheMissTTL = 30 * time.Second // retry a failed/empty fetch soon
)

// listModels returns a provider's models, from the injected lister (tests) or
// the live, cached fetch.
func (s *Server) listModels(provider string) []suggest.ModelInfo {
	if s.ModelLister != nil {
		return s.ModelLister(provider)
	}
	return cachedModelList(provider)
}

func cachedModelList(provider string) []suggest.ModelInfo {
	modelCacheMu.Lock()
	e, ok := modelCache[provider]
	modelCacheMu.Unlock()
	ttl := modelCacheTTL
	if len(e.models) == 0 {
		ttl = modelCacheMissTTL // don't cache a failure for long
	}
	if ok && time.Since(e.at) < ttl {
		return e.models
	}
	models := fetchModelList(provider)
	modelCacheMu.Lock()
	modelCache[provider] = modelCacheEntry{models: models, at: time.Now()}
	modelCacheMu.Unlock()
	return models
}

func fetchModelList(provider string) []suggest.ModelInfo {
	c, err := suggest.ClientForProvider(provider)
	if err != nil {
		return nil
	}
	models, err := c.ListModels()
	if err != nil {
		return nil
	}
	return models
}

// handleSettingsModels serves a provider's current model ids as JSON for the
// settings combo box. Admin-only (it exercises the model key). Never fails
// hard: an unknown or unconfigured provider returns an empty list.
func (s *Server) handleSettingsModels(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(s.sessionUser(r)) {
		http.Error(w, "a configured administrator (TACIT_ADMIN_EMAILS) can list models", http.StatusForbidden)
		return
	}
	provider := r.URL.Query().Get("provider")
	if !llmprovider.Valid(provider) {
		provider = llmprovider.Default
	}
	s.sendJSON(w, http.StatusOK, map[string]any{"models": s.listModels(provider)})
}

// modelCatalogScript turns the two model fields into combo boxes: it fills the
// shared <datalist> from /settings/models for the selected provider on load,
// refreshes it when the provider dropdown changes, and shows the selected
// model's name/description on the line under each field (whatever the provider
// supplies — OpenRouter a full blurb, Anthropic a display name, plain OpenAI
// nothing). Fails silently — on any error the fields remain plain free-text
// inputs. Inline is the house style for the dashboard (see tags.go), and the
// fetch is same-origin.
const modelCatalogScript = `<script>(function(){
var sel=document.querySelector('select[name="llm_provider"]');
var dl=document.getElementById('model-catalog');
if(!sel||!dl)return;
var inputs=document.querySelectorAll('input[list="model-catalog"]');
var meta={};
function byName(n){return document.querySelector('input[name="'+n+'"]');}
// A field still holding the PREVIOUS provider's default is stale the moment the
// dropdown moves: it names an address (or a model id) belonging to a provider
// this registry no longer talks to, and it is what a save would write. Moving
// the value with the dropdown is the whole point of showing a default — the box
// says what will be called. A value the operator typed themselves is left
// alone: it only follows when it is exactly the default it is replacing, or
// empty.
var was={};
function follow(el,from,to){
  if(!el||!from)return;
  // Exactly the default it is replacing, and nothing else: a value the operator
  // typed is theirs, and a BLANK field already means "this provider's default"
  // — filling it would turn that standing instruction into a pinned id.
  if(el.value.trim()===from)el.value=to||'';
}
function applyDefaults(moved){
  var opt=sel.options[sel.selectedIndex];if(!opt)return;
  var base=byName('llm_base_url'),sug=byName('suggest_model'),tag=byName('tagmerge_model');
  // Only on an actual move. Filling a blank field on load would make the page
  // dirty before anything was touched — Save would light up, and saving would
  // pin today's default ids into registry.env as if the operator had chosen them.
  if(moved){
    follow(base,was.base,opt.dataset.base);
    follow(sug,was.model,opt.dataset.model);
    follow(tag,was.model,opt.dataset.model);
  }
  if(base)base.placeholder=opt.dataset.base||'';
  if(sug)sug.placeholder=opt.dataset.model||'';
  if(tag)tag.placeholder=opt.dataset.model||'';
  was={base:opt.dataset.base,model:opt.dataset.model};
}
function showDesc(inp){
  var d=inp.parentNode.querySelector('.model-desc');if(!d)return;
  var m=meta[inp.value.trim()];
  d.textContent=m?((m.name||'')+(m.name&&m.description?'—':'')+(m.description||'')):'';
}
function load(){
  fetch('/settings/models?provider='+encodeURIComponent(sel.value),{headers:{Accept:'application/json'}})
    .then(function(r){return r.ok?r.json():{models:[]};})
    .then(function(d){
      dl.innerHTML='';meta={};
      (d.models||[]).forEach(function(m){
        meta[m.id]={name:m.name,description:m.description};
        var o=document.createElement('option');o.value=m.id;if(m.name)o.label=m.name;dl.appendChild(o);
      });
      inputs.forEach(showDesc);
    }).catch(function(){});
}
inputs.forEach(function(inp){inp.addEventListener('input',function(){showDesc(inp);});});
sel.addEventListener('change',function(){applyDefaults(true);load();});
applyDefaults(false);load();
})();</script>`
