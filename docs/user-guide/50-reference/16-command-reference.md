# Command reference

All commands are in one binary. Run `tacit help` to see the same list in
your terminal. If you run a command with wrong arguments, the command shows
its own usage.

## Set up and connect

| Command | What it does |
|---|---|
| `tacit init` | Set up a registry on this machine: configuration, API key, starter techniques, semantic retrieval, and an owner to sign in as. At a terminal it asks whether to keep the registry running in the background, and installs a service for your user if you say yes; in a script it installs none unless you pass `--service`. It reports what it did in a few lines; `--verbose` prints every step as it happens. It is safe to run the command again. Flags: `--embeddings` (on, off, auto), `--service` (systemd, launchd, none, auto; default none), `--owner` (on, off, auto), `--global-access` (on, off, auto), `--port`, `--start-over` (set up a new registry where a merged one was). |
| `tacit join <join-url>` | Join a registry from an invite link. Then wire this machine's harnesses. |
| `tacit connect [--registry URL --code C] [--harness NAME]` | Point this machine at a registry and wire its AI tools (all tools found, or one). The command saves member settings in `~/.config/tacit/agent.env` and makes sure that they are correct, then wires. With no flags, it reports where this machine points and re-wires. `--code` takes the handoff code from an invitation page and trades it for a member key: it works once and expires in 15 minutes. `--key` still takes a member key directly. Also: `--segment team=…,role=…`, `--session-salt`, and `--settings-only` for a machine with no AI tool on it. |
| `tacit merge <join-url> [--keep-address]` | Fold this machine's personal registry into an organization's: contribute its techniques, archive its evidence, point the harnesses at the new registry, then stop the local registry and release its address. Also: `--dry-run`, `--yes`. |
| `tacit disconnect [--harness NAME] [--registry --yes]` | The opposite of connect: unwire harnesses and retire member settings. With `--registry --yes`, the command also removes this machine's registry service. |
| `tacit invite [--ttl 24h] [--repo]` | Make a join link that a teammate can run. `--repo` also commits a keyless registry marker to the repository. |

## Day to day

| Command | What it does |
|---|---|
| `tacit audit <source>` | Audit a conversation against the organization's playbook. The source is a share URL, a transcript file, a captured session file, or `-` with `--text`. `--live <session-id>` audits a session turn by turn as it runs. |
| `tacit feedback <technique-id> --stage <stage>` | Record feedback for a technique. The stage is one of: shown, adopted, helped, or dismissed. `helped` also records the adoption. For a dismissal, add `--reason` (not-relevant, already-knew, didnt-work). |
| `tacit revise <technique-id> --<field> <value> [--note …]` | Propose a change to a technique as a revision draft. The command changes only the fields that you pass. |
| `tacit usage [--window 30d]` | Show your OWN OpenTacit activity from this machine's local log: queries, and suggestions shown, adopted, helped, and dismissed. It includes a breakdown for each technique. Use `--json` or `--html` for other formats. The dashboard's Usage view shows the same numbers. |
| `tacit usage --key` | Print the key that opens your usage on the dashboard, for a registry that runs somewhere other than this machine. Paste it into the Usage page once per browser. It stays in that browser: the registry never receives it and cannot read your usage without it, and anyone who has it can, so treat it like a password. |
| `tacit usage --publish` | Seal this machine's usage and file it with the registry now. Use it to update the dashboard before the running agent's next timed upload. |
| `tacit conventions [--root DIR]` | Report differences among your projects' convention files (`CLAUDE.md`, `AGENTS.md`, `.cursorrules`, and the Copilot instructions) and identify harnesses that read no conventions in a project. `--root` takes one repository or a directory of them. `--write` writes the playbook into every convention file a project already has, inside a managed block that leaves the rest of the file alone; `--create` also writes `CLAUDE.md` and `AGENTS.md` where a project has neither. |
| `tacit statusline` | Show the status-line segment for your harness: session counters and health warnings. |
| `tacit ask "<task>"` | Get one technique from the playbook for the task in hand: what to do, when it applies, and what happened when colleagues used it. With a model key set, the technique is checked against your task before you see it and you are told plainly when nothing fits. Without one, the results are labelled as retrieved and unchecked. |
| `tacit cohorts [--json]` | List the cohorts that colleagues already use, so you join one instead of making a near-duplicate. `--skip` stops this machine being asked for a cohort; the choice stays on the machine and is never sent to the registry. |
| `tacit pause` | Stop this machine being observed: no capture, no suggestions, no record of the turn. It takes effect on your next turn, unwires nothing, and loses no settings. Asking still works. |
| `tacit resume` | Start again. Suggestions can reach you from your next turn. |
| `tacit dashboard` | Print a fresh owner sign-in link for this registry. For a single-member registry with no identity provider. |

