// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// retirer is a client that can do nothing but retire: no handler, no tunnel.
// That is the case the command actually has — `tacit merge` releases the
// address after the registry has been stopped, so there is nothing left to
// publish.
func (h *harness) retirer(token string) *Client {
	return &Client{Addr: h.tunnelLn.Addr().String(), Token: token, Version: "test-build"}
}

func TestRetireFreesTheHostnameAndTheEnrolmentSlot(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	token := h.client.Token

	name, err := h.retirer(token).Retire(context.Background())
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if name != in.Name {
		t.Errorf("retired %q, want %q", name, in.Name)
	}
	if _, ok := h.srv.Store.Get(in.Name); ok {
		t.Errorf("%s is still in the route table", in.Name)
	}
	if _, ok := h.srv.Store.Resolve(token); ok {
		t.Errorf("the instance key still resolves after the instance was retired")
	}

	// The address stops answering, which is the whole observable point.
	resp, _ := h.get(in.Name, "/v1/health")
	if resp.StatusCode == 200 {
		t.Errorf("the retired hostname still serves the registry (status %d)", resp.StatusCode)
	}
}

// The name coming back is the reason to release at all: enrolment is capped,
// and a retired instance that kept its slot would be a cost carried for
// somebody who has left.
func TestRetiredNameCanBeAllocatedAgain(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.MaxInstances = 1
	h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	// The ceiling is real before the release.
	if _, _, err := h.srv.Store.Enroll(NewKey(), 1); err != ErrFull {
		t.Fatalf("enrol at the ceiling = %v, want ErrFull", err)
	}
	if _, err := h.retirer(h.client.Token).Retire(context.Background()); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, created, err := h.srv.Store.Enroll(NewKey(), 1); err != nil || !created {
		t.Fatalf("enrol after retiring = (%v, %v), want a new instance", created, err)
	}
}

// A merge interrupted between the delete and its acknowledgement has to be
// safe to run again: the state the caller asked for already holds.
func TestRetireIsIdempotentForAnUnknownKey(t *testing.T) {
	h := newHarness(t)
	before := len(h.srv.Store.List())

	name, err := h.retirer(NewKey()).Retire(context.Background())
	if err != nil {
		t.Fatalf("retiring an unknown key = %v, want no error", err)
	}
	if name != "" {
		t.Errorf("retired %q, want nothing named", name)
	}
	// And it must not have enrolled the key in order to answer.
	if after := len(h.srv.Store.List()); after != before {
		t.Errorf("route table went from %d to %d instances; retire enrolled the key", before, after)
	}
}

// The kill switch outranks the member: self-release plus re-enrolment would
// turn a suspension into a new hostname a minute later.
func TestASuspendedInstanceCannotRetireItself(t *testing.T) {
	h := newHarness(t)
	in := h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if err := h.srv.Store.SetDisabled(in.Name, true); err != nil {
		t.Fatalf("disable: %v", err)
	}

	_, err := h.retirer(h.client.Token).Retire(context.Background())
	if err == nil {
		t.Fatal("a suspended instance retired itself")
	}
	if !strings.Contains(err.Error(), "suspended") {
		t.Errorf("error = %v, want it to say the instance is suspended", err)
	}
	if _, ok := h.srv.Store.Get(in.Name); !ok {
		t.Errorf("%s was deleted despite being suspended", in.Name)
	}
}

// Retiring works with no tunnel up, because that is when it is used.
func TestRetireNeedsNoTunnel(t *testing.T) {
	h := newHarness(t)
	key := NewKey()
	if _, _, err := h.srv.Store.Enroll(key, 0); err != nil {
		t.Fatalf("enrol: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.retirer(key).Retire(ctx); err != nil {
		t.Fatalf("retire without a tunnel: %v", err)
	}
	if _, ok := h.srv.Store.Resolve(key); ok {
		t.Error("the instance survived")
	}
}

// The configured ingress address is usually a bare hostname — DefaultIngress is
// `ingress.tacit.zone` — and a bare hostname has no scheme and no port. Every
// caller that dials one has to normalize it first; the release path did not,
// and failed against the real ingress with "missing port in address" while
// passing against a test one, whose address is written with a scheme.
func TestNormalizeAddrMakesAConfiguredHostDialable(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ingress.tacit.zone", "https://ingress.tacit.zone"},
		{"https://ingress.tacit.zone/", "https://ingress.tacit.zone"},
		{"http://127.0.0.1:19453", "http://127.0.0.1:19453"},
		{"127.0.0.1:19453", "http://127.0.0.1:19453"},
		{"localhost:8443", "http://localhost:8443"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeAddr(c.in); got != c.want {
			t.Errorf("NormalizeAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A normalized address is a URL, which means the tunnel arrives as an HTTP
// upgrade on the ingress's ordinary port rather than as a raw connection to the
// tunnel listener. That is the shape a real deployment uses — one port, shared
// with whatever else answers on it — so the release has to work through it.
func TestRetireWorksThroughTheUpgradeAddressAConfigHasNormalized(t *testing.T) {
	h := newHarness(t)
	key := NewKey()
	if _, _, err := h.srv.Store.Enroll(key, 0); err != nil {
		t.Fatalf("enrol: %v", err)
	}
	addr := NormalizeAddr(strings.TrimPrefix(h.public.URL, "http://"))
	if addr != h.public.URL {
		t.Fatalf("NormalizeAddr gave %q for a loopback address, want %q", addr, h.public.URL)
	}
	c := &Client{Addr: addr, Token: key, Version: "test-build"}
	if _, err := c.Retire(context.Background()); err != nil {
		t.Fatalf("retire through the upgrade address: %v", err)
	}
	if _, ok := h.srv.Store.Resolve(key); ok {
		t.Error("the instance survived")
	}
}
