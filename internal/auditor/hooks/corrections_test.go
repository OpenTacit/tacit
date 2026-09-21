// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The ledger's whole job is to prove repetition without keeping what was
// repeated. Two sayings of the same correction have to land on one hash even
// when the member words them differently.
func TestCorrectionLedgerClustersRewordings(t *testing.T) {
	path := t.TempDir() + "/corrections.jsonl"
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	l := loadCorrectionLedger(path, "salt", fixedClock(now))

	h1, c1 := l.note("no, use fuser -k on the port, don't pkill the process")
	h2, c2 := l.note("I said use fuser -k on the port instead of pkill")
	if h1 == "" || h1 != h2 {
		t.Fatalf("two sayings of one correction got different hashes: %q %q", h1, h2)
	}
	if c1 != 1 || c2 != 2 {
		t.Fatalf("counts = %d then %d, want 1 then 2", c1, c2)
	}
	// A different correction is a different bucket.
	h3, _ := l.note("no, run the migration before the deploy, not after")
	if h3 == h1 {
		t.Fatalf("two unrelated corrections collided on %q", h1)
	}
}

// Not every message is a correction. A prompt that merely contains a "no" is
// ordinary work, and a false positive costs the member advice about something
// they never said twice.
func TestCorrectionDetectionIsConservative(t *testing.T) {
	corrections := []string{
		"no, use the connector",
		"that's wrong — the column is called account_id",
		"stop editing that file, work in the other package",
		"I told you to run the failing test first",
		"instead of grepping the source, check the rendered output",
	}
	ordinary := []string{
		"add a note about the retry budget",
		"there is no index on that column yet, add one",
		"summarise what changed",
		"can you check whether the deploy finished",
		"donate the remaining budget to the other team",
	}
	for _, m := range corrections {
		if !isCorrection(m) {
			t.Errorf("missed a correction: %q", m)
		}
	}
	for _, m := range ordinary {
		if isCorrection(m) {
			t.Errorf("ordinary work read as a correction: %q", m)
		}
	}
}

// A bare "no" repeats constantly and means nothing on its own, so it must not
// accumulate into a finding.
func TestCorrectionNeedsSomethingDistinctive(t *testing.T) {
	l := loadCorrectionLedger("", "salt", fixedClock(time.Now()))
	for _, m := range []string{"no", "no.", "nope", "stop"} {
		if h, _ := l.note(m); h != "" {
			t.Fatalf("%q became a tracked correction", m)
		}
	}
}

// The file holds hashes, counts and dates. What the member typed is not kept —
// that ordering is the whole reason the ledger is allowed to exist.
func TestCorrectionLedgerStoresNoText(t *testing.T) {
	path := t.TempDir() + "/corrections.jsonl"
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	l := loadCorrectionLedger(path, "salt", fixedClock(now))
	l.note("no, use SECRETTOOL on the SECRETHOST, not the other one")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"SECRETTOOL", "SECRETHOST", "secrettool", "secrethost"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("the ledger kept the wording: %s", raw)
		}
	}
	if !strings.Contains(string(raw), `"count":1`) {
		t.Fatalf("the count is missing: %s", raw)
	}
}

// The ledger is a log of states: a reload takes the last line per hash and the
// count does not restart or double.
func TestCorrectionLedgerSurvivesReload(t *testing.T) {
	path := t.TempDir() + "/corrections.jsonl"
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	l := loadCorrectionLedger(path, "salt", fixedClock(now))
	var hash string
	for i := 0; i < 3; i++ {
		hash, _ = l.note("no, use fuser -k on the port rather than pkill")
	}
	l.flush()

	l2 := loadCorrectionLedger(path, "salt", fixedClock(now))
	repeats := l2.repeated(2)
	if len(repeats) != 1 || repeats[0].Hash != hash || repeats[0].Count != 3 {
		t.Fatalf("after reload = %+v, want one entry with count 3", repeats)
	}
	// And a fourth saying continues rather than restarts.
	if _, c := l2.note("I said use fuser -k on the port rather than pkill"); c != 4 {
		t.Fatalf("count after reload = %d, want 4", c)
	}
}

// A draft is raised from a correction once. Crossing the threshold again on the
// next saying would nag the member about something already in their queue.
func TestCorrectionRaisedIsSticky(t *testing.T) {
	path := t.TempDir() + "/corrections.jsonl"
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	l := loadCorrectionLedger(path, "salt", fixedClock(now))
	hash, _ := l.note("no, use fuser -k on the port rather than pkill")
	l.markRaised(hash)
	l.flush()

	l2 := loadCorrectionLedger(path, "salt", fixedClock(now))
	repeats := l2.repeated(1)
	if len(repeats) != 1 || !repeats[0].Raised {
		t.Fatalf("raised did not survive the reload: %+v", repeats)
	}
}

// The salt is per-org, so the same correction on two machines does not produce
// a shared identifier. Nothing leaves the machine, but a hash that is stable
// across members would be one step from an identity if it ever did.
func TestCorrectionHashIsSalted(t *testing.T) {
	now := fixedClock(time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC))
	a := loadCorrectionLedger("", "salt-a", now)
	b := loadCorrectionLedger("", "salt-b", now)
	msg := "no, use fuser -k on the port rather than pkill"
	ha, _ := a.note(msg)
	hb, _ := b.note(msg)
	if ha == "" || ha == hb {
		t.Fatalf("hashes are not salted apart: %q %q", ha, hb)
	}
}
