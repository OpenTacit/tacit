// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package tagmerge

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fake struct {
	reply  string
	err    error
	prompt string
}

func (f *fake) Complete(prompt string, _ int) (string, error) {
	f.prompt = prompt
	return f.reply, f.err
}

func vocab() []Entry {
	return []Entry{
		{Tag: "setup", Count: 5, Examples: []string{"Wire the MCP servers"}},
		{Tag: "agent-setup", Count: 2, Examples: []string{"Design the agent-computer interface"}},
		{Tag: "audit", Count: 5, Examples: []string{"Ask for evidence of success"}},
		{Tag: "review", Count: 7, Examples: []string{"Close the loop"}},
	}
}

func TestProposeParsesAndKeepsValidMerges(t *testing.T) {
	m := &fake{reply: `Here you go:
	[{"from": ["agent-setup"], "into": "setup", "why": "both name configuring the agent"}]`}
	got, err := Propose(m, vocab())
	if err != nil {
		t.Fatal(err)
	}
	want := []Proposal{{From: []string{"agent-setup"}, Into: "setup", Why: "both name configuring the agent"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	// The vocabulary — with counts and example technique names — must reach the
	// model: "context" on a caching technique and "context" on a memory technique are the
	// same word doing two jobs, and only the names reveal it.
	for _, want := range []string{"agent-setup · 2 · Design the agent-computer interface", "setup · 5"} {
		if !strings.Contains(m.prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

// The validation gate is the whole safety story: a proposal that cannot be
// applied coherently is noise in a review queue, not a decision. The reviewer
// must never be shown any of these.
func TestValidateRejectsIncoherentProposals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
	}{
		{"invents a tag as the target — a merge may not GROW the vocabulary",
			`[{"from": ["setup"], "into": "configuration"}]`},
		{"merges a tag that does not exist",
			`[{"from": ["nonexistent"], "into": "setup"}]`},
		{"merges a tag into itself",
			`[{"from": ["setup"], "into": "setup"}]`},
		{"builds a chain — the result would depend on which merge ran first",
			`[{"from": ["agent-setup"], "into": "setup"}, {"from": ["setup"], "into": "audit"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Propose(&fake{reply: tc.reply}, vocab())
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range got {
				for _, f := range p.From {
					if f == "setup" && p.Into == "audit" {
						t.Fatalf("a chain survived validation: %+v", got)
					}
				}
			}
			if tc.name != "builds a chain — the result would depend on which merge ran first" && len(got) != 0 {
				t.Fatalf("kept an incoherent proposal: %+v", got)
			}
		})
	}
}

// The same tag cannot be claimed by two proposals — whichever ran second would
// find nothing to merge, so the reviewer would be approving a no-op.
func TestValidateClaimsEachTagOnce(t *testing.T) {
	got, err := Propose(&fake{reply: `[
		{"from": ["agent-setup"], "into": "setup"},
		{"from": ["agent-setup"], "into": "audit"}]`}, vocab())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Into != "setup" {
		t.Fatalf("a tag was claimed twice: %+v", got)
	}
}

// The model's tags are normalized on the same rules as any ingested tag, so a
// model that answers "Agent Setup" still resolves to the real tag.
func TestValidateNormalizesTheModelsTags(t *testing.T) {
	got, err := Propose(&fake{reply: `[{"from": ["Agent Setup"], "into": " SETUP "}]`}, vocab())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Into != "setup" || got[0].From[0] != "agent-setup" {
		t.Fatalf("model tags were not normalized: %+v", got)
	}
}

// "Nothing to merge" is a valid — and often correct — answer, and must not read
// as a failure.
func TestProposeAcceptsAnEmptyAnswer(t *testing.T) {
	got, err := Propose(&fake{reply: `[]`}, vocab())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v err=%v, want no proposals and no error", got, err)
	}
}

func TestProposeSurfacesModelFailure(t *testing.T) {
	if _, err := Propose(&fake{err: errors.New("boom")}, vocab()); err == nil {
		t.Fatal("a model failure must not be reported as 'no merges found'")
	}
	if _, err := Propose(&fake{reply: "I could not do that."}, vocab()); err == nil {
		t.Fatal("an unparseable answer must not be reported as 'no merges found'")
	}
}

// Nothing can merge with nothing — and a one-tag vocabulary must not burn an
// API call to discover that.
func TestProposeSkipsATrivialVocabulary(t *testing.T) {
	m := &fake{reply: `[{"from": ["x"], "into": "y"}]`}
	got, err := Propose(m, []Entry{{Tag: "setup", Count: 1}})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if m.prompt != "" {
		t.Fatal("called the model for a vocabulary that cannot possibly merge")
	}
}
