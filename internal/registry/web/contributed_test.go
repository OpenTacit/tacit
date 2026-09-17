// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/merge"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/session"
	"github.com/opentacit/tacit/internal/registry/store"
)

// ownerRequest is a page load by the one member who is signed in — the only
// viewer the merge control may be offered to.
func ownerRequest(s *Server) *http.Request {
	r := httptest.NewRequest("GET", "/techniques", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.ownerSigner().Pack(session.Claims{
		"sub": "owner", "owner": true, "exp": time.Now().Add(time.Hour).Unix()})})
	return r
}

// strangerRequest is anyone else.
func strangerRequest() *http.Request { return httptest.NewRequest("GET", "/techniques", nil) }

// destination stands in for the organization's registry: it answers
// GET /v1/techniques/<id> with a status, or 404 for a draft that is gone.
func destination(t *testing.T, statuses map[string]string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v1/techniques/")
		status, ok := statuses[id]
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "name": "Their name for " + id, "status": status})
	}))
	t.Cleanup(ts.Close)
	return ts
}

// ownerServerWithLedger is a single-member registry that has merged, with the
// ledger a merge would have left beside its settings.
func ownerServerWithLedger(t *testing.T, dest string, sent map[string]merge.Sent) *Server {
	t.Helper()
	dir := t.TempDir()
	envPath := filepath.Join(dir, "registry.env")
	if err := os.WriteFile(envPath, []byte("# test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", envPath)

	l, err := merge.OpenLedger(merge.LedgerPath(envPath))
	if err != nil {
		t.Fatal(err)
	}
	l.Destination, l.Key = dest, "member-key"
	for id, s := range sent {
		l.Sent[id] = s
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Load()
	cfg.DataDir = t.TempDir()
	cfg.AuthMode = config.AuthOwner
	cfg.OwnerSecret = "owner-secret-for-the-test"
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	embedder, _ := embed.New(cfg.EmbedModel, cfg.EmbedDim)
	return New(cfg, st, embedder, "")
}

// waitForPanel gives the background refresh a moment: the page never blocks on
// the network, so the answer arrives on a later render.
func waitForPanel(t *testing.T, s *Server, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	panel := ""
	for time.Now().Before(deadline) {
		panel = s.contributedPanel(ownerRequest(s))
		// The panel is HTML, so what it says is escaped: "organization's"
		// renders as "organization&#39;s" and a raw comparison never matches.
		if strings.Contains(panel, html.EscapeString(want)) {
			return panel
		}
		time.Sleep(20 * time.Millisecond)
	}
	return panel
}

func TestContributedPanelSaysWhatBecameOfEachTechnique(t *testing.T) {
	dest := destination(t, map[string]string{
		"took-it":    "stable",
		"reading-it": "draft",
		"declined":   "retired",
	})
	s := ownerServerWithLedger(t, dest.URL, map[string]merge.Sent{
		"took-it":    {Destination: dest.URL, DraftID: "took-it"},
		"reading-it": {Destination: dest.URL, DraftID: "reading-it"},
		"declined":   {Destination: dest.URL, DraftID: "declined"},
		"vanished":   {Destination: dest.URL, DraftID: "vanished"},
	})

	// The first render must not wait on the network; it says it is asking.
	first := s.contributedPanel(ownerRequest(s))
	if !strings.Contains(first, "Checking the destination registry now") {
		t.Errorf("first render = %q, want it to say the answer is being fetched", first)
	}
	if strings.Contains(first, "could not be read") {
		t.Error("a technique nobody has asked about yet was reported as unreadable")
	}

	panel := waitForPanel(t, s, "in your organization's playbook")
	for _, want := range []string{
		"in your organization's playbook", // promoted
		"waiting for review",              // still a draft there
		"not taken",                       // rejected, which the lifecycle calls "retired"
		"no longer there",                 // the draft is gone
	} {
		if !strings.Contains(panel, html.EscapeString(want)) {
			t.Errorf("panel does not say %q:\n%s", want, panel)
		}
	}
	// A contributor is told the decision, not the mechanism that carried it.
	if strings.Contains(panel, "retired") {
		t.Error("the panel calls a declined technique 'retired', which describes the lifecycle rather than the answer")
	}
	if !strings.Contains(panel, "Last asked") {
		t.Error("the panel does not say how old its answer is")
	}
}

// An organization's registry contributes to nobody and must show nothing,
// whatever happens to be on the disk beside it.
func TestContributedPanelIsOnlyForASingleMemberRegistry(t *testing.T) {
	dest := destination(t, map[string]string{"took-it": "stable"})
	s := ownerServerWithLedger(t, dest.URL, map[string]merge.Sent{
		"took-it": {Destination: dest.URL, DraftID: "took-it"},
	})
	s.Cfg.AuthMode = config.AuthOpen
	s.Cfg.OwnerSecret = ""
	if panel := s.contributedPanel(ownerRequest(s)); panel != "" {
		t.Errorf("an organization's registry rendered the panel:\n%s", panel)
	}
}

// A registry that has never merged offers the merge — to its owner, and to
// nobody else. The control hands this registry's whole playbook to an address
// the viewer chooses, so it is not something to render for a stranger.
func TestAnUnmergedRegistryOffersTheMergeToItsOwnerOnly(t *testing.T) {
	s := ownerServerWithLedger(t, "", nil)
	owner := s.contributedPanel(ownerRequest(s))
	if !strings.Contains(owner, `action="/admin/merge"`) {
		t.Errorf("the owner is not offered the merge:\n%s", owner)
	}
	if !strings.Contains(owner, `name="csrf"`) {
		t.Error("the merge form carries no CSRF token")
	}
	// One field: the join link is the whole credential.
	if strings.Contains(owner, `name="key"`) || strings.Contains(owner, `name="registry"`) {
		t.Error("the form asks for a credential beyond the join link")
	}
	if panel := s.contributedPanel(strangerRequest()); panel != "" {
		t.Errorf("a viewer who is not the owner was offered the merge:\n%s", panel)
	}
}

// Showing one organization's answers under another's name would be a lie.
func TestAChangedDestinationDropsTheOldAnswers(t *testing.T) {
	first := destination(t, map[string]string{"took-it": "stable"})
	s := ownerServerWithLedger(t, first.URL, map[string]merge.Sent{
		"took-it": {Destination: first.URL, DraftID: "took-it"},
	})
	if panel := waitForPanel(t, s, "in your organization's playbook"); !strings.Contains(panel, html.EscapeString("in your organization's playbook")) {
		t.Fatalf("first destination never answered:\n%s", panel)
	}

	second := destination(t, map[string]string{"took-it": "draft"})
	l, err := merge.OpenLedger(merge.LedgerPath(config.RegistryEnvPath()))
	if err != nil {
		t.Fatal(err)
	}
	l.Destination = second.URL
	l.Sent = map[string]merge.Sent{"took-it": {Destination: second.URL, DraftID: "took-it"}}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	next := s.contributedPanel(ownerRequest(s))
	if strings.Contains(next, html.EscapeString("in your organization's playbook")) {
		t.Errorf("the old organization's answer was shown under the new one's name:\n%s", next)
	}
	if panel := waitForPanel(t, s, "waiting for review"); !strings.Contains(panel, "waiting for review") {
		t.Errorf("the new destination's answer never arrived:\n%s", panel)
	}
}

// The merge control hands a whole playbook to an address the poster chooses, so
// its gates matter more than its happy path (which hack/merge-check exercises
// against a real organization).
func TestMergeStartRefusesWhatItShould(t *testing.T) {
	dest := destination(t, nil)
	s := ownerServerWithLedger(t, "", nil)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	post := func(t *testing.T, cookie *http.Cookie, form string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", ts.URL+"/admin/merge", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	owner := &http.Cookie{Name: sessionCookie, Value: s.ownerSigner().Pack(session.Claims{
		"sub": "owner", "owner": true, "exp": time.Now().Add(time.Hour).Unix()})}
	csrf := s.csrfToken(ownerRequest(s))

	if code := post(t, nil, "link="+url.QueryEscape(dest.URL+"/join/tok")); code == http.StatusSeeOther {
		t.Error("a signed-out visitor started a merge")
	}
	if code := post(t, owner, "link="+url.QueryEscape(dest.URL+"/join/tok")); code != http.StatusForbidden {
		t.Errorf("a post with no CSRF token = %d, want 403", code)
	}
	if code := post(t, owner, "csrf="+csrf+"&link=https://example.com/not-a-join-link"); code != http.StatusBadRequest {
		t.Errorf("a link that is not a join link = %d, want 400", code)
	}

	// And an organization's registry is not one member's to contribute.
	s.Cfg.AuthMode = config.AuthOpen
	s.Cfg.OwnerSecret = ""
	if code := post(t, owner, "csrf="+csrf+"&link="+url.QueryEscape(dest.URL+"/join/tok")); code != http.StatusForbidden {
		t.Errorf("an organization's registry accepted a merge = %d, want 403", code)
	}
}

