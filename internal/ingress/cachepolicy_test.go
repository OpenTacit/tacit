// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"net/http"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/cachepolicy"
)

// The ingress's side of the cache policy. Its job is almost entirely negative:
// do not touch what the registry decided, and classify only the answers the
// ingress invents.

// A proxied response arrives at the edge exactly as the origin wrote it. If the
// ingress ever stamped its own policy over a tunnelled response, every public page
// in the fleet would become uncacheable and nobody would see an error — which is
// why this is a test and not a comment.
func TestProxiedCachePolicyPassesThroughUntouched(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Cache-Control", cachepolicy.PublicControl(cachepolicy.DefaultMaxAge))
			w.Header().Set("ETag", `"originvalidator"`)
		case "/assets/app.css":
			w.Header().Set("Cache-Control", cachepolicy.ImmutableControl)
		default:
			w.Header().Set("Cache-Control", cachepolicy.PrivateControl)
		}
		_, _ = w.Write([]byte("from the registry"))
	}))

	for path, want := range map[string]string{
		"/":               cachepolicy.PublicControl(cachepolicy.DefaultMaxAge),
		"/assets/app.css": cachepolicy.ImmutableControl,
		"/settings":       cachepolicy.PrivateControl,
	} {
		resp, body := h.get(in.Name, path)
		if body != "from the registry" {
			t.Fatalf("%s did not reach the registry: %q", path, body)
		}
		if got := resp.Header.Get("Cache-Control"); got != want {
			t.Errorf("%s Cache-Control = %q, want the origin's %q", path, got, want)
		}
	}
	// The origin's validator survives too, so its conditional requests keep
	// working through the proxy.
	resp, _ := h.get(in.Name, "/")
	if got := resp.Header.Get("ETag"); got != `"originvalidator"` {
		t.Errorf("ETag = %q, want the origin's", got)
	}
}

// A hostname under the zone that names no instance. Scanners walk these all day,
// so the refusal is cacheable — it is one fixed sentence with nothing of anyone's
// in it, and every edge hit is an origin request that never happens.
func TestUnknownHostRefusalIsCacheable(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequest("GET", h.public.URL+"/", nil)
	req.Host = "nobody.test.local"
	resp, err := h.public.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	cc := resp.Header.Get("Cache-Control")
	if !strings.Contains(cc, "public") || !strings.Contains(cc, "stale-if-error") {
		t.Errorf("Cache-Control = %q, want the public refusal policy", cc)
	}
	for _, forbidden := range []string{"s-maxage", "must-revalidate", "no-cache", "no-store", "private"} {
		if strings.Contains(cc, forbidden) {
			t.Errorf("refusal policy %q carries the forbidden %q", cc, forbidden)
		}
	}
}

// Cache deception, refused before the tunnel: a path wearing an image extension
// that names no prefix the registry serves never reaches the origin, so there is
// no chance of an application page being cached as a static file. Cloudflare's
// Cache Deception Armor sits behind this rather than in front of it.
func TestCacheDeceptionPathsAreRefused(t *testing.T) {
	h := newHarness(t)
	reached := make(chan string, 8)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- r.URL.Path
		_, _ = w.Write([]byte("<html>the dashboard</html>"))
	}))

	for _, path := range []string{"/dashboard/foo.jpg", "/wp-admin/x.png", "/style.css"} {
		resp, body := h.get(in.Name, path)
		if resp.StatusCode != 404 {
			t.Errorf("%s status = %d, want 404", path, resp.StatusCode)
		}
		if strings.Contains(body, "the dashboard") {
			t.Errorf("%s returned application HTML", path)
		}
	}
	select {
	case p := <-reached:
		t.Errorf("%s reached the registry; the proxy should have refused it", p)
	default:
	}
}

// A transient refusal must not be cached: an outage that outlived itself would be
// worse than the outage.
func TestTransientRefusalsAreNotCacheable(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	h.srv.DropTunnel(in.Name)

	resp, _ := h.get(in.Name, "/")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
		t.Errorf("a disconnected registry answered %q, want %q", got, cachepolicy.PrivateControl)
	}

	// A suspension is lifted by hand, so nobody should wait out a TTL for it.
	h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	if err := h.srv.Store.SetDisabled(h.publishedName, true); err != nil {
		t.Fatal(err)
	}
	resp, _ = h.get(h.publishedName, "/")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("suspended status = %d, want 403", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
		t.Errorf("a suspended instance answered %q, want %q", got, cachepolicy.PrivateControl)
	}
}

// The console is an operator's view of the whole fleet. Every page of it is
// private, and it says so rather than relying on being behind a sign-in.
func TestConsolePagesArePrivate(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/", "/instances", "/ops", "/settings", "/health"} {
		req, _ := http.NewRequest("GET", h.public.URL+path, nil)
		req.Host = "localhost"
		resp, err := h.public.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
			t.Errorf("console %s = %q, want %q", path, got, cachepolicy.PrivateControl)
		}
	}
	// Its shared chrome is the exception, and it is fingerprinted.
	req, _ := http.NewRequest("GET", h.public.URL+"/assets/app.css?v=x", nil)
	req.Host = "localhost"
	resp, err := h.public.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != cachepolicy.ImmutableControl {
		t.Errorf("console stylesheet = %q, want %q", got, cachepolicy.ImmutableControl)
	}
}

// The console session cookie is host-only, like the registry's. A parent-domain
// cookie here would be sent to every tenant hostname in the zone and bypass the
// cache across the whole fleet.
func TestConsoleSessionCookieIsHostOnly(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequest("GET", h.public.URL+"/auth/logout", nil)
	req.Host = "localhost"
	// No redirect following: the Set-Cookie is on the 302 itself.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, raw := range resp.Header.Values("Set-Cookie") {
		if strings.Contains(strings.ToLower(raw), "domain=") {
			t.Errorf("console cookie carries a Domain: %q", raw)
		}
	}
}
