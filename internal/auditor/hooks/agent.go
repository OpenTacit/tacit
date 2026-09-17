// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package hooks is the in-harness hook agent — the persistent local process
// the harness POSTs hook events to (docs/harness/in-harness-hooks.md).
//
// Agent receives Claude Code / Codex CLI / Amp hook events (one route per
// harness), folds them into a canonical record, asks the registry for
// evidence, and delivers a coaching suggestion in-flow — while never letting
// audit latency touch the member's turn. The capture/gate/synthesis core is
// harness-neutral; only the capture reader differs per harness (the delivery
// envelope converged: camelCase on Claude Code and Codex, verified live; Amp
// has no shell hooks, so its plugin adapter speaks the same vocabulary over
// the relay and translates the envelope back into plugin-API returns).
//
// The delivery-latency contract: the audit fires at Stop (the only point where
// a turn is complete enough to characterize) and waits up to a small budget
// for a brief synthesis so the suggestion lands on the SAME turn, right under
// the answer it's about. A slow LLM keeps running and parks its result for the
// next UserPromptSubmit instead (graceful fallback) — Stop never blocks longer
// than the budget.
package hooks

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/auditor/audit"
	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/llm"
	"github.com/opentacit/tacit/internal/auditor/sessionhash"
	"github.com/opentacit/tacit/internal/modelid"
)

// hooksDebug, when TACIT_HOOKS_DEBUG is set, logs why a Stop audit did or did
// not surface a suggestion — the single most common "nothing showed" question.
var hooksDebug = os.Getenv("TACIT_HOOKS_DEBUG") != ""

func dbg(format string, args ...any) {
	if hooksDebug {
		log.Printf("[tacit-hooks:debug] "+format, args...)
	}
}

// Synthesizer is the one LLM capability the hook agent needs.
type Synthesizer interface {
	Synthesize(transcript string, evidence contracts.EvidenceBlock, brief bool) (string, error)
}

// UpgradeReporter is optionally implemented by the LLM (llm.Auto) to signal
// that adding a key would sharpen suggestions.
type UpgradeReporter interface {
	UpgradeAvailable() bool
}

// ShouldIdleExit is the pure daemon-shutdown decision: quiet long enough and
// not mid-synthesis. idleSecs<=0 disables idle-exit.
func ShouldIdleExit(now, lastActivity time.Time, idleSecs float64, busy bool) bool {
	return idleSecs > 0 && !busy && now.Sub(lastActivity).Seconds() >= idleSecs
}

type pending struct {
	auditID   string
	text      string
	technique contracts.EvidenceCandidate
	rank      int
	char      contracts.Characterization
	silent    bool // deliver additionalContext-only (autonomous application)
}

