// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package cachepolicy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve runs one request through the middleware and returns the response.
func serve(t *testing.T, h http.HandlerFunc, prep func(*http.Request), opts ...func(*Options)) *http.Response {
	t.Helper()
	o := Options{SessionCookie: "tacit_session"}
	for _, f := range opts {
		f(&o)
	}
	req := httptest.NewRequest("GET", "http://tenant-a.tacit.zone/", nil)
	if prep != nil {
		prep(req)
	}
	rec := httptest.NewRecorder()
	Middleware(o)(h).ServeHTTP(rec, req)
	return rec.Result()
}

// The whole design in one test: saying nothing means private.
func TestUndeclaredIsPrivate(t *testing.T) {
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("a page nobody classified"))
	}, nil)
	if got := resp.Header.Get("Cache-Control"); got != PrivateControl {
		t.Fatalf("Cache-Control = %q, want %q", got, PrivateControl)
	}
}

// A handler that sets its own public header does not get to keep it. Route-level
// cache policy is exactly what the central classifier exists to overrule.
func TestRouteLevelHeaderIsOverridden(t *testing.T) {
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=600, s-maxage=600")
		w.Header().Set("Expires", "Thu, 01 Jan 2037 00:00:00 GMT")
		w.Header().Set("Pragma", "cache-me")
		_, _ = w.Write([]byte("private really"))
	}, nil)
	if got := resp.Header.Get("Cache-Control"); got != PrivateControl {
		t.Fatalf("Cache-Control = %q, want %q", got, PrivateControl)
	}
	for _, h := range []string{"Expires", "Pragma"} {
		if v := resp.Header.Get(h); v != "" {
			t.Fatalf("%s survived as %q; a second opinion about caching must not travel", h, v)
		}
	}
}

// Bucket A's header, and everything the guidance forbids it from carrying.
func TestPublicHeaderShape(t *testing.T) {
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		MarkPublic(w, r, "test")
		_, _ = w.Write([]byte("public"))
	}, nil)
	cc := resp.Header.Get("Cache-Control")
	for _, want := range []string{"public", "max-age=60", "stale-while-revalidate=86400", "stale-if-error=86400"} {
		if !strings.Contains(cc, want) {
			t.Errorf("public Cache-Control %q is missing %q", cc, want)
		}
	}
	for _, forbidden := range []string{"s-maxage", "must-revalidate", "proxy-revalidate",
		"no-cache", "private", "no-store", "no-transform"} {
		if strings.Contains(cc, forbidden) {
			t.Errorf("public Cache-Control %q carries the forbidden %q", cc, forbidden)
		}
	}
}

func TestImmutableHeaderShape(t *testing.T) {
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		MarkImmutable(w, r, "test")
		_, _ = w.Write([]byte("asset"))
	}, nil)
	if got := resp.Header.Get("Cache-Control"); got != ImmutableControl {
		t.Fatalf("Cache-Control = %q, want %q", got, ImmutableControl)
	}
}

// The first downgrade: a response that writes a cookie is about one reader,
// whatever the route believed it was serving.
func TestSetCookieDowngradesPublic(t *testing.T) {
	for _, declared := range []struct {
		name string
		mark func(http.ResponseWriter, *http.Request)
	}{
		{"A", func(w http.ResponseWriter, r *http.Request) { MarkPublic(w, r, "test") }},
		{"C", func(w http.ResponseWriter, r *http.Request) { MarkImmutable(w, r, "test") }},
	} {
		t.Run(declared.name, func(t *testing.T) {
			var rec Record
			resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
				declared.mark(w, r)
				http.SetCookie(w, &http.Cookie{Name: "visitor", Value: "1"})
				_, _ = w.Write([]byte("hello"))
			}, nil, func(o *Options) {
				o.Observe = func(_ *http.Request, got Record) { rec = got }
			})
			if got := resp.Header.Get("Cache-Control"); got != PrivateControl {
				t.Fatalf("Cache-Control = %q, want %q", got, PrivateControl)
			}
			if !rec.Downgraded || !rec.SetCookie {
				t.Fatalf("record should show a cookie downgrade, got %+v", rec)
			}
		})
	}
}

