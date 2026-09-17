// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit"
	"github.com/opentacit/tacit/internal/ui"
)

func TestJoinLoop(t *testing.T) {
	srv, ts := newServer(t)

	// Minting requires the org key.
	resp, _ := request(t, "POST", ts.URL+"/v1/invite", "", "{}")
	if resp.StatusCode != 401 {
		t.Fatalf("unkeyed invite = %d, want 401", resp.StatusCode)
	}
	resp, body := request(t, "POST", ts.URL+"/v1/invite", "test-key", "{}")
	if resp.StatusCode != 200 {
		t.Fatalf("invite: %d %v", resp.StatusCode, body)
	}
	joinURL, _ := body["join_url"].(string)
	if !strings.Contains(joinURL, ts.URL+"/join/") {
		t.Fatalf("join_url = %q", joinURL)
	}

	// The join script is exactly the installer plus the join step, and never
	// contains the key.
	sresp, err := http.Get(joinURL)
	if err != nil {
		t.Fatal(err)
	}
	script, _ := io.ReadAll(sresp.Body)
	sresp.Body.Close()
	if sresp.StatusCode != 200 {
		t.Fatalf("join script: %d", sresp.StatusCode)
	}
	if !strings.Contains(string(script), "tacit\" join "+ts.URL+"/join/") {
		t.Errorf("script lacks the join step:\n%.300s", script)
	}
	if strings.Contains(string(script), srv.Cfg.APIKey) {
		t.Error("join script leaks the API key")
	}

	// Exchange mints a fresh member key — never the org root key.
	token := strings.TrimPrefix(joinURL, ts.URL+"/join/")
	resp, body = request(t, "POST", ts.URL+"/v1/join/exchange", "", `{"token":"`+token+`","label":"dana@laptop"}`)
	minted, _ := body["api_key"].(string)
	if resp.StatusCode != 200 || minted == "" || minted == "test-key" || body["registry_url"] != ts.URL {
		t.Fatalf("exchange: %d %v", resp.StatusCode, body)
	}

	// The minted key authenticates reads/search; revoking it (the Members
	// page action) shuts that machine out without touching anyone else.
	resp, _ = request(t, "GET", ts.URL+"/v1/techniques", minted, "")
	if resp.StatusCode != 200 {
		t.Fatalf("minted key rejected on read: %d", resp.StatusCode)
	}
	// But a member key must NOT reach the operator-only admin write surface:
	// promote, delete, edit, publish, subscriptions, sync, recompute, suggest.
	for _, ep := range []struct{ method, path string }{
		{"POST", "/v1/admin/promote"},
		{"POST", "/v1/admin/sync-techniques"},
		{"POST", "/v1/admin/recompute"},
		{"POST", "/v1/admin/suggest"},
		{"DELETE", "/v1/techniques/whatever"},
		{"POST", "/v1/admin/techniques/whatever"},
		{"POST", "/v1/admin/publish"},
		{"POST", "/v1/admin/subscriptions"},
		{"POST", "/v1/admin/poll-feeds"},
	} {
		if r, _ := request(t, ep.method, ts.URL+ep.path, minted, "{}"); r.StatusCode != 401 {
			t.Errorf("member key reached %s %s: got %d, want 401", ep.method, ep.path, r.StatusCode)
		}
		// The org root key still passes the gate (not 401; the handler may
		// then 4xx on the empty body, which is fine — auth is what we test).
		if r, _ := request(t, ep.method, ts.URL+ep.path, "test-key", "{}"); r.StatusCode == 401 {
			t.Errorf("root key rejected on %s %s", ep.method, ep.path)
		}
	}
	keys, err := srv.Store.ListMemberKeys()
	if err != nil || len(keys) != 1 || keys[0].Label != "dana@laptop" {
		t.Fatalf("member keys after join: %+v err=%v", keys, err)
	}
	if strings.Contains(strings.Join([]string{keys[0].Hash, keys[0].ID}, " "), minted) {
		t.Fatal("store holds the secret; it must hold only the hash")
	}
	resp, _ = request(t, "POST", ts.URL+"/members/revoke/"+keys[0].ID, "", "")
	if resp.StatusCode >= 400 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	resp, _ = request(t, "GET", ts.URL+"/v1/techniques", minted, "")
	if resp.StatusCode != 401 {
		t.Fatalf("revoked key still accepted: %d", resp.StatusCode)
	}
	// The org root key is untouched by the revocation.
	resp, _ = request(t, "GET", ts.URL+"/v1/techniques", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("root key broken by revocation: %d", resp.StatusCode)
	}

	// Garbage and expired tokens are rejected on both surfaces.
	resp, _ = request(t, "POST", ts.URL+"/v1/join/exchange", "", `{"token":"123.deadbeef"}`)
	if resp.StatusCode != 401 {
		t.Fatalf("forged exchange = %d, want 401", resp.StatusCode)
	}
	expired := mintJoinToken(srv.Cfg.APIKey, -time.Minute)
	if _, ok := verifyJoinToken(srv.Cfg.APIKey, expired); ok {
		t.Error("expired token verified")
	}
	sresp, err = http.Get(ts.URL + "/join/" + expired)
	if err != nil {
		t.Fatal(err)
	}
	sresp.Body.Close()
	if sresp.StatusCode != 404 {
		t.Fatalf("expired join script = %d, want 404", sresp.StatusCode)
	}
}

