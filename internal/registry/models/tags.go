// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package models

import "strings"

// Tags are a FREE vocabulary — any technique may coin any tag — and four unbounded
// sources feed it: curated techniques, member contributions, LLM suggestion runs, and
// federated imports. Left alone that vocabulary only grows, and it grows in the
// worst way: not with genuinely new concepts but with spellings of concepts it
// already has ("Setup", "setup", "set up", "set_up"). Those never merge back,
// they fragment every tag filter, and they fragment the Outcomes technique map,
// whose column axis is a technique's first task type — otherwise its first tag.
//
// NormalizeTags is the one gate. It is called by every Store's UpsertTechnique (a
// contract the storage conformance suite enforces), so no write path can coin a
// variant spelling, including write paths that do not exist yet. It cannot merge
// two tags that MEAN the same thing — that is a judgment, and it belongs to the
// human on the Tags page — but it guarantees that two tags which merely LOOK the
// same are the same tag.
//
// The rules, in order:
//   - lowercase, and trim surrounding space
//   - fold whitespace and underscores to hyphens ("task type" and "task_type"
//     are the same word as "task-type")
//   - collapse runs of hyphens, and strip leading/trailing ones
//   - drop empties
//   - dedupe, preserving first-seen order (a technique's tag order is meaningful:
//     the first tag can become its area on the technique map)
func NormalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, raw := range tags {
		t := NormalizeTag(raw)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NormalizeTag canonicalizes one tag. Exported because the Tags page has to
// normalize a merge target typed by a human, on the same rules.
func NormalizeTag(raw string) string {
	t := strings.ToLower(strings.TrimSpace(raw))
	t = strings.Map(func(r rune) rune {
		if r == '_' || r == ' ' || r == '\t' || r == '\n' || r == '/' {
			return '-'
		}
		return r
	}, t)
	for strings.Contains(t, "--") {
		t = strings.ReplaceAll(t, "--", "-")
	}
	return strings.Trim(t, "-")
}
