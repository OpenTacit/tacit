// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	auditcontracts "github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/pkg/contracts"
)

// evidenceServer answers /v1/evidence with the candidates given.
func evidenceServer(t *testing.T, candidates []contracts.EvidenceCandidate) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/evidence" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(contracts.EvidenceBlock{Candidates: candidates})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func rate(f float64) *float64 { return &f }

// unjudged is a config with no model key, so these tests exercise the path
// where nothing has checked the techniques against the task — deterministically,
// and without reading the developer's own settings or reaching a provider.
func unjudged(t *testing.T) auditorconfig.Config {
	t.Helper()
	return auditorconfig.Config{LLMKeyFile: filepath.Join(t.TempDir(), "no-key.env")}
}

// One technique, with what actually happened when colleagues used it — the
// point of the whole product, and the thing first use never reached.
func TestAskAnswersWithOneTechniqueAndItsEvidence(t *testing.T) {
	ts := evidenceServer(t, []contracts.EvidenceCandidate{
		{
			TechniqueID: "tq-1", Name: "Quarantine the flake first", Scope: "team=payments",
			AppliesWhen: "a test fails intermittently and blocks the queue",
			NotWhen:     "the failure is deterministic",
			Recipe:      "Move it out of the gating suite, then debug it off the critical path.",
			Outcomes:    &contracts.OutcomeSummary{HelpedRate: rate(0.78), AdoptionRate: rate(0.61), SampleSize: 23},
		},
		{TechniqueID: "tq-2", Name: "Second best"},
	})

	var b strings.Builder
	if rc := askWith(&b, unjudged(t), ts.URL, "member-key", "", "flaky integration tests"); rc != 0 {
		t.Fatalf("exit %d on a successful ask", rc)
	}
	got := b.String()
	for _, want := range []string{
		"Quarantine the flake first",
		"tq-1", "team=payments",
		"helped 78%", "adopted 61%", "n=23",
		"when: a test fails intermittently",
		"not when: the failure is deterministic",
		"do: Move it out of the gating suite",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the answer is missing %q:\n%s", want, got)
		}
	}
	// One answer, not a ranked page — the rest are named as a count only.
	if strings.Contains(got, "Second best") {
		t.Errorf("a second technique was printed:\n%s", got)
	}
	if !strings.Contains(got, "1 more retrieved") {
		t.Errorf("the answer does not say how many others were retrieved:\n%s", got)
	}
}

// A rate below the evidence floor must not be quoted as a rate. The renderer
// is shared with the ambient block and the MCP tool for exactly this reason.
func TestAskWillNotQuoteARateItCannotSupport(t *testing.T) {
	ts := evidenceServer(t, []contracts.EvidenceCandidate{{
		TechniqueID: "tq-9", Name: "Barely tried",
		Outcomes: &contracts.OutcomeSummary{HelpedRate: rate(1.0), SampleSize: 2},
	}})
	var b strings.Builder
	askWith(&b, unjudged(t), ts.URL, "member-key", "", "anything")
	got := b.String()
	if strings.Contains(got, "100%") {
		t.Errorf("quoted a helped rate off two outcomes:\n%s", got)
	}
	if !strings.Contains(got, "too few outcomes to rate yet") {
		t.Errorf("did not say the sample is too small:\n%s", got)
	}
}

// A technique nobody has measured says so, rather than showing a zero.
func TestAskSaysWhenNothingIsMeasuredYet(t *testing.T) {
	ts := evidenceServer(t, []contracts.EvidenceCandidate{{TechniqueID: "tq-3", Name: "Brand new"}})
	var b strings.Builder
	askWith(&b, unjudged(t), ts.URL, "member-key", "", "anything")
	got := b.String()
	if !strings.Contains(got, "no measured outcomes yet") {
		t.Errorf("an unmeasured technique does not say so:\n%s", got)
	}
	if strings.Contains(got, "0%") || strings.Contains(got, "n=0") {
		t.Errorf("invented a figure for an unmeasured technique:\n%s", got)
	}
}

