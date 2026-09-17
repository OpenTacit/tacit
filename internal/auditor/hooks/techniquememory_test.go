// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"testing"
	"time"
)

// The member-local memory is the resolution of a real collision: the registry
// may never know WHO adopted what (cohorts, never identities), yet re-suggesting
// a technique the member rejected last week burns the trust budget. Verdicts live on
// the member's machine; only technique IDs ever cross to the registry.
func TestTechniqueMemoryRemembersAcrossReload(t *testing.T) {
	path := t.TempDir() + "/memory.json"
	now := time.Now()

	m := loadTechniqueMemory(path)
	m.NoteAdopted("use-internal-data-connector", now)
	m.NoteDismissed("ask-for-a-diagram", now)

	// A fresh agent process on the same machine — the cross-session case.
	m2 := loadTechniqueMemory(path)
	if !m2.Suppressed("use-internal-data-connector", now) {
		t.Fatal("an adopted technique should not be re-suggested — they already use it")
	}
	if !m2.Suppressed("ask-for-a-diagram", now) {
		t.Fatal("a dismissed technique resurfaced next session — the exact repetition the trust budget forbids")
	}
	if m2.Suppressed("never-seen", now) {
		t.Fatal("suppressed a technique with no verdict")
	}
	if ids := m2.UsedIDs(now); len(ids) != 1 || ids[0] != "use-internal-data-connector" {
		t.Fatalf("UsedIDs = %v", ids)
	}

	// Verdicts expire: techniques evolve, and "already knew" must not be forever.
	later := now.Add(techniqueMemoryTTL + time.Hour)
	if m2.Suppressed("ask-for-a-diagram", later) {
		t.Fatal("a 30-day-old dismissal still suppresses")
	}
	if ids := m2.UsedIDs(later); len(ids) != 0 {
		t.Fatalf("expired adoption still reported as used: %v", ids)
	}
}

// Corruption degrades to amnesia, never to a broken agent.
func TestTechniqueMemoryToleratesGarbage(t *testing.T) {
	path := t.TempDir() + "/memory.json"
	if err := writeFile(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	m := loadTechniqueMemory(path)
	if m.Suppressed("anything", time.Now()) {
		t.Fatal("garbage memory suppressed a technique")
	}
	m.NoteAdopted("x", time.Now()) // and it can still write
	if !m.Suppressed("x", time.Now()) {
		t.Fatal("write after garbage load failed")
	}
}
