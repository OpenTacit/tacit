// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"html"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/contribute"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/session"
	"github.com/opentacit/tacit/internal/ui"
)

// keyAuthed gates the member-reachable API: reads, search/evidence, feedback,
// and contribution. The org root key OR any active member key passes.
func (s *Server) keyAuthed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// An endpoint that accepts a credential is bucket B, including the
		// refusal: the guidance is explicit that presenting a key is not by
		// itself protection from caching, because the RESPONSE decides, and a
		// response to an authorized request can still carry public directives.
		cachepolicy.MarkPrivate(w, r, "member key")
		if !s.keyOK(r.Header.Get("X-Tacit-Key")) {
			s.sendError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	}
}

// rootKeyAuthed gates the operator-only admin write surface: promoting/
// rejecting and deleting or editing techniques, federation publish/subscriptions,
// research runs, and sync/recompute. Member keys are distributed to every
// machine that joins and are trivially minted from an invite link, so they
// must NOT be able to rewrite the org's serving set or federation output —
// only the org root key (held by the operator, in registry.env) may. Until
// scoped keys land (docs/design/enterprise-readiness.md) the root key is the
// single admin credential; this keeps the /v1/admin/* mutations aligned with
// the dashboard's own adminOnly gate rather than open to any member key.
func (s *Server) rootKeyAuthed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cachepolicy.MarkPrivate(w, r, "root key")
		presented := r.Header.Get("X-Tacit-Key")
		if presented == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(s.cfg().APIKey)) != 1 {
			s.sendError(w, http.StatusUnauthorized, "unauthorized: this endpoint needs the org root key")
			return
		}
		h(w, r)
	}
}

// keyOK accepts the org root key or an active minted member key
// (join.go / the Members page). Member-key auth records liveness — last-seen
// is access metadata for the admin ("is this credential in use?"), not
// analytics; feedback events are never attributed to a key.
func (s *Server) keyOK(presented string) bool {
	if presented != "" && presented == s.cfg().APIKey {
		return true
	}
	if presented == "" {
		return false
	}
	sum := sha256.Sum256([]byte(presented))
	k, ok, err := s.Store.MemberKeyByHash(hex.EncodeToString(sum[:]))
	if err != nil || !ok || k.RevokedAt != "" {
		return false
	}
	s.touchKey(k.ID)
	return true
}

// touchKey persists a key's last-seen, at most once a minute per key — the
// file store rewrites the whole key file on Touch, so hooks firing every turn
// must not amplify into constant writes.
func (s *Server) touchKey(id string) {
	now := time.Now()
	s.touchMu.Lock()
	last, seen := s.touched[id]
	if seen && now.Sub(last) < time.Minute {
		s.touchMu.Unlock()
		return
	}
	if s.touched == nil {
		s.touched = map[string]time.Time{}
	}
	s.touched[id] = now
	s.touchMu.Unlock()
	_ = s.Store.TouchMemberKey(id, now.UTC().Format(time.RFC3339))
}

func (s *Server) sessionUser(r *http.Request) oidc.Claims {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	if s.OIDC != nil {
		return s.OIDC.VerifySession(c.Value)
	}
	// Owner mode: the same signed token, minted by this registry for its one
	// member rather than by an identity provider for a directory of them.
	if s.cfg().OwnerEnabled() {
		return oidc.Claims(s.ownerSigner().Unpack(c.Value))
	}
	return nil
}

// authRequired reports whether this registry expects a signed-in viewer.
//
// It asks what the SERVER has rather than what the configuration says, because
// a provider can be handed to a Server directly — the test that sweeps every
// browser POST for a gate does exactly that — and a gate that consulted the
// config would open for it.
func (s *Server) authRequired() bool { return s.OIDC != nil || s.cfg().OwnerEnabled() }

// ownerSigner signs owner sessions and the short-lived links that mint them.
func (s *Server) ownerSigner() session.Signer {
	return session.Signer{Secret: []byte(s.cfg().OwnerSecret)}
}

// ownerSessionTTL is how long a signed-in owner stays signed in. Long, because
// re-minting means going back to the console that started the process, and a
// gate that costs a member a terminal every morning is a gate they turn off.
const ownerSessionTTL = 30 * 24 * time.Hour

