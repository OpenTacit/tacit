// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package capture turns managed-runtime interactions into canonical records
// and Characterizations.
//
// The canonical interaction record is the source-agnostic middle of capture
// (docs/harness/agent-runtime-capture.md): every source (the Omnigent meta-harness,
// an AI gateway, a Claude Code hook) maps onto this one shape, so the
// characterizer and everything downstream never depend on which runtime
// produced the data. A runtime swap changes only the reader, never the code
// that consumes it.
package capture

import (
	"fmt"
	"strings"

	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/modelid"
)

// SchemaVersion of the canonical record.
const SchemaVersion = 1

var roleLabel = map[string]string{
	"user": "USER", "assistant": "ASSISTANT", "tool": "TOOL", "system": "SYSTEM",
}

func short(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return strings.TrimRight(string(r[:limit-1]), " ") + "…"
}

// ToTranscriptText renders the record as a plain transcript a human or the
// LLM synthesizer can read. Messages stay in order; tool calls are appended as
// a compact trace.
func ToTranscriptText(rec contracts.CanonicalRecord) string {
	var lines []string
	for _, m := range rec.Messages {
		role := roleLabel[m.Role]
		if role == "" {
			role = strings.ToUpper(m.Role)
			if role == "" {
				role = "?"
			}
		}
		text := strings.TrimSpace(m.Text)
		var extra []string
		for _, x := range m.Modalities {
			if x != "" && x != "text" {
				extra = append(extra, x)
			}
		}
		tag := ""
		if len(extra) > 0 {
			tag = " [+" + strings.Join(extra, ", ") + "]"
		}
		if text != "" || tag != "" {
			lines = append(lines, fmt.Sprintf("%s%s: %s", role, tag, text))
		}
	}
	for _, t := range rec.ToolCalls {
		call := fmt.Sprintf("TOOL_CALL %s(%s)", t.Name, short(t.Arguments, 120))
		if t.Output != "" {
			call += " -> " + short(t.Output, 120)
		}
		lines = append(lines, call)
	}
	return strings.Join(lines, "\n\n")
}

// Characterize is the deterministic, source-agnostic characterizer: canonical
// record -> Characterization, WITHOUT calling an LLM — the dependency-free
// floor. Because a managed-runtime record carries the actual tools,
// modalities, and segment, this structured characterization is often better
// grounded than one an LLM guesses from raw text.
func Characterize(rec contracts.CanonicalRecord) contracts.Characterization {
	var modalities []string
	seen := map[string]bool{}
	for _, m := range rec.Messages {
		mods := m.Modalities
		if len(mods) == 0 {
			mods = []string{"text"}
		}
		for _, mod := range mods {
			if mod != "" && !seen[mod] {
				seen[mod] = true
				modalities = append(modalities, mod)
			}
		}
	}
	if len(modalities) == 0 {
		modalities = []string{"text"}
	}

	var toolsUsed []string
	seenTool := map[string]bool{}
	for _, t := range rec.ToolCalls {
		if t.Name != "" && !seenTool[t.Name] {
			seenTool[t.Name] = true
			toolsUsed = append(toolsUsed, t.Name)
		}
	}

	seg := contracts.Segment{}
	for k, v := range rec.Segment {
		seg[k] = v
	}
	// The model is a cohort dimension, and this is the one place every source
	// passes through on its way to becoming a Characterization — so stamping it
	// here is what makes an Omnigent session, a hook stream and a pasted
	// transcript agree. Normalised, because the same model arrives under
	// several labels and a dimension that splits them computes every rate it
	// carries over a fraction of the evidence (internal/modelid). The raw label
	// stays on Characterization.Model for the support row and the change report.
	if key := modelid.Key(rec.Model); key != "" && seg["model"] == "" {
		seg["model"] = key
	}
	summary := strings.TrimSpace(ToTranscriptText(rec))
	if summary == "" {
		summary = "(empty interaction)"
	}
	domain := seg["domain"]
	if domain == "" {
		domain = seg["function"]
	}
	return contracts.Characterization{
		SummaryText:             summary,
		TaskType:                TaskType(toolsUsed),
		Model:                   rec.Model,
		Domain:                  domain,
		Modalities:              modalities,
		ToolsUsed:               toolsUsed,
		InternalResourcesInPlay: rec.InternalResourcesInPlay,
		Harness:                 rec.Harness,
		Surface:                 rec.Surface,
		Segment:                 seg,
	}
}

// TaskType labels the SHAPE of a turn from the tools it actually used. It is
// deliberately not a judgment about what the member was trying to achieve — no
// LLM runs on this path (the hook agent characterizes without one, to stay off
// the latency budget), and a guessed intent presented as a recorded fact is
// exactly the blurring of "observed" and "inferred" the evidence model forbids
// (docs/learning/synthesis-design.md § the three-claim discipline).
//
// So this reports what happened: a turn that called Edit and Write did editing,
// whatever it was for. That is an OBSERVED claim, which is the evidence class
// the detectors conditioning on it can reach — no more.
//
// task_type must be populated on every event: the findings conditioned on it
// (`context-condition`, and the useful half of `coverage-gap`) are dead on
// arrival without it.
//
// The vocabulary is small, closed, and priority-ordered: a turn does many things,
// and the most specific action wins over the incidental reads around it. Keep it
// aligned with the task_types techniques are tagged with.
func TaskType(toolsUsed []string) string {
	has := make(map[string]bool, len(toolsUsed))
	for _, t := range toolsUsed {
		has[t] = true
	}
	usesAny := func(names ...string) bool {
		for _, n := range names {
			if has[n] {
				return true
			}
		}
		return false
	}
	switch {
	case usesAny("Edit", "Write", "NotebookEdit"):
		return "editing" // changed something — the strongest signal there is
	case usesAny("Task", "Agent", "Workflow"):
		return "delegation" // handed work to another agent
	case usesAny("WebSearch", "WebFetch"):
		return "research" // reached outside the repo
	case usesAny("Bash", "BashOutput"):
		return "verification" // ran something to find out what happens
	case usesAny("Read", "Grep", "Glob", "NotebookRead"):
		return "exploration" // looked, changed nothing
	case len(toolsUsed) > 0:
		return "tool-use" // used tools we don't have a shape for yet
	default:
		return "conversation" // pure dialogue, no tools
	}
}
