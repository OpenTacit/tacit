// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/url"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

// seedRanked makes a technique that qualifies for the commons, with the evidence.
func seedRanked(t *testing.T, srv *Server, id string, helped, adopted float64) {
	t.Helper()
	c := models.Technique{
		ID: id, Name: "Name " + id, Description: "d", Recipe: "recipe " + id,
		Scope: "general", Status: "stable", Provenance: "curated", Version: 1,
		CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}
	if err := srv.Store.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	var rows []models.Outcome
	techniques, _ := srv.Store.ListTechniques(nil, 0)
	for _, existing := range techniques {
		if o, ok, err := srv.Store.GetOutcome(existing.ID, "__overall__"); err == nil && ok {
			rows = append(rows, o)
		}
	}
	rate := helped / adopted
	rows = append(rows, models.Outcome{
		TechniqueID: id, SegmentKey: "__overall__",
		Helped: int(helped), Adopted: int(adopted),
		WeightedHelped: helped, WeightedAdopted: adopted,
		HelpedRate: &rate, SampleSize: int(adopted), LastUpdated: models.Now(),
	})
	if err := srv.Store.ReplaceOutcomes(rows); err != nil {
		t.Fatal(err)
	}
}

// The Public section shows the evidence that produced the ranking, so an operator
// can see WHY a technique sits where it does without reading the design.
func TestFederationPageShowsTheRankingEvidence(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, true
	seedRanked(t, srv, "proven", 40, 50)
	seedRanked(t, srv, "thinner", 6, 8)
	srv.RecomputePublicChannel()

	_, fed := fetchHTML(t, ts.URL+"/federation")
	for _, want := range []string{
		`id="public"`, "⟳ computed", "/f/public/feed.json",
		"bound",   // the column that actually orders them
		"entered", // stickiness is otherwise invisible and looks like a bug
		"Name proven",
	} {
		if !strings.Contains(fed, want) {
			t.Errorf("Public section missing %q", want)
		}
	}
	// The computed channel has no Unpublish button, and says why instead.
	if !strings.Contains(fed, "nothing to publish here by hand") {
		t.Error("the absence of a publish control is not explained in place")
	}
	// Ordered by the bound: the better-evidenced technique comes first in the markup.
	if i, j := strings.Index(fed, "Name proven"), strings.Index(fed, "Name thinner"); i < 0 || j < 0 || i > j {
		t.Errorf("ranking order in the page is proven=%d thinner=%d; want proven first", i, j)
	}
}

// The staged state has to show the list and say plainly that nothing has left.
func TestFederationPageStagedSaysNothingHasLeft(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, false
	seedRanked(t, srv, "proven", 40, 50)
	srv.RecomputePublicChannel()

	_, fed := fetchHTML(t, ts.URL+"/federation")
	if !strings.Contains(fed, "confirmation needed") {
		t.Error("a staged registry does not say the feed is unserved")
	}
	if !strings.Contains(fed, "Nothing left the registry") {
		t.Error("a staged registry does not say plainly that nothing has been shared")
	}
	// And the techniques are still listed — consent needs the actual list.
	if !strings.Contains(fed, "Name proven") {
		t.Error("the staged list does not show what would be contributed")
	}
	// The feed URL is not offered, because it does not answer.
	if strings.Contains(fed, `href="/f/public/feed.json"`) {
		t.Error("a staged registry advertises a feed URL that 404s")
	}
}

// Every non-public channel says it needs a token; Public does not. That contrast
// is where the rule is learned.
func TestFederationPageMarksGatedChannels(t *testing.T) {
	srv, ts := newServer(t)
	seedChannelTechnique(t, srv, "partner-technique", "partners")

	_, fed := fetchHTML(t, ts.URL+"/federation")
	// The picture draws the bar across the mouth; the list says it in the page's
	// own badge vocabulary. The padlock glyph was the one emoji in the dashboard.
	if !strings.Contains(fed, `<span class="badge">token needed</span>`) {
		t.Error("a private channel is not marked as needing a token")
	}
	if strings.ContainsAny(fed, "🔒🌐") {
		t.Error("an emoji is back on the federation page")
	}
	if !strings.Contains(fed, `id="tokens"`) {
		t.Error("there is no token surface to mint from")
	}

	// An explicitly-open channel says so loudly, because that is the risky state.
	srv.Cfg.OpenFeedChannels = []string{"partners"}
	_, fed = fetchHTML(t, ts.URL+"/federation")
	if !strings.Contains(fed, "readable by anyone") {
		t.Error("an explicitly open channel does not say it is world-readable")
	}
}

// Minting shows the secret once and stores only its hash.
func TestMintFeedTokenShowsTheSecretOnce(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")

	resp, err := admin.PostForm(ts.URL+"/admin/feed-tokens",
		url.Values{"label": {"Acme Corp"}, "channels": {"partners, vendor-pack"}})
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, feedTokenPrefix) {
		t.Fatal("the minted secret was not shown")
	}
	if !strings.Contains(body, "keeps only its hash") {
		t.Error("the page does not say the secret cannot be read again")
	}

	tokens, err := srv.Store.ListFeedTokens()
	if err != nil || len(tokens) != 1 {
		t.Fatalf("ListFeedTokens = %d, err %v; want 1", len(tokens), err)
	}
	if len(tokens[0].Channels) != 2 {
		t.Errorf("token scope = %v, want both channels", tokens[0].Channels)
	}
	// The secret itself must not be recoverable from storage.
	for _, tok := range tokens {
		if strings.Contains(tok.Hash, feedTokenPrefix) {
			t.Error("the stored hash contains the secret")
		}
	}

	// Revoking closes the door and keeps the record.
	resp, err = admin.PostForm(ts.URL+"/admin/feed-tokens/revoke/"+tokens[0].ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	after, _ := srv.Store.ListFeedTokens()
	if len(after) != 1 || after[0].RevokedAt == "" {
		t.Errorf("after revoke: %+v; want the token kept and stamped", after)
	}
}

// A token naming only the open channel authorizes nothing, and saying so is
// better than minting a credential that does nothing.
func TestMintFeedTokenRefusesPublicOnly(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")

	resp, err := admin.PostForm(ts.URL+"/admin/feed-tokens",
		url.Values{"label": {"Confused"}, "channels": {"public"}})
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "has no gate") {
		t.Errorf("minting a public-only token was not explained: %s", firstLines(body))
	}
}

// A technique says where it stands in the commons, on its own page.
func TestTechniquePageStatesItsPublicStanding(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, true
	seedRanked(t, srv, "proven", 40, 50)
	srv.RecomputePublicChannel()

	_, page := fetchHTML(t, ts.URL+"/techniques/proven")
	if !strings.Contains(page, "Public feed") {
		t.Fatal("the technique does not say it is in the Public feed")
	}
	if !strings.Contains(page, "every registry in the pool receives it") {
		t.Error("the technique does not say what being in the feed means")
	}

	// An org-scoped technique says why it is NOT in it — the operator should not have
	// to infer that from an absence.
	c, _, _ := srv.Store.GetTechnique("proven")
	c.Scope = "org"
	if err := srv.Store.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	srv.RecomputePublicChannel()
	_, page = fetchHTML(t, ts.URL+"/techniques/proven")
	if !strings.Contains(page, "Not in the Public feed") {
		t.Error("an excluded technique does not say it is excluded")
	}
	if !strings.Contains(page, "org-scoped") {
		t.Error("an excluded technique does not give the reason")
	}
}

func firstLines(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
