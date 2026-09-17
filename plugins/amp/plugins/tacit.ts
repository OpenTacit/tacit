// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Tacit for Amp: observe each turn, pass it to the local hook agent, and show
// a suggestion without blocking or changing the member's work.

import {
  mkdirSync,
  readFileSync,
  readdirSync,
  renameSync,
  statSync,
  unlinkSync,
  writeFileSync,
} from "node:fs"
import { join } from "node:path"
import type {
  AgentEndEvent,
  CommandSubscription,
  PluginAPI,
  ThreadID,
  ThreadMessage,
  ThreadState,
} from "@ampcode/plugin"

export const description =
  "Connects Amp to your organization's Tacit playbook: observes turn events, delivers matched techniques, adds a native form for member-requested Tacit flows, and adds Search, Contribute, and Review commands."

const TACIT_BIN = process.env["TACIT_BIN"] ?? "{{TACIT_BIN}}"
const AGENT_URL = process.env["TACIT_HOOKS_URL"] ?? "http://127.0.0.1:8787"
const HOOKS_KEY = process.env["TACIT_HOOKS_KEY"] ?? ""
const DATA_HOME =
  process.env["XDG_DATA_HOME"] ??
  join(process.env["HOME"] ?? "/tmp", ".local", "share")
const PENDING_DIR = join(DATA_HOME, "tacit", "amp-pending")
const FETCH_TIMEOUT_MS = 5_000
const WARM_POST_TIMEOUT_MS = 500
const SPAWN_TIMEOUT_MS = 15_000
const PENDING_TTL_MS = 7 * 24 * 60 * 60 * 1000

type HookResponse = {
  systemMessage?: string
  hookSpecificOutput?: { additionalContext?: string }
}

type FormOption = { label: string; description?: string }
type FormQuestion = {
  question: string
  header: string
  options: FormOption[]
  multiSelect?: boolean
}

function safeThreadName(threadID: string): string {
  return encodeURIComponent(threadID).replaceAll("%", "_")
}

function pendingPath(threadID: string): string {
  return join(PENDING_DIR, safeThreadName(threadID) + ".json")
}

function cleanPending(): void {
  try {
    const cutoff = Date.now() - PENDING_TTL_MS
    for (const name of readdirSync(PENDING_DIR)) {
      const path = join(PENDING_DIR, name)
      if (statSync(path).mtimeMs < cutoff) unlinkSync(path)
    }
  } catch {}
}

function putPending(threadID: string, text: string): void {
  try {
    mkdirSync(PENDING_DIR, { recursive: true })
    cleanPending()
    const path = pendingPath(threadID)
    const temp = `${path}.${process.pid}.${Math.random().toString(36).slice(2)}.tmp`
    writeFileSync(temp, JSON.stringify({ text, ts: Date.now() }), { mode: 0o600 })
    renameSync(temp, path)
  } catch {}
}

function takePending(threadID: string): string {
  const path = pendingPath(threadID)
  const claim = `${path}.${process.pid}.${Math.random().toString(36).slice(2)}.claim`
  try {
    renameSync(path, claim)
  } catch {
    return ""
  }
  try {
    const entry = JSON.parse(readFileSync(claim, "utf8")) as {
      text?: unknown
      ts?: unknown
    }
    if (
      typeof entry.text === "string" &&
      typeof entry.ts === "number" &&
      entry.ts >= Date.now() - PENDING_TTL_MS
    ) {
      return entry.text
    }
  } catch {
  } finally {
    try {
      unlinkSync(claim)
    } catch {}
  }
  return ""
}

async function post(
  payload: Record<string, unknown>,
  timeoutMs = FETCH_TIMEOUT_MS,
): Promise<HookResponse | null> {
  try {
    const headers: Record<string, string> = { "Content-Type": "application/json" }
    if (HOOKS_KEY) headers["X-Tacit-Key"] = HOOKS_KEY
    const response = await fetch(AGENT_URL + "/v1/hooks/amp", {
      method: "POST",
      headers,
      body: JSON.stringify(payload),
      signal: AbortSignal.timeout(timeoutMs),
    })
    if (response.ok) return (await response.json()) as HookResponse
  } catch {}
  return null
}

async function relay(payload: Record<string, unknown>): Promise<HookResponse> {
  const direct = await post(payload)
  if (direct !== null) return direct
  try {
    const proc = Bun.spawn([TACIT_BIN, "hook-relay", "amp"], {
      stdin: "pipe",
      stdout: "pipe",
      stderr: "ignore",
    })
    proc.stdin.write(JSON.stringify(payload))
    proc.stdin.end()
    const out = await Promise.race([
      new Response(proc.stdout).text(),
      new Promise<string>((resolve) =>
        setTimeout(() => {
          proc.kill()
          resolve("")
        }, SPAWN_TIMEOUT_MS),
      ),
    ])
    return out ? (JSON.parse(out) as HookResponse) : {}
  } catch {
    return {}
  }
}

