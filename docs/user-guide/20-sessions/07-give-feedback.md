# Give feedback

OpenTacit uses member outcomes to rank techniques, show evidence, and flag decay.
It collects feedback from your usual actions and words, and records a
confidence level for each signal.

## What OpenTacit records, and when

OpenTacit monitors four stages for each technique:

| Stage | How OpenTacit records it |
|---|---|
| Shown | Automatically, when you see a technique: a pushed ◆ suggestion, or the one technique that an `@tacit` answer cites (a playbook search does not count) |
| Adopted | Automatically, when your action on a suggestion is verifiable, for example when you run a command that matches the recipe |
| Helped | From your words ("that worked") or an `@tacit that helped`; OpenTacit can also infer it carefully when a move that you adopted gets no complaint |
| Dismissed | From your words ("not relevant," "didn't work," "already knew"); OpenTacit maps each phrase to a reason |

No action is necessary from you for this. A reaction in your usual words in
your next message is sufficient: OpenTacit records "that helped" and "didn't
work" correctly.

## Explicit and inferred signals

Each event has a confidence level:

- *explicit* — you said it in clear words.
- *inferred* — OpenTacit deduced it from behavior.
- *verification* — this third level applies to autonomous "applied"
  sessions, where a build or test result replaces the reaction of a member.

The registry keeps explicit and inferred signals separate. The signal-trust
view of the dashboard shows the agreement between inferred signals and
explicit signals. Because of this, automation cannot increase the evidence
of a technique without a visible record. OpenTacit records inferred "helped"
events carefully: one time for each technique, and only after an adoption
continues through more turns without a complaint.

## Give a reason when you dismiss a suggestion

When you dismiss a suggestion, the reason is important:

- **Not relevant here** — the retrieval did not match your work. This
  signal adjusts the matching.
- **Didn't work** — evidence against the technique. A sufficient number of
  these signals flags the technique as decaying.
- **Already knew** — the technique is good, but you do not need it. OpenTacit
  does not show it to you again for 30 days.

## Record feedback deliberately

This is usually not necessary. OpenTacit already records a reaction in your
next message, or the application of the move. A `/tacit:feedback` picker is
available for the deliberate case. Do not use the picker and also give a
usual reaction for the same event, because that causes a double count.

In some cases you want to make sure that OpenTacit records an event. An example
is a suggestion from an earlier point in the session that you applied much
later. Then speak to OpenTacit directly:

```
@tacit that helped
```

OpenTacit acknowledges this and records the reaction against the technique that
it last suggested. From the command line, you can attach a reaction to a
specified technique by id:

```bash
tacit feedback <technique-id> --stage helped
```

A `helped` also records the adoption. Send one stage, not two.

## What leaves your machine, and what does not

Feedback events contain the id of the technique, the stage, the confidence,
and your *cohort* (team, role, harness). They do not contain your name, and
the registry has no member identities to connect them to. (Your history of
adoptions and dismissals stays [on your
machine](03-how-tacit-behaves-in-your-harness.md#what-stays-private), never
in the registry.)

There is one more data flow, and it applies only if your organization
configured it. The first adoption of a suggestion can send an anonymized
*sketch*: the situation and the move, with identification data removed.
Sketches help the organization find techniques that are not in the
playbook. The setting is a visible line in your `agent.env`
(`TACIT_SKETCH_URL`). OpenTacit tells you when the value is first set. If you
clear the value, you opt out permanently. Your raw transcript does not
leave your machine in either case.
