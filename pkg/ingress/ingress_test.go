// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// harness stands up a real ingress on loopback listeners, a real client, and a
// real registry-shaped handler behind it. Every test below drives traffic
// through an actual tunnel rather than a stub, because the parts most likely to
// be wrong — the connection reversal, the pool, the handshake — are exactly the
// parts a stub would skip.
type harness struct {
	t        *testing.T
	srv      *Server
	public   *httptest.Server
	tunnelLn net.Listener
	client   *Client
	cancel   context.CancelFunc
	// publishedName is the hostname the ingress allocated to the last publish.
	publishedName string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg := Config{
		Zone:           "test.local",
		DataDir:        t.TempDir(),
		Scheme:         "http",
		IdleConns:      4,
		DialWait:       2 * time.Second,
		RequestTimeout: 5 * time.Second,
		RatePerMinute:  0,
		// A zero Config is closed to newcomers on purpose — nothing should
		// start enrolling strangers because a field was left unset. Production
		// opens it in FromEnv; the harness says so explicitly.
		EnrollOpen:         true,
		OpLogMaxBytes:      1 << 20,
		OpLogRetentionDays: 7,
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(srv.Close)
	srv.Log = log.New(newTestWriter(t), "", 0)

	tunnelLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tunnel listener: %v", err)
	}
	t.Cleanup(func() { _ = tunnelLn.Close() })
	go func() { _ = srv.ServeTunnels(tunnelLn) }()

	public := httptest.NewServer(srv.PublicHandler())
	t.Cleanup(public.Close)

	return &harness{t: t, srv: srv, public: public, tunnelLn: tunnelLn}
}

// publish connects a registry that enrols itself, exactly as a real one does:
// it generates a key, presents it, and is told its hostname. The test never
// picks a name, because nothing can.
func (h *harness) publish(handler http.Handler) Instance {
	h.t.Helper()
	c := &Client{
		Addr:    h.tunnelLn.Addr().String(),
		Token:   NewKey(),
		Version: "test-build",
	}
	c.SetHandler(handler)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.client = cancel, c
	h.t.Cleanup(cancel)

	// The name arrives on the channel rather than in a shared variable: the
	// callback fires again on every reconnection, and a plain assignment would
	// race with the read below.
	ready := make(chan string, 1)
	var once sync.Once
	c.OnWelcome = func(w Welcome) { once.Do(func() { ready <- w.Instance }) }
	go func() { _ = c.Run(ctx) }()

	var assigned string
	select {
	case assigned = <-ready:
	case <-time.After(5 * time.Second):
		h.t.Fatal("client never connected")
	}
	h.publishedName = assigned
	h.waitOnline(assigned)
	in, ok := h.srv.Store.Get(assigned)
	if !ok {
		h.t.Fatalf("the ingress reported %q but has no such instance", assigned)
	}
	return in
}

func (h *harness) waitOnline(name string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := h.srv.StatusOf(name); st.Online && st.Parked > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("instance %s never reported a parked connection", name)
}

// get issues a request to the public listener as if it arrived for name's
// hostname. Host routing is the only routing there is, so the test sets Host
// rather than relying on DNS.
func (h *harness) get(name, path string) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequest("GET", h.public.URL+path, nil)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	req.Host = name + ".test.local"
	resp, err := h.public.Client().Do(req)
	if err != nil {
		h.t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestRequestReachesTheRegistryThroughTheTunnel(t *testing.T) {
	h := newHarness(t)
	h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "served %s for host %s", r.URL.Path, r.Host)
	}))

	in := h.publishedName
	resp, body := h.get(in, "/v1/health")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "served /v1/health") {
		t.Errorf("body = %q, want the registry's answer", body)
	}
	// The registry must see its own public hostname, not the ingress's: sign-in
	// redirects and join links are built from it.
	if !strings.Contains(body, in+".test.local") {
		t.Errorf("body = %q, want the public host forwarded to the registry", body)
	}
}

func TestPostBodyAndStatusSurvive(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "got %d bytes: %s", len(body), body)
	}))

	req, _ := http.NewRequest("POST", h.public.URL+"/v1/feedback", strings.NewReader(`{"stage":"helped"}`))
	req.Host = in.Name + ".test.local"
	resp, err := h.public.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if !strings.Contains(string(body), `{"stage":"helped"}`) {
		t.Errorf("body = %q, want the posted payload to have arrived intact", body)
	}
}

func TestManyRequestsReuseAndReplaceConnections(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	// More requests than there are parked connections, run concurrently: the
	// pool has to ask the registry for more and the client has to supply them.
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, body := h.get(in.Name, "/v1/techniques")
			if resp.StatusCode != 200 || body != "ok" {
				errs <- fmt.Errorf("status %d body %q", resp.StatusCode, body)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("request failed: %v", err)
	}

	m := h.srv.Metrics.For(in.Name)
	if m.Requests < 40 {
		t.Errorf("recorded %d requests, want at least 40", m.Requests)
	}
}

func TestOfflineInstanceAnswers502AndIsRecorded(t *testing.T) {
	h := newHarness(t)
	in, _, err := h.srv.Store.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}

	resp, _ := h.get(in.Name, "/v1/health")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for an instance with no tunnel", resp.StatusCode)
	}
	ops := h.srv.Ops.Recent(OpFilter{Instance: in.Name, Limit: 5})
	if len(ops) != 1 || ops[0].Note != "no tunnel" {
		t.Fatalf("operations log = %+v, want one entry noting the missing tunnel", ops)
	}
	if got := h.srv.Metrics.For(in.Name).ByClass["5xx"]; got != 1 {
		t.Errorf("5xx count = %d, want 1", got)
	}
}

