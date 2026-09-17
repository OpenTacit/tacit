// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package techniques loads git-tracked curated techniques (markdown + YAML frontmatter).
//
// Git is the editorial source of truth for curated techniques; the store is the
// runtime layer (docs/design/architecture.md). The frontmatter parser below covers
// exactly the YAML subset the technique files use — scalars, folded (>) and literal
// (|) block scalars, inline [a, b] lists, block lists whose items are strings,
// `key: value` one-line maps, or inline {k: v, ...} maps. Not a general YAML
// implementation — the same deliberate-subset philosophy as the markdown
// renderer, keeping the module dependency-free.
package techniques

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
)

// Required frontmatter fields for a curated technique.
var Required = []string{"id", "name", "description", "recipe"}

// ParseFile reads one techniques/*.md file into a Technique.
func ParseFile(path string) (models.Technique, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return models.Technique{}, err
	}
	text := string(raw)
	if !strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), "---") {
		return models.Technique{}, fmt.Errorf("%s: missing YAML frontmatter", path)
	}
	parts := strings.SplitN(text, "---", 3)
	if len(parts) < 3 {
		return models.Technique{}, fmt.Errorf("%s: unterminated YAML frontmatter", path)
	}
	fm, body := parts[1], parts[2]
	data, err := parseYAMLSubset(fm)
	if err != nil {
		return models.Technique{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, field := range Required {
		if s, _ := data[field].(string); strings.TrimSpace(s) == "" {
			return models.Technique{}, fmt.Errorf("%s: missing required field %q", path, field)
		}
	}
	if _, ok := data["before_after"]; !ok && strings.TrimSpace(body) != "" {
		data["before_after"] = strings.TrimSpace(body)
	}

	now := models.Now()
	technique := models.Technique{
		ID:          data["id"].(string),
		Name:        data["name"].(string),
		Description: data["description"].(string),
		Scope:       stringOr(data, "scope", "general"),
		// "" when the file doesn't say — UpsertTechnique then keeps the stored
		// status (an API retirement must survive content syncs; only an
		// explicit frontmatter status may change lifecycle state).
		Status:      stringOr(data, "status", ""),
		Provenance:  stringOr(data, "provenance", "curated"),
		Version:     intOr(data, "version", 1),
		Recipe:      data["recipe"].(string),
		BeforeAfter: stringOr(data, "before_after", ""),
		Tags:        stringList(data["tags"]),
		TaskTypes:   stringList(data["task_types"]),
		Channels:    stringList(data["channels"]),
		AppliesWhen: stringOr(data, "applies_when", ""),
		NotWhen:     stringOr(data, "not_when", ""),
		Shipped:     stringOr(data, "shipped", ""),
		Source:      path,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if ts, ok := data["triggers"].([]any); ok {
		technique.Triggers = ts
	}
	if sm, ok := data["support_matrix"].([]any); ok {
		for _, v := range sm {
			if m, ok := v.(map[string]any); ok {
				technique.SupportMatrix = append(technique.SupportMatrix, m)
			}
		}
	}
	return technique, nil
}

// SyncDir loads every techniques/*.md into the store via upsert. Returns the count.
func SyncDir(dir string, upsert func(models.Technique) error) (int, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return 0, err
	}
	sort.Strings(paths)
	loaded := 0
	for _, path := range paths {
		technique, err := ParseFile(path)
		if err != nil {
			return loaded, err
		}
		if err := upsert(technique); err != nil {
			return loaded, err
		}
		loaded++
	}
	return loaded, nil
}

func stringOr(m map[string]any, key, def string) string {
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return def
}

func intOr(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func stringList(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, fmt.Sprint(it))
	}
	return out
}

// --- minimal YAML-subset parser ---------------------------------------------

type yamlParser struct {
	lines []string
	pos   int
}

