// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Rebuilding the second level from the transcripts this machine still has.
//
// The session log keeps counts and vocabularies, never the text they came from
// (sessionlog.go). That is the whole design, and it has one consequence: when a
// capture rule is WRONG, what it recorded cannot be repaired from the log —
// there is nothing left to re-read. It happened: splitSegments treated every
// newline as a command separator, so the body of a here-doc was reduced as if
// each of its lines had run, and `import`, `EOF` and `An` were filed as
// programs a member had used.
//
// The harness transcript is the one place the original inputs survive, and the
// agent already reads it (capture.TranscriptUsage reads the token counts off
// the end of it). So a rebuild re-reads it, applies the CURRENT rules, and
// writes back only what those rules keep — the same vocabulary, from the same
// source, with the fixed reducer. Nothing new is retained, and no text reaches
// disk that would not have reached it had the rule been right the first time.
//
// Where a transcript is gone, the program detail is dropped rather than left:
// it was produced by a reducer known to be wrong, and no rule can tell its
// good keys from its bad ones after the fact. What was never affected — the
// file types, the agents, the skills — stays.
package hooks

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/sessionhash"
)

// RebuildReport is what a rebuild did, in the terms the member reads it in.
type RebuildReport struct {
	Transcripts  int      // transcripts found on this machine
	Records      int      // full session records in the log
	Rebuilt      int      // records re-derived from their transcript
	NoTranscript int      // records whose transcript is gone
	Cleared      int      // records whose program detail was dropped instead
	KeysBefore   int      // distinct detail keys before
	KeysAfter    int      // and after
	Dropped      []string // keys that existed before and do not survive the current rules
	Added        []string // keys the current rules find that the old ones missed
	AmpFilled    int      // Amp sessions that gained what their thread knows
}

// transcriptRoot is where Claude Code keeps its session transcripts: one file
// per session, named by the session id, under a directory per project. It is
// the only layout this rebuild knows; a record from another harness finds no
// transcript and is handled as one that has lost it.
func transcriptRoot(home string) string {
	return filepath.Join(home, ".claude", "projects")
}

// RebuildDetail re-derives the tool detail of every session this machine still
// has a transcript for, under the current capture rules, and rewrites the log.
//
// salt is the one the keys were hashed with (config.SessionSalt); without the
// same salt nothing matches, and the rebuild says so rather than guessing.
// dry reports what would change and writes nothing.
func RebuildDetail(stateDir, home, salt string, dry bool, now func() time.Time) (RebuildReport, error) {
	var rep RebuildReport
	detailPath, dailyPath := sessionLogPaths(stateDir)
	if detailPath == "" {
		return rep, fmt.Errorf("no state directory, so there is no session log to rebuild")
	}
	if salt == "" {
		return rep, fmt.Errorf("no session salt is configured, so a transcript cannot be matched to a record")
	}
	log := loadSessionLog(detailPath, dailyPath, now)
	log.mu.Lock()
	defer log.mu.Unlock()
	rep.Records = len(log.recs)
	if rep.Records == 0 {
		return rep, nil
	}

	// Index the transcripts by the key their session id hashes to, which is the
	// only join there is: the log holds the hash and never the id.
	byKey := map[string]string{}
	root := transcriptRoot(home)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		sid := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		byKey[sessionhash.Hash(salt, "claude-code:"+sid)] = path
		rep.Transcripts++
		return nil
	})

	before, after := map[string]bool{}, map[string]bool{}
	noteKeys := func(into map[string]bool, r *sessionRecord) {
		for tool, d := range r.ToolDetail {
			for k := range d {
				into[tool+" · "+k] = true
			}
		}
	}
	for _, r := range log.recs {
		noteKeys(before, r)
		path := byKey[r.Key]
		if path == "" {
			rep.NoTranscript++
			if clearProgramDetail(r) {
				rep.Cleared++
			}
			noteKeys(after, r)
			continue
		}
		tools, detail, err := transcriptTools(path)
		if err != nil || len(tools) == 0 {
			rep.NoTranscript++
			if clearProgramDetail(r) {
				rep.Cleared++
			}
			noteKeys(after, r)
			continue
		}
		// Counts and detail come back from ONE source, so the two cannot
		// disagree: a detail that outran its own tool's call count would put a
		// share over 100% on the page.
		r.Tools = tools
		r.ToolDetail = detail
		rep.Rebuilt++
		noteKeys(after, r)
	}
	// Amp names the model nowhere a hook can see, so a session recorded before
	// this asked its thread has no model at all. Its thread still does
	// (ampmodel.go).
	rep.AmpFilled = fillAmpModels(home, salt, log.recs)
	rep.KeysBefore, rep.KeysAfter = len(before), len(after)
	rep.Dropped = diffKeys(before, after)
	rep.Added = diffKeys(after, before)
	if !dry {
		log.rewriteLocked()
	}
	return rep, nil
}

func diffKeys(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// clearProgramDetail drops the detail of the tools whose keys the old reducer
// could have invented, and leaves every other vocabulary alone.
func clearProgramDetail(r *sessionRecord) bool {
	cleared := false
	for tool := range r.ToolDetail {
		if capture.ToolDetailKind(tool) == capture.DetailProgram {
			delete(r.ToolDetail, tool)
			cleared = true
		}
	}
	if len(r.ToolDetail) == 0 {
		r.ToolDetail = nil
	}
	return cleared
}

// transcriptLineCap bounds one transcript line. A tool input can be large — a
// whole file written in one call — and the default scanner buffer stops at 64KB
// and would silently skip the line.
const transcriptLineCap = 8 << 20

// transcriptTools counts the tool calls in one transcript and reduces each to
// its detail keys, exactly as the hook path would have at the time.
//
// It reads `tool_use` blocks and nothing else: not the prompts, not the
// results, not the thinking. What it returns is what the log already holds —
// names and counted vocabularies.
func transcriptTools(path string) (map[string]int, map[string]map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	tools := map[string]int{}
	detail := map[string]map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), transcriptLineCap)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type  string `json:"type"`
					Name  string `json:"name"`
					Input any    `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // a truncated or unfamiliar line is skipped, never fatal
		}
		if entry.Type != "assistant" {
			continue
		}
		for _, b := range entry.Message.Content {
			if b.Type != "tool_use" || b.Name == "" {
				continue
			}
			tools[b.Name]++
			for _, k := range capture.ToolDetail(b.Name, b.Input) {
				if detail[b.Name] == nil {
					detail[b.Name] = map[string]int{}
				}
				detail[b.Name][k]++
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	if len(detail) == 0 {
		detail = nil
	}
	return tools, detail, nil
}
