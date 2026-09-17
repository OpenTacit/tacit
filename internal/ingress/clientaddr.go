// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"net"
	"net/http"
	"strings"
)

// Who a connection is actually from, when something else answered the socket.
//
// The ingress is designed to sit behind a front proxy — Apache's
// mod_proxy_wstunnel in the project's own deployment, terminating TLS on 443 and
// forwarding to a loopback listener. Everything then arrives from 127.0.0.1: the
// operations log recorded one address for the whole internet, the tunnel-events
// table could not tell one registry from another, and the per-address enrolment
// ceiling counted every registry on earth against a single counter.
//
// The address the front proxy forwards is the answer, but only from a peer
// entitled to claim it: X-Forwarded-For is a request header like any other, and
// a client reaching the ingress directly can write whatever it likes in one.
// So the header is honoured only when the immediate peer is a trusted proxy —
// loopback by default, plus whatever TACIT_INGRESS_TRUSTED_PROXIES names — and
// ignored otherwise. A public client cannot make its own socket appear to come
// from loopback, so the untrusted case cannot be spoofed into the trusted one.

// clientAddr returns the address to attribute a request to: the originating
// client when a trusted proxy forwarded it, else the peer that connected.
func (s *Server) clientAddr(r *http.Request) string {
	if !s.trustsPeer(r.RemoteAddr) {
		return r.RemoteAddr
	}
	if fwd := forwardedFor(r.Header); fwd != "" {
		return fwd
	}
	return r.RemoteAddr
}

// forwardedFor reads the originating client from the forwarding headers, in the
// order of how specific they are. X-Forwarded-For accumulates left to right as a
// request crosses proxies, so the leftmost entry is the original client.
func forwardedFor(h http.Header) string {
	if xff := h.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
			return ip.String()
		}
	}
	if real := strings.TrimSpace(h.Get("X-Real-IP")); real != "" {
		if ip := net.ParseIP(real); ip != nil {
			return ip.String()
		}
	}
	// RFC 7239's replacement for the above, which some proxies send instead:
	// Forwarded: for=192.0.2.60;proto=http, for=198.51.100.17
	if f := h.Get("Forwarded"); f != "" {
		first, _, _ := strings.Cut(f, ",")
		for _, part := range strings.Split(first, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok || !strings.EqualFold(k, "for") {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"`)
			// for="[2001:db8::1]:4711" and for=192.0.2.60:4711 both occur.
			if host, _, err := net.SplitHostPort(v); err == nil {
				v = host
			}
			v = strings.Trim(v, "[]")
			if ip := net.ParseIP(v); ip != nil {
				return ip.String()
			}
		}
	}
	return ""
}

// trustsPeer reports whether an address is a front proxy whose forwarding
// headers this ingress will believe.
func (s *Server) trustsPeer(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true // the same-host proxy arrangement, trusted without configuration
	}
	for _, cidr := range s.Cfg.TrustedProxies {
		if _, netw, err := net.ParseCIDR(cidr); err == nil && netw.Contains(ip) {
			return true
		}
		if named := net.ParseIP(cidr); named != nil && named.Equal(ip) {
			return true
		}
	}
	return false
}
