# Glossary

### adoption

The confirmed use of a suggested technique. OpenTacit records an adoption
automatically when your actions match the recipe. An offer alone never
counts as adoption.

### area of practice

A cluster of related techniques on the playbook map: techniques that share
tags, or techniques that the same cohorts adopt together. The map uses areas
to divide the playbook into groups with names.

### technique

A validated method of working with AI that someone recorded for others to
use. A technique contains a name, description, recipe, boundaries, tags, and
measured outcomes. Together, an organization's techniques are its
[playbook](#playbook).

### cohort

An aggregate dimension for outcome reports, in the form `dimension:value`,
for example `team:payments` or `role:engineer`. OpenTacit uses cohorts to show
"who" without data about individuals.

### decay

The flag on a technique whose recent helped rate fell below its own
baseline. Decayed techniques do not continue to serve on old evidence. They
go to the queue on the Review page for revalidation. The flag shows as a
"Decaying" moment in the [Events feed](#events).

### draft

A technique that waits for review. Drafts never reach members through
retrieval. A reviewer promotes, edits, or rejects them.

### Events

The activity feed under [Outcomes](#funnel): a record of OpenTacit's activity,
with the newest items first. It shows delivery moments (the funnel, one
interaction at a time) and [lifecycle events](#lifecycle-event) (changes
that the playbook makes to itself). The feed shows cohort-level data only,
like every OpenTacit view.

### evidence

A technique's measured outcomes. OpenTacit always shows them exactly as measured
("helped 94% · n=120") or as "awaiting measured outcomes." Evidence comes
only from feedback. OpenTacit never asserts evidence.

### federation

The protocol that shares techniques between organizations. Organizations
publish signed feeds of techniques by channel. Evidence crosses only as
aggregate attestations.

### fit check

The test before delivery. Before OpenTacit shows a retrieved technique, it checks
the technique against your current work. Each suggestion includes a specific
reason for the match.

### funnel

The shown → adopted → helped progression. It measures whether suggestions
give results, from offer to impact.

### harness

The kind of [surface](#surface) that installs an integration on your
machine: an AI coding tool (Claude Code, Codex, Gemini CLI, GitHub Copilot
CLI, Cursor, Amp, pi, omp, or opencode). The tool's hooks let OpenTacit listen,
and let you address OpenTacit.

### helped rate

The share of a technique's adoptions that helped, as measured. It is the
core quality signal that controls rank order and decay.

### hook agent

The small local process that watches your session's events, asks the
registry for techniques relevant to your task, and delivers suggestions. It
runs on demand, on your machine, and stops when it is idle.

### lifecycle event

A record of an action that the registry does on the playbook itself. The
registry records a technique as **discovered** from usage, **promoted** to
serve on evidence, **retired**, or **decaying**. Each event has its reason.
These events, together with delivery moments, fill the
[Events feed](#events).

### member

A person who uses a [surface](#surface) with a connection to the registry: a
harness on their machine, or a chat connector that they signed into. The
registry identifies member *machines* by key for access control. The
registry never joins machines to feedback.

### miner

A separate service that reads sources OpenTacit cannot reach on its own and
proposes techniques back to your registry. None ships with OpenTacit, and you
need none to use it.

OpenTacit discovers techniques from two sources by itself. `tacit init` reads
the conventions a repository has already written down — `CLAUDE.md`,
`AGENTS.md`, `.cursorrules`, the Copilot instructions, and the skills, commands
and rules beside them — and files each as a draft. And the registry watches for
moves that worked and were repeated: when the same move appears in at least
three sessions across at least two [cohorts](#cohort), it becomes an *observed*
technique. Both work on one organization's own material, which is the limit:
the first sees the repositories you point it at, the second sees only what your
own members did.

A miner is how an organization goes wider. It reads what OpenTacit does not —
other repositories, ticket systems, incident write-ups, review comments,
whatever a particular miner is built for — and posts its proposals to the
registry, where they arrive in the [drafts](#draft) queue like any other
suggestion and wait for a person. You point a registry at one by setting
`TACIT_SKETCH_URL`, which is also what sends it [sketches](#sketch), and
OpenTacit tells the member when that value is first set.

### playbook

The organization's full validated set of techniques: its measured record of
what works with AI. The playbook is the knowledge. The [registry](#registry)
is the service that stores, measures, and serves the playbook.

### promote

The review decision that changes a draft into a technique that serves.

### provenance

The source of a technique: curated, contributed, suggested (by a research
pass), mined, federated, or *observed*. An *observed* technique comes from
repeated worked moves that the organization never wrote down.

### registry

The shared service that stores the playbook's techniques, measures outcomes,
and serves the dashboard. Each organization has one registry.

### retire / restore

To retire is to take a technique out of service without deletion. To
restore is to put the technique back.

### revision

A proposed change to a technique that already exists. A reviewer sees it as a draft
with a diff. The original continues to serve.

### scope

The property that shows whether a technique is *general* (good practice
anywhere) or *org-scoped* (specific to your organization). Org-scoped
techniques have an `org` badge.

### segment

The cohort values that your machine declares (`team=…,role=…`). OpenTacit
attaches them to feedback, so that OpenTacit can report outcomes by cohort.

### shadow

A technique under evaluation for relevance, which members never see.
Retrieval retrieves it, and the fit check judges it, but OpenTacit never shows
it. A shadow technique auto-promotes to serve when its fit evidence passes
the threshold. It auto-retires if it fits too rarely. Shadow is the
machine's own review lane.

### shown

The funnel's first stage: a technique that a member saw. This includes a
push suggestion, and the one technique that an `@tacit` answer cites. A
search of the playbook (a *pull*) is not a `shown` event. As a result,
search traffic never increases helped rates.

### signal trust

The dashboard view that compares inferred feedback with explicit feedback.
This keeps automated inference calibrated.

### sketch

An opt-in, scrubbed record of a situation and the adopted move. OpenTacit uses
sketches to discover techniques that the organization did not write down. A
sketch never includes a transcript.

### suggestion

A technique that OpenTacit offers in your session (the ◆ OpenTacit block) after
retrieval and a fit check.

### surface

Any AI tool through which OpenTacit reaches your work. A [harness](#harness) is
a surface that installs hooks. It can listen, and you can address it. A
*chat connector* (Claude or ChatGPT with the registry's MCP endpoint added)
is a lighter surface. It installs nothing, and you can only address it.
