// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit connect — connect-plan step 2 (docs/distribution/connect-plan.md):
// wire this machine's AI harnesses to OpenTacit using the plugin payloads
// embedded in the binary, so a member who installed via `curl | sh` needs no
// repo checkout. Where a harness loads from user directories, connect
// installs the files itself; where the harness owns the flow (Claude Code's
// plugin system, config files a member may have customized), it prints the
// exact step instead. Tacit-owned files are overwritten freely; member-owned
// config is edited only when the edit is provably additive, and always with a
// backup.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentacit/tacit"
	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/hooks"
	"github.com/opentacit/tacit/internal/merge"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

func cmdConnect(args []string) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	only := fs.String("harness", "", "wire one harness: "+harnessNames()+" (default: all detected)")
	registryURL := fs.String("registry", "", "org registry URL — saves member settings and checks them first (with --key)")
	key := fs.String("key", "", "member API key (from the registry's Members page)")
	code := fs.String("code", "", "handoff code from the invitation page — trades itself for a member key, once, within 15 minutes")
	segment := fs.String("segment", "", "cohort for this machine's outcomes, e.g. team=revops,role=analyst")
	sessionSalt := fs.String("session-salt", "", "org-shared salt for pseudonymous session grouping")
	settingsOnly := fs.Bool("settings-only", false, "save and check the settings, and wire no harness (a CI box, a server, a machine with no AI tool on it)")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	// A code stands in for the pair. The invitation page shows one instead of
	// printing the member key into a command the member has to carry to
	// another machine, so this is where the carrying ends: redeem it, and go
	// on exactly as if they had typed --registry and --key.
	if *code != "" {
		if *registryURL == "" {
			fmt.Fprintln(os.Stderr, "--code needs --registry: the code says who you are, not which registry to ask")
			return exitUsage
		}
		url, minted, err := merge.RedeemCode(&http.Client{Timeout: 15 * time.Second}, *registryURL, *code)
		var api *merge.APIError
		switch {
		case errors.As(err, &api):
			fmt.Fprintf(os.Stderr, "the code was refused: %s\n", api.Msg)
			return exitUnreachable
		case err != nil:
			fmt.Fprintf(os.Stderr, "cannot reach the registry at %s: %v\n", *registryURL, err)
			return exitUnreachable
		}
		if url != "" {
			*registryURL = url
		}
		*key = minted
		fmt.Println("redeemed the code for a member key of this machine's own")
	}

	// Settings and wiring are one onboarding step, not two: this ran the
	// settings flow only when credentials arrived, and `tacit setup` was the
	// command for the rest. Now it always runs — with values to save when the
	// caller brought some, and as a report of where this machine points when
	// they did not. One command answers "connect me" and "what am I connected
	// to?", which is how members asked the question in the first place.
	if rc := applyMemberSettings(memberSettings{
		registryURL: *registryURL, key: *key, segment: *segment, sessionSalt: *sessionSalt,
	}); rc != 0 {
		return rc
	}
	fmt.Println()
	if *settingsOnly {
		return 0
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "no home directory: %v\n", err)
		return 1
	}
	bin, err := os.Executable()
	if err == nil {
		bin, err = filepath.EvalSymlinks(bin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve binary path: %v\n", err)
		return 1
	}

	pluginsDir := filepath.Join(registryconfig.DataHome(), "plugins")
	if _, err := tacit.MaterializePlugins(pluginsDir, bin); err != nil {
		fmt.Fprintf(os.Stderr, "materialize plugins: %v\n", err)
		return 1
	}
	fmt.Printf("wrote plugin packages to %s (binary path %s)\n", pluginsDir, bin)

	h := &hctx{home: home, bin: bin, plugins: pluginsDir}
	ran, failed := 0, 0
	var ready []string
	var pending []harnessTodos
	for _, hs := range harnesses {
		if *only != "" && hs.name != *only {
			continue
		}
		if *only == "" && !hs.installed(home) {
			continue
		}
		fmt.Printf("\n%s:\n", hs.name)
		c := &connectReport{}
		hs.connect(h, c)
		if cap := hooks.Capability(hs.name); cap != "" {
			c.info("%s", cap)
		}
		ran++
		failed += c.fails
		switch {
		case len(c.todoLines) > 0:
			pending = append(pending, harnessTodos{name: hs.name, todos: c.todoLines})
		case c.fails == 0:
			ready = append(ready, hs.name)
		}
	}
	if ran == 0 {
		if *only != "" {
			fmt.Fprintf(os.Stderr, "unknown harness %q (%s)\n", *only, harnessNames())
			return 2
		}
		fmt.Println("\nno AI tool wired: found none of " + harnessNamesProse() + " on this machine")
		fmt.Println("run `tacit connect --harness <name>` to wire one anyway")
		return 0
	}

	fmt.Println()
	summarizeConnect(os.Stdout, ready, pending, failed)
	fmt.Println()
	reportModelKey()
	fmt.Println()
	mcfg := auditorconfig.Load()
	if mcfg.RegistryKey == auditorconfig.DefaultRegistryKey {
		fmt.Println("next: connect to your org's registry —")
		fmt.Println("  tacit connect --registry <url> --code <code>   (from the invitation page)")
	} else {
		// Questions last, and in this order: a key sharpens the answer the
		// next call is about to give, so it is worth asking for first.
		if offerModelKey(mcfg.LLMKeyFile) {
			mcfg = auditorconfig.Load()
		}
		// The run used to end by offering /tacit:test, which proves the pipe
		// carries a message and says nothing about whether the message is
		// worth having. Ask the playbook something real instead: a member who
		// has just wired their machine should see what it was for, not a
		// diagnostic (docs/distribution/first-adoption-plan.md, 1.2).
		offerFirstResult(mcfg)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// offerFirstResult ends a successful connect with a technique rather than a
// diagnostic — where there is somebody to ask what they are working on.
//
// Skipped without a terminal: a scripted connect has no task in hand, and a
// setup command that blocks on a question cannot run in CI.
func offerFirstResult(cfg auditorconfig.Config) {
	if !stdinIsTerminal() {
		fmt.Printf("ask your playbook any time:  %s ask \"what you are working on\"\n", selfCommand())
		return
	}
	fmt.Println("your machine is connected. ask the playbook something to see what it holds —")
	task := promptForTask()
	if task == "" {
		fmt.Printf("\nany time:  %s ask \"what you are working on\"\n", selfCommand())
		return
	}
	askOnce(os.Stdout, cfg.RegistryURL, cfg.RegistryKey, cfg.HooksSegment, task)
}

// harnessTodos is one tool's outstanding host actions, carried out of the loop
// so the closing summary can name the tool as well as the work.
type harnessTodos struct {
	name  string
	todos []string
}

// summarizeConnect says which tools this run left usable, and it exists
// because the command spent its whole life not saying.
//
// The per-tool output counted every `todo` and then threw the count away, so a
// run that prepared files and left Codex waiting on a trust prompt ended with
// the same next-steps block as a run that finished — no FAIL anywhere, exit 0,
// and a member with a tool that cannot load Tacit.
//
// Three states, said apart: ready, waiting on the member, failed. Only failure
// moves the exit code, because a host's trust prompt is not this command's
// error to report — but it is the member's, and it is the one they care about.
func summarizeConnect(w io.Writer, ready []string, pending []harnessTodos, failed int) {
	if len(ready) > 0 {
		fmt.Fprintf(w, "ready: %s\n", strings.Join(ready, ", "))
	}
	if len(pending) > 0 {
		fmt.Fprintln(w, "waiting on you — these cannot load tacit until you finish them:")
		for _, p := range pending {
			fmt.Fprintf(w, "  %s\n", p.name)
			for _, t := range p.todos {
				fmt.Fprintf(w, "    %s\n", t)
			}
		}
	}
	if failed > 0 {
		fmt.Fprintf(w, "%d step%s failed (FAIL above)\n", failed, map[bool]string{true: "", false: "s"}[failed == 1])
	}
	if len(pending) == 0 && failed == 0 {
		fmt.Fprintln(w, "nothing left to do on this machine.")
	}
}

type hctx struct {
	home, bin, plugins string
}

// claudeCode: the plugin system owns install, and its CLI drives it
// non-interactively — `claude plugin marketplace add/update` + `claude plugin
// install/update` — so connect performs the real install (and, on re-run,
// the refresh that picks up a new binary's plugin tree) whenever the claude
// binary is on PATH. The materialized tree is a valid local marketplace
// (.claude-plugin/marketplace.json at its root). Without the CLI, print the
// in-session steps as before.
func (h *hctx) claudeCode(c *connectReport) {
	// Independent of the plugin system and of every early return below: the
	// status line is a settings.json entry, and it is worth wiring on a re-run
	// for the members who connected before it was part of connect.
	defer h.claudeStatusLine(c)
	run := func(args ...string) (string, error) {
		out, err := exec.Command("claude", args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	installed := filepath.Join(h.home, ".claude", "plugins", "installed_plugins.json")
	already := false
	if raw, err := os.ReadFile(installed); err == nil && strings.Contains(string(raw), `"tacit@`) {
		already = true
	}

	if !haveExec("claude") {
		if already {
			c.ok("tacit plugin already installed in Claude Code — no change (claude CLI not on PATH, so no refresh)")
			return
		}
		c.todo("inside Claude Code run:  /plugin marketplace add %s", h.plugins)
		c.todo("then:                    /plugin install tacit@tacit   (pick user scope)")
		return
	}

	if already {
		// Same posture as symlinked installs elsewhere: a marketplace that
		// points at a repo checkout (the author setup) is member-owned; only
		// an install served from OUR materialized tree is ours to refresh.
		if src := h.claudeMarketplacePath(); src != "" && src != h.plugins {
			c.ok("tacit plugin installed from %s (not the embedded copy) — no change", src)
			return
		}
		// Refresh, don't skip: the marketplace dir was just re-materialized
		// from THIS binary with a content-stamped version, so updating pulls
		// the current plugin content into Claude Code's installed copy — the
		// step upgrades used to silently miss.
		if out, err := run("plugin", "marketplace", "update", "tacit"); err != nil {
			c.todo("refresh the marketplace yourself (/plugin marketplace update tacit): %s", out)
			return
		}
		if out, err := run("plugin", "update", "tacit@tacit"); err != nil {
			c.todo("update the plugin yourself (/plugin update tacit@tacit): %s", out)
			return
		}
		c.ok("refreshed the tacit plugin to this binary's version (restart Claude Code sessions to apply it)")
		return
	}

	// First install. `marketplace add` on an already-known name fails; treat
	// that as "known" and continue to install.
	if out, err := run("plugin", "marketplace", "add", h.plugins); err != nil &&
		!strings.Contains(strings.ToLower(out), "already") {
		c.todo("inside Claude Code run:  /plugin marketplace add %s   (CLI add failed: %s)", h.plugins, out)
		c.todo("then:                    /plugin install tacit@tacit   (pick user scope)")
		return
	}
	if out, err := run("plugin", "install", "tacit@tacit", "--scope", "user"); err != nil {
		c.todo("inside Claude Code run:  /plugin install tacit@tacit   (CLI install failed: %s)", out)
		return
	}
	c.ok("installed the tacit plugin in Claude Code (user scope)")
	h.claudeStatusLine(c)
}

// claudeStatusLine wires `tacit statusline` into ~/.claude/settings.json.
//
// It is the only signal that says OpenTacit is alive on a turn where it has
// nothing to suggest — and, more to the point, the only place a rotated key or
// a model out of credit is ever reported to the member (cmdStatusline). Both
// faults make OpenTacit go silent, and silence is what a quiet day looks like
// too. It was documented as one optional line in a harness guide, which meant
// the members who most needed the alarm were the ones who never saw it.
//
// It also carries the traffic the other way. The harness hands the status line
// the whole session — cost, lines written, context, prompt cache, what is left
// of the account's allowance — and `tacit statusline` posts that back to the
// agent. So an unwired status line is not only a missing alarm now: it is a
// Usage page that cannot say what a session cost.
//
// The house rule on member-owned config holds: write only when the edit is
// provably additive. An existing statusLine is somebody's own line, so we hand
// them the recipe instead of taking the slot.
func (h *hctx) claudeStatusLine(c *connectReport) {
	path := filepath.Join(h.home, ".claude", "settings.json")
	settings := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if json.Unmarshal(raw, &settings) != nil {
			c.todo("cannot parse %s — add the status line yourself: statusLine.command = %q", path, h.bin+" statusline")
			return
		}
	case !os.IsNotExist(err):
		c.fail("read %s: %v", path, err)
		return
	}
	if existing, taken := settings["statusLine"]; taken {
		if cmd, _ := existing.(map[string]any)["command"].(string); strings.Contains(cmd, "tacit statusline") {
			c.ok("status line already shows tacit")
			return
		}
		c.todo("you have your own status line; pipe the payload through tacit's segment to keep cost, lines and quota on your Usage page:  %s statusline", h.bin)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		c.fail("create %s: %v", filepath.Dir(path), err)
		return
	}
	if raw != nil {
		backup := path + ".bak-pre-tacit"
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			c.fail("backup %s: %v", backup, err)
			return
		}
	}
	settings["statusLine"] = map[string]any{"type": "command", "command": h.bin + " statusline", "padding": 0}
	out, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		c.fail("update %s: %v", path, err)
		return
	}
	c.ok("status line wired (%s) — it shows this session's suggestions, drafts that wait and any fault that makes tacit silent, and it feeds cost, lines written and your allowance to the Usage page", path)
}

func (h *hctx) codex(c *connectReport) {
	skillsDst := filepath.Join(h.home, ".codex", "skills")
	if symlinked, err := copyTree(filepath.Join(h.plugins, "codex", "skills"), skillsDst); err != nil {
		c.fail("copy skills: %v", err)
	} else if len(symlinked) > 0 {
		c.ok("skills: %d symlinked from another location (no change); installed the rest in %s", len(symlinked), skillsDst)
	} else {
		c.ok("installed skills in %s ($tacit-help, $tacit-search, ...)", skillsDst)
	}
	cfgPath := filepath.Join(h.home, ".codex", "config.toml")
	raw, err := os.ReadFile(cfgPath)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			c.fail("create %s: %v", filepath.Dir(cfgPath), err)
			return
		}
		if err := os.WriteFile(cfgPath, codexSnippet(h.plugins), 0o644); err != nil {
			c.fail("write %s: %v", cfgPath, err)
			return
		}
		c.ok("wrote %s (hooks + tacit MCP server)", cfgPath)
		c.todo("on first Codex run choose 'Trust all and continue' at the hooks-review prompt")
	case err != nil:
		c.fail("read %s: %v", cfgPath, err)
	case strings.Contains(string(raw), codexMarker) || strings.Contains(string(raw), "mcp_servers.tacit"):
		c.ok("%s already wires tacit — no change", cfgPath)
	case codexPlainHooksTable(raw) != "":
		// The one shape the append cannot survive. Everything below writes
		// array-of-tables; a plain [hooks.X] beside them is a TOML type
		// conflict and Codex would stop parsing the file. Name the line and
		// change nothing — an incomplete result the member can act on beats a
		// config we broke for them.
		c.todo("%s defines %s as a plain table; tacit's hooks are [[array]] entries and cannot be appended beside it. "+
			"Merge %s yourself, or rewrite that line as a [[double-bracket]] entry and re-run.",
			cfgPath, codexPlainHooksTable(raw), filepath.Join(h.plugins, "codex", "config.toml.example"))
	default:
		// Additive append. The member's own [[hooks.X]] entries are no reason
		// to skip: TOML appends to an array of tables, so their hooks and ours
		// both run. Only a tacit marker means there is nothing to do, and only
		// the plain-table case above means we must not.
		backup := cfgPath + ".bak-pre-tacit"
		if err := os.WriteFile(backup, raw, 0o644); err != nil {
			c.fail("backup %s: %v", backup, err)
			return
		}
		joined := append(append([]byte{}, raw...), append([]byte("\n"), codexSnippet(h.plugins)...)...)
		if err := os.WriteFile(cfgPath, joined, 0o644); err != nil {
			c.fail("append to %s: %v", cfgPath, err)
			return
		}
		c.ok("appended hooks + tacit MCP server to %s (backup: %s)", cfgPath, backup)
		c.todo("on next Codex run choose 'Trust all and continue' at the hooks-review prompt")
	}
}

