# Work with suggestions in your session

After you connect, OpenTacit operates silently in your session. This chapter
tells you what you can see in a session and which actions you can do.

## The ◆ OpenTacit suggestion

A suggestion shows below the answer when a turn completes and the registry
has a validated technique that matches your work:

```
┃ ◆ OpenTacit: a suggestion from your org's playbook
┃ Clear context instead of patching a derailed session
┃ why:  You've corrected the same misunderstanding three times this session.
┃ try:  Start a fresh session and restate the goal in one message …
┃ measured by colleagues: helped 94% · adopted 70% · n=120 · team:revops
┃ reply "helped" / "not relevant", or try it to record adoption
┃ automatically · ask @tacit anytime · <technique-id>
```

The parts of the suggestion:

- **why** — one sentence that tells you why this technique matches your
  current work. OpenTacit checks this against your context before it shows the
  suggestion.
- **try** — the recipe, ready for use.
- **measured by colleagues** — the real evidence for the technique. If a
  technique has no measurements, OpenTacit does not show this line. OpenTacit does
  not invent numbers.

You can respond to a suggestion in one of three ways:

- **Try it.** If you apply the move, OpenTacit records the adoption
  automatically. No reply is necessary.
- **Reply in your usual words.** OpenTacit understands and records replies such
  as "that helped," "didn't work," "not relevant," or "already knew that"
  in your next message.
- **Ignore it.** OpenTacit records no action.

OpenTacit limits suggestions by design:

- a maximum of one suggestion for each 6 turns and 45 minutes
- a maximum of 4 suggestions in each period of 6 hours.

This budget applies to you, not to one session. A second terminal or a
pause does not reset the budget. Direct requests (`@tacit`, the OpenTacit
skills) have no limit. If you dismissed a technique, or you already use it,
OpenTacit does not show it to you again for 30 days. If the registry or the
model is slow, the suggestion does not delay your turn. OpenTacit holds the
suggestion and delivers it with your next prompt.

Your harness controls where these blocks can appear. Some clients (Claude
Code on the web or a phone, GitHub Copilot CLI) do not show pushed
suggestions. See [Where OpenTacit can and can't
render](03-how-tacit-behaves-in-your-harness.md#where-opentacit-can-and-cant-render).
Where blocks cannot appear, pull the same techniques with
[`/tacit:review`](05-search-and-review.md) or `@tacit`. These show as usual
conversation text.

## Ask @tacit

To speak to OpenTacit directly, put `@tacit` in a prompt:

```
Refactor this handler — @tacit do we have a validated approach for schema migrations?
```

OpenTacit answers alongside the response to your work request. The answer uses the
techniques in the registry and quotes
their real evidence. If the organization has no validated move for your
question, the advisor tells you this clearly. It does not invent an answer.

Some phrases get a special response:

- **"@tacit are you working?"** — a one-line status. It gives the shown and
  adopted counts for the live sessions of this machine, and the condition
  of the registry and the model.
- **"@tacit how are we doing?"** — the shown and adopted counts for this
  machine, with a link to the full funnel, helped rate, and org-wide
  numbers on the [dashboard](../30-dashboard/09-explore-outcomes.md).
- **"@tacit remember this"** — refers you to
  [`/tacit:contribute`](06-contribute-a-technique.md), which captures the
  move correctly.
- **"@tacit that helped"** — OpenTacit acknowledges this. The automatic record
  of the reaction already exists.

## The status line

If your status line has the OpenTacit segment (see `tacit statusline` in the
[command reference](../50-reference/16-command-reference.md)), the segment
shows the session:

```
◆ tacit 2⚡ 1✓ 3⚑
```

- **2⚡** — the count of suggestions that OpenTacit showed in this session
- **1✓** — the count of suggestions that you adopted in this session
- **3⚑** — the count of drafts in the shared review queue (visible only
  when the count is more than zero)

When a problem needs attention, a warning replaces the counters, for
example `⚠ registry:unreachable (tacit doctor)`. The warning gives the
command that finds the cause.

## The one-time repository invitation

A single ◆ OpenTacit line invites you to join when two conditions are true: the
team of the repository uses OpenTacit, and your machine is not connected. The
line appears one time for each registry. Members with a connection do not
see it.

## The daily cohort question

If you are connected but you did not set a cohort, your first prompt gets a
single ◆ OpenTacit line that asks for one. Reply with your team and role in
your own words ("payments team, engineer — set my OpenTacit cohort"). The agent
then saves the cohort for you. Without a cohort, OpenTacit cannot credit your
outcomes to a team. Because of this, the question comes again, a maximum of
one time each day, until you set a cohort. You can set it at any time with
[`tacit connect --segment`](../10-get-started/03-join-your-organization.md).

## Your technique memory stays on your machine

The data about *your* choices (the suggestions that you adopted or
dismissed) stays only in `~/.tacit-technique-memory.json` on your machine.
This file prevents repeated suggestions. It also makes messages such as
"you use 4 of the 6 techniques common on your team" possible. No data about
you as a person goes to the registry. See [What stays
private](03-how-tacit-behaves-in-your-harness.md#what-stays-private).
