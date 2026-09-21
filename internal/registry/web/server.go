// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package web is the registry's single service surface (docs/design/design.md).
//
// Endpoints, all JSON, versioned under /v1:
//
//	POST /v1/evidence          Characterization -> EvidenceBlock (retrieval)
//	POST /v1/feedback          FeedbackEvent(s) -> {accepted[,duplicates]}
//	POST /v1/contribute        member-drafted technique -> held-out draft
//	GET  /v1/techniques[/{id}]      inspect techniques (admin)
//	POST /v1/admin/promote     reviewer promotes a draft
//	POST /v1/admin/sync-techniques  reload git techniques + embed
//	POST /v1/admin/recompute   force outcome rollup
//	GET  /v1/health            liveness
//
// Auth: a single X-Tacit-Key header for /v1 (not health). The browsable
// HTML view (/, /techniques, /drafts, /docs) has separate, optional OIDC sign-in;
// the one state-changing browser action (promote/reject on Drafts) is gated by
// a signed-in session when OIDC is on. The shell template in shell.go is
// extracted verbatim from the Python implementation's app.py.
package web

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/internal/registry/suggest"
	"github.com/opentacit/tacit/internal/registry/tagmerge"
	"github.com/opentacit/tacit/pkg/feed"
	"github.com/opentacit/tacit/pkg/webpaths"
)

const (
	// The session cookie's name is fleet-wide, not per-instance: one Cloudflare
	// rule bypasses every tenant's authenticated traffic because every tenant
	// spells it the same way (cachepolicy, docs/distribution/cloudflare-caching.md).
	sessionCookie = webpaths.SessionCookieName
	txnCookie     = "tacit_oidc_txn"

	// canonParam marks a sign-in already sent to the callback's host once, so a
	// proxy that rewrites Host cannot make the hop repeat (beginOnCallbackHost).
	canonParam = "canon"
	// switchParam asks the provider for its account chooser (handleAuthLogin).
	switchParam = "switch"
)