// ownerLinkTTL is how long a sign-in link is good for. Short, because it is
// printed to a console and may be scrolled past, copied into a chat, or left in
// shell history — it has to stop working before any of those matter.
const ownerLinkTTL = 15 * time.Minute

// OwnerLink is the URL that signs the owner in, built from the secret on this
// machine. The command line mints it; the registry only verifies it.
func OwnerLink(base, secret string) string {
	signer := session.Signer{Secret: []byte(secret)}
	tok := signer.Pack(session.Claims{
		"owner": true,
		"exp":   time.Now().Add(ownerLinkTTL).Unix(),
	})
	return strings.TrimRight(base, "/") + "/auth/owner?t=" + url.QueryEscape(tok)
}

// handleOwnerSignIn exchanges a sign-in link for a session cookie.
//
// The link proves the holder could read the console this registry was started
// from, which is the same proof the setup claim code asks for and the only one
// available without an identity provider.
//
// It is short-lived, NOT single-use: the token is a signed payload and nothing
// here records that one has been spent, so it keeps working until it expires.
// That is a deliberate consequence of stateless tokens — the same property that
// lets sessions survive a restart — and it is why the TTL is fifteen minutes
// and why nothing in the output calls it one-time. Making it single-use means
// keeping a set of spent tokens in memory; worth doing if these links ever
// start travelling further than the console they are printed on.
func (s *Server) handleOwnerSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.cfg().OwnerEnabled() {
		http.NotFound(w, r)
		return
	}
	claims := s.ownerSigner().Unpack(r.URL.Query().Get("t"))
	if claims == nil || claims["owner"] != true {
		// No detail. A link that says WHY it failed is a link that helps
		// somebody guess a better one.
		http.Error(w, "this sign-in link is not valid. Run `tacit dashboard` for a fresh one.",
			http.StatusForbidden)
		return
	}
	s.setCookie(w, sessionCookie, s.ownerSigner().Pack(session.Claims{
		"sub":   "owner",
		"name":  "Owner",
		"owner": true,
		"exp":   time.Now().Add(ownerSessionTTL).Unix(),
	}), int(ownerSessionTTL.Seconds()))
	http.Redirect(w, r, s.cfg().BasePath+"/", http.StatusFound)
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	// Scope to the base path when mounted under one, so the session doesn't
	// leak to (or collide with) other apps on the same proxy host.
	path := s.cfg().BasePath
	if path == "" {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: maxAge, Secure: s.cfg().CookieSecure,
	})
}

// --- OIDC routes ---------------------------------------------------------------

// callbackBase is the address the sign-in round trip has to run on: everything
// in the provider's callback URI up to /auth/callback, so scheme, host and any
// base path. "" when there is no OIDC, or when the configured callback is not
// one of ours to reason about.
//
// It exists because a registry commonly answers on more than one hostname — a
// tailnet name, the published proxy address, a local port — while the identity
// provider knows exactly ONE callback. See beginOnCallbackHost.
func (s *Server) callbackBase() string {
	if s.OIDC == nil {
		return ""
	}
	uri := s.OIDCRedirectURI()
	if !strings.HasSuffix(uri, "/auth/callback") {
		return ""
	}
	base := strings.TrimSuffix(uri, "/auth/callback")
	if u, err := url.Parse(base); err != nil || u.Host == "" || u.Scheme == "" {
		return ""
	}
	return base
}

