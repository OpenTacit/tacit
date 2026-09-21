// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The dashboard shell: design tokens (both color modes selected from the
// palette in docs/design/look-and-feel-directions.md), top navigation,
// chart chrome, restyles for the existing pages, and the dependency-free
// hover layer (crosshair + tooltip on line charts, per-mark tooltips on bars).
// Placeholders («…») are replaced at render time by renderShell/render_signin —
// «PRODUCT» among them, so the wordmark, the titles and the manifest all take
// the configured product name (internal/product).
package web

import (
	"html"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/ui"
)

// productHTML is the configured product name (internal/product) escaped for the
// markup «PRODUCT» lands in.
//
// There used to be a productJS beside it, escaping the same name for a
// single-quoted literal inside an inline <script> — inline script content is not
// HTML-parsed, so an entity written there reaches the reader verbatim and a name
// with an apostrophe read as "Foo&#39;s". Nothing pastes the name into script
// source any more: the Usage page was the last, and it takes the name as JSON
// and escapes it for the document itself. Anything tempted to reintroduce that
// should carry its value as JSON too rather than bring the escaper back.
func productHTML() string { return html.EscapeString(product.Name()) }

// The brand mark: the solid ◆ core framed by two unclosed chevrons — guillemets
// around knowledge nobody wrote down. The gaps at the top and bottom vertices
// are the point; never close them. In monospace surfaces (hook nudges, CLI
// output) the mark stays the plain ◆ character — this drawn form is HTML-only.
const markSVG = ui.MarkSVG

// The same mark as a data-URI favicon: accent blue, swapping shades with the
// browser's color scheme via the SVG's own media query. Inline so the shell
// stays a single string with no asset route.
const faviconLink = ui.FaviconLink

// homeScreenLinks make the registry a real standalone web app when added to the
// home screen — not just a bookmark. The manifest's scope ("/") is the key part:
// it tells iOS every same-origin page belongs to the app, so tapping a nav link
// stays inside the chromeless window instead of handing off to Safari (which
// would show the URL bar). apple-mobile-web-app-capable enables standalone on
// iOS; the raster apple-touch-icon is the home-screen icon (Safari won't use the
// SVG favicon here); apple-mobile-web-app-title sets the label under it.
const homeScreenLinks = `<link rel="apple-touch-icon" href="/apple-touch-icon.png">` +
	`<link rel="manifest" href="/manifest.webmanifest">` +
	`<meta name="apple-mobile-web-app-capable" content="yes">` +
	`<meta name="mobile-web-app-capable" content="yes">` +
	`<meta name="apple-mobile-web-app-status-bar-style" content="default">` +
	`<meta name="apple-mobile-web-app-title" content="«PRODUCT»">`

// webManifest is served at /manifest.webmanifest. display:standalone + scope:"/"
// keep in-app navigation from breaking out to Safari; the colors match the
// light-theme chrome (the manifest can't be theme-aware, so the neutral ground
// is used for the launch splash). «PRODUCT» is filled by the handler in icon.go.
const webManifest = `{
  "name": "«PRODUCT»",
  "short_name": "«PRODUCT»",
  "description": "Your organization’s measured playbook for work with AI.",
  "start_url": "/",
  "scope": "/",
  "display": "standalone",
  "theme_color": "#0a0d14",
  "background_color": "#eef1f6",
  "icons": [
    { "src": "/apple-touch-icon.png", "sizes": "180x180", "type": "image/png", "purpose": "any" }
  ]
}`

// Theme selection. The palette itself lives in assets/app.css and nowhere else;
// a second copy used to sit here, went unreferenced, and quietly fell a redesign
// behind. themeInitScript applies a saved choice before first paint so there's
// no flash of the wrong mode; the three glyphs drive the header toggle that
// cycles system → light → dark.
const themeInitScript = ui.ThemeInitScript

const sunSVG = ui.SunSVG
const moonSVG = ui.MoonSVG
const autoSVG = ui.AutoSVG

