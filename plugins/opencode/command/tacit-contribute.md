---
description: Capture a reusable approach from the current session as a draft Tacit technique
---

Capture a reusable approach from the current session as a draft Tacit technique.
Use `org` scope for approaches that depend on the organization's tools, data,
or conventions. This turns the current session into a draft technique (formally, a
technique) and submits it. Drafts are excluded from retrieval until a
reviewer promotes them.

## 1. Draft the technique from the session

From what the member just did, draft ALL of these fields yourself first (infer
sensible values; keep them concrete and reusable across instances):

- **name** (required): a short imperative title; draft 2–3 candidate phrasings.
- **description** (required): 1–2 sentences describing the approach and purpose.
- **recipe** (required): a paste-ready prompt/steps a colleague can reuse. For
  `org` moves, name the actual internal tool/connector/dataset. Use
  placeholders like `<table>` where specifics vary.
- **applies_when** / **not_when**: when it applies and when it does not
  (`not_when` matters most: it keeps the technique precise).
- **tags**, **task_types**: a few short keywords each.

## 2. Confirm the structured fields

Confirm the choice-shaped fields with the member. Use the harness's **native
question/picker form** if it has one (one call, three questions); otherwise ask
in ONE compact numbered chat message so the member can answer in a few
characters (e.g. "org, 2, tags 1+3"). Recommend a default for each:

- **Scope**: `org: uses our internal tools/data` (the recipe names something
  only this organization has) or `general: works anywhere` (a technique of the
  model itself).
- **Name**: your 2–3 drafted options, ordered by relevance, one line each on its
  emphasis; the member can also type their own.
- **Tags**: your drafted tags; the member picks the ones that fit.

Then show the **recipe** and **description** (fenced, exactly as they'd be
stored) and invite tweaks: free text is better edited in prose than in a form.
An `org` recipe must name the right internal tool; get that confirmed
explicitly.

## 3. Final confirmation with preview

Show a one-line preview (name · scope · first line of the recipe) and ask:
**submit for review / keep editing / discard**. Honor the answer exactly; on
discard, confirm the draft stayed local.

## 4. Submit it

POST the confirmed technique to the local hook agent (the agent stamps the
contributor cohort and forwards to the registry):

```bash
curl -s -X POST http://127.0.0.1:8787/v1/hooks/contribute \
  -H "Content-Type: application/json" \
  -d '{
    "name": "<name>",
    "description": "<description>",
    "recipe": "<paste-ready recipe>",
    "scope": "org",
    "applies_when": "<when to use it>",
    "not_when": "<when not to>",
    "tags": ["<tag>", "<tag>"],
    "task_types": ["<task-type>"]
  }'
```

A `{"id": "...", "status": "draft", ...}` response means it was accepted. Tell
the member the assigned id and that it's a **draft pending review**: it won't be
suggested to colleagues until a reviewer promotes it. If the response has an
`error`, relay it and offer to fix the draft.
