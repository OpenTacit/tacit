// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"log"
	"sync"

	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/internal/registry/models"
)

// The Public channel, from the server's side: when it is served, what is in it,
// and when that gets recomputed (docs/distribution/global-access-plan.md).

// publicState holds the last computed membership. It is a cache of something
// derivable from the lifecycle log, so losing it costs one recompute.
type publicState struct {
	mu      sync.Mutex
	members []federation.PublicMember
	// exclusions is why each near-miss technique is not in the channel, for the
	// Federation view. Recomputed alongside membership.
	exclusions []federation.PublicExclusion
	ranked     []federation.PublicCandidate
}

// GlobalAccessServing reports whether the commons half of Global Access is
// actually running: the switch is on AND a person has confirmed the technique-sharing
// bargain. The staged state — on but unconfirmed — is exactly the gap between
// these two, and it is why the check is not simply the switch.
func (s *Server) GlobalAccessServing() bool {
	c := s.cfg()
	return c.GlobalAccess && c.GlobalAccessConfirmed
}

// GlobalAccessStaged reports the upgrade state: reachable through the proxy,
// with the commons computed but withheld pending an admin's confirmation.
func (s *Server) GlobalAccessStaged() bool {
	c := s.cfg()
	return c.GlobalAccess && !c.GlobalAccessConfirmed
}

// accessMode is what /v1/health reports and the harness status line renders:
// whether this registry is private to its network, globally reachable, or staged
// mid-upgrade.
func (s *Server) accessMode() string {
	switch {
	case s.GlobalAccessServing():
		return "global"
	case s.GlobalAccessStaged():
		return "staged"
	default:
		return "private"
	}
}

// publicPolicy is the eligibility policy from live config, so a changed floor
// applies on the next recompute without a restart.
func (s *Server) publicPolicy() federation.PublicPolicy {
	return federation.PublicPolicy{MinN: s.cfg().PublicMinN}
}

// PublicMembers is the current membership, for the views.
func (s *Server) PublicMembers() []federation.PublicMember {
	s.public.mu.Lock()
	defer s.public.mu.Unlock()
	return append([]federation.PublicMember(nil), s.public.members...)
}

// PublicStatus is everything the Federation view needs about the channel.
func (s *Server) PublicStatus() ([]federation.PublicMember, []federation.PublicCandidate, []federation.PublicExclusion) {
	s.public.mu.Lock()
	defer s.public.mu.Unlock()
	return append([]federation.PublicMember(nil), s.public.members...),
		append([]federation.PublicCandidate(nil), s.public.ranked...),
		append([]federation.PublicExclusion(nil), s.public.exclusions...)
}

// publicMembersForFeed is what the publisher consults. It returns nil — meaning
// "this channel is not being served" — unless the commons is actually running, so
// the staged state computes and displays a channel it does not publish.
func (s *Server) publicMembersForFeed() []federation.PublicMember {
	if !s.GlobalAccessServing() {
		return nil
	}
	return s.PublicMembers()
}

// RecomputePublicChannel re-ranks the channel and records arrivals and
// departures. Called at startup (so a restart does not serve an empty channel
// until the first tick) and on every scheduler tick.
//
// It recomputes even when the commons is not being served, because the staged
// state has to be able to SHOW an operator the exact list they are being asked to
// consent to. Withholding happens at the feed, not here.
func (s *Server) RecomputePublicChannel() {
	if !s.cfg().GlobalAccess {
		s.public.mu.Lock()
		s.public.members, s.public.ranked, s.public.exclusions = nil, nil, nil
		s.public.mu.Unlock()
		return
	}
	members, delisted, err := federation.RecomputePublic(s.Store, s.publicPolicy())
	if err != nil {
		log.Printf("[public] recompute failed: %v", err) // a job failure must not kill the service
		return
	}
	// The ranking and exclusions are recomputed for display from the same inputs.
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		return
	}
	events, err := s.Store.LifecycleEvents("")
	if err != nil {
		return
	}
	ranked, exclusions := federation.RankPublic(techniques, s.overallOutcome,
		federation.ApprovedFromLifecycle(events, techniques), s.publicPolicy())

	s.public.mu.Lock()
	s.public.members, s.public.ranked, s.public.exclusions = members, ranked, exclusions
	s.public.mu.Unlock()

	if len(delisted) > 0 {
		log.Printf("[public] %d technique(s) de-listed from the Public feed", len(delisted))
	}
}

// overallOutcome is the rollup lookup the ranking consumes.
func (s *Server) overallOutcome(techniqueID string) (models.Outcome, bool) {
	got, found, err := s.Store.GetOutcome(techniqueID, "__overall__")
	if err != nil {
		return models.Outcome{}, false
	}
	return got, found
}

// publicMinNOrDefault is the floor actually in force, for display.
func (s *Server) publicMinNOrDefault() int {
	if s.cfg().PublicMinN > 0 {
		return s.cfg().PublicMinN
	}
	return federation.AttestationMinN
}
