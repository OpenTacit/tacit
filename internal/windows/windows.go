// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package windows holds the preset time windows every OpenTacit surface offers:
// the dashboard, the MCP tools, the member's own usage log, and the CLI flags.
// One list, one order, one default.
//
// It is a leaf on purpose — strings and durations only, no store, no clock, no
// OpenTacit types — so the registry, the auditor, and the command can all depend on
// it without depending on each other. Before it existed the four tokens were
// typed out in six places and kept together by a comment.
//
// The keys are a wire surface. The MCP tool schemas publish them as an enum and
// hosts validate against it, so the list and its order are frozen: adding a
// preset changes what every advertised tool accepts.
package windows

import (
	"strings"
	"time"
)

// Keys are the preset windows, shortest first. "all" is the whole retained
// history, which is why it has no fixed length.
var Keys = []string{"7d", "30d", "90d", "all"}

// Default is the window a surface shows when nobody asked for one.
const Default = "30d"

const day = 24 * time.Hour

// durations is the length behind each key. "all" is 0 — no lower bound.
var durations = map[string]time.Duration{
	"7d":  7 * day,
	"30d": 30 * day,
	"90d": 90 * day,
	"all": 0,
}

// Duration is how far back a preset reaches. "all" is 0, meaning no bound;
// a caller that treats 0 as "empty window" has to check Valid first. An
// unrecognized key gets the default window, which is what every surface does
// with a token it does not know.
func Duration(key string) time.Duration {
	if d, ok := durations[strings.TrimSpace(strings.ToLower(key))]; ok {
		return d
	}
	return durations[Default]
}

// Valid reports whether key is one of the presets.
func Valid(key string) bool {
	_, ok := durations[strings.TrimSpace(strings.ToLower(key))]
	return ok
}

// Help is the terse form for a flag help string: "7d | 30d | 90d | all".
func Help() string { return strings.Join(Keys, " | ") }

// Prose is the list as a sentence fragment, for a longer help string or a
// schema description: "7d, 30d, 90d, or all".
func Prose() string { return prose(false) }

// ProseDefault is Prose with the default window marked, for the surfaces whose
// description tells the reader which one they get for free:
// "7d, 30d (default), 90d, or all".
func ProseDefault() string { return prose(true) }

func prose(markDefault bool) string {
	parts := make([]string, len(Keys))
	for i, k := range Keys {
		if markDefault && k == Default {
			k += " (default)"
		}
		if i == len(Keys)-1 {
			k = "or " + k
		}
		parts[i] = k
	}
	return strings.Join(parts, ", ")
}
