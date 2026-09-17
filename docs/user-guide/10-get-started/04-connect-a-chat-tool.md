# Connect a chat tool

OpenTacit is not only for coding harnesses. Each AI tool that can add a *remote
MCP connector* can reach your organization's playbook. This includes plain
chat with Claude and ChatGPT. You add one URL and sign in with your normal
org account. Then the chat model can pull from OpenTacit in the conversation.

This option needs no binary, API key, or local installation. Use it where the
full installation cannot run, such as Claude on the web or on phones, or your
organization's standard chat app.

## What a connector gives you

A chat tool connector gives you the **pull** half of OpenTacit. This is the same
half that you reach with `@tacit` and the `/tacit:` commands in a coding
session:

| Tool the chat model can call | What it does |
|---|---|
| `tacit_search` | Finds the techniques that are the best match for a query or for the current work |
| `tacit_metrics` | Shows how well the playbook performs. Its `view` parameter selects the funnel, the cohort breakdown, or the playbook map. On clients that support it, the result appears as an interactive panel |
| `tacit_drafts` / `tacit_draft_action` | Review and decide drafts, if your key has that permission |

A connector does **not** give you the **push** features: unrequested ◆
suggestions during a task and automatic evidence capture. These features need
the lifecycle hooks that a coding harness installs. A chat app does not expose
these hooks. In a chat tool, OpenTacit responds only when the
model asks. The only evidence that OpenTacit records is explicit: a tool call, or
feedback that you give in words. If you want OpenTacit to observe your work and
give suggestions without a request, wire a coding harness with
[`tacit connect`](03-join-your-organization.md) instead.

## What your registry needs first

Chat connectors sign you in; they do not carry a member key. Thus your
organization's registry must have **sign-in (OIDC)** turned on. A registry
that uses key-only auth cannot accept a consumer chat connector. In that
case, connect a coding harness with your member key. Or ask your
administrator to
[configure sign-in](../40-administration/14-configure-the-registry.md).

You need one item: the registry's MCP address. This is the base URL with
`/mcp` at the end.

```
https://tacit.example.com/mcp
```

If your organization serves OpenTacit under a sub-path, keep the whole path:
`https://demo.example.com/apps/tacit/mcp`.

## Connect Claude (web, desktop, mobile)

1. Open **Settings → Connectors**. Select the option to add a custom
   connector.
2. Paste your registry's `/mcp` URL.
3. Sign in when Claude sends you to your organization's login. Use the same
   account that you use for the dashboard.

OpenTacit's tools are now available in each conversation. Ask in plain words, for
example "search OpenTacit for deploying a lambda behind an api gateway" or "check
this against our playbook". The model then calls the tool that matches. On
Claude's web and desktop clients, the richer results (the drafts queue, the
playbook map) appear as interactive panels. On other clients, they come back
as text.

## Connect ChatGPT

ChatGPT reaches OpenTacit through its **Connectors** surface. On some plans, it
is necessary to turn on developer/MCP support first.

1. Add a connector and give it your registry's `/mcp` URL.
2. Complete the sign-in when ChatGPT asks.

Then ask ChatGPT to consult OpenTacit the same way. Results come back as text.

> Menu names on both products change from release to release. If you cannot
> find "Connectors," look for "Custom connectors," "MCP servers," or a
> developer/beta settings area. The item that you add is always a remote MCP
> server URL with a sign-in.

## Ask the model to search by default

A chat tool cannot push suggestions. In a **Claude Project** or a **Custom
GPT**, you can add an instruction that the model reads on each turn:

> Before you propose an approach, search our OpenTacit playbook for a validated
> move. Follow the move if the evidence supports it.

The model then decides when to search OpenTacit. This can make playbook searches
the default, though it does not work as often as a lifecycle hook.

## Make sure that it works

Ask the tool to run a search that you know has an answer: "search OpenTacit for
&lt;something your playbook covers&gt;." A good result confirms the URL, the
sign-in, and retrieval, end to end. If the model says that it has no OpenTacit
tool, check the URL again. Make sure that the URL contains the sub-path, if
there is one, and `/mcp` at the end. Also make sure that you completed the
sign-in.

## What stays private

OpenTacit uses the text of your query to find techniques and does not store the
text. The
registry reports everything by cohort, never by person. See [How OpenTacit
behaves in your
harness](../20-sessions/03-how-tacit-behaves-in-your-harness.md#what-stays-private)
for the full description. Your cohort comes from the account that you use to
sign in. Thus reports go to the correct team, and you do not set anything.
