// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"errors"
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/product"
)

// handleTunnelUpgrade turns an HTTP request into a tunnel.
//
// This is how a registry reaches an ingress that shares its host with a web
// server: the connection arrives on 443 as an ordinary request, is upgraded,
// and from the 101 onward carries the tunnel protocol. Whatever sits in front —
// Apache's mod_proxy_wstunnel here — pumps the bytes without inspecting them.
//
// Nothing is authenticated at this layer. The upgrade only produces a stream;
// the instance key is presented in the handshake that follows, and an
// unauthenticated stream can do nothing but wait to be closed.
func (s *Server) handleTunnelUpgrade(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "this path speaks "+ProtocolID+" over an HTTP upgrade", http.StatusUpgradeRequired)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}
	// Resolved BEFORE the hijack: after it there is no request left to read, and
	// the socket alone says "the proxy in front of me" rather than which
	// registry this is.
	remote := s.clientAddr(r)
	hj, ok := w.(http.Hijacker)
	if !ok {
		// A server that cannot hand over the connection cannot carry a tunnel.
		http.Error(w, "this server cannot upgrade connections", http.StatusInternalServerError)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		http.Error(w, "could not take over the connection", http.StatusInternalServerError)
		return
	}

	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + AcceptKey(key) + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		_ = conn.Close()
		return
	}
	if err := brw.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	// brw.Reader may hold bytes the client sent immediately after its request —
	// it should not, since the client waits for the 101, but discarding them
	// would corrupt the handshake rather than fail it, so they are carried
	// through instead.
	s.serveTunnel(conn, brw.Reader, remote)
}

// upgradeRequest is the client's half: an ordinary HTTP request asking to
// become a tunnel. It is a valid WebSocket handshake so that proxies recognise
// and pass it; see the note on wsGUID for why nothing is framed afterwards.
func upgradeRequest(host, path, clientKey string) string {
	if path == "" {
		path = TunnelPath
	}
	return "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: " + clientKey + "\r\n" +
		"User-Agent: tacit-registry\r\n\r\n"
}

// retryableUpgradeStatus reports whether a refused upgrade is worth another
// attempt. The distinction decides whether a registry keeps its public address:
// a fatal answer ends Client.Run, and a registry that stopped publishing does
// not start again by itself — its address simply stops existing while it serves
// happily on localhost.
//
// So only an answer that says something permanent about THIS registry is fatal:
// a rejected key, a path that is not an ingress. Everything a proxy in front of
// the ingress emits while it is unwell — 502/503/504, Cloudflare's 520-524,
// 429, 408 — is a moment in time. One of those (a 520, mid-reconnect) once took
// a live registry off the internet until somebody restarted the service, which
// is the failure this classification exists to prevent.
func retryableUpgradeStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return code >= 500
}

// readUpgradeResponse checks the server switched protocols and that the answer
// came from something that knows the handshake.
func readUpgradeResponse(br *bufio.Reader, clientKey string) error {
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		msg := "the ingress refused the tunnel upgrade: " + resp.Status
		if retryableUpgradeStatus(resp.StatusCode) {
			return errors.New(msg)
		}
		return &fatalError{msg: msg}
	}
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != AcceptKey(clientKey) {
		return &fatalError{msg: "the upgrade was answered by something that is not the " + product.Name() + " ingress"}
	}
	return nil
}
