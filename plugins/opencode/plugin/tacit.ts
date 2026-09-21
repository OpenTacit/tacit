// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Tacit for opencode — the observe-only in-harness coaching layer.
//
// opencode's plugin system is its hook surface (hooks.json has no
// equivalent), so this plugin is the opencode counterpart of the Claude Code
// package's hooks.json: it maps opencode's plugin hooks and bus events onto
// the shared Tacit hook vocabulary and forwards each one to the local hook
// agent (docs/harness/in-harness-hooks.md). The pull-based ask path
// (tacit_search / tacit_insights) lives elsewhere because opencode has an
// MCP client, so `tacit mcp` is wired as an MCP server in opencode.jsonc,
// exactly like Claude Code's .mcp.json.
//
//   chat.message                -> UserPromptSubmit  (model latched here)
//   tool.execute.after          -> PostToolUse
//   event session.created       -> SessionStart
//   event session.idle          -> Stop              (turn end)
//   event session.deleted       -> SessionEnd
//   experimental.text.complete  -> accumulated; rides Stop as assistant_text
//
// Distinctive to opencode: the assistant's text arrives INLINE — the
// text-complete hook streams every completed text part, so Stop carries
// assistant_text directly and capture never needs a transcript file.
//
// Transport is fetch-first: POST straight to the warm agent (~1ms); if
// nothing is listening, fall back to spawning `tacit hook-relay opencode`,
// which forwards the event AND auto-starts the agent for the next one
// ("on-demand lifecycle"). Techniqueinal rule, inherited from the relay: NEVER
// break the member's turn — every handler is wrapped and time-capped; any
// failure resolves to a no-op. Observe-only: nothing here blocks or alters a
// tool call.
//
// Delivery:
//   - Stop suggestion (same turn): a TUI toast — visible immediately, but
//     transient and not model-facing (opencode has no way to append to a
//     finished turn without triggering a new one). /tacit-review exists for
//     an in-conversation rendering.
//   - Parked suggestion (slow-LLM fallback): the UserPromptSubmit response
//     is injected as a synthetic text part on the member's own message —
//     visible in the transcript AND in the model's context, the same
//     additionalContext semantics Claude Code gets.
//
// Install: symlink or copy this file into ~/.config/opencode/plugin/ (or add
// its path to `plugin` in opencode.jsonc), and merge opencode.jsonc.example
// for the MCP server. Set TACIT_BIN if `tacit` is not on PATH.

