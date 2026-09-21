---
id: search-the-playbook-first
name: Search the playbook before you choose an approach
description: >
  Before you settle on a library, a pattern, or a migration path, ask the org's playbook
  whether colleagues already measured one. The answer carries how it went for them.
scope: org
status: stable
provenance: curated
version: 1
tags: [playbook, context, efficiency]
task_types: [research, exploration]
triggers:
  - heuristic: "the member or the agent proposes an approach, library or pattern with no reference to an org convention"
  - llm_judge: "is this a choice the organization has probably made before?"
applies_when: >
  You are about to commit to an approach in an area your colleagues also work in:
  a framework, a deploy path, a migration, a test strategy.
not_when: >
  You already searched the playbook for this work, the choice is confined to one file,
  or the question is about a public API that no org convention touches.
shipped: 2026-09
recipe: |
  @tacit do we have a validated approach for <what you are about to build>?

  For the full matches and their evidence, run `/tacit:search <the same question>`.
  A result of nothing means the org has no measured move here, so choose freely.
---
The search itself records nothing. A technique an `@tacit` answer quotes does count as
shown, because you read it. Read the n beside a rate before you weigh it: the same
percentage over two tries and over a hundred are not the same claim.
