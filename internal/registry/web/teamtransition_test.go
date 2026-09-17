// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

// The browser-led move from one member to a team
// (docs/design/browser-led-team-transition.md). One test per milestone's
// load-bearing behaviour, named for the behaviour rather than the handler.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/merge"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/session"
	"github.com/opentacit/tacit/pkg/contracts"
)

// get fetches a URL with an Accept header, following no redirects, and returns
// the status, content type and body.
func fetch(t *testing.T, url, accept string, cookies ...*http.Cookie) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(body)
}

func postForm(t *testing.T, url, form string, cookies ...*http.Cookie) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(body)
}

func ownerSession(s *Server) *http.Cookie {
	return &http.Cookie{Name: sessionCookie, Value: s.ownerSigner().Pack(session.Claims{
		"sub": "owner", "owner": true, "exp": time.Now().Add(time.Hour).Unix()})}
}

// M4. The same link has to serve two clients, and it must not spend the
// invitation on anyone who merely looks at it.
func TestAJoinLinkServesAScriptToCurlAndAPageToABrowser(t *testing.T) {
	srv, ts := newServer(t)
	token := mintJoinToken(srv.Cfg.APIKey, time.Hour)
	link := ts.URL + "/join/" + token

	// curl sends */* or nothing at all, and must keep getting the installer.
	code, ctype, body := fetch(t, link, "*/*")
	if code != 200 || !strings.Contains(ctype, "shellscript") {
		t.Fatalf("curl got %d %q, want 200 and a shell script", code, ctype)
	}
	if !strings.Contains(body, `tacit" join `) {
		t.Error("the installer lost its join step")
	}

	// A browser gets the invitation.
	code, ctype, body = fetch(t, link, "text/html,application/xhtml+xml")
	if code != 200 || !strings.Contains(ctype, "text/html") {
		t.Fatalf("browser got %d %q, want 200 and HTML", code, ctype)
	}
	if strings.Contains(body, "INSTALL_DIR") {
		t.Error("a browser was served the installer")
	}
	if !strings.Contains(body, `action="/join/`) {
		t.Error("the invitation page has no way to accept it")
	}

	// Looking does not spend it: the token still works afterwards, and no
	// member key was minted by the page load.
	if keys, _ := srv.Store.ListMemberKeys(); len(keys) != 0 {
		t.Fatalf("looking at the invitation minted %d member keys", len(keys))
	}
	if code, _, _ = fetch(t, link, "text/html"); code != 200 {
		t.Errorf("the link stopped working after being looked at: %d", code)
	}
}

// M4. Accepting is the POST, and it hands over the credential exactly once.
func TestAcceptingAnInvitationInTheBrowserMintsOneKey(t *testing.T) {
	srv, ts := newServer(t)
	token := mintJoinToken(srv.Cfg.APIKey, time.Hour)

	code, _, body := postForm(t, ts.URL+"/join/"+token, "label=dana%40laptop")
	if code != 200 {
		t.Fatalf("accepting = %d, want 200:\n%.400s", code, body)
	}
	if !strings.Contains(body, "tacit connect --registry") {
		t.Error("the page does not tell the member how to wire their machine")
	}
	keys, err := srv.Store.ListMemberKeys()
	if err != nil || len(keys) != 1 {
		t.Fatalf("member keys after accepting = %d (%v), want 1", len(keys), err)
	}
	if keys[0].Label != "dana@laptop" {
		t.Errorf("key label = %q, want the name the member typed", keys[0].Label)
	}
	// An expired or forged token is refused in the medium it was asked for.
	code, _, body = postForm(t, ts.URL+"/join/not-a-token", "")
	if code != http.StatusNotFound || !strings.Contains(body, "expired") {
		t.Errorf("a forged token = %d, want a 404 page that says so", code)
	}
}

