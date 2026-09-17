// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/demo"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/store"
	"github.com/opentacit/tacit/internal/registry/web"
)

// TestEnableDemoMode drives the whole demonstration mode end to end: the
// shipped dataset materializes into an isolated instance through its own
// public API, the cookie routes to it, and production stays untouched.
func TestEnableDemoMode(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a full month of synthesized usage")
	}
	demoDir := t.TempDir()
	d, found, err := demo.ShippedDataset("software-vendor")
	if err != nil || !found {
		t.Fatalf("shipped dataset: found=%v err=%v", found, err)
	}
	if err := d.Save(filepath.Join(demoDir, "software-vendor.json")); err != nil {
		t.Fatal(err)
	}

	cfg := registryconfig.Config{
		DataDir: t.TempDir(), DemoDir: demoDir, APIKey: "test-key",
		EmbedModel: "hashing-v1", EmbedDim: 256,
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	embedder, err := embed.New(cfg.EmbedModel, cfg.EmbedDim)
	if err != nil {
		t.Fatal(err)
	}
	srv := web.New(cfg, st, embedder, "docs")
	handler, cleanup, err := enableDemoMode(cfg, embedder, "docs", srv, srv.Handler())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if srv.Demo == nil || srv.Demo.Catalog == nil {
		t.Fatal("production server should carry the shared demo catalog")
	}

	deadline := time.Now().Add(90 * time.Second)
	for !srv.Demo.Catalog.Ready("software-vendor") {
		if time.Now().After(deadline) {
			t.Fatalf("scenario never became ready: %+v", srv.Demo.Catalog.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}

	events := func(cookie string) float64 {
		t.Helper()
		req := httptest.NewRequest("GET", "/v1/health", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: web.DemoCookie, Value: cookie})
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("health: %v (%s)", err, rec.Body.String())
		}
		n, _ := body["events"].(float64)
		return n
	}
	if n := events("software-vendor"); n == 0 {
		t.Fatal("demo instance should hold the synthesized month of events")
	}
	// The month must be rebased to now, whatever window the dataset was
	// authored for — otherwise the windowed dashboard renders empty.
	since := time.Now().UTC().AddDate(0, 0, -31).Format(time.RFC3339)
	req := httptest.NewRequest("GET", "/v1/events?since="+since+"&limit=5", nil)
	req.Header.Set("X-Tacit-Key", "test-key")
	req.AddCookie(&http.Cookie{Name: web.DemoCookie, Value: "software-vendor"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var evPage struct {
		Events []any `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &evPage); err != nil {
		t.Fatalf("events: %v (%s)", err, rec.Body.String())
	}
	if len(evPage.Events) == 0 {
		t.Fatal("no events in the last 31 days — window was not rebased to now")
	}
	if n := events(""); n != 0 {
		t.Fatalf("production must stay untouched, has %v events", n)
	}

	// The dashboard shell on the demo instance carries the accented Demo menu.
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Del("X-Tacit-Key")
	req.AddCookie(&http.Cookie{Name: web.DemoCookie, Value: "software-vendor"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if page := rec.Body.String(); !strings.Contains(page, "demo-on") || !strings.Contains(page, "Production data") {
		t.Fatal("demo instance page should render the Demo menu with the active accent")
	}
}
