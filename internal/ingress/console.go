// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
	"github.com/opentacit/tacit/pkg/webpaths"
)

// The console: the operator's view of the route table, what it served, and what
// went wrong. It is the registry dashboard's smaller sibling and deliberately
// looks it — same stylesheet, same header, same flat plates — because an
// operator moving between the two should not have to learn a second interface.
//
// Everything here is read-mostly, and more so since registries began enrolling
// themselves: an operator no longer creates anything. What is left are the
// decisions that need a person — suspend an instance, release a name, and write
// down who a name turned out to belong to.

// ConsoleHandler is the management UI and its assets.
//
// Everything it renders is an operator's view of the whole fleet — the route
// table, who reached what, which names are suspended — so the console is bucket B
// throughout and says so in every response, not merely by sitting behind a
// sign-in. Only the shared chrome is cacheable, and those handlers classify
// themselves (internal/ui).
func (s *Server) ConsoleHandler() http.Handler {
	return s.classified(s.consoleMux())
}

// classified installs the response classifier over the console. Bucket B is the
// default, so a console page added later is private without anyone remembering to
// say so.
func (s *Server) classified(next http.Handler) http.Handler {
	return cachepolicy.Middleware(cachepolicy.Options{
		SessionCookie: sessionCookie,
		Counters:      s.cacheStats,
	})(next)
}

