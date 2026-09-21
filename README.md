<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/user-guide/images/hero-dark.svg">
    <img src="docs/user-guide/images/hero.svg" alt="OpenTacit" width="420">
  </picture>
</p>

An organization's **measured playbook for working with AI**. OpenTacit records
how people use AI coding agents, finds relevant validated techniques, and suggests
them during work. The suggestions use results measured from colleagues' use.

## Start with one person: you

You do not need colleagues, a server, or a model key to get something out of this.
Two commands, inside any repository you work in:

```bash
curl -fsSL https://opentacit.com/install.sh | sh
tacit init && tacit connect
```

Linux and macOS, amd64 and arm64. On Windows, run it under WSL2: the retrieval
model has no Windows build, so a native binary would fall back to lexical
matching without saying so, and a playbook that quietly matches worse is not
worth shipping.

Every release carries a build provenance attestation, so you can check where a
binary came from before you trust it —
[`SECURITY.md`](SECURITY.md#verifying-a-release) has the one command.

`init` reads the conventions already in this repository (`CLAUDE.md`,
`AGENTS.md`, `.cursorrules`, the copilot instructions, the skills and commands under
`.claude/`) and files each rule as a draft technique. `connect` links your coding
tools to it. From your next session, OpenTacit suggests those rules when they apply in
any connected agent.

For one person, the full process runs on the local machine:

```bash
tacit ask "<whatever you are working on>"   # the playbook's answer, with its evidence
tacit usage                                  # what you were shown, what you took, what helped
```

When a second person joins, results from their use inform the ranking. Run
`tacit invite` to add them. OpenTacit keeps the techniques from the personal registry.
The registry stores aggregate data and does not store per-person data.

## Dependencies

A default `go build` reaches the **Go standard library plus two dependencies**:
`modernc.org/sqlite` (pure-Go SQLite, for the optional SQLite backend) and
`golang.org/x/term`. The embedded file store, which is the default, needs neither.

Two build tags add the rest. `-tags pg` links `pgx` for the Postgres backend;
`-tags onnx` links `onnxruntime_go` for semantic retrieval. Released binaries carry
both. `make licenses` checks every module in both configurations against an
allowlist — see [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

## The pipeline

```
capture ─▶ characterize ─▶ retrieve (registry) ─▶ synthesize (LLM) ─▶ deliver in-flow ─▶ feedback write-back
```

1. **Capture:** turn an AI interaction (an Omnigent session, a Claude Code / Codex / Amp
   / pi / omp / opencode hook stream, a pasted transcript) into one source-agnostic canonical record.
2. **Characterize:** extract features (task type, tools used, modalities, segment). Use no
   LLM for structured records; an LLM for raw transcripts.
3. **Retrieve:** the registry returns relevant **techniques** from the org's playbook,
   ranked by *measured outcomes among similar colleagues* and semantic similarity.
4. **Synthesize:** a frontier LLM turns that evidence into a short, fit-checked audit.
5. **Deliver and record feedback:** the suggestion appears during the session; `shown` / `adopted` /
   `helped` events post back to the registry and improve future ranking.

## Install

The two commands at the top are the whole of it for one person. What they leave
out:

**`tacit init` is idempotent** and explains each thing it set up. Drafts it reads
from a repository are **org-scoped** and serve nobody until a reviewer promotes
them — which, on a registry of one, is you.

**It gives the registry an address on the internet** through the shared proxy, so
you can open the dashboard from a laptop or phone. `tacit init --global-access off`
keeps access on the host machine. Contributing techniques
to the public pool is a separate, explicit yes in Settings, asked for only when a
technique is eligible.

**Joining a registry someone else runs** skips all of the above:

```bash
tacit join <invite-link>                              # setup + connect in one step
tacit connect --registry <url> --key <key>            # or the two halves separately
```

Leaving is symmetric. `tacit disconnect` unwires the harnesses and retires the
member settings, touching only what connect installed.

**Building from source** needs Go. `make build` gives you the development binary;
production builds carry `-tags onnx,pg` and `make install` passes them for you
(the onnx tag swaps lexical retrieval for a local semantic embedder).

## One binary

```bash
go build -o tacit ./cmd/tacit         # or: go install ./cmd/tacit

tacit init [--from-repo DIR]         # bootstrap a registry, and read a repo's conventions into it
tacit serve                          # the registry service (:8080)
tacit audit <source> [--offline]     # audit a shared conversation
tacit audit <session.json>        # audit a captured Omnigent session
tacit audit --live <session-id>     # audit a LIVE session turn-by-turn
tacit feedback <id> --stage helped   # close the loop explicitly
tacit serve-hooks                    # the local in-harness hook agent (:8787)
tacit hook-relay <harness>           # per-hook relay (what the plugins invoke)
tacit migrate-store --db <url>       # file store -> Postgres/SQLite cutover (idempotent)
```

## Layout

```
cmd/tacit/              the CLI binary (all subcommands)
pkg/                    the PUBLIC packages (the open standard; schemas/ is normative):
  contracts             wire types, defined once (Technique, events, Sketch, ...)
  feed                  the tacit-feed/v1 federation protocol + signing
  client                typed registry HTTP client
  embed, scrub          shared vector space · boundary secret/PII scanner
  jsonschema            stdlib JSON Schema validator (spec enforcement)
  intelligence          the privacy-safe report a registry sends back upstream
  webpaths              which paths may be cached, which never, which cookies
                        make a response one reader's — read by the registry at
                        request time and by whatever configures a CDN in front
  installaddr           the two installer addresses, so a redirect configured
                        elsewhere and the docs cannot quote different ones
  ingress               the tunnel and proxy that give a self-hosted registry a
                        public name without a domain or an open port — both
                        halves, since the registry dials it from inside `serve`
  ui                    the shell, plates and charts a page is built from,
                        exported for a console that renders this product from
                        outside this module (internal/ui stays the source)
schemas/                normative JSON Schemas + OpenAPI 3.1 (embedded, self-served)
internal/registry/      the self-contained registry service
  config, models        tunables · data contracts + validation
  storage               the data-layer interface + backend conformance suite
  store                 embedded stdlib backend: techniques.json + events.jsonl,
                        derived rollups in memory (the zero-setup default)
  pgstore               Postgres backend (pgx) for shared / multi-instance deployments
  sqlitestore           SQLite backend (modernc.org/sqlite, pure Go) for single-machine scale
  migrate               file store -> database cutover (idempotent copy + verify)
  embed                 hashing embedder + cosine (deterministic across platforms)
  retrieval             relevance gate · shrinkage ranking · cohort
  feedback              rollup fanout per segment + decay detection
  techniques            markdown+YAML-subset frontmatter loader
  contribute, jobs      draft lifecycle · in-process scheduler
  federation            technique-feed publish/subscribe (tacit-feed/v1)
  markdown, oidc, web   docs renderer · sign-in · HTTP API + dashboard
internal/auditor/       the audit layer (the one part that calls an LLM)
  contracts, capture    wire types · canonical record, characterizer,
                        claude-code/codex/amp/pi/omp/opencode accumulators, omnigent batch + SSE live
  llm, client, audit    Anthropic/heuristic/auto clients · registry client · orchestrator
  hooks                 the in-harness hook agent + command relay
  ingest                transcript loading incl. share-link extraction
techniques/ schemas/ prompts/ docs/   shared assets (starter techniques, contracts, prompts, docs)
plugins/                Claude Code + Codex + Amp + pi/omp + opencode integration packages
testdata/               fixtures (Omnigent session)
```

**What `pkg/` promises.** The wire contracts are the stable thing: the JSON
Schemas under `schemas/`, the MCP tool names, and `tacit-feed/v1` are versioned,
and other implementations may depend on them. The **Go API is `v0` and may break
in a minor release** — it is published because a normative schema deserves a
reference implementation, not because the signatures are settled. Pin a version
if you import it.


## Storage backends

All three backends implement `internal/registry/storage.Store` and pass the same
conformance suite (`storage/conformance`); which one runs is a config choice:

- **Embedded file store** (default, no `TACIT_DB_URL`): techniques.json + append-only
  events.jsonl, rollups derived in memory. Zero infrastructure — the pilot on-ramp,
  and every file is human-readable.
- **SQLite** (`TACIT_DB_URL=sqlite:///var/lib/tacit/tacit.db`): the single-machine
  growth path — same binary, one database file, WAL journaling, instant startup at
  event volumes that would strain the file store's RAM and startup scan. Pure-Go
  driver (`modernc.org/sqlite`), so cross-compilation and static builds are
  unaffected. Single instance by design. The official container image defaults to
  this backend (`/data/tacit.db`), auto-importing any pre-existing file-store
  volume on first boot.
- **Postgres** (`TACIT_DB_URL=postgres://...`): the shared path — multiple registry
  instances against one database, with globally idempotent event ingest and an
  advisory lock electing a single rollup runner per cycle.

| | file store | SQLite | Postgres |
|---|---|---|---|
| techniques | ≤ ~5k | ~50k | ~50k+ |
| feedback events | ~1M comfortable, ~5M ceiling (RAM + startup scan) | tens of millions | tens of millions |
| instances | exactly one | exactly one | N |
| infrastructure | none | none (one file) | a database to run |

**Cutover runbook** (idempotent — safe to re-run after a partial failure; the
destination may be a `postgres://` or `sqlite:` URL):

```bash
tacit migrate-store --data ./data --db "$TACIT_DB_URL" --dry-run   # what would move
tacit migrate-store --data ./data --db "$TACIT_DB_URL"             # copy + verify counts
TACIT_DB_URL=... tacit serve                                       # now on the database
# curl /v1/health and compare technique/event counts, then archive ./data
```

SQLite-backed tests run ungated (a temp file needs no server), so the SQL-shaped side
of the storage contract is exercised on every `go test ./...`.

Postgres-backed tests are env-gated so `go test ./...` stays green with no database:

```bash
docker run -d --rm -e POSTGRES_PASSWORD=pg -p 127.0.0.1:15432:5432 postgres:17-alpine
TACIT_TEST_DB_URL="postgres://postgres:pg@127.0.0.1:15432/postgres?sslmode=disable" go test ./...
```

## Configuration

Environment variables use the `TACIT_*` prefix. A registry reads them at
startup and falls back to its config file; a member's harness reads them from
`agent.env`.

## Testing

```bash
go test ./...        # full suite
go test -race ./...  # with the race detector
```

The suite covers the registry loop (seed techniques → evidence → feedback →
recompute → ranking shifts), frontmatter parsing against the real techniques, the hook
agent's full behavior (same-turn delivery, parked slow-synthesis fallback, ambient
reactions, adoption inference, the adopted-once regression), relay connect-or-spawn,
OIDC token handling, SSE live capture, and the HTTP surfaces end to end.

## Documentation

**[The user guide](docs/user-guide/index.md)** — setup, daily use, the dashboard,
administration. It ships with the binary and the registry serves it at `/docs`.

The schemas are the other half of the documentation, and they are normative:
`schemas/` holds the JSON Schemas and the OpenAPI description, embedded in the
binary and served by a running registry. `pkg/contracts` is the reference
implementation of those types.

Design records for the storage conformance suite, retrieval cosine floor, and
harness wiring are kept outside this repository. Source comments cite them by
path. You do not need them to use or read the code.

## License

Apache-2.0 — see [`LICENSE`](LICENSE), with attributions in [`NOTICE`](NOTICE)
and [`THIRD-PARTY-NOTICES.md`](THIRD-PARTY-NOTICES.md). There is no CLA:
contributors keep their copyright, and the project holds no right to relicense
their work ([`CONTRIBUTING.md`](CONTRIBUTING.md)).

The contracts under [`schemas/`](schemas/) are an **open specification**. You may
implement them in any language and state that your software implements them. The
registry is one implementation of this standard.

The name is held back from the grant, as Apache-2 section 6 provides.
[`TRADEMARK.md`](TRADEMARK.md) lists permitted uses. Contributions use the DCO,
and there is no CLA. See [`CONTRIBUTING.md`](CONTRIBUTING.md).