// TestDialWaitBoundsTheWaitForAConnection drives the case DialWait exists for: a
// registry whose control connection is up and healthy but which never opens a
// data connection. Nothing in the request path notices that on its own — the
// tunnel is online, the path is allowed, the rate limit is clear — so without a
// bound on the dial the request waits for as long as the member's browser will.
func TestDialWaitBoundsTheWaitForAConnection(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.DialWait = 200 * time.Millisecond

	// A registry that handshakes and then reads its control connection without
	// ever acting on it: it is enrolled, it is online, and it has no capacity.
	c, err := net.Dial("tcp", h.tunnelLn.Addr().String())
	if err != nil {
		t.Fatalf("dial the tunnel listener: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	hello := Hello{Protocol: ProtocolID, Role: RoleControl, Token: NewKey(), Version: "never-answers"}
	if err := writeJSONLine(c, hello, handshakeTimeout); err != nil {
		t.Fatalf("hello: %v", err)
	}
	br := bufio.NewReader(c)
	var welcome Welcome
	if err := readJSONLine(br, c, &welcome, 5*time.Second); err != nil {
		t.Fatalf("welcome: %v", err)
	}
	if !welcome.OK {
		t.Fatalf("welcome refused: %s", welcome.Error)
	}
	// Drain the control stream so the ingress's requests for connections are read
	// and dropped rather than filling the socket, which is a different failure.
	go func() { _, _ = io.Copy(io.Discard, br) }()

	deadline := time.Now().Add(5 * time.Second)
	for h.srv.StatusOf(welcome.Instance).Online == false {
		if time.Now().After(deadline) {
			t.Fatal("the tunnel never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}

	req, err := http.NewRequest("GET", h.public.URL+"/v1/health", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = welcome.Instance + ".test.local"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := h.public.Client().Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("the request never came back: %v — DialWait must bound the wait for a connection", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 5 {
		t.Errorf("status = %d, want a 5xx: there is no connection to serve this on", resp.StatusCode)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("the request took %v, want it bounded by DialWait", took)
	}
}

func TestUnknownHostAndSuspendedInstance(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	if resp, _ := h.get("nobody-here-9999", "/v1/health"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown instance status = %d, want 404", resp.StatusCode)
	}

	if err := h.srv.Store.SetDisabled(in.Name, true); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if resp, _ := h.get(in.Name, "/v1/health"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("suspended instance status = %d, want 403", resp.StatusCode)
	}
}

func TestPathsOutsideTheRegistrySurfaceAreRefused(t *testing.T) {
	h := newHarness(t)
	var reached bool
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))

	resp, _ := h.get(in.Name, "/wp-admin/setup-config.php")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a path no registry serves", resp.StatusCode)
	}
	if reached {
		t.Error("the request reached the registry; the allowlist is meant to stop it at the ingress")
	}
}

// TestAControlWriteDoesNotStallTheRequestPath pins the reason the control socket
// has a lock of its own. A net.Pipe hands nothing over until the far side reads,
// so a peer that never reads is a control connection wedged for the whole write
// deadline — which is what a full TCP buffer is. The request path takes the
// tunnel's own lock on every proxied request, and it must not queue behind that.
func TestAControlWriteDoesNotStallTheRequestPath(t *testing.T) {
	peer, control := net.Pipe()
	t.Cleanup(func() { _ = peer.Close(); _ = control.Close() })
	tn := &tunnel{name: "wedged", control: control}

	writing := make(chan struct{})
	go func() {
		close(writing)
		_ = tn.send(Control{Type: MsgPing})
	}()
	<-writing
	time.Sleep(20 * time.Millisecond) // long enough for the write to be in flight

	allowed := make(chan bool, 1)
	go func() { allowed <- tn.allow(1000) }()
	select {
	case <-allowed:
	case <-time.After(2 * time.Second):
		t.Fatal("the rate check blocked behind a control write; the request path shares its lock")
	}
}

func TestEnrolmentIsSelfServiceAndStable(t *testing.T) {
	h := newHarness(t)
	key := NewKey()

	in, created, err := h.srv.Store.Enroll(key, 0)
	if err != nil || !created {
		t.Fatalf("Enroll = %+v, created=%v, err=%v; want a new instance", in, created, err)
	}
	if in.Name == "" || strings.Contains(in.Name, " ") {
		t.Errorf("allocated name %q is not a hostname label", in.Name)
	}
	if in.KeyHash == key || strings.Contains(in.KeyHash, key) {
		t.Error("the store kept the key itself; only its hash should survive")
	}

	// The same key must come back to the same name, or every restart would
	// move the registry's public address and break every OAuth client.
	again, created, err := h.srv.Store.Enroll(key, 0)
	if err != nil || created || again.Name != in.Name {
		t.Errorf("second Enroll = %q created=%v err=%v; want the same name and no creation", again.Name, created, err)
	}

	// A different key is a different registry.
	other, created, err := h.srv.Store.Enroll(NewKey(), 0)
	if err != nil || !created || other.Name == in.Name {
		t.Errorf("a second key got %q (created=%v, err=%v); want a distinct new name", other.Name, created, err)
	}

	if _, ok := h.srv.Store.Resolve(NewKey()); ok {
		t.Error("an unseen key resolved to an instance")
	}
}

func TestEnrolmentCeilingAndClosedIngress(t *testing.T) {
	h := newHarness(t)
	if _, _, err := h.srv.Store.Enroll(NewKey(), 1); err != nil {
		t.Fatalf("first enrolment: %v", err)
	}
	if _, _, err := h.srv.Store.Enroll(NewKey(), 1); !errors.Is(err, ErrFull) {
		t.Errorf("second enrolment past the ceiling = %v, want ErrFull", err)
	}

	// With enrolment closed, an unknown key is refused outright — but a
	// registry already enrolled must still be able to reconnect.
	known := NewKey()
	h.srv.Cfg.MaxInstances = 0
	in, _, err := h.srv.Store.Enroll(known, 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}
	h.srv.Cfg.EnrollOpen = false

	c := &Client{Addr: h.tunnelLn.Addr().String(), Token: NewKey()}
	c.SetHandler(http.NotFoundHandler())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Run(ctx); err == nil || !strings.Contains(err.Error(), "not accepting") {
		t.Errorf("a new registry against a closed ingress: %v; want a refusal", err)
	}

	known2 := &Client{Addr: h.tunnelLn.Addr().String(), Token: known}
	known2.SetHandler(http.NotFoundHandler())
	ready := make(chan string, 1)
	known2.OnWelcome = func(w Welcome) { ready <- w.Instance }
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = known2.Run(ctx2) }()
	select {
	case got := <-ready:
		if got != in.Name {
			t.Errorf("reconnected as %q, want %q", got, in.Name)
		}
	case <-time.After(5 * time.Second):
		t.Error("an already-enrolled registry could not reconnect to a closed ingress")
	}
}

func TestSuspendedInstanceCannotReconnect(t *testing.T) {
	h := newHarness(t)
	key := NewKey()
	in, _, err := h.srv.Store.Enroll(key, 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}
	if err := h.srv.Store.SetDisabled(in.Name, true); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	c := &Client{Addr: h.tunnelLn.Addr().String(), Token: key}
	c.SetHandler(http.NotFoundHandler())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Run(ctx); err == nil || !strings.Contains(err.Error(), "suspended") {
		t.Errorf("a suspended instance connected: %v", err)
	}
}

func TestSuspendDropsALiveTunnel(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	h.srv.Disconnect(in.Name)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !h.srv.StatusOf(in.Name).Online {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the tunnel is still online after Disconnect")
}

func TestOperationsLogRecordsTheEnvelopeAndNotTheContent(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "a secret answer")
	}))

	req, _ := http.NewRequest("GET", h.public.URL+"/v1/techniques?q=confidential-project", nil)
	req.Host = in.Name + ".test.local"
	req.Header.Set("User-Agent", "claude-code/1.2.3 (macOS)")
	resp, err := h.public.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()

	ops := h.srv.Ops.Recent(OpFilter{Instance: in.Name, Limit: 5})
	if len(ops) == 0 {
		t.Fatal("nothing recorded")
	}
	op := ops[0]
	if op.Path != "/v1/techniques" {
		t.Errorf("path = %q, want the path without its query string", op.Path)
	}
	if strings.Contains(op.Path, "confidential-project") {
		t.Error("the query string was retained; the log is meant to hold the envelope only")
	}
	if op.Bytes == 0 || op.Status != 200 {
		t.Errorf("op = %+v, want the size and status of what was served", op)
	}
	if !strings.HasSuffix(op.Client, "/24") && !strings.HasSuffix(op.Client, "/64") {
		t.Errorf("client = %q, want a truncated network", op.Client)
	}
}

