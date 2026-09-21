---
id: delegate-the-wide-search
name: Delegate the wide search instead of reading files yourself
description: >
  For a search across a repository, delegate the search as a separate task. Ask for the
  relevant files, functions, and links between them instead of loading each file into
  the main session.
scope: general
status: stable
provenance: curated
version: 1
tags: [efficiency, navigation, context]
task_types: [delegation, exploration]
triggers:
  - heuristic: "more than five consecutive read or grep turns without an edit or a conclusion"
  - llm_judge: "was this a broad search whose intermediate file contents nobody needed?"
applies_when: >
  You want to find everywhere something happens, or where a behaviour lives, and the
  question spans many files or several naming conventions. You want the answer rather
  than the evidence.
not_when: >
  You know roughly where it is, or you need to read the code yourself to judge it.
shipped: 2026-09
recipe: |
  Search the repository for <what you are looking for> and report back just
  the conclusion: which files, which functions, and how they connect.
  Do not paste the file contents.
---
This keeps irrelevant file contents out of the main session's context.
