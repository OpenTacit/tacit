// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/product"
)

// The two prompt files go to the model verbatim, so what they call the product
// is what the model calls itself back to the member. They were the only prompts
// that did not take the rename: BriefAuditPrompt and askSystem compose the name
// in this same file, while these were read off disk and used as they stood.
//
// New() is what applies it, so this goes through New() rather than Rename
// directly — the point is that the wiring is there, not that Rename works.
func TestFilePromptsTakeTheConfiguredName(t *testing.T) {
	t.Setenv(product.EnvKey, "Sagesse")
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("characterize-system-prompt.md", "You are a conversation analyst for OpenTacit.")
	write("audit-system-prompt.md", "You are **OpenTacit**, a coach. OpenTacit observes tacit_search and X-Tacit-Key.")

	r, err := New(Config{APIKey: "k", PromptsDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for what, got := range map[string]string{
		"the characterize prompt": r.charPrompt,
		"the audit prompt":        r.auditPrompt,
	} {
		if strings.Contains(got, product.Authored) {
			t.Errorf("%s still names %q in a Sagesse deployment: %q", what, product.Authored, got)
		}
		if !strings.Contains(got, "Sagesse") {
			t.Errorf("%s never took the configured name: %q", what, got)
		}
	}
	// The boundary rule is Rename's, and these files may one day carry a tool
	// name or a header. Neither is the product, and renaming either would
	// describe a system nobody is running.
	for _, keep := range []string{"tacit_search", "X-Tacit-Key"} {
		if !strings.Contains(r.auditPrompt, keep) {
			t.Errorf("%q was renamed; identifiers must survive verbatim: %q", keep, r.auditPrompt)
		}
	}
}

// The shipped files are the authored copy, so they carry the authored name and
// not the old one. A deployment that kept the name gets them unchanged, which is
// most of them, and that path never passes through a rename at all.
func TestShippedPromptFilesCarryTheAuthoredName(t *testing.T) {
	for _, name := range []string{"characterize-system-prompt.md", "audit-system-prompt.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "prompts", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := string(b)
		if !strings.Contains(body, product.Authored) {
			t.Errorf("%s never names the product, so a reader cannot tell whose coach it is", name)
		}
		// The bare old name, and only the bare one. A plain Contains here matched
		// "OpenTacit observes" INSIDE "OpenTacit observes" and failed on a file that
		// was already correct — the same boundary the rename itself is careful
		// about, got wrong by the test checking it.
		if m := staleName.FindString(body); m != "" {
			t.Errorf("%s still says %q — the product has not been called that since 2026-09-14",
				name, strings.TrimSpace(m))
		}
	}
}

// staleName matches the old product name standing on its own, never a fragment
// of the new one and never part of an identifier.
var staleName = regexp.MustCompile(`(^|[^A-Za-z0-9_-])Tacit([^A-Za-z0-9_-]|$)`)