func (s *Server) consoleMux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /assets/app.css", ui.ServeCSS)
	mux.HandleFunc("GET /assets/fonts/{name}", ui.ServeFont)
	mux.HandleFunc("GET /favicon.ico", ui.ServeFavicon)
	mux.HandleFunc("GET /health", s.handleHealth)

	mux.HandleFunc("GET /auth/login", s.handleLogin(s.OIDC))
	mux.HandleFunc("GET /auth/callback", s.handleCallback(s.OIDC))
	mux.HandleFunc("GET /auth/logout", s.handleLogout)

	mux.HandleFunc("GET /{$}", s.page(s.pageOverview))
	mux.HandleFunc("GET /instances", s.page(s.pageInstances))
	mux.HandleFunc("GET /instances/{name}", s.page(s.pageInstance))
	mux.HandleFunc("GET /intelligence", s.page(s.pageIntelligence))
	mux.HandleFunc("GET /ops", s.page(s.pageOps))
	// The overview's chart, re-rendered for each window the reader asks for.
	// Session-gated like the page that embeds it (s.gate), since it reads the
	// same log.
	mux.HandleFunc("GET /overview/chart", s.gate(s.handleRateChart))
	mux.HandleFunc("GET /settings", s.page(s.pageSettings))

	mux.HandleFunc("POST /instances/{name}/annotate", s.action(s.actionAnnotate))
	mux.HandleFunc("POST /instances/{name}/rename", s.action(s.actionRename))
	mux.HandleFunc("POST /instances/{name}/{action}", s.action(s.actionInstance))

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPrivate(w, r, "liveness")
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"instances":%d,"online":%d,"uptime_secs":%d,"cache":%s}`+"\n",
		len(s.Store.List()), len(s.LiveNames()), int(time.Since(s.started).Seconds()),
		cacheStatsJSON(s.cacheStats))
}

// cacheStatsJSON renders the classification tally for /health. The console's own
// responses are nearly all bucket B, so what this actually reports is whether the
// classifier is running at all.
func cacheStatsJSON(c *cachepolicy.Counters) string {
	raw, err := json.Marshal(c.Snapshot())
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// --- identity ---------------------------------------------------------------

// sessionCookie is the console's session cookie. Its name is owned by
// pkg/webpaths so this console and whatever configures the edge in front of it
// can never drift apart.
const sessionCookie = webpaths.IngressSessionCookieName

func (s *Server) user(r *http.Request) oidc.Claims { return s.userOf(s.providerFor(r, s.OIDC), r) }

// providerFor is the identity provider belonging to the host a request arrived
// on. Sign-in is per host — the callback address differs and the session cookie
// is host-only — so the provider cannot be bound when the mux is built; it has to
// be chosen per request or an operator signing in at one domain would be handed a
// callback at another. The fallback covers localhost and anything not in the map.
func (s *Server) providerFor(r *http.Request, fallback *oidc.Provider) *oidc.Provider {
	if p, ok := s.HostOIDC[normalHost(r.Host)]; ok && p != nil {
		return p
	}
	return fallback
}

// userOf reads the session a given provider signed. There are two providers, one
// per hostname the ingress signs anybody in on — the console's and the apex's
// (site.go) — because the identity provider is told exactly one callback address
// per client and those addresses differ. The cookie is host-only either way, so a
// session on one host is simply absent on the other.
func (s *Server) userOf(p *oidc.Provider, r *http.Request) oidc.Claims {
	if p == nil {
		return nil
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return p.VerifySession(c.Value)
}

// admitted reports whether this visitor may use the console. With no identity
// provider configured the console is open — correct for a laptop, and said
// plainly on every page rather than left for the operator to discover.
func (s *Server) admitted(user oidc.Claims) bool { return s.admittedBy(s.OIDC, user) }

func (s *Server) admittedBy(p *oidc.Provider, user oidc.Claims) bool {
	if p == nil {
		return true
	}
	if user == nil {
		return false
	}
	if len(s.Cfg.AdminEmails) == 0 {
		return true
	}
	email, _ := user["email"].(string)
	email = strings.ToLower(strings.TrimSpace(email))
	for _, allowed := range s.Cfg.AdminEmails {
		if allowed == email {
			return true
		}
	}
	return false
}

// The OAuth dance, parameterized by provider so the console and the apex share
// one implementation rather than two copies that drift. Everything host-specific
// is in the provider's redirect URI; the cookies below are host-only, which is
// what keeps the two sessions separate without a second cookie name.
func (s *Server) handleLogin(fallback *oidc.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.providerFor(r, fallback)
		if p == nil {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		state, txn := p.CreateTxn(r.URL.Query().Get("next"))
		http.SetCookie(w, &http.Cookie{
			Name: "tacit_ingress_txn", Value: txn, Path: "/", HttpOnly: true,
			Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 600,
		})
		// No account chooser here: each console host has its own provider and its
		// own host-only cookie, so this flow always starts and ends in one place.
		target, err := p.AuthorizeURL(state, "")
		if err != nil {
			http.Error(w, "the identity provider could not be reached: "+err.Error(), http.StatusBadGateway)
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
	}
}

func (s *Server) handleCallback(fallback *oidc.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.providerFor(r, fallback)
		if p == nil {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		txn, err := r.Cookie("tacit_ingress_txn")
		if err != nil {
			http.Error(w, "this sign-in did not start here", http.StatusBadRequest)
			return
		}
		next := localPath(p.VerifyTxn(txn.Value, r.URL.Query().Get("state")))
		tokens, err := p.ExchangeCode(r.URL.Query().Get("code"))
		if err != nil {
			http.Error(w, "the identity provider rejected this sign-in: "+err.Error(), http.StatusBadGateway)
			return
		}
		access, _ := tokens["access_token"].(string)
		claims, err := p.FetchUserinfo(access)
		if err != nil {
			http.Error(w, "could not read the profile behind this sign-in: "+err.Error(), http.StatusBadGateway)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: p.CreateSession(claims), Path: "/", HttpOnly: true,
			Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
			MaxAge: int(s.Cfg.SessionTTL.Seconds()),
		})
		http.Redirect(w, r, next, http.StatusFound)
	}
}

// handleLogout clears the session and returns to this host's own root, which is
// the overview on the console and the project page on the apex. Signing out of
// the apex therefore answers the question it is there to answer: what comes back
// with no session is what a stranger gets.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// --- page plumbing ----------------------------------------------------------

type view struct {
	title  string
	crumbs []ui.Crumb
	nav    string
	body   string
}

func (s *Server) page(render func(r *http.Request, user oidc.Claims) view) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := s.user(r)
		if !s.admitted(user) {
			s.sendHTML(w, http.StatusOK, s.renderSignin(r, user))
			return
		}
		v := render(r, user)
		s.sendHTML(w, http.StatusOK, s.shell(v, user).Render(ui.Page{
			Title: v.title, Crumbs: v.crumbs, Content: v.body,
		}))
	}
}

// action wraps a mutating handler: same admission check, then a redirect back
// to where the operator was.
func (s *Server) action(do func(r *http.Request, user oidc.Claims) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := s.user(r)
		if !s.admitted(user) {
			http.Error(w, "sign in first", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "unreadable form", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, do(r, user), http.StatusSeeOther)
	}
}

// gate wraps a handler that answers a page's fetch rather than serving a page:
// the same admission check, but a plain status instead of the sign-in document —
// nothing good comes of rendering a whole page into a fragment slot.
func (s *Server) gate(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.admitted(s.user(r)) {
			http.Error(w, "sign in first", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *Server) sendHTML(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func (s *Server) shell(v view, user oidc.Claims) ui.Shell {
	instances := s.Store.List()
	online := s.LiveNames()
	// The avatar, not the address: same component the registry dashboard uses,
	// so an operator moving between the two consoles sees one identity control.
	account := ui.Account(user, s.OIDC != nil, "")
	return ui.Shell{
		Brand: product.Name() + " Ingress",
		Nav: []ui.NavItem{
			{Label: "Overview", Href: "/", Active: v.nav == "overview"},
			{Label: "Instances", Href: "/instances", Active: v.nav == "instances"},
			{Label: "Intelligence", Href: "/intelligence", Active: v.nav == "intelligence"},
			{Label: "Operations", Href: "/ops", Active: v.nav == "ops"},
			{Label: "Settings", Href: "/settings", Active: v.nav == "settings"},
		},
		Meta:    fmt.Sprintf("%d published · %d online", len(instances), len(online)),
		Account: account,
	}
}

// renderSignin is the console's front door. It wears the registry's sign-in
// panel — centred card, the mark at full size, one accent button — because the
// two consoles are one product and a visitor should not have to work that out.
// What it does not wear is the registry's animated field: that page is a first
// impression, this is a gate on the way to work.
//
// Two visitors arrive here. One is signed out and needs the button. The other
// is signed in as somebody the admin list does not name, and the button would
// be a trap — it sends them back to a provider that will return them here. They
// get the address they are signed in as, since the usual cause is being signed
// in as the wrong one, and the way out.
func (s *Server) renderSignin(r *http.Request, user oidc.Claims) string {
	door := ui.SignIn{
		Brand:   product.Name(),
		Eyebrow: "Ingress console",
		Title:   "Sign in",
	}
	// Nothing else. Whoever reaches this page came here on purpose, from a link
	// or a bookmark; a sentence explaining what an ingress is would be selling
	// the thing they are already trying to get into.
	if user == nil {
		door.ActionLabel = "Sign in"
		door.ActionHref = "/auth/login?next=" + url.QueryEscape(r.URL.Path)
		return door.Render()
	}
	who := "this account"
	if email, _ := user["email"].(string); email != "" {
		who = email
	}
	door.Lede = "Signed in as " + who + ", which is not on this console's admin list."
	door.Note = "An operator adds an address to TACIT_INGRESS_ADMIN_EMAILS."
	door.Links = [][2]string{{"Sign out", "/auth/logout"}}
	return door.Render()
}

// --- pages ------------------------------------------------------------------

func (s *Server) pageOverview(r *http.Request, user oidc.Claims) view {
	instances := s.Store.List()
	online := s.LiveNames()

	var requests, errors5xx int64
	for _, in := range instances {
		m := s.Metrics.For(in.Name)
		requests += m.Requests
		errors5xx += m.ByClass["5xx"]
	}
	errRate := "0%"
	if requests > 0 {
		errRate = fmt.Sprintf("%.1f%%", 100*float64(errors5xx)/float64(requests))
	}

	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Every registry published through this ingress, and what it has served. ` +
		`The ingress holds no technique text or raw events. It keeps its route table, traffic record, and opted-in aggregate import outcomes.</p></div>`)
	if s.OIDC == nil {
		b.WriteString(`<p class="hint">No identity provider is configured, so this console is open to anyone who can reach it. ` +
			`Set TACIT_INGRESS_OIDC_ISSUER before putting it on a public address.</p>`)
	}
	b.WriteString(ui.Tiles(
		ui.Tile("Published", strconv.Itoa(len(instances)), ""),
		ui.Tile("Online now", strconv.Itoa(len(online)), offlineNote(len(instances)-len(online))),
		ui.Tile("Requests served", ui.FmtCount(int(requests)), coverageNote(s.Metrics.Covers())),
		ui.Tile("Server errors", errRate, "5xx share"),
	))
	// What every tenant adds up to, before the per-tenant table breaks it down.
	b.WriteString(s.memberPanel())
	b.WriteString(s.instanceTable(instances, online, 8))
	// Where a "Recent operations" panel used to repeat the Operations page's
	// newest dozen rows. Twelve records answered "what just happened" twice over
	// and never "is this normal" — which is what an overview is for.
	b.WriteString(s.ratePanel(s.parseRateView(r.URL.Query())))
	b.WriteString(RateChartScript)
	return view{title: "Overview", nav: "overview", body: b.String(),
		crumbs: []ui.Crumb{{Label: "Overview"}}}
}

