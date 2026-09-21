// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

// TestCohortDirectoryEndpoint: a joining member's machine holds a member key,
// not a session, so the directory has to be key-authed — and it has to name
// the dimensions a member may set even when nobody has set them, or a first
// joiner gets a blank page instead of a vocabulary.
func TestCohortDirectoryEndpoint(t *testing.T) {
	srv, ts := newServer(t)

	resp, _ := request(t, "GET", ts.URL+"/v1/cohorts", "", "")
	if resp.StatusCode != 401 {
		t.Fatalf("unkeyed = %d, want 401", resp.StatusCode)
	}

	resp, body := request(t, "GET", ts.URL+"/v1/cohorts", "test-key", "")
	if resp.StatusCode != 200 {
		t.Fatalf("keyed = %d: %v", resp.StatusCode, body)
	}
	settable, _ := body["settable"].([]any)
	if len(settable) != 4 || settable[0] != "team" {
		t.Fatalf("settable = %v, want the four member-typed dimensions", body["settable"])
	}
	if dims, _ := body["dimensions"].([]any); len(dims) != 0 {
		t.Fatalf("an untagged registry has no cohorts, got %v", dims)
	}

	// Two colleagues on one team, one on another: the directory reports them
	// in use order so the next joiner reads the common name first.
	for _, f := range []models.AuditFact{
		{AuditID: "a1", SessionHash: "h1", CreatedAt: "2026-07-01T10:00:00Z",
			Segment: models.Segment{"team": "payments", "role": "engineer"}},
		{AuditID: "a2", SessionHash: "h2", CreatedAt: "2026-07-01T11:00:00Z",
			Segment: models.Segment{"team": "payments"}},
		{AuditID: "a3", SessionHash: "h3", CreatedAt: "2026-07-01T12:00:00Z",
			Segment: models.Segment{"team": "platform"}},
	} {
		if _, err := srv.Store.AppendAuditFact(f); err != nil {
			t.Fatal(err)
		}
	}

	_, body = request(t, "GET", ts.URL+"/v1/cohorts", "test-key", "")
	dims, _ := body["dimensions"].([]any)
	if len(dims) != 2 {
		t.Fatalf("want team and role, got %v", dims)
	}
	team, _ := dims[0].(map[string]any)
	if team["dimension"] != "team" {
		t.Fatalf("want team first, got %v", team["dimension"])
	}
	vals, _ := team["values"].([]any)
	if len(vals) != 2 {
		t.Fatalf("want two team cohorts, got %v", vals)
	}
	top, _ := vals[0].(map[string]any)
	if top["value"] != "payments" || top["sessions"].(float64) != 2 {
		t.Fatalf("want payments with 2 sessions first, got %v", top)
	}
}
