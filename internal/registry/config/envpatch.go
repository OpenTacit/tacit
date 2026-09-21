// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/fsx"
)

// PatchEnv rewrites KEY=... lines in registry.env in place and appends new keys
// at the end — the operator's comments and hand-managed lines survive untouched
// (unlike the first-run wizard's writer, which owns the whole file). An empty
// value removes the line: the file documents choices, and "no external URL" is
// the absence of one. Atomic via temp+rename, mode 0600.
//
// It lives here, beside RegistryEnvPath and the reader, because two callers now
// edit this file from opposite sides: the dashboard's Settings form and
// `tacit secure` on the console. A registry with no sign-in configured has no
// admins, so the console is the only one of those two that can turn sign-in on
// — and both must leave the same file readable by the operator who wrote it.
func PatchEnv(path string, updates map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	pending := map[string]string{}
	for k, v := range updates {
		pending[k] = v
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if key, _, ok := strings.Cut(trimmed, "="); ok && !strings.HasPrefix(trimmed, "#") {
			if v, hit := pending[key]; hit {
				delete(pending, key)
				if v == "" {
					continue // cleared: drop the line
				}
				out = append(out, key+"="+v)
				continue
			}
		}
		out = append(out, line)
	}
	for _, k := range SortedKeys(pending) {
		if v := pending[k]; v != "" {
			out = append(out, k+"="+v)
		}
	}
	content := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	return fsx.WriteFileAtomic(path, []byte(content), 0o600)
}

// SortedKeys returns a map's keys in order — for deterministic file writes and
// log lines.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ReadEnv exposes registry.env's KEY=VALUE pairs to callers that edit the file
// rather than resolve configuration from it. Load is the way to READ settings
// (the environment wins there); this is the way to see what the FILE itself
// says, which is what an editor has to patch.
func ReadEnv(path string) map[string]string { return readEnvFile(path) }
