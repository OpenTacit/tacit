// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// Server is the whole ingress: a route table, a tunnel listener, a proxy, and a
// console. One process, no database.
type Server struct {
	Cfg     Config
	Store   *Store
	Intel   *IntelligenceStore
	Metrics *Metrics
	Ops     *OpLog
	OIDC    *oidc.Provider
	// SiteOIDC signs an operator in on the zone's apex, where the project page is
	// served (site.go). It is s.OIDC with the apex's own callback address.
	SiteOIDC *oidc.Provider
	// HostOIDC is a provider per hostname this ingress signs anybody in on —
	// every console host and every apex, which is two per zone once aliases are
	// configured. They differ in one field, the callback address, because the
	// identity provider is told one callback per host. Routing by request host
	// rather than by a fixed field is what lets one process serve several
	// domains without a session on one leaking into another (console.go).
	HostOIDC map[string]*oidc.Provider
	Log      *log.Logger

	mu   sync.RWMutex
	live map[string]*tunnel // by instance name

	// ephemeralSessions records that no session secret was configured, so one
	// was generated for this process and every console session dies with it.
	ephemeralSessions bool

	// certs serves the TLS certificate and reloads it after a renewal; nil on a
	// plain-HTTP ingress.
	certs *certKeeper

	// enrollments counts new enrolments per source address, reset hourly.
	enrollments fixedWindow

	// now is the clock the rate ceilings read. Tests inject one so a window
	// boundary is something they can step over rather than wait for; New leaves
	// it nil, which means time.Now.
	now func() time.Time

	// cacheStats tallies how the console classified its own responses
	// (cachepolicy). Proxied responses are not counted: the registry behind the
	// tunnel classified those, and it has its own tally on /v1/health.
	cacheStats *cachepolicy.Counters

	started time.Time
}

// tunnel is one connected registry: its parked connections, the transport that
// draws on them, and the control connection that asks for more.
type tunnel struct {
	name      string
	instance  Instance
	pool      *connPool
	transport *http.Transport
	proxy     *httputil.ReverseProxy
	connected time.Time
	version   string
	// remote is where the registry connected from, resolved through any front
	// proxy (clientaddr.go) rather than read off the socket. Set once at
	// construction and never written again, so it needs no lock.
	remote string

	mu      sync.Mutex
	control net.Conn
	closed  bool

	// sendMu serializes writes to the control connection, and does nothing else.
	// Two goroutines interleaving JSON lines would corrupt the stream, so the
	// serialization is required; what must not come with it is the lock the
	// request path takes. A write carries a ten-second deadline, and a control
	// socket whose buffer is full holds it for all ten — under one lock that is
	// every request to this instance stalled behind a heartbeat.
	sendMu sync.Mutex

	// rate is a coarse per-minute request counter, reset on the minute. The
	// point is to stop one instance monopolising the ingress, not to meter
	// anyone accurately.
	rate fixedWindow

	// now is the clock rate reads; nil means time.Now. A tunnel built by
	// newTunnel inherits the server's, so a test that pins one pins both.
	now func() time.Time
}

