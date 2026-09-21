// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/demo"
	pkgclient "github.com/opentacit/tacit/pkg/client"
)

// cmdDemo is the demonstration driver: it populates a running OpenTacit registry
// with a month of believable, organization-specific usage for a chosen
// scenario, so the whole product surface (outcomes, cohorts, the technique
// map, drafts) can be seen populated. Content is authored in natural language
// by an LLM (or shipped pre-generated) and stored as a portable dataset that
// re-creates the same scenario on demand.
func cmdDemo(args []string) int {
	if len(args) == 0 {
		demoUsage()
		return 2
	}
	switch args[0] {
	case "scenarios", "list":
		return cmdDemoScenarios(args[1:])
	case "generate", "gen":
		return cmdDemoGenerate(args[1:])
	case "load":
		return cmdDemoLoad(args[1:])
	case "help", "-h", "--help":
		demoUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown demo subcommand %q\n\n", args[0])
		demoUsage()
		return 2
	}
}

func demoUsage() {
	fmt.Fprint(os.Stderr, `tacit demo — fill a registry with one month of demonstration usage

Usage:
  tacit demo scenarios                       list the built-in scenarios
  tacit demo load [--scenario <key>] [flags] load a scenario into an active registry
  tacit demo generate --scenario <key>       author a fresh dataset with an LLM (needs TACIT_LLM_API_KEY)

Load flags:
  --scenario <key>   scenario to load (default: software-vendor)
  --registry <url>   target registry (default: your configured registry)
  --key <key>        registry ROOT api key (promote/recompute need it)
  --dataset <path>   load this portable dataset file instead of the shipped one
  --generate         author the dataset with an LLM; do not use the shipped one
  --out <path>       also save the loaded dataset to this file (a reusable copy)
  --force            load even if the target already holds substantial content

  Before you load, run tacit against a SCRATCH data dir. The driver refuses a
  target that looks like a real instance unless you pass --force.
`)
}

func cmdDemoScenarios(args []string) int {
	fs := flag.NewFlagSet("demo scenarios", flag.ContinueOnError)
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tTITLE\tSHIPPED\tSUMMARY")
	for _, s := range demo.Scenarios {
		shipped := "generate"
		if s.Shipped != "" {
			shipped = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Key, s.Title, shipped, s.Summary)
	}
	tw.Flush()
	fmt.Println("\nLoad the default:  tacit demo load")
	fmt.Println("Author a fresh one: tacit demo generate --scenario <key>   (needs TACIT_LLM_API_KEY)")
	return 0
}

func cmdDemoGenerate(args []string) int {
	fs := flag.NewFlagSet("demo generate", flag.ContinueOnError)
	scenario := fs.String("scenario", "software-vendor", "scenario key (see: tacit demo scenarios)")
	out := fs.String("out", "", "write the dataset here (default: ./<scenario>.json)")
	seed := fs.Int64("seed", 0, "dataset seed (0 keeps the model's)")
	days := fs.Int("days", 30, "length of the simulated window in days")
	model := fs.String("model", "", "override the generation model (default: $TACIT_DEMO_MODEL)")
	maxTokens := fs.Int("max-tokens", 0, "model output budget (0: $TACIT_DEMO_MAX_TOKENS, then a generous default)")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	sc, ok := demo.ScenarioByKey(*scenario)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown scenario %q (see: tacit demo scenarios)\n", *scenario)
		return 2
	}
	d, err := demo.Generate(sc, demo.GenerateOptions{Model: *model, Seed: *seed, Days: *days, MaxTokens: *maxTokens, Progress: os.Stdout})
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate failed: %v\n", err)
		return exitUnreachable
	}
	path := *out
	if path == "" {
		path = *scenario + ".json"
	}
	if err := d.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "cannot save the dataset: %v\n", err)
		return exitUnreachable
	}
	fmt.Printf("Wrote %s (%d techniques, %d teams). Load it with:\n  tacit demo load --dataset %s\n",
		path, len(d.Techniques), len(d.Teams), path)
	return 0
}

