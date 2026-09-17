// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

// TestShadowRowOpensTheTechnique: a technique under evaluation carries the same
// detail as a draft — description, recipe, applies when, not when — but the
// Under-evaluation table shows only a summary row. The row took app.css's
// pointer cursor and led nowhere, so the detail was reachable only by typing a
// URL. It links to /techniques/ (the /drafts/ page serves status=draft only).
func TestShadowRowOpensTheTechnique(t *testing.T) {
	srv, ts := newServer(t)
	shadow := models.Technique{
		ID: "batch-verification-checks", Name: "Batch verification checks",
		Description: "Collect verification into one pass.",
		Recipe:      "1. Checkpoint the files\n2. Run the batch",
		AppliesWhen: "Verification is a major workload",
		NotWhen:     "Single-file changes",
		Tags:        []string{"verification"}, Provenance: "suggested",
		Status: "shadow", Scope: "general", CreatedAt: "2026-08-02T17:34:31Z",
	}
	if err := srv.Store.UpsertTechnique(shadow); err != nil {
		t.Fatal(err)
	}

	code, page := fetchHTML(t, ts.URL+"/review")
	if code != 200 {
		t.Fatalf("review page: %d", code)
	}
	if !strings.Contains(page, `data-href="/techniques/batch-verification-checks"`) {
		t.Fatal("the Under-evaluation row does not open the technique — a row with a pointer cursor that goes nowhere")
	}

	// The target must actually serve a shadow technique, in full.
	code, detail := fetchHTML(t, ts.URL+"/techniques/batch-verification-checks")
	if code != 200 {
		t.Fatalf("shadow technique detail: %d, want 200", code)
	}
	for _, want := range []string{"Collect verification into one pass.", "Checkpoint the files",
		"Verification is a major workload", "Single-file changes"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("shadow detail page is missing %q", want)
		}
	}

	// Reading the technique in full is what the decision is made on, so the
	// decision belongs on the same page.
	for _, want := range []string{
		`action="/admin/techniques/shadow-promote/batch-verification-checks"`,
		`action="/admin/techniques/shadow-reject/batch-verification-checks"`,
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("shadow detail page offers no %q control", want)
		}
	}
}

// TestShadowActionsOnlyOnShadow: the accept/reject pair is pinned to the status
// it acts on, so a serving technique's page keeps its own lifecycle control and
// never offers a shadow decision.
func TestShadowActionsOnlyOnShadow(t *testing.T) {
	srv, ts := newServer(t)
	stable := models.Technique{
		ID: "already-serving", Name: "Already serving", Description: "d",
		Status: "stable", Scope: "general", CreatedAt: "2026-08-02T17:34:31Z",
	}
	if err := srv.Store.UpsertTechnique(stable); err != nil {
		t.Fatal(err)
	}
	_, detail := fetchHTML(t, ts.URL+"/techniques/already-serving")
	if strings.Contains(detail, "shadow-promote") || strings.Contains(detail, "shadow-reject") {
		t.Fatal("a serving technique offers a shadow decision")
	}
	if !strings.Contains(detail, "Return to drafts") {
		t.Fatal("a serving technique lost its own lifecycle control")
	}
}
