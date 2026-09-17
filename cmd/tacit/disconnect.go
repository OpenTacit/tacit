// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit disconnect — the inverse of connect/join: unwire this machine's
// harnesses and forget the registry. The same ownership rule as connect, in
// reverse: remove only what connect (or the documented copy steps) put here —
// tacit-named files, our appended config blocks, the embedded package
// registration. Symlinks, checkout-based installs, and anything a member
// customized are reported, never deleted. Registry teardown (the operator
// side, where the org's evidence lives) is a separate explicit flag, and the
// data directory survives even that unless --purge-data says otherwise.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/llm"
	"github.com/opentacit/tacit/internal/ingress"
	"github.com/opentacit/tacit/internal/merge"
	"github.com/opentacit/tacit/internal/onnxassets"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

func cmdDisconnect(args []string) int {
	fs := flag.NewFlagSet("disconnect", flag.ContinueOnError)
	only := fs.String("harness", "", "unwire one harness: "+harnessNames()+" (default: all)")
	registry := fs.Bool("registry", false, "ALSO remove this machine's registry service and its config (needs --yes)")
	purgeData := fs.Bool("purge-data", false, "with --registry: delete the data/techniques/models directories too")
	yes := fs.Bool("yes", false, "confirm the --registry teardown")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "no home directory: %v\n", err)
		return 1
	}
	bin, _ := os.Executable()
	d := &dctx{home: home, plugins: filepath.Join(registryconfig.DataHome(), "plugins")}

	fails := 0
	for _, hs := range harnesses {
		if *only != "" && hs.name != *only {
			continue
		}
		fmt.Printf("%s:\n", hs.name)
		c := &connectReport{}
		hs.undo(d, c)
		fails += c.fails
		fmt.Println()
	}

	if *only == "" {
		// Member state: settings and the local memories the hooks accumulated.
		agentEnv := auditorconfig.AgentEnvPath(home)
		removeWithBackup(agentEnv, "member settings")
		for _, f := range []string{
			filepath.Join(home, ".tacit-technique-memory.json"),
			filepath.Join(home, ".tacit-amp-pending.json"),
			filepath.Join(home, ".tacit-hooks.log"),
		} {
			if err := os.Remove(f); err == nil {
				(&report{}).ok("removed %s", f)
			}
		}
		if err := os.RemoveAll(d.plugins); err == nil {
			(&report{}).ok("removed %s", d.plugins)
		}
		fmt.Println("    -  an active hook agent stops on its own within about 15 minutes")
	}

	if *registry {
		if !*yes {
			fmt.Fprintln(os.Stderr, "\n--registry stops the service and retires its config; run again with --yes to confirm")
			return 2
		}
		fails += teardownRegistry(home, *purgeData)
	}

	// What is left behind, said out loud. Disconnect removed settings and
	// wiring and then stopped talking, so a member who meant "get this off my
	// machine" was left with a model key on disk and a live credential on
	// somebody else's registry, and no way to know it
	// (docs/distribution/first-adoption-plan.md, Phase 2).
	if *only == "" {
		fmt.Println("\nwhat this did NOT touch:")
		acfg := auditorconfig.Load()
		if llm.ResolveKey(acfg.LLMKeyFile) != "" {
			fmt.Printf("    -  your model key is still in %s (it is yours; delete it if you are done)\n", acfg.LLMKeyFile)
		}
		fmt.Println("    -  this machine's member key is still valid on the registry.")
		fmt.Println("       Disconnecting is local; only an admin can revoke a key, on the Members page.")
		fmt.Println("       Ask for that if the machine is being retired or was lost.")
	}
	if bin != "" {
		fmt.Printf("\nthe binary itself stays; remove it with:  rm %s\n", bin)
	}
	if fails > 0 {
		return 1
	}
	return 0
}

type dctx struct {
	home, plugins string
}

