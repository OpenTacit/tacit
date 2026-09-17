// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Command tacit-genplugins generates every per-harness OpenTacit skill/command file
// from the single canonical source per skill under plugins/_src/.
//
// It is the one source of truth for the ~95 harness instruction files that used
// to be hand-synced (13 skills x 8 harnesses). Edit plugins/_src/<skill>.md and
// run `make plugins`; never hand-edit a generated file (CI enforces this via
// pluginsgen_test.go).
//
// Dependency-free: stdlib only (text/template).
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// skillOrder is the deterministic list of logical skills. The `feedback` skill
// was retired (plan P0.3): implicit reaction capture replaces it.
var skillOrder = []string{
	"setup", "search", "review", "insights", "org", "usage",
	"drafts", "contribute", "audit", "status", "test", "testfeedback",
	"help", "suggest",
}

// toolWire maps a logical tool key to its bare MCP wire name. Renaming a tool
// (e.g. insights/org/map -> metrics) is a single edit here, not across 100 files.
var toolWire = map[string]string{
	"search": "tacit_search",
	// insights/org/map are consolidated into one tacit_metrics tool with a
	// `view` argument (funnel|cohorts|map); the skills call metrics now.
	"metrics":      "tacit_metrics",
	"drafts":       "tacit_drafts",
	"draft_action": "tacit_draft_action",
}

// Harness describes one target harness's file format, path layout, and the
// per-harness deltas (invocation idiom, argument reference, tool-name qualifier).
type Harness struct {
	Name      string // directory key
	Format    string // skillmd | cmd_cc | cursor | toml | cmd_oc
	PathFmt   string // relative path under plugins/, %s = slug
	DescField string // "short" or "long": which canonical description this harness shows
	IdiomFmt  string // how the member invokes a skill, %s = slug (e.g. "/tacit:%s")
	QuoteDesc bool   // wrap the YAML description scalar in double quotes
	QualTools bool   // qualify MCP tool names as mcp__plugin_tacit_tacit__<wire>
	ArgToken  string // literal argument token ($ARGUMENTS / {{args}}); empty => use ArgProse
	ArgProse  string // prose argument reference when ArgToken is empty, %s = idiom
	ArgNote   bool   // append " (arguments: <hint>)" to the description (opencode)
	SkipNativ bool   // skip skills whose claude-code form is a hand-maintained native skill
	Picker    string // native question-form tool name; empty => this harness has none
}

// nativeSkills are hand-maintained per-harness variants kept OUTSIDE the
// generator. claude-code's contribute is a native AskUserQuestion skill at
// plugins/claude-code/skills/contribute/ and is intentionally not generated.
var nativeSkills = map[string]map[string]bool{
	"claude-code": {"contribute": true},
}

var harnesses = []Harness{
	{Name: "amp", Format: "skillmd", PathFmt: "amp/skills/tacit-%s/SKILL.md", DescField: "long", IdiomFmt: "$tacit-%s", QuoteDesc: true, ArgProse: "what the member asked for, in plain words", Picker: "tacit_form"},
	{Name: "codex", Format: "skillmd", PathFmt: "codex/skills/tacit-%s/SKILL.md", DescField: "long", IdiomFmt: "$tacit-%s", ArgProse: "whatever the member wrote after the %s mention"},
	{Name: "copilot", Format: "skillmd", PathFmt: "copilot/skills/tacit-%s/SKILL.md", DescField: "long", IdiomFmt: "tacit %s", QuoteDesc: true, ArgProse: "whatever the member wrote after the \"%s\" request"},
	{Name: "pi", Format: "skillmd", PathFmt: "pi/skills/tacit-%s/SKILL.md", DescField: "long", IdiomFmt: "/skill:tacit-%s", ArgProse: "whatever the member wrote after the %s command", Picker: "AskUserQuestion"},
	{Name: "claude-code", Format: "cmd_cc", PathFmt: "claude-code/commands/%s.md", DescField: "short", IdiomFmt: "/tacit:%s", QualTools: true, ArgToken: "$ARGUMENTS", SkipNativ: true, Picker: "AskUserQuestion"},
	{Name: "cursor", Format: "cursor", PathFmt: "cursor/commands/tacit-%s.md", DescField: "long", IdiomFmt: "/tacit-%s", ArgProse: "whatever the member wrote after the %s command"},
	{Name: "gemini", Format: "toml", PathFmt: "gemini/commands/tacit/%s.toml", DescField: "short", IdiomFmt: "/tacit:%s", ArgToken: "{{args}}"},
	{Name: "opencode", Format: "cmd_oc", PathFmt: "opencode/command/tacit-%s.md", DescField: "short", IdiomFmt: "/tacit-%s", ArgToken: "$ARGUMENTS", ArgNote: true},
}

