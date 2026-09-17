// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit cohorts — the menu a member reads before naming their own cohort.
//
// The cohort is the one setting OpenTacit asks a member to invent, and it is worth
// almost nothing invented alone: a value only pays off when it matches what
// colleagues already type. So the first move is to look. This prints what the
// registry has seen, flags a value nobody else uses (usually a typo), and ends
// with the exact command for both answers — join one of these, or start a new
// one.

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/opentacit/tacit/internal/auditor/client"
	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/hooks"
)

// maxListed caps a dimension's line. A registry with forty teams should still
// print something a member can read; the tail is counted, not dropped
// silently, and --json always carries everything.
const maxListed = 8

func cmdCohorts(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("cohorts", flag.ContinueOnError)
	registryURL := fs.String("registry", cfg.RegistryURL, "registry URL")
	key := fs.String("key", cfg.RegistryKey, "registry API key")
	asJSON := fs.Bool("json", false, "print the directory as JSON")
	skip := fs.Bool("skip", false, "stop asking this machine for a cohort — machine-local, never sent to the registry")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	// The answer the daily ask never had. It went on renewing until a cohort
	// was set, so a member who did not want one had no way to say so.
	if *skip {
		if err := hooks.DeclineSegment(cfg.TechniqueMemoryPath, *registryURL); err != nil {
			fmt.Fprintf(os.Stderr, "cannot record the choice: %v\n", err)
			return 1
		}
		fmt.Printf("this machine will not be asked for a cohort again (%s)\n", *registryURL)
		fmt.Println("outcomes still reach the registry; they just carry no cohort.")
		fmt.Printf("changed your mind? %s connect --segment team=…,role=…\n", selfCommand())
		return 0
	}

	reg := &client.Registry{BaseURL: *registryURL, APIKey: *key,
		HTTP: &http.Client{Timeout: 10 * time.Second}}
	dir, err := reg.GetCohorts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read cohorts from %s: %v\n", *registryURL, err)
		return exitUnreachable
	}
	mine := auditorconfig.ParseSegment(cfg.HooksSegment)

	if *asJSON {
		out := map[string]any{
			"registry":   *registryURL,
			"dimensions": dir.Dimensions,
			"settable":   dir.Settable,
			"mine":       mine,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}

	fmt.Print(cohortReport(*registryURL, dir, mine))
	return 0
}

