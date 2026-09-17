// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Tacit for pi (and omp) — the observe-only in-harness coaching layer.
//
// pi has no shell-hook system and no MCP client (deliberately — extensions
// ARE its single integration surface), so this extension is the pi
// counterpart of the hooks.json + .mcp.json the Claude Code package ships:
// it maps pi's extension events onto the shared Tacit hook vocabulary,
// forwards each one to the local hook agent (docs/in-harness-hooks.md), and
// registers the two tools the other harnesses get from elsewhere.
//
// omp (oh-my-pi) is a pi-compatible fork with the SAME extension API, so this
// one file serves both. It detects the host at load time and relays under the
// matching harness id (/v1/hooks/pi vs /v1/hooks/omp) so sessions are
// attributed correctly; everything else — delivery, the status line
// (ctx.ui.setStatus, which omp renders too), the registered tools — is
// identical.
//
//   session_start      -> SessionStart       tool_call   -> PreToolUse
//   before_agent_start -> UserPromptSubmit   tool_result -> PostToolUse
//   agent_end          -> Stop               session_shutdown -> SessionEnd
//
// Transport is fetch-first: POST straight to the warm agent (~1ms); if
// nothing is listening, fall back to spawning `tacit hook-relay pi`, which
// forwards the event AND auto-starts the agent for the next one ("On-demand
// lifecycle"). Techniqueinal rule, inherited from the relay: NEVER break the
// member's turn — every handler is wrapped and time-capped; any failure
// resolves to a no-op. Observe-only: the tool_call handler never returns
// {block}, so it cannot stop or alter a tool call.
//
// Delivery uses the same envelopes as the Claude Code integration. pi can
// render each envelope the agent produces:
//
//   - Stop suggestion (same turn): agent_end calls pi.sendMessage(display:
//     true) — the ◆ Tacit block renders right under the answer it is about,
//     persists in the transcript, and is in the model's context next turn.
//   - Parked suggestion (slow-LLM fallback): before_agent_start returns the
//     UserPromptSubmit response as {message, display: true} — rides the
//     member's next prompt, visible and model-facing.
//
// The tier-2 auto-directive was dropped repo-wide (the agent no longer emits a
// model-facing-only envelope — Claude Code surfaced it as a sticky context
// chip). This extension still REGISTERS AskUserQuestion as a native pi tool
// (ctx.ui.select) so a deliberately-raised Tacit form — e.g. /tacit:feedback —
// and its PostToolUse funnel capture still work end to end.
//
// The extension also registers tacit_search (the pull-based "ask path") and
// tacit_insights (the stats dashboard). pi has no MCP client, so instead of
// wiring `tacit mcp` as a server it speaks the same protocol itself: each call
// spawns `tacit mcp`, performs initialize + tools/call over stdio, and returns
// the rendered result. A pi TUI can't render the insights app's HTML panel, so
// tacit_insights returns its text summary; the graphical panel shows in
// MCP-app harnesses.
//
// Install: `pi install /path/to/tacit/plugins/pi` — or, on omp,
// `omp plugin link /path/to/tacit/plugins/pi` (same package manifest, which
// loads this file and the skills/ directory). Set TACIT_BIN if `tacit` is not
// on PATH.