func parseYAMLSubset(src string) (map[string]any, error) {
	p := &yamlParser{lines: strings.Split(src, "\n")}
	return p.parseMap(0)
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func (p *yamlParser) parseMap(indent int) (map[string]any, error) {
	out := map[string]any{}
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			p.pos++
			continue
		}
		if indentOf(line) < indent {
			return out, nil
		}
		key, rest, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, fmt.Errorf("yaml subset: expected 'key:' at line %d: %q", p.pos+1, trimmed)
		}
		key = strings.TrimSpace(key)
		rest = strings.TrimSpace(rest)
		p.pos++
		switch {
		case rest == ">" || rest == "|":
			out[key] = p.blockScalar(indentOf(line), rest == ">")
		case rest == "":
			// nested block: a list or (unused in techniques, but supported) a map
			val, err := p.blockValue(indentOf(line))
			if err != nil {
				return nil, err
			}
			out[key] = val
		default:
			out[key] = parseScalar(rest)
		}
	}
	return out, nil
}

// blockScalar consumes indented lines following `key: >` or `key: |`.
func (p *yamlParser) blockScalar(keyIndent int, folded bool) string {
	var lines []string
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			p.pos++
			continue
		}
		if indentOf(line) <= keyIndent {
			break
		}
		lines = append(lines, strings.TrimLeft(line, " "))
		p.pos++
	}
	// drop trailing blank lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if folded {
		return strings.TrimSpace(strings.Join(lines, " "))
	}
	return strings.Join(lines, "\n") + "\n"
}

// blockValue consumes an indented block: either a `- item` list or a nested map.
func (p *yamlParser) blockValue(keyIndent int) (any, error) {
	// find the first content line
	for p.pos < len(p.lines) && strings.TrimSpace(p.lines[p.pos]) == "" {
		p.pos++
	}
	if p.pos >= len(p.lines) || indentOf(p.lines[p.pos]) <= keyIndent {
		return "", nil
	}
	first := strings.TrimSpace(p.lines[p.pos])
	if strings.HasPrefix(first, "- ") || first == "-" {
		return p.blockList(indentOf(p.lines[p.pos]))
	}
	return p.parseMap(indentOf(p.lines[p.pos]))
}

func (p *yamlParser) blockList(itemIndent int) ([]any, error) {
	var out []any
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			p.pos++
			continue
		}
		if indentOf(line) < itemIndent || !strings.HasPrefix(trimmed, "-") {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		p.pos++
		if item == "" {
			continue
		}
		// `- key: value` single-pair map item (e.g. triggers)
		if !strings.HasPrefix(item, "{") && !strings.HasPrefix(item, "[") {
			if k, v, ok := cutMapPair(item); ok {
				out = append(out, map[string]any{k: parseScalar(v)})
				continue
			}
		}
		out = append(out, parseScalar(item))
	}
	return out, nil
}

// cutMapPair detects `key: value` where key looks like a bare identifier.
func cutMapPair(item string) (string, string, bool) {
	k, v, ok := strings.Cut(item, ":")
	if !ok || v == "" || !strings.HasPrefix(v, " ") {
		return "", "", false
	}
	k = strings.TrimSpace(k)
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "", "", false
		}
	}
	return k, strings.TrimSpace(v), true
}

// parseScalar handles quoted strings, inline lists, inline maps, bools, ints,
// and bare strings.
func parseScalar(s string) any {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return ""
	case strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2:
		return s[1 : len(s)-1]
	case strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2:
		return s[1 : len(s)-1]
	case strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"):
		var out []any
		for _, part := range splitTopLevel(s[1 : len(s)-1]) {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, parseScalar(part))
			}
		}
		return out
	case strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}"):
		out := map[string]any{}
		for _, part := range splitTopLevel(s[1 : len(s)-1]) {
			if k, v, ok := strings.Cut(part, ":"); ok {
				out[strings.TrimSpace(k)] = parseScalar(v)
			}
		}
		return out
	case s == "true":
		return true
	case s == "false":
		return false
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return s
}

// splitTopLevel splits on commas not inside nested brackets/braces/quotes.
func splitTopLevel(s string) []string {
	var out []string
	depth := 0
	inQuote := rune(0)
	start := 0
	for i, r := range s {
		switch {
		case inQuote != 0:
			if r == inQuote {
				inQuote = 0
			}
		case r == '"' || r == '\'':
			inQuote = r
		case r == '[' || r == '{':
			depth++
		case r == ']' || r == '}':
			depth--
		case r == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
