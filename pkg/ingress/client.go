// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/pkg/intelligence"
)

// Client is the registry half of the tunnel, run inside `tacit serve` when
// "Access through the OpenTacit proxy" is on. It dials the ingress, keeps a control
// connection up, and opens data connections on request, each of which it serves
// as an ordinary HTTP connection whose requests it forwards to the registry.
//
// Nothing here listens on anything. That is the entire point: a laptop behind
// NAT with no domain, no proxy and no certificate can be reachable from the
// internet because every connection involved was dialled outward.
type Client struct {
	// Addr is where the ingress answers. A URL — https://ingress.tacit.zone —
	// makes the tunnel an HTTP upgrade on the ingress's ordinary port, which is
	// how it reaches a host whose 443 is already owned by a web server. A bare
	// host:port dials the tunnel listener directly, which is the local case.
	Addr string
	// Token is the instance token issued by the ingress.
	Token string
	// Local is the registry this publishes, as a base URL
	// (http://127.0.0.1:8080).
	Local string
	// Version is the registry build, reported at handshake so the console can
	// tell operators running something old.
	Version string
	// TLS dials the ingress over TLS. Off for a local test, on everywhere else.
	TLS bool
	// Test declares this a connection made to exercise the proxy rather than to
	// serve an organization (Hello.Test). It costs the instance nothing except
	// the right to keep its hostname forever: the ingress may reclaim a test
	// name that has gone quiet.
	Test bool
	// TLSSkipVerify is for testing against a self-signed ingress.
	TLSSkipVerify bool
	// Log receives one line per connect and disconnect; nil is silent.
	Log func(format string, args ...any)

	// Stats, when set, is asked for the figures this registry reports to the
	// proxy's operator — see Stats. It is called on connecting and every
	// statsInterval while the tunnel is up, on the control goroutine, so it
	// should be cheap and must not block.
	//
	// Left nil, nothing is reported and the proxy shows no figure for this
	// instance: the registry says what it chooses to say.
	Stats func() Stats
	// Intelligence returns the signed import and outcome snapshot sent beside
	// Stats. nil leaves the field out.
	Intelligence func() *intelligence.Report

	// OnWelcome, when set, is called with the assigned address each time the
	// control connection is established.
	OnWelcome func(Welcome)
	// OnDisconnect fires when a session ends and a reconnect is about to be
	// attempted, so a caller showing the publication's state can say
	// "reconnecting" while it is true rather than after it is over.
	OnDisconnect func(error)

	mu       sync.Mutex
	handler  http.Handler
	dataSrv  *http.Server
	dataFeed chan net.Conn
	dataLn   *feedListener
}

// Run maintains the publication until ctx is cancelled, reconnecting with
// backoff. It returns only when ctx ends or the ingress says something
// permanent about this registry — a rejected token, a closed publication.
// Anything else, including an unwell proxy in front of the ingress, is retried
// for as long as the registry runs: a public address that stays down until a
// person restarts the service is worse than any amount of reconnecting.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var fatal *fatalError
		if errors.As(err, &fatal) {
			return fatal
		}
		// The publication is down NOW, whatever happens on the next attempt.
		// Without this the caller's state keeps its last answer — "connected" —
		// for the whole reconnect, and an operator reads a green line while the
		// address serves nothing.
		if c.OnDisconnect != nil {
			c.OnDisconnect(err)
		}
		c.logf("ingress: %v; retrying in %s", err, backoff.Round(time.Second))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// Retire asks the ingress to forget this registry and free its hostname, and
// returns the name that was released ("" when the ingress had no record of this
// key, which is not an error — see Server.retire).
//
// It needs no tunnel and no running registry: it dials, says one line and
// reads one back. That is what lets `tacit merge` release the address as its
// LAST act, after the registry it belonged to has already been stopped.
//
// The caller should delete the instance key afterwards. Keeping it would leave
// a credential for a name that no longer exists, and the next thing to present
// it would silently enrol as somebody new.
func (c *Client) Retire(ctx context.Context) (string, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	welcome, err := c.handshake(conn, bufio.NewReader(conn), RoleRetire)
	if err != nil {
		return "", err
	}
	return welcome.Instance, nil
}