func TestMembersPageListsAndMints(t *testing.T) {
	srv, ts := newServer(t)
	// Mint via the form. The redirect carries a HANDOFF CODE, never the key:
	// a query string lands in browser history and a member key does not
	// expire, so what rides the redirect is worth fifteen minutes and one use.
	req, _ := http.NewRequest("POST", ts.URL+"/members/mint", strings.NewReader("label=erin%40desktop"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != 302 || !strings.HasPrefix(loc, "/members?code=") {
		t.Fatalf("mint: %d -> %q", resp.StatusCode, loc)
	}
	code := strings.TrimPrefix(loc, "/members?code=")
	if strings.Contains(loc, srv.Cfg.APIKey) {
		t.Fatal("the mint redirect carries a key")
	}
	// The code trades for a key that works, once.
	_, body := request(t, "POST", ts.URL+"/v1/connect/redeem", "", `{"code":"`+code+`"}`)
	secret, _ := body["api_key"].(string)
	if secret == "" {
		t.Fatalf("redeeming the mint code gave nothing: %v", body)
	}
	if r2, _ := request(t, "GET", ts.URL+"/v1/techniques", secret, ""); r2.StatusCode != 200 {
		t.Fatalf("minted key rejected: %d", r2.StatusCode)
	}
	// The page lists the key's label but never any secret.
	presp, err := http.Get(ts.URL + "/members")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(presp.Body)
	presp.Body.Close()
	if !strings.Contains(string(raw), "erin@desktop") {
		t.Error("members page does not list the minted key's label")
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), srv.Cfg.APIKey) {
		t.Error("members page leaks a secret")
	}

	// Deletion is two-step by design: refused while the key is active,
	// allowed once revoked.
	keys, _ := srv.Store.ListMemberKeys()
	if len(keys) != 1 {
		t.Fatalf("keys: %+v", keys)
	}
	id := keys[0].ID
	request(t, "POST", ts.URL+"/members/delete/"+id, "", "")
	if keys, _ = srv.Store.ListMemberKeys(); len(keys) != 1 {
		t.Fatal("active key was deleted without revocation")
	}
	request(t, "POST", ts.URL+"/members/revoke/"+id, "", "")
	request(t, "POST", ts.URL+"/members/delete/"+id, "", "")
	if keys, _ = srv.Store.ListMemberKeys(); len(keys) != 0 {
		t.Fatalf("revoked key not deleted: %+v", keys)
	}
	if r2, _ := request(t, "GET", ts.URL+"/v1/techniques", secret, ""); r2.StatusCode != 401 {
		t.Fatalf("deleted key still accepted: %d", r2.StatusCode)
	}
}

func TestMembersPageMintsWorkingLink(t *testing.T) {
	srv, ts := newServer(t)
	resp, err := http.Get(ts.URL + "/members")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("/members: %d", resp.StatusCode)
	}
	if strings.Contains(string(raw), srv.Cfg.APIKey) {
		t.Error("/members leaks the API key")
	}
	// The rendered token must actually pass verification.
	i := strings.Index(string(raw), "/join/")
	if i < 0 {
		t.Fatalf("/members renders no join link:\n%.300s", raw)
	}
	token := string(raw[i+len("/join/"):])
	if j := strings.IndexAny(token, " |<\"\n"); j >= 0 {
		token = token[:j]
	}
	if _, ok := verifyJoinToken(srv.Cfg.APIKey, token); !ok {
		t.Errorf("minted token %q does not verify", token)
	}
}

