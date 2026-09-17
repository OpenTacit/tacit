---
id: use-internal-data-connector
name: Query the warehouse instead of pasting rows
description: >
  When data is already in a connected internal system, point the model at it through
  the org's connector. That is faster and more current than pasted rows, and it
  prevents copy errors.
scope: org
status: stable
provenance: curated
version: 1
tags: [org-tool, data, connector, proprietary]
task_types: [research, tool-use]
triggers:
  - heuristic: "user pasted tabular/CSV-like rows that originate from an internal source"
  - llm_judge: "is the pasted data available through a connected internal system instead?"
applies_when: >
  The user manually pasted tabular data (CSV rows, a table dump) that is in an internal
  system, and the organization's connector can reach that system. A live query is more
  accurate and more current than the pasted snapshot.
not_when: >
  The data is ad-hoc, comes from an external source, or is not in a connected internal
  system; or the user explicitly wants an analysis of a fixed snapshot, not live data.
support_matrix:
  - {harness: claude-code, surface: cli, supported: true, verified: 2026-05}
  - {harness: claude, surface: web, supported: true, verified: 2026-05}
shipped: 2026-03
recipe: |
  Use the internal data connector; do not paste rows. Query the source directly, e.g.
  `@warehouse query <table> where <filter>`, then ask for the analysis you need. Replace
  the table and the filter with this org's real source and your question.
---
Before: the user copied rows from an internal dashboard into the chat, creating a fixed
snapshot. After: the model queried the connected source, so the analysis used current
data and could be repeated. This is a `scope: org` technique because the recipe depends
on the organization's own connector and tools.
