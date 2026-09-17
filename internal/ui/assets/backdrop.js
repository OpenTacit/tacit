// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The sign-in view's ambient WebGL scene: an emergent knowledge graph. Points
// (latent practices) drift in a slowly rotating 3-D field; when two pass near, a
// faint filament joins them; every few seconds a node lights and its activation
// diffuses outward along the edges before fading — the Tacit loop, felt more than
// seen: discover, validate, spread. It is decorative (aria-hidden,
// pointer-events:none, behind the opaque technique) but for one answer it gives back:
// the travellers keep their distance from the pointer. It is theme-derived (hues
// from --accent), and degrades cleanly: no WebGL leaves the plain background, and
// prefers-reduced-motion renders a single still frame and binds no pointer
// listeners at all. Raw WebGL, no library — the registry ships no JS dependencies.
//
// One document loads it: the front door renders the canvas in its markup and the
// scene runs, at one steady speed, for as long as the visitor is there. It plays
// no part in the sign-in navigation — nothing accelerates, nothing is carried
// across to the page the visitor lands on.
(function(){
// The canvas is found by attribute, not by id: two documents render this field
// now — the registry's front door and the project page (internal/ui/site.go) —
// and naming the element after one of them was what kept it in one place.
var cv=document.querySelector('canvas[data-backdrop]');if(!cv)return;
var gl=cv.getContext('webgl',{alpha:true,antialias:true,depth:false,premultipliedAlpha:true});if(!gl)return;
function sh(t,s){var o=gl.createShader(t);gl.shaderSource(o,s);gl.compileShader(o);return gl.getShaderParameter(o,gl.COMPILE_STATUS)?o:null;}
function prog(v,f){var a=sh(gl.VERTEX_SHADER,v),b=sh(gl.FRAGMENT_SHADER,f);if(!a||!b)return null;var p=gl.createProgram();gl.attachShader(p,a);gl.attachShader(p,b);gl.linkProgram(p);return gl.getProgramParameter(p,gl.LINK_STATUS)?p:null;}
var pPoint=prog('attribute vec2 p;attribute float s;attribute vec4 c;varying vec4 v;void main(){gl_Position=vec4(p,0.,1.);gl_PointSize=s;v=c;}','precision mediump float;varying vec4 v;void main(){float m=smoothstep(.5,.1,length(gl_PointCoord-vec2(.5)));float a=v.a*m;gl_FragColor=vec4(v.rgb*a,a);}');
// Edges are drawn as camera-facing quads (GL line width is capped at 1px), with
// s in [-1,1] across the width; the fragment fades alpha from the core outward
// for a soft glow, so a span reads thicker and luminous rather than hairline.
var pLine=prog('attribute vec2 p;attribute float s;attribute vec4 c;varying vec4 v;varying float vs;void main(){gl_Position=vec4(p,0.,1.);v=c;vs=s;}','precision mediump float;varying vec4 v;varying float vs;void main(){float g=1.0-abs(vs);g*=g;float a=v.a*g;gl_FragColor=vec4(v.rgb*a,a);}');
if(!pPoint||!pLine)return;
var Pp=gl.getAttribLocation(pPoint,'p'),Ps=gl.getAttribLocation(pPoint,'s'),Pc=gl.getAttribLocation(pPoint,'c');
var Lp=gl.getAttribLocation(pLine,'p'),Ls=gl.getAttribLocation(pLine,'s'),Lc=gl.getAttribLocation(pLine,'c');
var pointBuf=gl.createBuffer(),lineBuf=gl.createBuffer();
// Travellers: a handful of logo-shaped tokens ("individuals") that ride just
// above the nodes and hop along an existing edge to a neighbouring node about
// once a second. The point sprite draws the Tacit mark from gl_PointCoord — a
// filled diamond core framed by two chevrons (sg = distance to a stroke segment)
// — so each reads as someone traversing the graph of techniques.
var pTrav=prog('attribute vec2 p;attribute float s;attribute vec4 c;varying vec4 v;void main(){gl_Position=vec4(p,0.,1.);gl_PointSize=s;v=c;}','precision mediump float;varying vec4 v;float sg(vec2 p,vec2 a,vec2 b){vec2 pa=p-a,ba=b-a;float h=clamp(dot(pa,ba)/dot(ba,ba),0.,1.);return length(pa-ba*h);}void main(){vec2 c=(gl_PointCoord-vec2(.5))*1.15;c.y=-c.y;float df=1.-smoothstep(.12,.225,abs(c.x)+abs(c.y));float lc=min(sg(c,vec2(-.417,0.),vec2(-.125,.292)),sg(c,vec2(-.417,0.),vec2(-.125,-.292)));float rc=min(sg(c,vec2(.417,0.),vec2(.125,.292)),sg(c,vec2(.417,0.),vec2(.125,-.292)));float st=1.-smoothstep(.016,.066,min(lc,rc));float a=v.a*max(df,st);gl_FragColor=vec4(v.rgb*a,a);}');
if(!pTrav)return;
var Tp=gl.getAttribLocation(pTrav,'p'),Ts=gl.getAttribLocation(pTrav,'s'),Tc=gl.getAttribLocation(pTrav,'c'),travBuf=gl.createBuffer();
// Colour is by CONNECTED SET, not position: each node takes its component's
// colour, drawn from the component's lowest-index member — a stable
// representative found by union-find. A set keeps its colour while that member
// stays in it; a node joining or leaving recolours to its set. Because
// components form and dissolve as nodes drift, colours track the shifting
// connectivity instead of being pinned to a screen region. hsl() builds the rgb;
// theme() picks saturation/lightness per mode and the flare a lit node mixes
// toward (white on dark, near-black on light) so the diffusion pulse stays seen.
function hsl(h,s,l){if(s===0)return[l,l,l];var q=l<.5?l*(1+s):l+s-l*s,p=2*l-q;function f(t){if(t<0)t+=1;if(t>1)t-=1;if(t<1/6)return p+(q-p)*6*t;if(t<.5)return q;if(t<2/3)return p+(q-p)*(2/3-t)*6;return p;}return[f(h+1/3),f(h),f(h-1/3)];}
var N=200,nodes=[],act=new Float32Array(N),nodeR=new Float32Array(N),nodeG=new Float32Array(N),nodeB=new Float32Array(N),hue=new Float32Array(N),parent=new Int16Array(N),INT=1,flare=[1,1,1],accent=[.224,.529,.898],SAT=.62,LIT=.62,i;
function rnd(){return Math.random()*2-1;}
function find(x){while(parent[x]!==x){parent[x]=parent[parent[x]];x=parent[x];}return x;}
// Fly-through starfield: nodes are scattered through a deep corridor (depth ZN..ZF,
// transverse ±BX) and stream toward the camera every frame; any that crosses the near
// plane recycles to the far plane with a fresh position/colour, so the field never
// runs out. Each node's intrinsic hue (used when it represents a set) is golden-ratio
// spaced so representatives with adjacent indices land far apart on the wheel.
var SPEED=0.32,ZN=0.35,ZF=6.0,FOCAL=1.1,BX=1.9;
// WARP multiplies the fly-through speed, and is 1 for the whole of an ordinary
// visit. Committing to sign in (tacit:launch, signinScript) sets WARPING and the
// corridor picks up: it compounds each frame — acceleration, not a jump to a new
// speed — up to WMAX, and otherwise eases back toward 1. A modest WMAX on
// purpose: enough that the click reads as a departure over the 340ms before the
// browser leaves, not a dive. WMAX*SPEED*dt stays far inside the corridor depth,
// so the recycle at the near plane still catches every node.
var WARP=1,WARPING=0,WACC=4.5,WMAX=4,WDEC=2.2;
for(i=0;i<N;i++){hue[i]=(i*.6180339887)%1;nodes.push({x:rnd()*BX,y:rnd()*BX,z:ZN+Math.random()*(ZF-ZN),px:Math.random()*6.28,py:Math.random()*6.28,pz:Math.random()*6.28,fx:.12+Math.random()*.12,fy:.1+Math.random()*.12,fz:.11+Math.random()*.1});}
// A running scene picks up new colours on its next frame, so theme() only has to
// set them. A reduced-motion reader has no next frame — the scene is one still
// image — and switching light/dark left them looking at the old palette's field
// over the new page. repaint is how that frame gets redrawn: null while the loop
// is running (which would be a wasted draw), set by the still-frame branch below.
var repaint=null;
function theme(){var dt=document.documentElement.getAttribute('data-theme');
var dark=dt==='dark'||(dt!=='light'&&window.matchMedia&&matchMedia('(prefers-color-scheme:dark)').matches);
SAT=dark?.88:.78;LIT=dark?.60:.42;flare=dark?[.86,1,1]:[.04,.06,.10];accent=dark?[.345,.651,1.0]:[.122,.435,.922];INT=dark?1.0:0.95;
if(repaint)repaint();}
theme();
if(window.MutationObserver)new MutationObserver(theme).observe(document.documentElement,{attributes:true,attributeFilter:['data-theme']});
if(window.matchMedia){var mq=matchMedia('(prefers-color-scheme:dark)');mq.addEventListener?mq.addEventListener('change',theme):mq.addListener&&mq.addListener(theme);}
// R is deliberately below the giant-component threshold (~0.29 here): a dense
// web would be one connected set, hence one colour. This fragments the field
// into many small/medium sets, each its own colour, none dominating.
var R=0.44,DRIFT=0.04,S=0.82,SPREAD=0.5,DECAY=0.985,SEED=2.8;
var lineArr=new Float32Array(N*(N-1)*21),pointArr=new Float32Array(N*7);
var sx=new Float32Array(N),sy=new Float32Array(N),sp=new Float32Array(N),sv=new Float32Array(N);
var DX=new Float32Array(N),DY=new Float32Array(N),DZ=new Float32Array(N);
var maxE=N*(N-1)/2,EI=new Int16Array(maxE),EJ=new Int16Array(maxE),EW=new Float32Array(maxE),sumW=new Float32Array(N),inflow=new Float32Array(N);
// conn: which pairs were joined last frame (for detecting a NEW span). estabT:
// the time a span first formed, so it can be drawn growing from one node to the
// other over DUR seconds. ROFF>R gives hysteresis so a pair at the boundary
// doesn't strobe (and re-animate).
var conn=new Uint8Array(N*N),estabT=new Float32Array(N*N),DUR=0.5,ROFF=R*1.12,primed=false;
// Travellers: current (home) node, target node, phase (0 idle / 1 hopping), hop
// start time and duration, and the time an idle traveller next sets off — each
// randomised per traveller so they never move in lockstep. travArr holds their
// point vertices; ntv is how many are live this frame. NT is the live count: at
// 30 the graph reads as busy rather than as a few tokens wandering it, which is
// the point of the scene. NTMAX only bounds the buffers the dev panel can fill.
var NTMAX=100,NT=30,tCur=new Int16Array(NTMAX),tTo=new Int16Array(NTMAX),tPh=new Uint8Array(NTMAX),tT0=new Float32Array(NTMAX),tDur=new Float32Array(NTMAX),tNx=new Float32Array(NTMAX),travArr=new Float32Array(NTMAX*7),ntv=0;
for(i=0;i<NTMAX;i++){tCur[i]=(Math.random()*N)|0;tPh[i]=0;tNx[i]=.4+Math.random()*1.4;}
// The one thing the backdrop reacts to: travellers keep their distance from the
// pointer (or the finger held down). pmx/pmy track it in the same NDC the nodes
// project into, pmOn eases 0..1 so arriving and leaving fade rather than snap, and
// pmWant is where pmOn is headed. AVR is the radius they mind, measured in units of
// half the canvas HEIGHT and multiplied out by ASP on the x axis so it stays a
// circle on screen rather than an ellipse; AVP is how far a cornered one leans away.
var pmx=0,pmy=0,pmOn=0,pmWant=0,AVR=.85,AVP=.3;
// Comet trail: a per-traveller ring buffer of recent screen positions (trX/trY,
// capacity TR, head trH, trN samples filled so far). While a traveller hops it
// emits TL soft discs sampled TSTEP frames apart into the past, fading in size
// and alpha — a wake pointing back to where it set off. trailArr feeds pPoint.
var TR=20,TL=6,TSTEP=3,trX=new Float32Array(NTMAX*TR),trY=new Float32Array(NTMAX*TR),trH=0,trN=0,trailArr=new Float32Array(NTMAX*TL*7),ntr=0;
var W,H,DPR,ASP;
// Per-layer render toggles, flipped by the dev panel (see bottom of the IIFE). All
// on by default; draw() honours them each frame so a dev can isolate what costs what.
var TOG={nodes:1,edges:1,travellers:1,trails:1};
// Size the drawing buffer to the CSS box times DPR. Guard against the zero/stale
// dimensions iOS briefly reports mid-rotation, and only touch the buffer when it
// actually changed (resizing it clears it).
function resize(){DPR=Math.min(window.devicePixelRatio||1,1.75);var w=cv.clientWidth||window.innerWidth,h=cv.clientHeight||window.innerHeight;if(!w||!h)return;W=w;H=h;var cw=Math.round(w*DPR),ch=Math.round(h*DPR);if(cv.width!==cw||cv.height!==ch){cv.width=cw;cv.height=ch;}gl.viewport(0,0,cv.width,cv.height);ASP=cv.width/cv.height;}
// On orientation change iOS fires before the layout (and sometimes devicePixel-
// Ratio) has settled, so re-apply now, next frame, and after a beat to catch the
// final size — otherwise the buffer keeps the old aspect and renders blurry.
function reflow(){resize();requestAnimationFrame(resize);setTimeout(resize,180);setTimeout(resize,450);}
resize();window.addEventListener('resize',reflow);window.addEventListener('orientationchange',reflow);if(window.visualViewport)window.visualViewport.addEventListener('resize',reflow);
gl.enable(gl.BLEND);gl.blendFunc(gl.ONE,gl.ONE_MINUS_SRC_ALPHA);gl.clearColor(0,0,0,0);
function fog(p){return .3+.7*Math.min(1,Math.max(0,(p-.18)/1.1));}
var lastSeed=-99,prevT=0;
function step(t){
// Advance the fly-through: step every node toward the camera by SPEED*dt, recycling
// any that crosses the near plane back to the far plane (fresh x/y, hue, activation)
// so the stream is endless. Keep the world coords (DX/DY/DZ) for adjacency and the
// perspective-projected screen coords (sx/sy) for drawing. A slow auto-sway nudges the
// vanishing point, parallax-scaled by depth. Nodes take no notice of the pointer
// (no hover repulsion, no drag) — they just travel. Only the travellers dodge it.
var dt=Math.min(.05,Math.max(0,t-prevT));prevT=t;
if(WARPING)WARP=Math.min(WMAX,WARP*Math.exp(dt*WACC));
else if(WARP>1.001)WARP=1+(WARP-1)*Math.exp(-dt*WDEC);
pmOn+=(pmWant-pmOn)*Math.min(1,dt*7);
var swX=.05*Math.sin(t*.05),swY=.045*Math.sin(t*.037),n;
for(i=0;i<N;i++){n=nodes[i];
n.z-=SPEED*WARP*dt;
if(n.z<ZN){n.z+=(ZF-ZN);n.x=rnd()*BX;n.y=rnd()*BX;hue[i]=Math.random();act[i]=0;n.px=Math.random()*6.28;n.py=Math.random()*6.28;}
var X=n.x+DRIFT*Math.sin(t*n.fx+n.px),Y=n.y+DRIFT*Math.sin(t*n.fy+n.py),Z=n.z;
DX[i]=X;DY[i]=Y;DZ[i]=Z;
var p=FOCAL/Z,dx=X*p*S+swX*p*.3,dy=Y*p*S+swY*p*.3;
sx[i]=dx;sy[i]=dy;sp[i]=p;
// Edge-only vignette (soft fade past the frame edge) times a spawn fade so recycled
// nodes ease in from the far plane instead of popping into existence.
var ed=Math.abs(dx)>Math.abs(dy)?Math.abs(dx):Math.abs(dy);sv[i]=Math.max(0,Math.min(1,(1.14-ed)/.24))*Math.min(1,(ZF-n.z)/1.2);}
if(t-lastSeed>SEED){lastSeed=t;act[(Math.random()*N)|0]=1;}
// edges by drifting-world proximity, with R->ROFF hysteresis so a pair sitting
// on the boundary doesn't strobe (and re-trigger). When a pair first crosses
// into R, stamp the time so the span can be drawn growing from one node to the
// other (pass 2). On the priming frame the pre-existing web is stamped
// already-established, so it doesn't all animate in at once on load.
var ne=0,j;for(i=0;i<N;i++){sumW[i]=0;parent[i]=i;}
for(i=0;i<N;i++)for(j=i+1;j<N;j++){
var dx=DX[i]-DX[j],dy=DY[i]-DY[j],dz=(DZ[i]-DZ[j])*.5,d=Math.sqrt(dx*dx+dy*dy+dz*dz),key=i*N+j,cn=0;
if(d<R){if(!conn[key])estabT[key]=primed?t:-999;cn=1;}
else if(d<ROFF&&conn[key])cn=1;
conn[key]=cn;
if(cn){var w=1-d/R;if(w<0)w=0;EI[ne]=i;EJ[ne]=j;EW[ne]=w;ne++;sumW[i]+=w;sumW[j]+=w;var ra=find(i),rb=find(j);if(ra<rb)parent[rb]=ra;else if(rb<ra)parent[ra]=rb;}}
primed=true;
// colour each node by its connected set: the hue of the set's lowest-index node.
for(i=0;i<N;i++){var c=hsl(hue[find(i)],SAT,LIT);nodeR[i]=c[0];nodeG[i]=c[1];nodeB[i]=c[2];}
// diffuse activation along edges (conservation with loss: total decays as DECAY
// each frame, so every pulse fades; SPREAD is the fraction redistributed).
for(i=0;i<N;i++)inflow[i]=0;
var le=0,Wd=cv.width,Hd=cv.height,hw2=2.6*DPR;
for(var k=0;k<ne;k++){var a=EI[k],b=EJ[k],w=EW[k];
if(sumW[a]>0)inflow[b]+=act[a]*SPREAD*(w/sumW[a]);
if(sumW[b]>0)inflow[a]+=act[b]*SPREAD*(w/sumW[b]);
var am=(act[a]+act[b])*.5,vg=Math.min(sv[a],sv[b]);
// grow the span from a to b over DUR with an eased tip; a soft draw-in glow
// (env, fading out as it completes) keeps it visible while the endpoints are
// still near the connect radius, then it settles into the proximity alpha.
var pr=(t-estabT[a*N+b])/DUR;if(pr>1)pr=1;var e=pr*pr*(3-2*pr),env=pr<.7?1:(1-pr)/.3;
var pers=w*1.35*fog(Math.min(sp[a],sp[b]))+am*w*1.4,est=.32*env,ea=Math.min(1,INT*(pers>est?pers:est)*vg);
if(ea>.004){var m=Math.min(1,am),br=(nodeR[a]+nodeR[b])*.5,bg=(nodeG[a]+nodeG[b])*.5,bb=(nodeB[a]+nodeB[b])*.5,cr=br+(flare[0]-br)*m,cg=bg+(flare[1]-bg)*m,cb=bb+(flare[2]-bb)*m,ax=sx[a],ay=sy[a],bx=sx[a]+(sx[b]-sx[a])*e,by=sy[a]+(sy[b]-sy[a])*e;
// widen the span into a quad, offset perpendicular by hw2 device px (aspect-
// corrected so thickness is uniform at any angle); s=±1 across the width drives
// the fragment glow. Six vertices = two triangles.
var pdx=(bx-ax)*Wd,pdy=(by-ay)*Hd,pl=Math.sqrt(pdx*pdx+pdy*pdy);
if(pl>1e-4){var ox2=-pdy/pl*hw2*2/Wd,oy2=pdx/pl*hw2*2/Hd,o=le*7;
lineArr[o]=ax+ox2;lineArr[o+1]=ay+oy2;lineArr[o+2]=1;lineArr[o+3]=cr;lineArr[o+4]=cg;lineArr[o+5]=cb;lineArr[o+6]=ea;
lineArr[o+7]=ax-ox2;lineArr[o+8]=ay-oy2;lineArr[o+9]=-1;lineArr[o+10]=cr;lineArr[o+11]=cg;lineArr[o+12]=cb;lineArr[o+13]=ea;
lineArr[o+14]=bx+ox2;lineArr[o+15]=by+oy2;lineArr[o+16]=1;lineArr[o+17]=cr;lineArr[o+18]=cg;lineArr[o+19]=cb;lineArr[o+20]=ea;
lineArr[o+21]=ax-ox2;lineArr[o+22]=ay-oy2;lineArr[o+23]=-1;lineArr[o+24]=cr;lineArr[o+25]=cg;lineArr[o+26]=cb;lineArr[o+27]=ea;
lineArr[o+28]=bx-ox2;lineArr[o+29]=by-oy2;lineArr[o+30]=-1;lineArr[o+31]=cr;lineArr[o+32]=cg;lineArr[o+33]=cb;lineArr[o+34]=ea;
lineArr[o+35]=bx+ox2;lineArr[o+36]=by+oy2;lineArr[o+37]=1;lineArr[o+38]=cr;lineArr[o+39]=cg;lineArr[o+40]=cb;lineArr[o+41]=ea;le+=6;}}}
for(i=0;i<N;i++)act[i]=act[i]*DECAY*(1-SPREAD)+inflow[i]*DECAY;
for(i=0;i<N;i++){var a=act[i],m=Math.min(1,a),o=i*7,ps=6+sp[i]*10;if(ps>46)ps=46;
pointArr[o]=sx[i];pointArr[o+1]=sy[i];pointArr[o+2]=(ps+a*14)*DPR;
pointArr[o+3]=nodeR[i]+(flare[0]-nodeR[i])*m;pointArr[o+4]=nodeG[i]+(flare[1]-nodeG[i])*m;pointArr[o+5]=nodeB[i]+(flare[2]-nodeB[i])*m;
pointArr[o+6]=Math.min(1,INT*(fog(sp[i])+a*.8)*sv[i]);}
// Advance travellers: an idle one whose time has come sets off along a random
// edge currently incident to its home node (reservoir pick, so uniform among the
// neighbours); a hopping one eases toward its target and, on arrival, adopts it
// as home and schedules its next hop. A node with no live edge just waits and
// retries. Positions come from the final node screen coords (sx/sy) lifted a
// little in NDC so the token rides above the node instead of covering it.
ntv=0;ntr=0;var LIFT=52/H;
for(var ti=0;ti<NT;ti++){
// On arrival, light up the technique just reached: the traveller injects activation
// that then diffuses along the edges next frame, so individuals moving through
// the graph are what make the techniques they touch glow.
if(tPh[ti]===1&&(t-tT0[ti])/tDur[ti]>=1){tCur[ti]=tTo[ti];tPh[ti]=0;tNx[ti]=t+.6+Math.random();act[tCur[ti]]=1;}
// A traveller sitting inside the pointer's radius doesn't wait out its timer: it
// bolts, and along the edge whose far end lies furthest from the pointer rather
// than the usual uniform pick — so a cursor herds them instead of scattering them.
// The quarter-second since its last arrival keeps a cornered one from strobing
// between two nodes that are both too close for comfort.
var spooked=0;
if(pmOn>.05&&tPh[ti]===0&&t>=tT0[ti]+tDur[ti]+.25){var hdx=(sx[tCur[ti]]-pmx)*ASP,hdy=sy[tCur[ti]]+LIFT-pmy;if(hdx*hdx+hdy*hdy<AVR*AVR)spooked=1;}
if(tPh[ti]===0&&(spooked||t>=tNx[ti])){var cur=tCur[ti],cnt=0,pick=-1,far=-1;for(var e=0;e<ne;e++){var nb=EI[e]===cur?EJ[e]:(EJ[e]===cur?EI[e]:-1);if(nb>=0){
if(spooked){var bx=(sx[nb]-pmx)*ASP,by=sy[nb]+LIFT-pmy,bd=bx*bx+by*by;if(bd>far){far=bd;pick=nb;}}
else{cnt++;if(Math.random()*cnt<1)pick=nb;}}}
if(pick>=0){tTo[ti]=pick;tPh[ti]=1;tT0[ti]=t;tDur[ti]=spooked?.32+Math.random()*.2:.7+Math.random()*.6;}else tNx[ti]=t+.3+Math.random()*.5;}
var tpx,tpy,tn;
if(tPh[ti]===1){var pr=(t-tT0[ti])/tDur[ti];if(pr>1)pr=1;var ee=pr*pr*(3-2*pr),ta=tCur[ti],tb=tTo[ti];tpx=sx[ta]+(sx[tb]-sx[ta])*ee;tpy=sy[ta]+(sy[tb]-sy[ta])*ee;tn=ee<.5?ta:tb;}
else{tn=tCur[ti];tpx=sx[tn];tpy=sy[tn];}
// On top of the flee, a continuous lean away from the pointer, strongest up close
// and easing to nothing at AVR. It answers the pointer every frame — mid-hop, and
// when a traveller has no edge to leave by — so the field feels alive under the
// cursor rather than only twitching when a hop happens to fire.
if(pmOn>.005){var vdx=(tpx-pmx)*ASP,vdy=tpy+LIFT-pmy,vd=Math.sqrt(vdx*vdx+vdy*vdy);
if(vd<AVR){var ff=1-vd/AVR;ff*=ff*pmOn*AVP;if(vd>1e-4){tpx+=vdx/vd*ff/ASP;tpy+=vdy/vd*ff;}else tpy+=ff;}}
var tpn=Math.min(1,Math.max(0,(sp[tn]-.2)/1.0)),ty=tpy+LIFT,ta7=Math.min(1,INT*.95*sv[tn]),to=ntv*7;
travArr[to]=tpx;travArr[to+1]=ty;travArr[to+2]=(17+8*tpn)*DPR;
travArr[to+3]=accent[0];travArr[to+4]=accent[1];travArr[to+5]=accent[2];travArr[to+6]=ta7;ntv++;
// record this frame's position, then (while hopping) trail soft discs from past
// frames, shrinking and fading with distance behind the token.
trX[ti*TR+trH]=tpx;trY[ti*TR+trH]=ty;
if(tPh[ti]===1)for(var q=1;q<=TL;q++){var dd=q*TSTEP;if(dd>=trN)break;var si=ti*TR+((trH-dd+TR)%TR),tf=1-q/(TL+1),oo=ntr*7;
trailArr[oo]=trX[si];trailArr[oo+1]=trY[si];trailArr[oo+2]=(13+6*tpn)*DPR*tf;
trailArr[oo+3]=accent[0];trailArr[oo+4]=accent[1];trailArr[oo+5]=accent[2];trailArr[oo+6]=ta7*tf*tf*.6;ntr++;}}
trH=(trH+1)%TR;if(trN<TR)trN++;
return le;}
function draw(le){gl.clear(gl.COLOR_BUFFER_BIT);
if(TOG.edges&&le>0){gl.useProgram(pLine);gl.bindBuffer(gl.ARRAY_BUFFER,lineBuf);gl.bufferData(gl.ARRAY_BUFFER,lineArr.subarray(0,le*7),gl.DYNAMIC_DRAW);
gl.enableVertexAttribArray(Lp);gl.vertexAttribPointer(Lp,2,gl.FLOAT,false,28,0);gl.enableVertexAttribArray(Ls);gl.vertexAttribPointer(Ls,1,gl.FLOAT,false,28,8);gl.enableVertexAttribArray(Lc);gl.vertexAttribPointer(Lc,4,gl.FLOAT,false,28,12);gl.drawArrays(gl.TRIANGLES,0,le);}
// Nodes and comet trails share the soft-disc program and buffer (same attrib layout);
// set the program up once, then draw whichever the toggles leave on.
if(TOG.nodes||TOG.trails){gl.useProgram(pPoint);gl.bindBuffer(gl.ARRAY_BUFFER,pointBuf);
gl.enableVertexAttribArray(Pp);gl.vertexAttribPointer(Pp,2,gl.FLOAT,false,28,0);gl.enableVertexAttribArray(Ps);gl.vertexAttribPointer(Ps,1,gl.FLOAT,false,28,8);gl.enableVertexAttribArray(Pc);gl.vertexAttribPointer(Pc,4,gl.FLOAT,false,28,12);
if(TOG.nodes){gl.bufferData(gl.ARRAY_BUFFER,pointArr,gl.DYNAMIC_DRAW);gl.drawArrays(gl.POINTS,0,N);}
if(TOG.trails&&ntr>0){gl.bufferData(gl.ARRAY_BUFFER,trailArr.subarray(0,ntr*7),gl.DYNAMIC_DRAW);gl.drawArrays(gl.POINTS,0,ntr);}}
// Travellers last, so the tokens sit above the nodes and edges.
if(TOG.travellers&&ntv>0){gl.useProgram(pTrav);gl.bindBuffer(gl.ARRAY_BUFFER,travBuf);gl.bufferData(gl.ARRAY_BUFFER,travArr.subarray(0,ntv*7),gl.DYNAMIC_DRAW);
gl.enableVertexAttribArray(Tp);gl.vertexAttribPointer(Tp,2,gl.FLOAT,false,28,0);gl.enableVertexAttribArray(Ts);gl.vertexAttribPointer(Ts,1,gl.FLOAT,false,28,8);gl.enableVertexAttribArray(Tc);gl.vertexAttribPointer(Tc,4,gl.FLOAT,false,28,12);gl.drawArrays(gl.POINTS,0,ntv);}
}
// Reduced motion: one still frame, and nothing after it. The frame is redrawn on
// a theme change (repaint, above) — the picture is identical, only recoloured,
// because step() at the same t moves nothing: dt comes out zero on any call after
// the first. Redrawing rather than tinting keeps the one code path that knows how
// this scene is coloured.
if(window.matchMedia&&matchMedia('(prefers-reduced-motion:reduce)').matches){
act[(N/2)|0]=.7;
repaint=function(){draw(step(6));};
repaint();
// A resize has the same problem for the same reason: reallocating the drawing
// buffer clears it, and no frame is coming to fill it. reflow (above) is already
// bound and re-measures at 0, next frame, 180ms and 450ms for iOS's late
// dimensions, so the repaints trail those ticks rather than racing them.
addEventListener('resize',function(){repaint();setTimeout(repaint,200);setTimeout(repaint,470);});
return;}
// Pointer tracking, bound below the reduced-motion return so that reader never gets
// motion under the cursor. The canvas is pointer-events:none and sits under the
// technique, so these listen on the window and read clientX/Y against its box — the
// travellers dodge a pointer crossing the technique as readily as one over open field.
// A mouse that stops still is still there, so only leaving the page (or lifting a
// finger) withdraws it; the easing in step() fades the influence either way.
function pmAt(e){var r=cv.getBoundingClientRect();if(!r.width||!r.height)return;
pmx=(e.clientX-r.left)/r.width*2-1;pmy=1-(e.clientY-r.top)/r.height*2;pmWant=1;}
function pmOff(){pmWant=0;}
addEventListener('pointermove',pmAt,{passive:true});
addEventListener('pointerdown',pmAt,{passive:true});
addEventListener('pointerup',function(e){if(e.pointerType!=='mouse')pmOff();},{passive:true});
addEventListener('pointercancel',pmOff,{passive:true});
document.addEventListener('pointerleave',pmOff,{passive:true});
addEventListener('blur',pmOff);
// The launch signal is bound here, below the reduced-motion return, for the same
// reason the pointer handlers are: that reader is left with the single still frame.
// A restore from the back/forward cache — Back from the identity provider — comes
// back to a page that was still accelerating when it left, so take the throttle
// off and let the decay above settle it rather than warping on forever.
addEventListener('tacit:launch',function(){WARPING=1;});
addEventListener('pageshow',function(e){if(e.persisted)WARPING=0;});
// lastT keeps animation time continuous across a tab-pause: resuming rebases
// start from where it left off, so positions don't jump and spans don't all
// re-form at once (which would fire a flash storm).
var start=null,lastT=0,raf=0;
// Dev perf panel (top-left): live FPS + a rolling graph + a checkbox per render
// layer. Never built for ordinary visitors — it appears only with a ?dev / #dev URL
// flag or when a dev presses the backtick key, so the real sign-in pays nothing.
var DEV=/[?&#]dev\b/i.test(location.search+location.hash),dbg=null,fpsHist=[],lastF=0,fpsEMA=60,fCount=0,prof=null,PROF_W=6,PROF_M=24;
function buildPanel(){
var box=document.createElement('div');box.id='perf-panel';
box.style.cssText='position:fixed;top:12px;left:12px;z-index:2147483000;font:11px/1.45 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color:#e9e9ea;background:rgba(12,12,14,.82);border:1px solid rgba(255,255,255,.14);border-radius:9px;padding:9px 10px;pointer-events:auto;-webkit-user-select:none;user-select:none;-webkit-backdrop-filter:blur(6px);backdrop-filter:blur(6px);box-shadow:0 6px 24px rgba(0,0,0,.35)';
var hd=document.createElement('div');hd.textContent='backdrop · '+String.fromCharCode(96)+' hides';hd.style.cssText='font-size:10px;color:#8f8f94;margin-bottom:5px;letter-spacing:.03em';box.appendChild(hd);
var fps=document.createElement('div');fps.style.cssText='font-size:13px;font-weight:600;margin-bottom:5px;letter-spacing:.02em';fps.textContent='-- fps';box.appendChild(fps);
var cvs=document.createElement('canvas');cvs.width=176;cvs.height=44;cvs.style.cssText='display:block;width:176px;height:44px;background:rgba(0,0,0,.32);border-radius:4px;margin-bottom:7px';box.appendChild(cvs);
var opts=[['nodes','Nodes'],['edges','Edges'],['travellers','Travellers'],['trails','Trails']];
var cost={};
for(var oi=0;oi<opts.length;oi++){(function(key,label){
var row=document.createElement('label');row.style.cssText='display:flex;align-items:center;gap:7px;padding:1px 0;cursor:pointer';
var cb=document.createElement('input');cb.type='checkbox';cb.checked=!!TOG[key];cb.style.cssText='margin:0;cursor:pointer;accent-color:#4b87e0';
cb.addEventListener('change',function(){TOG[key]=cb.checked?1:0;});
var tx=document.createElement('span');tx.textContent=label;
var cs=document.createElement('span');cs.style.cssText='margin-left:auto;color:#9a9aa0;font-size:10px';cost[key]=cs;
row.appendChild(cb);row.appendChild(tx);row.appendChild(cs);box.appendChild(row);
})(opts[oi][0],opts[oi][1]);}
var sh=document.createElement('div');sh.textContent='travellers';sh.style.cssText='font-size:10px;color:#8f8f94;margin-top:8px;margin-bottom:2px';box.appendChild(sh);
var sr=document.createElement('div');sr.style.cssText='display:flex;align-items:center;gap:7px';
var sl=document.createElement('input');sl.type='range';sl.min='0';sl.max='100';sl.step='1';sl.value=NT;sl.style.cssText='flex:1;cursor:pointer;accent-color:#4b87e0';
var slv=document.createElement('span');slv.textContent=''+NT;slv.style.cssText='font-size:10px;color:#c8c8ce;min-width:46px;text-align:right';
sl.addEventListener('input',function(){NT=+sl.value;slv.textContent=''+NT;});
sr.appendChild(sl);sr.appendChild(slv);box.appendChild(sr);
var pb=document.createElement('button');pb.textContent='Profile cost';pb.style.cssText='margin-top:8px;width:100%;font:inherit;font-size:11px;color:#e9e9ea;background:rgba(75,135,224,.22);border:1px solid rgba(75,135,224,.5);border-radius:6px;padding:4px 6px;cursor:pointer';
pb.addEventListener('click',function(){if(!prof)startProfile(pb);});box.appendChild(pb);
document.body.appendChild(box);
return {el:box,visible:true,fps:fps,gx:cvs.getContext('2d'),cw:cvs.width,ch:cvs.height,cost:cost,toggle:function(){this.visible=!this.visible;this.el.style.display=this.visible?'':'none';}};}
function drawGraph(g){var x=g.gx,W2=g.cw,H2=g.ch,top=75;x.clearRect(0,0,W2,H2);
function yy(v){return H2-Math.min(v,top)/top*H2;}
x.strokeStyle='rgba(255,255,255,.16)';x.lineWidth=1;x.beginPath();x.moveTo(0,yy(60)+.5);x.lineTo(W2,yy(60)+.5);x.stroke();
x.strokeStyle='rgba(255,255,255,.08)';x.beginPath();x.moveTo(0,yy(30)+.5);x.lineTo(W2,yy(30)+.5);x.stroke();
var n=fpsHist.length;if(!n)return;x.beginPath();for(var i=0;i<n;i++){var vx=(120-n+i)/119*W2,vy=yy(fpsHist[i]);if(i===0)x.moveTo(vx,vy);else x.lineTo(vx,vy);}
var last=fpsHist[n-1];x.strokeStyle=last>=50?'#57d38c':(last>=28?'#e6c34a':'#e0604d');x.lineWidth=1.5;x.stroke();}
function perf(ts){var dt=lastF?ts-lastF:16.7;lastF=ts;if(dt>0)fpsEMA+=(1000/dt-fpsEMA)*0.12;
if(prof)profStep(dt);
if(!dbg||!dbg.visible)return;fpsHist.push(fpsEMA);if(fpsHist.length>120)fpsHist.shift();
if((++fCount%3)===0){dbg.fps.textContent=Math.round(fpsEMA)+' fps · '+(1000/Math.max(fpsEMA,1)).toFixed(1)+' ms';drawGraph(dbg);}}
// Cost profiler: A/B each currently-on layer by measuring mean frame time with it
// on (baseline) then off, over PROF_M frames after a PROF_W-frame settle, and report
// the delta as that layer's ms. Reveals real per-layer cost only when the scene is
// GPU-bound; on a machine with headroom (frames vsync-locked) every delta reads ~0,
// which is itself the answer — nothing is a bottleneck.
function startProfile(btn){btn.textContent='Profiling…';var q=[];for(var k in TOG)if(TOG[k])q.push(k);
prof={queue:q,qi:-1,phase:'base',warmN:PROF_W,sum:0,frames:0,base:0,cur:'',btn:btn,res:{}};
if(dbg)for(var c in dbg.cost)dbg.cost[c].textContent='';}
function profNext(){prof.qi++;if(prof.qi>=prof.queue.length){finishProfile();return;}
prof.cur=prof.queue[prof.qi];TOG[prof.cur]=0;prof.phase='layer';prof.warmN=PROF_W;prof.sum=0;prof.frames=0;}
function profStep(dt){if(prof.warmN>0){prof.warmN--;return;}prof.sum+=dt;prof.frames++;if(prof.frames<PROF_M)return;
var avg=prof.sum/prof.frames;if(prof.phase==='base'){prof.base=avg;profNext();return;}
prof.res[prof.cur]=Math.max(0,prof.base-avg);TOG[prof.cur]=1;profNext();}
function finishProfile(){var r=prof.res,mk='',mv=0,k;for(k in r)if(r[k]>mv){mv=r[k];mk=k;}
if(dbg)for(k in r){var el=dbg.cost[k];if(el){el.textContent=r[k].toFixed(1)+'ms';el.style.color=(k===mk&&mv>0.2)?'#e6c34a':'#9a9aa0';}}
if(prof.btn)prof.btn.textContent='Profile cost';prof=null;}
if(DEV)dbg=buildPanel();
window.addEventListener('keydown',function(e){if(e.code==='Backquote'&&!e.metaKey&&!e.ctrlKey){if(!dbg)dbg=buildPanel();else dbg.toggle();}});
function loop(ts){if(start===null)start=ts-lastT*1000;var t=(ts-start)/1000;lastT=t;draw(step(t));if(dbg)perf(ts);
raf=requestAnimationFrame(loop);}
document.addEventListener('visibilitychange',function(){if(document.hidden){if(raf)cancelAnimationFrame(raf);raf=0;}else if(!raf){start=null;raf=requestAnimationFrame(loop);}});
raf=requestAnimationFrame(loop);
})();