type sessionState struct {
	key     string // "harness:session_id" (sketch pseudonymization input)
	hashKey string // empty when the harness supplied no real session id
	capture *capture.HookCapture
	pending *pending
	// pendingSelfTest holds a self-test block for a harness with no visible
	// Stop channel (parkOnly): it rides the next prompt, like a parked
	// suggestion, so the test exercises the delivery this member really gets.
	pendingSelfTest  string
	lastPrompt       string // trigger fallback for sketches (never persisted)
	lastSummary      string // last characterization summary (sketch trigger)
	client           string // "terminal"|"bridge" as the relay saw it (DetectClient)
	questionsOffered int    // AskUserQuestion offers observed (E2 funnel)
	synthesizing     bool
	// synthesizingSince stamps the reservation, so a synthesis that never
	// returns cannot pin the daemon awake for ever. See Busy.
	synthesizingSince time.Time
	// enriching is the tools_absent single-flight guard. Observation is
	// un-gated, so without this every rapid-fire Stop would stack one Haiku
	// call each; one in flight per session bounds the worst case, and a
	// skipped fact simply stays unenriched — absence of the inference is an
	// honest state (contracts.AuditFactEnrichment).
	enriching bool
	shownIDs  map[string]bool
	// sugList is this session's shown suggestions, in order, with their
	// evolving verdicts — the review list behind /v1/hooks/suggestions
	// (surfaced by the opencode sidebar; a toast is transient, this is not).
	// Member-local, loopback-only: it never leaves the machine.
	sugList         []*ShownSuggestion
	suggestionsMade int
	// Fit-check declines. Without these, a session where retrieval found techniques
	// and the model rejected every one is indistinguishable from a quiet day:
	// `shown` stays 0 and no other counter moves. Same contract as the LLM and
	// registry health fields above — broken, or merely fussy, must never look
	// like idle. Local-only, like every counter here.
	declinedCount    int
	lastDeclined     string // technique_id of the most recent non-fit
	turnsSinceShown  int
	turn             int              // UserPromptSubmit turns seen (helped-confirmation clock)
	lastShown        []shownTechnique // for adoption detection
	lastAuditID      string
	lastSegment      contracts.Segment // segment the last suggestion was shown with
	adoptedIDs       map[string]bool
	adoptions        map[string]*adoption // behavioral adoptions awaiting a helped verdict
	reactedIDs       map[string]bool
	helpedIDs        map[string]bool // a helped event (explicit or inferred) already emitted
	upgradeHintShown bool
	// stats is this session's countable shape (sessionlog.go), folded into the
	// member-local session log at every Stop; corrections counts the turns that
	// opened by putting the agent right (corrections.go). Both are counts, on
	// this machine only.
	stats            sessionStats
	correctionsCount int
	// The correction that just crossed the repetition threshold, if any: the
	// live occurrence is what a draft gets written from, because the ledger
	// keeps only hashes (corrections.go).
	lastCorrectionHash  string
	lastCorrectionCount int
	// Workflow discovery (docs/learning/workflow-technique-capture.md): the session's
	// ordered phase trace (task_types), whether it reached a verified-good state,
	// and a once-per-session guard. Phases are structural (already in audit facts),
	// never content; only a scrubbed, distilled workflow sketch ever leaves.
	phaseSeq        []string
	verifiedSession bool
	workflowEmitted bool
	// The Amp model lookup (ampmodel.go): a recorded turn to ask the thread
	// about, whether an ask is already in flight, and how many times a thread
	// that never answers has been asked.
	ampModelDue     bool
	ampModelPending bool
	ampModelTries   int
}

type shownTechnique struct{ capID, name, recipe string }

// adoption records a behavioral adoption still awaiting an outcome, so a
// surviving one can be promoted to an inferred 'helped' with the SAME audit id
// and segment the adoption carried (keeping the funnel attributable and the
// helped-implies-adopted dedupe consistent).
type adoption struct {
	turn      int
	auditID   string
	segment   contracts.Segment
	technique shownTechnique
}

// feedbackSegment returns the cohort tags to stamp on this session's
// adopted/helped/dismissed events: the same segment the suggestion was shown
// with (the capture enriches it, e.g. with harness), overlaid on the configured
// base for any keys the shown segment lacked. Keeping shown and its downstream
// stages on one segment is what makes adoption attributable by cohort — without
// it, only 'shown' carries the harness and adopt/helped rates can't be
// broken out.
func (st *sessionState) feedbackSegment(base contracts.Segment) contracts.Segment {
	seg := contracts.Segment{}
	for k, v := range base {
		seg[k] = v
	}
	for k, v := range st.lastSegment {
		seg[k] = v
	}
	return seg
}

func newSessionState(c *capture.HookCapture) *sessionState {
	return &sessionState{
		capture: c, shownIDs: map[string]bool{}, adoptedIDs: map[string]bool{},
		adoptions:       map[string]*adoption{},
		reactedIDs:      map[string]bool{},
		helpedIDs:       map[string]bool{},
		turnsSinceShown: 1 << 20, // large so the first turn clears the cooldown
	}
}

