# Join your organization's registry

To use OpenTacit, your machine needs two things. It needs your member settings:
the address of the registry, and the key that identifies your machine. It
also needs the wiring that connects your AI coding tools to OpenTacit. One
command usually does both.

## Join with an invite link

Ask a colleague or your OpenTacit administrator for an invite link. Then run the
line that they send you:

```bash
curl -fsSL <join-link> | sh
```

That one line installs OpenTacit, joins the registry, and wires each AI coding
tool that it finds on your machine. If you already have the `tacit` binary,
run this command instead:

```bash
tacit join <join-link>
```

Invite links expire. If the join fails, ask for a new link.

## Join by hand

If you accepted an invitation in a browser, the page gives you a command with
a code in it. Run it on the machine where your AI tools are:

```bash
tacit connect --registry https://tacit.example.com --code ABCD-EFGH-JKLM
```

The code trades itself for a member key on this machine. It works once and
expires in 15 minutes. If it lapses, ask for a fresh invitation, or mint
another code on the Members page if you administer the registry.

If you were given a member key directly instead, pass it the same way:

```bash
tacit connect --registry https://tacit.example.com --key <your-key>
```

Either command saves your settings, checks them, and then
wires your harnesses. To do the two steps separately, first run
`tacit connect --registry <url> --code <code> --settings-only`. Then run
`tacit connect`. Use the registry URL exactly as you received it. If your organization serves OpenTacit
under a sub-path (for example `https://demo.example.com/apps/tacit`), the
path is part of the address.

**Tip:** In a Claude Code session, `/tacit:setup` does the same steps in
conversation. It shows you how to enter the key so that the key does not
appear in the transcript.

## What OpenTacit wires

`tacit connect` finds the supported harnesses on your machine. There are
nine: Claude Code, Codex, Gemini CLI, GitHub Copilot CLI, Cursor, Amp, pi,
omp, and opencode. It wires each harness that it finds.
For Claude Code, it installs the OpenTacit plugin (commands, skills, hooks, and
the MCP server). For each harness, it prints the result as `ok`, `todo`
(a step that you must complete yourself), or `FAIL` lines. To wire only one
harness, pass `--harness`, for example `tacit connect --harness claude-code`.

## Tell OpenTacit your cohort

OpenTacit reports outcomes by *cohort*: team, role, and similar aggregate
dimensions. Your organization can see which cohorts use a technique, while
OpenTacit does not track individual people. The cohort is optional. It
makes reports such as "the payments team adopted this move" possible.

In most cases, your team already has a cohort. Use your team's cohort; do not
create a new one. `tacit join` shows you the list at the end. You can ask for
the list again at any time:

```bash
tacit cohorts
```

```
Cohorts already in use at https://tacit.example.com
(the number after each is how many sessions it has been seen in)

  team       payments 12 · platform 9 · growth 2
  role       engineer 18 · analyst 4
  function   — nobody has set this yet
  domain     backend 7
  harness    claude-code 24 · codex 3   [set for you]
```

Copy the values that fit and save them:

```bash
tacit connect --segment team=payments,role=engineer
```

Spelling is important, and nothing corrects it. `payments` and `payments-eng`
are two separate cohorts to every report in the registry. This is the reason
to look at the list first. `tacit cohorts` also tells you when you are the
only user of your value. This is usually a typo.

If your team is not on the list, give it a name. Each value works. After you
use OpenTacit, the name appears on the list for the next person who joins.

It is not necessary to remember this. You can skip the step. Then, the first
time you prompt your agent on a machine with no cohort, a `◆ OpenTacit` line asks
for the cohort. The line names the cohorts that your colleagues already use.
Reply in plain words ("payments team, engineer — set my OpenTacit cohort"). The
agent then saves the cohort for you. If you ignore the line, OpenTacit sends the
reminder a maximum of one time each day until you set a cohort.
`/tacit:setup` also does this step in conversation when you are ready.

## Add a model key for better suggestions

The local agent works without model access. But suggestions get better when
the agent can fit-check candidates with a model. Put a model API key in
`~/.tacit-key.env`:

```
TACIT_LLM_API_KEY=<your-key>
```

The agent reads the key automatically while it runs. No restart is necessary.

## Check the connection

Run `tacit doctor --ready`. It checks the wiring, the local agent, and the
registry, and answers in one line. If something is wrong, it prints the full
report and names the step that fixes it.

To check whether a suggestion appears in a live session, run
`tacit doctor --deliver` and start a session.

Then use it. `tacit ask "what you are working on"` returns one technique from
the playbook, with what happened when your colleagues used it — or says
plainly that nothing matches yet.

## Files on your machine

| Path | What it is |
|---|---|
| `~/.config/tacit/agent.env` | Your member settings: registry URL, key, cohort |
| `~/.tacit-technique-memory.json` | Your personal record of adopted and dismissed suggestions. It never leaves your machine. |
| `~/.tacit-key.env` | Your optional model API key (`TACIT_LLM_API_KEY`) |
| `~/.tacit-hooks.log` | The local agent's log |

Your settings file is private to your user account (mode 0600). Environment
variables override it. The local agent runs on demand and stops after 15
minutes with no activity. An idle agent is normal, not a fault.

## Leave a registry

`tacit disconnect` is the opposite of connect. It unwires your harnesses and
removes your member settings. It touches only the items that connect
installed. It also removes your technique-memory file.
