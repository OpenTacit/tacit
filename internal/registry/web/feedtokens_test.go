// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/internal/registry/models"
)

// seedChannelTechnique puts a technique in a named channel.
func seedChannelTechnique(t *testing.T, srv *Server, id string, channels ...string) {
	t.Helper()
	c := models.Technique{
		ID: id, Name: "Name " + id, Description: "d", Recipe: "recipe " + id,
		Scope: "general", Status: "stable", Provenance: "curated", Version: 1,
		Channels:  channels,
		CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}
	if err := srv.Store.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
}

func getWith(t *testing.T, url, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// The consequence Global Access would otherwise have: taking a public address
// serves every channel to the world. A private channel needs a token, and the
// refusal is a 404 so the surface is not an oracle for channel names.
func TestPrivateChannelsNeedATokenAndRefuseWithoutLeaking(t *testing.T) {
	srv, ts := newServer(t)
	seedChannelTechnique(t, srv, "partner-technique", "partners")

	code, body := getWith(t, ts.URL+"/f/partners/feed.json", "")
	if code != 404 {
		t.Errorf("anonymous read of a private channel = %d, want 404", code)
	}
	if strings.Contains(body, "partner-technique") {
		t.Error("the refusal leaked the channel's contents")
	}
	// A gated channel and a nonexistent one must be indistinguishable.
	nonexistent, _ := getWith(t, ts.URL+"/f/no-such-channel/feed.json", "")
	if nonexistent != code {
		t.Errorf("gated channel answers %d and a nonexistent one %d: the surface is an oracle for channel names",
			code, nonexistent)
	}

	// With a scoped token it reads.
	_, secret, err := srv.mintFeedToken("Acme Corp", []string{"partners"})
	if err != nil {
		t.Fatal(err)
	}
	code, body = getWith(t, ts.URL+"/f/partners/feed.json", secret)
	if code != 200 {
		t.Fatalf("scoped token read = %d (%s), want 200", code, body)
	}
	if !strings.Contains(body, "partner-technique") {
		t.Error("an authorized read did not return the channel")
	}
	// A gated response must not be cacheable by anything shared.
	req, _ := http.NewRequest("GET", ts.URL+"/f/partners/feed.json", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("gated feed Cache-Control = %q, want it uncacheable by a shared cache", cc)
	}

	// A token for a DIFFERENT channel does not open this one.
	_, otherSecret, err := srv.mintFeedToken("Someone else", []string{"vendor-pack"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := getWith(t, ts.URL+"/f/partners/feed.json", otherSecret); code != 404 {
		t.Errorf("a token scoped elsewhere read this channel (%d)", code)
	}

	// The rejections are counted on the publisher's side — the compensation for
	// answering 404, so an operator can see a peer failing to authenticate.
	if n := srv.RejectedFeedReads()["partners"]; n < 2 {
		t.Errorf("rejected reads for partners = %d, want the refusals counted", n)
	}
}

// The bypass that makes a gate decorative: the technique-content path carries no
// channel, so without deriving authorization from the technique's own channels a
// gated channel's whole payload is readable one id at a time.
func TestTechniqueContentPathIsGatedByTheTechniquesChannels(t *testing.T) {
	srv, ts := newServer(t)
	seedChannelTechnique(t, srv, "secret-technique", "partners")

	code, body := getWith(t, ts.URL+"/f/techniques/secret-technique.md", "")
	if code != 404 {
		t.Errorf("anonymous technique content = %d, want 404", code)
	}
	if strings.Contains(body, "recipe secret-technique") {
		t.Fatal("a gated channel's technique content is readable without a token")
	}

	_, secret, err := srv.mintFeedToken("Acme Corp", []string{"partners"})
	if err != nil {
		t.Fatal(err)
	}
	code, body = getWith(t, ts.URL+"/f/techniques/secret-technique.md", secret)
	if code != 200 || !strings.Contains(body, "recipe secret-technique") {
		t.Errorf("authorized technique content = %d (%s)", code, body)
	}
}

// The descriptor is the quiet version of the same leak: it lists every channel
// with its size and URL, so unfiltered it is a directory of private channel names.
func TestDescriptorHidesChannelsTheCallerCannotRead(t *testing.T) {
	srv, ts := newServer(t)
	seedChannelTechnique(t, srv, "partner-technique", "partners")
	seedChannelTechnique(t, srv, "open-technique", "general")

	_, body := getWith(t, ts.URL+"/.well-known/tacit.json", "")
	if strings.Contains(body, "partners") {
		t.Error("the descriptor advertises a private channel to an anonymous caller")
	}

	_, secret, err := srv.mintFeedToken("Acme Corp", []string{"partners"})
	if err != nil {
		t.Fatal(err)
	}
	_, body = getWith(t, ts.URL+"/.well-known/tacit.json", secret)
	if !strings.Contains(body, "partners") {
		t.Error("an authorized caller cannot see the channel it may read")
	}
	// Whatever happens, the shape stays valid.
	var d map[string]any
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("descriptor is not valid JSON: %v", err)
	}
}

// The commons must stay anonymously readable: the pooled feed and every
// subscriber fetch it without credentials, so a token requirement there would
// break the feature rather than protect anything.
func TestPublicChannelIsOpenAndCannotBeGated(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, true
	seedChannelTechnique(t, srv, "good")
	rate := 0.9
	if err := srv.Store.ReplaceOutcomes([]models.Outcome{{
		TechniqueID: "good", SegmentKey: "__overall__",
		Helped: 9, Adopted: 10, WeightedHelped: 9, WeightedAdopted: 10,
		HelpedRate: &rate, SampleSize: 10, LastUpdated: models.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	srv.RecomputePublicChannel()

	code, body := getWith(t, ts.URL+"/f/public/feed.json", "")
	if code != 200 {
		t.Fatalf("anonymous read of the commons = %d (%s), want 200", code, body)
	}
	if !strings.Contains(body, "good") {
		t.Error("the Public feed does not carry its member")
	}
	// And it stays cacheable, which is what makes many subscribers cheap.
	req, _ := http.NewRequest("GET", ts.URL+"/f/public/feed.json", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Error("the commons was marked uncacheable; hourly polling by many subscribers is now expensive")
	}

	// Minting a token FOR the commons is meaningless and must not imply it can
	// be gated.
	tok, _, err := srv.mintFeedToken("Confused operator", []string{federation.PublicChannel, "partners"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range tok.Channels {
		if ch == federation.PublicChannel {
			t.Error("a token was scoped to the open channel")
		}
	}
}

// The staged upgrade state: reachable through the proxy, commons computed and
// shown, and NOT served until a person confirms.
func TestStagedGlobalAccessComputesButDoesNotServeTheCommons(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, false
	seedChannelTechnique(t, srv, "good")
	rate := 0.9
	if err := srv.Store.ReplaceOutcomes([]models.Outcome{{
		TechniqueID: "good", SegmentKey: "__overall__",
		Helped: 9, Adopted: 10, WeightedHelped: 9, WeightedAdopted: 10,
		HelpedRate: &rate, SampleSize: 10, LastUpdated: models.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	srv.RecomputePublicChannel()

	if !srv.GlobalAccessStaged() {
		t.Fatal("an unconfirmed registry is not reported as staged")
	}
	// Computed, so the operator can be shown exactly what they are consenting to.
	if members := srv.PublicMembers(); len(members) != 1 {
		t.Errorf("staged membership = %v, want it computed for display", members)
	}
	// And withheld.
	if code, _ := getWith(t, ts.URL+"/f/public/feed.json", ""); code != 404 {
		t.Errorf("staged registry serves the commons (%d); consent to a proxy was treated as consent to a technique feed", code)
	}
}

// A member key opens the whole JSON API. It must not double as a federation
// credential, or every member's machine is also a peer's feed credential.
func TestMemberKeyDoesNotOpenAGatedFeed(t *testing.T) {
	srv, ts := newServer(t)
	seedChannelTechnique(t, srv, "partner-technique", "partners")

	// A minted member key, the way join does it.
	secret := newSecret()
	raw := sha256.Sum256([]byte(secret))
	sum := hex.EncodeToString(raw[:])
	if err := srv.Store.InsertMemberKey(models.MemberKey{
		ID: "k-1", Label: "someone's laptop", Hash: sum, CreatedAt: models.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := getWith(t, ts.URL+"/f/partners/feed.json", secret); code != 404 {
		t.Errorf("a member key read a gated feed (%d)", code)
	}

	// The org root key is accepted, deliberately: it already opens everything,
	// and an operator debugging their own feed with it is reasonable.
	req, _ := http.NewRequest("GET", ts.URL+"/f/partners/feed.json", nil)
	req.Header.Set("X-Tacit-Key", srv.Cfg.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("the org root key was refused its own feed (%d)", resp.StatusCode)
	}
}

// Revocation has to actually close the door.
func TestRevokedFeedTokenIsRefused(t *testing.T) {
	srv, ts := newServer(t)
	seedChannelTechnique(t, srv, "partner-technique", "partners")
	tok, secret, err := srv.mintFeedToken("Acme Corp", []string{"partners"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := getWith(t, ts.URL+"/f/partners/feed.json", secret); code != 200 {
		t.Fatalf("token did not work before revocation (%d)", code)
	}
	if err := srv.Store.SetFeedTokenRevoked(tok.ID, models.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _ := getWith(t, ts.URL+"/f/partners/feed.json", secret); code != 404 {
		t.Errorf("a revoked token still reads the channel (%d)", code)
	}
}
