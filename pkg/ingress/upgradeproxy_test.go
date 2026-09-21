// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

// A proxied response must still be able to become a tunnel.
//
// statusRecorder wraps every proxied response to count what was sent, and
// httputil.ReverseProxy asks http.ResponseController — not a type assertion —
// for the hijack a 101 needs. A wrapper with no Unwrap is where that request
// stops: the switch fails, the client is answered with a plain 200, and the only
// sign is a line in the error log. Passing Flush through by hand covered the
// interface somebody remembered and left this one.
func TestProxiedResponseCanStillSwitchProtocols(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("upstream could not hijack: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: " + ProtocolID + "\r\nConnection: Upgrade\r\n\r\n")
		_ = brw.Flush()
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	var proxyErr error
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(http.ResponseWriter, *http.Request, error) {}
	proxy.ErrorLog = nil

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, err error) { proxyErr = err }
		proxy.ServeHTTP(rec, r)
	}))
	defer front.Close()

	u, err := url.Parse(front.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: " + u.Host +
		"\r\nUpgrade: " + ProtocolID + "\r\nConnection: Upgrade\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(status, "HTTP/1.1 101") {
		t.Errorf("the proxy answered %q instead of switching protocols (proxy error: %v)",
			strings.TrimSpace(status), proxyErr)
	}
}
