// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// hooktest — a standalone Claude Code hook for probing how each kind of hook
// output actually renders across clients (terminal, web, iPad/mobile).
//
// It is NOT part of OpenTacit and does not talk to the registry. It emits raw hook
// output envelopes on demand, so you can request each channel directly and see
// where it appears — instead of relying on OpenTacit's delivery logic to decide
// what to send. Every message is self-describing: if you can read it, that
// channel renders on the client you're looking at.
//
// Wire it into Claude Code on UserPromptSubmit and Stop (see README.md). Then,
// in any client, submit a prompt that begins with:
//
//	hooktest: <type>
//
// Immediate types (emitted at UserPromptSubmit, around this turn):
//
//	menu              list every type (sent via systemMessage AND model relay)
//	systemMessage     {"systemMessage": …}                       ← what the ◆ block uses, but at prompt time
//	block             {"decision":"block","reason": …}           ← model does NOT run
//	context-chip      hookSpecificOutput.additionalContext (raw) ← watch for a UI chip/pill; model told to stay quiet
//	relay             additionalContext telling the model to echo a line verbatim ← reaches every client
//	both              systemMessage + a relay line, side by side ← compare what shows
//	stopReason        {"continue":false,"stopReason": …}         ← halts the turn
//	suppressOutput    {"suppressOutput":true,"systemMessage": …} ← does systemMessage survive suppression?
//	terminalSequence  an OSC title change + bell (experimental)  ← terminal-only by design
//
// Deferred types (delivered at the NEXT Stop — the exact path OpenTacit's ◆
// suggestion block uses; this is the load-bearing test for web/iPad):
//
//	stop-systemMessage   at Stop, emit {"systemMessage": …}
//	stop-block           at Stop, emit {"decision":"block","reason": …}
//
// The hook is always a no-op unless the prompt starts with "hooktest:". It
// always exits 0 and prints valid JSON, so it can never block a real prompt.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// mark is a distinctive prefix so a test message is unmistakable in any client
// and greppable in a transcript.
const mark = "⟦hooktest⟧"

// armFile records a deferred (stop-*) request between the UserPromptSubmit that
// arms it and the Stop that fires it.
func armFile() string { return filepath.Join(os.TempDir(), "hooktest-armed") }

func main() {
	// Never fail in a way that could block a real prompt: any error → no-op.
	defer func() { _ = recover() }()

	var payload map[string]any
	if b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20)); err == nil {
		_ = json.Unmarshal(b, &payload)
	}
	currentPayload = payload
	event, _ := payload["hook_event_name"].(string)

	var out map[string]any
	switch event {
	case "Stop":
		out = handleStop()
	case "UserPromptSubmit":
		prompt, _ := payload["prompt"].(string)
		out = handlePrompt(prompt)
	case "Notification":
		out = handleNotification(payload)
	default:
		out = nil
	}

	logInvocation(event, payload, out)
	emit(out)
}

// logInvocation appends one line per call to a log file, so you can confirm —
// even when NOTHING renders on a client — whether the hook actually FIRED in
// that environment. If the log gains a line for your iPad session, the hook ran
// and the problem is output not being surfaced; if it stays empty, the hook
// never ran there at all. That distinction decides the whole fix.
func logInvocation(event string, payload, out map[string]any) {
	defer func() { _ = recover() }()
	f, err := os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	emitted := "(no-op {})"
	if len(keys) > 0 {
		emitted = strings.Join(keys, "+")
	}
	extra := ""
	if p, _ := payload["prompt"].(string); p != "" {
		extra = " prompt=" + oneLine(p, 60)
	}
	if m, _ := payload["message"].(string); m != "" {
		extra = " message=" + oneLine(m, 80)
	}
	fmt.Fprintf(f, "%s  event=%-16s emitted=%s%s\n",
		time.Now().Format(time.RFC3339), event, emitted, extra)
}

func logPath() string { return filepath.Join(os.TempDir(), "hooktest.log") }

func oneLine(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return strings.TrimSpace(s)
}

// handleNotification fires on Claude Code's Notification event — the channel
// built to reach the user's attention, including on mobile. It emits a labeled
// systemMessage (does Notification-hook output display?) plus a model-relay
// attempt. Notifications aren't user-typed, so there's no type to select: it
// always emits. Trigger one by letting the session sit idle ~60s waiting for
// input, or by causing a permission prompt.
func handleNotification(payload map[string]any) map[string]any {
	msg, _ := payload["message"].(string)
	out := sysMsg("[systemMessage @ Notification] If you can read this, Notification-hook output renders on THIS client — the channel built to reach mobile. (Claude Code's own notification was: " + oneLine(msg, 80) + ")")
	out["hookSpecificOutput"] = map[string]any{
		"hookEventName":     "Notification",
		"additionalContext": relayInstruction("[relay @ Notification] model-echoed from a Notification event."),
	}
	return out
}