func TestReconnectAfterTheIngressDropsIt(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))

	// An ingress restart drops the socket without a word. (The kill switch is
	// different: it sends a "bye", and a suspended registry is meant to stay
	// down rather than hammer the ingress — TestSuspendDropsALiveTunnel covers
	// that path.)
	h.srv.tunnelFor(in.Name).close()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := h.srv.StatusOf(in.Name); st.Online && st.Parked > 0 {
			resp, body := h.get(in.Name, "/v1/health")
			if resp.StatusCode == 200 && body == "ok" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the client never re-established the tunnel")
}

// testWriter routes the server's log into the test's output, so a failure
// carries the reason with it instead of a bare status code. Tunnel goroutines
// outlive the test that started them, and logging from one after it finishes is
// a panic, so the writer goes quiet at cleanup.
type testWriter struct {
	t    *testing.T
	mu   sync.Mutex
	done bool
}

func newTestWriter(t *testing.T) *testWriter {
	w := &testWriter{t: t}
	t.Cleanup(func() {
		w.mu.Lock()
		w.done = true
		w.mu.Unlock()
	})
	return w
}

func (w *testWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.done {
		w.t.Logf("ingress: %s", strings.TrimRight(string(b), "\n"))
	}
	return len(b), nil
}

// The console's figures are a fold over the operations log, so they have to
// survive a restart without anything else being written to disk — and they have
// to include tunnel churn, which is the one thing the retired metrics file held
// that no request record carries.
func TestMetricsRebuildFromTheLogAlone(t *testing.T) {
	dir := t.TempDir()
	log1, err := OpenOpLog(dir, 1<<20, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	log1.Append(Op{TS: now.Add(-30 * time.Minute), Instance: "cedar-hollow", Status: 200, Bytes: 100, MS: 4})
	log1.Append(Op{TS: now.Add(-20 * time.Minute), Instance: "cedar-hollow", Status: 500, Bytes: 20, MS: 900})
	log1.Append(Op{TS: now.Add(-25 * time.Minute), Kind: KindTunnelUp, Instance: "cedar-hollow"})
	log1.Append(Op{TS: now.Add(-10 * time.Minute), Kind: KindTunnelDown, Instance: "cedar-hollow"})
	if err := log1.Close(); err != nil {
		t.Fatal(err)
	}

	// A second process, with nothing but the log to go on.
	log2, err := OpenOpLog(dir, 1<<20, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log2.Close() }()
	m := NewMetrics()
	m.RebuildFrom(log2, 24*time.Hour)

	got := m.For("cedar-hollow")
	if got.Requests != 2 {
		t.Errorf("requests = %d, want 2", got.Requests)
	}
	if got.ByClass["5xx"] != 1 || got.ByClass["2xx"] != 1 {
		t.Errorf("status mix = %v, want one 2xx and one 5xx", got.ByClass)
	}
	if got.BytesOut != 120 {
		t.Errorf("bytes = %d, want 120", got.BytesOut)
	}
	if got.Connects != 1 || got.Disconnects != 1 {
		t.Errorf("tunnel churn = %d up / %d down, want 1 and 1 — the log is now the only place this lives",
			got.Connects, got.Disconnects)
	}
	if got.LatencyMax != 900 {
		t.Errorf("max latency = %d, want 900", got.LatencyMax)
	}
	// The rebuild must place each request in the hour it happened, not the hour
	// of the replay, or a restart would pile a day's traffic into one bar.
	day := got.Day()
	var total int64
	for _, h := range day {
		total += h.Requests
	}
	if total != 2 {
		t.Errorf("the rolling day holds %d requests, want 2", total)
	}
	if m.Covers().IsZero() {
		t.Error("the rebuild did not record what window its figures cover")
	}
}

// A read of the log is bounded by the page asked for, not by how much matches.
// The pages have to tile the match set exactly: every record once, newest first,
// none skipped at a boundary and none repeated across one.
func TestOpLogPagesTileTheMatchesWithoutGapsOrRepeats(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenOpLog(dir, 1<<20, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	now := time.Now().UTC()
	const n = 47
	for i := 0; i < n; i++ {
		// Interleave another instance so paging is exercised against a filter
		// rather than against the whole file.
		l.Append(Op{TS: now.Add(time.Duration(i) * time.Second), Instance: "other", Status: 200})
		l.Append(Op{TS: now.Add(time.Duration(i) * time.Second), Instance: "cedar-hollow",
			Status: 200, Path: fmt.Sprintf("/p/%d", i)})
	}

	const size = 10
	seen, wantPages := []string{}, 5 // 47 matches over pages of 10
	for page := 1; ; page++ {
		res := l.Page(OpFilter{Instance: "cedar-hollow", Limit: size, Offset: (page - 1) * size})
		if res.Total != n {
			t.Fatalf("page %d reports %d matches in total, want %d", page, res.Total, n)
		}
		if len(res.Ops) == 0 {
			if page != wantPages+1 {
				t.Fatalf("ran out of rows on page %d, expected %d pages of %d", page, wantPages, size)
			}
			break
		}
		if len(res.Ops) > size {
			t.Fatalf("page %d returned %d rows, over the %d asked for", page, len(res.Ops), size)
		}
		for _, op := range res.Ops {
			seen = append(seen, op.Path)
		}
	}
	if len(seen) != n {
		t.Fatalf("the pages hold %d records between them, want the %d that matched", len(seen), n)
	}
	// Newest first, across page boundaries as well as within a page.
	for i, path := range seen {
		if want := fmt.Sprintf("/p/%d", n-1-i); path != want {
			t.Fatalf("record %d is %s, want %s — the pages do not tile in newest-first order", i, path, want)
		}
	}
	// Recent is the first page and stays so.
	first := l.Recent(OpFilter{Instance: "cedar-hollow", Limit: size})
	if len(first) != size || first[0].Path != "/p/46" {
		t.Fatalf("Recent returned %d rows starting at %q, want %d starting at the newest",
			len(first), first[0].Path, size)
	}
}

// Retention has to be retention. Rotation alone kept a busy log for days while
// the console claimed months, and a quiet one forever.
func TestPruneDropsExpiredRecordsAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenOpLog(dir, 1<<20, 1, false) // one-day window
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	now := time.Now().UTC()
	l.Append(Op{TS: now.Add(-72 * time.Hour), Instance: "old-one", Status: 200})
	l.Append(Op{TS: now.Add(-48 * time.Hour), Instance: "old-two", Status: 200})
	l.Append(Op{TS: now.Add(-1 * time.Hour), Instance: "fresh", Status: 200})

	dropped, err := l.Prune()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if dropped != 2 {
		t.Errorf("dropped %d records, want the 2 past the window", dropped)
	}
	kept := l.Recent(OpFilter{Limit: 10})
	if len(kept) != 1 || kept[0].Instance != "fresh" {
		t.Fatalf("kept %+v, want only the record inside the window", kept)
	}

	// The log stays writable after a prune — the file was replaced underneath it.
	l.Append(Op{TS: now, Instance: "after-prune", Status: 204})
	if got := l.Recent(OpFilter{Limit: 10}); len(got) != 2 {
		t.Errorf("after pruning and appending the log holds %d records, want 2", len(got))
	}
}

// A rotate or a prune that fails after closing the live file leaves nothing to
// write to. Appending is silent by design, so the log then stops recording
// anything and nobody finds out until somebody goes looking for a request that
// was served weeks ago.
func TestTheOperationsLogComesBackAfterALostFileHandle(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenOpLog(dir, 1<<20, 7, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	now := time.Now().UTC()
	l.Append(Op{TS: now, Instance: "cedar-hollow", Path: "/v1/health", Status: 200})

	// Exactly what those two failure paths leave behind.
	l.mu.Lock()
	_ = l.f.Close()
	l.f = nil
	l.mu.Unlock()

	l.Append(Op{TS: now, Instance: "cedar-hollow", Path: "/v1/techniques", Status: 200})

	var paths []string
	l.Scan(time.Time{}, func(op Op) { paths = append(paths, op.Path) })
	if len(paths) != 2 || paths[1] != "/v1/techniques" {
		t.Fatalf("the log holds %v, want both records — an append after a lost handle "+
			"must reopen the file rather than fail in silence", paths)
	}
}

func TestALogThatCannotBeWrittenSaysSoOnce(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only file anyway")
	}
	dir := t.TempDir()
	l, err := OpenOpLog(dir, 1<<20, 7, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	var said []string
	l.Logf = func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }

	l.mu.Lock()
	_ = l.f.Close()
	l.f = nil
	l.mu.Unlock()
	if err := os.Chmod(filepath.Join(dir, "ops.jsonl"), 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "ops.jsonl"), 0o600) })

	for range 5 {
		l.Append(Op{TS: time.Now().UTC(), Instance: "cedar-hollow", Status: 200})
	}
	if len(said) != 1 {
		t.Fatalf("said %d things about a log it cannot write, want one line and not one per request: %v",
			len(said), said)
	}
}