// fileNames reports whether a file exists and mentions the given string. Used
// where the check is "has somebody already put us in here" and the file's
// format is one this command does not otherwise parse.
func fileNames(path, want string) bool {
	raw, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(raw), want)
}

// codexPlainHooksTable returns the first `[hooks…]` line written as a plain
// table rather than an array-of-tables entry, or "" when there is none.
//
// `[[hooks.Stop]]` twice is an array with two elements; `[hooks.Stop]` and
// `[[hooks.Stop]]` in one file is a type error, and Codex fails to parse the
// whole config rather than the offending line. Nobody writes the plain form
// against Codex's own documentation, so this is a rare shape — and a config
// this command destroyed is not one a member forgives.
func codexPlainHooksTable(raw []byte) string {
	for line := range strings.SplitSeq(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[[") {
			continue
		}
		if t == "[hooks]" || strings.HasPrefix(t, "[hooks.") {
			return t
		}
	}
	return ""
}

// codexSnippet is the config content of codex/config.toml.example — the
// materialized copy with the binary path already substituted, header comments
// dropped (they explain the manual path; this IS the automated path). Content
// starts at the first real table header, i.e. a line beginning with "[" —
// a bare Index("[") would cut inside the header comments, which mention
// table names.
func codexSnippet(pluginsDir string) []byte {
	raw, err := os.ReadFile(filepath.Join(pluginsDir, "codex", "config.toml.example"))
	if err != nil {
		return nil
	}
	lines := strings.SplitAfter(string(raw), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			return []byte(strings.Join(lines[i:], ""))
		}
	}
	return raw
}

