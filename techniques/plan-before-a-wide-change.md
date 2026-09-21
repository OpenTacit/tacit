---
id: plan-before-a-wide-change
name: Ask for the plan before a change that spans files
description: >
  For a change that touches several files, ask for a short plan before editing. Review
  the approach, affected files, and risks while changes are still cheap to make.
scope: general
status: stable
provenance: curated
version: 1
tags: [planning, review, large-change]
task_types: [editing, conversation, delegation]
triggers:
  - heuristic: "a single request that results in edits to three or more files with no plan turn before them"
  - llm_judge: "did a multi-file change begin without the member seeing the approach first?"
applies_when: >
  The work spans several files or introduces a pattern the codebase will repeat, such as
  a new module, migration, or refactor across call sites.
not_when: >
  The change is local and obvious, or the member has already agreed the approach. A plan
  for a one-line fix is a turn spent on ceremony.
shipped: 2024-09
recipe: |
  Before you change anything: what files does this touch, what is the approach, and what
  would break? Keep it short. Wait for me to say go.

  <the change you want>
---
A short plan lets the member correct the approach before reviewing a large diff.
