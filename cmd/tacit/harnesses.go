// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// The harness registry: every AI tool OpenTacit wires, listed once.
//
// The set used to be spelled six times — a table in connect.go, a parallel
// table in disconnect.go, three flag help strings and a validation switch in
// doctor.go — so adding a harness was a scavenger hunt, and a site missed gave
// a harness that connects but cannot be unwired or doctored. One ordered slice
// now feeds all of them, and the name lists members read are derived from it.
//
// The order is user-facing. It decides the order connect wires and disconnect
// unwires, and it is the order the names print in help text and errors. Add a
// harness at the end.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// harnessDef is one harness in its four roles: how to tell it is installed,
// how connect wires it, how disconnect takes that back, and what doctor
// checks on the wiring hop.
type harnessDef struct {
	name      string
	installed func(home string) bool
	connect   func(h *hctx, c *connectReport)
	undo      func(d *dctx, c *connectReport)
	wiring    func(c *checker, home, pluginsDir string)
}

// codexMarker is the string that proves tacit wired this config: the relay
// command itself, which is in every hook entry the snippet writes. connect
// tests for it before appending and doctor tests for it afterwards, so the two
// cannot drift into disagreeing about what "wired" means.
const codexMarker = "hook-relay codex"

var harnesses = []harnessDef{
	{
		name:      "claude-code",
		installed: func(home string) bool { return haveExec("claude") || isDir(filepath.Join(home, ".claude")) },
		connect:   (*hctx).claudeCode,
		undo:      (*dctx).claudeCode,
		wiring:    claudeCodeWiring,
	},
	{
		name:      "codex",
		installed: func(home string) bool { return haveExec("codex") || isDir(filepath.Join(home, ".codex")) },
		connect:   (*hctx).codex,
		undo:      (*dctx).codex,
		wiring: fileMarkerWiring(fileWiring{
			path:    func(home string) string { return filepath.Join(home, ".codex", "config.toml") },
			marker:  codexMarker,
			require: true,
			missing: "%s does not wire the tacit hooks — run `tacit connect --harness codex`",
			wired:   "codex hooks wired in %s",
		}),
	},
	{
		name:      "gemini",
		installed: func(home string) bool { return haveExec("gemini") || isDir(filepath.Join(home, ".gemini")) },
		connect:   (*hctx).gemini,
		undo:      (*dctx).gemini,
		wiring:    geminiWiring,
	},
	{
		name:      "copilot",
		installed: func(home string) bool { return haveExec("copilot") || isDir(filepath.Join(home, ".copilot")) },
		connect:   (*hctx).copilot,
		undo:      (*dctx).copilot,
		wiring: fileMarkerWiring(fileWiring{
			path:    func(home string) string { return filepath.Join(home, ".copilot", "hooks", "tacit.json") },
			marker:  "hook-relay copilot",
			require: true,
			missing: "copilot hooks file missing (%s) — run `tacit connect --harness copilot`",
			wired:   "copilot hooks wired (%s)",
			note:    "copilot is observation-only: suggestions do not render in-flow; the ask path (skills + MCP) is the member surface",
		}),
	},
	{
		name:      "cursor",
		installed: func(home string) bool { return haveExec("cursor-agent") || isDir(filepath.Join(home, ".cursor")) },
		connect:   (*hctx).cursor,
		undo:      (*dctx).cursor,
		wiring: fileMarkerWiring(fileWiring{
			path:    func(home string) string { return filepath.Join(home, ".cursor", "hooks.json") },
			marker:  "hook-relay cursor",
			require: true,
			missing: "%s does not wire the tacit hooks — run `tacit connect --harness cursor`",
			wired:   "cursor hooks wired (%s)",
			note:    "delivery is park-only: suggestions render with your NEXT prompt, not under the answer",
		}),
	},
	{
		name:      "amp",
		installed: func(home string) bool { return haveExec("amp") || isDir(filepath.Join(home, ".config", "amp")) },
		connect:   (*hctx).amp,
		undo:      (*dctx).amp,
		wiring: fileMarkerWiring(fileWiring{
			path:    func(home string) string { return filepath.Join(home, ".config", "amp", "plugins", "tacit.ts") },
			marker:  "hook-relay",
			missing: "amp plugin missing (%s) — run `tacit connect --harness amp`",
			wired:   "amp plugin present (%s)",
		}),
	},
	{
		name:      "pi",
		installed: func(home string) bool { return haveExec("pi") || isDir(filepath.Join(home, ".pi")) },
		connect:   (*hctx).pi,
		undo:      (*dctx).pi,
		wiring:    piPackageWiring,
	},
	{
		// omp is detected by its binary alone: it keeps no directory of its
		// own under the member's home.
		name:      "omp",
		installed: func(string) bool { return haveExec("omp") },
		connect:   (*hctx).omp,
		undo:      (*dctx).omp,
		wiring:    piPackageWiring,
	},
	{
		name: "opencode",
		installed: func(home string) bool {
			return haveExec("opencode") || isDir(filepath.Join(home, ".config", "opencode"))
		},
		connect: (*hctx).opencode,
		undo:    (*dctx).opencode,
		wiring: fileMarkerWiring(fileWiring{
			path:    func(home string) string { return filepath.Join(home, ".config", "opencode", "plugin", "tacit.ts") },
			marker:  "hook-relay",
			missing: "opencode plugin missing (%s) — run `tacit connect --harness opencode`",
			wired:   "opencode plugin present (%s)",
		}),
	},
}

// findHarness looks one up by name; ok is false for a name no harness claims.
func findHarness(name string) (harnessDef, bool) {
	for _, h := range harnesses {
		if h.name == name {
			return h, true
		}
	}
	return harnessDef{}, false
}

