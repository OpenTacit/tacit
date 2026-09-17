// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Demonstration mode (TACIT_DEMO_DIR): the dashboard grows a "Demo" menu that
// switches the reader between the production data and any of the demo
// scenarios found in the configured directory. Every scenario is a complete,
// isolated registry instance — its own file store, its own Server sharing this
// process's config and embedder — and the selection is a browser cookie read
// by DemoRouter, so agents and API clients (which carry no cookies) always see
// production. cmd/tacit builds the instances and loads their datasets; this
// file owns the shared catalog, the menu, the switch action, and the router.
package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// DemoCookie carries the reader's dataset selection: a scenario key, or absent
// for production. Deliberately not the session cookie — switching datasets
// must never touch who you are signed in as.
const DemoCookie = "tacit_demo"

// DemoScenario is one switchable dataset as the menu shows it. Err marks a
// scenario whose dataset failed to parse or load; it stays visible (the
// operator should see the failure where they expected the scenario) but is not
// selectable. Ready flips once the background load completes.
type DemoScenario struct {
	Key   string
	Title string
	Ready bool
	Err   string
}

// DemoCatalog is the scenario list shared by every Server instance in the
// process — production and each demo instance render the same menu, differing
// only in which entry is active. The background loaders write readiness into
// it while requests render from it, hence the lock.
type DemoCatalog struct {
	mu        sync.Mutex
	scenarios []DemoScenario // menu order
}

// Add appends a scenario in menu order (not yet ready).
func (c *DemoCatalog) Add(key, title string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scenarios = append(c.scenarios, DemoScenario{Key: key, Title: title})
}

// SetReady marks a scenario loaded and selectable.
func (c *DemoCatalog) SetReady(key string) { c.set(key, true, "") }

// SetErr marks a scenario failed; it renders as unavailable.
func (c *DemoCatalog) SetErr(key, msg string) { c.set(key, false, msg) }

func (c *DemoCatalog) set(key string, ready bool, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.scenarios {
		if c.scenarios[i].Key == key {
			c.scenarios[i].Ready = ready
			c.scenarios[i].Err = msg
		}
	}
}

// Ready reports whether the named scenario is loaded and selectable.
func (c *DemoCatalog) Ready(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, sc := range c.scenarios {
		if sc.Key == key {
			return sc.Ready
		}
	}
	return false
}

// Title returns the named scenario's menu title, or "" when unknown.
func (c *DemoCatalog) Title(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, sc := range c.scenarios {
		if sc.Key == key {
			return sc.Title
		}
	}
	return ""
}

// Snapshot returns the scenarios in menu order.
func (c *DemoCatalog) Snapshot() []DemoScenario {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]DemoScenario, len(c.scenarios))
	copy(out, c.scenarios)
	return out
}

// DemoState hangs off a Server when demonstration mode is on. Catalog is
// shared across all instances; Active names the scenario THIS instance serves
// ("" on the production instance) — the cookie decides which instance a
// request reaches, so each instance renders its own identity as current.
type DemoState struct {
	Catalog *DemoCatalog
	Active  string
}

// DemoRouter picks the registry instance per request from the DemoCookie:
// a known scenario key routes to that scenario's handler, anything else —
// no cookie, a stale key, an API client — falls through to production.
func DemoRouter(prod http.Handler, demos map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(DemoCookie); err == nil {
			if h, ok := demos[c.Value]; ok {
				h.ServeHTTP(w, r)
				return
			}
		}
		prod.ServeHTTP(w, r)
	})
}

