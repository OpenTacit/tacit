# guideshots — recapture the user-guide screenshots

The images in `docs/user-guide/images/*.png` are real captures of a scratch
registry loaded with the shipped `software-vendor` demo dataset. The dataset is
seeded, so a recapture reproduces its own counts; the totals on the page also
count the techniques in `techniques/`, which the recipe serves, so they climb as
that directory grows.

## Recipe

1. Start a scratch registry that cannot inherit your real config, with the
   env-path display pinned to the tilde form the wizard screenshot shows:

   ```bash
   rm -rf './~'                     # see "The tilde is a real directory" below
   SCRATCH=$(mktemp -d)
   HOME=$SCRATCH TACIT_REGISTRY_ENV='~/.config/tacit/registry.env' \
     tacit serve -port 9098 -data $SCRATCH/data \
     -docs "$PWD/docs" -techniques "$PWD/techniques" &
   ```

2. `node shots.js setup` — captures `setup-wizard(-dark).png` and claims the
   registry with the code printed on the serve console (pass it as
   `CLAIM=XXXX-XXXX`).

3. **Move the claim's settings to `$SCRATCH`, minus the sign-in pair, and
   restart without `TACIT_REGISTRY_ENV`.** The claim writes the API key the
   registry needs to consider itself configured, so the file has to travel;
   `TACIT_AUTH_MODE` and `TACIT_OWNER_SECRET` are what would make every
   remaining page render the sign-in front door, so they stay behind. Drop
   either half and the capture fails — without the key, `/v1/health` answers
   503 and `demo load` refuses; with the secret, the pages ask you to sign in.

   ```bash
   mkdir -p $SCRATCH/.config/tacit
   grep -v -E '^TACIT_(AUTH_MODE|OWNER_SECRET)=' './~/.config/tacit/registry.env' \
     > $SCRATCH/.config/tacit/registry.env
   chmod 600 $SCRATCH/.config/tacit/registry.env
   rm -rf './~'
   fuser -k 9098/tcp
   HOME=$SCRATCH tacit serve -port 9098 -data $SCRATCH/data \
     -docs "$PWD/docs" -techniques "$PWD/techniques" &
   ```

4. Load the demo data with that key:

   ```bash
   tacit demo load -registry http://127.0.0.1:9098 \
     -key $(grep '^TACIT_API_KEY=' $SCRATCH/.config/tacit/registry.env | cut -d= -f2)
   ```

5. `node shots.js` — captures the remaining pages in light and dark at
   2x device-scale, matching the committed dimensions.

6. Copy `out/*.png` over `docs/user-guide/images/`, then copy the three the
   project page shares — `outcomes`, `review-queue`, `technique-map`, both
   schemes — on to `internal/ui/assets/shots/`. They are the same bytes, and
   `TestSiteShotsAreTheGuidesOwnScreenshots` fails if they are not. Kill the
   registry by port (`fuser -k 9098/tcp` — never pkill by name).

Needs `npm install playwright` (any recent version; the script uses only
stable APIs). This directory has no `package.json`, so npm walks up and tries
to install into the repo root. Install somewhere outside the repo instead, copy
`shots.js` there, and run it from that directory.

## The tilde is a real directory

`TACIT_REGISTRY_ENV='~/.config/tacit/registry.env'` is a literal string. The
shell does not expand a tilde inside quotes and Go does not expand one at all,
so the registry reads and WRITES that path relative to the working directory:
the claim in step 2 creates `./~/.config/tacit/registry.env` in the repo. It is
gitignored (`/~/`), so it can go unnoticed. If it remains, the file carries
`TACIT_AUTH_MODE` and
`TACIT_OWNER_SECRET`, so a later run with the tilde still set comes up demanding
a sign-in whose link nobody has. The symptom is `node shots.js` timing out on
`locator('header.top')`, because what rendered was the sign-in page and it has
no top bar.

Step 1 removes it and step 3 moves what it holds into `$SCRATCH` before
deleting it. Without both steps, the recipe works only on the first run in a
checkout.

## The name in the captures comes from a setting

The registry reads its product name from `internal/product`, so **the images use
the value of `PRODUCT_NAME` at capture time**. Unset the variable to capture the
compiled-in default. Tests cannot detect a local name in an image.

This is also the one surface a rename cannot reach. `product.Rename` rewrites
prose on the way out, which covers the guide's words; it cannot rewrite a PNG.
Renaming the product therefore means recapturing, and this file is the recipe.

**Keep lowercase `tacit` in screenshots.** Product renames do not change the
binary, CLI verbs, `TACIT_*` variables, or `X-Tacit-*` headers. A capture may
therefore show `curl … | sh` and `tacit init` under a different wordmark.

## The four SVGs are not captures

`hero.svg`, `hero-dark.svg`, `sitemap.svg` and `sitemap-dark.svg` in the same
directory have the name drawn into them as `<text>`, and `shots.js` never
touches them. The sitemap pair also draws every route and menu label, so a
renamed section or a reordered menu is a hand edit there too — the two files
differ only in their colours, so the same edit applies to both. After a rename they have to be edited by hand, and the hero pair
needs its canvas remeasured. The lockup contains the 192px mark, a 36px gap, and
the wordmark inside equal margins, so the `width` and `viewBox` depend on the
word's width. Measure it with `getComputedTextLength()` in a headless page using
the same font stack, size, and letter spacing.
