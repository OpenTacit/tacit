// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
)

type diffOp struct {
	kind byte // '=' unchanged, '-' removed, '+' added
	text string
}

type sideBySideDiffLine struct {
	oldNo, newNo     int
	oldHTML, newHTML string
	oldClass         string
	newClass         string
}

const maxDiffCells = 250_000

func withinDiffBudget(a, b int) bool {
	return a == 0 || b <= maxDiffCells/a
}

// sequenceDiff returns an LCS-based edit script. Technique snapshots are small, so
// the quadratic table buys deterministic, well-aligned output without another
// dependency or the surprising edit choices a heuristic diff can make. The
// budgeted fallback remains linear for unexpectedly large contributed text.
func sequenceDiff(old, next []string) []diffOp {
	if !withinDiffBudget(len(old)+1, len(next)+1) {
		out := make([]diffOp, 0, len(old)+len(next))
		for _, text := range old {
			out = append(out, diffOp{'-', text})
		}
		for _, text := range next {
			out = append(out, diffOp{'+', text})
		}
		return out
	}
	dp := make([][]int, len(old)+1)
	for i := range dp {
		dp[i] = make([]int, len(next)+1)
	}
	for i := len(old) - 1; i >= 0; i-- {
		for j := len(next) - 1; j >= 0; j-- {
			if old[i] == next[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []diffOp
	for i, j := 0, 0; i < len(old) || j < len(next); {
		switch {
		case i < len(old) && j < len(next) && old[i] == next[j]:
			out = append(out, diffOp{'=', old[i]})
			i++
			j++
		case i < len(old) && (j == len(next) || dp[i+1][j] >= dp[i][j+1]):
			out = append(out, diffOp{'-', old[i]})
			i++
		default:
			out = append(out, diffOp{'+', next[j]})
			j++
		}
	}
	return out
}

func diffTokens(s string) []string {
	var out []string
	var run []rune
	kind := byte(0)
	flush := func() {
		if len(run) > 0 {
			out = append(out, string(run))
			run = nil
		}
	}
	for _, r := range s {
		k := byte('p')
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
			k = 'w'
		} else if unicode.IsSpace(r) {
			k = 's'
		}
		// Keep punctuation as individual tokens; grouping it can turn one changed
		// delimiter into a large highlighted run.
		if k == 'p' {
			flush()
			out = append(out, string(r))
			kind = 0
			continue
		}
		if kind != 0 && kind != k {
			flush()
		}
		kind = k
		run = append(run, r)
	}
	flush()
	return out
}

func renderTokenDiff(ops []diffOp, side byte) string {
	var b strings.Builder
	mark := byte(0)
	closeMark := func() {
		if mark != 0 {
			b.WriteString(`</mark>`)
			mark = 0
		}
	}
	for _, op := range ops {
		show := op.kind == '=' || op.kind == side
		if !show {
			continue
		}
		if op.kind == side {
			if mark == 0 {
				class := "diff-word-del"
				if side == '+' {
					class = "diff-word-add"
				}
				fmt.Fprintf(&b, `<mark class="%s">`, class)
				mark = side
			}
		} else {
			closeMark()
		}
		b.WriteString(html.EscapeString(op.text))
	}
	closeMark()
	if b.Len() == 0 {
		return `&nbsp;`
	}
	return b.String()
}

func inlineWordDiff(old, next string) (string, string) {
	ops := sequenceDiff(diffTokens(old), diffTokens(next))
	return renderTokenDiff(ops, '-'), renderTokenDiff(ops, '+')
}

type linePair struct {
	old, next int // -1 is a gap on that side
}

func diffWordToken(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' {
			return false
		}
	}
	return true
}

func linesRelated(old, next string) bool {
	words := map[string]bool{}
	for _, token := range diffTokens(old) {
		if diffWordToken(token) {
			words[strings.ToLower(token)] = true
		}
	}
	for _, token := range diffTokens(next) {
		if diffWordToken(token) && words[strings.ToLower(token)] {
			return true
		}
	}
	return false
}