// preamble is the shared registry-resolve block that opens most skills. It used
// to inline an eight-line bash block (source agent.env, default the URL and key,
// probe /v1/health for the dashboard URL) in every skill of every harness; that
// resolution now lives in the binary as `tacit env`, so the preamble is one line.
// Exposed to bodies as {{preamble}}.
const preamble = "## First: resolve the registry address\n\n" +
	"Run this once. Each command block starts a fresh shell, so substitute the\n" +
	"**resolved values** into every command and put the resolved URL (`$DASH`) in\n" +
	"member-facing links:\n\n" +
	"```bash\n" +
	"eval \"$(tacit env)\"   # sets REG (API base), KEY (X-Tacit-Key), DASH (dashboard URL)\n" +
	"```"

// skillSrc is one parsed canonical skill source.
type skillSrc struct {
	Slug    string
	Short   string
	Long    string
	ArgHint string
	Allowed string
	Body    string
}

// renderCtx is what a canonical body sees. Picker is the one field bodies
// branch on ({{if .Picker}}): only claude-code and pi expose a native question
// form, so a probe or flow that needs one must degrade for the other six.
type renderCtx struct {
	Slug   string
	Idiom  string
	ArgRef string
	Picker string
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit-genplugins:", err)
		os.Exit(1)
	}
	out := root
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	files, err := Generate(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit-genplugins:", err)
		os.Exit(1)
	}
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		p := filepath.Join(out, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "tacit-genplugins:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(p, files[rel], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "tacit-genplugins:", err)
			os.Exit(1)
		}
	}
	fmt.Printf("tacit-genplugins: wrote %d files under %s/plugins\n", len(files), out)
}

// Generate reads plugins/_src under root and returns the full set of generated
// files keyed by their repo-relative path. It writes nothing; callers persist
// the map. Deterministic.
func Generate(root string) (map[string][]byte, error) {
	srcDir := filepath.Join(root, "plugins", "_src")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, fmt.Errorf("read _src: %w", err)
	}
	srcBySlug := map[string]skillSrc{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			return nil, err
		}
		s, err := parseSrc(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		srcBySlug[s.Slug] = s
	}
	// Confirm every declared skill has a source.
	for _, slug := range skillOrder {
		if _, ok := srcBySlug[slug]; !ok {
			return nil, fmt.Errorf("missing _src for skill %q", slug)
		}
	}

	out := map[string][]byte{}
	for _, h := range harnesses {
		for _, slug := range skillOrder {
			if h.SkipNativ && nativeSkills[h.Name][slug] {
				continue
			}
			s := srcBySlug[slug]
			content, err := renderFile(h, s)
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", h.Name, slug, err)
			}
			rel := filepath.Join("plugins", fmt.Sprintf(h.PathFmt, slug))
			out[rel] = []byte(content)
		}
	}
	return out, nil
}

// parseSrc splits a canonical source file into its +++ metadata block and body.
func parseSrc(raw string) (skillSrc, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "+++" {
		return skillSrc{}, fmt.Errorf("expected +++ frontmatter fence")
	}
	var s skillSrc
	i := 1
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "+++" {
			i++
			break
		}
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return skillSrc{}, fmt.Errorf("bad metadata line: %q", line)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "slug":
			s.Slug = v
		case "short":
			s.Short = v
		case "long":
			s.Long = v
		case "arg_hint":
			s.ArgHint = v
		case "allowed_tools":
			s.Allowed = v
		default:
			return skillSrc{}, fmt.Errorf("unknown metadata key: %q", k)
		}
	}
	body := strings.Join(lines[i:], "\n")
	body = strings.TrimLeft(body, "\n")
	body = strings.TrimRight(body, "\n")
	s.Body = body
	if s.Slug == "" {
		return skillSrc{}, fmt.Errorf("missing slug")
	}
	return s, nil
}

