# Share techniques between organizations

Federation lets organizations share validated techniques. One registry
publishes selected techniques as a signed feed, and another subscribes. The
subscriber imports the techniques as drafts or live techniques and measures
them against its own results. Registries share evidence only as aggregate
attestations ("helped 91% · n=214"), never as events, sessions, or identities.

Open Settings from the account menu, then Federation.

## Your registry's identity

The identity strip shows your provider id, your public descriptor, and the
head of your signing key. The registry signs each published technique with
Ed25519. Subscribers can therefore verify that a feed came from you and has
not changed.

## Publish techniques

You publish each technique separately, from the page of that technique:

1. Open the technique. Find the federation panel.
2. Enter one or more channel names. Channels group techniques into feeds,
   for example `platform-practices`. Then save.
3. Send the URL of the feed to the organization that subscribes. The
   Federation page shows the URL.

The Federation page lists all published items, by channel and by
technique. Each published technique has an Unpublish action. When you
unpublish a technique, the registry sends a retraction. Subscribers see
the retraction at their next poll.

You can also export a channel as static files and serve them from a
location of your choice. See `tacit feed export` in the
[command reference](../50-reference/16-command-reference.md).

## Subscribe to a feed

1. In the "Add a feed" form, enter the feed URL and a name.
2. Select a trust level. The **review** level imports new techniques as
   drafts for your [Review queue](../30-dashboard/11-review-drafts.md).
   The **auto-accept** level puts them into service immediately. Start
   with review.
3. As an option, set a prefix. The prefix makes a namespace for the ids of
   the imported techniques. If the feed needs an auth token, enter the
   token.
4. Click Subscribe.

The registry polls the subscribed feeds automatically. Click **Poll now**
to poll immediately. The row of each subscription shows its status, the
number of imported techniques, the time of the last poll, and pending
retractions from the provider. Unsubscribe stops the subscription. Imported
techniques remain in your registry, where you can keep or retire them.

## Judge imported techniques by your own results

The detail page of a feed shows the attested helped rate of the provider
adjacent to the local rate of the technique. This comparison shows if the
experience of the provider applies to your organization. Local evidence
always controls local ranks. The rank of an imported technique increases
or decreases with the results for *your* members. The attestations of the
provider are context for display only.
