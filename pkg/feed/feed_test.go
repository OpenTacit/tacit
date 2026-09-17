// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package feed

import (
	"crypto/ed25519"
	"path/filepath"
	"testing"

	"github.com/opentacit/tacit/pkg/contracts"
)

func testTechnique() contracts.Technique {
	return contracts.Technique{
		ID: "use-internal-data-connector", Name: "Query the warehouse",
		Scope: "org", Status: "stable", Provenance: "curated", Version: 2,
		Recipe: "@warehouse query <table>",
	}
}

func TestTechniqueHashIgnoresVolatileFields(t *testing.T) {
	a := testTechnique()
	b := testTechnique()
	b.Embedding = []float32{1, 2}
	b.EmbeddingModel = "hashing-v1"
	b.DecaySignal = 1
	b.Channels = []string{"general"}
	b.CreatedAt = "2026-01-01T00:00:00Z"
	b.UpdatedAt = "2026-07-06T00:00:00Z"
	if TechniqueHash(a) != TechniqueHash(b) {
		t.Fatal("volatile fields changed the content hash")
	}
	b.Recipe = "changed"
	if TechniqueHash(a) == TechniqueHash(b) {
		t.Fatal("recipe change did not change the hash")
	}
}

func TestSignAndVerify(t *testing.T) {
	key, err := LoadOrCreateKey(filepath.Join(t.TempDir(), "feed_key"))
	if err != nil {
		t.Fatal(err)
	}
	pub := PublicKeyString(key.Public().(ed25519.PublicKey))

	technique := testTechnique()
	e := Entry{
		ID: EntryID("https://techniques.example.org", technique.ID), Kind: KindTechnique,
		Updated: "2026-07-06T10:00:00Z", Title: technique.Name,
		ContentHash: TechniqueHash(technique), Technique: &technique,
	}
	Sign(&e, key)
	if err := Verify(e, pub); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// tamper with the technique -> hash mismatch
	tampered := e
	evil := technique
	evil.Recipe = "exfiltrate everything"
	tampered.Technique = &evil
	if Verify(tampered, pub) == nil {
		t.Fatal("tampered technique verified")
	}

	// tamper with signed fields -> signature invalid
	moved := e
	moved.Updated = "2027-01-01T00:00:00Z"
	if Verify(moved, pub) == nil {
		t.Fatal("mutated entry verified")
	}

	// wrong key -> invalid
	other, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "other_key"))
	if err := Verify(e, PublicKeyString(other.Public().(ed25519.PublicKey))); err == nil {
		t.Fatal("foreign key verified")
	}
}

func TestRetractionSigning(t *testing.T) {
	key, _ := LoadOrCreateKey(filepath.Join(t.TempDir(), "k"))
	pub := PublicKeyString(key.Public().(ed25519.PublicKey))
	e := Entry{
		ID: "https://techniques.example.org/techniques/old-move", Kind: KindRetraction,
		Updated: "2026-07-06T10:00:00Z", Reason: "decayed",
	}
	Sign(&e, key)
	if err := Verify(e, pub); err != nil {
		t.Fatalf("retraction verify: %v", err)
	}
	e.Reason = "just kidding"
	if Verify(e, pub) == nil {
		t.Fatal("mutated retraction verified")
	}
}

func TestKeyPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed_key")
	k1, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !k1.Equal(k2) {
		t.Fatal("key did not persist")
	}
}

func TestParsePublicKeyRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "ed25519:", "ed25519:!!!", "rsa:AAAA", "ed25519:aGk="} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
