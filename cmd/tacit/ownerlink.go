// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// Where an owner sign-in link points.
//
// The link is a signed token with an expiry and nothing else in it (web.OwnerLink):
// it is not bound to a host, and it works from any address that reaches the
// registry. Every command that printed one nevertheless hard-coded
// http://127.0.0.1, which meant an operator who set a registry up over SSH — the
// ordinary case for a server — was handed a link only openable on the machine
// they were not sitting at. `tacit init` made this plainer than it had to: it
// printed the registry at its hostname and the sign-in link at loopback, three
// lines apart, and neither said why they differed.
//
// So the address is chosen rather than assumed, most reachable first, and a
// name that does not resolve is never printed: a dead hostname is worse than
// loopback, because loopback at least works somewhere.

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

// ownerLinkBase is the base URL to mint an owner link against, and how to
// describe where it works. public is the address the ingress allocated, when
// the caller already knows it ("" otherwise).
func ownerLinkBase(cfg registryconfig.Config, port int, public string) (base, where string) {
	if port == 0 {
		port = cfg.Port
	}
	if public != "" {
		return strings.TrimRight(public, "/"), "from anywhere"
	}
	if ext := strings.TrimRight(cfg.ExternalURL, "/"); ext != "" {
		return ext, "at this registry's own address"
	}
	if host := resolvableHostname(); host != "" {
		return fmt.Sprintf("http://%s:%d%s", host, port, cfg.BasePath), "from your network"
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, cfg.BasePath), "on this machine"
}

// resolvableHostname is this machine's name, when it is a name something else
// could look up. Empty otherwise.
//
// The check is the point. `tacit init` already printed the bare hostname as the
// registry's address without ever asking whether it resolves, and on a host
// whose name is only in its own /etc/hostname that address is fiction. A lookup
// that answers with a non-loopback address is the cheapest available proof that
// a colleague's laptop could follow the same name.
func resolvableHostname() string {
	host, err := os.Hostname()
	if err != nil || host == "" || host == "localhost" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
			return host
		}
	}
	return ""
}
