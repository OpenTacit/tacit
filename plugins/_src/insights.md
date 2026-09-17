+++
slug = insights
short = Tacit technique-usage metrics (funnel, helped rate) as a dashboard with text fallback — for who/where questions use {{cmd "org"}}
long = Show Tacit technique-usage metrics (funnel, helped rate) as a dashboard with text fallback. Use when the member invokes {{.Idiom}} or asks for adoption or helped-rate numbers. For who/where questions (cohorts, spread), use {{cmd "org"}}.
arg_hint = 7d | 30d | 90d | all (default 30d)
allowed_tools = Bash(curl:*)
+++
{{preamble}}

Show the organization's Tacit technique-usage insights for the window **{{.ArgRef}}**
(default `30d` if empty; valid values: 7d, 30d, 90d, all) as a graphical
dashboard where the harness supports it, and as text everywhere else.

## Method

1. Call the `{{tool "metrics"}}` MCP tool (from the `tacit` MCP server) with
   `view` set to `funnel` and `window` set. It returns three things in one result:
   - an **interactive HTML panel** (a `ui://tacit/insights` resource) rendered
     as a dashboard by Claude web/desktop and other MCP-app clients;
   - a **text summary** (the fallback the terminal shows); and
   - a **structured summary** (the numbers, for your own reference).

   If the tool is unavailable, denied, or its result is too large to return,
   fall back to the registry API and use its `text` field verbatim. Keep
   `format=text`: without it the reply carries the whole HTML panel, which no
   terminal can render and which crowds out the answer.

   ```bash
   curl -s --max-time 5 "$REG/v1/insights/app?w=<window>&format=text" \
     -H "X-Tacit-Key: $KEY"
   ```

2. Read the live in-session counters (the one thing the panel doesn't carry):

   ```bash
   curl -s --max-time 3 http://127.0.0.1:8787/v1/hooks/stats
   ```

   (If the hook daemon is idle-exited this fails: report the daemon as healthy
   and idle, and omit the counters.)

## Report

- When the graphical panel renders, let it carry the metrics. In text-only
  views, use the tool's text summary: the funnel headline
  (shown → adopted → helped), the helped and fit-check decline rates, and the
  technique summaries by helped rate and time to help.
- Add the current hook-daemon counters (sessions, shown, adopted, offered) if
  available.
- Quote measured rates exactly as returned.
- Include the dashboard link for playbook coverage and recent techniques:
  `$DASH/outcomes`.
