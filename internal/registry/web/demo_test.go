// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/oidc"
)

func TestDemoMenuHTML(t *testing.T) {
	s := &Server{}
	if got := s.demoHTML(); got != "" {
		t.Fatalf("demo mode off should render nothing, got %q", got)
	}

	catalog := &DemoCatalog{}
	catalog.Add("software-vendor", "B2B software vendor")
	catalog.Add("cpg-retailer", "CPG retailer")
	catalog.Add("broken", "broken.json")
	catalog.SetReady("software-vendor")
	catalog.SetErr("broken", "dataset JSON: boom")

	// The production instance: its entry is current, ready scenarios are
	// selectable, the still-loading and failed ones are visible but disabled.
	s.Demo = &DemoState{Catalog: catalog}
	got := s.demoHTML()
	for _, want := range []string{
		`id="demo-btn"`,
		`action="/demo/switch"`,
		`aria-checked="true" name="to" value=""`, // production is current here
		`Production data`,
		`value="software-vendor"`,
		`CPG retailer (loading…)`,
		`broken.json (unavailable)`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("production menu missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "demo-on") {
		t.Fatal("production instance must not carry the demo-active accent")
	}
	// Options that can't be selected are disabled submit buttons.
	if !strings.Contains(got, ` disabled`) {
		t.Fatalf("loading/failed scenarios should be disabled:\n%s", got)
	}

	// The button names the dataset in view: "Production" on production data.
	if !strings.Contains(got, `>Production</button>`) {
		t.Fatalf("production button should read Production:\n%s", got)
	}

	// A demo instance: its own scenario is current, the button is accented and
	// carries the scenario's title.
	s.Demo = &DemoState{Catalog: catalog, Active: "software-vendor"}
	got = s.demoHTML()
	if !strings.Contains(got, "demo-on") {
		t.Fatalf("active scenario should accent the Demo button:\n%s", got)
	}
	if !strings.Contains(got, `>B2B software vendor</button>`) {
		t.Fatalf("active scenario's title should be the button label:\n%s", got)
	}
	if !strings.Contains(got, `aria-checked="true" name="to" value="software-vendor"`) {
		t.Fatalf("active scenario should be checked:\n%s", got)
	}
}

func TestDemoSwitchSetsCookieAndRedirects(t *testing.T) {
	// The route exists only where demonstration mode does (routes_service.go).
	t.Setenv("TACIT_DEMO_DIR", t.TempDir())
	srv, ts := newServer(t)
	catalog := &DemoCatalog{}
	catalog.Add("software-vendor", "B2B software vendor")
	catalog.Add("cpg-retailer", "CPG retailer") // never ready
	catalog.SetReady("software-vendor")
	srv.Demo = &DemoState{Catalog: catalog}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	post := func(to, referer string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", ts.URL+"/demo/switch",
			strings.NewReader(url.Values{"to": {to}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	resp := post("software-vendor", "")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("switch status = %d, want 303", resp.StatusCode)
	}
	var set *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == DemoCookie {
			set = c
		}
	}
	if set == nil || set.Value != "software-vendor" || !set.HttpOnly {
		t.Fatalf("expected HttpOnly %s=software-vendor cookie, got %+v", DemoCookie, set)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("with no referer the switch should land on the dashboard home, got %q", loc)
	}

	// Switching keeps the reader on the view they were on, query intact.
	resp = post("software-vendor", ts.URL+"/techniques/map?group=cohort")
	if loc := resp.Header.Get("Location"); loc != "/techniques/map?group=cohort" {
		t.Fatalf("switch should stay on the current view, got %q", loc)
	}
	// A foreign or protocol-relative referer never becomes a redirect target.
	resp = post("software-vendor", "https://evil.example//attacker")
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("protocol-relative referer path must fall back to home, got %q", loc)
	}

	// A scenario that is not ready must not become the selection.
	resp = post("cpg-retailer", "")
	for _, c := range resp.Cookies() {
		if c.Name == DemoCookie {
			t.Fatalf("not-ready scenario must not set the cookie, got %+v", c)
		}
	}

	// Back to production: the cookie is cleared, not stored empty.
	resp = post("", "")
	set = nil
	for _, c := range resp.Cookies() {
		if c.Name == DemoCookie {
			set = c
		}
	}
	if set == nil || set.MaxAge >= 0 {
		t.Fatalf("switching to production should expire the cookie, got %+v", set)
	}
}

func TestDemoRouter(t *testing.T) {
	mark := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(name))
		})
	}
	router := DemoRouter(mark("prod"), map[string]http.Handler{"software-vendor": mark("demo")})

	serve := func(cookie string) string {
		req := httptest.NewRequest("GET", "/", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: DemoCookie, Value: cookie})
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	if got := serve(""); got != "prod" {
		t.Fatalf("no cookie should reach production, got %q", got)
	}
	if got := serve("software-vendor"); got != "demo" {
		t.Fatalf("scenario cookie should reach the demo instance, got %q", got)
	}
	// A stale cookie (scenario removed from TACIT_DEMO_DIR) falls back.
	if got := serve("gone"); got != "prod" {
		t.Fatalf("unknown scenario cookie should fall back to production, got %q", got)
	}
}

// Demonstration mode is for the instance that demonstrates the product, and
// TACIT_DEMO_DIR is the whole of the switch. With it unset, a registry carries
// no trace of the scenarios: no menu in the top bar, no switch route, and a
// tacit_demo cookie that routes nothing and so changes nothing — a visitor who
// invents one must not be able to turn every cacheable page private with it.
func TestWithoutADemoDirTheDemoCookieRoutesNothing(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	if srv.Demo != nil {
		t.Fatal("no TACIT_DEMO_DIR, yet the server carries demonstration state")
	}
	if got := srv.demoHTML(); got != "" {
		t.Fatalf("top bar should have no Demo control, got %q", got)
	}

	req, err := http.NewRequest("POST", ts.URL+"/demo/switch", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST /demo/switch = %d, want 404 — the route should not exist", resp.StatusCode)
	}

	// The same public page, with and without a cookie nobody here reads.
	plain := header(t, ts.URL+"/docs/user-guide", nil).Header.Get("Cache-Control")
	if !strings.Contains(plain, "public") {
		t.Fatalf("user guide = %q, want public", plain)
	}
	withCookie := header(t, ts.URL+"/docs/user-guide", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: DemoCookie, Value: "invented"})
	}).Header.Get("Cache-Control")
	if withCookie != plain {
		t.Errorf("a made-up %s cookie changed the answer to %q, want %q", DemoCookie, withCookie, plain)
	}
}
