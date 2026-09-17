// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package ingest loads a conversation transcript from a share link, a saved
// file, or raw text — the retained out-of-runtime import path
// (docs/delivery/low-intrusion-plan.md); the managed runtime is the primary front door.
package ingest

import (
	"os"
	"strings"
)

// Turn is one (role, text) pair extracted from a shared conversation.
type Turn struct {
	Role string
	Text string
}

// Format renders extracted turns as a plain transcript.
func Format(turns []Turn) string {
	parts := make([]string, 0, len(turns))
	for _, t := range turns {
		parts = append(parts, strings.ToUpper(t.Role)+": "+t.Text)
	}
	return strings.Join(parts, "\n\n")
}

// LoadTranscript resolves a source: a share URL, a file path, or (with
// asText) raw transcript text.
func LoadTranscript(source string, asText bool, cookie string) (string, error) {
	if asText {
		return source, nil
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		turns, err := FromURL(source, cookie)
		if err != nil {
			return "", err
		}
		return Format(turns), nil
	}
	turns, err := FromFile(source)
	if err != nil {
		return "", err
	}
	return Format(turns), nil
}

// ReadFileText is a small helper for FromFile and tests.
func ReadFileText(path string) (string, error) {
	raw, err := os.ReadFile(path)
	return string(raw), err
}
