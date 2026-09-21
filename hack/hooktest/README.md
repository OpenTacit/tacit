# hooktest — see how Claude Code hook output renders per client

A standalone Claude Code hook for checking where each type of hook output
appears: terminal, web (claude.ai/code), and iPad/mobile. It emits raw hook
envelopes without using Tacit or the registry, which isolates the client output
channel from Tacit's delivery logic.

Each test message names its output channel. If it appears, that client renders
the channel.

## 1. Wire it in

The hook must run **wherever your session's hooks execute** — for native
Claude Code on the web that's the cloud workspace; for a remote/tailnet setup
it's the connected machine. To make it travel with the repo, wire it with
`go run` (no prebuilt binary, works anywhere Go + this repo are present) in a
settings file Claude Code reads for this project — `.claude/settings.json` (or
`.claude/settings.local.json` to keep it out of git):

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "go run ./hack/hooktest", "timeout": 30 } ] }
    ],
    "Stop": [
      { "hooks": [ { "type": "command", "command": "go run ./hack/hooktest", "timeout": 30 } ] }
    ],
    "Notification": [
      { "hooks": [ { "type": "command", "command": "go run ./hack/hooktest", "timeout": 30 } ] }
    ]
  }
}
```

For lower latency on a fixed machine, build once and point at the binary
instead:

```bash
go build -o /usr/local/bin/hooktest ./hack/hooktest
# then use   "command": "/usr/local/bin/hooktest"
```

**Tip:** test in a scratch project with *only* this hook wired (no Tacit), so
Tacit's own ◆ blocks don't interleave with the probes. The hook is a no-op on
any prompt that doesn't start with `hooktest:`, so leaving it wired is harmless.

## 2. Run the probes

In any client, submit a prompt that starts with `hooktest:`. Start with the
menu:

```
hooktest: menu
```

### Immediate types (emitted around this turn, at UserPromptSubmit)

| Prompt | Emits | What you learn |
|---|---|---|
| `hooktest: systemMessage` | `{"systemMessage": …}` | does `systemMessage` render here? (the field the ◆ block uses) |
| `hooktest: block` | `{"decision":"block","reason": …}` | does a block **reason** render? (the prompt is blocked; model doesn't run) |
| `hooktest: context-chip` | raw `additionalContext` | does a **context chip/pill** appear? (model is told to stay quiet) |
| `hooktest: relay` | `additionalContext` → model echoes a line | the model-relay path — should render **everywhere** |
| `hooktest: both` | `systemMessage` + a relay line | see them side by side: if only the relay line shows, `systemMessage` didn't render |
| `hooktest: stopReason` | `{"continue":false,"stopReason": …}` | does `stopReason` render? (turn halts) |
| `hooktest: suppressOutput` | `suppressOutput` + `systemMessage` | does `systemMessage` survive `suppressOutput`? |
| `hooktest: terminalSequence` | an OSC title change + bell | terminal-only by design; web/mobile ignore it |

### Deferred types (delivered at the next **Stop**, as the ◆ block is)

| Prompt | Behavior | What you learn |
|---|---|---|
| `hooktest: stop-systemMessage` | arms; the model gives a one-line ack, then at Stop a `systemMessage` fires | does a Stop-time `systemMessage` render on web/iPad? |
| `hooktest: stop-block` | arms; at Stop a `decision:block` reason fires | does a Stop-time block reason render here? |

The deferred probes ask the model to reply with a single anchor line
(`⟦hooktest⟧ turn complete — watch below…`) so the turn ends promptly and you
have a visible marker right before the Stop output.

Every probe message is prefixed with `⟦hooktest⟧`, so it's unmistakable and
greppable.

### The Notification event

You can't type this one; Claude Code fires the `Notification` event on its own.
When it does, hooktest emits a labeled `systemMessage` + relay, so you can see
whether **Notification-hook output reaches the iPad** even when nothing else
does. Trigger a Notification by either:

- **idling** — finish a turn and wait ~60s without typing (the "waiting for
  your input" notification), or
- **a permission prompt** — ask Claude to do something that needs approval.

## If nothing renders, check the log

Every event appends a line to **`/tmp/hooktest.log`** (or
`$TMPDIR/hooktest.log`) on the machine where the hook runs. Use the log to tell
whether the hook ran when the client showed no output:

```bash
cat /tmp/hooktest.log
```

- **The log gains lines for your iPad session** → the hook runs there, but the
  client does not show that output. Test the Notification and relay rows next.
- **The log stays empty after iPad prompts** → the hook never ran in that
  environment. The remote session is not running your local hooks, so changing
  the output field will not help.

Check the log after testing each client.

## 3. Record what you see

Run each probe on all three clients and fill this in:

| Channel | Hook fired? (log) | Terminal | Web | iPad/mobile |
|---|---|---|---|---|
| `systemMessage` (at UserPromptSubmit) | | | | |
| `systemMessage` (at **Stop** — the ◆ path) | | | | |
| block `reason` (UserPromptSubmit) | | | | |
| block `reason` (Stop) | | | | |
| `additionalContext` chip | | | | |
| model relay | | (expected ✓) | (expected ✓) | (expected ✓) |
| `stopReason` | | | | |
| **`systemMessage` @ Notification** | | | | |
| `terminalSequence` | | (title/bell) | (expected ✗) | (expected ✗) |

The rows that matter most for Tacit are **`systemMessage` at Stop** (whether the
◆ block can render on web/iPad as-is) and the **chip** row (whether raw
`additionalContext` is a usable visible channel). If Stop-`systemMessage` now
renders on web/iPad, block mode can become the default again and the delivery
docs need updating; if it doesn't, the **relay** row confirms the fallback that
already works everywhere.

## How it works

- Reads the hook JSON on stdin (`hook_event_name`, `prompt`).
- On `UserPromptSubmit`, a prompt starting with `hooktest:` selects the type;
  immediate types emit right away, `stop-*` types write a marker in the temp dir.
- On `Stop`, if a marker is armed it emits the deferred envelope and clears it.
- Always exits 0 and prints valid JSON — it can never block a real prompt.

Not part of the Tacit product; a diagnostic for settling how the hook channels
behave across clients (see the delivery discussion in
`docs/harness/advisor-mention-plan.md`).
