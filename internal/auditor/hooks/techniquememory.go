// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Member-local technique memory: which techniques THIS member has adopted or dismissed,
// remembered across sessions, on the member's own machine and nowhere else.
//
// It exists because two product promises collided. "Cohorts, never identities"
// forbids the registry from remembering who adopted what — so, server-side,
// every session starts amnesiac, and a technique the member dismissed as
// already-known last week is eligible again today. But the trust budget
// (docs/delivery/low-intrusion-plan.md) says repetition of rejected advice is exactly
// how an unsolicited coaching layer gets its hooks disabled. And the
// personalization pillar — "you use 3 of 8 techniques common on your team"
// (docs/concepts/concepts.md) — needs to know what the member uses, which is per-member
// state by definition, and "cohorts, never identities" forbids holding it
// server-side. Nothing else can populate used_technique_ids.
//
// The resolution is locality. This file lives in the member's home directory,
// is written only by their own hook agent, and never leaves the machine. What
// crosses to the registry is exactly one thing: the list of technique IDS the
// member uses, on the characterization — technique names, not an identity, going to
// an endpoint that already deliberately carries no member identifier.
package hooks

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
)

// techniqueMemoryTTL bounds how long a verdict suppresses or personalizes. Techniques
// evolve — recipes get revised, models absorb techniques — so "I already
// know this" should not silence a technique forever, and "I use this" should not
// claim currency it no longer has.
const techniqueMemoryTTL = 30 * 24 * time.Hour

type techniqueMemory struct {
	mu   sync.Mutex
	path string // "" -> in-memory only (tests, or no home dir)
	m    techniqueMemoryFile
}

type techniqueMemoryFile struct {
	Adopted   map[string]time.Time `json:"adopted"`
	Dismissed map[string]time.Time `json:"dismissed"`
	// Nudged records repo-marker invitations already shown (keyed by registry
	// URL), so an unconnected member sees each org's nudge exactly once, ever
	// — no TTL: a declined invitation must not renew itself
	// (docs/distribution/growth-plan.md mechanism 2).
	Nudged map[string]time.Time `json:"nudged,omitempty"`
	// SegmentAsked records the most recent inline cohort prompt (keyed by
	// registry URL). Unlike Nudged, this one RENEWS: the cohort matters
	// enough to keep asking until it's set, but politely — the agent re-asks
	// at most once a day (segmentAskInterval), never per session or per turn.
	SegmentAsked map[string]time.Time `json:"segment_asked,omitempty"`
	// SegmentDeclined records a member who said no (keyed by registry URL).
	// Like Nudged and unlike SegmentAsked, it does NOT renew: the daily ask
	// had no way to end, so a member who did not want a cohort was asked every
	// day for as long as they used the product. Optional metadata has to be
	// optional, and a question you cannot answer with "no" is not a question.
	SegmentDeclined map[string]time.Time `json:"segment_declined,omitempty"`
}

// loadTechniqueMemory reads the member's memory, tolerating absence and corruption
// alike — a broken memory file must degrade to amnesia, never to a broken
// agent.
func loadTechniqueMemory(path string) *techniqueMemory {
	cm := &techniqueMemory{path: path, m: techniqueMemoryFile{
		Adopted: map[string]time.Time{}, Dismissed: map[string]time.Time{},
	}}
	if path == "" {
		return cm
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cm
	}
	var m techniqueMemoryFile
	if json.Unmarshal(raw, &m) == nil {
		if m.Adopted == nil {
			m.Adopted = map[string]time.Time{}
		}
		if m.Dismissed == nil {
			m.Dismissed = map[string]time.Time{}
		}
		cm.m = m
	}
	return cm
}

// Nudged reports whether this member has already seen the join nudge for a
// registry; NoteNudged records it. Once means once.
func (c *techniqueMemory) Nudged(registryURL string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.m.Nudged[registryURL]
	return ok
}

// NoteNudged records that the join nudge was shown for a registry.
func (c *techniqueMemory) NoteNudged(registryURL string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m.Nudged == nil {
		c.m.Nudged = map[string]time.Time{}
	}
	c.m.Nudged[registryURL] = now
	c.saveLocked()
}

