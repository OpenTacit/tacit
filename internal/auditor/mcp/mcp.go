// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package mcp is the ask path described in docs/harness/in-harness-hooks.md: a
// Model Context Protocol server over stdio exposing one tool,
// tacit_search, so the model — or the member through it — can PULL the
// org's validated techniques ("do we have a better way to do X?") instead
// of waiting for a push. Results arrive as first-class tool output with the
// evidence lines attached.
//
// The protocol slice implemented is exactly what a tools-only stdio server
// needs (JSON-RPC 2.0, newline-delimited): initialize, tools/list,
// tools/call, ping; notifications are consumed silently. Standard library
// only, consistent with the rest of the module (the pgx dependency remains
// storage-specific).
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/opentacit/tacit/internal/product"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/opentacit/tacit/internal/auditor/audit"
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/windows"
)

// SearchFunc runs stage-2 retrieval for a characterization and returns the
// evidence block (in production: a client.Registry closure). It takes the full
// characterization — not a bare query string — because the server mints the
// audit id BEFORE retrieval and must send it along: the id is the join key the
// registry files the audit fact under, and the member explicitly asking for a
// technique is the highest-intent signal the system receives.
type SearchFunc func(ch contracts.Characterization) (contracts.EvidenceBlock, error)

// InsightsFunc fetches the rendered insights dashboard for a window ("" -> default):
// graphical HTML for the UI panel, a text fallback, and a structured summary
// for the model. In production a client.Registry closure.
type InsightsFunc func(window string) (html, text string, summary map[string]any, err error)

// OrgFunc fetches the organization summary for a window ("" -> default):
// a chat-friendly text plus a structured summary. Text-first — no UI panel.
type OrgFunc func(window string) (text string, summary map[string]any, err error)

// MapFunc fetches the rendered technique-map app: interactive HTML for the UI
// panel, a text fallback, and a structured summary for the model. In production a
// client.Registry closure. The map is the whole live library — no arguments.
type MapFunc func() (html, text string, summary map[string]any, err error)

// DraftsFunc fetches the rendered drafts review app: interactive HTML for the
// UI panel, a text fallback, and the structured queue. In production a
// client.Registry closure.
type DraftsFunc func() (html, text string, summary map[string]any, err error)

// UsageFunc renders the member's OWN usage app for a window ("" -> default):
// graphical HTML for the UI panel, a text fallback, and a structured summary
// for the model. It reads member-LOCAL data (the usage log on this machine) via
// the same renderer the `tacit usage` CLI uses (hooks.RenderUsageApp) — the view
// lives in the binary; this returns it as an MCP App resource. On the shared
// registry endpoint it delegates to the co-located log (self-hosted) or a
// pointer; on the stdio plugin it reads the log directly.
type UsageFunc func(window string) (html, text string, summary map[string]any, err error)

// DraftActionFunc decides one draft: action is "promote" or "reject". It
// returns the confirmation text the member sees. The registry enforces who
// may decide (the admin key) — this path just carries the member's key and
// reports the refusal honestly.
type DraftActionFunc func(id, action string) (text string, err error)

// ContributeFunc files a technique as a draft. It returns the confirmation the
// member sees. Drafts never serve until a reviewer promotes them, which is what
// makes this safe to expose to a model: the worst outcome is a row in a queue.
type ContributeFunc func(c Contribution) (text string, err error)

// Contribution is what a member's agent captured. The shape mirrors the fields
// /tacit:contribute collects, because it lands in the same queue through the
// same endpoint.
type Contribution struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Recipe      string   `json:"recipe"`
	AppliesWhen string   `json:"applies_when"`
	NotWhen     string   `json:"not_when"`
	Scope       string   `json:"scope"`
	Tags        []string `json:"tags"`
	TaskTypes   []string `json:"task_types"`
}

// FeedbackFunc records what became of a technique the member was shown or
// found. Stage is one of the four the evidence model knows.
type FeedbackFunc func(techniqueID, stage, reason string) (text string, err error)

