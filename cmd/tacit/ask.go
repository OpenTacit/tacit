// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit ask — one technique for the task in hand.
//
// This is what first use is FOR (docs/distribution/first-adoption-plan.md).
// Everything else the product asks of a new member — a running registry, a
// wired tool, a member key, a health check that goes green — is preparation
// for this moment, and until this moment none of it has given them anything.
// The old end of `tacit connect` was "test end to end with /tacit:test", which
// proves the pipe carries a message and says nothing about whether the message
// is worth having.
//
// It answers with ONE technique, not a ranked page. A member who asked what to
// do about the thing in front of them can act on one answer; ten answers are a
// research task, which is the thing they were already doing. The rest stay one
// command away.
//
// No new retrieval. This is the same /v1/evidence call the MCP pull path makes
// (internal/auditor/mcp), rendered for a terminal, so a technique cannot be
// described one way when the model asks and another way when the member does.

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/auditor/audit"
	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	auditcontracts "github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/llm"
	pkgclient "github.com/opentacit/tacit/pkg/client"
	"github.com/opentacit/tacit/pkg/contracts"
)

func cmdAsk(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	registryURL := fs.String("registry", cfg.RegistryURL, "registry URL")
	key := fs.String("key", cfg.RegistryKey, "member API key")
	rest, ok := parseFlags(fs, args)
	if !ok {
		return exitUsage
	}

	task := strings.TrimSpace(strings.Join(rest, " "))
	if task == "" {
		task = promptForTask()
	}
	if task == "" {
		fmt.Fprintf(os.Stderr, "say what you are working on:  %s ask \"flaky integration tests\"\n", selfCommand())
		return exitUsage
	}
	return askOnce(os.Stdout, *registryURL, *key, cfg.HooksSegment, task)
}

// promptForTask asks only where there is somebody to ask. A hook or a script
// has the task in hand already and passes it as an argument.
func promptForTask() string {
	if !stdinIsTerminal() {
		return ""
	}
	fmt.Print("what are you working on? ")
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return ""
	}
	return strings.TrimSpace(sc.Text())
}

// askOnce runs the search and prints at most one technique.
//
// Returns 0 for a match AND for an honest no-match: a playbook with nothing to
// say about this task is a correct answer, and a member's shell should not
// treat it as a failure. Only an unreachable registry is an error.
func askOnce(w io.Writer, registryURL, key, segment, task string) int {
	return askWith(w, auditorconfig.Load(), registryURL, key, segment, task)
}

