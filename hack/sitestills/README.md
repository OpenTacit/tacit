# Recapturing the project page's session frames

The frames in `internal/ui/siteframes.go` are the moments of one interaction
with Claude Code, told three times against three demonstration orgs, with the
window's title bar picking between them. They are text, so fixing a caption or
a column is an edit to a string.

Do not change the commands in a frame. Names such as `tacit init` and
`/tacit:search` are stable binary and command names; a product rename does not
change them. `internal/product` defines this rule, and `rename_test.go` checks
it. Keep lowercase `tacit` even when the wordmark uses another name.

The second is any evidence line:

```
measured by colleagues: helped 94% · adopted 54% · n=41 · team:pretraining
```

Read these numbers from a registry; do not write them by hand. Repeat §1 when a
technique, cohort, or dataset changes. The member prompts, agent prose, and
output from demo-only tools are staged. The page caption identifies them as
staged.

§2–§3 are the full session capture. Redo those when the harness's own chrome
moves on and the frames start depicting a Claude Code that no longer exists.

The steps below use an isolated registry, usage log, technique memory, and
throttle budget.

## 1. A scratch registry, and the figures it reports

The three scenarios and the techniques they show. "Picker" is what the page calls
each telling and "Dataset" is the demo catalog's own key and title, which are not
always the same words: the catalog names the industry a dataset was generated for,
and the picker names the org in the frame:

| Picker | Dataset | Cohort | Technique |
| --- | --- | --- | --- |
| AI model lab | `ai-vendor` | `team:pretraining` | `forgeflow-template-launch` |
| Payments processor | `financial-institution` | `team:payments-platform` | `payrail-idempotency-check` |
| Telecommunications provider | `telecommunications-provider` | `team:network-provisioning` | `provisionguard-preflight` |

The datasets live in the demo dir, which is `TACIT_DEMO_DIR` in
`~/.config/tacit/registry.env`.

The registry MUST come up on the real embedder. An `HOME` override hides the
model and the registry falls back to lexical `hashing-v1` without failing, so
name every path explicitly and then check `embed_model` in `/v1/health` — if it
says hashing, stop.

```sh
mkdir -p /tmp/demo-org/data /tmp/demo-org/home
HOME=/tmp/demo-org/home XDG_DATA_HOME="$HOME/.local/share" \
  TACIT_EMBED_MODEL=onnx/all-MiniLM-L6-v2 \
  TACIT_ONNX_LIB="$HOME/.local/share/tacit/onnxruntime-1.30.0/libonnxruntime.so.1.30.0" \
  TACIT_ONNX_MODEL_DIR="$HOME/.local/share/tacit/models/all-MiniLM-L6-v2" \
  TACIT_API_KEY=demo-scratch-key \
  tacit serve -host 127.0.0.1 -port 8899 -data /tmp/demo-org/data &
tacit demo load --scenario software-vendor \
  --registry http://127.0.0.1:8899 --key demo-scratch-key --force
```

`--scenario` loads the pre-generated dataset embedded in the binary
(`internal/demo/datasets/`); `tacit demo scenarios` lists the keys, and
`tacit demo generate` authors a fresh one.

Then ask it what the harness would ask it — `/v1/evidence`, with a
characterization of the member's turn and the cohort they are in — and read the
answer:

```sh
curl -s -X POST http://127.0.0.1:8899/v1/evidence \
  -H "X-Tacit-Key: demo-scratch-key" -H "Content-Type: application/json" -d '{
  "summary_text": "<what the member was doing>",
  "task_type": "code-generation", "harness": "claude-code", "domain": "backend",
  "tools_used": ["Bash","Read","Edit"],
  "segment": {"team": "payments-platform", "role": "engineer"}
}' | jq '.candidates[] | {id: .technique_id, outcomes}'
```

Each candidate's `outcomes` — `helped_rate`, `adoption_rate`, `sample_size`,
`segment` — are the numbers, rendered by `contracts.EvidenceLine`: rates to
whole per cent, and the cohort dropped when it is `__overall__`. Two of the
three scenarios have no cohort on the end for exactly that reason; that is
honest and it must not be dressed up.

**What this call does not tell you is which technique belongs on the turn.**
Ranking is by measured impact within the cohort — `RelevanceK` keeps 20 of a
~30-technique corpus and `MinSimilarity` is 0 — so at this size the summary text
barely moves the order, and an unrelated prompt returns the same candidates.
Deciding which candidate FITS is the hook agent's fit-check. Pick a technique from
the candidate list whose `applies_when` genuinely covers the moment you are
staging, and write the moment to fit the technique, not the other way round.

The technique's `name` and `applies_when` in the frames are that technique's own text —
copy them, do not paraphrase.

## 2. A Claude Code session that only sees the scratch

Every one of these overrides matters; each one was found missing the hard way:

