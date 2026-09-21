# Start on your own

You do not need colleagues to get something out of OpenTacit. You do not need a
server, a model key, or anyone's permission. This chapter takes one person from
nothing to a technique arriving in their own coding session, and then shows
what changes when other people join.

Read it first even if you are setting OpenTacit up for a team. A team follows the
same steps for each member. The registry cannot send techniques until members
wire their tools to it.

## What one person gets

You may have already written down how you work. It is in `CLAUDE.md`, or
`AGENTS.md`, or `.cursorrules`, or the copilot instructions, or the skills and
commands under `.claude/`. Your agent reads these rules at the start of a
session, but may not apply them later in a long session.

OpenTacit turns each rule into a *technique*: one method stored on its own. It can
then suggest the technique when it applies instead of loading every rule at the
start of a session.

## Two commands

Install, then bootstrap, inside a repository you actually work in:

```bash
curl -fsSL https://opentacit.com/install.sh | sh
tacit init
```

OpenTacit runs on Linux and macOS, amd64 and arm64. On Windows, run it under WSL2 —
there is no native Windows build, because the model OpenTacit matches with has none.

`tacit init` sets up a registry on this machine and reads that repository's
conventions into it. When it finishes it tells you what it did and what to do
next. The first of those is:

```bash
tacit connect
```

This wires your coding tools — whichever of the nine supported ones you have —
so they can reach the registry. Until you run it, nothing reaches your
sessions.

`tacit init` is idempotent. Running it twice adds only what is missing.

## Review what your repository wrote

`tacit init` files the rules it found as **drafts**. OpenTacit does not suggest a
draft until someone promotes it. On a personal registry, you review them.

Open the dashboard from the sign-in link `tacit init` printed, go to
**Review**, and work through the queue. Promote the rules that are still true.
Reject the ones you no longer use. A rejection records useful information.

## Use it

From here the loop runs without you asking. Work as usual, and techniques
arrive in your session when they fit the task
([Work with suggestions in your session](../20-sessions/04-work-with-suggestions.md)).

Two things you can do directly:

```bash
tacit ask "<whatever you are working on>"
```

asks the playbook for its best answer to a real task, with the evidence behind
it. If no technique matches, it says "nothing matches that yet."

```bash
tacit usage
```

shows what you were shown, what you adopted, and what helped. It reads a log
that never leaves your machine. The registry does not collect this view, even
after colleagues join.

## What is true on day one

If the playbook has no measured outcomes, the dashboard states that. Rates show
their `n`, and an empty window explains what data it needs. The first few days
may contain little data.

At first, OpenTacit can retrieve your written conventions by meaning and suggest
them during work. As you use them, it records which ones you adopted and which
ones helped.

## Then: bring in colleagues

OpenTacit keeps the techniques and outcomes you recorded alone when somebody
joins. The added outcomes then affect ranking.

On your own, a technique ranks by how well it matches your task and by your own
outcomes. With colleagues, it ranks by what measurably worked for people doing
your kind of work. The shared outcomes make the ranking more useful than a set
of notes alone.

One command:

```bash
tacit invite
```

It prints a join link. They run it, and their tools are wired to the same
registry ([Join your organization's registry](03-join-your-organization.md)).

Before you send it, note that:

- **OpenTacit never shows per-person data.** Not to you, not to their manager, not
  to anyone. Outcomes aggregate across cohorts and the individual view is the
  member's own machine. This is part of the design and cannot be turned off.
- **Your drafts were org-scoped from the start.** The rules you promoted are
  already in the shape a colleague can use.

When a team is what you wanted from the beginning, read
[Set up a registry](02-set-up-a-registry.md) for the parts this chapter skipped:
sign-in, storage, the address on the internet, and the weekly digest.