// Server wires the store, embedder, and optional OIDC provider into handlers.
type Server struct {
	Cfg      config.Config
	Store    storage.Store
	Embedder embed.Embedder
	OIDC     *oidc.Provider // nil when OIDC is not configured
	// RestartRegistry brings startup-only settings into force — today, the
	// sign-in the Settings page can turn on (signin.go). Nil means "ask
	// servicectl", which is what a real registry does; a test sets it so the
	// suite never touches the developer's own service.
	RestartRegistry func() (bool, error)
	Suggest         suggest.Researcher // nil = build from env per call (tests inject)
	// ModelLister returns a provider's available models for the settings combo
	// box. nil = fetch live from the provider (cached); tests inject.
	ModelLister func(provider string) []suggest.ModelInfo
	DocsDir     string
	Version     string       // stamped build version (cmd/tacit); "" hides it from /v1/health
	MCP         http.Handler // optional remote MCP endpoint (POST /mcp); nil = not exposed

	// EmbedWanted is the embedder the config ASKED for, when it differs from
	// the one actually serving (the startup onnx fallback). Health reports the
	// mismatch so a degraded registry is observable from any machine — the
	// operator's local config is not required to notice it (tacit doctor).
	EmbedWanted string

	// QuietStartup drops the informational startup log. Set when `tacit init`
	// serves at a console, where every one of those lines repeats something the
	// report above it already said, in a service log's voice, to somebody
	// meeting the product. Failures are never quiet.
	QuietStartup bool

	// analytics is the shared read of the feedback log every analytic surface
	// folds (analytics_inputs.go), reused while the store's row counts say
	// nothing has been added.
	analytics analyticsCache

	// cfgMu guards Cfg against the handful of settings a running registry can
	// change under itself — the Settings save, the setup wizard's last step, and
	// the switch to team auth. Every other field is written once at startup and
	// only read afterwards.
	//
	// Readers go through cfg(), which hands back a snapshot: a request reading
	// four related settings should see one save's worth of them, not two halves
	// of two. Writers go through mutateCfg. Assigning to s.Cfg from a handler is
	// how this raced in the first place, and cfg() returning a value rather than
	// a pointer is what keeps it from happening again.
	cfgMu sync.RWMutex

	// joinMu serializes invitation redemption, so two requests carrying the
	// same invitation cannot both decide it is unspent (join.go).
	joinMu sync.Mutex

	// handoffs parks a freshly minted member key under a short code, so the
	// browser that accepted an invitation never displays the key itself
	// (handoff.go). In memory on purpose: the entries live minutes.
	handoffMu sync.Mutex
	handoffs  map[string]handoffEntry

	// pub owns the tunnel to the shared ingress when "Access through the OpenTacit
	// proxy" is on (publish.go). PublishHandler is what that tunnel serves —
	// the same handler the local listener uses, set by whoever built it.
	pub            publisher
	PublishHandler http.Handler

	// SetupGate is the first-run gate (setup.go); nil on a configured registry.
	SetupGate *SetupState

	// Demo is demonstration mode (demo.go); nil unless TACIT_DEMO_DIR is set.
	// Shared Catalog across the process's instances, per-instance Active.
	Demo *DemoState

	// contributed caches what an organization said about the techniques this
	// registry merged into it (contributed.go). Empty on any registry that has
	// not run `tacit merge`.
	contributed contributedCache

	// merging is a merge started from the playbook page, running in the
	// background of the process that served it (contributed.go).
	merging mergeProgress

	// RequestStop asks the process serving this registry to stop, after the
	// current request has been answered. nil means it cannot: a registry that
	// cannot stop itself must refuse to retire itself, because the alternative
	// is releasing a public address out from under a process that keeps
	// serving. `tacit serve` sets it; tests and embedded uses leave it nil.
	RequestStop func(why string)

	// WireHarnesses points this machine's AI tools at another registry. nil
	// runs `tacit connect` as a subprocess, which is the real path — the wiring
	// lives in the CLI and reaching it any other way would mean lifting a dozen
	// harnesses' settings handling into a library for one extra caller. Tests
	// replace it, because the subprocess a test would run is the test binary.
	WireHarnesses func(destination, key string) error

	// suggestTimes tracks recent research-run durations for the Drafts page
	// progress bar (suggeststats.go).
	suggestTimes *suggestStats

	// cacheStats tallies how every response was classified (cachepolicy).
	// /v1/health reports it, so "is anything actually cacheable?" is answerable
	// from the origin rather than only from Cloudflare's analytics.
	cacheOnce  sync.Once
	cacheStats *cachepolicy.Counters

	// preload is the stylesheet preload header every HTML response carries,
	// built once because it depends only on the mount prefix and the build.
	preloadOnce sync.Once
	preload     string

	// TagMerger proposes tag consolidations (tags.go). nil = build from env per
	// call, as Suggest does; tests inject a fake so the flow is exercised
	// without a network.
	TagMerger tagmerge.Model

	// tagProposal holds the merge proposals awaiting a human's verdict. They are
	// deliberately NOT persisted: a proposal is a suggestion mid-conversation,
	// not registry state, and a restart losing it costs one cheap API call. It
	// is a single slot because the registry has one operator at a time — a
	// second proposal run replaces the first rather than queueing behind it.
	tagPropMu sync.Mutex
	tagProp   *tagProposal

	// consolidate holds each lane's duplicate groups awaiting a verdict, on the
	// same terms as tagProp above: not persisted, a second run of a lane
	// replaces that lane's. Keyed by lane, because the playbook's duplicates and
	// the queue's are separate decisions and one must not clear the other.
	consolidateMu sync.Mutex
	consolidate   map[string]*consolidation

	// ClusterNamer names the knowledge-map clusters (techniquemap.go). nil = build
	// from env per call; tests inject a fake.
	ClusterNamer clusterModel

	// clusterCache holds LLM-derived names/descriptions for map clusters, keyed
	// by the cluster's member signature so a cluster with the same members keeps
	// its label across reloads and filters. In-memory: the labels are cosmetic
	// and cheap to regenerate, so a restart losing them costs one API call.
	clusterMu    sync.Mutex
	clusterCache map[string]clusterLabel

	public publicState

	// feedRejects counts unauthorized federation reads per channel (feedtokens.go).
	feedRejects rejectedFeedReads

	pubMu  sync.Mutex
	pubKey ed25519.PrivateKey // lazy: key file created on first use
	pubID  string             // pinned provider id, once there is an address worth pinning

	// rebaser rewrites root-absolute URLs in outgoing HTML onto Cfg.BasePath
	// (see rebase). Built once: the pair set is fixed for the process.
	rebaseOnce sync.Once
	rebaser    *strings.Replacer

	touchMu sync.Mutex
	touched map[string]time.Time // member-key id -> last persisted last-seen
}