// New builds a server from a configuration, opening its data files.
func New(cfg Config) (*Server, error) {
	cfg = cfg.withDefaults()
	store, err := OpenStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	intelStore, err := OpenIntelligenceStore(cfg.DataDir)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	ops, err := OpenOpLog(cfg.DataDir, cfg.OpLogMaxBytes, cfg.OpLogRetentionDays, cfg.FullClientIP)
	if err != nil {
		return nil, err
	}
	s := &Server{
		Cfg:        cfg,
		Store:      store,
		Intel:      intelStore,
		Metrics:    NewMetrics(),
		Ops:        ops,
		Log:        log.Default(),
		live:       map[string]*tunnel{},
		cacheStats: &cachepolicy.Counters{},
		started:    time.Now(),
	}
	// The log fails quietly on purpose, with one exception: if it has stopped
	// being written at all, the console's whole record goes with it, and that is
	// worth a line in the journal.
	ops.Logf = s.logf

	// The console's figures are a fold over the log, so a restart replays it
	// rather than reading a snapshot someone had to remember to write.
	s.Metrics.RebuildFrom(ops, MetricsWindow)

	// A route table that would not load is the one startup problem worth saying
	// out loud: the ingress carries on, and whoever reconnects may be given a new
	// hostname. The store cannot say it itself — it is opened before there is a
	// logger to say it to.
	if w := store.Warning(); w != "" {
		s.logf("%s", w)
	}

	if cfg.OIDCOn() {
		// A session secret must never be empty. It is the HMAC key that makes a
		// console session unforgeable, and an empty key is a known key: anyone
		// could mint themselves an admin cookie. Unset, generate one per
		// process — sessions then reset on restart, which is a visible
		// inconvenience rather than a silent hole.
		secret := cfg.SessionSecret
		if secret == "" {
			secret = newSessionSecret()
			s.ephemeralSessions = true
		}
		// Two providers, differing in one field. The apex signs people in too,
		// while the project page is unpublished (site.go): same client, same
		// session secret, its own callback address — because the identity
		// provider is told one callback per host, and a browser will only send a
		// session cookie back to the host that set it.
		//
		// Built field by field rather than copied: a Provider carries a mutex
		// around its discovery cache, and copying one is a vet error.
		newProvider := func(redirectURI string) *oidc.Provider {
			return &oidc.Provider{
				Issuer:       cfg.OIDCIssuer,
				ClientID:     cfg.OIDCClientID,
				ClientSecret: cfg.OIDCClientSecret,
				RedirectURI:  redirectURI,
				Scopes:       cfg.OIDCScopes,
				Secret:       []byte(secret),
				TTL:          cfg.SessionTTL,
			}
		}
		s.OIDC = newProvider(cfg.OIDCRedirectURI)
		s.SiteOIDC = newProvider(cfg.SiteRedirectURI())
		// And one per sign-in host once aliases are in play, which the two above
		// are already members of. They share the session secret, so a cookie any
		// of them minted verifies in any of them — but only the host that set it
		// will ever be sent it back, which is the point.
		s.HostOIDC = map[string]*oidc.Provider{}
		for host, uri := range cfg.RedirectURIs() {
			switch uri {
			case cfg.OIDCRedirectURI:
				s.HostOIDC[host] = s.OIDC
			case cfg.SiteRedirectURI():
				s.HostOIDC[host] = s.SiteOIDC
			default:
				s.HostOIDC[host] = newProvider(uri)
			}
		}
	}
	return s, nil
}

// MetricsWindow is how much of the log the console's counters replay at
// startup. A day is what the readouts actually show; replaying more would cost
// startup time to produce figures nothing displays.
const MetricsWindow = 24 * time.Hour

// Close flushes what should survive a restart: the route table, whose handshake
// writes are coalesced and may be owed one, and the log. The counters are
// derived and need no saving.
func (s *Server) Close() {
	_ = s.Store.Close()
	_ = s.Ops.Close()
	s.mu.Lock()
	live := s.live
	s.live = map[string]*tunnel{}
	s.mu.Unlock()
	for _, t := range live {
		t.close()
	}
}

// ServeTunnels accepts registry connections until the listener closes.
func (s *Server) ServeTunnels(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handleTunnelConn(c)
	}
}

// handleTunnelConn runs the opening exchange and then either parks the
// connection or takes it as an instance's control channel. Arriving on the
// tunnel port directly, the socket IS the registry, so it is its own address.
func (s *Server) handleTunnelConn(c net.Conn) {
	s.serveTunnel(c, bufio.NewReader(c), c.RemoteAddr().String())
}