// Server handles one MCP session.
type Server struct {
	Search      SearchFunc
	Insights    InsightsFunc    // optional; enables the tacit_insights app tool + UI resource
	Org         OrgFunc         // optional; enables the tacit_org summary tool
	Map         MapFunc         // optional; enables the tacit_map app tool + UI resource
	Drafts      DraftsFunc      // optional; enables the tacit_drafts app tool + UI resource
	DraftAction DraftActionFunc // optional; enables tacit_draft_action (the review app's decision path)
	Usage       UsageFunc       // optional; enables the tacit_usage app tool + UI resource (member-local)
	// The write half. A connector member — somebody who added one /mcp URL to
	// their chat tool and signed in, with no binary on their machine — could
	// draw on the org's evidence and produce none of it: search and metrics
	// were the whole surface. An entire population consuming a corpus it
	// cannot feed is a corpus that stops being true.
	Contribute ContributeFunc // optional; enables tacit_contribute
	Feedback   FeedbackFunc   // optional; enables tacit_feedback
	Segment    contracts.Segment
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	toolName         = "tacit_search"
	metricsToolName  = "tacit_metrics"
	insightsToolName = "tacit_insights" // back-compat: dispatched, no longer listed
	orgToolName      = "tacit_org"      // back-compat: dispatched, no longer listed
	mapToolName      = "tacit_map"      // back-compat: dispatched, no longer listed
	draftsToolName   = "tacit_drafts"
	actionToolName   = "tacit_draft_action"
	usageToolName    = "tacit_usage"
	contribToolName  = "tacit_contribute"
	feedbackToolName = "tacit_feedback"
	// insightsResourceURI is the UI resource the insights app renders under. The
	// ui:// scheme is what signals "this is a UI panel" to mcp-ui clients; the
	// same URI is registered as an MCP Apps template (resources/list+read).
	insightsResourceURI = "ui://tacit/insights"
	// mapResourceURI is the same, for the interactive technique map app.
	mapResourceURI = "ui://tacit/map"
	// draftsResourceURI is the same, for the interactive drafts review app.
	draftsResourceURI = "ui://tacit/drafts"
	// usageResourceURI is the same, for the member's personal usage app.
	usageResourceURI = "ui://tacit/usage"
	// appProfileMime is the official MCP Apps profile (ext-apps 2026-01-26);
	// the inline mcp-ui content item uses bare text/html for wider reach.
	appProfileMime = "text/html;profile=mcp-app"
	uiExtension    = "io.modelcontextprotocol/ui"
)

var toolDef = map[string]any{
	"name": toolName,
	"description": "Search the organization's " + product.Name() + " playbook for techniques relevant to a task. " +
		"Use it when internal tools, connectors, or conventions may apply. Report returned evidence " +
		"with measured values exactly as returned.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "what the member is trying to do, in plain words",
			},
		},
		"required": []string{"query"},
	},
}

// metricsToolDef consolidates the former tacit_insights / tacit_org / tacit_map
// into one tool with a `view`, so a member has ONE metrics entry point instead
// of three tools that each had to disclaim the other two ("for pipeline metrics
// use tacit_insights instead…"). view=funnel is the pipeline, view=cohorts the
// who/where spread (aggregate, never individuals), view=map the library drawn as
// a network. Interactive panel where the host supports it (funnel and map
// register UI resources); text-first everywhere else — which finally gives the
// map a text path on terminal-only harnesses, where tacit_map was unreachable.
// Default view is funnel; the static _meta hint points there, and each call
// embeds the resource its view needs (the render is per-call, not the static hint).
var metricsToolDef = map[string]any{
	"name": metricsToolName,
	"description": "Show " + product.Name() + " usage and outcomes for the organization. " +
		"view=funnel: the shown→adopted→helped pipeline, helped and fit-check-decline rates, and trends (default). " +
		"view=cohorts: adoption breadth by cohort, which areas of practice are spreading, opportunities, and proponent cohorts — cohort aggregates only, never individuals. " +
		"view=map: every live technique placed by how it relates to the others, clustered into areas of practice, sized by adoption and shaded by helped rate. " +
		"Returns an interactive panel when the host supports it and a text summary otherwise. Report measured values exactly as returned.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"view": map[string]any{
				"type":        "string",
				"description": "which metrics view: funnel (default), cohorts, or map",
				"enum":        []string{"funnel", "cohorts", "map"},
			},
			"window": map[string]any{
				"type":        "string",
				"description": "time window for funnel/cohorts: " + windows.ProseDefault(),
				"enum":        slices.Clone(windows.Keys),
			},
		},
	},
	"_meta": map[string]any{
		"ui": map[string]any{"resourceUri": insightsResourceURI},
	},
}

