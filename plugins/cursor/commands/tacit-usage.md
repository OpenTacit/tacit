# tacit-usage

Show the current member's OWN Tacit usage — their queries, and suggestions shown, adopted, and measurably helped over time, with a per-technique breakdown — read from this machine's local log. Use when the member invokes /tacit-usage or asks "how am I using Tacit". Personal and machine-local, never the org's cohort aggregates (for those use /tacit-insights).

## First: resolve the registry address

Run this once. Each command block starts a fresh shell, so substitute the
**resolved values** into every command and put the resolved URL (`$DASH`) in
member-facing links:

```bash
eval "$(tacit env)"   # sets REG (API base), KEY (X-Tacit-Key), DASH (dashboard URL)
```

Show **how the current member is using Tacit** — their own queries, and the
suggestions shown, adopted, and measurably helped, over time, with a per-technique
breakdown, for the window **whatever the member wrote after the /tacit-usage command** (default `30d`; valid: 7d, 30d, 90d, all).

This is personal and **machine-local**: the registry never collects it. It is read
from this machine's usage log by the `tacit` binary — there is nothing to fetch
from the registry.

## Method

1. Show the text summary inline — this renders in every harness:

   ```bash
   tacit usage --window <window>
   ```

   Substitute `<window>` with **whatever the member wrote after the /tacit-usage command** (omit `--window` for the `30d`
   default). If `tacit` is not on PATH, or the log does not exist yet, say so
   plainly — usage accrues once the member works with their agent.

2. For the **interactive panel** (chart, per-technique drill-down, window switcher),
   point the member at the dashboard's Usage page in a browser. It fetches this
   machine's hook agent over loopback, so it shows their real usage without the
   data leaving the machine:

       $DASH/usage

   Do not try to render the panel inline: `tacit usage --html` produces a
   self-contained page, but agent and terminal surfaces show it as a download.
   Open it in a browser for the chart, the drill-down, and the window switcher.

## Report

- Lead with the headline from the text summary: queries, and the
  shown → adopted → helped funnel with the adoption and helped rates.
- Quote measured rates exactly as returned.
- Include the `$DASH/usage` link for the interactive view.
- Reassure on privacy when relevant: this is read from the member's own machine
  and is never sent to the registry.
