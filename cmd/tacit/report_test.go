// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"flag"
	"io"
	"strings"
	"testing"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
)

// The prefixes are product voice and their widths are the point: every outcome
// is seven characters wide, so nothing after them shifts down the page.
func TestReportPrefixesAreTheProductVoice(t *testing.T) {
	var b bytes.Buffer
	c := &report{w: &b}
	c.ok("fine")
	c.info("a fact")
	c.todo("your move")
	c.warn("survivable")
	c.fail("not fine")

	want := []string{
		"   ok  fine",
		"    -  a fact",
		" todo  your move",
		" warn  survivable",
		" FAIL  not fine",
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("printed %d lines, want %d: %q", len(lines), len(want), b.String())
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
		if len(lines[i]) < 7 || lines[i][:7] != w[:7] {
			t.Errorf("line %d prefix = %q, want %q", i, lines[i][:min(7, len(lines[i]))], w[:7])
		}
	}
	if c.fails != 1 || c.warns != 1 || c.todos != 1 {
		t.Errorf("counts = %d fails, %d warns, %d todos; want one of each", c.fails, c.warns, c.todos)
	}
}

// connect and doctor used to carry two copies of this printer under two names.
// They are the same type now, so a change to the voice cannot reach one and
// miss the other.
func TestConnectAndDoctorShareOnePrinter(t *testing.T) {
	var a, b bytes.Buffer
	(&connectReport{w: &a}).ok("same")
	(&checker{w: &b}).ok("same")
	if a.String() != b.String() {
		t.Errorf("connectReport printed %q, checker printed %q", a.String(), b.String())
	}
}

// A bad flag used to take the process down from inside the flag package, which
// is why no test like this existed. Under ContinueOnError it returns through
// run() instead.
func TestABadFlagReportsUsageInsteadOfExiting(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("known", "", "a known flag")

	if _, ok := parseFlags(fs, []string{"--nonexistent"}); ok {
		t.Error("parseFlags accepted an unknown flag")
	}

	fs2 := flag.NewFlagSet("test", flag.ContinueOnError)
	fs2.SetOutput(io.Discard)
	known := fs2.String("known", "", "a known flag")
	pos, ok := parseFlags(fs2, []string{"one", "--known", "v", "two"})
	if !ok {
		t.Fatal("parseFlags rejected valid input")
	}
	if *known != "v" {
		t.Errorf("known = %q, want v", *known)
	}
	// Flags and positionals interleave, argparse-style, so `tacit audit - --text`
	// works.
	if strings.Join(pos, ",") != "one,two" {
		t.Errorf("positionals = %v, want [one two]", pos)
	}
}

// A generated registry key can begin with "-". The settings flow no longer
// round-trips values through a flag parser, so nothing can interpret one.
// (Checked before changing it: the flag package took a dash-prefixed value
// as the value, so this was not broken — the round trip was just pointless.)
func TestAKeyBeginningWithADashSurvives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Environment must not mask what we write, since Load prefers it.
	t.Setenv("TACIT_REGISTRY_URL", "")
	t.Setenv("TACIT_API_KEY", "")

	// The settings are written before reachability is checked, so the exit code
	// here reflects only that the fake registry does not answer. What matters is
	// what landed in agent.env.
	const dashKey = "-Zx9rT0pQw"
	applyMemberSettings(memberSettings{registryURL: "http://127.0.0.1:1/", key: dashKey})

	vals := auditorconfig.ReadEnvFile(auditorconfig.AgentEnvPath(home))
	if got := vals["TACIT_API_KEY"]; got != dashKey {
		t.Errorf("saved key = %q, want %q — a leading dash was eaten", got, dashKey)
	}
}
