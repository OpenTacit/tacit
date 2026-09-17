// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package cachepolicy is the one place a response's cacheability is decided.
//
// Every tenant of the fleet — a tenant's hostname, the next org's hostname, the
// console on the admin host — runs this same code behind the same Cloudflare
// zone, so a single response that says "public" when it means "yours" would put
// one member's dashboard into a shared cache for the next visitor. The rule the
// package exists to hold is narrow enough to check:
//
//	A response may be public only when every anonymous visitor asking for the
//	same URL on the same tenant should get semantically equivalent content.
//
// So Cloudflare decides ELIGIBILITY (its cache rules match on host and path) and
// this package decides SAFETY. It never trusts the edge to correct an origin
// mistake, and an edge rule is never the only thing keeping private bytes out of
// the cache: a private response says so itself, in the headers, on every request.
//
// Three buckets, and the default is the closed one:
//
//	A  public, cacheable      public, max-age=60, stale-while-revalidate=…, stale-if-error=…
//	B  private, never cached   private, no-store
//	C  fingerprinted asset     public, max-age=31536000, immutable
//
// A handler opts into A or C by naming a reason; anything that does not — a new
// route, a route whose visibility logic threw, a response type nobody has
// reviewed — is B. That asymmetry is the whole design. Forgetting to classify
// costs cache coverage; forgetting to classify in the other direction would cost
// a tenant's privacy.
//
// Three downgrades run after the handler has had its say, because a handler can
// be right about the route and wrong about the response:
//
//   - A response carrying Set-Cookie is user-specific by construction.
//   - A request carrying a session cookie or an API credential gets a private
//     answer, whatever the route usually serves.
//   - Only 200 and 304 may be cacheable; a redirect or an error is B.
//
// Bucket C survives the credential downgrade alone, and deliberately: a
// fingerprinted URL cannot vary by reader, and making signed-in members re-fetch
// the typefaces on every page would be a real regression bought with no privacy.
//
// The edge configuration this pairs with is generated from pkg/webpaths, which
// is where the public surface is written down once. A change to authentication,
// visibility or routing has to re-confirm both halves: that this package still
// classifies the response correctly, and that the path is on the right list
// there.
package cachepolicy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

// Bucket names a response's cache policy. The letters are the ones the guidance
// and the docs use, so a log line and a review checklist read the same way.
type Bucket string

const (
	// Public is anonymous content that is public for this tenant.
	Public Bucket = "A"
	// Private is everything else, and everything unresolved.
	Private Bucket = "B"
	// Immutable is a content-fingerprinted asset whose URL changes with its bytes.
	Immutable Bucket = "C"
)

const (
	// DefaultMaxAge is the browser TTL a public response carries. It is short on
	// purpose: a purge cannot reach a copy already in someone's browser, so
	// mutable public HTML and JSON keep a small max-age and Cloudflare's Edge
	// Cache TTL does the long retention.
	DefaultMaxAge = 60

	// StaleWindow is how long a stale copy may be served while it is refreshed
	// (stale-while-revalidate) or while the origin is down (stale-if-error). A
	// day of either beats an error page, and neither directive can leak: they
	// only ever re-serve a response that was already public.
	StaleWindow = 86400

	// ImmutableMaxAge is a year, the longest value worth sending.
	ImmutableMaxAge = 31536000

	// PrivateControl is the private policy. no-store, not no-cache: no-cache
	// permits storage and asks for revalidation, which is not what "never keep
	// this" means. Losing the back/forward cache is the price, and for an
	// authenticated page it is the right price.
	PrivateControl = "private, no-store"

	// ImmutableControl is the fingerprinted-asset policy.
	ImmutableControl = "public, max-age=31536000, immutable"
)

