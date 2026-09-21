# How OpenTacit behaves in your tools

OpenTacit operates the same on all surfaces. This chapter tells you about this
shared behavior. The chapters that follow give the details of each task.

OpenTacit connects to your work through *surfaces*. Most surfaces are
**harnesses**: AI coding tools with a small OpenTacit integration. The
integration lets OpenTacit monitor a session and give an occasional suggestion.
One surface is different: a **chat connector** that you add to Claude or
ChatGPT. The connector installs nothing, and it lets the model pull from the
playbook on demand ([Connect a chat
tool](../10-get-started/04-connect-a-chat-tool.md)). The behavior in this
chapter applies on all surfaces that can supply it. The surfaces are
different only in how much of this behavior each one supports.

**OpenTacit monitors each session in the background, limits how often it suggests
a technique, and includes evidence. It also answers direct requests.**

## The two modes of operation

OpenTacit operates in two modes:

- **Automatic suggestions.** This mode is always on. OpenTacit monitors
  the session. When OpenTacit has information of sufficient value, it gives one
  suggestion that you did not request. OpenTacit limits this mode to a small
  number of suggestions in each session,
  with no repeats. → [Work with suggestions](04-work-with-suggestions.md).
- **Direct requests.** Use `@tacit` in a prompt, a `/tacit:` command, or a
  tool that your agent calls. Because you asked, this mode has no limit. →
  [Search and review on demand](05-search-and-review.md).

Both modes can incur costs. A search runs inside your turn and its results enter
the context you pay for. When a model key is set, OpenTacit calls that provider
to check a candidate against the turn before you see it, and that call is
billed to whoever owns the key. What the modes ration is your attention.

This difference is important because a surface can support one mode without
the other. A harness supports the two modes. A chat connector supports only
the second mode. The connector has no hooks, so no suggestion comes unless
you or the model asks. This one fact causes most of the differences between
surfaces.

In the two modes, **OpenTacit does not make a turn slower and does not add
unwanted output.** All heavy operations occur in the background. If the
registry or the model is slow or not available, OpenTacit becomes silent. It
does not cause a delay, and it does not put an error into your work.

## What the connection installs

When you connect a harness, OpenTacit installs a small integration package. The
package has a maximum of four parts. All parts read one settings file
(`~/.config/tacit/agent.env`), so they use the same registry and key:

- **Lifecycle hooks** — these let OpenTacit monitor your session and give an
  occasional suggestion. The hooks do not change your prompt or your tools.
- **Skills / slash commands** — commands that you start by name to pull
  from the playbook. They include search, review, audit, contribute,
  drafts, insights, and org. They also include the operational commands
  status, test, and help. The chapters that follow describe them.
- **An MCP server** — this gives your agent direct access to OpenTacit tools
  (`tacit_search`, `tacit_metrics`, `tacit_drafts`, `tacit_draft_action`).
  On hosts that support panels, the tools show as interactive panels. On
  all other hosts, they show as text.
- **A status-line segment** — on harnesses that have a status line, the
  segment shows session data
  ([the status line](04-work-with-suggestions.md#the-status-line)).

To disconnect, run `tacit disconnect`. The command removes all the parts
that the connection installed, and no other files.

A chat connector is different: it installs nothing on your machine. You add
one URL and sign in. Only the pull tools are available. To remove the
connector, delete it in your chat app. See [Connect a chat
tool](../10-get-started/04-connect-a-chat-tool.md).

## Where OpenTacit can and can't render

The behavior is the same on all surfaces. The difference is **where a
pushed suggestion can appear**. OpenTacit sends the ◆ block as hook output, and
a surface without hooks cannot show the block. This is the one important
difference between surfaces:

| Surface | Pushed ◆ suggestions | If they cannot appear |
|---|---|---|
| Claude Code (terminal), Codex, Amp, pi/omp, opencode | Yes, below the answer (on Amp, with your next message) | — |
| Cursor | Yes, but always with your *next* prompt | there is a delay of one turn |
| Claude Code on web or phone | No. These clients do not show hook output | use pull: `@tacit`, `/tacit:review`, or the tools; all show as usual chat text |
| GitHub Copilot CLI | No | OpenTacit continues to monitor; pull with the `tacit-*` skills or the `tacit-review` agent |
| Chat connector (Claude, ChatGPT) | No. A connector has no hooks, so OpenTacit does not push or monitor | this surface is pull-only by design; ask in words, or use the tools, which show in the chat |

**The pull path operates on all surfaces, also where the push path
cannot.** To make sure that OpenTacit is connected, ask it: `@tacit status`.

## What stays private

OpenTacit limits what it sends and stores:

- The registry stores **no identity and no conversation text.** The data
  that leaves your machine is a structured *characterization* (the task
  type, the tools in use, your cohort, for example `team:revops`) plus
  feedback events. OpenTacit uses the retrieval text only to find techniques,
  and it does not store this text.
- All reports are **by cohort, never by person.** A line such as "helped
  94% · n=120 · team:revops" is a cohort statistic.
- Your record of the suggestions that you adopted or dismissed stays **only
  on your machine** (`~/.tacit-technique-memory.json`). Its function is to
  prevent repeated suggestions.
- OpenTacit removes secrets and PII from all text that you contribute, before
  the text leaves your machine.

[Give feedback](07-give-feedback.md) gives the full contents of a feedback
event, and describes the optional sketch opt-in.

## More information

- [Work with suggestions](04-work-with-suggestions.md) — the ◆ block, `@tacit`, and the status line.
- [Search and review on demand](05-search-and-review.md) — how to pull from the playbook on demand.
- [Contribute a technique](06-contribute-a-technique.md) — capture a move that worked, so colleagues can use it again.
- [Give feedback](07-give-feedback.md) — how OpenTacit records the evidence for each technique.