// serveTunnel runs the opening exchange on a connection that is already the
// tunnel's own — whether it arrived raw on the tunnel port or was upgraded out
// of an HTTP request. br carries anything read ahead during the upgrade.
//
// remote is where the registry is, which is not always where the socket is: an
// upgraded connection arrives from whatever front proxy forwarded it, and the
// caller has resolved that to the originating address (clientaddr.go). It is
// what the enrolment ceiling counts and what the console shows, so a proxied
// deployment must not collapse every registry onto the proxy's own address.
func (s *Server) serveTunnel(c net.Conn, br *bufio.Reader, remote string) {
	var hello Hello
	if err := readJSONLine(br, c, &hello, handshakeTimeout); err != nil {
		_ = c.Close()
		return
	}
	if hello.Protocol != ProtocolID {
		_ = writeJSONLine(c, Welcome{Error: "unknown protocol; this port speaks " + ProtocolID}, handshakeTimeout)
		_ = c.Close()
		return
	}
	// Retiring comes before enrolment, and that order is the point: enrolment
	// CREATES, so a retire carrying a key this ingress has never seen would
	// otherwise allocate a hostname in order to delete it.
	if hello.Role == RoleRetire {
		s.retire(c, hello, remote)
		return
	}
	// Enrolment is the handshake. A key the ingress has never seen becomes an
	// instance with a hostname, right here, with nobody asked to approve it —
	// which is what makes publishing self-service. A key it HAS seen resolves
	// to the same hostname it did the first time, for as long as the registry
	// keeps its key.
	in, created, err := s.enroll(remote, hello)
	if err != nil {
		_ = writeJSONLine(c, Welcome{Error: err.Error()}, handshakeTimeout)
		_ = c.Close()
		return
	}
	if created {
		s.logf("enrolled %s (key %s) from %s", in.Name, in.Fingerprint, remote)
	}
	welcome := Welcome{
		OK:       true,
		Instance: in.Name,
		Host:     s.Cfg.HostFor(in.Name),
		URL:      s.Cfg.PublicURL(in.Name),
		Want:     s.Cfg.IdleConns,
	}
	if err := writeJSONLine(c, welcome, handshakeTimeout); err != nil {
		_ = c.Close()
		return
	}

	switch hello.Role {
	case RoleControl:
		s.runControl(c, br, in, hello.Version, remote)
	case RoleData:
		t := s.tunnelFor(in.Name)
		if t == nil {
			// Data before control: the registry is racing its own handshake.
			// Refusing is safe — it will be asked for more once control lands.
			_ = c.Close()
			return
		}
		// The bufio.Reader read only the handshake line; if it buffered
		// anything beyond it the connection is not usable as a clean HTTP
		// socket, and that is a protocol violation rather than a case to
		// handle.
		if br.Buffered() > 0 {
			_ = c.Close()
			return
		}
		t.pool.Put(c)
	default:
		_ = c.Close()
	}
}

// retire deletes the instance a key names, freeing both its hostname and the
// enrolment slot it held. It is the member's own act: whoever holds the key IS
// the instance (identity.go), so no other authority has to be consulted, in the
// same way that nobody approves an enrolment.
//
// The name goes back into circulation immediately, exactly as Store.Delete
// says. That is the cost the caller has to have been told about: the key that
// used to resolve to this address resolves to nothing, and re-enrolling it
// later allocates a fresh name from the vocabulary. There is no way back to the
// old one.
func (s *Server) retire(c net.Conn, hello Hello, remote string) {
	defer func() { _ = c.Close() }()
	in, ok := s.Store.Resolve(hello.Token)
	if !ok {
		// Answering OK for a key that names nothing is deliberate. A merge
		// interrupted between the delete and its acknowledgement has to be
		// safe to run again, and "there is no such instance" is the state the
		// caller asked for rather than a failure to report.
		_ = writeJSONLine(c, Welcome{OK: true}, handshakeTimeout)
		return
	}
	if in.Disabled {
		// The kill switch outranks the member. An instance that could delete
		// itself while suspended could re-enrol under a fresh name a minute
		// later, which would turn suspension into an inconvenience.
		_ = writeJSONLine(c, Welcome{Error: "this instance is suspended; the ingress operator has to release it"}, handshakeTimeout)
		return
	}
	// Bye, not a bare drop: this registry is leaving on purpose, and a client
	// that reconnected would enrol itself all over again under a new name.
	s.Disconnect(in.Name)
	if err := s.Store.Delete(in.Name); err != nil {
		_ = writeJSONLine(c, Welcome{Error: err.Error()}, handshakeTimeout)
		return
	}
	s.logf("retired %s (key %s) from %s; the name is free again", in.Name, in.Fingerprint, remote)
	_ = writeJSONLine(c, Welcome{OK: true, Instance: in.Name}, handshakeTimeout)
}