// PublicControl is the header a bucket-A response carries.
//
// It names no s-maxage. Under shared-cache semantics s-maxage carries
// proxy-revalidate with it, which obliges a shared cache to revalidate an
// expired entry before serving it — the exact behaviour stale-while-revalidate
// exists to avoid. The browser TTL lives here; the edge TTL lives in the
// Cloudflare cache rule, where it can be raised without a deploy.
func PublicControl(maxAge int) string {
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	return fmt.Sprintf("public, max-age=%d, stale-while-revalidate=%d, stale-if-error=%d",
		maxAge, StaleWindow, StaleWindow)
}

// --- declaring a policy ------------------------------------------------------

// decision is the classification a handler declared, carried on the request so
// the middleware can read it back when the headers are committed.
type decision struct {
	bucket   Bucket
	maxAge   int
	reason   string
	declared bool
	// refusal permits bucket A on a non-200 (MarkPublicRefusal).
	refusal bool
}

type ctxKey struct{}

func from(r *http.Request) *decision {
	d, _ := r.Context().Value(ctxKey{}).(*decision)
	return d
}

// declare records the classification, and also writes the header immediately so
// a handler mounted on a mux WITHOUT this middleware still says something true.
// The middleware overwrites the value from the recorded decision, which is what
// makes a route-level header unable to widen a policy the middleware narrows.
func declare(w http.ResponseWriter, r *http.Request, b Bucket, maxAge int, reason string) {
	switch b {
	case Public:
		w.Header().Set("Cache-Control", PublicControl(maxAge))
	case Immutable:
		w.Header().Set("Cache-Control", ImmutableControl)
	default:
		w.Header().Set("Cache-Control", PrivateControl)
	}
	if d := from(r); d != nil {
		d.bucket, d.maxAge, d.reason, d.declared, d.refusal = b, maxAge, reason, true, false
	}
}

// MarkPublic declares bucket A with the default browser TTL. The reason is not
// decoration: it is what the access log and the metrics report, and what a
// reviewer reads when asking whether the route is still public.
func MarkPublic(w http.ResponseWriter, r *http.Request, reason string) {
	declare(w, r, Public, DefaultMaxAge, reason)
}

// MarkPublicFor declares bucket A with a longer browser TTL, for public content
// that genuinely does not change between releases — an icon, a manifest.
func MarkPublicFor(w http.ResponseWriter, r *http.Request, maxAge int, reason string) {
	declare(w, r, Public, maxAge, reason)
}

// MarkImmutable declares bucket C. Only for a URL that changes when its bytes
// change; a bare /assets/app.css is bucket A, not this.
func MarkImmutable(w http.ResponseWriter, r *http.Request, reason string) {
	declare(w, r, Immutable, 0, reason)
}

// MarkPublicRefusal declares bucket A on a response that is not a 200 — the one
// exception to "only 200 and 304 may be cacheable", and it exists for exactly one
// shape of answer: a refusal generated by the service itself, with a fixed body,
// no identity in it and nothing an application could have put there.
//
// The ingress's "no registry is published at this address" is the case. On a
// wildcard zone scanners walk hostnames all day, and every one of those walks is
// an origin request for a sentence that never changes. Cloudflare's status-code
// TTL could do this at the edge instead, but not safely for the application's own
// 404s — a 404 page there carries the signed-in shell, and the only thing keeping
// it out of a shared cache would be the authenticated-bypass rule. Leaning on one
// edge rule for that is what the guidance forbids, so the negative caching lives
// here, on the responses that can prove they hold nothing.
func MarkPublicRefusal(w http.ResponseWriter, r *http.Request, maxAge int, reason string) {
	declare(w, r, Public, maxAge, reason)
	if d := from(r); d != nil {
		d.refusal = true
	}
}

// MarkPrivate declares bucket B. It is already the default, so calling it is
// about the record rather than the header: an authenticated gate that says so
// shows up in the log as "authorization" instead of "undeclared", and the two
// mean different things to whoever is reading.
func MarkPrivate(w http.ResponseWriter, r *http.Request, reason string) {
	declare(w, r, Private, 0, reason)
}