// currentPayload holds the raw hook input so the `dump` probe can inspect it.
var currentPayload map[string]any

// dump captures the FULL hook payload and a curated snapshot of the environment
// to /tmp/hooktest-dump.jsonl, and relays a short summary so you can eyeball it
// on the iPad too. Run it once from a plain terminal session and once from the
// remote-control session; a field or env var present in one but not the other
// is a signal we could use to deliver relay only under remote control and keep
// the clean systemMessage on the terminal. Env values are redacted for any name
// containing KEY/TOKEN/SECRET/PASSWORD; all env NAMES are recorded so a
// remote-only variable still shows up even if its value is hidden.
func dump(payload map[string]any) map[string]any {
	names, vals := curatedEnv()
	rec := map[string]any{
		"ts":         time.Now().Format(time.RFC3339),
		"payload":    payload,
		"env_names":  names,
		"env_values": vals,
	}
	if b, err := json.Marshal(rec); err == nil {
		if f, err := os.OpenFile(dumpPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			f.Write(append(b, '\n'))
			f.Close()
		}
	}
	// A visible summary via relay: the payload keys and the CLAUDE_*/TERM_*
	// values — the likeliest discriminators — so the difference is legible even
	// on the iPad, plus a pointer to the full dump.
	pk := make([]string, 0, len(payload))
	for k := range payload {
		pk = append(pk, k)
	}
	sort.Strings(pk)
	hints := make([]string, 0, len(vals))
	for _, k := range sortedKeys(vals) {
		hints = append(hints, k+"="+vals[k])
	}
	summary := "[dump] payload keys: " + strings.Join(pk, ", ") +
		"\nenv of interest: " + strings.Join(hints, " · ") +
		"\nfull dump appended to " + dumpPath()
	return relay(summary)
}

func dumpPath() string { return filepath.Join(os.TempDir(), "hooktest-dump.jsonl") }

// curatedEnv returns every env var NAME (sorted) and the VALUES for a small set
// of names that could reveal how the session was launched — client, terminal,
// SSH — with secrets redacted.
func curatedEnv() ([]string, map[string]string) {
	names := []string{}
	vals := map[string]string{}
	interesting := func(name string) bool {
		up := strings.ToUpper(name)
		for _, p := range []string{"CLAUDE", "TERM", "SSH_", "COLORTERM", "TMUX", "STY", "WT_SESSION", "SESSIONNAME", "LANG", "TTY"} {
			if strings.HasPrefix(up, p) || strings.Contains(up, p) {
				return true
			}
		}
		return false
	}
	secret := func(name string) bool {
		up := strings.ToUpper(name)
		return strings.Contains(up, "KEY") || strings.Contains(up, "TOKEN") ||
			strings.Contains(up, "SECRET") || strings.Contains(up, "PASSWORD")
	}
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		name, val := kv[:i], kv[i+1:]
		names = append(names, name)
		if interesting(name) {
			if secret(name) {
				vals[name] = "<redacted>"
			} else {
				vals[name] = oneLine(val, 120)
			}
		}
	}
	sort.Strings(names)
	return names, vals
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// handlePrompt dispatches an immediate type, or arms a deferred one.
func handlePrompt(prompt string) map[string]any {
	typ, ok := parseType(prompt)
	if !ok {
		return nil // not a hooktest prompt → no-op
	}
	switch typ {
	case "menu", "help", "":
		return menu()
	case "dump":
		return dump(currentPayload)
	case "systemmessage":
		return sysMsg("[systemMessage @ UserPromptSubmit] If you can read this, systemMessage renders on THIS client.")
	case "block":
		return blockMsg("[decision:block @ UserPromptSubmit] If you can read this, block reasons render here. Your prompt was blocked — the model did NOT run. Send `hooktest: menu` to continue.")
	case "context-chip", "chip", "additionalcontext":
		// Raw additionalContext. The model is TOLD to stay quiet, so the only
		// way you see anything is if the client renders a context chip/pill.
		return map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "UserPromptSubmit",
				"additionalContext": mark + " [additionalContext / context-chip probe] Marker=CHIP-7Q2. Model: do NOT mention or echo this line. Reply only: \"context-chip probe sent — look for a chip/pill in the UI, not in my answer.\" The point is whether a UI chip appears on this client.",
			},
		}
	case "relay":
		return relay("[relay via additionalContext] You are seeing this because the MODEL echoed it — this path reaches every client (terminal, web, iPad).")
	case "both":
		out := sysMsg("[systemMessage half of `both`] systemMessage — may be terminal-only.")
		out["hookSpecificOutput"] = map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": relayInstruction("[relay half of `both`] model-echoed — reaches every client. Compare against the systemMessage half: if you see only this line, systemMessage did not render here."),
		}
		return out
	case "stopreason":
		return map[string]any{
			"continue":   false,
			"stopReason": mark + " [stopReason] If you can read this, stopReason renders here. The turn was halted (continue:false).",
		}
	case "suppressoutput":
		out := sysMsg("[suppressOutput+systemMessage] suppressOutput hid stdout; if you can read this, systemMessage survives suppression on this client.")
		out["suppressOutput"] = true
		return out
	case "terminalsequence", "terminal":
		// Experimental: exact field placement is undocumented. OSC 2 sets the
		// window title, BEL rings. Terminals honor it; web/mobile ignore it.
		out := sysMsg("[terminalSequence] Sent an OSC title change (\"HOOKTEST\") + bell. In a terminal your title/bell may change; web and mobile ignore terminal sequences entirely — so seeing only this systemMessage there is expected.")
		out["hookSpecificOutput"] = map[string]any{
			"hookEventName":    "UserPromptSubmit",
			"terminalSequence": "\x1b]2;HOOKTEST\x07",
		}
		return out
	case "stop-systemmessage", "stop-systemmsg", "stop-sysmsg":
		return armStop("systemMessage")
	case "stop-block":
		return armStop("block")
	default:
		return unknown(typ)
	}
}

