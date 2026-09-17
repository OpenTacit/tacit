# worldpath — regenerate the globe's coastlines

The Access picture's globe (`accessscene.go`, `.acc-earth` in `app.css`) is drawn
from one path, held in `internal/registry/web/worldpath.go`. This tool makes it.

## Recipe

1. Get Natural Earth's 110m land polygons as GeoJSON. Public domain, and the
   file the constant now in the tree was made from:

   ```bash
   curl -o land110.json https://raw.githubusercontent.com/martynafford/natural-earth-geojson/master/110m/physical/ne_110m_land.json
   ```

2. `go run ./hack/worldpath -in land110.json -tol 1.2 -min 4`

   It prints the path to stdout and its size to stderr. Paste the path into the
   `worldLandPath` constant — nothing else in that file changes.

3. `go test ./internal/registry/web/ -run 'World|Globe'` checks the result is
   geography in the box the globe expects, and that projecting it paints a
   plausible amount of land at every angle round the turn.

## The two numbers

`-tol` is the simplification tolerance and `-min` drops islands, both in output
units — a unit is a degree of longitude, and the globe is 180 of them across.
The defaults put the whole world in about 5KB and hold up to roughly 350px,
which exceeds the globe's display size. A lower `-tol` adds detail that is not
visible at that size and increases every settings page.

## What comes out is geography

The output uses `x = lon+180` and `y = 90-lat`, with one unit per degree. The
globe code projects these coordinates for the server-rendered still and for
each browser frame.

Simplification runs in three dimensions on the **unit sphere**, so the tolerance
has the same meaning at every latitude. A longitude-based tolerance would keep
too much detail near the poles.

## Ring winding

A coastline that runs over the edge of the world is closed with an arc of the
limb. The projection cannot determine the direction of that arc because an edge
that crosses the horizon runs to the rim and back along itself. This tool orients
each outer ring counter-clockwise and each hole clockwise before projection.

With the wrong winding, an arc takes the long route and fills most of the globe
with land. `TestGlobePaintsPlausibleLandAtEveryAngle` checks for this error.
