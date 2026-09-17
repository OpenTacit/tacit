---
id: hand-it-the-log
name: Hand it the running system's log, not your reading of it
description: >
  Provide the relevant log lines rather than a summary. Timestamps, event order, and
  nearby lines can explain the fault.
scope: general
status: stable
provenance: curated
version: 1
tags: [debugging, grounding, context]
task_types: [verification, exploration]
triggers:
  - heuristic: "a deployed or service-level problem described with no log excerpt in the turn"
  - llm_judge: "is the member paraphrasing output they could paste?"
applies_when: >
  Something misbehaves in a service, container or CI run you can read the output of.
not_when: >
  The log is large and unfiltered. Narrow it by time or request ID first.
shipped: 2026-09
recipe: |
  <what it does wrong>

  ```
  <the log, from just before the problem to just after>
  ```

  What in here explains it?
---
Include the lines before the error, since they may show the event that caused it.
