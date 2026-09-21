// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package capture

import "strings"

// What a tool call's failure may contribute, which is one word from this list
// and nothing else.
//
// It is the counterpart of ToolDetail and holds to the same standard: a tool's
// OUTPUT is closer to the member's own work than its name is — it is file
// contents, a diff, a stack trace, somebody's customer list — so nothing from
// it is kept. What survives is which KIND of failure it was, from a list fixed
// here, and that is a fact about how the agent is working rather than about
// what the member was working on.
//
// Sniffly's most-quoted finding is that 20–30% of Claude Code's errors are the
// agent going looking for a file or a function that was not there. That is
// something a member can act on the same day — and it is invisible unless the
// kinds are separated, because as one number "errors" says only that the day
// was rough.
const (
	FailNotFound = "not found"     // a path, a string to replace, a symbol
	FailDenied   = "denied"        // refused at the prompt, or stopped by a hook
	FailTimedOut = "timed out"     // it ran and was cut off
	FailExit     = "non-zero exit" // a program said no
	FailParse    = "parse error"   // the arguments or the output would not parse
	FailOther    = "other"         // it failed and none of the above fits
)

// FailureKinds is the vocabulary in the order a member reads it: commonest
// cause first, then the ones that are somebody's decision, then the rest.
var FailureKinds = []string{FailNotFound, FailDenied, FailTimedOut, FailExit, FailParse, FailOther}

// failurePatterns maps a phrase to its kind. Ordered, and the order is the
// whole of the classification: a shell that exited 2 because a file was not
// there is a not-found, not an exit code, because not-found is what the member
// would do something about.
//
// Every phrase here was taken off a real failure on a real machine rather than
// imagined — the fixtures in toolfailure_test.go are those failures.
var failurePatterns = []struct {
	phrase string
	kind   string
}{
	{"no such file or directory", FailNotFound},
	{"does not exist", FailNotFound},
	{"cannot access", FailNotFound},
	{"not found in", FailNotFound},
	{"string to replace not found", FailNotFound},
	{"file has not been read", FailNotFound},
	{"no matches found", FailNotFound},

	{"user doesn't want to proceed", FailDenied},
	{"user rejected", FailDenied},
	{"tool use was rejected", FailDenied},
	{"permission for this action was denied", FailDenied},
	{"permission denied", FailDenied},
	{"blocked by", FailDenied},
	{"is blocked", FailDenied},
	{"blocked:", FailDenied},
	{"operation not permitted", FailDenied},

	{"timed out", FailTimedOut},
	{"timeout", FailTimedOut},
	{"deadline exceeded", FailTimedOut},

	{"inputvalidationerror", FailParse},
	{"invalid json", FailParse},
	{"unexpected token", FailParse},
	{"not terminated", FailParse},
	{"syntaxerror", FailParse},
	{"parse error", FailParse},

	{"exit code", FailExit},
	{"exited with", FailExit},
}

// ToolFailure names the kind of failure a tool call's result describes, or ""
// when it describes no failure at all.
//
// failed is what the HARNESS said — an event that only fires for failures, or
// an explicit flag on the payload. It is required: inferring failure from text
// alone would read a `grep` that printed "no such file or directory" as a
// failed grep, and a page built on that would be confidently wrong about the
// thing it exists to measure. When the harness says a call failed and nothing
// in the text is recognisable, the answer is "other" — which is honest, and
// which a growing list of patterns makes rarer.
func ToolFailure(failed bool, response any) string {
	if !failed {
		return ""
	}
	text := strings.ToLower(AsText(response))
	if len(text) > 4000 {
		text = text[:4000] // the tail of a stack trace classifies nothing
	}
	for _, p := range failurePatterns {
		if strings.Contains(text, p.phrase) {
			return p.kind
		}
	}
	return FailOther
}

// FailedPayload reads the harness's own statement that a call failed. Claude
// Code sends failures as their own event rather than as a flag; other harnesses
// put a flag on the payload. Neither is inferred from the text (ToolFailure).
func FailedPayload(p map[string]any) bool {
	if p == nil {
		return false
	}
	if event, _ := p["hook_event_name"].(string); event == EvPostToolFail {
		return true
	}
	for _, key := range []string{"is_error", "error", "failed"} {
		switch v := p[key].(type) {
		case bool:
			if v {
				return true
			}
		case string:
			if strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
	if resp, ok := p["tool_response"].(map[string]any); ok {
		if v, _ := resp["is_error"].(bool); v {
			return true
		}
		if msg, _ := resp["error"].(string); strings.TrimSpace(msg) != "" {
			return true
		}
	}
	return false
}

// ToolResponse is a tool call's result off a hook payload, under whichever of
// the harnesses' names carries it. One list, here, so the reducers that read a
// result cannot come to disagree about where it lives.
func ToolResponse(p map[string]any) any {
	if p == nil {
		return nil
	}
	for _, key := range []string{"tool_response", "tool_output", "tool_result", "output", "error"} {
		if v := p[key]; v != nil {
			return v
		}
	}
	return nil
}
