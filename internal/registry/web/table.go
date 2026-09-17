// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The one data table component. Reuse it; don't hand-roll twins.
//
// Cell content is pre-rendered HTML: callers escape their own text (most cells
// are numbers or already-marked-up values like a rate with a delta), which is
// why this takes strings and not any.
package web

import (
	"fmt"
	"html"
	"net/url"
	"strings"
)

// tableCol is one column: its heading, and whether it carries numbers (right
// aligned, tabular figures, non-wrapping).
type tableCol struct {
	Label string
	Num   bool
}

// tableRow is one row: pre-rendered cells, an optional href making the whole
// row a link, and an optional class (the "total" footer row uses it).
type tableRow struct {
	Cells []string
	Href  string
	Total bool // rendered in <tfoot>, in source order after the body
}

// numCols is the common case: a leading label column followed by numeric ones.
func numCols(label string, nums ...string) []tableCol {
	cols := []tableCol{{Label: label}}
	for _, n := range nums {
		cols = append(cols, tableCol{Label: n, Num: true})
	}
	return cols
}

// dataTable renders the table. Rows marked Total are lifted into <tfoot>.
func dataTable(cols []tableCol, rows []tableRow) string {
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table class="data-table"><thead><tr>`)
	for _, c := range cols {
		cls := ""
		if c.Num {
			cls = ` class="num"`
		}
		// Headings are literal — some carry a hair space (&#8202;) to set
		// "adopt %" apart, so they are written through, not escaped.
		fmt.Fprintf(&b, `<th%s>%s</th>`, cls, c.Label)
	}
	b.WriteString(`</tr></thead><tbody>`)

	var foot []tableRow
	for _, r := range rows {
		if r.Total {
			foot = append(foot, r)
			continue
		}
		writeRow(&b, cols, r)
	}
	b.WriteString(`</tbody>`)
	if len(foot) > 0 {
		b.WriteString(`<tfoot>`)
		for _, r := range foot {
			writeRow(&b, cols, r)
		}
		b.WriteString(`</tfoot>`)
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

func writeRow(b *strings.Builder, cols []tableCol, r tableRow) {
	attrs := ""
	if r.Total {
		attrs = ` class="total"`
	}
	if r.Href != "" {
		attrs += fmt.Sprintf(` data-href="%s"`, html.EscapeString(r.Href))
	}
	fmt.Fprintf(b, `<tr%s>`, attrs)
	for i, cell := range r.Cells {
		cls := ""
		if i < len(cols) && cols[i].Num {
			cls = ` class="num"`
		}
		fmt.Fprintf(b, `<td%s>%s</td>`, cls, cell)
	}
	b.WriteString(`</tr>`)
}

// techniqueCell is the leading cell of every technique-keyed table: the
// org-scoped badge (silent for the "general" majority) followed by the name.
func techniqueCell(scope, name string) string {
	return orgOnlyBadge(scope) + html.EscapeString(name)
}

// techniqueHref is the drill-down every technique row points at.
func techniqueHref(id, windowKey string) string {
	return "/techniques/" + url.PathEscape(id) + "?w=" + url.QueryEscape(windowKey)
}
