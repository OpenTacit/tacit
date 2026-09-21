// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"net/http"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/pkg/webpaths"
)

// What an operator console needs from a running ingress.
//
// The console that runs the project's own instance is not in this repository.
// It manages instances nobody else has, on a host nobody else operates, and a
// reader who clones this and runs a proxy needs none of it — so it lives beside
// the deployment module instead, and reaches a Server through what is here.
//
// The seam is one field. Server.Console takes any handler; PublicHandler sends
// the console hostnames to it and everything else down a tunnel. Left nil, the
// built-in status page answers (status.go), which is enough to see that a proxy
// is working and deliberately not enough to run a service from.
//
// Nothing below is a convenience. Each is state the console has to read and had
// no other way to reach, or a helper that must not be reimplemented outside:
// host normalization decides routing, and a second copy of it that drifts is a
// request served by the wrong instance.

// Claims is one signed-in person, and Provider is the identity provider that
// vouched for them. Both are named here because a console outside this module
// writes handlers that take them.
type (
	Claims   = oidc.Claims
	Provider = oidc.Provider
)

// Started is when this process came up. The console reports uptime from it.
func (s *Server) Started() time.Time { return s.started }

// Classified wraps a handler so its responses carry cache headers decided by
// the same model the registry uses (pkg/webpaths, internal/cachepolicy), and
// tallies what it decided for the console's own health line.
func (s *Server) Classified(next http.Handler) http.Handler {
	return cachepolicy.Middleware(cachepolicy.Options{
		SessionCookie: webpaths.IngressSessionCookieName,
		Counters:      s.cacheStats,
	})(next)
}

// CacheStatsJSON is how this process classified its own responses, ready to
// embed in a health document. Proxied responses are not counted: the registry
// behind the tunnel classified those, and it reports its own tally.
func (s *Server) CacheStatsJSON() string { return cacheStatsJSON(s.cacheStats) }

// MarkPrivate, MarkPublicFor and MarkPublicRefusal put one response's cache
// headers on, and are the only way a console page should set them. The reason
// string is for the log, not the reader.
func MarkPrivate(w http.ResponseWriter, r *http.Request, reason string) {
	cachepolicy.MarkPrivate(w, r, reason)
}

func MarkPublicFor(w http.ResponseWriter, r *http.Request, maxAge int, reason string) {
	cachepolicy.MarkPublicFor(w, r, maxAge, reason)
}

func MarkPublicRefusal(w http.ResponseWriter, r *http.Request, maxAge int, reason string) {
	cachepolicy.MarkPublicRefusal(w, r, maxAge, reason)
}

// ProductName is the name the product is shipped under, read from one place so
// a rename reaches every page at once.
func ProductName() string { return product.Name() }
