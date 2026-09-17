---
id: name-the-check
name: Say which command proves it worked
description: >
  Name the test, build, or lint command that must pass. This gives the agent a clear
  check to run before it finishes.
scope: general
status: stable
provenance: curated
version: 1
tags: [verification, acceptance, testing]
task_types: [editing, verification]
triggers:
  - heuristic: "a change request with no test, build or run command named anywhere in the turn"
  - llm_judge: "was the member asking for a change whose success has a command that would prove it?"
applies_when: >
  The member asks for a change to code that has a test suite, a build, a linter, or any
  command whose exit status answers "did this work?".
not_when: >
  The work is exploratory or a question about the code, or nothing runnable can judge it
  — a wording change in a document, a design discussion.
shipped: 2024-06
recipe: |
  <what you want changed>

  Done means `<the command>` passes. Run it before you tell me you're finished.
---
The agent should report the command result rather than rely on a source review.
