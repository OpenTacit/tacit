# tacit-insights

Show Tacit technique-usage metrics (funnel, helped rate) as a dashboard with text fallback. Use when the member invokes /tacit-insights or asks for adoption or helped-rate numbers. For who/where questions (cohorts, spread), use /tacit-org.

## First: resolve the registry address

Run this once. Each command block starts a fresh shell, so substitute the
**resolved values** into every command and put the resolved URL (`$DASH`) in
member-facing links:

```bash
eval "$(tacit env)"   # sets REG (API base), KEY (X-Tacit-Key), DASH (dashboard URL)
```

Show the organization's Tacit technique-usage insights for the window **whatever the member wrote after the /tacit-insights command**
(default `30d` if empty; valid values: 7d, 30d, 90d, all) as a graphical
dashboard where the harness supports it, and as text everywhere else.

## Method

1. Call the `tacit_metrics` MCP tool (from the `tacit` MCP server) with
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
