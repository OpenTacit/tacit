---
id: say-what-you-already-tried
name: Say what you already tried
description: >
  List earlier attempts and what each one did. This prevents repeated suggestions and
  gives the agent more evidence about the cause.
scope: general
status: stable
provenance: curated
version: 1
tags: [debugging, context, efficiency]
task_types: [verification, exploration, editing]
triggers:
  - heuristic: "the agent proposes a fix the member has already described trying earlier in the session"
  - llm_judge: "had the member already ruled this out, and not said so?"
applies_when: >
  You have been on the problem for a while and have ruled things out.
not_when: >
  It is the first attempt, or the earlier attempts are already in the transcript the
  agent can see.
shipped: 2026-09
recipe: |
  <what is wrong>

  Already tried, and what happened:
  - <attempt> → <result>
  - <attempt> → <result>
---
Include the result of each attempt. For example, "restarting cleared it for ten minutes"
provides more evidence than "I tried restarting."
