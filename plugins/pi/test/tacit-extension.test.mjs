import { readFileSync } from "node:fs"
import { test } from "node:test"
import assert from "node:assert/strict"

const source = readFileSync(new URL("../extensions/tacit.ts", import.meta.url), "utf8")

test("extension is portable: no developer-local tacit binary path", () => {
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

test("extension reads tacit setup's agent.env fallback", () => {
  assert.match(source, /\.config", "tacit", "agent\.env"/)
  assert.match(source, /const AGENT_ENV = agentEnv\(\)/)
})