// beginOnCallbackHost sends a sign-in that started on some OTHER hostname over
// to the one the provider will come back to, and reports whether it did.
//
// The txn cookie carrying the CSRF state is host-only, by design. So a flow begun
// on a hostname the callback never visits sets its cookie somewhere the callback
// cannot read it: the member comes back to a request with no cookie at all and
// the attempt fails — and fails identically on every retry, for as long as they
// keep starting from that hostname, since nothing about it ever gets better.
// That was reachable here in the ordinary case of a registry published behind a
// proxy while its members still reach it by its original name.
//
// The one-shot marker is a query parameter rather than a cookie, so that a proxy
// which rewrites Host cannot turn this into a redirect loop: after one hop the
// flow proceeds wherever it lands, and at worst the old failure stands.
func (s *Server) beginOnCallbackHost(w http.ResponseWriter, r *http.Request, path, query string) bool {
	base := s.callbackBase()
	if base == "" || r.URL.Query().Get(canonParam) != "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil || strings.EqualFold(u.Host, r.Host) {
		return false
	}
	dest := base + path + "?" + canonParam + "=1"
	if query != "" {
		dest += "&" + query
	}
	http.Redirect(w, r, dest, http.StatusFound)
	return true
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		s.sendError(w, 404, "OIDC not configured")
		return
	}
	q := r.URL.Query()
	next := q.Get("next")
	// Only ever redirect back into this app — and never back into the flow
	// itself, or the sign-in that follows a failed callback would return the
	// member to that same bare callback URL and fail a second time.
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/auth/") {
		next = "/"
	}
	// Before minting anything: get onto the host the callback lands on, because
	// the cookie set below is only readable there.
	params := "next=" + url.QueryEscape(next)
	if q.Get(switchParam) != "" {
		params += "&" + switchParam + "=1"
	}
	if s.beginOnCallbackHost(w, r, "/auth/login", params) {
		return
	}
	// ?switch=1 is the "not this account" door: it asks the provider for its
	// account chooser instead of letting it reuse the session it already has.
	prompt := ""
	if q.Get(switchParam) != "" {
		prompt = "select_account"
	}
	state, txn := s.OIDC.CreateTxn(next)
	u, err := s.OIDC.AuthorizeURL(state, prompt)
	if err != nil {
		s.sendError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.setCookie(w, txnCookie, txn, 600)
	http.Redirect(w, r, u, http.StatusFound)
}

func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		s.sendError(w, 404, "OIDC not configured")
		return
	}
	q := r.URL.Query()
	var txn string
	if c, err := r.Cookie(txnCookie); err == nil {
		txn = c.Value
	}
	// The txn is single-use, so it goes now, on every path out of here. A
	// consumed or failed one left in the jar is indistinguishable from a live
	// one on the next attempt.
	s.setCookie(w, txnCookie, "", -1)

	if e := q.Get("error"); e != "" {
		s.signinFailed(w, r, "Your identity provider did not complete the sign-in.", "provider returned error="+e)
		return
	}
	claims := s.OIDC.VerifyTxnClaims(txn, q.Get("state"))
	code := q.Get("code")
	if code == "" || claims == nil {
		s.signinFailed(w, r, "That sign-in could not be completed. Try again.", txnFailure(txn, code))
		return
	}
	tokens, err := s.OIDC.ExchangeCode(code)
	if err != nil {
		s.signinFailed(w, r, "Your identity provider did not complete the sign-in.", "code exchange: "+err.Error())
		return
	}
	access, _ := tokens["access_token"].(string)
	user, err := s.OIDC.FetchUserinfo(access)
	if err != nil {
		s.signinFailed(w, r, "Your identity provider did not complete the sign-in.", "userinfo: "+err.Error())
		return
	}

	// An MCP authorization is completed by minting the client's code; a plain
	// dashboard sign-in sets the session cookie and returns to the app.
	if claims["kind"] == oidc.KindMCPTxn {
		s.finishMCPAuthorize(w, r, claims, user)
		return
	}
	s.setCookie(w, sessionCookie, s.OIDC.CreateSession(user), s.cfg().SessionTTLSecs)
	next, _ := claims["next"].(string)
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// txnFailure names which half of the handshake was missing, for the log. The
// distinction is the whole diagnosis: no cookie means the browser never had one
// to send (a flow begun on another hostname, or cookies refused), while a cookie
// that does not match means a stale or replayed attempt.
func txnFailure(txn, code string) string {
	switch {
	case code == "":
		return "no authorization code in the callback"
	case txn == "":
		return "no txn cookie came back with the callback"
	default:
		return "txn cookie did not match the callback state"
	}
}