func (d *dctx) claudeCode(c *connectReport) {
	d.claudeStatusLine(c)
	installed := filepath.Join(d.home, ".claude", "plugins", "installed_plugins.json")
	if raw, err := os.ReadFile(installed); err == nil && strings.Contains(string(raw), `"tacit@`) {
		c.todo("inside Claude Code run:  /plugin uninstall tacit@tacit   (the plugin system owns its files)")
		return
	}
	c.ok("no tacit plugin installed")
}

// claudeStatusLine takes back the settings.json entry connect wrote, and only
// that: a status line running any other command is the member's, whatever it
// mentions.
func (d *dctx) claudeStatusLine(c *connectReport) {
	path := filepath.Join(d.home, ".claude", "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	settings := map[string]any{}
	if json.Unmarshal(raw, &settings) != nil {
		return
	}
	entry, present := settings["statusLine"].(map[string]any)
	if !present {
		return
	}
	if cmd, _ := entry["command"].(string); !strings.HasSuffix(cmd, "tacit statusline") {
		c.ok("no change to the status line (not ours)")
		return
	}
	delete(settings, "statusLine")
	out, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		c.fail("update %s: %v", path, err)
		return
	}
	c.ok("removed the status line from %s", path)
}

func (d *dctx) codex(c *connectReport) {
	removeTacitEntries(c, filepath.Join(d.home, ".codex", "skills"))
	cfgPath := filepath.Join(d.home, ".codex", "config.toml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		c.ok("no config to unwind")
		return
	}
	// Reverse the merge only when it is provably ours: the backup connect
	// wrote plus our snippet reproduces the current content exactly. Anything
	// else has member edits layered on top — instructions, not surgery.
	backup := cfgPath + ".bak-pre-tacit"
	if pre, err := os.ReadFile(backup); err == nil &&
		string(raw) == string(pre)+"\n"+string(codexSnippet(d.plugins)) {
		if err := os.Rename(backup, cfgPath); err != nil {
			c.fail("restore %s: %v", backup, err)
			return
		}
		c.ok("restored %s from its pre-tacit backup", cfgPath)
		return
	}
	if string(raw) == string(codexSnippet(d.plugins)) {
		if err := os.Remove(cfgPath); err != nil {
			c.fail("remove %s: %v", cfgPath, err)
			return
		}
		c.ok("removed %s (it held only the tacit wiring)", cfgPath)
		return
	}
	if strings.Contains(string(raw), "tacit") {
		c.todo("%s has tacit wiring mixed with your edits — remove [mcp_servers.tacit] and the hook-relay [[hooks.*]] blocks yourself", cfgPath)
		return
	}
	c.ok("no tacit wiring in %s", cfgPath)
}

func (d *dctx) gemini(c *connectReport) {
	// The extension system owns the registration; a linked install points at
	// the materialized tree, which the caller (cmdDisconnect) removes. Unlink
	// via the CLI when it is present; otherwise say what to run.
	if haveExec("gemini") {
		out, err := exec.Command("gemini", "extensions", "uninstall", "tacit").CombinedOutput()
		msg := strings.ToLower(strings.TrimSpace(string(out)))
		switch {
		case err == nil:
			c.ok("uninstalled the tacit extension")
		case strings.Contains(msg, "not found") || strings.Contains(msg, "not installed"):
			c.ok("no tacit extension installed")
		default:
			c.todo("run `gemini extensions uninstall tacit` yourself (CLI uninstall failed: %s)", firstLine(strings.TrimSpace(string(out))))
		}
	} else {
		c.todo("if you linked the extension: gemini extensions uninstall tacit")
	}
	// Manual settings wiring is member-merged, never surgically reversed.
	settings := filepath.Join(d.home, ".gemini", "settings.json")
	if raw, err := os.ReadFile(settings); err == nil && strings.Contains(string(raw), "hook-relay gemini") {
		c.todo("%s has manual tacit wiring — remove the tacit-relay hooks and mcpServers.tacit entries yourself", settings)
	}
}