// enroll resolves a presented key to its instance, registering it the first
// time, and applies the two guards that self-service needs: a ceiling on how
// many registries this ingress will carry, and a limit on how fast one source
// address can create new ones. Neither touches a registry that is already
// enrolled — reconnection after an outage must never be rate-limited.
func (s *Server) enroll(remote string, hello Hello) (Instance, bool, error) {
	if in, ok := s.Store.Resolve(hello.Token); ok {
		if in.Disabled {
			return Instance{}, false, errors.New("this instance is suspended")
		}
		return in, false, nil
	}
	if !s.Cfg.EnrollOpen {
		return Instance{}, false, errors.New("this ingress is not accepting new registries")
	}
	if !s.enrollAllowed(remote) {
		return Instance{}, false, errors.New("too many new registries from this address; try again later")
	}
	in, created, err := s.Store.Enroll(hello.Token, s.Cfg.MaxInstances)
	if err != nil {
		return Instance{}, false, err
	}
	return in, created, nil
}

// enrollAllowed is a per-source-address hourly ceiling on NEW enrolments. It is
// deliberately coarse: the thing it exists to stop is a script claiming a
// thousand hostnames, not a company standing up four registries in an
// afternoon.
//
// The address is the resolved one, not the socket's: behind a front proxy every
// enrolment arrives from loopback, and counting those together would make one
// counter for the whole internet — the ceiling would then be a global one, hit
// by legitimate registries and no obstacle at all to a script.
func (s *Server) enrollAllowed(remote string) bool {
	if s.Cfg.EnrollPerHour <= 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	now := clockOf(s.now)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enrollments.every = time.Hour
	return s.enrollments.hit(now, host) <= s.Cfg.EnrollPerHour
}

// runControl owns an instance's control connection for its lifetime: it creates
// the tunnel, heartbeats it, and tears it down when the connection drops.
func (s *Server) runControl(c net.Conn, br *bufio.Reader, in Instance, version, remote string) {
	t := s.newTunnel(c, in, version, remote)
	s.installTunnel(t)
	t.askForConns(s.Cfg.IdleConns)

	// stop ends this tunnel's background goroutines; it is closed when the
	// control stream below finishes.
	stop := make(chan struct{})
	s.keepAlive(t, stop)

	dec := json.NewDecoder(br)
	for {
		_ = c.SetReadDeadline(time.Now().Add(90 * time.Second))
		var msg Control
		if err := dec.Decode(&msg); err != nil {
			break
		}
		if msg.Type == MsgBye {
			break
		}
		if msg.Type == MsgStats {
			// Recorded on the instance, not in the operations log: this is what
			// the tenant IS, not something that happened.
			s.Store.setStats(in.Name, Stats{Day: msg.MembersDay, Week: msg.Members,
				Month: msg.MembersMonth, Year: msg.MembersYear}, msg.Windows)
			if msg.Intelligence != nil {
				if _, err := s.Intel.Accept(in.Name, *msg.Intelligence); err != nil {
					s.logf("intelligence report from %s rejected: %v", in.Name, err)
				}
			}
		}
	}
	close(stop)

	s.removeTunnel(t)
}

