---
id: check-against-the-playbook
name: Check the work in flight against the playbook
description: >
  Run a review over the session you are in. It splits the work into its separate
  activities and checks each one against the registry, which a single search does not do.
scope: org
status: stable
provenance: curated
version: 1
tags: [playbook, review, context]
task_types: [editing, exploration]
triggers:
  - heuristic: "a long session with several distinct activities and no playbook access"
  - llm_judge: "has this session done several different kinds of work, any of which the playbook might cover?"
applies_when: >
  The session has run long enough to cover more than one activity, or you are on a client
  that cannot show pushed suggestions — Claude Code on the web or a phone, GitHub Copilot
  CLI, a chat connector.
not_when: >
  The session has done one small thing, or you want one question answered rather than the
  whole session examined. Search for that instead.
shipped: 2026-09
recipe: |
  /tacit:review

  Add a focus to narrow it — `/tacit:review the schema migration` — or ask in words
  ("review this against the playbook") on a client without slash commands.
  On a session that is already finished, use `/tacit:audit`, which also proposes
  follow-ups.
---
The pull path reaches every surface, including the clients that cannot show a pushed
suggestion: matches arrive as ordinary conversation text.