// M6/M7. Opening the registry is what gives a colleague a way in: before it,
// there is no door but the owner's; after it, a member key is one.
func TestOpeningToATeamTurnsMemberKeysIntoAWayIn(t *testing.T) {
	s := ownerServerWithLedger(t, "", nil)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	owner := ownerSession(s)

	// Before: /team carries the decision, and /members sends its readers there
	// rather than being a second door to the same thing.
	code, _, body := fetch(t, ts.URL+"/team", "text/html", owner)
	if code != 200 || !strings.Contains(body, "What changes when you invite somebody") {
		t.Fatalf("team before opening = %d, want the consequences panel:\n%.400s", code, body)
	}
	if code, _, _ := fetch(t, ts.URL+"/members", "text/html", owner); code != http.StatusFound {
		t.Errorf("/members on a single-member registry = %d, want a redirect to /team", code)
	}
	// The evidence decision is stated before the button, not after it.
	if !strings.Contains(body, "become part of the organization’s figures") {
		t.Error("the panel does not say what happens to the founder's figures")
	}
	if s.Cfg.TeamEnabled() {
		t.Fatal("a single-member registry already accepted member sign-ins")
	}

	csrf := s.csrfToken(ownerRequest(s))
	code, loc, body := postForm(t, ts.URL+"/members/open", "csrf="+csrf, owner)
	if code != http.StatusSeeOther {
		t.Fatalf("opening = %d, want a redirect:\n%.300s", code, body)
	}
	if loc != "/team" {
		t.Errorf("redirected to %q, want the page that says where the registry now stands", loc)
	}
	if !s.Cfg.TeamEnabled() || s.Cfg.SingleMember() {
		t.Fatalf("after opening: team=%v singleMember=%v", s.Cfg.TeamEnabled(), s.Cfg.SingleMember())
	}
	// The setting is on disk, so a restart keeps the door open.
	if config.Load().AuthMode != config.AuthTeam {
		t.Error("the mode was applied but not written to registry.env")
	}

	// And now a member key signs its holder in — as a reader, never an admin.
	secret, key := NewMemberKey("dana@laptop")
	if err := s.Store.InsertMemberKey(key); err != nil {
		t.Fatal(err)
	}
	code, _, body = postForm(t, ts.URL+"/auth/key", "key="+secret)
	if code != http.StatusFound {
		t.Fatalf("member sign-in = %d, want a redirect into the dashboard:\n%.300s", code, body)
	}
	claims := s.ownerSigner().Unpack(memberSessionCookie(t, s, secret))
	if claims == nil || claims["owner"] == true {
		t.Errorf("a member session claims %v, want a session that is not the owner's", claims)
	}
	if s.isAdmin(oidc.Claims(claims)) {
		t.Error("a member key made its holder an admin")
	}
}

// memberSessionCookie signs a key in and returns the session value it was given.
func memberSessionCookie(t *testing.T, s *Server, secret string) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/auth/key", strings.NewReader("key="+secret))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.handleMemberSignIn(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("no session cookie was set")
	return ""
}