// LastSegmentAsk reports when the inline cohort prompt was last shown for
// this registry; NoteSegmentAsked records a showing. The re-ask cadence
// (daily) is the agent's decision — memory just keeps the clock.
func (c *techniqueMemory) LastSegmentAsk(registryURL string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.m.SegmentAsked[registryURL]
	return at, ok
}

// DeclineSegment records, permanently and machine-locally, that this member
// does not want to be asked for a cohort against this registry.
//
// Exported because the answer arrives from a command (`tacit cohorts --skip`)
// rather than from inside a hook: the agent offers the ask, the member tells
// the agent to skip it, and the agent runs this. Nothing about the decision
// reaches the registry — declining to be labelled is not itself a label.
func DeclineSegment(memoryPath, registryURL string) error {
	if memoryPath == "" || registryURL == "" {
		return errors.New("need a memory path and a registry URL")
	}
	loadTechniqueMemory(memoryPath).NoteSegmentDeclined(registryURL, time.Now())
	return nil
}

// SegmentDeclined reports whether this member has said no to the cohort ask
// for this registry.
func (c *techniqueMemory) SegmentDeclined(registryURL string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.m.SegmentDeclined[registryURL]
	return ok
}

// NoteSegmentDeclined records the no, permanently.
func (c *techniqueMemory) NoteSegmentDeclined(registryURL string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m.SegmentDeclined == nil {
		c.m.SegmentDeclined = map[string]time.Time{}
	}
	c.m.SegmentDeclined[registryURL] = now
	c.saveLocked()
}

// MetAnything reports whether this member has ever adopted or dismissed a
// technique — the cheapest honest signal that they have actually met the
// product rather than merely finished installing it.
func (c *techniqueMemory) MetAnything() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m.Adopted) > 0 || len(c.m.Dismissed) > 0
}

// NoteSegmentAsked records a showing of the inline cohort prompt.
func (c *techniqueMemory) NoteSegmentAsked(registryURL string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m.SegmentAsked == nil {
		c.m.SegmentAsked = map[string]time.Time{}
	}
	c.m.SegmentAsked[registryURL] = now
	c.saveLocked()
}

// NoteAdopted remembers that the member adopted a technique.
func (c *techniqueMemory) NoteAdopted(capID string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m.Adopted[capID] = now
	c.saveLocked()
}

// NoteDismissed remembers a deliberate rejection.
func (c *techniqueMemory) NoteDismissed(capID string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m.Dismissed[capID] = now
	c.saveLocked()
}

// Suppressed reports whether a technique should not be re-suggested to this member:
// they rejected it, or they demonstrably already use it — coaching either is
// repetition, and repetition is how the trust budget dies.
func (c *techniqueMemory) Suppressed(capID string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if at, ok := c.m.Dismissed[capID]; ok && now.Sub(at) < techniqueMemoryTTL {
		return true
	}
	if at, ok := c.m.Adopted[capID]; ok && now.Sub(at) < techniqueMemoryTTL {
		return true
	}
	return false
}

// UsedIDs returns the techniques this member has adopted within the TTL, sorted —
// the input to "you use N of M techniques common on your team".
func (c *techniqueMemory) UsedIDs(now time.Time) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for id, at := range c.m.Adopted {
		if now.Sub(at) < techniqueMemoryTTL {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// saveLocked persists best-effort. A write failure costs cross-session memory,
// never a turn — but it says so, because the symptom otherwise is advice the
// member dismissed last week coming back, with nothing to point at. Caller
// holds c.mu.
func (c *techniqueMemory) saveLocked() {
	if c.path == "" {
		return
	}
	raw, err := json.Marshal(c.m)
	if err != nil {
		log.Printf("[tacit-hooks] technique memory not encoded: %v", err)
		return
	}
	if err := fsx.WriteFileAtomic(c.path, raw, 0o600); err != nil {
		log.Printf("[tacit-hooks] technique memory not saved to %s: %v", c.path, err)
	}
}
