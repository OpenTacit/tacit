// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/opentacit/tacit"
	"github.com/opentacit/tacit/internal/auditor/llm"
)

// The founder bug: `tacit join` on a machine that already ran the
// checkout-based pi package registered a SECOND tacit-pi package, and pi
// refused to start on duplicate tool registrations. connect must recognize a
// tacit package installed from anywhere else and leave the machine alone.
func TestPiExistingDetectsCheckoutInstall(t *testing.T) {
	home := t.TempDir()
	checkout := filepath.Join(home, "Repos", "tacit", "plugins", "pi")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "package.json"), []byte(`{"name":"tacit-pi"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settingsDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Relative package path, exactly as pi records it.
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"),
		[]byte(`{"packages":["../../Repos/tacit/plugins/pi"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &hctx{home: home, plugins: filepath.Join(home, ".local", "share", "tacit", "plugins")}
	other, registered := h.piExisting(filepath.Join(h.plugins, "pi"))
	if registered {
		t.Error("embedded package reported registered; it is not")
	}
	if other != checkout {
		t.Errorf("other install = %q, want %q", other, checkout)
	}

	// Once the embedded package IS the registered one, report that instead.
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"),
		[]byte(`{"packages":["`+filepath.Join(h.plugins, "pi")+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	other, registered = h.piExisting(filepath.Join(h.plugins, "pi"))
	if !registered || other != "" {
		t.Errorf("registered=%v other=%q, want true and empty", registered, other)
	}
}

// The codex config merge must be reversible: connect appends to a member's
// config with a backup, disconnect restores the original bytes — and refuses
// when member edits landed on top.
func TestCodexConfigMergeRoundTrip(t *testing.T) {
	home := t.TempDir()
	plugins := filepath.Join(home, "plugins")
	if _, err := tacit.MaterializePlugins(plugins, "/opt/tacit"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	member := "model = \"o3\"\n"
	if err := os.WriteFile(cfgPath, []byte(member), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &hctx{home: home, bin: "/opt/tacit", plugins: plugins}
	h.codex(&connectReport{})
	raw, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(raw), "model = \"o3\"") || !strings.Contains(string(raw), "[mcp_servers.tacit]") {
		t.Fatalf("merge lost content:\n%s", raw)
	}

	d := &dctx{home: home, plugins: plugins}
	d.codex(&connectReport{})
	raw, _ = os.ReadFile(cfgPath)
	if string(raw) != member {
		t.Errorf("disconnect did not restore the original config:\n%s", raw)
	}

	// With member edits on top of the merge, disconnect must not touch it.
	h.codex(&connectReport{})
	f, _ := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintln(f, "# my note")
	f.Close()
	edited, _ := os.ReadFile(cfgPath)
	d.codex(&connectReport{})
	after, _ := os.ReadFile(cfgPath)
	if string(after) != string(edited) {
		t.Error("disconnect modified a config with member edits")
	}
}

// copyFile must never write through a symlink: the destination may point into
// a repo checkout connect does not own.
func TestCopyFileRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "checkout-file.md")
	if err := os.WriteFile(target, []byte("checkout content"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "src.md")
	if err := os.WriteFile(src, []byte("embedded content"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := copyFile(src, link)
	var se *symlinkError
	if !errors.As(err, &se) {
		t.Fatalf("copyFile over symlink: err=%v, want symlinkError", err)
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "checkout content" {
		t.Errorf("symlink target modified: %q", raw)
	}
}

// The status line is where a rotated key or a model out of credit gets
// reported, so connect has to install it rather than document it — while
// leaving a member who already has one alone.
func TestStatusLineWiringRoundTrip(t *testing.T) {
	read := func(t *testing.T, path string) map[string]any {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]any{}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("wired into settings a member already has", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"model":"opus"}`), 0o600); err != nil {
			t.Fatal(err)
		}

		(&hctx{home: home, bin: "/opt/tacit"}).claudeStatusLine(&connectReport{})
		settings := read(t, path)
		if settings["model"] != "opus" {
			t.Errorf("clobbered a member setting: %v", settings)
		}
		entry, ok := settings["statusLine"].(map[string]any)
		if !ok || entry["command"] != "/opt/tacit statusline" {
			t.Fatalf("status line not wired: %v", settings["statusLine"])
		}
		if _, err := os.Stat(path + ".bak-pre-tacit"); err != nil {
			t.Error("edited a member's settings with no backup")
		}

		// Reversible, and idempotent on the way in.
		(&hctx{home: home, bin: "/opt/tacit"}).claudeStatusLine(&connectReport{})
		(&dctx{home: home}).claudeStatusLine(&connectReport{})
		settings = read(t, path)
		if _, present := settings["statusLine"]; present {
			t.Errorf("disconnect left the status line behind: %v", settings)
		}
		if settings["model"] != "opus" {
			t.Errorf("disconnect took a member setting with it: %v", settings)
		}
	})

	t.Run("no settings file yet", func(t *testing.T) {
		home := t.TempDir()
		(&hctx{home: home, bin: "/opt/tacit"}).claudeStatusLine(&connectReport{})
		entry, ok := read(t, filepath.Join(home, ".claude", "settings.json"))["statusLine"].(map[string]any)
		if !ok || entry["command"] != "/opt/tacit statusline" {
			t.Fatalf("status line not wired: %v", entry)
		}
	})

	t.Run("a member's own status line keeps the slot", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mine := `{"statusLine":{"type":"command","command":"~/bin/my-line.sh"}}`
		if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
			t.Fatal(err)
		}

		c := &connectReport{}
		(&hctx{home: home, bin: "/opt/tacit"}).claudeStatusLine(c)
		if c.todos != 1 {
			t.Errorf("want one todo telling the member how to compose, got %d", c.todos)
		}
		if entry := read(t, path)["statusLine"].(map[string]any); entry["command"] != "~/bin/my-line.sh" {
			t.Fatalf("took a member's status line: %v", entry)
		}
		// And disconnect leaves it just as alone.
		(&dctx{home: home}).claudeStatusLine(&connectReport{})
		if entry := read(t, path)["statusLine"].(map[string]any); entry["command"] != "~/bin/my-line.sh" {
			t.Fatalf("disconnect removed a status line it did not write: %v", entry)
		}
	})
}

// The false positive: `strings.Contains(raw, "hooks.")` matched ANY hook the
// member had ever written, so a Codex config with an unrelated hook was
// reported as "already has tacit wiring" and left untouched. The member got a
// green run and a tool that never loaded Tacit.
//
// The guard was also unnecessary. Every hook the snippet writes is an
// array-of-tables entry, and TOML appends those, so the member's hooks and
// ours both survive the merge.
func TestCodexWiresBesideAMembersOwnHooks(t *testing.T) {
	home := t.TempDir()
	plugins := filepath.Join(home, "plugins")
	if _, err := tacit.MaterializePlugins(plugins, "/opt/tacit"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	member := "[[hooks.Stop]]\n[[hooks.Stop.hooks]]\ntype = \"command\"\ncommand = \"my-own-thing\"\n"
	if err := os.WriteFile(cfgPath, []byte(member), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &connectReport{w: io.Discard}
	(&hctx{home: home, bin: "/opt/tacit", plugins: plugins}).codex(c)
	raw, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(raw), codexMarker) {
		t.Errorf("codex was not wired beside the member's own hook:\n%s", raw)
	}
	if !strings.Contains(string(raw), "my-own-thing") {
		t.Errorf("the member's hook did not survive the merge:\n%s", raw)
	}
	if c.fails > 0 {
		t.Errorf("a safe additive merge reported %d fail", c.fails)
	}
	// The one todo left is Codex's own trust prompt — a host action we cannot
	// perform — and not a refusal to merge.
	if len(c.todoLines) != 1 || !strings.Contains(c.todoLines[0], "Trust all and continue") {
		t.Errorf("todos = %q, want only the hooks-review prompt", c.todoLines)
	}

	// Second run: the marker is there now, so there is nothing to do and
	// nothing to append.
	before := string(raw)
	(&hctx{home: home, bin: "/opt/tacit", plugins: plugins}).codex(&connectReport{w: io.Discard})
	if after, _ := os.ReadFile(cfgPath); string(after) != before {
		t.Error("a second connect appended to an already-wired config")
	}
}

// The one shape the append cannot survive: a plain [hooks.X] table beside the
// [[hooks.X]] entries the snippet writes is a TOML type error, and Codex fails
// to parse the whole file. Refuse, name the line, change nothing.
func TestCodexRefusesToAppendBesideAPlainHooksTable(t *testing.T) {
	home := t.TempDir()
	plugins := filepath.Join(home, "plugins")
	if _, err := tacit.MaterializePlugins(plugins, "/opt/tacit"); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	member := "[hooks.Stop]\nwhatever = true\n"
	if err := os.WriteFile(cfgPath, []byte(member), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &connectReport{w: io.Discard}
	(&hctx{home: home, bin: "/opt/tacit", plugins: plugins}).codex(c)
	if raw, _ := os.ReadFile(cfgPath); string(raw) != member {
		t.Errorf("connect wrote to a config it cannot safely append to:\n%s", raw)
	}
	if c.todos != 1 {
		t.Errorf("todos = %d, want 1 naming the conflict", c.todos)
	}
	if len(c.todoLines) == 0 || !strings.Contains(c.todoLines[0], "[hooks.Stop]") {
		t.Errorf("the todo does not name the offending line: %q", c.todoLines)
	}
}

// A todo is work the tool cannot load OpenTacit without. The run counted them and
// discarded the count (`_ = todos`), so the closing block read the same whether
// the member had steps left or not.
func TestConnectSummarySaysWhoIsWaitingOnTheMember(t *testing.T) {
	var b strings.Builder
	summarizeConnect(&b, []string{"cursor"},
		[]harnessTodos{{name: "codex", todos: []string{"choose 'Trust all and continue'"}}}, 0)
	got := b.String()
	for _, want := range []string{"ready: cursor", "waiting on you", "codex", "Trust all and continue"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "nothing left to do") {
		t.Errorf("a run with outstanding work called itself finished:\n%s", got)
	}

	b.Reset()
	summarizeConnect(&b, []string{"cursor", "codex"}, nil, 0)
	if got := b.String(); !strings.Contains(got, "nothing left to do") {
		t.Errorf("a finished run does not say so:\n%s", got)
	}
}

// The key file exists so the secret stays out of shell history, and the
// command printed `echo 'TACIT_LLM_API_KEY=…' >> file` — the one instruction
// guaranteed to put it there. Nothing may offer that shape again.
func TestModelKeyIsNeverTaughtAsAShellCommand(t *testing.T) {
	// String literals only. The comment above the function quotes the old
	// instruction on purpose, to say why the function no longer prints it, and
	// a test that cannot tell the two apart would forbid recording the reason.
	f, err := parser.ParseFile(token.NewFileSet(), "modelkey.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if strings.Contains(v, "echo ") && strings.Contains(v, llm.KeyEnv) {
			t.Errorf("printed text teaches the member to echo their key into a file: %q", v)
		}
		return true
	})
}

// The key is written 0600 and replaces an earlier line rather than stacking a
// second one, which ResolveKey would silently shadow.
func TestStoreModelKeyReplacesAndStaysPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "tacit-key.env")
	if err := storeModelKey(path, "sk-first"); err != nil {
		t.Fatal(err)
	}
	if err := storeModelKey(path, "sk-second"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), llm.KeyEnv+"="); n != 1 {
		t.Errorf("%d key lines in the file, want 1:\n%s", n, raw)
	}
	if got := llm.ResolveKey(path); got != "sk-second" {
		t.Errorf("resolved %q, want the key written last", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v, want 0600", st.Mode().Perm())
	}
}

// A member's other settings in the same file survive.
func TestStoreModelKeyKeepsOtherLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tacit-key.env")
	if err := os.WriteFile(path, []byte("TACIT_LLM_MODEL=something\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storeModelKey(path, "sk-1"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "TACIT_LLM_MODEL=something") {
		t.Errorf("an unrelated setting was dropped:\n%s", raw)
	}
}

// `tacit init` must never stop for a question. reportModelKey is printed from
// the MIDDLE of init's report — above the registry address, the API key and
// the owner sign-in link — so a prompt there strands the operator at a
// question with no way yet to open the registry they just made. It is what
// hack/first_run.sh renders, and it read as broken because it was.
//
// The offer belongs at the end of an interactive setup, which is where connect
// puts it. This holds the line by construction: the reporting function does
// not reach the prompting one.
func TestReportModelKeyNeverPrompts(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "modelkey.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var reports *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "reportModelKey" {
			reports = fn
		}
	}
	if reports == nil {
		t.Fatal("no reportModelKey in modelkey.go")
	}
	ast.Inspect(reports, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok {
			switch id.Name {
			case "offerModelKey", "stdinIsTerminal":
				t.Errorf("reportModelKey calls %s — it reports, and a caller decides where a question goes", id.Name)
			}
		}
		return true
	})
}

// The console first run says everything in its own words and then serves in
// the same process, so the registry's startup log repeated those facts
// timestamped and tagged by subsystem, at somebody who had just met the
// product. Only the informational lines go quiet: anything reporting trouble
// keeps log.Printf, or a first run that half-works says nothing about it.
func TestOnlyInformationalStartupLinesGoQuiet(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	// Lines that must never be silenced: they are the ones a person acts on.
	mustShout := []string{
		"FIRST RUN: this registry is awaiting configuration.",
		"cannot write the user guide to",
		"demo mode disabled",
	}
	for _, want := range mustShout {
		i := strings.Index(string(raw), want)
		if i < 0 {
			continue // the line moved or went; the others still hold the rule
		}
		line := string(raw[strings.LastIndex(string(raw[:i]), "\n")+1 : i])
		if strings.Contains(line, "logStartup(") {
			t.Errorf("%q is routed through logStartup and would vanish on a console first run", want)
		}
	}
	// And the ones that duplicate init's own report are quiet.
	for _, want := range []string{
		`logStartup("[tacit] storage backend: embedded file store`,
		`logStartup("[tacit] publishing through`,
		`logStartup("[tacit] startup synced=`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("expected %q to be quiet on a console first run", want)
		}
	}
}