func (s *Server) pageInstances(r *http.Request, user oidc.Claims) view {
	instances := s.Store.List()
	online := s.LiveNames()

	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Registries enrol themselves. A registry generates its own key, ` +
		`presents it here, and is given a hostname — there is no name to approve and no token for anyone to hand out. ` +
		`What an operator decides is the opposite: which of them stops.</p></div>`)
	b.WriteString(s.instanceTable(instances, online, 0))
	b.WriteString(ui.Panel("Admission", "", s.admissionTable(len(instances))))
	return view{title: "Instances", nav: "instances", body: b.String(),
		crumbs: []ui.Crumb{{Label: "Instances"}}}
}

// admissionTable reports the policy that stands in for approval now that
// nobody approves anything.
func (s *Server) admissionTable(count int) string {
	return kvTable([][2]string{
		{"New registries", ui.Esc(map[bool]string{
			true:  "accepted — a registry that presents an unknown key is given a hostname",
			false: "closed — only registries already enrolled can connect",
		}[s.Cfg.EnrollOpen])},
		{"Capacity", ui.Esc(fmt.Sprintf("%d of %s used", count, unlimited(s.Cfg.MaxInstances)))},
		{"Per-address ceiling", ui.Esc(fmt.Sprintf("%s new enrolments an hour (reconnections are never counted)",
			unlimited(s.Cfg.EnrollPerHour)))},
		{"Hostnames", ui.Esc("allocated here, two words from a fixed vocabulary, never requested")},
	})
}

// instanceMemberPanel is the overview's Member machines panel, asked about one
// tenant: the same four windows, the same sparklines, the same sentence saying
// what they cover. An operator moving between the proxy's total and a registry's
// own page compares like with like, which is the point of using one panel.
//
// Each window is machines seen in the period ENDING NOW. They are not a series:
// the year is not twelve months added up, it is everyone who appeared at least
// once in it, so day ≤ week ≤ month ≤ year by construction.
func (s *Server) instanceMemberPanel(in Instance) string {
	day, week, month, year := memberSeriesOf([]Instance{in}, memberTrendDays)
	var cov strings.Builder
	cov.WriteString("machines, not people — as this registry reported them")
	if !in.MembersAt.IsZero() {
		// The stamp already says how long ago; the clause after it says what
		// that means, without saying the age a second time.
		fmt.Fprintf(&cov, " · reported %s", ui.Esc(stamp(in.MembersAt)))
		if time.Since(in.MembersAt) > staleMembers {
			cov.WriteString(", so these are its last known figures")
		}
	}
	if !in.MembersWindows && !in.MembersAt.IsZero() {
		cov.WriteString(" · this build reports the week only")
	}
	if len(day) > 1 {
		fmt.Fprintf(&cov, " · lines show the last %d days", len(day))
	}
	return memberPanelHTML(memberView{
		Stats: Stats{Day: in.MembersDay, Week: in.Members,
			Month: in.MembersMonth, Year: in.MembersYear},
		Reported:   !in.MembersAt.IsZero(),
		HasWindows: in.MembersWindows,
		Series:     [4][]int{day, week, month, year},
		Coverage:   cov.String(),
		Empty:      "This registry has not reported its size. Nothing about the past is recoverable, so its record starts whenever it first does.",
		Below:      membersHistory(in),
	})
}

// membersHistory is the whole of what a registry has reported, charted — the
// long form of the sparklines above it, which stop at thirty days.
//
// It is deliberately not offered as a window the reader can widen: this is the
// only history that exists, because none of it can be recovered. A member key
// carries one last-seen timestamp, overwritten on every use, so the day a
// registry first reports is the first day anyone can ever know about — the
// chart starts there and grows.
func membersHistory(in Instance) string {
	if len(in.Days) < 2 {
		if in.MembersAt.IsZero() {
			return ""
		}
		return `<p class="hint">A day's figures are kept each time this registry reports, so a trend appears ` +
			`here from tomorrow. Nothing before the first report can be recovered: a member key holds one ` +
			`last-seen timestamp, and it is overwritten every time the key is used.</p>`
	}
	labels := make([]string, len(in.Days))
	day := make([]int, len(in.Days))
	month := make([]int, len(in.Days))
	for i, d := range in.Days {
		// "Jul 28", not "2026-07-28": the axis pins its last label to the plot's
		// right edge and centres it there, so a ten-character date hangs half
		// its width off the panel and loses the end of itself. The house's
		// charts read in this format anyway.
		labels[i] = d.Day
		if t, err := time.Parse("2006-01-02", d.Day); err == nil {
			labels[i] = t.Format("Jan 2")
		}
		day[i], month[i] = d.Day1, d.Month
	}
	// Labels only, no instants: these buckets are stored DAY KEYS, already
	// aggregated into calendar days upstream. A bucket start can honestly be
	// re-dated for a reader elsewhere; a day key cannot, because its boundary
	// is fixed in whatever zone did the counting and moving the label alone
	// would misdescribe which rows are in it.
	return fmt.Sprintf(`<p class="hint">the whole record · %d days kept</p>`, len(in.Days)) +
		string(ui.LineChart(ui.Axis{Labels: labels}, []ui.Series{
			{Name: "seen that day", Key: ui.LineSeriesKeys[0], Values: day},
			{Name: "seen in the month before", Key: ui.LineSeriesKeys[1], Values: month},
		}, 200))
}

