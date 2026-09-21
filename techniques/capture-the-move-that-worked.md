---
id: capture-the-move-that-worked
name: Capture the move while the session is still open
description: >
  When something non-obvious unblocks you, contribute it as a draft technique before you
  close the session. The capture reads the session for the recipe, which it cannot do
  tomorrow.
scope: org
status: stable
provenance: curated
version: 1
tags: [contribution, playbook, context]
task_types: [editing, verification]
triggers:
  - heuristic: "a long stall resolved by a move the member had to work out, late in the session"
  - llm_judge: "did something reusable just work that the playbook does not already carry?"
applies_when: >
  A prompt pattern, a recovery tactic, or an org-specific step got you past something, it
  would work again, and no technique you were shown covers it.
not_when: >
  The fix was specific to this bug, or a technique in the playbook already says it — then
  propose a revision rather than a second entry.
shipped: 2026-09
recipe: |
  /tacit:contribute

  Or say "capture this as a technique". Keep it to one move: if it needs three recipes,
  it is three techniques. Fill in "applies when" and "not when" carefully, because they
  decide whether a colleague gets this at the right moment. Mark it org-scoped when the
  recipe leans on your own systems, data or conventions.
---
The draft waits in the review queue and reaches nobody until a reviewer promotes it. It
is attributed to your cohort, not your name, and no transcript goes with it.