// demoHTML renders the top-bar Demo control: a labeled button (accented while
// a scenario is active, so demo data is never mistaken for the real thing) and
// its dropdown of datasets. Each option is a submit button in one form —
// switching is a POST because it sets a cookie. Empty when demo mode is off.
func (s *Server) demoHTML() string {
	if s.Demo == nil {
		return ""
	}
	var b strings.Builder
	btnClass := "demo-btn"
	title := "Demo data: production"
	// The button names what you are looking at: the active scenario while one
	// is selected, "Production" on production data.
	label := "Production"
	if s.Demo.Active != "" {
		btnClass += " demo-on"
		title = "Demo data active"
		if t := s.Demo.Catalog.Title(s.Demo.Active); t != "" {
			label = t
		}
	}
	fmt.Fprintf(&b, `<span class="demo"><button id="demo-btn" class="%s" type="button" aria-haspopup="menu" `+
		`aria-expanded="false" aria-controls="demo-menu" title="%s">%s</button>`, btnClass, title, html.EscapeString(label))
	b.WriteString(`<div id="demo-menu" class="account-menu demo-menu" role="menu" hidden>` +
		`<form method="post" action="/demo/switch">`)
	writeOpt := func(key, label string, ready bool) {
		current := key == s.Demo.Active
		cls := "account-item demo-opt"
		if current {
			cls += " demo-current"
		}
		fmt.Fprintf(&b, `<button class="%s" role="menuitemradio" aria-checked="%t" name="to" value="%s"`,
			cls, current, html.EscapeString(key))
		if !ready && !current {
			b.WriteString(` disabled`)
		}
		fmt.Fprintf(&b, `><span class="demo-check" aria-hidden="true">%s</span>%s</button>`,
			map[bool]string{true: "✓", false: ""}[current], html.EscapeString(label))
	}
	writeOpt("", "Production data", true)
	for _, sc := range s.Demo.Catalog.Snapshot() {
		label := sc.Title
		switch {
		case sc.Err != "":
			label += " (unavailable)"
		case !sc.Ready:
			label += " (loading…)"
		}
		writeOpt(sc.Key, label, sc.Ready)
	}
	b.WriteString(`</form></div></span>`)
	return b.String()
}

// handleDemoSwitch sets the dataset cookie and returns the reader to the view
// they were on, so switching datasets compares like with like instead of
// resetting to the dashboard home. A page that has no counterpart in the
// target dataset (a technique detail, say) renders its honest not-found; the shell
// and the menu still stand. Session-gated like every HTML view; the cookie
// only selects which dataset this browser sees, it grants nothing.
func (s *Server) handleDemoSwitch(w http.ResponseWriter, r *http.Request) {
	if s.Demo == nil {
		http.NotFound(w, r)
		return
	}
	if !s.signedInOrForbidden(w, r, "sign in to switch datasets") {
		return
	}
	to := r.FormValue("to")
	if to != "" && !s.Demo.Catalog.Ready(to) {
		http.Redirect(w, r, s.demoReturnPath(r), http.StatusSeeOther)
		return
	}
	cookie := &http.Cookie{
		Name: DemoCookie, Value: to, Path: cookiePath(s.cfg().BasePath),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.cfg().CookieSecure,
	}
	if to == "" {
		cookie.MaxAge = -1 // back to production: clear rather than store ""
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, s.demoReturnPath(r), http.StatusSeeOther)
}

// demoReturnPath is where the switch lands: the referring page's own path and
// query (the menu is a same-origin form POST, so the Referer carries it),
// falling back to the dashboard home. Only a root-relative path is accepted —
// never a foreign or protocol-relative target — and the base path is stripped
// so the redirect re-prefixes it exactly once.
func (s *Server) demoReturnPath(r *http.Request) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Path == "" {
		return "/"
	}
	p := ref.Path
	if base := s.cfg().BasePath; base != "" {
		p = strings.TrimPrefix(p, base)
		if p == "" {
			p = "/"
		}
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return "/"
	}
	if ref.RawQuery != "" {
		p += "?" + ref.RawQuery
	}
	return p
}

// cookiePath scopes the demo cookie to the mount: the whole site on a root
// mount, the sub-path when TACIT_BASE_PATH is set.
func cookiePath(base string) string {
	if base == "" {
		return "/"
	}
	return base
}
