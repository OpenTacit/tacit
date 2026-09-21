// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

// The project page's JavaScript. Every line of it is optional: the page is
// correct before any of this runs, and what each piece adds is described where
// it is declared.

import "text/template"

// SiteSignOutHotkey is the operator's way out of their preview session: the
// escape key, bound to the sign-out route. It is the only thing that
// distinguishes their copy of the page from a visitor's, and it is invisible —
// which is the point. The first version was a bar above the stage naming the
// publication state, and it meant the composition an operator reviewed was
// never the composition anybody else got; the review that matters is of the
// page as it publishes, pixel for pixel.
//
// The check the bar used to offer survives at the cost of a keystroke: sign
// out, ask for the same address again, and what comes back is what a stranger
// gets. Which state the switch is in is the operator's own environment
// variable, and `serve` prints it at startup.
//
// Modifier chords are left alone, so a browser shortcut that happens to reach
// escape does not sign anybody out.
func SiteSignOutHotkey(signOutHref string) string {
	return `<script>addEventListener('keydown',function(e){` +
		`if(e.key==='Escape'&&!e.ctrlKey&&!e.altKey&&!e.metaKey)location.href='` +
		template.JSEscapeString(signOutHref) + `';});</script>`
}

const siteHighlightScript = `<script>(function(){
var queued=false,selector='[data-highlight-orange],[data-highlight-green]';
if(!document.createRange||!document.createTreeWalker)return;
var textRange=function(el,phrase){
 var walker=document.createTreeWalker(el,NodeFilter.SHOW_TEXT),nodes=[],all='',node;
 while((node=walker.nextNode())){nodes.push({node:node,start:all.length});all+=node.data;}
 phrase=phrase||all;var start=all.indexOf(phrase),end=start+phrase.length;
 if(start<0||!phrase)return null;
 var first=null,last=null,firstOffset=0,lastOffset=0;
 nodes.forEach(function(part){
  var partEnd=part.start+part.node.data.length;
  if(!first&&start>=part.start&&start<=partEnd){first=part.node;firstOffset=start-part.start;}
  if(end>=part.start&&end<=partEnd){last=part.node;lastOffset=end-part.start;}
 });
 if(!first||!last)return null;
 var range=document.createRange();range.setStart(first,firstOffset);range.setEnd(last,lastOffset);
 return range;
};
var paint=function(){
 document.querySelectorAll('.site-highlight-stroke').forEach(function(n){n.remove();});
 document.querySelectorAll(selector).forEach(function(el){
  el.classList.add('site-highlight-host');var box=el.getBoundingClientRect();
  [['orange','site-orange'],['green','site-green']].forEach(function(pair){
   if(!el.hasAttribute('data-highlight-'+pair[0]))return;
   var range=textRange(el,el.getAttribute('data-highlight-'+pair[0]));
   if(!range)return;
   Array.prototype.forEach.call(range.getClientRects(),function(rect){
    var stroke=document.createElement('i');
    stroke.className='site-highlight-stroke '+pair[1];stroke.setAttribute('aria-hidden','true');
    stroke.style.left=(rect.left-box.left)+'px';stroke.style.top=(rect.top-box.top+rect.height*.13)+'px';
    stroke.style.width=rect.width+'px';stroke.style.height=(rect.height*.76)+'px';
    el.appendChild(stroke);
   });
  });
 });
};
var queue=function(){if(queued)return;queued=true;requestAnimationFrame(function(){queued=false;paint();});};
if(document.fonts&&document.fonts.ready)document.fonts.ready.then(queue);else queue();
addEventListener('resize',queue,{passive:true});
})();</script>`

