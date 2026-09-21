// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Turning sign-in on from the dashboard.
//
// WHY THIS IS HERE AT ALL — `tacit secure` was the only way in, on the
// reasoning that a registry with no sign-in has no administrators, so its
// Settings page cannot be trusted with the switch. That reasoning holds for an
// OPEN registry and only for an open registry. A single-owner registry does
// have an administrator: the owner, who proved they could read the console this
// registry was started from (the owner link, `tacit dashboard`) — the same
// proof the first-run claim code asks for. Making that person go back to a
// terminal to type what the dashboard could ask them was a gap in the one way
// in, not a security property.
//
// So the control is offered in exactly one state: owner sign-in on, no identity
// provider yet, and the session is the owner's. An open registry has no owner
// session to offer it to, and a registry that already has a provider has
// nothing here to do.
//
// IT IS THE SAME SWITCH AS GLOBAL ACCESS, one row above it: two named positions
// over one checkbox, Personal or Shared. That is not decoration. Both are the
// same kind of question — who is this registry for — and the page already
// answered one of them with a control that names both answers rather than a
// box whose unticked state the operator has to infer. Choosing Shared reveals
// what Shared needs, the way Global Access reveals its address, and the page's
// one Save commits it: a wizard on its own URL made turning sign-in on a
// different kind of act from every other setting on the page, which it is not.
//
// WHAT IT DOES THAT A FORM COULD NOT — the same four things `tacit secure`
// does, because they are what make the difference between a config file and a
// working sign-in:
//
//	· derives the callback URL from what the registry already knows, so the
//	  value that has to match character-for-character at the provider is not
//	  typed twice;
//	· proves the issuer by fetching its discovery document BEFORE writing
//	  anything — a wrong issuer is otherwise discovered at the first sign-in,
//	  with the dashboard already closed;
//	· writes the four settings as a SET, plus the mode, the session secret
//	  nobody remembers and the secure-cookie flag an https callback implies —
//	  registry.env accepts three of four in silence and leaves the dashboard
//	  open;
//	· restarts the registry, because sign-in is read at startup. A save that
//	  wrote the settings and left the dashboard open would be the lie the
//	  Settings page refuses to tell — so this is the one save that answers with
//	  a page instead of a redirect, and then goes away.
//
// THE WAY BACK stays where it was: `tacit secure --off`, on this machine's
// console. This page can close the door and cannot reopen it — after the
// restart the provider is the only way in, and a wrong client secret would
// otherwise lock the operator out of the registry that holds the fix.
package web

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/servicectl"
)

// defaultScopes is the oidc package's, named here because this page writes it:
// the scopes a provider is asked for and the value the Scopes field defaults to.
const defaultScopes = oidc.DefaultScopes

// signInRestartDelay lets the success page reach the browser before the process
// it was rendered by goes away. The restart kills this process, so the response
// must be flushed first; a second is enough for a local write and long enough
// that nothing races.
var signInRestartDelay = time.Second

// canConfigureSignIn reports whether this session may turn sign-in on here.
//
// All three conditions are load-bearing. Owner mode is the one arrangement with
// an administrator but no provider. s.OIDC == nil keeps the page from
// re-securing a registry that is already secured (that is `tacit secure`'s job,
// from the console, where a locked-out operator can still reach it). isAdmin is
// the owner session itself.
func (s *Server) canConfigureSignIn(user oidc.Claims) bool {
	return s.OIDC == nil && s.cfg().OwnerEnabled() && s.isAdmin(user)
}

// signInWritten reports settings that are in the file but not in force: the
// state between writing and the restart. The page says so out loud, because a
// registry whose dashboard is still open while its settings say sign-in is on
// is the one state an operator must not have to guess at.
func (s *Server) signInWritten() bool {
	if s.OIDC != nil {
		return false
	}
	vals := config.ReadEnv(config.RegistryEnvPath())
	for _, k := range []string{"TACIT_OIDC_ISSUER", "TACIT_OIDC_CLIENT_ID",
		"TACIT_OIDC_CLIENT_SECRET", "TACIT_OIDC_REDIRECT_URI"} {
		if strings.TrimSpace(vals[k]) == "" {
			return false
		}
	}
	return true
}

// missingEnvFile reports the one setup this page cannot serve: a registry with
// no settings file, configured from its environment. PatchEnv would fail with a
// write error, which reads as a broken page rather than the true answer — that
// these settings belong wherever the rest of this registry's do.
func missingEnvFile() string {
	path := config.RegistryEnvPath()
	if path == "" {
		return "This process has no home directory, so there is no settings file to write. " +
			"Set TACIT_REGISTRY_ENV, or configure sign-in in the environment."
	}
	if _, err := os.Stat(path); err != nil {
		return "This registry has no settings file at " + path + "—it is configured from its " +
			"environment, so the four TACIT_OIDC_* settings belong there too."
	}
	return ""
}

