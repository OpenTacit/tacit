// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

// Feed tokens (models.FeedToken) — the SQL side of federation-feed access.
// A separate table from member_keys because it is a separate credential
// namespace: a feed token opens no API, and a member key opens no gated feed.

import (
	"database/sql"
	"errors"

	"github.com/opentacit/tacit/internal/registry/models"
)

const feedTokenColumns = `id, label, hash, channels, created_at, last_used, revoked_at`

// InsertFeedToken stores (or updates) a minted feed token.
func (s *Store) InsertFeedToken(t models.FeedToken) error {
	channels, err := JSON(t.Channels)
	if err != nil {
		return err
	}
	_, err = s.exec(`
		INSERT INTO feed_tokens (`+feedTokenColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			label = EXCLUDED.label, hash = EXCLUDED.hash, channels = EXCLUDED.channels,
			last_used = EXCLUDED.last_used, revoked_at = EXCLUDED.revoked_at`,
		t.ID, t.Label, t.Hash, channels, t.CreatedAt, t.LastUsed, t.RevokedAt)
	return err
}

func scanFeedToken(row Scanner) (models.FeedToken, error) {
	var t models.FeedToken
	var channels []byte
	if err := row.Scan(&t.ID, &t.Label, &t.Hash, &channels, &t.CreatedAt, &t.LastUsed, &t.RevokedAt); err != nil {
		return t, err
	}
	if err := ReadJSON(channels, &t.Channels); err != nil {
		return t, err
	}
	return t, nil
}

// FeedTokenByHash returns the token with this secret hash, or false.
func (s *Store) FeedTokenByHash(hash string) (models.FeedToken, bool, error) {
	t, err := scanFeedToken(s.queryRow(
		`SELECT `+feedTokenColumns+` FROM feed_tokens WHERE hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return models.FeedToken{}, false, nil
	}
	if err != nil {
		return models.FeedToken{}, false, err
	}
	return t, true, nil
}

// ListFeedTokens returns every feed token, newest first.
func (s *Store) ListFeedTokens() ([]models.FeedToken, error) {
	rows, err := s.query(
		`SELECT ` + feedTokenColumns + ` FROM feed_tokens ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.FeedToken
	for rows.Next() {
		t, err := scanFeedToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetFeedTokenRevoked marks a token revoked; a revoked token stays listed.
func (s *Store) SetFeedTokenRevoked(id, revokedAt string) error {
	_, err := s.exec(`UPDATE feed_tokens SET revoked_at = ? WHERE id = ?`, revokedAt, id)
	return err
}

// TouchFeedToken records when the token last read a feed.
func (s *Store) TouchFeedToken(id, lastUsed string) error {
	_, err := s.exec(`UPDATE feed_tokens SET last_used = ? WHERE id = ?`, lastUsed, id)
	return err
}
