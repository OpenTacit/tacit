// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import "strings"

// ToolKind classifies one tool by what it DOES to the work, so a member can
// read a mix of twenty tool names as five kinds of act.
//
// It shares its vocabulary with TaskType deliberately — that function decides
// what a whole turn was for, from the same names — so a session classed
// "editing" is one whose tools include an edit kind, and the two cannot drift
// into disagreeing about what Write is. What differs is the grain: TaskType
// answers once per turn and picks the strongest signal; this answers per tool.
//
// An unknown name is "other" rather than a guess. Harnesses add tools faster
// than anyone can keep a table current, and a wrong kind is worse than an
// honest unknown: it moves a slice of the mix under a heading the member would
// read as measured.
//
// MCP tools are their own kind and are recognised structurally rather than by
// name. `mcp__server__tool` is the wire shape every harness uses, so the prefix
// identifies them without a table, and "which of my own servers do I actually
// call" is a question only this kind can answer.
func ToolKind(name string) string {
	if name == "" {
		return "other"
	}
	if strings.HasPrefix(name, "mcp__") {
		return "mcp"
	}
	switch name {
	case "Edit", "Write", "NotebookEdit", "MultiEdit", "apply_patch", "str_replace_editor":
		return "edit"
	case "Bash", "BashOutput", "KillShell", "shell", "run_terminal_cmd":
		return "run"
	case "Read", "NotebookRead", "read_file":
		return "read"
	case "Grep", "Glob", "codebase_search", "file_search":
		return "search"
	case "WebSearch", "WebFetch":
		return "research"
	case "Task", "Agent", "Workflow":
		return "delegate"
	case "TodoWrite", "AskUserQuestion", "ExitPlanMode", "EnterPlanMode":
		return "steer"
	}
	return "other"
}

// ToolKinds is the order kinds are shown in, which is roughly the order work
// moves through them: find something, read it, change it, run it, and the three
// that step outside that loop. Fixed, so a mix never repaints when one kind
// empties — the same rule the series colours follow.
var ToolKinds = []string{"search", "read", "edit", "run", "research", "delegate", "steer", "mcp", "other"}

// ToolKindLabel is what a kind is called in front of a member.
func ToolKindLabel(kind string) string {
	switch kind {
	case "search":
		return "Searching"
	case "read":
		return "Reading"
	case "edit":
		return "Editing"
	case "run":
		return "Running"
	case "research":
		return "Looking outside"
	case "delegate":
		return "Delegating"
	case "steer":
		return "Steering"
	case "mcp":
		return "Your own tools"
	}
	return "Other"
}

// MCPServer names the server behind an MCP tool: `mcp__tacit__search` is the
// tacit server. Empty for anything that is not one.
//
// The server is the unit a member thinks in — they wired a server, not
// seventeen tools — and it is the unit the spend question is asked in. The
// tool half of the name is already kept as the tool name itself.
func MCPServer(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return ""
	}
	rest := strings.TrimPrefix(name, "mcp__")
	if server, _, found := strings.Cut(rest, "__"); found && server != "" {
		return server
	}
	return rest
}
