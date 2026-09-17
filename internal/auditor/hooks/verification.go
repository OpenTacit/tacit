// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// What counts as a check in a session, and what a check's output proves.
//
// One file, because two surfaces read the same rules and must not drift into
// disagreeing about them: technique evidence (funnel.go, discovery.go), where a
// test that passes in the same turn stands in for a verdict nobody gave, and
// the member's own session record (sessionlog.go), where the same reading
// becomes a countable fact about the work.
//
// The line this file does not cross. A check that passed says a check passed.
// It does not say the task was done, the request was met, or the session
// resolved. OpenTacit sees live work once, owns no task and injects no verifier, so
// there is no acceptance test here to pass
// (docs/design/real-work-usage-analysis-plan.md). Everything below is named
// "check" for that reason, and nothing derived from it may be called success.
//
// Nothing read here is kept. The command and the result text are matched while
// the hook event is being handled and then dropped; what survives is a count
// and a one-word state.
package hooks

import (
	"regexp"
	"strings"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// The state a check's own output leaves it in.
const (
	checkPassed  = "passed"  // an explicit pass marker and no fail marker
	checkFailed  = "failed"  // a fail marker, or the harness said the call failed
	checkUnknown = "unknown" // recognised as a check; its result proves neither
	checkStale   = "stale"   // it passed, and then something was edited
)

// Two command lists, deliberately.
//
// verifyCmdRe is the NARROW one: commands whose whole job is to run the
// project's own tests. It is what technique evidence has always meant by
// verification and it does not widen — a technique credited with a `helped`
// because a linter was happy would be a verdict nobody earned.
//
// checkCommands is the WIDE one the member's own record counts: tests, builds,
// linters and type checks, which is the set of things an agent runs to find out
// whether what it just did holds up. It is a superset of the narrow list.
var verifyCmdRe = regexp.MustCompile(`(?i)(^|[\s&;("'=:])(go test|go vet|pytest|npm test|npm run test|yarn test|pnpm test|cargo test|make test|make check|ctest|mvn test|gradle test|rspec|phpunit|tox)\b`)

// checkCommands is the vocabulary. It grows from commands seen in real hook
// payloads and nowhere else: an entry that never matches costs nothing, and one
// that matches the wrong thing misfiles a whole class of work.
var checkCommands = []string{
	// The project's tests.
	"go test", "pytest", "npm test", "npm run test", "yarn test", "pnpm test",
	"cargo test", "make test", "ctest", "mvn test", "gradle test", "rspec",
	"phpunit", "tox", "jest", "vitest", "dotnet test",
	// Does it still build.
	"go build", "npm run build", "yarn build", "pnpm build", "cargo build",
	"cargo check", "make build", "dotnet build", "mvn verify", "gradle build",
	"tsc", "cmake --build",
	// Linters and type checks.
	"go vet", "make check", "make lint", "make vet", "golangci-lint",
	"staticcheck", "eslint", "npm run lint", "npm run typecheck",
	"npm run check", "ruff", "mypy", "pyright", "flake8", "pylint",
	"cargo clippy", "shellcheck",
}

var checkCmdRe = regexp.MustCompile(`(?i)(^|[\s&;("'=:])(` +
	strings.Join(checkCommands, "|") + `)\b`)

// What a result says about itself. Conservative on both sides: the output must
// carry a positive marker rather than merely lack a negative one, and a fail
// marker beats a pass marker in the same text — "3 passed, 1 failed" is a
// failing run.
var (
	checkPassRe = regexp.MustCompile(`(?i)(\bPASS(ED)?\b|\bpassed\b|\bok\b|\d+ passed|0 failed|test result: ok|build succeeded|compiled successfully|no issues found|no problems found|\b0 errors?\b)`)
	checkFailRe = regexp.MustCompile(`(?i)(\bFAIL(ED|URE)?\b|\bpanic:\s|Traceback \(|[1-9]\d* failed|exit status [1-9]|command not found)`)
	// A runner counting its failures at zero is the clearest pass it prints,
	// and it says so with the word "failed" in it: `88 passed; 0 failed` read
	// as a failure, because the fail rule matches that word wherever it lands
	// and this expression engine has no way to look behind it. So the zero
	// clause is blanked before the fail rule runs, and left alone for the pass
	// rule, where it is exactly the evidence wanted.
	zeroFailRe = regexp.MustCompile(`(?i)\b0 (failed|failures|errors?)\b`)
)

// checkResultState reads one check's own output.
//
// failed is what the HARNESS said — an event that only fires for failures, or
// an explicit flag on the payload — and it is never inferred from the text, on
// the same rule capture.ToolFailure follows. A result that proves neither way
// is unknown rather than a failure: a build that printed nothing is not a build
// that broke, and counting it as one would make the quietest tools look like the
// worst.
func checkResultState(result string, failed bool) string {
	if failed || checkFailRe.MatchString(zeroFailRe.ReplaceAllString(result, "")) {
		return checkFailed
	}
	if checkPassRe.MatchString(result) {
		return checkPassed
	}
	return checkUnknown
}

// checkOutcome reads one tool call as check evidence: whether the command was a
// recognised check at all, and what its result proves.
func checkOutcome(command, result string, failed bool) (state string, isCheck bool) {
	if !checkCmdRe.MatchString(command) {
		return "", false
	}
	return checkResultState(result, failed), true
}

// verificationPassed reports whether any tool call in the turn ran one of the
// project's own tests and demonstrably passed. The narrow reading, and the only
// one technique evidence uses.
func verificationPassed(calls []contracts.ToolCall) bool {
	for _, tc := range calls {
		if !verifyCmdRe.MatchString(tc.Arguments) {
			continue
		}
		if checkResultState(tc.Output, false) == checkPassed {
			return true
		}
	}
	return false
}
