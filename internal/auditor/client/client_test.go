// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// TestRejectionKeepsTheStructuralMatch pins the seam the hook agent depends on:
// a refusal from the registry must still answer HTTPStatus(), because
// hooks/status.go (noteRegistry) and hooks/mention.go (registryFaultHint)
// classify a rejection by matching that method, not this package's type. A
// transport change that returned some other error would silently turn "your
// key was rejected — a human must act" into "the registry is down".
func TestRejectionKeepsTheStructuralMatch(t *testing.T) {
	var gotKey, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotPath = r.Header.Get("X-Tacit-Key"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer ts.Close()

	// A trailing slash on the base URL must not double the separator.
	r := &Registry{BaseURL: ts.URL + "/", APIKey: "member-key"}
	_, err := r.GetEvidence(contracts.Characterization{SummaryText: "a turn"})
	if err == nil {
		t.Fatal("a 401 must be an error")
	}
	if gotPath != "/v1/evidence" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotKey != "member-key" {
		t.Fatalf("X-Tacit-Key = %q", gotKey)
	}

	var httpErr interface{ HTTPStatus() int }
	if !errors.As(err, &httpErr) {
		t.Fatalf("no HTTPStatus() on %T — the hooks' classification breaks", err)
	}
	if code := httpErr.HTTPStatus(); code != http.StatusUnauthorized {
		t.Fatalf("HTTPStatus() = %d", code)
	}

	// The typed form callers still match on (cmd/tacit's draft actions).
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("not a *StatusError: %T", err)
	}
	if se.Path != "/v1/evidence" || se.Code != http.StatusUnauthorized {
		t.Fatalf("StatusError = %+v", se)
	}
	if want := `registry /v1/evidence HTTP 401: {"error":"unauthorized"}`; se.Error() != want {
		t.Fatalf("Error() = %q, want %q", se.Error(), want)
	}
}

// TestGetAndPostAgreeOnRejection: the GET routes classify the same way the
// POST routes do — one transport, one rule.
func TestGetAndPostAgreeOnRejection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer ts.Close()

	r := &Registry{BaseURL: ts.URL, APIKey: "k"}
	for name, call := range map[string]func() error{
		"GET":  func() error { _, err := r.GetCohorts(); return err },
		"POST": func() error { _, err := r.Promote("t1", "stable"); return err },
	} {
		var httpErr interface{ HTTPStatus() int }
		if err := call(); !errors.As(err, &httpErr) || httpErr.HTTPStatus() != http.StatusForbidden {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// TestHealthReadsTheSnapshot covers the one route with its own rules: keyless
// in practice, short timeout, and a status-only error so a health line never
// prints a body.
func TestHealthReadsTheSnapshot(t *testing.T) {
	fail := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"ok":false,"error":"store down"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"drafts": 3, "access": "global", "tunnel": "up"})
	}))
	defer ts.Close()

	r := &Registry{BaseURL: ts.URL, APIKey: "k"}
	h, err := r.Health()
	if err != nil {
		t.Fatal(err)
	}
	if h.Drafts != 3 || h.Access != "global" || h.Tunnel != "up" {
		t.Fatalf("snapshot = %+v", h)
	}
	if n, err := r.DraftsCount(); err != nil || n != 3 {
		t.Fatalf("drafts = %d %v", n, err)
	}

	fail = true
	if _, err := r.Health(); err == nil || err.Error() != "registry /v1/health HTTP 503" {
		t.Fatalf("health error = %v", err)
	}
}