// fatalError marks a failure that reconnecting cannot fix.
type fatalError struct{ msg string }

func (e *fatalError) Error() string { return e.msg }

// session runs one control connection to completion.
func (c *Client) session(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	br := bufio.NewReader(conn)
	welcome, err := c.handshake(conn, br, RoleControl)
	if err != nil {
		return err
	}
	c.logf(PublishedFormat, welcome.URL)
	if c.OnWelcome != nil {
		c.OnWelcome(welcome)
	}

	if err := c.startDataServer(); err != nil {
		return err
	}
	defer c.stopDataServer()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if welcome.Want > 0 {
		c.openData(ctx, welcome.Want)
	}

	// Report once on connecting, so a freshly published registry shows a figure
	// immediately rather than after the first interval.
	reported := c.reportStats(conn, time.Time{})

	dec := json.NewDecoder(br)
	for {
		// The ingress pings every 20 seconds; a silence far past that means the
		// path is gone even though the socket has not said so.
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		var msg Control
		if err := dec.Decode(&msg); err != nil {
			return fmt.Errorf("control connection ended: %w", err)
		}
		switch msg.Type {
		case MsgWant:
			c.openData(ctx, msg.N)
		case MsgPing:
			_ = writeJSONLine(conn, Control{Type: MsgPong}, 10*time.Second)
			// The ping is a timer this end already trusts, so the report rides
			// it rather than running a ticker of its own — and only ever on a
			// path just proven alive in both directions.
			reported = c.reportStats(conn, reported)
		case MsgBye:
			return &fatalError{msg: "the ingress closed this publication: " + msg.Text}
		}
	}
}

// statsInterval is how often a registry repeats its figures. The number moves
// on the scale of people joining a team, so anything faster would be traffic
// spent on a digit that has not changed.
const statsInterval = 10 * time.Minute

// reportStats sends the registry's figures if it is time to, and returns when
// they were last sent. A registry that supplies no Stats callback sends nothing
// at all — the zero value is silence, not a zero.
func (c *Client) reportStats(conn net.Conn, last time.Time) time.Time {
	if (c.Stats == nil && c.Intelligence == nil) || (!last.IsZero() && time.Since(last) < statsInterval) {
		return last
	}
	msg := Control{Type: MsgStats}
	if c.Stats != nil {
		st := c.Stats()
		msg.Members, msg.MembersDay = st.Week, st.Day
		msg.MembersMonth, msg.MembersYear, msg.Windows = st.Month, st.Year, true
	}
	if c.Intelligence != nil {
		msg.Intelligence = c.Intelligence()
	}
	if err := writeJSONLine(conn, msg, 10*time.Second); err != nil {
		return last // the next ping tries again; a lost figure is not worth a reconnection
	}
	return time.Now()
}

