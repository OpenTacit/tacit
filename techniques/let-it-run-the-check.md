---
id: let-it-run-the-check
name: Let it run the command instead of reasoning about the answer
description: >
  When a command can show the installed version, resolved config, or port state, run it.
  Source code alone may not match the state of the machine.
scope: general
status: stable
provenance: curated
version: 1
tags: [verification, grounding, tooling]
task_types: [verification, exploration]
triggers:
  - heuristic: "a factual claim about the running system made in a turn that ran no command"
  - llm_judge: "did the agent reason its way to a fact a single command would have settled?"
applies_when: >
  The question is about the actual state of a machine, a service, a dependency or a
  configuration, and the member can let the agent run a command.
not_when: >
  The command is slow, destructive, or needs unavailable credentials. In that case,
  state clearly that the answer is based on reasoning.
shipped: 2024-03
recipe: |
  Don't work it out from the source — run the command that tells us, and quote what it
  printed.
---
This avoids answers that match the repository but not the software running on the
machine.