// The old tacit_insights/org/map tools are still dispatched in tools/call for
// back-compat but are no longer advertised in tools/list (tacit_metrics
// supersedes them).

// draftsToolDef exposes the drafts review queue. App-capable hosts render the
// interactive review panel (whose buttons call tacit_draft_action back through
// the host); text-only hosts get the queue as text plus the structured list,
// and the command file drives the decisions through a native form or plain
// conversation instead.
var draftsToolDef = map[string]any{
	"name": draftsToolName,
	"description": "Show the " + product.Name() + " drafts awaiting review — techniques contributed, suggested, or " +
		"imported that serve nobody until a reviewer promotes them. Returns an interactive review panel " +
		"when supported and the queue as text otherwise. To decide a draft, use tacit_draft_action.",
	"inputSchema": map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	},
	"_meta": map[string]any{
		"ui": map[string]any{"resourceUri": draftsResourceURI},
	},
}

// usageToolDef exposes the member's OWN OpenTacit usage — the "show me how I'm
// using " + product.Name() ask. Personal and machine-local (never the registry's cohort
// aggregates): queries made, suggestions shown/adopted/helped, per-technique
// drill-down, over a window. App-capable hosts render the panel; text-only
// hosts get the same numbers as text plus the structured summary.
var usageToolDef = map[string]any{
	"name": usageToolName,
	"description": "Show how the CURRENT member is using " + product.Name() + " — their own queries, and the suggestions " +
		"shown, adopted, and measurably helped, over time, with a per-technique breakdown. This is personal and " +
		"machine-local (never collected by the registry); use it for \"how am I using " + product.Name() + "\" questions. " +
		"For the organization's aggregate outcomes use tacit_metrics instead. Returns an interactive panel when " +
		"supported and a text summary otherwise.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"window": map[string]any{
				"type":        "string",
				"description": "time window: " + windows.ProseDefault(),
				"enum":        slices.Clone(windows.Keys),
			},
		},
	},
	"_meta": map[string]any{
		"ui": map[string]any{"resourceUri": usageResourceURI},
	},
}

// actionToolDef is the decision path the review app (and the model, on the
// member's explicit ask) uses. The description binds the model: deciding a
// draft is the member's call, never the model's initiative.
var actionToolDef = map[string]any{
	"name": actionToolName,
	"description": "Decide one " + product.Name() + " draft: promote puts it in service, reject declines it (kept, not " +
		"erased). Call this ONLY when the member has explicitly chosen what to do with a specific draft — " +
		"never promote or reject on your own initiative.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "the draft's technique id",
			},
			"action": map[string]any{
				"type":        "string",
				"description": "what the member decided",
				"enum":        []string{"promote", "reject"},
			},
		},
		"required": []string{"id", "action"},
	},
}

// contribToolDef is the write half of the connector tier: a member who reached
// this registry by adding one URL can now add to the playbook, not only read it.
//
// The description binds the model the way actionToolDef does. A technique is a
// claim about what works, made in the member's name to every colleague, so it
// is theirs to make: the agent drafts it when asked and never on its own.
var contribToolDef = map[string]any{
	"name": contribToolName,
	"description": "File a technique the member has found into the organization's " + product.Name() + " playbook, as a draft " +
		"for review. Call this ONLY when the member has asked to contribute, capture, or add a move — " +
		"never on your own initiative. Drafts reach nobody until a reviewer promotes them. Write the " +
		"recipe as the concrete steps another person would follow, and say when it does NOT apply.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":         map[string]any{"type": "string", "description": "the move, as a short imperative sentence"},
			"description":  map[string]any{"type": "string", "description": "what it is and why it works, in two or three sentences"},
			"recipe":       map[string]any{"type": "string", "description": "the concrete steps, or the prompt to use"},
			"applies_when": map[string]any{"type": "string", "description": "the situations it fits"},
			"not_when":     map[string]any{"type": "string", "description": "the situations it does not fit"},
			"scope": map[string]any{
				"type": "string", "enum": []string{"general", "org"},
				"description": "org when it depends on this organization's own tools, data or conventions; general otherwise",
			},
			"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"task_types": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"name", "description", "recipe"},
	},
}

