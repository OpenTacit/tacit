---
id: tailor-the-ask
name: Tell it who you are and why you're asking
description: >
  A generic "explain X" prompt gives a generic answer. State your role, level, and
  goal, and the model adjusts depth, approach, and examples to you.
status: stable
provenance: curated
version: 1
tags: [prompting, context, personalization]
task_types: [research, conversation]
triggers:
  - heuristic: "short generic 'explain X' or 'what is X' prompt with no stated goal or level"
  - llm_judge: "did the user omit their role, skill level, or why they want to know?"
applies_when: >
  The user asks a broad "explain X" or "what is X" question and does not give their
  role, their level, or the reason they want to know.
not_when: >
  The user already gave their background and goal, or the task is a quick factual
  lookup and an adjusted approach does not change the answer.
support_matrix:
  - {harness: chatgpt, surface: web, supported: true, verified: 2026-05}
  - {harness: chatgpt, surface: mobile, supported: true, verified: 2026-05}
  - {harness: claude, surface: mobile, supported: true, verified: 2026-05}
shipped: 2023-01
recipe: |
  I am a [your role, e.g. backend engineer] who is [new to / experienced with] this topic.
  Explain [topic] at a practical working level. Focus on what I must think about when I
  work on [your goal]. Use one concrete example through the full answer, and name the
  usual errors.
---
Before: a generic "explain X" gave a textbook overview. After: the user gave a role and
a goal, and the answer matched the reader, with a relevant example and the usual errors.
