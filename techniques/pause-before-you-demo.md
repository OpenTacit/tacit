---
id: pause-before-you-demo
name: Pause capture before you demo or record
description: >
  A pause stops capture and suggestions on this machine, so no ◆ block lands in front of
  an audience and nothing about the session is recorded.
scope: org
status: stable
provenance: curated
version: 1
tags: [privacy, safety]
task_types: [conversation, verification]
triggers:
  - heuristic: "session framed as a demo, a recording, a workshop or a screen share"
  - llm_judge: "is this session going to be watched by people outside it?"
applies_when: >
  You are about to demonstrate, record, pair, or work on something you would rather
  nothing observed.
not_when: >
  You are working alone as usual. Suggestions are already rationed to a few per session,
  and a pause you forget to lift costs you all of them.
shipped: 2026-09
recipe: |
  tacit pause

  Then `tacit resume` when you are finished; suggestions reach you again from the next
  turn. Asking still works while paused: `tacit ask` and the `/tacit:` commands go
  straight to the playbook. An `@tacit` mention does not — the hook a pause switches
  off is the one that would have read it.
---
The pause is one file, `~/.tacit-paused`. Deleting it does what `tacit resume` does.
Nothing is unwired and no setting is lost.