// The same downgrade, one step later. A public response is held back so its body
// can be hashed, so a cookie set after WriteHeader still travels to the reader —
// and it has to take the response private with it.
func TestLateSetCookieDowngradesPublic(t *testing.T) {
	for _, declared := range []struct {
		name string
		mark func(http.ResponseWriter, *http.Request)
	}{
		{"A", func(w http.ResponseWriter, r *http.Request) { MarkPublic(w, r, "test") }},
		{"C", func(w http.ResponseWriter, r *http.Request) { MarkImmutable(w, r, "test") }},
	} {
		t.Run(declared.name, func(t *testing.T) {
			var rec Record
			resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
				declared.mark(w, r)
				w.WriteHeader(http.StatusOK)
				http.SetCookie(w, &http.Cookie{Name: "visitor", Value: "1"})
				_, _ = w.Write([]byte("hello"))
			}, nil, func(o *Options) {
				o.Observe = func(_ *http.Request, got Record) { rec = got }
			})
			if got := resp.Header.Get("Cache-Control"); got != PrivateControl {
				t.Fatalf("Cache-Control = %q, want %q", got, PrivateControl)
			}
			if got := resp.Header.Get("ETag"); got != "" {
				t.Fatalf("a no-store response kept the validator %q", got)
			}
			if rec.Bucket != Private || !rec.Downgraded || !rec.SetCookie || rec.ETag {
				t.Fatalf("record should show a cookie downgrade with no validator, got %+v", rec)
			}
			if rec.CacheControl != PrivateControl {
				t.Fatalf("record Cache-Control = %q, want %q", rec.CacheControl, PrivateControl)
			}
			body, _ := io.ReadAll(resp.Body)
			if string(body) != "hello" {
				t.Fatalf("body = %q, want %q", body, "hello")
			}
		})
	}
}

// The second downgrade: a credential in the request. Bucket C survives it — a
// fingerprinted URL cannot vary by reader — and bucket A does not.
func TestCredentialDowngradesPublicButNotImmutable(t *testing.T) {
	cases := []struct {
		name string
		prep func(*http.Request)
	}{
		{"session cookie", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "tacit_session", Value: "signed"})
		}},
		{"bearer token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") }},
		{"registry key", func(r *http.Request) { r.Header.Set("X-Tacit-Key", "k") }},
	}
	for _, c := range cases {
		t.Run(c.name+"/public", func(t *testing.T) {
			resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
				MarkPublic(w, r, "test")
				_, _ = w.Write([]byte("hello"))
			}, c.prep)
			if got := resp.Header.Get("Cache-Control"); got != PrivateControl {
				t.Fatalf("Cache-Control = %q, want %q", got, PrivateControl)
			}
		})
		t.Run(c.name+"/immutable", func(t *testing.T) {
			resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
				MarkImmutable(w, r, "test")
				_, _ = w.Write([]byte("asset"))
			}, c.prep)
			if got := resp.Header.Get("Cache-Control"); got != ImmutableControl {
				t.Fatalf("Cache-Control = %q, want the immutable policy", got)
			}
		})
	}
}

// The third downgrade: only a 200 or a 304 may be cacheable. A redirect or an
// error goes private even when the route usually serves public content.
func TestNon200IsPrivate(t *testing.T) {
	for _, code := range []int{301, 302, 400, 404, 500} {
		resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
			MarkPublic(w, r, "test")
			w.WriteHeader(code)
			_, _ = w.Write([]byte("nope"))
		}, nil)
		if got := resp.Header.Get("Cache-Control"); got != PrivateControl {
			t.Errorf("status %d: Cache-Control = %q, want %q", code, got, PrivateControl)
		}
	}
}

// MarkPublicRefusal is the one exception, and it is narrow: a 404 only, and only
// where the handler asked for it.
func TestPublicRefusal(t *testing.T) {
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		MarkPublicRefusal(w, r, 60, "no instance here")
		http.Error(w, "no registry is published at this address", 404)
	}, nil)
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "public") {
		t.Fatalf("a declared refusal should stay public, got %q", got)
	}
	// And it does not spread to other statuses.
	resp = serve(t, func(w http.ResponseWriter, r *http.Request) {
		MarkPublicRefusal(w, r, 60, "no instance here")
		http.Error(w, "gone", http.StatusGone)
	}, nil)
	if got := resp.Header.Get("Cache-Control"); got != PrivateControl {
		t.Fatalf("a 410 refusal should be private, got %q", got)
	}
}

