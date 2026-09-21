// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// The dispatch that replaced four commands with flags on two: `tacit audit`
// now recognizes a captured session by its shape, and `tacit doctor --probe`
// carries what `tacit health` used to.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestReadCapturedSession(t *testing.T) {
	rec, ok := readCapturedSession("../../testdata/omnigent-session.json")
	if !ok {
		t.Fatal("a real captured session was not recognized — `tacit audit <session.json>` would fall back to plain ingestion")
	}
	if rec.Source == "" || len(rec.Messages) == 0 {
		t.Errorf("recognized, but the record is empty: source=%q messages=%d", rec.Source, len(rec.Messages))
	}

	// The reader underneath accepts ANY JSON object, so everything that is not
	// a session must fall through to the ordinary transcript path rather than
	// be audited as an empty capture.
	dir := t.TempDir()
	notSessions := map[string]string{
		"plain-json.json":   `{"hello":"world"}`,
		"empty-object.json": `{}`,
		"array.json":        `[{"role":"user","content":"hi"}]`,
		"transcript.txt":    "user: hello\nassistant: hi",
	}
	for name, body := range notSessions {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := readCapturedSession(p); ok {
			t.Errorf("%s was taken for a captured session", name)
		}
	}
	if _, ok := readCapturedSession(filepath.Join(dir, "absent.json")); ok {
		t.Error("a missing file was taken for a captured session")
	}
	if _, ok := readCapturedSession(dir); ok {
		t.Error("a directory was taken for a captured session")
	}
	if _, ok := readCapturedSession("https://example.com/share/abc"); ok {
		t.Error("a share URL was taken for a captured session")
	}
}

func TestProbeHealth(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want int
	}{
		{"healthy", 200, `{"ok":true}`, 0},
		// The first-run gate: serving, claimable, not yet claimed. A probe that
		// killed the container here would make the registry unclaimable.
		{"awaiting first-run setup", 503, `{"ok":false,"error":"setup required"}`, 0},
		{"broken", 503, `{"ok":false,"error":"store unavailable"}`, 1},
		{"not a registry", 404, `nope`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			if got := probeHealth(ts.URL + "/v1/health"); got != tc.want {
				t.Errorf("probeHealth = %d, want %d", got, tc.want)
			}
		})
	}
	if probeHealth("http://127.0.0.1:1/v1/health") != 1 {
		t.Error("an unreachable registry probed healthy")
	}
}

func TestDefaultProbeURLFollowsTheRegistrySettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.env")
	if err := os.WriteFile(path, []byte("TACIT_PORT=9099\nTACIT_BASE_PATH=/apps/tacit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", path)
	want := "http://127.0.0.1:9099/apps/tacit/v1/health"
	if got := defaultProbeURL(); got != want {
		t.Errorf("defaultProbeURL = %q, want %q", got, want)
	}
}

func TestMemberSettingsReportWithNothingToChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// No flags: report where this machine points, write nothing. This is the
	// `tacit connect --settings-only` path, and what `tacit setup` alone did.
	if rc := applyMemberSettings(memberSettings{}); rc != 0 {
		t.Fatalf("applyMemberSettings(memberSettings{}) = %d, want 0", rc)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "tacit", "agent.env")); err == nil {
		t.Error("a report with nothing to change wrote agent.env")
	}
}