// copilot: user-level wiring, all tacit-owned files or namespaced entries —
// hooks are a standalone file (~/.copilot/hooks/tacit.json, exactly the
// package's hooks.json), skills are tacit-* directories, and the MCP server
// is a JSON merge into mcp-config.json. No plugin-system dependency: orgs
// that prefer `copilot plugin install` can serve the same subtree from a git
// repo (plugins/copilot/README.md).
func (h *hctx) copilot(c *connectReport) {
	base := filepath.Join(h.home, ".copilot")
	hooksDst := filepath.Join(base, "hooks", "tacit.json")
	if err := os.MkdirAll(filepath.Dir(hooksDst), 0o755); err != nil {
		c.fail("create %s: %v", filepath.Dir(hooksDst), err)
		return
	}
	if err := copyFile(filepath.Join(h.plugins, "copilot", "hooks.json"), hooksDst); err != nil {
		c.fail("write hooks file: %v", err)
	} else {
		c.ok("installed hooks (%s)", hooksDst)
	}
	if symlinked, err := copyTree(filepath.Join(h.plugins, "copilot", "skills"), filepath.Join(base, "skills")); err != nil {
		c.fail("copy skills: %v", err)
	} else if len(symlinked) > 0 {
		c.ok("skills: %d symlinked from another location (no change); installed the rest in %s", len(symlinked), filepath.Join(base, "skills"))
	} else {
		c.ok("installed skills in %s (ask: \"tacit help\", \"search tacit for …\")", filepath.Join(base, "skills"))
	}
	mcpPath := filepath.Join(base, "mcp-config.json")
	switch addMCPServer(c, mcpPath, "mcpServers", h.bin, false) {
	case mcpUnparseable:
		c.todo("cannot parse %s — add the tacit server yourself (see %s)", mcpPath, filepath.Join(h.plugins, "copilot", ".mcp.json"))
	case mcpPresent:
		c.ok("%s already names a tacit MCP server — no change", mcpPath)
	case mcpCreated, mcpAdded:
		c.ok("added the tacit MCP server to %s", mcpPath)
	}
}

