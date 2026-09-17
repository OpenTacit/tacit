---
name: contribute
description: Capture a reusable approach from the current session as a draft Tacit technique. Use when the member says things like "contribute this to Tacit", "capture this as a technique", "capture this as a technique", "add this move to the registry", "/tacit:contribute",. Drives a native multi-step form for the structured fields, then submits for review.
---

# Contribute a technique

Use `org` scope for approaches that depend on the organization's tools, data, or conventions.
This skill turns the current session into a draft technique (formally, a technique) and submits it. Drafts are
excluded from retrieval until a reviewer promotes them.

## 1. Draft the technique from the session

From what the member just did, draft ALL of these fields yourself first (infer sensible values;
keep them concrete and reusable across instances):

- **name** (required): a short imperative title: draft 2–3 candidate phrasings.
- **description** (required): 1–2 sentences describing the approach and purpose.
- **recipe** (required): a paste-ready prompt/steps a colleague can reuse. For `org` moves, name
  the actual internal tool/connector/dataset. Use placeholders like `<table>` where specifics vary.
- **applies_when** / **not_when**: when it applies and when it does not (`not_when` matters
  most: it keeps the technique precise).
- **tags**, **task_types**: a few short keywords each.

## 2. Confirm the structured fields in a native form

Use the **AskUserQuestion** tool for the fields that are choices (one call, three questions):

- **Question 1** (header `Scope`): "Where does this move work?"
  - `org: uses our internal tools/data` (the recipe names something only this organization has)
  - `general: works anywhere` (a technique of the model itself)
- **Question 2** (header `Name`): your 2–3 drafted name options, ordered by relevance, each with a one-line
  description of its emphasis. The member can always pick "Other" and type their own.
- **Question 3** (header `Tags`, **multiSelect**): your drafted tags as options; the member
  toggles the ones that fit.

Then show the **recipe** and **description** in chat (fenced, exactly as they'd be stored) and
ask for tweaks conversationally: free text is better edited in prose than in a form. An `org`
recipe must name the right internal tool; get that confirmed explicitly.

## 3. Final confirmation with preview

One last **AskUserQuestion** (header `Submit`): "Submit this technique to the review queue?"
- `Submit for review`: put the full drafted technique (name · scope · first line of the recipe) in
  this option's *description*, so the form itself shows the preview
- `Keep editing`: go back to step 2 with their corrections
- `Discard`: drop it entirely and confirm the draft stayed local

## 4. Submit it

POST the confirmed technique to the local hook agent with the Bash tool (the agent stamps the
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

A `{"id": "...", "status": "draft", ...}` response means it was accepted. Tell the member the
assigned id and that it's a **draft pending review**: it won't be suggested to colleagues until
a reviewer promotes it. If the response has an `error`, relay it and offer to fix the draft.
