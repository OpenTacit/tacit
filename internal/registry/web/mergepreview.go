// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

// Choosing what to contribute, and seeing what the organization already has
// (docs/design/browser-led-team-transition.md, M8).
//
// `tacit merge` is all-or-nothing, and its --dry-run reports what WOULD be sent
// rather than what would be redundant. Both are the terminal's limits showing:
// a list you cannot tick, and a comparison nobody can render as a line of text.
//
// So the form no longer starts a contribution. It redeems the invitation, asks
// the destination what it already has near each of these techniques, and shows
// the member a table. What that changes is the question they are answering —
// from "do I give away my playbook" to "here is what I would add" — and it puts
// the outcome claim on screen per row, so the sentence that travels in the draft
// body is one the contributor read rather than one written about them.
//
// The near-duplicate judgement is the DESTINATION'S. It embeds every draft on
// arrival and matches against its own playbook; anything computed here would be
// a worse answer from a registry that has never seen theirs.

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/merge"
	"github.com/opentacit/tacit/pkg/contracts"
)

// previewRow is one local technique, and what the destination has like it.
type previewRow struct {
	Technique contracts.Technique
	Outcome   contracts.Outcome
	// Match is the nearest technique at the destination, when one was close
	// enough to be worth showing.
	Match      string
	Similarity float64
	// Sent is a prior contribution of this same technique: it has already gone,
	// and sending it again would create a second copy, because the destination's
	// UniqueID never overwrites.
	Sent bool
	// Held: the member unticked it on an earlier visit.
	Held bool
	// Unchecked marks a row the preview could not ask about inside its budget.
	Unchecked bool
}

// duplicateFloor is how similar counts as "they already have this".
//
// High, deliberately. A false "already there" is the expensive mistake — it
// talks a member out of contributing something their organization does not have
// — where a missed one only means a reviewer sees two near neighbours, which is
// the case their review queue is for.
const duplicateFloor = 0.86

// previewBudget bounds the whole comparison. One request per technique against
// somebody else's registry is quick when it is quick and unbounded when it is
// not; a member watching a form should not wait on a slow host. Rows that do
// not resolve inside it render as unchecked rather than as "no match", because
// those are different answers.
const previewBudget = 12 * time.Second

// previewWorkers is how many of those run at once. Small: this is a courtesy
// query against a colleague's registry, not a load test.
const previewWorkers = 4

// buildPreview asks the destination about each technique and returns the rows in
// contribution order.
func buildPreview(dest, key string, techniques []contracts.Technique,
	outcomes map[string]contracts.Outcome, l *merge.Ledger) []previewRow {
	rows := make([]previewRow, len(techniques))
	for i, t := range techniques {
		_, sent := l.AlreadySent(t.ID, dest)
		_, held := l.Held[t.ID]
		rows[i] = previewRow{Technique: t, Outcome: outcomes[t.ID], Sent: sent, Held: held,
			Unchecked: true}
	}
	hc := &http.Client{Timeout: 6 * time.Second}
	deadline := time.Now().Add(previewBudget)

	var wg sync.WaitGroup
	queue := make(chan int)
	var mu sync.Mutex
	for w := 0; w < previewWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				if time.Now().After(deadline) {
					continue // drain: the budget is spent, and the rows say so
				}
				name, sim, ok := merge.Nearest(hc, dest, key, summaryOf(rows[i].Technique))
				mu.Lock()
				rows[i].Unchecked = !ok
				if ok && sim >= duplicateFloor {
					rows[i].Match, rows[i].Similarity = name, sim
				}
				mu.Unlock()
			}
		}()
	}
	for i := range rows {
		queue <- i
	}
	close(queue)
	wg.Wait()
	return rows
}

// summaryOf is the technique as a question: the same text a member's agent would
// be carrying on the turn where this technique was the right answer, which is
// what the destination's retrieval is built to match against.
func summaryOf(t contracts.Technique) string {
	return strings.TrimSpace(t.Name + "\n" + t.Recipe + "\n" + t.AppliesWhen)
}

