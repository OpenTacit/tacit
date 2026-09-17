// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package store

// Member keys (models.MemberKey): the file-store side of access management.
// Small set, atomic full-file writes — the same posture as techniques.json.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/registry/models"
)

func (s *Store) keysPath() string { return filepath.Join(s.dir, "member_keys.json") }

// loadKeys is called from Open (keys may legitimately not exist yet).
func (s *Store) loadKeys() error {
	raw, err := os.ReadFile(s.keysPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var keys []models.MemberKey
	if err := json.Unmarshal(raw, &keys); err != nil {
		return err
	}
	for _, k := range keys {
		s.keys[k.ID] = k
	}
	return nil
}

// saveKeys writes the full key set atomically. Callers hold s.mu.
func (s *Store) saveKeys() error {
	keys := make([]models.MemberKey, 0, len(s.keys))
	for _, k := range s.keys {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	raw, err := json.MarshalIndent(keys, "", " ")
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(s.keysPath(), raw, 0o600)
}

// InsertMemberKey stores (or updates) a minted member key.
func (s *Store) InsertMemberKey(k models.MemberKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[k.ID] = k
	return s.saveKeys()
}

// MemberKeyByHash returns the key with this secret hash, or false.
func (s *Store) MemberKeyByHash(hash string) (models.MemberKey, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.keys {
		if k.Hash == hash {
			return k, true, nil
		}
	}
	return models.MemberKey{}, false, nil
}

// ListMemberKeys returns every member key, newest first, with the id as the
// tie-break — keys minted in the same second must not shuffle between calls.
func (s *Store) ListMemberKeys() ([]models.MemberKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]models.MemberKey, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// SetMemberKeyRevoked marks a key revoked; a revoked key stays listed.
func (s *Store) SetMemberKeyRevoked(id, revokedAt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return nil
	}
	k.RevokedAt = revokedAt
	s.keys[id] = k
	return s.saveKeys()
}

// TouchMemberKey records when the key last authenticated.
func (s *Store) TouchMemberKey(id, lastSeen string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return nil
	}
	k.LastSeen = lastSeen
	s.keys[id] = k
	return s.saveKeys()
}

// DeleteMemberKey removes a key outright.
func (s *Store) DeleteMemberKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[id]; !ok {
		return nil
	}
	delete(s.keys, id)
	return s.saveKeys()
}
