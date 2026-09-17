// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A refused upgrade decides whether a published registry keeps its address: a
// fatal answer ends Client.Run for good, and nothing starts it again. So the
// classification is pinned status by status.
func TestUpgradeRefusalIsFatalOnlyWhenItIsPermanent(t *testing.T) {
	retryable := []int{
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502 — the ingress has no backend yet
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
		520,                            // Cloudflare: unintelligible origin answer
		521, 522, 523, 524,             // the rest of Cloudflare's origin family
		http.StatusTooManyRequests, // 429
		http.StatusRequestTimeout,  // 408
	}
	for _, code := range retryable {
		err := readUpgradeResponse(bufio.NewReader(strings.NewReader(rawResponse(code))), "k")
		var fatal *fatalError
		if err == nil {
			t.Errorf("%d was accepted as an upgrade", code)
		} else if errors.As(err, &fatal) {
			t.Errorf("%d is fatal — one of these took a live registry off the internet until a restart", code)
		}
	}

	permanent := []int{
		http.StatusUnauthorized,     // 401 — the key is wrong; retrying repeats it
		http.StatusForbidden,        // 403
		http.StatusNotFound,         // 404 — not an ingress path
		http.StatusGone,             // 410
		http.StatusUpgradeRequired,  // 426 — something that does not speak this
		http.StatusMovedPermanently, // 301
		http.StatusBadRequest,       // 400 — our own handshake is wrong
	}
	for _, code := range permanent {
		err := readUpgradeResponse(bufio.NewReader(strings.NewReader(rawResponse(code))), "k")
		var fatal *fatalError
		if !errors.As(err, &fatal) {
			t.Errorf("%d is retryable, so the client would reconnect forever against a permanent refusal (got %v)", code, err)
		}
	}

	// An answer from something that is not an ingress stays fatal whatever its
	// status: reconnecting to it can only produce the same wrong handshake.
	ok := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: not-the-key\r\n\r\n"
	var fatal *fatalError
	if err := readUpgradeResponse(bufio.NewReader(strings.NewReader(ok)), "k"); !errors.As(err, &fatal) {
		t.Errorf("a 101 from a non-ingress was not fatal: %v", err)
	}
}

func rawResponse(code int) string {
	return fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: 0\r\n\r\n", code, http.StatusText(code))
}

// The whole point of the classification: Run must keep trying. This stands up a
// listener that refuses the upgrade with a 520 — the exact answer that stopped
// a live registry — and proves the client comes back.
func TestRunRetriesATransientRefusalAndReportsBeingDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var attempts int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&attempts, 1)
			br := bufio.NewReader(conn)
			for { // drain the request head
				line, err := br.ReadString('\n')
				if err != nil || strings.TrimSpace(line) == "" {
					break
				}
			}
			_, _ = conn.Write([]byte(rawResponse(520)))
			_ = conn.Close()
		}
	}()

	var downs int32
	// A URL address is what takes the HTTP-upgrade path — the one that reads a
	// status back. A bare host:port speaks the tunnel protocol directly and
	// never sees an HTTP response at all.
	c := &Client{
		Addr:         "http://" + ln.Addr().String(),
		Token:        NewKey(),
		OnDisconnect: func(error) { atomic.AddInt32(&downs, 1) },
		Log:          func(f string, a ...any) { t.Logf(f, a...) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	deadline := time.After(10 * time.Second)
	for atomic.LoadInt32(&attempts) < 2 {
		select {
		case err := <-done:
			t.Fatalf("Run gave up after %d attempt(s) (%v) — the registry's address would stay dead until someone restarted it",
				atomic.LoadInt32(&attempts), err)
		case <-deadline:
			t.Fatalf("only %d attempt(s) in 10s", atomic.LoadInt32(&attempts))
		case <-time.After(20 * time.Millisecond):
		}
	}
	if atomic.LoadInt32(&downs) == 0 {
		t.Error("no OnDisconnect while the tunnel was down — Settings would still read 'connected'")
	}
	cancel()
	<-done
}