// publisher builds this registry's federation publisher (creating the signing
// key at DataDir/feed_key on first use).
//
// The key and the provider id are cached; the BASE URL is deliberately resolved
// on every call. Publishing is a hot-apply switch whose address arrives
// asynchronously — the ingress says what it is in OnWelcome, some time after the
// operator flips the setting — so a publisher memoised at first use would keep
// whatever answer was true before the tunnel came up, and serve loopback feed
// URLs for the rest of the process's life. That failure is invisible from here
// (the routes all answer) and total at the subscriber (nothing in the answer is
// reachable), which is the worst shape a bug can have.
func (s *Server) publisher() (*federation.Publisher, error) {
	// Resolved before the lock: federationBase reads the publish state under its
	// own mutex, and there is no reason to hold two.
	base, identity := s.federationBase(), s.identityCandidate()

	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	if s.pubKey == nil {
		key, err := feed.LoadOrCreateKey(filepath.Join(s.cfg().DataDir, "feed_key"))
		if err != nil {
			return nil, err
		}
		s.pubKey = key
	}
	providerID, err := s.providerIDLocked(identity)
	if err != nil {
		return nil, err
	}
	return &federation.Publisher{
		Store: s.Store, Key: s.pubKey,
		ProviderID: providerID, ProviderName: s.cfg().FeedProviderName,
		BaseURL:       base,
		PublicMembers: s.publicMembersForFeed,
	}, nil
}

// durableBase is where this registry is reachable from outside, or "" when the
// only honest answer is loopback.
//
// Publishing wins over the configured external URL for the reason PublishedBase
// gives: an operator who turned the proxy on has said where the registry is
// reachable from, and the URL they set for an earlier arrangement is the stale
// one. This is the same precedence externalBaseFor (join links) and oauthBaseURL
// (the MCP endpoint) already apply; the federation surface was the one public
// surface that did not, which meant a registry whose only address came from the
// proxy published a descriptor and feed advertising 127.0.0.1.
func (s *Server) durableBase() string {
	if base := s.PublishedBase(); base != "" {
		return base + s.cfg().BasePath
	}
	return s.cfg().ExternalBase()
}

// federationBase is where the descriptor, feeds and canonical technique documents are
// served — the address that goes INTO those documents, so it has to be one a
// stranger can dial.
func (s *Server) federationBase() string {
	c := s.cfg()
	if base := s.durableBase(); base != "" {
		return base
	}
	return fmt.Sprintf("http://%s:%d", orLocal(c.Host), c.Port) + c.BasePath
}

// identityCandidate is the address worth PINNING as this registry's provider id,
// or "" when there is nothing durable enough to pin yet.
//
// Its precedence is the reverse of durableBase's, and deliberately so. Location
// prefers the proxy, because that is where the registry currently answers.
// Identity prefers the CONFIGURED external URL, because a provider id must be the
// same value on every startup and a proxy address is not: it arrives
// asynchronously, so whether it is known at the moment the id is first needed
// depends on whether the tunnel happened to connect first. Pinning from the racy
// value would make a registry's permanent name a function of startup timing —
// observed on this project's own registry, which pinned its Funnel URL because
// the tunnel was still connecting, and would have pinned its proxy name on a
// luckier boot. An operator's configured URL is a deliberate, stable choice;
// falling back to the proxy address covers the self-hoster who has no URL of
// their own, which is the case Global Access exists for.
func (s *Server) identityCandidate() string {
	if base := s.cfg().ExternalBase(); base != "" {
		return base
	}
	if base := s.PublishedBase(); base != "" {
		return base + s.cfg().BasePath
	}
	return ""
}

