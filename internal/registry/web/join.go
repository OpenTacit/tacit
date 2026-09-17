// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The join loop (docs/distribution/growth-plan.md mechanism 1): a member
// mints a short-lived link, pastes one line in chat, and a teammate goes from
// nothing to a connected harness — the org key travels server-side, never
// through the chat.
//
// Tokens are stateless: HMAC-SHA256 over the expiry, keyed from the org API
// key. Nothing to store, nothing to sync between instances; individual
// revocation doesn't exist, but the horizon is short (default 24h, max 7d)
// and rotating the API key kills every outstanding token at once — the same
// blast radius the key itself already has. Scoped per-member keys
// (docs/design/enterprise-readiness.md) slot in here without changing the UX.
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit"
	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/session"
	"github.com/opentacit/tacit/internal/ui"
)

const (
	joinTTLDefault = 24 * time.Hour
	joinTTLMax     = 7 * 24 * time.Hour
)

// joinMAC signs an expiry timestamp and the token's own nonce. The signing key
// is derived, not the API key itself, so a token can never be confused for (or
// upgraded into) the key by construction.
func joinMAC(apiKey string, expiry int64, nonce string) string {
	kd := sha256.Sum256([]byte("tacit-join-v1:" + apiKey))
	m := hmac.New(sha256.New, kd[:])
	fmt.Fprintf(m, "%d.%s", expiry, nonce)
	return hex.EncodeToString(m.Sum(nil))
}

// mintJoinToken returns "<expiryUnix>.<nonce>.<hex mac>".
//
// The nonce is what makes an invitation a THING rather than a timestamp. The
// token used to be the expiry and its MAC, so two invitations minted in the
// same second were the same string, and no invitation could be told from
// another — which is why the page's promise that a link is "spent when you
// join" was never implemented and could not have been.
func mintJoinToken(apiKey string, ttl time.Duration) string {
	expiry := time.Now().Add(ttl).Unix()
	nonce := NewAPIKey()[:16]
	return fmt.Sprintf("%d.%s.%s", expiry, nonce, joinMAC(apiKey, expiry, nonce))
}

// verifyJoinToken reports whether the token is authentic and unexpired, and
// returns its nonce.
//
// Two-part tokens from before the nonce are refused. They cannot be redeemed
// once and only once, which is now what an invitation means, and the longest
// one outlives its minting by joinTTLMax.
func verifyJoinToken(apiKey, token string) (string, bool) {
	expStr, rest, ok := strings.Cut(token, ".")
	if !ok {
		return "", false
	}
	nonce, mac, ok := strings.Cut(rest, ".")
	if !ok || nonce == "" {
		return "", false
	}
	expiry, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return "", false
	}
	if !hmac.Equal([]byte(mac), []byte(joinMAC(apiKey, expiry, nonce))) {
		return "", false
	}
	return nonce, true
}

// joinKeyID is the member key an invitation mints, named after the invitation.
//
// This is how redemption is recorded without a table to record it in: the key
// IS the receipt. A second POST computes the same id, finds it already in the
// store, and is refused — on the file store, SQLite and Postgres alike, with
// no migration and nothing to keep in step. The id is derived rather than
// random, and that is safe: key ids are not secrets, and the Members page
// prints them.
func joinKeyID(nonce string) string {
	sum := sha256.Sum256([]byte("tacit-join-key:" + nonce))
	return "mk-" + hex.EncodeToString(sum[:])[:12]
}

// joinAlreadyRedeemed reports whether this invitation has already minted its
// member key.
func (s *Server) joinAlreadyRedeemed(nonce string) (bool, error) {
	keys, err := s.Store.ListMemberKeys()
	if err != nil {
		return false, err
	}
	want := joinKeyID(nonce)
	for _, k := range keys {
		if k.ID == want {
			return true, nil
		}
	}
	return false, nil
}

