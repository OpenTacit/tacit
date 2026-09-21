+++
slug = search
short = Search your org's Tacit playbook for validated techniques
long = Search the organization's Tacit playbook for techniques relevant to a task. Use when the member invokes {{.Idiom}} or asks to search Tacit for something.
arg_hint = what you're trying to do
allowed_tools = Bash(curl:*)
+++
{{preamble}}

Search the organization's Tacit playbook and report what applies.

This is the **one-query** member of a family that all run the same playbook
retrieval, differing only in what they point it at: **{{cmd "search"}}** a query
you name, **{{cmd "review"}}** the work you have in flight, **{{cmd "audit"}}** a
finished session (which also closes the loop with feedback and contributions).
Reach for whichever matches your scope — the evidence underneath is identical.

**Query:** {{.ArgRef}}
(If empty, derive the query from what the member is currently working on in this
conversation. Describe the activity in plain words.)

## Method

1. Call the `{{tool "search"}}` MCP tool (from the `tacit` MCP server) with the
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
