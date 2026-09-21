// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

// Member keys (models.MemberKey) — the SQL side of access management.

import (
	"database/sql"
	"errors"

	"github.com/opentacit/tacit/internal/registry/models"
)

const memberKeyColumns = `id, label, hash, created_at, last_seen, revoked_at`

// InsertMemberKey stores (or updates) a minted member key.
func (s *Store) InsertMemberKey(k models.MemberKey) error {
	_, err := s.exec(`
		INSERT INTO member_keys (`+memberKeyColumns+`)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			label = EXCLUDED.label, hash = EXCLUDED.hash,
			last_seen = EXCLUDED.last_seen, revoked_at = EXCLUDED.revoked_at`,
		k.ID, k.Label, k.Hash, k.CreatedAt, k.LastSeen, k.RevokedAt)
	return err
}

func scanKey(row Scanner) (models.MemberKey, error) {
	var k models.MemberKey
	err := row.Scan(&k.ID, &k.Label, &k.Hash, &k.CreatedAt, &k.LastSeen, &k.RevokedAt)
	return k, err
}

// MemberKeyByHash returns the key with this secret hash, or false.
func (s *Store) MemberKeyByHash(hash string) (models.MemberKey, bool, error) {
	k, err := scanKey(s.queryRow(
		`SELECT `+memberKeyColumns+` FROM member_keys WHERE hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return models.MemberKey{}, false, nil
	}
	if err != nil {
		return models.MemberKey{}, false, err
	}
	return k, true, nil
}

// ListMemberKeys returns every member key, newest first.
func (s *Store) ListMemberKeys() ([]models.MemberKey, error) {
	rows, err := s.query(
		`SELECT ` + memberKeyColumns + ` FROM member_keys ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.MemberKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// SetMemberKeyRevoked marks a key revoked; a revoked key stays listed.
func (s *Store) SetMemberKeyRevoked(id, revokedAt string) error {
	_, err := s.exec(`UPDATE member_keys SET revoked_at = ? WHERE id = ?`, revokedAt, id)
	return err
}

// TouchMemberKey records when the key last authenticated.
func (s *Store) TouchMemberKey(id, lastSeen string) error {
	_, err := s.exec(`UPDATE member_keys SET last_seen = ? WHERE id = ?`, lastSeen, id)
	return err
}

// DeleteMemberKey removes a key outright.
func (s *Store) DeleteMemberKey(id string) error {
	_, err := s.exec(`DELETE FROM member_keys WHERE id = ?`, id)
	return err
}