// cursor: three user-level pieces — a JSON merge of the seven relay hooks
// into ~/.cursor/hooks.json (single shared file; the merge is additive per
// event and skips events already naming our relay, with a backup on first
// touch), the tacit-* commands copied into ~/.cursor/commands, and the tacit
// MCP server merged into ~/.cursor/mcp.json.
func (h *hctx) cursor(c *connectReport) {
	base := filepath.Join(h.home, ".cursor")
	ours := map[string]any{}
	raw, err := os.ReadFile(filepath.Join(h.plugins, "cursor", "hooks.json"))
	if err != nil || json.Unmarshal(raw, &ours) != nil {
		c.fail("read materialized cursor hooks.json: %v", err)
		return
	}
	oursHooks, _ := ours["hooks"].(map[string]any)
	hooksPath := filepath.Join(base, "hooks.json")
	cfg := map[string]any{"version": float64(1)}
	existing, err := os.ReadFile(hooksPath)
	switch {
	case err == nil:
		if json.Unmarshal(existing, &cfg) != nil {
			c.todo("cannot parse %s — merge the hook entries from %s yourself", hooksPath, filepath.Join(h.plugins, "cursor", "hooks.json"))
			return
		}
		backup := hooksPath + ".bak-pre-tacit"
		if _, err := os.Stat(backup); os.IsNotExist(err) {
			_ = os.WriteFile(backup, existing, 0o644)
		}
	case !os.IsNotExist(err):
		c.fail("read %s: %v", hooksPath, err)
		return
	default:
		if err := os.MkdirAll(base, 0o755); err != nil {
			c.fail("create %s: %v", base, err)
			return
		}
	}
	events, _ := cfg["hooks"].(map[string]any)
	if events == nil {
		events = map[string]any{}
		cfg["hooks"] = events
	}
	added := 0
	for ev, def := range oursHooks {
		list, _ := events[ev].([]any)
		wired := false
		for _, entry := range list {
			if em, _ := entry.(map[string]any); em != nil {
				if cmd, _ := em["command"].(string); strings.Contains(cmd, "hook-relay cursor") {
					wired = true
				}
			}
		}
		if wired {
			continue
		}
		defs, _ := def.([]any)
		events[ev] = append(list, defs...)
		added++
	}
	if added == 0 {
		c.ok("%s already wires the tacit relay — no change", hooksPath)
	} else {
		out, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(hooksPath, append(out, '\n'), 0o644); err != nil {
			c.fail("write %s: %v", hooksPath, err)
			return
		}
		c.ok("merged hooks into %s (added %d event(s))", hooksPath, added)
	}

	if symlinked, err := copyTree(filepath.Join(h.plugins, "cursor", "commands"), filepath.Join(base, "commands")); err != nil {
		c.fail("copy commands: %v", err)
	} else if len(symlinked) > 0 {
		c.ok("commands: %d symlinked from another location (no change); installed the rest in %s", len(symlinked), filepath.Join(base, "commands"))
	} else {
		c.ok("installed commands in %s (/tacit-help, /tacit-search, ...)", filepath.Join(base, "commands"))
	}

	mcpPath := filepath.Join(base, "mcp.json")
	switch addMCPServer(c, mcpPath, "mcpServers", h.bin, false) {
	case mcpUnparseable:
		c.todo("cannot parse %s — add the tacit server yourself (see %s)", mcpPath, filepath.Join(h.plugins, "cursor", "mcp.json.example"))
	case mcpPresent:
		c.ok("%s already names a tacit MCP server — no change", mcpPath)
	case mcpCreated, mcpAdded:
		c.ok("added the tacit MCP server to %s", mcpPath)
	}
}

