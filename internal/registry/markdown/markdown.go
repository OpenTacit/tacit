// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package markdown is a minimal, dependency-free Markdown -> HTML renderer
// for the dashboard's Docs view.
//
// Covers exactly the subset used in docs/*.md: ATX headers, fenced code
// blocks, unordered/ordered lists (with lazy line-wrap continuation), pipe
// tables, block quotes (every line >-prefixed, no lazy continuation),
// thematic breaks, links,
// images, and bold/italic (asterisk only — the docs use underscores
// only inside identifiers like __overall__, never as emphasis). Not a general
// CommonMark implementation — kept small, consistent with the stdlib-only
// dependency policy (docs/design/architecture.md).
package markdown

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

var (
	fenceRe    = regexp.MustCompile("^```")
	hrRe       = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
	headerRe   = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	tableSepRe = regexp.MustCompile(`^\|?[\s:|-]+\|?$`)
	ulItemRe   = regexp.MustCompile(`^[-*+]\s+`)
	olItemRe   = regexp.MustCompile(`^\d+\.\s+`)

	inlineCodeRe = regexp.MustCompile("`([^`]+)`")
	imageRe      = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	linkRe       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	boldRe       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	italicRe     = regexp.MustCompile(`\*(.+?)\*`)
	stashRe      = regexp.MustCompile("\x00(\\d+)\x00")
)

// inline renders spans within one block of text: code, links, bold, italic.
func inline(text string) string {
	text = html.EscapeString(text)
	var codes []string
	text = inlineCodeRe.ReplaceAllStringFunc(text, func(m string) string {
		codes = append(codes, inlineCodeRe.FindStringSubmatch(m)[1])
		return fmt.Sprintf("\x00%d\x00", len(codes)-1)
	})
	// Images before links: the image syntax contains the link syntax, and
	// letting linkRe run first would leave a stray "!" and an anchor.
	text = imageRe.ReplaceAllString(text, `<img src="$2" alt="$1" loading="lazy">`)
	text = linkRe.ReplaceAllString(text, `<a href="$2">$1</a>`)
	text = boldRe.ReplaceAllString(text, "<strong>$1</strong>")
	text = italicRe.ReplaceAllString(text, "<em>$1</em>")
	return stashRe.ReplaceAllStringFunc(text, func(m string) string {
		i, _ := strconv.Atoi(stashRe.FindStringSubmatch(m)[1])
		return "<code>" + codes[i] + "</code>"
	})
}

// isBlockStart reports whether a line starts a new block (so a list can't
// lazily swallow it).
func isBlockStart(stripped string) bool {
	return stripped == "" || strings.HasPrefix(stripped, "```") || strings.HasPrefix(stripped, "|") ||
		strings.HasPrefix(stripped, ">") || hrRe.MatchString(stripped) ||
		headerRe.MatchString(stripped) || ulItemRe.MatchString(stripped) || olItemRe.MatchString(stripped)
}

// consumeList collects list items starting at i, folding lazily-wrapped
// continuation lines into the current item.
func consumeList(lines []string, i, n int, itemRe *regexp.Regexp) ([]string, int) {
	items := []string{}
	current := itemRe.ReplaceAllString(strings.TrimSpace(lines[i]), "")
	i++
	for i < n {
		stripped := strings.TrimSpace(lines[i])
		if itemRe.MatchString(stripped) {
			items = append(items, current)
			current = itemRe.ReplaceAllString(stripped, "")
			i++
		} else if stripped != "" && !isBlockStart(stripped) {
			current += " " + stripped
			i++
		} else {
			break
		}
	}
	items = append(items, current)
	return items, i
}

// Render converts the markdown subset to HTML.
func Render(text string) string {
	lines := strings.Split(text, "\n")
	n := len(lines)
	var out []string
	var paragraph []string

	flush := func() {
		if len(paragraph) > 0 {
			out = append(out, "<p>"+inline(strings.Join(paragraph, " "))+"</p>")
			paragraph = paragraph[:0]
		}
	}

	i := 0
	for i < n {
		stripped := strings.TrimSpace(lines[i])

		if fenceRe.MatchString(stripped) {
			flush()
			lang := strings.TrimSpace(stripped[3:])
			var code []string
			i++
			for i < n && !fenceRe.MatchString(strings.TrimSpace(lines[i])) {
				code = append(code, lines[i])
				i++
			}
			i++ // skip closing fence (tolerates a missing one: runs to EOF)
			cls := ""
			if lang != "" {
				cls = ` class="lang-` + html.EscapeString(lang) + `"`
			}
			out = append(out, "<pre><code"+cls+">"+html.EscapeString(strings.Join(code, "\n"))+"</code></pre>")
			continue
		}

		// Block quote: strip one marker off the contiguous >-run and render
		// the body recursively, so quotes carry paragraphs, lists, and code
		// for free. The docs prefix every quoted line (no lazy continuation).
		if strings.HasPrefix(stripped, ">") {
			flush()
			var quote []string
			for i < n {
				q := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(q, ">") {
					break
				}
				quote = append(quote, strings.TrimPrefix(strings.TrimPrefix(q, ">"), " "))
				i++
			}
			out = append(out, "<blockquote>"+Render(strings.Join(quote, "\n"))+"</blockquote>")
			continue
		}

		if hrRe.MatchString(stripped) {
			flush()
			out = append(out, "<hr>")
			i++
			continue
		}

		if m := headerRe.FindStringSubmatch(stripped); m != nil {
			flush()
			level := len(m[1])
			out = append(out, fmt.Sprintf("<h%d>%s</h%d>", level, inline(strings.TrimSpace(m[2])), level))
			i++
			continue
		}

		if strings.HasPrefix(stripped, "|") && i+1 < n && tableSepRe.MatchString(strings.TrimSpace(lines[i+1])) {
			flush()
			header := splitRow(stripped)
			i += 2
			var rows [][]string
			for i < n && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				rows = append(rows, splitRow(strings.TrimSpace(lines[i])))
				i++
			}
			var thead, tbody strings.Builder
			for _, c := range header {
				thead.WriteString("<th>" + inline(c) + "</th>")
			}
			for _, row := range rows {
				tbody.WriteString("<tr>")
				for _, c := range row {
					tbody.WriteString("<td>" + inline(c) + "</td>")
				}
				tbody.WriteString("</tr>")
			}
			out = append(out, "<table><thead><tr>"+thead.String()+"</tr></thead><tbody>"+tbody.String()+"</tbody></table>")
			continue
		}

		if ulItemRe.MatchString(stripped) {
			flush()
			items, next := consumeList(lines, i, n, ulItemRe)
			i = next
			out = append(out, "<ul>"+joinItems(items)+"</ul>")
			continue
		}

		if olItemRe.MatchString(stripped) {
			flush()
			items, next := consumeList(lines, i, n, olItemRe)
			i = next
			out = append(out, "<ol>"+joinItems(items)+"</ol>")
			continue
		}

		if stripped == "" {
			flush()
			i++
			continue
		}

		paragraph = append(paragraph, stripped)
		i++
	}

	flush()
	return strings.Join(out, "\n")
}

func splitRow(line string) []string {
	cells := strings.Split(strings.Trim(line, "|"), "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

func joinItems(items []string) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString("<li>" + inline(it) + "</li>")
	}
	return b.String()
}