// The figures a published registry reports to the shared proxy are the same
// count its own Members page shows, over four windows. One counter, because a
// tenant and its host reading two different truths about the tenant is worse
// than either being slightly wrong — and because "how many people use this" is
// exactly the number somebody eventually gets billed for.
func TestReportedActivityMatchesTheMembersPage(t *testing.T) {
	srv, _ := newServer(t)
	now := time.Now().UTC()
	seen := func(label string, ago time.Duration) {
		_, k := NewMemberKey(label)
		k.LastSeen = now.Add(-ago).Format(time.RFC3339)
		if err := srv.Store.InsertMemberKey(k); err != nil {
			t.Fatal(err)
		}
	}
	seen("today", 2*time.Hour)
	seen("yesterday", 30*time.Hour)
	seen("last-week", 5*24*time.Hour)
	seen("last-month", 20*24*time.Hour)
	seen("last-year", 200*24*time.Hour)
	seen("ancient", 500*24*time.Hour)
	// Revoked keys count for nothing, whenever they were last seen.
	_, revoked := NewMemberKey("revoked")
	revoked.LastSeen = now.Format(time.RFC3339)
	revoked.RevokedAt = now.Format(time.RFC3339)
	if err := srv.Store.InsertMemberKey(revoked); err != nil {
		t.Fatal(err)
	}

	got := srv.MemberActivity()
	// Each window is machines seen in the period ENDING NOW, so they nest:
	// a day's actives are also in the week's, and so on.
	for _, tc := range []struct {
		window string
		got    int
		want   int
	}{
		{"day", got.Day, 1},
		{"week", got.Week, 3},
		{"month", got.Month, 4},
		{"year", got.Year, 5},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.window, tc.got, tc.want)
		}
	}
	if got.Day > got.Week || got.Week > got.Month || got.Month > got.Year {
		t.Errorf("the windows do not nest: %+v", got)
	}
	// And the week is the number the page prints, from the same function.
	if got.Week != srv.ActiveMemberMachines() {
		t.Errorf("the reported week (%d) and the Members page (%d) disagree",
			got.Week, srv.ActiveMemberMachines())
	}
}

// The manual-install line names the host the reader is on, and that address
// answers. An ingress carrying several zones serves one registry at more than
// one hostname, so a line hard-coded to the project's own zone sends a member
// somewhere they did not come from — and the whole point of the panel is that
// the command can be pasted without a decision.
func TestInstallerIsServedFromTheRegistrysOwnAddress(t *testing.T) {
	_, ts := newServer(t)

	resp, err := http.Get(ts.URL + "/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/install.sh = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/x-shellscript") {
		t.Errorf("Content-Type = %q, want a shell script", ct)
	}
	// The same bytes the join link serves, not a second copy.
	if !strings.Contains(string(script), "INSTALL_DIR") {
		t.Errorf("that is not the installer:\n%.200s", script)
	}

	// And the members panel quotes exactly that address rather than another
	// domain's.
	req, _ := http.NewRequest("GET", ts.URL+"/members", nil)
	req.Host = "registry.example.test"
	presp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(presp.Body)
	presp.Body.Close()
	if want := "http://registry.example.test/install.sh"; !strings.Contains(string(page), want) {
		t.Errorf("the members panel does not quote %q", want)
	}
	if strings.Contains(string(page), ui.InstallURL) {
		t.Error("the members panel still quotes the product's own install address")
	}
}

