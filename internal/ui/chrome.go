// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/opentacit/tacit/pkg/installaddr"
)

// The brand mark: the solid ◆ core framed by two unclosed chevrons — guillemets
// around knowledge nobody wrote down. The gaps at the top and bottom vertices
// are the point; never close them. In monospace surfaces (hook nudges, CLI
// output) the mark stays the plain ◆ character — this drawn form is HTML-only.
const MarkSVG = `<svg class="mark" width="1em" height="1em" viewBox="0 0 24 24" aria-hidden="true">` +
	`<path fill="currentColor" d="M12 7.4 16.6 12 12 16.6 7.4 12Z"/>` +
	`<path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" d="M9 5 2 12l7 7"/>` +
	`<path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" d="M15 5 22 12l-7 7"/></svg>`

// FaviconSVG is the mark as a standalone SVG document: accent blue, swapping
// shades with the browser's color scheme via the SVG's own media query. It is
// what /favicon.ico serves.
//
// A page declares its icon inline (FaviconLink below, the same drawing as a data
// URI) so a shell stays a single string with no asset route — but plenty of
// clients ask for /favicon.ico regardless of what a page declares, and every one
// of those asks was a 404 in the log. The two copies are one drawing in two
// encodings, and a test asserts they stay that way.
const FaviconSVG = `<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24'>` +
	`<style>.f{fill:#1f6feb}.s{fill:none;stroke:#1f6feb;stroke-width:2;stroke-linecap:round}` +
	`@media(prefers-color-scheme:dark){.f{fill:#58a6ff}.s{stroke:#58a6ff}}</style>` +
	`<path class='f' d='M12 7.4 16.6 12 12 16.6 7.4 12Z'/>` +
	`<path class='s' d='M9 5 2 12l7 7'/>` +
	`<path class='s' d='M15 5 22 12l-7 7'/></svg>`

// The same mark as a data-URI favicon: accent blue, swapping shades with the
// browser's color scheme via the SVG's own media query. Inline so a shell stays
// a single string with no asset route.
const FaviconLink = `<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,` +
	`%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20viewBox='0%200%2024%2024'%3E` +
	`%3Cstyle%3E.f%7Bfill:%231f6feb%7D.s%7Bfill:none;stroke:%231f6feb;stroke-width:2;stroke-linecap:round%7D` +
	`@media(prefers-color-scheme:dark)%7B.f%7Bfill:%2358a6ff%7D.s%7Bstroke:%2358a6ff%7D%7D%3C/style%3E` +
	`%3Cpath%20class='f'%20d='M12%207.4%2016.6%2012%2012%2016.6%207.4%2012Z'/%3E` +
	`%3Cpath%20class='s'%20d='M9%205%202%2012l7%207'/%3E` +
	`%3Cpath%20class='s'%20d='M15%205%2022%2012l-7%207'/%3E%3C/svg%3E">`

// Theme selection. The palette itself lives in assets/app.css and nowhere else.
// ThemeInitScript applies a saved choice before first paint so there's no flash
// of the wrong mode; the three glyphs drive the header toggle that cycles
// system → light → dark.
const ThemeInitScript = `<script>(function(){try{var t=localStorage.getItem('tacit-theme');` +
	`if(t==='dark'||t==='light')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();</script>`

