// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package fsx replaces a file whole or not at all.
//
// Every state file OpenTacit keeps — the technique set, member keys, the route
// table, the member's own env file — is read back by something that assumes it
// is complete. A plain os.WriteFile truncates first and writes second, so a
// crash, a full disk or a reader arriving mid-write sees half a file. Writing
// to a temp file in the same directory and renaming it over the target closes
// that window: on every filesystem OpenTacit runs on, the rename either happened or
// it did not.
//
// This used to be about fifteen copies of the same six lines, and the copies
// had drifted in the parts that matter: fixed temp names, differing modes, and
// three that threw the rename error away.
package fsx

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to a temp file beside path and renames it over
// path. A reader sees the old contents or the new ones, never a mixture.
//
// The temp file takes a random name from os.CreateTemp rather than a fixed
// path+".tmp": two writers sharing one temp name interleave their bytes and
// then rename the result into place, which is the corruption the rename was
// meant to prevent.
//
// perm is applied with Chmod after the write, so the process umask cannot
// narrow it and a caller that asks for 0600 gets 0600.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return write(path, data, perm, false)
}

// WriteFileAtomicSync is WriteFileAtomic plus a flush of the file and of the
// directory that holds it, so the new contents survive a power cut and not
// merely a crash of this process. It costs two trips to the disk, which is
// worth spending on a file that has to come back and not on a copy of one.
func WriteFileAtomicSync(path string, data []byte, perm os.FileMode) error {
	return write(path, data, perm, true)
}

// WriteJSONAtomic marshals v with two-space indents and a trailing newline,
// then writes it through WriteFileAtomic. A caller whose file has any other
// shape — a different indent, no trailing newline, one line of compact JSON —
// marshals it itself and calls WriteFileAtomic, because the bytes on disk are
// a format somebody else already reads.
func WriteJSONAtomic(path string, v any, perm os.FileMode) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(raw, '\n'), perm)
}

func write(path string, data []byte, perm os.FileMode, flush bool) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // a no-op once the rename has happened
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if flush {
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
	}
	// Chmod before the rename, so the file is never readable at the wrong mode
	// under its real name.
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if flush {
		// Best effort: not every filesystem opens a directory for this, and a
		// failure leaves the rename as durable as it used to be.
		if d, err := os.Open(dir); err == nil {
			_ = d.Sync()
			_ = d.Close()
		}
	}
	return nil
}
