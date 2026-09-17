// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The playbook map's client: one controller, two fields.
//
// The CONTROLLER owns everything that is not geometry — the arrangement (tags or
// cohort), what is hovered, pinned or lit, the detail panel, the colour legend,
// the Areas list, the deep link, the theme observers. The FIELD is whatever
// draws the graph and answers "what is under this pointer": webglField, a 3-D
// scene in the same idiom as the sign-in backdrop, or canvasField, the flat 2-D
// map, for a browser with no WebGL. Both expose the same six methods, so the
// controller never asks which one it got.
//
// That split is the whole point of this file existing. The map used to be an
// inline script; adding a second renderer would have meant a second copy of the
// detail panel, the legend, the area wiring and the mode switch, with a comment
// asking whoever changed one to remember the other. One asset, two fields, and
// the shared half is shared.
//
// Positions arrive precomputed from the server (techmap: x/y flat, x3/y3/z3 in
// the cube, and the cohort arrangement beside each), so every device draws the
// same map and neither field runs a layout.
//
// Whatever the field draws, it draws sixty times a second, so the cost of one
// frame decides whether a large playbook is usable at all. Three rules hold that
// cost down, and each of them is load-bearing. What does not change between
// frames is worked out once and kept: the palette, every node and relation
// colour, which relations run inside an area, and the DOM measurement the graph
// is fitted into. What does change is written into typed arrays rather than
// fresh objects. And the area fog thins itself to a fill budget as the camera
// closes in, because a blob's radius grows as you approach it, and a few
// thousand window-sized blobs will stall any GPU.
//
// Two documents load it: the /techniques/map page fetches it by hashed URL, and the
// MCP app inlines the same bytes because a sandboxed frame cannot reach back to
// the origin for an asset.
(function () {
  var dataEl = document.getElementById('cmap-data');
  var cv = document.getElementById('cmap');
  if (!dataEl || !cv) return;
  var data;
  try { data = JSON.parse(dataEl.textContent); } catch (e) { return; }
  var N = data.nodes || [], CL = data.clusters || [], CCL = data.cohortClusters || [];
  data.tagEdges = data.tagEdges || [];       // a null (no-edge) graph would throw below
  data.cohortEdges = data.cohortEdges || [];
  var wrap = cv.parentElement;
  var labelCv = document.getElementById('cmap-labels');
  var controls = document.getElementById('cmap-controls');

  // The initial arrangement comes from the server-rendered control (data-group),
  // which the page resolves from the persisted ?group= preference.
  var mode = (function () {
    try { var s = document.querySelector('[data-group]'); return s && s.dataset.group === 'cohort' ? 'cohort' : 'tags'; }
    catch (e) { return 'tags'; }
  })();

  var hover = -1;        // node under the pointer
  var pinned = -1;       // node clicked open in the detail panel
  var litCluster = -1;   // an area SELECTED from the list (highlight + frame), or -1
  var hoverCluster = -1; // an area merely HOVERED in the list (highlight only), or -1

  // The area palette. One hue per area of practice (or per cohort), used by the
  // nodes, the area labels on the field, the swatches in the Areas list and the
  // colour key — one scheme, so a colour means the same thing everywhere.
  var AREA_VAR = ['--s6', '--s5', '--s2', '--s3', '--s8', '--s7', '--s4', '--s1'];

  function AC() { return mode === 'tags' ? CL : CCL; }
  function nodeCl(i) { return mode === 'tags' ? N[i].cluster : (N[i].cohortCluster == null ? -1 : N[i].cohortCluster); }
  function edges() { return mode === 'tags' ? data.tagEdges : data.cohortEdges; }
  function nx(nd) { return mode === 'tags' ? nd.x : nd.cx; }
  function ny(nd) { return mode === 'tags' ? nd.y : nd.cy; }
  function n3(nd) {
    return mode === 'tags' ? [nd.x3, nd.y3, nd.z3] : [nd.cx3, nd.cy3, nd.cz3];
  }
  var nbrTag = N.map(function () { return new Set(); }), nbrCoh = N.map(function () { return new Set(); });
  data.tagEdges.forEach(function (e) { nbrTag[e.a].add(e.b); nbrTag[e.b].add(e.a); });
  data.cohortEdges.forEach(function (e) { nbrCoh[e.a].add(e.b); nbrCoh[e.b].add(e.a); });
  function nbr() { return mode === 'tags' ? nbrTag : nbrCoh; }

  function hex(h) {
    h = (h || '').replace('#', '');
    if (h.length === 3) h = h.split('').map(function (c) { return c + c; }).join('');
    if (h.length < 6) return [128, 128, 128];
    return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
  }
  function mix(a, b, t) { return a.map(function (v, i) { return Math.round(v + (b[i] - v) * t); }); }
  function rgba(c, a) { return 'rgba(' + c[0] + ',' + c[1] + ',' + c[2] + ',' + a + ')'; }
  // The palette, resolved once. Reading a custom property costs a style
  // resolution, and the field asked for one per node, per end of every relation
  // and per area — several thousand of them a frame, for an answer that only
  // changes when the theme does. The theme observers at the foot of this file
  // drop it.
  var palCache = null;
  function palette() {
    if (palCache) return palCache;
    var cs = getComputedStyle(document.documentElement);
    var v = function (n) { return hex(cs.getPropertyValue(n).trim()); };
    palCache = { surface: v('--surface'), ink: v('--ink'), muted: v('--muted'), area: AREA_VAR.map(v) };
    return palCache;
  }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); }
  function short(s) { return s.length > 22 ? s.slice(0, 21) + '…' : s; }

  // Node size scales with adoption RELATIVE to the busiest node, bounded by a
  // count-aware target so the map never overflows regardless of absolute volume
  // or node count. Kept in sync with techmap.go's nodeRadius()/mapSizeTarget().
  var FLOOR_R = 5, MIN_T = 18, MAX_T = 32, AREA_K = 24000;
  var maxAdopt = Math.max(1, Math.max.apply(null, N.map(function (n) { return n.adopted || 0; })));
  var sizeTarget = Math.min(MAX_T, Math.max(MIN_T, Math.sqrt(AREA_K / Math.max(1, N.length))));
  function radius(nd) { return FLOOR_R + (sizeTarget - FLOOR_R) * Math.sqrt((nd.adopted || 0) / maxAdopt); }
  function outerR(nd) { return radius(nd) * (nd.scope === 'org' ? 1.2 : 1); }

  // A node's colour: hue by AREA — the shared-tag area of practice it belongs to,
  // or the cohort that adopts it most, whichever arrangement is showing — depth by
  // how often it helped, grey when it has never been adopted. Both fields ask for
  // it the same way, so the two never disagree about what the map means.
  //
  // It used to be hue by SOURCE in the tag arrangement, and that was incoherent:
  // the Areas list beside the map carries a colour swatch per area, so the panel
  // was offering a key to a colour scheme the field wasn't using. Areas are what
  // the map is FOR — where the clumps are and what they are called — so they get
  // the channel, and the swatch beside a name now names something you can see.
  // Provenance hasn't gone anywhere; it is on the technique, in the detail panel.
  function nodeColour(i, p) {
    var nd = N[i];
    if (!nd.adopted) return mix(p.muted, p.surface, 0.35);
    var cl = nodeCl(i);
    // A technique in no area at all (a registry too small to cluster) has no area
    // colour to take, so it stays neutral rather than borrowing someone else's.
    var base = cl >= 0 ? p.area[cl % p.area.length] : mix(p.muted, p.ink, 0.35);
    // The helped-rate ramp runs from MUTED to the full hue, not from the surface
    // colour. Fading toward the surface is invisible by construction on a light
    // theme — a technique that never helped came out white on white — and muted is a
    // mid grey in both themes, so the low end of the ramp is always legible and
    // still reads as "less".
    var rate = nd.helped / nd.adopted;
    return mix(mix(base, p.muted, 0.5), base, Math.min(1, rate * 1.4));
  }

  // How much of a relation's ink survives the crowd. A technique with sixty
  // neighbours has sixty filaments converging on it, and at a fixed opacity each
  // they stack into a mat: the area clouds underneath disappear and a well-used
  // playbook draws as a grey disc with some dots on it. So a filament thins as its
  // two ends get busier — CROWD of them together lay down what one used to — which
  // is the rule the area fog already follows, where each blob thins as the area
  // grows. Under CROWD neighbours nothing changes, so a small map is untouched;
  // past it, a clique reads as one bright weave instead of a slab.
  var CROWD = 6;
  var thins = null, thinMode = '';
  function thinning() {
    if (thins && thinMode === mode) return thins;
    var E = edges(), nb = nbr(), t = new Float32Array(E.length);
    for (var i = 0; i < E.length; i++) {
      var d = (nb[E[i].a].size + nb[E[i].b].size) * 0.5;
      t[i] = d <= CROWD ? 1 : CROWD / d;
    }
    thins = t; thinMode = mode;
    return thins;
  }

  // Every node's colour and every relation's, worked out once per theme and
  // arrangement. Both are pure functions of those two things, and the field
  // was recomputing all of them — a few thousand colour mixes — on every frame.
  var inks = null;
  function colours() {
    var p = palette();
    if (inks && inks.pal === p && inks.mode === mode) return inks;
    var i, nc = new Array(N.length);
    for (i = 0; i < N.length; i++) nc[i] = nodeColour(i, p);
    // A relation is coloured at each END by the area that end belongs to, and
    // runs from one to the other. It used to be one flat colour per relation,
    // the average of the two node colours, and that is what made the map grey:
    // two area hues meet in the middle at something desaturated, a never-adopted
    // node is grey by design, and a busy playbook draws hundreds of those over
    // the very clouds the hues exist to show. Now a relation inside an area is
    // that area's hue outright — the weave reads as part of the body it runs
    // through — and one that bridges two areas leaves the first cloud in its
    // colour and arrives in the second's, with no grey at either end.
    //
    // One array per area, shared by its members, so a field can tell an internal
    // relation from a bridge by comparing the two ends for identity.
    var ends = new Array(N.length), tbl = {};
    for (i = 0; i < N.length; i++) {
      var cl = nodeCl(i);
      if (!tbl[cl]) {
        // Pulled a quarter toward the ink: a filament tinted purely by hue
        // disappears against a light plane, where there is no darkness for a
        // glow to glow against.
        tbl[cl] = mix(cl >= 0 ? p.area[cl % p.area.length] : mix(p.muted, p.ink, 0.35), p.ink, 0.25);
      }
      ends[i] = tbl[cl];
    }
    inks = { pal: p, mode: mode, node: nc, end: ends };
    return inks;
  }

  // --- the detail panel -------------------------------------------------------
  // Anchored to the side AWAY from the node, so it never covers what is being
  // inspected; the field says where the node currently is on screen.
  function detail(i) {
    var el = document.getElementById('cmap-detail');
    if (!el) return;
    if (i < 0) { el.hidden = true; return; }
    var nd = N[i];
    var rate = nd.adopted ? Math.round(nd.helped / nd.adopted * 100) + '%' : '—';
    var mx = Math.max(nd.shown, 1);
    function bar(v) { return '<div class="cmap-track"><div class="cmap-fill" style="width:' + Math.round(v / mx * 100) + '%"></div></div>'; }
    var ns = Array.from(nbr()[i]).sort(function (a, b) { return N[b].adopted - N[a].adopted; }).slice(0, 6);
    var tags = (nd.tags || []).map(function (t) { return '<a class="cmap-tag" href="/outcomes/tag/' + encodeURIComponent(t) + '">#' + esc(t) + '</a>'; }).join('');
    var lead = mode === 'tags' ? 'Shares tags with ' : 'Adopted alongside ';
    var none = mode === 'tags'
      ? 'Shares no tags with any other technique.'
      : (nd.adopted ? 'No cohort has adopted this alongside another.' : 'Not yet adopted by any cohort.');
    var nbrs = ns.length
      ? '<p class="cmap-nbrs">' + lead + ns.map(function (j) { return '<a data-j="' + j + '">' + esc(short(N[j].name)) + '</a>'; }).join(', ') + '</p>'
      : '<p class="cmap-nbrs" style="color:var(--muted)">' + none + '</p>';
    el.innerHTML = '<button type="button" class="cmap-detail-x" id="cmap-x" aria-label="Close">×</button>'
      + '<p class="cmap-lbl">Technique</p>'
      + '<p class="cmap-name"><a href="/techniques/' + esc(nd.id) + '">' + esc(nd.name) + '</a></p>'
      + '<p class="cmap-meta">' + (nd.scope === 'org' ? '<span class="org">org</span> ' : '') + esc(nd.area) + ' · ' + esc(nd.source) + '</p>'
      + '<div class="cmap-funnel">'
      + '<div class="cmap-frow"><span>shown</span>' + bar(nd.shown) + '<b>' + nd.shown + '</b></div>'
      + '<div class="cmap-frow"><span>adopted</span>' + bar(nd.adopted) + '<b>' + nd.adopted + '</b></div>'
      + '<div class="cmap-frow"><span>helped</span>' + bar(nd.helped) + '<b>' + nd.helped + '</b></div>'
      + '<div class="cmap-frow"><span>helped rate</span><span></span><b>' + rate + '</b></div>'
      + '</div><div class="cmap-tags">' + tags + '</div>' + nbrs;
    el.hidden = false;
    var p = field.screen(i);
    // Anchor to the side away from the node, so the panel never covers what is
    // being read. The overlay owns the left when it is open, so the panel takes
    // the right whatever the node is doing — a panel underneath the overlay is not
    // a panel at all.
    var panelOpen = !!(controls && !controls.hidden);
    var toRight = panelOpen ? true : (p ? p[0] < field.width() / 2 : true);

    el.classList.toggle('cmap-detail-right', toRight);
    el.classList.toggle('cmap-detail-left', !toRight);
    var xb = document.getElementById('cmap-x');
    if (xb) xb.onclick = function () { pinned = -1; hover = -1; detail(-1); field.draw(); };
    Array.prototype.forEach.call(el.querySelectorAll('a[data-j]'), function (a) {
      a.onclick = function () { pinned = +a.dataset.j; detail(pinned); field.draw(); };
    });
  }

  // --- the colour legend -----------------------------------------------------
  // Follows the arrangement: source hues in tag mode, cohort hues in cohort mode.
  // The colour key follows the arrangement, and it is the same key the Areas
  // list above it is drawn with: areas of practice in tag mode, cohorts in
  // cohort mode.
  function updateColorLegend() {
    var lbl = document.getElementById('cmap-colour-lbl'), host = document.getElementById('cmap-src-legend');
    if (!host) return;
    var out = '';
    var list = mode === 'cohort' ? CCL : CL;
    if (lbl) lbl.textContent = mode === 'cohort' ? 'Color = cohort' : 'Color = area';
    list.forEach(function (cl) {
      out += '<span class="cmap-src"><i style="background:var(' + AREA_VAR[cl.id % AREA_VAR.length] + ')"></i>' + esc(cl.name) + '</span>';
    });
    if (!list.length) {
      out = mode === 'cohort'
        ? '<span class="cmap-src">no cohort adoption yet</span>'
        : '<span class="cmap-src">no areas yet</span>';
    }
    host.innerHTML = out;
  }

  // The FREE AREA: the part of the canvas nothing is floating over. In the
  // full-bleed view the field runs the whole window under the top bar, and the
  // page's furniture — crumbs, blurb, filter bar, the count — sits on top of it,
  // as does the Areas overlay. Both fields centre and fit their graph in what is
  // left, so the field bleeds behind the controls without ever hiding behind them.
  //
  // Measured from the DOM rather than configured: the furniture's height depends
  // on the filter chips, on how the blurb wraps and on the window's width, and
  // every one of those changes under us. On a page that still frames the map in a
  // panel (the MCP app) nothing overlaps it, and this returns the whole canvas.
  //
  // And it is measured only when something has moved it. A measurement forces
  // the browser to lay the page out, and doing that on every frame of an
  // animation is most of the cost of a map nobody is touching; the answer only
  // changes on a resize, a scroll or the overlay opening, so those mark it stale
  // and every other frame reads the last one.
  var frameStale = true, lastSafeTop = '';
  function markFrame() { frameStale = true; }
  function framed(W, H, out) {
    if (frameStale || out.mw !== W || out.mh !== H) {
      safeFrame(W, H, out); out.mw = W; out.mh = H; frameStale = false;
    }
    return out;
  }
  // How much glass the field keeps clear of every edge. The same number on all
  // four sides, because the rectangle's own centre is what the graph is aimed
  // at: an inset on the right with nothing matching it on the left is a bias
  // built into every fit, and the map came out half of it off centre.
  var EDGE = 14;
  function safeFrame(W, H, out) {
    out.x0 = EDGE; out.y0 = EDGE; out.x1 = W - EDGE; out.y1 = H - EDGE;
    if (!W || !H) return out;
    var r = cv.getBoundingClientRect();
    var stage = wrap.closest ? wrap.closest('.cmap-stage') : null;
    // The GLASS is full bleed; the PAGE is not. Every other row under the top bar
    // — the crumbs, the filter bar, the count, the Areas overlay — is held inside
    // one column, and a graph fitted to the window is fitted to a width nothing
    // else on the page uses. On a 2200px monitor that put the field's centre 363px
    // right of the column everything is read in, and the graph ran out past both
    // ends of the page. So the free area is capped to the column. Measured off the
    // element rather than repeated from the stylesheet, so one max-width governs
    // the page and the map together; where nothing constrains the field — the MCP
    // app, which frames it in a panel — the column is the canvas and this is a
    // no-op.
    var col = stage && stage.parentElement ? stage.parentElement.getBoundingClientRect() : null;
    if (col && col.width > 0) {
      out.x0 = Math.max(out.x0, col.left - r.left);
      out.x1 = Math.min(out.x1, col.right - r.left);
    }
    // Everything the page lays out ABOVE the field pushes the top down.
    if (stage && stage.parentElement) {
      var kids = stage.parentElement.children;
      for (var i = 0; i < kids.length; i++) {
        if (kids[i] === stage) break;
        var b = kids[i].getBoundingClientRect();
        if (b.height > 0 && b.bottom > r.top) out.y0 = Math.max(out.y0, b.bottom - r.top + 12);
      }
    }
    // Both horizontal limits below are fractions of the COLUMN, not the window.
    // They exist to stop the furniture squeezing the graph out of existence, and
    // the graph's room is now the column: measured against the window they go
    // slack on a wide monitor and hand back the width the cap above just took.
    var colX0 = out.x0, colW = out.x1 - out.x0;
    // The overlay pushes the left in, but only where there is room to spare: on a
    // narrow window it covers the field, and the graph keeps the full width
    // rather than being squeezed into a strip beside it.
    if (controls && !controls.hidden && W > 900) {
      var c = controls.getBoundingClientRect();
      out.x0 = Math.max(out.x0, Math.min(colX0 + colW * 0.42, c.right - r.left + 16));
    }
    // Never let the furniture squeeze the field to nothing: past a point it is
    // better to let the graph run under the controls than to have no graph.
    if (out.x1 - out.x0 < colW * 0.42) out.x0 = out.x1 - colW * 0.42;
    if (out.y1 - out.y0 < H * 0.45) out.y0 = out.y1 - H * 0.45;
    // Publish where the free area starts, so the overlay, its button and the
    // detail panel can sit at the top of it (app.css reads --cmap-safe-top).
    var top = Math.round(out.y0) + 'px';
    if (wrap.style && top !== lastSafeTop) { wrap.style.setProperty('--cmap-safe-top', top); lastSafeTop = top; }
    return out;
  }

  // ===========================================================================
  // FIELD: the 3-D one.
  //
  // The same idiom as the sign-in backdrop (assets/backdrop.js): the projection
  // runs on the CPU and WebGL only rasterises — soft-glow point sprites for the
  // techniques, camera-facing quads for the relations. Doing the perspective in JS is
  // what makes the map a map: every node's screen position and radius already
  // exist each frame, so hit-testing, the detail panel's anchor and the labels
  // are all just reads, with no depth buffer to interrogate and no picking pass.
  //
  // What the third dimension buys: the flat map has to solve edge crossings by
  // pushing clusters apart, so a big library spreads thin and the areas read as
  // blobs. In the cube an area is a volume you orbit around — the relations that
  // were a hairball resolve into depth, and the shape of the thing is legible.
  // ===========================================================================
  function webglField() {
    var gl = null;
    try {
      gl = cv.getContext('webgl', { alpha: true, antialias: true, depth: false, premultipliedAlpha: true })
        || cv.getContext('experimental-webgl', { alpha: true, antialias: true, depth: false, premultipliedAlpha: true });
    } catch (e) { return null; }
    if (!gl) return null;

    function sh(t, s) { var o = gl.createShader(t); gl.shaderSource(o, s); gl.compileShader(o); return gl.getShaderParameter(o, gl.COMPILE_STATUS) ? o : null; }
    function prog(v, f) {
      var a = sh(gl.VERTEX_SHADER, v), b = sh(gl.FRAGMENT_SHADER, f);
      if (!a || !b) return null;
      var p = gl.createProgram(); gl.attachShader(p, a); gl.attachShader(p, b); gl.linkProgram(p);
      return gl.getProgramParameter(p, gl.LINK_STATUS) ? p : null;
    }
    // Nodes: a disc for a general technique, a five-pointed STAR for an org-specific
    // one (r>0.5) — the same mark the flat map has always drawn, so the shape
    // means one thing in both fields and the legend needs one line.
    //
    // This used to be a concentric ring, on the theory that a star could not
    // survive being drawn at eight pixels. A ring is the wrong distinction: it
    // is a difference in TRIM, and trim is exactly what disappears first as a
    // node shrinks — the smallest techniques, where the ring collapses to a fatter
    // edge on a disc, are most of the map. A silhouette survives what an outline
    // does not, because it is read from the shape of the whole mark.
    //
    // Drawn as a signed distance field (Inigo Quilez's polar-fold star) rather
    // than a polygon, so it is exact at any size: the fold maps every angle into
    // one tenth of the star and measures the distance to that wedge's edge. The
    // antialiasing width comes from the sprite's own pixel size (vps), which is
    // why the vertex stage passes it down — WebGL 1 has no fwidth() without an
    // extension, and one that some drivers lack is not worth depending on for a
    // shape this small.
    //
    // The sprite is a solid mark with a halo around it, not a soft blob: the core
    // holds to full alpha out to the node's real radius (0.31 of the sprite) and
    // only then falls away. A pure smoothstep from the centre — the backdrop's
    // sprite, where nothing has to be read — turned every technique into a cloud, and
    // thirty overlapping clouds are a fog bank, not a map.
    var pPoint = prog(
      'attribute vec2 p;attribute float s;attribute vec4 c;attribute float r;' +
      'varying vec4 v;varying float vr;varying float vps;' +
      'void main(){gl_Position=vec4(p,0.,1.);gl_PointSize=s;v=c;vr=r;vps=s;}',
      'precision mediump float;varying vec4 v;varying float vr;varying float vps;' +
      // A star of outer radius R in sprite units. AN is half a point's angular
      // width (pi/5); ACS/ECS are the point and notch directions for sharpness
      // m=3, which puts the notches at 0.47 of the tips — the flat map's ratio,
      // so the same technique is the same star in either field.
      'const float R=.44;const float AN=.62831853;' +
      'const vec2 ACS=vec2(.809017,.587785);const vec2 ECS=vec2(.5,.8660254);' +
      'float star(vec2 q,float d){' +
      'float bn=mod(atan(q.x,q.y),2.*AN)-AN;' +
      'vec2 f=d*vec2(cos(bn),abs(sin(bn)))-R*ACS;' +
      'f+=ECS*clamp(-dot(f,ECS),0.,R*ACS.y/ECS.y);' +
      'return length(f)*sign(f.x);}' +
      'void main(){vec2 q=gl_PointCoord-vec2(.5);float d=length(q);' +
      'float core=1.-smoothstep(.30,.345,d);' +          // the disc, with an antialiased rim
      'float halo=(1.-smoothstep(.32,.5,d))*.28;' +      // the light it throws
      // The star's glow hugs the star, measured off the same distance field. A
      // round halo around a pointed mark fills its own notches back in, which is
      // the silhouette this change exists to protect.
      'if(vr>.5){float sd=star(q,d);float aa=1.6/max(vps,2.);' +
      'core=1.-smoothstep(-aa,aa,sd);halo=(1.-smoothstep(0.,.09,sd))*.28;}' +
      'float a=v.a*min(1.,max(core,halo));gl_FragColor=vec4(v.rgb*a,a);}');
    // Edges: camera-facing quads (GL line width is capped at 1px), s in [-1,1]
    // across the width, alpha falling from the core outward so a relation reads
    // as a luminous filament rather than a hairline.
    var pLine = prog(
      'attribute vec2 p;attribute float s;attribute vec4 c;varying vec4 v;varying float vs;' +
      'void main(){gl_Position=vec4(p,0.,1.);v=c;vs=s;}',
      'precision mediump float;varying vec4 v;varying float vs;' +
      'void main(){float g=1.-abs(vs);g*=g;float a=v.a*g;gl_FragColor=vec4(v.rgb*a,a);}');
    // Area clouds: the volume an area of practice occupies, as fog. The flat map
    // draws a padded convex hull round each cluster; a hull has no meaning in a
    // perspective volume — it is a silhouette, so it would cut a hard edge
    // through techniques floating in front of and behind it.
    //
    // So the volume is SPLATTED instead: a soft blob at each member, at the
    // centroid, and at the midpoints between the two, each fading to nothing at
    // its rim. Overlaid, they accumulate into one body that swells where the
    // techniques are dense and thins out where they are not, with no edge anywhere —
    // which is what an area actually is. Quads rather than point sprites because
    // a cloud is often wider than the driver's maximum point size.
    var pCloud = prog(
      'attribute vec2 p;attribute vec2 q;attribute vec4 c;varying vec2 vq;varying vec4 v;' +
      'void main(){gl_Position=vec4(p,0.,1.);vq=q;v=c;}',
      'precision mediump float;varying vec2 vq;varying vec4 v;' +
      // smoothstep, not a power curve: pow leaves a knee where the falloff turns,
      // and a hundred overlapping splats stack that knee into visible contour
      // rings. This one has no knee to stack.
      'void main(){float d=length(vq);if(d>1.)discard;' +
      'float f=1.-smoothstep(0.,1.,d);float a=v.a*f*f;gl_FragColor=vec4(v.rgb*a,a);}');
    if (!pPoint || !pLine || !pCloud) return null;

    var Pp = gl.getAttribLocation(pPoint, 'p'), Ps = gl.getAttribLocation(pPoint, 's'),
      Pc = gl.getAttribLocation(pPoint, 'c'), Pr = gl.getAttribLocation(pPoint, 'r');
    var Lp = gl.getAttribLocation(pLine, 'p'), Ls = gl.getAttribLocation(pLine, 's'), Lc = gl.getAttribLocation(pLine, 'c');
    var Fp = gl.getAttribLocation(pCloud, 'p'), Fq = gl.getAttribLocation(pCloud, 'q'), Fc = gl.getAttribLocation(pCloud, 'c');
    var pointBuf = gl.createBuffer(), lineBuf = gl.createBuffer(), cloudBuf = gl.createBuffer();
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA); // premultiplied
    gl.enable(gl.BLEND);

    var lctx = labelCv ? labelCv.getContext('2d') : null;
    var W = 0, H = 0, dpr = 1;
    // Camera: an orbit. target is what it looks at, dist how far back it sits,
    // yaw/pitch where it sits on the sphere around it.
    // panX/panY are a screen-space nudge, in pixels. Perspective is non-linear —
    // the projection of a cloud's centroid is not the centroid of its projection
    // — so aiming the camera at the middle of the graph does NOT centre the
    // picture of it. Rather than solve that in world space, the fit measures the
    // projected bounding box and slides the whole image over by the error.
    var cam = { yaw: 0.6, pitch: -0.35, dist: 3.2, tx: 0, ty: 0, tz: 0, panX: 0, panY: 0 };
    var camTo = null, camFrom = null, camT0 = 0, CAM_MS = 620;
    var SPAN = 2.0;        // the unit cube maps to a cube this wide in world units
    // A wide-ish lens on purpose. At a long focal length the near and far faces
    // of the cloud project to almost the same size and the scene flattens into
    // the 2-D map it replaced; opening the angle up is what makes near techniques
    // read as near.
    var FOCAL = 1.25;
    // How close and how far the camera may sit, in world units (the cube is SPAN
    // across). The near limit has to clear framing a SINGLE small area, not just
    // the whole field — set for the field, it silently refused to zoom in and
    // selecting an area only dimmed the others.
    var MIND = 0.3, MAXD = 14.0;
    // How much bigger than a node at the centre the nearest node may draw.
    //
    // This is really a bound on the camera, not on the nodes. A point on the
    // near face of the cloud sits at zc = dist - near and magnifies by
    // dist/(dist - near), so capping the magnification is the same as keeping
    // the camera OUT of the cloud: dist >= near * MAXMAG / (MAXMAG - 1). Without it
    // the fit could settle at a distance shorter than the cloud's own radius,
    // and then everything downstream is nonsense — the near nodes project
    // hundreds of screens wide, the ones past the lens are dropped from the fit
    // entirely, and the box the fit is chasing grows faster than it can zoom out
    // of it. See fitTo.
    var MAXMAG = 4;
    var reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
    var DRIFT = reduced ? 0 : 0.012;   // per-node breathing; zero when motion is unwelcome
    var SPIN = reduced ? 0 : 0.055;    // radians/second of ambient yaw
    var idleAt = 0, dragging = false, raf = 0, t0 = performance.now() / 1000, tNow = 0;
    // Whether the reader has taken the camera. A window that changes size after
    // the map has opened — a rotation, a Split View drag, a browser toolbar
    // settling on first paint — leaves the camera fitted to a frame that no
    // longer exists, and nothing here used to notice: the observer resized the
    // buffers and redrew the old view. But re-framing unasked would also undo
    // somebody's orbit every time the window twitched, so it is conditional on
    // this: while the camera is still the one the fit chose, a new frame gets a
    // new fit; once a hand is on it, the hand wins.
    var taken = false;
    // hidden tracks whether this document is on screen at all; lastT is the clock
    // reading at the last painted frame, which is what lets the drift resume
    // rather than jump when the tab comes back (see the visibilitychange handler).
    var hidden = !!document.hidden, lastT = 0;
    // freeArea, not `frame`: frame() is the camera move that fits the field, and
    // a var of the same name in the same scope silently replaces the function.
    var freeArea = { x0: 0, y0: 0, x1: 0, y1: 0 };
    function measureFrame() { framed(W, H, freeArea); }

    // Screen-space scratch, refilled every frame: this is what picking, the
    // labels and the detail panel's anchor all read.
    var sx = new Float32Array(N.length), sy = new Float32Array(N.length),
      sr = new Float32Array(N.length), sz = new Float32Array(N.length), svis = new Uint8Array(N.length);
    var order = [];
    for (var oi = 0; oi < N.length; oi++) order.push(oi);
    // A fixed per-node phase, so the drift is deterministic rather than random.
    var ph = N.map(function (_, i) { return [i * 1.7, i * 2.3 + 1.1, i * 0.9 + 2.2]; });

    var pointArr = new Float32Array(N.length * 8);
    // How many blobs the fog is allowed to spend, and how much of the window it
    // may cover with them (see the cloud pass). Six vertices per splat, eight
    // floats each.
    var MAX_SPLATS = 6000, CLOUD_FILL = 9;
    var cloudArr = new Float32Array(MAX_SPLATS * 6 * 8);
    var lineArr = new Float32Array(Math.max(1, edgesMax()) * 6 * 7);
    function edgesMax() { return Math.max(data.tagEdges.length, data.cohortEdges.length); }

    // The splats the cloud pass builds, kept as flat columns rather than as
    // objects: a large map makes thousands of them a frame, and thousands of
    // short-lived objects a frame is a garbage collector running through the
    // animation.
    var spX = new Float32Array(MAX_SPLATS), spY = new Float32Array(MAX_SPLATS),
      spZ = new Float32Array(MAX_SPLATS), spR = new Float32Array(MAX_SPLATS),
      spR0 = new Float32Array(MAX_SPLATS), spG = new Float32Array(MAX_SPLATS),
      spB = new Float32Array(MAX_SPLATS), spA = new Float32Array(MAX_SPLATS);
    var spIdx = [];
    function splatOrder(n) {
      spIdx.length = n;
      for (var i = 0; i < n; i++) spIdx[i] = i;
      spIdx.sort(function (a, b) { return spZ[b] - spZ[a]; });   // far first, like everything else
      return spIdx;
    }
    function linePut(le, px, py, sgn, r, g, b, a) {
      var o = le * 7;
      lineArr[o] = px / cv.width * 2 - 1; lineArr[o + 1] = 1 - py / cv.height * 2;
      lineArr[o + 2] = sgn; lineArr[o + 3] = r; lineArr[o + 4] = g; lineArr[o + 5] = b; lineArr[o + 6] = a;
      return le + 1;
    }
    function cloudVtx(fv, x, y, qx, qy, r, g, b, a) {
      var o = fv * 8;
      cloudArr[o] = x; cloudArr[o + 1] = y; cloudArr[o + 2] = qx; cloudArr[o + 3] = qy;
      cloudArr[o + 4] = r; cloudArr[o + 5] = g; cloudArr[o + 6] = b; cloudArr[o + 7] = a;
      return fv + 1;
    }

    // Which relations run INSIDE each area. The cloud follows them, and it used
    // to find them by walking every relation in the graph once per area, on
    // every frame — tens of millions of comparisons a second for an answer that
    // only changes when the arrangement does.
    var clEdges = null, clEdgesMode = '';
    function clusterEdges() {
      if (clEdges && clEdgesMode === mode) return clEdges;
      var A = AC(), E = edges(), owner = new Int32Array(N.length), i;
      for (i = 0; i < N.length; i++) owner[i] = -1;
      for (i = 0; i < A.length; i++) {
        var mem = A[i].members || [];
        for (var m = 0; m < mem.length; m++) owner[mem[m]] = i;
      }
      clEdges = A.map(function () { return []; });
      for (i = 0; i < E.length; i++) {
        var o = owner[E[i].a];
        if (o >= 0 && o === owner[E[i].b]) clEdges[o].push(i);
      }
      clEdgesMode = mode;
      return clEdges;
    }

    // Where every node IS this instant: its arranged position plus its breath.
    // Filled once a frame and read by everything after — the projection, the
    // clouds, the fit. Each of those used to work it out for itself, and the
    // clouds did it twice per relation per area, which on a large map was most
    // of the frame before a single pixel was drawn.
    var bx = new Float32Array(N.length), by = new Float32Array(N.length), bz = new Float32Array(N.length);
    var wx = new Float32Array(N.length), wy = new Float32Array(N.length), wz = new Float32Array(N.length);
    var baseMode = '';
    function updateWorld() {
      var i;
      if (baseMode !== mode) {
        for (i = 0; i < N.length; i++) {
          var q = n3(N[i]);
          bx[i] = (q[0] - 0.5) * SPAN; by[i] = (q[1] - 0.5) * SPAN; bz[i] = (q[2] - 0.5) * SPAN;
        }
        baseMode = mode;
      }
      var d = DRIFT;
      for (i = 0; i < N.length; i++) {
        wx[i] = bx[i] + (d ? d * Math.sin(tNow * 0.31 + ph[i][0]) : 0);
        wy[i] = by[i] + (d ? d * Math.sin(tNow * 0.27 + ph[i][1]) : 0);
        wz[i] = bz[i] + (d ? d * Math.sin(tNow * 0.23 + ph[i][2]) : 0);
      }
    }
    function world(i) { return [wx[i], wy[i], wz[i]]; }
    // The same point plus the node's own size, for the fit: a technique is a
    // disc, and the outermost one is only inside the frame if its EDGE is.
    function worldR(i) { return [wx[i], wy[i], wz[i], outerR(N[i])]; }
    // Project a world point: translate to the camera's target, yaw, pitch, push
    // back by dist, then divide. Returns null behind the lens.
    function project(w) {
      var x = w[0] - cam.tx, y = w[1] - cam.ty, z = w[2] - cam.tz;
      var cy = Math.cos(cam.yaw), sy2 = Math.sin(cam.yaw);
      var x1 = x * cy - z * sy2, z1 = x * sy2 + z * cy;
      var cp = Math.cos(cam.pitch), sp = Math.sin(cam.pitch);
      var y1 = y * cp - z1 * sp, z2 = y * sp + z1 * cp;
      var zc = z2 + cam.dist;
      if (zc < 0.05) return null;
      var k = Math.min(W, H) * 0.5;
      var s = FOCAL / zc;
      // Centre on the space the field actually has, not on the canvas: the
      // controls floating over it own the top and the overlay owns the left, and
      // a graph centred behind either of them is half a map.
      return [(freeArea.x0 + freeArea.x1) / 2 + cam.panX + x1 * s * k,
      (freeArea.y0 + freeArea.y1) / 2 + cam.panY - y1 * s * k, zc];
    }
    // Nodes at the focal plane draw at their nominal radius; nearer ones swell,
    // far ones shrink — the same cue the size encoding already uses, so the two
    // have to stay separable, which is what the fog below is for.
    // The 2-D radii were tuned for a 600px canvas where nodes never overlap. Here
    // they sit in a perspective volume and DO overlap, so they draw smaller: the
    // busiest technique is still unmistakable, and a cluster still reads as a cluster.
    function screenR(i, zc) { return outerR(N[i]) * (cam.dist / zc) * (Math.min(W, H) / 1500); }
    // Fog: distance reads as dimmer and flatter, so the depth is felt without a
    // second visual channel being spent on it.
    // Depth reads as dimmer, but never as gone: the floor is high enough that the
    // far side of the cloud is still a map on a light plane, where there is no
    // darkness to fade into.
    function fog(zc) {
      var t = (zc - (cam.dist - SPAN * 0.7)) / (SPAN * 1.4);
      return Math.max(0.4, Math.min(1, 1.06 - t * 0.95));
    }

    // fitTo frames a set of world points, measured where it matters: on screen.
    // It aims at the middle of their extent, finds the distance that puts the
    // whole projected picture inside the space the furniture has left, and pans
    // that picture to the middle of that space.
    //
    // THE SPACE THE FURNITURE HAS LEFT, on both axes — not the window. The map
    // is full bleed and what floats over it is an L: a band across the top, and
    // the Areas overlay down the left whenever there is room for it to sit
    // beside the graph rather than over it. So the free rectangle's own centre
    // is well right of the window's, and that is the one to aim at: while the
    // overlay holds the left of the glass, the space beside it is the space the
    // map has, and centring in the space you have is what centred means.
    //
    // This did read as shoved into the bottom-right once, which is why the aim
    // spent a while preferring the window's centre instead. That was the wrong
    // culprit. The picture was landing in the corner because the camera fit had
    // diverged — half a screen of pan, a third of the field off the bottom (see
    // the distance search below) — and aiming at the window only moved where the
    // mis-fitted picture sat. With the fit right, the two rules differ by the
    // half-width of the overlay, and the free area's centre is the one that
    // leaves equal margins around the graph.
    function fitTo(pts, fill) {
      var out = { tx: 0, ty: 0, tz: 0, dist: cam.dist, panX: 0, panY: 0 };
      if (!pts.length) return out;
      // Aim at the middle of the EXTENT, not at the centre of mass. Framing is
      // a question about the outside of the cloud, and the mean is pulled off it
      // by wherever the crowd is: on a library with fifty techniques that share
      // no tag with anything, the mean sat a third of the way to one corner, and
      // the far side was then 2.03 world units off the target where the middle of
      // the extent puts it 1.53. That difference is a quarter of the standoff
      // the camera needs — a quarter of the size the map gets to be drawn at.
      var lo3 = [1e9, 1e9, 1e9], hi3 = [-1e9, -1e9, -1e9];
      pts.forEach(function (w) {
        for (var d = 0; d < 3; d++) { lo3[d] = Math.min(lo3[d], w[d]); hi3[d] = Math.max(hi3[d], w[d]); }
      });
      out.tx = (lo3[0] + hi3[0]) / 2; out.ty = (lo3[1] + hi3[1]) / 2; out.tz = (lo3[2] + hi3[2]) / 2;
      measureFrame();
      // Room for the label that sits over the outermost node. Its disc is
      // already in the box — each point is measured with its own radius below —
      // so this is the only allowance the frame still owes.
      var pad = 16;
      var limX = Math.max(30, (freeArea.x1 - freeArea.x0) / 2 * fill - pad);
      var limY = Math.max(30, (freeArea.y1 - freeArea.y0) / 2 * fill - pad);
      var cxDes = (freeArea.x0 + freeArea.x1) / 2, cyDes = (freeArea.y0 + freeArea.y1) / 2;
      // The camera has to stand off the cloud before anything else is true, and
      // how far off is geometry, not a guess. What matters is the NEAR FACE
      // along the camera's own axis: a point there sits at zc = dist - near and
      // magnifies by dist/(dist - near), so bounding that by MAXMAG bounds the
      // distance from below at near * MAXMAG / (MAXMAG - 1).
      //
      // The face, not the bounding sphere. The sphere around a cloud shaped like
      // a cube is mostly empty corner — 2.1 world units of radius around a cloud
      // whose near face is 1.7 away — and a floor set from it holds the camera
      // back far enough to shrink the map to half the frame it was given.
      //
      // Worst case over yaw rather than the yaw of the moment, because the
      // ambient spin turns the cloud after the fit has run: a point's depth
      // offset is y*sin(pitch) + h*cos(pitch) with h anywhere in [-rxz, rxz] as
      // the yaw comes round, so the nearest it ever gets is with h at -rxz.
      var spt = Math.sin(cam.pitch), cpt = Math.cos(cam.pitch), near = 0;
      pts.forEach(function (w) {
        var dx = w[0] - out.tx, dy = w[1] - out.ty, dz = w[2] - out.tz;
        near = Math.max(near, Math.sqrt(dx * dx + dz * dz) * cpt - dy * spt);
      });
      var minD = Math.max(MIND, near * MAXMAG / (MAXMAG - 1));
      function clampD(d) { return Math.max(minD, Math.min(MAXD, d)); }

      // The painted box, not the box of the centres. A disc's radius on screen
      // is its nominal size times dist/zc (screenR), so the near nodes are the
      // big ones and one nominal allowance for all of them is wrong by exactly
      // the factor MAXMAG bounds.
      function boxOf(ps) {
        var b = { x0: 1e9, y0: 1e9, x1: -1e9, y1: -1e9 }, seen = 0;
        for (var i = 0; i < ps.length; i++) {
          var p = project(ps[i]);
          if (!p) continue;
          seen++;
          var r = (ps[i][3] || 0) * (cam.dist / p[2]) * (Math.min(W, H) / 1500);
          b.x0 = Math.min(b.x0, p[0] - r); b.x1 = Math.max(b.x1, p[0] + r);
          b.y0 = Math.min(b.y0, p[1] - r); b.y1 = Math.max(b.y1, p[1] + r);
        }
        return seen ? b : null;
      }
      var saved = { dist: cam.dist, tx: cam.tx, ty: cam.ty, tz: cam.tz, panX: cam.panX, panY: cam.panY };
      cam.tx = out.tx; cam.ty = out.ty; cam.tz = out.tz;
      function fitsAt(d) {
        cam.dist = d; cam.panX = 0; cam.panY = 0;
        var b = boxOf(pts);
        return !!b && (b.x1 - b.x0) / 2 <= limX && (b.y1 - b.y0) / 2 <= limY;
      }
      // Correcting the distance by the error in the size — dist <- dist / scale
      // — cannot be made to work here, and that is arithmetic rather than
      // tuning. The projected size goes as 1/(dist - near), so the correction's
      // gain at the answer is 1 - dist/(dist - near): the near face's
      // magnification, negated. Any magnification worth having therefore
      // overshoots by more than it corrects, and each pass swings wider than the
      // last. Three passes hid it while the magnification stayed near two and
      // the swings stayed small. On a library wide enough to bring the camera in
      // close they reached half a screen of pan, and a third of the field fell
      // off the bottom of the glass.
      //
      // The size is monotone in the distance, so ask the monotone question
      // instead — does the whole picture fit from here? — and bisect on the
      // answer, which cannot diverge. Twenty halvings of a bracket a few world
      // units wide settle well inside a pixel, and the smallest distance that
      // fits is the largest the map can be drawn.
      var lo = minD, hi = MAXD;
      if (fitsAt(lo)) hi = lo;
      else if (!fitsAt(hi)) lo = hi;
      else for (var pass = 0; pass < 20; pass++) {
        var mid = (lo + hi) / 2;
        if (fitsAt(mid)) hi = mid; else lo = mid;
      }
      out.dist = clampD(hi);
      // Where the picture lands is a separate question from how big it is, and
      // it is asked at the distance just chosen: a pan measured at any other
      // distance corrects an error the zoom has already moved.
      cam.dist = out.dist; cam.panX = 0; cam.panY = 0;
      var m = boxOf(pts);
      if (m) {
        out.panX = cxDes - (m.x0 + m.x1) / 2;
        out.panY = cyDes - (m.y0 + m.y1) / 2;
      }
      cam.dist = saved.dist; cam.tx = saved.tx; cam.ty = saved.ty; cam.tz = saved.tz;
      cam.panX = saved.panX; cam.panY = saved.panY;
      return out;
    }
    function allPoints() {
      updateWorld();
      var out = [];
      for (var i = 0; i < N.length; i++) out.push(worldR(i));
      return out;
    }
    function frameOfCluster(cl) {
      if (!cl.members || !cl.members.length) return null;
      updateWorld();
      return fitTo(cl.members.map(worldR), 0.62);
    }

    function applyCam(c) {
      cam.tx = c.tx; cam.ty = c.ty; cam.tz = c.tz; cam.dist = c.dist;
      cam.panX = c.panX || 0; cam.panY = c.panY || 0;
    }
    function setCam(to) {
      if (!to) return;
      // An explicit move counts as attention: the ambient spin must not start up
      // mid-flight and carry the thing you just asked to look at off-centre.
      idleAt = performance.now();
      if (reduced) { applyCam(to); draw(); return; }
      camFrom = { tx: cam.tx, ty: cam.ty, tz: cam.tz, dist: cam.dist, panX: cam.panX, panY: cam.panY };
      camTo = to; camT0 = performance.now();
      kick();
    }

    function resize() {
      dpr = Math.min(2, window.devicePixelRatio || 1);
      W = wrap.clientWidth; H = wrap.clientHeight;
      if (!W || !H) return;
      cv.width = Math.round(W * dpr); cv.height = Math.round(H * dpr);
      gl.viewport(0, 0, cv.width, cv.height);
      if (labelCv) {
        labelCv.width = Math.round(W * dpr); labelCv.height = Math.round(H * dpr);
        lctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      }
      draw();
    }

    // The camera basis, worked out once a frame. project() is fine for the
    // dozens of points a fit measures; the thousands the field projects every
    // frame share this instead, rather than paying four trig calls each.
    var camCY = 1, camSY = 0, camCP = 1, camSP = 0, camK = 0, camOX = 0, camOY = 0;
    function setBasis() {
      camCY = Math.cos(cam.yaw); camSY = Math.sin(cam.yaw);
      camCP = Math.cos(cam.pitch); camSP = Math.sin(cam.pitch);
      camK = Math.min(W, H) * 0.5;
      camOX = (freeArea.x0 + freeArea.x1) / 2 + cam.panX;
      camOY = (freeArea.y0 + freeArea.y1) / 2 + cam.panY;
    }
    function project_all() {
      setBasis();
      for (var i = 0; i < N.length; i++) {
        var x = wx[i] - cam.tx, y = wy[i] - cam.ty, z = wz[i] - cam.tz;
        var x1 = x * camCY - z * camSY, z1 = x * camSY + z * camCY;
        var y1 = y * camCP - z1 * camSP, zc = (y * camSP + z1 * camCP) + cam.dist;
        if (zc < 0.05) { svis[i] = 0; sz[i] = 1e9; continue; }
        var sc = FOCAL / zc * camK;
        svis[i] = 1; sx[i] = camOX + x1 * sc; sy[i] = camOY - y1 * sc; sz[i] = zc; sr[i] = screenR(i, zc);
      }
      // Painter's algorithm: no depth buffer (the glow has to blend), so far
      // first and the near nodes land on top.
      order.sort(function (a, b) { return sz[b] - sz[a]; });
    }

    function draw() {
      if (!W || !H) return;
      var pal = palette(), surface = pal.surface, ink = pal.ink, cols = colours();
      measureFrame();
      updateWorld();
      project_all();

      var focus = pinned >= 0 ? pinned : hover;
      var hc = litCluster >= 0 ? litCluster : hoverCluster;
      var lit = focus >= 0 ? new Set([focus].concat(Array.from(nbr()[focus])))
        : (hc >= 0 && AC()[hc] ? new Set(AC()[hc].members) : null);
      // A hover is a preview and dims the rest gently; a selection commits.
      var dim = litCluster >= 0 ? 0.14 : (hoverCluster >= 0 || focus >= 0 ? 0.3 : 1);

      gl.clear(gl.COLOR_BUFFER_BIT);

      // AREA CLOUDS first of all: they are the ground the graph sits on, so
      // everything else draws over them.
      var ns = 0, A = AC(), E = edges(), lists = clusterEdges();
      // Every area drawing fog gets an equal share of the blob budget, so a
      // late area is never left with nothing because the earlier ones spent it.
      var drawn = 0;
      for (var di = 0; di < A.length; di++) {
        if (!A[di].ungrouped && A[di].members && A[di].members.length >= 2) drawn++;
      }
      var share = Math.max(32, Math.floor(MAX_SPLATS / Math.max(1, drawn)));
      for (var ci = 0; ci < A.length; ci++) {
        var cl = A[ci];
        // The Ungrouped bucket is a place techniques are kept, not a place they are:
        // fogging it would draw a body around techniques whose only shared property
        // is having nothing in common.
        if (cl.ungrouped || !cl.members || cl.members.length < 2) continue;
        var mem = cl.members, el = lists[ci];
        var ccx = 0, ccy = 0, ccz = 0, m, mi;
        for (m = 0; m < mem.length; m++) { mi = mem[m]; ccx += wx[mi]; ccy += wy[mi]; ccz += wz[mi]; }
        ccx /= mem.length; ccy /= mem.length; ccz /= mem.length;
        var rad = 0, depth = 0, seen = 0;
        for (m = 0; m < mem.length; m++) {
          mi = mem[m];
          var ddx = wx[mi] - ccx, ddy = wy[mi] - ccy, ddz = wz[mi] - ccz;
          rad = Math.max(rad, Math.sqrt(ddx * ddx + ddy * ddy + ddz * ddz));
          if (svis[mi]) { depth += sz[mi]; seen++; }
        }
        if (!seen) continue;                 // the whole area is behind the lens
        // Blob size: big enough that neighbouring splats merge into one body,
        // small enough that the body still follows the shape of the cluster
        // rather than rounding it off into a ball.
        var blob = Math.max(0.05, rad * 0.2);
        // The splats sit on the members and along the RELATIONS between them —
        // not on spokes to the centroid. Areas interpenetrate in the cube (a technique
        // can sit deep inside another area's territory), so a star of blobs to
        // the middle inflated every area into the same sphere and the map
        // disappeared into fog. Following the edges makes the body take the shape
        // the cluster actually has: dense where techniques cluster, drawn out into arms
        // where the relations run.
        var nPts = mem.length + el.length * 3;
        // An area with many techniques would otherwise stack many blobs into a solid
        // slab, so each one thins as the count grows: the body reads the same
        // whether an area holds four techniques or twenty.
        var on = (hc < 0 && focus < 0) || hc === cl.id || (focus >= 0 && nodeCl(focus) === cl.id);
        var alpha = 0.42 / Math.sqrt(nPts) * (on ? 1 : dim * 0.7);
        if (alpha < 0.002) continue;
        // HOW MANY of those blobs to actually draw. A splat is a camera-facing
        // quad whose radius grows as you approach it, so zooming in turns every
        // one of an area's few thousand blobs into something the size of the
        // window: the fill rate, not the geometry, is what falls over. So the
        // set is thinned by a power of two until the fog's total coverage fits a
        // budget of CLOUD_FILL windows, and each surviving blob is darkened to
        // exactly the opacity the ones it stands in for had together —
        // 1-(1-a)^k is the opacity of k layers of a. Same fog, a fraction of the
        // pixels, and the thinning shows nowhere, because it only bites once the
        // blobs are large enough to overlap many deep.
        var rs = blob * (FOCAL / (depth / seen)) * camK;
        var cover = nPts * Math.min(4 * rs * rs, W * H);
        var step = 1;
        while (step < 512 && (cover / step > CLOUD_FILL * W * H || nPts / step > share)) step *= 2;
        var a1 = 1 - Math.pow(1 - alpha, step);
        var ccol = pal.area[cl.id % pal.area.length];
        var cr = ccol[0] / 255, cg = ccol[1] / 255, cb = ccol[2] / 255;
        for (var t = 0; t < nPts && ns < MAX_SPLATS; t += step) {
          var px, py, pz;
          if (t < mem.length) { mi = mem[t]; px = wx[mi]; py = wy[mi]; pz = wz[mi]; }
          else {
            // The three points along a relation: its midpoint and its quarters.
            var q = t - mem.length, e2 = E[el[(q / 3) | 0]], u = q % 3 === 0 ? 0.5 : (q % 3 === 1 ? 0.25 : 0.75);
            var ea = e2.a, eb = e2.b;
            px = wx[ea] + (wx[eb] - wx[ea]) * u;
            py = wy[ea] + (wy[eb] - wy[ea]) * u;
            pz = wz[ea] + (wz[eb] - wz[ea]) * u;
          }
          var dx3 = px - cam.tx, dy3 = py - cam.ty, dz3 = pz - cam.tz;
          var xr = dx3 * camCY - dz3 * camSY, zr = dx3 * camSY + dz3 * camCY;
          var yr = dy3 * camCP - zr * camSP, zc3 = (dy3 * camSP + zr * camCP) + cam.dist;
          if (zc3 < 0.05) continue;
          var sc3 = FOCAL / zc3 * camK;
          var spx = camOX + xr * sc3, spy = camOY - yr * sc3, r0 = blob * sc3;
          if (spx + r0 < 0 || spx - r0 > W || spy + r0 < 0 || spy - r0 > H) continue;
          spX[ns] = spx; spY[ns] = spy; spZ[ns] = zc3; spR[ns] = r0;
          spR0[ns] = cr; spG[ns] = cg; spB[ns] = cb; spA[ns] = a1 * fog(zc3);
          ns++;
        }
      }
      if (ns) {
        var sOrder = splatOrder(ns);
        var fv = 0;
        for (var si = 0; si < ns; si++) {
          var sp = sOrder[si], r = spR[sp], cxp = spX[sp], cyp = spY[sp];
          // The quad is clamped to the canvas, and its unit-circle coordinates
          // are clamped with it. The coordinate is linear across the quad, so
          // this draws precisely the same blob — it just stops paying for the
          // part of it that hangs outside the window, which once you have zoomed
          // in is most of every blob.
          var qx0 = Math.max(-1, (0 - cxp) / r), qx1 = Math.min(1, (W - cxp) / r);
          var qy0 = Math.max(-1, (0 - cyp) / r), qy1 = Math.min(1, (H - cyp) / r);
          var vx0 = cxp + qx0 * r, vx1 = cxp + qx1 * r, vy0 = cyp + qy0 * r, vy1 = cyp + qy1 * r;
          var g0x = vx0 * dpr / cv.width * 2 - 1, g1x = vx1 * dpr / cv.width * 2 - 1;
          var g0y = 1 - vy0 * dpr / cv.height * 2, g1y = 1 - vy1 * dpr / cv.height * 2;
          var cr2 = spR0[sp], cg2 = spG[sp], cb2 = spB[sp], ca = spA[sp];
          // Two triangles: (x0,y0) (x1,y0) (x0,y1), then (x1,y0) (x1,y1) (x0,y1).
          fv = cloudVtx(fv, g0x, g0y, qx0, qy0, cr2, cg2, cb2, ca);
          fv = cloudVtx(fv, g1x, g0y, qx1, qy0, cr2, cg2, cb2, ca);
          fv = cloudVtx(fv, g0x, g1y, qx0, qy1, cr2, cg2, cb2, ca);
          fv = cloudVtx(fv, g1x, g0y, qx1, qy0, cr2, cg2, cb2, ca);
          fv = cloudVtx(fv, g1x, g1y, qx1, qy1, cr2, cg2, cb2, ca);
          fv = cloudVtx(fv, g0x, g1y, qx0, qy1, cr2, cg2, cb2, ca);
        }
        gl.useProgram(pCloud);
        gl.bindBuffer(gl.ARRAY_BUFFER, cloudBuf);
        gl.bufferData(gl.ARRAY_BUFFER, cloudArr.subarray(0, fv * 8), gl.DYNAMIC_DRAW);
        gl.enableVertexAttribArray(Fp); gl.vertexAttribPointer(Fp, 2, gl.FLOAT, false, 32, 0);
        gl.enableVertexAttribArray(Fq); gl.vertexAttribPointer(Fq, 2, gl.FLOAT, false, 32, 8);
        gl.enableVertexAttribArray(Fc); gl.vertexAttribPointer(Fc, 4, gl.FLOAT, false, 32, 16);
        gl.drawArrays(gl.TRIANGLES, 0, fv);
        gl.disableVertexAttribArray(Fq);
      }

      // EDGES next, so the nodes sit on their filaments rather than under them.
      var le = 0, hw = 1.55 * dpr, ends = cols.end, thin = thinning();
      var vw = cv.width, vh = cv.height;
      for (var ei = 0; ei < E.length; ei++) {
        var e = E[ei];
        if (!svis[e.a] || !svis[e.b]) continue;
        var strong = lit ? (lit.has(e.a) && lit.has(e.b)) : true;
        // One technique's own relations are drawn whole, crowded or not: asking for
        // a node's reach is asking to see all of it, and it is one node's worth.
        // A lit AREA keeps the thinning, because an area is a crowd by definition.
        var full = focus >= 0 && strong;
        var a = (0.14 + Math.min(0.34, e.w * 0.09)) * (full ? 1 : thin[ei] * (strong ? 1 : dim));
        if (a < 0.004) continue;
        a *= fog((sz[e.a] + sz[e.b]) * 0.5);
        var ax = sx[e.a] * dpr, ay = sy[e.a] * dpr, bxp = sx[e.b] * dpr, byp = sy[e.b] * dpr;
        // A relation between two techniques off opposite edges of the window is
        // a quad the length of the whole graph, drawn for the few hundred pixels
        // of it that show. Trimmed to the canvas it draws the same filament and
        // pays for what is on screen — which, zoomed in, is nearly none of it.
        var pdx = bxp - ax, pdy = byp - ay, pl = Math.sqrt(pdx * pdx + pdy * pdy);
        if (pl < 0.5) continue;
        var t0 = 0, t1 = 1, clipped = true;
        for (var side = 0; side < 4 && clipped; side++) {
          var pq = side === 0 ? -pdx : side === 1 ? pdx : side === 2 ? -pdy : pdy;
          var qq = side === 0 ? ax + hw : side === 1 ? vw - ax + hw : side === 2 ? ay + hw : vh - ay + hw;
          if (pq === 0) { if (qq < 0) clipped = false; continue; }
          var rr = qq / pq;
          if (pq < 0) { if (rr > t1) clipped = false; else if (rr > t0) t0 = rr; }
          else { if (rr < t0) clipped = false; else if (rr < t1) t1 = rr; }
        }
        if (!clipped) continue;
        var cax = ax + pdx * t0, cay = ay + pdy * t0;
        var cbx = ax + pdx * t1, cby = ay + pdy * t1;
        var ox = -pdy / pl * hw, oy = pdx / pl * hw;
        // Each end carries its own area's hue and the quad interpolates between
        // them, so a bridge between two areas is never grey anywhere along it.
        // The clip above moved the ends, so the colours are read at t0 and t1
        // rather than at the nodes — inline on the channels, because a mix()
        // here would allocate two arrays per relation per frame.
        var c0 = ends[e.a], c1 = ends[e.b];
        var dr = c1[0] - c0[0], dg = c1[1] - c0[1], db = c1[2] - c0[2];
        var e0r = (c0[0] + dr * t0) / 255, e0g = (c0[1] + dg * t0) / 255, e0b = (c0[2] + db * t0) / 255;
        var e1r = (c0[0] + dr * t1) / 255, e1g = (c0[1] + dg * t1) / 255, e1b = (c0[2] + db * t1) / 255;
        le = linePut(le, cax + ox, cay + oy, 1, e0r, e0g, e0b, a);
        le = linePut(le, cax - ox, cay - oy, -1, e0r, e0g, e0b, a);
        le = linePut(le, cbx + ox, cby + oy, 1, e1r, e1g, e1b, a);
        le = linePut(le, cbx + ox, cby + oy, 1, e1r, e1g, e1b, a);
        le = linePut(le, cax - ox, cay - oy, -1, e0r, e0g, e0b, a);
        le = linePut(le, cbx - ox, cby - oy, -1, e1r, e1g, e1b, a);
      }
      if (le) {
        gl.useProgram(pLine);
        gl.bindBuffer(gl.ARRAY_BUFFER, lineBuf);
        gl.bufferData(gl.ARRAY_BUFFER, lineArr.subarray(0, le * 7), gl.DYNAMIC_DRAW);
        gl.enableVertexAttribArray(Lp); gl.vertexAttribPointer(Lp, 2, gl.FLOAT, false, 28, 0);
        gl.enableVertexAttribArray(Ls); gl.vertexAttribPointer(Ls, 1, gl.FLOAT, false, 28, 8);
        gl.enableVertexAttribArray(Lc); gl.vertexAttribPointer(Lc, 4, gl.FLOAT, false, 28, 12);
        gl.drawArrays(gl.TRIANGLES, 0, le);
      }

      // NODES, far to near.
      var np = 0, ncols = cols.node;
      for (var k = 0; k < order.length; k++) {
        var i = order[k];
        if (!svis[i]) continue;
        var c = ncols[i];
        var al = fog(sz[i]) * (lit && !lit.has(i) ? dim : 1);
        if (focus === i) al = 1;
        var o = np * 8;
        pointArr[o] = sx[i] * dpr / cv.width * 2 - 1;
        pointArr[o + 1] = 1 - sy[i] * dpr / cv.height * 2;
        // The disc occupies 0.62 of the sprite's width (see the shader), so the
        // sprite is that much wider than the node — the rest is the halo.
        //
        // A disc still reads at three pixels; a star does not. So an org-specific
        // technique gets a floor under its sprite: the smallest nodes are precisely
        // where the old ring stopped being legible, and a mark that only says
        // "org" above some size says it nowhere it was needed. The floor bites
        // only below ~4px of screen radius, so it costs the size channel nothing
        // anywhere a size comparison was readable in the first place.
        var org = N[i].scope === 'org';
        pointArr[o + 2] = Math.max(org ? 12 * dpr : 3, sr[i] * 2 * dpr / 0.62);
        pointArr[o + 3] = c[0] / 255; pointArr[o + 4] = c[1] / 255; pointArr[o + 5] = c[2] / 255;
        pointArr[o + 6] = Math.min(1, al);
        pointArr[o + 7] = org ? 1 : 0;
        np++;
      }
      if (np) {
        gl.useProgram(pPoint);
        gl.bindBuffer(gl.ARRAY_BUFFER, pointBuf);
        gl.bufferData(gl.ARRAY_BUFFER, pointArr.subarray(0, np * 8), gl.DYNAMIC_DRAW);
        gl.enableVertexAttribArray(Pp); gl.vertexAttribPointer(Pp, 2, gl.FLOAT, false, 32, 0);
        gl.enableVertexAttribArray(Ps); gl.vertexAttribPointer(Ps, 1, gl.FLOAT, false, 32, 8);
        gl.enableVertexAttribArray(Pc); gl.vertexAttribPointer(Pc, 4, gl.FLOAT, false, 32, 12);
        gl.enableVertexAttribArray(Pr); gl.vertexAttribPointer(Pr, 1, gl.FLOAT, false, 32, 28);
        gl.drawArrays(gl.POINTS, 0, np);
      }
      drawLabels(ink, surface, pal, lit, focus, hc);
    }

    // Labels live on their own 2-D canvas over the scene: text through a point
    // sprite would mean an atlas and a lot of machinery to get worse type.
    function drawLabels(ink, surface, pal, lit, focus, hc) {
      if (!lctx) return;
      lctx.clearRect(0, 0, W, H);
      // Area names, at the projected centroid of each area's members. Nearest
      // first, and a name is dropped rather than drawn over one already placed —
      // two labels on top of each other name nothing.
      var taken = [];
      function free(x, y, w) {
        for (var t = 0; t < taken.length; t++) {
          var r = taken[t];
          if (Math.abs(x - r[0]) < (w + r[2]) / 2 + 6 && Math.abs(y - r[1]) < 22) return false;
        }
        return true;
      }
      var named = AC().map(function (cl) {
        if (cl.ungrouped) return null;   // a bucket of strays is not a place
        if (!cl.members || !cl.members.length) return null;
        var cx = 0, cy = 0, cz = 0, n = 0;
        cl.members.forEach(function (i) { if (svis[i]) { cx += sx[i]; cy += sy[i]; cz += sz[i]; n++; } });
        if (!n) return null;
        return { cl: cl, x: cx / n, y: cy / n, z: cz / n };
      }).filter(Boolean).sort(function (a, b) { return a.z - b.z; });
      lctx.font = '600 11px ui-sans-serif,system-ui,sans-serif';
      lctx.textAlign = 'center'; lctx.textBaseline = 'middle';
      named.forEach(function (it) {
        var label = short(it.cl.name || ''), wpx = lctx.measureText(label).width;
        if (!free(it.x, it.y, wpx)) return;
        taken.push([it.x, it.y, wpx]);
        var on = hc < 0 || hc === it.cl.id;
        var col = pal.area[it.cl.id % pal.area.length];
        var a = (on ? 0.92 : 0.18) * Math.max(0.45, fog(it.z));
        lctx.fillStyle = rgba(surface, 0.8 * a);
        roundRect(lctx, it.x - wpx / 2 - 6, it.y - 9, wpx + 12, 18, 9);
        lctx.fill();
        lctx.fillStyle = rgba(mix(col, ink, 0.3), a);
        lctx.fillText(label, it.x, it.y);
      });
      // The focused technique's name, and its neighbours' when there is room.
      if (focus >= 0 && svis[focus]) nodeLabel(focus, ink, surface, true);
      else if (lit) {
        var shown = 0;
        for (var k = order.length - 1; k >= 0 && shown < 14; k--) {
          var i = order[k];
          if (!svis[i] || !lit.has(i)) continue;
          if (sr[i] < 6) continue;
          nodeLabel(i, ink, surface, false); shown++;
        }
      }
    }
    function nodeLabel(i, ink, surface, strong) {
      var label = short(N[i].name || ''), x = sx[i], y = sy[i] - sr[i] - 10;
      lctx.font = (strong ? '600 12px' : '500 11px') + ' ui-sans-serif,system-ui,sans-serif';
      lctx.textAlign = 'center'; lctx.textBaseline = 'middle';
      var w = lctx.measureText(label).width;
      lctx.fillStyle = rgba(surface, strong ? 0.92 : 0.7);
      roundRect(lctx, x - w / 2 - 6, y - 9, w + 12, 18, 9);
      lctx.fill();
      lctx.fillStyle = rgba(ink, strong ? 0.95 : 0.65);
      lctx.fillText(label, x, y);
    }
    function roundRect(c, x, y, w, h, r) {
      c.beginPath();
      c.moveTo(x + r, y); c.arcTo(x + w, y, x + w, y + h, r); c.arcTo(x + w, y + h, x, y + h, r);
      c.arcTo(x, y + h, x, y, r); c.arcTo(x, y, x + w, y, r); c.closePath();
    }

    // --- the loop ---
    // Ambient rotation while nobody is touching it; it yields the moment a
    // pointer arrives and picks up again once the map has been left alone.
    function tick(now) {
      raf = 0;
      var t = now / 1000;
      lastT = t;
      tNow = t - t0;
      if (camTo) {
        var p = Math.min(1, (now - camT0) / CAM_MS);
        var e = p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2; // easeInOutQuad
        cam.tx = camFrom.tx + (camTo.tx - camFrom.tx) * e;
        cam.ty = camFrom.ty + (camTo.ty - camFrom.ty) * e;
        cam.tz = camFrom.tz + (camTo.tz - camFrom.tz) * e;
        cam.dist = camFrom.dist + (camTo.dist - camFrom.dist) * e;
        cam.panX = camFrom.panX + ((camTo.panX || 0) - camFrom.panX) * e;
        cam.panY = camFrom.panY + ((camTo.panY || 0) - camFrom.panY) * e;
        if (p >= 1) camTo = null;
      }
      // The field drifts only when nobody is using it. A pinned technique or a lit
      // area means someone is reading, and reading a moving target is work.
      var idle = !dragging && hover < 0 && pinned < 0 && litCluster < 0 && (now - idleAt) > 4000;
      if (idle && SPIN) cam.yaw += SPIN * (1 / 60);
      draw();
      if (!reduced) kick();
    }
    function kick() { if (!raf && !reduced && !hidden) raf = requestAnimationFrame(tick); }
    // What every input handler asks for. Painting straight from the handler
    // costs a whole extra frame's work while the loop is already running, and a
    // trackpad sends wheel events faster than the display refreshes — so a zoom
    // gesture, the one thing this field is slowest at, was the one thing that
    // asked it to draw twice. With motion off there is no loop, so the paint
    // has to happen here.
    function repaint() { if (reduced) draw(); else kick(); }

    // A dashboard tab left open in the background has no business animating.
    // Browsers already throttle rAF for hidden documents, but throttled is not
    // stopped: the loop still wakes, still projects every node, still repaints.
    // So it stops, and picks up where it left off.
    document.addEventListener('visibilitychange', function () {
      hidden = !!document.hidden;
      if (hidden) {
        if (raf) cancelAnimationFrame(raf);
        raf = 0;
        return;
      }
      // Roll the drift clock forward over the hidden stretch, so the field
      // resumes its breathing from where it stopped rather than snapping to
      // wherever the phase would have travelled while nobody was watching.
      var nowSec = performance.now() / 1000;
      if (lastT) t0 += nowSec - lastT;
      // And don't count time spent hidden as time spent idle, or the field is
      // already spinning by the moment the tab comes back.
      idleAt = performance.now();
      // A camera flight interrupted by the tab going away has no animation left
      // to run: land it on its destination rather than easing from a stale start.
      if (camTo) { applyCam(camTo); camTo = null; }
      kick();
    });

    function pick(mx, my) {
      var best = -1, bd = 1e9;
      for (var k = 0; k < order.length; k++) {   // near-first: order is far→near
        var i = order[order.length - 1 - k];
        if (!svis[i]) continue;
        var dx = mx - sx[i], dy = my - sy[i], d = Math.sqrt(dx * dx + dy * dy);
        if (d <= sr[i] + 5 && d < bd) { bd = d; best = i; }
      }
      return best;
    }

    // --- input: orbit, dolly, pick ---
    var down = false, moved = false, lx = 0, ly = 0, pinchD = 0;
    function pos(ev) {
      var r = cv.getBoundingClientRect();
      var t = ev.touches && ev.touches[0] ? ev.touches[0] : ev;
      return [t.clientX - r.left, t.clientY - r.top];
    }
    // Dragging grabs the FIELD, not the camera: pull down and the near face of
    // the cloud follows your hand down, the same way every orbit control worth
    // using behaves. The vertical term is negated for that reason — moving the
    // camera down (the other reading) sends the field up, away from the drag,
    // and every hand reads it as backwards.
    function orbit(dx, dy) {
      taken = true;
      cam.yaw += dx * 0.006;
      cam.pitch = Math.max(-1.35, Math.min(1.35, cam.pitch - dy * 0.006));
      idleAt = performance.now();
      repaint();
    }
    // An orbit is a press and a drag, which is also the browser's gesture for
    // selecting text. The field is the page's ground and the furniture floats on
    // it, so a drag that began on the map ran the selection through the count
    // line and the whole Areas overlay and painted them blue. preventDefault
    // stops the gesture where it starts; body.dragging covers everything the
    // pointer then crosses, for as long as the hand is down, and it carries the
    // -webkit- property iPad wants, which a userSelect set from script does not.
    function handOn() { document.body.classList.add('dragging'); }
    function handOff() { document.body.classList.remove('dragging'); }
    cv.addEventListener('mousedown', function (ev) {
      if (ev.button === 0) ev.preventDefault();
      handOn();
      down = true; dragging = true; moved = false; var p = pos(ev); lx = p[0]; ly = p[1];
    });
    window.addEventListener('mouseup', function () { down = false; dragging = false; handOff(); idleAt = performance.now(); });
    cv.addEventListener('mousemove', function (ev) {
      var p = pos(ev);
      if (down) {
        if (Math.abs(p[0] - lx) + Math.abs(p[1] - ly) > 2) moved = true;
        orbit(p[0] - lx, p[1] - ly); lx = p[0]; ly = p[1];
        return;
      }
      var h = pick(p[0], p[1]);
      if (h !== hover) { hover = h; cv.style.cursor = h >= 0 ? 'pointer' : 'grab'; idleAt = performance.now(); repaint(); }
    });
    cv.addEventListener('mouseleave', function () { if (hover >= 0) { hover = -1; repaint(); } });
    cv.addEventListener('click', function (ev) {
      if (moved) { moved = false; return; }   // a drag is not a click
      var p = pos(ev), i = pick(p[0], p[1]);
      pinned = (i >= 0 && i === pinned) ? -1 : i;
      detail(pinned); repaint();
    });
    cv.addEventListener('wheel', function (ev) {
      ev.preventDefault();
      taken = true;
      cam.dist = Math.max(MIND, Math.min(MAXD, cam.dist * Math.exp(ev.deltaY * 0.0012)));
      idleAt = performance.now(); repaint();
    }, { passive: false });
    cv.addEventListener('touchstart', function (ev) {
      handOn();
      dragging = true; moved = false;
      if (ev.touches.length === 2) {
        var a = ev.touches[0], b = ev.touches[1];
        pinchD = Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY);
      } else { var p = pos(ev); lx = p[0]; ly = p[1]; }
    }, { passive: true });
    cv.addEventListener('touchmove', function (ev) {
      if (ev.touches.length === 2) {
        var a = ev.touches[0], b = ev.touches[1];
        var d = Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY);
        if (pinchD > 0) { taken = true; cam.dist = Math.max(MIND, Math.min(MAXD, cam.dist * (pinchD / Math.max(1, d)))); }
        pinchD = d; moved = true; idleAt = performance.now(); repaint();
        ev.preventDefault();
        return;
      }
      var p = pos(ev);
      if (Math.abs(p[0] - lx) + Math.abs(p[1] - ly) > 3) moved = true;
      orbit(p[0] - lx, p[1] - ly); lx = p[0]; ly = p[1];
      ev.preventDefault();
    }, { passive: false });
    // touchcancel as well as touchend: a touch the system takes away — a call, a
    // gesture claimed by the browser — never ends, and the class would stick and
    // leave the whole page unselectable.
    cv.addEventListener('touchcancel', function () { dragging = false; pinchD = 0; handOff(); });
    cv.addEventListener('touchend', function (ev) {
      dragging = false; pinchD = 0; handOff(); idleAt = performance.now();
      if (moved) return;
      var t = ev.changedTouches && ev.changedTouches[0];
      if (!t) return;
      var r = cv.getBoundingClientRect(), i = pick(t.clientX - r.left, t.clientY - r.top);
      pinned = (i >= 0 && i === pinned) ? -1 : i;
      hover = -1; detail(pinned); repaint();
      ev.preventDefault();   // stop the ghost click re-picking
    }, { passive: false });

    cv.style.cursor = 'grab';
    // Reset to the whole field, or frame the lit area.
    function frame() {
      taken = false;
      var cl = litCluster >= 0 ? AC()[litCluster] : null;
      setCam(cl ? frameOfCluster(cl) : fitTo(allPoints(), 0.92));
      if (reduced) draw();
    }
    // Open on the whole field, framed. A fixed opening distance left the cloud
    // adrift in a corner on any graph whose extent wasn't the one it assumed.
    // The controller re-frames once the overlay has decided whether it is open,
    // because that changes how much frame there is to fill.
    function home() { taken = false; applyCam(fitTo(allPoints(), 0.92)); }
    home();
    resize();
    kick();
    return {
      kind: '3d',
      draw: repaint,
      resize: resize,
      frame: frame,
      // The frame changed on its own: re-fit for it, unless a hand is on the
      // camera. Either way the buffers have to follow the new size.
      reframe: function () { resize(); if (!taken) { home(); draw(); } },
      refit: function () { home(); draw(); },
      width: function () { return W; },
      screen: function (i) { return svis[i] ? [sx[i], sy[i]] : null; },
      reset: function () { hover = -1; }
    };
  }

  // ===========================================================================
  // FIELD: the flat one, for a browser with no WebGL. The 2-D map as it was —
  // hulls, edges, discs, a star for org scope — driven by the same controller.
  // ===========================================================================
  function canvasField() {
    var ctx = cv.getContext('2d');
    if (!ctx) return null;
    if (labelCv) labelCv.hidden = true;   // this field draws its own labels
    var W = 0, H = 0, dpr = 1;
    var view = { s: 1, tx: 0, ty: 0 }, animRAF = 0, animStart = 0, animFrom = null, animTo = { s: 1, tx: 0, ty: 0 };
    var maxR = Math.max.apply(null, N.map(outerR)) + 10;
    // The flat map lays its unit square into the free area, not the raw canvas:
    // full bleed puts page furniture over the top-left of the field here too, and
    // a hull drawn under the filter bar is a hull nobody can read.
    var fr = { x0: 0, y0: 0, x1: 0, y1: 0 };
    function px(nd) {
      var w = Math.max(2 * maxR + 1, fr.x1 - fr.x0), h = Math.max(2 * maxR + 1, fr.y1 - fr.y0);
      return [fr.x0 + maxR + nx(nd) * (w - 2 * maxR), fr.y0 + maxR + ny(nd) * (h - 2 * maxR)];
    }
    function spx(nd) { var p = px(nd); return [p[0] * view.s + view.tx, p[1] * view.s + view.ty]; }
    function areaView(cl) {
      var xs = [], ys = [];
      framed(W, H, fr);
      cl.members.forEach(function (i) {
        var p = px(N[i]), r = radius(N[i]);
        xs.push(p[0] - r); xs.push(p[0] + r); ys.push(p[1] - r); ys.push(p[1] + r);
      });
      var x0 = Math.min.apply(null, xs), x1 = Math.max.apply(null, xs);
      var y0 = Math.min.apply(null, ys), y1 = Math.max.apply(null, ys);
      var pad = 52, bw = Math.max(1, x1 - x0), bh = Math.max(1, y1 - y0);
      var fw = fr.x1 - fr.x0, fh = fr.y1 - fr.y0;
      var s = Math.max(1.05, Math.min(Math.min((fw - 2 * pad) / bw, (fh - 2 * pad) / bh), 3));
      return { s: s, tx: (fr.x0 + fr.x1) / 2 - (x0 + x1) / 2 * s, ty: (fr.y0 + fr.y1) / 2 - (y0 + y1) / 2 * s };
    }
    function frame() {
      var to = (litCluster >= 0 && AC()[litCluster]) ? areaView(AC()[litCluster]) : { s: 1, tx: 0, ty: 0 };
      if (Math.abs(to.s - view.s) < 0.002 && Math.abs(to.tx - view.tx) < 0.5 && Math.abs(to.ty - view.ty) < 0.5) { draw(); return; }
      animFrom = { s: view.s, tx: view.tx, ty: view.ty }; animTo = to; animStart = performance.now();
      if (!animRAF) animRAF = requestAnimationFrame(step);
    }
    function step(now) {
      var t = Math.min(1, (now - animStart) / 380), e = t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2;
      view.s = animFrom.s + (animTo.s - animFrom.s) * e;
      view.tx = animFrom.tx + (animTo.tx - animFrom.tx) * e;
      view.ty = animFrom.ty + (animTo.ty - animFrom.ty) * e;
      draw();
      if (t < 1) { animRAF = requestAnimationFrame(step); } else { animRAF = 0; view = animTo; draw(); }
    }
    function hull(pts) {
      if (pts.length < 3) return pts.slice();
      var p = pts.slice().sort(function (a, b) { return a[0] - b[0] || a[1] - b[1]; });
      function cr(o, a, b) { return (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0]); }
      var lo = [], up = [], i;
      for (i = 0; i < p.length; i++) { while (lo.length >= 2 && cr(lo[lo.length - 2], lo[lo.length - 1], p[i]) <= 0) lo.pop(); lo.push(p[i]); }
      for (i = p.length - 1; i >= 0; i--) { while (up.length >= 2 && cr(up[up.length - 2], up[up.length - 1], p[i]) <= 0) up.pop(); up.push(p[i]); }
      lo.pop(); up.pop(); return lo.concat(up);
    }
    function areaPad(cl) { return Math.max.apply(null, cl.members.map(function (i) { return radius(N[i]); })) + 16; }
    function starPath(x, y, outer, inner) {
      ctx.beginPath();
      for (var i = 0; i < 10; i++) {
        var rad = i % 2 ? inner : outer, ang = Math.PI / 5 * i - Math.PI / 2;
        var s2x = x + Math.cos(ang) * rad, s2y = y + Math.sin(ang) * rad;
        i ? ctx.lineTo(s2x, s2y) : ctx.moveTo(s2x, s2y);
      }
      ctx.closePath();
    }
    function roundRect(x, y, w, h, r) {
      ctx.beginPath();
      ctx.moveTo(x + r, y); ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
      ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r); ctx.closePath();
    }
    function resize() {
      dpr = Math.min(2, window.devicePixelRatio || 1);
      W = wrap.clientWidth; H = wrap.clientHeight;
      cv.width = W * dpr; cv.height = H * dpr; ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      markFrame(); framed(W, H, fr);
      view = (litCluster >= 0 && AC()[litCluster]) ? areaView(AC()[litCluster]) : { s: 1, tx: 0, ty: 0 };
      draw();
    }
    function draw() {
      if (!W || !H) return;
      var pal = palette(), surface = pal.surface, ink = pal.ink, cols = colours();
      framed(W, H, fr);
      ctx.clearRect(0, 0, W, H);
      var focus = pinned >= 0 ? pinned : hover;
      var hc = litCluster >= 0 ? litCluster : hoverCluster;
      var hovering = litCluster < 0 && hoverCluster >= 0;
      var lit = focus >= 0 ? new Set([focus].concat(Array.from(nbr()[focus])))
        : (hc >= 0 && AC()[hc] ? new Set(AC()[hc].members) : null);
      ctx.save();
      ctx.translate(view.tx, view.ty); ctx.scale(view.s, view.s);
      AC().forEach(function (cl) {
        if (!cl.members || !cl.members.length) return;
        var col = pal.area[cl.id % pal.area.length];
        var dimmed = (hc >= 0 && hc !== cl.id) || (focus >= 0 && nodeCl(focus) !== cl.id);
        var a = dimmed ? (hovering ? 0.04 : 0.012) : (hc === cl.id ? 0.15 : 0.08);
        var hl = hull(cl.members.map(function (i) { return px(N[i]); })), pad = areaPad(cl);
        ctx.beginPath();
        hl.forEach(function (p, k) { k ? ctx.lineTo(p[0], p[1]) : ctx.moveTo(p[0], p[1]); });
        if (hl.length > 2) ctx.closePath();
        ctx.lineJoin = 'round'; ctx.lineCap = 'round'; ctx.lineWidth = pad * 2;
        ctx.fillStyle = rgba(col, a); ctx.fill();
        ctx.strokeStyle = rgba(col, a); ctx.stroke();
      });
      // Relations: the same area hue and the same crowd thinning the 3-D field
      // uses. This used to be its own flat grey, which meant the flat map had a
      // second opinion about what a relation looks like, and the busier the
      // playbook the more of the map that opinion covered.
      var thin = thinning();
      edges().forEach(function (e, ei) {
        var strong = lit ? (lit.has(e.a) && lit.has(e.b)) : true;
        var full = focus >= 0 && strong;
        var a = (0.06 + Math.min(0.22, e.w * 0.06)) * (full ? 1 : thin[ei] * (strong ? 1 : 0.18));
        if (a < 0.004) return;
        var pa = px(N[e.a]), pb = px(N[e.b]), c0 = cols.end[e.a], c1 = cols.end[e.b];
        ctx.lineWidth = Math.min(2.4, 0.7 + e.w * 0.35);
        if (c0 === c1) {
          ctx.strokeStyle = rgba(c0, a);
          ctx.beginPath(); ctx.moveTo(pa[0], pa[1]); ctx.lineTo(pb[0], pb[1]); ctx.stroke();
          return;
        }
        // A bridge in two halves, each in its own end's hue: the flat map's
        // version of the 3-D field's gradient, and it costs two strokes rather
        // than a gradient object per relation per frame.
        var mx = (pa[0] + pb[0]) / 2, my = (pa[1] + pb[1]) / 2;
        ctx.strokeStyle = rgba(c0, a);
        ctx.beginPath(); ctx.moveTo(pa[0], pa[1]); ctx.lineTo(mx, my); ctx.stroke();
        ctx.strokeStyle = rgba(c1, a);
        ctx.beginPath(); ctx.moveTo(mx, my); ctx.lineTo(pb[0], pb[1]); ctx.stroke();
      });
      N.forEach(function (nd, i) {
        var p = px(nd), r = radius(nd), c = cols.node[i];
        var a = lit && !lit.has(i) ? 0.22 : 1;
        if (nd.scope === 'org') {
          ctx.fillStyle = rgba(c, a);
          starPath(p[0], p[1], r * 1.55, r * 0.72); ctx.fill();
        } else {
          ctx.beginPath(); ctx.arc(p[0], p[1], r, 0, Math.PI * 2);
          ctx.fillStyle = rgba(c, a); ctx.fill();
        }
        if (i === focus) {
          ctx.beginPath(); ctx.arc(p[0], p[1], r + 3.5, 0, Math.PI * 2);
          ctx.strokeStyle = rgba(ink, 0.75); ctx.lineWidth = 1.5; ctx.stroke();
        }
      });
      ctx.restore();
      // Labels in screen space, at a fixed readable size.
      if (focus >= 0) label(focus, ink, surface, true);
      else if (lit) {
        var n = 0;
        N.forEach(function (nd, i) { if (lit.has(i) && n < 12 && radius(nd) > 7) { label(i, ink, surface, false); n++; } });
      }
      AC().forEach(function (cl) {
        if (!cl.members || !cl.members.length) return;
        var xs = 0, ys = 0;
        cl.members.forEach(function (i) { var p = spx(N[i]); xs += p[0]; ys += p[1]; });
        areaLabel(cl, xs / cl.members.length, ys / cl.members.length, ink, surface, hc === cl.id);
      });
    }
    function label(i, ink, surface, strong) {
      var p = spx(N[i]), t = short(N[i].name || '');
      ctx.font = (strong ? '600 12px' : '500 11px') + ' ui-sans-serif,system-ui,sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      var w = ctx.measureText(t).width, y = p[1] - radius(N[i]) * view.s - 11;
      ctx.fillStyle = rgba(surface, strong ? 0.92 : 0.7);
      roundRect(p[0] - w / 2 - 6, y - 9, w + 12, 18, 9); ctx.fill();
      ctx.fillStyle = rgba(ink, strong ? 0.95 : 0.65);
      ctx.fillText(t, p[0], y);
    }
    function areaLabel(cl, x, y, ink, surface, strong) {
      var t = short(cl.name || '');
      ctx.font = '600 11px ui-sans-serif,system-ui,sans-serif';
      ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      var w = ctx.measureText(t).width;
      ctx.fillStyle = rgba(surface, strong ? 0.85 : 0.6);
      roundRect(x - w / 2 - 6, y - 9, w + 12, 18, 9); ctx.fill();
      ctx.fillStyle = rgba(ink, strong ? 0.9 : 0.55);
      ctx.fillText(t, x, y);
    }
    function nearest(mx, my) {
      var best = -1, bd = 1e9;
      N.forEach(function (nd, i) {
        var p = spx(nd), d = Math.hypot(mx - p[0], my - p[1]);
        if (d < bd) { bd = d; best = i; }
      });
      return [best, bd];
    }
    function hit(mx, my) {
      var r = nearest(mx, my);
      return (r[0] >= 0 && r[1] <= outerR(N[r[0]]) * view.s + 6) ? r[0] : -1;
    }
    cv.addEventListener('mousemove', function (ev) {
      var b = cv.getBoundingClientRect(), h = hit(ev.clientX - b.left, ev.clientY - b.top);
      if (h !== hover) { hover = h; cv.style.cursor = h >= 0 ? 'pointer' : 'default'; draw(); }
    });
    cv.addEventListener('mouseleave', function () { hover = -1; draw(); });
    cv.addEventListener('click', function (ev) {
      var b = cv.getBoundingClientRect(), i = hit(ev.clientX - b.left, ev.clientY - b.top);
      pinned = (i >= 0 && i === pinned) ? -1 : i;
      detail(pinned); draw();
    });
    cv.addEventListener('touchend', function (ev) {
      var t = ev.changedTouches && ev.changedTouches[0];
      if (!t) return;
      var b = cv.getBoundingClientRect(), i = hit(t.clientX - b.left, t.clientY - b.top);
      pinned = (i >= 0 && i === pinned) ? -1 : i;
      hover = -1; detail(pinned); draw();
      ev.preventDefault();
    }, { passive: false });
    resize();
    return {
      kind: '2d',
      draw: draw,
      resize: resize,
      frame: frame,
      reframe: resize,   // the flat field re-fits as part of resizing
      refit: resize,
      width: function () { return W; },
      screen: function (i) { return spx(N[i]); },
      reset: function () { hover = -1; }
    };
  }

  // --- pick a field ----------------------------------------------------------
  var field = webglField() || canvasField();
  if (!field) return;
  wrap.setAttribute('data-field', field.kind);
  if (field.kind !== '3d' && labelCv) labelCv.hidden = true;

  // ===========================================================================
  // CONTROLLER
  // ===========================================================================
  updateColorLegend();
  // Sync the Areas panels to the initial mode — the lists name different
  // arrangements, and a cohort-loaded page must not show the tag list.
  (function () {
    var at = document.getElementById('cmap-areas-tags'), ac = document.getElementById('cmap-areas-cohort');
    if (at) at.hidden = mode !== 'tags';
    if (ac) ac.hidden = mode !== 'cohort';
  })();

  function setAreaActive() {
    Array.prototype.forEach.call(document.querySelectorAll('.cmap-area-list li'), function (li) {
      li.classList.toggle('active', +li.dataset.cluster === litCluster);
    });
  }
  Array.prototype.forEach.call(document.querySelectorAll('.cmap-area-list li'), function (li) {
    var col = AREA_VAR[(+li.dataset.cluster) % AREA_VAR.length];
    var sw = document.createElement('i');
    sw.className = 'cmap-area-sw'; sw.style.background = 'var(' + col + ')';
    li.insertBefore(sw, li.firstChild);
    li.addEventListener('click', function () {
      litCluster = (litCluster === +li.dataset.cluster) ? -1 : +li.dataset.cluster;
      pinned = -1; hover = -1; detail(-1); setAreaActive(); field.frame();
    });
    // Hover previews an area — highlight only, never select or frame.
    li.addEventListener('mouseenter', function () { hoverCluster = +li.dataset.cluster; field.draw(); });
    li.addEventListener('mouseleave', function () { hoverCluster = -1; field.draw(); });
  });

  // The panel button toggles the one overlay that holds the Areas list and the
  // legend; it closes on a second click, on Escape, or on a click outside it.
  //
  // Opening or closing it changes how much room the graph has, so the framing
  // has to be recomputed — or the map slides sideways by the difference. It used
  // to get away without that: panX is measured from the free area's own centre,
  // and the fit aimed at exactly that centre, so the offset was zero and the
  // picture tracked the centre for free. The fit aims at the WINDOW's centre now
  // (fitTo), which is a real offset, and a stale one moves the map.
  var infoBtn = document.getElementById('cmap-info-btn');
  var panelSettled = false;
  function setPanel(open) {
    if (!controls || !infoBtn) return;
    markFrame();          // the overlay is half of what the free area is measured against
    controls.hidden = !open;
    infoBtn.classList.toggle('active', open);
    infoBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
    // The first call is the page deciding what to open with, and the observer
    // that fires just after it frames the map once anyway. Every call after
    // that is the reader, and the map re-frames where they can watch it happen.
    if (panelSettled && field && field.frame) field.frame();
  }
  if (infoBtn && controls) {
    infoBtn.addEventListener('click', function (e) { e.stopPropagation(); setPanel(controls.hidden); });
    controls.addEventListener('click', function (e) { e.stopPropagation(); });
    document.addEventListener('click', function () { if (!controls.hidden && window.innerWidth < 900) setPanel(false); });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && !controls.hidden) setPanel(false); });
    // Open by default where there is room for it to sit beside the map, closed
    // on a phone, where it would be the whole screen.
    setPanel(window.innerWidth >= 900);
    panelSettled = true;
  }

  // The arrangement control re-lays the SAME nodes out; no reload.
  Array.prototype.forEach.call(document.querySelectorAll('.cmap-seg button'), function (btn) {
    btn.addEventListener('click', function () {
      if (btn.dataset.mode === mode) return;
      mode = btn.dataset.mode;
      // The URL is the single grouping state: reflect the choice there so a
      // refresh or share keeps it, and so the view-switcher (which carries
      // ?group=) hands it to the list. No localStorage, no redirect-on-load flash.
      try { var u = new URL(location.href); u.searchParams.set('group', mode); history.replaceState(null, '', u); } catch (e) { }
      Array.prototype.forEach.call(document.querySelectorAll('.cmap-seg button'), function (b) {
        var on = b.dataset.mode === mode;
        b.classList.toggle('active', on); b.setAttribute('aria-pressed', on);
      });
      var at = document.getElementById('cmap-areas-tags'), ac = document.getElementById('cmap-areas-cohort');
      if (at) at.hidden = mode !== 'tags';
      if (ac) ac.hidden = mode !== 'cohort';
      updateColorLegend();
      hover = -1; pinned = -1; litCluster = -1; hoverCluster = -1;
      field.reset(); setAreaActive(); detail(-1); field.frame();
    });
  });

  // A deep link names an area: /techniques/map?area=Name. Every area link on Outcomes
  // arrives this way — the heatmap's column headers, the "Areas gaining adoption"
  // bars, the gap and proponent chips. Select it exactly as a click on the list
  // would, so the map opens already framed on the area.
  (function () {
    var want;
    try { want = (new URL(location.href)).searchParams.get('area'); } catch (e) { }
    if (!want) return;
    var hit = AC().filter(function (c) { return (c.name || '').toLowerCase() === want.toLowerCase(); })[0];
    if (!hit) return;   // an area this filter or arrangement doesn't hold: just show the map
    litCluster = hit.id; setAreaActive(); field.frame();
  })();

  // The overlay is open or closed by now, and any deep-linked area is selected:
  // frame the field for the space it actually ended up with.
  if (litCluster < 0) field.refit();

  new ResizeObserver(function () { markFrame(); field.reframe(); }).observe(wrap);
  // The page scrolling or reflowing moves the furniture the field fits itself
  // around, so the cached measurement goes with it.
  window.addEventListener('scroll', markFrame, { passive: true });
  window.addEventListener('resize', markFrame);
  // A theme change is the one thing that moves the palette, so it is also the
  // one thing that drops it.
  var repaint = function () { palCache = null; field.draw(); };
  new MutationObserver(repaint).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  var mq = matchMedia('(prefers-color-scheme:dark)');
  var onScheme = repaint;
  if (mq.addEventListener) mq.addEventListener('change', onScheme); else mq.addListener(onScheme);
})();
