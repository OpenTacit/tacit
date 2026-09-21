+++
slug = org
short = How the organization uses its playbook — cohort spread, opportunities, proponents (cohort aggregates, never individuals)
long = Show how the organization uses its playbook — cohort spread, opportunities, and proponent cohorts (aggregates only, never individuals). Use when the member invokes {{.Idiom}} or asks who is adopting what, where practice is established, or where a technique could spread.
arg_hint = 7d | 30d | 90d | all (default 30d)
allowed_tools = Bash(curl:*)
+++
{{preamble}}

Show how the organization uses its playbook for the window **{{.ArgRef}}**
(default `30d` if empty; valid values: 7d, 30d, 90d, all): who is adopting
what, where practice is established, and where a validated move has room to
spread. This is the who/where view — for pipeline metrics (funnel, helped
rate, evidence mix) use {{cmd "insights"}} instead.

## Method

1. Call the `{{tool "metrics"}}` MCP tool (from the `tacit` MCP server) with
   `view` set to `cohorts` and `window` set. It returns a text summary plus structured numbers. If the tool
   is unavailable or denied, fall back to the registry API and use its `text`
   field verbatim:

   ```bash
   curl -s --max-time 5 "$REG/v1/organization/app?w=<window>&format=text" \
     -H "X-Tacit-Key: $KEY"
   ```

## Report

- Present the summary as returned: adoption breadth, the spreading areas
  (with their cohort deltas), opportunities, and proponent cohorts.
- **Keep the framing intact**: these are cohort-level aggregates — routes for
  knowledge transfer, never individual rankings. Do not editorialize a cohort
  line into a statement about a person, even in a small team.
- Quote measured numbers exactly as returned; never invent or round them into
  claims.
- Include the dashboard link for the full map: `$DASH/outcomes`.
