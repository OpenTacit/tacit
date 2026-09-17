// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// flush() has to reach net/http itself, not the nearest wrapper that happens to
// carry the method.
//
// The Settings page flushes before it stops the tunnel the operator is reading
// through, and before it restarts the process for sign-in. A flush that lands in
// one of the chain's own writers leaves the answer in a buffer behind a socket
// that is about to go away, and the browser hangs — which is the bug the comment
// in handleSettingsSave says was already fixed once.
//
// It went wrong by type assertion: the chain's writers carried Flush by hand or
// not at all, and w.(http.Flusher) only ever sees the outermost one. Both mounts
// are checked because they end in different writers.
func TestFlushReachesTheWireThroughTheWholeChain(t *testing.T) {
	for _, base := range []string{"", "/apps/tacit"} {
		t.Run("base="+base, func(t *testing.T) {
			srv, _ := newServer(t)
			srv.Cfg.BasePath = base

			release := make(chan struct{})
			done := make(chan struct{})
			probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("first\n"))
				flush(w)
				<-release // nothing else may be written until the client has read
				_, _ = w.Write([]byte("second\n"))
			})
			ts := httptest.NewServer(srv.wrap(probe))
			defer ts.Close()

			// Both the request and the first read run off the main goroutine: an
			// unflushed response never sends its header either, so a direct Get
			// would hang here instead of failing.
			read := make(chan string, 1)
			go func() {
				resp, err := http.Get(ts.URL + base + "/anything")
				if err != nil {
					read <- "error: " + err.Error()
					return
				}
				defer func() { _ = resp.Body.Close() }()
				line, _ := bufio.NewReader(resp.Body).ReadString('\n')
				read <- line
			}()

			select {
			case got := <-read:
				if got != "first\n" {
					t.Fatalf("read %q before the handler wrote the rest, want %q", got, "first\n")
				}
			case <-time.After(5 * time.Second):
				close(release) // let the handler finish so the server can shut down
				<-done
				t.Fatal("nothing reached the client: flush() did not get past the chain's own writers")
			}
			close(release)
			<-done
		})
	}
}

// Every writer in the chain has to be walkable, or whichever one forgets becomes
// the floor for flushes, hijacks and deadlines alike. This is the cheap version
// of the test above, and the one that names the missing method.
func TestEveryWriterInTheChainCanBeUnwrapped(t *testing.T) {
	srv, _ := newServer(t)
	srv.Cfg.BasePath = "/apps/tacit"

	var unreachable string
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for cur := w; ; {
			if _, ok := cur.(interface{ FlushError() error }); ok {
				return // net/http's own writer: the chain is whole
			}
			u, ok := cur.(interface{ Unwrap() http.ResponseWriter })
			if !ok {
				unreachable = typeOf(cur)
				return
			}
			cur = u.Unwrap()
		}
	})
	ts := httptest.NewServer(srv.wrap(probe))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/apps/tacit/anything")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if unreachable != "" {
		t.Errorf("%s has no Unwrap() http.ResponseWriter, so the chain dead-ends there", unreachable)
	}
}

func typeOf(v any) string { return fmt.Sprintf("%T", v) }
