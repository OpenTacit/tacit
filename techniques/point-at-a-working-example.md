---
id: point-at-a-working-example
name: Point at something in the codebase that already does it
description: >
  Name a similar implementation in the codebase. It shows the agent which structure,
  helpers, error handling, and test style to follow.
scope: general
status: stable
provenance: curated
version: 1
tags: [context, conventions, consistency]
task_types: [editing, exploration]
triggers:
  - heuristic: "new code introduces a pattern that differs from a sibling file doing the same job"
  - llm_judge: "does this repository already contain a close precedent the member could have named?"
applies_when: >
  Something similar already exists, such as another handler, migration, or test of the
  same shape.
not_when: >
  The precedent is one you are deliberately moving away from. Then say that instead,
  and say what replaces it.
shipped: 2026-09
recipe: |
  Add <the new thing>, following `<path/to/existing/example>` — same structure,
  same error handling, same test layout.
---
This also lets the reviewer focus on where the new code differs from the example.