import { spawn } from "node:child_process"
import { readFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent"
import { Type } from "typebox"

// Mirror tacit's Go config loader enough for the extension's own direct paths:
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

// HARNESS is the hook-vocabulary identity this process relays under. pi and
// omp share this extension; omp (a Bun-compiled binary) is recognized by its
// argv0/argv1 so its sessions are attributed to the "omp" harness, not "pi".
function detectHarness(): string {
  try {
    const argv0 = (process.argv0 || "").toLowerCase()
    const argv1 = (process.argv[1] || "").toLowerCase()
    if (argv0 === "omp" || argv0.endsWith("/omp") || argv1.includes("omp")) return "omp"
  } catch {}
  return "pi"
}
const HARNESS = env(["TACIT_HARNESS"], detectHarness())

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
const SEARCH_TIMEOUT_MS = 20_000
// Footer status line: how often to refresh the ambient counters. The GET does
// not touch the agent, so polling never keeps it from idle-exiting.
const STATUS_POLL_MS = 15_000
const STATUS_TIMEOUT_MS = 2_000

type HookResponse = {
  systemMessage?: string
  hookSpecificOutput?: { hookEventName?: string; additionalContext?: string }
}

// --- relay -----------------------------------------------------------------

// relay forwards one payload to the local hook agent. Fast path: direct POST
// to the (usually warm) agent. Slow path: spawn the relay binary, which
// forwards this event and auto-starts the agent. Any failure — nothing
// listening and spawn fails, timeout, bad JSON — resolves to {}.
async function relay(payload: Record<string, unknown>): Promise<HookResponse> {
  const body = JSON.stringify(payload)
  try {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    }
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

// contentText flattens a tool result's content blocks to the text the
// characterizer reads.
function contentText(content: unknown): string {
  if (typeof content === "string") return content
  if (!Array.isArray(content)) return ""
  return content
    .map((b) => (b && typeof b === "object" && (b as any).type === "text" ? String((b as any).text ?? "") : ""))
    .filter(Boolean)
    .join("\n")
}

function sessionID(ctx: ExtensionContext): string {
  try {
    return ctx.sessionManager.getSessionId() ?? "unknown"
  } catch {
    return "unknown"
  }
}

// --- footer status line (the pi counterpart of the Claude Code statusline) ---

// setStatus lives on ctx.ui; typed structurally so a pi build without it just
// no-ops instead of throwing.
type StatusCtx = { hasUI?: boolean; ui?: { setStatus?: (key: string, text: string) => void } }

type StatsResponse = {
  shown?: number
  adopted?: number
  drafts?: number
  session?: { shown?: number; adopted?: number }
}

// updateStatus reads the shared hook-agent counters and paints the footer:
// this session's shown/adopted, plus the org-wide review-queue depth when any
// drafts await review. Any failure (agent idle-exited, absent) leaves the last
// value rather than flickering the line.
async function updateStatus(ctx: ExtensionContext): Promise<void> {
  const ui = (ctx as StatusCtx).ui
  if (!ui?.setStatus) return
  try {
    const url = AGENT_URL + "/v1/hooks/stats?session_id=" + encodeURIComponent(sessionID(ctx))
    const resp = await fetch(url, { signal: AbortSignal.timeout(STATUS_TIMEOUT_MS) })
    if (!resp.ok) return
    const s = (await resp.json()) as StatsResponse
    const sess = s.session ?? s
    let line = `◆ tacit ${sess.shown ?? 0}⚡ ${sess.adopted ?? 0}✓`
    if (typeof s.drafts === "number" && s.drafts > 0) line += ` ${s.drafts}⚑`
    ui.setStatus("tacit", line)
  } catch {}
}

// whenIdle polls until the agent loop has fully wound down (or the cap
// expires). A message sent before idle is treated as steering input — it
// would TRIGGER another LLM turn on the member's dime (observed live: an
// agent_end sendMessage without this guard produced an infinite respond/Stop
// loop). It must be called from a DETACHED task, never awaited inside the
// agent_end handler itself: the loop only goes idle after all agent_end
// handlers return, so waiting in-handler deadlocks into the cap (also
// observed live).
async function whenIdle(ctx: ExtensionContext, capMs: number): Promise<boolean> {
  const start = Date.now()
  for (;;) {
    try {
      if (ctx.isIdle()) return true
    } catch {
      return false
    }
    if (Date.now() - start >= capMs) return false
    await new Promise((r) => setTimeout(r, 50))
  }
}

// --- tacit_search (the ask path; pi has no MCP client) ----------------------

// mcpSearch runs one tacit_search call against a freshly spawned `tacit mcp`
// stdio server: initialize (id 1) + tools/call (id 2), newline-delimited
// JSON-RPC — the same slice of the protocol the server implements.
async function mcpSearch(query: string): Promise<{ text: string; isError: boolean }> {
  const requests =
    JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }) +
    "\n" +
    JSON.stringify({
      jsonrpc: "2.0",
      id: 2,
      method: "tools/call",
      params: { name: "tacit_search", arguments: { query } },
    }) +
    "\n"
  const out = await runProcess(TACIT_BIN, ["mcp"], requests, SEARCH_TIMEOUT_MS)
  for (const line of out.split("\n")) {
    if (!line.trim()) continue
    try {
      const msg = JSON.parse(line)
      if (msg.id === 2) {
        const text = contentText(msg.result?.content) || msg.error?.message || "registry unavailable"
        return { text, isError: Boolean(msg.result?.isError ?? msg.error) }
      }
    } catch {}
  }
  return { text: "registry unavailable: no response from tacit mcp", isError: true }
}

