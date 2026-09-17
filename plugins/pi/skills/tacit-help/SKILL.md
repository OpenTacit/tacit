---
name: tacit-help
description: Explain Tacit features and commands. Use when the member invokes /skill:tacit-help or asks how Tacit works.
---

## First: resolve the registry address

Run this once. Each command block starts a fresh shell, so substitute the
**resolved values** into every command and put the resolved URL (`$DASH`) in
member-facing links:

```bash
eval "$(tacit env)"   # sets REG (API base), KEY (X-Tacit-Key), DASH (dashboard URL)
```

Explain Tacit using the facts below. Keep the response concise and do not add
features that are not listed.

## What Tacit is

Tacit surfaces techniques from the organization's playbook during agent
work. Outcome evidence is reported in aggregate.

## Automatic features

- **◆ Tacit suggestion block**: appears after a reply when a technique from the
  org's playbook fits what you just did, with the evidence line (how often it
  helped colleagues, sample size). To adopt it, just use the recipe. To react,
  say it in your next message, such as "that helped", "already knew that", or
  "not relevant here". Feedback is captured automatically — no command needed.
- **Tacit question form**: occasionally asks
  whether a suggestion helped. Answer and move on.
- **Statusline**: `◆ tacit N⚡ M✓ K⚑` shows **N⚡** suggestions
  shown and **M✓** adopted *this session*, plus **K⚑** draft techniques
  awaiting review in the org registry. The draft count is a shared reviewer
  count (not per-session) and only appears when some are queued. Open them
  with /skill:tacit-drafts. It also carries a warning when a fault has made Tacit
  go silent — a rotated key, a model out of credit — which otherwise looks just
  like a quiet day. `tacit connect` wires the line; if you don't see it, run
  `tacit connect` again, or add `tacit statusline` to the status line you already have.

## On demand

- /skill:tacit-search `<what you're trying to do>`: ask whether the org has
  a validated way to do something. Do this before inventing an approach that
  internal tools or conventions might cover.
- /skill:tacit-review: check the work in flight against the registry and surface
  the moves that apply, **rendered inline as conversation** so it shows in the
  web/mobile Remote Control view (where the automatic hook nudge can't). Or say
  "review this against Tacit" to run the deeper `tacit-review` agent.
- /skill:tacit-contribute: capture a reusable approach as a draft technique. Drafts are
  excluded from retrieval until a reviewer promotes them.

## Other commands

- /skill:tacit-drafts: the review queue (promote/reject/edit).
- /skill:tacit-setup `[url]`: configure and verify the registry URL and key.
- /skill:tacit-suggest `[count]`: research techniques matched to observed usage; files up to 10 drafts.
- /skill:tacit-insights `[7d|30d|90d|all]`: technique-usage metrics (funnel, helped rate).
- /skill:tacit-org `[7d|30d|90d|all]`: how the org uses its playbook — cohort spread, opportunities, proponents.
- /skill:tacit-status: stack health; /skill:tacit-test: end-to-end pipeline
  check with a paste-ready trigger prompt; /skill:tacit-testfeedback: deliver one
  block through the real channel, to see whether suggestions are visible here.
- Dashboard: `$DASH/outcomes` (techniques, drafts, insights,
  federation, docs).

When an approach depends on the organization's tools, data, or conventions,
it can be contributed as an org-scoped draft. Reactions to suggestions update
the aggregate outcome evidence.
