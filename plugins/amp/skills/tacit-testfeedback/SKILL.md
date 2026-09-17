---
name: tacit-testfeedback
description: "Arm a delivery self-test so one ◆ Tacit block arrives through the real hook channel at the end of this turn, proving the member would see a live suggestion. Use when the member invokes $tacit-testfeedback or asks whether Tacit's feedback actually reaches them."
---

Prove that a Tacit suggestion is VISIBLE in this session.

$tacit-status says the pieces are running and $tacit-test says retrieval
works. Neither answers the last question: when a technique matches, does the block
reach the member's eyes? Some clients render no hook output at all, and there
the failure is silent — everything healthy, nothing ever shown.

## Method

Run this once:

```bash
tacit doctor --deliver
```

That arms the local hook agent. At the end of THIS turn its Stop hook delivers
one block through the same channel a real suggestion uses.

**If the command printed a `--- RELAY BLOCK ---` section**, this member's client
renders no hook output and the armed block will reach their terminal only.
Reproduce that block **verbatim** in your closing reply — copy it exactly as
printed, changing nothing. The agent wrote it; you are carrying it. If the
command printed no such section, add nothing: the hook's own block is coming.

**The command also printed a `--- FORM ---` section.** Raise it as exactly one
**tacit_form**, still inside this turn, before you finish — header, question
and all four options **verbatim** as printed. The binary wrote that spec; you
are carrying it, the same way you carry a relay block.

This is the third channel, and the one a member is most likely to see. The
block is hook output, which web and mobile clients drop. The relay is model
output, which always renders and therefore proves nothing about push. The form
is a native prompt the client draws itself, so it reaches every surface — and
it is the shape a real suggestion takes when Tacit offers one: the move,
and four ways to answer.

Then close in two lines at most:

- Say whether the form appeared, and that a live suggestion arrives the same
  way, carrying a real technique instead of the placeholder wording.
- Say the ◆ block should follow your reply. If it does not, that is the
  web/mobile split rather than a fault — hook output does not render there —
  and $tacit-review pulls suggestions into ordinary conversation instead.

## Rules

- **Never compose the block yourself.** Do not imitate a ◆ Tacit block, do not
  describe what it will contain, and do not reconstruct one from memory or from
  this description. The whole point is that the member sees text this session did
  not author. An imitation proves nothing and destroys the test. Copying the
  `--- RELAY BLOCK ---` the command printed is the one exception, and it is not
  really one: that text is the agent's, and carrying it unchanged is the test.
- **The form is yours to raise and not yours to write.** Copy the printed spec
  exactly. Never substitute a technique name, a recipe or a measured figure for
  the placeholder wording: an invented evidence line forges the one thing no
  model may author, and would prove Tacit can be imitated rather than
  delivered.
- Run `tacit doctor --deliver`, raise the one form, and finish. Nothing else: the
  block rides the end of the turn.
- The block is labelled a self-test and carries no technique. Nothing is retrieved
  and no outcome is recorded, so this never touches the org's measurements.

## If no block appears

Have the member run:

```bash
tacit doctor --deliver-status
```

- `delivered` — the agent returned the block and the client dropped it. Claude
  Code's web and mobile clients render no hook output; the automatic nudge cannot
  reach the member there, though the terminal they are connected to does render
  it. This is the case the relay block covers, so a member on those clients still
  sees a block — a relayed one, which says so in its closing line. For live
  suggestions, pull: ask for a technique, or run $tacit-review.
- `parked` — this harness renders nothing at a turn's end, so the block arrives
  with the member's next message. That is the real delivery here, not a fault.
- `suppressed` — this harness renders no hook output at all. Tacit never pushes
  here; the skills are the way in.
- no agent, or `expired` — the hooks are not wired into this harness. Run
  `tacit doctor --harness <name>`, which names the failing hop.

A `delivered` block that never appeared as hook output, alongside a form that did
and a relayed block that did, is the sharpest result this test produces: push is
dead on that client, while its native prompts and ordinary model output are both
alive. Tacit has to ask, relay or offer a form there — never push.