// The join link serves an installer, and install.sh ends by telling the reader
// how to start a registry of their own. For somebody who is joining one, that
// is the single worst next step available: a personal registry has to be folded
// back in with `tacit merge` before their outcomes count for the org. The
// registry sets a flag the script reads, and the footer stays behind.
func TestJoinInstallerDoesNotTellTheJoinerToStartTheirOwnRegistry(t *testing.T) {
	script := joiningInstallScript()
	if !strings.HasPrefix(script, "#!/bin/sh\n"+joinInstallerFlag+"\n") {
		t.Fatalf("the flag is not set under the shebang; script starts:\n%.80s", script)
	}
	if !strings.Contains(script, `[ -z "${TACIT_JOINING:-}" ]`) {
		t.Error("install.sh no longer guards its next-steps footer on TACIT_JOINING — " +
			"the join installer is back to advising `tacit init`")
	}
	// The plain installer keeps the footer: somebody who ran `curl | sh` with no
	// invitation is exactly the person that advice is for.
	if !strings.Contains(tacit.InstallScript, "start a registry for your org") {
		t.Error("the plain installer lost its next-steps footer")
	}
	if strings.Contains(tacit.InstallScript, joinInstallerFlag) {
		t.Error("the plain installer carries the joining flag; it must not")
	}
}

// The page promised "The invitation is spent when you join" and nothing
// implemented it: handleJoinAccept verified an HMAC and minted a member key on
// every POST, so one leaked link was an unlimited supply of memberships for as
// long as the token had left to run.
//
// It could not have been implemented as the token stood. The token was the
// expiry and its MAC, so two invitations minted in the same second were the
// same string and no invitation could be told from another.
func TestInvitationIsSpentOnce(t *testing.T) {
	srv, ts := newServer(t)

	// Two invitations minted together are distinguishable now.
	a := mintJoinToken(srv.Cfg.APIKey, time.Hour)
	b := mintJoinToken(srv.Cfg.APIKey, time.Hour)
	if a == b {
		t.Fatal("two invitations minted in the same second are the same token")
	}

	before := memberKeyCount(t, srv)

	// Looking is still free, however many times.
	for range 3 {
		resp, err := http.Get(ts.URL + "/join/" + a)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("preview: %d", resp.StatusCode)
		}
	}
	if n := memberKeyCount(t, srv); n != before {
		t.Errorf("previewing the invitation minted %d key(s)", n-before)
	}

	// The first redemption works.
	resp, body := request(t, "POST", ts.URL+"/v1/join/exchange", "", `{"token":"`+a+`","label":"dana@laptop"}`)
	if resp.StatusCode != 200 || body["api_key"] == "" {
		t.Fatalf("first exchange: %d %v", resp.StatusCode, body)
	}
	first := memberKeyCount(t, srv)
	if first != before+1 {
		t.Fatalf("first exchange minted %d keys, want 1", first-before)
	}

	// The second is refused, and mints nothing.
	resp, _ = request(t, "POST", ts.URL+"/v1/join/exchange", "", `{"token":"`+a+`","label":"someone-else"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second exchange = %d, want 409", resp.StatusCode)
	}
	if n := memberKeyCount(t, srv); n != first {
		t.Errorf("the refused exchange still minted %d key(s)", n-first)
	}

	// The same holds for the browser's button, and the page says used rather
	// than expired — the remedy is the same, the fact is not.
	resp, _ = request(t, "POST", ts.URL+"/join/"+b, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("browser accept: %d", resp.StatusCode)
	}
	afterB := memberKeyCount(t, srv)
	resp, _ = request(t, "POST", ts.URL+"/join/"+b, "", "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second browser accept = %d, want 409", resp.StatusCode)
	}
	if n := memberKeyCount(t, srv); n != afterB {
		t.Errorf("the refused accept still minted %d key(s)", n-afterB)
	}
}

// Tokens minted before the nonce cannot be redeemed once and only once, so
// they are not honoured at all. The longest outlives its minting by joinTTLMax.
func TestPreNonceTokensAreRefused(t *testing.T) {
	srv, ts := newServer(t)
	expiry := time.Now().Add(time.Hour).Unix()
	old := fmt.Sprintf("%d.%s", expiry, joinMAC(srv.Cfg.APIKey, expiry, ""))
	if _, ok := verifyJoinToken(srv.Cfg.APIKey, old); ok {
		t.Error("a two-part token verified")
	}
	resp, _ := request(t, "POST", ts.URL+"/v1/join/exchange", "", `{"token":"`+old+`"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("exchange of a pre-nonce token = %d, want 401", resp.StatusCode)
	}
}