// feedbackToolDef closes the loop for a member with no hook agent.
//
// In a coding harness the stages are inferred from what the member does. A chat
// tool exposes no lifecycle hooks, so the only honest evidence is what the
// member says — which makes this the one path by which a connector member's
// outcomes reach the ranking every colleague sees.
var feedbackToolDef = map[string]any{
	"name": feedbackToolName,
	"description": "Record what happened to a " + product.Name() + " technique: adopted when the member acted on it, helped " +
		"when it improved the outcome, dismissed when they declined it. Call this when the member says so, " +
		"or plainly acts on a technique you offered them — never to report a result you have not seen.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"technique_id": map[string]any{"type": "string", "description": "the technique's id, as tacit_search returned it"},
			"stage": map[string]any{
				"type": "string", "enum": []string{"adopted", "helped", "dismissed"},
				"description": "what became of it",
			},
			"reason": map[string]any{"type": "string", "description": "with dismissed: why, in the member's words"},
		},
		"required": []string{"technique_id", "stage"},
	},
}

// toolRow is one dispatchable tool: what tools/list advertises, when it is
// advertised, when it can be served at all, and the call itself. Advertising
// and dispatch are separate on purpose — the superseded names (tacit_insights,
// tacit_org, tacit_map) carry no def, so a client that still holds one keeps
// working while no new client learns it.
type toolRow struct {
	name string
	// def is the advertised definition; nil means dispatched but never listed.
	def map[string]any
	// listed reports whether this server advertises the tool. Read only when
	// def is set.
	listed func(*Server) bool
	// wired reports whether the seam the tool needs is present. false answers
	// "unknown tool", which is the honest answer for a tool this server cannot
	// serve at all. A tool that reports its own missing views (tacit_metrics)
	// is always wired.
	wired func(*Server) bool
	call  func(s *Server, args json.RawMessage) map[string]any
}

func always(*Server) bool         { return true }
func never(*Server) bool          { return false }
func hasInsights(s *Server) bool  { return s.Insights != nil }
func hasOrg(s *Server) bool       { return s.Org != nil }
func hasMap(s *Server) bool       { return s.Map != nil }
func hasDrafts(s *Server) bool    { return s.Drafts != nil }
func hasUsage(s *Server) bool     { return s.Usage != nil }
func hasAction(s *Server) bool    { return s.DraftAction != nil }
func hasContrib(s *Server) bool   { return s.Contribute != nil }
func hasFeedback(s *Server) bool  { return s.Feedback != nil }
func hasAnyMetric(s *Server) bool { return s.Insights != nil || s.Org != nil || s.Map != nil }