// mergePreviewPage is the table.
func (s *Server) mergePreviewPage(r *http.Request, dest string, rows []previewRow) page {
	newCount, dupeCount, sentCount := 0, 0, 0
	for _, row := range rows {
		switch {
		case row.Sent:
			sentCount++
		case row.Match != "":
			dupeCount++
		default:
			newCount++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<section class="panel"><h2>What you would add to %s</h2>`,
		html.EscapeString(displayHost(dest)))
	// The headline is the reframe: a count of what is NEW leads, because that is
	// the thing the member is deciding about.
	fmt.Fprintf(&b, `<p class="contributed-tally">%d new`, newCount)
	if dupeCount > 0 {
		fmt.Fprintf(&b, ` · %d they already have`, dupeCount)
	}
	if sentCount > 0 {
		fmt.Fprintf(&b, ` · %d already contributed`, sentCount)
	}
	b.WriteString(`</p>`)
	b.WriteString(`<p class="hint">Each selected technique enters the destination review queue as a draft. ` +
		`Your outcome data stays on this machine. Each technique includes the description shown below.</p>`)

	b.WriteString(`<form method="post" action="/admin/merge/contribute">` +
		fmt.Sprintf(`<input type="hidden" name="csrf" value="%s">`, html.EscapeString(s.csrfToken(r))))
	// The tick column is given a width: an empty header cell takes an equal
	// share of the table and pushed the techniques into the middle of the page.
	b.WriteString(`<div class="table-wrap"><table class="techniques"><thead><tr>` +
		`<th style="width:4rem">send</th><th>technique</th><th>what travels with it</th><th>at ` +
		html.EscapeString(displayHost(dest)) + `</th></tr></thead><tbody>`)
	for _, row := range rows {
		b.WriteString(previewRowHTML(row))
	}
	b.WriteString(`</tbody></table></div>`)
	fmt.Fprintf(&b, `<p><button class="btn" type="submit">Contribute the selected techniques</button> `+
		`<a class="muted" href="/team">Not now</a></p>`)
	b.WriteString(`</form></section>`)
	return page{active: "team", crumbs: []crumb{{label: "Team", href: "/team"}, {label: "What you would add", href: ""}},
		content: b.String()}
}

// previewRowHTML is one row. A technique already contributed is shown and NOT
// offered: sending it again would make a second copy of it in somebody's review
// queue, which is the one outcome this table exists to prevent.
func previewRowHTML(row previewRow) string {
	var b strings.Builder
	b.WriteString(`<tr>`)
	switch {
	case row.Sent:
		b.WriteString(`<td class="muted">✓</td>`)
	default:
		checked := ""
		// Ticked unless there is a reason not to be: a near-duplicate, or a
		// technique the member has already decided to keep back.
		if row.Match == "" && !row.Held {
			checked = " checked"
		}
		fmt.Fprintf(&b, `<td><input type="checkbox" name="id" value="%s"%s aria-label="Contribute %s"></td>`,
			html.EscapeString(row.Technique.ID), checked, html.EscapeString(row.Technique.Name))
	}
	fmt.Fprintf(&b, `<td><a href="/techniques/%s">%s</a></td>`,
		html.EscapeString(row.Technique.ID), html.EscapeString(row.Technique.Name))
	// The claim that would travel, quoted. A contributor should not learn what
	// was said about their evidence by reading somebody else's review queue.
	note := merge.StandingNote(row.Outcome, "")
	if note == "" {
		note = "no measured outcomes yet"
	}
	fmt.Fprintf(&b, `<td class="muted">%s</td>`, html.EscapeString(note))
	switch {
	case row.Sent:
		b.WriteString(`<td class="muted">already contributed</td>`)
	case row.Unchecked:
		b.WriteString(`<td class="muted">could not ask</td>`)
	case row.Match != "":
		fmt.Fprintf(&b, `<td>they have <strong>%s</strong><br><span class="muted">%d%% alike—deselected by default</span></td>`,
			html.EscapeString(row.Match), int(row.Similarity*100))
	default:
		b.WriteString(`<td class="muted">nothing like it</td>`)
	}
	b.WriteString(`</tr>`)
	return b.String()
}

// sortedIDs keeps the ledger writes deterministic, which matters only for the
// tests and for anybody reading the file.
func sortedIDs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