// --- observability -----------------------------------------------------------

// Record is what one classified response looked like. It carries no cookie
// value, no credential and no body — enough to answer "why was that not
// cached?" without logging anything worth stealing.
type Record struct {
	Host          string
	Path          string
	Method        string
	Status        int
	Bucket        Bucket
	Reason        string
	CacheControl  string
	Credentialed  bool // the request carried a session cookie or an API key
	SessionCookie bool // specifically the session cookie
	SetCookie     bool // the response wrote one
	ETag          bool
	Streamed      bool // too large or flushed: no validator generated
	Fallback      bool // no handler declared anything; B by default
	Downgraded    bool // a declared A or C was narrowed to B
}

// Counters are process-lifetime totals, exposed on /v1/health. They are the
// numbers that tell an operator whether the caching is working at all without
// opening Cloudflare's analytics: a bucket-A count stuck at zero means the
// origin is not offering the edge anything to cache.
type Counters struct {
	public       atomic.Int64
	private      atomic.Int64
	immutable    atomic.Int64
	fallback     atomic.Int64
	downgraded   atomic.Int64
	cookieOnPub  atomic.Int64
	etags        atomic.Int64
	streamed     atomic.Int64
	notModified  atomic.Int64
	credentialed atomic.Int64
}

func (c *Counters) note(rec Record) {
	if c == nil {
		return
	}
	switch rec.Bucket {
	case Public:
		c.public.Add(1)
	case Immutable:
		c.immutable.Add(1)
	default:
		c.private.Add(1)
	}
	if rec.Fallback {
		c.fallback.Add(1)
	}
	if rec.Downgraded {
		c.downgraded.Add(1)
	}
	if rec.SetCookie && rec.Downgraded {
		c.cookieOnPub.Add(1)
	}
	if rec.ETag {
		c.etags.Add(1)
	}
	if rec.Streamed {
		c.streamed.Add(1)
	}
	if rec.Status == http.StatusNotModified {
		c.notModified.Add(1)
	}
	if rec.Credentialed {
		c.credentialed.Add(1)
	}
}

// Snapshot is the counter set, for /v1/health.
func (c *Counters) Snapshot() map[string]int64 {
	if c == nil {
		return map[string]int64{}
	}
	return map[string]int64{
		"public":           c.public.Load(),
		"private":          c.private.Load(),
		"immutable":        c.immutable.Load(),
		"undeclared":       c.fallback.Load(),
		"downgraded":       c.downgraded.Load(),
		"cookie_on_public": c.cookieOnPub.Load(),
		"etags":            c.etags.Load(),
		"streamed":         c.streamed.Load(),
		"not_modified":     c.notModified.Load(),
		"credentialed":     c.credentialed.Load(),
	}
}

// --- the middleware ----------------------------------------------------------

// Options configures Middleware.
type Options struct {
	// SessionCookie is the cookie whose presence means the reader is signed in.
	// One name across the fleet is what lets a single Cloudflare rule bypass
	// every tenant's authenticated traffic.
	SessionCookie string

	// PrivateCookies are further cookies whose presence makes a response
	// unshareable — a cookie that ROUTES the request rather than authenticating
	// it. Demonstration mode is the case: tacit_demo picks which dataset answers,
	// so the same URL renders different content depending on it, and a cached copy
	// would be the wrong dataset for the next reader.
	//
	// The origin header alone cannot fix that one. It stops the demo response
	// being stored, but not an already-cached production response being served to
	// a demo reader — so these names must also appear in the zone's bypass rule,
	// which is why they come from the same list (zone.go).
	PrivateCookies []string

	// CredentialHeaders carry an API key or a bearer token. A request holding
	// one gets a private answer even on a route that is usually public.
	CredentialHeaders []string

	// Observe receives one record per response, if set, alongside the request it
	// describes — so a caller whose access log wraps this middleware can fold the
	// classification into its own line rather than emitting a second one.
	Observe func(*http.Request, Record)

	// Counters accumulates totals, if set.
	Counters *Counters

	// MaxETagBytes caps the body held in memory to hash. Past it the response
	// streams without a validator rather than growing the heap for one.
	// Zero means DefaultMaxETagBytes.
	MaxETagBytes int
}

