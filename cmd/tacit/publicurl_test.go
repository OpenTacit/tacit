// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// awaitPublicURL is what stands between `tacit init` and handing somebody a
// link to 127.0.0.1 after they asked for an address on the internet.
func TestInitWaitsForTheAddressTheIngressAllocates(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// The tunnel is not up on the first ask, which is the ordinary case: the
		// registry starts, then dials.
		if calls < 2 {
			fmt.Fprint(w, `{"ok":true,"access":"private","external_url":""}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"access":"global","external_url":"https://juniper-grove.tacit.zone/"}`)
	}))
	defer ts.Close()

	got := awaitPublicURLAt(ts.URL, 10*time.Second)
	if got != "https://juniper-grove.tacit.zone" {
		t.Errorf("url = %q, want the allocated address with no trailing slash", got)
	}
}

func TestNoPublicAddressMeansNoPublicLink(t *testing.T) {
	// A registry that never reports one must not have a local address passed
	// off as the answer; the caller says so instead.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"access":"private","external_url":""}`)
	}))
	defer ts.Close()

	if got := awaitPublicURLAt(ts.URL, 2*time.Second); got != "" {
		t.Errorf("url = %q, want none", got)
	}
}

func TestInitDoesNotPollWhenNothingWillDial(t *testing.T) {
	// The case that produced a health check per second for thirty seconds into
	// the log somebody was watching: --service none, so init started nothing,
	// and a registry already running from before the change that could not
	// answer however long it was asked.
	polls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		fmt.Fprint(w, `{"ok":true,"access":"private","external_url":""}`)
	}))
	defer ts.Close()

	start := time.Now()
	base, where := ownerBaseAt(registryconfig.Config{Port: 8080}, 8080, ts.URL, true, false)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("waited %s for an address nothing was going to allocate", took)
	}
	if polls > 2 {
		t.Errorf("polled %d times; one look is enough to say a registry is up", polls)
	}
	// `local` is the address this function PROBES; the base it returns is the
	// address a person opens, and the two are deliberately different now
	// (ownerlink.go). What this case cares about is that it gave up quickly and
	// still handed back a usable link with the caveat attached.
	if base == "" || !strings.HasSuffix(where, "until then") {
		t.Errorf("base = %q where = %q, want an address and a caveat", base, where)
	}
}

func TestInitNamesACommandTheReaderCanRun(t *testing.T) {
	// "tacit dashboard" printed to somebody whose PATH holds a different tacit
	// — or none, which is every first run until the next shell — is advice that
	// fails at the one moment a member has nothing else to go on.
	got := selfCommand()
	if got == "tacit" {
		// The test binary is not on PATH as "tacit", so this would mean the
		// check resolved something it should not have.
		t.Fatalf("selfCommand = %q from a test binary", got)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("selfCommand = %q, want an absolute path when PATH does not resolve to this build", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("selfCommand = %q, which does not exist: %v", got, err)
	}
}

// A registry that is up, serving, and showing its operator no way to open it is
// the exact wall a public address exists to remove — so the sign-in link is
// never held back waiting for the tunnel. The caller prints the public link
// afterwards, if one lands.
func TestTheSignInLinkNeverWaitsForTheTunnel(t *testing.T) {
	// A registry that answers and will never report a public address: the
	// tunnel is dialling, or it is dialling something that is not there.
	polls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		fmt.Fprint(w, `{"ok":true,"access":"staged","external_url":""}`)
	}))
	defer ts.Close()

	start := time.Now()
	base, where := ownerBaseAt(registryconfig.Config{Port: 8080}, 8080, ts.URL, true, true)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("held the sign-in link for %s waiting on an address", took)
	}
	if polls != 0 {
		t.Errorf("polled %d times before handing over a link", polls)
	}
	if base == "" || where == "" {
		t.Errorf("base = %q where = %q, want an address and a caveat", base, where)
	}
}

// A registry that has just been created reports "staged": the tunnel is up and
// the address is live, and only the technique feed is withheld pending a
// person's yes. Waiting for "global" meant the public sign-in link — the one
// thing a new operator on a server actually needs — never printed.
func TestPublicURLAcceptsAStagedRegistry(t *testing.T) {
	for _, access := range []string{"global", "staged"} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"ok":true,"access":%q,"external_url":"https://ember-mesa-2.tacit.zone"}`, access)
		}))
		got := awaitPublicURLAt(ts.URL, 3*time.Second)
		ts.Close()
		if got != "https://ember-mesa-2.tacit.zone" {
			t.Errorf("access=%q: got %q, want the allocated address", access, got)
		}
	}
	// Private is not an address, however long you wait for one.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"access":"private","external_url":""}`)
	}))
	defer ts.Close()
	if got := awaitPublicURLAt(ts.URL, 1*time.Second); got != "" {
		t.Errorf("private registry reported %q", got)
	}
}
