// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Recaptures docs/user-guide/images/*.png against a scratch registry.
// See README.md for the full recipe. Modes:
//   node shots.js setup   — setup-wizard pair, then claim with CLAIM=XXXX-XXXX
//   node shots.js         — every post-claim page, light and dark
const { chromium } = require('playwright')
const BASE = process.env.BASE || 'http://127.0.0.1:9098'

// The three the project page carries in its swipeable track — outcomes,
// technique-map and review-queue — share a viewport on purpose. The track takes
// the tallest slide's height so it does not resize under a reader's thumb, and
// three heights there means the two shorter screens float in a band of empty
// page. Keep them equal, or that section grows a gap again.
const DASHBOARD_VP = { width: 1280, height: 1000 }

const PAGES = [
  { name: 'outcomes', url: '/outcomes', vp: DASHBOARD_VP },
  { name: 'navigation', url: '/outcomes', vp: DASHBOARD_VP, element: 'header.top' },
  { name: 'members', url: '/members', vp: { width: 1280, height: 730 } },
  { name: 'review-queue', url: '/review', vp: DASHBOARD_VP },
  { name: 'technique', url: '/techniques/check-beacon-flags-before-debugging', vp: { width: 895, height: 695 } },
  { name: 'technique-map', url: '/techniques/map', vp: DASHBOARD_VP, settle: 5000 },
]

async function capture(page, t, sfx) {
  await page.goto(BASE + t.url, { waitUntil: 'networkidle' })
  await page.waitForTimeout(t.settle || 400)
  const out = `out/${t.name}${sfx}.png`
  if (t.element) await page.locator(t.element).first().screenshot({ path: out })
  else await page.screenshot({ path: out, clip: { x: 0, y: 0, ...t.vp } })
  console.log(out)
}

// The funnel image is the hero panel plus the pulse-tile row beneath it; the
// cohorts image is the adoption heatmap panel. Both live on /outcomes.
async function captureOutcomesCrops(page, sfx) {
  await page.goto(BASE + '/outcomes', { waitUntil: 'networkidle' })
  await page.waitForTimeout(500)
  const funnel = await page.locator('section#funnel').boundingBox()
  const tiles = await page.locator('.tiles').first().boundingBox()
  await page.screenshot({
    path: `out/outcomes-funnel${sfx}.png`,
    clip: { x: funnel.x, y: funnel.y, width: funnel.width, height: tiles.y + tiles.height - funnel.y },
  })
  await page
    .locator('section.panel', { hasText: 'Adoption by cohort and area' })
    .first()
    .screenshot({ path: `out/outcomes-cohorts${sfx}.png` })
  console.log(`out/outcomes-{funnel,cohorts}${sfx}.png`)
}

;(async () => {
  require('fs').mkdirSync('out', { recursive: true })
  const browser = await chromium.launch()
  const setupMode = process.argv[2] === 'setup'
  for (const scheme of ['light', 'dark']) {
    const sfx = scheme === 'dark' ? '-dark' : ''
    if (setupMode) {
      const page = await browser.newPage({ viewport: { width: 760, height: 570 }, deviceScaleFactor: 2, colorScheme: scheme })
      await page.goto(BASE + '/setup', { waitUntil: 'networkidle' })
      await page.screenshot({ path: `out/setup-wizard${sfx}.png`, clip: { x: 0, y: 0, width: 760, height: 570 } })
      console.log(`out/setup-wizard${sfx}.png`)
      await page.close()
      continue
    }
    const page = await browser.newPage({ viewport: { width: 1280, height: 2400 }, deviceScaleFactor: 2, colorScheme: scheme })
    for (const t of PAGES) {
      const p = await browser.newPage({ viewport: t.vp, deviceScaleFactor: 2, colorScheme: scheme })
      await capture(p, t, sfx)
      await p.close()
    }
    await captureOutcomesCrops(page, sfx)
    await page.close()
  }
  if (setupMode && process.env.CLAIM) {
    const page = await browser.newPage()
    await page.goto(BASE + '/setup', { waitUntil: 'networkidle' })
    await page.fill('input[name="claim_code"]', process.env.CLAIM)
    const key = await page.inputValue('input[name="api_key"]')
    await Promise.all([page.waitForLoadState('networkidle'), page.click('button[type="submit"], input[type="submit"]')])
    console.log('claimed; API key: ' + key)
    await page.close()
  }
  await browser.close()
})()
