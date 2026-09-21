// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package markdown

import (
	"strings"
	"testing"
)

func TestHeadersAndParagraphs(t *testing.T) {
	got := Render("# Title\n\nA paragraph with **bold**, *italic*, `code`, and a [link](https://x).")
	for _, want := range []string{
		"<h1>Title</h1>", "<strong>bold</strong>", "<em>italic</em>",
		"<code>code</code>", `<a href="https://x">link</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestFencedCodeEscapesAndKeepsUnderscores(t *testing.T) {
	got := Render("```sql\nSELECT * FROM t WHERE a < b -- __overall__\n```")
	if !strings.Contains(got, `<pre><code class="lang-sql">`) {
		t.Fatalf("fence class missing: %s", got)
	}
	if !strings.Contains(got, "a &lt; b") {
		t.Fatalf("code not escaped: %s", got)
	}
	if !strings.Contains(got, "__overall__") {
		t.Fatalf("underscores mangled: %s", got)
	}
}

func TestLists(t *testing.T) {
	got := Render("- one\n- two\n  wrapped lazily\n- three\n\n1. a\n2. b")
	if !strings.Contains(got, "<ul><li>one</li><li>two wrapped lazily</li><li>three</li></ul>") {
		t.Fatalf("ul wrong: %s", got)
	}
	if !strings.Contains(got, "<ol><li>a</li><li>b</li></ol>") {
		t.Fatalf("ol wrong: %s", got)
	}
}

func TestPipeTable(t *testing.T) {
	got := Render("| a | b |\n|---|---|\n| 1 | 2 |")
	for _, want := range []string{"<table>", "<th>a</th>", "<td>2</td>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
}

func TestInlineHTMLEscaped(t *testing.T) {
	got := Render("evil <script>alert(1)</script> text")
	if strings.Contains(got, "<script>") {
		t.Fatalf("html not escaped: %s", got)
	}
}

func TestUnterminatedFenceRunsToEOF(t *testing.T) {
	got := Render("```\ncode line")
	if !strings.Contains(got, "code line") {
		t.Fatalf("unterminated fence lost content: %s", got)
	}
}

func TestBlockQuote(t *testing.T) {
	got := Render("intro\n\n> **Note.** First line\n> continues here.\n>\n> 1. and holds\n> 2. a list\n\nafter")
	if !strings.Contains(got, "<blockquote>") || !strings.Contains(got, "</blockquote>") {
		t.Fatalf("no blockquote: %s", got)
	}
	if !strings.Contains(got, "<p><strong>Note.</strong> First line continues here.</p>") {
		t.Fatalf("quoted paragraph wrong: %s", got)
	}
	if !strings.Contains(got, "<ol><li>and holds</li><li>a list</li></ol>") {
		t.Fatalf("quoted list wrong: %s", got)
	}
	// The quote must not swallow the surrounding paragraphs.
	if !strings.Contains(got, "<p>intro</p>") || !strings.Contains(got, "<p>after</p>") {
		t.Fatalf("neighbors damaged: %s", got)
	}
}

func TestThematicBreak(t *testing.T) {
	got := Render("before\n\n---\n\nafter")
	if !strings.Contains(got, "<p>before</p>\n<hr>\n<p>after</p>") {
		t.Fatalf("hr wrong: %s", got)
	}
	// A list item line ("- x") must not become a rule, and a rule must not
	// be folded into a preceding list item as lazy continuation.
	got = Render("- item\n---")
	if !strings.Contains(got, "<li>item</li>") || !strings.Contains(got, "<hr>") {
		t.Fatalf("hr after list wrong: %s", got)
	}
}

func TestImages(t *testing.T) {
	got := Render("![The map](images/map.png)")
	if !strings.Contains(got, `<img src="images/map.png" alt="The map" loading="lazy">`) {
		t.Fatalf("image wrong: %s", got)
	}
	// The image syntax must win over the link syntax it contains — a stray
	// leading "!" plus an anchor is the failure mode.
	if strings.Contains(got, "!<a") || strings.Contains(got, "<a href=\"images/map.png\"") {
		t.Fatalf("image rendered as link: %s", got)
	}
}
