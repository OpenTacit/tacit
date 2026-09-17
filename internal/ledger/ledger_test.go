// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ledger

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

// The property the whole design rests on: two machines holding the same member
// credential derive the same address and the same key, without ever talking to
// each other. That is what lets a laptop and a desktop file into one ledger.
func TestDeriveIsStableAcrossMachines(t *testing.T) {
	k1, id1, err := Derive("mk-secret-abc123")
	if err != nil {
		t.Fatal(err)
	}
	k2, id2, err := Derive("mk-secret-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1, k2) || id1 != id2 {
		t.Fatal("the same credential derived a different key or address")
	}
	if len(k1) != KeySize {
		t.Errorf("key is %d bytes, want %d", len(k1), KeySize)
	}
	if !ValidID(id1) {
		t.Errorf("derived id %q is not a valid address", id1)
	}
}

// A different member must land somewhere else. Colliding addresses would put
// two people's blobs in one directory, which is the failure this design exists
// to make impossible.
func TestDifferentMembersDoNotCollide(t *testing.T) {
	_, a, _ := Derive("mk-one")
	_, b, _ := Derive("mk-two")
	if a == b {
		t.Fatal("two credentials derived the same address")
	}
}

// The address is published in a URL and the key never is. Deriving both from
// one secret is only safe if the address says nothing about the key, so they
// must not share bytes.
func TestAddressLeaksNothingAboutTheKey(t *testing.T) {
	key, id, err := Derive("mk-secret-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(id, hex.EncodeToString(key)) {
		t.Fatal("the address contains the key")
	}
	raw, err := hex.DecodeString(id)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, key) {
		t.Fatal("the address IS the key")
	}
}

// A machine with no credential must be told what to do about it, not handed a
// key derived from the empty string — which would be one address every
// unconnected machine on earth shared.
func TestNoCredentialIsRefused(t *testing.T) {
	if _, _, err := Derive(""); err != ErrKeyRequired {
		t.Fatalf("Derive(\"\") = %v, want ErrKeyRequired", err)
	}
}

func TestSealRoundTrips(t *testing.T) {
	key, _, _ := Derive("mk-secret")
	want := []byte(`{"queries":55,"shown":24}`)
	blob, err := Seal(key, want)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, want) {
		t.Fatal("the payload is readable in the sealed bytes")
	}
	got, err := Open(key, blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round trip = %q, want %q", got, want)
	}
}

// The registry holds these blobs and must not be able to read them. A wrong key
// fails closed, and says nothing that would help somebody trying keys.
func TestWrongKeyCannotOpen(t *testing.T) {
	mine, _, _ := Derive("mk-mine")
	theirs, _, _ := Derive("mk-theirs")
	blob, err := Seal(mine, []byte("my week"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Open(theirs, blob)
	if err == nil {
		t.Fatal("another member's key opened this blob")
	}
	if strings.Contains(err.Error(), "my week") {
		t.Error("the failure quoted the plaintext")
	}
}

// Two seals of one payload must differ, or a watcher learns when a week was
// unchanged by comparing bytes.
func TestSealIsNotDeterministic(t *testing.T) {
	key, _, _ := Derive("mk-secret")
	a, _ := Seal(key, []byte("same"))
	b, _ := Seal(key, []byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("sealing twice produced identical bytes")
	}
}

// Both path segments arrive over HTTP and become a directory and a filename, so
// the validators are what stand between a URL and the registry's disk.
func TestPathSegmentsAreValidated(t *testing.T) {
	_, id, _ := Derive("mk-secret")
	for _, tc := range []struct {
		name string
		id   string
		want bool
	}{
		{"derived", id, true},
		{"traversal", "../../etc/passwd", false},
		{"short", "abc123", false},
		{"uppercase hex", strings.ToUpper(id), false},
		{"empty", "", false},
	} {
		if got := ValidID(tc.id); got != tc.want {
			t.Errorf("ValidID(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct {
		name string
		m    string
		want bool
	}{
		{"label", "dana-laptop", true},
		{"dotted", "dana.laptop.local", true},
		{"traversal", "..", false},
		{"slash", "a/b", false},
		{"leading dot", ".hidden", false},
		{"empty", "", false},
	} {
		if got := ValidMachine(tc.m); got != tc.want {
			t.Errorf("ValidMachine(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
