// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/jobs"
	"github.com/opentacit/tacit/internal/registry/store"
	"github.com/opentacit/tacit/internal/registry/web"
	"github.com/opentacit/tacit/pkg/contracts"
	"github.com/opentacit/tacit/pkg/embed"
)

// newRegistry spins up the real registry (file store, seed techniques) so the
// public client is tested against the actual server, not a stub.
func newRegistry(t *testing.T) *Registry {
	t.Helper()
	// Hermetic: never read the developer machine's real registry.env.
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "no-such.env"))
	cfg := config.Load()
	cfg.APIKey = "test-key"
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	embedder, err := embed.New(cfg.EmbedModel, cfg.EmbedDim)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := jobs.Startup(st, "../../techniques", embedder); err != nil {
		t.Fatal(err)
	}
	srv := web.New(cfg, st, embedder, "../../docs")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &Registry{BaseURL: ts.URL, APIKey: "test-key"}
}

func TestHealthAndTechniques(t *testing.T) {
	r := newRegistry(t)
	health, err := r.Health()
	if err != nil || health["ok"] != true {
		t.Fatalf("health: %v %v", health, err)
	}
	techniques, err := r.Techniques()
	if err != nil || len(techniques) == 0 {
		t.Fatalf("techniques: %d %v", len(techniques), err)
	}
	one, err := r.Technique(techniques[0].ID)
	if err != nil || one.ID != techniques[0].ID {
		t.Fatalf("technique: %+v %v", one, err)
	}
	if len(one.Embedding) != 0 {
		t.Fatal("embedding leaked over the API")
	}
}

func TestEvidenceTyped(t *testing.T) {
	r := newRegistry(t)
	block, err := r.GetEvidence(contracts.Characterization{
		SummaryText: "user pasted CSV rows from the internal warehouse",
	})
	if err != nil || len(block.Candidates) == 0 {
		t.Fatalf("evidence: %+v %v", block, err)
	}
	if block.Candidates[0].TechniqueID == "" {
		t.Fatal("candidate not typed")
	}
}

func TestFeedbackAndEventPaging(t *testing.T) {
	r := newRegistry(t)
	var drafts []contracts.FeedbackEventDraft
	for i := 0; i < 7; i++ {
		drafts = append(drafts, contracts.FeedbackEventDraft{
			EventID: fmt.Sprintf("evt_page_%d", i), TechniqueID: "ask-for-a-diagram",
			Stage: "shown", Confidence: "inferred",
		})
	}
	accepted, _, err := r.PostFeedback(drafts)
	if err != nil || accepted != 7 {
		t.Fatalf("feedback: %d %v", accepted, err)
	}
	// idempotent replay
	_, duplicates, err := r.PostFeedback(drafts)
	if err != nil || duplicates != 7 {
		t.Fatalf("replay: %d %v", duplicates, err)
	}

	page, err := r.Events("", 3)
	if err != nil || len(page.Events) != 3 || page.NextSince == "" {
		t.Fatalf("page: %d next=%q %v", len(page.Events), page.NextSince, err)
	}
	all, err := r.AllEvents("")
	if err != nil || len(all) != 7 {
		t.Fatalf("drain: %d %v", len(all), err)
	}
}

func TestAuthRequired(t *testing.T) {
	r := newRegistry(t)
	r.APIKey = "wrong"
	if _, err := r.Techniques(); err == nil {
		t.Fatal("bad key accepted")
	}
	var apiErr *APIError
	if _, err := r.Techniques(); err != nil {
		var ok bool
		apiErr, ok = err.(*APIError)
		if !ok || apiErr.Status != 401 {
			t.Fatalf("want APIError 401, got %v", err)
		}
	}
}

// TestRejectionIsClassifiable covers what consumers do with a refusal: they
// match HTTPStatus() rather than this package's type (the hook agent tells a
// rejected key from an outage that way), and they show Message() to a person
// rather than the whole envelope.
func TestRejectionIsClassifiable(t *testing.T) {
	r := newRegistry(t)
	r.APIKey = "wrong"
	err := r.Do("GET", "/v1/techniques", nil, nil)
	var httpErr interface{ HTTPStatus() int }
	if !errors.As(err, &httpErr) || httpErr.HTTPStatus() != 401 {
		t.Fatalf("want an HTTPStatus() 401, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Message() != "unauthorized" {
		t.Fatalf("message = %q (%v)", apiErr.Message(), err)
	}
	// A body that is not the registry's error envelope survives whole.
	plain := &APIError{Status: 502, Path: "/v1/health", Body: "bad gateway"}
	if plain.Message() != "bad gateway" {
		t.Fatalf("message = %q", plain.Message())
	}
}

// TestJoinLoop walks the two ends of an invitation through the typed methods:
// the admin mints a link, and the machine that holds the token trades it for a
// member key it can then use. The exchange carries no key of its own.
func TestJoinLoop(t *testing.T) {
	r := newRegistry(t)
	joinURL, expiresAt, err := r.Invite(time.Hour)
	if err != nil || joinURL == "" || expiresAt == "" {
		t.Fatalf("invite: %q %q %v", joinURL, expiresAt, err)
	}
	base, token, ok := strings.Cut(joinURL, "/join/")
	if !ok {
		t.Fatalf("join link has no token: %q", joinURL)
	}
	joiner := &Registry{BaseURL: base}
	regURL, key, err := joiner.JoinExchange(token, "dana@laptop")
	if err != nil || key == "" || regURL == "" {
		t.Fatalf("exchange: %q %q %v", regURL, key, err)
	}
	if _, err := (&Registry{BaseURL: r.BaseURL, APIKey: key}).Techniques(); err != nil {
		t.Fatalf("the minted key does not work: %v", err)
	}
	if _, _, err := joiner.JoinExchange("nonsense", ""); err == nil {
		t.Fatal("a bad token was exchanged")
	}
}

// TestTypedHealth: the fields tools read off the open endpoint.
func TestTypedHealth(t *testing.T) {
	r := newRegistry(t)
	h, err := r.GetHealth()
	if err != nil || !h.OK || h.Techniques == 0 {
		t.Fatalf("health: %+v %v", h, err)
	}
}
