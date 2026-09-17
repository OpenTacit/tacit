// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// robots.txt, for a fleet that lives on a wildcard domain.
//
// Almost nothing a registry serves is for a crawler. The dashboard is a member's
// working surface behind a sign-in, the API wants a key, and the one genuinely
// public tree — the user guide — is the same guide on every tenant, so twenty
// tenants indexed would be nineteen duplicates of it. What is left worth
// crawling is the front door, which is how anyone finds the guide anyway.
//
// So the file is a short allowlist, and it exists mostly to stop the crawl
// rather than to shape it: every disallowed path a crawler skips is a request
// that never reaches the origin, and every one it makes anyway is answered from
// the edge because this response is bucket A.
package web

import (
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/product"
)

// robotsTXT is deliberately not derived from the route table. A generated file
// would grow a new Allow line every time someone added a public-looking route,
// which is the wrong default: a path becomes crawlable when a person decides it
// should be, not when it happens to answer without a session.
const robotsTXT = `# «PRODUCT» registry. The dashboard and API are for members and their tools;
# the user guide is public and identical across tenants, so one copy is enough.
User-agent: *
Allow: /$
Allow: /docs/user-guide
Disallow: /

# The commons feed is machine-readable and open, but it is an API, not a page.
User-agent: *
Disallow: /f/
`

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPublicFor(w, r, 3600, "robots.txt")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	body := strings.ReplaceAll(robotsTXT, "«PRODUCT»", product.Name())
	if base := s.cfg().BasePath; base != "" {
		// Mounted under a prefix, the paths a crawler sees carry it.
		body = strings.NewReplacer(
			"Allow: /$", "Allow: "+base+"/$",
			"Allow: /docs/user-guide", "Allow: "+base+"/docs/user-guide",
			"Disallow: /f/", "Disallow: "+base+"/f/",
			"Disallow: /\n", "Disallow: "+base+"/\n",
		).Replace(body)
	}
	_, _ = w.Write([]byte(body))
}
