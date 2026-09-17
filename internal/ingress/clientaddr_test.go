// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Who a connection is from, when a front proxy answered the socket. Trusting a
// forwarding header is only safe from a peer entitled to send one, so the table
// covers both halves: the proxy's claim is believed, a stranger's is not.
func TestClientAddrBelievesOnlyATrustedProxy(t *testing.T) {
	s := &Server{Cfg: Config{TrustedProxies: []string{"10.9.0.0/16"}}}
	for _, tc := range []struct {
		name   string
		peer   string
		header map[string]string
		want   string
	}{
		{"same-host proxy forwards the client",
			"127.0.0.1:44210", map[string]string{"X-Forwarded-For": "203.0.113.7"}, "203.0.113.7"},
		{"a chain keeps the leftmost, which is the original client",
			"127.0.0.1:44210", map[string]string{"X-Forwarded-For": "203.0.113.7, 70.41.3.18, 10.0.0.1"}, "203.0.113.7"},
		{"X-Real-IP when that is what the proxy sends",
			"[::1]:44210", map[string]string{"X-Real-IP": "203.0.113.9"}, "203.0.113.9"},
		{"RFC 7239 Forwarded, port and quotes stripped",
			"127.0.0.1:44210", map[string]string{"Forwarded": `for="192.0.2.60:4711";proto=https`}, "192.0.2.60"},
		{"a configured proxy elsewhere is trusted too",
			"10.9.4.4:33001", map[string]string{"X-Forwarded-For": "203.0.113.7"}, "203.0.113.7"},
		{"no header from the proxy leaves the socket",
			"127.0.0.1:44210", nil, "127.0.0.1:44210"},
		// The spoof case. A client reaching the ingress directly cannot make its
		// own socket look like the proxy's, so its claim is simply ignored.
		{"an untrusted peer claiming to be someone else is not believed",
			"198.51.100.23:51000", map[string]string{"X-Forwarded-For": "127.0.0.1"}, "198.51.100.23:51000"},
		{"nor when it claims a public address",
			"198.51.100.23:51000", map[string]string{"X-Forwarded-For": "203.0.113.7"}, "198.51.100.23:51000"},
		{"a proxy outside the configured range is not trusted",
			"10.8.0.5:33001", map[string]string{"X-Forwarded-For": "203.0.113.7"}, "10.8.0.5:33001"},
		{"garbage in the header falls back to the socket",
			"127.0.0.1:44210", map[string]string{"X-Forwarded-For": "not-an-address"}, "127.0.0.1:44210"},
	} {
		r, _ := http.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		for k, v := range tc.header {
			r.Header.Set(k, v)
		}
		if got := s.clientAddr(r); got != tc.want {
			t.Errorf("%s: clientAddr = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The whole point of the column. A tunnel that arrives through a front proxy
// records where the REGISTRY is, not where the proxy is — otherwise the console
// shows one loopback address for every instance, which distinguishes nothing,
// and the per-address enrolment ceiling counts the whole internet as one source.
func TestProxiedTunnelRecordsTheRegistryAddressNotTheProxy(t *testing.T) {
	h := newHarness(t)

	// Dial the public listener the way a front proxy would: an upgrade request
	// carrying the address it forwarded for. The listener is loopback, so this
	// connection IS the trusted-peer case.
	target := strings.TrimPrefix(h.public.URL, "http://")
	conn, err := net.Dial("tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	key := NewClientKey()
	req := "GET " + TunnelPath + " HTTP/1.1\r\n" +
		"Host: ingress.test.local\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n" +
		"X-Forwarded-For: 203.0.113.7\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade = %s", resp.Status)
	}

	hello, _ := json.Marshal(Hello{Protocol: ProtocolID, Role: RoleControl,
		Token: NewKey(), Version: "proxied-build"})
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		t.Fatal(err)
	}
	var welcome Welcome
	if err := json.NewDecoder(br).Decode(&welcome); err != nil {
		t.Fatalf("welcome: %v", err)
	}
	if !welcome.OK {
		t.Fatalf("welcome refused: %s", welcome.Error)
	}
	h.waitOnlineName(t, welcome.Instance)

	// The instance page's "from", and the tunnel-events table's.
	if got := h.srv.StatusOf(welcome.Instance).RemoteStr; got != "203.0.113.7" {
		t.Errorf("the instance reports it connected from %q, want the forwarded address; "+
			"a proxied deployment would show the proxy's own loopback for every registry", got)
	}
	// The tunnel is registered as live a moment before its event is written, so
	// coming online is not proof the record has landed.
	var ops []Op
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if ops = h.srv.Ops.Recent(OpFilter{Instance: welcome.Instance, Kind: FilterTunnel, Limit: 5}); len(ops) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(ops) == 0 {
		t.Fatal("no tunnel event recorded")
	}
	// Truncated to a network, as every recorded address is.
	if ops[0].Kind != KindTunnelUp || ops[0].Client != "203.0.113.0/24" {
		t.Errorf("tunnel event = %s from %q, want tunnel-up from the forwarded network",
			ops[0].Kind, ops[0].Client)
	}
}

// The same question for served requests: the operations log exists to answer
// "which clients connect", and behind a front proxy it was answering "the proxy"
// for every request ever forwarded — one address for the whole internet.
func TestProxiedRequestRecordsTheClientNotTheProxy(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req, _ := http.NewRequest("GET", h.public.URL+"/v1/health", nil)
	req.Host = in.Name + ".test.local"
	req.Header.Set("X-Forwarded-For", "198.51.100.42")
	resp, err := h.public.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	ops := h.srv.Ops.Recent(OpFilter{Instance: in.Name, Kind: FilterRequests, Limit: 1})
	if len(ops) == 0 {
		t.Fatal("nothing recorded")
	}
	if ops[0].Client != "198.51.100.0/24" {
		t.Errorf("recorded client = %q, want the forwarded network — the log would otherwise "+
			"credit every request to whatever proxies them", ops[0].Client)
	}
}

// The instances table carries the source, and carries it for every row — which
// is why the address is stored on the instance rather than read off the live
// tunnel. An operator looking for where a registry lives is most often looking
// at one that has stopped answering.
func TestInstancesTableShowsTheSourceOnlineAndOff(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	page := h.consoleHTML(t, "/instances")
	// The whole header, in order: each column was asked for in a particular
	// place, and "somewhere in the table" is not what was asked for.
	if !strings.Contains(page, "<th>name</th><th>state</th><th>build</th><th>source</th>"+
		"<th class=\"num\">members</th><th class=\"num\">requests</th>") {
		t.Errorf("the columns are not in the order they were asked for:\n%s", tableHead(page))
	}
	// A directly-dialled tunnel is loopback here; the port is ephemeral and
	// identifies nothing, so the column shows the address alone.
	live := h.srv.StatusOf(in.Name).RemoteStr
	if live == "" || !strings.Contains(live, ":") {
		t.Fatalf("expected a host:port tunnel source while online, got %q", live)
	}
	host := sourceHost(live)
	if !strings.Contains(page, "<td><code>"+host+"</code></td>") {
		t.Errorf("the row does not show the live source %q", host)
	}
	if strings.Contains(page, "<td><code>"+live+"</code></td>") {
		t.Error("the row shows the ephemeral port, which changes on every reconnection")
	}

	// Drop the tunnel: the instance goes offline and the column must still
	// answer, from what was stored at the handshake.
	h.srv.DropTunnel(in.Name)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if !h.srv.StatusOf(in.Name).Online {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stored, ok := h.srv.Store.Get(in.Name)
	if !ok || stored.Source == "" {
		t.Fatalf("the handshake recorded no source on the instance: %+v", stored)
	}
	offline := h.consoleHTML(t, "/instances")
	if !strings.Contains(offline, "<td><code>"+sourceHost(stored.Source)+"</code></td>") {
		t.Errorf("an offline instance shows no source, so the column empties exactly when it is wanted:\n%s",
			tableHead(offline))
	}
}

// consoleHTML fetches a console page as text.
func (h *harness) consoleHTML(t *testing.T, path string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", h.public.URL+path, nil)
	req.Host = "ingress.test.local"
	resp, err := h.public.Client().Do(req)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s: status %d", path, resp.StatusCode)
	}
	return string(body)
}

// tableHead is the row of column headers, for a readable failure.
func tableHead(page string) string {
	i := strings.Index(page, "<thead>")
	if i < 0 {
		return "(no table)"
	}
	rest := page[i:]
	if j := strings.Index(rest, "</thead>"); j >= 0 {
		return rest[:j]
	}
	return rest[:200]
}

// waitOnlineName is waitOnline for a name the caller already has.
func (h *harness) waitOnlineName(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.srv.StatusOf(name).Online {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("instance %s never came online", name)
}