// DefaultMaxETagBytes is generous for a rendered page and small enough that a
// burst of them cannot matter.
const DefaultMaxETagBytes = 4 << 20

// DefaultCredentialHeaders are the request headers this codebase authenticates
// with.
var DefaultCredentialHeaders = []string{"Authorization", "X-Tacit-Key"}

// Middleware installs the classification on every response. It must wrap the
// whole handler and nothing may write past it, because the policy is committed
// with the header and a header cannot be corrected once the body has started.
func Middleware(opts Options) func(http.Handler) http.Handler {
	if opts.MaxETagBytes <= 0 {
		opts.MaxETagBytes = DefaultMaxETagBytes
	}
	if opts.CredentialHeaders == nil {
		opts.CredentialHeaders = DefaultCredentialHeaders
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d := &decision{bucket: Private, reason: "undeclared"}
			cw := &writer{
				ResponseWriter: w,
				opts:           &opts,
				d:              d,
				req:            r,
				status:         http.StatusOK,
			}
			cw.session = opts.SessionCookie != "" && hasCookie(r, opts.SessionCookie)
			cw.credentialed = cw.session
			for _, h := range opts.CredentialHeaders {
				if r.Header.Get(h) != "" {
					cw.credentialed = true
				}
			}
			for _, name := range opts.PrivateCookies {
				if hasCookie(r, name) {
					cw.credentialed = true
				}
			}
			next.ServeHTTP(cw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, d)))
			cw.finish()
		})
	}
}

func hasCookie(r *http.Request, name string) bool {
	c, err := r.Cookie(name)
	return err == nil && c.Value != ""
}

// writer commits the policy, and holds a public body long enough to hash it.
type writer struct {
	http.ResponseWriter
	opts *Options
	d    *decision
	req  *http.Request

	status       int
	session      bool
	credentialed bool

	// buffering is on between the header decision and the end of the handler,
	// for a response we intend to give a validator.
	buffering bool
	buf       bytes.Buffer
	committed bool
	streamed  bool
	rec       Record
	reported  bool
}

func (w *writer) WriteHeader(code int) {
	if w.committed || w.buffering {
		return
	}
	w.status = code
	w.decide()
	if w.eligibleForETag() {
		w.buffering = true // the header waits until the body has been hashed
		return
	}
	w.commit()
}