// actionLog records what the finish did. The handler performs the wiring and the
// stop on a goroutine of its own — the response has to be on the wire before the
// registry stops serving — so the test reads this while that goroutine writes it.
type actionLog struct {
	mu   sync.Mutex
	done []string
}

func (l *actionLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.done = append(l.done, s)
}

func (l *actionLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.done)
}

// finishHarness is a merged registry that can stop itself, with the harness
// wiring and the stop both recorded rather than performed.
func finishHarness(t *testing.T) (*Server, *httptest.Server, *actionLog) {
	t.Helper()
	dest := destination(t, map[string]string{"took-it": "stable"})
	s := ownerServerWithLedger(t, dest.URL, map[string]merge.Sent{
		"took-it": {Destination: dest.URL, DraftID: "took-it"},
	})
	did := &actionLog{}
	s.WireHarnesses = func(destination, key string) error {
		did.add("wired:" + destination)
		return nil
	}
	s.RequestStop = func(why string) { did.add("stopped:" + why) }
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, did
}

func postFinish(t *testing.T, s *Server, ts *httptest.Server, form string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/admin/merge/finish", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.ownerSigner().Pack(session.Claims{
		"sub": "owner", "owner": true, "exp": time.Now().Add(time.Hour).Unix()})})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestFinishingFromThePageWiresStopsAndSaysSo(t *testing.T) {
	s, ts, did := finishHarness(t)
	csrf := s.csrfToken(ownerRequest(s))

	code, body := postFinish(t, s, ts, "csrf="+csrf+"&confirm=yes")
	if code != 200 {
		t.Fatalf("finish = %d, want 200\n%s", code, body)
	}
	if !strings.Contains(body, "Merged into") {
		t.Errorf("the last page this registry serves does not say what happened:\n%s", body)
	}
	// It must not claim the machine was wired when it was not, and must not
	// claim an address was released when there never was one.
	if !strings.Contains(body, "no public address to release") {
		t.Errorf("a registry with no address did not say so:\n%s", body)
	}

	deadline := time.Now().Add(3 * time.Second)
	acted := did.all()
	for time.Now().Before(deadline) && len(acted) < 2 {
		time.Sleep(20 * time.Millisecond)
		acted = did.all()
	}
	if len(acted) < 2 || !strings.HasPrefix(acted[0], "wired:") {
		t.Fatalf("actions = %v, want the harnesses wired first", acted)
	}
	if !strings.HasPrefix(acted[1], "stopped:") {
		t.Errorf("actions = %v, want the registry stopped after", acted)
	}
}