// M6. The door only exists where it is meant to: a registry with one member has
// nobody to let in, and a wrong key is refused without explaining itself.
func TestMemberSignInIsRefusedWhereItShouldBe(t *testing.T) {
	s := ownerServerWithLedger(t, "", nil)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	secret, key := NewMemberKey("dana@laptop")
	if err := s.Store.InsertMemberKey(key); err != nil {
		t.Fatal(err)
	}
	// Single-member: the route is not there at all.
	if code, _, _ := postForm(t, ts.URL+"/auth/key", "key="+secret); code != http.StatusNotFound {
		t.Errorf("member sign-in on a single-member registry = %d, want 404", code)
	}

	s.Cfg.AuthMode = config.AuthTeam
	if code, _, _ := postForm(t, ts.URL+"/auth/key", "key=not-a-key"); code != http.StatusForbidden {
		t.Errorf("a wrong key = %d, want 403", code)
	}
	// The org root key is the operator's credential and is not a member door.
	if code, _, _ := postForm(t, ts.URL+"/auth/key", "key="+s.Cfg.APIKey); code != http.StatusForbidden {
		t.Errorf("the root key signed somebody in = %d, want 403", code)
	}
	// A revoked key stops working.
	if err := s.Store.SetMemberKeyRevoked(key.ID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := postForm(t, ts.URL+"/auth/key", "key="+secret); code != http.StatusForbidden {
		t.Errorf("a revoked key still signed in = %d, want 403", code)
	}
}

// M7. Opening a registry is the owner's decision and nobody else's.
func TestOnlyTheOwnerCanOpenTheRegistryToATeam(t *testing.T) {
	s := ownerServerWithLedger(t, "", nil)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	if code, _, _ := postForm(t, ts.URL+"/members/open", "csrf=whatever"); code == http.StatusSeeOther {
		t.Error("a signed-out visitor opened the registry to a team")
	}
	if s.Cfg.TeamEnabled() {
		t.Fatal("the registry was opened by somebody with no session")
	}
	// A stranger is not even shown the control.
	if strings.Contains(s.openToTeamPanel(strangerRequest()), `action="/members/open"`) {
		t.Error("the invite control was rendered to a visitor")
	}
	// And the owner's own post needs its CSRF token.
	if code, _, _ := postForm(t, ts.URL+"/members/open", "csrf=stale", ownerSession(s)); code != http.StatusForbidden {
		t.Errorf("a post with a stale CSRF token = %d, want 403", code)
	}
}

// M5. One page answers "where am I in this", and the states are distinguishable.
func TestTeamPageAnswersWhereTheMemberStands(t *testing.T) {
	s := ownerServerWithLedger(t, "", nil)
	alone := s.pageTeam(ownerRequest(s), nil).content
	if !strings.Contains(alone, "Single-member registry") {
		t.Errorf("a registry with one member does not say so:\n%.400s", alone)
	}
	// The way to invite somebody is ON this page — the control, not a link to
	// another page carrying it.
	if !strings.Contains(alone, `action="/members/open"`) {
		t.Error("the alone state offers no way to invite anybody")
	}
	if strings.Contains(alone, `href="/members"`) {
		t.Error("the alone state links to a page that redirects back to it")
	}

	s.Cfg.AuthMode = config.AuthTeam
	opened := s.pageTeam(ownerRequest(s), nil).content
	if !strings.Contains(opened, "Your team’s playbook") {
		t.Errorf("an opened registry still reads as one member's:\n%.400s", opened)
	}
	if strings.Contains(opened, "You are the only one here") {
		t.Error("an opened registry still offers to be opened")
	}
	// The merged page: who is here AND the surface for adding the next person,
	// which used to be a second destination in the same menu.
	for _, want := range []string{"Add a member", "Share a join link", "Mint a key by hand", "Access"} {
		if !strings.Contains(opened, want) {
			t.Errorf("the team page is missing the members surface: %q", want)
		}
	}
	if strings.Contains(opened, `href="/members"`) {
		t.Error("the team page links to a page that redirects back to it")
	}
}

// M5. The Playbook page keeps a pointer, so a member who read their standings
// there is not left hunting for them.
func TestThePlaybookPagePointsAtTheTeamPage(t *testing.T) {
	s := ownerServerWithLedger(t, "", nil)
	if p := s.teamPointer(ownerRequest(s)); !strings.Contains(p, `href="/team"`) {
		t.Errorf("playbook pointer = %q, want a link to /team", p)
	}
	if p := s.teamPointer(strangerRequest()); p != "" {
		t.Errorf("a visitor was shown %q", p)
	}
}

// orgDestination stands in for the organization being contributed to: it
// redeems a join token, answers what it has near a technique, and takes drafts.
// nearest maps a technique name to the similarity it reports back.
func orgDestination(t *testing.T, nearest map[string]float64) (*httptest.Server, *[]string) {
	t.Helper()
	var took []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/join/exchange", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"registry_url": "http://" + r.Host, "api_key": "their-member-key"})
	})
	mux.HandleFunc("POST /v1/evidence", func(w http.ResponseWriter, r *http.Request) {
		var ch contracts.Characterization
		_ = json.NewDecoder(r.Body).Decode(&ch)
		block := contracts.EvidenceBlock{}
		for name, sim := range nearest {
			if strings.Contains(ch.SummaryText, name) {
				block.Candidates = append(block.Candidates, contracts.EvidenceCandidate{
					TechniqueID: "theirs", Name: "Their " + name, Similarity: sim})
			}
		}
		_ = json.NewEncoder(w).Encode(block)
	})
	mux.HandleFunc("POST /v1/contribute", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		name, _ := body["name"].(string)
		took = append(took, name)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "draft-1"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &took
}