// mcpInsights runs one tacit_insights call against a freshly spawned `tacit mcp`. pi
// is a TUI and can't render the HTML app resource, so it returns only the text
// summary content item (the same fallback text-only MCP hosts show).
async function mcpInsights(window: string): Promise<{ text: string; isError: boolean }> {
  const requests =
    JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }) +
    "\n" +
    JSON.stringify({
      jsonrpc: "2.0",
      id: 2,
      method: "tools/call",
      params: { name: "tacit_insights", arguments: window ? { window } : {} },
    }) +
    "\n"
  const out = await runProcess(TACIT_BIN, ["mcp"], requests, SEARCH_TIMEOUT_MS)
  for (const line of out.split("\n")) {
    if (!line.trim()) continue
    try {
      const msg = JSON.parse(line)
      if (msg.id === 2) {
        const text = contentText(msg.result?.content) || msg.error?.message || "registry unavailable"
        return { text, isError: Boolean(msg.result?.isError ?? msg.error) }
      }
    } catch {}
  }
  return { text: "registry unavailable: no response from tacit mcp", isError: true }
}

// mcpOrg runs one tacit_org call against a freshly spawned `tacit mcp` —
// the who/where counterpart of mcpInsights, text-only by design.
async function mcpOrg(window: string): Promise<{ text: string; isError: boolean }> {
  const requests =
    JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }) +
    "\n" +
    JSON.stringify({
      jsonrpc: "2.0",
      id: 2,
      method: "tools/call",
      params: { name: "tacit_org", arguments: window ? { window } : {} },
    }) +
    "\n"
  const out = await runProcess(TACIT_BIN, ["mcp"], requests, SEARCH_TIMEOUT_MS)
  for (const line of out.split("\n")) {
    if (!line.trim()) continue
    try {
      const msg = JSON.parse(line)
      if (msg.id === 2) {
        const text = contentText(msg.result?.content) || msg.error?.message || "registry unavailable"
        return { text, isError: Boolean(msg.result?.isError ?? msg.error) }
      }
    } catch {}
  }
  return { text: "registry unavailable: no response from tacit mcp", isError: true }
}

// --- AskUserQuestion (native one-keystroke question form) -------------------

// The same input shape as Claude Code's AskUserQuestion, because the hook
// agent's question-funnel capture keys on it: tool_input.questions[].header
// tags the Tacit question, and the answers map (question text -> chosen
// label) rides the PostToolUse relay via this tool's result details.
const questionOption = Type.Object({
  label: Type.String({ description: "The display text for this option" }),
  description: Type.Optional(Type.String({ description: "What this option means" })),
})
const questionItem = Type.Object({
  question: Type.String({ description: "The complete question to ask the user" }),
  header: Type.String({ description: "Very short label for the question (max 12 chars)" }),
  options: Type.Array(questionOption, { description: "2-4 distinct choices" }),
  multiSelect: Type.Optional(Type.Boolean({ description: "Allow selecting multiple options" })),
})
const askUserQuestionParams = Type.Object({
  questions: Type.Array(questionItem, { description: "Questions to ask the user (usually one)" }),
})

const OTHER_LABEL = "Other…"
const DONE_LABEL = "(done selecting)"

async function askOne(
  ctx: ExtensionContext,
  q: { question: string; header: string; options: { label: string; description?: string }[]; multiSelect?: boolean },
): Promise<string | undefined> {
  const title = (q.header ? `[${q.header}] ` : "") + q.question
  const display = q.options.map((o) => (o.description ? `${o.label}: ${o.description}` : o.label))
  if (!q.multiSelect) {
    const picked = await ctx.ui.select(title, [...display, OTHER_LABEL])
    if (picked === undefined) return undefined
    if (picked !== OTHER_LABEL) return q.options[display.indexOf(picked)]?.label ?? picked
    return await ctx.ui.input(q.question, "type your answer")
  }
  const chosen: string[] = []
  for (;;) {
    const remaining = display.filter((_, i) => !chosen.includes(q.options[i].label))
    const picked = await ctx.ui.select(`${title} (multi-select)`, [...remaining, DONE_LABEL])
    if (picked === undefined || picked === DONE_LABEL) break
    chosen.push(q.options[display.indexOf(picked)]?.label ?? picked)
    if (chosen.length === q.options.length) break
  }
  return chosen.length ? chosen.join(", ") : undefined
}

