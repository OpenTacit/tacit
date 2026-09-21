// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Demonstration mode for `tacit serve` (TACIT_DEMO_DIR): every *.json dataset
// in the directory becomes a switchable scenario in the dashboard's Demo menu
// (internal/registry/web/demo.go). Each scenario is served by its own isolated
// registry instance — a file store under DataDir/demo/<scenario> and a
// web.Server sharing the process's config, embedder, and OIDC — and is
// populated in the background by the demo loader driving that instance's own
// public HTTP API in-process, exactly as `tacit demo load` would over the
// network. Production data is never touched: the browser's dataset cookie
// picks the instance per request, and cookie-less clients (agents, MCP, the
// /v1 API) always reach production.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/demo"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/store"
	"github.com/opentacit/tacit/internal/registry/web"
	pkgclient "github.com/opentacit/tacit/pkg/client"
)

// enableDemoMode scans cfg.DemoDir, builds one registry instance per dataset,
// kicks off background loads for the ones not already materialized, and
// returns the cookie-routing handler wrapping prodHandler. The returned
// cleanup closes the demo stores. prodSrv gains the shared catalog so the
// production instance renders the Demo menu too.
func enableDemoMode(cfg registryconfig.Config, embedder embed.Embedder, docsDir string,
	prodSrv *web.Server, prodHandler http.Handler) (http.Handler, func(), error) {

	files, err := filepath.Glob(filepath.Join(cfg.DemoDir, "*.json"))
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no *.json datasets in %s", cfg.DemoDir)
	}
	sort.Strings(files)

	catalog := &web.DemoCatalog{}
	prodSrv.Demo = &web.DemoState{Catalog: catalog}
	demos := map[string]http.Handler{}
	var closers []io.Closer

	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[demo] %s: %v", path, err)
			continue
		}
		base := filepath.Base(path)
		d, err := demo.ParseDataset(raw)
		if err != nil {
			// Visible but unselectable: the operator should see the failure in
			// the menu where they expected the scenario, not only in the log.
			catalog.Add("file:"+base, base)
			catalog.SetErr("file:"+base, err.Error())
			log.Printf("[demo] %s: %v", base, err)
			continue
		}
		key := demoKey(d.Scenario)
		if key == "" || demos[key] != nil {
			log.Printf("[demo] %s: scenario %q is empty or duplicates another dataset — skipped", base, d.Scenario)
			continue
		}
		// Menu label: the catalog's vertical title ("B2B software vendor")
		// when the scenario is a known one, else the dataset's own org name.
		title := key
		if sc, ok := demo.ScenarioByKey(d.Scenario); ok {
			title = sc.Title
		} else if d.Org.Name != "" {
			title = d.Org.Name
		}

		// Whatever month the dataset was authored for, the demo shows it as the
		// month ending yesterday — otherwise a windowed dashboard opened a few
		// weeks after generation renders empty.
		d.RebaseWindowTo(time.Now())

		// The store is derived state, reproducible from the dataset + window
		// anchor: a changed file — or a restart on a later day, which moves the
		// anchor — rebuilds it from scratch; otherwise it is served as-is.
		dir := filepath.Join(cfg.DataDir, "demo", key)
		sum := sha256.Sum256(raw)
		stamp := hex.EncodeToString(sum[:]) + " " + d.Window.StartDate
		stampPath := filepath.Join(dir, "dataset.sha256")
		prev, _ := os.ReadFile(stampPath)
		materialized := strings.TrimSpace(string(prev)) == stamp
		if !materialized {
			if err := os.RemoveAll(dir); err != nil {
				log.Printf("[demo] %s: reset %s: %v", key, dir, err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				log.Printf("[demo] %s: %v", key, err)
				continue
			}
		}
		st, err := store.Open(dir)
		if err != nil {
			catalog.Add(key, title)
			catalog.SetErr(key, err.Error())
			log.Printf("[demo] %s: open store: %v", key, err)
			continue
		}
		closers = append(closers, st)

		dcfg := cfg
		dcfg.DataDir = dir // per-instance state (feed key, suggest stats) stays isolated
		dcfg.DBURL = ""    // demo instances always use the file store
		dsrv := web.New(dcfg, st, embedder, docsDir)
		dsrv.Version = version
		dsrv.Demo = &web.DemoState{Catalog: catalog, Active: key}
		catalog.Add(key, title)
		h := dsrv.Handler()
		demos[key] = h

		if materialized {
			catalog.SetReady(key)
			log.Printf("[demo] %s ready (already materialized)", key)
			continue
		}
		go loadDemoInstance(h, cfg.APIKey, d, key, stampPath, stamp, catalog)
	}

	log.Printf("[tacit] demonstration mode: %d scenario(s) from %s", len(demos), cfg.DemoDir)
	cleanup := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
	return web.DemoRouter(prodHandler, demos), cleanup, nil
}

// loadDemoInstance populates one demo instance through its own public API and
// flips it selectable. Force is safe here: the target is our scratch store,
// never a real registry.
func loadDemoInstance(h http.Handler, apiKey string, d *demo.Dataset, key, stampPath, stamp string, catalog *web.DemoCatalog) {
	reg := &pkgclient.Registry{
		BaseURL: "http://tacit.demo",
		APIKey:  apiKey,
		HTTP:    &http.Client{Transport: inProcessTransport{h: h}},
	}
	res, err := demo.Load(reg, d, demo.LoadOptions{Force: true})
	if err != nil {
		catalog.SetErr(key, err.Error())
		log.Printf("[demo] %s: load failed: %v", key, err)
		return
	}
	if err := os.WriteFile(stampPath, []byte(stamp+"\n"), 0o644); err != nil {
		log.Printf("[demo] %s: write stamp: %v", key, err)
	}
	catalog.SetReady(key)
	log.Printf("[demo] %s ready: %d techniques, %d events, %d rollups",
		key, res.TechniquesContributed+res.TechniquesSkipped, res.EventsAccepted, res.OutcomesRecomputed)
}

// demoKey reduces a scenario name to a filesystem- and cookie-safe slug.
//
// Deliberately not internal/slug. This is not a technique id: it keeps hyphens
// the caller wrote, maps only space, underscore and dot to a hyphen, and drops
// everything else rather than collapsing it — so "a,b" is "ab" here and "a-b"
// there. It is also a cookie key, and rewriting it would sign every visitor out
// of a running demo for no gain.
func demoKey(scenario string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(scenario)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ', r == '_', r == '.':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// inProcessTransport serves a client.Registry request straight into an
// http.Handler — the demo loader speaks the public API without a socket.
type inProcessTransport struct{ h http.Handler }

func (t inProcessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := &memResponse{header: http.Header{}, status: http.StatusOK}
	t.h.ServeHTTP(rec, req)
	return &http.Response{
		Status:     fmt.Sprintf("%d %s", rec.status, http.StatusText(rec.status)),
		StatusCode: rec.status,
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:        rec.header,
		Body:          io.NopCloser(bytes.NewReader(rec.body.Bytes())),
		ContentLength: int64(rec.body.Len()),
		Request:       req,
	}, nil
}

// memResponse is the minimal in-memory http.ResponseWriter behind
// inProcessTransport.
type memResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
	wrote  bool
}

func (m *memResponse) Header() http.Header { return m.header }

func (m *memResponse) Write(p []byte) (int, error) {
	m.wrote = true
	return m.body.Write(p)
}

func (m *memResponse) WriteHeader(code int) {
	if !m.wrote {
		m.status = code
		m.wrote = true
	}
}
