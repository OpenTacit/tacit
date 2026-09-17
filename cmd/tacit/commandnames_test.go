// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// `tacit setup` was renamed to `tacit connect`. The rename was done in the
// dispatch table and in the help text, and it was NOT done in the six places
// that print a command AT a member: the claim form's confirmation page, the
// cohort nudge that a hook writes into a live session, the remedy shown when a
// key is rejected, two dashboard empty states, and a skill. For weeks the
// product's answer to "how do I set my cohort" was a command that exits
// non-zero — and the nudge handed it to the agent to run.
//
// Grepping the SOURCE for the old name would not have caught it; every one of
// those sites was written before the rename and nobody was looking. What catches
// it is the other direction: take every command the product prints, and check it
// against the commands the binary answers.
//
// So this walks string literals — Go, and the skill and command markdown the
// harness plugins ship — for anything shaped like `tacit <verb>`, and fails on a
// verb `run` does not dispatch. A renamed command now breaks a test in the same
// commit that renames it.
func TestEveryPrintedCommandIsOneTheBinaryAnswers(t *testing.T) {
	known := dispatchedSubcommands(t)

	// The name of the binary, then a lowercase verb, in a position where the
	// text is OFFERING a command rather than mentioning the product. Those
	// positions are few and they are conventions this codebase already keeps:
	// inside backticks or a <code> element, after a shell prompt, after "run:",
	// on an indented line (how every next-steps block is printed), or as a
	// markdown bullet. Bare prose — "tacit reads the key file live" — is not a
	// command and is not matched.
	//
	// A command printed through selfCommand() interpolates a path and cannot be
	// matched here; usage() names every one of them literally, so they are
	// covered by that block instead.
	//
	// The verb may carry hyphens (hook-relay, migrate-store, onnx-fetch).
	// `tacit_search` and `TACIT_API_KEY` cannot match — the separator is a
	// literal space and the prefixes above exclude an identifier character.
	// The separator swallows a soft wrap. `tacit setup --registry …` in the omp
	// guide survived this test for as long as it has run, because the markdown
	// wrapped between "tacit" and "setup" and a literal space cannot match a
	// newline — the one command in the user guides that the binary does not
	// answer, hidden by where the line happened to break.
	cmdRe := regexp.MustCompile("(?m)(?:`|<code>|[$] |run:[ ]+|^[ \t]*[-*][ ]|[ ]{2,})tacit[ \t]*\n?[ \t]*([a-z][a-z-]*)([^\n]*)")

	for _, file := range printedTextFiles(t) {
		for _, text := range printedStrings(t, file) {
			for _, m := range cmdRe.FindAllStringSubmatch(text, -1) {
				verb, rest := m[1], m[2]
				if known[verb] {
					continue
				}
				// In a harness package's skill text, "tacit <name>" is how a
				// member invokes the SKILL — Copilot has no slash commands, so
				// its help lists "tacit search", "tacit drafts". Those are skill
				// names and this test has no business ruling on them. What is
				// still a CLI claim there is a verb carrying a flag.
				if isSkillText(file) && !strings.Contains(rest, "--") {
					continue
				}
				// A few English words follow the product's name in ordinary
				// prose ("tacit is", "tacit reads"). The product name is
				// capitalised there; a lowercase "tacit" followed by a
				// lowercase word is the command, with these exceptions.
				if prose[verb] {
					continue
				}
				t.Errorf("%s prints `tacit %s`, which the binary does not answer\n"+
					"  in: %s\n"+
					"  known subcommands: %s",
					file, verb, snippet(text, m[0]), strings.Join(sortedKeys(known), " "))
			}
		}
	}
}

// prose is the short list of lowercase words that legitimately follow a
// lowercase "tacit" without naming a subcommand. Kept explicit and small: each
// entry is a place the sentence reads better than a rewrite would, and a long
// list here would mean the test had stopped checking anything.
var prose = map[string]bool{
	"plugin":   true, // "/plugin install tacit@tacit" advice, and "the tacit plugin"
	"plugins":  true,
	"relay":    true, // "the tacit relay"
	"server":   true, // "add the tacit server yourself"
	"package":  true, // "the tacit package" (pi)
	"wiring":   true,
	"hook":     true,
	"hooks":    true,
	"skills":   true,
	"key":      true,
	"data":     true,
	"registry": true, // "tacit registry service"
}

