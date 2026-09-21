// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The iOS/iPadOS "Add to Home Screen" icon. Unlike the favicon — which stays
// an inline theme-aware SVG data-URI in the shell (shell.go) — Safari's
// apple-touch-icon must be a RASTER image fetched from a real URL: it ignores
// SVG here, does not reliably accept data-URIs, and flattens transparency onto
// black. So this is a pre-rendered opaque 180×180 PNG of the brand mark (white
// on accent blue, full-bleed so iOS's rounded-rect mask frames it), embedded
// in the binary and served at a fixed path the shell links to.
package web

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/product"
)

//go:embed apple-touch-icon.png
var appleTouchIcon []byte

// handleAppleTouchIcon serves the home-screen icon. Public (no auth): it's
// linked from the sign-in page too, and it's just the logo.
// The URL is not fingerprinted — Safari asks for this exact path — so it is
// bucket A with a day's browser TTL rather than bucket C. An icon that changed
// with a release would take a day to reach a home screen, which is the right
// trade for a logo.
func (s *Server) handleAppleTouchIcon(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPublicFor(w, r, 86400, "home-screen icon")
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(appleTouchIcon)
}

// handleManifest serves the Web App Manifest (shell.go). Public: it must load
// before any sign-in so iOS resolves the app's scope. Its display:standalone +
// scope keep home-screen navigation inside the app instead of opening Safari.
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPublicFor(w, r, 86400, "web app manifest")
	w.Header().Set("Content-Type", "application/manifest+json")
	// The name is the app's label on the home screen. Substituted quotes and
	// all, from json.Marshal, so a product name carrying a quote or a backslash
	// cannot break the manifest.
	name, err := json.Marshal(product.Name())
	if err != nil {
		name = []byte(`"` + product.Default + `"`)
	}
	m := strings.ReplaceAll(webManifest, `"«PRODUCT»"`, string(name))
	if base := s.cfg().BasePath; base != "" {
		// The scope decides which URLs count as inside the home-screen app,
		// so it must carry the mount prefix, as must start_url and the icon.
		m = strings.NewReplacer(
			`"start_url": "/"`, `"start_url": "`+base+`/"`,
			`"scope": "/"`, `"scope": "`+base+`/"`,
			`"src": "/apple-touch-icon.png"`, `"src": "`+base+`/apple-touch-icon.png"`,
		).Replace(m)
	}
	_, _ = w.Write([]byte(m))
}
