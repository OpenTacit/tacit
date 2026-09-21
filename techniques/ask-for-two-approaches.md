---
id: ask-for-two-approaches
name: Ask for two approaches before committing to one
description: >
  Ask for two options and their trade-offs before making a costly choice. Comparing
  them can expose missed needs or costs.
scope: general
status: stable
provenance: curated
version: 1
tags: [planning, design, review]
task_types: [research, exploration]
triggers:
  - heuristic: "a design decision taken in one turn and then substantially reworked later in the session"
  - llm_judge: "was there an alternative worth seeing before this was built?"
applies_when: >
  The change is hard to reverse, spans several files, or sets a pattern others will copy.
not_when: >
  The work is small or obvious. Two options for a one-line change is ceremony.
shipped: 2026-09
recipe: |
  Before writing anything: give me two ways to do <the thing>, with what each costs
  and what it rules out later. Recommend one and say why.
---
Ask for a recommendation as well as the two options, so the response includes a clear
choice and its reasons.