// alignChangedLines distinguishes substitutions from gaps inside one changed
// hunk. A shared word makes two lines plausible counterparts; unrelated lines
// cost more to pair than rendering one removal and one addition. This avoids
// shifting every inline highlight when a line is inserted before nearby edits.
func alignChangedLines(old, next []string) []linePair {
	if !withinDiffBudget(len(old)+1, len(next)+1) {
		pairs := make([]linePair, 0, len(old)+len(next))
		for i := range old {
			pairs = append(pairs, linePair{i, -1})
		}
		for i := range next {
			pairs = append(pairs, linePair{-1, i})
		}
		return pairs
	}
	const gapCost = 2
	dp := make([][]int, len(old)+1)
	for i := range dp {
		dp[i] = make([]int, len(next)+1)
	}
	for i := 1; i <= len(old); i++ {
		dp[i][0] = i * gapCost
	}
	for j := 1; j <= len(next); j++ {
		dp[0][j] = j * gapCost
	}
	for i := 1; i <= len(old); i++ {
		for j := 1; j <= len(next); j++ {
			subCost := 5 // unrelated: prefer a removal plus an addition (cost 4)
			if linesRelated(old[i-1], next[j-1]) {
				subCost = 1
			}
			dp[i][j] = min(dp[i-1][j]+gapCost, dp[i][j-1]+gapCost, dp[i-1][j-1]+subCost)
		}
	}

	i, j := len(old), len(next)
	reversed := make([]linePair, 0, i+j)
	for i > 0 || j > 0 {
		subCost := 5
		if i > 0 && j > 0 && linesRelated(old[i-1], next[j-1]) {
			subCost = 1
		}
		switch {
		case i > 0 && j > 0 && dp[i][j] == dp[i-1][j-1]+subCost:
			reversed = append(reversed, linePair{i - 1, j - 1})
			i--
			j--
		case i > 0 && dp[i][j] == dp[i-1][j]+gapCost:
			reversed = append(reversed, linePair{i - 1, -1})
			i--
		default:
			reversed = append(reversed, linePair{-1, j - 1})
			j--
		}
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func sideBySideTextDiff(old, next string) []sideBySideDiffLine {
	ops := sequenceDiff(strings.Split(old, "\n"), strings.Split(next, "\n"))
	oldNo, newNo := 1, 1
	var rows []sideBySideDiffLine
	for i := 0; i < len(ops); {
		if ops[i].kind == '=' {
			escaped := html.EscapeString(ops[i].text)
			if escaped == "" {
				escaped = `&nbsp;`
			}
			rows = append(rows, sideBySideDiffLine{oldNo, newNo, escaped, escaped, "diff-same", "diff-same"})
			oldNo++
			newNo++
			i++
			continue
		}
		var removed, added []string
		for i < len(ops) && ops[i].kind != '=' {
			if ops[i].kind == '-' {
				removed = append(removed, ops[i].text)
			} else {
				added = append(added, ops[i].text)
			}
			i++
		}
		for _, pair := range alignChangedLines(removed, added) {
			row := sideBySideDiffLine{oldClass: "diff-empty", newClass: "diff-empty"}
			if pair.old >= 0 && pair.next >= 0 {
				row.oldNo, row.newNo = oldNo, newNo
				row.oldHTML, row.newHTML = inlineWordDiff(removed[pair.old], added[pair.next])
				row.oldClass, row.newClass = "diff-removed", "diff-added"
				oldNo++
				newNo++
			} else if pair.old >= 0 {
				row.oldNo, row.oldHTML, row.oldClass = oldNo, html.EscapeString(removed[pair.old]), "diff-removed"
				oldNo++
			} else {
				row.newNo, row.newHTML, row.newClass = newNo, html.EscapeString(added[pair.next]), "diff-added"
				newNo++
			}
			if row.oldHTML == "" {
				row.oldHTML = `&nbsp;`
			}
			if row.newHTML == "" {
				row.newHTML = `&nbsp;`
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func techniqueSnapshotText(technique models.Technique) string {
	var lines []string
	scalar := func(label, value string) { lines = append(lines, label+": "+strconv.Quote(value)) }
	block := func(label, value string) {
		lines = append(lines, label+": |")
		for _, line := range strings.Split(value, "\n") {
			lines = append(lines, "  "+line)
		}
	}
	jsonValue := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			return "[]"
		}
		return string(raw)
	}
	scalar("name", technique.Name)
	block("description", technique.Description)
	scalar("scope", technique.Scope)
	scalar("status", technique.Status)
	scalar("provenance", technique.Provenance)
	block("recipe", technique.Recipe)
	block("before_after", technique.BeforeAfter)
	lines = append(lines, "tags: "+jsonValue(technique.Tags))
	lines = append(lines, "task_types: "+jsonValue(technique.TaskTypes))
	lines = append(lines, "triggers: "+jsonValue(technique.Triggers))
	block("applies_when", technique.AppliesWhen)
	block("not_when", technique.NotWhen)
	lines = append(lines, "support_matrix: "+jsonValue(technique.SupportMatrix))
	lines = append(lines, "channels: "+jsonValue(technique.Channels))
	scalar("shipped", technique.Shipped)
	scalar("source", technique.Source)
	return strings.Join(lines, "\n")
}

func techniqueVersionDiff(old, next models.Technique, current, canRevert bool, techniqueID string) string {
	nextLabel := fmt.Sprintf("v%d", next.Version)
	if current {
		nextLabel += " · current"
	}
	// Reverting targets the archived version on the LEFT (old) — the one being
	// looked at — and lands its content as a new current version. Confirmed
	// inline; the handler re-checks auth and range (contribute.RevertTo).
	revertHTML := ""
	if canRevert {
		revertHTML = fmt.Sprintf(`<form class="version-revert" method="post" action="/admin/techniques/revert/%d/%s" onsubmit="return confirm('Revert this technique to v%d? This creates a new current version with the content of v%d and archives the version it replaces.');"><button class="btn" type="submit">Revert to v%d</button></form>`,
			old.Version, html.EscapeString(techniqueID), old.Version, old.Version, old.Version)
	}
	rows := sideBySideTextDiff(techniqueSnapshotText(old), techniqueSnapshotText(next))
	var b strings.Builder
	fmt.Fprintf(&b, `<section class="version-diff panel"><header class="version-diff-title"><h2>v%d → v%d</h2><div class="version-diff-actions"><span>%s</span>%s</div></header>`+
		`<div class="version-diff-scroll" tabindex="0" aria-label="Changes from version %d to version %d"><div class="version-diff-grid">`+
		`<div class="version-diff-head">v%d <span>%s</span></div><div class="version-diff-head">%s <span>%s</span></div>`,
		old.Version, next.Version, ui.LocalTimeISO(next.UpdatedAt, ui.LTStamp), revertHTML, old.Version, next.Version,
		old.Version, ui.LocalTimeISO(old.UpdatedAt, ui.LTStamp), nextLabel, ui.LocalTimeISO(next.UpdatedAt, ui.LTStamp))
	for _, row := range rows {
		oldNum, newNum := "", ""
		if row.oldNo > 0 {
			oldNum = strconv.Itoa(row.oldNo)
		}
		if row.newNo > 0 {
			newNum = strconv.Itoa(row.newNo)
		}
		fmt.Fprintf(&b, `<div class="version-diff-line %s"><span class="version-line-no">%s</span><code>%s</code></div>`+
			`<div class="version-diff-line %s"><span class="version-line-no">%s</span><code>%s</code></div>`,
			row.oldClass, oldNum, row.oldHTML, row.newClass, newNum, row.newHTML)
	}
	b.WriteString(`</div></div></section>`)
	return b.String()
}

// pageTechniqueHistory compares each archived version with the version that
// replaced it, newest change first — the on-demand view behind the "prior
// versions" link (docs/design/revision-design.md).
func (s *Server) pageTechniqueHistory(r *http.Request, user oidc.Claims) page {
	id, _ := url.PathUnescape(r.PathValue("id"))
	cid := html.EscapeString(id)
	technique, ok, err := s.Store.GetTechnique(id)
	if err != nil || !ok {
		return page{status: 404, active: "techniques",
			content: "<p>No technique with that ID. <a href=\"/techniques\">All techniques.</a></p>"}
	}
	versions, err := s.Store.TechniqueVersions(id)
	trail := techniqueCrumbs(id, technique.Name, technique.Status, "History", len(versions))
	if err != nil {
		return storeUnavailablePage("techniques", trail, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="page-head"><p class="sub"><code>%s</code> is at <strong>v%d</strong>. Each comparison shows an archived version on the left and its replacement on the right, newest first.</p></div>`,
		cid, technique.Version)
	// Show the revert control to exactly the users adminOnly would let POST it:
	// anyone on a pilot with no admin list, otherwise the named admins.
	canRevert := len(s.cfg().AdminEmails) == 0 || s.isAdmin(user)
	newer := technique
	for i, archived := range versions {
		b.WriteString(techniqueVersionDiff(archived, newer, i == 0, canRevert, id))
		newer = archived
	}
	return page{active: "techniques", crumbs: trail, content: b.String()}
}

// handleTechniqueVersions is the JSON view of the same archive.
func (s *Server) handleTechniqueVersions(w http.ResponseWriter, r *http.Request) {
	id, _ := url.PathUnescape(r.PathValue("id"))
	if _, ok, err := s.Store.GetTechnique(id); err != nil {
		s.sendError(w, 500, err.Error())
		return
	} else if !ok {
		s.sendError(w, 404, "no technique with that ID")
		return
	}
	versions, err := s.Store.TechniqueVersions(id)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	if versions == nil {
		versions = []models.Technique{}
	}
	s.sendJSON(w, 200, map[string]any{"id": id, "versions": versions})
}