func cmdDemoLoad(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("demo load", flag.ContinueOnError)
	scenario := fs.String("scenario", "software-vendor", "scenario key (see: tacit demo scenarios)")
	registryURL := fs.String("registry", cfg.RegistryURL, "target registry URL")
	key := fs.String("key", cfg.RegistryKey, "registry root API key")
	datasetPath := fs.String("dataset", "", "load this portable dataset file instead of the shipped one")
	generate := fs.Bool("generate", false, "author the dataset with an LLM; do not use the shipped one")
	out := fs.String("out", "", "also save the loaded dataset to this file")
	force := fs.Bool("force", false, "load even if the target already holds substantial content")
	seed := fs.Int64("seed", 0, "dataset seed override (for --generate)")
	days := fs.Int("days", 30, "window length for --generate")
	maxTokens := fs.Int("max-tokens", 0, "model output budget for --generate (0: $TACIT_DEMO_MAX_TOKENS, then a generous default)")
	keepDates := fs.Bool("keep-dates", false, "keep the dataset's own dates; do not move the window to end today")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	if *registryURL == "" {
		fmt.Fprintln(os.Stderr, "no registry URL — pass --registry <url> (and --key <root-key>), or configure one with tacit connect")
		return 2
	}

	// Resolve the dataset: an explicit file, a fresh LLM generation, or the
	// shipped pre-generated one.
	var (
		d   *demo.Dataset
		err error
	)
	switch {
	case *datasetPath != "":
		d, err = demo.LoadDataset(*datasetPath)
	case *generate:
		sc, ok := demo.ScenarioByKey(*scenario)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown scenario %q\n", *scenario)
			return 2
		}
		d, err = demo.Generate(sc, demo.GenerateOptions{Seed: *seed, Days: *days, MaxTokens: *maxTokens, Progress: os.Stdout})
	default:
		var found bool
		d, found, err = demo.ShippedDataset(*scenario)
		if err == nil && !found {
			fmt.Fprintf(os.Stderr, "scenario %q has no pre-generated dataset — run with --generate (needs TACIT_LLM_API_KEY)\n", *scenario)
			return 2
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot prepare the dataset: %v\n", err)
		return exitUnreachable
	}

	// Anchor the window so it ends today, so the 7d/30d insight views are always
	// populated no matter when the demo is run. Event ids are date-independent,
	// so a same-day re-load still dedupes cleanly.
	if !*keepDates {
		start := time.Now().UTC().AddDate(0, 0, -(d.Window.Days - 1))
		d.Window.StartDate = start.Format("2006-01-02")
	}

	if *out != "" {
		if err := d.Save(*out); err != nil {
			fmt.Fprintf(os.Stderr, "cannot save the dataset copy: %v\n", err)
			return exitUnreachable
		}
		fmt.Printf("Saved a reusable copy to %s\n", *out)
	}

	fmt.Printf("Load started: %q — %s (%s) into %s\n", d.Scenario, d.Org.Name, d.Org.Industry, *registryURL)
	reg := &pkgclient.Registry{BaseURL: *registryURL, APIKey: *key}
	res, err := demo.Load(reg, d, demo.LoadOptions{Force: *force, Progress: os.Stdout})
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nload failed: %v\n", err)
		return exitUnreachable
	}

	fmt.Printf("\nDone. %d techniques (%d org · %d general · %d drafts), %d members, %d events across %d days.\n",
		res.TechniquesContributed+res.TechniquesSkipped, res.Stats.OrgTechniques, res.Stats.GeneralTechniques, res.Stats.Drafts,
		res.Stats.Members, res.EventsAccepted+res.EventsDuplicate, d.Window.Days)
	fmt.Println("\nExplore it:")
	// Links use the address the user targeted — that's guaranteed to reach the
	// instance they just loaded (and works in a browser on the same machine).
	urls := demo.ViewURLs(*registryURL)
	labels := make([]string, 0, len(urls))
	for k := range urls {
		labels = append(labels, k)
	}
	sort.Strings(labels)
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, l := range labels {
		fmt.Fprintf(tw, "  %s\t%s\n", l, urls[l])
	}
	tw.Flush()
	if ext := externalURL(reg); ext != "" && !strings.HasPrefix(*registryURL, ext) {
		fmt.Printf("\nThis registry also advertises a public dashboard at %s\n", ext)
	}
	return 0
}

// externalURL returns the registry's advertised public dashboard origin, if any.
func externalURL(reg *pkgclient.Registry) string {
	if h, err := reg.Health(); err == nil {
		if ext, ok := h["external_url"].(string); ok {
			return ext
		}
	}
	return ""
}