// signInConfigurable reports the one state this control can act in. Given
// admin, it is canConfigureSignIn without the claims — the settings view has
// already resolved them.
func (s *Server) signInConfigurable(admin bool) bool {
	return admin && s.OIDC == nil && s.cfg().OwnerEnabled()
}

// signInRows are the Sign-in rows of the Access tab: the switch, and everything
// the Shared position needs, revealed with it.
//
// Every row here is inside the page's ONE form and stays in the DOM in both
// positions, hidden rather than removed — the same contract the rest of the page
// keeps, and the reason a save from the Personal position does not clear the
// administrators list.
func (s *Server) signInRows(r *http.Request, admin bool, out settingsOutcome) string {
	if !admin {
		return setFact("Administrators", orDash(strings.Join(s.cfg().AdminEmails, ", ")), "") +
			setFact("Sign-in", oidcLabel(s.Cfg), restartBadge)
	}
	configurable := s.signInConfigurable(admin)
	inForce := s.OIDC != nil
	pending := s.signInWritten()
	// The switch follows the POST on a re-render, so an operator who moved it and
	// then mistyped an issuer gets their answer back with the error, not the
	// position they had before they started.
	shared := inForce || pending || (configurable && posted(r, "signin_shared") != "")
	callback, addrProblem := s.signInAddress()

	var b strings.Builder
	// Locked wherever this page cannot move it: once sign-in is in force (the
	// console's to undo), and while it is written and waiting for the restart
	// that brings it into force — a switch offering a move that does nothing is
	// worse than one that reports the position.
	b.WriteString(setSwitch("signin_shared", "Shared", "Personal", shared, !configurable || pending,
		signInSharedSlot(s.Cfg, inForce, pending), signInPersonalSlot()))

	reveal := func(row string) { b.WriteString(onlyWhenSwitch("signin_shared", row, shared)) }
	switch {
	case inForce:
		reveal(setFact("Identity provider", s.cfg().OIDCIssuer, restartBadge))
		reveal(setFact("Callback", orDash(s.OIDCRedirectURI()), ""))
		reveal(setNote("Change or remove the provider from this machine’s console: " +
			"<code>tacit secure</code>."))
	case pending:
		file := config.ReadEnv(config.RegistryEnvPath())
		reveal(setFact("Identity provider", file["TACIT_OIDC_ISSUER"], restartBadge))
		reveal(setFact("Callback", file["TACIT_OIDC_REDIRECT_URI"], ""))
		reveal(setWarn("Written, not in force: sign-in is read at startup, so the dashboard stays open " +
			"until this restarts. <code>systemctl --user restart tacit-registry.service</code>"))
	default:
		b.WriteString(s.signInSetupRows(r, shared, callback, addrProblem))
	}
	// The administrators list belongs to the Shared position — with no provider
	// there is nobody for it to name — but it is written by the same save either
	// way, so it is hidden here rather than dropped.
	reveal(s.adminEmailRows(r))
	// No bargain where there is no bargain to strike: with no address, Save has
	// nothing to write, and a box describing what it would do is a promise the
	// button cannot keep.
	if !inForce && !pending && addrProblem == "" {
		reveal(signInBargain())
		// Where the test's answer lands — filled by the script, or by the page
		// itself when there is no script to fill it. Always present, so the
		// script has somewhere to write without building the container first.
		reveal(`<div class="set-row set-wide" id="signin-checks">` + out.checks + `</div>`)
	}
	return b.String()
}

// adminEmailRows is the administrators list: one address to a row, each with
// its own Remove.
//
// It used to be a single box holding "you@example.com, lead@example.com", and a
// list punctuated inside a text field is a format the operator has to guess at
// — the placeholder was the only place the comma was ever explained, and a
// placeholder disappears the moment anything is typed. One field per address
// needs no explaining, and it makes removing the colleague who left a click
// rather than an exercise in comma surgery.
//
// The field is type="text", not type="email", deliberately. These rows are
// hidden while the switch says Personal, and a browser refuses to submit a form
// containing an invalid control it cannot scroll to — one malformed address on
// the hidden half of this page would make the whole Settings page unsavable
// with no visible error. The handler validates instead, and says which address
// it means.
//
// There is always one blank row at the end, so a list can be added to with no
// script at all; with one, filling the last row grows another.
func (s *Server) adminEmailRows(r *http.Request) string {
	emails := s.cfg().AdminEmails
	if posted, ok := postedEmails(r, "admin_emails"); ok {
		emails = posted
	}
	// Your own row is marked, and that mark is what the script reads to leave its
	// ✕ dead. It comes from the session against the list IN FORCE, the same
	// question the handler refuses on — not from the rows on screen, which the
	// operator may be halfway through editing.
	self := ""
	if s.namedAdmin(s.sessionUser(r)) {
		self = claimEmail(s.sessionUser(r))
	}
	var b strings.Builder
	b.WriteString(`<div class="set-row set-wide set-emails">` +
		`<span class="set-label">Administrators</span><div class="set-emails-list">`)
	for _, e := range emails {
		b.WriteString(adminEmailRow(e, self != "" && strings.EqualFold(strings.TrimSpace(e), self)))
	}
	b.WriteString(adminEmailRow("", false))
	b.WriteString(`</div></div>`)
	return b.String()
}

