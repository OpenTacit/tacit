# Contribute a technique

When you find a useful move, such as a prompt pattern or a recovery tactic,
contribute it as a draft technique. Once a reviewer promotes the draft, OpenTacit
can suggest the technique to colleagues during relevant work.

## Contribute from a session

In the session where the move worked, run:

```
/tacit:contribute
```

You can also say it in your own words: "contribute this to OpenTacit" or
"capture this as a technique." OpenTacit then does these steps:

1. OpenTacit writes a draft of the technique from the session: the name, the
   description, the recipe, when it applies, when it does not apply, and
   the tags.
2. OpenTacit confirms the structured choices with you in a short native form:
   the scope (org-specific or general), the name, and the tags.
3. OpenTacit shows the description and the recipe in the conversation, so you
   can edit the text.
4. OpenTacit asks for a final confirmation with a preview: submit, continue to
   edit, or discard.

After you submit, you get the id of the technique. You also get a note that
the technique is a draft that waits for review. OpenTacit does not offer the
draft to a member until a reviewer promotes it.

## Write a useful technique

The best techniques have these properties:

- **One move, not a manual.** If a technique needs three recipes, make
  three techniques.
- **A recipe with clear steps** that a person can follow without more
  context.
- **Correct limits.** The "applies when" and "not when" fields prevent an
  offer of the technique at an incorrect moment. These fields have the same
  value as the recipe.
- **The correct scope.** Choose org-specific for a move that depends on the
  systems or conventions of your organization. Choose general only if the
  move is applicable in all organizations.

## Propose a change to a technique in the playbook

If a technique is incorrect or not complete (it gave you incorrect
guidance, or you found a better recipe), propose a revision. Do not
contribute a duplicate:

```bash
tacit revise <technique-id> --not-when "monorepos with generated code" \
  --note "Misfired on generated files; scoping it out."
```

Give only the fields that you want to change. Add `--note` to tell the
reviewer the reason. The revision goes into the drafts queue with a
field-by-field diff. The serving technique continues without a change until
a reviewer promotes the revision. When a technique gives incorrect
guidance, `/tacit:audit` offers to prepare a revision for you.

## What happens to your draft

Drafts wait in the [Review queue](../30-dashboard/11-review-drafts.md).
There, a reviewer can edit, promote, or reject them. OpenTacit attributes your
draft to the cohort of your team, without your name. No transcript goes
with the draft. To examine the queue at any time, use `/tacit:drafts`, or
look at the ⚑ count in your
[status line](04-work-with-suggestions.md).
