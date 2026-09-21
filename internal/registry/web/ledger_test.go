// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/ledger"
)

// put files one blob the way a member's agent does: raw bytes, member key in
// the header.
func put(t *testing.T, ts *httptest.Server, id, machine string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/v1/ledger/"+id+"/"+machine, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Tacit-Key", "test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// The round trip the whole design rests on: a machine files sealed bytes, and a
// browser gets exactly those bytes back. The registry is a courier and the test
// asserts it behaves like one — what comes out is what went in, byte for byte,
// and nothing in between ever had the key.
func TestLedgerRoundTripsSealedBytes(t *testing.T) {
	_, ts := newServer(t)
	key, id, err := ledger.Derive("mk-a-member-key")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"schema":1,"windows":{"30d":{}}}`)
	sealed, err := ledger.Seal(key, want)
	if err != nil {
		t.Fatal(err)
	}
	resp := put(t, ts, id, "laptop", sealed)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("PUT = %d, want 200", resp.StatusCode)
	}

	get, err := http.Get(ts.URL + "/v1/ledger/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()
	var out struct {
		Machines []struct{ Machine, Sealed, Updated string }
	}
	if err := json.NewDecoder(get.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Machines) != 1 || out.Machines[0].Machine != "laptop" {
		t.Fatalf("GET returned %+v, want one slot named laptop", out.Machines)
	}
	back, err := base64.StdEncoding.DecodeString(out.Machines[0].Sealed)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ledger.Open(key, back)
	if err != nil {
		t.Fatalf("the member's own key could not open what the registry returned: %v", err)
	}
	if !bytes.Equal(plain, want) {
		t.Fatalf("round trip = %q, want %q", plain, want)
	}
}

// The registry is a courier, and a courier that can read the post is the thing
// this design exists to prevent. Nothing recognisable from the payload may
// appear on disk.
func TestRegistryStoresOnlyCiphertext(t *testing.T) {
	s, ts := newServer(t)
	key, id, _ := ledger.Derive("mk-a-member-key")
	sealed, _ := ledger.Seal(key, []byte(`{"queries":55,"secret-project":"apollo"}`))
	resp := put(t, ts, id, "laptop", sealed)
	resp.Body.Close()

	onDisk, err := os.ReadFile(filepath.Join(s.ledgerDir(), id, "laptop.blob"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(onDisk, []byte("apollo")) || bytes.Contains(onDisk, []byte("queries")) {
		t.Fatal("the payload is readable on the registry's disk")
	}
}

// Both path segments become a directory and a filename, so a URL must never be
// able to walk out of the ledger directory. Two layers do that, and the test
// asserts the outcome rather than which one fired: the router cleans a
// traversal away before the handler sees it, and the handler's own validators
// refuse anything malformed that survives cleaning.
func TestLedgerRefusesMalformedAddresses(t *testing.T) {
	s, ts := newServer(t)
	for _, tc := range []struct {
		name, id, machine string
		want              int
	}{
		// Cleaned by the router to /etc/passwd, which routes nowhere.
		{"traversal", "../../etc", "passwd", http.StatusNotFound},
		{"right length, not hex", strings.Repeat("z", 64), "laptop", http.StatusBadRequest},
		{"too short", "short", "laptop", http.StatusBadRequest},
		// Cleaned away too: /v1/ledger/<id>/.. is /v1/ledger/, which routes nowhere.
		{"machine dot-dot", strings.Repeat("a", 64), "..", http.StatusNotFound},
		// Survives cleaning, so the handler's own validator is what refuses it.
		{"machine name too long", strings.Repeat("a", 64), strings.Repeat("m", 65), http.StatusBadRequest},
	} {
		resp := put(t, ts, tc.id, tc.machine, []byte("x"))
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("PUT %s = %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
	// Whatever the status, nothing may exist outside the ledger directory, and
	// the ledger directory itself may hold nothing from these attempts.
	if entries, err := os.ReadDir(s.ledgerDir()); err == nil && len(entries) > 0 {
		t.Errorf("a malformed address wrote %d entries into the ledger", len(entries))
	}
}

// A member who has just entered their key on a new device, before any machine
// of theirs has published, is in this state. It is not an error and must not
// render as one.
func TestUnknownAddressIsEmptyNotAnError(t *testing.T) {
	_, ts := newServer(t)
	_, id, _ := ledger.Derive("mk-nobody-has-published")
	resp, err := http.Get(ts.URL + "/v1/ledger/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET on an empty address = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"machines":[]`) {
		t.Errorf("empty address returned %s, want an empty machine list", body)
	}
}

// The ledger must not become a file store: one machine's summaries are
// kilobytes, and anything far past that is not what this endpoint is for.
func TestOversizedBlobIsRefused(t *testing.T) {
	_, ts := newServer(t)
	_, id, _ := ledger.Derive("mk-a-member-key")
	resp := put(t, ts, id, "laptop", bytes.Repeat([]byte("x"), maxSealedBlob+1))
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized PUT = %d, want 413", resp.StatusCode)
	}
}

// The binding is a convenience index, and the thing it must never become is a
// member list. It records a hash of the subject, so the file cannot be read as
// a directory of who has an account here.
func TestBindingStoresNoSubject(t *testing.T) {
	key := subjectKey(map[string]any{"sub": "google-oauth2|11223344", "email": "dana@example.com"})
	if key == "" {
		t.Fatal("a signed-in subject produced no binding key")
	}
	if strings.Contains(key, "11223344") || strings.Contains(key, "dana") {
		t.Fatalf("the binding key %q carries the subject", key)
	}
	// Two subjects must not collide, or one member would be handed another's
	// address.
	other := subjectKey(map[string]any{"sub": "google-oauth2|99887766"})
	if key == other {
		t.Fatal("two subjects produced one binding key")
	}
	// And an unauthenticated reader has nothing to file under at all.
	if subjectKey(map[string]any{}) != "" {
		t.Error("a claimless session produced a binding key")
	}
}
