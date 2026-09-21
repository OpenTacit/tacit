// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/windows"
)

// The payload must carry every window, because a member switching period on the
// dashboard is reading a blob that was sealed hours ago by a machine that may
// now be asleep. Re-publishing on demand is not available to them.
func TestPublishedPayloadCarriesEveryWindow(t *testing.T) {
	var got []byte
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline,
			PublishLedger: func(payload []byte) error { got = payload; return nil }})
	driveToSuggestion(agent)

	if err := agent.PublishLedger(); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Schema  int    `json:"schema"`
		Machine string `json:"machine"`
		Windows map[string]struct {
			Usage map[string]any `json:"usage"`
			Work  map[string]any `json:"work"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatal(err)
	}
	if p.Schema != LedgerSchema {
		t.Errorf("schema = %d, want %d", p.Schema, LedgerSchema)
	}
	if p.Machine == "" {
		t.Error("the payload does not say which machine it came from")
	}
	for _, k := range windows.Keys {
		w, ok := p.Windows[k]
		if !ok {
			t.Errorf("window %s missing from the payload", k)
			continue
		}
		if w.Usage == nil || w.Work == nil {
			t.Errorf("window %s carries only one of the two summaries", k)
		}
	}
}

// A machine that never connected has no member credential, so wiring it a sink
// would be wiring every unconnected machine to one shared address. The agent
// must do nothing at all rather than publish under a derived-from-nothing key.
func TestNoSinkMeansNoPublish(t *testing.T) {
	agent, _ := newTestAgent(t, []contracts.EvidenceCandidate{orgCandidate()},
		Options{MaxPerWindow: 5, CooldownTurns: -1, CooldownFor: -1, RunAsync: inline})
	if err := agent.PublishLedger(); err != nil {
		t.Fatalf("publishing with no sink should be a no-op, got %v", err)
	}
}

// The label becomes a filename on the registry and a name the member reads.
// Whatever the host is called, it has to come out as one safe segment.
func TestMachineLabelIsSafeAsAPathSegment(t *testing.T) {
	got := MachineLabel()
	if got == "" {
		t.Fatal("no label")
	}
	if strings.ContainsAny(got, "/\\ ") || strings.HasPrefix(got, ".") {
		t.Errorf("MachineLabel() = %q, which is not safe as a path segment", got)
	}
	if len(got) > 63 {
		t.Errorf("MachineLabel() = %q, too long", got)
	}
}

// Two things build the sealed payload now: the running agent, from the logs it
// holds, and `tacit usage --publish`, from the same logs on disk. They must
// produce the same thing, or a publish forced after a deploy would show the
// member something the daemon never would.
func TestForcedPublishAgreesWithTheDaemon(t *testing.T) {
	dir := t.TempDir()
	usagePath := filepath.Join(dir, "usage.jsonl")
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return at }

	// Write through the same log the agent would, so both readers see one file.
	// append persists each event as it goes, so there is nothing to flush.
	log := loadUsageLog(usagePath, now)
	log.append(usageEvent{Kind: usageQuery, Harness: "claude-code"})
	log.append(usageEvent{Kind: usageShown, Cap: "tq-1", Name: "A technique"})
	log.append(usageEvent{Kind: usageAdopted, Cap: "tq-1"})

	fromFile, err := LedgerPayload(FileSummarizer{
		UsageLogPath: usagePath, StateDir: dir, Now: now,
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Schema  int `json:"schema"`
		Windows map[string]struct {
			Usage struct {
				Totals map[string]int `json:"totals"`
			} `json:"usage"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(fromFile, &p); err != nil {
		t.Fatal(err)
	}
	if p.Schema != LedgerSchema {
		t.Errorf("schema = %d", p.Schema)
	}
	for _, k := range windows.Keys {
		if _, ok := p.Windows[k]; !ok {
			t.Errorf("window %s missing from a forced publish", k)
		}
	}
	// The numbers are the log's, not zeroes from a reader that opened nothing.
	if got := p.Windows["30d"].Usage.Totals["queries"]; got != 1 {
		t.Errorf("queries = %d, want 1 — the file reader is not seeing the log", got)
	}
	if got := p.Windows["30d"].Usage.Totals["adopted"]; got != 1 {
		t.Errorf("adopted = %d, want 1", got)
	}
}
