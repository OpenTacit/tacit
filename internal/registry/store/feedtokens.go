// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package store

// Feed tokens (models.FeedToken): the file-store side of federation-feed access.
// Same posture as member_keys.json — a small set, atomic full-file writes — and
// deliberately a separate file, because it is a separate credential namespace.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/registry/models"
)

func (s *Store) feedTokensPath() string { return filepath.Join(s.dir, "feed_tokens.json") }

// loadFeedTokens is called from Open (the file legitimately may not exist).
func (s *Store) loadFeedTokens() error {
	raw, err := os.ReadFile(s.feedTokensPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var tokens []models.FeedToken
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return err
	}
	for _, t := range tokens {
		s.feedTokens[t.ID] = t
	}
	return nil
}

// saveFeedTokens writes the full set atomically. Callers hold s.mu.
func (s *Store) saveFeedTokens() error {
	tokens := make([]models.FeedToken, 0, len(s.feedTokens))
	for _, t := range s.feedTokens {
		tokens = append(tokens, t)
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].ID < tokens[j].ID })
	raw, err := json.MarshalIndent(tokens, "", " ")
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(s.feedTokensPath(), raw, 0o600)
}

// InsertFeedToken stores (or updates) a minted feed token.
func (s *Store) InsertFeedToken(t models.FeedToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feedTokens[t.ID] = t
	return s.saveFeedTokens()
}

// FeedTokenByHash returns the token with this secret hash, or false.
func (s *Store) FeedTokenByHash(hash string) (models.FeedToken, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.feedTokens {
		if t.Hash == hash {
			return t, true, nil
		}
	}
	return models.FeedToken{}, false, nil
}

// ListFeedTokens returns every feed token, newest first, with the id as the
// tie-break — the same rule ListMemberKeys follows.
func (s *Store) ListFeedTokens() ([]models.FeedToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]models.FeedToken, 0, len(s.feedTokens))
	for _, t := range s.feedTokens {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// SetFeedTokenRevoked marks a token revoked; a revoked token stays listed.
func (s *Store) SetFeedTokenRevoked(id, revokedAt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.feedTokens[id]
	if !ok {
		return nil
	}
	t.RevokedAt = revokedAt
	s.feedTokens[id] = t
	return s.saveFeedTokens()
}

// TouchFeedToken records when the token last read a feed.
func (s *Store) TouchFeedToken(id, lastUsed string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.feedTokens[id]
	if !ok {
		return nil
	}
	t.LastUsed = lastUsed
	s.feedTokens[id] = t
	return s.saveFeedTokens()
}
