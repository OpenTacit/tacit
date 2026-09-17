# merge-check — does `tacit merge` actually fold one registry into another?

One script, `check.sh`, that stands up an ingress, an organization's registry
and a member's personal one, and merges the second into the first:

```bash
./hack/merge-check/check.sh
```

It proves, in the order the command does it:

1. `tacit merge` refuses a registry that is not single-member — folding an
   organization's is not one member's decision
2. the owner can start the merge from the playbook page, with the join link and
   nothing else; a signed-out visitor is not offered it
3. `--dry-run` names what would go and its standing, contributes nothing, and
   **does not redeem the invitation** (a join token is spent by the exchange,
   so looking first must not kill the link)
4. techniques land at the organization as held-out **contributed drafts**, at
   version 1, with no id and no embedding carried across
5. the contributor's figures ride in the description as a claim, disclaimed as
   not measured there — and **no outcome field crosses as data**
6. the evidence archive is written before anything can be retired, holding
   every event and the counts folded from them
7. the harnesses are rewired to the organization
8. a second run sends nothing: the ledger recognises what has gone, and
   resumes with no new invitation
9. the member's playbook page names the organization and shows whether it has
   promoted each technique
10. it refuses to release the address of a registry it could not stop, and
   keeps the instance key
11. stopped, it finishes: the hostname goes back to the ingress, the route
    table forgets it, the public address answers nothing, and the key is gone

Checks 5, 8, and 10 guard against the most costly failures. Copying evidence as
data would corrupt an organization's promotion floors with a sample of one. A
second send would duplicate each technique in the review queue because the
destination's `UniqueID` never overwrites. Releasing the address of a running
registry would leave it serving on a name that no longer resolves.

The script uses its own ingress on 127.0.0.1, `HOME`, ports, and data. It removes
them on exit, including after a failure or Ctrl-C, and does not use the project's
real ingress.

The zone is `localtest.me`, where every name resolves to 127.0.0.1, so the
allocated hostname is a real one that reaches the local ingress.

If the default ports are busy:

```bash
PERSONAL_PORT=29121 ORG_PORT=29122 INGRESS_PORT=29453 TUNNEL_PORT=29454 \
  ./hack/merge-check/check.sh
```

Exit code is 0 only when every check passes.
