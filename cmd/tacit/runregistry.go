// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The pieces shared by the commands that start a registry, or have to find one.
//
// They lived beside the command that used to set a registry up and then serve
// it. `tacit init` does that now,
// and `tacit merge` and `tacit dashboard` probe the same registry, so these
// belong beside no one command.

package main

import (
	"fmt"
	"net"
	"os"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/web"
)

// portFreeWithin waits for a port to become bindable, up to a budget.
//
// A kill returns before the socket does. `serve` used to reach its listener
// only after opening a store and loading an embedder, which gave a restart
// script a second or two of slack it never knew it was using; checking the port
// up front took that away and turned "stop it and start it again" into a race.
// So the check waits for the port rather than refusing the moment it is busy.
// It is only reached when nothing recognisable is answering there — a registry
// that IS answering is refused at once, because waiting for that one would be
// waiting for a process nobody is going to stop.
func portFreeWithin(host string, port int, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if portFree(host, port) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// portFree reports whether this run could bind the port, by trying it. An HTTP
// probe cannot answer the question: a listener that is not a registry answers
// nothing recognisable, which is exactly the case this exists for.
func portFree(host string, port int) bool {
	if host == "" {
		host = "0.0.0.0"
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// announceOwnerLink waits for the registry this process is starting, then says
// where to sign in.
//
// It runs beside the server rather than before it because there is nothing to
// wait for until the server is listening, and it prints once: a command that
// repeated the link every minute would be a command nobody could read the log
// of.
func announceOwnerLink(cfg registryconfig.Config, portFlag int) {
	port := cfg.Port
	if portFlag != 0 {
		port = portFlag
	}
	local := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Long enough for a cold start (an embedder load, a large event log) and
	// then a tunnel dial; short enough that a registry which never comes up
	// does not leave somebody waiting for a line that is not coming.
	if !waitUntilAnswering(local, 60*time.Second) {
		return
	}
	secret := cfg.OwnerSecret
	if secret == "" { // written by init in this same process
		secret = auditorconfig.ReadEnvFile(registryconfig.RegistryEnvPath())["TACIT_OWNER_SECRET"]
	}
	if secret == "" {
		return
	}

	// The way in goes out NOW, at whatever address is already known.
	//
	// This used to wait for the ingress first — up to forty-five seconds of a
	// registry that is up, serving, and showing the operator no way to open it,
	// which is exactly the wall the public address exists to remove. A local
	// link that works immediately beats a public one that arrives later, and
	// the public one can follow on its own line when it lands.
	base, where := ownerLinkBase(cfg, port, "")
	// ONE BLANK LINE UNDER THE ANNOUNCEMENT, whichever of the four lines below
	// turns out to be the last: this is the end of what `tacit init` says, and
	// the registry's own output follows it in the same terminal. Deferred
	// rather than appended, so a branch added later cannot forget it. Below the
	// two early returns above, which print nothing to close.
	defer fmt.Fprintln(os.Stdout)
	// The third line is the way back. Fifteen minutes is short enough that
	// somebody who steps away needs to know how to get another, and dropping it
	// to save a line would trade the one recovery path for nothing.
	fmt.Fprintf(os.Stdout, "\nsign in as the owner (%s, good for 15 minutes):\n  %s\n  a fresh one any time:  %s dashboard\n",
		where, web.OwnerLink(base, secret), selfCommand())

	if !cfg.GlobalAccess {
		return
	}
	public, refusal := awaitPublicURLReason(local, 45*time.Second)
	switch {
	case refusal != "":
		// The ingress said no, and said why. Repeating its sentence beats
		// inventing a friendlier one that sends somebody looking in the wrong
		// place — an enrolment ceiling is not a network problem.
		// The ingress's own sentence, once. It already names itself and the
		// reason; a prefix of ours only says the same thing twice.
		fmt.Fprintf(os.Stdout, "\nno public address — %s\nthe link above still works; `%s dashboard` prints a public one if that clears.\n",
			refusal, selfCommand())
		return
	case public == "":
		fmt.Fprintf(os.Stdout, "\nno public address yet — the link above still works; `%s dashboard` prints a public one once the tunnel is up.\n",
			selfCommand())
		return
	}
	fmt.Fprintf(os.Stdout, "\nfrom anywhere:\n  %s\n", web.OwnerLink(public, secret))
}

// waitUntilAnswering reports whether the registry came up inside the budget.
func waitUntilAnswering(base string, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if registryRunning(base) {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}
