// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package client is the public, typed HTTP client for an OpenTacit registry —
// the supported way for external tools (the miner, exporters, integrations)
// to talk to /v1. It returns the typed contracts from tacit/pkg/contracts;
// the endpoint reference is docs/api/openapi.yaml.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/pkg/contracts"
)

// Registry talks to one registry service.
type Registry struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func (r *Registry) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Do sends one request to the registry and is the single transport every
// consumer shares: it joins the path to the base URL, encodes payload as JSON
// when there is one, sends the X-Tacit-Key header, and turns any status at or
// above 400 into an *APIError. A non-nil into is filled from the response body.
func (r *Registry) Do(method, path string, payload any, into any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, strings.TrimRight(r.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Tacit-Key", r.APIKey)
	resp, err := r.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode, Path: path, Body: string(bytes.TrimSpace(raw))}
	}
	if into != nil {
		return json.Unmarshal(raw, into)
	}
	return nil
}

// APIError is a non-2xx registry response.
type APIError struct {
	Status int
	Path   string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("registry %s HTTP %d: %s", e.Path, e.Status, e.Body)
}

// HTTPStatus reports the response code. Callers match this method rather than
// the type, so they can tell "the registry said no" (a key problem — a human
// must act) from "the registry didn't answer" (an outage, usually transient)
// without importing this package.
func (e *APIError) HTTPStatus() int { return e.Status }

// Message is the reason the registry gave: the "error" field of its JSON error
// envelope, or the raw body when the body is not that envelope. Use it where a
// person reads the result; use Error where a log does.
func (e *APIError) Message() string {
	var env struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(e.Body), &env) == nil && env.Error != "" {
		return env.Error
	}
	return e.Body
}

// Health reports liveness and store counts.
func (r *Registry) Health() (map[string]any, error) {
	var out map[string]any
	err := r.Do("GET", "/v1/health", nil, &out)
	return out, err
}

// HealthInfo is the typed form of /v1/health — the fields tools read. The
// endpoint is open and keyless, and older registries omit later fields, so a
// zero value in any field means "this registry did not say".
type HealthInfo struct {
	OK            bool   `json:"ok"`
	Techniques    int    `json:"techniques"`
	Events        int    `json:"events"`
	Drafts        int    `json:"drafts"`
	EmbedModel    string `json:"embed_model"`
	EmbedDegraded bool   `json:"embed_degraded"`
	EmbedWanted   string `json:"embed_wanted"`
	Version       string `json:"version"`
	// ExternalURL is the address the registry says it answers on RIGHT NOW —
	// the proxy address while Global Access is on, which supersedes the
	// configured one. Sign-in follows it, so it decides which callback the
	// identity provider actually sees.
	ExternalURL string `json:"external_url"`
	Access      string `json:"access"` // private | global | staged
	Tunnel      string `json:"tunnel"` // up | down, only while Global Access is on
	// TunnelError is why the tunnel is down, in the ingress's own words. A
	// registry that is refused an address says so here and nowhere else a caller
	// can reach: without it, every tool waiting on a public address had to poll
	// until it gave up and then guess, and "the ingress has not answered yet"
	// is a bad way to report "the ingress refused you and always will".
	TunnelError string `json:"tunnel_error,omitempty"`
}

// GetHealth reads /v1/health into the typed form. Health is the one endpoint a
// caller may reach before it holds a key, so give it a short timeout in HTTP
// when the answer gates something a person is waiting on.
func (r *Registry) GetHealth() (HealthInfo, error) {
	var out HealthInfo
	err := r.Do("GET", "/v1/health", nil, &out)
	return out, err
}

// Invite mints a join link that carries its own token, valid for ttl. The
// registry caps the lifetime; a zero ttl takes the registry's default.
func (r *Registry) Invite(ttl time.Duration) (joinURL, expiresAt string, err error) {
	var out struct {
		JoinURL   string `json:"join_url"`
		ExpiresAt string `json:"expires_at"`
	}
	err = r.Do("POST", "/v1/invite", map[string]any{"ttl_secs": int64(ttl.Seconds())}, &out)
	return out.JoinURL, out.ExpiresAt, err
}

