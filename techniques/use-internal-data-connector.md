---
id: use-internal-data-connector
name: Query the source instead of pasting rows
description: >
  When data is already in a connected internal system, point the model at it through
  the org's connector. That is faster and more current than pasted rows, and it
  prevents copy errors.
scope: org
status: stable
provenance: curated
version: 2
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
  Nor is this about files: a directory being copied, a fixture being loaded, or any
  other data moving between machines. It is about rows a person pasted into the
  conversation.
recipe: |
  Don't paste the rows. Query <the internal source> directly with <the tool this org
  uses to reach it>, then ask for the analysis you want.
---
A pasted table is a snapshot: it cannot be repeated, and it ages from the moment it is
pasted. This is a `scope: org` technique because the recipe depends on the organization's
own connector and tools, which is also why it names neither.