// toolRows is the whole tool surface, in the order tools/list reports it.
var toolRows = []toolRow{
	{name: toolName, def: toolDef, listed: always, wired: always,
		call: func(s *Server, args json.RawMessage) map[string]any {
			var a struct {
				Query string `json:"query"`
			}
			_ = json.Unmarshal(args, &a)
			text, isErr := s.search(a.Query)
			return textResult(text, isErr)
		}},
	// One metrics tool with a `view`, not three tools that disclaim each other.
	// Listed when any of its views can be served; each view guards its own
	// closure and says so when it is missing.
	{name: metricsToolName, def: metricsToolDef, listed: hasAnyMetric, wired: always,
		call: func(s *Server, args json.RawMessage) map[string]any {
			var a struct {
				View   string `json:"view"`
				Window string `json:"window"`
			}
			_ = json.Unmarshal(args, &a)
			switch a.View {
			case "", "funnel":
				if s.Insights == nil {
					return textResult("the funnel view is not available on this registry", true)
				}
				return s.insightsResult(a.Window)
			case "cohorts":
				if s.Org == nil {
					return textResult("the cohorts view is not available on this registry", true)
				}
				return s.orgResult(a.Window)
			case "map":
				if s.Map == nil {
					return textResult("the map view is not available on this registry", true)
				}
				return s.mapResult()
			default:
				return textResult(`view must be "funnel", "cohorts", or "map"`, true)
			}
		}},
	{name: draftsToolName, def: draftsToolDef, listed: hasDrafts, wired: hasDrafts,
		call: func(s *Server, _ json.RawMessage) map[string]any { return s.draftsResult() }},
	{name: usageToolName, def: usageToolDef, listed: hasUsage, wired: hasUsage,
		call: func(s *Server, args json.RawMessage) map[string]any {
			return s.usageResult(windowArg(args))
		}},
	{name: contribToolName, def: contribToolDef, listed: hasContrib, wired: hasContrib,
		call: func(s *Server, args json.RawMessage) map[string]any {
			var c Contribution
			_ = json.Unmarshal(args, &c)
			if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Recipe) == "" ||
				strings.TrimSpace(c.Description) == "" {
				return textResult("name, description and recipe are required", true)
			}
			text, err := s.Contribute(c)
			if err != nil {
				return textResult(err.Error(), true)
			}
			return textResult(text, false)
		}},
	{name: feedbackToolName, def: feedbackToolDef, listed: hasFeedback, wired: hasFeedback,
		call: func(s *Server, args json.RawMessage) map[string]any {
			var a struct {
				TechniqueID string `json:"technique_id"`
				Stage       string `json:"stage"`
				Reason      string `json:"reason"`
			}
			_ = json.Unmarshal(args, &a)
			switch {
			case strings.TrimSpace(a.TechniqueID) == "":
				return textResult("technique_id is required", true)
			case a.Stage != "adopted" && a.Stage != "helped" && a.Stage != "dismissed":
				return textResult(`stage must be "adopted", "helped" or "dismissed"`, true)
			}
			text, err := s.Feedback(a.TechniqueID, a.Stage, a.Reason)
			if err != nil {
				return textResult(err.Error(), true)
			}
			return textResult(text, false)
		}},
	{name: actionToolName, def: actionToolDef, listed: hasAction, wired: hasAction,
		call: func(s *Server, args json.RawMessage) map[string]any {
			var a struct {
				ID     string `json:"id"`
				Action string `json:"action"`
			}
			_ = json.Unmarshal(args, &a)
			if a.ID == "" || (a.Action != "promote" && a.Action != "reject") {
				return textResult(`id and action ("promote" or "reject") are required`, true)
			}
			text, err := s.DraftAction(a.ID, a.Action)
			if err != nil {
				return textResult(err.Error(), true)
			}
			return textResult(text, false)
		}},
	// The three names tacit_metrics superseded: dispatched, never advertised.
	{name: insightsToolName, listed: never, wired: hasInsights,
		call: func(s *Server, args json.RawMessage) map[string]any {
			return s.insightsResult(windowArg(args))
		}},
	{name: orgToolName, listed: never, wired: hasOrg,
		call: func(s *Server, args json.RawMessage) map[string]any {
			return s.orgResult(windowArg(args))
		}},
	{name: mapToolName, listed: never, wired: hasMap,
		call: func(s *Server, _ json.RawMessage) map[string]any { return s.mapResult() }},
}

// toolRowFor finds a tool by name; nil means this server has no such tool.
func toolRowFor(name string) *toolRow {
	for i := range toolRows {
		if toolRows[i].name == name {
			return &toolRows[i]
		}
	}
	return nil
}

// windowArg reads the `window` argument every windowed tool takes ("" -> the
// callee's default).
func windowArg(args json.RawMessage) string {
	var a struct {
		Window string `json:"window"`
	}
	_ = json.Unmarshal(args, &a)
	return a.Window
}

// Run serves newline-delimited JSON-RPC until EOF.
func (s *Server) Run(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue // not ours to crash over
		}
		if resp := s.Handle(req); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// ServeHTTP exposes the same session over MCP's Streamable HTTP transport
