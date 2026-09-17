# Technique schema

A technique is a reusable practice in Tacit's registry. Techniques are the unit that
the audit pipeline **retrieves** (stage 2) and the unit that **outcome write-back**
(stage 4) attaches statistics to. Authors write them as Markdown with frontmatter;
the frontmatter defines the structured fields below.

Two fields support retrieval and ranking:

- **`embedding`** lets retrieval find techniques by similarity to conversation features.
- **`outcomes`** contains measured results by segment for ranking.

```yaml
# --- identity ---
id: read-images-directly                 # stable kebab-case slug
name: "Attach the screenshot directly"
description: "Let the model read an image or PDF directly."
scope: general                           # general (model technique) | org (proprietary to this org)
status: stable                           # draft | mined | stable | decayed | retired
provenance: curated                      # curated | mined | contributed
version: 3                               # bump on any change to recipe/triggers

# --- retrieval ---
embedding: [0.013, -0.044, ...]          # vector over name+description+triggers
tags: [modality, vision, data-entry]
triggers:                                # detection signals for retrieval similarity
  - heuristic: "user pastes a textual description of a UI, table, or image"
  - llm_judge: "did the user manually transcribe visual content the model could read?"
applies_when: >                          # LLM-facing: when this is the right move (precision)
  The user manually transcribed an image/table/screenshot they could have attached.
not_when: >                              # LLM-facing: anti-conditions (negatives embeddings miss)
  The user already attached the file, or no visual/source artifact is involved.
task_types: [data-extraction, summarization, debugging-from-screenshot]

# --- the offer ---
recipe: |                                # reusable instructions or prompt
  Here's a screenshot. Read it directly and pull out [what you need].
  [attach image]
support_matrix:                          # where it works; versioned because techniques change
  - {harness: chatgpt, surface: mobile,  supported: true,  verified: 2026-05}
  - {harness: claude,  surface: mobile,  supported: true,  verified: 2026-05}
before_after: "Before: user retyped a table by hand. After: attached it; instant, accurate."

# --- measured outcomes (written back by stage 4) ---
outcomes:
  overall: {shown: 4120, adoption_rate: 0.61, helped_rate: 0.93, sample_size: 2513}
  by_segment:                            # enables "people like you" ranking + personalization
    - {segment: "role:marketer",   adoption_rate: 0.68, helped_rate: 0.95, sample_size: 612}
    - {segment: "role:developer",  adoption_rate: 0.44, helped_rate: 0.88, sample_size: 333}
  last_updated: 2026-05-30

# --- freshness ---
freshness:
  shipped: 2025-11                       # when the technique became available (may be post-cutoff)
  decay_signal: false                    # true when helped_rate drops sharply across users
  decay_checked: 2026-05-30
```

## Field notes

- **`scope`** identifies applicability. `general` techniques use public model techniques.
  `org` techniques depend on the organization's tools, data, or conventions. Federation
  exports `general` techniques. The default is `general`.
- **`status`** identifies lifecycle state. `mined` techniques are auto-discovered candidates;
  `decayed` status pauses suggestions until repair (set by stage 4 when `helped_rate`
  falls across many users).
- **`outcomes.helped_rate × sample_size`** is the primary ranking key the audit prompt
  uses; `by_segment` lets the same technique rank differently for different users.
- **`recipe`** contains the reusable instructions presented to the user. Tested variants
  can replace it over time.
- **`freshness.shipped`** lets the registry surface techniques released after the
  language model's training cutoff.
- **`triggers` and `applies_when`/`not_when`** serve different stages. `triggers` feed
  retrieval. `applies_when` and `not_when` provide fit conditions for the audit model.
  A matching `not_when` condition excludes the candidate.
- **`embedding`** is recomputed when `name`/`description`/`triggers`/`applies_when`
  change (version bump).
