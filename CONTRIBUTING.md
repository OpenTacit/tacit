# Contributing

We welcome bug reports, harness integrations, schema implementations, and
documentation fixes. Please also report errors in measured evidence.

## Licensing and sign-off

Contributions are licensed under [Apache-2.0](LICENSE), the license the project
carries. Section 5 of that license already makes this so for anything you
deliberately submit; the sign-off below is how we keep the provenance auditable.

**There is no CLA.** You keep your copyright, and we hold no right to relicense
your work under different terms.

We use the [Developer Certificate of Origin](https://developercertificate.org/).
Sign each commit:

```bash
git commit -s -m "your message"
```

which appends `Signed-off-by: Your Name <your@email>`. It certifies that you
wrote the contribution or have the right to submit it under Apache-2.0 — not
that you assign anything.

If your employer owns your work output, get their clearance before you send it.

## Before you send a change

- `make test` passes (`make vet`, Go tests, plugin tests) and `make fmt` is clean.
- New behavior comes with a test. Registry contracts are schema-enforced and
  round-tripped against the normative schemas in [`schemas/`](schemas/).
- Visual changes read the header comment of
  `internal/ui/assets/app.css` first. It defines the registry's house
  style. Reuse its existing patterns.
- Prose in docs, UI strings, and commit messages follows the rules at the top of
  [`CLAUDE.md`](CLAUDE.md).
- The README's Layout section maps the tree; `pkg/contracts` and `schemas/` are
  where the wire surface is defined.

## Things that need a conversation first

Open an issue before building any of these, so the design discussion happens
before the effort does:

- A new capture surface or harness adapter. These changes affect product scope,
  and the capture layer stays small.
- Anything that changes what leaves a member's machine, or what the registry
  stores about an individual. The rule is aggregate-never-individual, and it is
  a fixed design requirement.
- Schema or wire-contract changes. They are versioned and other implementations
  depend on them.

## Security

Do not open a public issue for a vulnerability. Mail team@opentacit.com and
allow a reasonable window before disclosure.
