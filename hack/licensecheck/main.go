// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// licensecheck is the authority THIRD-PARTY-NOTICES.md claims to defer to.
//
// That file used to say a scanner "is the authority if the two ever disagree"
// while nothing ran one, and it had already drifted: golang.org/x/term was a
// direct dependency listed in neither the README's count nor the notices
// table. This program closes that loop. `make licenses` fails CI when a
// dependency arrives under a license the project has not accepted, and
// `make licenses-report` regenerates the table so the document cannot drift
// again by hand.
//
// It scans two build configurations, because the project has two. The default
// build is what `go build` produces and what the README describes; the tagged
// build is what ships (see the Makefile header). A dependency reachable only
// under -tags pg is still redistributed in every release binary, so it has to
// clear the same bar.
//
// # Why there is an override table
//
// go-licenses classifies by matching license text in a file whose NAME matches
// a regexp. Two modules in this tree defeat it, both wrongly and both in ways
// that are safe to correct by hand:
//
//   - modernc.org/mathutil ships plain BSD-3-Clause text in a file the
//     classifier will not accept, and is reported "Unknown".
//   - modernc.org/libc ships its own BSD-3-Clause LICENSE next to a
//     LICENSE-3RD-PARTY.md describing what it vendors. The scanner picks the
//     second and reports MIT.
//
// Each override records the file that was read and what it actually says. An
// override is a claim a human checked, so it names its evidence; anything not
// in the table and not in the allowlist fails.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// goLicensesVersion is pinned. An unpinned scanner that silently changes its
// classifier turns this check into a flake, and the failure would look like a
// licensing problem rather than a tooling one.
const goLicensesVersion = "github.com/google/go-licenses@v1.6.0"

// allowed is every license the project accepts in a dependency. All are
// permissive and none is copyleft, which is the promise THIRD-PARTY-NOTICES.md
// makes in its second paragraph. Adding a line here is a licensing decision:
// make it deliberately, in a commit that says why.
var allowed = map[string]bool{
	"Apache-2.0":   true,
	"MIT":          true,
	"BSD-2-Clause": true,
	"BSD-3-Clause": true,
	"ISC":          true,
}

// override corrects a module the scanner misreads. Evidence is the file a
// human read and what it contained — without it an override is just a way to
// silence the check.
type override struct {
	license  string
	evidence string
}

var overrides = map[string]override{
	"modernc.org/mathutil": {
		license:  "BSD-3-Clause",
		evidence: "LICENSE in the module root is verbatim 3-clause BSD; the classifier rejects the filename, not the text",
	},
	"modernc.org/libc": {
		license:  "BSD-3-Clause",
		evidence: "LICENSE in the module root is 3-clause BSD; the scanner reads LICENSE-3RD-PARTY.md, which describes what libc vendors rather than libc's own terms",
	},
}

// builds are the two configurations that must both pass. The tags string is
// what GOFLAGS carries into the scanner.
var builds = []struct {
	name string
	tags string
}{
	{"default", ""},
	{"release (-tags onnx,pg)", "onnx,pg"},
}

type dep struct {
	module  string
	url     string
	license string
	source  string // "scanner" or the override's evidence
}

func main() {
	report := flag.Bool("report", false, "print the markdown table for THIRD-PARTY-NOTICES.md instead of checking")
	flag.Parse()

	// Two unrelated things this command answers, both about licensing and both
	// cheap: do our own files carry the notice, and are the dependencies'
	// licences acceptable. The header scan is local and instant, so it runs
	// first — no reason to wait on a network fetch to be told about a missing
	// two-line header.
	headersOK := true
	if !*report {
		headersOK = reportHeaders()
	}

	seen := map[string]dep{}
	failed := false
	for _, b := range builds {
		deps, err := scan(b.tags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scanning the %s build: %v\n", b.name, err)
			os.Exit(2)
		}
		for _, d := range deps {
			if !allowed[d.license] {
				fmt.Fprintf(os.Stderr, "FAIL %s is %q, which is not on the allowlist (%s build)\n",
					d.module, d.license, b.name)
				failed = true
				continue
			}
			seen[d.module] = d
		}
	}

	if *report {
		printTable(seen)
		return
	}
	if failed {
		fmt.Fprintln(os.Stderr, "\nEither the dependency is unacceptable, or hack/licensecheck/main.go needs a")
		fmt.Fprintln(os.Stderr, "new allowlist entry or an override with evidence. Do not add one without reading")
		fmt.Fprintln(os.Stderr, "the module's license file.")
	}
	if failed || !headersOK {
		os.Exit(1)
	}
	fmt.Printf("%d modules, every license on the allowlist; every source file carries the header\n", len(seen))
}

// scan runs the pinned scanner over one build configuration and applies the
// override table. The project's own module is dropped: it is the thing being
// licensed, not a third party.
func scan(tags string) ([]dep, error) {
	args := []string{"run", goLicensesVersion, "csv", "--include_tests=false", "./..."}
	cmd := exec.Command("go", args...)
	cmd.Env = os.Environ()
	if tags != "" {
		cmd.Env = append(cmd.Env, "GOFLAGS=-tags="+tags)
	}
	// The scanner writes progress and its "cannot find a known license"
	// complaints to stderr. Those are the cases the override table answers, so
	// they are not failures here; a genuine failure shows up as an
	// unallowlisted license below.
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%v (is the network available for `go run %s`?)", err, goLicensesVersion)
	}

	var deps []dep
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), ",", 3)
		if len(parts) != 3 {
			continue
		}
		d := dep{module: parts[0], url: parts[1], license: parts[2], source: "scanner"}
		if strings.HasPrefix(d.module, "github.com/opentacit/tacit") {
			continue
		}
		if o, ok := overrides[moduleRoot(d.module)]; ok {
			d.module = moduleRoot(d.module)
			d.license = o.license
			d.source = o.evidence
		}
		deps = append(deps, d)
	}
	return deps, sc.Err()
}

// moduleRoot trims the package path the scanner sometimes reports
// (golang.org/x/sys/unix) back to something an override can key on.
func moduleRoot(path string) string {
	for m := range overrides {
		if path == m || strings.HasPrefix(path, m+"/") {
			return m
		}
	}
	return path
}

// printTable emits the notices table grouped by license, which is how
// THIRD-PARTY-NOTICES.md reads today — a reader checking compliance wants to
// know which obligations apply, not which module sorts first.
func printTable(seen map[string]dep) {
	byLicense := map[string][]string{}
	for _, d := range seen {
		byLicense[d.license] = append(byLicense[d.license], d.module)
	}
	licenses := make([]string, 0, len(byLicense))
	for l := range byLicense {
		licenses = append(licenses, l)
	}
	sort.Strings(licenses)

	fmt.Println("| Module | License |")
	fmt.Println("|---|---|")
	for _, l := range licenses {
		mods := byLicense[l]
		sort.Strings(mods)
		quoted := make([]string, len(mods))
		for i, m := range mods {
			quoted[i] = "`" + m + "`"
		}
		fmt.Printf("| %s | %s |\n", strings.Join(quoted, ", "), l)
	}
	fmt.Println()
	fmt.Println("Generated by `make licenses-report` (hack/licensecheck). Do not edit by hand.")
	for _, d := range seen {
		if d.source != "scanner" {
			fmt.Printf("\n<!-- %s: %s recorded by hand — %s -->\n", d.module, d.license, d.source)
		}
	}
}
