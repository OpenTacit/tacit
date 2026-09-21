// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"unicode"
)

// Account renders the signed-in member in a top bar: their identity provider's
// profile picture as an avatar button that opens a menu naming them and
// offering a way out.
//
// Identity costs one 32px circle rather than a line of text. An email address
// in the bar is both wider than anything else there and duplicated inside the
// menu, and on a narrow window it is the thing that wraps.
//
// Providers that return no picture — or a picture that fails to load, which the
// shell's script drops — fall back to the initials underneath. With no session
// it is a plain Sign in link.
//
// menuItems is raw markup for any destinations above the separator; the
// identity block and Sign out are always added.
func Account(user map[string]any, oidcOn bool, menuItems string) string {
	if oidcOn && user == nil {
		return `<a class="signin" href="/auth/login">Sign in</a>`
	}
	var b strings.Builder
	if user == nil {
		// No identity provider: nothing to show, but the menu still needs a
		// home, so a generic button carries the same destinations.
		b.WriteString(`<button id="account-btn" class="avatar-btn" type="button" aria-haspopup="menu" ` +
			`aria-expanded="false" aria-controls="account-menu" title="Menu" aria-label="Menu">` +
			`<span class="avatar-initials" aria-hidden="true">≡</span></button>` +
			`<div id="account-menu" class="account-menu" role="menu" hidden>` +
			menuItems + `</div>`)
		return b.String()
	}

	name := fmt.Sprint(firstNonEmpty(user["name"], user["email"], user["sub"], "Account"))
	email := fmt.Sprint(firstNonEmpty(user["email"], ""))
	fmt.Fprintf(&b, `<button id="account-btn" class="avatar-btn" type="button" aria-haspopup="menu" `+
		`aria-expanded="false" aria-controls="account-menu" title="%s" aria-label="Account: %s">`,
		html.EscapeString(name), html.EscapeString(name))
	fmt.Fprintf(&b, `<span class="avatar-initials" aria-hidden="true">%s</span>`, html.EscapeString(Initials(name)))
	if pic := avatarURL(user["picture"]); pic != "" {
		fmt.Fprintf(&b, `<img class="avatar-img" src="%s" alt="" referrerpolicy="no-referrer">`, html.EscapeString(pic))
	}
	b.WriteString(`</button><div id="account-menu" class="account-menu" role="menu" hidden>`)
	fmt.Fprintf(&b, `<div class="account-who"><span class="who-name">%s</span>`, html.EscapeString(name))
	if email != "" && email != name {
		fmt.Fprintf(&b, `<span class="who-email">%s</span>`, html.EscapeString(email))
	}
	b.WriteString(`</div>` + menuItems)
	if menuItems != "" {
		b.WriteString(`<hr class="account-sep">`)
	}
	b.WriteString(`<a class="account-item" role="menuitem" href="/auth/logout">Sign out</a></div>`)
	return b.String()
}

// avatarURL accepts only an absolute http(s) URL: the claim comes from an
// identity provider and lands in a src attribute, so a javascript: or data:
// value has no business being rendered.
func avatarURL(claim any) string {
	pic, _ := claim.(string)
	u, err := url.Parse(pic)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return pic
}

