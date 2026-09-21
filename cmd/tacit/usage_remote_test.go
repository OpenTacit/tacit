// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"
)

// The registry /mcp endpoint is shared and identity-less, so tacit_usage there
// must NEVER disclose the host's usage unless the operator opts in for a
// single-member deployment. Default (flag off): a pointer app, no data.
func TestRemoteUsageFuncPointerByDefault(t *testing.T) {
	t.Setenv("TACIT_LOCAL_USAGE", "") // explicitly off
	path := t.TempDir() + "/usage.jsonl"
	_ = os.WriteFile(path, []byte(`{"ts":"2026-07-20T12:00:00Z","kind":"query"}`+"\n"), 0o600)

	html, _, summary, err := remoteUsageFunc(path)("30d")
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := summary["totals"]; leaked {
		t.Fatalf("flag off must not disclose host usage totals: %v", summary)
	}
	if summary["local_only"] != true {
		t.Fatalf("flag off should mark local_only: %v", summary)
	}
	if !strings.Contains(html, "stays on your machine") {
		t.Errorf("pointer app should explain usage is local: %q", html)
	}
}

// Flag on (self-hosted, single-member): render the co-located log as the app —
// the same source the CLI reads.
func TestRemoteUsageFuncDelegatesWhenEnabled(t *testing.T) {
	t.Setenv("TACIT_LOCAL_USAGE", "1")
	path := t.TempDir() + "/usage.jsonl"
	_ = os.WriteFile(path, []byte(
		`{"ts":"2026-07-20T12:00:00Z","kind":"query"}`+"\n"+
			`{"ts":"2026-07-20T12:01:00Z","kind":"shown","cap":"c1","name":"Technique one"}`+"\n"), 0o600)

	html, _, summary, err := remoteUsageFunc(path)("all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "<!doctype html") {
		t.Error("delegated result should be the rendered app")
	}
	tot, ok := summary["totals"].(map[string]any)
	if !ok || tot["queries"] != 1 || tot["shown"] != 1 {
		t.Fatalf("delegated totals wrong: %v", summary["totals"])
	}
}