// adminEmailRow is one address. Your own is marked, so the script can leave its
// Remove dead: this page removes anybody but you.
//
// The Remove button is rendered hidden and shown
// by the script, because without one it would be a button that does nothing —
// clearing the box is what removes an address when there is no script, and that
// is what Remove does with one.
func adminEmailRow(email string, self bool) string {
	mine := ""
	if self {
		mine = ` data-self="1"`
	}
	return fmt.Sprintf(`<div class="set-email"%s>`+
		`<input name="admin_emails" type="text" value="%s" placeholder="you@example.com" `+
		`autocomplete="off" spellcheck="false" aria-label="Administrator email">`+
		`<button type="button" class="set-email-del" hidden `+
		`aria-label="Remove this administrator" title="Remove">&#10005;</button></div>`,
		mine, html.EscapeString(email))
}

// postedEmails reads the administrators as the form now posts them: one value
// per row. It still splits on commas, so a value typed the old way — or posted
// by an older page — becomes rows rather than one row holding a list.
//
// The bool is "the field came back at all". Empty is a real answer (every
// administrator removed) and must not re-render the stored list.
func postedEmails(r *http.Request, name string) ([]string, bool) {
	if r == nil || r.PostForm == nil {
		return nil, false
	}
	vals, ok := r.PostForm[name]
	if !ok {
		return nil, false
	}
	return splitEmailList(vals), true
}

// splitEmailList is the one reading of the administrators field, used by the
// page that renders it and the handler that writes it: comma or row, trimmed,
// blanks dropped, no address twice.
func splitEmailList(vals []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range vals {
		for _, e := range strings.Split(v, ",") {
			e = strings.TrimSpace(e)
			if e == "" || seen[strings.ToLower(e)] {
				continue
			}
			seen[strings.ToLower(e)] = true
			out = append(out, e)
		}
	}
	return out
}

// adminEmailsScript is what the list gains from a script: Remove, and a row
// that appears when the last one is filled.
//
// Remove CLEARS AND HIDES the row rather than removing it from the DOM, and
// that is the interesting part. The page's Save is dead until something differs
// from what was rendered (saveGuardScript), and it decides that by walking the
// form's current controls — so a removed row would take the evidence of its own
// removal with it, and deleting an administrator would leave Save greyed out.
// A cleared row still posts, posts "", and is dropped on the way in.
//
// It also refuses one removal: your own. Nobody demotes themselves here,
// whoever else is named — the page that decides who administers this registry
// is not a place to discover you have just signed out of it, and removing
// everyone is worse than a lockout (every member who signs in becomes an
// administrator). Removing an administrator is somebody else's act: another
// administrator, or registry.env on the console. The handler refuses the same
// edit however it arrives (a cleared box, a page with no script, a hand-written
// post); this is what stops it being offered.
const adminEmailsScript = `<script>(function(){
var list=document.querySelector('.set-emails-list');
if(!list)return;
function rows(){return list.querySelectorAll('.set-email');}
function blank(){
  var row=list.lastElementChild.cloneNode(true);
  var box=row.querySelector('input');
  box.value='';box.defaultValue='';row.hidden=false;
  list.appendChild(row);
  wire(row);
  return row;
}
function grow(){
  var all=rows(),last=null;
  for(var i=0;i<all.length;i++){if(!all[i].hidden)last=all[i];}
  if(last&&last.querySelector('input').value.trim()!=='')blank();
}
function guard(){
  var self=list.querySelector('.set-email[data-self] .set-email-del');
  if(!self)return;
  self.disabled=true;
  self.title='You cannot remove yourself';
}
function wire(row){
  var box=row.querySelector('input'),del=row.querySelector('.set-email-del');
  if(del){
    del.hidden=false;
    del.addEventListener('click',function(){
      if(del.disabled)return;
      box.value='';row.hidden=true;
      box.dispatchEvent(new Event('input',{bubbles:true}));
      grow();guard();
    });
  }
  box.addEventListener('input',function(){grow();guard();});
}
Array.prototype.forEach.call(rows(),wire);
guard();
})();</script>`

