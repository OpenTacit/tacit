// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// `tacit init` reads the conventions this organization has already written
// down, and files them as drafts.
//
// The registry it sets up is otherwise empty of anything the org knows: a
// starter set of general moves, no evidence, an Outcomes page with nothing in
// it, and a Review queue that says nothing waits on you. The operator's honest
// next step was to invite colleagues and wait weeks for the loop to produce
// something. Meanwhile the org's own practice was sitting in CLAUDE.md, in the
// repository they were standing in.
//
// So this runs at the end of init, against the working directory when it is a
// checkout. Drafts only — retrieval never serves a draft — so nothing here
// reaches a colleague before somebody has read it and promoted it. That is the
// point: it turns the first session with the product from "read the manual" into
// "review your own playbook", which is the act everything else is built around.
//
// Straight to the store rather than over HTTP, because init has not started a
// server yet and in the single-member case never returns from starting one.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentacit/tacit/internal/originate"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/contribute"
	registryembed "github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/store"
)

// originateFromRepo files a draft for each convention unit found under from.
// Returns how many it filed and how many it recognised as already there.
// embedSettings is the retrieval space to write these drafts into. Passed
// rather than read from the environment because `tacit init` has just CHOSEN
// these values and written them to a file — the process it is running in has
// never seen them, which is how the whole step came to fail with "set
// TACIT_ONNX_LIB" on exactly the machines that had semantic retrieval working.
type embedSettings struct {
	model, onnxLib, modelDir string
	dim                      int
}

// embedder builds the configured embedder, putting the settings where
// registry/embed reads them from and taking them back out again. Scoped rather
// than exported process-wide: a setup command that permanently rewrites its own
// environment changes the behaviour of everything after it, for one call that
// needed two variables.
func (e embedSettings) embedder() (registryembed.Embedder, error) {
	for k, v := range map[string]string{
		"TACIT_ONNX_LIB":       e.onnxLib,
		"TACIT_ONNX_MODEL_DIR": e.modelDir,
	} {
		if v == "" || os.Getenv(k) != "" {
			continue
		}
		old := os.Getenv(k)
		_ = os.Setenv(k, v)
		defer func() { _ = os.Setenv(k, old) }()
	}
	return registryembed.New(e.model, e.dim)
}

func originateFromRepo(dataDir, from string, embed embedSettings) (filed, already int, err error) {
	sources := originate.Find(from)
	if len(sources) == 0 {
		return 0, 0, nil
	}
	var candidates []originate.Candidate
	for _, s := range sources {
		candidates = append(candidates, originate.Candidates(s)...)
	}
	candidates = originate.Cap(originate.Dedup(candidates), originate.MaxCandidates)
	if len(candidates) == 0 {
		return 0, 0, nil
	}

	st, err := store.Open(dataDir)
	if err != nil {
		return 0, 0, err
	}
	defer st.Close()
	// A drafts pass is worth more than a perfect vector. Startup re-embeds any
	// technique whose stored model differs from the serving one
	// (jobs.RunEmbed via TechniquesNeedingEmbedding), so a draft written in the
	// lexical space is corrected the moment the registry runs — whereas an
	// abandoned pass leaves the review queue empty and the operator with an
	// error about a library path.
	embedder, err := embed.embedder()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (%v — filing these with the built-in embedder; the registry re-embeds them at startup)\n", err)
		embedder, err = registryembed.New(registryconfig.DefaultEmbedModel, registryconfig.DefaultEmbedDim)
		if err != nil {
			return 0, 0, err
		}
	}

	// What is already here, by the words it carries. Re-running init in the
	// same checkout must not file a second copy of every convention, and an id
	// collision would not catch it: contribute mints slug-2 rather than
	// refusing, which is right for two members contributing similar moves and
	// wrong for the same file read twice.
	existing := map[string]bool{}
	if live, lerr := st.ListTechniques(nil, 0); lerr == nil {
		for _, t := range live {
			existing[recipeKey(t.Recipe)] = true
		}
	}

	for _, c := range candidates {
		if existing[recipeKey(c.Recipe)] {
			already++
			continue
		}
		body := map[string]any{
			"name":        c.Name,
			"description": c.Description,
			"recipe":      c.Recipe,
			// org, and not a guess: these are this organization's own written
			// conventions, which is exactly the half of a playbook no public
			// source can supply.
			"scope":      "org",
			"provenance": "observed",
			"source_url": c.Source,
			"tags":       anySlice(c.Tags),
			"task_types": anySlice(c.TaskTypes),
		}
		if _, cerr := contribute.Create(st, body, embedder); cerr != nil {
			// One malformed section is not a reason to abandon the rest. The
			// safety screen rejects here too, and a convention file that trips
			// it is a thing the operator should hear about by name.
			fmt.Fprintf(os.Stderr, "  skipped %q (%v)\n", c.Name, cerr)
			continue
		}
		existing[recipeKey(c.Recipe)] = true
		filed++
	}
	return filed, already, nil
}

// recipeKey normalises a recipe for the "have we already got this" check:
// whitespace and case only, so a reformatted file does not read as new.
func recipeKey(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func anySlice(ss []string) []any {
	out := make([]any, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

// repoToOriginate resolves the --from-repo answer to a directory, or "" for
// nothing to read.
//
// "auto" looks at the working directory and stops there. Walking up to find a
// checkout would mean `tacit init` in a home directory that happens to be
// version-controlled — which is a real and common setup — reading dotfiles as
// organizational practice.
func repoToOriginate(mode string) string {
	switch mode {
	case "off":
		return ""
	case "auto":
		cwd, err := os.Getwd()
		if err != nil || !originate.IsRepoRoot(cwd) {
			return ""
		}
		return cwd
	default:
		abs, err := filepath.Abs(mode)
		if err != nil {
			return ""
		}
		return abs
	}
}
