// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// The org salt is optional and has no default, so on most machines it is
// empty. sessionhash.Hash returns "" for an empty salt — deliberately, because
// an audit fact that LEAVES the machine must carry no pseudonym rather than a
// weak one — and upsert refuses a record with no key. The whole member-local
// session log was therefore empty on any machine that had never been given an
// org salt, and nothing anywhere said so.
func TestTheLocalLogRecordsWithoutAnOrgSalt(t *testing.T) {
	dir := t.TempDir()
	quiet := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, nil
	}
	opts := Options{StateDir: dir, RunAsync: inline,
		UsageLogPath: filepath.Join(dir, "usage.jsonl"),
		Segment:      contracts.Segment{"team": "revops"}}
	// No SessionSalt at all: the state most machines are in.
	agent := NewAgent(quiet, stubLLM{}, nil, nil, opts)
	agent.Handle(ev("UserPromptSubmit", map[string]any{
		"prompt": "run the tests", "model": "claude-opus-5", "cwd": "/home/x/tacit"}), "claude-code")
	agent.Handle(ev("PostToolUse", map[string]any{"tool_name": "Bash",
		"tool_input": map[string]any{"command": "go test ./..."}}), "claude-code")
	agent.Handle(ev("Stop", nil), "claude-code")

	sum := agent.WorkSummary(0)
	if sum.Totals.Sessions != 1 || sum.Totals.Turns != 1 {
		t.Fatalf("a machine with no org salt recorded nothing: %+v", sum.Totals)
	}
	// The salt is kept beside the log it keys, so clearing the state directory
	// clears both.
	if _, err := os.Stat(filepath.Join(dir, localSaltFile)); err != nil {
		t.Fatalf("the local salt was not persisted: %v", err)
	}
}

// It has to be the SAME salt next time, or every restart files the same
// session under a new key and a day of work reads as a day of one-turn
// sessions.
func TestTheLocalSaltIsStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	first := resolveLocalSalt("", dir)
	second := resolveLocalSalt("", dir)
	if first == "" || first != second {
		t.Fatalf("the local salt moved between reads: %q then %q", first, second)
	}
	if other := resolveLocalSalt("", t.TempDir()); other == first {
		t.Error("two machines got the same salt; it is meant to be this machine's")
	}
}

// Where an org salt IS set it still wins, which keeps a machine's existing
// records readable and keeps a member's local keys matching the ones their
// org-scoped facts carried.
func TestAnOrgSaltStillWins(t *testing.T) {
	dir := t.TempDir()
	if got := resolveLocalSalt("org-salt", dir); got != "org-salt" {
		t.Fatalf("local salt = %q, want the org's", got)
	}
	// And it does not leave a local salt behind that a later run would prefer.
	if _, err := os.Stat(filepath.Join(dir, localSaltFile)); err == nil {
		t.Error("an org-salted machine wrote a local salt it will never use")
	}
}

// No state directory is a machine that keeps no log, and it still has to work
// rather than crash: the salt lives for the process and the records live in
// memory, which is exactly what every other file here does without a home.
func TestNoStateDirectoryStillGetsASalt(t *testing.T) {
	if got := resolveLocalSalt("", ""); got == "" {
		t.Fatal("a homeless agent got no salt, so it would record nothing")
	}
}

// The keys are still pseudonyms. Whatever salts it, a session id must not
// survive into the log.
func TestTheLocalKeyIsStillAHash(t *testing.T) {
	dir := t.TempDir()
	quiet := func(contracts.Characterization) (contracts.EvidenceBlock, error) {
		return contracts.EvidenceBlock{}, nil
	}
	agent := NewAgent(quiet, stubLLM{}, nil, nil, Options{StateDir: dir, RunAsync: inline,
		UsageLogPath: filepath.Join(dir, "usage.jsonl"), Segment: contracts.Segment{}})
	payload := ev("UserPromptSubmit", map[string]any{"prompt": "x", "cwd": "/home/x/tacit"})
	payload["session_id"] = "SECRETSESSIONID"
	agent.Handle(payload, "claude-code")
	agent.Handle(ev2("Stop", "SECRETSESSIONID"), "claude-code")
	agent.Flush()

	raw, err := os.ReadFile(filepath.Join(dir, "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("nothing was recorded")
	}
	if strings.Contains(string(raw), "SECRETSESSIONID") {
		t.Fatalf("the session id reached the log: %s", raw)
	}
}

func ev2(name, session string) map[string]any {
	p := ev(name, nil)
	p["session_id"] = session
	return p
}
