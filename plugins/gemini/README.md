# Tacit for Gemini CLI

The distributable Gemini CLI integration. What a member sees once it is installed:
[How Tacit behaves in your tools](../../docs/user-guide/20-sessions/03-how-tacit-behaves-in-your-harness.md).

Layout:

- `gemini-extension.json` — the extension manifest: the `tacit` MCP server
  (`tacit_search` / `tacit_insights`) and the `GEMINI.md` context file.
- `hooks/hooks.json` — the six relay hooks (SessionStart, BeforeAgent,
  BeforeTool, AfterTool, AfterAgent, SessionEnd). Extension hooks load from
  this file (same layout as the Claude Code plugin), NOT from the manifest —
  verified against the v0.51.0 extension loader (`loadExtensionHooks`).
  Timeouts are milliseconds.
- `GEMINI.md` — loaded into model context each session; names the
  ask-by-phrase → `/tacit:*` command mappings.
- `commands/tacit/*.toml` — the fourteen `/tacit:*` commands. Generated from the
  single canonical source per skill in `plugins/_src/` by `make plugins`; never
  hand-edit them (Gemini commands serve as the skill surface). Edit `_src` and
  regenerate.
- `settings.json.example` — manual wiring for members not using the extension:
  the same hooks + MCP config merged into `~/.gemini/settings.json`.

`{{TACIT_BIN}}` is a template token: `tacit connect` materializes this tree
(rewriting it to the member's absolute binary path) and installs via
`gemini extensions link <materialized>/gemini`. Hooks require member trust on
first run (recorded in `~/.gemini/trusted_hooks.json`).

Event mapping (Gemini names → canonical) lives registry-side in
`internal/auditor/capture/gemini.go` — the relay forwards payloads verbatim
under the `gemini` harness id; nothing in this package rewrites events.
