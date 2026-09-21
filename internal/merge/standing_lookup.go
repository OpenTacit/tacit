// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Standing is what became of one contributed technique at the organization.
//
// It is the answer to the only question a member has after merging — did any of
// my work land — and it cannot be answered locally: the decision was made in
// somebody else's review queue, and nothing tells this registry about it.
type Standing struct {
	LocalID string // the technique's id on the personal registry
	DraftID string // what it became at the organization
	Name    string // the organization's name for it, when it could be read
	SentAt  string
	// State is one of: "waiting", "promoted", "shadow", "declined", "gone",
	// "checking", "unreachable". Deliberately not the destination's raw status: "retired"
	// is what the lifecycle calls a rejected draft, and telling a contributor
	// their technique was "retired" describes the mechanism rather than the
	// decision.
	State string
}

// Standing states.
const (
	StateWaiting  = "waiting"
	StatePromoted = "promoted"
	StateShadow   = "shadow"
	StateDeclined = "declined"
	StateGone     = "gone"
	// StateChecking is "nobody has asked yet", which is not the same answer as
	// "we asked and could not tell" and must not read like it.
	StateChecking    = "checking"
	StateUnreachable = "unreachable"
)

// Describe is the sentence for one standing, in the contributor's terms.
func (s Standing) Describe() string {
	switch s.State {
	case StatePromoted:
		return "in your organization's playbook"
	case StateWaiting:
		return "waiting for review"
	case StateShadow:
		return "being evaluated in shadow"
	case StateDeclined:
		return "not taken"
	case StateGone:
		return "no longer there"
	case StateChecking:
		return "checking"
	default:
		return "could not be read"
	}
}

// LookUp asks the organization what became of each contributed technique.
//
// One request per technique rather than a listing, because a listing would pull
// the organization's whole playbook to answer a question about a handful of
// entries — and a member key is entitled to that, which is a reason to be
// careful rather than a reason to help yourself.
//
// A technique that cannot be read is reported as unreachable rather than
// dropped. "We could not ask" and "they said no" are different answers, and a
// contributor who is shown the second when the first is true is being misled
// about their own work.
// This asks over plain net/http rather than through pkg/client, deliberately.
// The registry's web package renders this panel, and pkg/client's own tests
// exercise that package — so a dependency from here to the typed client closes
// a loop the compiler will not allow. Two fields of one JSON object are not
// worth a shared client.
func LookUp(destination, key string, sent map[string]Sent, hc *http.Client) []Standing {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	base := strings.TrimRight(destination, "/")
	out := make([]Standing, 0, len(sent))
	for localID, s := range sent {
		st := Standing{LocalID: localID, DraftID: s.DraftID, SentAt: s.SentAt, State: StateUnreachable}
		if name, status, code := fetchTechnique(hc, base, key, s.DraftID); code == http.StatusNotFound {
			st.State = StateGone
		} else if code == http.StatusOK {
			st.Name, st.State = name, stateFor(status)
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LocalID < out[j].LocalID })
	return out
}

// fetchTechnique reads one technique from a registry. code is 0 when the
// request could not be made at all, which the caller reports as unreachable
// rather than as an answer.
func fetchTechnique(hc *http.Client, base, key, id string) (name, status string, code int) {
	req, err := http.NewRequest("GET", base+"/v1/techniques/"+url.PathEscape(id), nil)
	if err != nil {
		return "", "", 0
	}
	req.Header.Set("X-Tacit-Key", key)
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", 0
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", resp.StatusCode
	}
	var body struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", 0
	}
	return body.Name, body.Status, http.StatusOK
}

// stateFor translates the destination's lifecycle into the contributor's
// question. A rejected draft is "retired" there, which is the mechanism; here
// it is "not taken", which is the decision.
func stateFor(status string) string {
	switch status {
	case "stable", "mined":
		return StatePromoted
	case "draft":
		return StateWaiting
	case "shadow":
		return StateShadow
	case "retired", "decayed":
		return StateDeclined
	default:
		return StateUnreachable
	}
}
