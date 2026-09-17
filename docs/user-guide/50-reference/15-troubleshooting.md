# Troubleshooting

OpenTacit includes checks for the command line and for active sessions. Each check
reports a suggested fix.

## Run the checkup

```bash
tacit doctor
```

`tacit doctor` checks everything on its own machine:

- The registry service, if this machine runs one.
- Your member settings.
- The API key.
- The local agent.
- The availability of a newer release.

Each check reports `ok`, a warning, or `FAIL` with the fix.

Two flags do deeper checks:

- `tacit doctor --harness claude-code` makes sure that the full hook path
  operates, from the harness's wiring through the local agent to the
  registry. It reports the hop that broke.
- `tacit doctor --fix` repairs a rejected member key. Paste a new key at the
  prompt. The command saves the key and makes sure that it is correct.

## Check from inside a session

- `/tacit:status` — a health check of each component: registry, local
  agent, model, services, and the session's own counters. If the model is
  unhealthy (out of credit, invalid key, rate-limited), the check shows
  that first and quotes the remedy.
- `/tacit:test` — an end-to-end test. It returns a prompt that causes a real
  suggestion and tests the full pipeline.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| "API key rejected (401)" | An administrator revoked the member key, or you typed it incorrectly | Ask your administrator for a key from the Members page. Then run `tacit doctor --fix` |
| "Registry unreachable" | The URL is wrong, or the service is down | Check `TACIT_REGISTRY_URL` with `tacit connect --settings-only`. On the registry machine, run `systemctl --user status tacit-registry.service` |
| No suggestions appear | The wiring has a fault, the suggestion budget is at its limit, or no techniques match | Run `tacit doctor --harness <name>`, then `/tacit:test`. Note: suggestions have a limit of one for each 6 turns and 45 minutes, and 4 for each 6 hours |
| Suggestions appear without evidence lines | The techniques are real but not measured | Nothing is wrong. The state "awaiting measured outcomes" is correct until colleagues adopt the techniques |
| Suggestions show in the terminal but not on web or mobile | Those clients do not show hook output | Use `/tacit:review`. It shows as usual conversation text |
| Status line shows `⚠ registry:…` or `⚠ llm:…` | An upstream fault needs an operator | Run the command that the warning names: `tacit doctor` or `/tacit:status` |
| Dashboard says the embedder is degraded | The semantic model did not load. Retrieval fell back to lexical matching | Run `tacit init --embeddings on` again on the registry machine. If you built from source, build with `-tags onnx` |
| Behind a reverse proxy, pages show without styles, or all links return 404 | The registry serves from a sub-path that it does not know about, or the proxy removes the prefix | Set `TACIT_BASE_PATH` to the sub-path. Forward the sub-path without changes. See [Serve under a sub-path](../40-administration/14-configure-the-registry.md#serve-under-a-sub-path) |
| A member's machine label shows "gone quiet" | The registry did not see their key recently | The usual cause is vacation, not breakage. Ask before you revoke the key |
| You set the `TACIT_OIDC_*` values, but the dashboard still opens without a sign-in | One of the four values is missing or empty. The registry turns sign-in on only when all four are present | `tacit doctor` reports it on the registry machine. `tacit secure` completes the set and restarts; on a single-owner registry, the Personal/Shared switch on **Settings → Access and sign-in** does the same from the browser. See [Turn on sign-in](../40-administration/14-configure-the-registry.md#turn-on-sign-in) |
| A wrong sign-in configuration locked you out of your own dashboard (on a single-owner registry, **Test configuration** catches this before Save) | Every HTML page needs a session, including the Settings page, so the browser cannot open it | On the registry machine, run `tacit secure --off` to reopen the dashboard. Then run `tacit secure` to correct the settings |
| Sign-in fails with `redirect_uri mismatch` directly after you publish | Sign-in now redirects to the proxy address. The identity provider does not know that address | Register the callback that the [Settings page](../40-administration/14-configure-the-registry.md#publish-through-the-opentacit-proxy) shows with your provider |
| You published, but the address says "not connected" | The registry cannot reach the proxy, or the proxy is down | Settings shows the reason below the switch. The registry does new attempts automatically. You do not have to do anything for a short outage |
| Members cannot reach the registry after you publish | They joined on the old address, and that address no longer answers | Each member runs this once: `tacit connect --registry <public address> --key <their key>`. New invites contain the new address |
| Container registry: where is the claim code? | The first-run code shows on the container console, not in a file | Run `docker logs <container>`. Then open the mapped port and claim |
| Container registry: settings or data are gone after you recreate the container | The container ran without a `/data` volume | Always attach a volume (`-v tacit-data:/data`). The volume is the registry: it holds the settings, the store, and the techniques |

## Where the logs are

| Log | Location |
|---|---|
| Registry service | `journalctl --user -u tacit-registry.service` (Linux) or `~/Library/Logs/tacit-registry.log` (macOS) |
| Local agent spawns | `~/.tacit-hooks.log` |

## An idle agent is normal

The local agent runs on demand. It stops after 15 minutes without activity.
If `tacit doctor` reports that the agent is stopped, this is information,
not a fault. The agent starts again with your next session.

## Update OpenTacit

```bash
tacit upgrade
```

`tacit upgrade` does these steps:

1. It makes sure that the release checksum is correct before it changes
   anything.
2. It replaces the binary in one atomic step.
3. It refreshes your harness wiring.
4. It restarts the registry service, if this machine runs one.

Add `--version vX.Y.Z` to get a specific release.
