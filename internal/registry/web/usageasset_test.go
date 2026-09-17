// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The renderer is an asset, and the page is what is left.
//
// It used to be one 3,800-line constant: markup, a stylesheet and three thousand
// lines of script, sent in full on every request, cacheable by nobody and
// readable in no diff. The split is only worth anything if the document stays
// small and the script stays out of it.
func TestTheUsagePageDoesNotCarryItsRenderer(t *testing.T) {
	_, ts := newServer(t)
	doc := usageGet(t, ts, "/usage")

	if strings.Contains(doc, "function seriesPanel(") {
		t.Error("the renderer is inlined in the document again")
	}
	if !strings.Contains(doc, `src="/assets/usage.js?v=`) {
		t.Error("the document does not link the renderer")
	}
	if n := len(doc); n > 120_000 {
		t.Errorf("the Usage document is %d bytes; the renderer has leaked back into it", n)
	}
	// Deferred, so the configuration it reads is in the document first.
	if !strings.Contains(doc, `src="/assets/usage.js?v=`) || !strings.Contains(doc, `defer></script>`) {
		t.Error("the renderer is not deferred behind its configuration")
	}
}

// A fingerprinted URL is a promise about the bytes, so it is cached for a year;
// the bare one is not. Same asset, same ETag either way.
func TestTheUsageRendererIsServedAndFingerprinted(t *testing.T) {
	_, ts := newServer(t)

	hashed := usageGet(t, ts, "/assets/usage.js?v="+usageJSHash[:12])
	if hashed != usageJS {
		t.Fatal("the fingerprinted URL does not serve the renderer")
	}
	resp, err := http.Get(ts.URL + "/assets/usage.js?v=" + usageJSHash[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("a fingerprinted asset answered Cache-Control: %q", cc)
	}
	if resp.Header.Get("ETag") != usageJSETag {
		t.Errorf("ETag = %q, want %q", resp.Header.Get("ETag"), usageJSETag)
	}
}

// Everything the renderer cannot know arrives as JSON, and it must be JSON the
// renderer can actually read — a config that fails to parse takes the whole page
// down on its first line.
func TestTheUsageConfigurationIsReadableJSON(t *testing.T) {
	srv, ts := newServer(t)
	doc := usageGet(t, ts, "/usage?w=7d")

	const open = `<script id="usage-config" type="application/json">`
	i := strings.Index(doc, open)
	if i < 0 {
		t.Fatal("the document carries no configuration for the renderer")
	}
	raw := doc[i+len(open):]
	raw = raw[:strings.Index(raw, "</script>")]

	var got usageConfig
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("the configuration is not valid JSON: %v\n%s", err, raw)
	}
	if got.Window != "7d" {
		t.Errorf("window = %q, want the one the server resolved from ?w=", got.Window)
	}
	if got.View != usageView("/usage") {
		t.Errorf("view = %q, want the one the route resolves to", got.View)
	}
	if got.MinSample == 0 || got.PriceDate == "" || got.AgentURL == "" {
		t.Errorf("the configuration is missing values the renderer quotes: %+v", got)
	}
	if len(got.KnownTechniques) == 0 || len(got.Harnesses) == 0 {
		t.Error("the configuration carries no technique ids or client list")
	}
	// Nothing in it may close the element it sits in, whatever a technique is
	// called or an operator renamed the product to.
	if strings.Contains(raw, "</script") || strings.Contains(raw, "<!--") {
		t.Error("the configuration can break out of its script element")
	}
	_ = srv
}
