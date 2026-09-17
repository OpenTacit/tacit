// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

// VizHoverScript is the charts' hover layer: a crosshair and an all-series
// tooltip on a line chart, a per-mark tooltip on anything carrying data-tip. It
// belongs to the chart grammar in chart.go rather than to either console — the
// ingress draws the same charts from the same renderer, and a second
// implementation of "what happens when you point at a mark" would be a second
// feel for one look.
//
// It expects the #tip element the shells carry, and the classes app.css defines
// for it. window.tacitWireViz is exported for pages that build a plot in the
// browser (the registry's Usage chart, the ingress's request-rate chart): they
// call it on each chart they insert and get the identical tooltip.
const VizHoverScript = `<script>
(function(){
// hover layer — crosshair + all-series tooltip on line charts; per-mark on bars
var tip=document.getElementById('tip');
// Remember the latest pointer so a synthetic (focus) show can place itself like
// the interaction that triggered it — and, for touch, anchor at the finger.
var lastTouch=false,lastX=0,lastY=0;
addEventListener('pointerdown',function(e){lastTouch=(e.pointerType==='touch');lastX=e.clientX;lastY=e.clientY;},true);
function place(e){var r=tip.getBoundingClientRect(),touch=e.pointerType==='touch',pad=touch?18:14;
  // Touch: sit up-and-LEFT of the contact point, clear of a right-hand finger.
  // Mouse: the conventional down-and-right of the cursor. Flip either axis back
  // toward the point if the preferred side would run off-screen.
  var x=touch?e.clientX-r.width-pad:e.clientX+pad;
  var y=touch?e.clientY-r.height-pad:e.clientY+pad;
  if(x<8)x=e.clientX+pad; else if(x+r.width>innerWidth-8)x=e.clientX-r.width-pad;
  if(y<8)y=e.clientY+pad; else if(y+r.height>innerHeight-8)y=e.clientY-r.height-pad;
  tip.style.left=x+'px';tip.style.top=y+'px';}
function hide(){tip.style.display='none';}

function wireViz(svg){
  var names=(svg.dataset.series||'').split('|');
  var keys=(svg.dataset.keys||'').split('|');
  var cross=svg.querySelector('.cross');
  function hideCross(){if(cross)cross.setAttribute('visibility','hidden');}
  function show(hit,e){
    tip.replaceChildren();
    var h=document.createElement('div');h.className='tip-h';
    h.textContent=hit.dataset.label;tip.append(h);        // untrusted → textContent
    var vals=(hit.dataset.v||'').split('|');
    names.forEach(function(n,i){
      var row=document.createElement('div');row.className='tip-row';
      var k=document.createElement('span');k.className='k '+(keys[i]||'');
      var b=document.createElement('b');b.textContent=vals[i]||'0';     // value leads
      var lbl=document.createElement('span');lbl.textContent=n;         // label follows
      row.append(k,b,lbl);tip.append(row);});
    tip.style.display='block';place(e);
    if(cross){cross.setAttribute('x1',hit.dataset.x);cross.setAttribute('x2',hit.dataset.x);
      cross.setAttribute('visibility','visible');}}
  // One pointer handler for the whole plot: resolve the column from the
  // pointer's COORDINATES, not from the element the event fired on. On touch
  // the browser sets implicit pointer capture on the first-touched rect, so
  // every move keeps firing there — a per-rect handler would freeze the
  // tooltip on the initial column through a drag; elementFromPoint follows the
  // finger. touch-action:pan-y (CSS) keeps a horizontal scrub firing moves
  // instead of being stolen for a scroll.
  svg.addEventListener('pointermove',function(e){
    lastTouch=(e.pointerType==='touch');lastX=e.clientX;lastY=e.clientY;
    var el=document.elementFromPoint(e.clientX,e.clientY);
    if(el&&svg.contains(el)&&el.classList.contains('hit'))show(el,e);
    else{hide();hideCross();}});
  svg.addEventListener('pointerleave',function(){hide();hideCross();});
  // Keyboard: each hit is focusable. A touch tap anchors at the finger (so the
  // up-left placement clears it); a keyboard tab, having no pointer, anchors at
  // the focused column's own box.
  svg.querySelectorAll('.hit').forEach(function(hit){
    hit.addEventListener('focus',function(){
      if(lastTouch){show(hit,{clientX:lastX,clientY:lastY,pointerType:'touch'});return;}
      var r=hit.getBoundingClientRect();show(hit,{clientX:r.left+r.width/2,clientY:r.top});});
    hit.addEventListener('blur',function(){hide();hideCross();});});}
// Server-rendered charts are wired once, here. A page that builds its plot in
// the browser (usage.go fetches its data, so its SVG doesn't exist yet) calls
// the same function on each chart it inserts, and gets the identical tooltip.
window.tacitWireViz=wireViz;
document.querySelectorAll('svg.viz').forEach(wireViz);
// The tooltip is position:fixed at a viewport point captured when it showed;
// once the page scrolls, that point no longer sits over the mark (and a
// tap-focused tooltip never gets a pointerleave). Dismiss it on any scroll —
// capture phase so scrolls inside nested containers count too.
addEventListener('scroll',function(){
  if(tip.style.display==='none')return;
  hide();
  document.querySelectorAll('svg.viz .cross').forEach(function(c){c.setAttribute('visibility','hidden');});
  var a=document.activeElement;
  if(a&&a.classList&&a.classList.contains('hit'))a.blur();
},true);

document.querySelectorAll('[data-tip]').forEach(function(el){
  el.addEventListener('pointermove',function(e){
    tip.replaceChildren();
    var h=document.createElement('div');h.className='tip-h';
    h.textContent=el.dataset.tip;tip.append(h);
    tip.style.display='block';place(e);});
  el.addEventListener('pointerleave',hide);});
})();
</script>`
