// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/suggest"
)

// postSuggest posts the Drafts page's research button the way its script does —
// with fetch's Accept: application/json — and returns the status and the decoded
// body.
func postSuggest(t *testing.T, url string) (int, map[string]string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url+"/admin/suggest", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func suggestStatus(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url + "/admin/suggest/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// TestSuggestBusyIsNotAFailure is the bug this guards: a click that lands while
// a research pass is already running used to answer 503 with the reason only in
// a body the page threw away, so a healthy run about to file ten drafts read as
// "Suggestion run failed (503)". Busy is 409 and says so; the page can then wait
// for the run instead of reporting it broken.
func TestSuggestBusyIsNotAFailure(t *testing.T) {
	srv, ts := newServer(t)
	srv.Suggest = fakeResearcher{}

	if !suggestRuns.tryStart() {
		t.Fatal("gate was not idle at the start of the test")
	}
	defer suggestRuns.done()

	code, body := postSuggest(t, ts.URL)
	if code != http.StatusConflict {
		t.Fatalf("busy suggest = %d, want 409 — a run in progress is not a failed run", code)
	}
	if !strings.Contains(body["error"], "already in progress") {
		t.Fatalf("busy suggest gave no reason: %v", body)
	}

	// The run is visible to any page that asks, which is what lets a reloaded
	// Drafts page show it rather than an armed button inviting a second click.
	st := suggestStatus(t, ts.URL)
	if st["running"] != true {
		t.Fatalf("status during a run: %v", st)
	}
	code, page := fetchHTML(t, ts.URL+"/review")
	if code != 200 || !strings.Contains(page, `data-running="0"`) {
		t.Fatalf("review page did not carry the in-flight run: %d", code)
	}

	suggestRuns.done()
	if st := suggestStatus(t, ts.URL); st["running"] != false {
		t.Fatalf("status after a run: %v", st)
	}
	if _, page := fetchHTML(t, ts.URL+"/review"); !strings.Contains(page, `data-running="-1"`) {
		t.Fatal("review page shows a run in flight when the gate is idle")
	}
}

// TestSuggestNoResearcherIs503 keeps the other half of the split honest: a
// missing model key is a real outage the operator must fix, and stays 503.
func TestSuggestNoResearcherIs503(t *testing.T) {
	_, ts := newServer(t) // no srv.Suggest, and newServer's env carries no key
	t.Setenv("TACIT_LLM_API_KEY", "")
	code, body := postSuggest(t, ts.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("suggest with no researcher = %d, want 503", code)
	}
	if !strings.Contains(body["error"], "TACIT_LLM_API_KEY") {
		t.Fatalf("503 did not name what to configure: %v", body)
	}
}

// TestSuggestFormReleasesTheGate: the button's handler must free the slot when
// the research pass fails, or one bad run wedges every later one behind a
// refusal for the life of the process.
func TestSuggestFormReleasesTheGate(t *testing.T) {
	srv, ts := newServer(t)
	srv.Suggest = failingResearcher{}

	code, _ := postSuggest(t, ts.URL)
	if code != http.StatusBadGateway {
		t.Fatalf("failed research = %d, want 502 (a broken pass, not a missing researcher)", code)
	}
	if st := suggestStatus(t, ts.URL); st["running"] != false {
		t.Fatalf("gate still held after a failed run: %v", st)
	}
}

type failingResearcher struct{}

func (failingResearcher) Research(string) ([]suggest.Draft, error) {
	return nil, errString("the model refused")
}

type errString string

func (e errString) Error() string { return string(e) }
