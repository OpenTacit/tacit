# Security policy

## Reporting a vulnerability

Mail **team@opentacit.com**. Do not open a public issue.

Include what you did, what happened, and what you expected. A proof of concept
helps, and so does the output of `tacit doctor` if the problem is specific to a
deployment.

You can also use GitHub's private vulnerability reporting on this repository,
which reaches the same person.

## What to expect

This project has one maintainer, so response times depend on that person's
availability. We commit to:

- An acknowledgement within **five working days**. If you do not get one, mail
  again.
- An assessment of whether it is a vulnerability, and of its severity, within
  **fifteen working days** of the acknowledgement.
- Credit in the release notes, unless you would rather not be named.

Please give a **ninety-day** window before public disclosure. We will tell you
if a fix will take longer. If the flaw is being exploited, say so and we will
treat the window as closed.

## What is in scope

Anything that lets someone read or change what they should not:

- **The registry service** (`tacit serve`) — its HTTP API, the dashboard, the
  sign-in and invitation flows, the join and merge paths.
- **The local agent** (`tacit serve-hooks`, `tacit hook-relay`) and anything it
  writes into a member's harness configuration.
- **The federation protocol** (`tacit-feed/v1`) — signature verification,
  subscription handling, imported attestations.
- **The installer** (`install.sh`) and `tacit upgrade`, including checksum
  verification.
- **Privacy.** Any path by which per-person data leaves a member's
  machine, or by which a registry could attribute an outcome to an individual,
  is a vulnerability in this project. The registry must use aggregate data and
  must not store per-person data.

## What is not

- Findings against a registry you do not run and were not invited to test.
  Testing your own instance is encouraged; testing somebody else's is not.
- Missing hardening that costs nothing to enable yourself — a registry
  deliberately started without sign-in, say.
- Vulnerabilities in a model provider's API, or in a harness that loads this
  software. Report those to the people who ship them.
- Reports produced by running a scanner and forwarding its output without
  reading it.

## Verifying a release

Every release artifact carries a build provenance attestation: a statement,
signed by GitHub, that the file came from this repository's release workflow at
a particular commit. Checking it needs the `gh` CLI and nothing else:

```sh
gh attestation verify tacit_v0.1.0_linux_amd64.tar.gz --repo opentacit/tacit
gh attestation verify oci://ghcr.io/opentacit/tacit:v0.1.0 --repo opentacit/tacit
```

`SHA256SUMS` is attested too, so the file `install.sh` checks against is covered
rather than only the file it checks.

This is worth doing if you are packaging OpenTacit, reviewing it before an
organization-wide rollout, or pulling the image into a build. It is worth less
if you are about to run `curl … | sh`, because by then you have already agreed
to run a script you did not verify — the honest thing to say is that the
installer checks the digest and does not yet check the signature.

## Supported versions

Only the latest release. There is no long-term support branch and there will
not be one until there are more maintainers; claiming otherwise would be a
promise this project cannot keep.

Fixes ship in a new patch release, and `tacit upgrade` verifies checksums
before replacing a binary.