function deliverable(response: HookResponse): string {
  if (!response.systemMessage) return ""
  return response.hookSpecificOutput?.additionalContext || response.systemMessage
}

function trailingAssistantText(event: AgentEndEvent): string {
  for (let i = event.messages.length - 1; i >= 0; i--) {
    const message: ThreadMessage = event.messages[i]
    if (message.role !== "assistant") continue
    return message.content
      .filter((block) => block.type === "text")
      .map((block) => block.text.trim())
      .join("\n")
      .trim()
  }
  return ""
}

function formFallback(): string {
  return "Tacit's native form is unavailable in this thread. Ask the questions in one compact numbered chat message and let the member reply in chat."
}

function optionText(option: FormOption): string {
  return option.description
    ? `${option.label} — ${option.description}`
    : option.label
}

export default function tacit(amp: PluginAPI) {
  const cwd = amp.system.workspaceRoot
    ? amp.helpers.filePathFromURI(amp.system.workspaceRoot)
    : undefined
  const queues = new Map<string, Promise<void>>()
  const warmThreads = new Set<string>()

  const payload = (fields: Record<string, unknown>) =>
    cwd ? { ...fields, cwd } : fields

  const enqueue = (
    threadID: string,
    work: () => Promise<void>,
  ): Promise<void> => {
    const prior = queues.get(threadID) ?? Promise.resolve()
    const next = prior.catch(() => {}).then(work).catch(() => {})
    queues.set(threadID, next)
    void next.finally(() => {
      if (queues.get(threadID) === next) queues.delete(threadID)
    })
    return next
  }

  amp.on("session.start", (event) => {
    void enqueue(event.thread.id, async () => {
      await relay(payload({ hook_event_name: "SessionStart", session_id: event.thread.id }))
      warmThreads.add(event.thread.id)
    })
  })

  amp.on("agent.start", (event) => {
    void enqueue(event.thread.id, async () => {
      const response = await relay(payload({
        hook_event_name: "UserPromptSubmit",
        session_id: event.thread.id,
        prompt: event.message,
      }))
      warmThreads.add(event.thread.id)
      const text = deliverable(response)
      if (text) putPending(event.thread.id, text)
    })
    const pending = takePending(event.thread.id)
    if (pending) return { message: { content: pending, display: true } }
  })

  amp.on("tool.call", (event) => {
    void enqueue(event.thread.id, async () => {
      // Keep a cold process spawn off the tool request path. A session or turn
      // boundary starts the agent; tool events use the warm path only.
      if (!warmThreads.has(event.thread.id)) return
      const response = await post(payload({
        hook_event_name: "PreToolUse",
        session_id: event.thread.id,
        tool_name: event.tool,
        tool_input: event.input,
      }), WARM_POST_TIMEOUT_MS)
      if (response === null) warmThreads.delete(event.thread.id)
    })
    return { action: "allow" }
  })

  amp.on("tool.result", (event) => {
    void enqueue(event.thread.id, async () => {
      if (!warmThreads.has(event.thread.id)) return
      const response = await post(payload({
        hook_event_name: "PostToolUse",
        session_id: event.thread.id,
        tool_name: event.tool,
        tool_input: event.input,
        tool_output: event.output ?? event.error ?? "",
      }), WARM_POST_TIMEOUT_MS)
      if (response === null) warmThreads.delete(event.thread.id)
    })
  })

  amp.on("agent.end", async (event, ctx) => {
    let response: HookResponse = {}
    await enqueue(event.thread.id, async () => {
      response = await relay(payload({
        hook_event_name: "Stop",
        session_id: event.thread.id,
        assistant_text: trailingAssistantText(event),
      }))
      warmThreads.add(event.thread.id)
    })
    const text = deliverable(response)
    if (!text) return
    if (amp.activeThread.current?.id !== event.thread.id) {
      putPending(event.thread.id, text)
      return
    }
    try {
      await ctx.ui.confirm({
        title: "Tacit recommendation",
        message: text,
        confirmButtonText: "Got it",
      })
    } catch {
      // Execute mode and clients without plugin UI cannot open the dialog.
      // Preserve the suggestion for synchronous delivery with the next prompt.
      putPending(event.thread.id, text)
    }
  })

  amp.registerTool({
    name: "tacit_form",
    description:
      "Show a native question form only during member-requested Tacit setup, contribution, draft review, or delivery self-test flows. Do not use it for other work or to start a Tacit flow.",
    inputSchema: {
      type: "object",
      properties: {
        questions: {
          type: "array",
          items: {
            type: "object",
            properties: {
              question: { type: "string" },
              header: { type: "string" },
              options: {
                type: "array",
                items: {
                  type: "object",
                  properties: {
                    label: { type: "string" },
                    description: { type: "string" },
                  },
                  required: ["label"],
                },
              },
              multiSelect: { type: "boolean" },
            },
            required: ["question", "header", "options"],
          },
        },
      },
      required: ["questions"],
    },
    async execute(input, ctx) {
      if (amp.activeThread.current?.id !== ctx.thread.id) return formFallback()
      const questions = Array.isArray(input.questions)
        ? (input.questions as FormQuestion[])
        : []
      const answers: Record<string, string | string[]> = {}
      try {
        for (const question of questions) {
          const choices = question.options.map(optionText)
          if (!question.multiSelect) {
            const answer = await ctx.ui.select({
              title: question.header,
              message: question.question,
              options: choices,
              allowOther: true,
            })
            if (answer !== undefined) {
              answers[question.question] =
                question.options.find((option) => optionText(option) === answer)?.label ?? answer
            }
            continue
          }
          const selected: string[] = []
          let cancelled = false
          while (true) {
            const available = question.options
              .filter((option) => !selected.includes(option.label))
              .map(optionText)
            const answer = await ctx.ui.select({
              title: question.header,
              message: question.question,
              options: ["(done selecting)", ...available],
              allowOther: true,
            })
            if (answer === undefined) {
              cancelled = true
              break
            }
            if (answer === "(done selecting)") break
            const label =
              question.options.find((option) => optionText(option) === answer)?.label ?? answer
            if (!selected.includes(label)) selected.push(label)
          }
          if (!cancelled) answers[question.question] = selected
        }
      } catch (error) {
        if (
          error instanceof Error &&
          amp.helpers.isPluginUINotAvailableError(error)
        ) return formFallback()
        return formFallback()
      }
      return JSON.stringify({ answers })
    },
  })

  const commands: CommandSubscription[] = []
  const canRun = (state?: ThreadState) =>
    amp.activeThread.current && state === "idle"
      ? { type: "enabled" as const }
      : { type: "disabled" as const, reason: "Open an idle thread first." }

  async function activeIdle(ctxThreadID?: string): Promise<boolean> {
    const active = amp.activeThread.current
    if (!active || (ctxThreadID && active.id !== ctxThreadID)) return false
    return (await amp.threads.get(active.id).state.get()) === "idle"
  }

  commands.push(amp.registerCommand("tacit-search-playbook", {
    title: "Search Playbook",
    category: "Tacit",
    description: "Ask Tacit to search the playbook for a task.",
    availability: canRun(),
  }, async (ctx) => {
    if (!ctx.thread || !(await activeIdle(ctx.thread.id))) return
    const query = await ctx.ui.input({ title: "Search Playbook", helpText: "What are you trying to do?" })
    if (!query?.trim() || !(await activeIdle(ctx.thread.id))) return
    await ctx.thread.append([{ type: "user-message", content: `Search Tacit for: ${query.trim()}` }])
  }))
  commands.push(amp.registerCommand("tacit-contribute-current-work", {
    title: "Contribute Current Work",
    category: "Tacit",
    description: "Ask Tacit to draft a technique from this thread.",
    availability: canRun(),
  }, async (ctx) => {
    if (!ctx.thread || !(await activeIdle(ctx.thread.id))) return
    const confirmed = await ctx.ui.confirm({ title: "Contribute Current Work", message: "Ask Tacit to draft a technique from this thread?", confirmButtonText: "Continue" })
    if (!confirmed || !(await activeIdle(ctx.thread.id))) return
    await ctx.thread.append([{ type: "user-message", content: "Contribute the reusable approach from our current work to Tacit." }])
  }))
  commands.push(amp.registerCommand("tacit-review-drafts", {
    title: "Review Drafts",
    category: "Tacit",
    description: "Ask Tacit to show drafts awaiting review.",
    availability: canRun(),
  }, async (ctx) => {
    if (!ctx.thread || !(await activeIdle(ctx.thread.id))) return
    await ctx.thread.append([{ type: "user-message", content: "Show me the Tacit drafts awaiting review." }])
  }))

  let stateSubscription: { unsubscribe(): void } | undefined
  let switchVersion = 0
  const setAvailability = (state?: ThreadState) => {
    const availability = canRun(state)
    for (const command of commands) command.setAvailability(availability)
  }
  const watchActive = (active: { id: ThreadID } | null) => {
    const version = ++switchVersion
    stateSubscription?.unsubscribe()
    stateSubscription = undefined
    setAvailability()
    if (!active) return
    const thread = amp.threads.get(active.id)
    void thread.state.get().then((state) => {
      if (
        version !== switchVersion ||
        amp.activeThread.current?.id !== active.id
      ) return
      setAvailability(state)
      const subscription = thread.state.subscribe((next) => {
        if (amp.activeThread.current?.id === active.id) setAvailability(next)
      })
      if (
        version !== switchVersion ||
        amp.activeThread.current?.id !== active.id
      ) {
        subscription.unsubscribe()
        return
      }
      stateSubscription = subscription
    }).catch(() => {})
  }
  const activeSubscription = amp.activeThread.subscribe(watchActive)
  watchActive(amp.activeThread.current)

  amp.onDispose(() => {
    activeSubscription.unsubscribe()
    stateSubscription?.unsubscribe()
  })
}
