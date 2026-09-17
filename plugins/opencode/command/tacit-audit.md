---
description: Retrospective Tacit audit of an agent session: review what happened and follow up (arguments: transcript file or share URL (blank = this session))
---

## First: resolve the registry address

Run this once. Each command block starts a fresh shell, so substitute the
**resolved values** into every command and put the resolved URL (`$DASH`) in
member-facing links:

```bash
eval "$(tacit env)"   # sets REG (API base), KEY (X-Tacit-Key), DASH (dashboard URL)
```

Run a retrospective Tacit audit of an agent session: replay what actually
happened against the organization's Tacit playbook, then feed
what you learn back into it.

This is the **finished-session** member of one family that all run the same
playbook retrieval: /tacit-search (a query you name), /tacit-review (work in
flight), and this (a finished session). What makes an audit distinct is that it
does not stop at retrieval — it **closes the loop**: what should have gone
better, what feedback to record, what's missing from the registry.

## 1. Resolve the source

- A transcript file path or a shared-conversation URL in the arguments → use
  it directly.
- No arguments (or "this session") → reconstruct the current session as plain
  text: each member request, what was done, **every correction or workaround
  the member had to supply by hand**, and the outcome. Write it to a scratch
  file. Include corrections, workarounds, and the resulting outcome.

## 2. Run the auditor

```bash
tacit audit <transcript-file-or-url>            # saved transcript or share URL
tacit audit - --text < /tmp/session-notes.txt   # reconstructed text on stdin
```

If the `tacit` binary is unavailable, fall back to per-activity retrieval
against the registry API:

```bash
curl -s -X POST $REG/v1/evidence \
  -H "X-Tacit-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"summary_text": "<one activity from the session, in plain words>"}'
```

## 3. Report and follow up

Present the audit's suggestions. Quote measured evidence lines verbatim, such as
"helped 94% · n=120". Then present the three loop-closing moves and obtain the
member's confirmation before acting on each:

1. **A suggested technique for the next session** → note it for next time,
   and record the outcome later with `tacit feedback <technique-id>`.
2. **Recurring uncovered friction**: something the member
   corrected by hand more than once → that's a missing technique: offer to capture
   it with /tacit-contribute, naming the move.
3. **A technique that fired but misled** → propose a reviewed fix:
   `tacit revise <technique-id> --not-when "..." --note "why"`: it lands in the
   drafts lane while the serving technique stays untouched.

Never record feedback or submit techniques without the member confirming.