// signInAddress is the callback to REGISTER and to store, or the reason there is
// none yet.
//
// The reason it is not simply config.CallbackURL() is what that function falls
// back to when a registry has no address of its own: http://localhost:<port>,
// which is a bind address wearing a hostname. Offered here as "the redirect URI
// to register", it is worse than nothing — a provider that accepts it (most
// accept loopback) returns members to their OWN machine, so sign-in works for
// nobody except someone sitting at the registry's console, and the operator
// finds that out after registering a client. Shared means colleagues; a callback
// only the server itself can reach cannot serve them.
//
// So this asks the question the page is actually asking: is there an address a
// provider could return a member to? Three answers, and the third is not a
// callback at all — it is the setting to go and change first, one row above.
//
// The published address counts, and has to: Global Access exists so that a
// registry with no domain of its own gets a public https address, which is
// exactly the operator who cannot answer this question any other way. It is
// second, though — an operator's own address survives the switch being turned
// off, and the proxy's does not.
func (s *Server) signInAddress() (callback, problem string) {
	c := s.cfg()
	own := c.ExternalBase()
	switch {
	case strings.HasPrefix(own, "https://"):
		return own + "/auth/callback", ""
	case own != "":
		// An address IS set and cannot carry a callback. The reason is about
		// that address, so say it about that address.
		return "", oidc.CheckCallback(own + "/auth/callback")
	case strings.HasPrefix(s.PublishedBase(), "https://"):
		return s.PublishedBase() + c.BasePath + "/auth/callback", ""
	}
	// No invented URL here, not even as an example. The whole failure this
	// replaces was a plausible-looking address on a page that said "register
	// this" — so what is missing is named as a setting, never as a value to
	// paste.
	return "", fmt.Sprintf("A provider needs an address to return members to, and this registry knows "+
		"only its port (%d). Set Own address above, or turn Global Access on to be given one. "+
		"Save, then move this switch.", c.Port)
}

// signInSetupRows is what an operator has to do and provide, in the order they
// have to do it: register a callback at the provider, then paste the client back
// here. It is rendered even in the Personal position — hidden, ready for the
// switch — so choosing Shared costs no round trip.
func (s *Server) signInSetupRows(r *http.Request, shared bool, callback, addrProblem string) string {
	var b strings.Builder
	reveal := func(row string) { b.WriteString(onlyWhenSwitch("signin_shared", row, shared)) }

	if problem := missingEnvFile(); problem != "" {
		reveal(setWarn(html.EscapeString(problem)))
		return b.String()
	}
	// The callback is derived, not asked for: it is the value that has to match
	// character for character at the provider, and typing it twice is how it
	// comes not to. When there is nothing to derive it from, that is the whole
	// answer — the fields below would be work spent on a client that could never
	// sign anybody in, so they are not offered.
	if addrProblem != "" {
		reveal(setWarn(html.EscapeString(addrProblem)))
		return b.String()
	}
	reveal(setNote("At your provider, create a web client that keeps a secret, with these values:"))
	reveal(setFact("Redirect URI", callback, ""))
	// A published registry answers on an address that SUPERSEDES the stored one,
	// so the URI sign-in will send today is not the one about to be written. Both
	// belong at the provider: the live one for now, the stored one for when that
	// address goes away.
	if live := s.publishedCallback(); live != "" && !strings.EqualFold(live, callback) {
		reveal(setFact("And also", live, "live"))
		reveal(setNote("Sign-in redirects to the published address while Global Access is on. " +
			"Register both and the switch works either way."))
	} else if strings.EqualFold(callback, s.publishedCallback()) {
		// The only address this registry has is the one the proxy lends it. That
		// works, and it is worth knowing that it is on loan: turning Global
		// Access off later takes the callback with it.
		reveal(setNote("Global Access lends this address. Turn it off later and sign-in needs a new " +
			"callback: set Own address and register that one too."))
	}
	// No Scopes row here: the field below is the one that decides what the
	// registry asks for, and a fact repeating it would be a second answer to a
	// question with one.
	reveal(setFact("Client type", "web / server-side", ""))

	reveal(setText("signin_issuer", "Issuer", posted(r, "signin_issuer"),
		"https://accounts.google.com", true))
	reveal(setNote("The base URL serving <code>/.well-known/openid-configuration</code>. " +
		"Checked before anything is written."))
	reveal(setText("signin_client_id", "Client ID", posted(r, "signin_client_id"), "", true))
	reveal(setPassword("signin_client_secret", "Client secret", "the value your provider showed once"))
	reveal(setText("signin_scopes", "Scopes", orElse(posted(r, "signin_scopes"), defaultScopes),
		defaultScopes, false))

	return b.String()
}

