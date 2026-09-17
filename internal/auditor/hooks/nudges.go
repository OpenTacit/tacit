// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The nudges: short member-facing asks that ride a turn without being
// suggestions. One invites an unconnected member whose repo carries a join
// marker; the other asks a connected member for the cohort their outcomes
// should count under. Both are rate-limited in member-local memory.
package hooks

import (
	"fmt"
	"github.com/opentacit/tacit/internal/product"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// repoMarkerNudge implements growth-plan mechanism 2's receiving end: when
// this member is NOT connected to a registry and the session's repo carries a
// .tacit/registry.toml marker, invite them — exactly once per registry URL,
// recorded in member-local memory. The marker holds a URL and never a key, so
// the nudge points at a teammate, not at credentials.
func (a *Agent) repoMarkerNudge(cwd string) string {
	if a.opts.RegistryConfigured || cwd == "" {
		return ""
	}
	url := findRepoMarker(cwd)
	if url == "" || a.memory.Nudged(url) {
		return ""
	}
	a.memory.NoteNudged(url, a.opts.Now())
	return Mark() + ": this repo's organization shares its AI playbook at " + url + "\n" +
		"  To get coached with what worked for your colleagues, ask a teammate for a join link (they run: tacit invite)."
}

// segmentAskInterval caps how often the cohort ask renews. The cohort is
// important enough to keep asking for — an unsegmented machine's outcomes are
// invisible to every team-level report — but a reminder that fired per
// session or per turn would be nagging, not asking. Daily.
const segmentAskInterval = 24 * time.Hour

// segmentAsk is the inline counterpart of `tacit connect --segment`: a CONNECTED
// member whose machine reports outcomes without a cohort is asked to set one —
// at most once a day per registry, the clock kept in member-local memory. It
// fires at UserPromptSubmit because that is the one event every
// delivery-capable harness renders (including park-only Cursor, where it rides
// user_message), and it uses the DeliverContext envelope so the MODEL sees the
// ask too: the member can just answer with their team and role and the agent
// completes the save conversationally — no command to remember. Deliverless
// harnesses never consume the ask; the cohort stays settable via the skills.
func (a *Agent) segmentAsk(harness string) string {
	if !a.opts.RegistryConfigured || len(a.opts.Segment) != 0 ||
		a.opts.RegistryURL == "" || deliverless(harness) {
		return ""
	}
	// Not during first use. The ask fired as soon as a machine was connected,
	// so the first thing a new member heard from the product was a request for
	// data cleanup — before it had shown them anything worth having. It waits
	// until they have met a technique.
	if !a.memory.MetAnything() {
		return ""
	}
	// And never again once they have said no.
	if a.memory.SegmentDeclined(a.opts.RegistryURL) {
		return ""
	}
	now := a.opts.Now()
	if at, ok := a.memory.LastSegmentAsk(a.opts.RegistryURL); ok && now.Sub(at) < segmentAskInterval {
		return ""
	}
	a.memory.NoteSegmentAsked(a.opts.RegistryURL, now)

	lead := Mark() + ": outcomes from this machine aren't credited to a cohort yet, so your organization can't see which team its playbook is helping (cohorts only — never individuals).\n"
	// The menu is the whole point of asking here rather than in a doc: a
	// member reading their colleagues' actual cohort names picks one of them.
	// Asked cold, the same member invents `payments-eng` beside the existing
	// `payments` and splits every number that keys on the value.
	if menu, example := cohortMenu(a.cohortDirectory()); menu != "" {
		return lead +
			"  Cohorts your colleagues already use — " + menu + "\n" +
			"  Tell me which of those is yours, or name a new one, and I'll run:  tacit connect --segment " + example + "\n" +
			"  The full list with counts:  tacit cohorts. Or tell me to skip it and I won't ask again."
	}
	return lead +
		"  To fix it, tell me your team and role (e.g. \"payments team, engineer\") and ask me to set your " + product.Name() + " cohort — I'll run:  tacit connect --segment team=payments,role=engineer\n" +
		"  Useful dimensions: team, role, function, domain; `tacit cohorts` lists the ones colleagues already use. Or tell me to skip it and I won't ask again:  tacit cohorts --skip"
}

// CohortDim is one dimension of the registry's cohort directory as the ask
// needs it: the dimension a member types, and its values most-used first. The
// wiring supplies only member-settable dimensions — offering `harness` as a
// choice would be offering a setting that overwrites itself.
type CohortDim struct {
	Name   string
	Values []string
}

// cohortMenuMax caps the values named per dimension. The ask is three lines in
// somebody's terminal, not a directory; the long tail is one command away.
const cohortMenuMax = 5

// cohortMenu renders the directory as one line, plus a copy-pasteable
// --segment built from the most-used value of the first two dimensions. Both
// are empty when the registry has no cohorts yet — a first joiner is told to
// invent one, because that is genuinely what they must do.
func cohortMenu(dims []CohortDim) (menu, example string) {
	var parts, ex []string
	for _, d := range dims {
		if len(d.Values) == 0 {
			continue
		}
		vals := d.Values
		suffix := ""
		if len(vals) > cohortMenuMax {
			suffix = fmt.Sprintf(" (+%d more)", len(vals)-cohortMenuMax)
			vals = vals[:cohortMenuMax]
		}
		parts = append(parts, d.Name+": "+strings.Join(vals, ", ")+suffix)
		if len(ex) < 2 {
			ex = append(ex, d.Name+"="+d.Values[0])
		}
	}
	if len(parts) == 0 {
		return "", ""
	}
	return strings.Join(parts, " · "), strings.Join(ex, ",")
}

// markerRe pulls the URL out of .tacit/registry.toml's `registry = "..."`
// line — the file is deliberately one key, so a full TOML parser is not worth
// a dependency.
var markerRe = regexp.MustCompile(`(?m)^\s*registry\s*=\s*"([^"]+)"`)

// findRepoMarker walks from dir toward the filesystem root looking for
// .tacit/registry.toml, mirroring how git finds .git.
func findRepoMarker(dir string) string {
	for range 40 { // bounded: no filesystem is this deep, and Handle is hot
		raw, err := os.ReadFile(filepath.Join(dir, ".tacit", "registry.toml"))
		if err == nil {
			if m := markerRe.FindSubmatch(raw); m != nil {
				return string(m[1])
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}
