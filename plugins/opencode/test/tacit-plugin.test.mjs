// Source-level checks on the opencode plugin — the same portability and
// contract assertions the pi extension test makes (a full opencode host is
// not available in CI; the Go suite covers the agent side of the contract).
import { readFileSync } from "node:fs"
import { test } from "node:test"
import assert from "node:assert/strict"

const source = readFileSync(new URL("../plugin/tacit.ts", import.meta.url), "utf8")

test("plugin is portable: no developer-local tacit binary path", () => {
  assert.equal(source.includes("/home/"), false)
  assert.match(source, /const TACIT_BIN = env\(\["TACIT_BIN"\], "tacit"\)/)
})

test("direct hook POST uses the same default hooks key as the Go daemon", () => {
  assert.match(
    source,
    /const HOOKS_KEY = env\(\["TACIT_HOOKS_KEY"\], "dev-hooks-key"\)/,
  )
  assert.match(source, /if \(HOOKS_KEY\) headers\["X-Tacit-Key"\] = HOOKS_KEY/)
})

test("plugin reads tacit setup's agent.env fallback", () => {
  assert.match(source, /\.config", "tacit", "agent\.env"/)
  assert.match(source, /const AGENT_ENV = agentEnv\(\)/)
})

test("relays under the opencode harness id", () => {
  assert.match(source, /const HARNESS = "opencode"/)
  assert.match(source, /hook-relay", HARNESS/)
})

test("maps every lifecycle event onto the shared hook vocabulary", () => {
  for (const ev of ["SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"]) {
    assert.match(source, new RegExp(`hook_event_name: "${ev}"`), ev)
  }
})

test("assistant text is accumulated inline and capped", () => {
  assert.match(source, /experimental\.text\.complete/)
  assert.match(source, /assistant_text: text/)
  assert.match(source, /MAX_ASSISTANT_CHARS = 8_000/)
})

test("every handler is failure-proof: wrapped in try/catch", () => {
  // Each hook body opens with try { — a thrown error must never surface
  // into the member's turn.
  const handlers = source.match(/async \((?:input|_?\{ event \})[^)]*\) => \{\s*try \{/g) ?? []
  assert.ok(handlers.length >= 4, `expected >=4 wrapped handlers, found ${handlers.length}`)
})

test("observe-only: the plugin never blocks or rewrites a tool call", () => {
  assert.equal(source.includes("tool.execute.before"), false)
  assert.equal(source.includes("permission.ask"), false)
})

// The TUI sidebar plugin (the persistent review list).
const tuiSource = readFileSync(new URL("../plugin/tacit-tui.tsx", import.meta.url), "utf8")

test("tui plugin is the tui module shape and registers the sidebar slot", () => {
  assert.match(tuiSource, /@jsxImportSource @opentui\/solid/)
  assert.match(tuiSource, /sidebar_content\(/)
  assert.match(tuiSource, /const plugin: TuiPluginModule/)
})

test("tui plugin polls the agent's suggestions endpoint for its session only", () => {
  assert.match(tuiSource, /\/v1\/hooks\/suggestions\?session_id=/)
  assert.match(tuiSource, /props\.session_id/)
})

test("tui plugin refreshes on session.idle and never throws into the TUI", () => {
  assert.match(tuiSource, /session\.idle/)
  assert.match(tuiSource, /catch \{\}/)
})