// selfSigned writes a certificate/key pair for the given names, so the TLS
// tests exercise the real handshake path rather than a stub.
func selfSigned(t *testing.T, dir, serial string, names ...string) (certPath, keyPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: serial},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     names,
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	writePEM(t, certPath, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	writePEM(t, keyPath, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPath, keyPath
}

func writePEM(t *testing.T, path string, blk *pem.Block) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := pem.Encode(f, blk); err != nil {
		t.Fatal(err)
	}
}

// The certificate has to be re-read when it changes on disk. A renewal rewrites
// the same two files every couple of months, and an ingress that only read them
// at startup would serve an expired certificate until someone restarted it —
// dropping every tunnel in the process, which is the thing a reload avoids.
func TestCertificateReloadsAfterRenewalWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := selfSigned(t, dir, "first", "a.test.local")

	keeper, err := newCertKeeper(certPath, keyPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := keeper.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cn := got.Leaf.Subject.CommonName; cn != "first" {
		t.Fatalf("serving %q, want the first certificate", cn)
	}

	// Renewal: same paths, new contents, and an mtime the keeper will notice.
	certPath2, keyPath2 := selfSigned(t, t.TempDir(), "renewed", "a.test.local")
	for _, m := range [][2]string{{certPath2, certPath}, {keyPath2, keyPath}} {
		b, err := os.ReadFile(m[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(m[1], b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatal(err)
	}
	// Force the stat: the keeper deliberately does not touch the filesystem on
	// every handshake.
	keeper.mu.Lock()
	keeper.lastStat = time.Now().Add(-2 * statInterval)
	keeper.mu.Unlock()

	got, err = keeper.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cn := got.Leaf.Subject.CommonName; cn != "renewed" {
		t.Errorf("serving %q after renewal, want the renewed certificate", cn)
	}
}

// A broken or half-written certificate must not take the ingress down: during a
// renewal the files are briefly inconsistent, and refusing connections for that
// window would be a worse outage than the one it avoids.
func TestBadCertificateOnDiskKeepsServingTheOldOne(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := selfSigned(t, dir, "good", "a.test.local")
	keeper, err := newCertKeeper(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, []byte("-----BEGIN CERTIFICATE-----\nhalf written"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(certPath, future, future)
	keeper.mu.Lock()
	keeper.lastStat = time.Now().Add(-2 * statInterval)
	keeper.mu.Unlock()

	got, err := keeper.GetCertificate(nil)
	if err != nil {
		t.Fatalf("a corrupt file on disk stopped the ingress serving: %v", err)
	}
	if cn := got.Leaf.Subject.CommonName; cn != "good" {
		t.Errorf("serving %q, want the last good certificate", cn)
	}
}

// End to end over TLS: a published registry answers on https, through the same
// tunnel, with the certificate the ingress was configured with.
func TestServesPublishedInstanceOverTLS(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	certPath, keyPath := selfSigned(t, dir, "ingress", "*.test.local", "test.local")
	h.srv.Cfg.TLSCert, h.srv.Cfg.TLSKey = certPath, keyPath

	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "served over tls to "+r.Host)
	}))

	tlsCfg, err := h.srv.TLSConfig()
	if err != nil {
		t.Fatalf("TLSConfig: %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("no TLS config built from a configured certificate")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h.srv.PublicHandler(), TLSConfig: tlsCfg}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })

	pool := x509.NewCertPool()
	pem, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	pool.AppendCertsFromPEM(pem)
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: in.Name + ".test.local", MinVersion: tls.VersionTLS12},
	}}
	req, _ := http.NewRequest("GET", "https://"+ln.Addr().String()+"/v1/health", nil)
	req.Host = in.Name + ".test.local"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("https request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), in.Name+".test.local") {
		t.Errorf("status %d body %q; want the registry's answer over TLS", resp.StatusCode, body)
	}
}