// signInBargain is the consequence, at the point of deciding — not afterwards.
// The owner link going inert is the one that matters: it is the credential the
// operator is holding as they read it. It comes last, below every field it is
// about, because it describes what pressing Save will do with all of them.
func signInBargain() string {
	return `<div class="set-row set-wide set-bargain"><div class="set-confirm">` +
		`<p><b>Save writes <code>` + html.EscapeString(config.RegistryEnvPath()) +
		`</code> and restarts the registry.</b> Your provider becomes the only way in, and the owner ` +
		`link stops working. Sign in once with your own account before you tell anyone else.</p>` +
		`<p>If sign-in fails, run <code>tacit secure --off</code> on this machine to restore owner access.</p>` +
		`</div></div>`
}

// The two slots beside the switch: what each position MEANS, in the row that
// chooses between them. The Global Access switch puts the address there for the
// same reason — a position is easier to choose when its consequence is beside it.
func signInSharedSlot(cfg config.Config, inForce, pending bool) string {
	switch {
	case inForce:
		return `<span class="set-addr-wait">members sign in with ` +
			html.EscapeString(cfg.OIDCIssuer) + `</span>`
	case pending:
		return `<span class="set-addr-wait">configured; in force at the next restart</span>`
	}
	return `<span class="set-addr-wait">your identity provider, below</span>`
}

func signInPersonalSlot() string {
	return `<span class="set-addr-wait">one person: whoever holds this machine’s owner link</span>`
}

// posted is what the operator just sent, for a re-render that must not lose
// their typing. Empty on any GET, so it falls through to the stored value.
func posted(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.PostFormValue(name))
}

// ---- testing the provider before committing to it ---------------------------
//
// Save is not the first thing this switch does. Moving it to Shared turns the
// page's one button into **Test configuration**, and only a provider that
// answers every question correctly turns it back into Save changes.
//
// The reason is the shape of the mistake: everything here is typed from another
// system's console, and every way of getting it wrong produces the same symptom
// — sign-in fails for everybody, at a registry whose dashboard is now behind
// that sign-in. The console can undo it (`tacit secure --off`), but an operator
// who has to use it has already told colleagues to sign in. A test that costs
// two seconds and names the field to fix is worth a button press.
//
// The checks themselves are in the oidc package (Preflight), because they are
// about the provider rather than about this page.

// signInPreflight is what the operator has typed, ready to be proved.
func (s *Server) signInPreflight(r *http.Request) (oidc.Preflight, string) {
	callback, problem := s.signInAddress()
	if problem != "" {
		return oidc.Preflight{}, problem
	}
	get := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	if get("signin_issuer") == "" || get("signin_client_id") == "" || get("signin_client_secret") == "" {
		return oidc.Preflight{}, "Fill in the issuer, the client ID, and the client secret, then test again."
	}
	return oidc.Preflight{
		Issuer:       strings.TrimRight(get("signin_issuer"), "/"),
		ClientID:     get("signin_client_id"),
		ClientSecret: get("signin_client_secret"),
		RedirectURI:  callback,
		Scopes:       orElse(get("signin_scopes"), defaultScopes),
	}, ""
}

// checksHeader tells the script whether the button may become Save changes. It
// is a header rather than a body convention so the fragment stays what it is —
// the same HTML the no-script path embeds in the page.
const checksHeader = "X-Tacit-Checks"

