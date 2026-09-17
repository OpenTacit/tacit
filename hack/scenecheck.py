#!/usr/bin/env python3
# Copyright 2026 The OpenTacit Authors
# SPDX-License-Identifier: Apache-2.0

"""Does any label in any diagram sit on any drawing?

Every picture in the registry places its captions as HTML at percentages over a
scaled grid, and a caption paints the panel's own colour so it can occlude the
1px beam it crosses. That is the whole trap: when a caption lands on a plate or
on a boundary it does not LOOK broken on screen — it takes a bite out of the
drawing and the drawing looks like that was its shape. The type is absolute px
and the grid scales with its own unit, so it goes wrong at some widths and not
others, and the width it goes wrong at is rarely the one you had open.

So this measures it. Render every scene, hide the labels, the beams and the
boundaries, and ask whether any pixel under a label's rectangle was painted.
Boxes are no good here: a plate is a square projected in 3D and its bounding
rectangle is a good deal taller than the diamond it draws, so a box test calls a
caption that clears the plate's apex an overlap and sends you moving pictures
that were already right.

  go test -run TestDumpScenes ./internal/registry/web   # writes the page
  hack/scenecheck.py /tmp/scenes/scenes.html            # measures it

TACIT_SCENE_DUMP names the file; app.css has to sit beside it. Exits non-zero
and names every label, and the widths it fails at, when anything is sitting on
anything.
"""
import io
import sys

from PIL import Image
from playwright.sync_api import sync_playwright

WIDTHS = [390, 520, 768, 901, 960, 1024, 1100, 1280, 1440, 1920]
# A beam is a rule a label may cross; a fence is a region a label may sit in.
# Everything else is a drawing, and a label on one is the fault.
HIDDEN = ".dgm-tag,.dgm-beam"
TOL = 12    # how far from the panel's own colour counts as ink
INSET = 1   # ignore a hairline of anti-aliasing at the label's edge
FLOOR = 4   # fewer painted pixels than this is a rounding artefact

RECTS = """()=>[...document.querySelectorAll('[data-scene]')].flatMap(d=>{
  const sc=d.querySelector('.dgm-scene'); if(!sc) return [];
  const S=sc.getBoundingClientRect(); if(!S.width||!S.height) return [];
  return [...sc.querySelectorAll('.dgm-tag')].map(e=>{const b=e.getBoundingClientRect();
    return {scene:d.dataset.scene, cls:e.className.split(' ').filter(x=>x!=='dgm-tag').join('.'),
      x:b.x+scrollX, y:b.y+scrollY, w:b.width, h:b.height};});})"""


def check(url, hidden=HIDDEN):
    found = {}
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        page = browser.new_page()
        for width in WIDTHS:
            page.set_viewport_size({"width": width, "height": 1200})
            page.goto(url)
            page.wait_for_timeout(150)
            rects = page.evaluate(RECTS)
            if not rects:
                continue
            page.add_style_tag(content=hidden + "{visibility:hidden!important}")
            shot = Image.open(io.BytesIO(page.screenshot(full_page=True))).convert("RGB")
            for tag in rects:
                x0 = max(0, int(tag["x"]) + INSET)
                y0 = max(0, int(tag["y"]) + INSET)
                x1 = min(shot.width, int(tag["x"] + tag["w"]) - INSET)
                y1 = min(shot.height, int(tag["y"] + tag["h"]) - INSET)
                if x1 <= x0 or y1 <= y0:
                    continue
                # The panel's colour, taken just above the label rather than
                # assumed, so this works in either theme.
                bg = shot.getpixel((min(x0, shot.width - 1), max(0, y0 - 3)))
                ink = sum(1 for c in shot.crop((x0, y0, x1, y1)).getdata()
                          if max(abs(c[i] - bg[i]) for i in range(3)) > TOL)
                if ink > FLOOR:
                    found.setdefault((tag["scene"], tag["cls"]), []).append((width, ink))
        browser.close()
    return found


# Is the mark ON its plate? The logo is drawn OUTSIDE the plate stack, so no 3D
# can squash it (memberscene_test.go says why) — which means its position is a
# pair of coordinates beside the node's, kept in step by hand. Nothing stops the
# two drifting apart, and in the members scene's phone layout they had: the mark
# carried twice its offset and hung low and right of the plate on every phone,
# on the one screen where the picture is the whole panel.
#
# The review scene is exempt by design: its mark stands at the fork where a
# technique is made, not on a plate.
MARK_EXEMPT = ("review",)
MARK_TOLERANCE = 0.12  # scene units

MARKS = """()=>[...document.querySelectorAll('[data-scene]')].flatMap(d=>{
  const sc=d.querySelector('.dgm-scene'); if(!sc) return [];
  if(!sc.getBoundingClientRect().width) return [];
  const m=sc.querySelector('.dgm-mark'); if(!m) return [];
  const u=parseFloat(getComputedStyle(sc).fontSize);
  const mb=m.getBoundingClientRect(), mc=[mb.x+mb.width/2, mb.y+mb.height/2];
  const nodes=[...sc.querySelectorAll('.dgm-node')]; if(!nodes.length) return [];
  let best=null,bd=1e9;
  for(const n of nodes){const r=n.getBoundingClientRect();
    const dd=Math.hypot(r.x+r.width/2-mc[0], r.y+r.height/2-mc[1]);
    if(dd<bd){bd=dd;best=n;}}
  const slabs=[...best.querySelectorAll('.dgm-slab')].map(s=>s.getBoundingClientRect());
  if(!slabs.length) return [];
  const top=slabs.reduce((a,b)=>a.y<b.y?a:b);
  return [{scene:d.dataset.scene,
    dx:(mc[0]-(top.x+top.width/2))/u, dy:(mc[1]-(top.y+top.height/2))/u}];})"""


def check_marks(url):
    off = {}
    with sync_playwright() as pw:
        browser = pw.chromium.launch()
        page = browser.new_page()
        for width in WIDTHS:
            page.set_viewport_size({"width": width, "height": 1200})
            page.goto(url)
            page.wait_for_timeout(150)
            for m in page.evaluate(MARKS):
                if m["scene"].startswith(MARK_EXEMPT):
                    continue
                if abs(m["dx"]) > MARK_TOLERANCE or abs(m["dy"]) > MARK_TOLERANCE:
                    off.setdefault(m["scene"], []).append((width, m["dx"], m["dy"]))
        browser.close()
    return off


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    url = sys.argv[1]
    if not url.startswith("http"):
        url = "file://" + url
    found = check(url)
    for (scene, cls), hits in sorted(found.items()):
        print(f"{scene:16} {cls:22} " + " ".join(f"{w}:{n}" for w, n in hits))
    print(len(found), "labels sitting on a drawing")

    marks = check_marks(url)
    for scene, hits in sorted(marks.items()):
        print(f"{scene:16} mark off its plate: "
              + " ".join(f"{w}:{dx:+.2f},{dy:+.2f}" for w, dx, dy in hits))
    print(len(marks), "marks off their plate")
    sys.exit(1 if found or marks else 0)


if __name__ == "__main__":
    main()
