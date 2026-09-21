---
description: Review Tacit drafts awaiting decision — promote, reject, or edit, without the dashboard
---

Run the draft review queue for the member, in their harness. Three tiers, in
order of support — use the FIRST one this client supports and stop there:

1. **The MCP review app** — an interactive panel with the full techniques and
   Promote/Reject buttons.
2. **A native form** — one structured decision (a native question/picker
   form where the harness has one) per draft.
3. **Plain conversation** — present each draft, act only on what the member
   says.

## First: resolve the registry address

Run this once. Each command block starts a fresh shell, so substitute the
**resolved values** into every command and put the resolved URL (`$DASH`) in
member-facing links:

```bash
eval "$(tacit env)"   # sets REG (API base), KEY (X-Tacit-Key), DASH (dashboard URL)
```

## Tier 1: the MCP review app

Call the `tacit_drafts` MCP tool (load it via ToolSearch if deferred). It
returns the queue three ways at once: an interactive review panel (rendered by
MCP-Apps-capable clients — Claude web and desktop), a text fallback, and a
structured summary.

- **If this client renders the panel** (the member sees the drafts with
  Promote/Reject buttons): tell the member the queue is on screen and each
  decision takes effect immediately, and stop. The panel's buttons call
  `tacit_draft_action` themselves; do not duplicate its work in chat.
- **If the queue is empty** (`structuredContent.count == 0`): say the review
  queue is empty and stop — no tiers needed.
- **In a terminal** (no panel renders): you still have the full queue from the
  tool's text content — proceed to tier 2 with it. Do not call the registry
  again for the same data.
- **If the MCP tool is unavailable entirely**: fetch the queue directly, then
  proceed to tier 2:

```bash
curl -s "$REG/v1/review/app?format=text" -H "X-Tacit-Key: $KEY"
```

## Tier 2: the native form

For each draft, newest first:

1. Present the full technique in the conversation — name, id, provenance, scope,
   date added, description, recipe, applies when / not when, tags. For a
   revision (`supersedes` set), say what it revises, quote the proposer's
   `revision_note`, and say that promoting applies the change onto the base
   technique, which keeps serving unchanged until then.
2. Ask with the harness's native question form (header "Tacit"), options:
   - **Accept** — puts it in service now
   - **Reject** — declines it (kept, not erased)
   - **Edit first** — the member describes changes; apply them, show the
     result, then ask again
   - **Skip** — leave it in the queue, move on
3. Act on the answer immediately, before presenting the next draft, and
   confirm what happened in one line.

With many drafts (more than 5), first show the list in one message and ask
which to review — reviewing all, one by one, is a valid answer.

## Tier 3: plain conversation

Where no native question form is available: present the queue (same technique detail
as tier 2), ask the member what they want to do with each, and act only on their
explicit instruction. Never promote, reject, or edit a draft they did not
name.

## Acting on decisions

Prefer the `tacit_draft_action` MCP tool: arguments `{id, action}` with
action `promote` or `reject`. Without MCP, the API equivalents (header
`X-Tacit-Key: $KEY`):

- promote: `POST $REG/v1/admin/promote` body `{"id": "<id>", "status": "stable"}`
  (for a revision draft this applies it onto the base technique)
- reject: same endpoint with `"status": "retired"`
- edit a draft before deciding: `POST $REG/v1/admin/techniques/<id>` with only the
  changed fields

If an action returns 401 or 403, the member's key can't decide drafts —
deciding needs the admin key. Say so plainly and point at the dashboard
(`$DASH/review`) or their Tacit admin; do not retry with variations.

A member who wants to propose a change to a technique already **in service** is not
reviewing — that's `tacit revise <id> --<field> <value> --note "why"`, which
files a new revision draft into this queue.
