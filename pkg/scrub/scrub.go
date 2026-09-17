// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package scrub detects and redacts secrets and personal identifiers in text
// bound for a boundary crossing — a technique sketch leaving a member's
// machine, a mined draft entering review, a federated technique arriving from
// outside. It is deliberately conservative: a redacted false positive costs a
// little clarity; a leaked credential costs trust the product cannot buy back.
//
// The scanner is pattern-based and dependency-free so it can run identically
// in the hook agent (where its transparency is the point) and in any pipeline
// that imports it.
package scrub

import (
	"math"
	"regexp"
	"strings"
)

// Finding is one detected item.
type Finding struct {
	Kind  string // e.g. "aws-access-key", "private-key", "email"
	Match string // the matched text (for reviewer display; handle with care)
}

type rule struct {
	kind string
	re   *regexp.Regexp
}

// Ordered: specific credentials first, generic assignments later, PII last.
var rules = []rule{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`)},
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,255}\b`)},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{10,}\b`)},
	{"openai-key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
	{"bearer-token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{16,}=*`)},
	{"basic-auth-url", regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^/\s:@]{1,64}:[^@\s]{1,128}@`)},
	{"secret-assignment", regexp.MustCompile(`(?i)\b(?:api[_-]?key|apikey|secret|token|passwd|password|credential)s?\b\s*[:=]\s*['"]?[A-Za-z0-9+/_.~-]{8,}['"]?`)},
	{"email", regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)},
}

// entropyCandidateRe pre-filters long unbroken tokens for the entropy check.
var entropyCandidateRe = regexp.MustCompile(`\b[A-Za-z0-9+/=_-]{32,}\b`)

// Scan reports findings without modifying the text.
func Scan(text string) []Finding {
	var out []Finding
	for _, r := range rules {
		for _, m := range r.re.FindAllString(text, -1) {
			out = append(out, Finding{Kind: r.kind, Match: m})
		}
	}
	for _, m := range entropyCandidateRe.FindAllString(text, -1) {
		if looksLikeSecret(m) {
			out = append(out, Finding{Kind: "high-entropy-string", Match: m})
		}
	}
	return out
}

// Redact replaces every finding with a [redacted:kind] marker and returns the
// clean text plus what was removed. Idempotent: markers do not re-match.
func Redact(text string) (string, []Finding) {
	findings := Scan(text)
	// longest matches first so overlapping shorter rules can't split a marker
	ordered := make([]Finding, len(findings))
	copy(ordered, findings)
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if len(ordered[j].Match) > len(ordered[i].Match) {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}
	for _, f := range ordered {
		text = strings.ReplaceAll(text, f.Match, "[redacted:"+f.Kind+"]")
	}
	return text, findings
}

// looksLikeSecret flags long unbroken tokens with credential-like entropy,
// while letting ordinary long words, paths, and repeated padding through.
func looksLikeSecret(s string) bool {
	if len(s) < 32 {
		return false
	}
	// require mixed character classes — prose and identifiers rarely mix all of these
	var lower, upper, digit int
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z':
			lower++
		case c >= 'A' && c <= 'Z':
			upper++
		case c >= '0' && c <= '9':
			digit++
		}
	}
	classes := 0
	for _, n := range []int{lower, upper, digit} {
		if n > 0 {
			classes++
		}
	}
	if classes < 3 {
		return false
	}
	return shannon(s) > 4.2
}

func shannon(s string) float64 {
	freq := map[rune]float64{}
	for _, c := range s {
		freq[c]++
	}
	n := float64(len(s))
	h := 0.0
	for _, f := range freq {
		p := f / n
		h -= p * math.Log2(p)
	}
	return h
}
