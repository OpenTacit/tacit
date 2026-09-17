// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package client is the HTTP client for the registry service.
//
//	GetEvidence  -> POST /v1/evidence  (stage 2 retrieval)
//	PostFeedback -> POST /v1/feedback  (stage 4 write-back)
package client

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/opentacit/tacit/internal/auditor/contracts"
	pkgclient "github.com/opentacit/tacit/pkg/client"
)

// Registry talks to one registry service.
type Registry struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// transport is this registry as the shared client sees it. The transport
// itself — base URL, JSON, the X-Tacit-Key header, the non-2xx rule — lives in
// pkg/client and is written once; what stays here is the auditor's own set of
// routes and response types.
func (r *Registry) transport() *pkgclient.Registry {
	return &pkgclient.Registry{BaseURL: r.BaseURL, APIKey: r.APIKey, HTTP: r.HTTP}
}

// StatusError is an HTTP-level rejection from the registry. It exists so
// callers can tell "the registry said no" (a key problem — a human must act)
// apart from "the registry didn't answer" (an outage — usually transient)
// without parsing error strings. The hook agent matches it structurally via
// an HTTPStatus() interface, so it takes no dependency on this package.
type StatusError struct {
	Path string
	Code int
	Body string
}

// Error renders the path, status code, and response body.
func (e *StatusError) Error() string {
	return fmt.Sprintf("registry %s HTTP %d: %s", e.Path, e.Code, e.Body)
}

// HTTPStatus reports the response code (the structural seam callers match).
func (e *StatusError) HTTPStatus() int { return e.Code }

// asStatusError restates the shared transport's rejection in this package's
// terms. It is the whole seam: callers here have always received a
// *StatusError and keep receiving one.
func asStatusError(err error) error {
	var api *pkgclient.APIError
	if errors.As(err, &api) {
		return &StatusError{Path: api.Path, Code: api.Status, Body: api.Body}
	}
	return err
}

func (r *Registry) post(path string, payload any, into any) error {
	return asStatusError(r.transport().Do("POST", path, payload, into))
}

func (r *Registry) get(path string, into any) error {
	return asStatusError(r.transport().Do("GET", path, nil, into))
}

// GetEvidence runs stage-2 retrieval.
func (r *Registry) GetEvidence(ch contracts.Characterization) (contracts.EvidenceBlock, error) {
	var block contracts.EvidenceBlock
	err := r.post("/v1/evidence", ch, &block)
	return block, err
}

// InsightsApp is the registry's rendered insights dashboard: graphical HTML for
// the MCP UI panel, a text fallback, and a structured summary for the model.
type InsightsApp struct {
	Window  string         `json:"window"`
	HTML    string         `json:"html"`
	Text    string         `json:"text"`
	Summary map[string]any `json:"summary"`
}

// CohortUse is one cohort value in use on the registry, with the counts that
// separate the name a team actually uses from a one-off.
type CohortUse struct {
	Value    string `json:"value"`
	Sessions int    `json:"sessions"`
	Shown    int    `json:"shown"`
}

// CohortDimension is one dimension of the cohort directory. Derived marks the
// dimensions the session fills in by itself (harness, surface) — context to
// read, not a choice to make.
type CohortDimension struct {
	Dimension string      `json:"dimension"`
	Values    []CohortUse `json:"values"`
	Derived   bool        `json:"derived,omitempty"`
}

// CohortDirectory is the registry's answer to "what do my colleagues call
// themselves?" — the cohorts already in use, plus the dimensions a member may
// set whether or not anyone has used them yet.
type CohortDirectory struct {
	Dimensions []CohortDimension `json:"dimensions"`
	Settable   []string          `json:"settable"`
}

// Values returns dimension's values, or nil when nobody has used it.
func (d CohortDirectory) Values(dimension string) []CohortUse {
	for _, dim := range d.Dimensions {
		if dim.Dimension == dimension {
			return dim.Values
		}
	}
	return nil
}

// GetCohorts fetches the cohort directory.
func (r *Registry) GetCohorts() (CohortDirectory, error) {
	var dir CohortDirectory
	err := r.get("/v1/cohorts", &dir)
	return dir, err
}

// GetInsightsApp fetches the rendered insights app for a window ("" -> registry
// default, currently 30d).
func (r *Registry) GetInsightsApp(window string) (InsightsApp, error) {
	path := "/v1/insights/app"
	if window != "" {
		path += "?w=" + url.QueryEscape(window)
	}
	var app InsightsApp
	err := r.get(path, &app)
	return app, err
}

