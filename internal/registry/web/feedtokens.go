// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/internal/registry/models"
)

// The federation feed gate: `public` is open, every other channel needs a token
// (docs/distribution/global-access-plan.md).
//
// Without this, taking a public address publishes every channel a registry has.
// The feed surface is registered open by design and `/f/` is in the proxy's path
// allowlist, so an org with a hand-curated `partners` channel would serve it to
// the world the moment it switched Global Access on. That is true of the code as
// it stood and is not a consequence anyone opted into.

// feedTokenPrefix marks the secret as what it is when it turns up in a log or a
// config file someone is trying to identify.
const feedTokenPrefix = "tacit-feed-"

// rejectedFeedReads counts unauthorized reads per channel.
//
// It is the compensation for answering 404 rather than 401 (see feedAuthorized):
// the subscriber cannot tell a revoked token from a renamed channel, so the
// information has to surface on the publisher's side, where the operator can act
// on it — a peer failing to authenticate, or somebody probing for channel names.
// Counters only, no identity, no addresses.
type rejectedFeedReads struct {
	mu sync.Mutex
	n  map[string]int
}

func (r *rejectedFeedReads) note(channel string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n == nil {
		r.n = map[string]int{}
	}
	r.n[channel]++
}

func (r *rejectedFeedReads) snapshot() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.n))
	for k, v := range r.n {
		out[k] = v
	}
	return out
}

// RejectedFeedReads is the per-channel rejection count, for the Federation view.
func (s *Server) RejectedFeedReads() map[string]int { return s.feedRejects.snapshot() }

// presentedFeedSecret pulls the bearer token off a feed request. Only the
// Authorization header is accepted: a query parameter would end up in the
// ingress's operations log and in every intermediary's access log, and the
// tunnel already carries this header through untouched.
func presentedFeedSecret(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	if v, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := strings.CutPrefix(auth, "bearer "); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// feedToken resolves the presented secret to an active token, or false.
func (s *Server) feedToken(r *http.Request) (models.FeedToken, bool) {
	secret := presentedFeedSecret(r)
	if secret == "" {
		return models.FeedToken{}, false
	}
	sum := sha256.Sum256([]byte(secret))
	t, ok, err := s.Store.FeedTokenByHash(hex.EncodeToString(sum[:]))
	if err != nil || !ok || t.RevokedAt != "" {
		return models.FeedToken{}, false
	}
	s.touchFeedToken(t.ID)
	return t, true
}

// touchFeedTokenEvery bounds last-used writes, as touchKey does for member keys:
// a subscriber polling hourly is nothing, but a misconfigured one retrying in a
// loop must not turn a read surface into a write amplifier.
const touchFeedTokenEvery = time.Minute

func (s *Server) touchFeedToken(id string) {
	now := time.Now()
	s.touchMu.Lock()
	last, seen := s.touched["feed:"+id]
	if seen && now.Sub(last) < touchFeedTokenEvery {
		s.touchMu.Unlock()
		return
	}
	if s.touched == nil {
		s.touched = map[string]time.Time{}
	}
	s.touched["feed:"+id] = now
	s.touchMu.Unlock()
	_ = s.Store.TouchFeedToken(id, now.UTC().Format(time.RFC3339))
}

// feedAuthorized reports whether this request may read this channel.
//
// `public` is always open and cannot be gated: the pool depends on anonymous
// fetches, by the hub and by any subscriber, so a token requirement there would
// break the commons rather than protect anything. The org root key is also
// accepted, because an operator debugging their own feed with the key that
// already opens everything is reasonable — but a MEMBER key is not, since that
// is the credential on every member's machine and it must not double as a
// federation credential.
func (s *Server) feedAuthorized(r *http.Request, channel string) bool {
	if channel == federation.PublicChannel {
		return true
	}
	// Channels the operator has deliberately declared world-readable. Empty by
	// default: the safe posture is the one you get by doing nothing, and an org
	// that federates openly (or that federated openly BEFORE this gate existed and
	// does not want its subscribers to start 404ing) says so out loud.
	if containsString(s.cfg().OpenFeedChannels, channel) {
		return true
	}
	if presented := r.Header.Get("X-Tacit-Key"); presented != "" && presented == s.cfg().APIKey {
		return true
	}
	t, ok := s.feedToken(r)
	return ok && t.Allows(channel)
}

// feedAuthorizedForAny is the technique-content path's question. That URL carries no
// channel of its own — /f/techniques/{id}.md — so authorization comes from the technique's
// own channel set, including the computed one. Gating the feed and leaving this
// open would make the gate decorative: a feed's whole payload is reachable one
// id at a time by anyone who can guess an id.
func (s *Server) feedAuthorizedForAny(r *http.Request, channels []string) bool {
	for _, ch := range channels {
		if s.feedAuthorized(r, ch) {
			return true
		}
	}
	return false
}

// mintFeedToken creates a token and returns the secret, which is shown once here
// and never recoverable afterwards — the MemberKey posture, for the same reason.
func (s *Server) mintFeedToken(label string, channels []string) (models.FeedToken, string, error) {
	secret := feedTokenPrefix + newSecret()
	sum := sha256.Sum256([]byte(secret))
	var clean []string
	for _, ch := range channels {
		ch = strings.TrimSpace(ch)
		// Minting for the computed channel is meaningless — it is open — and
		// accepting it would suggest the commons can be gated.
		if ch == "" || ch == federation.PublicChannel {
			continue
		}
		clean = append(clean, ch)
	}
	t := models.FeedToken{
		ID:        models.Slugify(label) + "-" + hex.EncodeToString(sum[:4]),
		Label:     label,
		Hash:      hex.EncodeToString(sum[:]),
		Channels:  clean,
		CreatedAt: models.Now(),
	}
	if err := s.Store.InsertFeedToken(t); err != nil {
		return models.FeedToken{}, "", err
	}
	return t, secret, nil
}
