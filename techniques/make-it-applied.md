---
id: make-it-applied
name: Make it solve your real problem, not just define terms
description: >
  Give the model your real task instead of asking a series of separate definition
  questions. It can then apply the terms to a step-by-step solution.
status: stable
provenance: curated
version: 1
tags: [prompting, reasoning, problem-solving]
task_types: [research, exploration]
triggers:
  - heuristic: "multiple separate 'explain' / 'define' questions with no concrete task"
  - llm_judge: "is the user collecting definitions rather than solving a real problem?"
applies_when: >
  The user asks a series of "explain/define" questions and clearly has a task or
  problem behind them.
not_when: >
  The user wants a neutral overview, or learns with no specific problem to solve.
shipped: 2023-01
recipe: |
  Here is my situation: [describe the real problem]. Apply how [topic] works: show me,
  step by step, what to check, what to run, and the likely causes. Do not only define
  the terms.
---
Applied to the problem in hand, the same concepts come back as a diagnosis rather than as
an explanation you still have to translate.