// groundCSS is the only style that ships inline, and it exists to stop a flash of
// the browser's own white page on the way to a dark one.
//
// An external stylesheet in <head> is render-blocking, so in theory nothing paints
// before it lands. Chromium holds the previous page's pixels while it waits, so
// there is nothing to see. WebKit does not always: it can paint the new document
// on its UA defaults first, and a full viewport of white on the way to #0a0d14 is
// the flash. The bigger the page — Outcomes above all — the longer the server
// takes to produce it and the wider that window gets.
//
// So the ground is stated inline, before the link, and the first paint is already
// the right colour whatever the engine decides to do. Three tokens only: the
// board, the plate, and the text on them. Everything else can arrive with the
// stylesheet, because by then the page is already the right colour.
//
// These values are duplicated from assets/app.css, which is exactly the kind of
// copy that goes stale unnoticed — a second palette in this file has done it
// before. TestGroundCSSMatchesStylesheet fails the build if they drift.
// bootHideScript hides the page's CONTENT — never its background — until the
// stylesheet has applied, which is the other half of the flash.
//
// groundCSS fixed the colour, so the page no longer flashes white. It still
// flashed a LAYOUT: the header's controls stacked at the top-left, the nav
// checkbox that is normally hidden, and SVGs at their natural size, because none
// of that has any geometry until app.css lands. Nothing sensible can be painted
// there, so nothing is.
//
// Two things take the class off again, and either alone is enough: the
// stylesheet's own load event, and a timer. The timer is what makes this safe —
// if the stylesheet 404s or is blocked, the page reveals itself unstyled after a
// beat rather than staying blank forever. And because the class is only ever
// added by script, a reader with JavaScript off never has anything hidden.
const bootHideScript = ui.BootHideScript

const groundCSS = ui.GroundCSS