// GetMapApp fetches the rendered technique-map app. Same shape as the insights
// app; the map is the whole live library, so it takes no window.
func (r *Registry) GetMapApp() (InsightsApp, error) {
	var app InsightsApp
	err := r.get("/v1/map/app", &app)
	return app, err
}

// GetOrganizationApp fetches the rendered organization summary for a window
// ("" -> registry default). Same shape as the insights app; HTML is empty
// (text-first — see web/orgapp.go).
func (r *Registry) GetOrganizationApp(window string) (InsightsApp, error) {
	path := "/v1/organization/app"
	if window != "" {
		path += "?w=" + url.QueryEscape(window)
	}
	var app InsightsApp
	err := r.get(path, &app)
	return app, err
}

// GetReviewApp fetches the rendered drafts review app: interactive HTML for
// the MCP UI panel, a text fallback, and the structured queue summary.
func (r *Registry) GetReviewApp() (InsightsApp, error) {
	var app InsightsApp
	err := r.get("/v1/review/app", &app)
	return app, err
}

// Promote decides a draft: status "stable" puts it in service, "retired"
// rejects it. Root-key authed server-side — a plain member key gets a 401.
func (r *Registry) Promote(id, status string) (map[string]any, error) {
	var resp map[string]any
	err := r.post("/v1/admin/promote", map[string]string{"id": id, "status": status}, &resp)
	return resp, err
}

// PostFeedback writes stage-4 events back.
func (r *Registry) PostFeedback(events []contracts.FeedbackEventDraft) (map[string]any, error) {
	var resp map[string]any
	err := r.post("/v1/feedback", events, &resp)
	return resp, err
}

// EnrichAuditFact posts the model-inferred half of an audit fact (tools_absent),
// keyed by audit id, after the turn was delivered.
func (r *Registry) EnrichAuditFact(e contracts.AuditFactEnrichment) error {
	var resp map[string]any
	return r.post("/v1/audit-facts/enrich", e, &resp)
}

// Contribute stores a member-drafted technique as a held-out draft.
func (r *Registry) Contribute(technique map[string]any) (map[string]any, error) {
	var resp map[string]any
	err := r.post("/v1/contribute", technique, &resp)
	return resp, err
}

// Revise proposes a reviewed update to an existing technique; it lands as a
// revision draft in the review lane (docs/design/revision-design.md).
func (r *Registry) Revise(techniqueID string, fields map[string]any) (map[string]any, error) {
	var resp map[string]any
	err := r.post("/v1/techniques/revise/"+techniqueID, fields, &resp)
	return resp, err
}

// Suggest asks the registry to research and draft up to n best-practice
// techniques matched to observed usage (docs/mining/suggest-design.md).
func (r *Registry) Suggest(n int) (map[string]any, error) {
	var resp map[string]any
	err := r.post("/v1/admin/suggest", map[string]any{"count": n}, &resp)
	return resp, err
}

// Recompute forces an outcome rollup.
func (r *Registry) Recompute() (map[string]any, error) {
	var resp map[string]any
	err := r.post("/v1/admin/recompute", map[string]any{}, &resp)
	return resp, err
}

// HealthSnapshot is the part of /v1/health the harness surfaces: the review-queue
// depth, and what KIND of instance this is (docs/distribution/global-access-plan.md).
//
// Access is answered by the registry rather than inferred from the URL on purpose.
// A member on the office network reaches the registry at a private address while
// that same registry may be globally proxied; guessing from the configured URL
// would tell two members of one org two different stories, and would be wrong for
// a self-hosted proxy or a custom domain.
type HealthSnapshot struct {
	Drafts int    `json:"drafts"`
	Access string `json:"access"` // private | global | staged; "" from an older registry
	Tunnel string `json:"tunnel"` // up | down, only while Global Access is on
}

// Health reads the open, keyless /v1/health. A short timeout: this must never
// delay a hook.
func (r *Registry) Health() (HealthSnapshot, error) {
	t := r.transport()
	if t.HTTP == nil || t.HTTP.Timeout == 0 || t.HTTP.Timeout > 3*time.Second {
		t.HTTP = &http.Client{Timeout: 3 * time.Second}
	}
	var body HealthSnapshot
	if err := t.Do("GET", "/v1/health", nil, &body); err != nil {
		var api *pkgclient.APIError
		if errors.As(err, &api) {
			return HealthSnapshot{}, fmt.Errorf("registry /v1/health HTTP %d", api.Status)
		}
		return HealthSnapshot{}, err
	}
	return body, nil
}

// DraftsCount reports the review-queue depth. Kept as the narrow accessor its
// callers already use; it costs the same request as Health.
func (r *Registry) DraftsCount() (int, error) {
	h, err := r.Health()
	return h.Drafts, err
}
