// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package merge

// What happens to a registry after it has been merged.
//
// Not much to its files at the moment it happens: the merge stops the process
// and hands back the address, and deliberately deletes nothing — the member's
// techniques and their whole event log stay on the disk they have always been
// on.
//
// What ends is the INSTANCE. A registry that started again from the files a
// merged one left behind would be the merged one: the same data, the same
// owner secret, and a ledger still reporting "contributed to your
// organization" about a registry that no longer exists. So a finished merge
// marks the ledger retired and `tacit serve` refuses to start a retired
// registry. The elimination is that refusal, not a rename.
//
// The configuration directory survives it. There is one profile per machine
// (docs/design/registry-first-personal-tier.md) and it holds this machine's
// identity: the settings, the ledger that records where the work went, and the
// agent settings the merge has just pointed at the organization. Nothing in it
// can re-claim the hostname, because the instance key is already gone —
// deleted by the release that handed the name back (release.go).
//
// Starting over is therefore a deliberate act, `tacit init --start-over`, and
// that is the one moment the old data would be reused. So that is where the
// sweep happens: when nothing is serving and no file is open, rather than
// inside the process that is about to exit.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MarkRetired records that this instance is finished: contributed, stopped, and
// its address handed back.
//
// It is written last, after the address is released, so that a release which
// failed leaves the profile intact and `tacit merge --yes` can retry it. A
// profile swept aside before its hostname was released would take the instance
// key with it.
func (l *Ledger) MarkRetired() error {
	l.RetiredAt = time.Now().UTC().Format(time.RFC3339)
	return l.Save()
}

// IsRetired reports a profile whose registry has been merged and retired.
func IsRetired(configDir string) bool {
	l, err := OpenLedger(LedgerPath(filepath.Join(configDir, "registry.env")))
	return err == nil && l.RetiredAt != ""
}

// SweepMerged moves a merged registry's data aside so that what follows is a
// new instance rather than the old one wearing its clothes, and retires the
// ledger that said it was finished. It returns the paths it moved things to,
// and does nothing at all to a registry that has not been merged.
//
// The configuration directory is never touched: it is the machine's, not the
// instance's. Sweeping it would take the settings, the API key and the record
// of the merge itself, which is the one thing that can still answer "where did
// my playbook go?".
//
// Moved, never deleted. The member's own measurements are in there, and the
// archive a merge writes is a summary of them; this is the evidence itself, and
// nothing about retiring an instance is a reason to destroy it.
//
// techniquesDir is moved only when it lives under dataHome. A registry can be
// pointed at a shared techniques directory — the repo's own, on a developer's
// machine — and renaming that would take something else's files with it.
func SweepMerged(configDir, dataDir, techniquesDir, dataHome string) (moved []string, err error) {
	if !IsRetired(configDir) {
		return nil, nil
	}
	suffix := ".merged-" + time.Now().UTC().Format("20060102-150405")
	if dataDir != "" {
		to, err := renameAside(dataDir, suffix)
		if err != nil {
			return moved, err
		}
		if to != "" {
			moved = append(moved, to)
		}
	}
	if techniquesDir != "" && dataHome != "" && under(techniquesDir, dataHome) {
		to, err := renameAside(techniquesDir, suffix)
		if err != nil {
			return moved, err
		}
		if to != "" {
			moved = append(moved, to)
		}
	}
	// The ledger last, because until the data is out of the way it has to keep
	// saying this registry is finished: a run interrupted halfway leaves a
	// registry that still refuses to start, which is the safe end of it.
	// Renamed rather than deleted — it is the record of where the work went.
	old := LedgerPath(filepath.Join(configDir, "registry.env"))
	to := filepath.Join(configDir, "merged-"+time.Now().UTC().Format("20060102-150405")+".json")
	if _, statErr := os.Stat(old); statErr == nil {
		if err := os.Rename(old, to); err != nil {
			return moved, fmt.Errorf("move %s aside: %w", old, err)
		}
		moved = append(moved, to)
	}
	return moved, nil
}

// RetiredInto reports where a registry's work went, for the commands that have
// to explain why this one will not start. The destination is empty when the
// ledger never recorded one, which is not a reason to start it.
func RetiredInto(configDir string) (destination string, retired bool) {
	l, err := OpenLedger(LedgerPath(filepath.Join(configDir, "registry.env")))
	if err != nil || l.RetiredAt == "" {
		return "", false
	}
	return l.Destination, true
}

// renameAside moves one directory out of the way, returning where it went (""
// when there was nothing there).
func renameAside(dir, suffix string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", nil //nolint:nilerr // nothing there is nothing to move
	}
	to := strings.TrimRight(dir, string(os.PathSeparator)) + suffix
	if err := os.Rename(dir, to); err != nil {
		return "", fmt.Errorf("move %s aside: %w", dir, err)
	}
	return to, nil
}

// under reports that path is inside root.
func under(path, root string) bool {
	p, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
