// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Opens every destination in a real browser and fails on anything the source
// assertions in the Go suite cannot see. See README.md.
const { chromium } = require('playwright')

const BASE = process.env.BASE || 'http://127.0.0.1:9099'

// filled: a region the script builds. Empty after settle means the renderer
// threw, or never ran, or could not reach the data it needs.
const PAGES = [
  { url: '/outcomes' },
  { url: '/outcomes/events', filled: '#events-root' },
  { url: '/outcomes/cohorts' },
  { url: '/techniques' },
  { url: '/techniques/map', settle: 6000, asset: '/assets/techniquemap.js', painted: true },
  { url: '/review' },
  { url: '/usage', settle: 3000, asset: '/assets/usage.js', filled: '#usage-root' },
  { url: '/settings' },
]

// Chromium says these about things nobody can fix from here.
const IGNORE = [
  /favicon/i,
  /apple-touch-icon/i,
  /Failed to load resource: the server responded with a status of 404/i,
]

// Layout claims the Go suite can only make about the stylesheet's TEXT —
// "@media (max-width:900px){.usg-band{grid-template-columns:minmax(0,1fr)}}" and
// its neighbours. Asserting the declaration exists says nothing about whether
// the page lays out that way; measuring the rendered columns does, and survives
// a reformat of the stylesheet.
const LAYOUT = [
  {
    what: 'the Usage band is two columns on a wide screen and one on a phone',
    url: '/usage',
    at: [
      { width: 1280, height: 900, expect: 2 },
      { width: 600, height: 900, expect: 1 },
    ],
    columns: '.usg-band',
  },
  {
    what: 'a settings form collapses to one column on a phone',
    url: '/settings',
    at: [
      { width: 1280, height: 900, expect: 2 },
      { width: 520, height: 900, expect: 1 },
    ],
    columns: '.form-grid',
    optional: true, // not every settings tab renders one
  },
]

async function checkLayout(ctx) {
  let failures = 0
  for (const L of LAYOUT) {
    for (const vp of L.at) {
      const page = await ctx.newPage()
      await page.setViewportSize({ width: vp.width, height: vp.height })
      await page.goto(BASE + L.url, { waitUntil: 'networkidle' })
      await page.waitForTimeout(600)
      const cols = await page.evaluate(sel => {
        const el = document.querySelector(sel)
        if (!el) return null
        const t = getComputedStyle(el).gridTemplateColumns
        return t && t !== 'none' ? t.trim().split(/\s+/).length : 0
      }, L.columns)
      await page.close()
      if (cols === null) {
        if (L.optional) continue
        failures++
        console.log(`FAIL  ${L.columns} is not on ${L.url}`)
        continue
      }
      if (cols !== vp.expect) {
        failures++
        console.log(`FAIL  ${vp.width}px  ${L.what}`)
        console.log(`        ${L.columns} rendered ${cols} column(s), want ${vp.expect}`)
      } else {
        console.log(`ok    ${String(vp.width).padEnd(5)} ${L.columns} in ${cols} column(s)`)
      }
    }
  }
  return failures
}

// The sticky header only works if anchored content clears it.
async function checkStickyHeader(ctx) {
  const page = await ctx.newPage()
  await page.goto(BASE + '/outcomes', { waitUntil: 'networkidle' })
  const out = await page.evaluate(() => {
    const hdr = document.querySelector('header.top')
    const anchored = document.querySelector('[id]')
    return {
      sticky: hdr ? getComputedStyle(hdr).position : 'none',
      margin: anchored ? parseFloat(getComputedStyle(anchored).scrollMarginTop) || 0 : -1,
      height: hdr ? hdr.getBoundingClientRect().height : 0,
    }
  })
  await page.close()
  const problems = []
  if (out.sticky !== 'sticky') problems.push(`the header is position:${out.sticky}, not sticky`)
  if (out.margin < out.height) {
    problems.push(`anchors clear ${out.margin}px for a ${Math.round(out.height)}px header, so a linked heading lands under it`)
  }
  if (problems.length) {
    console.log('FAIL  the sticky header and the anchors that must clear it')
    for (const p of problems) console.log(`        ${p}`)
    return 1
  }
  console.log(`ok          header sticky, anchors clear ${out.margin}px`)
  return 0
}

