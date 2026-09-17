// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Publishing this machine's summaries to the member's sealed ledger.
//
// The Usage page can read a member's numbers directly only when the registry
// and the agent share a host. Everywhere else the page has nothing, and the
// member's own record sits on a disk their phone cannot reach. This is the
// write half of the answer: the agent seals what it already computes and files
// it under an address derived from the member's own credential, so the registry
// stores something it cannot read and the member's browser can open it from
// anywhere (docs/delivery/out-of-band-plan.md).
//
// Three things this must not become:
//
//   - A second source of truth. The payload is exactly what the two loopback
//     feeds already serve — UsageSummary and WorkSummary — so the page renders
//     it with the code it already has, and a figure cannot mean one thing
//     locally and another remotely.
//   - A leak. Everything here is the same derived, countable material those
//     feeds carry: no prompt text, no completions, no file contents, no
//     transcripts. Sealing is the second line, not the first.
//   - A cost on the hook path. Publishing happens on a timer and at shutdown,
//     never during a turn, and a failure is logged once and forgotten. A member
//     whose registry is unreachable loses a refresh, not a session.
package hooks

import (
	"encoding/json"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/windows"
)

// LedgerSchema versions the sealed payload. The browser refuses a schema it
// does not know rather than rendering fields it half-understands.
const LedgerSchema = 1

// LedgerPublishEvery is how often a running agent refreshes its slot. It is
// generous on purpose: this is a page somebody opens occasionally, not a live
// feed, and the shutdown publish already catches the end of any real session.
const LedgerPublishEvery = 10 * time.Minute

// LedgerRetryAfter is how soon a failed publish is tried again. Short, because
// the usual cause is a registry that has not finished starting or a network
// that has not arrived, and both resolve in seconds.
const LedgerRetryAfter = 30 * time.Second

// ledgerWindow is one window's pair of summaries, named as the page's two feeds
// name them.
type ledgerWindow struct {
	Usage UsageSummary `json:"usage"`
	Work  WorkSummary  `json:"work"`
}

// ledgerPayload is what gets sealed. Every window ships together, because the
// member switching period on the page should not wait for a machine that may be
// asleep to publish again.
type ledgerPayload struct {
	Schema    int                     `json:"schema"`
	Machine   string                  `json:"machine"`
	Published string                  `json:"published"`
	Windows   map[string]ledgerWindow `json:"windows"`
}

// MachineLabel names this machine's slot in the ledger. The hostname is what a
// member recognises when two laptops disagree, and it is already visible to
// every service on the network, so it reveals nothing the ledger did not
// already have to carry to be useful.
func MachineLabel() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	h = unsafeLabelChars.ReplaceAllString(h, "-")
	h = strings.Trim(h, "-._")
	if h == "" {
		return "unknown"
	}
	if len(h) > 63 {
		h = h[:63]
	}
	return h
}

var unsafeLabelChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Summarizer is whatever can answer the two questions the payload is made of.
// The running agent answers them from the logs it already holds; a one-shot
// command answers them by reading the same files off disk. One payload builder
// over both, so a publish forced from the command line cannot disagree with the
// one the daemon makes.
type Summarizer interface {
	UsageSummary(window time.Duration) UsageSummary
	WorkSummary(window time.Duration) WorkSummary
}

// LedgerPayload builds the sealed payload's plaintext: every window, both
// summaries, and which machine said so.
func LedgerPayload(s Summarizer, now time.Time) ([]byte, error) {
	payload := ledgerPayload{
		Schema:    LedgerSchema,
		Machine:   MachineLabel(),
		Published: now.UTC().Format(time.RFC3339),
		Windows:   make(map[string]ledgerWindow, len(windows.Keys)),
	}
	for _, k := range windows.Keys {
		d := ParseUsageWindow(k)
		payload.Windows[k] = ledgerWindow{Usage: s.UsageSummary(d), Work: s.WorkSummary(d)}
	}
	return json.Marshal(payload)
}

// FileSummarizer answers from the member-local logs on disk rather than from a
// running agent. It is what `tacit usage --publish` uses, so a change can be
// looked at in the dashboard without waiting for the daemon's next timer.
type FileSummarizer struct {
	UsageLogPath string
	StateDir     string
	Now          func() time.Time
}

func (f FileSummarizer) UsageSummary(window time.Duration) UsageSummary {
	return loadUsageLog(f.UsageLogPath, f.Now).summarize(window)
}

func (f FileSummarizer) WorkSummary(window time.Duration) WorkSummary {
	detail, daily := sessionLogPaths(f.StateDir)
	sum := loadSessionLog(detail, daily, f.Now).summarizeWork(window)
	sum.Quotas = ReadQuota(f.StateDir, f.Now)
	sum.QuotaHistory = ReadQuotaHistory(f.StateDir, f.Now)
	sum.WorkHistory = ReadWorkHistory(f.StateDir, f.Now)
	sum.Landed = LandedFilings(f.StateDir, nowOr(f.Now))
	return sum
}

// nowOr is the summarizer's clock, or the wall clock where it has none. Every
// other reader here takes a func(); LandedFilings takes an instant, because it
// is answering "is this reading still worth showing" rather than bucketing.
func nowOr(now func() time.Time) time.Time {
	if now != nil {
		return now()
	}
	return time.Now()
}

// PublishLedger seals this machine's summaries and hands them to the sink. It
// is safe to call at any time and does nothing at all when no sink is
// configured, which is the state of every agent whose member never connected to
// a registry.
func (a *Agent) PublishLedger() error {
	if a.opts.PublishLedger == nil {
		return nil
	}
	body, err := LedgerPayload(a, a.opts.Now())
	if err != nil {
		return err
	}
	return a.opts.PublishLedger(body)
}

// publishLedgerQuietly is the call the lifecycle makes when nothing will act on
// the outcome. Publishing is a convenience for a page nobody is looking at yet,
// so a failure is worth one line in the log and nothing more — never a retry
// storm against a registry that is down, and never a reason to hold up a
// shutdown.
func (a *Agent) publishLedgerQuietly(why string) { _ = a.publishLedgerOK(why) }

// publishLedgerOK is the same call for a caller that wants to know, so the
// timer can come back soon after a failure instead of waiting out the full
// interval. A missing sink counts as success: there is nothing to retry.
func (a *Agent) publishLedgerOK(why string) bool {
	if a.opts.PublishLedger == nil {
		return true
	}
	if err := a.PublishLedger(); err != nil {
		log.Printf("[tacit-hooks] usage ledger not published (%s): %v", why, err)
		return false
	}
	return true
}
