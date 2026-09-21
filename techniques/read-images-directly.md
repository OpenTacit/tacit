---
id: read-images-directly
name: Attach the screenshot instead of retyping it
description: >
  Attach an image, table, screenshot, or PDF instead of transcribing it. This saves time
  and avoids transcription errors.
status: stable
provenance: curated
version: 1
tags: [modality, vision, data-entry]
task_types: [verification, exploration]
triggers:
  - heuristic: "user pastes a textual description or transcription of a UI, table, or image"
  - llm_judge: "did the user manually transcribe visual content the model could read?"
applies_when: >
  The user transcribed or described an image, table, screenshot, or PDF by hand, but
  an attachment of the source was possible.
not_when: >
  The user already attached the file, or there is no visual source artifact.
shipped: 2025-11
recipe: |
  Here is a screenshot. Read it directly and extract [what you need]. [attach the image]
---
Retyping what a model can read is slower and introduces errors the original does not
have.
