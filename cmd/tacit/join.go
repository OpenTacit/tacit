// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit invite / tacit join — the member ends of the join loop
// (docs/distribution/growth-plan.md mechanism 1). invite turns "here's the
// org key, keep it safe" into a line anyone can paste in chat; join is what
// that line ultimately runs.

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/product"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	pkgclient "github.com/opentacit/tacit/pkg/client"
)

func cmdInvite(args []string) int {
	cfg := auditorconfig.Load()
	// The operator running this has registry.env and usually no agent.env, so
	// the member-resolved pair is the compiled-in default and every call was a
	// 401. adminCreds falls back to this host's own registry (localadmin.go);
	// an explicit --registry/--key still overrides, because a flag is an answer.
	defURL, defKey, fromLocal := adminCreds(cfg.RegistryURL, cfg.RegistryKey)
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	ttl := fs.Duration("ttl", 24*time.Hour, "how long the link stays valid (max 168h)")
	registryURL := fs.String("registry", defURL, "registry URL")
	key := fs.String("key", defKey, "registry API key")
	repo := fs.Bool("repo", false, "also write .tacit/registry.toml (URL only, never a key) so this repo itself invites teammates")
	force := fs.Bool("force", false, "mint the link even when its address only works on this machine")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}
	if fromLocal && *registryURL == defURL {
		fmt.Printf("using this machine's registry (%s) and the key in %s\n\n",
			defURL, registryconfig.RegistryEnvPath())
	}

	// --repo needs no token and no registry round-trip: the marker carries
	// only the URL, and the harness plugins nudge unconnected teammates who
	// open the repo (growth-plan.md mechanism 2 — the invitation spreads with
	// ordinary git workflow).
	if *repo {
		if err := writeRepoMarker(*registryURL); err != nil {
			fmt.Fprintf(os.Stderr, "write repo marker: %v\n", err)
			return 1
		}
	}

	reg := &pkgclient.Registry{BaseURL: *registryURL, APIKey: *key,
		HTTP: &http.Client{Timeout: 10 * time.Second}}
	joinURL, expiresAt, err := reg.Invite(*ttl)
	var api *pkgclient.APIError
	switch {
	case errors.As(err, &api):
		fmt.Fprintf(os.Stderr, "invite failed (%s): %s\n", statusLine(api.Status), api.Message())
		return exitUnreachable
	case err != nil:
		fmt.Fprintf(os.Stderr, "cannot reach the registry at %s: %v\n", *registryURL, err)
		return exitUnreachable
	case joinURL == "":
		fmt.Fprintln(os.Stderr, "invite failed: the registry answered without a join link")
		return exitUnreachable
	}
	// A join link is a line to paste into chat. When its address only resolves
	// on this machine, that sentence is false and nothing downstream ever says
	// so: the teammate gets a connection refused, minutes after being told the
	// line installs OpenTacit for them. Refuse here, where the operator can still
	// act on it, and name the two roads to a reachable address.
	if !*force && !reachableJoinBase(joinURL) {
		fmt.Fprintf(os.Stderr, "not minting: %s only resolves on this machine, so the link would fail for everybody else.\n\n", joinURL)
		fmt.Fprintf(os.Stderr, "give this registry an address your colleagues can reach, either:\n")
		fmt.Fprintf(os.Stderr, "  behind your own proxy   set TACIT_EXTERNAL_URL=https://tacit.example.com and restart\n")
		fmt.Fprintf(os.Stderr, "  through the shared one   %s init --global-access on\n\n", selfCommand())
		fmt.Fprintf(os.Stderr, "to mint it anyway (testing on this machine):  %s invite --force\n", selfCommand())
		return 1
	}
	fmt.Printf("send a teammate this line — it installs tacit, joins this registry, and wires their harnesses:\n\n")
	fmt.Printf("  curl -fsSL %s | sh\n\n", joinURL)
	fmt.Printf("if you already have tacit, run:  tacit join %s\n", joinURL)
	fmt.Printf("expires %s; anyone with the link can join, so treat it as a secret\n", expiresAt)
	return 0
}

