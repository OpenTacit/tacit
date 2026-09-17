---
id: ask-for-the-failing-test-first
name: Ask for the failing test before the fix
description: >
  Write a test that reproduces the bug before making the fix. This confirms the test
  can detect the bug and that the fix addresses it.
scope: general
status: stable
provenance: curated
version: 1
tags: [testing, verification, debugging]
task_types: [verification, editing]
triggers:
  - heuristic: "a bug fix whose accompanying test passes against the unfixed code"
  - llm_judge: "does this test fail without the change it ships with?"
applies_when: >
  Fixing a defect with a reproducible symptom.
not_when: >
  New behaviour with no bug behind it, or a change whose failure mode cannot be
  expressed as a test.
shipped: 2026-09
recipe: |
  Before fixing this, write a test that fails because of the bug. Run it and show me
  the failure. Then fix it and run it again.
---
Check that the test fails for the expected reason. A summary does not show whether the
test exposed the bug or failed due to an error in the test, so inspect the output.