var shellTemplate = `<!doctype html><html lang="en" data-base="«BASE»"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>«PRODUCT»</title>
` + faviconLink + homeScreenLinks + themeInitScript + bootHideScript + groundCSS + `
` + cssLink + `
</head><body>
<header class="top">
 <a class="brand" href="/" aria-label="«PRODUCT» home">` + markSVG + `«PRODUCT»</a>
 <input type="checkbox" id="nav-toggle" class="nav-toggle" aria-label="Menu">
 «DEMO»<span class="account">«ACCOUNT»</span>
 <label for="nav-toggle" class="nav-burger" aria-hidden="true">☰</label>
 <div class="bar-collapse">
  <nav>«NAV»</nav>
  <div class="search"><input id="q" type="search" placeholder="Find…" autocomplete="off"
    aria-label="Find on this page" title="Find rows on this page. The structured Filters below scope the data itself."></div>
  <button id="theme-toggle" class="theme-toggle" type="button" aria-label="Color theme" title="Color theme">` + autoSVG + `</button>
 </div>
</header>
<main class="wrap«WRAPMOD»">«CRUMBS»«CONTENT»</main>
<div id="tip" role="status"></div>
` + ui.MenuScript + ui.RowLinkScript + ui.VizHoverScript + ui.LocalTimeScript + ui.WordmarkFitScript + ui.StylesheetGuardScript + `
<script>
(function(){
 ` + ui.FindRowsJS + `
 ` + ui.SortableTableJS + `
 ` + ui.BulkSelectJS + `
 ` + ui.BusyFormJS + `
 ` + ui.FilterMenuJS + `
 // Copy buttons: any button[data-copy] copies its payload; the .copied class
 // shows the tooltip (styles above). Shared with the project page, which has a
 // copy control in its hero and none of the rest of this script.` + ui.CopyButtonJS + `
 ` + ui.HeatmapEdgesJS + `
 ` + ui.PageControlsJS + `

 // iOS home-screen web app: a plain <a> tap can hand a same-origin navigation
 // back to Safari (revealing the URL bar). The manifest scope prevents this on
 // modern iOS; this keeps older iOS in-app too. Standalone-only, same-origin
 // plain clicks only — external links still open in Safari as expected.
 if(navigator.standalone){
   document.addEventListener('click',function(e){
     var a=e.target.closest('a');
     if(!a||a.target||a.hasAttribute('download'))return;
     if(e.defaultPrevented||e.button!==0||e.metaKey||e.ctrlKey||e.shiftKey||e.altKey)return;
     var href=a.getAttribute('href');
     if(!href||href.charAt(0)==='#')return;
     var u;try{u=new URL(a.href,location.href);}catch(err){return;}
     if(u.origin!==location.origin)return;     // external -> allow Safari
     e.preventDefault();location.href=u.href;});
 }

 // top-bar dropdowns — the avatar button reveals who you're signed in as (and
 // Sign out); the Demo button (demonstration mode only) reveals the dataset
 // picker. Click-outside and Escape dismiss either.
 //
 // Only one menu ANYWHERE in the chrome may be open at a time, and that is the
 // one closer every overlay shares (ui.MenuScript). This used to keep a
 // registry of its own, which closed the other BAR menus and knew nothing about
 // the breadcrumb dropdowns beside them.
 function barMenu(btnId,menuId){
   var btn=document.getElementById(btnId),menu=document.getElementById(menuId);
   if(!btn||!menu)return null;
   var setOpen=function(open){
     if(open&&window.tacitCloseMenus)window.tacitCloseMenus(menu);
     menu.hidden=!open;
     btn.setAttribute('aria-expanded',open?'true':'false');};
   btn.addEventListener('click',function(e){e.stopPropagation();setOpen(menu.hidden);});
   document.addEventListener('click',function(e){
     if(!menu.hidden&&!menu.contains(e.target))setOpen(false);});
   document.addEventListener('keydown',function(e){
     if(e.key==='Escape'&&!menu.hidden){setOpen(false);btn.focus();}});
   return btn;
 }
 // A provider picture that fails to load is dropped, revealing the initials
 // rendered underneath it (the load may have failed before this script ran,
 // hence the complete/naturalWidth check).
 var accBtn=barMenu('account-btn','account-menu');
 if(accBtn){
   var pic=accBtn.querySelector('.avatar-img');
   if(pic){var dropPic=function(){pic.remove();};
     pic.addEventListener('error',dropPic);
     if(pic.complete&&pic.naturalWidth===0)dropPic();}
 }
 barMenu('demo-btn','demo-menu');


 // docs image lightbox — click an illustration to see it full-view; click
 // anywhere (or Escape) to dismiss. The modal image reuses the clicked img's
 // src, so the theme-matched variant of a paired diagram is what enlarges.
 // FLIP, via the Web Animations API (a CSS transition on a just-inserted
 // element races its first style commit and can snap): the enlarged image
 // glides from the in-page image's rect to its natural centered size, and
 // back on dismiss — recomputing the source rect, which may have scrolled.
 // The backdrop fades through the .open transition.
 var imFlip=function(el,fromT,toT,onDone){
   if(matchMedia('(prefers-reduced-motion: reduce)').matches||!el.animate){
     if(onDone)onDone();
     return;
   }
   var a=el.animate([{transform:fromT},{transform:toT}],
     {duration:280,easing:'cubic-bezier(.2,.7,.3,1)',fill:'forwards'});
   if(onDone){a.addEventListener('finish',onDone);setTimeout(onDone,400);}
 };
 var imRectT=function(from,to){
   return 'translate('+(from.left-to.left)+'px,'+(from.top-to.top)+'px) '+
     'scale('+(from.width/to.width)+','+(from.height/to.height)+')';
 };
 document.querySelectorAll('.docs-main img').forEach(function(im){
   if((im.currentSrc||im.src).indexOf('images/hero')>=0)return; // the masthead is not a figure
   im.addEventListener('click',function(){
     var from=im.getBoundingClientRect();
     var overlay=document.createElement('div');overlay.className='img-modal';
     var big=document.createElement('img');big.src=im.currentSrc||im.src;big.alt=im.alt;
     overlay.appendChild(big);
     document.body.appendChild(overlay);
     requestAnimationFrame(function(){overlay.classList.add('open');});
     imFlip(big,imRectT(from,big.getBoundingClientRect()),'none');
     var closing=false;
     var close=function(){
       if(closing)return;
       closing=true;
       document.removeEventListener('keydown',onKey);
       overlay.classList.remove('open');
       var done=function(){overlay.remove();};
       imFlip(big,'none',imRectT(im.getBoundingClientRect(),big.getBoundingClientRect()),done);
     };
     var onKey=function(e){if(e.key==='Escape')close();};
     overlay.addEventListener('click',close);
     document.addEventListener('keydown',onKey);
   });
 });

 // docs on-page index — highlight the section currently in view. Offset-based
 // rather than IntersectionObserver: the active entry is simply the last
 // heading above the fold line, which never flickers between sections.
 var toc=document.querySelector('.docs-toc');
 if(toc){
   var tlinks=[].slice.call(toc.querySelectorAll('a[href^="#"]'));
   var theads=tlinks.map(function(a){
     return document.getElementById(decodeURIComponent(a.getAttribute('href').slice(1)));});
   var spy=function(){
     var y=scrollY+80,cur=0;
     theads.forEach(function(h,i){if(h&&h.offsetTop<=y)cur=i;});
     tlinks.forEach(function(a,i){a.classList.toggle('active',i===cur);});};
   addEventListener('scroll',spy,{passive:true});
   addEventListener('resize',spy,{passive:true});
   spy();
 }

})();
</script>
` + ui.ThemeToggleScript + `
</body></html>`

