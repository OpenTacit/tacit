// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
)

// seedShadow files one shadow technique and n fit verdicts against it, shown of
// them positive, so the Under-evaluation section has real evidence to report.
func seedShadow(t *testing.T, srv *Server, id string, shown, declined int) {
	t.Helper()
	if err := srv.Store.UpsertTechnique(models.Technique{
		ID: id, Name: "Batch verification checks", Description: "d",
		Status: "shadow", Scope: "general", Provenance: "suggested",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < shown+declined; i++ {
		stage := "shadow_shown"
		if i >= shown {
			stage = "shadow_declined"
		}
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{
			EventID: fmt.Sprintf("%s-%d", id, i), TechniqueID: id,
			Stage: stage, CreatedAt: stamp,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestShadowLaneReadsAsAReadoutUnderAutoPromotion: the whole point of the split.
// With auto-promotion on, a shadow technique promotes or retires itself, so the
// section must not ask for a decision it does not need — and must say where the
// evidence has got to, since "is it getting there" is the only live question.
func TestShadowLaneReadsAsAReadoutUnderAutoPromotion(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.AutoPromoteEnabled = true
	srv.Cfg.AutoPromoteMinFit = 0.6
	srv.Cfg.AutoPromoteMinJudged = 12
	seedShadow(t, srv, "batch-verification", 5, 3) // 8 judged, 62%

	code, page := fetchHTML(t, ts.URL+"/review")
	if code != 200 {
		t.Fatalf("review page: %d", code)
	}
	for _, want := range []string{
		"Under automatic evaluation",
		// The gate is a figure on the sub-heading now, and "nothing waits on
		// you" is what the heading and the quiet queue chip already say — so
		// the promise is asserted where a reader who never opens prose meets
		// it, and the sentence itself sits in the disclosure.
		"promoted at 60% fit over 12 judgments",
		"No items need review",
		"62% · 8 of 12 judged", // progress toward the bar, not a bare rate
		"rq-quiet",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("readout mode is missing %q", want)
		}
	}
	// No per-row decision: the override lives on the technique's own page.
	if strings.Contains(page, "shadow-promote") || strings.Contains(page, "shadow-reject") {
		t.Fatal("a self-resolving lane still puts a decision on every row")
	}
	// The drafts lane is the one that blocks, and says so.
	if !strings.Contains(page, "Drafts to review") {
		t.Fatal("the drafts lane does not say it waits on the reader")
	}
}

// TestShadowLaneReadsAsAQueueWithoutAutoPromotion: with the gate off nothing
// moves until a person moves it, so the same section is a queue again — row
// controls back, and no promise of an automatic bar that will never come.
func TestShadowLaneReadsAsAQueueWithoutAutoPromotion(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.AutoPromoteEnabled = false
	seedShadow(t, srv, "batch-verification", 5, 3)

	_, page := fetchHTML(t, ts.URL+"/review")
	for _, want := range []string{
		"Under evaluation: review required",
		`action="/admin/techniques/shadow-promote/batch-verification"`,
		`action="/admin/techniques/shadow-reject/batch-verification"`,
		"shadow-queue",
		"62% · judged 8",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("queue mode is missing %q", want)
		}
	}
	for _, unwanted := range []string{"No items need review", "of 12 judged", "rq-quiet"} {
		if strings.Contains(page, unwanted) {
			t.Fatalf("queue mode promises the automatic gate it does not have: %q", unwanted)
		}
	}
}

// TestReviewNavExcludesSelfResolvingWork: a lane that needs nobody must not keep
// the page from saying nothing waits on the reader.
func TestReviewNavExcludesSelfResolvingWork(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.AutoPromoteEnabled = true
	srv.Cfg.AutoPromoteMinJudged = 12
	seedShadow(t, srv, "batch-verification", 5, 3)

	_, page := fetchHTML(t, ts.URL+"/review")
	if !strings.Contains(page, "No items need review") {
		t.Fatal("shadow work still counts as backlog when it resolves itself")
	}
}

// TestShadowRetiringIsShownInBothModes: auto-retire runs on every recompute
// cycle regardless of the promotion flag, so a technique under the floor is on
// its way out either way — and must not be reported as progressing toward a
// promotion bar it will never reach.
func TestShadowRetiringIsShownInBothModes(t *testing.T) {
	for _, auto := range []bool{true, false} {
		srv, ts := newServer(t)
		srv.Cfg.AutoPromoteEnabled = auto
		srv.Cfg.AutoPromoteMinFit = 0.6
		srv.Cfg.AutoPromoteMinJudged = 12
		seedShadow(t, srv, "poor-fit", 1, 9) // 10 judged, 10% — under the floor

		_, page := fetchHTML(t, ts.URL+"/review")
		if !strings.Contains(page, "10% · retiring") {
			t.Fatalf("auto-promote=%v: a technique under the retire floor is not shown as retiring", auto)
		}
		if strings.Contains(page, "10 of 12 judged") {
			t.Fatalf("auto-promote=%v: a retiring technique is shown progressing toward promotion", auto)
		}
	}
}