// No match is an answer, not a failure — and never a weak suggestion dressed
// as a fit, because the product's whole claim is that what it offers was
// measured.
func TestAskSaysNoRatherThanReachingForSomething(t *testing.T) {
	ts := evidenceServer(t, nil)
	var b strings.Builder
	if rc := askWith(&b, unjudged(t), ts.URL, "member-key", "", "quantum tunnelling"); rc != 0 {
		t.Errorf("exit %d on an honest no-match; a playbook with nothing to say has not failed", rc)
	}
	got := b.String()
	if !strings.Contains(got, "Nothing in your playbook matches") {
		t.Errorf("the no-match answer does not say so plainly:\n%s", got)
	}
	if !strings.Contains(got, "quantum tunnelling") {
		t.Errorf("the no-match answer does not repeat what was asked:\n%s", got)
	}
}

// An unreachable registry is the one real failure, and must not read like a
// playbook that had nothing to offer.
func TestAskDistinguishesUnreachableFromNoMatch(t *testing.T) {
	var b strings.Builder
	if rc := askWith(&b, unjudged(t), "http://127.0.0.1:1", "member-key", "", "anything"); rc != exitUnreachable {
		t.Errorf("exit %d for an unreachable registry, want %d", rc, exitUnreachable)
	}
	if strings.Contains(b.String(), "Nothing in your playbook") {
		t.Error("an unreachable registry was reported as no match")
	}
	// And a machine that was never connected says that instead of dialling.
	var c strings.Builder
	if rc := askWith(&c, unjudged(t), "", "", "", "anything"); rc != exitUnreachable {
		t.Errorf("exit %d with no registry configured, want %d", rc, exitUnreachable)
	}
}

// stubJudge answers as the model would: a technique whose name is in `fits`
// applies, everything else comes back NONE.
type stubJudge struct {
	fits map[string]bool
	err  error
}

func (j stubJudge) Synthesize(_ string, ev auditcontracts.EvidenceBlock, _ bool) (string, error) {
	if j.err != nil {
		return "", j.err
	}
	if len(ev.Candidates) == 1 && j.fits[ev.Candidates[0].Name] {
		return "Try this: it applies here.", nil
	}
	return "NONE", nil
}

// THE BUG THIS EXISTS FOR. The registry's relevance floor is a deliberate
// no-op (config.MinSimilarity == 0), so a task matching nothing still comes
// back with a full block — asking about medieval falconry returned "Ground the
// answer in Nexus internal docs", at a helped rate of 87%. Retrieval does not
// decide fit; the fit-check does, and without one there is no answer to give.
func TestFitCheckDecidesWhatApplies(t *testing.T) {
	block := contracts.EvidenceBlock{Candidates: []contracts.EvidenceCandidate{
		{TechniqueID: "tq-1", Name: "Ground the answer in internal docs"},
		{TechniqueID: "tq-2", Name: "Quarantine the flake first"},
	}}

	// The model picks the one that applies, not the one retrieval ranked first.
	i, judged := chooseFit(stubJudge{fits: map[string]bool{"Quarantine the flake first": true}}, "flaky tests", block)
	if !judged || i != 1 {
		t.Errorf("chose index %d (judged=%v), want the technique the model accepted", i, judged)
	}

	// Nothing applies: a verdict of none, which the caller renders as no match.
	if i, judged := chooseFit(stubJudge{}, "medieval falconry", block); !judged || i != -1 {
		t.Errorf("chose index %d (judged=%v), want a judged non-fit", i, judged)
	}

	// A broken model is NOT a verdict of none. Reporting "nothing fits" when
	// the judge never answered would be the same lie in the other direction.
	if _, judged := chooseFit(stubJudge{err: errors.New("provider down")}, "anything", block); judged {
		t.Error("a failed model call was reported as a judgment")
	}
}

// Without a judge the command must not claim these fit the task.
func TestAskLabelsUnjudgedResultsAsSuch(t *testing.T) {
	ts := evidenceServer(t, []contracts.EvidenceCandidate{{TechniqueID: "tq-1", Name: "Something"}})
	var b strings.Builder
	askWith(&b, unjudged(t), ts.URL, "member-key", "", "medieval falconry")
	got := b.String()
	if !strings.Contains(got, "Nothing has checked these against your task") {
		t.Errorf("unjudged results are not labelled as unjudged:\n%s", got)
	}
}
