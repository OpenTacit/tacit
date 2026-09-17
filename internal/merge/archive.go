// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/pkg/contracts"
)

// Archive is everything a personal registry knew that its organization will
// not be told.
//
// It exists because of what merging now does. Leaving the evidence at home was
// harmless while the personal registry kept running — its own dashboard still
// held the funnel, and the member could look. A command that retires the
// registry turns "stays home" into "is destroyed": the measurements that
// justified every technique in the merge disappear with the hostname that
// served them. So they are written to a file first, and the member is told
// where it is.
//
// It is a record, not a format anything reads back. Nothing imports an archive,
// because importing one into an organization is precisely the thing the
// evidence rule forbids.
type Archive struct {
	WrittenAt   string                    `json:"written_at"`
	Registry    string                    `json:"registry"`
	Destination string                    `json:"destination,omitempty"`
	Note        string                    `json:"note"`
	Techniques  []contracts.Technique     `json:"techniques"`
	Outcomes    []contracts.Outcome       `json:"outcomes"`
	Events      []contracts.FeedbackEvent `json:"events"`
	Sent        map[string]Sent           `json:"contributed,omitempty"`
}

const archiveNote = "The measurements a personal registry made of its own use. " +
	"They were deliberately not contributed: an organization's promotion floors " +
	"are computed over cohorts, and one member's outcomes entering them would be " +
	"a sample of one. This file is that history, kept because retiring the " +
	"registry would otherwise destroy it."

// NewArchive assembles the record. events is the whole log; outcomes are
// derived from it rather than fetched, because the rollups ARE the events
// folded up and the log is the thing that cannot be recomputed.
func NewArchive(registry, destination string, techniques []contracts.Technique, events []contracts.FeedbackEvent, sent map[string]Sent) Archive {
	return Archive{
		WrittenAt:   time.Now().UTC().Format(time.RFC3339),
		Registry:    registry,
		Destination: destination,
		Note:        archiveNote,
		Techniques:  techniques,
		Outcomes:    Outcomes(events),
		Events:      events,
		Sent:        sent,
	}
}

// Write saves the archive to path, creating parent directories.
func (a Archive) Write(path string) error {
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// ArchiveName is a filename that says what the file is and when it was made,
// so it is still identifiable in a downloads folder a year later.
func ArchiveName(now time.Time) string {
	return "tacit-personal-archive-" + now.UTC().Format("20060102-150405") + ".json"
}

// ArchivePath decides where a run's archive goes: the one already written if
// it is still there, otherwise a fresh name under the member's state
// directory.
//
// It lives here because there are two merge surfaces — the command and the
// browser flow — and they each grew their own copy of "$HOME plus a timestamp".
// Fixing one and not the other is exactly what happened: the count in the home
// directory went on climbing from the copy nobody had looked at.
//
// Reusing the recorded path is the other half. A merge is retried — the
// destination is unreachable, a key is wrong, the member changes their mind —
// and a fresh timestamp per attempt turned one merge into a hundred files. Same
// merge, same destination, same evidence, one file. A recorded path the member
// has since deleted or moved falls through to a new name rather than failing:
// the archive exists so the evidence survives, and re-writing it is always safe.
//
// An empty stateDir means nothing could tell us where state goes, and the home
// directory is then better than not writing the archive at all — which is the
// one outcome the step it serves refuses.
func ArchivePath(stateDir, recorded string) string {
	if recorded != "" {
		if _, err := os.Stat(recorded); err == nil {
			return recorded
		}
	}
	name := ArchiveName(time.Now())
	if stateDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return name
		}
		return filepath.Join(home, name)
	}
	return filepath.Join(stateDir, "archives", name)
}

// Outcomes folds the event log into per-technique counts, overall — the same
// shape the registry's own rollups use, for the segment that covers everything.
//
// Only the four stages a member would recognise are counted. The shadow stages
// measure techniques that were never shown to anybody, and putting them in a
// summary of "how this went for me" would inflate it with an experiment the
// member never saw.
func Outcomes(events []contracts.FeedbackEvent) []contracts.Outcome {
	by := map[string]*contracts.Outcome{}
	for _, e := range events {
		if e.TechniqueID == "" {
			continue
		}
		o, ok := by[e.TechniqueID]
		if !ok {
			o = &contracts.Outcome{TechniqueID: e.TechniqueID, SegmentKey: "__overall__"}
			by[e.TechniqueID] = o
		}
		switch e.Stage {
		case "shown":
			o.Shown++
		case "adopted":
			o.Adopted++
		case "helped":
			// This file was the only one that asked; the rule lives on the event
			// now, so every fold of the log answers the same way.
			if e.CountsAsHelped() {
				o.Helped++
			}
		case "dismissed":
			o.Dismissed++
		}
		if e.CreatedAt > o.LastUpdated {
			o.LastUpdated = e.CreatedAt
		}
	}
	out := make([]contracts.Outcome, 0, len(by))
	for _, o := range by {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TechniqueID < out[j].TechniqueID })
	return out
}

// OutcomeIndex is Outcomes by technique id, for building standing sentences.
func OutcomeIndex(events []contracts.FeedbackEvent) map[string]contracts.Outcome {
	out := map[string]contracts.Outcome{}
	for _, o := range Outcomes(events) {
		out[o.TechniqueID] = o
	}
	return out
}

// FirstSeen is the day this registry's log begins, as "2 January 2006" — the
// "since" in a standing sentence. Empty when there are no events.
func FirstSeen(events []contracts.FeedbackEvent) string {
	earliest := ""
	for _, e := range events {
		if e.CreatedAt != "" && (earliest == "" || e.CreatedAt < earliest) {
			earliest = e.CreatedAt
		}
	}
	if earliest == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(earliest))
	if err != nil {
		return ""
	}
	return t.Format("2 January 2006")
}