// Options configures an Agent. Zero values take the documented defaults;
// CooldownTurns and CooldownFor: -1 means "no cooldown" (0 is the unset
// sentinel).
type Options struct {
	// The unsolicited-suggestion budget (throttle.go). A suggestion needs BOTH
	// cooldowns to have cleared, and the rolling window to have room. It is the
	// MEMBER's budget, not a session's, and it is rebuilt from the usage log at
	// startup so a daemon restart cannot refill it.
	CooldownTurns int
	CooldownFor   time.Duration
	MaxPerWindow  int
	Window        time.Duration
	// MaxFitChecks caps how many candidates are fit-checked (one LLM call each,
	// fanned out) per suggestion — a burst bound against rate limits. 0 -> 4.
	MaxFitChecks int
	SynthBudget  time.Duration
	Segment      contracts.Segment
	// RunAsync runs a background task; tests inject an inline runner for
	// deterministic synchronous behavior. Defaults to `go fn()`.
	RunAsync func(fn func())
	// Now is injectable for idle-exit tests. Defaults to time.Now.
	Now func() time.Time
	// Enrich, when set, posts the model-inferred half of an audit fact back to
	// the registry (tools_absent). Runs AFTER the turn is delivered and is never
	// awaited — see enrichFactAsync.
	Enrich func(contracts.AuditFactEnrichment) error
	// TechniqueMemoryPath persists the member-local technique memory ("" -> in-memory
	// only). Member-local by design — see techniquememory.go.
	TechniqueMemoryPath string
	// Paused reports whether this machine is paused, asked ONCE PER EVENT so a
	// pause takes hold on the next turn rather than the next restart. nil means
	// never paused, which is what a test wants and what an agent with no home
	// directory falls back to.
	Paused func() bool
	// UsageLogPath persists the member-local usage history ("" -> in-memory
	// only). Member-local by design — see usagelog.go.
	UsageLogPath string
	// StateDir holds the member-local state that is not one of the named paths
	// above: the session log, its rollups, the correction ledger ("" ->
	// in-memory only). Passed in rather than derived from UsageLogPath, whose
	// directory is the member's home directory.
	StateDir string
	// Sketch, when set, is the member's opt-in mining channel: it receives
	// scrubbed technique sketches at first-adoption moments (sketch.go).
	Sketch      SketchSink
	SketchSalt  string // per-org salt for session pseudonymization
	SessionSalt string // optional per-org salt for audit fact session pseudonymization
	// Drafts, when set, reports the registry's review-queue depth for status
	// lines. Called off the hook path, cached (DraftsTTL); nil disables it.
	Drafts func() (int, error)
	// Access, when set, reports the registry's own account of what kind of
	// instance it is: mode is private|global|staged and tunnel is up|down while
	// Global Access is on (docs/distribution/global-access-plan.md). It comes from
	// the REGISTRY rather than from the URL this machine happens to dial, because
	// a member on the office network reaches a globally-proxied registry at a
	// private address and would otherwise be told the opposite of their colleague.
	// Called off the hook path, cached (AccessTTL); nil disables the marker.
	Access func() (mode, tunnel string, err error)
	// Cohorts, when set, reports the cohorts already in use on the registry —
	// member-settable dimensions only, values most-used first. It turns the
	// inline cohort ask from a blank page into a menu, which is the difference
	// between a member joining their team's cohort and inventing a second
	// spelling of it. Called off the hook path, cached (CohortsTTL); nil
	// leaves the ask in its generic form.
	Cohorts func() ([]CohortDim, error)
	// RegistryConfigured is true when this member has real registry settings
	// (not the compiled-in dev default). When false, a repo carrying a
	// .tacit/registry.toml marker earns a one-time join nudge at SessionStart
	// (docs/distribution/growth-plan.md mechanism 2).
	RegistryConfigured bool
	// RegistryURL keys the inline cohort ask (segmentAsk): a connected
	// member whose Segment is empty is prompted — at most once a day per
	// registry — to set one. Empty disables the ask.
	RegistryURL string

	// PublishLedger files this machine's sealed summaries with the registry so
	// the member can read their own numbers from a device that is not this one
	// (ledgerpublish.go). nil — the state of any machine that never connected —
	// disables publishing entirely, which is why this is a closure and not a
	// URL: the agent holds no credential and does no crypto, and cannot
	// accidentally publish without one being wired in.
	PublishLedger func(payload []byte) error
	// AskBudget bounds the @tacit advisor answer (mention.go): retrieval +
	// synthesis for a question the member is actively waiting on. Larger than
	// SynthBudget because explicit pull tolerates seconds; 0 -> 6s.
	AskBudget time.Duration
	// MentionBlock permits answering a PURE @tacit prompt via
	// decision:"block" on harnesses whose rendering is validated
	// (mentionBlockCapable). OFF by default: the CLI renders a block reason
	// but Claude Code's web/mobile clients render no hook output at all, and
	// the agent cannot tell which client is attached — so the default shape
	// makes the MODEL relay the answer (the one channel every client
	// renders). Terminal-only members opt in for the token-free answer.
	MentionBlock bool
}