// With a certificate configured, published addresses must say https — a
// registry handed an http:// URL for an ingress serving TLS would advertise an
// address that does not work, and its OIDC callback would be rejected.
func TestSchemeFollowsTheCertificate(t *testing.T) {
	plain := Config{Zone: "tacit.zone"}.WithDefaults()
	if plain.Scheme != "http" || strings.HasPrefix(plain.PublicURL("cedar-hollow"), "https") {
		t.Errorf("without a certificate the scheme is %q", plain.Scheme)
	}
	secure := Config{Zone: "tacit.zone", TLSCert: "c.pem", TLSKey: "k.pem", PublicAddr: ":443"}.WithDefaults()
	if secure.Scheme != "https" {
		t.Errorf("with a certificate the scheme is %q, want https", secure.Scheme)
	}
	if got := secure.PublicURL("cedar-hollow"); got != "https://cedar-hollow.tacit.zone" {
		t.Errorf("published URL = %q; want no port on the scheme's default", got)
	}
	// An explicit setting still wins, for an ingress behind someone else's TLS.
	fronted := Config{Zone: "tacit.zone", Scheme: "https"}.WithDefaults()
	if fronted.Scheme != "https" {
		t.Errorf("explicit scheme was overridden: %q", fronted.Scheme)
	}
}