// The front door — shown to a visitor without a session in place of any gated
// page. It is more than a sign-in button: members sign in, but a first-time
// member has no session yet and needs the (public) user guide and a connect
// command carrying THIS registry's address. «LOGINURL» preserves the page they
// were reaching; «REGISTRYURL» is filled from the request host so the copyable
// command wires the right host (sub-path included). renderSignin substitutes both.
// A standalone document (not the shell), so it ships its own small script for
// the theme toggle and copy button — signinScript, below.
var signinTemplate = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="description" content="«PRODUCT»: your organization’s playbook for AI work.">
<title>«PRODUCT» sign in</title>
` + faviconLink + homeScreenLinks + themeInitScript + bootHideScript + groundCSS + ui.FrontDoorCSS + cssLink + `
</head><body class="signin">
<canvas id="signin-bg" class="signin-bg" data-backdrop aria-hidden="true"></canvas>
<button id="theme-toggle" class="theme-toggle signin-theme" type="button" aria-label="Color theme" title="Color theme">` + autoSVG + `</button>
<main class="technique">
<h1 style="--name-len:«PRODUCTLEN»"` + ui.WordmarkAttr("3.5") + `>` + markSVG + ui.Wordmark(`«PRODUCT»`) + `</h1>
<p class="signin-lede">Automatically capture and share the best ways to use AI across your organization.</p>
«NOTICE»
«SIGNINBUTTON»
<nav class="signin-links" aria-label="Documentation">
 <a href="/docs/user-guide">Read the documentation</a>
