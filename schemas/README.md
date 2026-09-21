# Schemas

Data contracts for Tacit's audit pipeline — characterize, retrieve, ground and
synthesize, then write the outcome back. Together they support evidence
selection and model-written guidance.

| File | What it is | Pipeline role |
|---|---|---|
| [`technique.md`](./technique.md) | one reusable "move", with embedding + measured outcomes | **retrieved** in stage 2; ranked by its `outcomes` in stage 3 |
| [`feedback-event.md`](./feedback-event.md) | one step of a suggestion's lifecycle (shown → adopted → helped) | the **write-back** in stage 4; aggregates into technique `outcomes` |
| [`intelligence-report.schema.json`](./intelligence-report.schema.json) | signed, aggregate-only results for an imported technique | the optional cross-registry import → outcome return path |

Data flow:

```
conversation ─▶ characterize ─▶ retrieve techniques ─▶ ground & synthesize (audit)
                                        ▲                              │
                                        │                              ▼
                                  technique.outcomes ◀── aggregate ── feedback events
```

The audit prompt that consumes the retrieved techniques lives at
`prompts/audit-system-prompt.md` (the `COLLECTIVE EVIDENCE` block it expects is
assembled from techniques + their segmented outcomes).
