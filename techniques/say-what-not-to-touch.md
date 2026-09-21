---
id: say-what-not-to-touch
name: Say what must not change
description: >
  State which APIs, schemas, generated files, or tests must not change. This keeps the
  agent from meeting the goal through changes outside the allowed scope.
scope: general
status: stable
provenance: curated
version: 1
tags: [scope, safety, review]
task_types: [editing, delegation]
triggers:
  - heuristic: "a diff touches files or symbols outside the stated scope of the request"
  - llm_judge: "did the change alter something the member would not have agreed to?"
applies_when: >
  The change is near a published API, migration, generated file, or test that guards a
  past bug.
not_when: >
  Early exploration, where strict limits could hide a useful answer.
shipped: 2026-09
recipe: |
  <what to change>

  Do not change: <the public signature / the schema / the generated files / this test>.
  If the change seems to need one of those, stop and say so instead.
---
The last line tells the agent to report a conflict between the goal and a constraint
instead of choosing which one to ignore.
