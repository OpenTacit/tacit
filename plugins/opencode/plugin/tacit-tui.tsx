/** @jsxImportSource @opentui/solid */
// Tacit sidebar for opencode — the persistent half of delivery.
//
// The server plugin (tacit.ts) delivers same-turn suggestions as a toast,
// which vanishes in seconds. This TUI plugin renders the session's REVIEW
// LIST — every suggestion shown this session with its evolving verdict — in
// the right-hand sidebar, beside TODOs and changed files, via the
// sidebar_content slot. Data comes from the local hook agent's
// /v1/hooks/suggestions endpoint (member-local, loopback-only).
//
//   · shown       ✓ adopted       ⚡ helped       × dismissed
//
// TUI plugins are a separate module shape from server plugins ({ tui }, not
// { server }) — hence a second file. Refresh: on session.idle (when a new
// suggestion can appear) plus a slow poll (verdicts land asynchronously —
// adoption verification runs off the turn path).
//
// Install: symlink next to tacit.ts in ~/.config/opencode/plugin/.
import { createSignal, onCleanup } from "solid-js"
import type { TuiPlugin, TuiPluginModule } from "@opencode-ai/plugin/tui"

type Suggestion = {
  technique_id: string
  name: string
  evidence?: string
  status: "shown" | "adopted" | "helped" | "dismissed"
}

const GLYPH: Record<Suggestion["status"], string> = {
  shown: "·",
  adopted: "✓",
  helped: "⚡",
  dismissed: "×",
}

const POLL_MS = 10_000
const FETCH_TIMEOUT_MS = 2_000

// Mirror the server plugin's config resolution (env > agent.env > default)
// for the hook agent's address. require() rather than top-level node imports:
// the Bun plugin runtime does not support the latter.
function agentURL(): string {
  const env = (k: string): string | undefined => process.env[k]
  let fileEnv: Record<string, string> = {}
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const fs = require("fs") as typeof import("fs")
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const os = require("os") as typeof import("os")
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const path = require("path") as typeof import("path")
    for (const raw of fs
      .readFileSync(path.join(os.homedir(), ".config", "tacit", "agent.env"), "utf8")
      .split("\n")) {
      const line = raw.trim()
      if (!line || line.startsWith("#")) continue
      const eq = line.indexOf("=")
      if (eq > 0) fileEnv[line.slice(0, eq).trim()] = line.slice(eq + 1).trim()
    }
  } catch {}
  const get = (keys: string[], fallback: string) => {
    for (const k of keys) if (env(k)) return env(k)!
    for (const k of keys) if (fileEnv[k]) return fileEnv[k]
    return fallback
  }
  return get(
    ["TACIT_HOOKS_URL"],
    `http://${get(["TACIT_HOOKS_HOST"], "127.0.0.1")}:${get(
      ["TACIT_HOOKS_PORT"],
      "8787",
    )}`,
  )
}

const tui: TuiPlugin = async (api) => {
  const base = agentURL()

  api.slots.register({
    order: 10,
    slots: {
      sidebar_content(_ctx, props: { session_id: string }) {
        const theme = () => api.theme.current
        const [items, setItems] = createSignal<Suggestion[]>([])

        async function refresh() {
          try {
            const resp = await fetch(
              base + "/v1/hooks/suggestions?session_id=" + encodeURIComponent(props.session_id),
              { signal: AbortSignal.timeout(FETCH_TIMEOUT_MS) },
            )
            if (!resp.ok) return
            const body = (await resp.json()) as { suggestions?: Suggestion[] }
            setItems(body.suggestions ?? [])
          } catch {} // agent idle or absent: keep the last known list
        }

        void refresh()
        const offIdle = api.event.on("session.idle", (e) => {
          if ((e as { properties?: { sessionID?: string } }).properties?.sessionID === props.session_id) {
            void refresh()
          }
        })
        const timer = setInterval(() => void refresh(), POLL_MS)
        onCleanup(() => {
          offIdle()
          clearInterval(timer)
        })

        return (
          <box flexDirection="column" visible={items().length > 0}>
            <text fg={theme().text}>
              <b>◆ Tacit</b>
            </text>
            {items().map((sg) => (
              <box flexDirection="column">
                <text fg={theme().text}>
                  {GLYPH[sg.status] ?? "·"} {sg.name}
                </text>
                <text fg={theme().textMuted}>
                  {"  "}
                  {sg.evidence ? sg.evidence : "awaiting measured outcomes"} · {sg.status}
                </text>
              </box>
            ))}
          </box>
        )
      },
    },
  })
}

const plugin: TuiPluginModule & { id: string } = { id: "tacit-sidebar", tui }

export default plugin