// The console's session key must never be empty. It is the HMAC key that makes
// a session cookie unforgeable, so an empty one is a known one — anyone could
// mint themselves an admin session on a console that is, by design, reachable
// from the internet.
func TestSessionSecretIsNeverEmpty(t *testing.T) {
	cfg := Config{
		Zone: "test.local", DataDir: t.TempDir(),
		OIDCIssuer: "https://accounts.google.com", OIDCClientID: "console",
		// SessionSecret deliberately unset.
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if srv.OIDC == nil {
		t.Fatal("OIDC configured but no provider built")
	}
	if len(srv.OIDC.Secret) == 0 {
		t.Fatal("the provider signs sessions with an empty key; any cookie could be forged")
	}
	if !srv.EphemeralSessions() {
		t.Error("a generated secret is not reported as ephemeral, so nobody is told sessions die on restart")
	}

	// A cookie minted by one process must not verify in another, or the
	// generated key is not actually random.
	other, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tok := srv.OIDC.CreateSession(map[string]any{"email": "ops@example.com"})
	if claims := other.OIDC.VerifySession(tok); claims != nil {
		t.Error("a session from one process verified in another; the key is not random")
	}

	// A configured secret is used as given, and is not flagged ephemeral.
	cfg.SessionSecret = "a-configured-secret"
	fixed, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fixed.Close()
	if string(fixed.OIDC.Secret) != "a-configured-secret" || fixed.EphemeralSessions() {
		t.Error("a configured session secret was not honoured")
	}
}

// Choosing a hostname is an operator privilege, not something enrolment can ask
// for. A stranger publishing a laptop gets what the vocabulary gives them; the
// person running the ingress can name an instance whatever they like, from a
// console that already sits behind an identity provider.
func TestOperatorCanRenameButEnrolmentCannotAsk(t *testing.T) {
	h := newHarness(t)
	key := NewKey()
	in, _, err := h.srv.Store.Enroll(key, 0)
	if err != nil {
		t.Fatal(err)
	}
	generated := in.Name

	if err := h.srv.Store.Rename(generated, "quiet-harbor"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, ok := h.srv.Store.Get(generated); ok {
		t.Error("the old name still resolves after a rename")
	}
	renamed, ok := h.srv.Store.Get("quiet-harbor")
	if !ok {
		t.Fatal("the new name does not resolve")
	}
	// The identity is untouched: same registry, new address.
	if renamed.KeyHash != in.KeyHash {
		t.Error("the rename changed the instance's identity")
	}
	// And the key still resolves to it, so the registry reconnects as itself.
	back, ok := h.srv.Store.Resolve(key)
	if !ok || back.Name != "quiet-harbor" {
		t.Errorf("the key resolves to %+v; want the renamed instance", back)
	}

	// The guards: a taken name, a reserved one, and one that is not a hostname.
	other, _, _ := h.srv.Store.Enroll(NewKey(), 0)
	if err := h.srv.Store.Rename(other.Name, "quiet-harbor"); !errors.Is(err, ErrTaken) {
		t.Errorf("renaming onto a taken name = %v, want ErrTaken", err)
	}
	if err := h.srv.Store.Rename(other.Name, "ingress"); !errors.Is(err, ErrReserved) {
		t.Errorf("renaming to the console's own label = %v, want ErrReserved", err)
	}
	// Case is normalised rather than rejected: hostnames are case-insensitive,
	// so "Quiet-Harbor" is a spelling of the taken name, not a fresh one.
	if err := h.srv.Store.Rename(other.Name, "Quiet-Harbor"); !errors.Is(err, ErrTaken) {
		t.Errorf("renaming onto a mixed-case spelling of a taken name = %v, want ErrTaken", err)
	}
	for _, bad := range []string{"has space", "-leading", "a", "x!y", ""} {
		if err := h.srv.Store.Rename(other.Name, bad); !errors.Is(err, ErrBadName) {
			t.Errorf("rename to %q = %v, want ErrBadName", bad, err)
		}
	}
	// A failed rename must not have moved anything.
	if _, ok := h.srv.Store.Get(other.Name); !ok {
		t.Error("a rejected rename lost the instance")
	}
}

// After a rename the registry has to learn its new address, which it does by
// reconnecting — so the tunnel under the old name must not survive.
func TestRenameDropsTheTunnelSoTheRegistryLearnsTheNewName(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	if !h.srv.StatusOf(in.Name).Online {
		t.Fatal("not online to begin with")
	}

	if err := h.srv.Store.Rename(in.Name, "quiet-harbor"); err != nil {
		t.Fatal(err)
	}
	h.srv.DropTunnel(in.Name)

	// The client reconnects on its own and enrols under the name its key now
	// maps to.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := h.srv.StatusOf("quiet-harbor"); st.Online && st.Parked > 0 {
			resp, body := h.get("quiet-harbor", "/v1/health")
			if resp.StatusCode == 200 && body == "ok" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the registry never came back under its new name")
}

// A registry has to be able to reach an ingress whose host already runs a web
// server on 443. The tunnel therefore arrives as an HTTP upgrade on the
// ordinary port and becomes a raw stream after the 101 — no second port to open
// through a firewall, and everything in the path sees a request it understands.
func TestTunnelArrivesAsAnHTTPUpgrade(t *testing.T) {
	h := newHarness(t)

	// No raw tunnel listener in play: the client is given the public HTTP
	// address, exactly as it would be given https://ingress.tacit.zone.
	c := &Client{Addr: h.public.URL, Token: NewKey(), Version: "test-build"}
	c.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "reached through the upgrade")
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	var once sync.Once
	c.OnWelcome = func(w Welcome) { once.Do(func() { ready <- w.Instance }) }
	go func() { _ = c.Run(ctx) }()

	var name string
	select {
	case name = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the client never established a tunnel over the upgrade")
	}
	h.waitOnline(name)

	resp, body := h.get(name, "/v1/health")
	if resp.StatusCode != 200 || !strings.Contains(body, "reached through the upgrade") {
		t.Errorf("status %d body %q; want the registry's answer through an upgraded tunnel", resp.StatusCode, body)
	}
}

// The upgrade endpoint has to behave for things that are not tunnels: a plain
// GET arrives from health checkers and curious people, and must not hang.
func TestTunnelPathRefusesNonUpgradeRequests(t *testing.T) {
	h := newHarness(t)
	resp, err := h.public.Client().Get(h.public.URL + TunnelPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("plain GET %s = %d, want 426", TunnelPath, resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), ProtocolID) {
		t.Errorf("the refusal does not say what the path speaks: %q", body)
	}
}

// The accept token is what tells a client it reached an ingress rather than
// some other server that happens to answer 101.
func TestUpgradeAcceptTokenIsTheRFCOne(t *testing.T) {
	// The example from RFC 6455 section 1.3.
	if got := AcceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Errorf("AcceptKey = %q, want the RFC 6455 example value", got)
	}
	first, second := NewClientKey(), NewClientKey()
	if first == second {
		t.Error("client keys repeat")
	}
}

