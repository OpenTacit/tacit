# Search and review on demand

You can use `/tacit:` commands to search the registry at any time: before a
task, during a task, or after a task.

The first three commands (**search**, **review**, and **audit**) do the
same playbook retrieval on three different inputs: a query that you give,
your current work, and a completed session. Use the command that matches
your scope. The evidence is the same for all three.

Because these commands are *pulls*, they do not change the funnel. This
type of playbook access records no `shown` event, so it does not increase
the adoption or helped rate of a technique. (A pushed ◆ suggestion and a
technique that an `@tacit` answer cites do count, because a member saw the
technique.)

## Search the playbook

```
/tacit:search deploying a lambda behind an api gateway
```

OpenTacit searches the playbook of your organization and reports the best
matches (a maximum of four). Each match includes:

- the name and the id
- the measured evidence, quoted without a change
- one sentence that tells you when the technique applies
- the recipe, ready for use.

If you run the command with no query, OpenTacit makes a query from your current
work.

A result with no matches means that your organization has no
validated move for that subject. When you find a move that works,
[contribute one](06-contribute-a-technique.md).

## Review your current work

```
/tacit:review
```

`/tacit:review` examines your current work: the full session, or a focus
that you give. It finds the different activities in the work and checks
each activity against the registry. Matches show as ◆ OpenTacit blocks in the
conversation. Because of this, the command operates on all surfaces. This
includes Claude Code on the web and on phones, where automatic suggestions
cannot appear.

You can also ask in your own words ("review this against OpenTacit"). This
runs the same check in a subagent that examines more deeply.

## Audit a finished session

```
/tacit:audit
```

`/tacit:review` examines current work. `/tacit:audit` examines a completed
session: the current session, a transcript file, or a shared conversation
link. The audit shows the techniques that the registry had for that
session. It then proposes follow-up actions. Each action needs your
confirmation:

- A technique to try the next time
- A repeated problem that the registry does not cover, with an invitation
  to contribute
- A revision for a technique that gave incorrect guidance; the revision
  goes for review, and the serving technique does not change

You can run the same audit from the command line: `tacit audit <file-or-url>`.

## Ask for usage data

Three commands answer the larger questions from your session:

- **`/tacit:insights [7d|30d|90d|all]`** — the pipeline of technique usage:
  the shown → adopted → helped funnel, the helped rate, and trends. In
  clients with inline panels, the output shows as a small dashboard. In the
  terminal, it shows as text.
- **`/tacit:org [7d|30d|90d|all]`** — the view of who and where: the
  techniques that each cohort adopts, the areas of practice that grow, and
  the places where the proven move of one team can help another team. The
  data is always cohort aggregates, never individuals.
- **The playbook map** — in clients with inline panels, ask for "the tacit
  map" to see the full live playbook as a graph. The position of each
  technique shows its relation to the other techniques. The size shows
  adoption. The shade shows the helped rate.

The [dashboard](../30-dashboard/08-the-dashboard.md) gives all of this and
more in the browser. This includes the
[**Events** feed](../30-dashboard/09-explore-outcomes.md#the-activity-feed),
a continuous record of OpenTacit activity.

## Review drafts without the dashboard

```
/tacit:drafts
```

This command opens the draft review queue in your session. It offers the same
decisions as the dashboard's [Review page](../30-dashboard/11-review-drafts.md).
The command uses the options that your client supports:

- **An interactive panel** — in clients that show MCP apps (Claude on the
  web and desktop), the queue shows the full technique of each draft, with
  Promote and Reject buttons. Decisions apply immediately.
- **A native form** — in the terminal, OpenTacit shows each draft in full. A
  one-keystroke question asks for your decision: promote, reject, edit
  first, or skip.
- **Plain conversation** — on all other clients, OpenTacit shows the drafts in
  the chat. OpenTacit does an action only when you give an instruction.

With each mechanism, OpenTacit promotes or rejects a draft only when you give
an explicit instruction. If your member key cannot decide drafts, the
command tells you this and refers you to the dashboard.