// Agent is the capture -> gate -> budgeted-synthesis -> deliver core, plus
// feedback capture. Observe-only: it never returns a permissionDecision, so it
// cannot block or alter the member's work — with ONE narrow, documented
// exception: a prompt addressed entirely to OpenTacit ("@tacit …", mention.go)
// may be answered via decision:"block" on harnesses whose rendering is
// validated, because there the agent is not altering the member's work; it IS
// the addressee. Mixed prompts always pass through.
type Agent struct {
	evidence   audit.EvidenceProvider
	llm        Synthesizer
	feedback   func([]contracts.FeedbackEventDraft) error
	contribute func(map[string]any) (map[string]any, error)
	opts       Options

	mu           sync.Mutex
	sessions     map[string]*sessionState
	lastActivity time.Time

	// The member's unsolicited-suggestion budget (throttle.go). On the Agent,
	// not a session: a member with two terminals open is one member with one
	// attention span.
	throttle *throttle

	// The three registry facts the agent serves from memory (status.go): the
	// review-queue depth, the access mode, and the cohort directory. All three
	// keep their own lock, are refreshed in the background, are never awaited,
	// and are stale-served rather than blocking a member's turn on a registry
	// round-trip. nil when the matching Options seam is unset, which is what
	// makes the reader report "unknown" and the caller omit the indicator.
	drafts  *asyncCached[int]
	access  *asyncCached[accessKind]
	cohorts *asyncCached[[]CohortDim]

	// LLM health. The fit-check swallows every synthesis error so a broken model
	// can never break a member's turn — correct, but on its own it means OpenTacit
	// goes SILENT rather than degraded, and silence is indistinguishable from
	// "nothing was relevant today". Nothing anywhere reported that the LLM had
	// stopped working. So record it: the failure is swallowed, never hidden.
	llmState   llm.Diagnosis
	llmErrors  int
	llmErrorAt time.Time
	llmOKAt    time.Time

	// Registry health, same rationale as LLM health: every registry error is
	// swallowed so an outage or a revoked key can never break a member's turn —
	// but a member whose key was rotated out from under them would otherwise
	// see OpenTacit simply go quiet, forever. Recorded here, surfaced by
	// /v1/hooks/stats, worn by the status line.
	regState      string // "" (never called) · "ok" · "unauthorized" · "unreachable"
	regConsecErrs int    // consecutive failures; a lone blip is not a fault
	regErrorAt    time.Time
	regOKAt       time.Time

	// memory is the member-LOCAL verdict record (techniquememory.go): what this
	// member adopted or dismissed, across sessions, on their machine only.
	memory *techniqueMemory

	// usage is the member-LOCAL activity history (usagelog.go): this member's
	// own queries/shown/adopted/helped/dismissed over time, on their machine
	// only — the data behind the Usage view. Never nil after NewAgent.
	usage *usageLog

	// sessions log and corrections ledger, both member-LOCAL for the same
	// reason as usage (sessionlog.go, corrections.go): the countable shape of
	// how this member works, and how often they have had to say the same thing
	// twice. Neither leaves the machine; both are derived counts, never text.
	sessionLog *sessionLog
	quota      *quotaFile
	// localSalt keys the member's OWN session log, which never leaves the
	// machine (localkey.go). Not opts.SessionSalt: that one is the org's, it is
	// optional, and where it was absent every local record was silently
	// dropped.
	localSalt   string
	corrections *correctionLedger

	// selfTest is the armed delivery self-test (selftest.go): one block, on
	// request, through the real channel, so a member can see whether OpenTacit is
	// visible in their session at all.
	selfTest selfTestState
}

