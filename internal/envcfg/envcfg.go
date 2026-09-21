// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package envcfg reads configuration from the environment with a KEY=VALUE file
// behind it. The auditor, the registry and the ingress each grew their own copy
// of this — a file parser and an env/envInt/envFloat trio — and the copies had
// drifted on the details that matter.
//
// The rule every caller shares, and the one worth stating out loud: an
// environment variable set to the empty string does NOT clear a value the file
// supplies. A variable is "set" here only when it is non-empty. Anything else
// would make an exported-but-empty variable silently erase a member's
// configured OIDC client, which is a trap the registry has already walked into
// once.
//
// Precedence is environment, then file, then the caller's default. Within one
// lookup the keys are tried in order, so an old name can be listed after a new
// one and keep working.
package envcfg

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ParseFile reads a KEY=VALUE file: blank lines and # comments are skipped,
// whitespace around the key and value is dropped, and one layer of matching or
// stray quotes is trimmed off the value. A missing or unreadable file is not an
// error — it yields an empty map, because the file is a fallback and its
// absence is the normal case.
// DataHome is the per-user tacit state directory ($XDG_DATA_HOME/tacit, or
// ~/.local/share/tacit when that is unset).
//
// It lives here for the reason this package exists: the registry had one copy
// of this and the auditor was about to grow a second, which is how the
// env-reading helpers drifted apart before they were pulled together. One
// answer, read from the environment, in the package that reads the
// environment.
//
// Everything a member's own machine keeps belongs under it. What used to
// happen instead is that each new piece of member-local state picked a name in
// $HOME, and a home directory ends up with a hundred and forty files nobody
// chose to put there.
func DataHome() string {
	if p := os.Getenv("XDG_DATA_HOME"); p != "" {
		return filepath.Join(p, "tacit")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "share", "tacit")
}

func ParseFile(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

// Lookup resolves values from the environment, then from File, then from the
// caller's default.
type Lookup struct {
	// File is the parsed fallback file. A nil map is fine and means "no file",
	// which is what the ingress has.
	File map[string]string

	// Trim reports whether to trim whitespace around an environment value
	// before deciding it is set. The ingress does; the auditor and the registry
	// do not, and a value there is taken exactly as exported.
	Trim bool
}

// Str returns the first non-empty value among keys, looking at the environment
// first and then the file, or def when nothing is set.
func (l Lookup) Str(def string, keys ...string) string {
	for _, k := range keys {
		v := os.Getenv(k)
		if l.Trim {
			v = strings.TrimSpace(v)
		}
		if v != "" {
			return v
		}
	}
	for _, k := range keys {
		if v := l.File[k]; v != "" {
			return v
		}
	}
	return def
}

// Int is Str parsed as an integer. A value that will not parse is treated as
// absent, so a typo falls back to def rather than to zero.
func (l Lookup) Int(def int, keys ...string) int {
	if v := l.Str("", keys...); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Float is Str parsed as a float, with the same treatment of a value that will
// not parse.
func (l Lookup) Float(def float64, keys ...string) float64 {
	if v := l.Str("", keys...); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// Bool reads the spellings an operator actually types. Anything else is treated
// as absent and falls back to def, so a misspelt "ture" leaves a default alone
// rather than reading as false.
func (l Lookup) Bool(def bool, keys ...string) bool {
	switch strings.ToLower(l.Str("", keys...)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
