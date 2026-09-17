---
description: Health check of the Tacit stack (registry, hooks, model, MCP)
argument-hint: optional harness name for an end-to-end hop check
allowed-tools: Bash(tacit:*)
---

Report the health of the member's Tacit integration.

The binary already knows how to do this, and it is the only thing that knows
the current answer: `tacit doctor` checks the same components in the same order
every time, names the exact failing hop, and quotes the remedy it diagnosed. Run
it and relay it. Do not rebuild the checks out of curl commands — a health
report assembled in a prompt drifts from what the system itself considers
healthy, and then two things disagree about whether Tacit is up.

## Method

```bash
tacit doctor
```

Add `--harness $ARGUMENTS` when the member names one, or when suggestions have
stopped and nothing in the plain run explains it: that fires a synthetic event
through the real relay → agent → registry path and reports which hop died.

The one thing `tacit doctor` cannot see is **this session**. Check your own tool
list for `mcp__plugin_tacit_tacit__tacit_search` (load it via ToolSearch if deferred); registered
means the plugin's MCP wiring is live here.

## Report

Relay what the command printed — component, state, and its exact remedy where
one is given. Two things to get right:

1. **Lead with the model when it has failed.** A broken model makes Tacit
   go silent, which looks exactly like a quiet day: retrieval and recording keep
   working, suggestions stop. Quote the remedy verbatim. `rate-limited` and
   `overloaded` clear on their own — mention them without raising an alarm.
2. **An idle hook daemon is healthy**, and so is `0 shown`. The daemon
   idle-exits by design and the relay respawns it on the next hook; zero
   suggestions means zero were delivered, not that anything is broken. When
   `0 shown` accompanies a failed model, the model is the reason.

The exit status is the summary: 0 means every check passed, 1 means at least one
failed. If `tacit` is not on PATH, say so — that is itself the finding, and
`tacit connect` is the fix.

For the org's numbers rather than this machine's health, use /tacit:insights.
