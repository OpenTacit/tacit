// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// The tacit MCP server entry in a member's own config file, put in and taken
// back out.
//
// Six blocks used to do this by hand: copilot, cursor and amp on the way in,
// the same three on the way out. Each read a JSON file, found the map of
// servers, added or deleted the "tacit" entry and wrote the file back, and each
// connect half had to stay a mirror of its disconnect half. They had drifted —
// two spellings of the already-wired test, three wordings of its answer.
//
// The rules that matter live here once:
//
//   - A file this code cannot parse is never edited. A member's settings may
//     hold comments or trailing commas that our parser rejects and their
//     harness accepts, so the answer is to tell them what to add, not to
//     rewrite what we half understood.
//   - An entry already named "tacit" is left alone, whatever it points at.
//   - Editing a file the member already had leaves a .bak-pre-tacit copy of
//     what was there first.
//   - Every write goes through fsx, so a reader sees the old file or the new
//     one and never half of either.

import (
	"encoding/json"
	"os"

	"github.com/opentacit/tacit/internal/fsx"
)

// mcpEdit says what happened to the file. The caller decides what to print:
// each harness names its own file and its own example to copy from, and members
// grep for those lines.
type mcpEdit int

const (
	// mcpUnparseable: nothing was read that we understood, nothing was written,
	// and the caller owes the member instructions.
	mcpUnparseable mcpEdit = iota
	// mcpFailed: reported through c.fail already.
	mcpFailed
	// mcpPresent: the file already names a tacit server.
	mcpPresent
	// mcpCreated: there was no file, and now there is one holding our entry.
	mcpCreated
	// mcpAdded: our entry went into a file the member already had.
	mcpAdded
)

// addMCPServer puts a tacit entry into the serversKey map of the JSON file at
// path, running bin as the server. serversKey is the only thing that varies
// between harnesses: "mcpServers" for copilot and cursor, "amp.mcpServers" for
// amp. backup asks for the pre-edit copy beside the file.
//
// The two-space indent and the trailing newline match what these files already
// carry, which is what fsx.WriteJSONAtomic writes.
func addMCPServer(c *connectReport, path, serversKey, bin string, backup bool) mcpEdit {
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		fresh := map[string]any{serversKey: map[string]any{"tacit": mcpEntry(bin)}}
		if err := fsx.WriteJSONAtomic(path, fresh, 0o644); err != nil {
			c.fail("write %s: %v", path, err)
			return mcpFailed
		}
		return mcpCreated
	default:
		c.fail("read %s: %v", path, err)
		return mcpFailed
	}

	cfg := map[string]any{}
	if json.Unmarshal(raw, &cfg) != nil {
		return mcpUnparseable
	}
	servers, _ := cfg[serversKey].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		cfg[serversKey] = servers
	}
	// Key presence, not the value under it: a member who wrote "tacit" into
	// their own config gets to keep whatever they wrote.
	if _, exists := servers["tacit"]; exists {
		return mcpPresent
	}
	servers["tacit"] = mcpEntry(bin)

	if backup {
		bak := path + ".bak-pre-tacit"
		if err := fsx.WriteFileAtomic(bak, raw, 0o644); err != nil {
			c.fail("backup %s: %v", bak, err)
			return mcpFailed
		}
	}
	if err := fsx.WriteJSONAtomic(path, cfg, 0o644); err != nil {
		c.fail("update %s: %v", path, err)
		return mcpFailed
	}
	return mcpAdded
}

// removeMCPServer takes the tacit entry back out of the serversKey map of the
// JSON file at path, and reports whether there was a file to read at all — the
// one line each caller still says in its own words.
//
// The cascade matters: an empty servers map goes with the entry, and a file
// left holding nothing else goes with the map. Connect made that file, so
// disconnect leaves no shell of it behind.
func removeMCPServer(c *connectReport, path, serversKey string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	cfg := map[string]any{}
	if json.Unmarshal(raw, &cfg) != nil {
		c.todo("cannot parse %s — remove the %s tacit entry yourself", path, serversKey)
		return true
	}
	servers, _ := cfg[serversKey].(map[string]any)
	if servers == nil || servers["tacit"] == nil {
		c.ok("no tacit MCP server in %s", path)
		return true
	}
	delete(servers, "tacit")
	if len(servers) == 0 {
		delete(cfg, serversKey)
	}
	if len(cfg) == 0 {
		if err := os.Remove(path); err != nil {
			c.fail("remove %s: %v", path, err)
			return true
		}
		c.ok("removed %s (it held only the tacit wiring)", path)
		return true
	}
	if err := fsx.WriteJSONAtomic(path, cfg, 0o644); err != nil {
		c.fail("update %s: %v", path, err)
		return true
	}
	c.ok("removed the tacit MCP server from %s", path)
	return true
}

// mcpEntry is how every harness names the server: this binary, one argument.
func mcpEntry(bin string) map[string]any {
	return map[string]any{"command": bin, "args": []string{"mcp"}}
}