// dispatchedSubcommands reads the case list of run's switch in main.go. The
// switch IS the contract — a name in it is a name the binary answers — so the
// test asks it rather than keeping a second copy that could drift the same way
// the printed strings did.
func dispatchedSubcommands(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}
	names := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "run" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if v, err := strconv.Unquote(lit.Value); err == nil {
					names[v] = true
				}
			}
			return true
		})
		return false
	})
	if len(names) < 20 {
		t.Fatalf("found only %d subcommands in run's switch — the parse is wrong, not the code", len(names))
	}
	return names
}

// printedTextFiles is everything that puts a command in front of a member: Go
// source anywhere in the tree (its string literals reach terminals, HTML and
// hook payloads alike) and the markdown the harness packages ship as skills and
// commands. Tests are excluded — a test may legitimately quote an old name while
// asserting it is gone.
func printedTextFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	// The user guide teaches a member what to run today, so a command it names
	// has to be one the binary answers. It is the only documentation that ships
	// with the binary; the design records that used to sit beside it are kept
	// outside this repository, where they record what was true when they were
	// written rather than what the dispatch table says now.
	for _, root := range []string{"..", "../../internal", "../../plugins", "../../techniques",
		"../../docs/user-guide"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			name := d.Name()
			switch {
			case strings.HasSuffix(name, "_test.go"):
			case strings.HasSuffix(name, ".go"), strings.HasSuffix(name, ".md"):
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if len(files) < 50 {
		t.Fatalf("found only %d files to check — the walk is wrong", len(files))
	}
	return files
}

// printedStrings returns the text of a file that a member could read. For Go
// that is its string literals only: a comment is source a developer reads, and
// the historical note explaining WHY a command was renamed has to keep naming
// the old one. For markdown it is the whole file — every word of a skill is
// read by a model and acted on.
func printedStrings(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.HasSuffix(path, ".go") {
		return []string{string(raw)}
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, raw, 0)
	if err != nil {
		return nil // not parseable as Go here is not this test's business
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING {
			if v, err := strconv.Unquote(lit.Value); err == nil {
				out = append(out, v)
			}
		}
		return true
	})
	return out
}

// isSkillText reports whether a file is a harness package's skill or command
// markdown, where "tacit <name>" names a skill rather than a subcommand.
func isSkillText(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/plugins/")
}

func snippet(text, match string) string {
	i := strings.Index(text, match)
	if i < 0 {
		return match
	}
	start := max(0, i-40)
	end := min(len(text), i+len(match)+40)
	return strings.ReplaceAll(text[start:end], "\n", " ")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// The other direction, and the one nothing checked: every command the binary
// answers has to be documented.
//
// The test above walks from printed text to the dispatch table, so a renamed
// command breaks it. A NEW command breaks nothing — `ask`, `pause` and `resume`
// shipped and were named in neither the CLI help nor the command reference, and
// `cohorts`, `dashboard`, `env`, `eval` and `merge` had been missing from the
// reference for longer than that. A command nobody can find is a command that
// does not exist.
func TestEveryCommandTheBinaryAnswersIsDocumented(t *testing.T) {
	// version and help document themselves by being the thing you run to find
	// out; onnx-fetch is a hidden build-stage step and deliberately unlisted.
	unlisted := map[string]bool{"version": true, "help": true, "onnx-fetch": true,
		"-v": true, "--version": true, "-h": true, "--help": true}

	usageText := usageBlock(t)
	reference, err := os.ReadFile("../../docs/user-guide/50-reference/16-command-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	for verb := range dispatchedSubcommands(t) {
		if unlisted[verb] {
			continue
		}
		if !strings.Contains(usageText, "tacit "+verb) {
			t.Errorf("`tacit %s` is dispatched but absent from usage() — run `tacit help` and you will not find it", verb)
		}
		if !strings.Contains(string(reference), "tacit "+verb) {
			t.Errorf("`tacit %s` is dispatched but absent from the command reference", verb)
		}
	}
}

// usageBlock is the raw text of usage()'s single literal.
func usageBlock(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	i := strings.Index(s, "func usage()")
	if i < 0 {
		t.Fatal("no usage() in main.go")
	}
	rest := s[i:]
	open := strings.Index(rest, "`")
	end := strings.Index(rest[open+1:], "`")
	if open < 0 || end < 0 {
		t.Fatal("usage() has no backquoted block")
	}
	return rest[open+1 : open+1+end]
}
