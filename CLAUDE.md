1. Never use a metaphor, simile or other figure of speech which you are used to seeing in print.
2. Never use a long word where a short one will do.
3. If it is possible to cut a word out, always cut it out.
4. Never use the passive where you can use the active.
5. Never use a foreign phrase, a scientific word or a jargon word if you can think of an everyday English equivalent.
6. Break any of these rules sooner than say anything outright barbarous.
7. don't build a straw man to knock down. use not X, it's Y once per piece, max
8. two examples are enough. don't stretch to three
9. don't announce what you're about to say. say it
10. don't end two paragraphs in a row with punchlines
11. vary the length and shape of neighboring sentences
12. break any of these rules sooner than write like a machine
Review every prose output against these rules before delivering.

# Interface

The house style is the live-data wall, chosen on 2026-07-25 over three other
whole-interface directions that were built and reviewed beside it. Treat it as
settled and match it. The failure here is not blandness — it is the ninth
stat-tile row and the second palette. Reuse is the discipline.

Read the header comment of `internal/ui/assets/app.css` before changing
anything visual. It is the single source of truth for how the registry looks, and
the dashboard, sign-in page, setup wizard and MCP apps all serve from it.

## The look: a live-data wall

Flat plates on a near-black ground, each panel a readout. The light theme is the
same board under room light. One rule holds it together:

**The chrome is nearly monochrome. The colour is the data.**

1. Panels are flat plates: one hairline border, one fill, square corners, a 14px
   gap between them. No shadow, no gradient fill, no glow, no bracket. Round only
   what is genuinely round — a dot, an avatar.
2. The figure is the hero. 40px at weight 250 in `--figure` steel, several times
   the text around it. It is loud through scale, never through colour.
3. Headings are quiet. Title Case, weight 600, reading size, no tracking, no
   uppercase. A panel title is a caption, and it needs no eyebrow above it.
4. The spectrum is rationed: the page's single most important figure, and the
   2px hairline over its first panel. Anywhere else the ramp is a lie about
   importance.
5. Mona Sans is the interface — 200 to 900 in one file, and the range is the
   point. Plex Mono is machine text only: ids, commands, keys, diffs, the event
   log. It is not the interface voice.

## Colour is data

Every colour resolves from a token in the `:root` block; a hex literal in a
component is a bug. Charts carry a series key and never a colour, so both themes
follow for free and one edit to the token block moves every chart. The keys are
semantic and fixed, never repainted by rank: `s1` shown, `s2` adopted, `s4`
helped, `s6` dismissed, and `o1`→`o3` for funnel depth. Status colour never
travels alone — an icon or a label goes with it.

## Motion is functional

120–150ms on hover, focus and disclosure, plus a progress bar when real work is
running. No page-load choreography, no staggered reveals, no scroll-triggered
anything. `prefers-reduced-motion` and `forced-colors` branches must survive —
the bar bloom is light, and light does not survive a two-colour palette.

## Reuse before you add

One page per question, one name per concept, one component per pattern. Tiles
come from `TileRow`/`Tile`, tables from `dataTable`, filters from the one
URL-state `filterBar`, the period from `windowSelect`, charts from `viz.go`. A
view that builds its own markup in the browser still calls the shell's
`tacitSortableTable` and `tacitWireViz` rather than writing a second sort or a
second tooltip. Four destinations answer four questions — Outcomes, Playbook,
Review and You — and everything else is a drill-down or an admin setting. The
first three are the organization's; You is the member's own machine, and it is
the only view this registry cannot collect. Its own Outcomes view asks of one
member what the first destination asks of everybody, which is why they share a
word and are told apart by where they sit.

Charts are server-rendered inline SVG against the tokens. The plot itself is
text-free and stretches; every label sits outside it as HTML placed by
percentage, because a non-uniform stretch smears SVG text. Hover targets span
the whole bucket, never the painted pixels, and a tooltip only ever repeats a
value that also exists as a tick, a label or a table row.

## State lives in the URL

Filters, window, grouping and view are query state, so every view is bookmarkable
and survives a reload with no localStorage and no redirect flash. Find and
Filters stay separate: Find hides rows already on screen, Filters scope the data.
Disclosures are native `<details>` and the period select works with JavaScript
off — script only moves it onto the breadcrumb line. A moved URL 301s with its
query intact, and wire names, routes, CSS classes and MCP tool names do not
churn.

## Say only what is true

No invented numbers and no fake zeros. Every rate shows its n, a cold start says
"no measured outcomes yet", and an empty window names what would fill it and
offers the next move. A panel with nothing to say says so rather than drawing an
empty grid. Mark the exception and stay silent on the rule — badge the org-scoped
technique, not the general one — and let a column earn its width before it takes any.
Never show per-person data: aggregate, never individual, is the trust
precondition of the product, not a UI preference.

Language: playbook (singular, the org's); technique (countable, defined once on
a page and then used plainly); and shown → adopted → helped for the funnel.

## Every surface has to survive

Light and dark. A phone — fold low-value columns away rather than squeezing them.
A keyboard and a screen reader. And an isolated network: nothing is fetched at
runtime, so no CDN, no icon font, no framework.

## Look at what you changed

Run it and screenshot it before claiming it works — a scratch registry with
`HOME` overridden bypasses the sign-in wall, and `tacit demo load --registry`
fills it with a month of data. Rendering bugs (a double-escaped entity, a chart
stretched to its viewBox) are invisible in a diff and obvious on screen.