// newTunnel builds one instance's tunnel: the pool of connections the registry
// parks with us, the transport that draws on them, and the proxy that speaks
// HTTP over the result. Nothing here is published yet — installTunnel does that.
func (s *Server) newTunnel(c net.Conn, in Instance, version, remote string) *tunnel {
	t := &tunnel{
		name:      in.Name,
		instance:  in,
		connected: time.Now(),
		version:   version,
		control:   c,
		remote:    remote,
		now:       s.now,
	}
	t.pool = newConnPool(s.Cfg.IdleConns, s.Cfg.ConnMaxIdle, t.askForConns)
	t.transport = &http.Transport{
		// "Dialling" here means taking a connection the registry already opened
		// towards us, and DialWait is the bound on how long that may take. It has
		// to live here rather than on the request: the request's context carries
		// no server-side deadline, so a registry whose control connection is up
		// but which has stopped opening data connections would otherwise hold
		// every request until the member's browser gave up. The bound is per dial
		// attempt, and retryOnce may spend it twice for a request with no body.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if s.Cfg.DialWait > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, s.Cfg.DialWait)
				defer cancel()
			}
			return t.pool.Get(ctx)
		},
		// Every connection here was opened by the registry at our request, so
		// an idle one is capacity we asked for rather than a resource to
		// reclaim. A low ceiling would have the transport close connections the
		// registry then has to dial again — churn, and a wider window in which
		// a request meets a socket that is already going away.
		MaxIdleConnsPerHost:   4 * s.Cfg.IdleConns,
		ResponseHeaderTimeout: s.Cfg.RequestTimeout,
		DisableCompression:    true, // the registry already chose its encoding
	}
	// The URL host is a placeholder: DialContext ignores it entirely and hands
	// back a connection the registry opened. It exists because ReverseProxy
	// needs a target to rewrite towards.
	target, _ := url.Parse("http://tunnel.invalid")
	t.proxy = &httputil.ReverseProxy{
		Transport: retryOnce{t.transport},
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host // the registry needs its own hostname, not ours
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.logf("proxy error for %s%s: %v", in.Name, r.URL.Path, err)
			// Waiting out DialWait is not the same failure as a registry that
			// answered badly: the tunnel is up and the registry is simply out of
			// connections, so the honest answer is "busy, come back" rather than
			// "the thing behind this address is broken".
			if errors.Is(err, context.DeadlineExceeded) {
				w.Header().Set("Retry-After", "5")
				http.Error(w, "this registry has no spare connection right now", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "the registry behind this address did not answer", http.StatusBadGateway)
		},
	}
	return t
}

// installTunnel publishes a tunnel as the instance's live one and records that
// it came up.
//
// One tunnel per instance. A second control connection replaces the first,
// which is what happens when a registry restarts before the old socket has
// timed out.
func (s *Server) installTunnel(t *tunnel) {
	s.mu.Lock()
	if old := s.live[t.name]; old != nil {
		go old.close()
	}
	s.live[t.name] = t
	s.mu.Unlock()

	s.Store.Touch(t.name, t.version, t.remote)
	s.Metrics.RecordConnect(t.name)
	s.Ops.Append(Op{TS: time.Now().UTC(), Kind: KindTunnelUp, Instance: t.name,
		Client: t.remote, Note: t.version})
	s.logf("tunnel up: %s (%s) from %s", t.name, t.version, t.remote)
}

// keepAlive starts the two background clocks a live tunnel runs on, and stops
// both when stop closes.
func (s *Server) keepAlive(t *tunnel, stop <-chan struct{}) {
	// Keep the pool warm. Idle connections are retired on our own clock, before
	// whatever sits in front of us closes them without saying so — see the note
	// on connPool.maxIdle.
	go func() {
		retire := time.NewTicker(s.Cfg.ConnMaxIdle / 3)
		defer retire.Stop()
		for {
			select {
			case <-retire.C:
				t.pool.Retire()
			case <-stop:
				return
			}
		}
	}()

	// Heartbeat: a ping every 20 seconds proves the path is alive in both
	// directions, and a read deadline well past it is what notices a peer that
	// vanished without closing.
	go func() {
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				if err := t.send(Control{Type: MsgPing}); err != nil {
					return
				}
			case <-stop:
				return
			}
		}
	}()
}

// removeTunnel withdraws a tunnel, closes it, and records that it went down.
// The live entry is only cleared if it is still this tunnel: a replacement may
// have taken the name already, and that one is not ours to remove.
func (s *Server) removeTunnel(t *tunnel) {
	s.mu.Lock()
	if s.live[t.name] == t {
		delete(s.live, t.name)
	}
	s.mu.Unlock()
	t.close()
	s.Metrics.RecordDisconnect(t.name)
	s.Ops.Append(Op{TS: time.Now().UTC(), Kind: KindTunnelDown, Instance: t.name,
		MS: time.Since(t.connected).Milliseconds()})
	s.logf("tunnel down: %s", t.name)
}

