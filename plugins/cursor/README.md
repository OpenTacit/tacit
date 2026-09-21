# Tacit for Cursor

The distributable Cursor integration. What a member sees once it is installed:
[How Tacit behaves in your tools](../../docs/user-guide/20-sessions/03-how-tacit-behaves-in-your-harness.md).

Layout:

- `hooks.json` — the seven relay hooks (sessionStart, beforeSubmitPrompt,
  preToolUse, postToolUse, afterAgentResponse, stop, sessionEnd). Cursor
  payloads name their own event, so one relay command serves all; timeouts
  are seconds. `tacit connect` merges these entries into
  `~/.cursor/hooks.json` (a single shared file — the merge is additive and
  reversible, backed up first).
- `commands/tacit-*.md` — the fourteen `/tacit-*` slash commands, ported
  from `plugins/codex/skills` (plain markdown; Cursor commands have no
  frontmatter). Keep bodies aligned.
- `mcp.json.example` — the `tacit` MCP server for `~/.cursor/mcp.json`.

**Park-only delivery.** Cursor's `stop` output cannot render to the member
(`followup_message` would force another agent loop), but
`beforeSubmitPrompt`'s `user_message` does render — so suggestions always
park at stop and deliver visibly with the member's next prompt; `shown`
records at that delivery (`parkOnly` + `TranslateCursorResponse` in
`internal/auditor/hooks/agent.go`). Event/field mapping:
`internal/auditor/capture/cursor.go` (conversation_id → session key,
workspace_roots → cwd, afterAgentResponse → inline assistant text).

`{{TACIT_BIN}}` is rewritten to the member's absolute binary path when
`tacit connect` materializes this tree. Hooks fire in the IDE agent, the
Cursor CLI, and cloud agents (cloud agents read project/team hooks only —
see the guide).
