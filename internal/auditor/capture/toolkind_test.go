// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import "testing"

// The kinds share their vocabulary with TaskType, so a session classed
// "editing" is one whose tools include an edit kind. A tool that drifted
// between the two would make the two views disagree about what Write is.
func TestToolKindAgreesWithTaskType(t *testing.T) {
	for _, tc := range []struct{ tool, kind string }{
		{"Edit", "edit"}, {"Write", "edit"}, {"NotebookEdit", "edit"},
		{"Bash", "run"}, {"BashOutput", "run"},
		{"Read", "read"}, {"NotebookRead", "read"},
		{"Grep", "search"}, {"Glob", "search"},
		{"WebSearch", "research"}, {"WebFetch", "research"},
		{"Task", "delegate"}, {"Agent", "delegate"},
		{"TodoWrite", "steer"},
	} {
		if got := ToolKind(tc.tool); got != tc.kind {
			t.Errorf("ToolKind(%q) = %q, want %q", tc.tool, got, tc.kind)
		}
	}
}

// MCP tools are recognised by their wire shape, not by a table: every harness
// spells them mcp__server__tool, and no table could keep up with what members
// connect. "Which of my own servers do I actually call" is a question only this
// kind can answer.
func TestMCPToolsAreRecognisedStructurally(t *testing.T) {
	for _, name := range []string{"mcp__tacit__tacit_search", "mcp__figma__get_file", "mcp__x__y"} {
		if got := ToolKind(name); got != "mcp" {
			t.Errorf("ToolKind(%q) = %q, want mcp", name, got)
		}
	}
}

// An unknown tool is "other", never a guess. Harnesses add tools faster than a
// table can be kept current, and a wrong kind moves a slice of the mix under a
// heading the member would read as measured.
func TestUnknownToolsAreNotGuessed(t *testing.T) {
	for _, name := range []string{"SomeNewTool", "zzz", "Readme"} {
		if got := ToolKind(name); got != "other" {
			t.Errorf("ToolKind(%q) = %q, want other", name, got)
		}
	}
	if ToolKind("") != "other" {
		t.Error("an empty name should be other, not a crash or a kind")
	}
	// Every kind the classifier can return has a place in the display order,
	// or a slice of the mix would render with no heading at all.
	inOrder := map[string]bool{}
	for _, k := range ToolKinds {
		inOrder[k] = true
	}
	for _, k := range []string{"search", "read", "edit", "run", "research", "delegate", "steer", "mcp", "other"} {
		if !inOrder[k] {
			t.Errorf("kind %q is returned but not in ToolKinds", k)
		}
		if ToolKindLabel(k) == "" {
			t.Errorf("kind %q has no label", k)
		}
	}
}