// gemini: the extension system owns install. `gemini extensions link` points
// the harness at the materialized package directory, so a connect re-run —
// which rewrites that tree from this binary — updates the extension in place
// with no further command. The one extension carries the six relay hooks, the
// tacit MCP server, the GEMINI.md context, and the /tacit:* commands; hook
// trust is granted in-session on first use (~/.gemini/trusted_hooks.json).
func (h *hctx) gemini(c *connectReport) {
	extDir := filepath.Join(h.plugins, "gemini")
	if !haveExec("gemini") {
		c.todo("gemini not on PATH — link the extension yourself:  gemini extensions link %s", extDir)
		c.todo("or wire manually: merge %s into ~/.gemini/settings.json", filepath.Join(extDir, "settings.json.example"))
		return
	}
	// --consent answers the install-confirmation prompt (without it the CLI
	// waits interactively); running `tacit connect` IS the member's consent to
	// installing tacit's own extension. Hook trust stays with the harness.
	out, err := exec.Command("gemini", "extensions", "link", "--consent", extDir).CombinedOutput()
	msg := strings.TrimSpace(string(out))
	switch {
	case err == nil:
		c.ok("linked the extension (%s)", extDir)
		c.todo("on the first Gemini run, approve the tacit hooks at the prompt (Gemini records the approval against their content)")
	case strings.Contains(strings.ToLower(msg), "already"):
		// Linked installs read the materialized tree directly, and connect just
		// rewrote it — the refresh already happened.
		c.ok("extension already linked — connect refreshed the content in place (%s)", extDir)
	default:
		c.todo("gemini extensions link failed (%s) — run it yourself:  gemini extensions link %s", firstLine(msg), extDir)
	}
}

