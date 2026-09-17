---
id: name-the-files
name: Name the file when you already know it
description: >
  If you know which file, function, or directory needs work, name it. This avoids an
  unnecessary search and directs the agent to the right code.
scope: general
status: stable
provenance: curated
version: 1
tags: [context, navigation, efficiency]
task_types: [exploration, editing]
triggers:
  - heuristic: "several consecutive search or read turns before the first edit"
  - llm_judge: "did the member know which file this concerns, and not say?"
applies_when: >
  The member already knows the file, function, or directory the work touches.
not_when: >
  Finding where the behaviour lives is the task, or the member does not know the path.
shipped: 2024-01
recipe: |
  In `<path/to/file.go>`, `<function or symbol>` does <what it does now>.
  Change it so <what it should do>. The related code is in `<other path>`.
---
This can prevent edits to a file with a similar name elsewhere in the repository.