import { spawn } from "node:child_process"
import { readFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"
import type { Plugin } from "@opencode-ai/plugin"

// Mirror tacit's Go config loader enough for the plugin's own direct paths:
// environment wins, then ~/.config/tacit/agent.env, then documented defaults.
// The spawned `tacit` binary uses the full Go loader for everything else.
function agentEnv(): Record<string, string> {
  try {
    const out: Record<string, string> = {}
    for (const raw of readFileSync(join(homedir(), ".config", "tacit", "agent.env"), "utf8").split("\n")) {
      const line = raw.trim()
      if (!line || line.startsWith("#")) continue
      const eq = line.indexOf("=")
      if (eq < 0) continue
      const key = line.slice(0, eq).trim()
      const val = line.slice(eq + 1).trim().replace(/^["']|["']$/g, "")
      if (key) out[key] = val
    }
    return out
  } catch {
    return {}
  }
}

const AGENT_ENV = agentEnv()
function env(keys: string[], fallback: string): string {
  for (const k of keys) if (process.env[k]) return process.env[k]!
  for (const k of keys) if (AGENT_ENV[k]) return AGENT_ENV[k]
  return fallback
}

const HARNESS = "opencode"
const TACIT_BIN = env(["TACIT_BIN"], "tacit")
const AGENT_URL = env(
  ["TACIT_HOOKS_URL"],
  `http://${env(["TACIT_HOOKS_HOST"], "127.0.0.1")}:${env(
    ["TACIT_HOOKS_PORT"],
    "8787",
  )}`,
)
const HOOKS_KEY = env(["TACIT_HOOKS_KEY"], "dev-hooks-key")

// The direct POST is capped tight; the relay fallback is generous enough for
// the one-time agent spawn it performs (health-polled for up to ~10s).
const FETCH_TIMEOUT_MS = 5_000
const SPAWN_TIMEOUT_MS = 15_000
// Assistant text cap per turn — matches the Go side's ingestion cap, so the
// plugin never ships more than capture would keep.
const MAX_ASSISTANT_CHARS = 8_000

type HookResponse = {
  systemMessage?: string
  hookSpecificOutput?: { hookEventName?: string; additionalContext?: string }
}

// relay forwards one payload to the local hook agent. Fast path: direct POST
// to the (usually warm) agent. Slow path: spawn the relay binary, which
// forwards this event and auto-starts the agent. Any failure — nothing
// listening and spawn fails, timeout, bad JSON — resolves to {}.
async function relay(payload: Record<string, unknown>): Promise<HookResponse> {
  const body = JSON.stringify(payload)
  try {
    const headers: Record<string, string> = { "Content-Type": "application/json" }
    if (HOOKS_KEY) headers["X-Tacit-Key"] = HOOKS_KEY
    const resp = await fetch(AGENT_URL + "/v1/hooks/" + HARNESS, {
      method: "POST",
      headers,
      body,
      signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
    })
    if (resp.ok) return (await resp.json()) as HookResponse
  } catch {}
  try {
    const out = await runProcess(TACIT_BIN, ["hook-relay", HARNESS], body, SPAWN_TIMEOUT_MS)
    return out ? (JSON.parse(out) as HookResponse) : {}
  } catch {
    return {}
  }
}

// runProcess spawns a command, writes stdin, and resolves with stdout —
// empty on timeout or spawn failure, never a rejection that could surface
// into a turn.
function runProcess(bin: string, args: string[], stdin: string, timeoutMs: number): Promise<string> {
  return new Promise((resolve) => {
    let settled = false
    const finish = (out: string) => {
      if (!settled) {
        settled = true
        resolve(out)
      }
    }
    try {
      const proc = spawn(bin, args, { stdio: ["pipe", "pipe", "ignore"] })
      const chunks: Buffer[] = []
      const timer = setTimeout(() => {
        proc.kill()
        finish("")
      }, timeoutMs)
      proc.stdout.on("data", (c: Buffer) => chunks.push(c))
      proc.on("error", () => {
        clearTimeout(timer)
        finish("")
      })
      proc.on("close", () => {
        clearTimeout(timer)
        finish(Buffer.concat(chunks).toString("utf8"))
      })
      proc.stdin.write(stdin)
      proc.stdin.end()
    } catch {
      finish("")
    }
  })
}

// deliverable extracts the member-visible suggestion text from an agent
// envelope, or "".
function deliverable(resp: HookResponse): string {
  const visible = typeof resp.systemMessage === "string" && resp.systemMessage !== ""
  if (!visible) return ""
  return resp.hookSpecificOutput?.additionalContext || resp.systemMessage!
}

export const TacitPlugin: Plugin = async ({ client, directory }) => {
  // Per-session turn state: the assistant text accumulated since the last
  // user message, and the model latched from chat.message. Sessions are
  // reaped lazily — entries are deleted at session.deleted/idle flush and the
  // maps are tiny (one entry per live session).
  const assistantText = new Map<string, string>()
  const modelBySession = new Map<string, string>()

  function appendAssistant(sessionID: string, text: string) {
    if (!text) return
    const cur = assistantText.get(sessionID) ?? ""
    if (cur.length >= MAX_ASSISTANT_CHARS) return
    assistantText.set(sessionID, (cur ? cur + "\n" : "") + text.slice(0, MAX_ASSISTANT_CHARS - cur.length))
  }

  return {
    // The member's half of the turn. Also where a PARKED suggestion (the
    // slow-LLM fallback) is delivered: injected as a synthetic text part on
    // the member's own message — visible and model-facing, the same
    // semantics as Claude Code's additionalContext.
    "chat.message": async (input, output) => {
      try {
        const sessionID = input.sessionID ?? output.message?.sessionID ?? "unknown"
        if (input.model) modelBySession.set(sessionID, `${input.model.providerID}/${input.model.modelID}`)
        assistantText.delete(sessionID) // a new turn starts a fresh assistant buffer
        const prompt = (output.parts ?? [])
          .map((p: any) => (p?.type === "text" && !p.synthetic ? String(p.text ?? "") : ""))
          .filter(Boolean)
          .join("\n")
        const resp = await relay({
          hook_event_name: "UserPromptSubmit",
          session_id: sessionID,
          cwd: directory,
          model: modelBySession.get(sessionID),
          prompt,
        })
        const text = deliverable(resp)
        if (text && output.message?.id) {
          output.parts.push({
            id: "prt_tacit" + Date.now().toString(36),
            sessionID,
            messageID: output.message.id,
            type: "text",
            text,
            synthetic: true,
          } as any)
        }
      } catch {}
    },

    // A completed tool call (name + input + result) — the signal
    // characterization uses. Observe-only: output is read, never modified.
    "tool.execute.after": async (input, output) => {
      try {
        await relay({
          hook_event_name: "PostToolUse",
          session_id: input.sessionID,
          tool_name: input.tool,
          tool_input: (input as any).args,
          tool_output: typeof output?.output === "string" ? output.output : "",
        })
      } catch {}
    },

    // The assistant's half of the turn, streamed as parts complete.
    "experimental.text.complete": async (input, output) => {
      try {
        appendAssistant(input.sessionID, String(output?.text ?? ""))
      } catch {}
    },

    // Bus events carry the turn/session lifecycle.
    event: async ({ event }) => {
      try {
        const e = event as any
        switch (e?.type) {
          case "session.created": {
            const id = e.properties?.info?.id ?? e.properties?.sessionID
            if (id) await relay({ hook_event_name: "SessionStart", session_id: id, cwd: directory })
            break
          }
          case "session.idle": {
            // Turn end: ship the turn (with the accumulated assistant text)
            // and deliver any same-turn suggestion as a toast. Transient by
            // nature; /tacit-review renders in-conversation on demand.
            const id = e.properties?.sessionID
            if (!id) break
            const text = assistantText.get(id) ?? ""
            assistantText.delete(id)
            const resp = await relay({
              hook_event_name: "Stop",
              session_id: id,
              model: modelBySession.get(id),
              assistant_text: text,
            })
            const suggestion = deliverable(resp)
            if (suggestion) {
              await client.tui
                .showToast({ body: { title: "◆ Tacit", message: suggestion, variant: "info" } })
                .catch(() => {})
            }
            break
          }
          case "session.deleted": {
            const id = e.properties?.info?.id ?? e.properties?.sessionID
            if (id) {
              assistantText.delete(id)
              modelBySession.delete(id)
              await relay({ hook_event_name: "SessionEnd", session_id: id })
            }
            break
          }
        }
      } catch {}
    },
  }
}