// signinFailed answers a sign-in that could not be completed.
//
// It renders the front door, not JSON. The reader is a person in a browser, and
// the bare {"error":…} this used to serve was a dead end: nothing to click, no
// way back, and — since it named no cause — nothing to tell them apart from a
// registry that was simply broken. The button on this page carries ?switch=1, so
// the retry offers the account chooser: whatever went wrong, "sign in as someone
// else" is the recovery that was previously impossible to reach.
//
// The detail goes to the log rather than the page. What failed here is the
// operator's to read; the member only needs the way forward.
func (s *Server) signinFailed(w http.ResponseWriter, r *http.Request, note, detail string) {
	log.Printf("[auth] sign-in not completed (host=%s): %s", r.Host, detail)
	cachepolicy.MarkPrivate(w, r, "failed sign-in")
	s.sendHTML(w, http.StatusBadRequest, s.renderSigninNote(r, note))
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	s.setCookie(w, sessionCookie, "", -1)
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleAdminTechniqueAction is the Drafts view's Promote/Reject buttons. Separate
// from the key-authed /v1 API: browsers can't send X-Tacit-Key, so this is
// gated by a signed-in OIDC session when OIDC is on (the SameSite=Lax session
// cookie gives CSRF protection on cross-site POSTs); on a local pilot with
// OIDC off the whole view is open anyway.
func (s *Server) handleAdminTechniqueAction(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/review") {
		return
	}
	// Each browser action is pinned to the one lifecycle state it may act
	// on: on an open pilot (OIDC off) a drive-by POST must not be able to flip
	// arbitrary statuses. promote/reject act on drafts, restore on retired, and
	// to-draft on a serving (stable) technique — pulling it back into the review lane.
	// to-shadow sends a draft into shadow evaluation instead of straight to
	// serving: retrieved and judged for relevance, never surfaced
	// (docs/learning/validation-without-review.md). Full lifecycle control stays
	// on the key-authed /v1/admin/promote.
	actions := map[string]struct{ to, requires, back string }{
		"promote":        {"stable", "draft", "/review"},
		"reject":         {"retired", "draft", "/review"},
		"restore":        {"stable", "retired", "/techniques"},
		"to-draft":       {"draft", "stable", "/review"},
		"to-shadow":      {"shadow", "draft", "/review"},
		"shadow-promote": {"stable", "shadow", "/review"},
		"shadow-reject":  {"retired", "shadow", "/review"},
	}
	act, ok := actions[r.PathValue("action")]
	if !ok {
		s.sendError(w, 404, "unknown action")
		return
	}
	id, _ := url.PathUnescape(r.PathValue("id"))
	if technique, found, err := s.Store.GetTechnique(id); err != nil || !found || technique.Status != act.requires {
		http.Redirect(w, r, act.back, http.StatusFound) // no-op
		return
	}
	_, _, _ = contribute.Promote(s.Store, id, act.to, s.Embedder)
	http.Redirect(w, r, act.back, http.StatusFound)
}

// handleTechniqueRevert lands a prior version's content as a new current version.
// Same posture as handleAdminTechniqueAction: adminOnly wraps the route (the email
// gate when TACIT_ADMIN_EMAILS is set), and this repeats the signed-in check so
// an open pilot still requires a session when OIDC is on. contribute.RevertTo
// does the archive+copy and refuses an out-of-range or draft target, so a
// drive-by POST cannot revert to an arbitrary version. Success and every no-op
// alike return to the technique's history, where the new version is now visible.
func (s *Server) handleTechniqueRevert(w http.ResponseWriter, r *http.Request) {
	id, _ := url.PathUnescape(r.PathValue("id"))
	back := "/techniques/history/" + url.PathEscape(id)
	if !s.signedInOrRedirect(w, r, back) {
		return
	}
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		http.Redirect(w, r, back, http.StatusFound) // malformed version: no-op
		return
	}
	_, _, _ = contribute.RevertTo(s.Store, id, n, s.Embedder)
	http.Redirect(w, r, back, http.StatusFound)
}

// handleBulkDeleteDrafts removes the checked drafts outright — the review
// lane's cleanup action for suggestion runs that missed. Same session gate as
// the other technique actions, and the draft-only rule is enforced per id here,
// not just in the UI: a drive-by POST cannot delete a stable technique's evidence.
func (s *Server) handleBulkDeleteDrafts(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/review") {
		return
	}
	_ = r.ParseForm()
	for _, id := range r.Form["id"] {
		if technique, found, err := s.Store.GetTechnique(id); err == nil && found && technique.Status == "draft" {
			_, _ = s.Store.DeleteTechnique(id)
		}
	}
	http.Redirect(w, r, "/review", http.StatusFound)
}