// cohortReport renders the directory. Split out from cmdCohorts so it is
// testable without a registry.
func cohortReport(registryURL string, dir client.CohortDirectory, mine map[string]string) string {
	var b strings.Builder
	used := 0
	for _, dim := range dir.Dimensions {
		used += len(dim.Values)
	}

	if used == 0 {
		fmt.Fprintf(&b, "No cohorts in use yet at %s — you are the first.\n\n", registryURL)
		b.WriteString("Pick names that your colleagues will recognise and reuse. The next member\nwho joins sees them here and can match them, which keeps the org's totals complete.\n\n")
		fmt.Fprintf(&b, "  tacit connect --segment team=<your-team>,role=<your-role>\n\n")
		fmt.Fprintf(&b, "Dimensions you can set: %s.\n", strings.Join(dir.Settable, ", "))
		return b.String()
	}

	unit, count := cohortUnit(dir)
	fmt.Fprintf(&b, "Cohorts already in use at %s\n", registryURL)
	fmt.Fprintf(&b, "(the number after each is %s)\n\n", unit)

	settable := map[string]bool{}
	for _, d := range dir.Settable {
		settable[d] = true
	}
	// Every settable dimension gets a line whether or not anyone uses it: an
	// empty one is an invitation, and its absence would read as unavailable.
	listed := map[string]bool{}
	for _, dim := range dir.Dimensions {
		listed[dim.Dimension] = true
		b.WriteString(cohortLine(dim.Dimension, dim.Values, dim.Derived, mine[dim.Dimension], count))
	}
	for _, d := range dir.Settable {
		if !listed[d] {
			b.WriteString(cohortLine(d, nil, false, mine[d], count))
		}
	}

	b.WriteString("\n")
	if len(mine) == 0 {
		b.WriteString("Your cohort: not set.\n\n")
		b.WriteString("To join one above, copy its values:\n")
		fmt.Fprintf(&b, "  tacit connect --segment %s\n\n", exampleSegment(dir))
		b.WriteString("Or start a new cohort — the same command with any value you like. It\nappears here for the next joiner as soon as you use Tacit.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Your cohort: %s\n", formatSegment(mine))
	// A value nobody else uses is usually a typo, and a typo is invisible:
	// nothing rejects it, the member's outcomes just quietly file under a
	// cohort of one. Say so here, where the correct spellings are on screen.
	var lonely, fix []string
	for _, d := range dir.Settable {
		v := mine[d]
		if v == "" || usedByOthers(dir, d, v) {
			continue
		}
		lonely = append(lonely, d+"="+v)
		// Suggest the NEAREST existing value, never the most popular one:
		// telling someone who typed `paymnets` to join `platform` is worse
		// than saying nothing, because it is confidently wrong about which
		// team they are on.
		if near := nearest(dir, d, v); near != "" {
			fix = append(fix, d+"="+near)
		}
	}
	if len(lonely) == 0 {
		return b.String()
	}
	fmt.Fprintf(&b, "\nNo other session here uses %s.\n", strings.Join(lonely, " or "))
	if len(fix) > 0 {
		fmt.Fprintf(&b, "The near match is %s. To use it:\n  tacit connect --segment %s\n",
			strings.Join(fix, " and "), strings.Join(fix, ","))
		return b.String()
	}
	b.WriteString("If it is a new cohort, nothing to do — it will appear above after you use\nTacit. If it is a typo, the list above shows the spellings in use.\n")
	return b.String()
}

// nearest returns the existing value on a dimension closest to val, or "" when
// nothing is close enough to be worth naming. Close means a small edit
// distance, scaled to the word — two typos in `sre` is a different cohort,
// two in `data-platform` is a slip.
func nearest(dir client.CohortDirectory, dim, val string) string {
	budget := max(1, utf8.RuneCountInString(val)/4)
	best, bestDist := "", budget+1
	for _, v := range dir.Values(dim) {
		if d := editDistance(strings.ToLower(val), strings.ToLower(v.Value)); d < bestDist {
			best, bestDist = v.Value, d
		}
	}
	return best
}

// editDistance is Levenshtein over runes — two rows, because the inputs are
// cohort names and the caller runs it a handful of times.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// cohortDims adapts the registry client to the hook agent's cohort seam,
// keeping only the dimensions a member types. Offering `harness` in the ask
// would offer a setting the session overwrites on its own.
func cohortDims(reg *client.Registry) func() ([]hooks.CohortDim, error) {
	return func() ([]hooks.CohortDim, error) {
		dir, err := reg.GetCohorts()
		if err != nil {
			return nil, err
		}
		settable := map[string]bool{}
		for _, d := range dir.Settable {
			settable[d] = true
		}
		var out []hooks.CohortDim
		for _, dim := range dir.Dimensions {
			if dim.Derived || !settable[dim.Dimension] || len(dim.Values) == 0 {
				continue
			}
			vals := make([]string, 0, len(dim.Values))
			for _, v := range dim.Values {
				vals = append(vals, v.Value)
			}
			out = append(out, hooks.CohortDim{Name: dim.Dimension, Values: vals})
		}
		return out, nil
	}
}

// fetchCohorts reads the directory, or reports failure to the caller to
// swallow. Every caller outside `tacit cohorts` itself is decorating some
// other command's success, so none of them may fail on it.
func fetchCohorts(registryURL, key string) (client.CohortDirectory, error) {
	reg := &client.Registry{BaseURL: registryURL, APIKey: key,
		HTTP: &http.Client{Timeout: 5 * time.Second}}
	return reg.GetCohorts()
}

// reportCohortFit tells a member who just set a cohort whether they landed in
// one their colleagues use or started a new one. Both are legitimate — the
// point is that they find out now rather than a month later, when a report
// they expected to be in turns out to be somebody else's spelling.
func reportCohortFit(registryURL, key string, seg map[string]string) {
	dir, err := fetchCohorts(registryURL, key)
	if err != nil {
		return
	}
	var joined, fresh, near []string
	for _, d := range dir.Settable {
		v := seg[d]
		if v == "" {
			continue
		}
		if usedByOthers(dir, d, v) {
			joined = append(joined, d+"="+v)
			continue
		}
		fresh = append(fresh, d+"="+v)
		if n := nearest(dir, d, v); n != "" {
			near = append(near, d+"="+n)
		}
	}
	if len(joined) > 0 {
		fmt.Printf("Cohort: you joined %s, which colleagues here already use.\n", strings.Join(joined, " and "))
	}
	if len(fresh) == 0 {
		return
	}
	fmt.Printf("Cohort: %s is new here — nobody else uses it yet.\n", strings.Join(fresh, " and "))
	if len(near) > 0 {
		fmt.Printf("  The near match is %s. To use it:  tacit connect --segment %s\n",
			strings.Join(near, " and "), strings.Join(near, ","))
		return
	}
	fmt.Println("  If your team already has a name on this registry, use theirs:  tacit cohorts")
}

// offerCohorts is the join loop's last step: the new member sees the cohorts
// their colleagues use, and the command to pick one, at the only moment they
// are certain to be watching. Silent when the registry cannot answer — a
// cohort menu is never worth failing a join over.
func offerCohorts(registryURL, key string) {
	dir, err := fetchCohorts(registryURL, key)
	if err != nil {
		return
	}
	fmt.Print("\n" + cohortReport(registryURL, dir, nil))
}

// cohortUnit picks ONE unit for the whole report and names it. Sessions is the
// better measure of how many people use a cohort, but a registry can carry
// events without audit facts — and a column of zeroes reads as a broken
// feature, not as a missing log. So: sessions when any exist, suggestions
// otherwise, and never the two mixed down one line where they'd compare as if
// they were the same thing.
func cohortUnit(dir client.CohortDirectory) (string, func(client.CohortUse) int) {
	for _, dim := range dir.Dimensions {
		for _, v := range dim.Values {
			if v.Sessions > 0 {
				return "how many sessions it appears in",
					func(u client.CohortUse) int { return u.Sessions }
			}
		}
	}
	return "how many suggestions have gone to it",
		func(u client.CohortUse) int { return u.Shown }
}

// cohortLine renders one dimension's row, marking the member's own value and
// the dimensions they never type.
func cohortLine(dim string, values []client.CohortUse, derived bool, mineVal string,
	count func(client.CohortUse) int) string {

	label := fmt.Sprintf("  %-10s ", dim)
	if len(values) == 0 {
		return label + "— nobody has set this yet\n"
	}
	parts := make([]string, 0, maxListed)
	for i, v := range values {
		if i == maxListed {
			parts = append(parts, fmt.Sprintf("+%d more", len(values)-maxListed))
			break
		}
		part := fmt.Sprintf("%s %d", v.Value, count(v))
		if v.Value == mineVal {
			part += " (yours)"
		}
		parts = append(parts, part)
	}
	suffix := ""
	if derived {
		suffix = "[set for you]"
	}
	return wrapParts(label, parts, suffix)
}

// wrapParts joins the values with a separator, folding onto continuation lines
// aligned under the first value. An org with eight teams still reads in an
// 80-column terminal, which is where this command is run. The suffix is a
// note about the dimension, not a value, so it never takes the separator.
func wrapParts(label string, parts []string, suffix string) string {
	const width = 78
	w := func(s string) int { return utf8.RuneCountInString(s) }
	indent := strings.Repeat(" ", w(label))
	var b strings.Builder
	b.WriteString(label)
	col := w(label)
	wrap := func(sep string, next int) {
		if col+w(sep)+next > width {
			b.WriteString("\n" + indent)
			col = w(indent)
			return
		}
		b.WriteString(sep)
		col += w(sep)
	}
	for i, p := range parts {
		if i > 0 {
			wrap(" · ", w(p))
		}
		b.WriteString(p)
		col += w(p)
	}
	if suffix != "" {
		wrap("   ", w(suffix))
		b.WriteString(suffix)
	}
	return b.String() + "\n"
}

// exampleSegment builds a copy-pasteable --segment from the most-used value of
// each cohort dimension the registry actually has, so the example a member
// reads is a real cohort rather than a placeholder they must translate.
func exampleSegment(dir client.CohortDirectory) string {
	var parts []string
	for _, d := range dir.Settable {
		vals := dir.Values(d)
		if len(vals) == 0 {
			continue
		}
		parts = append(parts, d+"="+vals[0].Value)
		if len(parts) == 2 { // team and role carry the reporting; more is noise
			break
		}
	}
	if len(parts) == 0 {
		return "team=<your-team>,role=<your-role>"
	}
	return strings.Join(parts, ",")
}

// usedByOthers reports whether a dimension's value already exists in the
// directory.
func usedByOthers(dir client.CohortDirectory, dim, val string) bool {
	for _, v := range dir.Values(dim) {
		// Case-insensitive: `Payments` and `payments` are two cohorts to every
		// rollup in the registry, which is exactly the collision worth naming.
		if strings.EqualFold(v.Value, val) {
			return true
		}
	}
	return false
}

// formatSegment prints a segment in the dimension order a member reads.
func formatSegment(seg map[string]string) string {
	var parts []string
	seen := map[string]bool{}
	for _, d := range []string{"team", "role", "function", "domain", "harness", "surface"} {
		if v := seg[d]; v != "" {
			parts = append(parts, d+"="+v)
			seen[d] = true
		}
	}
	var rest []string
	for d, v := range seg {
		if !seen[d] && v != "" {
			rest = append(rest, d+"="+v)
		}
	}
	sort.Strings(rest)
	parts = append(parts, rest...)
	if len(parts) == 0 {
		return "not set"
	}
	return strings.Join(parts, ",")
}
