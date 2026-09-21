---
id: every-shape
name: Every frontmatter shape the parser claims to read
description: >
  A folded scalar, which the parser has to join into one line. It exists so the
  shapes below are exercised by a fixture that nothing ships.
scope: org
status: stable
provenance: curated
version: 2
tags: [org-tool, data, connector]
task_types: [research, tool-use]
triggers:
  - heuristic: "a block list whose items are one-line maps"
  - llm_judge: "does the second item parse the same way as the first?"
applies_when: >
  The parser is asked to read frontmatter that uses every shape at once.
not_when: >
  A technique file is the fixture. A shipped technique is content, and content
  changes for reasons that have nothing to do with the parser.
support_matrix:
  - {harness: claude-code, surface: cli, supported: true, verified: 2026-05}
  - {harness: claude, surface: web, supported: true, verified: 2026-05}
shipped: 2026-03
recipe: |
  A literal block, which keeps its own line breaks:
  run `something <with-a-placeholder>` and read what it says.
---
Before: the parser test read a technique that ships, so editing that technique
broke a test about YAML. After: the shapes live here.