async function main() {
  const browser = await chromium.launch()
  let failures = 0
  for (const scheme of ['light', 'dark']) {
    const ctx = await browser.newContext({ colorScheme: scheme })
    for (const p of PAGES) {
      const page = await ctx.newPage()
      const problems = []
      page.on('pageerror', e => problems.push('uncaught: ' + e.message))
      page.on('console', m => {
        if (m.type() === 'error' && !IGNORE.some(re => re.test(m.text()))) {
          problems.push('console: ' + m.text())
        }
      })
      const resp = await page.goto(BASE + p.url, { waitUntil: 'networkidle' })
      if (!resp || resp.status() !== 200) {
        problems.push(`status ${resp ? resp.status() : 'none'}`)
      }
      await page.waitForTimeout(p.settle || 800)

      if (p.asset) {
        const html = await page.content()
        if (!html.includes(`src="${p.asset}?v=`)) {
          problems.push(`does not link ${p.asset} with a fingerprint`)
        }
      }
      if (p.painted) {
        // The map draws to a canvas, which has no markup to look at. Two of
        // them, in fact: the WebGL field and the 2-D fallback. Ask their size,
        // and where a 2-D context is available ask the pixels too — a canvas
        // that is one flat colour was never drawn on. WebGL here is software
        // (SwiftShader), so the field's own pixels are not worth reading.
        const seen = await page.evaluate(() => {
          const cs = [...document.querySelectorAll('canvas')]
          if (!cs.length) return { sized: 0, shades: null }
          const sized = cs.filter(c => c.width > 0 && c.height > 0).length
          let shades = null
          for (const c of cs) {
            let ctx = null
            try { ctx = c.getContext('2d') } catch (e) { /* already a GL canvas */ }
            if (!ctx) continue
            const px = ctx.getImageData(0, 0, c.width, c.height).data
            const set = new Set()
            for (let i = 0; i < px.length; i += 4 * 97) {
              set.add(`${px[i]},${px[i + 1]},${px[i + 2]},${px[i + 3]}`)
              if (set.size > 8) break
            }
            shades = Math.max(shades ?? 0, set.size)
          }
          return { sized, shades }
        }).catch(e => ({ sized: -1, shades: null, err: String(e) }))

        if (seen.sized <= 0) problems.push('no canvas was given a size: the field never laid itself out')
        else if (seen.shades !== null && seen.shades < 2) {
          problems.push('the 2-D canvas is one flat colour: nothing was drawn')
        }
      }
      if (p.filled) {
        const text = await page.locator(p.filled).first()
          .innerHTML().catch(() => '')
        // A hint ("Loading…") means the renderer never replaced it.
        if (!text || /Loading|Load in progress/i.test(text)) {
          problems.push(`${p.filled} was never filled by the renderer (${text.slice(0, 60)})`)
        }
      }
      if (problems.length) {
        failures++
        console.log(`FAIL  ${scheme.padEnd(5)} ${p.url}`)
        for (const x of problems) console.log(`        ${x}`)
      } else {
        console.log(`ok    ${scheme.padEnd(5)} ${p.url}`)
      }
      await page.close()
    }
    await ctx.close()
  }

  console.log('')
  const layoutCtx = await browser.newContext()
  failures += await checkLayout(layoutCtx)
  failures += await checkStickyHeader(layoutCtx)
  await layoutCtx.close()

  await browser.close()
  if (failures) {
    console.log(`\n${failures} page(s) failed`)
    process.exit(1)
  }
  console.log('\nevery page rendered clean in both themes')
}

main().catch(e => { console.error(e); process.exit(1) })
