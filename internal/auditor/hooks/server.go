// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The HTTP shell around the Agent (thin; the interesting behavior lives in
// agent.go), plus the idle watchdog that gives the daemon its on-demand
// lifecycle: created by the relay when needed, allowed to stop when quiet.

package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Handler builds the loopback HTTP surface. The agent is loopback-bound and
// per-machine, so the key is defense-in-depth, not the security boundary:
// when apiKey is empty, auth is open (zero-setup local dogfood); when set,
// it's required on every /v1/hooks/* request.
func Handler(agent *Agent, apiKey string) http.Handler {
	mux := http.NewServeMux()

	authed := func(r *http.Request) bool {
		return apiKey == "" || r.Header.Get("X-Tacit-Key") == apiKey
	}
	sendJSON := func(w http.ResponseWriter, code int, obj any) {
		body, _ := json.Marshal(obj)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	}
	readBody := func(r *http.Request) (map[string]any, bool) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, false
		}
		return body, true
	}

	mux.HandleFunc("GET /v1/hooks/health", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, 200, map[string]any{"ok": true, "sessions": agent.SessionCount()})
	})

	// Status-line feed: open like health (loopback; counters only, no content).
	mux.HandleFunc("GET /v1/hooks/stats", func(w http.ResponseWriter, r *http.Request) {
		totals, session := agent.StatsFor(r.URL.Query().Get("session_id"))
		resp := map[string]any{"sessions": agent.SessionCount(),
			"shown": totals.Shown, "adopted": totals.Adopted, "offered": totals.Offered,
			// Declines belong next to shown, not buried: a fit-check rejecting every
			// candidate is the one failure mode that moves no other counter here.
			"declined": totals.Declined}
		if session != nil {
			resp["session"] = session
		}
		if drafts, ok := agent.DraftsCount(); ok {
			resp["drafts"] = drafts
		}
		// LLM health, with the remedy. Without this, a dead model looks exactly
		// like a quiet day: suggestions simply stop, and no counter moves. This is
		// the line an operator reads to find out that OpenTacit is not working and what
		// to do about it.
		d, errCount, lastError, lastOK := agent.LLMHealth()
		health := map[string]any{"state": d.State, "errors": errCount}
		if d.Detail != "" {
			health["detail"] = d.Detail
		}
		if d.Remedy != "" {
			health["remedy"] = d.Remedy
		}
		if d.NeedsOperator {
			health["needs_operator"] = true
		}
		if !lastError.IsZero() {
			health["last_error_at"] = lastError.UTC().Format(time.RFC3339)
		}
		if !lastOK.IsZero() {
			health["last_ok_at"] = lastOK.UTC().Format(time.RFC3339)
		}
		resp["llm"] = health
		// Registry health, same contract as llm: broken must look different
		// from quiet. "unauthorized" is the rotated-key case — the member's
		// hooks fire, every call 401s, and without this line nothing anywhere
		// would ever say so.
		regState, regNeeds, regErr, regOK := agent.RegistryHealth()
		reg := map[string]any{"state": regState}
		if regNeeds {
			reg["needs_operator"] = true
			reg["remedy"] = "Run `tacit doctor`; a rejected key needs `tacit connect --registry <url> --key <new-key>`."
		}
		if !regErr.IsZero() {
			reg["last_error_at"] = regErr.UTC().Format(time.RFC3339)
		}
		if !regOK.IsZero() {
			reg["last_ok_at"] = regOK.UTC().Format(time.RFC3339)
		}
		// What KIND of registry this is, for the ambient marker. Absent when the
		// registry has not answered yet or predates the field, which the status
		// line reads as "say nothing" rather than as "private".
		if mode, tunnel, ok := agent.AccessMode(); ok && mode != "" {
			reg["access"] = mode
			if tunnel != "" {
				reg["tunnel"] = tunnel
			}
		}
		resp["registry"] = reg
		// The delivery self-test's record (selftest.go): armed, and what became
		// of the last one. When a member saw no block, this is the line that
		// separates "the agent delivered it and your client dropped it" from
		// "the Stop hook never reached the agent".
		resp["selftest"] = agent.SelfTestStatus()
		sendJSON(w, 200, resp)
	})

	// Usage view feed: this member's own activity history (usagelog.go),
	// aggregated for the dashboard's Usage view. Open like /stats (loopback;
	// counters only, no content) — but the reader is a BROWSER page served by
	// the registry, a different origin from this loopback agent, so it needs
	// CORS. usageCORS reflects the page's origin (no secret to protect — the
	// only reader that can reach 127.0.0.1 is on the same machine) and answers
	// the Private Network Access preflight so an HTTPS dashboard (served over a
	// Funnel) may still fetch http://127.0.0.1.
	usageCORS := func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "X-Tacit-Key")
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
	}
	mux.HandleFunc("OPTIONS /v1/hooks/usage", func(w http.ResponseWriter, r *http.Request) {
		usageCORS(w, r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/hooks/usage", func(w http.ResponseWriter, r *http.Request) {
		usageCORS(w, r)
		sendJSON(w, 200, agent.UsageSummary(ParseUsageWindow(r.URL.Query().Get("window"))))
	})

	// The same feed, one question over: not how the member uses OpenTacit but how
	// they work — sessions, turns, tools, retries, cost, by model and project
	// (sessionlog.go). Same origin story, same CORS, same locality: it is the
	// member's own machine answering about the member's own machine.
	mux.HandleFunc("OPTIONS /v1/hooks/sessions", func(w http.ResponseWriter, r *http.Request) {
		usageCORS(w, r)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/hooks/sessions", func(w http.ResponseWriter, r *http.Request) {
		usageCORS(w, r)
		sendJSON(w, 200, agent.WorkSummary(ParseUsageWindow(r.URL.Query().Get("window"))))
	})

	// The status line, posting back what it was handed.
	//
	// Claude Code renders the status line several times a second and gives it
	// the whole session — cost, lines written, context, prompt cache, the
	// account's allowance. `tacit statusline` was decoding the session id out
	// of that and discarding the rest, which is why the Usage page could not
	// answer what a session cost unless a gateway happened to say.
	//
	// Authed like every other POST here, and answering with {} whatever
	// happens: the status line runs under a tight timeout on the member's
	// terminal, and an agent having a bad day must never turn into a render
	// that stalls.
	mux.HandleFunc("POST /v1/hooks/statusline", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			sendJSON(w, 401, map[string]string{})
			return
		}
		body, ok := readBody(r)
		if !ok {
			sendJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		agent.HandleStatusLine(body)
		sendJSON(w, 200, map[string]string{})
	})

	// The member-facing page. Same document the MCP app serves, at an address a
	// person can open, because the org's dashboard cannot serve these numbers
	// and should hand off rather than apologise
	// (docs/delivery/out-of-band-plan.md, Phase 0).
	//
	// Open, like the two feeds above and for the same reason: anything that can
	// reach this port can already read /v1/hooks/usage, so the page adds a
	// renderer and not an exposure. no-store because the document IS the data —
	// a cached copy is a stale answer to "how did this week go".
	mux.HandleFunc("GET /usage", func(w http.ResponseWriter, r *http.Request) {
		window := r.URL.Query().Get("w")
		if window == "" {
			window = r.URL.Query().Get("window") // the feeds' spelling, accepted here too
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, agent.UsageAppHTML(window))
	})

	// A member who types the agent's address alone has one thing here to look
	// at; send them to it rather than to a 404.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/usage", http.StatusFound)
	})

	// One route per row of the harness table (harness.go). The path segments
	// are the frozen part of the contract; adding a harness there routes it
	// here for free.
	for _, harness := range harnessNames() {
		mux.HandleFunc("POST /v1/hooks/"+harness, func(w http.ResponseWriter, r *http.Request) {
			if !authed(r) {
				// The relay forwards whatever body it gets straight to the
				// harness, so a key mismatch must answer with the same clean
				// no-op ({}) every other failure produces. The 401 status keeps
				// the fault diagnosable by hand (curl, doctor).
				sendJSON(w, 401, map[string]string{})
				return
			}
			body, ok := readBody(r)
			if !ok {
				sendJSON(w, 400, map[string]string{"error": "invalid JSON"})
				return
			}
			// The relay's consumer classification (human vs autonomous
			// session) rides a header, not the harness payload; fold it in
			// so the capture can stamp the segment dimension.
			if consumer := r.Header.Get("X-Tacit-Consumer"); consumer != "" {
				body["tacit_consumer"] = consumer
			}
			// Where the member reads this session (DetectClient). Recorded
			// only — no delivery decision reads it yet — so that real sessions
			// answer whether hook subprocesses receive the bridge env var at
			// all. Read it back from GET /v1/hooks/stats?session_id=…
			if client := r.Header.Get("X-Tacit-Client"); client != "" {
				body["tacit_client"] = client
			}
			sendJSON(w, 200, agent.Handle(body, harness))
		})
	}

	// The review list: what was suggested this session and what became of it.
	// Feeds persistent surfaces (the opencode sidebar) that outlive a toast.
	mux.HandleFunc("GET /v1/hooks/suggestions", func(w http.ResponseWriter, r *http.Request) {
		list := agent.SuggestionsFor(r.URL.Query().Get("session_id"))
		if list == nil {
			list = []*ShownSuggestion{}
		}
		sendJSON(w, 200, map[string]any{"suggestions": list})
	})

	// The advisor's pull seam: skills and scripts ask the same engine the
	// @tacit mention uses, so ranking, funnel recording, and voice stay one
	// thing (docs/harness/advisor-mention-plan.md A1).
	mux.HandleFunc("POST /v1/hooks/ask", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			sendJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		body, okBody := readBody(r)
		if !okBody {
			sendJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		ok, resp := agent.HandleAsk(body)
		code := 200
		if !ok {
			code = 400
		}
		sendJSON(w, code, resp)
	})

	// Arm the delivery self-test: the next Stop hook (of the named harness, or
	// of whichever session stops first) returns a block through the real
	// channel. `tacit hook-selftest` and the /tacit:testfeedback command are
	// the callers; nothing is retrieved and no funnel event is recorded.
	mux.HandleFunc("POST /v1/hooks/selftest", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			sendJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		harness, mode := "", ""
		if body, ok := readBody(r); ok {
			harness, _ = body["harness"].(string)
			mode, _ = body["mode"].(string)
		}
		expires := agent.ArmSelfTest(harness, mode)
		resp := map[string]any{"armed": true, "mode": agent.SelfTestStatus()["mode"],
			"expires_at": expires.UTC().Format(time.RFC3339)}
		if harness != "" {
			resp["harness"] = harness
		}
		sendJSON(w, 200, resp)
	})

	mux.HandleFunc("POST /v1/hooks/feedback", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			sendJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		body, okBody := readBody(r)
		if !okBody {
			sendJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		ok, resp := agent.HandleFeedback(body)
		code := 200
		if !ok {
			code = 400
		}
		sendJSON(w, code, resp)
	})

	mux.HandleFunc("POST /v1/hooks/contribute", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			sendJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		body, okBody := readBody(r)
		if !okBody {
			sendJSON(w, 400, map[string]string{"error": "invalid JSON"})
			return
		}
		ok, resp := agent.HandleContribution(body)
		code := http.StatusCreated
		if !ok {
			code = 400
		}
		sendJSON(w, code, resp)
	})

	return mux
}