// Initials is the fallback inside the avatar circle: up to two letters from the
// name, or from the local part of an email when that is all there is.
func Initials(name string) string {
	if at := strings.IndexByte(name, '@'); at > 0 {
		name = name[:at]
	}
	var out []rune
	for _, word := range strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out = append(out, unicode.ToUpper([]rune(word)[0]))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// firstNonEmpty takes ...any, not ...string, because its callers pass raw
// claim lookups (user["name"] and friends are any); a value is "empty" when it
// is nil or the empty string, and any other non-nil value passes through for
// fmt.Sprint to render.
func firstNonEmpty(vals ...any) any {
	for _, v := range vals {
		if v != nil && v != "" {
			return v
		}
	}
	return ""
}

// MenuScript keeps one overlay menu open at a time, and dismisses the open one
// on a click outside it or on Escape.
//
// The breadcrumb carries two dropdowns now — the destination and the level
// under it — so a second popover opening over the first was suddenly reachable
// in one move, and two popovers on one line are unreadable. The top bar's menus
// already closed each other, through a registry private to the script that
// built them; this is that behaviour for every overlay in the chrome, in one
// place, so the next menu inherits it rather than needing a third mechanism.
//
// Three shapes, because the chrome has three. A native <details> holds the
// breadcrumb levels and the filter menus. A button beside a hidden panel holds
// the account and demo menus, and those are reached through the aria-controls
// that already points one at the other rather than through a list of ids kept
// here. And a checkbox with a label holds the phone's nav, which is built that
// way so it works with no script at all.
//
// `toggle` does not bubble, so the listener for the first shape is on the
// CAPTURE phase — and so is the outside-click one, because a control that stops
// propagation on its own button would otherwise hide that click from this.
//
// What is deliberately NOT in the set: a <details class="fineprint">
// disclosure. It expands inline rather than over the page, so it overlaps
// nothing — and closing somebody's expanded fine print because they opened a
// nav menu would lose them their place. Clicking one still dismisses an open
// menu, because that click is outside it.
const MenuScript = `<script>(function(){
var POPS='details.crumb-menu,details.fmenu',PANELS='.account-menu';
function close(except){
  document.querySelectorAll(POPS).forEach(function(d){
    if(d!==except&&d.open)d.open=false;
  });
  document.querySelectorAll(PANELS).forEach(function(m){
    if(m===except||m.hidden)return;
    m.hidden=true;
    var btn=m.id?document.querySelector('[aria-controls="'+m.id+'"]'):null;
    if(btn)btn.setAttribute('aria-expanded','false');
  });
  var nav=document.getElementById('nav-toggle');
  if(nav&&nav!==except&&nav.checked)nav.checked=false;
}
window.tacitCloseMenus=close;
document.addEventListener('toggle',function(e){
  var d=e.target;
  if(d&&d.open&&d.matches&&d.matches(POPS))close(d);
},true);
document.addEventListener('click',function(e){
  var t=e.target;
  if(!t||!t.closest)return;
  // Inside a menu, on the summary that opens one, on the button that does, or
  // on the burger and the checkbox behind it: those clicks are the menu's own
  // business.
  //
  // Both halves of the burger, and this is the one that is easy to miss:
  // activating a <label for> makes the browser forward a SECOND click to the
  // control itself. Exempting only the label left that forwarded click looking
  // like a click on the page, so opening the phone's nav closed it again in the
  // same gesture.
  if(t.closest(POPS)||t.closest(PANELS)||t.closest('[aria-controls]')||
     t.closest('.nav-burger')||t.closest('.nav-toggle')||t.closest('.bar-collapse'))return;
  close(null);
},true);
document.addEventListener('keydown',function(e){
  if(e.key==='Escape')close(null);
});
// The third shape: the phone's nav, which is a checkbox and a label because it
// has to work with no script at all. It overlays the breadcrumb line, so it
// belongs in the set both ways.
var nav=document.getElementById('nav-toggle');
if(nav)nav.addEventListener('change',function(){if(nav.checked)close(nav);});
})();</script>`

// AccountMenuScript opens and closes the avatar menu, and drops a profile
// picture that fails to load so the initials underneath show through — an
// identity provider's image URL can expire or be blocked, and a broken-image
// glyph in the top bar is worse than initials.
const AccountMenuScript = `<script>(function(){
var btn=document.getElementById('account-btn'),menu=document.getElementById('account-menu');
if(btn&&menu){
  var setOpen=function(open){menu.hidden=!open;btn.setAttribute('aria-expanded',open?'true':'false');};
  btn.addEventListener('click',function(e){e.stopPropagation();setOpen(menu.hidden);});
  document.addEventListener('click',function(e){if(!menu.hidden&&!menu.contains(e.target))setOpen(false);});
  document.addEventListener('keydown',function(e){
    if(e.key==='Escape'&&!menu.hidden){setOpen(false);btn.focus();}});
}
document.querySelectorAll('.avatar-img').forEach(function(img){
  img.addEventListener('error',function(){img.remove();});
});
})();</script>`