// NewAgent wires the dependency-injected seams.
func NewAgent(evidence audit.EvidenceProvider, model Synthesizer,
	feedbackSink func([]contracts.FeedbackEventDraft) error,
	contributeSink func(map[string]any) (map[string]any, error), opts Options) *Agent {

	if opts.MaxPerWindow == 0 {
		opts.MaxPerWindow = defaultMaxPerWindow
	}
	if opts.Window == 0 {
		opts.Window = defaultWindow
	}
	if opts.MaxFitChecks <= 0 {
		opts.MaxFitChecks = 4
	}
	if opts.CooldownTurns == 0 {
		opts.CooldownTurns = defaultCooldownTurns
	} else if opts.CooldownTurns < 0 {
		opts.CooldownTurns = 0
	}
	if opts.CooldownFor == 0 {
		opts.CooldownFor = defaultCooldownFor
	} else if opts.CooldownFor < 0 {
		opts.CooldownFor = 0
	}
	memory := loadTechniqueMemory(opts.TechniqueMemoryPath)
	if opts.SynthBudget == 0 {
		opts.SynthBudget = 4 * time.Second
	}
	if opts.AskBudget == 0 {
		opts.AskBudget = 6 * time.Second
	}
	if opts.RunAsync == nil {
		opts.RunAsync = func(fn func()) { go fn() }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	// After opts.Now is finalised, so the log shares the agent's clock (tests
	// inject one for deterministic timestamps).
	usage := loadUsageLog(opts.UsageLogPath, opts.Now)
	detailPath, dailyPath := sessionLogPaths(opts.StateDir)
	sessions := loadSessionLog(detailPath, dailyPath, opts.Now)
	corrections := loadCorrectionLedger(correctionLedgerPath(opts.StateDir),
		opts.SessionSalt, opts.Now)
	if feedbackSink == nil {
		feedbackSink = func([]contracts.FeedbackEventDraft) error { return nil }
	}
	if contributeSink == nil {
		contributeSink = func(map[string]any) (map[string]any, error) {
			return map[string]any{"error": "contribution not wired"}, nil
		}
	}
	// Seeded from the usage log, so a daemon that idle-exited mid-cooldown
	// comes back still owing it. This is the difference between a real ceiling
	// and the per-session one it replaced, which any pause longer than the
	// 15-minute idle-exit silently refunded.
	th := newThrottle(opts.CooldownTurns, opts.CooldownFor, opts.MaxPerWindow, opts.Window, opts.Now)
	th.seed(usage.throttleState(opts.Window))
	a := &Agent{
		evidence: evidence, llm: model, feedback: feedbackSink, contribute: contributeSink,
		opts: opts, sessions: map[string]*sessionState{}, memory: memory, usage: usage,
		sessionLog: sessions, corrections: corrections,
		quota:        newQuotaFile(opts.StateDir, opts.Now),
		localSalt:    resolveLocalSalt(opts.SessionSalt, opts.StateDir),
		lastActivity: opts.Now(), throttle: th,
	}
	// Each cache exists only when its seam is wired, so an unwired fact reads
	// as unknown instead of as a zero the status line would render.
	if opts.Drafts != nil {
		a.drafts = newAsyncCached(DraftsTTL, opts.Now, opts.RunAsync, opts.Drafts)
	}
	if opts.Access != nil {
		a.access = newAsyncCached(AccessTTL, opts.Now, opts.RunAsync,
			func() (accessKind, error) {
				mode, tunnel, err := opts.Access()
				return accessKind{mode: mode, tunnel: tunnel}, err
			})
	}
	if opts.Cohorts != nil {
		a.cohorts = newAsyncCached(CohortsTTL, opts.Now, opts.RunAsync, opts.Cohorts)
	}
	return a
}

// Flush compacts every member-local file the agent writes. Safe to call more
// than once, and safe on an agent with no home directory (each log is
// in-memory only and the call is a no-op).
func (a *Agent) Flush() {
	a.sessionLog.flush()
	a.corrections.flush()
}

// Touch records hook activity for the idle-exit decision.
func (a *Agent) Touch() {
	a.mu.Lock()
	a.lastActivity = a.opts.Now()
	a.mu.Unlock()
}

// LastActivity returns the most recent hook time.
func (a *Agent) LastActivity() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastActivity
}

