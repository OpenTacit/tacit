# Tacit for opencode

The opencode integration: hook capture via an [opencode plugin](https://opencode.ai/docs/plugins),
the ask path via opencode's native MCP client, and the `/tacit-*` command set.

## Features

- **In-flow suggestions**: the plugin captures each turn outside the tool-call path and
  audits it against the org registry. Relevant validated techniques appear in a
  `◆ Tacit` toast at turn end. Parked suggestions ride your next prompt as a
  synthetic message part (visible and model-facing).
- **A persistent review list in the sidebar**: the toast is transient; the
  `◆ Tacit` sidebar section (a TUI plugin) lists every suggestion shown this
  session with its evolving verdict (`·` shown, `✓` adopted, `⚡` helped,
  `×` dismissed), beside TODOs and changed files.
- **`tacit_search` / `tacit_insights`**: available through the `tacit` MCP server.
- **Commands**: `/tacit-search`, `/tacit-review`, `/tacit-audit`,
  `/tacit-org`, `/tacit-contribute`, `/tacit-drafts`, `/tacit-insights`,
  `/tacit-usage`, `/tacit-suggest`, `/tacit-status`, `/tacit-setup`,
  `/tacit-test`, `/tacit-testfeedback`, `/tacit-help`.

## Install

1. **Member wiring** (once): `tacit connect --registry <URL> --key <KEY>` writes
   `~/.config/tacit/agent.env`, which the plugin also reads.
2. **Plugin**:
   `ln -s /path/to/tacit/plugins/opencode/plugin/tacit.ts ~/.config/opencode/plugin/tacit.ts`
   (or add its path to `plugin` in `opencode.jsonc`).
3. **MCP + config**: merge `opencode.jsonc.example` into
   `~/.config/opencode/opencode.jsonc`, replacing the `{{TACIT_BIN}}`
   placeholder with the absolute path to the `tacit` binary (`tacit connect
   --harness opencode` does this substitution for you).
4. **Commands**:
   `ln -s /path/to/tacit/plugins/opencode/command/*.md ~/.config/opencode/commands/`
   (opencode also accepts a project-local `.opencode/commands/`).
5. **Sidebar (TUI plugin)**: merge `tui.json.example` into
   `~/.config/opencode/tui.json`, the configuration source for TUI plugins.

Set `TACIT_BIN` when `tacit` uses a custom path. The plugin POSTs to a warm
agent and falls back to `tacit hook-relay opencode`, which auto-starts it.

## Event mapping

| opencode | shared hook vocabulary |
|---|---|
| `session.created` (bus) | `SessionStart` |
| `chat.message` | `UserPromptSubmit` |
| `tool.execute.after` | `PostToolUse` |
| `session.idle` (bus) | `Stop`: carries `assistant_text` accumulated from `experimental.text.complete` |
| `session.deleted` (bus) | `SessionEnd` |

Assistant text arrives **inline** through event payloads. The plugin reads the model
from the provider/model pair in `chat.message`.