// Everything past the confirmation is one-way, so an unconfirmed post must do
// none of it.
func TestFinishNeedsTheConfirmation(t *testing.T) {
	s, ts, did := finishHarness(t)
	csrf := s.csrfToken(ownerRequest(s))
	if code, _ := postFinish(t, s, ts, "csrf="+csrf); code != http.StatusSeeOther {
		t.Errorf("unconfirmed finish = %d, want a redirect back", code)
	}
	time.Sleep(200 * time.Millisecond)
	if acted := did.all(); len(acted) != 0 {
		t.Errorf("an unconfirmed post did %v", acted)
	}
}

// A registry that cannot stop itself must not release its address either: the
// alternative is a live process serving on a name that resolves to nothing.
func TestARegistryThatCannotStopItselfWillNotRetireItself(t *testing.T) {
	s, ts, _ := finishHarness(t)
	s.RequestStop = nil
	csrf := s.csrfToken(ownerRequest(s))
	code, body := postFinish(t, s, ts, "csrf="+csrf+"&confirm=yes")
	if code != http.StatusConflict {
		t.Fatalf("finish without a way to stop = %d, want 409\n%s", code, body)
	}
	if !strings.Contains(body, "tacit merge --yes") {
		t.Errorf("the refusal does not say how to finish instead: %s", body)
	}
	// And the panel offers the command rather than a button that would refuse.
	panel := s.contributedPanel(ownerRequest(s))
	if strings.Contains(panel, `action="/admin/merge/finish"`) {
		t.Error("a registry that cannot stop itself offered the finish button")
	}
	if !strings.Contains(panel, "tacit merge --yes") {
		t.Errorf("it did not name the command instead:\n%s", panel)
	}
}

// The ordering of the release turns on this: a member watching over the tunnel
// is watching over the connection the release destroys.
func TestSameHostRecognisesTheTunnelledRequest(t *testing.T) {
	cases := []struct {
		host, base string
		want       bool
	}{
		{"cedar-hollow.tacit.zone", "https://cedar-hollow.tacit.zone", true},
		{"cedar-hollow.tacit.zone:443", "https://cedar-hollow.tacit.zone", true},
		{"CEDAR-HOLLOW.tacit.zone", "https://cedar-hollow.tacit.zone/", true},
		{"127.0.0.1:8081", "https://cedar-hollow.tacit.zone", false},
		{"localhost:8081", "", false},
	}
	for _, c := range cases {
		if got := sameHost(c.host, c.base); got != c.want {
			t.Errorf("sameHost(%q, %q) = %v, want %v", c.host, c.base, got, c.want)
		}
	}
}

// The finish button is the owner's, like the merge itself.
func TestFinishOfferIsForTheOwnerOnly(t *testing.T) {
	s, _, _ := finishHarness(t)
	if !strings.Contains(s.contributedPanel(ownerRequest(s)), `action="/admin/merge/finish"`) {
		t.Error("the owner is not offered the finish")
	}
	if strings.Contains(s.contributedPanel(strangerRequest()), "/admin/merge/finish") {
		t.Error("a stranger was offered the finish")
	}
}