func unlimited(n int) string {
	if n <= 0 {
		return "unlimited"
	}
	return strconv.Itoa(n)
}

func (s *Server) pageInstance(r *http.Request, user oidc.Claims) view {
	name := r.PathValue("name")
	in, ok := s.Store.Get(name)
	if !ok {
		return view{title: "Unknown instance", nav: "instances",
			body:   `<p class="empty">No instance by that name.</p>`,
			crumbs: []ui.Crumb{{Label: "Instances", Href: "/instances"}, {Label: name}}}
	}
	st := s.StatusOf(name)
	m := s.Metrics.For(name)

	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">` + statusSentence(in, st) + `</p></div>`)
	if msg := r.URL.Query().Get("error"); msg != "" {
		b.WriteString(`<p class="error">` + ui.Esc(msg) + `</p>`)
	}
	b.WriteString(ui.Tiles(
		ui.Tile("Requests", ui.FmtCount(int(m.Requests)), coverageNote(s.Metrics.Covers())),
		ui.Tile("p95 latency", strconv.Itoa(m.P95())+" ms", "p50 "+strconv.Itoa(m.P50())+" ms"),
		ui.Tile("Server errors", fmt.Sprintf("%.1f%%", 100*m.ErrorRate()), ""),
		ui.Tile("Parked links", strconv.Itoa(st.Parked), "spare connections"),
	))

	rows := [][2]string{
		{"Public address", `<a href="` + ui.Esc(s.Cfg.PublicURL(name)) + `">` + ui.Esc(s.Cfg.PublicURL(name)) + `</a>`},
		{"Identity", `<code>` + ui.Esc(orDash(in.Fingerprint)) + `</code>`},
		{"Owner", ui.Esc(orDash(in.Owner))},
		{"Enrolled", ui.LocalTime(in.Created, ui.LTStamp)},
		{"Registry build", ui.Esc(orDash(firstNonEmpty(st.Version, in.Version)))},
		{"Last handshake", stamp(in.LastSeen)},
		// The live tunnel's address while there is one, the last recorded
		// otherwise — the same answer the instances table gives, so the two
		// views cannot disagree about where a registry connects from.
		{"Tunnel source", ui.Esc(orDash(firstNonEmpty(st.RemoteStr, in.Source)))},
		{"Tunnel churn", fmt.Sprintf("%d connects · %d drops", m.Connects, m.Disconnects)},
		{"Note", ui.Esc(orDash(in.Note))},
	}
	b.WriteString(ui.Panel("Route", "", kvTable(rows)))

	b.WriteString(s.instanceMemberPanel(in))
	b.WriteString(ui.Panel("Traffic, last 24 hours", "requests per hour · red where the registry returned 5xx", dayBars(m.Day())))
	b.WriteString(ui.Panel("Status mix", "", classTable(m)))
	b.WriteString(ui.Panel("Tunnel events", "every time this registry connected or dropped, from the operations log",
		tunnelTable(s.Ops.Recent(OpFilter{Instance: name, Limit: 12, Kind: FilterTunnel}))))
	b.WriteString(ui.Panel("Recent operations", `newest first · <a href="/ops?instance=`+url.QueryEscape(name)+`">all for this instance</a>`,
		opsTable(s.Ops.Recent(OpFilter{Instance: name, Limit: 25, Kind: FilterRequests}), false)))

	b.WriteString(ui.Panel("Name",
		"the registry keeps its identity and learns the new address when it reconnects; "+
			"anything already pointing at the old one — join links, an OIDC callback — needs updating",
		`<form method="post" action="/instances/`+ui.Esc(name)+`/rename" class="form-grid">`+
			`<label class="full">Hostname<input name="name" value="`+ui.Esc(name)+`" `+
			`pattern="[a-z0-9][a-z0-9-]{1,30}[a-z0-9]" required></label>`+
			`<div class="full"><button class="btn" type="submit">Rename</button> `+
			`<span class="hint">answers at <code>`+ui.Esc(s.Cfg.Scheme+"://&lt;name&gt;."+s.Cfg.Zone)+`</code></span></div>`+
			`</form>`))

	b.WriteString(ui.Panel("Annotate", "the registry never sends either; this is the operator's own note of who a name belongs to",
		`<form method="post" action="/instances/`+ui.Esc(name)+`/annotate" class="form-grid">`+
			`<label>Owner<input name="owner" type="email" value="`+ui.Esc(in.Owner)+`" placeholder="who is answerable for it"></label>`+
			`<label>Note<input name="note" value="`+ui.Esc(in.Note)+`" placeholder="optional"></label>`+
			`<div class="full"><button class="btn" type="submit">Save</button></div>`+
			`</form>`))

	suspend, label := "suspend", "Suspend"
	if in.Disabled {
		suspend, label = "resume", "Resume"
	}
	b.WriteString(ui.Panel("Operator actions",
		"suspending drops the tunnel immediately and refuses reconnection; releasing frees the name, and the registry "+
			"will enrol again under a NEW one the next time it connects — its key still works, it just no longer means this hostname",
		`<div class="btn-row">`+
			`<form method="post" action="/instances/`+ui.Esc(name)+`/`+suspend+`">`+
			`<button class="btn" type="submit">`+label+`</button></form>`+
			`<form method="post" action="/instances/`+ui.Esc(name)+`/delete" `+
			`onsubmit="return confirm('Release `+ui.Esc(name)+`? Anyone can claim the name afterwards.')">`+
			`<button class="btn danger" type="submit">Release name</button></form>`+
			`</div>`))

	return view{title: name, nav: "instances", body: b.String(),
		crumbs: []ui.Crumb{{Label: "Instances", Href: "/instances"}, {Label: name}}}
}