// handleSignInTest runs the checks and answers with the results, as markup. It
// writes nothing: an operator can test a wrong secret as often as they like.
func (s *Server) handleSignInTest(w http.ResponseWriter, r *http.Request) {
	user := s.sessionUser(r)
	if !s.canConfigureSignIn(user) {
		http.Error(w, "only this registry’s owner can configure sign-in", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	if !s.checkCSRF(r) {
		http.Error(w, "stale form; reload the settings page and try again", http.StatusForbidden)
		return
	}
	pf, problem := s.signInPreflight(r)
	if problem != "" {
		w.Header().Set(checksHeader, "fail")
		s.sendHTML(w, 200, signInChecksHTML(nil, problem))
		return
	}
	checks := pf.Run()
	if oidc.OK(checks) {
		w.Header().Set(checksHeader, "pass")
	} else {
		w.Header().Set(checksHeader, "fail")
	}
	s.sendHTML(w, 200, signInChecksHTML(checks, ""))
}

// renderSignInTest answers a Test press that came as a form post — a browser
// with no script, or one where it failed to load. Same checks, same markup; the
// page comes back around them, and the button follows the result.
func (s *Server) renderSignInTest(w http.ResponseWriter, r *http.Request, user oidc.Claims) {
	out := settingsOutcome{}
	if pf, problem := s.signInPreflight(r); problem != "" {
		out.checks = signInChecksHTML(nil, problem)
	} else {
		checks := pf.Run()
		out.checks, out.checksPassed = signInChecksHTML(checks, ""), oidc.OK(checks)
	}
	s.sendHTML(w, 200, s.renderShell(page{active: "settings",
		crumbs: []crumb{{label: "Settings", href: ""}}, content: s.settingsView(r, user, out)}, user))
}

// signInChecksHTML renders the results: what was asked, what came back, and —
// where something failed — which box to change. Failures come first, because a
// list of green ticks with one red line in the middle buries the only line that
// needs reading.
func signInChecksHTML(checks []oidc.Check, problem string) string {
	var b strings.Builder
	b.WriteString(`<div class="set-checks">`)
	if problem != "" {
		b.WriteString(`<p class="set-checks-head bad">` + html.EscapeString(problem) + `</p></div>`)
		return b.String()
	}
	failed, warned := 0, 0
	for _, c := range checks {
		switch c.Status {
		case oidc.CheckFail:
			failed++
		case oidc.CheckWarn:
			warned++
		}
	}
	switch {
	case failed > 0:
		fmt.Fprintf(&b, `<p class="set-checks-head bad">%s. Change what is named below and test again.</p>`,
			countOf(failed, "check failed", "checks failed"))
	case warned > 0:
		fmt.Fprintf(&b, `<p class="set-checks-head ok">Everything the provider answered was correct, with %s `+
			`this test could not establish. Save changes applies it.</p>`, countOf(warned, "thing", "things"))
	default:
		b.WriteString(`<p class="set-checks-head ok">Every check passed. Save changes applies it.</p>`)
	}
	b.WriteString(`<ul>`)
	for _, c := range order(checks) {
		fmt.Fprintf(&b, `<li class="chk-%s"><span class="chk-mark" aria-hidden="true">%s</span>`+
			`<span class="chk-body"><b>%s</b>%s <span class="chk-detail">%s</span></span></li>`,
			html.EscapeString(string(c.Status)), mark(c.Status), html.EscapeString(c.Name),
			fieldTag(c), html.EscapeString(c.Detail))
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

// fieldTag names the setting to change, and only on a check that failed: on a
// passing row it would be noise, and on a warning there is nothing to do.
func fieldTag(c oidc.Check) string {
	if c.Status != oidc.CheckFail {
		return ""
	}
	label := map[oidc.CheckField]string{
		oidc.FieldIssuer:       "Issuer",
		oidc.FieldClientID:     "Client ID",
		oidc.FieldClientSecret: "Client secret",
		oidc.FieldRedirectURI:  "Own address",
		oidc.FieldScopes:       "Scopes",
		oidc.FieldProvider:     "at your provider",
	}[c.Field]
	if label == "" {
		return ""
	}
	return ` <span class="set-badge">` + html.EscapeString(label) + `</span>`
}

// order puts what has to be acted on first.
func order(checks []oidc.Check) []oidc.Check {
	rank := map[oidc.CheckStatus]int{oidc.CheckFail: 0, oidc.CheckWarn: 1, oidc.CheckPass: 2}
	out := append([]oidc.Check(nil), checks...)
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Status] < rank[out[j].Status] })
	return out
}

func mark(st oidc.CheckStatus) string {
	switch st {
	case oidc.CheckFail:
		return "✕"
	case oidc.CheckWarn:
		return "!"
	}
	return "✓"
}

// countOf is "1 check failed" / "3 checks failed" — the noun changes with the
// number, which the package's plural (a bare "s") cannot do.
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// signInTestScript is the button's two states, and the round trip between them.
//
// It is the whole of the difference JavaScript makes here. Without it the button
// stays Save changes and the save runs its own checks, as it always has — the
// page is correct, just less kind. With it, moving the switch to Shared makes
// the button Test configuration, a press posts the fields to /settings/signin/test
// and renders what came back, and only a passing result turns the button into
// Save changes. Editing any of the fields afterwards takes it back to Test: a
// pass belongs to the values that were tested, not to the form.
const signInTestScript = `<script>(function(){
var form=document.querySelector('form.set');
if(!form)return;
var sw=form.querySelector('input[name="signin_shared"]');
var btn=form.querySelector('.set-save button');
var out=document.getElementById('signin-checks');
if(!sw||!btn||!out||sw.disabled)return;
var fields=['signin_issuer','signin_client_id','signin_client_secret','signin_scopes'];
var tested=null,passed=false,busy=false;
function values(){return fields.map(function(n){var el=form.elements[n];return el?el.value:'';}).join('\u0000');}
function refresh(){
  var testing=sw.checked&&!(passed&&tested===values());
  btn.value=testing?'test':'save';
  btn.textContent=testing?'Test configuration':'Save changes';
}
function test(){
  if(busy)return;
  busy=true;btn.disabled=true;out.innerHTML='<p class="set-checks-head">Asking the provider…</p>';
  var body=new URLSearchParams();
  body.set('csrf',(form.elements['csrf']||{}).value||'');
  fields.forEach(function(n){var el=form.elements[n];if(el)body.set(n,el.value);});
  fetch('/settings/signin/test',{method:'POST',body:body,headers:{
    'Content-Type':'application/x-www-form-urlencoded'},credentials:'same-origin'})
   .then(function(r){return r.text().then(function(t){
      out.innerHTML=t;
      passed=r.headers.get('X-Tacit-Checks')==='pass';
      if(passed)tested=values();
   });})
   .catch(function(e){out.innerHTML='<p class="set-checks-head bad">The test could not run: '+e+'</p>';})
   .then(function(){busy=false;btn.disabled=false;refresh();});
}
form.addEventListener('submit',function(ev){
  if(btn.value!=='test')return;
  ev.preventDefault();
  test();
});
sw.addEventListener('change',refresh);
fields.forEach(function(n){
  var el=form.elements[n];
  if(el)el.addEventListener('input',function(){passed=false;refresh();});
});
refresh();
})();</script>`

// stageSignIn folds a Personal → Shared move into the page's one save: it
// validates, proves the issuer, and adds the settings to the same patch every
// other field on the page is written by.
//
// It never writes the OFF direction. A page that could turn sign-in off would be
// a page that reopens the registry it closed, and the situation that calls for
// that — a provider that refuses the operator — is exactly the one in which this
// page cannot be reached. That way back is the console's: `tacit secure --off`.
func (s *Server) stageSignIn(r *http.Request, user oidc.Claims, updates map[string]string) (on bool, problems []string) {
	if r.PostFormValue("signin_shared") == "" {
		return false, nil
	}
	if !s.canConfigureSignIn(user) {
		// Nothing to do rather than a refusal: an admin of a registry that
		// already has a provider posts this switch every time they save, because
		// it is rendered in the Shared position and disabled.
		return false, nil
	}
	get := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	issuer := strings.TrimRight(get("signin_issuer"), "/")
	clientID, clientSecret := get("signin_client_id"), get("signin_client_secret")
	if issuer == "" {
		problems = append(problems, "Shared sign-in needs an issuer.")
	}
	// Both, always: the registry exchanges the authorization code from the
	// server, so a public client cannot carry this flow.
	switch {
	case clientID != "" && clientSecret == "":
		// The commonest way to arrive here: a test was run, the page came back,
		// and the password field came back empty because it is never echoed.
		problems = append(problems, "Re-enter the client secret to save—it is never sent back to the page.")
	case clientID == "" || clientSecret == "":
		problems = append(problems, "Shared sign-in needs a client ID and a client secret.")
	}
	// The address first: with no callback there is nothing to check the rest
	// against, and "needs an issuer" would send the operator to the wrong field.
	callback, problem := s.signInAddress()
	if problem != "" {
		return false, []string{problem}
	}
	if problem := missingEnvFile(); problem != "" {
		problems = append(problems, problem)
	}
	if len(problems) > 0 {
		return false, problems
	}
	// The issuer is proved before anything is written. This is the check that
	// stands between a typo and a locked dashboard.
	if _, err := oidc.CheckIssuer(issuer); err != nil {
		return false, []string{err.Error() + " " + issuerFormatHint()}
	}

	updates["TACIT_OIDC_ISSUER"] = issuer
	updates["TACIT_OIDC_CLIENT_ID"] = clientID
	updates["TACIT_OIDC_CLIENT_SECRET"] = clientSecret
	updates["TACIT_OIDC_REDIRECT_URI"] = callback
	// The mode, because the four values open the gate without saying what this
	// registry now IS. Left at "owner", SingleMember() would go on reporting true
	// — an organization's registry presenting itself as one person's, with an
	// owner link that still signed its holder in past the provider they had just
	// configured.
	updates["TACIT_AUTH_MODE"] = config.AuthOIDC
	if sc := get("signin_scopes"); sc != "" && sc != defaultScopes {
		updates["TACIT_OIDC_SCOPES"] = sc
	}
	// Without a stored session secret the signing key is new at every start, so
	// every restart signs every member out. Kept if the file already has one.
	if config.ReadEnv(config.RegistryEnvPath())["TACIT_SESSION_SECRET"] == "" {
		updates["TACIT_SESSION_SECRET"] = NewAPIKey()
	}
	if strings.HasPrefix(callback, "https://") && !s.cfg().CookieSecure {
		updates["TACIT_COOKIE_SECURE"] = "1"
	}
	return true, nil
}

// publishedCallback is the callback the running registry would send while it is
// published, or "" when that is not a different address.
func (s *Server) publishedCallback() string {
	base := s.PublishedBase()
	if !strings.HasPrefix(base, "https://") {
		return ""
	}
	return base + s.cfg().BasePath + "/auth/callback"
}

// serviceRestarts reports whether this process can bring the settings into
// force itself: a service unit that is running, and one that reads the file
// just written. A test suite always fails the second check, which is what keeps
// it from restarting the developer's own registry.
func (s *Server) serviceRestarts(envPath string) bool {
	if s.RestartRegistry != nil {
		return true
	}
	return servicectl.ReadsEnv(envPath) && servicectl.Active()
}

func (s *Server) restartForSignIn() {
	restart := s.RestartRegistry
	if restart == nil {
		restart = servicectl.Restart
	}
	go func() {
		time.Sleep(signInRestartDelay)
		if ok, err := restart(); err != nil || !ok {
			log.Printf("[signin] restart to apply sign-in: restarted=%v err=%v", ok, err)
		}
	}()
}

// flush puts what has been written on the wire before the caller does something
// the response cannot survive — stopping the tunnel it is travelling down, or
// restarting the process serving it.
//
// It goes through ResponseController rather than a type assertion because the
// handler's writer is never net/http's own: the access log, the cache
// classifier and the base-path rewriter each wrap it, and an assertion only
// sees the outermost one. The controller walks Unwrap() to the writer that can
// actually flush.
func flush(w http.ResponseWriter) {
	_ = http.NewResponseController(w).Flush()
}

// signInDone is the last page this process serves as an open registry.
func signInDone(issuer, callback, admins string, restarting bool) string {
	var b strings.Builder
	b.WriteString(`<p><b>Sign-in is configured.</b> ` + html.EscapeString(issuer) +
		` is this registry’s identity provider.</p>`)
	var rows strings.Builder
	rows.WriteString(setFact("Issuer", issuer, ""))
	rows.WriteString(setFact("Callback", callback, ""))
	rows.WriteString(setFact("Administrators", orDash(admins), ""))
	b.WriteString(plate("What is now in force", rows.String()))
	if restarting {
		// The page outlives the process that served it, so it has to find its
		// own way back: poll until the registry answers again, then go to the
		// dashboard, which is now the provider's front door.
		b.WriteString(`<p class="set-lede" id="signin-wait">The registry is restarting with these settings. ` +
			`This page will return to the dashboard, where you must sign in.</p>`)
		b.WriteString(`<p><a href="/">Open the dashboard</a></p>`)
		b.WriteString(signInWaitScript)
	} else {
		b.WriteString(`<p class="set-warn">Nothing here could restart this registry, and it reads ` +
			`sign-in at startup—so the dashboard is still open until you restart it:</p>` +
			`<p><code>systemctl --user restart tacit-registry.service</code> ` +
			`&nbsp;·&nbsp; or stop <code>tacit serve</code> and start it again.</p>`)
	}
	b.WriteString(`<p class="set-lede">Sign in once with your own account before you tell anyone else. ` +
		`If sign-in fails, run <code>tacit secure --off</code> on this machine to restore owner access.</p>`)
	return b.String()
}

// signInWaitScript waits for the restarted registry rather than guessing how
// long it takes. It stops after two minutes: a registry that has not come back
// by then has a problem this page cannot fix, and a page that reloads forever
// hides it.
const signInWaitScript = `<script>(function(){
var tries=0;
function poll(){
  if(++tries>120)return;
  fetch('/v1/health',{cache:'no-store'}).then(function(r){
    if(r.ok){location.href='/';return;}
    setTimeout(poll,1000);
  }).catch(function(){setTimeout(poll,1000);});
}
setTimeout(poll,2000);
})();</script>`

func issuerFormatHint() string {
	parts := make([]string, 0, len(oidc.IssuerFormats))
	for _, f := range oidc.IssuerFormats {
		parts = append(parts, f.Provider+": "+f.Example)
	}
	return "The issuer is the base URL that serves the discovery document—" + strings.Join(parts, "; ") + "."
}
