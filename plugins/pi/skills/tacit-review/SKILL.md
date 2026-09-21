---
name: tacit-review
description: Review current work for relevant techniques from the org's Tacit playbook. Use when the member invokes /skill:tacit-review or asks to review work against Tacit.
---

## First: resolve the registry address

Run this once. Each command block starts a fresh shell, so substitute the
**resolved values** into every command and put the resolved URL (`$DASH`) in
member-facing links:

```bash
eval "$(tacit env)"   # sets REG (API base), KEY (X-Tacit-Key), DASH (dashboard URL)
```

Check the work **currently in flight** against the organization's Tacit
playbook and surface the moves that apply. This is the **current-work** member
of one family that all run the same playbook retrieval: /skill:tacit-search (a query
you name), this (work in flight), and /skill:tacit-audit (a finished session, which
also closes the loop). It is also the on-demand counterpart to the automatic
in-flow nudge.

Why this command exists: a Stop hook delivers the automatic ◆ Tacit nudge as a
terminal-only `systemMessage`; it does **not** render in the Claude web /
mobile Remote Control view. This command produces the same suggestions as
**ordinary conversation output**, which mirrors to every surface. Use it when you
want Tacit feedback in a web/mobile session, or a deliberate check any time.

## 1. Identify the work in flight

From this session (and any files/context you can read), name **2–4 distinct
activities** in plain words, such as "writing a database migration" or "granting
an agent broad tool access". If arguments were
given, focus there. Different phrasings retrieve different techniques, so make
each concrete. Describe the work directly without quoting technique names.

## 2. Search the registry per activity

Prefer the `tacit_search` MCP tool. If it's unavailable or denied, fall
back to the evidence API:

```bash
curl -s -X POST $REG/v1/evidence \
  -H "X-Tacit-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"summary_text": "<one activity, in plain words>"}'
```

## 3. Filter candidates

Retrieval is recall-oriented. Check each candidate's `applies_when` / `not_when`
against the actual work and retain up to four that fit,
ordered by relevance, with org-scoped techniques ahead of general ones.

## 4. Render each survivor as a ◆ Tacit block

Output the retained techniques **as your reply**, so they mirror to every client.
Render each as a **markdown blockquote** — one per technique:

> ◆ **Tacit** — {technique name}
>
> why: {one sentence tying the recipe to THIS work}
>
> try: {the recipe, ready to use}
>
> measured by colleagues: helped 94% · adopted 70% · n=120
>
> `{technique-id}` · {scope}

Do **not** draw the `┃` spine, and do **not** wrap the technique in a fenced code
block. The spine is a terminal affordance; the hook agent applies it when it
delivers through a channel that renders no markdown. A fenced block is clipped
at the right edge rather than reflowed on a narrow screen — and a phone is
exactly where this command is most needed. The blockquote's rule is the spine's
equivalent, and the client wraps it to whatever width it has.

Keep each field on its own quoted line, whole: let the client choose where to
wrap rather than breaking lines yourself.

Use the evidence line **verbatim** when measured; write "awaiting measured
outcomes" when absent. If the registry returns zero applicable techniques,
say so in one line.

## 5. Close the loop

If the work contains a reusable move the registry is missing, name it and offer
to capture it with /skill:tacit-contribute. Record outcomes only with the member's
say-so (`tacit feedback <id>`).