// externalBaseFor is the registry address a joining machine should record: the
// proxy address when this registry is published, else the configured external
// URL, else the address this request arrived on.
//
// Publishing wins because it is the more specific answer — an operator who
// turned it on is telling us where the registry is reachable from, and the
// external URL they set for some earlier arrangement is now the stale one.
func (s *Server) externalBaseFor(r *http.Request) string {
	c := s.cfg()
	if base := s.PublishedBase(); base != "" {
		return base + c.BasePath
	}
	if base := c.ExternalBase(); base != "" {
		return base
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + c.BasePath
}

// handleInvite mints a join link. Key-authed: anyone who already holds the
// org key may invite — they could hand the key over anyway; this gives them a
// safer artifact to hand over instead.
func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TTLSecs int64 `json:"ttl_secs"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // empty body = defaults
	ttl := joinTTLDefault
	if req.TTLSecs > 0 {
		ttl = time.Duration(req.TTLSecs) * time.Second
	}
	if ttl > joinTTLMax {
		ttl = joinTTLMax
	}
	token := mintJoinToken(s.cfg().APIKey, ttl)
	s.sendJSON(w, 200, map[string]any{
		"join_url":   s.externalBaseFor(r) + "/join/" + token,
		"expires_at": time.Now().Add(ttl).UTC().Format(time.RFC3339),
	})
}

// requestBase is the address this request actually arrived at, which is not
// always the registry's canonical one: an ingress serving several zones answers
// for the same registry at more than one hostname (internal/ingress, zone
// aliases). Use it for things a reader is asked to fetch from the page in front
// of them; use externalBaseFor for anything a machine records as the registry's
// identity, which has to stay canonical wherever it was read.
func (s *Server) requestBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + s.cfg().BasePath
}

// handleInstallScript serves the installer at this registry's own address, so a
// member is told to fetch it from the host they are already looking at rather
// than from the project's zone. That matters for a self-hosted registry on a
// domain of its own as much as it does for a proxy carrying two zones: an
// install command naming somewhere else is one a reader has to decide to trust.
//
// The bytes are the same ones the join link serves (tacit.InstallScript, already
// embedded for that purpose), so there is no second copy to drift and no
// redirect to a code host that a firewall might not allow.
//
// Private: the classifier's default, and the same call the ingress apex makes
// for its own install address. Nothing here is per-reader, but declaring public
// would put a path on the edge's cacheable list without it having been reviewed
// there — and pkg/webpaths.PublicPaths takes an entry only with a note saying
// why it is safe.
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPrivate(w, r, "the installer, served from this registry")
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	fmt.Fprintln(w, tacit.InstallScript)
}

// handleJoinExchange swaps a valid token for the registry settings a member
// needs. Each exchange MINTS a fresh member key (models.MemberKey) rather
// than handing out the org root key: what the admin sees on the Members page
// is what joined, and revoking one machine never means rotating everyone.
func (s *Server) handleJoinExchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
		Label string `json:"label"`
	}
	nonce, ok := "", false
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
		nonce, ok = verifyJoinToken(s.cfg().APIKey, req.Token)
	}
	if !ok {
		s.sendError(w, http.StatusUnauthorized, "invalid or expired join token")
		return
	}
	secret, _, err := s.redeemJoinToken(nonce, req.Label)
	switch {
	case errors.Is(err, errJoinSpent):
		s.sendError(w, http.StatusConflict, "this invitation has already been used — ask for a fresh one")
		return
	case err != nil:
		s.sendError(w, http.StatusInternalServerError, "could not mint a member key")
		return
	}
	s.sendJSON(w, 200, map[string]any{
		"registry_url": s.externalBaseFor(r),
		"api_key":      secret,
	})
}

// errJoinSpent is the second person through a one-person door.
var errJoinSpent = errors.New("invitation already redeemed")

// redeemJoinToken mints the one member key an invitation is worth.
//
// Both doors call it — the JSON exchange `tacit join` uses and the button on
// the invitation page — because both used to mint a key per request, without
// limit, for as long as the token had left to live. A leaked link was a
// standing offer of membership.
//
// The lock closes the double-click: two requests that both read "not redeemed"
// would both insert, computing the same id, and the second would overwrite the
// first — leaving the earlier member holding a secret the store no longer
// recognizes. One registry process, so one mutex is the whole fix.
func (s *Server) redeemJoinToken(nonce, label string) (string, models.MemberKey, error) {
	s.joinMu.Lock()
	defer s.joinMu.Unlock()
	spent, err := s.joinAlreadyRedeemed(nonce)
	if err != nil {
		return "", models.MemberKey{}, err
	}
	if spent {
		return "", models.MemberKey{}, errJoinSpent
	}
	secret, key := NewMemberKey(joinLabel(label))
	key.ID = joinKeyID(nonce)
	if err := s.Store.InsertMemberKey(key); err != nil {
		return "", models.MemberKey{}, err
	}
	return secret, key, nil
}

// joinLabel is what the Members page will show for this machine.
func joinLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "joined " + time.Now().UTC().Format("2006-01-02")
	}
	if len(label) > 80 {
		label = label[:80]
	}
	return label
}

// handleConnectRedeem trades a handoff code for the member key it stands for.
//
// Unauthenticated by design, like the join exchange beside it: the code IS the
// credential, and a machine that has one has nothing else to present. It is
// worth little — sixty bits, fifteen minutes, one use — and it is gone from
// this process the moment it is spent.
func (s *Server) handleConnectRedeem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, "malformed request")
		return
	}
	secret, base, ok := s.redeemHandoff(req.Code)
	if !ok {
		s.sendError(w, http.StatusUnauthorized,
			"this code is expired, already used, or mistyped — open the invitation page again for a fresh one")
		return
	}
	cachepolicy.MarkPrivate(w, r, "handoff code redeemed")
	if base == "" {
		base = s.externalBaseFor(r)
	}
	s.sendJSON(w, 200, map[string]any{"registry_url": base, "api_key": secret})
}

// NewMemberKey mints a credential: the secret to hand to the member (shown
// once) and the hashed record to store.
func NewMemberKey(label string) (secret string, k models.MemberKey) {
	secret = NewAPIKey()
	sum := sha256.Sum256([]byte(secret))
	return secret, models.MemberKey{
		ID:        "mk-" + NewAPIKey()[:12],
		Label:     label,
		Hash:      hex.EncodeToString(sum[:]),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// --- M4: the same link, opened in a browser -------------------------------
//
// A join link is minted to be pasted into chat, and what happens to a link in
// chat is that somebody taps it. Serving a shell script to that tap is correct
// for `curl` and useless for the person: they see a wall of installer, and the
// one member of the team least invested in any of this is asked to work out
// that they were supposed to pipe it somewhere.
//
// So the route answers what the client asked for. `curl` gets the installer it
// requested, byte for byte; a browser gets the page below — whose registry this
// is, what is in it, and then the join.
//
// The token is NOT spent by looking. A member who opens the link twice, or opens
// it to see what it is before deciding, must not find their invitation consumed
// by a page load, and a chat client that fetches a preview of every URL must not
// consume it either. Redemption is the POST behind the button.

// wantsHTML reports whether this request came from a browser.
//
// Asks what the client will ACCEPT rather than sniffing User-Agent: `curl`
// sends no Accept at all (or */*), every browser leads with text/html, and the
// installer must keep working for anything that does not ask for a page.
func wantsHTML(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		if strings.HasPrefix(strings.TrimSpace(part), "text/html") {
			return true
		}
	}
	return false
}

// handleJoin serves a join link: the personalized installer to a client that
// asked for one, and the join page to a browser.
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	// Looking still costs nothing. Redemption is the POST below, and a preview
	// fetched by a chat client unfurling the link must not consume anything.
	if _, ok := verifyJoinToken(s.cfg().APIKey, token); !ok {
		s.joinExpired(w, r)
		return
	}
	if wantsHTML(r) {
		s.joinPage(w, r, token)
		return
	}
	base := s.externalBaseFor(r)
	cachepolicy.MarkPrivate(w, r, "personalized installer")
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	fmt.Fprintf(w, "%s\nexec \"$INSTALL_DIR/tacit\" join %s/join/%s\n", joiningInstallScript(), base, token)
}

// joinInstallerFlag is the line the join installer carries that the plain one
// does not. install.sh reads it and drops its "start a registry for your org"
// footer: this member is joining one that exists, and starting a second is the
// move that costs them a `tacit merge` to undo.
const joinInstallerFlag = "TACIT_JOINING=1"

// joiningInstallScript is install.sh with that flag set, placed under the
// shebang so the served file is still a script somebody can save and read.
func joiningInstallScript() string {
	const shebang = "#!/bin/sh\n"
	if !strings.HasPrefix(tacit.InstallScript, shebang) {
		return joinInstallerFlag + "\n" + tacit.InstallScript
	}
	return shebang + joinInstallerFlag + "\n" + strings.TrimPrefix(tacit.InstallScript, shebang)
}

// joinExpired answers a spent or forged link in the medium it was asked for.
func (s *Server) joinExpired(w http.ResponseWriter, r *http.Request) {
	const msg = "this join link is invalid or has expired; ask your colleague for a fresh one (tacit invite)"
	if !wantsHTML(r) {
		http.Error(w, msg, http.StatusNotFound)
		return
	}
	cachepolicy.MarkPrivate(w, r, "expired invitation")
	s.sendHTML(w, http.StatusNotFound, s.renderShell(page{
		status: http.StatusNotFound,
		crumbs: []crumb{{label: "Invitation", href: ""}},
		narrow: true,
		content: `<section class="panel"><h2>This invitation has expired</h2>` +
			`<p class="hint">Join links expire because anyone holding one can join. ` +
			`Ask whoever sent it for a fresh one.</p></section>`,
	}, nil))
}

// joinSpent answers the second person to press the button. Distinct from
// joinExpired because the remedy is the same but the fact is not, and a member
// told their invitation "expired" seconds after a colleague used it would go
// looking for a clock problem.
func (s *Server) joinSpent(w http.ResponseWriter, r *http.Request) {
	const msg = "this invitation has already been used; ask your colleague for a fresh one (tacit invite)"
	if !wantsHTML(r) {
		http.Error(w, msg, http.StatusConflict)
		return
	}
	cachepolicy.MarkPrivate(w, r, "spent invitation")
	s.sendHTML(w, http.StatusConflict, s.renderShell(page{
		status: http.StatusConflict,
		crumbs: []crumb{{label: "Invitation", href: ""}},
		narrow: true,
		content: `<section class="panel"><h2>This invitation has been used</h2>` +
			`<p class="hint">Each invitation can join one machine, and someone has already used this one. ` +
			`Ask whoever sent it for a fresh one.</p></section>`,
	}, nil))
}

// joinPage is what a person sees when they tap the link.
//
// It shows what they are being offered before asking them to take it: whose
// registry, how many techniques, and a few real ones. That is a disclosure to
// somebody holding an unredeemed token, and it is a deliberate one — the link
// already grants full membership, so anything shown here is strictly less than
// what accepting hands over, and a person deciding whether to join a colleague's
// playbook is owed a look at it.
func (s *Server) joinPage(w http.ResponseWriter, r *http.Request, token string) {
	cachepolicy.MarkPrivate(w, r, "invitation preview")
	var b strings.Builder
	// "a/an" cannot be written for a product name that is configurable, and the
	// team is the thing being joined anyway.
	b.WriteString(`<section class="panel"><h2>You have been invited to a team’s playbook</h2>`)
	b.WriteString(`<p class="hint">A playbook is the techniques this team has found that work with AI ` +
		`assistants, carrying what actually happened when people used them. Joining points your own AI ` +
		`tools at it, and lets you add to it.</p>`)
	b.WriteString(s.joinPreview())
	b.WriteString(`<form method="post" action="/join/` + html.EscapeString(token) + `" class="inline-form">` +
		`<input name="label" maxlength="80" placeholder="who you are, for example dana@laptop" aria-label="Name this machine"> ` +
		`<button type="submit">Join this playbook</button></form>`)
	b.WriteString(`<p class="hint">Opening this page does not use the invitation. ` +
		`It expires when you join.</p></section>`)
	s.sendHTML(w, 200, s.renderShell(page{crumbs: []crumb{{label: "Invitation", href: ""}}, narrow: true,
		content: b.String()}, nil))
}

// joinPreview is the honest advertisement: the count, and a few names.
//
// Serving techniques rather than a number matters — "14 techniques" is a claim
// and "Free a port with fuser -k" is the thing itself, and the second is what
// tells somebody whether this playbook is about work they do.
func (s *Server) joinPreview() string {
	techniques, err := s.Store.ListTechniques(servingStatuses, 0)
	if err != nil || len(techniques) == 0 {
		return `<p class="hint">This playbook has no techniques yet. ` +
			`New techniques are added as members use Tacit.</p>`
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<p><strong>%d technique%s</strong> in this playbook</p><ul class="join-preview">`,
		len(techniques), plural(len(techniques)))
	for i, t := range techniques {
		if i == joinPreviewMax {
			break
		}
		fmt.Fprintf(&b, `<li>%s</li>`, html.EscapeString(t.Name))
	}
	b.WriteString(`</ul>`)
	if len(techniques) > joinPreviewMax {
		fmt.Fprintf(&b, `<p class="hint">and %d more.</p>`, len(techniques)-joinPreviewMax)
	}
	return b.String()
}

