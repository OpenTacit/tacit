# browsercheck — run the dashboard in a real browser

The test suite does not run a browser. Around sixty assertions check source strings such as:
`strings.Contains(techniqueMapJS, "out.panX = cxDes - (m.x0 + m.x1) / 2;")` and
its neighbours in `techniquemap_test.go`, `usagemerge_test.go` and the scene
tests. The comments state that the renderer runs in a browser while the suite does not.
These tests can pass even when the rendered page does not work.

This check boots a scratch registry, fills it with the demo month,
opens every destination in headless Chromium, light and dark, and fails on

  * any uncaught page error or console error,
  * a page whose script-built region never fills,
  * a document that does not link the assets it needs.

It is **not** in `make test` and not in CI. Playwright is a large dependency
tree and a browser download, and this project keeps its dependencies countable
(`go.mod` has four). Run it by hand after changing browser code such as the map
renderer, usage renderer, or shell script, and
before trusting a source assertion that says the same thing.

## Running it

Playwright is not vendored. Use whatever copy is already on the machine:

```bash
export NODE_PATH=$(npm root -g)          # or any node_modules holding playwright
npx playwright install chromium          # first time only
./hack/browsercheck/check.sh
```

`PORT=9099 ./check.sh` if that port is busy. It touches nothing of yours: its
own `HOME`, its own data directory, its own port, all removed on exit.

## What it does not do

It does not compare pixels. Use `hack/guideshots` to inspect visual errors such as a
chart stretched to its viewBox or a double-escaped entity.