func (w *writer) Write(b []byte) (int, error) {
	if !w.committed && !w.buffering {
		w.WriteHeader(http.StatusOK)
	}
	if w.buffering {
		if w.buf.Len()+len(b) > w.opts.MaxETagBytes {
			// Bigger than we will hold: give up the validator, send what we have
			// and stream the rest. The policy is already decided, so nothing
			// about correctness turns on this.
			w.streamed = true
			w.buffering = false
			w.commit()
			if w.buf.Len() > 0 {
				if _, err := w.ResponseWriter.Write(w.buf.Bytes()); err != nil {
					return 0, err
				}
				w.buf.Reset()
			}
			return w.ResponseWriter.Write(b)
		}
		return w.buf.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush ends any buffering: a handler that flushes is streaming, and a streaming
// response gets no rendered-body validator. The classification is untouched —
// it was committed before the first byte, which is the point of deciding it in
// WriteHeader rather than at the end.
func (w *writer) Flush() {
	if w.buffering {
		w.streamed = true
		w.buffering = false
		w.commit()
		if w.buf.Len() > 0 {
			_, _ = w.ResponseWriter.Write(w.buf.Bytes())
			w.buf.Reset()
		}
	}
	// Through the controller, not a type assertion: this classifier is mounted
	// inside the access log, whose writer carries neither Flush nor Hijack. An
	// assertion sees only that writer and stops, so the drained buffer would sit
	// in net/http's own bufio with nothing to push it out.
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Hijack hands the connection over untouched (the tunnel upgrade uses it).
func (w *writer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, fmt.Errorf("cachepolicy: the underlying writer cannot hijack: %w", err)
	}
	w.committed = true // nothing more of ours is going out on this connection
	return conn, brw, nil
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// decide resolves the final bucket and stamps the headers. Everything a handler
// declared is input; the downgrades below are not negotiable.
func (w *writer) decide() {
	b, maxAge, reason := w.d.bucket, w.d.maxAge, w.d.reason
	fallback := !w.d.declared
	if fallback {
		b, reason = Private, "undeclared"
	}
	setCookie := len(w.Header().Values("Set-Cookie")) > 0
	downgraded := false

	// A response that writes a cookie is about one reader. Whatever the route
	// thought it was serving, this one is not shared.
	if setCookie && b != Private {
		b, reason, downgraded = Private, w.warnSetCookie(b), true
	}

	// A credential in the request means the answer may be about its holder. This
	// does not touch bucket C: a fingerprinted URL cannot vary by reader, and
	// making signed-in members re-fetch the fonts on every page would buy no
	// privacy at all.
	if b == Public && w.credentialed {
		b, reason, downgraded = Private, "credentialed request", true
	}

	// Only a 200 or a 304 may be cacheable here. Negative and redirect caching
	// are real and useful, but they belong in a reviewed Cloudflare status-code
	// TTL, not in an origin header a handler can reach by accident.
	if b != Private && w.status != http.StatusOK && w.status != http.StatusNotModified &&
		!(w.d.refusal && w.status == http.StatusNotFound) {
		b, reason, downgraded = Private, fmt.Sprintf("status %d", w.status), true
	}

	h := w.Header()
	// Nothing downstream should be reading a second, older opinion.
	h.Del("Expires")
	h.Del("Pragma")
	h.Del("CDN-Cache-Control")
	h.Del("Cloudflare-CDN-Cache-Control")
	switch b {
	case Public:
		h.Set("Cache-Control", PublicControl(maxAge))
	case Immutable:
		h.Set("Cache-Control", ImmutableControl)
	default:
		h.Set("Cache-Control", PrivateControl)
	}
	scrubVary(h)

	w.rec = Record{
		Host: w.req.Host, Path: w.req.URL.Path, Method: w.req.Method,
		Status: w.status, Bucket: b, Reason: reason,
		CacheControl: h.Get("Cache-Control"),
		Credentialed: w.credentialed, SessionCookie: w.session,
		SetCookie: setCookie, Fallback: fallback, Downgraded: downgraded,
	}
}

// warnSetCookie reports the cookie downgrade and names its reason. Both the
// decision made with the header and the late check in commit go through here, so
// an operator reading the log cannot tell them apart and neither wording can
// drift away from the other.
func (w *writer) warnSetCookie(b Bucket) string {
	log.Printf("[cache] WARNING %s %s declared bucket %s and set a cookie; serving %s",
		w.req.Method, w.req.URL.Path, b, PrivateControl)
	return "set-cookie on bucket " + string(b)
}

// downgradeForLateCookie catches a cookie set after the policy was decided.
//
// For a response we hold to hash, the real header write waits until the handler
// has returned. Plain net/http would drop a Set-Cookie added in that window; here
// it goes out on the wire, and it would go out on a response already classed as
// shared. So the last thing before the status line is one more look for a cookie.
// Bucket C downgrades too, exactly as it does in decide.
//
// It reports whether it had to act, which is also the signal to skip the 304
// comparison: a no-store response has no validator to match.
func (w *writer) downgradeForLateCookie() bool {
	if w.rec.Bucket == Private || len(w.Header().Values("Set-Cookie")) == 0 {
		return false
	}
	reason := w.warnSetCookie(w.rec.Bucket)
	h := w.Header()
	h.Set("Cache-Control", PrivateControl)
	h.Del("ETag")
	w.rec.Bucket = Private
	w.rec.Reason = reason
	w.rec.CacheControl = PrivateControl
	w.rec.SetCookie = true
	w.rec.Downgraded = true
	w.rec.ETag = false
	return true
}

// scrubVary drops the Vary values Cloudflare does not key on. Leaving them would
// invite someone to believe they separate cache entries; they do not, and a
// response that needs separating needs a distinct URL or a private policy.
func scrubVary(h http.Header) {
	raw := h.Values("Vary")
	if len(raw) == 0 {
		return
	}
	var keep []string
	for _, line := range raw {
		for _, f := range strings.Split(line, ",") {
			f = strings.TrimSpace(f)
			switch {
			case f == "":
			case strings.EqualFold(f, "cookie"), strings.EqualFold(f, "authorization"),
				strings.EqualFold(f, "user-agent"), f == "*", strings.HasPrefix(strings.ToLower(f), "x-"):
			default:
				keep = append(keep, f)
			}
		}
	}
	h.Del("Vary")
	if len(keep) > 0 {
		h.Set("Vary", strings.Join(keep, ", "))
	}
}

// eligibleForETag reports whether it is worth holding this body to hash it.
func (w *writer) eligibleForETag() bool {
	if w.status != http.StatusOK {
		return false
	}
	if w.rec.Bucket == Private {
		return false // a no-store response has nothing to revalidate
	}
	if w.Header().Get("ETag") != "" {
		return false // the handler has its own validator (an asset hash)
	}
	switch w.req.Method {
	case http.MethodGet, http.MethodHead:
		return true
	}
	return false
}

// commit writes the status line. After this the policy is on the wire.
func (w *writer) commit() {
	if w.committed {
		return
	}
	w.downgradeForLateCookie()
	w.committed = true
	w.rec.Streamed = w.streamed
	w.rec.ETag = w.Header().Get("ETag") != ""
	w.ResponseWriter.WriteHeader(w.status)
}

// finish closes out a buffered response: hash the rendered bytes, answer a
// matching If-None-Match with 304, otherwise send the body.
//
// The hash is of the FINAL rendered output, never of the inputs someone believes
// it was derived from. A validator built from a template version or a row's
// updated_at is a promise about a dependency graph nobody has enumerated, and
// the failure is silent: a stale page with a matching ETag.
func (w *writer) finish() {
	defer w.report()
	if !w.buffering {
		if !w.committed {
			// A handler that wrote nothing at all: net/http would send a bare
			// 200, so send it with a policy on it.
			w.decide()
			w.commit()
		}
		return
	}
	w.buffering = false
	sum := sha256.Sum256(w.buf.Bytes())
	etag := `"` + hex.EncodeToString(sum[:])[:16] + `"`
	w.Header().Set("ETag", etag)
	if !w.downgradeForLateCookie() && etagMatches(w.req.Header.Get("If-None-Match"), etag) {
		w.status = http.StatusNotModified
		w.rec.Status = http.StatusNotModified
		w.Header().Del("Content-Type")
		w.Header().Del("Content-Length")
		w.commit()
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(w.buf.Len()))
	w.commit()
	_, _ = w.ResponseWriter.Write(w.buf.Bytes())
}

func (w *writer) report() {
	if w.reported {
		return
	}
	w.reported = true
	w.rec.ETag = w.Header().Get("ETag") != ""
	w.opts.Counters.note(w.rec)
	if w.opts.Observe != nil {
		w.opts.Observe(w.req, w.rec)
	}
}

// etagMatches implements If-None-Match against one validator. A client may send
// a list, a weak form, or "*"; Cloudflare in particular weakens strong tags when
// compression changes the representation it transferred, so the W/ prefix is
// compared away rather than treated as a mismatch.
func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == want {
			return true
		}
	}
	return false
}