## Operate the registry

| Command | What it does |
|---|---|
| `tacit serve` | Run the registry service in the foreground. Flags: `--host`, `--port`, `--data`, `--db`, `--techniques`, `--docs`. |
| `tacit secure [--off]` | Turn dashboard sign-in on with your identity provider, from the console of the registry machine. A single-owner registry can do the same thing with the Personal/Shared switch on **Settings → Access and sign-in**; this command is the way when the dashboard has no administrator, or when its sign-in already locks you out. The command derives the callback URL, tests the issuer's discovery document before it writes, completes all four `TACIT_OIDC_*` settings together, makes a session secret, and restarts. `--off` removes the settings and opens the dashboard again: it is the way back in when a sign-in configuration locks you out. Also: `--dry-run`, `--issuer`, `--client-id`, `--client-secret`, `--callback`, `--admins`, `--yes`, `--no-restart`. See [Turn on sign-in](../40-administration/14-configure-the-registry.md#turn-on-sign-in). |
| `tacit suggest [--n 10]` | Research practices that match observed usage, and file them as drafts. The drafts are general, not org-scoped. The Suggest candidate techniques button on Review does the same. |
| `tacit digest [--window 7d]` | Make the team digest as markdown. `--out` writes a file. `--slack <webhook>` posts it. |
| `tacit demo load [--scenario <key>]` | Fill a registry with a month of demonstration usage. Without `--force`, the command refuses a busy target. Also: `demo scenarios`, `demo generate`. |
| `tacit migrate-store --db <url>` | Copy the embedded file store into Postgres. It is safe to run the command again. The command accepts `--dry-run`. |
| `tacit feed export --channel <ch> --out <dir>` | Export a federation channel as static files. You can host the files anywhere. |

## Diagnose and maintain

| Command | What it does |
|---|---|
| `tacit doctor [flags]` | Is OpenTacit working here? `--ready` is the one to run after connecting: it answers in a single line and prints the full report only when something is wrong. Without flags it checks this machine's wiring in both roles. On a registry machine it also reports what the registry exposes: whether sign-in is on, and whether its settings are coherent. `--harness <name>` makes sure that one hook path operates end to end. `--deliver` arms the next turn to deliver one ◆ block, so you see whether suggestions render where you sit; `--deliver-status` reports what became of it. `--fix` repairs a rejected key. `--probe` is a bare liveness check: one request to `/v1/health`, exit 0/1, for container health checks and probes. |
| `tacit upgrade [--version vX.Y.Z]` | Replace this binary with a released build, and restart the service if one runs here. The command makes sure that the checksum is correct first. In a container, the command refuses. Pull a newer image tag instead. |
| `tacit version` | Print the build version. |

## Plumbing

You do not usually run these commands yourself. The plugins and services run
them:

| Command | What it does |
|---|---|
| `tacit serve-hooks` | Run the local in-harness agent. The relay starts it on demand. It stops after 15 minutes without activity. |
| `tacit hook-relay <harness>` | Forward one hook event to the local agent. If the agent is not active, the relay starts it. Harness hooks invoke this command. |
| `tacit env` | Print the resolved registry URL, key and dashboard address for skills to read (`eval "$(tacit env)"`). |
| `tacit eval retrieval --set <json>` | Compare dense retrieval with a hybrid challenger. `--model` and `--dim` override the configured embedder. |
| `tacit mcp` | Run the MCP server (stdio). It gives these tools: `tacit_search` (pull retrieval), `tacit_metrics` (funnel / cohorts / map views), `tacit_drafts` and `tacit_draft_action` (review), and `tacit_usage` (this machine's own activity). |

## Configuration files

| File | Role |
|---|---|
| `~/.config/tacit/agent.env` | Member settings. `connect` and `join` write this file |
| `~/.config/tacit/registry.env` | Registry settings. `init` and the setup form write this file |
| `~/.tacit-key.env` | Your optional model API key (`TACIT_LLM_API_KEY`) for the local agent |

Both `.env` files are plain `KEY=VALUE`. Environment variables override
them. Each command's own documentation has the full list of variables.
[Configure the registry](../40-administration/14-configure-the-registry.md)
shows the variables that you usually set by hand.
