# Read your own usage

**You** is the fourth destination in the top bar, and the only personal one.
It shows how you work with your AI tools, what that costs, and the results.
The other three destinations cover your organization's playbook.

Outcomes in the top bar shows organization-wide measures. You / Outcomes
shows the same measures for your own use.

The registry cannot collect this view. Every figure on it is read from your
own machines, and the aggregate-never-individual rule that governs the rest
of the dashboard is the reason: no one, including an administrator, can open
your numbers from here.

It opens on **Now**. The last breadcrumb is a menu with the other three
views. The period control at the top right sets the window for all of them.

## Now — the state

This view shows:

- **Allowance used** — how much of your five-hour window is gone, and when it
  resets. Click it for the climb, the projection of when it runs out at the
  current rate, and your weekly and spend windows where your provider reports
  them.
- **Open right now** — sessions running on this machine.
- The period's sessions, turns, tool calls, retries, and corrections.

## Work — mechanics and friction

This view shows turn times, errors, and successful tool calls. **Work
schedule** shows which days and hours you worked and the length of each
sitting. The days are UTC; the hours use your machine's clock, and the page
states both.

Tool calls open their own page: which tools, which programs inside a shell
command, which subagents and skills.

## Cost — money and tokens

This view shows cost, input and output tokens, the prompt-cache split, and
where work is handed off.

Two kinds of money appear here and they never add together. Where your client
reports what a session cost, that figure is a measurement. Where it does not,
the page estimates the cost from published token rates and gives the date of
those rates. It omits models that have no published rate.

A cache read costs a fraction of fresh input, so the cache split can show
where restarting sessions increases cost.

## Outcomes — what came of it

This view shows the shown → adopted → helped funnel for the techniques you
were offered. It uses the same layout as the organization's Outcomes view and
places the registry's rate beside yours at each step. For example, an 88%
adoption rate has different context when the organization rate is 68% than
when it is 90%. Below the funnel are a per-technique breakdown and the
techniques you stopped using.

**Was it checked** shows how many sessions with changes ran a test, build,
linter, or type check, and what the last check reported. It reads the command
and result during the session and keeps neither.

The panel breaks that down by model *and* client together, with cost, tokens,
tool calls, working time and turns per changed session on the same rows. The
page names both because the client controls the tools, prompts, and stop
conditions, so the same model can yield different results in two clients.

A passing check means that a check ran, its output indicated a pass, and no
edit followed it. It does not mean the work met your request because this view
does not know the request. When an edit follows the last pass, the session
reads **stale**. When a check ran but did not report a result, it reads
**unknown**.

The view has four limits:

- It shows no confidence band. Your sessions are self-selected and their tasks
  change from one to the next, so an interval would assume trials nobody ran.
- It ranks nothing. Rows are ordered by how much evidence each rests on, which
  is not the same as how well each did.
- Under 30 changed sessions, a row is marked thin and shows counts instead of
  percentages. OpenTacit does not pool thin session types.
- It never guesses why something failed. A failed tool call is an operational
  fact and stays one; missed requirements, regressions and wrong files are
  verdicts that need a verifier this has not got.

A session joins the panel when an edit tool runs or when your status line
reports changed lines. OpenTacit may miss a session that edits only through the
shell, so the panel lists its data sources. It does not count a missed session
as unchecked.

**Code still present** shows how many lines written by your agents remain
in the branch. The page cannot measure this on its own because it stores project
names but not paths. Run this command in the repository:

```
tacit usage --landed --publish
```

Run it inside a repository. It reads that directory and nothing else, writes
nothing to it, and files the result for this page. The result does not refresh
on its own. It includes the date when you ran the command and leaves the page
after three months.

## Where the numbers come from

Your machine keeps two files with mode 0600 and uploads neither. Full session
records remain for 30 days in `sessions.jsonl`; after that they become one row
per day per client, model, project and kind of session in `session-days.jsonl`,
which is kept for 400 days. Neither holds a prompt, a completion, a command, a
path, file contents, a transcript or an identity. Everything in them is a
count, a duration, a tool name, a project basename or a model label, and the
session key is hashed.

Check evidence adds four counted facts to a session record and nothing else:
whether a change was observed, how many recognised checks ran, how many passed,
and the one word the latest check left it on. The command and result are
matched while the hook event is handled and then dropped. None of this reaches
the registry, the organization's Outcomes, an export, or a federated peer.
Unlike other dashboard data, it has no organization-wide aggregate.

Sessions recorded before OpenTacit added these counts have no check data. An older
window may therefore show no evidence; OpenTacit does not count those sessions as
unchecked.

Cost, lines written and cache figures come from your client's status line.
`tacit init` offers to wire it. Every panel says how many sessions in the
window reported these figures and which clients reported any data. This
distinguishes a quiet week from a client that has not been set up.

If the registry runs on a different machine from the one you work on, your
machines seal their usage and publish it here, and only your key opens it.
Run `tacit usage --key` on a machine you work on and paste what it prints.
The key stays in your browser and is never sent to the registry, which is
what lets the registry hold your numbers without being able to read them.

For the same figures as text, run `tacit usage`. See the
[command reference](../50-reference/16-command-reference.md).
