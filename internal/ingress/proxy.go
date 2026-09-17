// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"net/http"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
)

// PublicHandler is the front door: one listener carrying both the console (on
// the admin hostname) and every published registry (on its own hostname).
// Routing is by Host and nothing else, which is what makes a wildcard
// certificate and a single address enough for any number of instances.
//
// Nothing here classifies a proxied response. The registry behind the tunnel has
// already decided whether its own bytes are public, private or immutable, and it
// is the only thing that can: it knows who is signed in and what its tenant has
// made visible. So a tunnelled response passes through with its Cache-Control
// exactly as the origin wrote it, and this file only classifies the answers the
// ingress invents itself.
func (s *Server) PublicHandler() http.Handler {
	console := s.ConsoleHandler()
	site := s.siteHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The tunnel first, on any hostname: a registry dials the ingress by
		// the name its operator was given, which may be the console's or an
		// instance's, and either has to reach the same place.
		if r.URL.Path == TunnelPath {
			s.handleTunnelUpgrade(w, r)
			return
		}
		host := hostOnly(r.Host)
		if s.Cfg.IsAdminHost(host) || host == "" || host == "localhost" || host == "127.0.0.1" {
			console.ServeHTTP(w, r)
			return
		}
		// The product's own domain: the project page and nothing else (site.go).
		// It carries no instances — a tenant hostname and a marketing page have
		// nothing in common but a binary.
		if s.Cfg.IsSiteHost(host) {
			site.ServeHTTP(w, r)
			return
		}
		// The zone's apex names no instance and never can — instanceFromHost wants
		// a label under the zone. With a product domain configured it sends people
		// there rather than serving a second copy of the page; without one it IS
		// the page's address.
		if s.Cfg.IsZoneApex(host) {
			s.handleApexElsewhere(w, r)
			return
		}
		name, ok := s.instanceFromHost(host)
		if !ok {
			// A hostname under a zone that names no instance. Scanners walk
			// these constantly, and the answer is one fixed sentence with nothing
			// of anyone's in it, so let the edge keep it and stop the walk from
			// reaching here at all.
			cachepolicy.MarkPublicRefusal(w, r, unknownHostTTL, "no instance at this hostname")
			http.Error(w, "no registry is published at this address", http.StatusNotFound)
			return
		}
		s.proxyTo(name, w, r)
	})
}

// unknownHostTTL is how long the edge may keep "nothing is published here". Short
// enough that a newly enrolled instance answers within a minute of connecting —
// enrolment is self-service, so this is a real wait somebody sits through — and
// long enough to flatten a hostname sweep.
const unknownHostTTL = 60

// instanceFromHost turns a request's Host into an instance name. One zone, one
// label under it: instance hostnames are the whole of what the zone carries, and
// the product's own addresses live on another domain entirely (Config.SiteHost).
func (s *Server) instanceFromHost(host string) (string, bool) {
	name, ok := strings.CutSuffix(strings.ToLower(host), "."+normalHost(s.Cfg.Zone))
	if !ok || name == "" || strings.Contains(name, ".") {
		return "", false // one label only; no nesting under an instance
	}
	return name, true
}

// proxyTo forwards one request down an instance's tunnel, recording what
// happened whether or not it got there.
func (s *Server) proxyTo(name string, w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	op := Op{
		TS:       start.UTC(),
		Instance: name,
		Host:     hostOnly(r.Host),
		Method:   r.Method,
		Path:     r.URL.Path,
		UA:       r.UserAgent(),
		Client:   s.clientAddr(r),
	}
	finish := func(status int, bytes int64, note string) {
		op.Status, op.Bytes, op.MS, op.Note = status, bytes, time.Since(start).Milliseconds(), note
		s.Ops.Append(op)
		s.Metrics.Record(name, status, bytes, time.Since(start))
	}

	in, known := s.Store.Get(name)
	switch {
	case !known:
		cachepolicy.MarkPublicRefusal(w, r, unknownHostTTL, "unknown instance")
		http.Error(w, "no registry is published at this address", http.StatusNotFound)
		finish(http.StatusNotFound, 0, "unknown instance")
		return
	case in.Disabled:
		// A suspension is a decision about one tenant and it gets lifted by hand;
		// nobody should have to wait out a TTL for that.
		cachepolicy.MarkPrivate(w, r, "instance suspended")
		http.Error(w, "this address has been suspended", http.StatusForbidden)
		finish(http.StatusForbidden, 0, "instance disabled")
		return
	}

	// A path outside the registry's surface. This is also the backstop against
	// cache deception: /dashboard/foo.jpg names no prefix the registry serves, so
	// it 404s here without the tunnel ever seeing it and without any chance of an
	// application page being cached under an image extension. Cloudflare's Cache
	// Deception Armor sits behind this, not in front of it.
	if !s.pathAllowed(r.URL.Path) {
		cachepolicy.MarkPublicRefusal(w, r, unknownHostTTL, "path outside the registry surface")
		http.Error(w, "not found", http.StatusNotFound)
		finish(http.StatusNotFound, 0, "path outside the registry surface")
		return
	}

	t := s.tunnelFor(name)
	if t == nil {
		// Transient by definition: the registry is reconnecting. Caching it would
		// keep serving the outage after it ended.
		cachepolicy.MarkPrivate(w, r, "no tunnel")
		w.Header().Set("Retry-After", "15")
		http.Error(w, "this registry is not connected right now", http.StatusBadGateway)
		finish(http.StatusBadGateway, 0, "no tunnel")
		return
	}
	if !t.allow(s.Cfg.RatePerMinute) {
		cachepolicy.MarkPrivate(w, r, "rate limited")
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests for this address", http.StatusTooManyRequests)
		finish(http.StatusTooManyRequests, 0, "rate limited")
		return
	}

	if s.Cfg.MaxRequestBytes > 0 && r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.MaxRequestBytes)
	}

	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	t.proxy.ServeHTTP(rec, r)
	finish(rec.status, rec.written, "")
}

// pathAllowed keeps the tunnel from being used as a general-purpose proxy. The
// registry does its own authentication; this is about what shapes of request
// are worth forwarding at all.
func (s *Server) pathAllowed(p string) bool {
	if p == "/" {
		return true
	}
	for _, prefix := range s.Cfg.AllowPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// statusRecorder remembers what was actually sent, since the metrics and the
// operations log both want it and ReverseProxy reports neither.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
	wrote   bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	w.wrote = true
	n, err := w.ResponseWriter.Write(b)
	w.written += int64(n)
	return n, err
}

// Unwrap is what lets a wrapped response still be flushed, hijacked or given a
// deadline: http.ResponseController walks it to the writer underneath, and
// httputil.ReverseProxy asks the controller for all three.
//
// Passing Flush through by hand covered only the interface somebody remembered.
// ReverseProxy needs Hijacker for a 101, and without this it answers a protocol
// switch with a plain 200 and "can't switch protocols using non-Hijacker
// ResponseWriter type *ingress.statusRecorder" in the log.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// hostOnly strips any port from a Host header.
func hostOnly(h string) string {
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i:], "]") {
		return h[:i]
	}
	return h
}