func memberKeyCount(t *testing.T, srv *Server) int {
	t.Helper()
	keys, err := srv.Store.ListMemberKeys()
	if err != nil {
		t.Fatal(err)
	}
	return len(keys)
}

// The invitation page printed the member key itself, inside a command the
// member was told to run on another machine. It now prints a code that stands
// for the key: short-lived, single-use, and worth nothing once spent.
func TestBrowserJoinHandsOverACodeNotAKey(t *testing.T) {
	srv, ts := newServer(t)
	token := mintJoinToken(srv.Cfg.APIKey, time.Hour)

	resp, err := http.Post(ts.URL+"/join/"+token, "application/x-www-form-urlencoded",
		strings.NewReader("label=dana@laptop"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(raw)
	if resp.StatusCode != 200 {
		t.Fatalf("accept: %d", resp.StatusCode)
	}
	if !strings.Contains(page, "--code ") {
		t.Errorf("the page does not offer a handoff code:\n%.600s", page)
	}
	if strings.Contains(page, "--key ") {
		t.Error("the page still hands over a member key")
	}
	// Whatever secret the store now holds, its plaintext must not be here.
	keys, err := srv.Store.ListMemberKeys()
	if err != nil || len(keys) != 1 {
		t.Fatalf("member keys = %v (%v)", len(keys), err)
	}
	code := between(page, "--code ", "<")
	if code == "" {
		t.Fatalf("no code on the page:\n%.600s", page)
	}

	// The code redeems once, for a key that actually authenticates.
	resp, body := request(t, "POST", ts.URL+"/v1/connect/redeem", "", `{"code":"`+strings.TrimSpace(code)+`"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("redeem: %d %v", resp.StatusCode, body)
	}
	minted, _ := body["api_key"].(string)
	if minted == "" || minted == srv.Cfg.APIKey {
		t.Fatalf("redeem returned %q", minted)
	}
	resp, _ = request(t, "GET", ts.URL+"/v1/techniques", minted, "")
	if resp.StatusCode != 200 {
		t.Errorf("the redeemed key does not authenticate: %d", resp.StatusCode)
	}

	// And only once.
	resp, _ = request(t, "POST", ts.URL+"/v1/connect/redeem", "", `{"code":"`+strings.TrimSpace(code)+`"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("second redeem = %d, want 401", resp.StatusCode)
	}
}

// A code is retyped from a phone screen, so case and dashes must not decide
// whether it works — but a wrong code still has to fail.
func TestHandoffCodeNormalizes(t *testing.T) {
	srv, _ := newServer(t)
	code := srv.newHandoff("sk-parked", "https://example.test")
	if strings.Count(code, "-") != 2 || len(code) != handoffCodeLen+2 {
		t.Fatalf("code %q is not XXXX-XXXX-XXXX", code)
	}
	for _, ch := range []string{"I", "O", "0", "1"} {
		if strings.Contains(code, ch) {
			t.Errorf("code %q uses the ambiguous character %q", code, ch)
		}
	}
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	secret, base, ok := srv.redeemHandoff(typed)
	if !ok || secret != "sk-parked" || base != "https://example.test" {
		t.Errorf("redeeming %q gave %q %q %v", typed, secret, base, ok)
	}
	if _, _, ok := srv.redeemHandoff(code); ok {
		t.Error("the code redeemed twice")
	}
}

// An expired code is gone, and takes no key with it.
func TestHandoffCodeExpires(t *testing.T) {
	srv, _ := newServer(t)
	code := srv.newHandoff("sk-parked", "https://example.test")
	srv.handoffMu.Lock()
	e := srv.handoffs[code]
	e.expires = time.Now().Add(-time.Second)
	srv.handoffs[code] = e
	srv.handoffMu.Unlock()
	if _, _, ok := srv.redeemHandoff(code); ok {
		t.Error("an expired code redeemed")
	}
}

// between returns what sits between the first cut and the next stop after it.
func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	rest := s[i+len(from):]
	if j := strings.Index(rest, to); j >= 0 {
		return rest[:j]
	}
	return rest
}