func (d *dctx) copilot(c *connectReport) {
	removeOwnedFile(c, filepath.Join(d.home, ".copilot", "hooks", "tacit.json"), "hooks file")
	removeTacitEntries(c, filepath.Join(d.home, ".copilot", "skills"))
	mcpPath := filepath.Join(d.home, ".copilot", "mcp-config.json")
	if !removeMCPServer(c, mcpPath, "mcpServers") {
		c.ok("no mcp-config to unwind")
	}
}

func (d *dctx) cursor(c *connectReport) {
	removeTacitEntries(c, filepath.Join(d.home, ".cursor", "commands"))
	// hooks.json: strip exactly the entries whose command names our relay —
	// the same signature connect's merge keyed on — and drop any event whose
	// list that empties.
	hooksPath := filepath.Join(d.home, ".cursor", "hooks.json")
	raw, err := os.ReadFile(hooksPath)
	if err != nil {
		c.ok("no hooks.json to unwind")
	} else {
		var cfg map[string]any
		if json.Unmarshal(raw, &cfg) != nil {
			c.todo("cannot parse %s — remove the hook-relay cursor entries yourself", hooksPath)
		} else if events, _ := cfg["hooks"].(map[string]any); events == nil {
			c.ok("no tacit hooks in %s", hooksPath)
		} else {
			removed := 0
			for ev, def := range events {
				list, _ := def.([]any)
				kept := make([]any, 0, len(list))
				for _, entry := range list {
					em, _ := entry.(map[string]any)
					cmd := ""
					if em != nil {
						cmd, _ = em["command"].(string)
					}
					if strings.Contains(cmd, "hook-relay cursor") {
						removed++
						continue
					}
					kept = append(kept, entry)
				}
				if len(kept) == 0 {
					delete(events, ev)
				} else {
					events[ev] = kept
				}
			}
			switch {
			case removed == 0:
				c.ok("no tacit hooks in %s", hooksPath)
			case len(events) == 0:
				if err := os.Remove(hooksPath); err != nil {
					c.fail("remove %s: %v", hooksPath, err)
				} else {
					c.ok("removed %s (it held only the tacit wiring)", hooksPath)
				}
			default:
				out, _ := json.MarshalIndent(cfg, "", "  ")
				if err := os.WriteFile(hooksPath, append(out, '\n'), 0o644); err != nil {
					c.fail("update %s: %v", hooksPath, err)
				} else {
					c.ok("removed %d tacit hook entr(ies) from %s", removed, hooksPath)
				}
			}
		}
	}
	mcpPath := filepath.Join(d.home, ".cursor", "mcp.json")
	if !removeMCPServer(c, mcpPath, "mcpServers") {
		c.ok("no mcp.json to unwind")
	}
}

func (d *dctx) amp(c *connectReport) {
	removeOwnedFile(c, filepath.Join(d.home, ".config", "amp", "plugins", "tacit.ts"), "event plugin")
	removeTacitEntries(c, filepath.Join(d.home, ".config", "agents", "skills"))
	settings := filepath.Join(d.home, ".config", "amp", "settings.json")
	if !removeMCPServer(c, settings, "amp.mcpServers") {
		c.ok("no settings to unwind")
	}
}

