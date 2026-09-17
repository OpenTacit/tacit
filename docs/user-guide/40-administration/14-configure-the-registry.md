# Configure the registry

The registry reads its configuration from `~/.config/tacit/registry.env`.
This file is a plain `KEY=VALUE` file. Environment variables override the
file. The command `tacit init` or the first-run setup form writes most of
the configuration for you. This chapter describes the settings that you
can change later.

## The Settings page

Open Settings from the account menu. It lands on General, where each member
that signed in sees the effective configuration: the service address, the
storage backend, the sign-in status, the embedder, and the other settings.
The page masks the secrets.

Administrators also get controls, on the settings that apply without a
restart:

- **Global Access**, and the address in force: the one the shared proxy gives
  this registry, or your own external URL. See
  [Publish through the OpenTacit proxy](#publish-through-the-opentacit-proxy)
- The administrator emails, one address to a row: type in the blank row at the
  end to add someone, and use the **✕** beside a row to remove them. Both take
  effect when you save. Your own ✕ is dead: nobody removes themselves here,
  whoever else is named. Another administrator can remove you, or edit
  `TACIT_ADMIN_EMAILS` in `registry.env` on the registry's console
- The four automation switches, under their labels on the page: **Apply proven
  techniques silently** ([evidence-gated autonomy](#evidence-gated-autonomy)),
  **Evaluate new machine techniques without review**, **Serve what passes
  evaluation without review**, and **Discover techniques from usage**
- The model API key
- The model selection for the registry's own features: the provider, the
  base URL, and the suggest / tag-merge models (see
  [Use a different model provider](#use-a-different-model-provider))

Some settings are deliberately not on this page, because they are decided
once and then left alone. The proxy address, the federation provider name
and id, the Public feed evidence floor, and the evidence thresholds the
automation switches run on are `registry.env` and a restart; the first-run
form asks for the provider name and id.

Global Access also returns outcome evidence for techniques this registry
imported from others. This once had its own switch. Imported outcome reports
now cite their source feed, and local evidence always takes precedence over an
imported claim.

The two model fields are combo boxes. You can type free text. The fields
also propose the current catalog of the selected provider. The registry
gets the catalog live. The name and the description of the selected model
appear below the field when the provider supplies them. OpenRouter gives a
full description. Anthropic gives a display name. A plain OpenAI endpoint
gives only the id. Click **Save and apply**. The settings apply
immediately, because the registry reads them for each call. For all other
settings (ports, storage, sign-in), edit `registry.env`. Then restart the
registry:

```bash
systemctl --user restart tacit-registry.service
```

## In a container

The official image uses the same configuration model, with two
differences. First, the file `registry.env` is at `/data/registry.env`,
inside the volume. Thus the data that the setup form and the Settings page
write stays safe when you replace the container. Second, environment
variables override the file. The environment of the container can set
values, and the image itself sets some values, for example
`TACIT_EMBED_MODEL`, which locks semantic retrieval to the included model.
You can change these values only in the environment of the container
(`docker run -e …` or compose `environment:`). Then make the container
again. The Settings page cannot change these values. The restart command
is `docker restart <container>`. Start from
[deploy/docker-compose.yml](https://github.com/opentacit/tacit/blob/main/deploy/docker-compose.yml).
Then all these settings stay in one file that you can review.

## The settings that matter most

| Setting | What it does |
|---|---|
| `TACIT_API_KEY` | The root API key of the organization. Mint member keys from the [Members page](12-manage-members.md). Keep the root key for administration. |
| `TACIT_EXTERNAL_URL` | The address where members connect to the registry. The registry uses it in links, invites, and sign-in redirects. |
| `TACIT_BASE_PATH` | Serves the registry under a sub-path behind a reverse proxy, for example `/apps/tacit` for `https://demo.example.com/apps/tacit`. The default is `/` (root). See [Serve under a sub-path](#serve-under-a-sub-path). |
| `TACIT_GLOBAL_ACCESS`, `TACIT_PUBLISH_INGRESS` | Connects out to a shared OpenTacit proxy, which gives the registry a public address, so you do not serve one yourself. `tacit init` sets this to `1` for a new registry that has a sign-in, has no address of its own, and can reach the proxy; set it to `0` to keep the registry local. The second setting names the proxy. While it is on, the address from the proxy replaces `TACIT_EXTERNAL_URL`. See [Publish through the OpenTacit proxy](#publish-through-the-opentacit-proxy). |
| `TACIT_GLOBAL_ACCESS_CONFIRMED` | Whether this registry contributes its strongest general techniques to the public pool. Separate from the switch above, and set only by a person ticking the box in Settings — no flag, no default and no upgrade writes it. |
| `TACIT_OIDC_ISSUER`, `TACIT_OIDC_CLIENT_ID`, `TACIT_OIDC_CLIENT_SECRET`, `TACIT_OIDC_REDIRECT_URI` | Sign-in with your identity provider. Set all four settings or none. See [Turn on sign-in](#turn-on-sign-in). |
| `TACIT_ADMIN_EMAILS` | The persons that can do administrative actions in the dashboard. An empty value lets each member that signed in do them. This is acceptable for a pilot, not for production. |
| `TACIT_EMBED_MODEL` | The retrieval embedder. `onnx/all-MiniLM-L6-v2` gives semantic retrieval. `hashing-v1` is the lexical fallback. |
| `TACIT_DB_URL` | A database URL: `postgres://…` for Postgres, `sqlite:///path/to/tacit.db` for SQLite. An empty value selects the embedded file store. The official container image sets `sqlite:///data/tacit.db` as the default. To change it, use the container environment. The Settings page cannot change it. |
| `TACIT_AUTONOMY`, `TACIT_AUTONOMY_MIN_HELPED_RATE`, `TACIT_AUTONOMY_MIN_N` | Evidence-gated agent autonomy. With `TACIT_AUTONOMY=1`, agents in autonomous sessions can silently apply the techniques that pass the evidence limits. Defaults: off, 0.8, 20. See [Evidence-gated autonomy](#evidence-gated-autonomy). |
| `TACIT_AUTO_SHADOW`, `TACIT_AUTO_PROMOTE`, `TACIT_AUTO_DISCOVER` (+ `_MIN_FIT` 0.6, `_MIN_JUDGED` 12) | These settings automate the review decision for machine-produced techniques: evaluation without review, serving on fit evidence, and the discovery of techniques from usage. The three switches default to off. The two thresholds are not on the Settings page — `registry.env` and a restart. See [Automate the review decision](#automate-the-review-decision). |
| `TACIT_LLM_API_KEY` | The model API key. It lets the registry run model-backed features: suggestion research, cluster descriptions, and tag-merge proposals. The default model is Claude. To use a different model, see [Use a different model provider](#use-a-different-model-provider). |
| `TACIT_FEED_PROVIDER_NAME`, `TACIT_FEED_PROVIDER_ID` | How this registry names itself on the feeds it publishes. The registry pins and keeps the first real public address it receives as the id, so set this only to override that value. These settings require `registry.env` and a restart; they are not on the Settings page. |
| `TACIT_PUBLIC_MIN_N` | The sample-size floor a technique must reach before it can enter the Public channel. The default is 5. Raise it when your techniques have enough evidence. If the floor exceeds their sample sizes, the Public channel will be empty. This setting requires `registry.env` and a restart; it is not on the Settings page. |
| `TACIT_DIGEST_WEBHOOK` | A Slack incoming-webhook URL. When it is set, the registry posts a weekly digest automatically. |
| `TACIT_DEMO_DIR` | A directory of demo dataset `*.json` files (from `tacit demo generate`, or the supplied scenarios). This variable is the whole of the switch for demonstration mode, and the mode is off until you set it. Set, the dashboard shows a **Demo** menu adjacent to the avatar, and the menu changes between production data and each scenario. Unset, the registry has no Demo menu, no switch route, and no scenarios. Set it on the instance that demonstrates the product, and nowhere else. See [Demonstration mode](#demonstration-mode). |
| `PRODUCT_NAME` | What the registry calls itself on every page it serves: the wordmark, the browser title, the home-screen name, the prose, and this user guide as the registry serves it. It carries no `TACIT_` prefix because its whole purpose is to outlive the name. It renames the product and nothing else: the command line, the `TACIT_*` settings, the `X-Tacit-Key` header, every command and address printed in this guide, and the development documentation all keep their spelling. |

## Turn on sign-in

A new registry has one way in: an owner link that `tacit dashboard` mints on
the machine the registry runs on. That gate has one member and cannot have two.
Sign-in replaces it with your identity provider over OIDC — Google Workspace,
Okta, Entra ID, Auth0, Keycloak, and other providers that publish a discovery
document — so colleagues sign in with the accounts they already have.

Sign-in gates the HTML dashboard only. The `/v1` API keeps its own
authentication, the `X-Tacit-Key` header. So agents, hooks, and any
[miner](../50-reference/17-glossary.md#miner) you have pointed at the registry
continue without a change.

### Before you start: decide the address

The callback URL contains the address that members use. Set
`TACIT_EXTERNAL_URL` first, and `TACIT_BASE_PATH` if the registry shares a
hostname. The callback is that address plus `/auth/callback`:

```
https://tacit.example.com/auth/callback              # its own hostname
https://demo.example.com/apps/tacit/auth/callback    # under a sub-path
```

Use `https`. Identity providers refuse a plaintext redirect URI. Many
providers accept `http://localhost:8080/auth/callback` for a trial on your
own machine.

### Step 1: register the registry with your provider

Create an application in your identity provider. Select a **web
application**, a confidential client. The registry keeps a client secret and
exchanges the authorization code from a server.

1. Enter the callback URL from above as the authorized redirect URI. The
   provider returns members to the URIs that you register, and to no other
   address. Enter it exactly: the scheme, the host, the port, and the path,
   with no trailing slash.
2. Request the scopes `openid`, `email`, and `profile`.
3. Write down the issuer URL, the client ID, and the client secret.

The issuer is the base URL that serves the discovery document. The registry
reads the endpoints from that document. Thus you give it no individual
endpoint URLs. Confirm the issuer before you continue:

```bash
curl -s https://accounts.google.com/.well-known/openid-configuration | head
```

| Provider | Issuer |
|---|---|
| Google | `https://accounts.google.com` |
| Okta | `https://<org>.okta.com`, or `https://<org>.okta.com/oauth2/default` for a custom authorization server |
| Entra ID | `https://login.microsoftonline.com/<tenant-id>/v2.0` |
| Auth0 | `https://<tenant>.<region>.auth0.com/` |
| Keycloak | `https://<host>/realms/<realm>` |

### Step 2: give the four values to the registry

**From the dashboard.** If you signed in with the owner link and have not yet
set an identity provider, you can turn on sign-in from the Settings page. Open
**Settings → Access and sign-in**. The Sign-in row is
a two-position switch, **Personal** or **Shared**, like the Global Access
switch above it. Move it to Shared and the page shows what Shared needs: the
callback URL to register with your provider, then the issuer, the client ID and
the client secret to paste back.

The button becomes **Test configuration** while the switch is on Shared. The
test checks that the issuer serves a discovery document with the expected
name; the endpoints are secure; the provider offers the authorization code
flow and accepts a client secret in the request body; the scopes and email
claim exist; the token endpoint accepts the client ID and secret; and the
redirect URI is registered. A failed check quotes the provider's response and
names the field to change. If the test cannot check something, such as a
provider whose document lists no scopes or an error the registry does not
recognise, it reports the limit without treating it as a failure.

When every check passes the button becomes **Save changes**. That writes the
four settings together with the session secret and the cookie flag, and
restarts the registry so they take effect.

The test writes nothing, so run it as often as you like. The client secret is
never sent back to the page: after a page reload, enter it again to save.

The switch can be moved in that one state only. An open registry has no
administrator to offer it to, and a registry that already has a provider shows
the switch locked in the Shared position: changing or removing a provider is
done from the console, which stays reachable when a sign-in does not.

A shared registry needs an external address because an identity provider must
return members to a registered URI. A bind address does not meet that need. If
the registry has no external address, the page links to **Own address** and
Global Access instead of showing a callback URI.

**From the console.** Run this command on the registry machine:

```bash
tacit secure
```

The command asks for the issuer, the client ID, and the client secret. It
tests the issuer before it writes anything, it writes all four settings
together, it makes a session secret, and it records that this registry now
signs members in with a provider (`TACIT_AUTH_MODE=oidc`).

It then makes sure the registry is running as a service. It installs one
(systemd on Linux, launchd on macOS) if needed, or restarts the existing
service. A shared registry must be available when members sign in and restart
after a reboot. If you are running the registry in a terminal, the command
asks you to stop it yourself. You can then start it again or run `tacit init
--service auto`. `--no-restart` writes the settings and starts nothing.

Nothing else on this page is necessary for a standard installation. Continue
at [Step 3](#step-3-restart-then-verify).

Each answer is also a flag, for an installation with no person at the
keyboard:

```bash
tacit secure --issuer https://accounts.google.com \
  --client-id … --client-secret … --admins ops@example.com --yes
```

Add `--dry-run` to see the settings that the command would write. Use the
console when you cannot use the dashboard. An open registry has no
administrators, so its Settings page stays read-only until sign-in exists. If
sign-in is set up incorrectly, you cannot reach the dashboard. In either case,
use `tacit secure --off` to restore access.

**The manual alternative.** The first-run form has a Sign-in section, and
`registry.env` accepts the four settings directly:

```bash
TACIT_OIDC_ISSUER=https://accounts.google.com
TACIT_OIDC_CLIENT_ID=…
TACIT_OIDC_CLIENT_SECRET=…
TACIT_OIDC_REDIRECT_URI=https://tacit.example.com/auth/callback
```

**Set all four values or none.** The registry turns sign-in on only when all
four are present. With three, sign-in stays off and the dashboard stays open,
and the registry reports no error. The first-run form and `tacit secure`
refuse a partial set for this reason. A hand-edited `registry.env` does not.
`tacit doctor` reports the condition, and the Settings page shows
`enabled · <issuer>` or `no sign-in`.

These settings are optional. Each one has a default. `tacit secure` sets the
session secret and the cookie flag for you.

| Setting | What it does |
|---|---|
| `TACIT_OIDC_SCOPES` | The scopes to request. The default is `openid email profile`. `email` identifies the member and matches `TACIT_ADMIN_EMAILS`. `profile` gives the name and the avatar in the top bar. |
| `TACIT_SESSION_SECRET` | The key that signs session cookies. Give it a durable value, for example from `openssl rand -hex 32`. Without it the registry makes a new key at each start, and each restart signs out every member. The first-run form writes one for you. |
| `TACIT_SESSION_TTL_SECS` | The lifetime of a session. The default is 28800, which is 8 hours. |
| `TACIT_COOKIE_SECURE` | `1` sends the session cookie over HTTPS only. Set it whenever the registry is not on loopback. |
| `TACIT_ADMIN_EMAILS` | The persons that can do administrative actions. An empty value lets each member that signed in do them. |

### Step 3: restart, then verify

The registry reads these settings at startup. Both the Settings page's sign-in
form and `tacit secure` restart the service for you. After a manual edit,
restart it yourself:

```bash
systemctl --user restart tacit-registry.service
```

Then open the dashboard. It shows a front door with a **Sign in** button in
place of the page. **Complete one sign-in with your own account before you
tell anybody else that sign-in is on.** The top bar then shows your name, and
Settings shows the issuer beside **Sign-in**.

If that first sign-in does not succeed, nobody can open the dashboard, the
Settings page included. The console gives it back:

```bash
tacit secure --off
```

The dashboard opens again immediately. Your session secret stays, so the
sign-in that you correct next keeps the sessions that it issues.

### What sign-in changes

- **Members** sign in to see the dashboard. Their agents do not change. The
  member key continues to authenticate the `/v1` API, the hooks, and the CLI.
- **Chat tools** can now sign in to the MCP endpoint with the same provider.
  The registry brokers that flow: the chat tool authorizes against the
  registry, and the registry delegates the human login to your provider. See
  [Connect a chat tool](../10-get-started/04-connect-a-chat-tool.md). Without
  OIDC, chat tools use the member key.
- **Administrative actions** belong to `TACIT_ADMIN_EMAILS`. With no list,
  each member that signed in can do them. This is acceptable for a pilot, not
  for production.

### Keep one address for the callback

A registry commonly answers on more than one name: a tailnet name, a LAN
address, a port on the machine itself. Your provider knows exactly one
callback. A sign-in that starts on a different hostname cannot complete on the
callback host, because the browser holds the login cookie on the hostname
where the flow began. The registry prevents this failure. It sends the sign-in
to the callback host before it begins.

The registered URI itself is your responsibility. **When the public address
changes, register the new callback before the next person signs in.** This
applies when you
[publish through the OpenTacit proxy](#publish-through-the-opentacit-proxy), which
moves the callback to the proxy address, and when you move the registry to a
new hostname. The Settings page shows the exact URL to add.

A published registry has two callbacks, and it is worth registering both. The
public address supersedes `TACIT_OIDC_REDIRECT_URI` while the registry is
published, so that address is the URI that sign-in sends today. The stored
setting is the fallback, which the registry uses again the moment you stop
publishing. With both registered at your provider, the switch works in each
direction. `tacit doctor` names which of the two is live.

### When sign-in does not complete

| Symptom | Cause | Fix |
|---|---|---|
| `redirect_uri mismatch` from the provider | The URI at the provider is not the same, character for character, as `TACIT_OIDC_REDIRECT_URI` | Compare the scheme, the host, the port, the path, and the trailing slash. After you publish, register the callback that Settings shows |
| The dashboard is still open after the restart | One of the four values is missing or empty | `tacit doctor` names the condition, and `tacit secure` completes the set |
| Nobody can sign in, and the Settings page is behind the same sign-in | The registered callback, the client, or the issuer is not correct, and the dashboard is now closed to its own operator | On the console: `tacit secure --off` opens the dashboard again. Then `tacit secure` to correct the values |
| "That sign-in could not be completed" on return | The browser sent no login cookie: the attempt began on a different hostname, or it is more than 10 minutes old, or the browser refuses cookies | Start again from the address in the callback URL. The button on that page also offers a different account |
| `discovery failed for <issuer>` | The issuer does not serve `/.well-known/openid-configuration`, or the registry cannot reach it | Fetch that URL with `curl` from the registry machine. Correct `TACIT_OIDC_ISSUER`, or open the outbound path. `tacit secure` makes this test before it writes |
| Every member is signed out after each restart | `TACIT_SESSION_SECRET` is not set, so the signing key is new at each start | Set a durable secret, then restart one last time. `tacit secure` writes one |

## Use a different model provider

Model selection uses one set of `TACIT_LLM_*` keys. The keys are the same
for each provider, Anthropic included. The keys configure the registry
(suggestion research, cluster descriptions, tag-merge). They also
configure the member-side audit layer (the in-flow fit-check and `@tacit`
answers). Select a provider by name. The base URL and the default models
then fill in automatically. Almost all providers use the OpenAI
`/chat/completions` wire format. Anthropic uses its Messages API.

Known providers: `anthropic` (default), `openai`, `openrouter`, `google`
(Gemini), `groq`, `mistral`, `deepseek`, `together`, `xai` (Grok),
`fireworks`. A different OpenAI-compatible endpoint (a self-hosted
vLLM/Ollama, or a gateway) also operates. Select the most similar provider
and set `TACIT_LLM_BASE_URL`.

| Setting | What it does |
|---|---|
| `TACIT_LLM_API_KEY` | The model API key, for each provider (Anthropic included). |
| `TACIT_LLM_PROVIDER` | The provider name from the list above (default `anthropic`). It selects the wire format, the default base URL, and the default models. |
| `TACIT_LLM_BASE_URL` | Overrides the default endpoint of the provider, for a self-hosted URL or a gateway URL. Give the API root, with the version segment included (for example `https://openrouter.ai/api/v1`). |
| `TACIT_CHAR_MODEL`, `TACIT_SYNTH_MODEL` | The audit-layer models. Model ids are **provider-scoped**. An Anthropic id does not exist on OpenRouter. For example, use `anthropic/claude-haiku-4.5` or `openai/gpt-4o-mini`. |
| `TACIT_SUGGEST_MODEL`, `TACIT_TAGMERGE_MODEL` | The models for the registry-side research and the tag-merge proposals. |
| `TACIT_LLM_REFERER`, `TACIT_LLM_TITLE` | The optional OpenRouter headers for request attribution (`HTTP-Referer` / `X-Title`). |

You can edit the registry-side selection (the key, the provider, the base
URL, and the suggest / tag-merge models) from the
[Settings page](#the-settings-page). Changes apply immediately.
`TACIT_CHAR_MODEL` / `TACIT_SYNTH_MODEL` are the **member-side** audit
models. Each developer sets them in the developer's own agent environment.
Thus they are not on the Settings page of the registry.

An example for OpenRouter:

```bash
TACIT_LLM_PROVIDER=openai
TACIT_LLM_BASE_URL=https://openrouter.ai/api/v1
TACIT_LLM_API_KEY=sk-or-...
TACIT_SYNTH_MODEL=anthropic/claude-haiku-4.5     # or openai/gpt-4o-mini, etc.
TACIT_CHAR_MODEL=anthropic/claude-haiku-4.5
TACIT_SUGGEST_MODEL=openai/gpt-4o                 # a capable model for research
```

**Web search caveat.** Suggestion research needs live web search.
Anthropic gives it server-side. OpenRouter gives it through its `web`
plugin, which turns on automatically. A plain OpenAI-compatible base
without web search cannot run the registry-side research pass. The
registry reports this condition. The `/tacit:suggest` skill then does the
research with the web access of the agent harness. For a gateway that
proxies search, set `TACIT_SUGGEST_SEARCH=1` to turn web search on.

If you do not set `TACIT_LLM_PROVIDER`, the registry keeps the Anthropic
(Claude) default models and the `api.anthropic.com` base. You must still
give the key as `TACIT_LLM_API_KEY`.

## Serve under a sub-path

By default, the registry is the only service on its hostname. To serve it
from a sub-path such as `https://demo.example.com/apps/tacit`, set:

```bash
TACIT_BASE_PATH=/apps/tacit
TACIT_EXTERNAL_URL=https://demo.example.com/apps/tacit   # include the path
```

Point your reverse proxy at the registry. **Do not remove the prefix.**
The registry removes the prefix itself:

```nginx
location /apps/tacit/ {
    proxy_pass http://127.0.0.1:8080;   # no trailing URI: path forwarded as-is
}
```

All parts (the dashboard, the `/v1` API, the MCP endpoint, and the
federation feeds) move under the prefix. Thus members connect with the
full URL:
`tacit connect --registry https://demo.example.com/apps/tacit --key …`.

You need one more proxy rule only if you use MCP OAuth sign-in. OAuth
discovery (RFC 8414/9728) is at the *origin root*, with the sub-path after
the well-known segment. Also send these paths to the registry:

```nginx
location /.well-known/oauth-protected-resource/apps/tacit { proxy_pass http://127.0.0.1:8080; }
location /.well-known/oauth-authorization-server/apps/tacit { proxy_pass http://127.0.0.1:8080; }
```

If you set an OIDC redirect URI, it also contains the prefix:
`https://demo.example.com/apps/tacit/auth/callback`.

## Publish through the OpenTacit proxy

Sometimes the registry cannot have its own public hostname. Examples: it
runs on a laptop, or the network has no open inbound port. Then the
registry can connect out to a shared OpenTacit proxy, and the proxy gives it a
public address. You need no domain, no certificate, and no inbound port.
You run no extra program. The tunnel is part of `tacit serve`.

Set the function to on in **Settings** (administrators only):

1. Select **Global Access**.
2. Save.

The change applies immediately, without a restart. The registry dials the
shared OpenTacit proxy. To use a different one, name it in `registry.env` and
restart — it is not a Settings field:

```bash
TACIT_GLOBAL_ACCESS=1
TACIT_PUBLISH_INGRESS=ingress.example.com   # the proxy hostname you were given
```

The page then shows the address that you got from the proxy:

```
https://slate-orchard.example.com   connected
```

The address stays yours. It belongs to the **instance key** of this
registry. The command `tacit init` made this key in `~/.config/tacit/`.
The key never leaves the machine. Thus restarts and new connections give
the same name. Know these two effects before you depend on the address:

- **Make a backup of the config directory.** If you lose the key, the
  registry enrols again under a *new* name. Then no saved link operates.
- **When you copy the key, you move the registry.** Use this procedure to
  restore the registry on new hardware.

### What it changes for you

The proxy address becomes the public address of the registry. It replaces
`TACIT_EXTERNAL_URL` while the publish function is on. Join links, share
URLs, MCP addresses, the health endpoint, and sign-in redirects all use
it. The registry keeps your configured external URL. It does not
overwrite it. When you set the publish function to off, that URL becomes
active again.

**If you use OIDC sign-in, register the new callback before the next
person signs in.** Sign-in now redirects to
`<your public address>/auth/callback`. An identity provider returns
members only to the callbacks that you registered with it. The Settings
page shows the exact URL to add. If you do not register the callback, the
next member that signs in gets a `redirect_uri mismatch` error. This error
gives no explanation.

There is one exception. A **plaintext** proxy address never becomes the
sign-in callback, because providers refuse `http` redirect URIs. Links and
share URLs use the proxy. Sign-in continues to use your configured
callback. The Settings page shows a note when this condition applies.

### What it changes for members

Members usually must do nothing. The command `tacit invite` mints join
links on the new address. The registry shows the address at `/v1/health`.
Thus each client that asks the registry for its address gets the public
address.

Members that joined *before* the publication continue to use the old
address. If that address continues to operate (for example, for a
colleague on the same network), no change is necessary. If it does not,
the member points the tools at the new address one time:

```bash
tacit connect --registry https://slate-orchard.example.com --key <their key>
```

The same rule applies to chat tools. The MCP address is the public address
plus `/mcp` at the end. See
[Connect a chat tool](../10-get-started/04-connect-a-chat-tool.md).

### Before you turn it on

**Do not use the OpenTacit proxy if the data of your organization must stay
hidden from a third party. The TLS connection ends at the proxy, and
requests get to the proxy without encryption.** The proxy sees the envelope of all traffic that
it sends on: the requester, the path, and the result. If this is not
acceptable, keep the function off and use
[your own proxy](#serve-under-a-sub-path). The other parts of the registry
stay the same in both cases.

**Your registry also reports four numbers about itself.** The numbers are
the counts of member machines that it saw in the last day, week, month,
and year. The [Members page](12-manage-members.md) counts the same numbers
for you. The operator of the proxy must know the value of each registry
that the proxy carries. These four counts are the smallest figures that
give that answer.

The report names no member. It contains no data that identifies one member
from a different member. The registry sends only the four counts. It sends
them every ten minutes while the tunnel is open. It sends nothing after
you set the publish function to off. The proxy keeps one sample each day.
Thus its operator can see if a registry becomes larger. The first day that
the proxy can know data is the day when you set the publish function to
on. The registry sends no data about the past, and no procedure can
recover that data.

## Evidence-gated autonomy

By default, each suggestion is visible. A member sees the technique and
decides. OpenTacit measures the result. In autonomous sessions (CI runs,
scheduled agents, headless invocations), no person monitors the delivery.
OpenTacit tags these sessions with the `consumer:agent` cohort dimension.
OpenTacit finds them from CI markers. You can also set
`TACIT_CONSUMER=agent`/`human` in the environment of the session.

The setting `TACIT_AUTONOMY=1` adds a graduation rule. A **stable**
technique qualifies when its measured record passes the limits: a helped
rate of `TACIT_AUTONOMY_MIN_HELPED_RATE` minimum (default 0.8) across
`TACIT_AUTONOMY_MIN_N` adoptions minimum (default 20). Agents in
autonomous sessions can then **apply the technique silently**. The agent
gets the technique as context for its work. The session shows no
suggestion. Techniques below the limits stay visible suggestions in all
sessions. Interactive sessions never change. The switch is on the
[Settings page](#the-settings-page), in the Automation section, and applies
immediately, because retrieval reads it for each request. The two limits are
`registry.env` and a restart.

The ledger records all activity. Each delivery record shows if the
delivery was `suggested` or `applied`. The Outcomes page gets an
**Autonomy** panel. The panel shows the techniques that qualify and the
count of silent applications. It also compares the helped rate of applied
deliveries with the rate of suggested deliveries. The outcomes page of each technique shows its own status (for
example, "needs 12 more measured adoptions"). Drafts never qualify. When
you return a technique to draft, it immediately loses its qualification.
When a technique decays, the same occurs.

## Automate the review decision

By default, a person promotes techniques. The system can use fit evidence to
automate part of that decision. Three switches make the decision automatic for
machine-produced techniques, and two thresholds shape it. All three
switches are off by default, so the operator must select this function
intentionally. The switches are on the Settings page; the thresholds are
in `registry.env`. Change the thresholds there and restart the registry.

| Setting | Default | What it does |
|---|---|---|
| `TACIT_AUTO_SHADOW` | off | Sends screened techniques with machine provenance (mined, suggested, observed) into the **under evaluation** lane, not into the review queue. The lane measures relevance at zero exposure. |
| `TACIT_AUTO_PROMOTE` | off | Serves a technique that is under evaluation — promoting it to **stable** — when its fit evidence passes the limits. |
| `TACIT_AUTO_PROMOTE_MIN_FIT` | 0.6 | The fit rate under evaluation that a technique must pass before it is served. |
| `TACIT_AUTO_PROMOTE_MIN_JUDGED` | 12 | The minimum count of fit-check verdicts behind that rate. |
| `TACIT_AUTO_DISCOVER` | off | Runs the discovery pass for observed techniques on the recompute cycle. The pass collects the "worked moves" of members into clusters and makes candidate techniques. |

Retirement from evaluation is always automatic. A technique under evaluation
with many verdicts and a fit rate that is too low retires without a decision.
The thresholds are part of the compiled program. You cannot tune them
with environment variables. A technique that a member *contributed* still
needs the approval of a person
before its first exposure. Monitor this in the under-evaluation section of
the Review page and in the
[Events feed](../30-dashboard/09-explore-outcomes.md#the-activity-feed).

## Keep retrieval semantic

Semantic retrieval finds techniques by meaning. It needs the ONNX
embedder. The command `tacit init` prepares the embedder. If the registry
cannot load the model, it continues to serve, but it changes to lexical
matches. Both `tacit doctor` and the health endpoint report this
condition. If you build from source, build with `-tags onnx`. A plain
build does not have the semantic embedder, and it gives no warning.

## Move to a database

The embedded file store is the default. It needs no preparation. When you
need more capacity, two databases are available. The same idempotent
migration applies to both:

```bash
tacit migrate-store --db sqlite:///var/lib/tacit/tacit.db   # single machine, no server
tacit migrate-store --db postgres://…                       # shared / multi-instance
```

**SQLite** supports more events on a single machine. It uses one database
file and requires no other software. It holds event volumes that are too
large for the file store. **Postgres** lets several instances share one
registry. The migration makes sure that its counts are correct. Add
`--dry-run` to see the planned actions first. Then set `TACIT_DB_URL` to
the same URL and restart.

## Send a weekly digest

The digest is a compact summary of the week for the persons that do not
open dashboards. It shows the adopted techniques, the techniques that
helped, and the items that wait for review.

- **Automatically:** set `TACIT_DIGEST_WEBHOOK` to a Slack
  incoming-webhook URL. The registry then posts each week.
- **On demand:** the command `tacit digest --window 7d` writes the digest
  as markdown. The option `--slack <webhook-url>` posts it one time.

## Demonstration mode

A registry can show OpenTacit with one month of realistic usage. Your real
data does not change. The mode belongs to an instance whose job is to
demonstrate the product, and it stays off until you point that registry at
a directory of demo datasets:

```bash
TACIT_DEMO_DIR=/path/to/demo-datasets
```

That variable is the whole of the switch. Leave it unset, as an ordinary
registry does, and there is no Demo menu in the top bar and no
`POST /demo/switch` route behind it. The `tacit_demo` cookie then reaches a
registry that reads nothing into it, and the page it gets back is the page
any other browser gets.

Each `*.json` dataset in the directory becomes an entry in a **Demo**
menu. The command
[`tacit demo generate`](../50-reference/16-command-reference.md) writes
the datasets, with one file for each scenario. The menu appears in the top
bar adjacent to the avatar. The menu changes the browser between
**Production data** and a scenario. The active scenario gives the Demo
button an accent color to distinguish demo data from real data.

The registry serves each scenario from its own isolated store under
`TACIT_DATA/demo/<scenario>`. The registry fills the store in the
background at startup. A scenario shows "loading…" until its month of
usage is complete. The initial month of a dataset is not important. At
startup, the registry moves the window of the dataset to end yesterday.
Thus the dashboards always show a current month. The selection applies to
one browser only. Agents, hooks, MCP clients, and the `/v1` API always see
production data. The registry builds a scenario fully again in these
conditions: you change the dataset file, or you restart on a later day,
which moves the window. Production data never changes.

## Watch learning readiness

Learning readiness is in Settings, beside General. It shows whether the evidence
corpus for the synthesis layer of OpenTacit meets its volume, history, and
completeness gates. The page is read-only. The numbers change as the registry
records more real usage.

## Keep OpenTacit up to date

To update OpenTacit, run this command:

```bash
tacit upgrade
```

The command downloads the latest verified release and replaces the binary
atomically. It refreshes the harness wiring. It restarts the registry
service if the service runs. The command `tacit doctor` reports when a
newer release is available.