// providerIDLocked returns the URI that names this registry as a publisher,
// pinning it on first use. Callers hold pubMu.
//
// It deliberately does NOT follow the address. feed.EntryID derives an entry's
// permanent id from the provider id, and federation-design.md keeps that URI as
// an imported technique's provenance — so a provider id that moved when the proxy was
// switched on would make every subscriber see the whole channel as new techniques
// rather than updates: the feed duplicated in every consumer's drafts lane, the
// old copies orphaned under an id nothing will ever retract. What authenticates a
// publisher is the signing key, which is stable and stored separately, so pinning
// the id costs nothing in trust and buys continuity across every future address
// change (docs/distribution/global-access-plan.md).
//
// The pin waits for an address worth pinning: naming a registry permanently after
// http://127.0.0.1:8080 would be worse than having no pin at all, so a registry
// that has never been reachable serves a loopback id and pins the first real
// address it is given.
func (s *Server) providerIDLocked(candidate string) (string, error) {
	c := s.cfg()
	if id := strings.TrimSpace(c.FeedProviderID); id != "" {
		return id, nil // an operator's explicit choice outranks the pin
	}
	if s.pubID != "" {
		return s.pubID, nil
	}
	if id := feed.PinnedProviderID(c.DataDir); id != "" {
		s.pubID = id
		return id, nil
	}
	if candidate == "" {
		return fmt.Sprintf("http://%s:%d", orLocal(c.Host), c.Port) + c.BasePath, nil
	}
	if err := feed.PinProviderID(c.DataDir, candidate); err != nil {
		return "", err
	}
	s.pubID = candidate
	return candidate, nil
}

func orLocal(host string) string {
	if host == "" || host == "0.0.0.0" || host == "::" {
		return "127.0.0.1"
	}
	return host
}

// New builds a Server; OIDC is enabled only when fully configured.
func New(cfg config.Config, st storage.Store, embedder embed.Embedder, docsDir string) *Server {
	s := &Server{Cfg: cfg, Store: st, Embedder: embedder, DocsDir: docsDir}
	s.suggestTimes = &suggestStats{path: filepath.Join(cfg.DataDir, "suggest_runs.json")}
	if cfg.OIDCEnabled() {
		secret := cfg.SessionSecret
		if secret == "" {
			secret = models.NewID("") // fresh per process: sessions reset on restart
		}
		s.OIDC = &oidc.Provider{
			Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret, RedirectURI: cfg.OIDCRedirectURI,
			Scopes: cfg.OIDCScopes, Secret: []byte(secret),
			TTL: time.Duration(cfg.SessionTTLSecs) * time.Second,
		}
		// Resolved per request, not at construction: publishing can be switched
		// on while the registry runs, and the callback has to follow it.
		s.OIDC.RedirectURIFunc = s.OIDCRedirectURI
	}
	return s
}

// cfg is the settings snapshot a request reads. Cheap enough to call freely —
// it copies a plain struct under a read lock — and safe to hold for the length
// of a handler, because the copy cannot be changed underneath it.
func (s *Server) cfg() config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.Cfg
}

// mutateCfg applies a hot-applied settings change. The whole change goes in
// under one lock, so no reader sees a save half-done.
//
// Slice fields must be REPLACED, never appended to in place: a snapshot shares
// the backing array with the live config, and editing one would reach into
// every reader already holding it.
func (s *Server) mutateCfg(apply func(*config.Config)) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	apply(&s.Cfg)
}

// Handler returns the routed http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	s.mountAPIRoutes(mux)

	s.mountServiceRoutes(mux)

	s.mountPageRoutes(mux)

	return s.wrap(s.setupGate(mux))
}

// wrap is the middleware chain every response passes through, factored out so a
// test can put a handler at the end of the real one rather than an approximation
// of it.
//
// Classification wraps everything, inside the access log so one line can report
// what the response was and why (cachepolicy, and
// docs/distribution/cloudflare-caching.md). Nothing may write past it: the policy
// is committed with the header, and a header cannot be corrected once the body
// has started.
//
// Every writer added here must carry Unwrap() http.ResponseWriter, or it becomes
// the point where a flush, a hijack or a deadline stops.
func (s *Server) wrap(next http.Handler) http.Handler {
	return logRequests(s.cfg().BasePath, s.cacheClassified(s.withBasePath(next)))
}

