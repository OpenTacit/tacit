# Tacit plugins

Distributable integrations for **Claude Code** (`claude-code/`),
**OpenAI Codex CLI** (`codex/`), **Gemini CLI** (`gemini/`),
**GitHub Copilot CLI** (`copilot/`), **Cursor** (`cursor/`), **Amp** (`amp/`),
**pi** (`pi/`, also serving omp), and
**opencode** (`opencode/`).

> `tacit connect` automates the installs below. The plugin tree ships inside the
> tacit binary. The command writes it to disk, rewrites the binary path for the
> current machine, detects installed harnesses, installs files, and merges config.
> When a harness controls part of the flow, it prints the required step. The
> sections below document the manual path.

All the packages talk to the **local hook agent**, a loopback HTTP server. Each
wires its events through a command **relay** (`tacit hook-relay <harness>`, referenced
by absolute path because hooks use an isolated PATH) that auto-starts the agent on the
first hook of a session and lets it idle-exit when quiet. Claude Code and Codex
run the relay as shell hooks; Amp, pi, and opencode map their native events onto
the same relay through a TypeScript plugin/extension. For persistent operation under supervision, start the agent:

```bash
tacit serve-hooks --segment team=revops   # needs the registry (tacit serve) running too
```

The agent sends tool calls through unchanged, processes events outside the request path,
limits suggestion frequency, and reports aggregate outcomes.

## Claude Code: self-serve

The Tacit plugin marketplace is this repository (github.com/opentacit/tacit, under
`plugins/`) — there is no separate marketplace repo. The checked-in tree is a
template (the binary path is a placeholder), so point Claude Code at the
materialized copy; `tacit connect --harness claude-code` does both steps.
Manually, inside Claude Code:

```
/plugin marketplace add <materialized plugins dir>
/plugin install tacit@tacit
```

Pick **user** scope to be coached across all projects. Use `/hooks` to see exactly what was
registered: the plugin is intentionally inspectable. The plugin ships the hooks, the
`tacit_search` + `tacit_insights` MCP tools, the full `/tacit:*` command set, the
contribute skill (a native AskUserQuestion form), and the `tacit-review` agent.

## Codex CLI: self-serve

Add the marketplace (the materialized copy of this repository's `plugins/`
tree — `tacit connect --harness codex` automates it) as a shell subcommand,
then install from the in-session browser:

```
codex plugin marketplace add <materialized plugins dir>
```

Then, inside a Codex session, run `/plugins`, switch to the Tacit marketplace, and install
**tacit-codex** (Space toggles enabled state). On first run Codex shows a "Hooks need
review" prompt: choose **Trust all and continue**; trust is recorded against the relay
command's content hash, so you're not re-prompted unless it changes. `/hooks` inspects at any
time. The manual configuration path merges `codex/config.toml.example` into
`~/.codex/config.toml` and provides the same behavior directly.

For full parity with the Claude Code plugin, also wire the two pieces covered in
`codex/config.toml.example`:

```bash
codex mcp add tacit -- /path/to/tacit mcp    # the tacit_search tool
cp -r /path/to/plugins/codex/skills/* ~/.codex/skills/   # all fourteen tacit skills
```

Codex skills are invoked with a `$` mention (type `$` to pick): `$tacit-help`,
`$tacit-search`, `$tacit-status`, `$tacit-insights`, `$tacit-org`, `$tacit-usage`,
`$tacit-setup`, `$tacit-suggest`, `$tacit-test`, `$tacit-testfeedback`,
`$tacit-drafts`, `$tacit-review`, `$tacit-audit`, `$tacit-contribute`. Copy the
skills because skill discovery does not follow symlinked directories reliably.

## Gemini CLI: self-serve

The extension system owns install; connect drives it non-interactively:

```bash
tacit connect --harness gemini     # materializes + `gemini extensions link --consent`
```

Or by hand: `gemini extensions link <materialized plugins dir>/gemini`. The
package bundles the six relay hooks (`hooks/hooks.json` — the loader reads
hooks from that file, NOT the manifest; timeouts are milliseconds), the tacit
MCP server, the `GEMINI.md` context, and the fourteen `/tacit:*` commands
(`commands/tacit/*.toml`). Approve the tacit-relay hooks when the harness
prompts (`~/.gemini/trusted_hooks.json`). Manual settings wiring:
`gemini/settings.json.example`.

## Copilot CLI: self-serve

```bash
tacit connect --harness copilot
```