// (spec 2025-03-26): the client POSTs one JSON-RPC message and, because this
// server is tools-only with no server-initiated messages, receives a single
// application/json response. Requests without an id are notifications — they
// are acknowledged with 202 Accepted and no body. The transport is stateless:
// no Mcp-Session-Id is issued, so every POST stands alone and the handler is
// safe to share across concurrent callers (Handle holds no per-call state).
//
// Batching (removed from the spec in 2025-03-26) is not supported: one message
// per request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON-RPC message", http.StatusBadRequest)
		return
	}
	resp := s.Handle(req)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted) // notification: acknowledged, no body
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Handle dispatches one message; nil means no response (a notification).
func (s *Server) Handle(req request) *response {
	if req.ID == nil {
		return nil // notification (e.g. notifications/initialized): consume silently
	}
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := params.ProtocolVersion
		if version == "" {
			version = "2024-11-05"
		}
		caps := map[string]any{"tools": map[string]any{}}
		if s.Insights != nil || s.Map != nil || s.Drafts != nil || s.Usage != nil {
			// The app tools are served as MCP UI resources: advertise the resources
			// capability and the MCP Apps UI extension so app-capable hosts enable
			// rendering.
			caps["resources"] = map[string]any{}
			caps["extensions"] = map[string]any{
				uiExtension: map[string]any{"mimeTypes": []any{appProfileMime}},
			}
		}
		return ok(req, map[string]any{
			"protocolVersion": version,
			"capabilities":    caps,
			"serverInfo":      map[string]any{"name": "tacit", "version": "0.1.0"},
		})
	case "ping":
		return ok(req, map[string]any{})
	case "tools/list":
		tools := []any{}
		for _, row := range toolRows {
			if row.def != nil && row.listed(s) {
				tools = append(tools, row.def)
			}
		}
		return ok(req, map[string]any{"tools": tools})
	case "resources/list":
		return ok(req, map[string]any{"resources": s.resourceList()})
	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &params)
		return s.readResource(req, params.URI)
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return fail(req, -32602, "invalid params")
		}
		row := toolRowFor(params.Name)
		if row == nil || !row.wired(s) {
			return fail(req, -32602, "unknown tool")
		}
		return ok(req, row.call(s, params.Arguments))
	default:
		return fail(req, -32601, "method not found")
	}
}

// textResult is a plain text tool result (the search path and error fallbacks).
func textResult(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": isErr,
	}
}

// maxInlineAppBytes caps the HTML an app tool will INLINE in its tool result.
//
// Inlining is an mcp-ui convenience: hosts that render a resource content item
// get the panel without a second round trip. Every panel is ALSO registered as
// an MCP Apps template and served by resources/read, which is the path Claude
// and other app hosts actually take — so the inline copy is a convenience, not
// the delivery mechanism, and it is not worth failing a call for.
//
// Uncapped it failed calls. A host that does not render resources — Claude Code
// in a terminal, the reference harness — flattens the item to text, and the
// panels are large because each embeds the whole stylesheet: insights 180KB,
// drafts 168KB, map 248KB, against a tool-result ceiling well under that. All
// three tools returned nothing but an over-limit error, taking /tacit:insights
// and /tacit:drafts down with them, while the 1KB text fallback sat unread in
// the same result.
//
// 32KB keeps the small panel (usage, ~21KB) inline for mcp-ui hosts and bounds
// the worst case at roughly 8k tokens; anything larger travels as a
// resource_link, which every host can carry and app hosts can follow.
const maxInlineAppBytes = 32 << 10

// appResult builds an app tool's result: the text fallback first (the only part
// a terminal host can use), then the panel — inline when it is small enough to
// be free, as a link when it is not — plus the structured summary for the model
// and the _meta.ui pointer app hosts render from.
func appResult(uri, name, html, text string, summary map[string]any) map[string]any {
	content := []any{map[string]any{"type": "text", "text": text}}
	if len(html) <= maxInlineAppBytes {
		content = append(content, map[string]any{"type": "resource", "resource": map[string]any{
			"uri":      uri,
			"mimeType": appProfileMime, // MCP Apps hosts (Claude) key off the profile mime to render
			"text":     html,
			"_meta":    uiResourceMeta(),
		}})
	} else {
		content = append(content, map[string]any{
			"type":     "resource_link",
			"uri":      uri,
			"name":     name,
			"mimeType": appProfileMime,
			"_meta":    uiResourceMeta(),
		})
	}
	return map[string]any{
		"content":           content,
		"structuredContent": summary,
		"isError":           false,
		"_meta":             map[string]any{"ui": map[string]any{"resourceUri": uri}},
	}
}

