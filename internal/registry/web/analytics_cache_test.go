// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"sync"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
)

// countingStore records how often the whole log was read.
type countingStore struct {
	storage.Store
	mu    sync.Mutex
	reads int
}

func (c *countingStore) AllEvents(since string) ([]models.FeedbackEvent, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.Store.AllEvents(since)
}

func (c *countingStore) readCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

// Two renders of the same unchanged log must read it once.
//
// Every analytic surface folds the whole feedback log in Go, and each of them
// was asking the store for its own copy — a full table scan per page load on a
// SQL backend, several per dashboard.
func TestTheEventLogIsReadOnceWhileNothingHasBeenAddedToIt(t *testing.T) {
	srv, _ := newServer(t)
	counting := &countingStore{Store: srv.Store}
	srv.Store = counting

	if _, err := srv.readAnalyticsInputs(false); err != nil {
		t.Fatal(err)
	}
	first := counting.readCount()
	for i := 0; i < 5; i++ {
		if _, err := srv.readAnalyticsInputs(false); err != nil {
			t.Fatal(err)
		}
	}
	if got := counting.readCount(); got != first {
		t.Errorf("five more renders read the log %d more times, want 0", got-first)
	}
}

// And a reader must never be shown a funnel that predates their own feedback.
//
// This is why the cache is keyed on the store's row counts rather than on a
// clock: a member who records an outcome and reloads has to see it. A stale
// window here would be the product contradicting itself.
func TestAnEventWrittenNowShowsInTheNextRender(t *testing.T) {
	srv, _ := newServer(t)
	counting := &countingStore{Store: srv.Store}
	srv.Store = counting

	before, err := srv.readAnalyticsInputs(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.InsertEvent(models.FeedbackEvent{
		EventID: "e-new", TechniqueID: "t1", Stage: "shown",
		Confidence: "explicit", CreatedAt: "2026-09-14T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	after, err := srv.readAnalyticsInputs(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.events) != len(before.events)+1 {
		t.Errorf("the render after the write saw %d events, want %d — the cache went stale",
			len(after.events), len(before.events)+1)
	}
}

// Asking for the audit facts must not be answered from a read taken without
// them.
func TestAReadWithoutFactsDoesNotAnswerARequestForThem(t *testing.T) {
	srv, _ := newServer(t)
	if _, err := srv.readAnalyticsInputs(false); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.AppendAuditFact(models.AuditFact{
		AuditID: "a1", CreatedAt: "2026-09-14T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	in, err := srv.readAnalyticsInputs(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.facts) == 0 {
		t.Error("a request for the audit facts was answered from a read taken without them")
	}
}