// askWith is askOnce with the config handed in, so a test can run it without
// the machine's own settings deciding whether a fit-check happens.
func askWith(w io.Writer, cfg auditorconfig.Config, registryURL, key, segment, task string) int {
	if registryURL == "" || key == "" {
		fmt.Fprintf(os.Stderr, "this machine is not connected to a registry yet:  %s connect --registry <url> --code <code>\n",
			selfCommand())
		return exitUnreachable
	}
	reg := &pkgclient.Registry{BaseURL: registryURL, APIKey: key,
		HTTP: &http.Client{Timeout: 20 * time.Second}}
	block, err := reg.GetEvidence(contracts.Characterization{
		AuditID:     audit.NewAuditID(),
		SummaryText: task,
		// The same surface the MCP pull path uses. This is an ask, and the fact
		// log has to be able to tell an ask from something OpenTacit volunteered.
		Surface: "cli",
		Segment: contracts.Segment(auditorconfig.ParseSegment(segment)),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach the playbook at %s: %v\n", registryURL, err)
		return exitUnreachable
	}
	if len(block.Candidates) == 0 {
		reportNoMatch(w, task)
		return 0
	}
	// Retrieval does NOT decide fit. The registry's relevance floor is a
	// deliberate no-op (config.MinSimilarity == 0, retrieval.go), so a task
	// matching nothing still comes back with a full block and the fit-check
	// "absorbs the whole cost of saying no". Printing candidates[0] as though
	// it answered the question is therefore how you tell somebody asking about
	// medieval falconry to ground their answer in the internal docs, at a
	// helped rate of 87%.
	fit, judged := fitCheck(cfg, task, block)
	switch {
	case judged && fit < 0:
		reportNoMatch(w, task)
		return 0
	case judged:
		printCandidate(w, block.Candidates[fit])
	default:
		// No judge available. Then these are search results, and calling them
		// anything else would be the lie this whole command exists to avoid.
		fmt.Fprintf(w, "\n  Closest in your playbook. Nothing has checked these against your task —\n")
		fmt.Fprintf(w, "  that needs a model key (%s), or ask in your AI tool, where your agent reads them.\n", cfg.LLMKeyFile)
		printCandidate(w, block.Candidates[0])
	}
	if n := len(block.Candidates) - 1; n > 0 {
		fmt.Fprintf(w, "\n%d more retrieved. Ask in your AI tool for the rest: /tacit:search\n", n)
	}
	return 0
}

// fitCheck asks the model which retrieved technique actually applies here, and
// returns its index — or -1 for none of them. judged is false when there is no
// model to ask, which is a different answer from "nothing fits" and must not be
// rendered as one.
//
// It is the SAME judgment the ambient path makes (hooks/pipeline.go): one
// synthesis per candidate against a block scoped to that candidate, with an
// unusable answer meaning "not this one". A second standard of fit here would
// mean a technique could be offered in the terminal that the hook path would
// have declined to show.
func fitCheck(cfg auditorconfig.Config, task string, block contracts.EvidenceBlock) (int, bool) {
	key := llm.ResolveKey(cfg.LLMKeyFile)
	if key == "" {
		return -1, false
	}
	model, err := llm.New(llmConfig(cfg, key))
	if err != nil {
		return -1, false
	}
	return chooseFit(model, task, block)
}

// synthesizer is the judging half of the LLM client, named here so a test can
// stand in for it without a provider.
type synthesizer interface {
	Synthesize(transcript string, evidence auditcontracts.EvidenceBlock, brief bool) (string, error)
}

// chooseFit walks the retrieved techniques and returns the first the model
// says applies, or -1 for none of them.
func chooseFit(model synthesizer, task string, block contracts.EvidenceBlock) (int, bool) {
	for i, c := range block.Candidates {
		text, err := model.Synthesize(task, auditcontracts.EvidenceBlock{
			Candidates: []auditcontracts.EvidenceCandidate{auditCandidate(c)},
		}, true)
		if err != nil {
			// A broken model is not a verdict. Say we could not judge rather
			// than reporting every technique as a non-fit.
			return -1, false
		}
		if usableSuggestion(text) {
			return i, true
		}
	}
	return -1, true
}

// auditCandidate carries one candidate across from the wire type to the one
// the synthesis prompt reads. The two shapes differ only in how loosely they
// type outcomes and freshness, and the prompt wants the loose form.
func auditCandidate(c contracts.EvidenceCandidate) auditcontracts.EvidenceCandidate {
	out := auditcontracts.EvidenceCandidate{
		TechniqueID: c.TechniqueID,
		Name:        c.Name,
		Scope:       c.Scope,
		Recipe:      c.Recipe,
		AppliesWhen: c.AppliesWhen,
		NotWhen:     c.NotWhen,
		Support:     c.Support,
		Outcomes:    outcomeMap(c.Outcomes),
	}
	return out
}

// usableSuggestion is the push path's own test for a real answer: the model
// says NONE, or nothing, when the technique does not apply.
func usableSuggestion(text string) bool {
	t := strings.TrimSpace(text)
	return t != "" && !strings.EqualFold(t, "NONE") && !strings.HasPrefix(strings.ToUpper(t), "NONE")
}

// printCandidate is one technique as a member reads it: what to do, when it
// applies, and what actually happened when colleagues used it.
//
// The evidence line comes from contracts.EvidenceLine, shared with the ambient
// block and the MCP tool, so the same technique carries the same claim wherever
// it appears — and says "too few outcomes to rate yet" rather than inventing a
// percentage out of two data points.
func printCandidate(w io.Writer, c contracts.EvidenceCandidate) {
	scope := c.Scope
	if scope == "" {
		scope = "general"
	}
	fmt.Fprintf(w, "\n  %s\n", c.Name)
	fmt.Fprintf(w, "  %s · %s\n", c.TechniqueID, scope)
	if ev := auditcontracts.EvidenceLine(outcomeMap(c.Outcomes)); ev != "" {
		fmt.Fprintf(w, "  %s\n", ev)
	} else {
		fmt.Fprintf(w, "  no measured outcomes yet — new, or rarely tried\n")
	}
	for _, f := range []struct{ label, text string }{
		{"when", c.AppliesWhen},
		{"not when", c.NotWhen},
		{"do", c.Recipe},
	} {
		if t := strings.TrimSpace(f.text); t != "" {
			fmt.Fprintf(w, "\n  %s: %s\n", f.label, t)
		}
	}
}

// outcomeMap puts the typed outcome summary into the loose shape the shared
// evidence renderer reads.
//
// An adapter rather than a second renderer, and the difference matters: the
// rules about when a sample is too small to quote, and how a rate is worded,
// exist once (contracts.EvidenceLine). A copy here would drift, and the copy
// that drifts is the one a member reads.
func outcomeMap(o *contracts.OutcomeSummary) map[string]any {
	if o == nil {
		return nil
	}
	m := map[string]any{"sample_size": float64(o.SampleSize)}
	if o.HelpedRate != nil {
		m["helped_rate"] = *o.HelpedRate
	}
	if o.AdoptionRate != nil {
		m["adoption_rate"] = *o.AdoptionRate
	}
	if o.Segment != "" {
		m["segment"] = o.Segment
	}
	return m
}

// reportNoMatch says no, and means it.
//
// The one answer this command must never give is a weak suggestion dressed as
// a fit, because the whole claim of the product is that what it offers was
// measured. A playbook with nothing to say about this task says so.
func reportNoMatch(w io.Writer, task string) {
	fmt.Fprintf(w, "\n  Nothing in your playbook matches %q yet.\n", task)
	fmt.Fprintf(w, "\n  That is an answer, not a fault: the playbook only holds what your\n")
	fmt.Fprintf(w, "  colleagues have actually tried and measured.\n")
	fmt.Fprintf(w, "\n  Try naming the tool or the failure rather than the goal, or add what\n")
	fmt.Fprintf(w, "  you know: /tacit:contribute in your AI tool.\n")
}
