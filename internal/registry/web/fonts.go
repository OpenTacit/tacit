// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"

	"github.com/opentacit/tacit/internal/ui"
)

// The typefaces the registry sets itself in live in internal/ui alongside the
// stylesheet that calls for them — the ingress console needs the same faces, and
// a font is not a thing to ship twice. See the note there for why these two
// families and no others.

func (s *Server) handleFont(w http.ResponseWriter, r *http.Request) {
	ui.ServeFont(w, r)
}