func (d *dctx) pi(c *connectReport) {
	settingsPath := filepath.Join(d.home, ".pi", "agent", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		c.ok("no pi settings")
		return
	}
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil {
		c.todo("cannot parse %s — remove the tacit package entry yourself", settingsPath)
		return
	}
	ours := filepath.Join(d.plugins, "pi")
	pkgs, _ := cfg["packages"].([]any)
	kept := make([]any, 0, len(pkgs))
	removed, foreign := 0, ""
	for _, p := range pkgs {
		entry, _ := p.(string)
		abs := entry
		if entry != "" && !filepath.IsAbs(entry) && !strings.Contains(entry, ":") {
			abs = filepath.Clean(filepath.Join(filepath.Dir(settingsPath), entry))
		}
		switch {
		case abs == ours:
			removed++
		case strings.Contains(abs, "tacit"):
			foreign = abs
			kept = append(kept, p)
		default:
			kept = append(kept, p)
		}
	}
	if removed == 0 {
		if foreign != "" {
			c.ok("the registered tacit package is %s (not connect's) — no change; remove it yourself with `pi remove` if you want", foreign)
		} else {
			c.ok("no tacit package registered")
		}
		return
	}
	cfg["packages"] = kept
	out, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o644); err != nil {
		c.fail("update %s: %v", settingsPath, err)
		return
	}
	c.ok("unregistered the embedded package from %s", settingsPath)
	if foreign != "" {
		c.ok("a checkout-based tacit package remains (%s) — no change", foreign)
	}
}

func (d *dctx) omp(c *connectReport) {
	c.todo("if you wired omp: remove the tacit entries with `omp config set extensions ...` / `omp config set skills.customDirectories ...` (config get shows the current values)")
}

func (d *dctx) opencode(c *connectReport) {
	base := filepath.Join(d.home, ".config", "opencode")
	removeOwnedFile(c, filepath.Join(base, "plugin", "tacit.ts"), "plugin")
	removeTacitEntries(c, filepath.Join(base, "commands"))
	jsonc := filepath.Join(base, "opencode.jsonc")
	raw, err := os.ReadFile(jsonc)
	if err != nil {
		c.ok("no opencode.jsonc to unwind")
		return
	}
	example, exErr := os.ReadFile(filepath.Join(d.plugins, "opencode", "opencode.jsonc.example"))
	if exErr == nil && string(raw) == string(example) {
		if err := os.Remove(jsonc); err != nil {
			c.fail("remove %s: %v", jsonc, err)
			return
		}
		c.ok("removed %s (it held only the tacit wiring)", jsonc)
		return
	}
	if strings.Contains(string(raw), `"tacit"`) {
		c.todo(`remove the "tacit" mcp entry from %s yourself (member edits present)`, jsonc)
		return
	}
	c.ok("no tacit wiring in %s", jsonc)
}

// removeOwnedFile deletes a tacit-named regular file; a symlink there is a
// checkout wiring disconnect does not own.
func removeOwnedFile(c *connectReport, path, label string) {
	st, err := os.Lstat(path)
	if err != nil {
		c.ok("no %s installed", label)
		return
	}
	if st.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(path)
		c.todo("%s is your symlink to %s — remove it yourself if you want", path, target)
		return
	}
	if err := os.Remove(path); err != nil {
		c.fail("remove %s: %v", path, err)
		return
	}
	c.ok("removed %s", path)
}

// removeTacitEntries deletes tacit-* files and directories inside dir —
// exactly the namespace the skill/command payloads occupy. Symlinked entries
// are left (checkout wiring).
func removeTacitEntries(c *connectReport, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.ok("nothing in %s", dir)
		return
	}
	removed, kept := 0, 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "tacit-") && e.Name() != "tacit" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
			kept++
			continue
		}
		if err := os.RemoveAll(path); err == nil {
			removed++
		}
	}
	switch {
	case removed == 0 && kept == 0:
		c.ok("no tacit entries in %s", dir)
	case kept > 0:
		c.ok("removed %d tacit entr(ies) from %s; kept %d symlinked one(s)", removed, dir, kept)
	default:
		c.ok("removed %d tacit entr(ies) from %s", removed, dir)
	}
}

func removeWithBackup(path, label string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	retired := path + ".removed-" + time.Now().Format("20060102-150405")
	if err := os.Rename(path, retired); err != nil {
		(&report{w: os.Stderr}).fail("retire %s: %v", path, err)
		return
	}
	(&report{}).ok("%s retired to %s", label, retired)
}

