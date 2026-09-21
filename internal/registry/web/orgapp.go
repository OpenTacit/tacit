// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The organization app endpoint: GET /v1/organization/app — the machine
// counterpart of the Organization dashboard view, as /v1/insights/app is for
// Insights. It feeds the tacit_org MCP tool and the /tacit:org skills: a
// chat-friendly text summary plus a structured summary for the model.
// Text-first — no HTML panel yet; the tool renders as text everywhere, and a
// graphical MCP app can follow the insights app's pattern when it earns it.
//
// Disclosure posture: everything here is cohort-aggregate, already visible
// to any keyed member on the dashboard; the registry stores no identities to
// leak. The text carries the dashboard's own framing (cohort-level routes,
// never individual rankings) so transcripts keep the norm legible.
package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/organization"
)

func (s *Server) handleOrganizationApp(w http.ResponseWriter, r *http.Request) {
	in, err := s.readAnalyticsInputs(true)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	events, techniques, facts := in.events, in.techniques, in.facts
	now := time.Now().UTC()
	win := insights.WindowByKey(r.URL.Query().Get("w"), now, insights.Earliest(events))
	// Areas are the Playbook map's groups, the same grouping the Outcomes view
	// renders — the tool and the dashboard must say the same thing.
	rep := organization.Compute(techniques, events, facts, now, win, s.mapAreaOf(techniques, events), r.URL.Query().Get("dimension"))
	s.sendAppJSON(w, r, map[string]any{
		"window":  win.Key,
		"html":    "", // text-first; see the package comment
		"text":    OrganizationAppText(rep),
		"summary": organizationSummary(rep),
	})
}

// OrganizationAppText renders the report the way a member should read it in
// chat: short, numbers verbatim, cohorts never individuals.
func OrganizationAppText(rep organization.Report) string {
	o := rep.Overview
	var b strings.Builder
	fmt.Fprintf(&b, "Organization · window %s", o.Window.Key)
	if rep.Lens != "" {
		fmt.Fprintf(&b, ", grouped by %s", rep.Lens)
	}
	b.WriteString("\n")

	orgLine := "org-specific adoption: —"
	if share, n := insights.OrgShare(o.AdoptedByScope); n > 0 {
		orgLine = fmt.Sprintf("org-specific adoption: %.0f%% (n=%d)", share*100, n)
	}
	breadth := 0
	for _, c := range rep.Cohorts {
		if c.Dimension == rep.Lens && c.Funnel.Adopted > 0 {
			breadth++
		}
	}
	fmt.Fprintf(&b, "%s · active breadth: %d %s cohorts adopting\n", orgLine, breadth, rep.Lens)

	areaByKey := map[string]organization.Area{}
	for _, a := range rep.Areas {
		areaByKey[a.Key] = a
	}
	if len(rep.Spread) > 0 {
		hasPrev := rep.Overview.Window.PrevStart.Before(rep.Overview.Window.Start)
		spread := append([]organization.Spread(nil), rep.Spread...)
		sort.Slice(spread, func(i, j int) bool { return spread[i].CurrentAdopted > spread[j].CurrentAdopted })
		b.WriteString("Spread (areas by adopting cohorts):\n")
		for i, sp := range spread {
			if i == 5 {
				fmt.Fprintf(&b, "  … %d more areas\n", len(spread)-5)
				break
			}
			delta := ""
			if hasPrev {
				delta = fmt.Sprintf(" (Δ%+d)", sp.Current-sp.Previous)
			}
			fmt.Fprintf(&b, "  %s: %d cohorts%s · %d adoptions\n", areaByKey[sp.Area].Label, sp.Current, delta, sp.CurrentAdopted)
		}
	}
	if len(rep.Opportunities) > 0 {
		b.WriteString("Opportunities (shown in this cohort and adopted with helped outcomes in peer cohorts):\n")
		for i, op := range rep.Opportunities {
			if i == 3 {
				fmt.Fprintf(&b, "  … %d more\n", len(rep.Opportunities)-3)
				break
			}
			fmt.Fprintf(&b, "  %s · %s: %d of %d adopted in this cohort; peer cohorts adopted %d with %d helped\n",
				op.Cohort, areaByKey[op.Area].Label, op.Target.Adopted, op.Target.Shown, op.Peer.Adopted, op.Peer.Helped)
		}
	}
	if len(rep.Proponents) > 0 {
		b.WriteString("Proponents (cohorts with established use; no individual data):\n")
		for i, pr := range rep.Proponents {
			if i == 3 {
				fmt.Fprintf(&b, "  … %d more\n", len(rep.Proponents)-3)
				break
			}
			hr, _ := pr.Funnel.HelpedRate()
			fmt.Fprintf(&b, "  %s · %s: helped %.0f%% · n=%d\n", pr.Cohort, areaByKey[pr.Area].Label, hr*100, pr.Funnel.Adopted)
		}
	}
	h := rep.Health
	fmt.Fprintf(&b, "Health: %d live techniques (%d org-scoped) · %d drafts awaiting review", h.Live, h.OrganizationTechniques, h.Drafts)
	if h.Audit.Total > 0 {
		fmt.Fprintf(&b, " · session grouping %d/%d facts", h.Audit.WithSession, h.Audit.Total)
	}
	b.WriteString("\nTechnique-usage detail (funnel, helped rate, evidence mix): tacit_insights / the Outcomes view.\n")
	return b.String()
}

// organizationSummary is the model-facing structured form.
func organizationSummary(rep organization.Report) map[string]any {
	areaByKey := map[string]organization.Area{}
	for _, a := range rep.Areas {
		areaByKey[a.Key] = a
	}
	breadth := 0
	for _, c := range rep.Cohorts {
		if c.Dimension == rep.Lens && c.Funnel.Adopted > 0 {
			breadth++
		}
	}
	spread := make([]map[string]any, 0, len(rep.Spread))
	for _, sp := range rep.Spread {
		spread = append(spread, map[string]any{
			"area": areaByKey[sp.Area].Label, "cohorts": sp.Current,
			"previous_cohorts": sp.Previous, "adopted": sp.CurrentAdopted,
		})
	}
	opps := make([]map[string]any, 0, len(rep.Opportunities))
	for _, op := range rep.Opportunities {
		opps = append(opps, map[string]any{
			"cohort": op.Cohort, "area": areaByKey[op.Area].Label,
			"target_shown": op.Target.Shown, "target_adopted": op.Target.Adopted,
			"peer_adopted": op.Peer.Adopted, "peer_helped": op.Peer.Helped,
		})
	}
	props := make([]map[string]any, 0, len(rep.Proponents))
	for _, pr := range rep.Proponents {
		hr, _ := pr.Funnel.HelpedRate()
		props = append(props, map[string]any{
			"cohort": pr.Cohort, "area": areaByKey[pr.Area].Label,
			"helped_rate": hr, "adopted": pr.Funnel.Adopted,
		})
	}
	return map[string]any{
		"window": rep.Overview.Window.Key, "lens": rep.Lens, "active_breadth": breadth,
		"spread": spread, "opportunities": opps, "proponents": props,
		"health": map[string]any{
			"live": rep.Health.Live, "org_techniques": rep.Health.OrganizationTechniques,
			"drafts": rep.Health.Drafts, "audit_facts": rep.Health.Audit.Total,
			"facts_with_session": rep.Health.Audit.WithSession,
		},
	}
}
