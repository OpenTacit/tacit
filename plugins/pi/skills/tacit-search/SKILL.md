---
name: tacit-search
description: Search the organization's Tacit playbook for techniques relevant to a task. Use when the member invokes /skill:tacit-search or asks to search Tacit for something.
---

## First: resolve the registry address

Run this once. Each command block starts a fresh shell, so substitute the
**resolved values** into every command and put the resolved URL (`$DASH`) in
member-facing links:

```bash
eval "$(tacit env)"   # sets REG (API base), KEY (X-Tacit-Key), DASH (dashboard URL)
```

Search the organization's Tacit playbook and report what applies.

This is the **one-query** member of a family that all run the same playbook
retrieval, differing only in what they point it at: **/skill:tacit-search** a query
you name, **/skill:tacit-review** the work you have in flight, **/skill:tacit-audit** a
finished session (which also closes the loop with feedback and contributions).
Reach for whichever matches your scope — the evidence underneath is identical.

**Query:** whatever the member wrote after the /skill:tacit-search command
(If empty, derive the query from what the member is currently working on in this
conversation. Describe the activity in plain words.)

## Method

1. Call the `tacit_search` MCP tool (from the `tacit` MCP server) with the
   query. If the tool is unavailable or denied, fall back to the registry API:

   ```bash
   curl -s -X POST $REG/v1/evidence \
     -H "X-Tacit-Key: $KEY" -H "Content-Type: application/json" \
     -d '{"summary_text": "<the query, in plain words>"}'
   ```

2. **Filter candidates.** Retrieval is recall-oriented. Check each candidate's
   `applies_when`/`not_when` against what the member actually asked and retain
   candidates that fit. A zero-result response is valid.

## Report

For each technique that survives (best first, at most 4):

- **Name** `[id · scope]`.
- The **evidence line** verbatim when measured ("measured by colleagues: helped
  94% · n=120"); say "awaiting measured outcomes" when absent.
- One sentence on how it applies to this query, then the recipe ready to use.
- Technique page: `$DASH/techniques/<id>`
