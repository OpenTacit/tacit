// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"html"
	"slices"
	"sync"

	"github.com/opentacit/tacit/internal/registry/models"
)

type analyticsInputs struct {
	techniques []models.Technique
	events     []models.FeedbackEvent
	facts      []models.AuditFact
}

// analyticsCache is the last read of the feedback log, and the lock that keeps
// concurrent renders from each making their own.
//
// Every analytic surface reads the WHOLE log and folds it in Go: the window
// selector needs the earliest event to know which windows it may offer, so
// there is no cursor to push down. That is comfortable at pilot scale and a full
// table scan per page load after it — and the dashboard's panels, its MCP apps
// and every open tab were each asking separately.
//
// The cache is keyed on the store's own row counts, not on a clock and not on
// the writers remembering to invalidate. Counts() is one round trip of COUNT(*)s
// against indexes, against fetching and decoding every row; the log is
// append-only, so equal counts mean nothing has been added. It is also the only
// signal that works when a second instance shares the database, which a
// local invalidation would miss entirely.
//
// This makes the fold rarer, not cheaper. Aggregating in SQL rather than in Go
// is the other half, and that is a change to the store's contract.
//
// EVERY READER SHARES THESE SLICES. Before the cache each caller got its own
// copy from the store, so sorting one in place was harmless; now it would
// reorder the log under every other page. Nothing does, and the slices are
// clipped so an append cannot write into them either, but a reader that needs
// its own order must take its own copy.
type analyticsCache struct {
	mu     sync.Mutex
	in     analyticsInputs
	facts  bool // whether `in` was read with the audit facts
	counts map[string]int
	valid  bool
}

// sameCounts reports whether the store holds exactly what it held when the
// cached read was taken.
func sameCounts(a, b map[string]int) bool {
	if a == nil || b == nil || len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (s *Server) readAnalyticsInputs(withFacts bool) (analyticsInputs, error) {
	c := &s.analytics
	// The lock is held across the read on purpose: the second arrival should
	// wait for the first one's answer rather than start a second scan.
	c.mu.Lock()
	defer c.mu.Unlock()

	// A store that cannot say what it holds gets read rather than trusted.
	counts, err := s.Store.Counts()
	if err == nil && c.valid && (c.facts || !withFacts) && sameCounts(counts, c.counts) {
		return c.in, nil
	}

	var in analyticsInputs
	in.events, err = s.Store.AllEvents("")
	if err != nil {
		return analyticsInputs{}, err
	}
	in.techniques, err = s.Store.ListTechniques(nil, 0)
	if err != nil {
		return analyticsInputs{}, err
	}
	if withFacts {
		in.facts, err = s.Store.AuditFacts("")
		if err != nil {
			return analyticsInputs{}, err
		}
	}
	// Clipped, so an append by one reader allocates rather than writing into the
	// array every other reader is holding.
	in.events = slices.Clip(in.events)
	in.techniques = slices.Clip(in.techniques)
	in.facts = slices.Clip(in.facts)
	if counts != nil {
		c.in, c.facts, c.counts, c.valid = in, withFacts, counts, true
	} else {
		c.valid = false
	}
	return in, nil
}

func storeUnavailablePage(active string, crumbs []crumb, err error) page {
	return page{status: 500, active: active, crumbs: crumbs,
		content: "<p>Store unavailable: " + html.EscapeString(err.Error()) + "</p>"}
}