// firstLine trims a CLI error dump to its lead line for one-line reports.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (h *hctx) amp(c *connectReport) {
	pluginDst := filepath.Join(h.home, ".config", "amp", "plugins", "tacit.ts")
	var se *symlinkError
	switch err := copyFile(filepath.Join(h.plugins, "amp", "plugins", "tacit.ts"), pluginDst); {
	case errors.As(err, &se):
		c.ok("event plugin already wired via symlink to %s — no change", se.target)
	case err != nil:
		c.fail("install plugin: %v", err)
	default:
		c.ok("installed the event plugin at %s", pluginDst)
	}
	skillsDst := filepath.Join(h.home, ".config", "agents", "skills")
	if symlinked, err := copyTree(filepath.Join(h.plugins, "amp", "skills"), skillsDst); err != nil {
		c.fail("copy skills: %v", err)
	} else if len(symlinked) > 0 {
		c.ok("skills: %d symlinked from another location (no change); installed the rest in %s", len(symlinked), skillsDst)
	} else {
		c.ok("skills installed in %s", skillsDst)
	}

	// settings.json is member-owned JSON: add the one key if it is absent,
	// with a backup; anything unparseable (comments etc.) gets instructions
	// instead of edits.
	settings := filepath.Join(h.home, ".config", "amp", "settings.json")
	switch addMCPServer(c, settings, "amp.mcpServers", h.bin, true) {
	case mcpUnparseable:
		c.todo("cannot parse %s — merge %s/amp/settings.jsonc.example into it yourself", settings, h.plugins)
	case mcpPresent:
		c.ok("%s already lists the tacit MCP server", settings)
	case mcpCreated:
		c.ok("wrote %s (tacit MCP server)", settings)
	case mcpAdded:
		c.ok("added the tacit MCP server to %s (backup: %s)", settings, settings+".bak-pre-tacit")
	}
	c.todo("reload Amp plugins (command palette: 'plugins: reload', or restart Amp)")
}