// retryOnce re-sends a request whose connection died before any answer came
// back.
//
// This is a tunnel, so an idle connection can be minutes old and the registry
// behind it may have restarted, been upgraded, or simply timed the socket out.
// The transport picks a connection, writes the request, and reads EOF. For a
// request with no body that is a connection failure rather than a request
// failure: nothing was processed, so sending it again is safe and is what the
// member would otherwise do by reloading the page.
//
// A request WITH a body is not retried. Its body has already been consumed by
// the first attempt, and replaying a POST the registry might have processed is
// a worse failure than the 502.
type retryOnce struct{ base http.RoundTripper }

func (rt retryOnce) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := rt.base.RoundTrip(r)
	if err == nil || (r.Body != nil && r.Body != http.NoBody) {
		return resp, err
	}
	return rt.base.RoundTrip(r)
}

// askForConns asks the registry to open more data connections. Failures are
// ignored: if the control connection is gone the tunnel is about to be torn
// down anyway, and the request that triggered this will time out with a 503.
func (t *tunnel) askForConns(n int) {
	if n <= 0 {
		return
	}
	_ = t.send(Control{Type: MsgWant, N: n})
}

// send writes one control message. The state is read under t.mu and the write
// happens under sendMu, so the write never holds the lock the request path
// wants. Taking a connection that close may drop a moment later is safe and is
// the point: closing it is what unblocks a write already in flight, and a write
// to a closed connection returns an error, which is what every caller does with
// it anyway.
func (t *tunnel) send(msg Control) error {
	t.mu.Lock()
	c, closed := t.control, t.closed
	t.mu.Unlock()
	if closed || c == nil {
		return errPoolClosed
	}
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	return writeJSONLine(c, msg, 10*time.Second)
}

func (t *tunnel) close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	c := t.control
	t.control = nil
	t.mu.Unlock()

	if c != nil {
		_ = c.Close()
	}
	t.pool.Close()
	t.transport.CloseIdleConnections()
}

// allow reports whether this instance is under its per-minute ceiling.
func (t *tunnel) allow(limit int) bool {
	if limit <= 0 {
		return true
	}
	now := clockOf(t.now)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rate.every = time.Minute
	// One counter for the whole tunnel, so every request shares a key.
	return t.rate.hit(now, "") <= limit
}

// tunnelFor returns the live tunnel for a name, or nil.
func (s *Server) tunnelFor(name string) *tunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.live[name]
}

// LiveNames returns every connected instance.
func (s *Server) LiveNames() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.live))
	for k := range s.live {
		out[k] = true
	}
	return out
}

// Status is what the console shows for one instance's tunnel.
type Status struct {
	Online    bool
	Since     time.Time
	Version   string
	Parked    int
	RemoteStr string
}

// StatusOf reports the live state of one instance.
func (s *Server) StatusOf(name string) Status {
	t := s.tunnelFor(name)
	if t == nil {
		return Status{}
	}
	return Status{Online: true, Since: t.connected, Version: t.version,
		Parked: t.pool.Len(), RemoteStr: t.remote}
}

// DropTunnel closes an instance's tunnel WITHOUT telling it to stay away, so
// the registry reconnects on its own. That is what a rename wants: the address
// changed and the client has to come back to be told the new one. Disconnect is
// the other case — a suspension, where coming back is exactly what should not
// happen.
func (s *Server) DropTunnel(name string) {
	if t := s.tunnelFor(name); t != nil {
		t.close()
	}
}

// Disconnect drops an instance's tunnel and tells the client not to retry — the
// kill switch, applied live.
func (s *Server) Disconnect(name string) {
	if t := s.tunnelFor(name); t != nil {
		_ = t.send(Control{Type: MsgBye, Text: "disabled by the ingress operator"})
		t.close()
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}

// EphemeralSessions reports that the console's session key was generated for
// this process because none was configured.
func (s *Server) EphemeralSessions() bool { return s.ephemeralSessions }
