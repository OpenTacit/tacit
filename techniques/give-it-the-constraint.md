---
id: give-it-the-constraint
name: Give it the constraint, not just the goal
description: >
  State requirements such as backward compatibility, offline use, or no new
  dependencies. If you omit them, an agent may produce a solution you cannot use.
scope: general
status: stable
provenance: curated
version: 1
tags: [context, scope, design]
task_types: [editing, research]
triggers:
  - heuristic: "a proposed change is rejected for a reason not stated anywhere in the turn"
  - llm_judge: "did the member reject this for a constraint they never mentioned?"
applies_when: >
  The obvious solution is ruled out by something about your environment, your users, or
  a decision already taken.
not_when: >
  There are no real constraints. Inventing them narrows the answer for nothing.
shipped: 2026-09
recipe: |
  <what you want>

  Constraints: <no new dependencies> · <must work offline> · <public API stays as it is>
  Say so if these make the task impossible rather than working around them.
---
Clear constraints also let the agent report when the task cannot meet all requirements.