// Addr accepts both shapes, and the URL form decides TLS and port for itself.
func TestClientResolvesItsTarget(t *testing.T) {
	for _, tc := range []struct {
		addr    string
		wantTo  string
		wantTLS bool
		upgrade bool
	}{
		{"https://ingress.tacit.zone", "ingress.tacit.zone:443", true, true},
		{"https://ingress.tacit.zone:8443", "ingress.tacit.zone:8443", true, true},
		{"http://localhost:18443", "localhost:18443", false, true},
		{"localhost:8444", "localhost:8444", false, false}, // the raw listener
	} {
		got, err := (&Client{Addr: tc.addr}).target()
		if err != nil {
			t.Errorf("%s: %v", tc.addr, err)
			continue
		}
		if got.addr != tc.wantTo || got.useTLS != tc.wantTLS || got.upgrade != tc.upgrade {
			t.Errorf("%s resolved to %+v; want %s tls=%v upgrade=%v",
				tc.addr, got, tc.wantTo, tc.wantTLS, tc.upgrade)
		}
	}
	if _, err := (&Client{Addr: "ftp://nope"}).target(); err == nil {
		t.Error("an unusable scheme was accepted")
	}
}

// Behind a TLS terminator the ingress listens on one port and the world reaches
// it on another. The published address has to carry the outside one — a
// registry handed its own proxy's internal port advertises an address nobody
// can reach, and its OIDC callback would be registered against it.
func TestPublishedPortIsTheOutsideOne(t *testing.T) {
	// Apache on 443 proxying to a loopback listener: what the host runs.
	fronted := Config{
		Zone: "tacit.zone", Scheme: "https",
		PublicAddr: "127.0.0.1:8443", PublicPort: "443",
	}.WithDefaults()
	if got := fronted.PublicURL("basalt-reach"); got != "https://basalt-reach.tacit.zone" {
		t.Errorf("published URL = %q; want no port, since 443 is the scheme's default", got)
	}

	// A non-default outside port is stated.
	odd := Config{Zone: "tacit.zone", Scheme: "https", PublicAddr: "127.0.0.1:8443", PublicPort: "9443"}.WithDefaults()
	if got := odd.PublicURL("basalt-reach"); got != "https://basalt-reach.tacit.zone:9443" {
		t.Errorf("published URL = %q; want the outside port", got)
	}

	// Unset, the listener's port stands — the direct case, unchanged.
	direct := Config{Zone: "tacit.zone", PublicAddr: ":8443"}.WithDefaults()
	if got := direct.PublicURL("cedar-hollow"); got != "http://cedar-hollow.tacit.zone:8443" {
		t.Errorf("published URL = %q; want the listener's port when nothing fronts it", got)
	}
}