Writes user-level wiring: `~/.copilot/hooks/tacit.json` (the six relay hooks —
Copilot payloads carry no event name, so each command passes it:
`hook-relay copilot <event>`; timeouts are seconds), the fourteen `tacit-*`
skills into `~/.copilot/skills/` (keep frontmatter descriptions QUOTED —
Copilot's YAML parser rejects unquoted colons), and the tacit MCP server
merged into `~/.copilot/mcp-config.json`. **Observation-only tier**: no
Copilot hook output renders to the member, so the agent never synthesizes
suggestions there — ask-path only ("tacit help", "search tacit for …", the
`tacit-review` agent). Orgs can serve `copilot/` from a git repo as a plugin
(`copilot plugin install <owner>/<repo>:copilot` — it carries `plugin.json`).

## Cursor: self-serve

```bash
tacit connect --harness cursor
```

JSON-merges the seven relay hooks into `~/.cursor/hooks.json` (additive per
event, member entries untouched, backup on first touch; timeouts are
seconds), installs the fourteen `/tacit-*` commands into
`~/.cursor/commands/`, and merges the tacit MCP server into
`~/.cursor/mcp.json`. **Park-only delivery**: Cursor's stop output can't
render to the member, so suggestions park and arrive visibly
(`user_message`) with the next prompt — `shown` records at that delivery.
Team rollout: put the same hook entries in a repo's `.cursor/hooks.json` or
the Enterprise/Team hook tiers.

## Amp: self-serve

Amp installation uses two copies and a settings merge, with all three steps in
`amp/settings.jsonc.example`:

```bash
cp plugins/amp/plugins/tacit.ts ~/.config/amp/plugins/        # events -> hook relay
cp -r plugins/amp/skills/* ~/.config/agents/skills/           # the full tacit-* skill set
# then merge amp/settings.jsonc.example into ~/.config/amp/settings.json (MCP wiring)
```

Reload with the command palette's `plugins: reload` (or restart Amp). Project-scoped
variants use `.amp/plugins/`, `.agents/skills/`, and `.amp/settings.json`. Copy the skills;
skill discovery may not load them directly from the checkout.

Two Amp-specific behaviors to know:

- **Delivery uses a native dialog**: at `agent.end`, the plugin opens the full suggestion with
  `ctx.ui.confirm` and waits for the member to dismiss it. For a background thread or a client
  without plugin UI, it parks the suggestion under
  `$XDG_DATA_HOME/tacit/amp-pending` (or `~/.local/share/tacit/amp-pending`) and injects it at
  the next `agent.start`.
- **Skills are agent-invoked**: Amp loads them on demand by name/description. Ask
  directly ("search tacit for …", "is tacit up?",
  "contribute this to Tacit").

The plugin's `tool.call` handler returns an empty action and lets each tool call proceed
unchanged. Its implementation is in `tacit.ts`.

## opencode: symlink + config merge

opencode loads plugins from `~/.config/opencode/plugin/` and commands from
`~/.config/opencode/commands/`; MCP servers come from `opencode.jsonc`:

```bash
ln -s /path/to/tacit/plugins/opencode/plugin/tacit.ts ~/.config/opencode/plugin/tacit.ts
ln -s /path/to/tacit/plugins/opencode/command/*.md    ~/.config/opencode/commands/
# then merge plugins/opencode/opencode.jsonc.example into ~/.config/opencode/opencode.jsonc
```

## pi: self-serve

Install the pi package with one command:

```bash
pi install /path/to/tacit/plugins/pi        # local checkout registered in settings
# or, from the org's repo:
pi install git:github.com/opentacit/tacit   # if plugins/pi is published as a pi package
```

The package manifest (`pi/package.json`) loads the extension and all fourteen skills; `pi
list` shows it, `pi config` toggles individual resources, `pi remove` uninstalls. Set
`TACIT_BIN` in your environment when the tacit binary uses a custom path. Use `-l`
for a project installation in `.pi/settings.json`; the default is `~/.pi/agent/settings.json`.

Three pi-specific behaviors to know:

- **The extension registers `tacit_search` and `tacit_insights` as native pi tools.**
  Each call speaks the same JSON-RPC to a spawned
  `tacit mcp`, so output (and search's shown-event recording) is identical to the other
  harnesses. As a TUI, pi presents the dashboard's text summary.
- **Delivery**: the ◆ Tacit block appears at turn end (`agent_end` →
  `pi.sendMessage`), parked suggestions ride the next prompt visibly, and the tier-2
  follow-up directive is injected model-facing-only (`display: false`). The extension also
  registers a native **AskUserQuestion** picker for the one-keystroke question flow and
  explicit feedback capture.
- **Skills are both slash-invoked and agent-invoked**: type `/skill:tacit-search <goal>`
  (or any `/skill:tacit-*`), or just ask ("search tacit for …", "is tacit up?",
  "contribute this to Tacit").
- **A footer status line** (`ctx.ui.setStatus`): the pi counterpart of the Claude Code
  statusline: `◆ tacit N⚡ M✓` for this session's shown/adopted, plus `K⚑` when K drafts
  await review. Claude Code and pi show the indicator; Codex and Amp provide the same
  numbers through `tacit-status`.

The extension's `tool_call` handler returns an empty action and lets each tool call proceed
unchanged. Its implementation is in `pi/extensions/tacit.ts`.

### omp (oh-my-pi) reuses this package

**omp** is a pi-compatible fork served by the shared `plugins/pi` package. The extension
is **harness-aware**: it detects omp and relays under the `omp`
harness id (attributed separately from pi), while the status line (`ctx.ui.setStatus`, which
omp renders), delivery, and the registered tools are identical. Install **bun-free** via
config (recommended: omp's `plugin install/uninstall` shells out to `bun`, which may not be
present):

```bash
omp config set extensions '["/path/to/tacit/plugins/pi/extensions/tacit.ts"]'
omp config set skills.customDirectories '["/path/to/tacit/plugins/pi/skills"]'
```

`omp plugin link /path/to/tacit/plugins/pi` also works directly. Later `omp plugin
uninstall`/`upgrade` operations require bun.

## Org-managed rollout

Push it via policy so every member gets it automatically: Claude Code managed settings
(`extraKnownMarketplaces` / `enabledPlugins` / `strictKnownMarketplaces`); Codex
`requirements.toml` managed hooks (trusted-by-policy, un-disableable); Amp
managed settings (`/etc/ampcode/managed-settings.json` on Linux, which overrides
individual settings) with the plugin/skill files delivered by the same MDM; for pi,
deliver the package by MDM and add it to the `packages` array of
`~/.pi/agent/settings.json` (or ship a project `.pi/settings.json` in the repo: pi
auto-installs missing packages after the project is trusted). For opencode, distribute its plugin, commands,
`opencode.jsonc` MCP configuration, and the `tacit` binary through your existing device
management mechanism.

## Layout

```
plugins/
  .claude-plugin/marketplace.json  lists the Claude Code + Codex plugins for
                                   `plugin marketplace add` (Amp reads no marketplace format)
  claude-code/
    .claude-plugin/plugin.json     manifest (name, version, description)
    hooks/hooks.json               hook config: `tacit hook-relay` → local agent :8787
    .mcp.json                      tacit MCP server (tacit_search + tacit_insights app)
    commands/                      the full /tacit:* command set (contribute ships as a
                                   native skill instead of a command)
    skills/contribute/             technique-authoring skill (native AskUserQuestion form)
    agents/tacit-review.md         deep on-demand registry review
  codex/
    .codex-plugin/plugin.json      manifest (name, semver version, description)
    hooks/hooks.json               command-relay hook config (Codex is command-only)
    config.toml.example            manual-path config: [hooks] + [mcp_servers.tacit]
    skills/                        the full $tacit-* skill set: the Claude Code command
                                   set + review (≙ the tacit-review agent) + contribute in
                                   conversational form (Codex has no AskUserQuestion)
  gemini/
    gemini-extension.json          extension manifest (context file + tacit MCP server)
    hooks/hooks.json               relay hook config (the loader reads hooks here,
                                   NOT the manifest; timeouts are milliseconds)
    GEMINI.md                      context file bundled with the extension
    commands/tacit/                the /tacit:* commands (*.toml)
    settings.json.example          manual settings wiring
  copilot/
    plugin.json                    manifest (for the org git-repo plugin install path)
    hooks.json                     relay hooks (each command names its event; timeouts
                                   are seconds)
    .mcp.json                      tacit MCP server
    skills/                        the tacit-* skills (frontmatter descriptions quoted
                                   for Copilot's YAML parser)
    agents/tacit-review.agent.md   deep on-demand registry review
  cursor/
    hooks.json                     relay hooks, JSON-merged into ~/.cursor/hooks.json
                                   (timeouts are seconds)
    commands/                      the /tacit-* commands
    mcp.json.example               tacit MCP server configuration
  amp/                             no manifest: Amp has no plugin marketplace
    plugins/tacit.ts               the hooks.json equivalent: Amp plugin-API events
                                   (session.start/agent.start/tool.call/tool.result/
                                   agent.end) -> `tacit hook-relay amp`
    settings.jsonc.example         install steps + amp.mcpServers (+ amp.skills.path)
    skills/                        the same skills, phrased for Amp's agent-invoked
                                   loading by name and description
  pi/                              a pi package: `pi install <path-or-git>` wires everything
    package.json                   manifest: pi.extensions + pi.skills
    extensions/tacit.ts            the hooks.json equivalent: pi extension events
                                   (session_start/before_agent_start/tool_call/tool_result/
                                   agent_end/session_shutdown) -> `tacit hook-relay pi`,
                                   plus registered tools: tacit_search + tacit_insights (pi has
                                   no MCP client, so the extension shells out to `tacit mcp`)
                                   and AskUserQuestion (native picker for the tier-2 flow)
    skills/                        the same skills, phrased for pi's /skill:tacit-*
                                    commands and agent-invoked loading
  opencode/                        TypeScript plugin package
    plugin/tacit.ts                opencode plugin events -> local hook agent (direct POST,
                                    relay fallback); observe-only, self-time-capped
    opencode.jsonc.example         `tacit mcp` server configuration
    command/                       /tacit-* commands
```