// Busy reports whether any synthesis is in flight — don't idle-exit and drop
// a pending suggestion. Under the lock: the watchdog calls this while Handle
// may be mutating sessions.
// synthesisCeiling bounds how long a reservation may keep the daemon awake.
// Four times the budget: long enough that a slow-but-working pipeline still
// parks its result for the next prompt, short enough that a provider which has
// stopped answering cannot hold the daemon open until its HTTP timeout — eight
// goroutines against a 120-second timeout used to do exactly that.
//
// It deliberately is not SynthBudget itself. The budget bounds what the MEMBER
// waits for; parking has to outlive it, or a slow turn would lose its
// suggestion instead of receiving it on the next prompt.
func (a *Agent) synthesisCeiling() time.Duration { return 4 * a.opts.SynthBudget }

// Busy reports whether any session is mid-synthesis, which is what holds off
// idle exit. A reservation older than the ceiling does not count: the work may
// still be blocked on a provider, but the daemon has waited long enough, and
// exiting reclaims the goroutine with the process.
func (a *Agent) Busy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.opts.Now()
	for _, st := range a.sessions {
		if !st.synthesizing {
			continue
		}
		if ceiling := a.synthesisCeiling(); ceiling > 0 &&
			!st.synthesizingSince.IsZero() && now.Sub(st.synthesizingSince) > ceiling {
			continue // stale reservation: stopped counting as busy
		}
		return true
	}
	return false
}

// SessionCount reports live sessions (health endpoint).
func (a *Agent) SessionCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sessions)
}

// Handle processes one hook payload and returns the JSON body the harness
// expects ({} for no-op). Cursor's response vocabulary differs from the
// Claude-converged envelope, so its responses pass through
// TranslateCursorResponse on the way out (the payload was canonicalized in
// place, so its event name is readable after handle returns).
func (a *Agent) Handle(payload map[string]any, harness string) HookResponse {
	resp := a.handle(payload, harness)
	if harness == "cursor" {
		event, _ := payload["hook_event_name"].(string)
		resp = TranslateCursorResponse(event, resp)
	}
	return resp
}

func (a *Agent) handle(payload map[string]any, harness string) HookResponse {
	a.Touch()
	// Paused means watch nothing: no capture, no retrieval, no delivery, and
	// no memory of the turn. Checked before the payload is even translated,
	// because a pause a member cannot verify is not a pause — the promise is
	// that nothing is observed, not that nothing is shown.
	//
	// Asking directly still works. The MCP tools and `tacit ask` never come
	// through here, and a member who pauses the interruptions has not asked to
	// lose the playbook.
	if a.opts.Paused != nil && a.opts.Paused() {
		return nil
	}
	payload = translateHarnessPayload(payload, harness)
	event, _ := payload["hook_event_name"].(string)
	sid, _ := payload["session_id"].(string)
	hashKey := ""
	if sid != "" {
		hashKey = harness + ":" + sid
	}
	if sid == "" {
		sid = "unknown"
	}
	key := harness + ":" + sid // namespaced: both harnesses at once can't collide

	return a.dispatch(a.decide(payload, event, key, hashKey, harness))
}

