---
id: ask-for-the-sources
name: Ask where the answer came from
description: >
  Check claims about a library's behaviour against its source or docs. Ask for the file,
  version, doc section, or command output needed to verify each claim.
scope: general
status: stable
provenance: curated
version: 1
tags: [research, verification, grounding]
task_types: [research, verification]
triggers:
  - heuristic: "a factual claim about an external library, API or version with no file path, link or command behind it"
  - llm_judge: "could this claim be wrong in a way the member would not notice?"
applies_when: >
  The answer depends on how something outside your codebase actually behaves, and the
  cost of it being wrong is more than a re-run.
not_when: >
  The claim is about code available in the current session; read that code instead.
shipped: 2026-09
recipe: |
  <the question>

  Say where each part of the answer comes from: the file and line, the installed
  version, or the command whose output shows it. Mark anything you are inferring.
---
Marking inferences separates facts found in a source from assumptions.
