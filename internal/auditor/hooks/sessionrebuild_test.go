// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/sessionhash"
)

// writeTranscript lays out one Claude Code transcript where the rebuild looks
// for it: a file per session, named by the session id.
func writeTranscript(t *testing.T, home, sid string, calls []map[string]any) {
	t.Helper()
	dir := filepath.Join(transcriptRoot(home), "-home-x-repo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, c := range calls {
		line, err := json.Marshal(map[string]any{
			"type": "assistant",
			"message": map[string]any{"content": []any{map[string]any{
				"type": "tool_use", "name": c["name"], "input": c["input"]}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		body = append(body, line...)
		body = append(body, '\n')
	}
	// A line that is not a tool call, and one that is not even JSON: neither
	// may stop the read.
	body = append(body, []byte("{\"type\":\"user\",\"message\":{\"content\":\"do the thing\"}}\n")...)
	body = append(body, []byte("{ truncated\n")...)
	if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The log keeps no text, so a capture rule that recorded the wrong thing cannot
// be repaired from the log. The transcript is where the inputs survive, and the
// rebuild applies the CURRENT rules to it — which is what makes a fixed reducer
// reach the records that the broken one wrote.
func TestRebuildRederivesDetailFromTheTranscript(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	salt := "org-salt"
	sid := "9d1f0a2b-0000-4444-8888-abcdefabcdef"
	writeTranscript(t, home, sid, []map[string]any{
		// The command that broke it: a here-doc whose body was read as if each
		// line had run.
		{"name": "Bash", "input": map[string]any{
			"command": "python3 - <<'PY'\nimport json\nprint('An answer')\nPY\ngit status"}},
		{"name": "Read", "input": map[string]any{"file_path": "/home/x/repo/main.go"}},
		{"name": "Task", "input": map[string]any{"subagent_type": "Explore", "prompt": "find it"}},
	})

	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	detail, daily := sessionLogPaths(state)
	log := loadSessionLog(detail, daily, fixedClock(now))
	log.upsert(sessionRecord{
		Key: sessionhash.Hash(salt, "claude-code:"+sid), Start: now, End: now,
		Harness: "claude-code", Model: "anthropic/claude-opus-5", Project: "repo",
		Turns: 3, Tools: map[string]int{"Bash": 1, "Read": 1, "Task": 1},
		// What the broken reducer left behind.
		ToolDetail: map[string]map[string]int{
			"Bash": {"python3": 1, "import": 1, "An": 1, "PY": 1, "git status": 1},
			"Read": {".go": 1},
		},
	})

	rep, err := RebuildDetail(state, home, salt, false, fixedClock(now))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rebuilt != 1 || rep.NoTranscript != 0 {
		t.Fatalf("report = %+v, want one record rebuilt", rep)
	}
	rebuilt := loadSessionLog(detail, daily, fixedClock(now))
	var got *sessionRecord
	for _, r := range rebuilt.recs {
		got = r
	}
	bash := got.ToolDetail["Bash"]
	for _, junk := range []string{"import", "An", "PY"} {
		if _, ok := bash[junk]; ok {
			t.Errorf("%q survived the rebuild: %v", junk, bash)
		}
	}
	if bash["python3"] != 1 || bash["git status"] != 1 {
		t.Errorf("the real programs did not come back: %v", bash)
	}
	// A vocabulary added after the record was written is backfilled by the same
	// pass, because the rebuild runs the rules of today rather than of then.
	if got.ToolDetail["Task"]["Explore"] != 1 {
		t.Errorf("the agent was not backfilled: %v", got.ToolDetail["Task"])
	}
	if got.ToolDetail["Read"][".go"] != 1 {
		t.Errorf("an untouched vocabulary was lost: %v", got.ToolDetail["Read"])
	}
	// The report names what went and what arrived, because a count alone
	// cannot be checked against the page the member was reading.
	if len(rep.Dropped) == 0 {
		t.Error("the report does not say which keys went")
	}
}

// A record whose transcript is gone cannot be rebuilt. Its program detail is
// dropped rather than kept: it came from a reducer known to be wrong, and no
// rule can sort its good keys from its bad ones after the fact. Everything the
// bug never touched stays.
func TestRebuildDropsWhatItCannotRederive(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	detail, daily := sessionLogPaths(state)
	log := loadSessionLog(detail, daily, fixedClock(now))
	log.upsert(sessionRecord{
		Key: "no-transcript-for-this-one", Start: now, End: now, Harness: "claude-code",
		Turns: 2, Tools: map[string]int{"Bash": 1, "Edit": 1},
		ToolDetail: map[string]map[string]int{
			"Bash": {"EOF": 1, "git commit": 1},
			"Edit": {".css": 2},
		},
	})

	rep, err := RebuildDetail(state, home, "org-salt", false, fixedClock(now))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rebuilt != 0 || rep.NoTranscript != 1 || rep.Cleared != 1 {
		t.Fatalf("report = %+v, want one record cleared", rep)
	}
	after := loadSessionLog(detail, daily, fixedClock(now))
	for _, r := range after.recs {
		if _, ok := r.ToolDetail["Bash"]; ok {
			t.Errorf("program detail survived with no transcript to check it: %v", r.ToolDetail)
		}
		if r.ToolDetail["Edit"][".css"] != 2 {
			t.Errorf("a vocabulary the bug never touched was dropped: %v", r.ToolDetail)
		}
	}
	// Counts are never touched: a rebuild repairs what is behind a tool, not
	// how often it was used.
	for _, r := range after.recs {
		if r.Tools["Bash"] != 1 || r.Turns != 2 {
			t.Errorf("the rebuild changed the counts: %+v", r)
		}
	}
}

// A dry run says what it would do and writes nothing, because the thing it
// would do is irreversible: the log is the only copy.
func TestRebuildDryRunWritesNothing(t *testing.T) {
	home, state := t.TempDir(), t.TempDir()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	detail, daily := sessionLogPaths(state)
	log := loadSessionLog(detail, daily, fixedClock(now))
	log.upsert(sessionRecord{Key: "k", Start: now, End: now, Harness: "claude-code", Turns: 1,
		Tools: map[string]int{"Bash": 1}, ToolDetail: map[string]map[string]int{"Bash": {"EOF": 1}}})

	rep, err := RebuildDetail(state, home, "org-salt", true, fixedClock(now))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Cleared != 1 {
		t.Fatalf("a dry run should still report what it would clear: %+v", rep)
	}
	after := loadSessionLog(detail, daily, fixedClock(now))
	for _, r := range after.recs {
		if r.ToolDetail["Bash"]["EOF"] != 1 {
			t.Errorf("a dry run wrote to the log: %v", r.ToolDetail)
		}
	}
}

// Without the salt the keys were hashed with, no transcript matches any record.
// Rebuilding on that basis would clear every session's detail and report it as
// success, so it refuses instead.
func TestRebuildRefusesWithoutTheSalt(t *testing.T) {
	if _, err := RebuildDetail(t.TempDir(), t.TempDir(), "", false, nil); err == nil {
		t.Error("a rebuild with no salt should refuse rather than clear everything")
	}
}