// opsPageSize is how many operations one view of the log carries. The log grows
// without limit and this page is a window onto it, so the response has to be
// bounded by something that does not grow with the log: a page, walked with the
// pager under the table. The whole match set was never going to be read from top
// to bottom in a browser, and sending it made every visit pay for the part
// nobody looked at.
const opsPageSize = 50

func (s *Server) pageOps(r *http.Request, user oidc.Claims) view {
	f := OpFilter{
		Instance: r.URL.Query().Get("instance"),
		Class:    r.URL.Query().Get("class"),
		Kind:     r.URL.Query().Get("kind"),
		Limit:    opsPageSize,
	}
	if f.Kind == "" {
		f.Kind = FilterRequests // the default view is what was served
	}
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 {
			f.Since = time.Now().Add(-time.Duration(n) * time.Hour)
		}
	}
	page := 1
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 1 {
		page = n
	}
	f.Offset = (page - 1) * opsPageSize
	res := s.Ops.Page(f)
	// A page past the end — a link followed after the records it named aged out,
	// or a filter narrowed with ?page= still on the URL — shows the last page
	// that has rows rather than an empty table and a dead pager.
	if last := opsLastPage(res.Total); page > last {
		page = last
		f.Offset = (page - 1) * opsPageSize
		res = s.Ops.Page(f)
	}
	ops := res.Ops

	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">What the ingress forwarded: the envelope of every request — who asked, ` +
		`for what path, with what result — plus every tunnel connect and drop. No bodies, no query strings, no headers ` +
		`beyond the user agent, and client addresses truncated to a network. ` + s.retentionSentence() + `</p></div>`)

	b.WriteString(`<form method="get" class="form-grid form-filter">` +
		`<label>Instance<input name="instance" value="` + ui.Esc(f.Instance) + `" placeholder="all"></label>` +
		`<label>Status<select name="class">` + classOptions(f.Class) + `</select></label>` +
		`<label>Kind<select name="kind">` + kindOptions(f.Kind) + `</select></label>` +
		`<label>Hours<input name="hours" type="number" min="1" max="720" value="` +
		ui.Esc(r.URL.Query().Get("hours")) + `" placeholder="all"></label>` +
		`<div class="full"><button class="btn" type="submit">Filter</button></div>` +
		`</form>`)
	table := opsTable(ops, true)
	if f.Kind == FilterTunnel {
		table = tunnelTable(ops)
	}
	b.WriteString(ui.Panel("", opsCountHint(page, len(ops), res),
		table+opsPager(r.URL, page, opsLastPage(res.Total))))
	return view{title: "Operations", nav: "ops", body: b.String(),
		crumbs: []ui.Crumb{{Label: "Operations"}}}
}