// cacheClassified installs the response classifier. Bucket B — private,
// no-store — is what every route gets unless it positively declares otherwise,
// so a route added tomorrow is private until someone has thought about it.
func (s *Server) cacheClassified(next http.Handler) http.Handler {
	// tacit_demo does not authenticate anybody — it chooses which
	// demonstration dataset answers, so the same URL renders different content
	// with and without it (demo.go, DemoRouter). A request carrying it gets a
	// private answer, and the zone bypasses it, because a private header alone
	// cannot stop an already-cached production page reaching a demo reader.
	//
	// Only where demonstration mode exists. On every other registry the cookie
	// routes nothing, and honoring it would let any visitor turn each
	// cacheable page private by sending a name they made up.
	var routing []string
	if s.cfg().DemoDir != "" {
		routing = webpaths.RoutingCookies
	}
	return cachepolicy.Middleware(cachepolicy.Options{
		SessionCookie:  sessionCookie,
		PrivateCookies: routing,
		Counters:       s.cacheCounters(),
		Observe: func(r *http.Request, rec cachepolicy.Record) {
			if n, ok := r.Context().Value(cacheNoteKey{}).(*cacheNote); ok {
				n.rec = rec
			}
		},
	})(next)
}

// cacheCounters is the process-lifetime classification tally /v1/health reports.
func (s *Server) cacheCounters() *cachepolicy.Counters {
	s.cacheOnce.Do(func() { s.cacheStats = &cachepolicy.Counters{} })
	return s.cacheStats
}

// withBasePath mounts the root-relative mux under Cfg.BasePath (a reverse
// proxy serving the registry at https://host/apps/tacit). Inbound, the prefix
// is stripped so every route stays as registered; outbound, root-relative
// Location headers get the prefix back (sendHTML re-prefixes the markup
// itself). OAuth discovery is the one root-level surface: RFC 8414/9728 insert
// the resource's path into the well-known URI at the ORIGIN root, so those
// paths are recognized pre-strip and mapped onto the internal discovery
// routes — the operator's proxy must forward them alongside the prefix.
func (s *Server) withBasePath(next http.Handler) http.Handler {
	base := s.cfg().BasePath
	if base == "" {
		return next
	}
	stripped := http.StripPrefix(base, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if internal, ok := wellKnownAtRoot(r.URL.Path, base); ok {
			r2 := r.Clone(r.Context())
			r2.URL.Path = internal
			next.ServeHTTP(w, r2)
			return
		}
		if p := r.URL.Path; p == base || p == "/" {
			// The bare prefix (and a stray hit on the proxy root) land on the
			// dashboard home rather than a 404.
			http.Redirect(w, r, base+"/", http.StatusFound)
			return
		}
		if !strings.HasPrefix(r.URL.Path, base+"/") {
			http.NotFound(w, r)
			return
		}
		stripped.ServeHTTP(&baseLocationWriter{ResponseWriter: w, base: base}, r)
	})
}

// wellKnownAtRoot recognizes the two OAuth discovery URIs that RFC 8414/9728
// place at the origin root when the advertised issuer/resource carries a
// path — /.well-known/<name>/apps/tacit[/mcp] — and maps them onto the
// internal root-mounted discovery routes.
func wellKnownAtRoot(p, base string) (string, bool) {
	for _, wk := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server"} {
		if rest, ok := strings.CutPrefix(p, wk); ok {
			if rest == base {
				return wk, true
			}
			if rest == base+"/mcp" {
				return wk + "/mcp", true
			}
		}
	}
	return "", false
}

// baseLocationWriter re-prefixes root-relative redirects. Handlers issue
// Location values against the internal (stripped) root; the browser resolves
// them against the proxy origin, so the prefix must return before the response
// leaves — one place, covering every http.Redirect call site. Values already
// carrying the prefix (a browser-supplied path echoed back) pass unchanged.
type baseLocationWriter struct {
	http.ResponseWriter
	base string
}

