---
id: run-the-failing-command
name: Paste the error, not a description of it
description: >
  Provide the failed command and its output instead of saying only that the build or
  test failed. The output gives the agent the file, line, and error message.
scope: general
status: stable
provenance: curated
version: 1
tags: [debugging, context, verification]
task_types: [verification, exploration, editing]
triggers:
  - heuristic: "a failure reported in prose with no command output, stack trace or exit status in the turn"
  - llm_judge: "did the member describe a failure the agent could have read verbatim?"
applies_when: >
  Something failed and the member has, or can get, the exact output from a test run,
  build, stack trace, or non-zero exit.
not_when: >
  The failure has no output (a hang, a wrong-looking result), or the member is asking
  about a design rather than reporting a break.
shipped: 2024-01
recipe: |
  $ <the exact command you ran>
  <paste its full output, including the stack trace>

  Fix this. Run the command again to check.
---
Giving the command also lets the agent run it again after the fix.