// opsLastPage is the number of the last page with rows on it; at least 1, so an
// empty log still has a page to be on.
func opsLastPage(total int) int {
	if total <= opsPageSize {
		return 1
	}
	return (total + opsPageSize - 1) / opsPageSize
}

// opsCountHint says which slice of the log is on screen. It names the total as
// well as the range because the range alone reads as the whole thing, and it
// says when the total is a count of the tail rather than of the file — a page
// count over a truncated read would otherwise present part of the log as all of
// it.
func opsCountHint(page, shown int, res OpPage) string {
	if shown == 0 {
		return "no operations match"
	}
	first := (page-1)*opsPageSize + 1
	hint := fmt.Sprintf("%d–%d of %d operations", first, first+shown-1, res.Total)
	if res.Truncated {
		hint += " in the newest " + formatBytes(maxScan) + " of the log"
	}
	if last := opsLastPage(res.Total); last > 1 {
		hint += fmt.Sprintf(" · page %d of %d", page, last)
	}
	return hint
}

// opsPager walks the pages. It keeps whatever filter the operator set — the
// links carry the current query with only page changed — and offers only the
// directions that lead somewhere, so there is no dead control to click. The
// filter form has no page field, so filtering always lands back on page 1.
func opsPager(u *url.URL, page, last int) string {
	if last <= 1 {
		return ""
	}
	href := func(p int) string {
		q := u.Query()
		if p <= 1 {
			q.Del("page") // page 1 is the plain address
		} else {
			q.Set("page", strconv.Itoa(p))
		}
		if enc := q.Encode(); enc != "" {
			return "/ops?" + enc
		}
		return "/ops"
	}
	var b strings.Builder
	b.WriteString(`<nav class="btn-row table-pager" aria-label="Operation pages">`)
	if page > 1 {
		fmt.Fprintf(&b, `<a class="btn" href="%s" rel="prev">« Newer</a>`, ui.Esc(href(page-1)))
	}
	if page < last {
		fmt.Fprintf(&b, `<a class="btn" href="%s" rel="next">Older »</a>`, ui.Esc(href(page+1)))
	}
	b.WriteString(`</nav>`)
	return b.String()
}

