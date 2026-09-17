// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/config"
)

// A single-member registry used to lead with the user guide — nav item and
// "/" redirect both — because the alternative was Outcomes drawn as fourteen
// empty panels, which answered a question its owner had not asked. Outcomes
// opens with what the registry holds and what to do next now, so the owner gets
// a next step instead of a twenty-six-chapter table of contents, and the nav is
// the same three questions for everybody.
func TestTheOwnerLandsOnSomethingToDo(t *testing.T) {
	srv, base := ownerServer(t)

	items := srv.navFor()
	if len(items) == 0 || items[0].label != "Outcomes" {
		t.Fatalf("nav = %+v, want Outcomes first, as on every registry", items)
	}
	for _, item := range items {
		if item.key == "docs" {
			t.Error("the guide is back in the top nav; it belongs in the account menu")
		}
	}

	req, _ := http.NewRequest("GET", base+"/", nil)
	req.AddCookie(ownerCookie(t, base))
	resp, err := noRedirects().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / signed in = %d, want the dashboard rendered", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// The welcome, which is what orients a reader on a registry that has
	// measured nothing. It used to be the hero's empty funnel; that hero is
	// gone from this view, because it could only ever draw three noughts here.
	if !strings.Contains(string(body), `class="cs-welcome"`) {
		t.Error("the owner's landing page does not orient the reader")
	}
	if !strings.Contains(string(body), `href="/review"`) {
		t.Error("the owner's landing page offers nothing to do next")
	}
	// And the guide is still one click away, from that same screen.
	if !strings.Contains(string(body), "/docs/user-guide") {
		t.Error("the landing page does not link to the guide")
	}
}

func TestAVisitorWithNoSessionStillGetsTheFrontDoor(t *testing.T) {
	// "/" is the address somebody is handed. Answering it with documentation
	// would replace the one page that says whose registry this is and how to
	// get in.
	_, base := ownerServer(t)
	resp, err := noRedirects().Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / anonymous = %d, want the front door rendered", resp.StatusCode)
	}
	body := readAll(t, resp)
	if !strings.Contains(body, "tacit dashboard") {
		t.Error("the front door does not say how to sign in")
	}
}

// ownerCookie signs in and returns the session.
func ownerCookie(t *testing.T, base string) *http.Cookie {
	t.Helper()
	resp, err := noRedirects().Get(OwnerLink(base, "an-owner-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestAnOrganizationsRegistryStillLeadsWithOutcomes(t *testing.T) {
	srv, ts := newServer(t)
	if got := srv.navFor()[0].label; got != "Outcomes" {
		t.Errorf("nav[0] = %q, want Outcomes", got)
	}
	for _, item := range srv.navFor() {
		if item.label == "User Guide" {
			t.Error("an organization's nav gained a User Guide tab")
		}
	}
	resp, err := noRedirects().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / = %d, want the dashboard rendered", resp.StatusCode)
	}
	_ = config.AuthOwner
}
