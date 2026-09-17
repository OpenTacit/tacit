---
id: ask-for-a-diagram
name: Ask for a diagram you can keep
description: >
  A visual topic shown as prose or ASCII is hard to remember. Ask for a real
  diagram to get a visual reference that you can use again.
status: stable
provenance: curated
version: 1
tags: [modality, visual, diagram, output-format]
task_types: [exploration, research]
triggers:
  - heuristic: "explanation of a spatial/structural/visual topic delivered as text only"
  - llm_judge: "would a diagram convey this better than the prose the model produced?"
applies_when: >
  The topic is spatial, structural, or relational, and the model gave only prose or
  ASCII. A labelled diagram would make the topic clearer and easier to remember.
not_when: >
  The user already asked for a diagram or received one, or the content is not visual
  (e.g. a short factual answer).
support_matrix:
  - {harness: chatgpt, surface: web, supported: true, verified: 2026-05}
  - {harness: chatgpt, surface: mobile, supported: true, verified: 2026-05}
  - {harness: claude, surface: web, supported: true, verified: 2026-05}
shipped: 2024-06
recipe: |
  Make a clear, labelled diagram of [the thing]. Show [the key parts and how they
  relate], so I can keep it as a visual reference.
---
Before: a visual concept came back as prose and ASCII. After: a labelled diagram made the
structure clear and reusable.
