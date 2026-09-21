// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// usagePageAsBrowserSees returns what a browser ends up holding for a Usage
// URL: the document, and the renderer the document links.
//
// The two used to arrive as one response — three thousand lines of script
// pasted into every page — so a test asking "is this on the page" meant both.
// Fetching the asset over HTTP rather than reading the embedded string keeps
// the route and its fingerprinted URL in the test's path too.
func usagePageAsBrowserSees(t *testing.T, ts *httptest.Server, path string) string {
	t.Helper()
	doc := usageGet(t, ts, path)
	src, ok := linkedScript(doc, "/assets/usage.js")
	if !ok {
		t.Fatalf("GET %s does not link the usage renderer", path)
	}
	return doc + "\n" + usageGet(t, ts, src)
}

func usageGet(t *testing.T, ts *httptest.Server, path string) string {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// linkedScript finds the src a document gives for an asset, fingerprint and all.
func linkedScript(doc, asset string) (string, bool) {
	i := strings.Index(doc, `src="`+asset)
	if i < 0 {
		return "", false
	}
	rest := doc[i+len(`src="`):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}
