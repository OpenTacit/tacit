// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit pause / tacit resume — the off switch that is not an uninstall.
//
// A reversible trial is easier to accept than one whose only exit is removal
// (docs/distribution/first-adoption-plan.md, Phase 2). Somebody pairing, or in
// a demo, or working on something they would rather nothing looked at, needs a
// way to stop being observed for an hour without unwiring five tools and
// losing their settings.
//
// Pause means WATCH NOTHING: the hook agent returns on every event before it
// captures, retrieves or delivers. Not "suggest less" and not "stay quiet" —
// a pause the member cannot verify is not a pause.
//
// Asking still works. `tacit ask` and the MCP tools never go through the hook
// path, and somebody who paused the interruptions has not asked to lose the
// playbook.

import (
	"flag"
	"fmt"
	"os"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
)

func cmdPause(args []string) int {
	fs := flag.NewFlagSet("pause", flag.ContinueOnError)
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}
	cfg := auditorconfig.Load()
	if cfg.PausePath == "" {
		fmt.Fprintln(os.Stderr, "no home directory, so there is nowhere to record a pause")
		return 1
	}
	// The file's content is for the member reading it later, wondering what
	// this is and whether they can delete it. Its EXISTENCE is the signal.
	body := fmt.Sprintf("paused %s\nDelete this file, or run `%s resume`, to start again.\n",
		time.Now().Format(time.RFC3339), selfCommand())
	if err := os.WriteFile(cfg.PausePath, []byte(body), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write %s: %v\n", cfg.PausePath, err)
		return 1
	}
	fmt.Printf("paused. nothing on this machine is observed: no capture, no suggestions.\n")
	fmt.Printf("it takes hold on your next turn — no restart, and nothing was unwired.\n")
	fmt.Printf("\nasking still works:  %s ask \"what you are working on\"\n", selfCommand())
	fmt.Printf("start again with:    %s resume\n", selfCommand())
	return 0
}

func cmdResume(args []string) int {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}
	cfg := auditorconfig.Load()
	if !auditorconfig.Paused(cfg.PausePath) {
		fmt.Println("not paused — this machine is already being observed as normal.")
		return 0
	}
	if err := os.Remove(cfg.PausePath); err != nil {
		fmt.Fprintf(os.Stderr, "cannot remove %s: %v\n", cfg.PausePath, err)
		return 1
	}
	fmt.Println("resumed. suggestions can reach you again from your next turn.")
	return 0
}