// dial opens one connection to the ingress, ready for the tunnel handshake.
// Over a URL it performs the HTTP upgrade first, so what comes back is the raw
// stream on the far side of a 101.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	target, err := c.target()
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 10 * time.Second}

	var conn net.Conn
	if target.useTLS {
		td := &tls.Dialer{NetDialer: d, Config: &tls.Config{
			ServerName:         target.host,
			InsecureSkipVerify: c.TLSSkipVerify, //nolint:gosec // operator-selected, for a self-signed test ingress
			MinVersion:         tls.VersionTLS12,
		}}
		conn, err = td.DialContext(ctx, "tcp", target.addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", target.addr)
	}
	if err != nil {
		return nil, err
	}
	if !target.upgrade {
		return conn, nil
	}

	key := NewClientKey()
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	if _, err := io.WriteString(conn, upgradeRequest(target.hostHeader, target.path, key)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	if err := readUpgradeResponse(br, key); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if br.Buffered() > 0 {
		// The ingress waits for the client's Hello, so anything already here is
		// not ours to interpret.
		_ = conn.Close()
		return nil, errors.New("the ingress sent data before the tunnel began")
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// NormalizeAddr turns a configured ingress address into one Client.Addr can
// dial. The configured form is usually a bare hostname — `ingress.tacit.zone`,
// which is what DefaultIngress is — and a bare hostname has no port and no
// scheme, so dialling it directly fails with "missing port in address".
//
// This lives here rather than beside the settings page that used to own it
// because it is the CLIENT's rule about its own address, and every caller needs
// it: the registry turning publishing on, and a merge handing the address back
// long after that page is out of the picture. It was the second of those that
// found the omission — released addresses worked against a test ingress, whose
// address is written with a scheme, and failed against the real one.
func NormalizeAddr(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return ""
	case strings.Contains(raw, "://"):
		return strings.TrimRight(raw, "/")
	case IsLoopbackAddr(raw):
		// A loopback ingress is a test one, and test ones are not on TLS.
		return "http://" + raw
	default:
		return "https://" + raw
	}
}

// IsLoopbackAddr reports an ingress running on this machine.
func IsLoopbackAddr(addr string) bool {
	host := addr
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return false
}

// tunnelTarget is where and how to reach an ingress, resolved from Addr.
type tunnelTarget struct {
	addr       string // host:port to dial
	host       string // hostname, for TLS verification
	hostHeader string // Host: header, which carries a non-default port
	path       string
	useTLS     bool
	upgrade    bool
}

func (c *Client) target() (tunnelTarget, error) {
	raw := strings.TrimSpace(c.Addr)
	if raw == "" {
		return tunnelTarget{}, errors.New("no ingress address configured")
	}
	if !strings.Contains(raw, "://") {
		// A bare host:port is the tunnel listener, spoken directly.
		host, _, err := net.SplitHostPort(raw)
		if err != nil {
			host = raw
		}
		return tunnelTarget{addr: raw, host: host, useTLS: c.TLS, upgrade: false}, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return tunnelTarget{}, fmt.Errorf("ingress address %q is not a URL or a host:port", raw)
	}
	t := tunnelTarget{host: u.Hostname(), hostHeader: u.Host, path: TunnelPath, upgrade: true}
	switch u.Scheme {
	case "https":
		t.useTLS = true
		t.addr = u.Host
		if u.Port() == "" {
			t.addr = u.Host + ":443"
		}
	case "http":
		t.addr = u.Host
		if u.Port() == "" {
			t.addr = u.Host + ":80"
		}
	default:
		return tunnelTarget{}, fmt.Errorf("ingress address %q: only http and https are understood", raw)
	}
	return t, nil
}

// handshake sends the opening line and reads the answer.
func (c *Client) handshake(conn net.Conn, br *bufio.Reader, role string) (Welcome, error) {
	hello := Hello{Protocol: ProtocolID, Role: role, Token: c.Token, Version: c.Version, Test: c.Test}
	if err := writeJSONLine(conn, hello, handshakeTimeout); err != nil {
		return Welcome{}, err
	}
	var w Welcome
	if err := readJSONLine(br, conn, &w, handshakeTimeout); err != nil {
		return Welcome{}, fmt.Errorf("no answer from the ingress: %w", err)
	}
	if !w.OK {
		// A refused token or a disabled instance will be refused again.
		return Welcome{}, &fatalError{msg: "the ingress refused this registry: " + w.Error}
	}
	return w, nil
}

// startDataServer stands up the HTTP server that answers on data connections.
// Its listener yields nothing but connections this client dialled — the
// reversal that makes the whole scheme work.
func (c *Client) startDataServer() error {
	target, err := url.Parse(strings.TrimSuffix(c.Local, "/"))
	if err != nil {
		return fmt.Errorf("local registry address %q: %w", c.Local, err)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			host := pr.In.Host // the public hostname, which the registry needs
			pr.SetURL(target)
			pr.Out.Host = host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "the local registry did not answer", http.StatusBadGateway)
		},
	}
	handler := c.handler
	if handler == nil {
		handler = proxy
	}

	feed := make(chan net.Conn)
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	ln := &feedListener{feed: feed, done: make(chan struct{})}

	c.mu.Lock()
	c.dataFeed, c.dataLn, c.dataSrv = feed, ln, srv
	c.mu.Unlock()

	go func() { _ = srv.Serve(ln) }()
	return nil
}