func funcMapFor(h Harness) template.FuncMap {
	return template.FuncMap{
		"preamble": func() string { return preamble },
		"tool": func(key string) (string, error) {
			wire, ok := toolWire[key]
			if !ok {
				return "", fmt.Errorf("unknown tool key %q", key)
			}
			if h.QualTools {
				return "mcp__plugin_tacit_tacit__" + wire, nil
			}
			return wire, nil
		},
		"cmd": func(slug string) string { return fmt.Sprintf(h.IdiomFmt, slug) },
	}
}

func ctxFor(h Harness, s skillSrc) renderCtx {
	idiom := fmt.Sprintf(h.IdiomFmt, s.Slug)
	argRef := h.ArgToken
	if argRef == "" {
		if strings.Contains(h.ArgProse, "%s") {
			argRef = fmt.Sprintf(h.ArgProse, idiom)
		} else {
			argRef = h.ArgProse
		}
	}
	return renderCtx{Slug: s.Slug, Idiom: idiom, ArgRef: argRef, Picker: h.Picker}
}

func renderStr(name, tmpl string, fm template.FuncMap, ctx renderCtx) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	t, err := template.New(name).Funcs(fm).Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, ctx); err != nil {
		return "", err
	}
	return b.String(), nil
}

func renderFile(h Harness, s skillSrc) (string, error) {
	fm := funcMapFor(h)
	ctx := ctxFor(h, s)

	body, err := renderStr("body", s.Body, fm, ctx)
	if err != nil {
		return "", err
	}
	short, err := renderStr("short", s.Short, fm, ctx)
	if err != nil {
		return "", err
	}
	long, err := renderStr("long", s.Long, fm, ctx)
	if err != nil {
		return "", err
	}
	allowed, err := renderStr("allowed", s.Allowed, fm, ctx)
	if err != nil {
		return "", err
	}

	desc := short
	if h.DescField == "long" {
		desc = long
	}

	var b strings.Builder
	switch h.Format {
	case "skillmd":
		b.WriteString("---\n")
		fmt.Fprintf(&b, "name: tacit-%s\n", s.Slug)
		if h.QuoteDesc {
			fmt.Fprintf(&b, "description: %q\n", desc)
		} else {
			fmt.Fprintf(&b, "description: %s\n", desc)
		}
		b.WriteString("---\n\n")
		b.WriteString(body)
		b.WriteString("\n")
	case "cmd_cc":
		b.WriteString("---\n")
		fmt.Fprintf(&b, "description: %s\n", desc)
		if s.ArgHint != "" {
			fmt.Fprintf(&b, "argument-hint: %s\n", s.ArgHint)
		}
		if allowed != "" {
			fmt.Fprintf(&b, "allowed-tools: %s\n", allowed)
		}
		b.WriteString("---\n\n")
		b.WriteString(body)
		b.WriteString("\n")
	case "cursor":
		fmt.Fprintf(&b, "# tacit-%s\n\n", s.Slug)
		b.WriteString(desc)
		b.WriteString("\n\n")
		b.WriteString(body)
		b.WriteString("\n")
	case "toml":
		fmt.Fprintf(&b, "description = %q\n\n", desc)
		b.WriteString("prompt = '''\n")
		b.WriteString(body)
		b.WriteString("\n'''\n")
	case "cmd_oc":
		full := desc
		if h.ArgNote && s.ArgHint != "" {
			full = fmt.Sprintf("%s (arguments: %s)", desc, s.ArgHint)
		}
		b.WriteString("---\n")
		fmt.Fprintf(&b, "description: %s\n", full)
		b.WriteString("---\n\n")
		b.WriteString(body)
		b.WriteString("\n")
	default:
		return "", fmt.Errorf("unknown format %q", h.Format)
	}
	return b.String(), nil
}

// repoRoot walks up from the working directory to the module root (go.mod).
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from working directory")
		}
		dir = parent
	}
}
