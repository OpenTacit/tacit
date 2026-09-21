# registry-first-check — does the single-member registry actually work?

One script, `check.sh`, that stands the whole thing up and asserts it:

```bash
./hack/registry-first-check/check.sh
```

It proves, in the order a member meets it:

1. `tacit init --owner on --global-access on` configures single-member auth
2. the registry dials the ingress and is allocated a public hostname
3. `tacit dashboard` prints a sign-in link **at that hostname**, not localhost
4. an anonymous request through the public hostname gets the front door only —
   no settings, no counts
5. the link is exchanged for a session, and the dashboard renders behind it
6. `tacit init` sets a registry up on its first run and only serves on its
   second, keeping the same address across restarts
7. one registry per machine: a second process over one file store is refused,
   and a port held by something that is not a registry is named as such
8. a registry with no sign-in refuses to take a public address at all

The script runs its own ingress on 127.0.0.1, with its own `HOME`, ports, and
data directory. It removes all of them on
exit — including on failure or Ctrl-C. Nothing enrols on the project's real
ingress, and your own registry is never contacted.

The zone is `localtest.me`, a public DNS convenience where every name resolves
to 127.0.0.1, so the allocated hostname is a real hostname that reaches the
local ingress.

If the default ports are busy:

```bash
PORT=29111 INGRESS_PORT=29443 TUNNEL_PORT=29444 ./hack/registry-first-check/check.sh
```

Exit code is 0 only when every check passes.
