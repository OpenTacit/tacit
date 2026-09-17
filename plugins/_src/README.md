# plugins/_src — canonical skill sources

One file per logical Tacit skill. `make plugins` (the `cmd/tacit-genplugins`
generator) reads these and writes every per-harness skill/command file:

    plugins/amp/skills/tacit-<slug>/SKILL.md      (YAML frontmatter + body)
    plugins/codex/skills/tacit-<slug>/SKILL.md
    plugins/copilot/skills/tacit-<slug>/SKILL.md
    plugins/pi/skills/tacit-<slug>/SKILL.md
    plugins/claude-code/commands/<slug>.md        (description/argument-hint/allowed-tools)
    plugins/cursor/commands/tacit-<slug>.md       (H1 title + body)
    plugins/gemini/commands/tacit/<slug>.toml     (description + prompt)
    plugins/opencode/command/tacit-<slug>.md      (description + body)

The generated files are committed output. **Never hand-edit a generated file.**
Edit the source here and run `make plugins`.
`cmd/tacit-genplugins/pluginsgen_test.go` fails CI if any committed file drifts
from what the generator produces.

## Source file format

A `+++`-fenced metadata block, then the shared markdown body:

    +++
    slug = search
    short = <command-style one-liner (claude-code, gemini, opencode)>
    long  = <skill-style description with a "Use when …" trigger (amp/codex/copilot/pi/cursor)>
    arg_hint = <argument hint; drives claude-code argument-hint and opencode "(arguments: …)">
    allowed_tools = <claude-code allowed-tools line>
    +++
    <body>

Every field and the body are Go `text/template`. The per-harness deltas are the
only things that vary; express them with:

- `{{.Idiom}}` — how this harness's member invokes the skill (`/tacit:search`,
  `$tacit-search`, `/skill:tacit-search`, `tacit search`, …).
- `{{.ArgRef}}` — the harness's argument reference (`$ARGUMENTS`, `{{args}}`, or
  a prose phrase for skill harnesses).
- `{{cmd "other-slug"}}` — a cross-reference to another skill, in this harness's
  idiom.
- `{{tool "search"}}` — the MCP tool's wire name, fully-qualified for claude-code
  (`mcp__plugin_tacit_tacit__tacit_search`) and bare elsewhere. Tool wire names
  live in one map (`toolWire`) in the generator, so a rename is one edit.
- `{{preamble}}` — the shared 8-line "resolve the registry address" bash block.

Harness formats, paths, idioms and argument conventions are the `harnesses`
table in `cmd/tacit-genplugins/main.go`.

## Exceptions (hand-maintained, NOT generated)

- **claude-code `skills/contribute/`** — a richer native AskUserQuestion
  multi-step form. It is a legitimate per-harness variant and lives outside the
  generator (`nativeSkills` in the generator skips generating claude-code's
  `contribute`). The other seven harnesses get `contribute.md` here.

## Retired

- **`feedback`** — deleted (plan P0.3). Ordinary reactions to a ◆ Tacit block
  are captured automatically; a standalone skill would double-count. Do not
  re-add it here.
