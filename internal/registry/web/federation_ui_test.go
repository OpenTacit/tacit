// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The Federation page is flat by design: entity rows directly on the page
// plane, hairline-ruled section headers, no panels-in-panels — the seven-column
// subscriptions table that was unusable on a phone is gone. This test pins the
// structure so it can't quietly re-nest.
func TestFederationPageIsFlatAndPhoneReady(t *testing.T) {
	_, ts := newServer(t)

	// Subscribe so every section renders populated.
	form := url.Values{"name": {"Platform feed"},
		"feed_url": {"https://example.test/f/general/feed.json"}, "trust": {"review"}}
	resp, err := http.PostForm(ts.URL+"/admin/subscriptions", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	_, fed := fetchHTML(t, ts.URL+"/federation")

	if strings.Contains(fed, `<section class="panel">`) {
		t.Fatal("federation page has re-grown panels — the flat layout is the contract")
	}
	for _, want := range []string{
		`class="fed-item"`,           // entity rows
		`class="fed-h"`,              // hairline section headers
		`class="fed-url"`,            // URLs on their own wrapping line
		`class="danger"`,             // destructive actions carry the red-filled danger style
		`action="/admin/poll-feeds"`, // the manual poll, previously API-only
		`class="form-grid"`,          // the flat add-feed form
		`name="feed_url"`,            // existing contract: the form field
		`>Platform feed</a>`,         // existing contract: row linked by name
		`last poll no poll yet`,      // a new feed states its polling status
	} {
		if !strings.Contains(fed, want) {
			t.Errorf("federation page missing %q", want)
		}
	}
	// No subscriptions/publishing table remains on the main page (the feed
	// DETAIL page keeps its genuinely tabular local-vs-attested comparison).
	if strings.Contains(fed, `<table>`) {
		t.Error("a bare table crept back onto the main federation page")
	}
}

// Poll now: the browser counterpart of POST /v1/admin/poll-feeds. It must
// round-trip back to the page rather than dumping JSON at the member.
func TestFederationPollNowRoundTrips(t *testing.T) {
	_, ts := newServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Post(ts.URL+"/admin/poll-feeds", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/federation" {
		t.Fatalf("poll-now = %d -> %q, want 302 -> /federation", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// THE TWO COLUMNS READ IN THE ORDER THE PICTURE DRAWS THEM. The drawing puts
// what arrives on the left and what leaves on the right; the columns under it
// were the other way round, so a reader who had just followed a feed in from the
// left edge found its list on the right. Order in the DOM, not a CSS flip, so
// the tab order and a screen reader follow the drawing as well.
//
// The picture's own sides are read out of the stylesheet rather than assumed:
// turn the drawing around and this fails, which is the point — the columns are
// not independently correct, they are correct relative to it.
func TestFederationColumnsFollowThePictureTheySitUnder(t *testing.T) {
	_, ts := newServer(t)
	resp, err := http.Get(ts.URL + "/federation")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	arrives := strings.Index(body, "What arrives from others")
	leaves := strings.Index(body, "What leaves this registry")
	if arrives < 0 || leaves < 0 {
		t.Fatal("the page is missing one of its two directions")
	}
	peer := fedNum(t, ".fed-peer", "left") // where a feed comes in
	port := fedNum(t, ".fed-port", "left") // where a channel goes out
	if peer > port {
		t.Fatalf("the picture draws arrivals at %.1fem and departures at %.1fem: "+
			"it has been turned around, and the columns below have to follow", peer, port)
	}
	if arrives > leaves {
		t.Error("the picture puts what arrives on the left, but the columns lead with what leaves")
	}
}