// --- the extension -----------------------------------------------------------

export default function tacit(pi: ExtensionAPI) {
  // Answers stashed by AskUserQuestion executions, merged into the
  // PostToolUse relay payload (Claude Code carries them in tool_input).
  const answersByCall = new Map<string, Record<string, string>>()

  // Footer status poller — session-scoped (started here, cleared at shutdown),
  // per the pi guidance against starting background resources from the factory.
  let statusTimer: ReturnType<typeof setInterval> | undefined
  function startStatus(ctx: ExtensionContext) {
    if (!(ctx as StatusCtx).hasUI) return
    if (statusTimer) clearInterval(statusTimer)
    void updateStatus(ctx)
    statusTimer = setInterval(() => void updateStatus(ctx), STATUS_POLL_MS)
    // never keep the process alive just to poll the status line
    ;(statusTimer as { unref?: () => void }).unref?.()
  }

  pi.on("session_start", async (_event, ctx) => {
    try {
      await relay({
        hook_event_name: "SessionStart",
        session_id: sessionID(ctx),
        cwd: ctx.cwd,
      })
    } catch {}
    startStatus(ctx)
  })

  pi.on("before_agent_start", async (event, ctx) => {
    try {
      const resp = await relay({
        hook_event_name: "UserPromptSubmit",
        session_id: sessionID(ctx),
        cwd: ctx.cwd,
        prompt: event.prompt,
      })
      // Parked suggestion (slow-LLM fallback): visible AND model-facing.
      const text = deliverable(resp)
      if (text) {
        return { message: { customType: "tacit-suggestion", content: text, display: true } }
      }
      // (No tier-2 directive: the hidden model-facing envelope was dropped
      // repo-wide — Claude Code surfaced it as a sticky context chip.)
    } catch {}
  })

  pi.on("tool_call", async (event, ctx) => {
    try {
      await relay({
        hook_event_name: "PreToolUse",
        session_id: sessionID(ctx),
        tool_name: event.toolName,
        tool_input: event.input,
      })
    } catch {}
    // Observe-only: no {block} returned, the call proceeds untouched.
  })

  pi.on("tool_result", async (event, ctx) => {
    try {
      let input: unknown = event.input
      const answers = answersByCall.get(event.toolCallId)
      if (answers) {
        answersByCall.delete(event.toolCallId)
        input = { ...(event.input as object), answers }
      }
      await relay({
        hook_event_name: "PostToolUse",
        session_id: sessionID(ctx),
        tool_name: event.toolName,
        tool_input: input,
        tool_output: contentText(event.content),
      })
    } catch {}
  })

  pi.on("agent_end", async (_event, ctx) => {
    try {
      // The Stop response is the same-turn delivery vehicle: pi can render
      // it directly — a displayed custom message lands right under the
      // answer it is about, persists in the transcript, and is in the
      // model's context from the next turn on.
      const resp = await relay({
        hook_event_name: "Stop",
        session_id: sessionID(ctx),
      })
      const text = deliverable(resp)
      if (!text) return
      // Deliver DETACHED (the handler must return so the loop can go idle —
      // see whenIdle), then inject WITHOUT triggerTurn: the block renders
      // under the answer and joins the context, but no LLM call is spent on
      // it. If idle never arrives, park it for the next user prompt instead
      // ("nextTurn" never interrupts or triggers).
      setTimeout(() => {
        void whenIdle(ctx, 3_000).then((idle) => {
          try {
            pi.sendMessage(
              { customType: "tacit-suggestion", content: text, display: true },
              idle ? undefined : { deliverAs: "nextTurn" },
            )
          } catch {}
        })
      }, 0)
    } catch {}
    // Refresh the footer promptly if this turn changed the counters.
    void updateStatus(ctx)
  })

  pi.on("session_shutdown", async (_event, ctx) => {
    if (statusTimer) {
      clearInterval(statusTimer)
      statusTimer = undefined
    }
    try {
      ;(ctx as StatusCtx).ui?.setStatus?.("tacit", "")
    } catch {}
    try {
      // Best-effort hygiene: lets the agent drop the session state early
      // instead of waiting for idle-exit to reap it.
      await relay({
        hook_event_name: "SessionEnd",
        session_id: sessionID(ctx),
      })
    } catch {}
  })

  // The pull-based ask path — Claude Code and Codex get this from the
  // `tacit mcp` server; pi has no MCP client, so the extension IS the client.
  pi.registerTool({
    name: "tacit_search",
    label: "Tacit search",
    description:
      "Search the organization's Tacit playbook for techniques relevant to a task. " +
      "Use it when internal tools, connectors, or conventions may apply. Report returned " +
      "evidence with measured values exactly as returned.",
    parameters: Type.Object({
      query: Type.String({ description: "what the member is trying to do, in plain words" }),
    }),
    async execute(_toolCallId, params) {
      const { text, isError } = await mcpSearch(params.query)
      return { content: [{ type: "text", text }], isError, details: {} }
    },
  })

  pi.registerTool({
    name: "tacit_insights",
    label: "Tacit insights",
    description:
      "Show the organization's Tacit technique-usage stats: the delivery funnel " +
      "(shown -> adopted -> helped), helped and fit-check-decline rates, and the " +
      "technique summaries. Use when the member asks how Tacit is doing or " +
      "wants adoption/helped-rate numbers. pi shows the text summary; the graphical " +
      "panel renders in MCP-app harnesses.",
    parameters: Type.Object({
      window: Type.Optional(
        Type.String({ description: "time window: 7d, 30d (default), 90d, or all" }),
      ),
    }),
    async execute(_toolCallId, params) {
      const { text, isError } = await mcpInsights(params.window ?? "")
      return { content: [{ type: "text", text }], isError, details: {} }
    },
  })

  // The native question form for a deliberately-raised Tacit picker (e.g.
  // /tacit:feedback). Input shape is Claude Code's AskUserQuestion so the hook
  // agent's funnel capture (offered at PreToolUse, answered at PostToolUse)
  // works unchanged. (No longer auto-raised — the tier-2 directive was dropped.)
  pi.registerTool({
    name: "tacit_org",
    label: "Tacit organization",
    description:
      "Show how the organization uses its Tacit playbook: adoption breadth by cohort, " +
      "which areas of practice are spreading, opportunities (a cohort saw a move that helped peers), " +
      "and proponent cohorts as knowledge-transfer routes. Cohort-level aggregates only — never " +
      "individuals. For pipeline metrics (funnel, helped rate), use tacit_insights instead.",
    parameters: Type.Object({
      window: Type.Optional(
        Type.String({ description: "time window: 7d, 30d (default), 90d, or all" }),
      ),
    }),
    async execute(_toolCallId, params) {
      const { text, isError } = await mcpOrg(params.window ?? "")
      return { content: [{ type: "text", text }], isError, details: {} }
    },
  })

  pi.registerTool({
    name: "AskUserQuestion",
    label: "Ask user question",
    description:
      "Ask the user one or more multiple-choice questions with a native picker and get " +
      "their selections back. Use when you need the user to decide between concrete options " +
      "before proceeding. The user can also answer with free text.",
    parameters: askUserQuestionParams,
    async execute(toolCallId, params, _signal, _onUpdate, ctx) {
      if (!ctx.hasUI) {
        return {
          content: [
            {
              type: "text",
              text: "Interactive UI is unavailable. Ask the question in plain text.",
            },
          ],
          isError: true,
          details: {},
        }
      }
      const answers: Record<string, string> = {}
      const lines: string[] = []
      for (const q of params.questions) {
        const answer = await askOne(ctx, q)
        if (answer === undefined) {
          lines.push(`"${q.question}": dismissed by user`)
          continue
        }
        answers[q.question] = answer
        lines.push(`"${q.question}": ${answer}`)
      }
      answersByCall.set(toolCallId, answers)
      return {
        content: [{ type: "text", text: lines.join("\n") || "No questions asked." }],
        details: { answers },
      }
    },
  })
}