// A tunnel usually runs through someone else's web server, and those close idle
// proxied connections on their own schedule — often without a clean shutdown
// this side can detect. A parked connection older than the pool's limit must
// therefore never be handed to a request: the cost of using one is a timeout,
// which is what turned a working tunnel into 20-second page loads.
func TestStaleParkedConnectionsAreNeverHandedOut(t *testing.T) {
	var wanted int
	p := newConnPool(2, 50*time.Millisecond, func(n int) { wanted += n })

	fresh, freshPeer := net.Pipe()
	stale, stalePeer := net.Pipe()
	t.Cleanup(func() {
		_ = fresh.Close()
		_ = freshPeer.Close()
		_ = stale.Close()
		_ = stalePeer.Close()
	})

	p.Put(stale)
	time.Sleep(80 * time.Millisecond) // older than maxIdle now
	p.Put(fresh)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := p.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != fresh {
		t.Error("the pool handed out a connection that had been idle past its limit")
	}
	if wanted == 0 {
		t.Error("retiring a connection did not ask the registry for a replacement")
	}
}

// Retire sweeps on its own clock, so the pool is warm before a request arrives
// rather than being repaired by one.
func TestRetireClearsIdleConnectionsAndTopsUp(t *testing.T) {
	var wanted int
	p := newConnPool(3, 40*time.Millisecond, func(n int) { wanted += n })
	for range 3 {
		c, peer := net.Pipe()
		t.Cleanup(func() { _ = c.Close(); _ = peer.Close() })
		p.Put(c)
	}
	if p.Len() != 3 {
		t.Fatalf("parked %d, want 3", p.Len())
	}
	time.Sleep(70 * time.Millisecond)
	p.Retire()

	if p.Len() != 0 {
		t.Errorf("%d connections survived retirement", p.Len())
	}
	if wanted < 3 {
		t.Errorf("asked for %d replacements, want the 3 that were retired", wanted)
	}
	// A fresh connection is left alone.
	c, peer := net.Pipe()
	t.Cleanup(func() { _ = c.Close(); _ = peer.Close() })
	p.Put(c)
	p.Retire()
	if p.Len() != 1 {
		t.Error("retirement discarded a connection that was still fresh")
	}
}

// The declaration crosses the wire. A registry says what it is at the
// handshake and the proxy records what it was told — through a real tunnel,
// because a flag that is set correctly in the struct and dropped on the way out
// is exactly the bug a stub would miss.
func TestARegistryDeclaresItselfATestAcrossTheTunnel(t *testing.T) {
	h := newHarness(t)

	c := &Client{
		Addr: h.tunnelLn.Addr().String(), Token: NewKey(),
		Version: "test-build", Test: true,
	}
	c.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ready := make(chan string, 1)
	var once sync.Once
	c.OnWelcome = func(w Welcome) { once.Do(func() { ready <- w.Instance }) }
	go func() { _ = c.Run(ctx) }()

	var name string
	select {
	case name = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("client never connected")
	}
	h.waitOnline(name)

	in, ok := h.srv.Store.Get(name)
	if !ok {
		t.Fatalf("the ingress reported %q and has no such instance", name)
	}
	if !in.Test {
		t.Error("the registry declared itself a test and the proxy did not record it")
	}

	// And a registry that says nothing is not one. This is the half that
	// matters: the default has to be "somebody's registry", because the cost of
	// getting it wrong is a real hostname inside the reach of a sweep.
	plain := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if plain.Test {
		t.Error("a registry that declared nothing was recorded as a test")
	}

	// A test instance is routed like any other. A proxy that treated them
	// differently would not be testing the proxy.
	resp, _ := h.get(name, "/health")
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode >= 500 {
		t.Errorf("a test instance is not being served: %d", resp.StatusCode)
	}
}

// The declaration a registry's CONFIG produces, carried to the proxy. The two
// halves are tested apart elsewhere — the config default in
// internal/registry/config, the wire format above — and this is the join: what
// a registry loaded under `go test` actually puts on the handshake.
//
// It matters because the default address a registry publishes through is the
// proxy the project runs. A test that enables publishing and forgets to point
// that somewhere harmless reaches production, and the only thing standing
// between that and an unaccountable hostname is this flag arriving set.
func TestAConfigDeclaredTestReachesTheProxy(t *testing.T) {
	h := newHarness(t)

	// What internal/registry/web/publish.go builds, with the field it now takes
	// from config.PublishTest.
	for _, tc := range []struct {
		declared bool
		why      string
	}{
		{true, "a registry whose config says it is a test"},
		{false, "a registry whose config says it is not"},
	} {
		c := &Client{Addr: h.tunnelLn.Addr().String(), Token: NewKey(),
			Version: "test-build", Test: tc.declared}
		c.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		ctx, cancel := context.WithCancel(context.Background())
		ready := make(chan string, 1)
		var once sync.Once
		c.OnWelcome = func(w Welcome) { once.Do(func() { ready <- w.Instance }) }
		go func() { _ = c.Run(ctx) }()

		var name string
		select {
		case name = <-ready:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatalf("%s never connected", tc.why)
		}
		h.waitOnline(name)
		in, ok := h.srv.Store.Get(name)
		if !ok {
			cancel()
			t.Fatalf("%s: the ingress reported %q and has no such instance", tc.why, name)
		}
		if in.Test != tc.declared {
			t.Errorf("%s arrived as test=%v", tc.why, in.Test)
		}
		cancel()
	}
}