// decide is the whole locked half of a turn: fold the payload into the session
// and work out what to do about it. The lock is taken here and released by
// defer, so no branch below can leave the daemon holding it. Everything slow —
// the registry, the model, the formatting — happens afterwards, in dispatch
// (agent_events.go).
func (a *Agent) decide(payload map[string]any, event, key, hashKey, harness string) decision {
	a.mu.Lock()
	defer a.mu.Unlock()

	st := a.sessions[key]
	if st == nil {
		st = newSessionState(newHarnessCapture(harness, a.opts.Segment))
		st.key = key
		st.hashKey = hashKey
		// A session outlives this process. The agent idle-exits after fifteen
		// quiet minutes and the member carries on working, so the next hook
		// arrives at an instance that has never heard of a session already
		// hours old. Starting it at zero was not merely a gap: the record this
		// instance wrote at its next Stop REPLACED the fuller one, and the
		// session kept only what happened after the restart.
		if hashKey != "" {
			if prior, ok := a.sessionLog.lookup(sessionhash.Hash(a.localSalt, hashKey)); ok {
				st.stats.resume(prior)
				st.correctionsCount = prior.Corrections
				// The turn counter comes back too. "A correction needs
				// something to correct" is a fact about the SESSION, and
				// reading it off this process made it a fact about the
				// process: the agent idle-exits every fifteen quiet minutes
				// while the member works on, so the first thing said after
				// each restart was treated as an opening instruction and
				// never counted, however plainly it was a repair.
				st.turn = prior.Turns
			}
		}
		a.sessions[key] = st
	}
	st.stats.observe(a.opts.Now(), event, payload)
	// Where the member reads this session, as the relay saw it (DetectClient).
	// Last writer wins: a session can gain a bridge partway through, and the
	// most recent event is the best evidence. Recorded, never routed on — see
	// DetectClient's note on what is still unsettled.
	if client, _ := payload["tacit_client"].(string); client != "" {
		st.client = client
	}
	st.capture.Apply(payload)

	switch event {
	case capture.EvUserPrompt:
		return a.decidePromptLocked(st, payload, harness)
	case capture.EvPreTool:
		return a.decidePreToolLocked(st, payload, harness)
	case capture.EvPostTool:
		return a.decidePostToolLocked(st, payload, harness)
	case capture.EvSessionEnd:
		a.recordSessionLocked(st, harness)
		delete(a.sessions, key) // Codex has no SessionEnd — idle-exit reaps it
		return decision{}
	case capture.EvStop:
		// Upserted at every Stop, not only at SessionEnd: Codex never sends
		// one, and a laptop lid closes. A session recorded up to its last
		// completed turn is the honest version of "what happened"; a session
		// recorded only when it ends cleanly would quietly drop the long ones.
		a.recordSessionLocked(st, harness)
		return a.decideStopLocked(st, harness)
	case capture.EvSessionStart:
		return a.decideSessionStartLocked(payload, harness)
	default: // PostToolUse / anything else
		return decision{}
	}
}

// recordSessionLocked folds this session's running counts into the
// member-local session log. Caller holds a.mu.
//
// The key is the session HASH, never the id: the log is a record of how the
// member works, and it needs to tell two sessions apart without naming either.
// A session with no id at all is skipped rather than collected under one
// shared key, which would blend unrelated work into a single fictional
// session.
func (a *Agent) recordSessionLocked(st *sessionState, harness string) {
	if st.hashKey == "" || st.stats.turns == 0 {
		return
	}
	key := sessionhash.Hash(a.localSalt, st.hashKey)
	a.sessionLog.upsert(st.stats.record(key, harness, st.capture.Model,
		st.capture.Cwd, st.correctionsCount))
	// Amp names the model nowhere a hook can see it, so the session asks its
	// own thread — off this lock, in dispatch, because that answer is a
	// subprocess and a network read away (ampmodel.go).
	if harness == "amp" && st.capture.Model == "" {
		st.ampModelDue = true
	}
}

