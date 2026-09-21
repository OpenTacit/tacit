---
id: show-it-the-output
name: Show the rendered result, not the code that renders it
description: >
  A visual or formatted result cannot be checked by reading its source. Capture what it
  produced, such as the page, chart, or generated file, and give it to the agent.
scope: general
status: stable
provenance: curated
version: 1
tags: [verification, modality, visual]
task_types: [verification, editing]
triggers:
  - heuristic: "a change to templates, styles or output formatting confirmed without rendering anything"
  - llm_judge: "was a visual or formatting change verified by reading source rather than by looking at the result?"
applies_when: >
  The work changes a page, document, chart, terminal display, or other rendered output
  that can be captured.
not_when: >
  The change has no visual result, or rendering it costs more than the change is worth.
shipped: 2025-04
recipe: |
  Render it and look at the result before you tell me it works. Show me the before and
  the after.
---
A screenshot can show errors that a diff cannot, such as double-escaped entities or a
chart stretched past its box.