// A public response gets a validator hashed from the bytes actually sent, and a
// client holding it gets a 304 with no body.
func TestETagAndConditionalRequest(t *testing.T) {
	page := func(w http.ResponseWriter, r *http.Request) {
		MarkPublic(w, r, "test")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<p>the commons</p>"))
	}
	first := serve(t, page, nil)
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("a buffered public response carries no ETag")
	}
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Fatalf("ETag %q is not double-quoted; an unquoted validator is dropped, not repaired", etag)
	}
	body, _ := io.ReadAll(first.Body)
	if string(body) != "<p>the commons</p>" {
		t.Fatalf("body = %q", body)
	}

	second := serve(t, page, func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("matching If-None-Match got %d, want 304", second.StatusCode)
	}
	if b, _ := io.ReadAll(second.Body); len(b) != 0 {
		t.Fatalf("a 304 carried %d bytes of body", len(b))
	}
	if second.Header.Get("Cache-Control") == "" {
		t.Fatal("a 304 must still carry the policy")
	}
}

// Cloudflare weakens a strong ETag when compression changes what it transferred,
// so the W/ form has to match.
func TestWeakenedETagStillMatches(t *testing.T) {
	page := func(w http.ResponseWriter, r *http.Request) {
		MarkPublic(w, r, "test")
		_, _ = w.Write([]byte("body"))
	}
	etag := serve(t, page, nil).Header.Get("ETag")
	resp := serve(t, page, func(r *http.Request) {
		r.Header.Set("If-None-Match", `"someone-elses", W/`+etag)
	})
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("weakened ETag in a list got %d, want 304", resp.StatusCode)
	}
}

// A private response is not held in memory to be hashed: there is nothing to
// revalidate against a no-store policy.
func TestPrivateResponseGetsNoETag(t *testing.T) {
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("yours"))
	}, nil)
	if got := resp.Header.Get("ETag"); got != "" {
		t.Fatalf("private response carries ETag %q", got)
	}
}

// A handler with its own validator keeps it — the asset handlers hash their
// embedded bytes once at startup rather than per request.
func TestHandlerETagIsKept(t *testing.T) {
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		MarkImmutable(w, r, "asset")
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte("asset bytes"))
	}, nil)
	if got := resp.Header.Get("ETag"); got != `"abc123"` {
		t.Fatalf("ETag = %q, want the handler's own", got)
	}
}

// A streaming response is classified before its first byte and then left alone.
// Buffering it to manufacture a validator is exactly what §6.3 forbids.
func TestFlushEndsBufferingWithoutLosingThePolicy(t *testing.T) {
	var rec Record
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		MarkPublic(w, r, "feed")
		_, _ = w.Write([]byte("first chunk"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte(" second chunk"))
	}, nil, func(o *Options) {
		o.Observe = func(_ *http.Request, got Record) { rec = got }
	})
	if !strings.Contains(resp.Header.Get("Cache-Control"), "public") {
		t.Fatalf("streaming lost the policy: %q", resp.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("ETag") != "" {
		t.Fatal("a streamed response should carry no rendered-body validator")
	}
	if !rec.Streamed {
		t.Fatalf("record should say it streamed, got %+v", rec)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "first chunk second chunk" {
		t.Fatalf("body = %q; flushing must not lose or reorder bytes", body)
	}
}

// Past the buffer cap the response streams rather than growing the heap, and
// every byte still arrives exactly once.
func TestOversizedBodyStreamsIntact(t *testing.T) {
	big := strings.Repeat("x", 5000)
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		MarkPublic(w, r, "big")
		for i := 0; i < 3; i++ {
			_, _ = w.Write([]byte(big))
		}
	}, nil, func(o *Options) { o.MaxETagBytes = 4096 })
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 3*len(big) {
		t.Fatalf("body = %d bytes, want %d", len(body), 3*len(big))
	}
	if resp.Header.Get("ETag") != "" {
		t.Fatal("an oversized body should not be hashed")
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "public") {
		t.Fatal("oversized body lost the policy")
	}
}

// Vary is never load-bearing here. Values Cloudflare does not key on are
// stripped, so nothing downstream can come to believe they separate entries.
func TestVaryIsScrubbed(t *testing.T) {
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		MarkPublic(w, r, "test")
		w.Header().Add("Vary", "Cookie, Accept-Language")
		w.Header().Add("Vary", "X-Tenant-Setting")
		w.Header().Add("Vary", "User-Agent")
		_, _ = w.Write([]byte("hi"))
	}, nil)
	vary := resp.Header.Get("Vary")
	for _, gone := range []string{"Cookie", "X-Tenant-Setting", "User-Agent"} {
		if strings.Contains(vary, gone) {
			t.Errorf("Vary %q still claims to separate on %q", vary, gone)
		}
	}
	if !strings.Contains(vary, "Accept-Language") {
		t.Errorf("Vary %q dropped a value that is not about cache separation", vary)
	}
}