// HandleStatusLine folds one status-line payload into the session it belongs
// to, and writes the account's allowance to its own file.
//
// The status line is rendered several times a second and is handed the whole
// session: what it cost, how long it has been working, how many lines it has
// written, how full the context and the prompt cache are, and how much of the
// account's allowance is gone. Until this, `tacit statusline` decoded the
// session id out of that and dropped the rest — which is to say the four
// columns a member most wants were arriving on loopback, already parsed, and
// being thrown away.
//
// What it does NOT do is create a session. A status line renders before the
// first prompt and goes on rendering after the last one; a session the hooks
// have never seen has nothing to attach these figures to, and inventing one
// would put a cost against no turns. The quota is written either way, because
// it belongs to the account rather than to any session.
func (a *Agent) HandleStatusLine(payload map[string]any) {
	in := capture.ReadStatusLine(payload)
	if in.HasFiveHour || in.HasSevenDay || in.HasSpend {
		q := Quota{}
		if in.HasFiveHour {
			q.FiveHour = &QuotaWindow{UsedPct: in.FiveHourPct, ResetsAt: in.FiveHourAt}
		}
		if in.HasSevenDay {
			q.SevenDay = &QuotaWindow{UsedPct: in.SevenDayPct, ResetsAt: in.SevenDayAt}
		}
		if in.HasSpend {
			q.Spend = &QuotaWindow{UsedPct: in.SpendPct, ResetsAt: in.SpendAt}
		}
		// Whose allowance it is, taken from the model the line was rendering.
		// A member with a Claude session and a Codex session has two of them,
		// and one row for both would let each overwrite the other on every
		// render.
		//
		// The vendor where one is recognised, and the model's own family where
		// none is: a provider this build has never heard of still gets a row of
		// its own. Keying an unknown one under "" would have made every
		// unrecognised provider share a row with every other, which is the same
		// bug one row further down.
		id := modelid.Normalize(in.Model)
		source := id.Vendor
		if source == "" {
			source = id.Family
		}
		a.quota.write(source, q)
		// And a point in the history, where this reading says something the
		// last one did not. The work counters are a walk of the session log, so
		// they are fetched only if a point is actually due.
		a.quota.sample(source, q, a.sessionLog.counts)
	}
	if in.SessionID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// By session id alone, the way StatsFor matches. A status line knows which
	// session it is rendering and not which of this agent's harness routes the
	// hooks came in on, and guessing that prefix is the kind of assumption that
	// fails silently: the quota still lands, the session figures never do.
	for key, st := range a.sessions {
		if !strings.HasSuffix(key, ":"+in.SessionID) {
			continue
		}
		st.stats.noteStatus(in, in.Measured())
		// Upserted here rather than waiting for the next Stop: a session can
		// run for an hour with the status line reporting all the while, and a
		// member who opens the page during it should read what it has cost so
		// far.
		harness, _, _ := strings.Cut(key, ":")
		a.recordSessionLocked(st, harness)
		return
	}
}

// HandleContribution accepts a member-drafted technique, stamps the
// contributor's cohort (so the registry records who contributed, without any
// raw transcript), and forwards to the registry, which stores it as a
// held-out draft for review.
func (a *Agent) HandleContribution(body map[string]any) (bool, map[string]any) {
	a.Touch()
	for _, field := range []string{"name", "description", "recipe"} {
		if s, _ := body[field].(string); strings.TrimSpace(s) == "" {
			return false, map[string]any{"error": field + " is required"}
		}
	}
	technique := map[string]any{}
	for k, v := range body {
		technique[k] = v
	}
	if _, ok := technique["segment"]; !ok { // attribute the draft to this cohort
		technique["segment"] = a.opts.Segment
	}
	resp, err := a.contribute(technique)
	if err != nil { // surface, never crash the turn
		return false, map[string]any{"error": "registry contribution failed: " + err.Error()}
	}
	if e, _ := resp["error"].(string); e != "" {
		return false, resp
	}
	return true, resp
}