// siteScript is the page's only JavaScript, and everything it does is optional.
// It stops the marquee when the reader has asked for less motion, and on a screen wide
// enough for two columns it turns the user-flow section into a scroll stage.
// With JavaScript off there is no marquee animation, and the flow section reads
// top to bottom — the page loses nothing but motion.
//
// The stage is the reference pattern for this kind of section: the section is
// taller than the screen, the composition inside it is sticky, and progress
// through the extra height drives the tape behind the glass — position by
// position, not moment by moment, so the session runs on under the reader's own
// scroll and then holds still on the moment they have reached. The list is
// navigation in both directions: scrolling moves the highlight, clicking a
// moment moves the scroll. Only that click is animated, and only for readers
// who have not asked for reduced motion; the travel itself is the reader's
// scroll and has no clock of its own to slow down.
var siteScript = `<script>(function(){
var reduce=window.matchMedia&&window.matchMedia('(prefers-reduced-motion:reduce)').matches;
// The guide window. showModal() rather than an open attribute: it is what puts the
// dialog in the top layer, dims the page with ::backdrop, holds focus inside, and
// makes Escape close it — all of which a hand-rolled overlay has to reimplement,
// and the last of which the reader will try first.
//
// Clicking outside closes it, and "outside" is a click whose target IS the dialog:
// the dialog's own box is the full-viewport backdrop area, and the window inside it
// is a child that swallows its own clicks. No coordinate arithmetic, and no
// listener on the document that would fight the rest of the page.
//
// A browser without <dialog> leaves the light inert rather than opening something
// it cannot dismiss.
var guide=document.querySelector('.site-guide');
if(guide&&guide.showModal){
 var win=guide.querySelector('.site-guide-win');
 var from=document.querySelector('.site-install');
 // The open is a FLIP: measure where the hero's little window is, measure where
 // this one has landed, and play the difference as one transform. The window
 // appears to be the SAME window, opened — which is the thing a green light in a
 // title bar promises, and a modal that merely fades in quietly breaks.
 //
 // Measured after showModal(), never before: until the dialog is in the top layer
 // it has no box, and the arithmetic would be against a rect of zeros.
 var morph=function(opening){
  if(reduce||!win.animate)return null;
  var b=win.getBoundingClientRect();
  if(!b.width)return null;
  var a=from&&from.getBoundingClientRect();
  // The window grows out of the hero's window only while the hero's window is on
  // screen. Opened from the button at the foot of the page it is a screen or two
  // above, and a modal that flies in from off-frame reads as a glitch rather than
  // as the same window opening. Then it is a plain lift from where it already is.
  var onScreen=a&&a.width&&a.bottom>0&&a.top<(window.innerHeight||0);
  var small=onScreen
   ?{transform:'translate('+(a.left-b.left)+'px,'+(a.top-b.top)+'px) scale('+
     (a.width/b.width)+','+(a.height/b.height)+')',opacity:0.4}
   :{transform:'translate(2%,2%) scale(.96)',opacity:0};  // top-left origin, so nudge back to centre
  var full={transform:'none',opacity:1};
  return win.animate(opening?[small,full]:[full,small],
   {duration:opening?220:170,easing:'cubic-bezier(.2,.8,.25,1)'});
 };
 var openGuide=function(){
  guide.classList.remove('site-guide-closing');  // a close that was interrupted
  guide.showModal();morph(true);};
 // Closing waits for the window to arrive back where it came from; close() first
 // would take the element out of the top layer mid-flight and the animation would
 // play on something nobody can see. A browser that gave us no animation closes
 // immediately, which is the same behaviour reduced motion asks for.
 //
 // The class runs the backdrop's dim backwards over the same 170ms, because the
 // window minimising while the dim behind it holds at full strength and then
 // vanishes is two gestures, and the second one is a flash. Removed after close()
 // so the next open starts from a backdrop that fades in rather than one still
 // holding transparent.
 var closeGuide=function(){
  var back=morph(false);
  if(!back)return void guide.close();
  guide.classList.add('site-guide-closing');
  back.onfinish=function(){
   guide.close();
   guide.classList.remove('site-guide-closing');};
 };
 [].forEach.call(document.querySelectorAll('[data-guide-open]'),function(b){
  b.addEventListener('click',openGuide);});
 // The whole install window opens it too. That is the same bargain the
 // dashboard's clickable rows make (RowLinkScript): ONE real control — the green
 // light, which is a button, is labelled and can be tabbed to — and a larger
 // surface for the pointer, which has no reason to hunt for an 11px circle. The
 // surface is not a second control and does not appear in the tab order.
 //
 // Clicks that land on the light itself are left to it, or the light would open
 // the window twice on one press.
 //
 // A click that ENDS A SELECTION is not a click on the window. The install
 // command is the one selectable thing inside it, and somebody dragging across it
 // to copy by hand finishes on mouseup — which without this opens the guide over
 // the text they were taking, and takes the selection with it.
 [].forEach.call(document.querySelectorAll('[data-guide-surface]'),function(bar){
  bar.addEventListener('click',function(e){
   if(e.target.closest('[data-guide-open]'))return;
   var sel=window.getSelection&&window.getSelection();
   if(sel&&!sel.isCollapsed&&String(sel).trim())return;
   openGuide();});});
 [].forEach.call(guide.querySelectorAll('[data-guide-close]'),function(b){
  b.addEventListener('click',closeGuide);});
 guide.addEventListener('click',function(e){if(e.target===guide)closeGuide();});
 // Escape is the browser's, and it closes without asking us — so the window would
 // vanish rather than fold away. Take the cancel, run the same close as the
 // chrome, and the three routes out are one behaviour.
 guide.addEventListener('cancel',function(e){e.preventDefault();closeGuide();});
}
var m=document.querySelector('.site-marquee-track');
if(m&&reduce)m.style.animation='none';
var flow=document.querySelector('[data-flow]');
// Set by the stage below: a scenario swap replaces the tape behind the glass,
// and an unscrolled tape would drop the reader back to the first moment. Null
// where there is no stage, because there the window is not cut and the whole
// session is already on the page.
var toActiveMoment=null;
// The scenario picker, wired before anything about width: it is the one control
// on this page and it has to work on a phone too. Everything scenario-specific
// carries data-scen and starts hidden but for the first, so switching is one
// attribute per element and the no-script page is already correct.
//
// A switch leaves the reader on the moment they were reading, and the swap
// happens under them. A version of this rewound the stage to the first moment
// instead — the argument being that the three are a sequence, and arriving in
// "one answer applies it" for a story you have not seen the start of tells you
// nothing. It did not survive contact: the scroll it needed was cancelled by
// something in a real browser that no headless run here reproduced, and it was
// removed rather than left half-working. Do not put it back without a way to
// see it fail.
if(flow){
 var picks=[].slice.call(flow.querySelectorAll('.site-term-pick select'));
 if(picks.length){
  flow.classList.add('flow-pick');
  var setScen=function(key){
   [].forEach.call(flow.querySelectorAll('[data-scen]'),function(el){
    el.hidden=el.getAttribute('data-scen')!==key;});
   picks.forEach(function(p){p.value=key;});
   // The new tape starts at the top; put it where the reader already is.
   if(toActiveMoment)toActiveMoment();};
  picks.forEach(function(p){p.addEventListener('change',function(){setScen(p.value);});});
 }
}
if(flow&&window.matchMedia&&window.matchMedia('(min-width:980px)').matches){
 flow.classList.add('flow-live');
 var steps=[].slice.call(flow.querySelectorAll('.site-flow-step'));
 var view=flow.querySelector('.site-term-view');
 var tapes=[].slice.call(flow.querySelectorAll('.site-term-tape'));
 var n=steps.length,active=-1,offs=null;
 // Where each moment sits on the tape, measured rather than counted: the browser
 // already knows, and a line-count arithmetic here would be a second copy of
 // the line height to keep in step with the stylesheet. Only the visible tape
 // can be measured — a hidden one has no layout — so the offsets are taken from
 // whichever is on screen, and every tape is the same shape anyway. Cached,
 // because the tape's position is now read on every frame of the scroll, and
 // dropped whenever the layout under it can have moved: a resize, or a scenario
 // swap putting a different tape behind the glass.
 var offsets=function(){
  if(offs)return offs;
  var t=tapes.filter(function(x){return !x.hidden;})[0]||tapes[0];
  var zero=t.children[0]?t.children[0].offsetTop:0;
  // Clamped to where the tape can actually go: the last moment is near the end
  // of the session, so its own offset is past the tape's travel and the glass
  // stops short of it. Unclamped, the easing spends its last tenth arriving
  // somewhere the tape cannot reach, which reads as the motion dying early.
  var max=Math.max(0,view.scrollHeight-view.clientHeight);
  offs=steps.map(function(_,i){var pre=t.children[i];
   return pre?Math.min(max,pre.offsetTop-zero):0;});
  return offs;};
 var setActive=function(i){if(i===active)return;active=i;
  steps.forEach(function(b,k){b.setAttribute('aria-pressed',k===i?'true':'false');});};
 var span=function(){return flow.offsetHeight-window.innerHeight;};
 var top=function(){return flow.getBoundingClientRect().top+window.pageYOffset;};
 // The travel, moment by moment. Each moment owns an equal slice of the
 // section; inside its slice the tape ARRIVES over the first travel-fraction of
 // it and then holds for the rest. So the session runs on under the reader's
 // own scroll — the thing the window is there to show — and then sits still
 // while the moment it reached is being read. Eased, so the arrival starts and
 // ends at rest instead of stopping dead.
 //
 // An earlier version set the tape straight to the moment's offset, which the
 // browser's own smooth scrolling then animated: the tape moved on ITS clock,
 // not the reader's, so it lurched a screenful at each boundary and finished
 // whenever it finished. The stylesheet's scroll-behavior went back to auto
 // with this — a per-frame write and a smooth scroll fight each other.
 var travel=0.45;
 // The rail, drawn from the same three numbers the glass is scrolled by. It is
 // reported and never read back: the thumb's size and position are computed from
 // scrollTop, scrollHeight and clientHeight after each write, so it cannot
 // disagree with the tape, and there is nothing to grab that would disagree with
 // the page.
 //
 // The thumb has a floor. A long session in a 24-row window puts the honest
 // proportion under a couple of pixels, which reads as dirt on the screen rather
 // than as a position — so it stops shrinking and the travel is scaled to what is
 // left, which is what every native scrollbar does.
 var rail=flow.querySelector('.site-term-rail');
 var thumb=rail&&rail.firstElementChild;
 var drawRail=function(){
  if(!thumb)return;
  var h=view.clientHeight,all=view.scrollHeight,room=all-h;
  // A tape shorter than its window has no position worth reporting, and a rail
  // with a full-length thumb in it is furniture saying nothing.
  if(room<=0){rail.hidden=true;return;}
  rail.hidden=false;
  var railH=rail.clientHeight;
  var th=Math.max(Math.min(railH,18),railH*h/all);
  thumb.style.height=th+'px';
  thumb.style.top=(railH-th)*Math.min(1,view.scrollTop/room)+'px';};
 var place=function(){
  var s=span(),o=offsets();
  var u=s>0?Math.max(0,Math.min(n-1e-4,(window.pageYOffset-top())/s*n)):0;
  var i=Math.floor(u);
  setActive(i);
  // Moment one is where the tape already starts; there is nothing before it to
  // come from. The hold is also what makes the list's click targets land at
  // rest — mid-slice is past the travel.
  if(!i){view.scrollTop=o[0];return void drawRail();}
  var t=Math.min(1,(u-i)/travel);
  view.scrollTop=o[i-1]+(o[i]-o[i-1])*t*t*(3-2*t);
  drawRail();};
 // One write per frame at most. The unthrottled version wrote scrollTop from
 // inside the scroll event, which on a trackpad is several times a frame and
 // every one of them a forced layout.
 var raf=window.requestAnimationFrame||function(f){return setTimeout(f,16);},queued=false;
 var onScroll=function(){if(queued)return;queued=true;
  raf(function(){queued=false;place();});};
 steps.forEach(function(b,k){b.addEventListener('click',function(){
  window.scrollTo({top:top()+span()*(k+0.5)/n,behavior:reduce?'auto':'smooth'});});});
 // The tape has been swapped under the glass, not moved along: the cached
 // offsets belong to the tape that left, and the one that arrived is at its
 // top. Re-measure and put it where the reader already is.
 toActiveMoment=function(){offs=null;place();};
 window.addEventListener('scroll',onScroll,{passive:true});
 window.addEventListener('resize',function(){offs=null;place();});
 // The first measurement can be taken on the fallback face: the tape is
 // monospace, the embedded one arrives after the script, and every line of it
 // changes height when it lands. Measured once and cached, the stage would spend
 // the rest of the page a few pixels wrong about where each moment is.
 if(document.fonts&&document.fonts.ready)document.fonts.ready.then(function(){offs=null;place();});
 place();
}
// The dashboard carousel. The track scrolls natively, so the swipe, the snap and
// the momentum at each end are the platform's and not a transform this script
// animates by hand — on a touch screen that difference is most of the feel. What
// is left for the script is the part the platform has no opinion about: reveal
// the controls, move the track when a dot is pressed, and keep the dots and the
// description column reporting whichever screen the reader stopped on.
//
// Nothing is revealed unless the whole set is here — a track, more than one
// slide, and a dot for each. A carousel missing a dot is a reader who cannot
// tell how many screens there are, and the base page below it is honest.
var see=document.querySelector('[data-see-carousel]');
if(see){
 var track=see.querySelector('.site-see-track');
 var slides=[].slice.call(see.querySelectorAll('.site-see-slide'));
 var seeDots=[].slice.call(see.querySelectorAll('.site-see-dot'));
 if(track&&slides.length>1&&seeDots.length===slides.length){
  see.classList.add('see-live');
  slides.forEach(function(s){s.hidden=false;});
  var seeTexts=[].slice.call(see.querySelectorAll('[data-see-text]'));
  // Two positions, and they are not the same thing. seeAt is where the track IS,
  // which is what the dots and the column report; seeTarget is where it has been
  // sent, which is what the next keypress counts from. Collapsing them is what
  // made the keys feel wrong: a second press during the glide read the position
  // the reader could still see and sent the track where it was already going.
  var seeAt=-1,seeTarget=0,gliding=false;
  var setSee=function(i){
   if(i===seeAt)return;
   seeAt=i;
   var key=slides[i].getAttribute('data-see');
   seeDots.forEach(function(d){
    d.setAttribute('aria-current',d.getAttribute('data-see-to')===key?'true':'false');});
   // The column is swapped, not cross-faded: it sits beside the picture rather
   // than over it, and a reader mid-swipe should find the new words already
   // there. A whole block at a time — title, deck and paragraph move together or
   // the column spends the swap describing two screens at once.
   seeTexts.forEach(function(el){el.hidden=el.getAttribute('data-see-text')!==key;});};
  // Where a slide starts, measured against its first sibling rather than against
  // the track: offsets between siblings share an offsetParent whatever the track
  // is positioned as, and this stays right if the slides ever stop being exactly
  // one track wide.
  var seeAtX=function(i){return slides[i].offsetLeft-slides[0].offsetLeft;};
  // Which slide the reader stopped on, measured and not counted: the middle of
  // the track against the middle of each slide, so a half swipe that snapped
  // back reports the slide it snapped back TO.
  var nearest=function(){
   var mid=track.scrollLeft+track.clientWidth/2,best=0,gap=Infinity;
   slides.forEach(function(s,i){
    var g=Math.abs(seeAtX(i)+s.offsetWidth/2-mid);
    if(g<gap){gap=g;best=i;}});
   return best;};
  // Throttled to a frame: a trackpad fires this several times per frame and each
  // one reads layout.
  var seeRaf=window.requestAnimationFrame||function(f){return setTimeout(f,16);},seeQ=false;
  track.addEventListener('scroll',function(){
   if(seeQ)return;
   seeQ=true;
   seeRaf(function(){seeQ=false;
    var i=nearest();
    // Mid-glide the track is passing through positions the reader did not ask
    // for, so the target is not taken from them — only the arrival clears the
    // flag. Every other scroll is the reader's own hand, and there the target
    // is wherever they have moved to.
    if(gliding){if(i===seeTarget)gliding=false;}
    else seeTarget=i;
    setSee(i);});},{passive:true});
  // A hand on the track outranks a glide in progress: whatever it was gliding
  // towards, the reader is steering now.
  var seeTakeOver=function(){gliding=false;};
  track.addEventListener('pointerdown',seeTakeOver,{passive:true});
  track.addEventListener('touchstart',seeTakeOver,{passive:true});
  track.addEventListener('wheel',seeTakeOver,{passive:true});
  // Scroll, and say nothing about it. The column and the dots are changed by the
  // scroll handler as the track passes each screen, which is the same path a
  // swipe takes and therefore the same feel. Setting them here as well is what
  // made a keypress look broken: the sentence swapped to the destination before
  // the picture had left, and then the glide's own scroll events dragged it
  // backwards through the screen in between.
  var goSee=function(i){
   seeTarget=i;
   gliding=true;
   track.scrollTo({left:seeAtX(i),behavior:reduce?'auto':'smooth'});};
  seeDots.forEach(function(d,i){d.addEventListener('click',function(){goSee(i);});});
  // The arrow keys, for the reader who is looking at the section rather than at
  // one of its dots. Bound to the document and not to the track, because nothing
  // here is focused when somebody scrolls down and reaches for a key — a control
  // you must find with Tab first is one that gets used by whoever already knew.
  //
  // Which makes the guards the substance of it. It answers only while the section
  // is actually on screen, so the same keys elsewhere on the page stay the
  // browser's; never while a field, a picker or the scenario select has focus,
  // where left and right belong to the caret; never under a modifier, which is a
  // browser shortcut; and never while the guide window is open over the top of
  // everything. preventDefault comes only once a move is certain, so an arrow at
  // either end of the track is left to the page.
  document.addEventListener('keydown',function(e){
   if(e.defaultPrevented||e.altKey||e.ctrlKey||e.metaKey||e.shiftKey)return;
   var back=e.key==='ArrowLeft',fwd=e.key==='ArrowRight';
   if(!back&&!fwd)return;
   var t=e.target,tag=t&&t.tagName;
   if(tag==='INPUT'||tag==='SELECT'||tag==='TEXTAREA'||(t&&t.isContentEditable))return;
   if(document.querySelector('dialog[open]'))return;
   var box=see.getBoundingClientRect(),h=window.innerHeight||0;
   if(box.bottom<=0||box.top>=h)return;
   var to=seeTarget+(fwd?1:-1);
   if(to<0||to>=slides.length)return;
   e.preventDefault();
   goSee(to);});
  setSee(0);
 }
}
})();</script>`