// harnessNames is the list members see in `--harness` help and in the message
// for a name OpenTacit does not know.
func harnessNames() string { return joinHarnessNames("|") }

// harnessNamesProse is the same list inside a sentence.
func harnessNamesProse() string { return joinHarnessNames(", ") }

func joinHarnessNames(sep string) string {
	names := make([]string, 0, len(harnesses))
	for _, h := range harnesses {
		names = append(names, h.name)
	}
	return strings.Join(names, sep)
}

// fileWiring describes the wiring check five harnesses share: one file that
// must be there, and the relay command baked into it.
type fileWiring struct {
	// path locates the file doctor reads, under the member's home.
	path func(home string) string
	// marker is the relay command in that file. amp and opencode carry a
	// TypeScript plugin that names the relay without the harness word, so they
	// match the bare "hook-relay" and leave require false: for them the plugin
	// file being there IS the wiring, and the marker only points at the binary.
	marker  string
	require bool
	// missing and wired are the member-facing lines, each taking the path.
	missing string
	wired   string
	// note, when set, prints after a check that passed.
	note string
}

// fileMarkerWiring builds a wiring check from that description.
func fileMarkerWiring(w fileWiring) func(c *checker, home, pluginsDir string) {
	return func(c *checker, home, _ string) {
		path := w.path(home)
		raw, err := os.ReadFile(path)
		if err != nil || (w.require && !strings.Contains(string(raw), w.marker)) {
			c.fail(w.missing, path)
			return
		}
		c.ok(w.wired, path)
		checkRelayBinaryIn(c, path, string(raw), w.marker)
		if w.note != "" {
			c.info("%s", w.note)
		}
	}
}

// claudeCodeWiring: the plugin system owns the install, so the check is that
// Claude Code lists our plugin, and that the hooks file IT is serving still
// runs a binary that exists.
//
// Which copy is being served is a question with a wrong answer that looks
// right. Claude Code keeps every version it has ever installed under
// plugins/cache/<marketplace>/<plugin>/<version>/, and only one of them is
// live: installed_plugins.json names it. Searching the tree instead reported
// whichever abandoned version sorted first — so a member whose plugin was
// healthy was told, correctly-shaped and wrongly, that it ran a binary in a
// mktemp directory, and told to run `tacit connect` again, which cannot delete
// a directory Claude Code owns and no longer uses.
func claudeCodeWiring(c *checker, home, _ string) {
	installed := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	raw, err := os.ReadFile(installed)
	if err != nil || !strings.Contains(string(raw), `"tacit@`) {
		c.fail("Claude Code has no tacit plugin — run `tacit connect` and do its /plugin steps")
		return
	}
	c.ok("Claude Code plugin installed")
	root := installedPluginPath(raw)
	if root == "" {
		// A manifest we cannot read the install path out of is not a reason to
		// check nothing: fall back to the whole tree, and say which copy the
		// answer came from so a stale hit is legible rather than baffling.
		c.info("installed_plugins.json names no install path for tacit@tacit; searching every cached copy")
		checkRelayBinary(c, filepath.Join(home, ".claude", "plugins"), "hook-relay claude-code")
		return
	}
	checkRelayBinary(c, root, "hook-relay claude-code")
}

// installedPluginPath reads the install path Claude Code records for
// tacit@tacit. It parses only as much of the manifest as it needs, so a schema
// that grows a field does not cost a check.
func installedPluginPath(raw []byte) string {
	var manifest struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return ""
	}
	for name, entries := range manifest.Plugins {
		if !strings.HasPrefix(name, "tacit@") {
			continue
		}
		for _, e := range entries {
			if e.InstallPath != "" {
				return e.InstallPath
			}
		}
	}
	return ""
}

// geminiWiring: two valid wirings: the linked extension (whose manifest lives
// in the materialized tree) or a manual settings.json merge. Either must name
// the relay.
func geminiWiring(c *checker, home, pluginsDir string) {
	ext := filepath.Join(pluginsDir, "gemini", "gemini-extension.json")
	settings := filepath.Join(home, ".gemini", "settings.json")
	if raw, err := os.ReadFile(ext); err == nil && strings.Contains(string(raw), "hook-relay gemini") {
		c.ok("gemini extension package materialized (%s)", ext)
		checkRelayBinaryIn(c, ext, string(raw), "hook-relay gemini")
		c.info("the harness owns the registration — check it with `gemini extensions list`")
		return
	}
	raw, err := os.ReadFile(settings)
	if err != nil || !strings.Contains(string(raw), "hook-relay gemini") {
		c.fail("no gemini wiring (extension not materialized, %s has no relay hooks) — run `tacit connect --harness gemini`", settings)
		return
	}
	c.ok("gemini hooks wired in %s", settings)
	checkRelayBinaryIn(c, settings, string(raw), "hook-relay gemini")
}

// piPackageWiring serves pi and omp: both load the pi package; registration
// lives in pi's settings (pi) or omp's config (omp) — connect.go owns the
// details. Presence of the materialized package is the checkable half.
func piPackageWiring(c *checker, _, pluginsDir string) {
	pkg := filepath.Join(pluginsDir, "pi")
	if _, err := os.Stat(filepath.Join(pkg, "package.json")); err != nil {
		c.fail("embedded pi package not materialized (%s) — run `tacit connect`", pkg)
		return
	}
	c.ok("pi package materialized (%s)", pkg)
	c.info("the harness owns the registration — check it with `pi list` / `omp config get extensions`")
}
