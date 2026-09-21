// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package merge

// What the destination already has near a technique
// (docs/design/browser-led-team-transition.md, M8).
//
// The judgement is theirs, not ours. A registry embeds every draft on arrival
// and matches against its own playbook; a sender comparing titles would be
// guessing at a question the receiver can actually answer. So this asks their
// retrieval the same thing an agent would ask it, with the technique itself as
// the context, and reads back the nearest thing they serve.
//
// It lives here beside the other round trips a merge makes to somebody else's
// registry, and for one more reason: the dashboard cannot import pkg/client,
// whose own tests import the dashboard.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// Nearest reports the closest technique the destination serves to this text,
// and how close it is. ok is false when the question could not be asked at all —
// which the caller must show as "not checked" rather than as "nothing like it",
// because those are different answers.
func Nearest(hc *http.Client, destination, key, summary string) (name string, similarity float64, ok bool) {
	payload, err := json.Marshal(map[string]any{"summary_text": summary})
	if err != nil {
		return "", 0, false
	}
	req, err := http.NewRequest("POST", strings.TrimRight(destination, "/")+"/v1/evidence",
		bytes.NewReader(payload))
	if err != nil {
		return "", 0, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tacit-Key", key)
	resp, err := hc.Do(req)
	if err != nil {
		return "", 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", 0, false
	}
	var body struct {
		Candidates []struct {
			Name       string  `json:"name"`
			Similarity float64 `json:"similarity"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", 0, false
	}
	for _, c := range body.Candidates {
		if c.Similarity > similarity {
			name, similarity = c.Name, c.Similarity
		}
	}
	return name, similarity, true
}