// releaseIngressName hands this registry's public hostname back to the shared
// ingress before its settings are removed.
//
// Nothing did this outside `tacit merge`, so a registry that was published and
// then torn down kept its name allocated for good. That was survivable while
// publishing was opt-in and rare. It is not now: `tacit init` gives every new
// registry an address by default, and somebody who tries the product and walks
// away is the ordinary case — each one holding a name on a shared proxy nobody
// can reclaim.
//
// Gated on the switch rather than on the key, because init writes an instance
// key whether or not anything was ever published, and dialling an ingress that
// was never used costs a twenty-second timeout on a machine with no route to it.
//
// Failure is reported and not fatal. The rest of the teardown is local and
// still worth doing, and a name that could not be released is a thing to say
// out loud rather than a reason to leave the unit installed.
func releaseIngressName() {
	cfg := registryconfig.Load()
	if !cfg.GlobalAccess {
		return
	}
	configDir := filepath.Dir(registryconfig.RegistryEnvPath())
	name, err := merge.ReleaseAddress(configDir, cfg.PublishIngress, cfg.PublishTLS, version)
	switch {
	case errors.Is(err, merge.ErrNoAddress):
		return
	case err != nil:
		(&report{w: os.Stderr}).warn("could not release this registry's public address from %s: %v",
			cfg.PublishIngress, err)
		(&report{}).info("the name stays allocated; the instance key is still at %s",
			ingress.KeyPath(configDir))
	case name == "":
		(&report{}).ok("the ingress had no record of this registry — nothing to release")
	default:
		(&report{}).ok("released %s back to %s", name, cfg.PublishIngress)
	}
}

// teardownRegistry stops the operator side. The unit/plist goes only when it
// is the one init wrote; the evidence directories survive unless --purge-data.
func teardownRegistry(home string, purgeData bool) (fails int) {
	fmt.Println("registry:")
	if runtime.GOOS == "linux" && haveExec("systemctl") {
		// Gate on this home's unit file: systemctl acts on the session, not
		// the HOME, so an unconditional disable could stop a service this
		// invocation does not own.
		unit := filepath.Join(home, ".config", "systemd", "user", "tacit-registry.service")
		if raw, err := os.ReadFile(unit); err == nil {
			_ = exec.Command("systemctl", "--user", "disable", "--now", "tacit-registry.service").Run()
			(&report{}).ok("service stopped and disabled")
			if strings.Contains(string(raw), "written by tacit init") {
				_ = os.Remove(unit)
				_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
				(&report{}).ok("removed %s", unit)
			} else {
				(&report{}).todo("%s is not init's unit — remove it yourself if you want", unit)
			}
		} else {
			fmt.Println("    -  no tacit-registry unit in this home")
		}
	}
	if runtime.GOOS == "darwin" {
		plist := filepath.Join(home, "Library", "LaunchAgents", "com.tacit.registry.plist")
		if _, err := os.Stat(plist); err == nil {
			_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/com.tacit.registry", os.Getuid())).Run()
			_ = os.Remove(plist)
			(&report{}).ok("unloaded and removed %s", plist)
		}
	}
	releaseIngressName()
	removeWithBackup(registryconfig.RegistryEnvPath(), "registry settings")
	dataHome := registryconfig.DataHome()
	if purgeData {
		for _, sub := range []string{"data", "techniques", "models", "onnxruntime-" + onnxassets.Version} {
			p := filepath.Join(dataHome, sub)
			if err := os.RemoveAll(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				(&report{w: os.Stderr}).fail("remove %s: %v", p, err)
				fails++
			} else {
				(&report{}).ok("removed %s", p)
			}
		}
	} else {
		fmt.Printf("    -  evidence kept: %s/{data,techniques,models} (delete with --purge-data)\n", dataHome)
	}
	return fails
}
