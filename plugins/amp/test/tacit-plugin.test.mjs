// Source-level checks for the Amp plugin. CI has no full Amp host, so these
// checks guard the stable API contract without matching whole functions.
import { readFileSync } from "node:fs"
import { test } from "node:test"
import assert from "node:assert/strict"

const source = readFileSync(new URL("../plugins/tacit.ts", import.meta.url), "utf8")

test("exports a short static description", () => {
  const match = source.match(/export const description\s*=\s*\n?\s*"([^"]+)"/)
  assert.ok(match)
  assert.ok(match[1].length <= 300)
})

test("maps all five lifecycle events", () => {
  const mappings = {
    "session.start": "SessionStart",
    "agent.start": "UserPromptSubmit",
    "tool.call": "PreToolUse",
    "tool.result": "PostToolUse",
    "agent.end": "Stop",
  }
  for (const [event, hook] of Object.entries(mappings)) {
    assert.match(source, new RegExp(`amp\\.on\\("${event}"`), event)
    assert.match(source, new RegExp(`hook_event_name: "${hook}"`), hook)
  }
})

test("tool request explicitly allows at once and uses the warm relay path", () => {
  const start = source.indexOf('amp.on("tool.call"')
  const end = source.indexOf('amp.on("tool.result"', start)
  const handler = source.slice(start, end)
  assert.match(handler, /void enqueue\(/)
  assert.match(handler, /await post\(/)
  assert.match(handler, /WARM_POST_TIMEOUT_MS/)
  assert.doesNotMatch(handler, /await relay\(/)
  assert.match(handler, /return \{ action: "allow" \}/)
})

test("orders events per thread and awaits Stop", () => {
  assert.match(source, /new Map<string, Promise<void>>\(\)/)
  assert.match(source, /const prior = queues\.get\(threadID\)/)
  assert.match(source, /if \(queues\.get\(threadID\) === next\) queues\.delete\(threadID\)/)
  assert.match(source, /amp\.on\("agent\.end", async[\s\S]*?await enqueue\(event\.thread\.id/)
})

test("adds the workspace cwd to canonical payloads", () => {
  assert.match(source, /amp\.system\.workspaceRoot/)
  assert.match(source, /amp\.helpers\.filePathFromURI\(amp\.system\.workspaceRoot\)/)
  assert.match(source, /cwd \? \{ \.\.\.fields, cwd \} : fields/)
  assert.ok((source.match(/payload\(\{/g) ?? []).length >= 5)
})

test("shows a dialog only for the active event thread", () => {
  assert.match(source, /amp\.activeThread\.current\?\.id !== event\.thread\.id/)
  assert.match(source, /await ctx\.ui\.confirm\(\{/)
  assert.match(source, /title: "Tacit recommendation"/)
  assert.match(source, /message: text/)
  assert.match(source, /confirmButtonText: "Got it"/)
  assert.doesNotMatch(source, /ctx\.ui\.notify/)
  assert.match(source, /clients without plugin UI cannot open the dialog[\s\S]*?putPending\(event\.thread\.id, text\)/)
})

test("pending delivery uses per-thread atomic files", () => {
  assert.match(source, /"tacit", "amp-pending"/)
  assert.match(source, /safeThreadName\(threadID\)/)
  assert.match(source, /writeFileSync\(temp,/)
  assert.match(source, /renameSync\(temp, path\)/)
  assert.match(source, /renameSync\(path, claim\)/)
  assert.doesNotMatch(source, /\.tacit-amp-pending\.json/)
})

test("registers the guarded native form", () => {
  assert.match(source, /name: "tacit_form"/)
  assert.match(source, /questions:/)
  assert.match(source, /multiSelect:/)
  assert.match(source, /allowOther: true/)
  assert.match(source, /options: \["\(done selecting\)", \.\.\.available\]/)
  assert.match(source, /optionText\(option\) === answer/)
  assert.doesNotMatch(source, /ctx\.thread\.state\.get\(\)[\s\S]*?formFallback/)
  assert.match(source, /isPluginUINotAvailableError/)
  assert.match(source, /JSON\.stringify\(\{ answers \}\)/)
})

test("registers three idle active-thread palette commands", () => {
  assert.equal((source.match(/amp\.registerCommand\(/g) ?? []).length, 3)
  for (const title of ["Search Playbook", "Contribute Current Work", "Review Drafts"]) {
    assert.match(source, new RegExp(`title: "${title}"`))
  }
  assert.equal((source.match(/category: "Tacit"/g) ?? []).length, 3)
  assert.match(source, /setAvailability/)
  assert.match(source, /thread\.state\.subscribe/)
  assert.match(source, /watchActive\(amp\.activeThread\.current\)/)
  assert.doesNotMatch(source, /setInterval|setTimeout\([^)]*availability/)
})

test("does not read identity or copy MCP business tools", () => {
  assert.doesNotMatch(source, /system\.user|\.workspace\b/)
  assert.equal((source.match(/amp\.registerTool\(/g) ?? []).length, 1)
  for (const tool of ["tacit_search", "tacit_drafts", "tacit_draft_action", "tacit_metrics", "tacit_usage"]) {
    assert.doesNotMatch(source, new RegExp(`name: "${tool}"`))
  }
})
