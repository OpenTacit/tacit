# Tacit for GitHub Copilot CLI

The distributable Copilot CLI integration. What a member sees once it is installed:
[How Tacit behaves in your tools](../../docs/user-guide/20-sessions/03-how-tacit-behaves-in-your-harness.md).

**This subtree is a valid Copilot CLI plugin** (`plugin.json` at its root):
orgs that mirror the materialized tree to a git repository can install it with
`copilot plugin install <owner>/<repo>:copilot`. `tacit connect` skips the
plugin system and writes the same pieces to user-level locations directly
(hooks file, skills, MCP config) — see the guide.

Layout:

- `plugin.json` — the plugin manifest.
- `hooks.json` — the six relay hooks (sessionStart, userPromptSubmitted,
  preToolUse, postToolUse, agentStop, sessionEnd). Copilot payloads carry no
  event name, so each hook command passes the event to the relay
  (`hook-relay copilot <event>`); timeouts are seconds (`timeoutSec`). The
  same file is what connect copies to `~/.copilot/hooks/tacit.json`.
- `skills/tacit-*/SKILL.md` — the fourteen skills, ported from
  `plugins/codex/skills` with ask-by-name phrasing ("tacit search", "is tacit
  up"); Copilot discovers them by name/description. Keep bodies aligned.
- `agents/tacit-review.agent.md` — the review agent (`--agent tacit-review`
  or repo `.github/agents/`).
- `.mcp.json` — the `tacit` MCP server (`tacit_search` / `tacit_insights`).

**Observation-only tier.** No Copilot hook output renders to the member
(`agentStop` supports only `decision: block/allow`; `userPromptSubmitted`
documents no output contract), so the hook agent deliberately never
synthesizes suggestions for `copilot` sessions — capture, implicit feedback,
and audit facts flow; the member-visible surface is the ask path (skills +
MCP). See `deliverless` in `internal/auditor/hooks/agent.go`.

`{{TACIT_BIN}}` is rewritten to the member's absolute binary path when
`tacit connect` materializes this tree. Event/field mapping lives in
`internal/auditor/capture/copilot.go`.