// stopDataServer ends a session's data plane. The feed channel is deliberately
// NOT closed: connections are still being dialled towards it by goroutines that
// outlive this call, and closing it under them would turn a routine reconnect
// into a panic. The listener's own close is what they select on instead — and
// it is idempotent, because http.Server closes the listener too.
func (c *Client) stopDataServer() {
	c.mu.Lock()
	srv, ln := c.dataSrv, c.dataLn
	c.dataSrv, c.dataFeed, c.dataLn = nil, nil, nil
	c.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	if srv != nil {
		_ = srv.Close()
	}
}

// SetHandler serves the tunnel from an in-process handler instead of proxying
// to a local address. Used by the tests, and by anything that embeds a registry.
func (c *Client) SetHandler(h http.Handler) {
	c.mu.Lock()
	c.handler = h
	c.mu.Unlock()
}

// openData dials n more data connections and hands each to the data server.
func (c *Client) openData(ctx context.Context, n int) {
	for range n {
		go func() {
			conn, err := c.dial(ctx)
			if err != nil {
				return
			}
			br := bufio.NewReader(conn)
			if _, err := c.handshake(conn, br, RoleData); err != nil {
				_ = conn.Close()
				return
			}
			// Anything already buffered is the START OF A REQUEST, not a
			// protocol violation. The moment the ingress finishes its side of
			// the handshake the connection is usable, and if a request is
			// waiting on the pool it is written immediately — so under load the
			// first bytes routinely arrive before this goroutine looks again.
			//
			// This used to close the connection here, calling that "the ingress
			// spoke out of turn". The ingress then read EOF on a request it had
			// already sent and answered 502. retryOnce hid most of it; two in a
			// row on the same request did not hide, which is the flake that hit
			// CI. Handing br to the server rather than conn keeps those bytes
			// and removes the case entirely — an empty buffer reads straight
			// through to the socket, so there is nothing special about either
			// path any more.
			dc := bufferedConn{Conn: conn, r: br}
			c.mu.Lock()
			feed, ln := c.dataFeed, c.dataLn
			c.mu.Unlock()
			if feed == nil || ln == nil {
				_ = conn.Close()
				return
			}
			select {
			case feed <- dc:
			case <-ln.done: // this session ended while we were dialling
				_ = conn.Close()
			case <-ctx.Done():
				_ = conn.Close()
			}
		}()
	}
}

// PublishedFormat is the tunnel coming up — the one message on this channel
// that reports success rather than trouble. Named so a caller can single it
// out: `tacit init` announces the public address itself, with the sign-in link
// on it, and does not want the same fact twice in two voices. Everything else
// the client logs is a failure or a retry and must always print.
const PublishedFormat = "published at %s"

func (c *Client) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

// feedListener turns a channel of connections into a net.Listener, so the
// standard HTTP server can serve sockets it did not accept.
// bufferedConn is a data connection that reads through whatever the handshake
// left buffered before it touches the socket. bufio.Reader falls through to the
// underlying connection once its buffer is drained, so one type serves the
// connection that arrived with a request already on it and the one that did
// not.
type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

type feedListener struct {
	feed <-chan net.Conn
	done chan struct{}
	once sync.Once
}

func (l *feedListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.feed:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *feedListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *feedListener) Addr() net.Addr { return tunnelAddr{} }

type tunnelAddr struct{}

func (tunnelAddr) Network() string { return "tacit-ingress" }
func (tunnelAddr) String() string  { return "tunnel" }
