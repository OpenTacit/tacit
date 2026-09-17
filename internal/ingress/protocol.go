// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/opentacit/tacit/pkg/intelligence"
)

// The tunnel protocol, version 1.
//
// A registry opens two kinds of connection to the ingress, both dialled
// outward, both authenticated by the same instance token:
//
//   - one CONTROL connection, which stays up for the life of the publication
//     and carries heartbeats and requests for more capacity; and
//   - some number of DATA connections, which are parked at the ingress and
//     then used, one at a time, as ordinary HTTP/1.1 connections with the roles
//     reversed — the ingress writes requests, the registry answers them.
//
// The reversal is the whole trick, and it is why this needs no framing layer
// and no multiplexing library. A parked data connection is just a socket that
// http.Transport can write a request to and net/http.Server can read one from.
// Everything either side already knows about keep-alive, chunking, trailers and
// pipelining applies unchanged.
//
// The cost of that simplicity is that concurrency is bounded by the number of
// parked connections, which is why the control connection exists: when the
// ingress runs low it asks for more.

// ProtocolID is sent by the client and checked by the server, so a stray
// connection from something else fails immediately and legibly.
const ProtocolID = "tacit-ingress/1"

// TunnelPath is where a registry asks to be upgraded into a tunnel.
//
// The tunnel is not HTTP, but it has to arrive looking like HTTP: an ingress
// usually shares its host with a web server that already owns 443, and asking
// operators to open a second port — through a firewall, on a machine serving
// other people's sites — is a worse ask than speaking the protocol everything
// in the path already understands. So the connection opens as an ordinary
// request, is upgraded, and becomes a raw stream from the 101 onward.
//
// The underscore marks it as the ingress's own rather than a path any registry
// serves, and it is checked before hostname routing so it works on the console
// host as well as an instance's.
const TunnelPath = "/_tunnel"

// wsGUID is the constant from RFC 6455 used to derive the accept token.
//
// The handshake is a WebSocket one and what follows it is not: after the 101
// this is the tunnel's own stream, unframed. That is deliberate. Proxies pass
// an upgraded connection through as opaque bytes — Apache's mod_proxy_wstunnel
// pumps without looking — so borrowing the handshake buys passage through the
// middleboxes that already exist, without the cost of framing, masking and
// ping/pong that would buy nothing here. A proxy that insisted on parsing
// frames would break this; if one ever sits in the path, the frames go in.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// AcceptKey computes the Sec-WebSocket-Accept value for a client's key.
func AcceptKey(clientKey string) string {
	sum := sha1.Sum([]byte(clientKey + wsGUID)) //nolint:gosec // RFC 6455 handshake constant, not a security primitive
	return base64.StdEncoding.EncodeToString(sum[:])
}

// NewClientKey is the random Sec-WebSocket-Key a client offers.
func NewClientKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// Roles a connection can take.
const (
	RoleControl = "control"
	RoleData    = "data"
	// RoleRetire asks the ingress to forget this instance and free its
	// hostname. It is a one-line exchange on a connection of its own rather
	// than a control message, because the registry that wants to be forgotten
	// is usually no longer running — `tacit merge` releases the address AFTER
	// stopping the registry, and a control message would need a tunnel that no
	// longer exists.
	RoleRetire = "retire"
)

// Hello is the client's first line.
type Hello struct {
	Protocol string `json:"tacit"`
	Role     string `json:"role"`
	Token    string `json:"token"`
	Version  string `json:"version,omitempty"` // the registry's build
}

// Welcome is the server's answer.
type Welcome struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Instance string `json:"instance,omitempty"`
	Host     string `json:"host,omitempty"`
	URL      string `json:"url,omitempty"`
	// Want is how many data connections the ingress would like parked. The
	// client treats it as a target, not a maximum.
	Want int `json:"want,omitempty"`
}

// Control message types.
const (
	MsgWant  = "want" // open N more data connections
	MsgPing  = "ping"
	MsgPong  = "pong"
	MsgBye   = "bye"   // the ingress is going away or the instance was disabled
	MsgStats = "stats" // the registry's own figures, for whoever runs the proxy
)

// Control is one line on the control connection, in either direction.
type Control struct {
	Type string `json:"type"`
	N    int    `json:"n,omitempty"`
	Text string `json:"text,omitempty"`
	// Members and the three beside it ride a stats message: how many member
	// machines the registry has seen in a day, a week, a month and a year.
	//
	// They are COUNTS, and the reason they arrive this way rather than being
	// derived at the proxy is the whole privacy argument for this service. The
	// ingress could distinguish members itself — it terminates TLS, and the
	// member key is on every /v1 request — and the moment it did, its log could
	// profile one person's paths and hours across an organization. So the
	// registry, which is entitled to know who its members are, does the counting
	// and sends the totals. Nothing that identifies anyone crosses this channel.
	//
	// Members keeps its original name and its original meaning (the week) so a
	// registry built before the other three still reports something a newer
	// proxy understands. The proxy shows a dash for whichever windows it was not
	// told about rather than inventing them from the one it has.
	Members      int `json:"members,omitempty"`       // seen in the last 7 days
	MembersDay   int `json:"members_day,omitempty"`   // last 24 hours
	MembersMonth int `json:"members_month,omitempty"` // last 30 days
	MembersYear  int `json:"members_year,omitempty"`  // last 365 days

	// Windows says the sender filled in all four, so a zero among them is a
	// zero. Without it the proxy cannot tell "nobody was active yesterday" from
	// "this registry is too old to have been asked" — both arrive as an absent
	// field, and showing a real zero as "unknown" misleads exactly the person
	// this message exists for.
	Windows bool `json:"members_windows,omitempty"`
	// Intelligence is a signed, aggregate-only import and outcome snapshot.
	Intelligence *intelligence.Report `json:"intelligence,omitempty"`
}

// Stats is what a registry reports about itself. The proxy's operator needs to
// know how much each tenant is worth carrying; these are the smallest numbers
// that answer it.
//
// Every figure is member MACHINES, not people: one member with a laptop and a
// desktop holds two keys, and a chat tool signed in through MCP holds none of
// them. They are the same numbers the registry's own Members page counts, from
// the same rule, over four windows.
//
// Each is a ROLLING window — machines seen in the period ending now — because
// that is what the registry can answer truthfully. A member key carries one
// last-seen timestamp, overwritten each time it is used, so "how many were
// active last Tuesday" is not a question the data can be asked. Nothing here is
// history; history is what the proxy builds by keeping these samples (store.go).
type Stats struct {
	Day   int // machines seen in the last 24 hours
	Week  int // in the last 7 days
	Month int // in the last 30 days
	Year  int // in the last 365 days
}

// handshakeTimeout bounds the opening exchange. A connection that has not
// identified itself in this long is not a registry.
const handshakeTimeout = 10 * time.Second

// readJSONLine reads one newline-delimited JSON value with a deadline.
func readJSONLine(r *bufio.Reader, c net.Conn, into any, wait time.Duration) error {
	if wait > 0 {
		_ = c.SetReadDeadline(time.Now().Add(wait))
		defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	}
	line, err := r.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return io.EOF
		}
		if len(line) == 0 {
			return err
		}
	}
	return json.Unmarshal(line, into)
}

// writeJSONLine writes one newline-delimited JSON value with a deadline.
func writeJSONLine(c net.Conn, v any, wait time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if wait > 0 {
		_ = c.SetWriteDeadline(time.Now().Add(wait))
		defer func() { _ = c.SetWriteDeadline(time.Time{}) }()
	}
	_, err = c.Write(b)
	return err
}
