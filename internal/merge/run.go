// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"errors"
	"net/http"

	"github.com/opentacit/tacit/pkg/contracts"
)

// Note is how a run reports one technique. ok distinguishes a crossing from a
// refusal; the caller decides whether that is a console line or an HTML row.
type Note func(ok bool, format string, a ...any)

// ContributeAll sends everything that has not gone, recording each crossing
// before attempting the next.
//
// Per technique, and carrying on past a refusal: contributions are the
// adversary-plausible lane, so the destination's safety screen hard-rejects a
// High finding at the door, and one technique it will not take is not a reason
// to abandon a merge of forty others.
//
// It stops for exactly two things. A rejected key means nothing will be
// accepted and every further attempt is noise. And a ledger that cannot be
// written after a technique has landed means this run cannot prove what it
// did — continuing would be building on a record that is already wrong, and
// the next run would contribute a second copy of everything after that point.
func ContributeAll(hc *http.Client, l *Ledger, techniques []contracts.Technique,
	outcomes map[string]contracts.Outcome, since string, note Note) (sent, failed int, fatal error) {
	for _, t := range techniques {
		if prior, done := l.AlreadySent(t.ID, l.Destination); done {
			note(true, "%s — already there as %s", t.Name, prior.DraftID)
			continue
		}
		draftID, err := Send(hc, l.Destination, l.Key, Contribution(t, StandingNote(outcomes[t.ID], since)))
		var api *APIError
		switch {
		case errors.As(err, &api):
			failed++
			note(false, "%s — refused (%d): %s", t.Name, api.Status, api.Message())
			if api.Status == http.StatusUnauthorized || api.Status == http.StatusForbidden {
				return sent, failed, errors.New("the organization refused this member key; nothing further will be accepted")
			}
			continue
		case err != nil:
			failed++
			note(false, "%s — %v", t.Name, err)
			continue
		}
		if err := l.Record(t.ID, l.Destination, draftID); err != nil {
			failed++
			note(false, "%s landed as %s but could not be recorded: %v", t.Name, draftID, err)
			return sent, failed, errors.New("the ledger could not be written; running again would contribute a second copy")
		}
		sent++
		note(true, "%s — contributed as %s", t.Name, draftID)
	}
	return sent, failed, nil
}
