// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Apache-2.0 does not require a per-file notice, but its appendix recommends
// one and it is the first thing a corporate legal review looks for. The holder
// string matches NOTICE exactly — a header naming a person while NOTICE names
// the authors is worse than no header, because now two documents disagree
// about who owns the code.
//
// SPDX rather than the thirteen-line boilerplate: it says the same thing,
// machines can read it, and it costs two lines in 559 files instead of seven
// thousand.
const (
	copyrightLine = "// Copyright 2026 The OpenTacit Authors"
	spdxLine      = "// SPDX-License-Identifier: Apache-2.0"
)

// The same two lines in each language's comment syntax. Go was the only one
// checked at first, which is how the stylesheet, the installer and the plugin
// TypeScript went a year without a header: they were never unlabelled on
// purpose, just never looked at. A shipped file is a shipped file, and the
// installer is the single most-read one in the repository.
var headerForms = map[string][2]string{
	".go":  {copyrightLine, spdxLine},
	".js":  {copyrightLine, spdxLine},
	".ts":  {copyrightLine, spdxLine},
	".css": {"/* Copyright 2026 The OpenTacit Authors */", "/* SPDX-License-Identifier: Apache-2.0 */"},
	".sh":  {"# Copyright 2026 The OpenTacit Authors", "# SPDX-License-Identifier: Apache-2.0"},
	".py":  {"# Copyright 2026 The OpenTacit Authors", "# SPDX-License-Identifier: Apache-2.0"},
}

// skipDirs are not ours to label. onnx and dist hold fetched or built
// artifacts; node_modules is somebody else's code.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "onnx": true, "dist": true,
}

// checkHeaders reports every source file missing the licence header. The blank
// line after the header is checked too: without it the copyright is absorbed
// into the package doc comment, and any //go:build line below it silently
// stops being a build constraint — which would un-gate the Postgres driver
// this program also checks the licence of.
//
// A shebang keeps line 1, because a `#!` pushed down by two comment lines is
// no longer a shebang and the script stops being executable.
func checkHeaders() []string {
	var missing []string
	_ = filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		want, ok := headerForms[filepath.Ext(p)]
		if !ok {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(b), "\n")
		if len(lines) > 0 && strings.HasPrefix(lines[0], "#!") {
			lines = lines[1:]
		}
		switch {
		case len(lines) < 3:
			missing = append(missing, p+" (too short to carry a header)")
		case lines[0] != want[0]:
			missing = append(missing, p+" (no copyright line)")
		case lines[1] != want[1]:
			missing = append(missing, p+" (no SPDX line)")
		case strings.TrimSpace(lines[2]) != "":
			missing = append(missing, p+" (no blank line after the header — this breaks //go:build and the package doc)")
		}
		return nil
	})
	return missing
}

// reportHeaders prints the failures and says whether the check passed.
func reportHeaders() bool {
	missing := checkHeaders()
	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "FAIL %s\n", m)
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d source file(s) missing the licence header. Add, in that file's\ncomment syntax:\n\n%s\n%s\n\nfollowed by a blank line, at the top — below a shebang if there is one.\n",
			len(missing), copyrightLine, spdxLine)
		return false
	}
	return true
}
