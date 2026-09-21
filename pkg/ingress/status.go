// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/ui"
)

// The status page: what a proxy answers on its own hostname when no console is
// mounted (Server.Console, operator.go).
//
// It says that the process is up, how many registries are published and how many
// are connected, and it names none of them. That silence is the point. A console
// sits behind an identity provider and shows an operator the route table; this
// page has no sign-in to sit behind, so a hostname printed here would be an
// enumeration of somebody else's registries for anybody who asked. The route
// table has an address already — `tacit-ingress instances list`, on the host,
// where whoever runs the proxy is the only one who can read it.

// statusHandler answers the console hostnames of a proxy with no console.
func (s *Server) statusHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /assets/app.css", ui.ServeCSS)
	mux.HandleFunc("GET /assets/fonts/{name}", ui.ServeFont)
	mux.HandleFunc("GET /favicon.ico", ui.ServeFavicon)
	mux.HandleFunc("GET /health", s.HandleHealth)
	mux.HandleFunc("GET /{$}", s.handleStatus)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		MarkPrivate(w, r, "not a page this proxy serves")
		http.Error(w, "not found", http.StatusNotFound)
	})
	return s.Classified(mux)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	MarkPrivate(w, r, "the proxy's own status")
	published := len(s.Store.List())
	online := len(s.LiveNames())
	shell := ui.Shell{
		Brand: ProductName() + " Ingress",
		Meta:  fmt.Sprintf("%d published · %d online", published, online),
	}
	body := ui.Tiles(
		ui.Tile("Published", ui.FmtCount(published), "registries holding a name here"),
		ui.Tile("Connected", ui.FmtCount(online), "tunnels open right now"),
		ui.Tile("Uptime", time.Since(s.started).Round(time.Second).String(), "since this process started"),
	) + ui.Panel("The Route Table", "",
		`<p>Which registry holds which name is not published here, on purpose: this `+
			`page has no sign-in in front of it. Read it on the host, where `+
			`<code>tacit-ingress instances list</code> prints the whole table, and `+
			`<code>tacit-ingress instances rm &lt;name&gt;</code> releases a name.</p>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(shell.Render(ui.Page{Title: "Status", Content: body})))
}

// HandleHealth is the liveness endpoint. A console mounts it on its own mux —
// the address whatever watches this process is already pointed at.
func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPrivate(w, r, "liveness")
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"instances":%d,"online":%d,"uptime_secs":%d,"cache":%s}`+"\n",
		len(s.Store.List()), len(s.LiveNames()), int(time.Since(s.started).Seconds()),
		cacheStatsJSON(s.cacheStats))
}

// cacheStatsJSON renders the classification tally for /health. These responses
// are nearly all bucket B, so what it actually reports is whether the classifier
// is running at all.
func cacheStatsJSON(c *cachepolicy.Counters) string {
	raw, err := json.Marshal(c.Snapshot())
	if err != nil {
		return "{}"
	}
	return string(raw)
}
