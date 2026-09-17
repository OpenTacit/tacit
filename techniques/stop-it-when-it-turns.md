---
id: stop-it-when-it-turns
name: Stop it at the first wrong turn
description: >
  Stop an agent as soon as its work shows that it misunderstood the task. A quick
  correction avoids a larger wrong diff and the work needed to review it.
scope: general
status: stable
provenance: curated
version: 1
tags: [steering, efficiency, review]
task_types: [editing, delegation]
triggers:
  - heuristic: "a long run of turns ending in a revert, a restart, or the member describing the work as off-track"
  - llm_judge: "was the wrong direction visible several turns before the member said so?"
applies_when: >
  You can see from the first file it opens, or the first thing it writes, that it has
  misread the task.
not_when: >
  It is doing something unexpected that might be right. Ask why before stopping it.
shipped: 2026-09
recipe: |
  Stop — that is not the change. <What it misread> actually <what is true>.
  Start again from <the right place>.
---
Correcting the first wrong step is often faster than reviewing and reverting the full
change.
