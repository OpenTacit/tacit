// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The product's domain and the tenants' zone are different things, and this file
// is the line between them. A tenant hostname is a name allocated to a stranger's
// data; the project page is the first thing a stranger reads. They share a binary
// and nothing else.
//
// What these tests rule out is the arrangement that looks equivalent and is not:
// serving the page at both, so that "where does this live" has two answers, two
// cache keys and two documents to review.

// hostGet issues a request to the public listener with an arbitrary Host, which
// is the only thing routing looks at. Redirects are not followed — where a
// hostname sends somebody is itself the assertion.
func (h *harness) hostGet(host, path string) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequest("GET", h.public.URL+path, nil)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	req.Host = host
	client := *h.public.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestProductDomainCarriesThePageAndNoInstances(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.SiteHost = "product.example"
	h.srv.Cfg.SitePublish = true

	resp, body := h.hostGet("product.example", "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the product domain answered %d, want the project page", resp.StatusCode)
	}
	if !strings.Contains(body, siteMarker) {
		t.Fatal("the product domain did not serve the project page")
	}

	// A registry publishes under the ZONE and is reachable there and nowhere
	// else. A tenant hostname under the product's domain is not a second address
	// for that registry; it is nobody's.
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("registry"))
	}))
	if resp, _ := h.hostGet(in.Name+".test.local", "/v1/health"); resp.StatusCode != http.StatusOK {
		t.Fatalf("%s.test.local answered %d, want 200", in.Name, resp.StatusCode)
	}
	if resp, _ := h.hostGet(in.Name+".product.example", "/v1/health"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("%s.product.example answered %d — the product's domain must carry no instances",
			in.Name, resp.StatusCode)
	}
	if got := h.srv.Cfg.PublicURL(in.Name); !strings.Contains(got, ".test.local") {
		t.Errorf("published URL is %q, want the zone", got)
	}
}

// The zone's apex moves rather than forking. Anybody who wrote down the old
// address — and the install command is the address people write down — still
// arrives, at the page's one home.
func TestZoneApexRedirectsToTheProductDomain(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.SiteHost = "product.example"
	h.srv.Cfg.SitePublish = true

	for _, path := range []string{"/", "/install.sh"} {
		resp, _ := h.hostGet("test.local", path)
		if resp.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("the zone apex answered %d for %s, want 301", resp.StatusCode, path)
		}
		if want := h.srv.Cfg.SiteURL() + path; resp.Header.Get("Location") != want {
			t.Errorf("apex %s redirected to %q, want %q", path, resp.Header.Get("Location"), want)
		}
	}

	// And it does NOT serve the page itself. Two addresses for one document is
	// the thing this arrangement exists to avoid.
	if _, body := h.hostGet("test.local", "/"); strings.Contains(body, siteMarker) {
		t.Error("the zone apex still serves the project page as well as redirecting")
	}
}

// With no product domain configured, nothing moves: the apex IS the page's
// address, which is what a single-domain deployment and a laptop both want.
func TestWithoutAProductDomainTheApexStillServesThePage(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.SitePublish = true

	resp, body := h.hostGet("test.local", "/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, siteMarker) {
		t.Fatalf("the apex answered %d without serving the page", resp.StatusCode)
	}
	if h.srv.Cfg.SiteElsewhere() {
		t.Error("SiteElsewhere is true with no SiteHost set")
	}
}

// The console operates the proxy, so it stays on the proxy's own domain. Putting
// it on the product's would be a second sign-in host, a second registered
// callback, and an operator surface on the one address strangers are sent to.
func TestConsoleStaysUnderTheZone(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.SiteHost = "product.example"

	if resp, _ := h.hostGet("ingress.test.local", "/health"); resp.StatusCode != http.StatusOK {
		t.Fatalf("the console answered %d under the zone", resp.StatusCode)
	}
	if resp, _ := h.hostGet("ingress.product.example", "/health"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("ingress.product.example answered %d — the console does not live on the product's domain",
			resp.StatusCode)
	}
}

// Two sign-in hosts, and the page's callback follows the page. Registering the
// zone's apex instead is the failure that shows up at the identity provider
// rather than here.
func TestCallbacksFollowTheSplit(t *testing.T) {
	cfg := Config{
		Zone:            "tacit.zone",
		SiteHost:        "opentacit.com",
		Scheme:          "https",
		PublicPort:      "443",
		OIDCRedirectURI: "https://ingress.tacit.zone/auth/callback",
	}.withDefaults()

	want := map[string]string{
		"ingress.tacit.zone": "https://ingress.tacit.zone/auth/callback",
		"opentacit.com":      "https://opentacit.com/auth/callback",
	}
	got := cfg.RedirectURIs()
	if len(got) != len(want) {
		t.Fatalf("got %d callbacks, want %d: %v", len(got), len(want), got)
	}
	for host, uri := range want {
		if got[host] != uri {
			t.Errorf("callback for %s is %q, want %q", host, got[host], uri)
		}
	}
	if cfg.SiteURL() != "https://opentacit.com" {
		t.Errorf("SiteURL is %q, want the product's domain", cfg.SiteURL())
	}
	if cfg.HostFor("cedar-hollow") != "cedar-hollow.tacit.zone" {
		t.Errorf("HostFor names %q, want the zone", cfg.HostFor("cedar-hollow"))
	}
	// Case and stray whitespace are an operator's environment variable, not a
	// different domain.
	loud := cfg
	loud.SiteHost = " OpenTacit.COM "
	if !loud.IsSiteHost("opentacit.com:443") || loud.SiteHostname() != "opentacit.com" {
		t.Error("the product domain is not normalised")
	}
}