</nav>
<details class="signin-setup">
<summary>Connect your AI to «PRODUCT»</summary>
<div class="signin-setup-body">
<div class="signin-options">
<div class="signin-option"><h3>Invite Link</h3>
<div class="copyable"><pre><code>curl -fsSL &lt;link&gt; | sh</code></pre>
<button type="button" class="copy-btn" data-copy="curl -fsSL &lt;link&gt; | sh" aria-label="Copy the Invite Link command">` + copyIcon + `</button></div></div>
<div class="signin-or">or</div>
<div class="signin-option"><h3>Member Key</h3>
<div class="copyable"><pre><code>tacit connect --registry «REGISTRYURL» --key &lt;your-key&gt;</code></pre>
<button type="button" class="copy-btn" data-copy="tacit connect --registry «REGISTRYURL» --key &lt;your-key&gt;" aria-label="Copy the Member Key command">` + copyIcon + `</button></div></div>
</div>
<p class="signin-hint">Full steps are in the <a href="/docs/user-guide/10-get-started/03-join-your-organization.md">setup guide</a>.</p>
</div>
</details>
</main>
` + signinScript + ui.WordmarkFitScript + ui.StylesheetGuardScript + `
</body></html>`

// signinScript is the front door's only JavaScript: the theme toggle (same
// system→light→dark cycle as the shell) and the copy button (same button[data-copy]
// contract). The standalone page carries no shell script, so this repeats those
// two behaviours in miniature; the pre-paint themeInitScript already set the mode.
var signinScript = `<script>(function(){
var order=['system','light','dark'];
var icons={system:'` + autoSVG + `',light:'` + sunSVG + `',dark:'` + moonSVG + `'};
var labels={system:'Theme: system',light:'Theme: light',dark:'Theme: dark'};
var btn=document.getElementById('theme-toggle');
function now(){var t=document.documentElement.getAttribute('data-theme');return t==='light'||t==='dark'?t:'system';}
function apply(m){if(m==='system'){document.documentElement.removeAttribute('data-theme');try{localStorage.removeItem('tacit-theme');}catch(e){}}else{document.documentElement.setAttribute('data-theme',m);try{localStorage.setItem('tacit-theme',m);}catch(e){}}if(btn){btn.innerHTML=icons[m];btn.title=labels[m];btn.setAttribute('aria-label',labels[m]);}}
if(btn){apply(now());btn.addEventListener('click',function(){apply(order[(order.indexOf(now())+1)%order.length]);});}
document.querySelectorAll('button[data-copy]').forEach(function(b){
if(!navigator.clipboard){b.style.display='none';return;}
b.addEventListener('click',function(){navigator.clipboard.writeText(b.dataset.copy).then(function(){b.classList.add('copied');setTimeout(function(){b.classList.remove('copied');},1200);});});
});
// Slide the "New here?" disclosure open/closed instead of snapping. Native
// <details> toggles instantly; here the summary's click is taken over to
// animate the body's height (Web Animations, so it works in Safari too). With
// JS off — or reduced motion — the native instant toggle stands.
var det=document.querySelector('.signin-setup');
var reduce=window.matchMedia&&window.matchMedia('(prefers-reduced-motion:reduce)').matches;
if(det&&det.animate&&!reduce){
var sum=det.querySelector('summary'),body=det.querySelector('.signin-setup-body'),busy=false;
sum.addEventListener('click',function(e){
e.preventDefault();
if(busy)return;busy=true;
body.style.overflow='hidden';
var end=function(){body.style.overflow='';busy=false;};
if(!det.open){
det.open=true;
var h=body.offsetHeight;
body.animate([{height:'0px',opacity:0},{height:h+'px',opacity:1}],{duration:220,easing:'ease-out'}).onfinish=end;
}else{
var h2=body.offsetHeight;
body.animate([{height:h2+'px',opacity:1},{height:'0px',opacity:0}],{duration:170,easing:'ease-in'}).onfinish=function(){det.open=false;end();};
}
});
}
// Signing in: fade the panel out and tell the backdrop to pick up a little speed
// (tacit:launch, answered by WARP in assets/backdrop.js) before handing the
// browser to the identity provider, so the click departs rather than stalls on a
// still page. The navigation is deferred only by the length of the fade and fires
// from a timer regardless, so neither a missing backdrop nor a dropped frame can
// strand the visitor here. Modified clicks (new tab/window) and reduced motion
// take the plain link.
//
// 340ms is the whole of it: the panel is gone by 260 and the field has the rest.
// The departure has to be quick enough that nobody waits through it — a sign-in
// button that pauses is a sign-in button that feels broken — so the acceleration
// is tuned to reach its (modest) ceiling inside this window rather than the
// window being stretched to fit it.
//
// The field is optional. Without WebGL there is nothing to render, so the
// backdrop is never fetched — a probe context answers that in one line, where
// leaving it to the script itself means paying 40KB to be told no — and the
// canvas, never painted, stays transparent over the page. Nothing else about the
// front door changes; the panel still resolves in and the sign-in still departs
// on its fade, over the plain background. A visitor with JavaScript off reaches
// none of this and gets the same plain page.
//
// Loaded outside the reduced-motion guard below, because that reader is still
// shown the field — one still frame, no animation, which the backdrop draws and
// then stops.
var bg=document.getElementById('signin-bg'),canGL=false;
try{var probe=document.createElement('canvas');
canGL=!!(window.WebGLRenderingContext&&(probe.getContext('webgl')||probe.getContext('experimental-webgl')));}catch(err){}
if(canGL&&bg){var bjs=document.createElement('script');bjs.src='` + ui.BackdropURL() + `';
document.body.appendChild(bjs);}
var go=document.querySelector('.technique a.btn[href]'),technique=document.querySelector('.technique');
if(go&&technique&&!reduce){
go.addEventListener('click',function(e){
if(e.button!==0||e.metaKey||e.ctrlKey||e.shiftKey||e.altKey)return;
e.preventDefault();
technique.classList.add('launching');
try{window.dispatchEvent(new CustomEvent('tacit:launch'));}catch(err){}
setTimeout(function(){location.href=go.href;},340);
});
// Arriving is the same move played backwards: the panel eases in over the field,
// which is already running at its one steady speed. Removing .launching is what
// puts a restored panel back on screen — a page restored from the back/forward
// cache, Back from the identity provider most of all; the animation carries it
// rather than snapping it.
//
// The entry is timed off html.booting coming away — the moment the stylesheet
// lands and the body becomes visible (bootHideScript), which is the only moment
// the panel can actually be seen to arrive. Waiting for load instead would let a
// slow connection paint the panel first and then fade it in from nothing. The
// keyframes leave no fill behind, so a later launch fade starts from the panel's
// own resting style.
//
// It takes 1.1s and holds most of the fade until the second half (the easing is
// gentle early, unlike an ease-out, which spends ~90% of the opacity in its first
// quarter and lands as a pop). Coming up out of a blur, from slightly small, is
// the panel condensing out of the field rather than being switched on.
var arrive=function(){technique.classList.remove('launching');
technique.animate([{opacity:0,transform:'scale(.94)',filter:'blur(7px)'},
{opacity:1,transform:'none',filter:'blur(0px)'}],
{duration:1100,easing:'cubic-bezier(.5,0,.25,1)'});};
var root=document.documentElement;
if(root.classList.contains('booting')&&window.MutationObserver){
var mo=new MutationObserver(function(){if(!root.classList.contains('booting')){mo.disconnect();arrive();}});
mo.observe(root,{attributes:true,attributeFilter:['class']});
}else arrive();
// A back/forward-cache restore fires no load and touches no classes, so it needs
// its own call — this is the one that has a faded panel to bring back. A still-
// faded panel counts as a restore even if the browser doesn't say persisted: the
// worst case there is a blank front door, so trust the DOM over the flag. On a
// fresh load this fires with neither condition met and nothing plays twice.
addEventListener('pageshow',function(e){
if(e.persisted||technique.classList.contains('launching'))arrive();});
}
})();</script>`

// The backdrop's own documentation now lives at the top of assets/backdrop.js,
// where the code is. What matters here: the front door renders <canvas
// id="signin-bg">, the script finds it and takes over, and a document without
// that canvas (every page but the front door) loads nothing.
