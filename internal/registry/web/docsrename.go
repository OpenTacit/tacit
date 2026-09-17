// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Renaming the product in the served user guide.
//
// The guide is authored in plain English naming the product, because the same
// files are read on GitHub and in an editor where nothing substitutes anything.
// The registry serves them to members, and there the name has to be the one the
// deployment chose (PRODUCT_NAME) — otherwise the page a member is reading calls
// the thing in front of them by a name it does not answer to.
//
// So the rename happens on the way out, and late: after the markdown is HTML and
// after docTOC has assigned the heading ids.
//
// Late matters. Renaming the source instead would rewrite a heading and with it
// the id derived from it, while the in-page link pointing at that id — written
// lowercase in the markdown, and a URL rather than prose — would not move. The
// guide has such a link today. Renaming HTML, with every tag left alone, keeps
// ids and hrefs exactly as authored and changes only what is read.
package web

import (
	"regexp"
	"strings"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/ui"
)

// The guide's masthead is an image of the wordmark — a hand-laid-out SVG with
// the mark placed at one coordinate and the name typeset at another. A picture
// of text cannot follow a setting, and this one could not even be regenerated
// honestly: centring the pair means knowing how wide the name renders, which is
// a font metric the server does not have.
//
// So the registry serves the masthead as the lockup it draws everywhere else —
// the mark and the name, as markup. It takes the configured name, the fit pass
// sizes it to the column, one rule themes it in both modes instead of two SVGs,
// and it is text a reader can select and a screen reader can read.
//
// The markdown keeps the image. On GitHub and in an editor there is no registry
// to substitute anything, and there the authored name is the right one.
var docMastheadRe = regexp.MustCompile(`<img[^>]*\bsrc="[^"]*images/hero\.svg"[^>]*>`)

// docMasthead replaces the guide's masthead image with the live wordmark.
func docMasthead(rendered string) string {
	name := product.Name()
	lockup := `<p class="doc-masthead" style="--name-len:` + ui.WordmarkLen(name) + `"` +
		ui.WordmarkAttr("4.5") + `>` + markSVG + ui.Wordmark(productHTML()) + `</p>`
	return docMastheadRe.ReplaceAllLiteralString(rendered, lockup)
}

// guideText renames plain text — a title, a summary — that belongs to a user
// guide document. Everything the guide shows about a document rather than in it
// passes through here, so a card, a crumb and a rail entry cannot end up naming
// the product three different ways.
//
// Scoped to the guide, as the rename is throughout: the development docs under
// /docs describe this codebase, where the name is a package path and a binary
// and renaming it would describe something that does not exist.
func guideText(slug, text string) string {
	if !inUserGuide(slug) {
		return text
	}
	return product.Rename(text)
}

// renameProductHTML applies product.Rename to the text of a rendered document,
// leaving markup untouched.
//
// Two things are skipped, and they are skipped for different reasons. Tags,
// because an href, an id and a data- attribute are addresses and keys rather
// than prose. Code, because a command is a thing a member types: the guide says
// to run `tacit connect`, and a registry that renamed that would be handing out
// a command that does not exist.
func renameProductHTML(doc string) string {
	if product.Name() == product.Authored || !strings.Contains(doc, product.Authored) {
		return doc
	}
	var b strings.Builder
	b.Grow(len(doc))
	literal := 0 // open <code>/<pre> elements
	for i := 0; i < len(doc); {
		j := strings.IndexByte(doc[i:], '<')
		if j < 0 {
			b.WriteString(renameUnlessLiteral(doc[i:], literal))
			break
		}
		b.WriteString(renameUnlessLiteral(doc[i:i+j], literal))
		i += j
		k := strings.IndexByte(doc[i:], '>')
		if k < 0 {
			// No closing bracket: not markup, whatever it is. Emit the rest as it
			// stands rather than guessing where the tag was meant to end.
			b.WriteString(doc[i:])
			break
		}
		tag := doc[i : i+k+1]
		b.WriteString(tag)
		i += k + 1
		switch {
		case isTag(tag, "<code"), isTag(tag, "<pre"):
			if !strings.HasSuffix(tag, "/>") {
				literal++
			}
		case isTag(tag, "</code"), isTag(tag, "</pre"):
			if literal > 0 {
				literal--
			}
		}
	}
	return b.String()
}

func renameUnlessLiteral(text string, literal int) string {
	if literal > 0 {
		return text
	}
	return product.Rename(text)
}

// isTag reports whether tag opens (or closes) the named element — matching the
// name and not merely its first letters, so <precise> is not <pre>.
func isTag(tag, name string) bool {
	if !strings.HasPrefix(tag, name) {
		return false
	}
	rest := tag[len(name):]
	return rest == ">" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, ">") ||
		strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "\t") || strings.HasPrefix(rest, "\n")
}