// A handler that writes nothing at all still gets a policy.
func TestEmptyResponseIsClassified(t *testing.T) {
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {}, nil)
	if got := resp.Header.Get("Cache-Control"); got != PrivateControl {
		t.Fatalf("Cache-Control = %q, want %q", got, PrivateControl)
	}
}

// Without the middleware the helpers still say something true — a handler shared
// with a mux that has not been wrapped must not fall silent.
func TestHelpersWorkWithoutTheMiddleware(t *testing.T) {
	for _, c := range []struct {
		name string
		mark func(http.ResponseWriter, *http.Request)
		want string
	}{
		{"public", func(w http.ResponseWriter, r *http.Request) { MarkPublic(w, r, "x") }, PublicControl(DefaultMaxAge)},
		{"immutable", func(w http.ResponseWriter, r *http.Request) { MarkImmutable(w, r, "x") }, ImmutableControl},
		{"private", func(w http.ResponseWriter, r *http.Request) { MarkPrivate(w, r, "x") }, PrivateControl},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		c.mark(rec, req)
		if got := rec.Header().Get("Cache-Control"); got != c.want {
			t.Errorf("%s without middleware = %q, want %q", c.name, got, c.want)
		}
	}
}

// The counters are what /v1/health reports; they have to move.
func TestCountersTally(t *testing.T) {
	counters := &Counters{}
	mw := Middleware(Options{SessionCookie: "tacit_session", Counters: counters})
	for _, h := range []http.HandlerFunc{
		func(w http.ResponseWriter, r *http.Request) { MarkPublic(w, r, "a"); _, _ = w.Write([]byte("1")) },
		func(w http.ResponseWriter, r *http.Request) { MarkImmutable(w, r, "c"); _, _ = w.Write([]byte("2")) },
		func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("3")) },
	} {
		mw(h).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}
	got := counters.Snapshot()
	for key, want := range map[string]int64{"public": 1, "immutable": 1, "private": 1, "undeclared": 1} {
		if got[key] != want {
			t.Errorf("counter %s = %d, want %d (all: %v)", key, got[key], want, got)
		}
	}
}

// Two tenants asking for the same path get their own answers: nothing in the
// classification is shared process state, and the record names the host it
// described. Hostname isolation itself is Cloudflare's cache key, which this
// cannot test — but a classifier that mixed up hosts would break it.
func TestRecordNamesItsTenant(t *testing.T) {
	seen := map[string]string{}
	mw := Middleware(Options{
		SessionCookie: "tacit_session",
		Observe:       func(r *http.Request, rec Record) { seen[rec.Host] = rec.Reason },
	})
	for _, host := range []string{"tenant-a.tacit.zone", "tenant-b.tacit.zone"} {
		req := httptest.NewRequest("GET", "http://"+host+"/f/public/feed.json", nil)
		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			MarkPublic(w, r, "commons on "+r.Host)
			_, _ = w.Write([]byte("{}"))
		})).ServeHTTP(httptest.NewRecorder(), req)
	}
	for _, host := range []string{"tenant-a.tacit.zone", "tenant-b.tacit.zone"} {
		if want := "commons on " + host; seen[host] != want {
			t.Errorf("record for %s = %q, want %q", host, seen[host], want)
		}
	}
}

// A HEAD request is classified and validated like the GET it stands in for.
func TestHeadIsClassified(t *testing.T) {
	req := httptest.NewRequest("HEAD", "http://tenant-a.tacit.zone/", nil)
	rec := httptest.NewRecorder()
	Middleware(Options{SessionCookie: "tacit_session"})(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			MarkPublic(w, r, "front door")
			_, _ = w.Write([]byte("<p>door</p>"))
		})).ServeHTTP(rec, req)
	resp := rec.Result()
	if !strings.Contains(resp.Header.Get("Cache-Control"), "public") {
		t.Fatalf("HEAD Cache-Control = %q", resp.Header.Get("Cache-Control"))
	}
	if resp.Header.Get("ETag") == "" {
		t.Fatal("HEAD should carry the same validator as the GET")
	}
}

// The Content-Length a buffered response reports is the length it sends.
func TestBufferedContentLength(t *testing.T) {
	body := "<p>a rendered page</p>"
	resp := serve(t, func(w http.ResponseWriter, r *http.Request) {
		MarkPublic(w, r, "page")
		_, _ = w.Write([]byte(body))
	}, nil)
	if got := resp.Header.Get("Content-Length"); got != fmt.Sprint(len(body)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(body))
	}
}
