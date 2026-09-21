// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit doctor --deliver — the last hop `--harness X` cannot check: whether a
// OpenTacit suggestion actually RENDERS where the member is sitting. The hop check
// proves the wiring, the relay, the agent and the registry. This arms the agent
// so the next Stop hook of a live session delivers a labelled block through the
// real channel; the member's own eyes are the assertion.
//
// The two are halves of one question ("do suggestions reach me?"), which is why
// they share doctor rather than standing as separate commands.
//
// `--deliver-status` reads the other half back: whether the agent delivered,
// parked or suppressed the last armed test. A member who saw nothing learns
// which of the two failures they have — a client that renders no hook output,
// or a Stop hook that never fired.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/hooks"
	"github.com/opentacit/tacit/internal/product"
)

func runDelivery(harnessName string, status, context bool) int {
	harness, statusOnly := &harnessName, status
	mode := hooks.ModeBlock
	if context {
		mode = hooks.ModeContext
	}

	cfg := auditorconfig.Load()
	base := fmt.Sprintf("http://%s:%d", cfg.HooksHost, cfg.HooksPort)
	hc := &http.Client{Timeout: 5 * time.Second}
	check := func(b string) bool {
		resp, err := hc.Get(b + "/v1/hooks/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}

	if statusOnly {
		return selfTestStatus(hc, base, check)
	}

	// The same connect-or-spawn a real hook performs: in a live session the
	// agent is already up, but arming from a cold shell must not fail for that.
	relay := hooks.RelayConfig{BaseURL: base, APIKey: cfg.HooksAPIKey, SpawnLog: cfg.HooksSpawnLog}
	if !hooks.EnsureUp(base, check, func() error { return hooks.SpawnAgent(relay) }, time.Sleep, 8) {
		fmt.Fprintf(os.Stderr, "hook agent did not come up on %s — check the spawn log (%s)\n",
			base, cfg.HooksSpawnLog)
		return 1
	}

	body, _ := json.Marshal(map[string]string{"harness": *harness, "mode": mode})
	req, err := http.NewRequest("POST", base+"/v1/hooks/selftest", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.HooksAPIKey != "" {
		req.Header.Set("X-Tacit-Key", cfg.HooksAPIKey)
	}
	resp, err := hc.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot arm the self-test on %s (%v)\n", base, err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		fmt.Fprintln(os.Stderr, "hook agent rejected the request (401) — its --hooks-key does not match TACIT_HOOKS_API_KEY")
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "hook agent answered %d\n", resp.StatusCode)
		return 1
	}
	var armed struct {
		ExpiresAt string `json:"expires_at"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&armed)

	fmt.Printf("hook agent up on %s\n", base)
	if mode == hooks.ModeContext {
		// The model-facing probe. Its reading comes from the MEMBER, not from
		// this command and not from the model: two independent observations,
		// because either one alone is unreadable. A chip with no context
		// received is impossible; context received with no chip is the result
		// tier 2 needs; neither appearing means the channel never fired and the
		// test says nothing at all.
		fmt.Printf("armed: the next prompt runs the tier-2 loop once (expires %s)\n", armed.ExpiresAt)
		fmt.Println()
		fmt.Println("Send any prompt in a FRESH session. Hook context asks the model to raise one")
		fmt.Println("AskUserQuestion, so the client SHOWS a suggestion as a native form — the delivery")
		fmt.Println("that tier 2 proposed. The form carries placeholders, never a real technique. Check two points:")
		fmt.Println()
		fmt.Println("  1. Make sure the form appears and that it reads as an offer of a suggestion.")
		fmt.Println("     Answer it — " + product.Name() + " captures the answer on the live route, not a test one.")
		fmt.Println("  2. Look for a context chip, banner, or pill that names tacit, and check")
		fmt.Println("     if it stays. That leak caused the tier-2 revert. A form that")
		fmt.Println("     arrives beside a visible directive is not a usable channel.")
		fmt.Println()
		fmt.Println("Then: tacit doctor --deliver-status   (whether the form reached the capture path)")
		return 0
	}
	fmt.Printf("armed: the next turn's Stop hook delivers one ◆ %s block (expires %s)\n", product.Name(), armed.ExpiresAt)
	fmt.Println("End the turn and read the reply. If no block appears, run `tacit doctor --deliver-status`.")

	// The form, for the skill to carry. It goes out on every arming because it
	// probes a channel neither of the others reaches: the block is hook output
	// (dropped by web and mobile), the relay is model output (always renders,
	// proves nothing about push), and the form is a native prompt the client
	// draws itself. Printed rather than described, so the model carries a spec
	// it did not write.
	fmt.Println()
	fmt.Println("--- FORM (verbatim) ---")
	fmt.Print(hooks.SelfTestFormSpec())
	fmt.Println("--- END FORM ---")

	// A web or mobile client renders no hook output, so the armed block would
	// reach the terminal and nobody else. This command runs as a subprocess of
	// the session, so it can see the bridge the agent's own delivery cannot act
	// on, and hand the model a block to carry. The member then sees one either
	// way — hook-delivered on a terminal, relayed here — and the closing line
	// inside the block says which, so the two are never confused.
	if hooks.DetectClient(os.Getenv) == "bridge" {
		fmt.Println()
		fmt.Println("client: web or mobile (Remote Control) — it renders no hook output, so the")
		fmt.Println("armed block will reach the terminal only. Reproduce the RELAY BLOCK below")
		fmt.Println("VERBATIM in your final reply, so a block shows here too. Do not edit or")
		fmt.Println("re-word it, and do not write one of your own if this section is absent.")
		fmt.Println()
		fmt.Println("--- RELAY BLOCK (verbatim) ---")
		fmt.Println(hooks.SelfTestRelayBlock(*harness))
		fmt.Println("--- END RELAY BLOCK ---")
	}
	return 0
}

// selfTestStatus prints the agent's record of the last armed test.
func selfTestStatus(hc *http.Client, base string, check func(string) bool) int {
	if !check(base) {
		fmt.Printf("the hook agent is not up on %s — nothing is armed, because an armed self-test starts the agent.\n", base)
		return 1
	}
	resp, err := hc.Get(base + "/v1/hooks/stats")
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent stats unreadable (%v)\n", err)
		return 1
	}
	defer resp.Body.Close()
	var stats struct {
		SelfTest struct {
			Armed bool `json:"armed"`
			Last  *struct {
				Outcome string `json:"outcome"`
				At      string `json:"at"`
				Harness string `json:"harness"`
			} `json:"last"`
		} `json:"selftest"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		fmt.Fprintf(os.Stderr, "agent stats unparseable (%v)\n", err)
		return 1
	}
	s := stats.SelfTest
	switch {
	case s.Armed:
		fmt.Println("a self-test is armed and waits — it fires at the end of a turn, so end this one.")
	case s.Last == nil:
		fmt.Println("no self-test has run on this agent. Run `tacit doctor --deliver` to arm one.")
	default:
		fmt.Printf("last self-test: %s (%s, %s)\n", s.Last.Outcome, s.Last.Harness, s.Last.At)
		switch {
		case strings.HasPrefix(s.Last.Outcome, "context-form-answered"):
			// The whole tier-2 loop, machine side: directive out, form raised,
			// answer back through the live PreToolUse/PostToolUse capture. What
			// this cannot see is whether the directive was visible while it
			// happened, which is the half that decides the design.
			fmt.Println("The full loop ran: the directive reached the model, the model raised the form,")
			fmt.Println("and your answer came back through the same capture a live suggestion uses.")
			fmt.Println("Form-based delivery works mechanically. Its USABILITY depends on the other")
			fmt.Println("half — if a context chip was visible while it happened.")
			return 0
		}
		switch s.Last.Outcome {
		case "delivered":
			fmt.Println("The agent returned the block to the harness. If you did not see it, the harness or")
			fmt.Println("client dropped it — Claude Code's web and mobile clients render no hook output, and")
			fmt.Println("suggestions will not reach you there. Use the terminal, or ask for techniques directly.")
		case "parked":
			fmt.Println("This harness renders nothing at a turn's end, so the block goes with your next message.")
		case "suppressed":
			fmt.Println("This harness renders no hook output at all, so " + product.Name() + " never pushes here. Pull instead:")
			fmt.Println("ask for a technique by name, or run the review skill.")
		case "context-delivered":
			fmt.Printf("%s sent the directive, and no %s form followed. The model received the\n", product.Name(), product.Name())
			fmt.Println("ask and did not act on it — the channel works, the delivery does not. Ask the")
			fmt.Println("model if it saw the instruction: if it did, the model refused a directive,")
			fmt.Println("which is the primary risk in form-based delivery.")
		case "context-form-offered":
			fmt.Println("The model raised the form and " + product.Name() + " captured its offer, but no answer came back.")
			fmt.Println("Either the form is still on screen, or you dismissed it without a choice.")
		case "expired":
			fmt.Println("The armed test timed out before any turn ended. Arm it again and finish a turn.")
		}
	}
	return 0
}
