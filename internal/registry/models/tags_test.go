// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package models

import (
	"reflect"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"case is not a distinction", []string{"Setup", "setup", "SETUP"}, []string{"setup"}},
		{"whitespace and underscores fold to hyphens",
			[]string{"task type", "task_type", "task-type"}, []string{"task-type"}},
		{"surrounding space is not a distinction", []string{"  audit  "}, []string{"audit"}},
		{"hyphen runs collapse", []string{"agent--setup", "agent-setup"}, []string{"agent-setup"}},
		{"stray hyphens are trimmed", []string{"-review-"}, []string{"review"}},
		{"slashes fold too", []string{"input/output"}, []string{"input-output"}},
		{"empties are dropped", []string{"", "   ", "-", "audit"}, []string{"audit"}},
		{"order is preserved — the first tag can become the technique's map area",
			[]string{"zebra", "apple", "mango"}, []string{"zebra", "apple", "mango"}},
		{"dedupe keeps the first occurrence",
			[]string{"audit", "review", "Audit"}, []string{"audit", "review"}},
		{"nothing to normalize", []string{"audit", "review"}, []string{"audit", "review"}},
		{"empty in, nil out", nil, nil},
		{"all-empty in, nil out", []string{"", "  "}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeTags(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NormalizeTags(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The whole point of normalizing is that it is idempotent: a normalized tag
// must survive a second pass unchanged, or re-saving a technique would keep churning
// its tags.
func TestNormalizeTagsIsIdempotent(t *testing.T) {
	in := []string{"Agent Setup", "audit", "task_type", "-x-", "AUDIT"}
	once := NormalizeTags(in)
	twice := NormalizeTags(once)
	if !reflect.DeepEqual(once, twice) {
		t.Fatalf("not idempotent: %q -> %q", once, twice)
	}
}
