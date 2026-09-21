---
id: revise-dont-duplicate
name: Revise the technique that misfired
description: >
  When a technique gives you wrong guidance or you find a better recipe, propose a
  revision against its id. A second entry with the same idea splits the evidence between
  them.
scope: org
status: stable
provenance: curated
version: 1
tags: [contribution, review, playbook]
task_types: [conversation, editing]
triggers:
  - heuristic: "a suggested technique was dismissed as wrong, or applied and then worked around"
  - llm_judge: "did a playbook technique misfire here in a way its own text could fix?"
applies_when: >
  A technique you were shown was wrong for the situation, incomplete, or has a recipe you
  have since improved.
not_when: >
  The technique is sound and simply did not fit this turn. Say "not relevant" — that
  corrects the matching, which is the actual fault.
shipped: 2026-09
recipe: |
  tacit revise <technique-id> --not-when "<where it should stay quiet>" \
    --note "<what went wrong here>"

  Pass only the fields you want to change. `/tacit:audit` offers to prepare the revision
  for you when it finds a technique that misled a session.
---
The revision goes to the review queue with a field-by-field diff against the live
technique, which keeps serving unchanged until a reviewer promotes the replacement.
