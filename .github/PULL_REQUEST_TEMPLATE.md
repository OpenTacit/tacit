<!--
Thank you for sending this. Delete anything below that does not apply.

If you have not read CONTRIBUTING.md yet, the two things it asks for that are
easy to miss are the DCO sign-off and a test named for the behaviour you
changed.
-->

## What this changes

<!-- What it does, and why. If it fixes an issue, "Fixes #123" closes it on merge. -->

## Before you send it

- [ ] `make test` passes, and `make fmt` leaves nothing to do.
- [ ] `make licenses` passes — it also checks that any new file carries the
      two-line copyright header.
- [ ] New behaviour comes with a test named for that behaviour.
- [ ] Commits are signed off: `git commit -s`. There is no CLA; the sign-off is
      the [DCO](https://developercertificate.org/), certifying you have the
      right to submit this under Apache-2.0.

## If it applies

- [ ] **Visual change:** I read the header comment of
      `internal/ui/assets/app.css` first, and I ran the page and
      looked at it. Reuse beats invention, and rendering bugs do not show up in
      a diff.
- [ ] **Changes what leaves a member's machine,** or what a registry stores
      about an individual. Aggregate-never-individual is a precondition rather
      than a setting, so say plainly what changed.
- [ ] **Schema or wire-contract change** (`schemas/`, MCP tool names,
      `tacit-feed/v1`). These are versioned and other implementations depend on
      them — CONTRIBUTING.md asks for an issue before the effort on these.
- [ ] **New dependency.** Say what it buys that the standard library does not.
      The default build reaches two, and that is a feature.

## Anything unresolved

<!-- Known gaps, decisions you were unsure about, things you want a second opinion on.
     Saying "I could not test X" is more useful than leaving it to be discovered. -->