func (h *hctx) pi(c *connectReport) {
	pkg := filepath.Join(h.plugins, "pi")
	// pi loads every registered package: a second copy of the same package
	// (e.g. the embedded one next to a repo-checkout install) makes every
	// tacit tool a duplicate registration and pi refuses to start. One
	// registration, wherever it points, is the correct end state.
	switch other, registered := h.piExisting(pkg); {
	case registered:
		c.ok("pi package already registered (`pi list` shows it)")
		return
	case other != "":
		c.ok("pi already loads a tacit package from %s — no change (run `pi remove` first to switch to the embedded copy)", other)
		return
	}
	if !haveExec("pi") {
		c.todo("when pi is available, install the package:  pi install %s", pkg)
		return
	}
	if out, err := exec.Command("pi", "install", pkg).CombinedOutput(); err != nil {
		c.fail("pi install: %v\n%s", err, out)
		return
	}
	c.ok("installed the pi package (extension + skills; `pi list` shows it)")
}

// piExisting inspects pi's settings for tacit packages: `registered` when the
// embedded package itself is already listed, `other` when a tacit package is
// installed from somewhere else (a checkout, a git spec).
func (h *hctx) piExisting(ours string) (other string, registered bool) {
	settingsPath := filepath.Join(h.home, ".pi", "agent", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return "", false
	}
	var s struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	for _, p := range s.Packages {
		abs := p
		if !filepath.IsAbs(p) && !strings.Contains(p, ":") {
			abs = filepath.Clean(filepath.Join(filepath.Dir(settingsPath), p))
		}
		if abs == ours {
			registered = true
			continue
		}
		// Same package from elsewhere? The manifest name is the identity.
		var m struct {
			Name string `json:"name"`
		}
		if mraw, err := os.ReadFile(filepath.Join(abs, "package.json")); err == nil &&
			json.Unmarshal(mraw, &m) == nil && m.Name == "tacit-pi" {
			other = abs
		} else if strings.Contains(p, ":") && strings.Contains(p, "tacit") {
			other = p // git spec — can't read its manifest, name is the signal
		}
	}
	return other, registered
}

func (h *hctx) omp(c *connectReport) {
	// omp's own plugin commands shell out to bun; the config path works
	// everywhere (docs/harness/harness-omp.md). `config set` REPLACES the
	// array, so a member with existing extensions must merge, not paste.
	c.todo("check first: `omp config get extensions` — if a tacit.ts is already there, skip these steps; merge (do not replace) your other entries:")
	c.todo("omp config set extensions '[\"%s\"]'", filepath.Join(h.plugins, "pi", "extensions", "tacit.ts"))
	c.todo("omp config set skills.customDirectories '[\"%s\"]'", filepath.Join(h.plugins, "pi", "skills"))
}