// retentionSentence says what retention actually is, which is a size ceiling
// with an age sweep behind it — not the flat "90 days" this page used to claim
// while keeping a busy log for two days and a quiet one forever.
func (s *Server) retentionSentence() string {
	return fmt.Sprintf("Records are dropped once they are %d days old, and the log rotates if it passes %s before then.",
		s.Cfg.OpLogRetentionDays, formatBytes(s.Cfg.OpLogMaxBytes))
}

func (s *Server) pageSettings(r *http.Request, user oidc.Claims) view {
	rows := [][2]string{
		{"Zone", s.Cfg.Zone + " — registries only"},
	}
	if s.Cfg.SiteElsewhere() {
		rows = append(rows, [2]string{"Product domain", s.Cfg.SiteHostname() + " — the project page; the zone's apex redirects here"})
	}
	rows = append(rows, [][2]string{
		{"Console host", s.Cfg.AdminHost},
		{"Public listener", s.Cfg.PublicAddr},
		{"Tunnel listener", s.Cfg.TunnelAddr},
		{"Scheme", s.Cfg.Scheme},
		{"Data directory", s.Cfg.DataDir},
		{"Identity provider", orDash(s.Cfg.OIDCIssuer)},
		{"Admin addresses", orDash(strings.Join(s.Cfg.AdminEmails, ", "))},
		{"Spare connections per instance", strconv.Itoa(s.Cfg.IdleConns)},
		{"Requests per instance per minute", strconv.Itoa(s.Cfg.RatePerMinute)},
		{"Max request body", formatBytes(s.Cfg.MaxRequestBytes)},
		{"Operations retained", strconv.Itoa(s.Cfg.OpLogRetentionDays) + " days, pruned every 6 hours"},
		{"Operations log rotates at", formatBytes(s.Cfg.OpLogMaxBytes) + ", one generation deep"},
		{"Console figures cover", coverageSince(s.Metrics.Covers())},
		{"Client addresses", map[bool]string{true: "kept in full", false: "truncated to a network"}[s.Cfg.FullClientIP]},
		{"Uptime", time.Since(s.started).Round(time.Second).String()},
	}...)
	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Configuration is read from the environment at startup ` +
		`(TACIT_INGRESS_*), so this page reports rather than edits — the deployment that set these values is where they change.</p></div>`)
	b.WriteString(ui.Panel("Running configuration", "", kvCodeTable(rows)))
	b.WriteString(ui.Panel("Forwarded paths",
		"requests outside these prefixes are refused at the ingress and never reach a tunnel",
		`<p><code>`+ui.Esc(strings.Join(s.Cfg.AllowPrefixes, " "))+`</code></p>`))
	return view{title: "Settings", nav: "settings", body: b.String(),
		crumbs: []ui.Crumb{{Label: "Settings"}}}
}

// --- actions ----------------------------------------------------------------

func (s *Server) actionAnnotate(r *http.Request, user oidc.Claims) string {
	name := r.PathValue("name")
	_ = s.Store.SetOwnerNote(name, r.FormValue("owner"), r.FormValue("note"))
	return "/instances/" + url.PathEscape(name)
}

// actionRename gives an instance a chosen hostname. Enrolment cannot request
// one — this is the operator's privilege, and it is available only because this
// console is behind an identity provider and an address allowlist.
func (s *Server) actionRename(r *http.Request, user oidc.Claims) string {
	from, to := r.PathValue("name"), r.FormValue("name")
	if err := s.Store.Rename(from, to); err != nil {
		return "/instances/" + url.PathEscape(from) + "?error=" + url.QueryEscape(err.Error())
	}
	// The tunnel is keyed by the old name and the registry still believes it.
	// Drop it WITHOUT a bye, so the client reconnects and is told the new
	// address — a bye would make it stop trying, which is the suspension path.
	s.DropTunnel(from)
	s.Metrics.Drop(from)
	s.logf("instance renamed: %s -> %s by %s", from, to, orDash(emailOf(user)))
	return "/instances/" + url.PathEscape(to)
}

func emailOf(user oidc.Claims) string {
	email, _ := user["email"].(string)
	return email
}

func (s *Server) actionInstance(r *http.Request, user oidc.Claims) string {
	name := r.PathValue("name")
	switch r.PathValue("action") {
	case "suspend":
		_ = s.Store.SetDisabled(name, true)
		s.Disconnect(name)
		s.logf("instance suspended: %s", name)
	case "resume":
		_ = s.Store.SetDisabled(name, false)
	case "delete":
		s.Disconnect(name)
		_ = s.Store.Delete(name)
		s.Metrics.Drop(name)
		s.logf("instance released: %s", name)
		return "/instances"
	}
	return "/instances/" + url.PathEscape(name)
}

// tunnelHost is the address a registry points its "Access through the OpenTacit
// proxy" setting at: this ingress's public URL. The tunnel arrives on the same
// port as everything else, so there is no second address to explain.
func (s *Server) tunnelHost() string {
	return s.Cfg.Scheme + "://" + hostOnly(s.Cfg.AdminHost) + s.Cfg.PublicPortSuffix()
}