// signedInOrRedirect is the gate on a browser form POST. Browsers cannot send
// X-Tacit-Key, so a signed-in OIDC session is what stands in front of these
// actions; the SameSite=Lax session cookie gives CSRF protection on cross-site
// POSTs. A caller without one is sent to `back` and nothing happens — the
// refusal reads to the browser as a no-op, which is what the page wants.
//
// With OIDC off (a local pilot) there is no identity to check and the action
// stays open. That is deliberate, and it is why this returns true rather than
// refusing when s.OIDC is nil.
//
// Callers must return when this returns false.
func (s *Server) signedInOrRedirect(w http.ResponseWriter, r *http.Request, back string) bool {
	if s.authRequired() && s.sessionUser(r) == nil {
		http.Redirect(w, r, back, http.StatusFound) // not signed in: no-op
		return false
	}
	return true
}

// signedInOrJSON is the same gate for the endpoints a page fetches as JSON,
// where a redirect would arrive as HTML the caller cannot read. Same open-pilot
// rule: OIDC off means no gate.
func (s *Server) signedInOrJSON(w http.ResponseWriter, r *http.Request) bool {
	if s.authRequired() && s.sessionUser(r) == nil {
		s.sendError(w, http.StatusUnauthorized, "sign-in required")
		return false
	}
	return true
}

// signedInOrForbidden is the same gate for an action with nowhere sensible to
// send the caller back to, so it says no in plain text instead. Same open-pilot
// rule: OIDC off means no gate.
func (s *Server) signedInOrForbidden(w http.ResponseWriter, r *http.Request, msg string) bool {
	if s.authRequired() && s.sessionUser(r) == nil {
		http.Error(w, msg, http.StatusForbidden)
		return false
	}
	return true
}