// reachableJoinBase reports whether a join link names an address somebody else
// could open. Loopback and the bare machine name are the two the registry hands
// out before anybody has configured an external URL, and neither travels.
func reachableJoinBase(joinURL string) bool {
	u, err := url.Parse(joinURL)
	if err != nil {
		return true // unparseable is not our judgment to make; let the operator see it
	}
	host := u.Hostname()
	switch strings.ToLower(host) {
	case "", "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsUnspecified()
	}
	// A single-label name (the machine's hostname) resolves inside one network
	// at best and is what `tacit init` prints when nothing else is configured.
	return strings.Contains(host, ".")
}

// statusLine renders a status code the way a response's status line carried it
// ("401 Unauthorized"), so messages that used to quote the response read the
// same now that the typed client keeps the response to itself.
func statusLine(code int) string {
	return fmt.Sprintf("%d %s", code, http.StatusText(code))
}

// writeRepoMarker drops .tacit/registry.toml at the repo root (the git
// toplevel when there is one, else the working directory). URL only — a
// checked-in file must never carry a credential; joining still requires a
// join link or a minted key.
func writeRepoMarker(registryURL string) error {
	root := "."
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			root = top
		}
	}
	dir := filepath.Join(root, ".tacit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "registry.toml")
	content := fmt.Sprintf(`# This repository's organization runs %s (its AI playbook).
# This file is an invitation, not a credential: there is no key here.
# To connect, ask a teammate for a join link (they run: tacit invite),
# or see %s
registry = %q
`, product.Name(), registryURL, registryURL)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s — commit it and every supported harness will point unconnected teammates here\n\n", path)
	return nil
}

func cmdJoin(args []string) int {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	positionals, ok := parseFlags(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positionals) < 1 {
		fmt.Fprintln(os.Stderr, "usage: tacit join <join-url>   (get one from a colleague: tacit invite)")
		return 2
	}
	joinURL := strings.TrimSpace(positionals[0])
	base, token, ok := strings.Cut(joinURL, "/join/")
	if !ok || token == "" {
		fmt.Fprintf(os.Stderr, "this is not a join link (the format is .../join/<token>): %s\n", joinURL)
		return 2
	}

	// The label is what the admin sees on the Members page — name the machine.
	label := ""
	if u := os.Getenv("USER"); u != "" {
		label = u
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		if label != "" {
			label += "@"
		}
		label += host
	}
	// The token is the credential here, so this call carries no key.
	reg := &pkgclient.Registry{BaseURL: base, HTTP: &http.Client{Timeout: 10 * time.Second}}
	memberURL, memberKey, err := reg.JoinExchange(token, label)
	var api *pkgclient.APIError
	switch {
	case errors.As(err, &api):
		fmt.Fprintf(os.Stderr, "join failed (%s): %s — an expired link is a frequent cause; ask for a new one\n", statusLine(api.Status), api.Message())
		return exitUnreachable
	case err != nil:
		fmt.Fprintf(os.Stderr, "cannot reach the registry at %s: %v\n", base, err)
		return exitUnreachable
	case memberKey == "":
		fmt.Fprintln(os.Stderr, "join failed: the registry answered without a key — an expired link is a frequent cause; ask for a new one")
		return exitUnreachable
	}

	// From here it is the standard flows: setup writes+verifies agent.env,
	// connect wires the harnesses.
	if rc := applyMemberSettings(memberSettings{registryURL: memberURL, key: memberKey}); rc != 0 {
		return rc
	}
	if rc := cmdConnect(nil); rc != 0 {
		return rc
	}
	// Everything above join could derive; the cohort is the one thing only the
	// member knows, and the one thing they will get wrong if they answer it
	// from nothing. So the join ends by showing what their colleagues already
	// call themselves.
	offerCohorts(memberURL, memberKey)
	return 0
}
