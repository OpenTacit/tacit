// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package product holds the one thing the product is called.
//
// The name has moved before (Tacit → Tacit Zone → OpenTacit), and each move was a
// repo-wide substitution that had to find every displayed occurrence by hand.
// Everything the registry renders now asks this package instead, so the next
// move is one environment variable rather than another grep.
//
// Scope is the registry's own chrome and prose — titles, the wordmark, the
// manifest, page ledes, the text exports. Deliberately NOT in scope: the
// binary, the CLI verbs, TACIT_* environment variables, the X-Tacit-* headers,
// the MCP wire names, and the docs served under /docs, which are content rather
// than chrome. Those are identifiers or prose an operator reads elsewhere, and
// renaming them on a config value would break commands or rewrite documents.
// rename_test.go holds each of those cases, because the cost of getting one
// wrong is documentation for a system nobody is running.
//
// One surface asks this package and then freezes the answer: the screenshots in
// docs/user-guide/images, which are captures of a running registry. They carry
// whatever PRODUCT_NAME said when the shutter fell, nothing can rewrite a PNG,
// and no test can read one. A rename is therefore also a recapture —
// hack/guideshots/README.md is the recipe, and it is the only place the loop
// closes.
package product

import (
	"os"
	"strings"
)

// Authored is the name the documentation is WRITTEN with. It answers "what word
// will I find in the prose", where Default answers "what is this called when
// nobody said". The two agree today, and the separation still earns its place:
// Rename turns this word into the configured name on the way out, so a
// deployment that sets PRODUCT_NAME gets a guide in its own name.
//
// It was "Tacit" until 2026-09-16, which meant the guide read "Tacit" wherever
// no substitution runs — on GitHub, and in an editor. That is most of the
// people who will ever read it, and the product is not called Tacit.
const Authored = "OpenTacit"

// EnvKey is the environment variable that sets the name. It carries no TACIT_
// prefix on purpose: it is the one setting whose whole job is to survive the
// product no longer being called Tacit.
const EnvKey = "PRODUCT_NAME"

// Default is the compiled-in name, used when EnvKey is unset.
//
// Settled on 2026-09-14, along with the domain and the module path. It matches
// Authored, which makes Rename a no-op for every deployment that kept the name
// — the cheap path, and the common one. A deployment that wants another name
// sets PRODUCT_NAME rather than editing here.
const Default = "OpenTacit"

// Name returns the product name for display.
//
// Read from the environment on every call rather than memoized, because the
// value reaches templates that are built at package init — before any config is
// loaded — and a cached miss there would freeze the wrong name into the
// process. config.Load materializes the registry.env value into the
// environment, so a name set in the settings file is visible here too.
func Name() string {
	if v := os.Getenv(EnvKey); v != "" {
		return v
	}
	return Default
}

// Rename replaces the authored name with the configured one in a run of plain
// text. A no-op when the two agree, which is every deployment that kept the
// name.
//
// It renames the word and only the word. An occurrence touching a letter, a
// digit, an underscore or a hyphen belongs to an identifier, not to prose —
// X-Tacit-Key is a header a client sends and TACIT_API_KEY is a variable an
// operator sets, and renaming either would document a system nobody is running.
// Case matters for the same reason: the `+"`tacit`"+` command and tacit.zone keep their
// spelling. A trailing apostrophe or full stop is punctuation, so "OpenTacit's" and
// a sentence ending in the name both rename correctly.
//
// This is for authored prose. Text the registry itself composes should ask for
// Name directly rather than write one name and substitute another.
func Rename(text string) string {
	name := Name()
	if name == Authored || !strings.Contains(text, Authored) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		j := strings.Index(text[i:], Authored)
		if j < 0 {
			b.WriteString(text[i:])
			break
		}
		j += i
		end := j + len(Authored)
		b.WriteString(text[i:j])
		if partOfIdentifier(text, j-1) || partOfIdentifier(text, end) {
			b.WriteString(Authored)
		} else {
			b.WriteString(name)
		}
		i = end
	}
	return b.String()
}

// partOfIdentifier reports whether the byte at i binds the name to something
// larger than itself. Out of range is the start or end of the text, which binds
// to nothing.
func partOfIdentifier(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return false
	}
	c := text[i]
	return c == '-' || c == '_' ||
		('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}
