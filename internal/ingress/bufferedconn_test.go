// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"io"
	"net"
	"testing"
	"time"
)

// A data connection arrives at the registry's own HTTP server through
// openData, which handshakes over a bufio.Reader first. Whatever that reader
// has already pulled off the socket has to reach the server.
//
// It used to be thrown away, and the connection with it, on the theory that
// bytes arriving that early meant the ingress had spoken out of turn. They mean
// the opposite: the handshake is done, the connection is in the pool, and if a
// request was waiting the ingress writes it at once. Under load that is routine
// — a request beat this goroutine to the socket — and closing the connection
// made the ingress read EOF on a request it had already sent, then answer 502.
//
// retryOnce hid one. It could not hide two in a row on the same request, which
// is how it reached CI as TestManyRequestsReuseAndReplaceConnections failing
// with "proxy error … EOF" and four 502s.
func TestBufferedConnDeliversWhatTheHandshakeAlreadyRead(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })

	const request = "GET /v1/techniques HTTP/1.1\r\nHost: x\r\n\r\n"
	go func() {
		// The far side sends a greeting line and then, without waiting, a
		// request — the sequence that produced the flake.
		_, _ = io.WriteString(client, "hello\n"+request)
	}()

	br := bufio.NewReader(server)
	greeting, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the greeting: %v", err)
	}
	if greeting != "hello\n" {
		t.Fatalf("greeting = %q", greeting)
	}
	if br.Buffered() == 0 {
		t.Skip("the reader did not over-read this time; the race needs it to")
	}

	// What the server is handed must still carry the request.
	dc := bufferedConn{Conn: server, r: br}
	_ = dc.SetReadDeadline(time.Now().Add(2 * time.Second))
	got := make([]byte, len(request))
	if _, err := io.ReadFull(dc, got); err != nil {
		t.Fatalf("the request buffered during the handshake never reached the server: %v", err)
	}
	if string(got) != request {
		t.Errorf("read %q, want the request %q", got, request)
	}
}

// The other half: a connection that arrives with nothing buffered must read
// straight through to the socket, so one type serves both and neither path is
// special.
func TestBufferedConnReadsThroughWhenNothingWasBuffered(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })

	br := bufio.NewReader(server)
	dc := bufferedConn{Conn: server, r: br}
	go func() { _, _ = io.WriteString(client, "later bytes") }()

	_ = dc.SetReadDeadline(time.Now().Add(2 * time.Second))
	got := make([]byte, len("later bytes"))
	if _, err := io.ReadFull(dc, got); err != nil {
		t.Fatalf("reading through to the socket: %v", err)
	}
	if string(got) != "later bytes" {
		t.Errorf("read %q", got)
	}
}

// bufferedConn must stay a net.Conn: the HTTP server sets deadlines on it and
// reads its addresses, and an embedded interface makes that free — but only as
// long as nobody replaces the embedding with a field.
func TestBufferedConnIsStillAConn(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })

	var c net.Conn = bufferedConn{Conn: server, r: bufio.NewReader(server)}
	if err := c.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Errorf("SetDeadline: %v", err)
	}
	if c.LocalAddr() == nil || c.RemoteAddr() == nil {
		t.Error("addresses do not reach the underlying connection")
	}
	// net.Pipe is unbuffered, so a write needs somebody reading it.
	read := make(chan string, 1)
	go func() {
		b := make([]byte, 1)
		if _, err := io.ReadFull(client, b); err != nil {
			read <- ""
			return
		}
		read <- string(b)
	}()
	if _, err := io.WriteString(c, "x"); err != nil {
		t.Errorf("writes do not reach the underlying connection: %v", err)
	}
	if got := <-read; got != "x" {
		t.Errorf("the far side read %q, want the byte the write sent", got)
	}
}
