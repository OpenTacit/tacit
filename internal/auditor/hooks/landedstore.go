// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Keeping a survival reading, so the page can show what only the terminal knew.
//
// `tacit usage --landed` measures exactly — blame at the tip says which of
// today's lines still come from a commit in the window — and until now it
// printed and forgot. The Usage page could name the command and nothing else,
// because the session log holds a project BASENAME and never a path, so nothing
// stored can find the repository later.
//
// The way round it is the same trick the command already turns: the member
// supplies the path by standing in it. What comes back is a LandedReport, which
// is a basename, a branch name and counts — the same shape the sealed ledger
// already publishes, and nothing the log was not already allowed to hold. So a
// reading can be FILED, and the page can read the file.
//
// What is not done here: measuring on a schedule. The agent never goes looking
// for repositories, because it does not know where any of them are and this is
// not the seam that teaches it. A reading exists because somebody asked for it,
// and the page says when they last did — a survival figure from three weeks ago
// is worse than none, and the only defence is the date beside it.
package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
)

// landedRetention is how long a filed reading stays worth showing. A branch
// moves; a survival figure measured a season ago is describing a repository
// that no longer exists in that shape, and quietly keeping it on the page would
// be the page telling a lie it was told honestly.
const landedRetention = 90 * 24 * time.Hour

// FiledLanded is one reading, as it was filed.
type FiledLanded struct {
	LandedReport
	// AskedAt is when the member ran the command. The page prints it, because
	// this is the one figure here that nothing refreshes on its own.
	AskedAt time.Time `json:"asked_at"`
	// Window is the period the reading covered, in the same vocabulary the page
	// uses, so a 7-day survival is never read as a 90-day one.
	WindowDays int `json:"window_days,omitempty"`
}

type landedStore struct {
	ByProject map[string]FiledLanded `json:"by_project"`
}

func landedPath(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "landed.json")
}

// SaveLanded files one reading, replacing whatever this project had before. One
// row per project rather than a history: the question is "does the work hold
// up", which is about now, and a series of readings taken whenever somebody
// happened to run a command is not a trend.
func SaveLanded(stateDir string, rep LandedReport, window time.Duration, now time.Time) error {
	path := landedPath(stateDir)
	if path == "" || rep.Project == "" {
		return nil
	}
	s := loadLandedStore(path)
	if s.ByProject == nil {
		s.ByProject = map[string]FiledLanded{}
	}
	s.ByProject[rep.Project] = FiledLanded{
		LandedReport: rep,
		AskedAt:      now.UTC(),
		WindowDays:   int(window / (24 * time.Hour)),
	}
	prune(&s, now)
	return fsx.WriteJSONAtomic(path, s, 0o600)
}

// LandedFilings returns what has been filed, freshest first, dropping anything
// past the retention horizon. A missing or unreadable file is an empty list:
// this is a page decoration, and it must never be the reason a summary fails.
func LandedFilings(stateDir string, now time.Time) []FiledLanded {
	path := landedPath(stateDir)
	if path == "" {
		return nil
	}
	s := loadLandedStore(path)
	prune(&s, now)
	out := make([]FiledLanded, 0, len(s.ByProject))
	for _, f := range s.ByProject {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].AskedAt.Equal(out[j].AskedAt) {
			return out[i].AskedAt.After(out[j].AskedAt)
		}
		return out[i].Project < out[j].Project
	})
	return out
}

func loadLandedStore(path string) landedStore {
	var s landedStore
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	return s
}

func prune(s *landedStore, now time.Time) {
	cut := now.UTC().Add(-landedRetention)
	for k, f := range s.ByProject {
		if f.AskedAt.Before(cut) {
			delete(s.ByProject, k)
		}
	}
}
