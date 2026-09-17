// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import (
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/auditor/capture"
)

// The join is a session's totals against what that session handed work to, and
// the whole of getting it right is not claiming more than that.
//
// Cost is measured a session at a time. A session that called a subagent once
// and then worked for an hour is not an hour of subagent, so what is reported
// is the totals of the SESSIONS that used a thing — which is a real
// measurement and a weaker one than the reader wants.
func TestDelegationsCarryTheSessionsTotalsAndSayNoMore(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l := loadSessionLog(detail, daily, fixedClock(now))

	one := rec("s1", now, "anthropic/claude-opus-5", "tacit", 10)
	one.CostUSD, one.InTokens, one.OutTokens = 4.00, 90000, 3000
	one.Tools = map[string]int{"Task": 2, "Skill": 1, "mcp__tacit__search": 5}
	one.ToolDetail = map[string]map[string]int{
		"Task":  {"Explore": 2},
		"Skill": {"tacit:review": 1},
	}
	l.upsert(one)

	two := rec("s2", now, "anthropic/claude-opus-5", "tacit", 4)
	two.CostUSD, two.InTokens = 1.00, 20000
	two.Tools = map[string]int{"Task": 1, "mcp__other__ping": 2}
	two.ToolDetail = map[string]map[string]int{"Task": {"Explore": 1}}
	l.upsert(two)

	byName := map[string]DelegationUse{}
	for _, d := range l.summarizeWork(0).Delegations {
		byName[d.Kind+":"+d.Name] = d
	}
	ex := byName["agent:Explore"]
	if ex.Calls != 3 || ex.Sessions != 2 {
		t.Fatalf("Explore = %d calls over %d sessions, want 3 and 2", ex.Calls, ex.Sessions)
	}
	// Both sessions' whole cost, because both sessions used it — never a share
	// of one, which nothing here could compute.
	if ex.SessionCost != 5.00 {
		t.Fatalf("Explore's sessions came to %v, want 5.00", ex.SessionCost)
	}
	if ex.SessionTokens != 113000 {
		t.Fatalf("Explore's session tokens = %d, want 113000", ex.SessionTokens)
	}
	sk := byName["skill:tacit:review"]
	if sk.Calls != 1 || sk.SessionCost != 4.00 {
		t.Fatalf("the skill did not join: %+v", sk)
	}
	// An MCP server is the first half of the tool name, counted per server
	// because that is the unit a member wired.
	if srv := byName["mcp:tacit"]; srv.Calls != 5 || srv.Sessions != 1 {
		t.Fatalf("the MCP server did not join: %+v", srv)
	}
	if srv := byName["mcp:other"]; srv.Calls != 2 {
		t.Fatalf("a second server was missed: %+v", srv)
	}
	// Dearest first where anything measured a cost: that is the order the
	// question is asked in.
	rows := l.summarizeWork(0).Delegations
	if rows[0].SessionCost < rows[len(rows)-1].SessionCost {
		t.Errorf("delegations are not ordered by what their sessions came to: %+v", rows)
	}
}

// A window where nothing was delegated has no panel, rather than an empty one.
func TestNoDelegationsIsNoRows(t *testing.T) {
	_, detail, daily := sessionPaths(t)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	l := loadSessionLog(detail, daily, fixedClock(now))
	l.upsert(rec("plain", now, "anthropic/claude-opus-5", "tacit", 3))
	if got := l.summarizeWork(0).Delegations; len(got) != 0 {
		t.Fatalf("a session that delegated nothing produced %v", got)
	}
}

// The server is the unit, and the tool half of the name is already kept as the
// tool name itself.
func TestMCPServerIsTheFirstHalfOfTheName(t *testing.T) {
	for name, want := range map[string]string{
		"mcp__tacit__search":                   "tacit",
		"mcp__plugin_tacit_tacit__tacit_usage": "plugin_tacit_tacit",
		"Bash":                                 "",
		"mcp__bare":                            "bare",
	} {
		if got := capture.MCPServer(name); got != want {
			t.Errorf("MCPServer(%q) = %q, want %q", name, got, want)
		}
	}
}