const SunSVG = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>`
const MoonSVG = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"/></svg>`
const AutoSVG = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path fill="currentColor" stroke="none" d="M12 3v18a9 9 0 0 0 0-18Z"/></svg>`

// BootHideScript hides the page's CONTENT — never its background — until the
// stylesheet has applied. Two things take the class off again, and either alone
// is enough: the stylesheet's own load event, and a timer. The timer is what
// makes this safe — if the stylesheet 404s the page reveals itself unstyled
// after a beat rather than staying blank forever. And because the class is only
// ever added by script, a reader with JavaScript off never has anything hidden.
const BootHideScript = `<script>(function(){var d=document.documentElement;` +
	`d.classList.add('booting');` +
	`setTimeout(function(){d.classList.remove('booting');},1500);})();</script>`

// GroundCSS is the only style that ships inline, and it exists to stop a flash
// of the browser's own white page on the way to a dark one. An external
// stylesheet is render-blocking, so in theory nothing paints before it lands;
// WebKit does not always agree, and a full viewport of white on the way to
// #0a0d14 is the flash. Three tokens only: the board, the plate, and the text.
//
// These values are duplicated from assets/app.css, which is exactly the kind of
// copy that goes stale unnoticed. TestGroundCSSMatchesStylesheet fails the build
// if they drift.
const GroundCSS = `<style>:root{--plane:#eef1f6;--surface:#ffffff;--ink:#111823}` +
	`@media(prefers-color-scheme:dark){:root:not([data-theme="light"])` +
	`{--plane:#0a0d14;--surface:#101620;--ink:#c9d4e3}}` +
	`:root[data-theme="dark"]{--plane:#0a0d14;--surface:#101620;--ink:#c9d4e3}` +
	`html{background:var(--plane)}body{background:var(--plane);color:var(--ink)}` +
	`html.booting body{visibility:hidden}</style>`

// ThemeToggleScript wires a #theme-toggle button: cycle system → light → dark,
// persisted in localStorage. The pre-paint script already applied any saved
// choice; this syncs the button icon and label and lets clicks advance it.
const ThemeToggleScript = `<script>(function(){
var ICONS={system:'` + AutoSVG + `',light:'` + SunSVG + `',dark:'` + MoonSVG + `'};
var LABELS={system:'Theme: system',light:'Theme: light',dark:'Theme: dark'};
var ORDER=['system','light','dark'];
var btn=document.getElementById('theme-toggle');
function now(){var t=document.documentElement.getAttribute('data-theme');
  return t==='light'||t==='dark'?t:'system';}
function apply(m){
  if(m==='system'){document.documentElement.removeAttribute('data-theme');
    try{localStorage.removeItem('tacit-theme');}catch(e){}}
  else{document.documentElement.setAttribute('data-theme',m);
    try{localStorage.setItem('tacit-theme',m);}catch(e){}}
  if(btn){btn.innerHTML=ICONS[m];btn.title=LABELS[m];btn.setAttribute('aria-label',LABELS[m]);}}
if(btn){apply(now());btn.addEventListener('click',function(){
  apply(ORDER[(ORDER.indexOf(now())+1)%ORDER.length]);});}
})();</script>`

// RowLinkScript makes a table row that carries data-href open it — the house
// row idiom, and the reason app.css gives such a row a pointer cursor. Both
// consoles include it; a row that looks clickable in one and is dead in the
// other is the kind of split a shared chrome exists to prevent.
//
// Two mechanisms, because either alone is worse. The first cell's contents are
// promoted into a real <a class="row-link">, which is what gives the pointer
// (notably iPadOS's adaptive pointer), the keyboard and a screen reader
// something to act on, and what makes cmd/middle-click open a new tab. A
// delegated click on <main> then covers the rest of the row, where there is no
// anchor to hit. Clicks that land on a control of their own — a link, a button,
// a form field, a bulk-select checkbox — are left to it.
//
// Delegation rather than per-row handlers is deliberate: rows a page renders
// client-side after this pass has run still navigate, without re-running
// anything.
const RowLinkScript = `<script>(function(){
document.querySelectorAll('[data-href]').forEach(function(el){
 var href=el.getAttribute('data-href');
 if(!href)return;
 // Skip selection cells (bulk checkboxes): wrapping a checkbox in a link would
 // turn every tick into a navigation.
 var cell=el.querySelector('td:not(:has(input)),th:not(:has(input))');
 if(!cell||cell.querySelector('a'))return;   // a cell that already links is left alone
 var a=document.createElement('a');
 a.href=href; a.className='row-link';
 while(cell.firstChild)a.appendChild(cell.firstChild);
 cell.appendChild(a);
});
var main=document.querySelector('main');
if(main)main.addEventListener('click',function(e){
 // let links, row actions (e.g. Suspend), and bulk-select checkboxes act on their own
 if(e.target.closest('a,button,form,input,label'))return;
 var el=e.target.closest('[data-href]');
 if(el)window.location.href=el.getAttribute('data-href');});
})();</script>`

// FrontDoorCSS is the sign-in page's legibility floor: enough inline style that
// the page reads as a page when app.css never arrives.
//
// It exists because the front door is the one document a first-time visitor
// must be able to read, and it depended entirely on a 136 KB asset fetched over
// whatever network the visitor happens to be on. A blocked request, a stale
// cache entry or a dropped tunnel turned it into serif text and blue underlined
// links — which is what a stranger would have judged the product by.
//
// Two rules keep this from becoming a second design, and both are tested:
//
//  1. It declares NO colours. Everything visible comes from the three tokens
//     GroundCSS already carries, so there is no palette here to drift.
//  2. Every selector is at or below the specificity app.css uses for the same
//     element, so when the real stylesheet does arrive it wins on the cascade
//     rather than fighting this one.
//
// It is a floor, not a facsimile. The dashboard gets no equivalent: tables,
// panels and charts cannot be made legible by fifteen lines, and pretending
// otherwise would be a lie written in CSS.
const FrontDoorCSS = `<style>` +
	`body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;` +
	`line-height:1.55;padding:2.5rem 1.25rem}` +
	`.signin-bg,.site-bg,.theme-toggle,.copy-btn{display:none}` +
	// The door's own shape, so a stylesheet that never arrives costs the panel
	// its polish and not its position. Scoped to body.signin: the project page
	// carries this floor too, and turning ITS body into a centring flex box
	// would lay the whole document out in a row. Same specificity as app.css's
	// rule, which still wins — it is the later sheet.
	`body.signin{min-height:100dvh;display:flex;align-items:center;justify-content:center}` +
	`.technique{max-width:30rem;margin:0 auto}` +
	`h1{display:flex;align-items:center;gap:.5rem;font-size:1.6rem;margin:0 0 .6rem}` +
	`.signin-lede{margin:0 0 1.6rem}` +
	`a{color:inherit}` +
	`.btn{display:inline-block;padding:.55rem 1.1rem;text-decoration:none;font-weight:600;` +
	`border:1px solid color-mix(in srgb,var(--ink) 35%,transparent)}` +
	`.signin-links{margin:1.1rem 0}` +
	`.signin-setup{margin-top:1.6rem}` +
	`summary{cursor:pointer;font-weight:600}` +
	`h3{font-size:.95rem;margin:1.1rem 0 .35rem}` +
	`pre{background:var(--surface);padding:.6rem .7rem;overflow-x:auto;font-size:.85em;` +
	`border:1px solid color-mix(in srgb,var(--ink) 20%,transparent)}` +
	`</style>`

// GuardProperty is the custom property the stylesheet guard probes for. It must
// be one app.css defines on :root and GroundCSS does not, or the guard cannot
// tell "the stylesheet applied" from "only the inline ground did".
const GuardProperty = "--ring"

// Where install.sh answers, and the one line a first-time reader is given to run.
// These are here rather than beside a page because several surfaces quote them —
// the console's member panel, the project page's hero, the README and the user
// guide — and a version of this line that disagrees with another is a reader
// typing something that does not work.
//
// Two addresses, and the difference matters:
//
//   - InstallURL is what a person types. It is on the project's own zone because
//     a command somebody reads off a screen and retypes should be short, and
//     because the address survives the repository moving, being renamed, or the
//     script one day being served rather than redirected to.
//   - InstallScriptURL is where the bytes are, and it is what InstallURL
//     redirects to (internal/ingress/site.go). Nothing quotes it at a reader.
//
// The redirect is why the command still carries -L, and why -L was never
// optional: raw.githubusercontent.com redirects on its own account too.
//
// The README, the guide and the script's own header are prose in a repository and
// cannot import a constant; they are held to this one by
// TestInstallCommandMatchesEveryPlaceThatQuotesIt.
//
// The addresses themselves live in pkg/installaddr, because the short one is a
// redirect on the project's DNS zone and whatever configures that zone is not
// in this module. These names stay: everything in the registry quotes them.
const (
	InstallURL       = installaddr.URL
	InstallScriptURL = installaddr.ScriptURL
	InstallCommand   = installaddr.Command
)

// InstallURLFor and InstallCommandFor are those two addresses as quoted to a
// reader on a particular host. An ingress answering for more than one zone
// serves /install.sh at every apex it carries, so somebody reading the page at
// one domain should be handed the address in front of them: a command retyped
// off a screen has to work, and one naming a domain the reader never visited
// invites the question of whether they are in the right place at all.
//
// An empty host means the canonical address. The constants above stay canonical
// on purpose — the README, the user guide and the script's own header are prose
// in a repository with no request to follow, and they are held to the constant
// by TestInstallCommandMatchesEveryPlaceThatQuotesIt.
func InstallURLFor(host string) string {
	if host == "" {
		return InstallURL
	}
	return InstallURLAt("https://" + host)
}

// InstallCommandFor is the line a first-time reader on host is given to run.
func InstallCommandFor(host string) string { return InstallCommandAt(hostBase(host)) }

// InstallURLAt and InstallCommandAt are the same, for a caller that already has
// a base URL rather than a bare hostname — a registry serving its own installer
// knows its scheme and any base path, and neither is guessable from the host.
func InstallURLAt(base string) string {
	if base == "" {
		return InstallURL
	}
	return strings.TrimSuffix(base, "/") + "/install.sh"
}

func InstallCommandAt(base string) string {
	if base == "" {
		return InstallCommand
	}
	return "curl -fsSL " + InstallURLAt(base) + " | sh"
}

func hostBase(host string) string {
	if host == "" {
		return ""
	}
	return "https://" + host
}

// CopyIcon is the clipboard glyph on every copy control: two offset rounded
// squares, stroked in currentColor so it follows the theme it lands in.
const CopyIcon = `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">` +
	`<rect x="5.5" y="5.5" width="8.5" height="8.5" rx="1.5"/>` +
	`<path d="M10.5 5.5V3.5a2 2 0 0 0-2-2h-5a2 2 0 0 0-2 2v5a2 2 0 0 0 2 2h2"/></svg>`

// InfoIcon is the circled i on the hero's install window: the window opens the
// guide, and this says there is something behind it to open. Drawn to the same
// recipe as CopyIcon — 16-unit box, stroked in currentColor, no fill — so the two
// sit at the same weight wherever one replaces the other.
//
// It is never a control. The window around it is the target and the green light
// is the control; a third thing to press, wearing the same job, would only split
// the gesture.
const InfoIcon = `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">` +
	`<circle cx="8" cy="8" r="6.3"/><path d="M8 7.3v4" stroke-linecap="round"/>` +
	`<path d="M8 4.8v.6" stroke-linecap="round"/></svg>`

// CopyButtonJS wires every button[data-copy] on a page: the click copies the
// payload and the .copied class runs the acknowledgement the stylesheet draws. It
// is a fragment rather than a whole <script> because the console splices it into
// its one script block; the project page includes it as its own.
//
// How long the bubble lasts is the stylesheet's business, not this file's, so the
// class comes off on animationend rather than on a timer that would be the same
// number written in two places and true in one. The timer that remains is a
// backstop for a browser that does not report a pseudo-element's animation: it is
// deliberately longer than any plausible acknowledgement, because its job is to
// let the NEXT click work, not to time this one.
//
// Re-arming matters more than it looks: without removing the class, a second copy
// of the same button gives no feedback at all, and the reader is left wondering
// whether the click registered.
//
// The Clipboard API needs a secure context (https or localhost). Where it is
// absent the button would be a control that silently does nothing, so it hides
// itself and leaves the text — which is selectable either way.
const CopyButtonJS = `
document.querySelectorAll('button[data-copy]').forEach(function(btn){
  if(!navigator.clipboard){btn.style.display='none';return;}
  var clear=function(){btn.classList.remove('copied');};
  btn.addEventListener('animationend',clear);
  btn.addEventListener('click',function(){
    navigator.clipboard.writeText(btn.dataset.copy).then(function(){
      btn.classList.remove('copied');
      void btn.offsetWidth;          // restart the animation on a repeated click
      btn.classList.add('copied');
      setTimeout(clear,6000);
    });
  });
});
`

// Wordmark is the front door's brand text, wrapped so the fit pass has a box to
// measure. Both front doors — the registry's and a console's — build their <h1>
// with it, because the CSS and the script that size the pair are shared and a
// bare text node would leave one of them unmeasurable.
func Wordmark(name string) string { return `<span class="wordmark">` + name + `</span>` }

// WordmarkLen is the name's length for the h1's --name-len, which is how the
// stylesheet sizes the wordmark when the fit script does not run. Runes, not
// bytes: the divisor counts characters, and an accented name is not a third
// wider for having been encoded in UTF-8.
func WordmarkLen(name string) string { return strconv.Itoa(utf8.RuneCountInString(name)) }

// WordmarkAttr marks an element as a wordmark lockup for the fit pass, and
// carries its design ceiling in rem — the size it takes when the name is short
// enough to leave it alone. In rem rather than a custom property because the
// script has to parse it, and a computed "3.5rem" would have to be resolved
// against the root font size by hand.
func WordmarkAttr(maxRem string) string { return ` data-wordmark="` + maxRem + `"` }

// WordmarkFitScript sizes a wordmark to the name it actually has.
//
// The name is a deployment setting (PRODUCT_NAME), so the size cannot be chosen
// once: the 3.5rem that framed a five-letter brand overflowed the card at every
// width the moment the name reached nine letters. A per-character estimate in CSS
// was the alternative and a poor one — in this face an ordinary name runs from
// .385em to .67em per character, so any constant safe for the widest name would
// shrink the narrowest by a third for nothing.
//
// So it measures instead: start at the ceiling the design wants (data-wordmark,
// in rem) and shrink by what the line actually overflows by until it fits. Reading the overflow rather
// than modelling it from the parts is what makes this exact — glyph advances do
// not scale perfectly linearly with font-size, and a single ratio computed from
// the parts landed several pixels wide. Short names never enter the loop: at the
// ceiling they do not overflow, so they keep the size the design chose.
//
// Every [data-wordmark] on the page is fitted, so the front door's heading and
// the user guide's masthead — the same lockup at two sizes — share one pass.
//
// Measured after the webfont resolves, because the fallback's metrics belong to a
// font that is about to be replaced. Both passes run while html.booting still
// hides the page, so no reader watches the wordmark resize. Without JavaScript
// the CSS floor stands, which is why that floor is conservative.
const WordmarkFitScript = `<script>(function(){
var root=parseFloat(getComputedStyle(document.documentElement).fontSize)||16;
var lockups=[];
document.querySelectorAll('[data-wordmark]').forEach(function(el){
 var text=el.querySelector('.wordmark');
 if(!text)return;
 lockups.push({el:el,text:text,mark:el.querySelector('svg'),
   ceiling:(parseFloat(el.getAttribute('data-wordmark'))||3.5)*root});
});
if(!lockups.length)return;
// A lockup is measured from its parts, not from its scrollWidth. It centres its
// content, and a centred overflow spills both ways while scrollWidth sees only
// the right-hand half — it reports half the excess, and never less than
// clientWidth, so a loop aiming at it converges on the wrong number or not at
// all. The mark, the gap and the text add up to what the line actually wants.
var taken=function(w){
 var cs=getComputedStyle(w.el);
 return (w.mark?w.mark.getBoundingClientRect().width:0)+
   (parseFloat(cs.columnGap)||0)+w.text.getBoundingClientRect().width;
};
var fit=function(){
 lockups.forEach(function(w){
  var size=w.ceiling;
  w.el.style.fontSize=size+'px';
  // Two passes reach it; the rest are headroom. One would not: glyph advances do
  // not scale perfectly linearly with font-size, so the first correction lands a
  // few pixels out and the second closes it.
  for(var i=0;i<4;i++){
   // A pixel of headroom: the correction is not exact, and landing a hair over is
   // the one outcome worth spending a pixel to avoid.
   var avail=w.el.clientWidth-1,want=taken(w);
   if(!(avail>0&&want>avail))break;
   size=size*avail/want;
   w.el.style.fontSize=size+'px';
  }
 });
};
fit();
try{if(document.fonts&&document.fonts.ready)document.fonts.ready.then(fit);}catch(e){}
addEventListener('resize',fit);
})();</script>`

// StylesheetGuardScript recovers a page whose stylesheet did not apply.
//
// The onload handler on the <link> covers a stylesheet that arrives. Nothing
// covered one that does not, and the failure has more shapes than a missing
// file: a content blocker dropping the subresource, a proxy returning an error,
// a truncated body, or — the case that prompted this — a bad response cached
// under a URL served `+"`immutable`"+` for a year, which the browser will not re-request
// and which no server-side change can dislodge.
//
// So rather than trying to name the failure, this checks the outcome: read a
// custom property only the stylesheet defines, and if it is missing, fetch the
// stylesheet again with a cache-busting parameter. Once per session, so a
// genuinely absent stylesheet costs one extra request rather than a loop.
const StylesheetGuardScript = `<script>(function(){
setTimeout(function(){
 var root=document.documentElement;
 try{if(getComputedStyle(root).getPropertyValue('` + GuardProperty + `').trim())return;}catch(e){return;}
 try{
  if(sessionStorage.getItem('tacit-css-retry'))return;
  sessionStorage.setItem('tacit-css-retry','1');
 }catch(e){}
 var link=document.querySelector('link[rel="stylesheet"]');
 if(!link)return;
 var url=link.href.split('#')[0];
 var retry=document.createElement('link');
 retry.rel='stylesheet';
 retry.href=url+(url.indexOf('?')<0?'?':'&')+'retry='+Date.now();
 retry.onload=function(){root.classList.remove('booting');};
 document.head.appendChild(retry);
},1800);
})();</script>`
