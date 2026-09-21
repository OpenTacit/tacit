// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit doctor --ready — the one check, in place of three.
//
// The join guide asked a new member for `tacit doctor`, then an in-session
// status check, then an end-to-end test that wanted another prompt from them
// (docs/distribution/first-adoption-plan.md, 1.3). Three commands to answer one
// question, and each one printed a report the member had no way to read: hop
// names, agent stats, a registry state machine. A person who has just wired
// their laptop wants a yes or a next step.
//
// So the checks are the same checks — the four hops doctor already walks — and
// what changes is that they run quietly. Success is a sentence. Failure prints
// the whole report, because at that point the detail IS the answer.
//
// It stops short of claiming delivery. Everything here proves the hook path
// carries an event and the agent can reach the playbook; whether a suggestion
// RENDERS in the member's client is the one hop no command can prove from
// outside a live session, and `--deliver` is where that is settled. Saying
// "ready" about the parts we tested, and naming the part we did not, is the
// honest shape.

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/hooks"
)

func runReady(harnessName string) int {
	acfg := auditorconfig.Load()
	if acfg.RegistryURL == "" || acfg.RegistryKey == auditorconfig.DefaultRegistryKey {
		fmt.Println("not connected yet — this machine points at no playbook.")
		fmt.Printf("  %s connect --registry <url> --code <code>   (the code is on your invitation page)\n", selfCommand())
		return exitUnreachable
	}

	name, code := resolveReadyHarness(harnessName)
	if code != 0 {
		return code
	}

	// The same hops, into a buffer. A member who is fine never reads them.
	var buf bytes.Buffer
	c := &checker{w: &buf}
	doctorHarness(c, &http.Client{Timeout: 5 * time.Second}, name, acfg)

	if c.fails > 0 {
		fmt.Printf("%s cannot reach your playbook yet.\n", name)
		fmt.Print(buf.String())
		fmt.Println("\nthe first FAIL above is the one to fix; the hops after it depend on it.")
		return 1
	}

	fmt.Printf("Ready: %s reaches your playbook at %s.\n", name, acfg.RegistryURL)
	// A paused machine passes every hop and shows nothing. Without this line
	// the member reads a green check and then waits for suggestions that were
	// switched off on purpose — the exact ambiguity this command exists to end.
	if auditorconfig.Paused(acfg.PausePath) {
		fmt.Printf("\nbut this machine is PAUSED, so nothing is observed and nothing will be\n")
		fmt.Printf("suggested. start again with:  %s resume\n", selfCommand())
		return 0
	}
	if c.warns > 0 {
		fmt.Print("\n" + warnLines(buf.String()))
	}
	if cap := hooks.Capability(name); cap != "" {
		fmt.Printf("in %s, it %s.\n", name, cap)
	}
	// The hop no command can walk from out here.
	fmt.Printf("\nthat covers the wiring, the agent and the registry. whether a suggestion\n")
	fmt.Printf("appears where you sit is the one thing only a live session shows:\n")
	fmt.Printf("  %s doctor --deliver --harness %s   then start a session\n", selfCommand(), name)
	fmt.Printf("\nor just use it:  %s ask \"what you are working on\"\n", selfCommand())
	return 0
}

// resolveReadyHarness picks the tool to check, and asks only when it must.
//
// One installed tool is the common case and needs no question. Several is a
// real ambiguity — checking the wrong one would report a working machine to a
// member whose actual tool is broken — so it names them and stops.
func resolveReadyHarness(explicit string) (string, int) {
	if explicit != "" {
		if _, known := findHarness(explicit); !known {
			fmt.Fprintf(os.Stderr, "unknown harness %q (%s)\n", explicit, harnessNames())
			return "", exitUsage
		}
		return explicit, 0
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "no home directory: %v\n", err)
		return "", 1
	}
	var found []string
	for _, h := range harnesses {
		if h.installed(home) {
			found = append(found, h.name)
		}
	}
	switch len(found) {
	case 0:
		fmt.Println("no AI tool found on this machine, so there is no hook path to check.")
		fmt.Printf("  %s connect --harness <name>   wires one anyway (%s)\n", selfCommand(), harnessNames())
		return "", 1
	case 1:
		return found[0], 0
	default:
		fmt.Printf("several AI tools are installed here: %s\n", strings.Join(found, ", "))
		fmt.Printf("check the one you work in:  %s doctor --ready --harness <name>\n", selfCommand())
		return "", exitUsage
	}
}

// warnLines keeps the warnings out of a report nobody else reads.
//
// A warning is survivable and still worth one line — a model key that is not
// set, say. Printing the whole hop report to carry it would put the member back
// in front of the wall of detail this command exists to spare them.
func warnLines(report string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(report, "\n") {
		if strings.HasPrefix(line, " warn  ") {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
