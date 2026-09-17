---
name: tacit-setup
description: Wire this member's harness to the org Tacit registry: prompt for the registry URL and API key, save them to ~/.config/tacit/agent.env via `tacit connect --settings-only`, and verify. Use when a member asks to connect to Tacit, set the registry URL/key, or onboard this machine.
---

Wire this member into the org's Tacit registry. Settings are saved to
`~/.config/tacit/agent.env` (mode 600) by `tacit connect`, and every tacit
component (hooks, relay, MCP server, and CLI) reads that file automatically.
Environment variables provide optional overrides.

## 1. Show the current state

```bash
tacit connect --settings-only
```

Reports the currently resolved registry URL, whether a key is set, the
member's cohort, and the settings-file path. If it already points at the org
registry with an accepted key AND a cohort is set, say so and stop. If the
cohort line reads `not set`, skip to step 4 — an unwired cohort is worth
fixing on its own, even when everything else is already connected.

## 2. Collect the two values

Ask the member (a native question form works well) for:

- **Registry URL**: where the org's `tacit serve` runs, e.g.
  `http://<registry-host>:8080` (may be given as the command argument).
- **API key**: the org's `X-Tacit-Key`. If the member prefers the key not
  appear in the conversation, have them run the save themselves by typing
  `! tacit connect --registry <url> --key <key>`: same effect.

## 3. Save and verify

```bash
tacit connect --registry "<url>" --key "<key>"
```

The command saves the file, then checks the registry is reachable and the key
is accepted. Report both results. A 401 means the key is wrong: do
not retry variations; tell the member to check with their Tacit admin.

## 4. Offer a cohort, don't ask for one cold

A cohort is a group label such as `team=payments,role=engineer`. It names a
group, never the member (cohorts, not identities), rides `shown`/`adopted`/
`helped` events, and is what makes **Insights → Cohorts** and the organization
view work. It is optional; if the member declines, proceed without it.

Never ask for it from nothing. The key is saved now, so look at what the org
already uses:

```bash
tacit cohorts
```

That lists every cohort value on this registry, per dimension, with how many
sessions each has been seen in. **Offer those values as the choices** — a
native question form per dimension where the harness has one, otherwise read
the top few out — and always include a "something else" option so a new team
can name itself. Existing values are the whole point: `payments` and
`payments-eng` are two different cohorts to every report in the registry, and
nothing anywhere warns that they were meant to be one.

Settable dimensions are `team`, `role`, `function`, and `domain`; `harness` and
`surface` fill themselves in and are not a choice. If `tacit cohorts` lists
nothing, this member is the first — ask for their team and role in their own
words and tell them the names they pick are what colleagues will see and match.

Save the answer:

```bash
tacit connect --segment team=...,role=...
```

It reports whether the cohort joined one colleagues already use or started a
new one. Relay that either way: it is the last cheap moment to catch a typo.

## 5. Confirm end to end

Suggest /skill:tacit-status (stack health) and /skill:tacit-test (full pipeline).
If the member previously exported `TACIT_REGISTRY_URL`/`TACIT_API_KEY` in
their shell profile, note those exports override the file and can now be
removed.