func (w *baseLocationWriter) WriteHeader(code int) {
	if l := w.Header().Get("Location"); strings.HasPrefix(l, "/") && !strings.HasPrefix(l, "//") &&
		l != w.base && !strings.HasPrefix(l, w.base+"/") {
		w.Header().Set("Location", w.base+l)
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap keeps the prefix rewriter from breaking the chain; see
// logResponseWriter.Unwrap.
func (w *baseLocationWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// cacheNote is where the classifier leaves its verdict for the access log. The
// log wraps the classifier, so the two cannot see each other's writers; a holder
// on the context is how one line ends up carrying both.
type cacheNote struct{ rec cachepolicy.Record }

type cacheNoteKey struct{}

// logRequests emits one concise line per request — method, path, status, size,
// duration, user-agent, and the cache classification with the reason for it. For
// /mcp it also parses the JSON-RPC envelope so the method (and tools/call name
// or resources/read uri) is visible: that handshake is how you see what an MCP
// Apps host actually fetches to render the app.
//
// The cache fields are the diagnostic the guidance asks for: a route that should
// be cacheable and is not says so here, with the reason, without anyone having to
// open Cloudflare's analytics or guess from a cf-cache-status. Nothing sensitive
// travels with them — a bucket, a reason, and whether a credential was present,
// never its value.
func logRequests(basePath string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		note := &cacheNote{}
		r = r.WithContext(context.WithValue(r.Context(), cacheNoteKey{}, note))
		var rpc string
		if r.URL.Path == basePath+"/mcp" && r.Body != nil {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body)) // rewind for the real handler
			var m struct {
				Method string `json:"method"`
				Params struct {
					Name string `json:"name"`
					URI  string `json:"uri"`
				} `json:"params"`
			}
			if json.Unmarshal(body, &m) == nil && m.Method != "" {
				rpc = " rpc=" + m.Method
				if m.Params.Name != "" {
					rpc += "/" + m.Params.Name
				}
				if m.Params.URI != "" {
					rpc += " uri=" + m.Params.URI
				}
			}
		}
		lw := &logResponseWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(lw, r)
		if selfPoll(r, basePath) {
			return
		}
		log.Printf("[req] %s %s -> %d (%dB %s) ua=%q%s%s",
			r.Method, r.URL.Path, lw.status, lw.size, time.Since(start).Round(time.Millisecond),
			r.UserAgent(), rpc, cacheField(note.rec))
	})
}

// selfPoll reports a liveness check this machine made against itself.
//
// `tacit init` serves in the foreground and, from a goroutine, polls its own
// /v1/health to learn when the registry is up and what address the ingress
// gave it. Every poll was logged, so the product's own waiting appeared in the
// middle of the sentences it was waiting to print — two `GET /v1/health` lines
// cutting the owner's sign-in link in half, on the one screen a new operator
// reads most carefully.
//
// Loopback only, and only this path. A supervisor probing the health endpoint
// from anywhere else is still logged, which is the case an operator actually
// wants to see; nothing off-box can silence itself by choosing a URL.
func selfPoll(r *http.Request, basePath string) bool {
	if r.Method != http.MethodGet || r.URL.Path != basePath+"/v1/health" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// cacheField renders the classification for the access log: the bucket, the
// reason it landed there, and a marker for each thing worth noticing — a
// downgrade, an undeclared route, a validator, a credentialed request.
func cacheField(rec cachepolicy.Record) string {
	if rec.Bucket == "" {
		return "" // nothing classified this response (a hijacked connection)
	}
	out := " cache=" + string(rec.Bucket) + "/" + strings.ReplaceAll(rec.Reason, " ", "_")
	for _, f := range []struct {
		on   bool
		name string
	}{
		{rec.Downgraded, "downgraded"},
		{rec.ETag, "etag"},
		{rec.Streamed, "streamed"},
		{rec.SetCookie, "set-cookie"},
		{rec.Credentialed, "credentialed"},
	} {
		if f.on {
			out += "," + f.name
		}
	}
	return out
}

// logResponseWriter captures the status and byte count for the access log.
type logResponseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *logResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *logResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

// Unwrap is how http.ResponseController reaches the writer underneath. Without
// it every wrapper in the chain is a dead end: a Flush, a Hijack or a deadline
// stops here and is silently dropped, because none of them is an error a
// handler sees. cachepolicy.writer has had one; these two had not, so flush()
// below the classifier reached nothing at all.
func (w *logResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