// insightsResult builds the tacit_insights tool result.
func (s *Server) insightsResult(window string) map[string]any {
	html, text, summary, err := s.Insights(window)
	if err != nil {
		return textResult("insights unavailable: "+err.Error(), true)
	}
	return appResult(insightsResourceURI, product.Name()+" insights", html, text, summary)
}

// orgResult builds the cohorts view: the organization summary as text, plus the
// structured summary for the model. Text-first — there is no panel, so the
// result carries no UI resource. The superseded tacit_org name returns exactly
// this, which is what makes it a name for the same answer rather than a second
// implementation of it.
func (s *Server) orgResult(window string) map[string]any {
	text, summary, err := s.Org(window)
	if err != nil {
		return textResult("organization summary unavailable: "+err.Error(), true)
	}
	res := textResult(text, false)
	res["structuredContent"] = summary
	return res
}

// mapResult builds the tacit_map tool result.
func (s *Server) mapResult() map[string]any {
	html, text, summary, err := s.Map()
	if err != nil {
		return textResult("playbook map unavailable: "+err.Error(), true)
	}
	return appResult(mapResourceURI, product.Name()+" playbook map", html, text, summary)
}

// draftsResult builds the tacit_drafts tool result: the queue as text, plus the
// interactive review app.
func (s *Server) draftsResult() map[string]any {
	html, text, summary, err := s.Drafts()
	if err != nil {
		return textResult("drafts queue unavailable: "+err.Error(), true)
	}
	return appResult(draftsResourceURI, product.Name()+" drafts review", html, text, summary)
}

// usageResult builds the tacit_usage tool result. The HTML comes from the same
// renderer the CLI uses (hooks.RenderUsageApp), read from member-local data —
// so the panel appears with no registry data at all.
func (s *Server) usageResult(window string) map[string]any {
	html, text, summary, err := s.Usage(window)
	if err != nil {
		return textResult("usage unavailable: "+err.Error(), true)
	}
	return appResult(usageResourceURI, "Your "+product.Name()+" usage", html, text, summary)
}

// resourceList advertises the app templates (MCP Apps model) that are wired.
func (s *Server) resourceList() []any {
	out := []any{}
	if s.Insights != nil {
		out = append(out, map[string]any{
			"uri":         insightsResourceURI,
			"name":        product.Name() + " insights",
			"description": "Graphical dashboard of the organization's measured technique outcomes.",
			"mimeType":    appProfileMime,
			"_meta":       uiResourceMeta(),
		})
	}
	if s.Map != nil {
		out = append(out, map[string]any{
			"uri":         mapResourceURI,
			"name":        product.Name() + " playbook map",
			"description": "Interactive map of the organization's playbook and its areas of practice.",
			"mimeType":    appProfileMime,
			"_meta":       uiResourceMeta(),
		})
	}
	if s.Drafts != nil {
		out = append(out, map[string]any{
			"uri":         draftsResourceURI,
			"name":        product.Name() + " drafts review",
			"description": "Interactive review of the draft techniques awaiting a promote-or-reject decision.",
			"mimeType":    appProfileMime,
			"_meta":       uiResourceMeta(),
		})
	}
	if s.Usage != nil {
		out = append(out, map[string]any{
			"uri":         usageResourceURI,
			"name":        "Your " + product.Name() + " usage",
			"description": "Graphical view of your own " + product.Name() + " usage over time — personal and machine-local.",
			"mimeType":    appProfileMime,
			"_meta":       uiResourceMeta(),
		})
	}
	return out
}

// uiResourceMeta is the render configuration hosts read off the UI resource. The
// app paints its own surface, so it asks for no host border; it loads nothing
// external, so the host's default (locked-down) CSP is left in place. The frame
// size is negotiated at runtime — the app reports its rendered height over the
// ui/initialize handshake — but mcp-ui hosts size the iframe before any script
// runs, so give them a sensible first box (a dashboard, not a chat bubble).
func uiResourceMeta() map[string]any {
	return map[string]any{
		"ui":                                map[string]any{"prefersBorder": false},
		"mcpui.dev/ui-preferred-frame-size": []any{"900px", "720px"},
	}
}