func (h *hctx) opencode(c *connectReport) {
	base := filepath.Join(h.home, ".config", "opencode")
	var se *symlinkError
	switch err := copyFile(filepath.Join(h.plugins, "opencode", "plugin", "tacit.ts"),
		filepath.Join(base, "plugin", "tacit.ts")); {
	case errors.As(err, &se):
		c.ok("plugin already wired via symlink to %s — no change", se.target)
	case err != nil:
		c.fail("install plugin: %v", err)
	default:
		c.ok("installed the plugin at %s", filepath.Join(base, "plugin", "tacit.ts"))
	}
	if symlinked, err := copyTree(filepath.Join(h.plugins, "opencode", "command"), filepath.Join(base, "commands")); err != nil {
		c.fail("copy commands: %v", err)
	} else if len(symlinked) > 0 {
		c.ok("commands: %d symlinked from another location (no change); installed the rest in %s", len(symlinked), filepath.Join(base, "commands"))
	} else {
		c.ok("installed the /tacit-* commands in %s", filepath.Join(base, "commands"))
	}
	// The sidebar is a SECOND plugin, in a second file. connect wires the event
	// plugin below and never touched tui.json, so a member who ran it got
	// same-turn suggestions and no sidebar — while the guide describes both and
	// says nothing about which one connect gives you. Naming it as outstanding
	// puts it in the closing summary rather than leaving the member to notice a
	// missing panel.
	if tui := filepath.Join(base, "tui.json"); !fileNames(tui, "tacit") {
		c.todo("the review sidebar is a separate TUI plugin: merge %s into %s",
			filepath.Join(h.plugins, "opencode", "tui.json.example"), tui)
	} else {
		c.ok("%s already registers the tacit TUI plugin", tui)
	}
	jsonc := filepath.Join(base, "opencode.jsonc")
	raw, err := os.ReadFile(jsonc)
	switch {
	case os.IsNotExist(err):
		src, rerr := os.ReadFile(filepath.Join(h.plugins, "opencode", "opencode.jsonc.example"))
		if rerr != nil {
			c.fail("read example config: %v", rerr)
			return
		}
		if err := os.WriteFile(jsonc, src, 0o644); err != nil {
			c.fail("write %s: %v", jsonc, err)
			return
		}
		c.ok("wrote %s (tacit MCP server)", jsonc)
	case err != nil:
		c.fail("read %s: %v", jsonc, err)
	case strings.Contains(string(raw), `"tacit"`):
		c.ok("%s already lists the tacit MCP server", jsonc)
	default:
		// JSONC with member content: no reliable programmatic merge.
		c.todo("add the tacit MCP server to %s — see %s/opencode/opencode.jsonc.example", jsonc, h.plugins)
	}
}

// claudeMarketplacePath reports where Claude Code's "tacit" marketplace
// actually points ("" when unknown) — the materialized tree on a member
// machine, a repo checkout on the author's.
func (h *hctx) claudeMarketplacePath() string {
	raw, err := os.ReadFile(filepath.Join(h.home, ".claude", "plugins", "known_marketplaces.json"))
	if err != nil {
		return ""
	}
	var known map[string]struct {
		InstallLocation string `json:"installLocation"`
	}
	if json.Unmarshal(raw, &known) != nil {
		return ""
	}
	return known["tacit"].InstallLocation
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// symlinkError marks a destination that is a symlink — a deliberate wiring to
// somewhere else (typically a repo checkout). os.WriteFile would write
// THROUGH it into the target, silently modifying files connect does not own,
// so installs must skip these and say so instead.
type symlinkError struct{ dst, target string }

func (e *symlinkError) Error() string {
	return fmt.Sprintf("%s is a symlink to %s", e.dst, e.target)
}

func copyFile(src, dst string) error {
	if st, err := os.Lstat(dst); err == nil && st.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(dst)
		return &symlinkError{dst: dst, target: target}
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}

// copyTree copies srcDir's contents into dstDir (created as needed),
// overwriting existing regular files — the sources are tacit-owned
// skills/commands. Symlinked destinations are left alone and returned, so
// callers can report an existing checkout-based install instead of touching it.
func copyTree(srcDir, dstDir string) (symlinked []string, err error) {
	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(srcDir, path)
		dst := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		cerr := copyFile(path, dst)
		var se *symlinkError
		if errors.As(cerr, &se) {
			symlinked = append(symlinked, dst)
			return nil
		}
		return cerr
	})
	return symlinked, err
}
