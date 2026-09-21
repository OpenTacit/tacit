// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package screen is the automated safety gate (docs/learning/validation-without-review.md,
// D3): the deterministic layer that lets machine and member contributions enter
// the pipeline without a human reading each one, by catching the unambiguous
// attacks a technique must never carry. A technique is INSTRUCTIONS injected into a
// trusted agent's context, so the danger is not "unhelpful" (the outcome loop
// measures that) but "hostile": a contribution that tells every colleague's
// agent to ignore its instructions, exfiltrate secrets, or that smuggles
// credential material.
//
// This is the rules half of the layered screen D3 describes — fast, deterministic,
// un-foolable on known-bad patterns, and impossible to prompt-inject because it
// runs no model. The LLM-judge half (novel adversarial phrasing, contradiction
// with existing techniques) layers on top and is not built here. Rules are deliberately
// CONSERVATIVE: a legitimate technique does not say "ignore previous
// instructions" or embed an API key, so High findings are safe to hard-block,
// and anything subtler is left for a human or the (future) judge.
package screen

import (
	"fmt"
	"regexp"
	"strings"
)

// Severity ranks a finding. Only High blocks automatically; Medium is surfaced
// for a human but never auto-rejects, because at Medium a false positive would
// cost a real contribution.
type Severity string

const (
	Medium Severity = "medium"
	High   Severity = "high"
)

// Finding is one rule match.
type Finding struct {
	Rule     string
	Field    string
	Severity Severity
	Detail   string
}

// Input is the technique text to screen, kept as plain fields so this package stays
// dependency-free (no import cycle with models).
type Input struct {
	Name        string
	Description string
	Recipe      string
	Extra       string // applies_when / not_when / before_after, concatenated
}

type rule struct {
	name     string
	severity Severity
	re       *regexp.Regexp
	detail   string
}

// rules are the deterministic patterns. Each is anchored to an attack class a
// technique has no legitimate reason to contain.
var rules = []rule{
	// Prompt-injection: a technique that tries to override the agent's own instructions.
	{"prompt-injection", High,
		regexp.MustCompile(`(?i)ignore\s+(all\s+|any\s+)?(previous|prior|earlier|the\s+above)\s+(instructions|prompts?|directions)`),
		"text instructs the agent to ignore its own instructions"},
	{"prompt-injection", High,
		regexp.MustCompile(`(?i)disregard\s+(all\s+|any\s+)?(previous|prior|the\s+above|your)\b`),
		"text instructs the agent to disregard prior context"},
	{"prompt-injection", High,
		regexp.MustCompile(`(?i)\b(you\s+are\s+now|from\s+now\s+on\s+you\s+are)\b`),
		"text attempts to re-cast the agent's role"},
	{"prompt-injection", High,
		regexp.MustCompile(`(?i)(reveal|print|repeat|show)\s+(me\s+)?(your\s+)?(the\s+)?(system\s+)?(prompt|instructions)`),
		"text attempts to extract the system prompt"},
	// Exfiltration: a technique that routes data or secrets out of the session.
	{"exfiltration", High,
		regexp.MustCompile(`(?i)\bexfiltrat`),
		"text references exfiltration"},
	{"exfiltration", High,
		regexp.MustCompile(`(?i)curl\b[^\n]*\|\s*(sh|bash|zsh)\b`),
		"text pipes a remote fetch straight into a shell"},
	{"exfiltration", High,
		regexp.MustCompile(`(?i)\b(send|post|upload|email|leak)\b[^\n]{0,40}\b(secret|token|api[-_ ]?key|password|credential|env(ironment)?\s+var)`),
		"text routes credentials to an external destination"},
	// Embedded secret material: a technique is a shared artifact; it must not carry live keys.
	{"embedded-secret", High,
		regexp.MustCompile(`sk-[A-Za-z0-9]{16,}`),
		"an OpenAI-style secret key is embedded in the technique"},
	{"embedded-secret", High,
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		"an AWS access key id is embedded in the technique"},
	{"embedded-secret", High,
		regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
		"a GitHub token is embedded in the technique"},
	{"embedded-secret", High,
		regexp.MustCompile(`-----BEGIN\s+[A-Z ]*PRIVATE KEY-----`),
		"a private key is embedded in the technique"},
	// Safety-bypass: surfaced, not blocked — subtler and more false-positive prone.
	{"safety-bypass", Medium,
		regexp.MustCompile(`(?i)\b(disable|bypass|turn\s+off|circumvent)\b[^\n]{0,24}\b(safety|guardrail|filter|moderation|content\s+policy)\b`),
		"text references disabling a safety control"},
}

// Scan runs every rule over the technique's text and returns the findings, in rule
// order. Each field is scanned separately so a finding names where it hit.
func Scan(in Input) []Finding {
	fields := []struct{ name, text string }{
		{"name", in.Name}, {"description", in.Description},
		{"recipe", in.Recipe}, {"extra", in.Extra},
	}
	var out []Finding
	for _, r := range rules {
		for _, f := range fields {
			if f.text == "" {
				continue
			}
			if r.re.MatchString(f.text) {
				out = append(out, Finding{Rule: r.name, Field: f.name, Severity: r.severity, Detail: r.detail})
			}
		}
	}
	return out
}

// Blocks reports whether any finding is High — the auto-reject bar. Machine
// lanes gate entry to shadow on this; the contributed lane (member → colleagues)
// hard-rejects on it, since that is the one lane with a plausible adversary.
func Blocks(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == High {
			return true
		}
	}
	return false
}

// Summary renders the blocking findings for an error message or a reviewer note.
func Summary(fs []Finding) string {
	var parts []string
	for _, f := range fs {
		if f.Severity == High {
			parts = append(parts, fmt.Sprintf("%s in %s (%s)", f.Rule, f.Field, f.Detail))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}