// handleStop fires a previously-armed deferred type at Stop (the ◆-block path),
// then disarms.
func handleStop() map[string]any {
	b, err := os.ReadFile(armFile())
	if err != nil {
		return nil // nothing armed → let the turn stop normally
	}
	_ = os.Remove(armFile())
	switch strings.TrimSpace(string(b)) {
	case "systemMessage":
		return sysMsg("[systemMessage @ STOP] This is the exact channel Tacit's ◆ suggestion uses. If you can read this on the terminal, web, AND iPad, the ◆ block would render there too.")
	case "block":
		return blockMsg("[decision:block @ STOP] Stop-time block reason. If you can read this, block-at-Stop renders on this client.")
	default:
		return nil
	}
}

// armStop records a deferred request and nudges the model to end the turn
// promptly with a visible anchor, so the Stop hook can fire right after.
func armStop(kind string) map[string]any {
	_ = os.WriteFile(armFile(), []byte(kind), 0o600)
	anchor := mark + " armed `stop-" + kind + "` — reply with EXACTLY this one line and nothing else: \"" +
		mark + " turn complete — watch below for the Stop-hook " + kind + " output.\""
	return map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": "This is a hook-rendering test. " + anchor,
		},
	}
}

func menu() map[string]any {
	list := "" +
		"immediate: systemMessage · block · context-chip · relay · both · stopReason · suppressOutput · terminalSequence\n" +
		"at next Stop (the ◆-block path): stop-systemMessage · stop-block\n" +
		"detection: dump (records payload+env to compare terminal vs remote control)\n" +
		"on the Notification event (auto — trigger by idling ~60s or a permission prompt): fires on its own\n" +
		"every call is logged to " + logPath() + " — check it to see if the hook fired at all\n" +
		"usage: send a prompt starting with `hooktest: <type>`"
	out := sysMsg("[menu — via systemMessage]\n" + list)
	// Also relay it, so the menu shows even where systemMessage doesn't render.
	out["hookSpecificOutput"] = map[string]any{
		"hookEventName":     "UserPromptSubmit",
		"additionalContext": relayInstruction("[menu — via model relay]\n" + list),
	}
	return out
}

func unknown(typ string) map[string]any {
	return sysMsg("[unknown type \"" + typ + "\"] Send `hooktest: menu` for the list.")
}

// ---- envelope helpers ----

func sysMsg(s string) map[string]any { return map[string]any{"systemMessage": mark + " " + s} }

func blockMsg(s string) map[string]any {
	return map[string]any{"decision": "block", "reason": mark + " " + s}
}

// relay returns an additionalContext-only envelope that makes the model echo a
// labeled line verbatim.
func relay(s string) map[string]any {
	return map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": relayInstruction(s),
		},
	}
}

func relayInstruction(s string) string {
	line := mark + " " + s
	return "A hook-rendering test is running. Begin your reply with the following line, quoted VERBATIM, then stop:\n\n" + line
}

func parseType(prompt string) (string, bool) {
	p := strings.TrimSpace(prompt)
	low := strings.ToLower(p)
	for _, pre := range []string{"hooktest:", "hooktest ", "/hooktest"} {
		if strings.HasPrefix(low, pre) {
			return strings.ToLower(strings.TrimSpace(p[len(pre):])), true
		}
	}
	return "", false
}

func emit(out map[string]any) {
	if out == nil {
		out = map[string]any{} // valid empty envelope; never blocks
	}
	b, err := json.Marshal(out)
	if err != nil {
		os.Stdout.WriteString("{}")
		return
	}
	os.Stdout.Write(b)
}