```sh
tmux new-session -d -s stills -x 96 -y 44 'env \
  TACIT_REGISTRY_URL=http://127.0.0.1:8899 \
  TACIT_API_KEY=demo-scratch-key \
  TACIT_SKETCH_URL=http://127.0.0.1:8899/v1/sketches \
  TACIT_SKETCH_TOKEN=demo-scratch-key \
  TACIT_HOOKS_PORT=8788 \
  TACIT_HOOKS_SEGMENT=team=pretraining,role=research-scientist \
  TACIT_USAGE_LOG=/tmp/demo-org/usage.jsonl \
  TACIT_TECHNIQUE_MEMORY=/tmp/demo-org/technique-memory.json \
  TACIT_HOOKS_COOLDOWN_TURNS=-1 \
  TACIT_HOOKS_COOLDOWN_MINUTES=0.01 \
  TACIT_HOOKS_MAX_PER_WINDOW=99 \
  TACIT_HOOKS_DEBUG=1 \
  sh -c "cd /tmp/demo-org/meridian && claude"'
tmux set -t stills window-size manual && tmux resize-window -t stills -x 96 -y 44
```

- The segment must be the one whose figures §1 read. A session run as a
  different team gets a different cohort's numbers, and the frames would show
  an evidence line no member in them would have seen.
- Use **96 columns**, the `termCols` value in `siteframes.go` and the width used
  to size the page text. A wider transcript may wrap on the page at points where
  it did not wrap in the terminal. tmux ignores `-x/-y`
  when the server already has clients — hence the explicit `window-size manual`
  + `resize-window`, and verify with `tmux display-message -p '#{pane_width}'`.
- Make the target technique the only candidate it can retrieve — delete the rest
  (`curl -X DELETE -H "X-Tacit-Key: …" …/v1/techniques/<id>`). The fit-check happily
  accepts semantically-nearby techniques, so a one-technique deck is what makes the
  capture deterministic. Nothing of the deck is visible in the frames.
- Put the work in the directory first (for these frames, a `runs/` with a
  plausible previous run's YAML), and a `CLAUDE.md` telling the agent to work
  from files directly rather than consult the playbook — otherwise the model
  tacit_searches on its own and burns the ambient reveal for that technique.
- `TACIT_HOOKS_PORT` gives the session its own hook daemon (spawned by the
  first relay, inheriting this environment). Kill it by port afterwards.
- `TACIT_TECHNIQUE_MEMORY` is the trap that costs the most time when missed: the
  daemon suppresses any technique the member's `~/.tacit-technique-memory.json` already
  records as adopted or dismissed — and a previous capture run records
  exactly that. (It also pollutes the member's real memory with demo ids;
  if that happened, back the file up and strip the demo dataset's ids.)
- The cooldown/window overrides disarm the ambient throttle so a wrong-technique
  delivery costs one Esc, not a 6-turn/45-minute wait.
- `TACIT_HOOKS_DEBUG=1` logs `characterized / candidates / synth for <id>`
  lines to the daemon's spawn log — the only way to see whether a Stop parked
  your technique, and for which competitor it was passed over.

The flow that produces one scenario's three moments (on Claude Code an ambient
suggestion parks at Stop and delivers at the NEXT prompt, as a form — two member
turns minimum, and a turn that already delivered a form runs no new synthesis).
The constants are `<scen>Work`, `<scen>Offer`, `<scen>Adopt` — `lab`, `bank`,
`telco`:

1. Create the condition that the technique addresses, such as a copied config,
   retry, or bulk changeset: **`<scen>Work`**.
2. The turn the technique actually triggers on: submit it, ship it, push it. The
   Tacit form arrives before the answer: **`<scen>Offer`**.
3. Pick "Apply it now": recipe fetch, then the internal tool's own commands —
   **`<scen>Adopt`**. Put a stub for that tool on PATH first so it returns
   plausible output instead of command-not-found; delete it afterwards.

Do all three moments in one session per scenario. The member's prompts have to
sound like the same person, and a scenario stitched from two sittings reads
like one.

Capture each moment as plain text:

```sh
tmux capture-pane -t stills -p -S - > stepN.txt
```

## 3. Transcribe

Take the slice that starts at the member's prompt and excludes the TUI header
and status bar — they carry the operator's own name, host and path — and paste
it into the matching constant in `internal/ui/siteframes.go`, trimming the
trailing spaces tmux pads every row with. A new scenario also needs an entry in
`siteFlowScenarios` (internal/ui/site.go): a key, the label the picker shows,
and a body and description for each of the three moments. The moment TITLES are
shared and stay put — the section is one story told three times, and a scenario
that renames the beats has stopped being a telling of it.

Then paint it. The capture's colour is thrown away with the escapes, so the
inks go back by hand as `{d …}` / `{b …}` / `{g …}` / `{c …}` / `{u …}`, which
that file documents. The markup does not occupy columns, so it cannot push a
line out of alignment.

`go test ./internal/ui/` is the check: `TestSiteFramesFitTheTerminal` holds
every frame to `termCols` × `termRows`, which is what the stage is cut for.
Both numbers live in siteframes.go, and app.css encodes the shape they imply —
`aspect-ratio`, the `(100svh - 8rem)*<w/h>` height cap, the title bar's own
height, and the `/ 62` the type is sized by. Changing a frame's shape means
changing all of them together. If the
tallest frame ends up well short of `termRows`, lower it: the leftover rows are
dead space inside the stage, and the type shrinks to pay for them.

## 4. Clean up

Kill the tmux session, run `fuser -k 8788/tcp 8899/tcp`, and remove the tool
stub. If you ran without `TACIT_TECHNIQUE_MEMORY`, repair the member's real
`~/.tacit-technique-memory.json`. Update the staged-data captions if the
scenario changes.