// JoinExchange trades a join token for this machine's own member key. The
// token is the credential, so the call needs no key of its own; label is what
// the admin sees on the Members page.
func (r *Registry) JoinExchange(token, label string) (registryURL, apiKey string, err error) {
	var out struct {
		RegistryURL string `json:"registry_url"`
		APIKey      string `json:"api_key"`
	}
	err = r.Do("POST", "/v1/join/exchange", map[string]string{"token": token, "label": label}, &out)
	return out.RegistryURL, out.APIKey, err
}

// GetEvidence runs stage-2 retrieval for a characterization.
func (r *Registry) GetEvidence(ch contracts.Characterization) (contracts.EvidenceBlock, error) {
	var block contracts.EvidenceBlock
	err := r.Do("POST", "/v1/evidence", ch, &block)
	return block, err
}

// PostFeedback writes funnel events back (idempotent when drafts carry
// event_id).
func (r *Registry) PostFeedback(events []contracts.FeedbackEventDraft) (accepted, duplicates int, err error) {
	var out struct {
		Accepted   int `json:"accepted"`
		Duplicates int `json:"duplicates"`
	}
	err = r.Do("POST", "/v1/feedback", events, &out)
	return out.Accepted, out.Duplicates, err
}

// Contribute submits a technique draft into the held-out review lane.
func (r *Registry) Contribute(technique map[string]any) (id string, err error) {
	var out struct {
		ID string `json:"id"`
	}
	err = r.Do("POST", "/v1/contribute", technique, &out)
	return out.ID, err
}

// Promote flips a draft technique to a serving status (admin/root key). status is
// usually "stable"; "" lets the server default. It returns the technique's status
// after the change. Not found is reported as an *APIError with Status 404.
func (r *Registry) Promote(id, status string) (string, error) {
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	err := r.Do("POST", "/v1/admin/promote", map[string]string{"id": id, "status": status}, &out)
	return out.Status, err
}

// Recompute rebuilds the outcome rollups from the event log (admin/root key)
// and returns the number of techniques recomputed. Rollups otherwise refresh on the
// server's own interval; callers that need them fresh immediately (a bulk load)
// call this.
func (r *Registry) Recompute() (int, error) {
	var out struct {
		Techniques int `json:"techniques"`
	}
	err := r.Do("POST", "/v1/admin/recompute", map[string]any{}, &out)
	return out.Techniques, err
}

// Techniques lists techniques (admin surface; embeddings omitted by the server).
// The server's own page size applies; TechniquesUpTo raises it.
func (r *Registry) Techniques() ([]contracts.Technique, error) {
	return r.TechniquesUpTo(0)
}

// TechniquesUpTo lists techniques with an explicit ceiling, up to the server's
// maximum. A caller that must see every technique rather than a page — `tacit
// merge`, contributing a whole registry — says so here, because the default is
// a page size and stopping at it silently would leave work behind.
func (r *Registry) TechniquesUpTo(limit int) ([]contracts.Technique, error) {
	var out struct {
		Techniques []contracts.Technique `json:"techniques"`
	}
	path := "/v1/techniques"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	err := r.Do("GET", path, nil, &out)
	return out.Techniques, err
}

// Technique fetches one technique by id.
func (r *Registry) Technique(id string) (contracts.Technique, error) {
	var out contracts.Technique
	err := r.Do("GET", "/v1/techniques/"+url.PathEscape(id), nil, &out)
	return out, err
}

// EventsPage is one page of the append-only event log.
type EventsPage struct {
	Events    []contracts.FeedbackEvent `json:"events"`
	NextSince string                    `json:"next_since"`
}

// Events pages through the event log from a created_at cursor (inclusive).
// Pages may overlap at the cursor timestamp; consumers dedupe by event_id.
func (r *Registry) Events(since string, limit int) (EventsPage, error) {
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/events"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out EventsPage
	err := r.Do("GET", path, nil, &out)
	return out, err
}

// AllEvents drains the event log from a cursor, deduplicating across page
// boundaries.
func (r *Registry) AllEvents(since string) ([]contracts.FeedbackEvent, error) {
	seen := map[string]bool{}
	var out []contracts.FeedbackEvent
	cursor := since
	for {
		page, err := r.Events(cursor, 1000)
		if err != nil {
			return out, err
		}
		for _, e := range page.Events {
			if !seen[e.EventID] {
				seen[e.EventID] = true
				out = append(out, e)
			}
		}
		if page.NextSince == "" {
			return out, nil
		}
		cursor = page.NextSince
	}
}