// adminOnly guards the dashboard's mutating actions. With no admin list
// configured the handler's own signed-in check is the whole rule (any member
// may curate — a pilot posture); once TACIT_ADMIN_EMAILS is set, these
// actions belong to the named operators.
func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cachepolicy.MarkPrivate(w, r, "admin action")
		if len(s.cfg().AdminEmails) > 0 && !s.isAdmin(s.sessionUser(r)) {
			http.Error(w, "only the registry’s administrators (TACIT_ADMIN_EMAILS) can do this action", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// renderSignin builds the front door for a visitor without a session: the
// sign-in call to action, links into the (public) user guide, and a connect
// command carrying THIS registry's address so a first-time member wires the
// right host. The login link preserves the page they were reaching.
func (s *Server) renderSignin(r *http.Request) string {
	return s.renderSigninNote(r, "")
}

// renderSigninNote is the front door with an optional line of explanation above
// the button — what a member sees when a sign-in did not complete. The note
// makes the page carry an account chooser on its button, since a retry that
// cannot change identity is no retry at all for someone signed in as the wrong
// person.
func (s *Server) renderSigninNote(r *http.Request, note string) string {
	// The page they were reaching — unless that IS the flow, which it is when
	// this page is a failed callback. Sending them back to a bare callback URL
	// would fail again, and look identical to the loop they are already in.
	next := r.URL.Path
	if strings.HasPrefix(next, "/auth/") {
		next = "/"
	}
	login := "/auth/login?next=" + url.QueryEscape(next)
	notice := ""
	if note != "" {
		login += "&" + switchParam + "=1"
		notice = `<p class="signin-hint signin-notice">` + html.EscapeString(note) + `</p>`
	}
	// Owner mode has no provider to send anybody to. The way in is a command on
	// the machine that runs this registry, so the door says so instead of
	// offering a button that would 404 — and it is the same sentence whether
	// the visitor is the owner on another device or a stranger who found the
	// address, because the door cannot tell them apart and must not try.
	if s.OIDC == nil && s.cfg().OwnerEnabled() {
		login = ""
		if s.cfg().TeamEnabled() {
			// Opened to a team: most people arriving here are members holding a
			// key, so the door asks for one. The owner's way in is unchanged and
			// is named second, because there is exactly one of them.
			notice = memberKeyForm() +
				`<p class="signin-hint">Set this registry up? Run <code>tacit dashboard</code> ` +
				`on the machine it runs on for an owner link.</p>` + notice
		} else {
			notice = `<p class="signin-hint signin-notice">This registry has one member. ` +
				`Run <code>tacit dashboard</code> on the machine that runs it for a sign-in link.</p>` + notice
		}
	}
	button := `<a class="btn" href="` + login + `">Sign in to the registry</a>`
	if login == "" {
		button = ""
	}
	return strings.NewReplacer(
		"«SIGNINBUTTON»", button,
		"«LOGINURL»", login,
		"«NOTICE»", notice,
		"«REGISTRYURL»", html.EscapeString(s.externalBaseFor(r)),
		"«PRODUCT»", productHTML(),
		"«PRODUCTLEN»", ui.WordmarkLen(product.Name()),
	).Replace(signinTemplate)
}

// publicHTMLView renders a page that MAY be viewed without a session: when
// allow(r) reports the request is public and there's no session, the page
// renders (signed-out shell) instead of the sign-in front door. htmlView is the
// closed case — allow always false — which every gated page uses.
func (s *Server) publicHTMLView(allow func(*http.Request) bool, render func(r *http.Request, user oidc.Claims) page) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := s.sessionUser(r)
		if s.authRequired() && user == nil {
			if !allow(r) {
				// The front door: a sign-in call to action, the guide links, and
				// the connect command for this host. No identity and no counts.
				//
				// It is NOT bucket A, though every anonymous visitor does get
				// exactly this page. It is served at whatever gated URL was asked
				// for, and that URL answers with the member's own dashboard when a
				// session comes with it — so the two live at one address and only
				// the cookie tells them apart.
				//
				// The credential downgrade cannot save a private cache here,
				// because a private cache does not make the request it would
				// downgrade. A browser that stored this page while signed out
				// reused it after signing in, for max-age and then for a whole day
				// of stale-while-revalidate: the member landed on the sign-in
				// screen while holding a valid session, on and off, for as long as
				// the copy lived. The zone bypasses on the session cookie, so the
				// shared cache was never the one at fault.
				//
				// This did cost the fleet its cheapest cache entry, and crawlers on
				// a wildcard zone do ask for it constantly. Buying it back means
				// splitting the two audiences rather than trusting one header:
				// CDN-Cache-Control for the edge (which keys on the cookie) and a
				// revalidating Cache-Control for the browser (which does not), or
				// the front door at an address of its own. Worth doing when origin
				// load says so; not worth guessing at.
				cachepolicy.MarkPrivate(w, r, "sign-in front door")
				s.sendHTML(w, 200, s.renderSignin(r))
				return
			}
			// A public page rendered for a visitor with no session: the signed-out
			// shell, which carries no member and no library totals.
			//
			// This stays bucket A, unlike the front door above, and the difference
			// is what a stale copy costs. Here the document is the same document
			// either way and only the chrome around it differs, so a member served
			// their own cached signed-out copy sees a signed-out header on a page
			// they can read anyway. There the cached page was a different page,
			// and it locked them out. The user guide is also the tree crawlers
			// actually index, and the zone's allowlist is generated to cache it.
			cachepolicy.MarkPublic(w, r, "public page, no session")
		} else {
			// Either a signed-in reader, or a registry with no identity provider
			// at all — where "anonymous" sees the whole dashboard and the content
			// is the org's private configuration rather than anything public.
			cachepolicy.MarkPrivate(w, r, privateHTMLReason(s.authRequired()))
		}
		p := render(r, user)
		if p.status == 0 {
			p.status = 200
		}
		if p.status != 200 {
			// A 404 for an unknown technique or slug is private and stays that way; the
			// classifier enforces it, and this says so on the record.
			cachepolicy.MarkPrivate(w, r, "non-200 page")
		}
		s.sendHTML(w, p.status, s.renderShell(p, user))
	}
}

// privateHTMLReason distinguishes the two ways a page ends up private, because
// they need different fixes: a signed-in reader is working as designed, while an
// ungated registry is serving org-private content to anyone who asks and could
// not be made cacheable without changing that.
func privateHTMLReason(gated bool) string {
	if gated {
		return "signed-in reader"
	}
	return "no identity provider"
}