// servingStatuses is what a visitor is shown: the techniques this registry
// actually serves to agents. Drafts and retired entries are the registry's own
// working state and are nobody's advertisement.
var servingStatuses = []string{"stable"}

const joinPreviewMax = 5

// handleJoinAccept redeems the invitation from the page: it mints the member
// key, and — where the registry has a door a member can use — signs them
// straight in with it.
//
// Signing in here is the point of the whole milestone. A member who accepts an
// invitation in a browser should be looking at the playbook a moment later, not
// reading instructions about how to look at it. Wiring their agent is the second
// thing they need and it is on the page below, where they can come back to it.
func (s *Server) handleJoinAccept(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	nonce, ok := verifyJoinToken(s.cfg().APIKey, token)
	if !ok {
		s.joinExpired(w, r)
		return
	}
	cachepolicy.MarkPrivate(w, r, "invitation redeemed")
	_ = r.ParseForm()
	secret, key, err := s.redeemJoinToken(nonce, r.PostFormValue("label"))
	switch {
	case errors.Is(err, errJoinSpent):
		s.joinSpent(w, r)
		return
	case err != nil:
		s.sendHTML(w, http.StatusInternalServerError, s.renderShell(page{
			status:  http.StatusInternalServerError,
			narrow:  true,
			content: `<p class="error">Could not mint a member key. Ask your colleague to try again.</p>`}, nil))
		return
	}
	label := key.Label
	// The session this response creates, kept so the page around it can be the
	// signed-in one. Reading it back off the request would find no cookie — the
	// browser has not been given one yet — and render "Sign in" chrome around a
	// page saying the reader is signed in.
	var signedIn oidc.Claims
	if s.cfg().TeamEnabled() && s.OIDC == nil {
		claims := session.Claims{
			"sub":   "member:" + key.ID,
			"name":  key.Label,
			"owner": false,
			"exp":   time.Now().Add(memberSessionTTL).Unix(),
		}
		s.setCookie(w, sessionCookie, s.ownerSigner().Pack(claims), int(memberSessionTTL.Seconds()))
		signedIn = oidc.Claims(claims)
	}
	base := s.externalBaseFor(r)
	// The key goes into a code, and the code goes on the page. The key itself
	// used to be printed here, in a command the member was told to carry to
	// another machine — a long-lived credential through a paste buffer, and on
	// a phone a secret for a computer they were not sitting at (handoff.go).
	code := s.newHandoff(secret, base)
	var b strings.Builder
	b.WriteString(`<section class="panel"><h2>You are in</h2>`)
	b.WriteString(`<div class="notice"><p><strong>Run this on the machine where your AI tools are.</strong> ` +
		`The code works once and expires in 15 minutes:</p>` +
		copyablePre(fmt.Sprintf("tacit connect --registry %s --code %s", base, code)) +
		`<p class="muted">On a phone? Leave this page open and type the code on your computer. ` +
		`If it expires, ask your colleague for a fresh invitation. No tacit installed yet? ` +
		html.EscapeString(ui.InstallCommandAt(s.requestBase(r))) + `</p></div>`)
	if signedIn != nil {
		b.WriteString(`<p>You are signed in here as <strong>` + html.EscapeString(label) +
			`</strong>. <a href="/techniques/map">Look at the playbook</a>.</p>`)
	}
	b.WriteString(`</section>`)
	s.sendHTML(w, 200, s.renderShell(page{crumbs: []crumb{{label: "Invitation", href: ""}}, narrow: true,
		content: b.String()}, signedIn))
}