// readResource serves an app template for MCP Apps hosts that fetch it via
// resources/read: the insights dashboard (default window) or the technique map.
func (s *Server) readResource(req request, uri string) *response {
	var html string
	var err error
	switch {
	case s.Insights != nil && uri == insightsResourceURI:
		html, _, _, err = s.Insights("")
	case s.Map != nil && uri == mapResourceURI:
		html, _, _, err = s.Map()
	case s.Drafts != nil && uri == draftsResourceURI:
		html, _, _, err = s.Drafts()
	case s.Usage != nil && uri == usageResourceURI:
		html, _, _, err = s.Usage("")
	default:
		return fail(req, -32602, "resource not found")
	}
	if err != nil {
		return fail(req, -32603, "app unavailable: "+err.Error())
	}
	return ok(req, map[string]any{
		"contents": []any{map[string]any{
			"uri":      uri,
			"mimeType": appProfileMime,
			"text":     html,
			"_meta":    uiResourceMeta(),
		}},
	})
}

func ok(req request, result any) *response {
	return &response{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func fail(req request, code int, msg string) *response {
	return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}}
}

// search runs retrieval and renders the pulled techniques with their evidence.
func (s *Server) search(query string) (text string, isError bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "query is required", true
	}
	// Mint the id first so the evidence request carries it and the registry
	// records a fact for this pull — surface "mcp" marks it as the ask-path,
	// distinguishable from push-path audits in the fact log.
	auditID := audit.NewAuditID()
	block, err := s.Search(contracts.Characterization{
		AuditID:     auditID,
		SummaryText: query,
		Surface:     "mcp",
		Segment:     s.Segment,
	})
	if err != nil {
		return "registry unavailable: " + err.Error(), true
	}
	if len(block.Candidates) == 0 {
		return "Playbook search returned zero matching techniques.", false
	}

	var b strings.Builder
	// NOT "highest relevance first", which this said for as long as it has
	// existed. The registry orders candidates by measured impact, with
	// similarity only as a tiebreak, and selectWithExploration deliberately
	// reserves a slot for a technique that is not top-ranked at all
	// (retrieval.go). Nor does the block promise relevance: the similarity
	// floor is off by default, so a query matching nothing still fills it.
	fmt.Fprintf(&b, "Playbook techniques retrieved for %q. Ordered by measured impact, "+
		"not by how well they match — judge each against the task before offering it:\n", query)
	for i, c := range block.Candidates {
		if i >= 3 {
			break
		}
		fmt.Fprintf(&b, "\n%d. %s  [%s · %s]\n", i+1, c.Name, c.TechniqueID, scopeOf(c))
		if ev := evidence(c.Outcomes); ev != "" {
			b.WriteString("   " + ev + "\n")
		} else {
			b.WriteString("   awaiting measured outcomes (new or rarely tried)\n")
		}
		if c.AppliesWhen != "" {
			b.WriteString("   when: " + strings.TrimSpace(c.AppliesWhen) + "\n")
		}
		if c.NotWhen != "" {
			b.WriteString("   not when: " + strings.TrimSpace(c.NotWhen) + "\n")
		}
		if c.Recipe != "" {
			b.WriteString("   recipe: " + strings.TrimSpace(c.Recipe) + "\n")
		}
	}
	// A search is a PULL, not a member-facing delivery. The agent (or member)
	// asked to browse the playbook, and an agent may pull many candidates it will
	// never act on — it can't act on a dozen suggestions at once. Emitting `shown`
	// per result would inflate the funnel's denominator and depress the adoption /
	// helped rates that represent OpenTacit's actual value, so a pull records NO funnel
	// event. `shown` is reserved for techniques a MEMBER actually sees: the push path's
	// presented set (audit.ShownEvents) and the one technique an @tacit answer cites
	// (mention.go, at explicit confidence). The search still leaves an AuditFact
	// (surface="mcp") recording that it happened.
	return b.String(), false
}

func scopeOf(c contracts.EvidenceCandidate) string {
	if c.Scope == "" {
		return "general"
	}
	return c.Scope
}

// evidence renders the ask path's evidence line. The rendering lives in the
// contracts package, shared with the ambient hook block: a technique must not be
// described one way when OpenTacit offers it and another way when the member
// asks for it.
func evidence(outcomes map[string]any) string {
	return contracts.EvidenceLine(outcomes)
}