// Serve runs the hook agent until the idle watchdog (or ctx) stops it.
// idleSecs<=0 disables idle-exit (stay up until killed).
func Serve(ctx context.Context, agent *Agent, host string, port int, apiKey string, idleSecs int) error {
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: Handler(agent, apiKey),
	}
	auth := "auth: OPEN (loopback, no key)"
	if apiKey != "" {
		auth = "auth: key required"
	}
	life := "no idle-exit"
	if idleSecs > 0 {
		life = fmt.Sprintf("idle-exit %ds", idleSecs)
	}
	log.Printf("[tacit-hooks] serving on http://%s (max %d per %s, cooldown %d turns + %s; %s; %s)",
		srv.Addr, agent.opts.MaxPerWindow, agent.opts.Window,
		agent.opts.CooldownTurns, agent.opts.CooldownFor, auth, life)

	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()

	if idleSecs > 0 {
		go func() {
			step := time.Duration(min(30, max(5, idleSecs))) * time.Second
			ticker := time.NewTicker(step)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if ShouldIdleExit(time.Now(), agent.LastActivity(), float64(idleSecs), agent.Busy()) {
						log.Printf("[tacit-hooks] idle for %ds; shutting down (relay will restart on the next hook).", idleSecs)
						_ = srv.Shutdown(context.Background())
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Keep the member's sealed ledger fresh while the agent runs, so a page
	// opened on another device is not looking at whenever this machine last
	// shut down. Off the request path, and silent on failure.
	if agent.opts.PublishLedger != nil {
		go func() {
			// The first publish routinely loses a race: the relay starts this
			// agent and the registry it publishes to may be a service that is
			// still coming up, or a laptop that has not found the network yet.
			// Settling straight into the ten-minute cadence after that would
			// leave the member's page ten minutes stale for no reason, so a
			// failure retries soon and a success goes back to the slow clock.
			delay := LedgerPublishEvery
			if !agent.publishLedgerOK("startup") {
				delay = LedgerRetryAfter
			}
			for {
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
					if agent.publishLedgerOK("timer") {
						delay = LedgerPublishEvery
					} else {
						delay = LedgerRetryAfter
					}
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}
		}()
	}

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	err := <-done
	// Compact the member-local files on the way out. Both are append-a-state
	// logs, so a daemon that ran all day leaves one line per turn behind;
	// without this they only shrink on the next start, which on a machine that
	// idle-exits many times a day is always later than it should be.
	agent.Flush()
	// The last word on this machine's slot: a session that just ended is
	// exactly what somebody opening the page tonight wants to see.
	agent.publishLedgerQuietly("shutdown")
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