// M8. The form no longer contributes: it shows what the organization already
// has, and the member ticks what goes.
func TestContributingShowsWhatTheOrganizationAlreadyHasAndSendsOnlyWhatIsTicked(t *testing.T) {
	s := ownerServerWithLedger(t, "", nil)
	for _, tq := range []models.Technique{
		{ID: "port-kill", Name: "Free a port with fuser", Description: "d", Status: "stable",
			Scope: "general", CreatedAt: time.Now().UTC().Format(time.RFC3339)},
		{ID: "render-check", Name: "Render the page headlessly", Description: "d", Status: "stable",
			Scope: "general", CreatedAt: time.Now().UTC().Format(time.RFC3339)},
	} {
		if err := s.Store.UpsertTechnique(tq); err != nil {
			t.Fatal(err)
		}
	}
	// They already have something very like the first, and nothing like the second.
	dest, took := orgDestination(t, map[string]float64{"Free a port with fuser": 0.95})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	owner, csrf := ownerSession(s), s.csrfToken(ownerRequest(s))

	code, _, body := postForm(t, ts.URL+"/admin/merge",
		"csrf="+csrf+"&link="+url.QueryEscape(dest.URL+"/join/tok"), owner)
	if code != 200 {
		t.Fatalf("preview = %d, want the table:\n%.500s", code, body)
	}
	if !strings.Contains(body, "Free a port with fuser") || !strings.Contains(body, "Render the page headlessly") {
		t.Fatalf("the preview does not list both techniques:\n%.800s", body)
	}
	if !strings.Contains(body, "they have <strong>Their Free a port with fuser</strong>") {
		t.Errorf("the near-duplicate is not shown as one:\n%.800s", body)
	}
	if !strings.Contains(body, "nothing like it") {
		t.Error("the technique they do not have is not marked as new")
	}
	// Nothing has been contributed by looking.
	if len(*took) != 0 {
		t.Fatalf("the preview contributed %v", *took)
	}
	// The key is banked before anything is sent, so a member who closes the tab
	// can come back without a second invitation.
	l, err := merge.OpenLedger(merge.LedgerPath(config.RegistryEnvPath()))
	if err != nil || l.Key == "" {
		t.Fatalf("the redeemed key was not recorded: %v", err)
	}

	// Tick only the new one.
	code, loc, body := postForm(t, ts.URL+"/admin/merge/contribute",
		"csrf="+csrf+"&id=render-check", owner)
	if code != http.StatusSeeOther || loc != "/team" {
		t.Fatalf("contribute = %d %q:\n%.300s", code, loc, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.mergeRunning() {
		time.Sleep(20 * time.Millisecond)
	}
	if len(*took) != 1 || (*took)[0] != "Render the page headlessly" {
		t.Errorf("the organization received %v, want only the ticked technique", *took)
	}
	// The one left behind is recorded as held, so the table does not offer it
	// again every time it is opened.
	l, _ = merge.OpenLedger(merge.LedgerPath(config.RegistryEnvPath()))
	if _, held := l.Held["port-kill"]; !held {
		t.Errorf("the unticked technique was not recorded as held: %v", l.Held)
	}
	if _, held := l.Held["render-check"]; held {
		t.Error("a contributed technique was recorded as held")
	}
}

// The people who can reach this playbook are ONE entry in the Settings
// switcher, under the name that describes it here. Two entries — "Team" and
// "Members" together — asked the reader to resolve a distinction that did not
// exist.
func TestOneMenuEntryForWhoCanReachThisRegistry(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"one member", config.Config{AuthMode: config.AuthOwner, OwnerSecret: "s"}, "/team"},
		{"opened to a team", config.Config{AuthMode: config.AuthTeam, OwnerSecret: "s"}, "/team"},
		{"an organization", config.Config{AuthMode: config.AuthOIDC}, "/members"},
		{"a pilot with no sign-in", config.Config{}, "/members"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{Cfg: tc.cfg}
			var hrefs []string
			for _, o := range s.registryViews("people") {
				hrefs = append(hrefs, o.href)
			}
			// Exactly one of the two, never both.
			other := map[string]string{"/team": "/members", "/members": "/team"}[tc.want]
			var got, both bool
			for _, h := range hrefs {
				got = got || h == tc.want
				both = both || h == other
			}
			if !got || both {
				t.Errorf("the switcher offers %v, want %s and not %s", hrefs, tc.want, other)
			}
			// The avatar menu holds the section, not its pages: one entry, and
			// it lands on General (registryViews).
			menu := s.accountMenu()
			if strings.Contains(menu, `href="`+tc.want+`"`) {
				t.Error("the people page is still its own menu entry")
			}
			if !strings.Contains(menu, `href="/settings">Settings<`) {
				t.Errorf("the shared menu does not lead to Settings:\n%s", menu)
			}
		})
	}
}

// And the page itself is reachable by typing the address, so it says which door
// it is not rather than rendering an empty column.
func TestTeamPageOnAnOrganizationsRegistrySaysSo(t *testing.T) {
	s := ownerServerWithLedger(t, "", nil)
	s.Cfg.AuthMode = config.AuthOIDC
	s.Cfg.OwnerSecret = ""
	content := s.pageTeam(ownerRequest(s), nil).content
	if !strings.Contains(content, "already belongs to a team") {
		t.Errorf("an organization's registry renders:\n%.300s", content)
	}
	if strings.Contains(content, "Single-member registry") {
		t.Error("an organization's registry was offered the single-member transition")
	}
}