func (s *Server) htmlView(render func(r *http.Request, user oidc.Claims) page) http.HandlerFunc {
	return s.publicHTMLView(func(*http.Request) bool { return false }, render)
}

// --- M6: the rung between one owner and an identity provider --------------
//
// A registry that has been opened to a team (config.AuthTeam) signs its owner
// in exactly as before — the secret on the machine, `tacit dashboard` — and
// signs everybody else in with the member key their invitation minted.
//
// This is deliberately not an identity store. Nothing new is issued, nothing
// records who a person is, and the only fact a member session carries is the
// label the key was minted with ("dana@laptop"), which an operator typed for
// their own benefit on the Members page. It is a way IN to the dashboard for
// people who have already been given a credential, and no more than that: a
// member session is never an admin session, so curation, settings and the
// invite surface stay with the owner.
//
// The alternative — passkeys — was assessed and deferred: WebAuthn needs this
// project's first store of who a person is, which is a privacy line that wants
// deciding on its own rather than as a side effect of somebody inviting a
// colleague.

// memberSessionTTL matches the owner's. A member whose session expires has to
// find their key again, which lives in a config file on a machine they may not
// be at; the credential itself is what gets revoked when access should end.
const memberSessionTTL = 30 * 24 * time.Hour

// handleMemberSignIn exchanges a member key for a dashboard session.
//
// Only where member keys are a way in at all. On a single-member registry there
// is nobody to let in, and on an OIDC registry the provider is the door — a
// second one accepting a key that every joined machine holds would quietly
// undercut it.
func (s *Server) handleMemberSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.cfg().TeamEnabled() || s.OIDC != nil {
		http.NotFound(w, r)
		return
	}
	cachepolicy.MarkPrivate(w, r, "member sign-in")
	_ = r.ParseForm()
	key := strings.TrimSpace(r.PostFormValue("key"))
	k, ok := s.memberKeyFor(key)
	if !ok {
		// No detail, and the same answer for a malformed key as for a revoked
		// one: a door that explains itself is a door that helps somebody guess.
		s.sendHTML(w, http.StatusForbidden, s.renderSigninNote(r,
			"That member key was not recognized. Ask whoever invited you for a fresh join link."))
		return
	}
	s.setCookie(w, sessionCookie, s.ownerSigner().Pack(session.Claims{
		"sub":   "member:" + k.ID,
		"name":  k.Label,
		"owner": false,
		"exp":   time.Now().Add(memberSessionTTL).Unix(),
	}), int(memberSessionTTL.Seconds()))
	http.Redirect(w, r, s.cfg().BasePath+"/", http.StatusFound)
}

// memberKeyFor resolves a presented secret to the key record behind it, so the
// session can carry the label an operator gave it. The org root key is NOT
// accepted: it is the operator's credential, its holder already has a door
// (`tacit dashboard`), and minting a non-admin session from an admin secret
// would be a confusing thing to have built.
func (s *Server) memberKeyFor(presented string) (models.MemberKey, bool) {
	if presented == "" || presented == s.cfg().APIKey {
		return models.MemberKey{}, false
	}
	sum := sha256.Sum256([]byte(presented))
	k, ok, err := s.Store.MemberKeyByHash(hex.EncodeToString(sum[:]))
	if err != nil || !ok || k.RevokedAt != "" {
		return models.MemberKey{}, false
	}
	s.touchKey(k.ID)
	return k, true
}

// memberKeyForm is the second door, rendered on the front page of a registry
// that has one. It asks for the credential the member already has rather than
// inventing a second one: the key their join link minted, which `tacit connect`
// wrote to agent.env on their machine.
func memberKeyForm() string {
	return `<form class="signin-key" method="post" action="/auth/key">` +
		`<label for="member-key">Signing in with a member key</label>` +
		`<div class="inline-form"><input id="member-key" name="key" type="password" autocomplete="off" ` +
		`placeholder="the key from your join link" required>` +
		`<button type="submit">Sign in</button></div>` +
		`<p class="signin-hint">It is in <code>~/.config/tacit/agent.env</code> on the machine you connected.</p>` +
		`</form>`
}
