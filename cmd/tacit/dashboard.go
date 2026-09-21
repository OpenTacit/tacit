// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// `tacit dashboard`: a fresh sign-in link for the registry's owner.
//
// Single-member mode has no identity provider and no password to remember. What
// it has is this machine: the owner secret sits beside the instance key in the
// config directory, and being able to read it IS the proof of ownership — the
// same proof the first-run claim code asks for.
//
// So signing in is a command rather than a form. It mints a short-lived link
// and prints it; the browser exchanges it for a session.

package main

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/web"
)

func cmdDashboard(args []string) int {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	local := fs.Bool("local", false, "link to this machine's address even when the registry has a public one")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}
	cfg := registryconfig.Load()
	c := &report{}

	if !cfg.OwnerEnabled() {
		if cfg.OIDCEnabled() {
			c.info("This registry signs members in with your identity provider, so there is no owner link.")
			c.info("Open %s and sign in there.", registryBase(cfg))
			return 0
		}
		c.fail("This registry has no owner sign-in configured.")
		c.info("Turn it on with: tacit init --owner on")
		return 1
	}

	base, public := registryBase(cfg), ""
	if !*local {
		public = livePublicBase(cfg)
		if public != "" {
			base = public
		}
	}
	c.ok("Sign in as the owner — good for 15 minutes:")
	fmt.Printf("\n  %s\n\n", web.OwnerLink(base, cfg.OwnerSecret))

	// Which address this is, and why, said plainly. A link to 127.0.0.1 handed
	// over without comment is one somebody mails to their phone and cannot open.
	switch {
	case public != "":
		c.info("That address is this registry's own, allocated by %s. It works from any device.", cfg.PublishIngress)
	case *local:
		c.info("This machine's address, because you asked for it. It works here only.")
	case !cfg.GlobalAccess:
		c.info("Global Access is off, so this registry has no public address.")
		c.info("Turn it on in Settings and the link becomes one you can open anywhere.")
	default:
		c.info("This machine's address. Global Access is on, but the registry is not reporting a")
		c.info("public one yet — it is either not running or has not finished dialling %s.", cfg.PublishIngress)
	}
	c.info("The link carries the session. Anyone who has it has this registry until it expires,")
	c.info("so paste it into a browser rather than into a chat.")
	return 0
}

// registryBase is the address to sign in at, most reachable first
// (ownerlink.go). Loopback is the floor, not the default: an operator who set
// this registry up over SSH cannot open a link to the machine's own loopback.
func registryBase(cfg registryconfig.Config) string {
	base, _ := ownerLinkBase(cfg, 0, "")
	return base
}

// livePublicBase asks the running registry for the address the ingress
// allocated it. Empty when it is not running or not published, which is not an
// error: the local link works from this machine either way.
func livePublicBase(cfg registryconfig.Config) string {
	if !cfg.GlobalAccess {
		return ""
	}
	health, err := fetchHealth(&http.Client{Timeout: 3 * time.Second},
		fmt.Sprintf("http://127.0.0.1:%d", cfg.Port))
	if err != nil {
		return ""
	}
	return strings.TrimRight(health.ExternalURL, "/")
}
