// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Usage page's client: it fetches /usage/data (same-origin, so the registry
// proxies the machine's own hook agent) and renders the member's funnel, their
// spend and their sessions.
//
// It is an ASSET, not an inline script, for the reason the map renderer is one:
// three thousand lines pasted into every page response is three thousand lines
// no browser may cache, no tool may lint, and no diff may read. What the server
// knew and this file cannot — the resolved window and view, the org funnel, the
// technique ids, the date the prices were read — arrives as JSON in the
// document (usage-config), which is the same seam techniquemap.js uses.
//
// Nothing here leaves the machine. The page has no member identity and the
// registry holds no per-member usage; see the header comment in usage.go.

(function(){
 var APP=document.documentElement.getAttribute('data-base')||'';
 // Everything the server knew and this file cannot: the window and view it
 // resolved, the org funnel, the technique ids, the prices' date. It arrives as
 // JSON in the document rather than as values pasted into this source, which is
 // what lets the source be a cached asset instead of part of every page.
 var CFG=JSON.parse(document.getElementById('usage-config').textContent);
 // The bloom filters, identical to the ones the server-rendered charts carry
 // (viz.go vizBloomDefs) — SVG marks need an SVG filter, because WebKit will not
 // apply a CSS one to them.
 var BLOOMDEFS=CFG.bloomDefs;
 var WINDOW=CFG.window; // resolved from ?w= server-side; the top-right control navigates to change it
 var VIEW=CFG.view;     // summary | trends | models | tools, resolved from the path server-side
 // The techniques this registry can still open a page for (knownTechniqueIDs).
 var KNOWN={};
 CFG.knownTechniques.forEach(function(id){KNOWN[id]=true;});
 // The clients this machine's agent can read, from its own routing table.
 var READS=CFG.harnesses;
 // The registry's own funnel over this window, or null where it has shown
 // nothing. Read-only and org-wide: nothing about this member travels back.
 var ORG=CFG.orgFunnel;
 // The sample floor the whole product uses (config.MinSample). Below it a rate
 // is a number that will move next week, so the panels that show one mark the
 // row and keep its counts in front. One constant, from the registry's own,
 // because two floors would disagree the first time somebody moved one.
 var MINSAMPLE=CFG.minSample;
 // The product's name and the agent's address, for the sentences that carry
 // them. PRODUCT is text, never markup: it reaches the page through esc().
 var PRODUCT=CFG.product, AGENTURL=CFG.agentURL, PRICEDAT=CFG.priceDate;
 var root=document.getElementById('usage-root');
 function esc(s){var d=document.createElement('div');d.textContent=(s==null?'':String(s));return d.innerHTML;}
 function pct(n,d){return d>0?Math.round(100*n/d)+'%':'—';}
 // good: true reads as progress, false as a problem, null as neither — and
 // neither is the honest answer for a ratio like retries-per-call, which the
 // member reads against their own past rather than against a target.
 // tileLink is a tile that opens a view. Same plate, same figure: the affordance
 // is the row idiom the tables already use, so a plate does not grow a button.
 //
 // sub is the line under the figure, and it is optional because opening
 // something is not a reason to lose what the plate said: a rate that reads
 // "2% of turns" beside a count carries its own denominator, and a doorway that
 // dropped it would be asking the member to click to get the figure back.
 function tileLink(label,value,href,sub){
  return '<a class="tile tile-link" href="'+esc(href)+'"><div class="tile-label">'+esc(label)+'</div>'+
   '<div class="tile-row"><span class="tile-value">'+esc(value)+'</span>'+
   (sub?'<span class="tile-delta">'+esc(sub)+'</span>':'')+'</div></a>';
 }
 // tip is the one-clause definition of the term in the label, carried on the
 // label itself. A plate called "Retries" needed a paragraph under the row
 // saying what a retry is; it needs a tooltip on the word instead, and the
 // disclosure at the foot of the panel holds the same sentence for anybody
 // reading rather than pointing.
 function tile(label,value,delta,good,tip){
  var cls=good===null?'':(good?' up':' down');
  var dh=delta?('<span class="tile-delta'+cls+'">'+esc(delta)+'</span>'):'';
  return '<div class="tile"><div class="tile-label"'+(tip?' title="'+esc(tip)+'"':'')+'>'+
   esc(label)+'</div><div class="tile-row"><span class="tile-value">'+esc(value)+'</span>'+dh+'</div></div>';
 }
 // tiles and workTiles return the plates themselves, not a row: beside the
 // picture they share ONE row so ten of them wrap four across into three lines,
 // which is the height the drawing happens to be. Two rows of five wrapped into
 // two lines each however wide they were, and no width made them level.
 // ---- Over time: the same chart grammar the Insights dashboard uses (viz.go +
 // the .vchart / svg.viz rules in app.css), built here in the browser because
 // this page's data arrives by fetch.
 var MONTHS=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
 var DAY=86400000;
 function dayMS(s){
  var m=/^(\d{4})-(\d{2})-(\d{2})/.exec(String(s||''));
  if(!m)return NaN;
  var ms=Date.UTC(+m[1],+m[2]-1,+m[3]);
  return ms<Date.UTC(2000,0,1)?NaN:ms;   // the zero time of an "all" window
 }
 function dayLabel(ms){var d=new Date(ms);return MONTHS[d.getUTCMonth()]+' '+d.getUTCDate();}
 // The local log only emits days that saw activity, so its rows can't be read as
 // evenly spaced columns — a quiet week would silently collapse and put the
 // surviving bars under the wrong dates. Re-lay the counts on a continuous
 // calendar spanning the whole window, so an empty day is a visible gap and the
 // x axis means what it says. Past five weeks the columns become weeks, matching
 // the Insights windows.
 // fields is what each bucket accumulates. It defaults to the funnel's, which
 // is what every caller wanted until the work trends needed turns and retries
 // over the same day columns — one bucketer, so the two views cannot disagree
 // about where a week starts.
 // maxes are the fields that are LEVELS rather than counts: a week's peak
 // context is the fullest any day in it got, not the sum of seven peaks.
 function buckets(series,from,to,fields,maxes){
  fields=fields||['queries','shown','adopted','helped'];
  maxes=maxes||[];
  var by={},lo=NaN,hi=NaN;
  (series||[]).forEach(function(r){
   var ms=dayMS(r.date);
   if(isNaN(ms))return;
   by[ms]=r;
   if(isNaN(lo)||ms<lo)lo=ms;
   if(isNaN(hi)||ms>hi)hi=ms;
  });
  var f=dayMS(from),t=dayMS(to);
  if(!isNaN(f)&&(isNaN(lo)||f<lo))lo=f;
  if(!isNaN(t)&&(isNaN(hi)||t>hi))hi=t;
  if(isNaN(lo)||isNaN(hi))return [];
  if(hi-lo>800*DAY)lo=hi-800*DAY;   // a stray far-past date can't spawn a column per day
  var size=(hi-lo)/DAY+1>35?7:1;
  var out=[],end=hi;
  while(end>=lo){
   var start=Math.max(lo,end-(size-1)*DAY);
   var b={label:(size>1?'wk ':'')+dayLabel(start)};
   fields.forEach(function(f){b[f]=0;});
   maxes.forEach(function(f){b[f]=0;});
   for(var ms=start;ms<=end;ms+=DAY){
    var r=by[ms];
    if(!r)continue;
    fields.forEach(function(f){b[f]+=r[f]||0;});
    maxes.forEach(function(f){if((r[f]||0)>b[f])b[f]=r[f]||0;});
   }
   out.unshift(b);
   end=start-DAY;
  }
  return out;
 }
 // niceAxis and fmtCount mirror viz.go's, so a member reading their own usage
 // and the org's Insights sees ticks stepped and counts abbreviated the same way.
 function niceAxis(dataMax){
  if(dataMax<=4)return {max:4,step:1};
  var raw=dataMax/5,mag=Math.pow(10,Math.floor(Math.log10(raw))),s=10*mag;
  [1,2,2.5,5].some(function(m){if(m*mag>=raw){s=m*mag;return true;}return false;});
  var step=Math.max(1,Math.round(s));
  return {max:Math.ceil(dataMax/step)*step,step:step};
 }
 function fmtCount(v){
  if(v>=1000000)return String(+(v/1000000).toFixed(1))+'M';
  if(v>=10000)return String(+(v/1000).toFixed(1))+'K';
  if(v>=1000)return Math.floor(v/1000)+','+String(v%1000).padStart(3,'0');
  return String(v);
 }
 // One plot: y labels in a gutter, a text-free stretching SVG, and — for the
 // lower chart of a pair — the dates beneath. showX false lets two charts share
 // the axis drawn under the last one.
 //
 // Bars are grouped, never stacked: adopted is a subset of shown, so a stack
 // would count the same suggestion twice. A stack mode was built here for tokens
 // in and out, which ARE disjoint parts of a total, and then removed — at the
 // real ratio of a few hundred to one the smaller segment pins to its minimum
 // height on every column and stops encoding anything, so the two became small
 // multiples with their own scales. Nothing else wanted a stack, and the
 // server-rendered charts have StackedBarChart (viz.go) if something does.
 // gap is the space between the bars of one bucket, 2px unless a caller asks
 // otherwise. Shown and adopted are drawn touching, because adopted is a SUBSET
 // of shown: they are one measurement read at two depths, and a gap between them
 // reads as two independent counts that happen to sit together.
 // full is an axis the data does not get to choose. A percentage of an
 // allowance is read against the wall at 100, not against its own biggest
 // column: scaled to itself, a climb from 6% to 8% draws the same picture as a
 // climb from 6% to 80%, which is the one thing this chart must never do.
 function vchart(bks,series,height,aria,showX,gap,full){
  var W=760,H=height,top=6,bot=6,plotH=H-top-bot,base=top+plotH;
  var dataMax=0;
  bks.forEach(function(b){series.forEach(function(s){if(b[s.f]>dataMax)dataMax=b[s.f];});});
  var ax=full?{max:full,step:full/4}:niceAxis(dataMax),n=bks.length,colW=W/n;
  var yFrac=function(v){return (top+plotH*(1-v/ax.max))/H;};
  gap=gap===undefined?2:gap;
  var ns=series.length;
  var groupW=Math.min(colW*0.72,ns*18+(ns-1)*gap),bw=Math.max(1,(groupW-gap*(ns-1))/ns);
  var yl='',grid='',bars='',hits='',xl='';
  for(var v=0;v<=ax.max;v+=ax.step){
   var gy=yFrac(v);
   yl+='<span style="top:'+(gy*100).toFixed(2)+'%">'+fmtCount(v)+'</span>';
   grid+='<line class="gl" x1="0" y1="'+(gy*H).toFixed(1)+'" x2="'+W+'" y2="'+(gy*H).toFixed(1)+'"/>';
  }
  // Bloom needs the magnitude in the markup, the same as the server-rendered
  // charts do (viz.go): --v is this bar against the largest single bar anywhere
  // on the chart, and the stylesheet turns it into light. Built here rather than
  // in Go because this chart is assembled in the browser.
  // Same five steps as the server-rendered charts (viz.go bloomStep).
  var bloomStep=function(v){if(v<=0)return '';var b=v*(0.5+0.5*v);
   return b>=0.72?' bloom4':b>=0.44?' bloom3':b>=0.20?' bloom2':b>=0.06?' bloom1':'';};
  var barMax=0;
  bks.forEach(function(b){series.forEach(function(s){var v=b[s.f]||0;if(v>barMax)barMax=v;});});
  bks.forEach(function(b,i){
   var gx=colW*(i+0.5)-groupW/2;
   series.forEach(function(s,si){
    var val=b[s.f]||0;
    if(val<=0)return;
    // A single event against a tall axis still gets a visible sliver.
    var h=Math.max(1.5,plotH*val/ax.max);
    bars+='<rect class="vbar '+s.key+bloomStep(barMax>0?val/barMax:0)+'" x="'+(gx+si*(bw+gap)).toFixed(1)+'" y="'+(base-h).toFixed(1)+
     '" width="'+bw.toFixed(1)+'" height="'+h.toFixed(1)+'"/>';
   });
   var vals=series.map(function(s){return b[s.f]||0;}).join('|');
   hits+='<rect class="hit" x="'+(colW*i).toFixed(1)+'" y="'+top+'" width="'+colW.toFixed(1)+'" height="'+plotH+
    '" data-x="'+(colW*(i+0.5)).toFixed(1)+'" data-label="'+esc(b.label)+'" data-v="'+vals+'" tabindex="0"/>';
  });
  if(showX){
   // Every column when they fit; otherwise the ends and the middle, as Insights does.
   var at=n<=8?bks.map(function(_,i){return i;}):[0,n>>1,n-1];
   at.forEach(function(i){
    xl+='<span style="left:'+(((i+0.5)/n)*100).toFixed(2)+'%">'+esc(bks[i].label)+'</span>';
   });
  }
  return '<div class="vchart'+(height>150?' tall':'')+'">'+
   '<div class="vchart-y" aria-hidden="true">'+yl+'</div>'+
   '<div class="vchart-plot"><svg class="viz" viewBox="0 0 '+W+' '+H+'" preserveAspectRatio="none"'+
   ' data-series="'+series.map(function(s){return esc(s.name);}).join('|')+'"'+
   ' data-keys="'+series.map(function(s){return s.key;}).join('|')+'"'+
   ' role="img" aria-label="'+esc(aria)+'">'+BLOOMDEFS+grid+bars+hits+'</svg></div>'+
   '<div class="vchart-x" aria-hidden="true">'+xl+'</div></div>';
 }
 // A row's technique, when the registry still has it: the same drill-down every other
 // table on the dashboard offers, carrying the window across so the technique's own
 // Performance section opens on the period being read here. Built with APP
 // rather than a literal "/techniques/…" because the server's base-path rewrite only
 // reaches static href attributes, not ones assembled in the browser.
 function techniqueHref(cap){
  return APP+'/techniques/'+encodeURIComponent(cap)+'?w='+encodeURIComponent(WINDOW);
 }
 // A technique with nothing in the window still arrives, because the dormant
 // panel below reads behind the window to find it. It has no business in this
 // table: a row of zeros says a technique failed, when what happened is that it
 // was not offered.
 function active(c){return (c.shown||0)+(c.adopted||0)+(c.helped||0)+(c.dismissed||0)>0;}
 function techniquesTable(techniques){
  var live=(techniques||[]).filter(active);
  if(!live.length)return '';
  var rows='',linked=false;
  live.forEach(function(c){
   var label=esc(c.name||c.cap),name=label,tr='<tr>';
   // Techniques this registry no longer serves stay plain text: the numbers are the
   // member's own history and outlive the library. row-link and data-href are
   // the shell's row idiom, written out here because its load-time pass ran
   // long before this table existed.
   if(c.cap&&KNOWN[c.cap]){
    linked=true;
    name='<a class="row-link" href="'+esc(techniqueHref(c.cap))+'">'+label+'</a>';
    tr='<tr data-href="'+esc(techniqueHref(c.cap))+'">';
   }
   rows+=tr+'<td>'+name+'</td>'+
    '<td class="num">'+(c.shown||0)+'</td>'+
    '<td class="num">'+(c.adopted||0)+'</td>'+
    '<td class="num">'+pct(c.adopted||0,c.shown||0)+'</td>'+
    '<td class="num">'+(c.helped||0)+'</td>'+
    '<td class="num">'+(c.dismissed||0)+'</td></tr>';
  });
  // Five rows, then a control that says how many are folded away. The cap is a
  // class on the table and the CSS hides by position, so a member who sorts by
  // Adopted sees the top five BY ADOPTED — the shell's sorter reorders the rows
  // in place and the cap follows, which a per-row hide would not.
  var capped=live.length>techniqueRowLimit;
  var more=capped
   ? '<button type="button" class="table-more" aria-expanded="false" aria-controls="usage-techniquetable">'+
     'Show '+(live.length-techniqueRowLimit)+' more</button>'
   : '';
  return '<section class="panel"><h2>By technique</h2>'+
   '<div class="table-wrap">'+
   '<table id="usage-techniquetable" class="list data-table'+(capped?' capped':'')+'"><thead><tr><th>Technique</th><th class="num">Shown</th><th class="num">Adopted</th><th class="num">Adopt&nbsp;%</th><th class="num">Helped</th><th class="num">Dismissed</th></tr></thead><tbody>'+rows+'</tbody></table></div>'+more+'</section>';
 }
 // Five, the same length the organization's leaderboards open at
 // (organization.go momentumRowLimit), so a reader meets one convention rather
 // than two.
 var techniqueRowLimit=5;
 // The control is wired after render, like the sorter and the charts.
 function wireTableMore(){
  root.querySelectorAll('button.table-more').forEach(function(btn){
   btn.onclick=function(){
    var tbl=document.getElementById(btn.getAttribute('aria-controls'));
    if(!tbl)return;
    var open=tbl.classList.toggle('capped')===false;
    btn.setAttribute('aria-expanded',open?'true':'false');
    var hidden=tbl.tBodies[0]?tbl.tBodies[0].rows.length-techniqueRowLimit:0;
    btn.textContent=open?'Show fewer':'Show '+hidden+' more';
   };
  });
 }
 // ---- The panels only one person can have. Everything below reads the
 // member's own machine and compares them against their own past, because a
 // single member has no cohort to be compared with — which is the whole reason
 // the registry cannot show any of it (docs/design/single-user-value.md).
 function modelName(m){
  // The cohort key is vendor/family. The vendor is chrome once you have read
  // it twice; the family is the part being compared.
  var i=String(m||'').indexOf('/');
  return i<0?String(m||''):String(m).slice(i+1);
 }
 function mono(m){return '<code>'+esc(modelName(m))+'</code>';}
 function num(v){return '<td class="num">'+esc(v)+'</td>';}
 // A rate is shown with its denominator or not at all: "63%" over four
 // adoptions is a number that will move next week and read as a finding.
 function rate(n,d){return d>0?(Math.round(100*n/d)+'% of '+d):'—';}
 // A count and its unit. The plural follows the one English rule that bites
 // here: a consonant before a final y becomes -ies, so three repositories are
 // not three repositorys. Everything else takes an s.
 function one(n,unit){
  if(n===1)return n+' '+unit;
  return n+' '+(/[^aeiou]y$/.test(unit)?unit.slice(0,-1)+'ies':unit+'s');
 }
 function fixed(v,dp){return (Math.round(v*Math.pow(10,dp))/Math.pow(10,dp)).toFixed(dp);}
 // A clock time in the reader's own zone. Everything else on this page is a
 // UTC day, but an allowance resets at a moment the member is living through,
 // and "resets 21:00" is only useful if it is their 21:00.
 // drillHref opens a page one level down, carrying the window.
 //
 // It used to carry the view it was opened FROM as well, so the trail could
 // name the door rather than the room. The trail is a location now (drillDown
 // in usage.go), so the origin is nobody's business but the back button's.
 function drillHref(path){
  return APP+path+'?w='+encodeURIComponent(WINDOW);
 }
 function clock(iso){
  var d=new Date(iso);
  if(isNaN(d.getTime()))return '';
  return ('0'+d.getHours()).slice(-2)+':'+('0'+d.getMinutes()).slice(-2);
 }
 // Whether the harness status line fed this window at all, and what it brought.
 // Absent is not zero: a machine with no status line wired wrote just as much
 // code as one with, and reads as having written none unless the page says so.
 function hasStatus(w){return (((w&&w.totals)||{}).status_sessions||0)>0;}
 function hasLines(w){var t=(w&&w.totals)||{};return (t.lines_added||0)+(t.lines_removed||0)>0;}
 // One row's money, and the rule that keeps every table here honest: a client
 // that reports no dollars has no cost, and $0.00 is the one thing that is
 // certainly false. Where the published rates can price the row it says so in
 // the cell — marking the exception, since most rows on a mixed window are
 // measured — and where they cannot it shows nothing at all.
 function moneyCell(r,extra){
  var cls='num'+(extra||'');
  if((r.cost_usd||0)>0)return '<td class="'+cls+'">$'+esc(fixed(r.cost_usd,2))+'</td>';
  if((r.est_cost_usd||0)>0){
   return '<td class="'+cls+'"><span class="muted" title="Estimated at '+PRICEDAT+' rates; this client reports no cost of its own">$'+
    esc(fixed(r.est_cost_usd,2))+' est</span></td>';
  }
  return '<td class="'+cls+'">\u2014</td>';
 }
 // Whether a money column can be drawn at all: something measured a cost, or
 // the rates could price something. Neither, and the column would be a row of
 // dashes explaining nothing.
 function hasMoney(w,rows){
  var t=(w&&w.totals)||{};
  if(w.cost_reported||(t.est_cost_usd||0)>0)return true;
  return (rows||[]).some(function(r){return (r.cost_usd||0)>0||(r.est_cost_usd||0)>0;});
 }
 function hasCache(w){return (((w&&w.totals)||{}).cache_requests||0)>0;}
 // The three classes of input, split the way the bill splits them. A cache read
 // costs a fraction of fresh input and a cache write a premium over it, so two
 // sessions with the same tokens in can be two very different invoices — which
 // is what makes this the one figure on the page a member can act on the same
 // afternoon, by keeping a session warm rather than starting a new one.
 //
 // Fresh is the remainder, never its own field: the three have to add up to the
 // count the rest of the page is built on, and a fourth stored number is a
 // fourth number that can disagree.
 // The prompt cache, as the composition it is. A read costs a fraction of fresh
 // input and a write costs a premium over it, so the same token count can be two
 // very different invoices -- which makes this the one figure on the page a
 // member can act on the same afternoon. It was a forty-word sentence; it is now
 // three widths and three names, ordered cheapest to dearest so the bar reads as
 // the advice without anybody having to be given it.
 var CACHEPARTS=[{f:'read',label:'Read from cache',key:'s2'},
                 {f:'write',label:'Written to cache',key:'s3'},
                 {f:'fresh',label:'Fresh',key:'s1'}];
 function cacheBar(w){
  var sp=cacheSplit(w);
  if(!sp)return '';
  var out=sub3('Prompt cache','of '+fmtCount(sp.in)+' tokens in')+'<div class="mix mix-hero">';
  CACHEPARTS.forEach(function(part){
   var v=sp[part.f]||0;
   if(!v)return;
   out+='<span class="mix-seg '+part.key+'" style="flex-grow:'+v+
    '" data-tip="'+esc(part.label+': '+fmtCount(v)+' \u00b7 '+pct(v,sp.in))+'"></span>';
  });
  out+='</div><div class="legend">';
  CACHEPARTS.forEach(function(part){
   out+='<span class="lg"><span class="lg-swatch '+part.key+'"></span>'+esc(part.label)+
    ' <b>'+pct(sp[part.f]||0,sp.in)+'</b></span>';
  });
  return out+'</div>';
 }
 function cacheSplit(w){
  var t=(w&&w.totals)||{},inTok=t.in_tokens||0;
  if(!inTok||!w||!w.tokens_reported)return null;
  var read=t.cache_read_tokens||0,write=t.cache_write_tokens||0;
  if(!read&&!write)return null; // a window from before the split was recorded
  var fresh=inTok-read-write;
  return {in:inTok,read:read,write:write,fresh:fresh>0?fresh:0};
 }
 // Whether anything counted what the tools were OFFERED. A window recorded
 // before they were counted holds calls and no offers, and reading that as a
 // window where nothing was accepted would be the worst kind of wrong number:
 // confident, specific and backwards.
 function hasOffers(w){return (((w&&w.totals)||{}).tool_offers||0)>0;}
 // And the rate is over the calls from the SAME sessions, never over every
 // call in the window: a window that straddles the day offers started being
 // counted has more calls than offers, and dividing one by the other reads as
 // better than perfect.
 function ranRate(w){
  var t=(w&&w.totals)||{},offers=t.tool_offers||0;
  if(!offers)return null;
  var paired=t.tool_calls_offered||0;
  // A session that was open when offers started being counted resumes its
  // calls from the record and its offers from zero, so it can hold more calls
  // than it was ever offered. The rate is clamped and the partial flag says
  // why, because a hundred and one per cent is not a better answer than a
  // hundred — it is the same wrong denominator, showing.
  return {ran:Math.min(paired,offers),offers:offers,
   partial:paired!==(t.tool_calls||0)||paired>offers};
 }

 // Suggestions under each model — the breakdown the registry can compute
 // org-wide and can never compute for one person. It reads on the Models view,
 // beside the work the same cohort key did.
 function modelsPanel(models){
  if(!models||models.length<2)return '';
  var rows='';
  models.forEach(function(m){
   rows+='<tr><td>'+mono(m.model)+'</td>'+
    num(m.queries||0)+num(m.shown||0)+num(m.adopted||0)+
    '<td class="num">'+esc(rate(m.adopted||0,m.shown||0))+'</td>'+
    num(m.helped||0)+num(m.dismissed||0)+'</tr>';
  });
  return '<section class="panel"><h2>Suggestions under each model</h2>'+
   '<div class="table-wrap"><table id="usage-modeltable" class="list data-table"><thead><tr>'+
   '<th>Model</th>'+uh('Queries','')+uh('Shown','')+uh('Adopted','')+
   uh('Adopt %','of shown')+uh('Helped','')+uh('Dismissed','')+
   '</tr></thead><tbody>'+rows+'</tbody></table></div></section>';
 }

 // Model change — the event nobody announces, and the one that quietly
 // retires part of how a member works.
 function changePanel(w){
  var c=w&&w.change;
  if(!c)return '';
  function per(t){return t.sessions>0?fixed(t.turns/t.sessions,1):null;}
  var bT=per(c.before),aT=per(c.after);
  var rows='';
  function row(label,before,after,note){
   if(before==null&&after==null)return;
   rows+='<tr><td>'+esc(label)+'</td>'+
    '<td class="num">'+esc(before==null?'—':before)+'</td>'+
    '<td class="num">'+esc(after==null?'—':after)+'</td>'+
    '<td class="hint">'+esc(note||'')+'</td></tr>';
  }
  row('Turns per session',bT,aT,'');
  row('Retries',c.before.retries,c.after.retries,'the same tool run again with the same arguments');
  row('Corrections',c.before.corrections,c.after.corrections,'turns that opened by putting the agent right');
  // Either side of a model change can be a client that reports no money — that
  // is half of why a member changes model — so each side says its own piece.
  var spend=function(r){
   if((r.cost_usd||0)>0)return '$'+fixed(r.cost_usd,2);
   if((r.est_cost_usd||0)>0)return '$'+fixed(r.est_cost_usd,2)+' est';
   return null;
  };
  row('Cost',spend(c.before),spend(c.after),'');
  return '<section class="panel"><h2>Model change</h2>'+
   '<p class="hint">'+esc(c.at)+' \u00b7 '+mono(c.from)+' \u2192 '+mono(c.to)+'</p>'+
   '<div class="table-wrap"><table class="list tight"><thead><tr><th></th>'+
   '<th class="num">'+mono(c.from)+'<span class="u">'+esc(one(c.before_days,'day'))+
    ', '+esc(one(c.before.sessions,'session'))+'</span></th>'+
   '<th class="num">'+mono(c.to)+'<span class="u">'+esc(one(c.after_days,'day'))+
    ', '+esc(one(c.after.sessions,'session'))+'</span></th><th></th>'+
   '</tr></thead><tbody>'+rows+'</tbody></table></div>'+
   fine(['This compares your work before and after the model change. It does not rank the models.'])+'</section>';
 }

 // Work — turns, retries, corrections and (when a source reports them)
 // tokens, cost and peak context, by model and by project.
 // The window's headline figures for how the member worked. They sit beside the
 // picture with the funnel's, because between them they ARE the summary: the
 // panel below is the breakdown, and it read oddly opening with the same five
 // numbers the top of the page had just given.
 // The account's allowance, and the only figure on this page that goes wrong
 // by sitting still. It is read live from its own file rather than aggregated
 // over the window (hooks/quota.go), and a window whose reset has already
 // passed is dropped there rather than shown stale — a member acts on this
 // one, and acting on an hour-old percentage is worse than having none.
 //
 // It rides at the end of the window's figures rather than among them, and its
 // caption says what it is, because everything to its left is thirty days and
 // this is the next five hours.
 // One plate per allowance, because a member running two providers is spending
 // two of them — and they run whichever two they run, so nothing here holds a
 // list of who they might be. The row carries its own name (modelid.Label),
 // and a provider this build has never heard of gets a plate on the same terms
 // as the rest.
 //
 // Named only where there IS more than one: mark the exception, stay silent on
 // the rule, so the common case reads exactly as it always did.
 function quotaTiles(w){
  var qs=(w&&w.quotas)||[];
  if(!qs.length)return '';
  var named=qs.length>1;
  return qs.map(function(q){
   var href=allowanceHref(w,q.source);
   return quotaTile(q,named,href)+spendTile(q,named,href);
  }).join('');
 }
 // The spend limit keeps a plate of its own. It is the third window the harness
 // reports and the only one that is money rather than usage — a cap on
 // spending and a cap on usage are different claims, and a reader who takes one
 // for the other acts on the wrong one. Absent unless a gateway reports it,
 // which is most members, so most rows never grow it.
 //
 // It can pass 100, and it is shown passing it: that is what the reading says.
 function spendTile(q,named,href){
  var s=q.spend;
  if(!s)return '';
  var label=named&&q.source_label?q.source_label+' spend':'Spend limit used';
  var sub='of the spend limit · resets '+resetAt(s.resets_at);
  var value=(s.used_pct||0)+'%';
  // The same doorway as the allowance beside it: the climb it opens is drawn
  // in the same panel, under the same provider.
  return href?tileLink(label,value,href,sub):tile(label,value,sub,null);
 }
 // A reset the member will live through today is a clock; one further out needs
 // its day beside it, or "resets 09:00" reads as this morning when it is next
 // Thursday. The five hours are always today; the week and the spend limit are
 // the ones this is for.
 function resetAt(iso){
  var ms=Date.parse(iso);
  if(!(ms>0))return '';
  if(ms-Date.now()<20*3600000)return clock(iso);
  var d=new Date(ms);
  return MONTHS[d.getMonth()]+' '+d.getDate()+' '+clock(iso);
 }
 function quotaTile(q,named,href){
  var five=q.five_hour,week=q.seven_day,lead=five||week;
  if(!lead)return '';
  var sub=five?'of 5 hours · resets '+resetAt(five.resets_at)
              :'of the week · resets '+resetAt(week.resets_at);
  if(five&&week)sub+=' · week '+(week.used_pct||0)+'%';
  // Say when it was read once the reading is old enough to have moved. A
  // percentage with no timestamp invites a member to trust yesterday's.
  if(Date.now()-Date.parse(q.at)>15*60000)sub='read '+clock(q.at)+' · '+sub;
  var label='Allowance used';
  if(named&&q.source_label)label=q.source_label+' allowance';
  var value=(lead.used_pct||0)+'%';
  // It opens where there is a climb behind it, and stays a plain plate where
  // there is not: on a machine whose history has not been sampled yet the
  // level is the whole of what is known.
  return href?tileLink(label,value,href,sub):tile(label,value,sub,null);
 }
 // Where this allowance's climb is drawn, or '' when none is kept for it.
 function allowanceHref(w,source){
  var has=((w&&w.quota_history)||[]).some(function(h){return (h.source||'')===(source||'');});
  if(!has)return '';
  return drillHref('/usage/allowance')+'#'+sourceAnchor(source);
 }
 function sourceAnchor(source){return 'src-'+String(source||'one').replace(/[^a-z0-9-]/gi,'-');}
 // Now's headline: the state first — the allowance, what is open, and whether
 // the last day was a usual one — because it is the reason this view exists,
 // then the shape of the window for orientation. Eight plates, four across,
 // which is two full rows against the picture's own height. The funnel used to
 // ride here too — twelve plates wrapping three deep, which is the stat-tile
 // row the house style names — and it moved to the view whose question it
 // answers.
 function workTiles(w){
  var t=w&&w.totals;
  if(!t||!t.sessions)return '';
  return quotaTiles(w)+liveTile(w)+todayTile(w)+
   tile('Sessions',t.sessions,'',true)+
   tile('Turns',fmtCount(t.turns),fixed(t.turns/t.sessions,1)+' per session',null)+
   // The tile is the way in: the number is here and everything behind it is one
   // click away, which is what a drill-down is for.
   tileLink('Tool calls',fmtCount(t.tool_calls||0),drillHref('/usage/tools'))+
   tile('Retries',t.retries||0,(t.tool_calls||0)>0?rate(t.retries||0,t.tool_calls||0)+' calls':'',null)+
   correctionsTile(w);
 }
 // Corrections, and the one plate in this row whose figure has something behind
 // it a member can act on: the corrections made more than once, how far back,
 // and which are already drafted as techniques. That panel is three screens into
 // a view this plate never names, so the plate opens it.
 //
 // Only where there ARE repeats. Every correction made once leaves the panel
 // silent, and a doorway onto a room with nothing in it is the same lie as an
 // empty grid.
 function correctionsTile(w){
  var t=w.totals,n=t.corrections||0;
  var sub=(t.turns||0)>0?rate(n,t.turns||0)+' turns':'';
  if(!((w.repeats||[]).length))return tile('Corrections',n,sub,null);
  return tileLink('Corrections',n,APP+'/usage/work?w='+encodeURIComponent(WINDOW)+'#repeats',sub);
 }
 // fmtSpan is a duration a person reads rather than counts: hours for a
 // window, minutes for a session. Seconds are wall clock — a session's first
 // event to its last — so the caption beside it says so and nothing here calls
 // it time spent working.
 function fmtSpan(sec){
  sec=sec||0;
  if(sec<3600)return Math.max(1,Math.round(sec/60))+'m';
  var h=sec/3600;
  return (h<10?fixed(h,1):String(Math.round(h)))+'h';
 }
 // Work — the window's own account of itself.
 //
 // The headline figures are the row beside the picture (sessions, turns,
 // models, tool calls, retries, corrections), so this panel opens on what is
 // NOT up there. It used to open on a SECOND row of plates and then a third:
 // cost and tokens, then four 40px figures for the turn-time distribution. Two
 // stat rows inside one panel is the ninth stat-tile row the house style names,
 // and a distribution is not four heroes — it is four lengths, which is what it
 // reads as now.
 //
 // Four readings, each answering something the row above cannot: what the
 // window cost, how long its turns took, and where the work went — by project
 // and by the kind of session it was. What a fit-check said was MISSING moved
 // to the Tools view, which already ends on it: it is a claim about tools, and
 // it reads against the tools that were used rather than under a panel about
 // hours and projects.
 // Which sources fed this window.
 //
 // Cost, lines, cache and the allowance all come from one place — the harness
 // status line — and it only reports on a machine where it is wired. Without
 // this line a member with two machines and one status line reads the other as
 // a machine that cost nothing and wrote no code, because an absent figure and
 // a measured zero look identical.
 // Which sources fed this window, as chips.
 //
 // This replaced three sentences that ran over the top of three panels -- which
 // clients were read, how many of the window's sessions carried a status line,
 // whether anything measured a cost. A member scans a row of chips; nobody read
 // the sentences, and the one fact they carried that matters most (an unwired
 // machine reads as a cheap one) was buried in the middle of the longest of
 // them. An unlit chip says "not reported" without being read.
 function chip(label,on,value){
  return '<span class="chip'+(on?'':' off')+'"><i></i>'+esc(label)+
   (value?' <b>'+esc(value)+'</b>':'')+'</span>';
 }
 // The clients that ran, then one chip for the rest of the readable ones. The
 // count is what makes a quiet week readable as quiet rather than as unwired,
 // and naming each absent client was eight chips about tools a member may not
 // even own.
 function clientChips(w){
  var rows=(w&&w.clients)||[],seen={},out='';
  rows.forEach(function(c){
   if(c.key&&!seen[c.key]){seen[c.key]=1;out+=chip(c.key,true,fmtCount(c.sessions||0));}
  });
  if(!out)return '';
  var rest=READS.filter(function(h){return !seen[h];}).length;
  if(rest>0)out+=chip(rest+' more readable',false,'');
  return out;
 }
 // What the status line brought, which is the difference between a machine that
 // wrote no code and one nobody asked. The share rides on the chip.
 function sourceRow(w){
  var t=(w&&w.totals)||{},n=t.status_sessions||0,sess=t.sessions||0;
  var out=clientChips(w);
  if(!out)return '';
  var money=w&&w.cost_reported?'measured':((t.est_cost_usd||0)>0?'at list':'');
  out+=chip('Cost',!!(w&&w.cost_reported),money)+
   chip('Tokens',!!(w&&w.tokens_reported),'')+
   chip('Status line',n>0,n>0&&n<sess?pct(n,sess):'')+
   chip('Tool failures',!!(w&&w.failures_reported),'');
  // How far back the names go. A compacted stretch reads as a quiet one unless
  // the page says where the line falls, and a chip says it in three words.
  if(w&&w.detail_from&&dayMS(w.detail_from)>dayMS(w.from)){
   out+=chip('Full detail from '+String(w.detail_from).slice(0,10),true,'');
  }
  return '<div class="chips">'+out+'</div>';
 }
 // ---- One break-down, not four ------------------------------------------
 //
 // A dimension is not a destination, and it is not a table of its own either.
 // The page used to carry a By project table and a By kind of session table
 // built by one closure, and a By model table built somewhere else entirely,
 // all asking the same question of the same rows. This is that question once,
 // with a control saying which way to cut it.
 //
 // Which way is URL state, like the period and the view: ?by=project is
 // bookmarkable and survives a reload, and it works with no JavaScript because
 // the options are links.
 var BREAKDOWNS=[
  {key:'project',label:'Project',head:'Project',

   rows:function(w){return w.projects||[];},cell:function(r){return esc(r.key);}},
  {key:'kind',label:'Kind of session',head:'Kind',

   rows:function(w){return w.task_types||[];},cell:function(r){return esc(r.key);}},
  {key:'model',label:'Model',head:'Model',

   rows:function(w){return w.models||[];},
   cell:function(r){return '<a class="row-link" href="'+esc(modelHref(r.key))+'">'+mono(r.key)+'</a>';}},
  {key:'client',label:'Client',head:'Client',

   rows:function(w){return w.clients||[];},cell:function(r){return mono(r.key);}}
 ];
 // The chosen cut, or the one this view's question is about: what it costs
 // opens on the model, because the model is the thing a member changes to
 // change the bill, and how you work opens on the project, because that is
 // where the hours landed.
 function breakdownBy(fallback){
  var want=new URLSearchParams(location.search).get('by');
  for(var i=0;i<BREAKDOWNS.length;i++)if(BREAKDOWNS[i].key===want)return BREAKDOWNS[i];
  for(var j=0;j<BREAKDOWNS.length;j++)if(BREAKDOWNS[j].key===fallback)return BREAKDOWNS[j];
  return BREAKDOWNS[0];
 }
 // The control itself: one link per way of cutting, carrying the period so a
 // member who changes the cut does not silently lose the window they chose.
 function breakdownControl(w,active){
  var live=BREAKDOWNS.filter(function(b){return (b.rows(w)||[]).length>1;});
  if(live.length<2)return '';
  return '<p class="hint">Break down by ' +live.map(function(b){
   var href=APP+usageHere()+'?w='+encodeURIComponent(WINDOW)+'&by='+encodeURIComponent(b.key);
   return b.key===active.key?'<strong>'+esc(b.label)+'</strong>'
    :'<a href="'+esc(href)+'">'+esc(b.label)+'</a>';
  }).join(' · ')+'</p>';
 }
 // usageHere is the path of the view being read, which the control needs to
 // build a link back to itself. Derived from VIEW rather than from
 // location.pathname so a base path or a trailing slash cannot break it.
 function usageHere(){
  return VIEW==='work'?'/usage/work':VIEW==='cost'?'/usage/cost':
   VIEW==='results'?'/usage/results':'/usage';
 }
 // cols names which columns this view wants: the friction ones where the
 // question is how you work, the money ones where it is what it costs. Same
 // rows, same control, same table.
 function breakdownTable(w,cols){
  var by=breakdownBy(cols==='cost'?'model':'project');
  var t2=(w&&w.totals)||{};
  var rows=by.rows(w)||[];
  if(rows.length<2)return ''; // one row restating the totals is not a comparison
  var money=cols==='cost';
  var head='<th>'+esc(by.head)+'</th><th class="num">Sessions</th><th class="num">Turns</th>';
  var body='';
  rows.forEach(function(r){
   body+='<tr>'+'<td>'+by.cell(r)+'</td>'+num(r.sessions||0)+num(r.turns||0)+
    (money
     ? ((w.cost_reported||(t2.est_cost_usd||0)>0)?moneyCell(r):'')+
       (w.tokens_reported?'<td class="num">'+esc(fmtCount(r.in_tokens||0))+'</td>'+
         '<td class="num">'+esc(fmtCount(r.out_tokens||0))+'</td>':'')+
       (hasLines(w)?'<td class="num">'+esc(fmtCount(r.lines_added||0))+'</td>':'')
     : '<td class="num">'+esc((r.sessions||0)>0?fixed((r.turns||0)/r.sessions,1):'—')+'</td>'+
       '<td class="num">'+esc(fmtSpan(r.seconds))+'</td>'+
       num(r.retries||0)+num(r.corrections||0))+
    '</tr>';
  });
  head+=(money
   ? ((w.cost_reported||(t2.est_cost_usd||0)>0)?'<th class="num">Cost</th>':'')+
     (w.tokens_reported?'<th class="num">Tokens&nbsp;in</th><th class="num">Tokens&nbsp;out</th>':'')+
     (hasLines(w)?'<th class="num">Lines</th>':'')
   : '<th class="num">Per&nbsp;session</th><th class="num">Time</th>'+
     '<th class="num">Retries</th><th class="num">Corrections</th>');
  return '<h3 class="chart-sub">'+esc(cols==='cost'?'Where the money went':'Where the work went')+'</h3>'+
   breakdownControl(w,by)+
   (by.note?'<p class="hint">'+by.note+'</p>':'')+
   '<div class="table-wrap"><table class="list data-table"><thead><tr>'+head+
   '</tr></thead><tbody>'+body+'</tbody></table></div>';
 }

 // The line every panel on this page ends on: where the numbers were measured
 // and how far back the full detail reaches.
 // Which clients fed this window, and how many more would have if they had run.
 //
 // A page that never names what it reads leaves a member on two tools unable to
 // tell a quiet week from an unwired one — the reading that matters most and the
 // one nothing here was saying. So the sentence carries both halves: the clients
 // in the window come from the data, and the rest come from the agent's own
 // routing table, which is why a client missing here ran no sessions rather than
 // went uncounted.
 // Work — the mechanics and the friction. What a session costs moved
 // to the view that asks that, because they are two questions and the panel
 // was answering both at once.
 function workPanel(w){
  var t=w&&w.totals;
  if(!t||!t.sessions)return '';
  var html='<div class="tiles">'+
   // Wall clock is first event to last, which includes the coffee break. The
   // harness reports what it actually spent working, and the two are different
   // enough — a quarter of the span, on the machine this was written on — that
   // the caption carries both rather than letting one stand for the other.
   tile('Session time',fmtSpan(t.seconds),
    (t.active_seconds||0)>0?'first event to last \u00b7 '+fmtSpan(t.active_seconds)+' of it working'
     :'first event to last, '+fmtSpan((t.seconds||0)/t.sessions)+' a session',null)+
   tile('Turns',fmtCount(t.turns||0),fixed(t.turns/t.sessions,1)+' per session',null)+
   tileLink('Tool calls',fmtCount(t.tool_calls||0),drillHref('/usage/tools'))+
   tile('Retries',t.retries||0,(t.tool_calls||0)>0?rate(t.retries||0,t.tool_calls||0)+' calls':'',null,
    'The same tool called again with the same arguments')+
   tile('Corrections',t.corrections||0,(t.turns||0)>0?rate(t.corrections||0,t.turns||0)+' turns':'',null,
    'A turn that opened by putting the agent right');
  // What was asked of the tools against what came back. The rate lives beside
  // the tools themselves; here it is the one figure that says whether the
  // agent is being let get on with it.
  var rr=ranRate(w);
  if(rr){
   html+=tile('Came back clean',pct(rr.ran,rr.offers),'of '+fmtCount(rr.offers)+' offered',null);
  }
  html+='</div>';
  // The turn-time distribution, as lengths in the vocabulary's own order —
  // "under 30s" before "over 10m" is the only order that reads, and it is the
  // order the summary sends them in. Silent where no harness reported timings,
  // rather than an empty grid.
  if(w.turn_times&&w.turn_times.length){
   html+=sub3('Turn times','over '+fmtCount(t.turns||0)+' turns')+
    '<div class="bars-tight-label">'+bars(w.turn_times.map(function(b){
     return {label:b.key,value:b.count||0,sub:pct(b.count||0,t.turns||0)+' of turns'};
    }),'')+'</div>';
  }
  // What broke, where it is a fact about the mechanics rather than about the
  // bill. The tools view has the per-tool reading behind it.
  html+=failuresPanel(w);
  html+=breakdownTable(w,'work');
  // The chip row goes after the figures it qualifies and before the fine print:
  // provenance is data a reader checks once a number has raised a question, and
  // the disclosure is the foot of the panel.
  return '<section class="panel"><h2>How you work</h2>'+html+sourceRow(w)+fine([
   'A retry calls the same tool again with the same arguments. A correction is a turn that starts by correcting the agent. These counts do not inspect message text.',
   'Session time is first event to last, so a session you left open counts the gap. Working time '+
   'is the harness\u2019s own, where it reports one.',
   'Failures are stored by kind, not by message. Tool output is not kept. Not every client reports failures.'])+'</section>';
 }

 // What a session handed work to, against what that session came to.
 //
 // The honest version is weaker than the one a reader wants, and saying so is
 // most of the panel. Cost is measured per SESSION, so nothing here knows what
 // one delegation cost: what it knows is which sessions reached for a thing and
 // what those sessions came to. A session that called a subagent once and then
 // worked for an hour is not an hour of subagent, and the column says
 // "sessions that used it" rather than letting the reader supply the wrong noun.
 var DELEGATENOUN={agent:'Subagent',skill:'Skill',mcp:'MCP server'};
 function delegationsPanel(w){
  var rows=(w&&w.delegations)||[];
  if(!rows.length)return '';
  var t=(w&&w.totals)||{};
  var priced=w&&w.cost_reported;
  var body='';
  rows.forEach(function(r){
   body+='<tr><td>'+esc(r.name)+'</td>'+
    '<td>'+esc(DELEGATENOUN[r.kind]||r.kind)+'</td>'+
    num(fmtCount(r.calls||0))+num(r.sessions||0)+
    (priced?'<td class="num">$'+esc(fixed(r.session_cost||0,2))+'</td>'+
      '<td class="num">'+esc((r.sessions||0)>0?'$'+fixed((r.session_cost||0)/r.sessions,2):'—')+'</td>':'')+
    (w.tokens_reported?'<td class="num">'+esc(fmtCount(r.session_tokens||0))+'</td>':'')+
    '</tr>';
  });
  // The one comparison worth drawing: what a session with a delegation in it
  // came to, against the window's own average. It is a fact about the sessions
  // and the copy says so.
  var lead=rows[0],note='';
  if(priced&&(t.sessions||0)>1&&(lead.sessions||0)>0&&(lead.sessions||0)<(t.sessions||0)){
   var mine=(lead.session_cost||0)/lead.sessions;
   var all=(t.cost_usd||0)/t.sessions;
   if(all>0){
    note=' Sessions that used '+esc(lead.name)+' came to $'+esc(fixed(mine,2))+
     ' each against $'+esc(fixed(all,2))+' across every session in the window.';
   }
  }
  return '<section class="panel"><h2>Delegated work</h2>'+
   (note?'<p class="hint">'+note+'</p>':'')+
   '<div class="table-wrap"><table class="list data-table"><thead><tr>'+
   '<th>Name</th><th>Kind</th>'+uh('Calls','')+uh('Sessions','that used it')+
   (priced?uh('Cost','of those sessions')+uh('Each','per session'):'')+
   (w.tokens_reported?uh('Tokens','of those sessions'):'')+
   '</tr></thead><tbody>'+body+'</tbody></table></div>'+
   fine(['Cost is measured per session. These totals cover sessions that used each item; they do not measure the cost of the delegation itself.'])+
   '</section>';
 }

 // Cost — money, tokens, cache, context, lines. Everything here is a
 // number a member can act on by changing how they work, which is why the
 // break-down below defaults to the model.
 function costPanel(w){
  var t=w&&w.totals;
  if(!t||!t.sessions)return '';
  var html='<div class="tiles">';
  // Cost and tokens appear only when something measured them. A total of zero
  // from a source that reports neither is not a measurement of zero.
  // Measured money and estimated money are different claims and never one
  // figure. The status line reports what a Claude Code session actually cost;
  // the other eight clients report tokens, which the published rates turn into
  // a price. A plate that added them would be specific, plausible and
  // impossible to attribute — so where both exist they are two plates, and the
  // estimate says out loud what it is and when it was priced.
  if(w.cost_reported){
   // Measured money leads and the estimate rides under it, never beside it as
   // a second hero. They are different claims about the same window — what the
   // account was charged, and what the tokens come to at list — and two plates
   // invited a reader to add them.
   var sub='$'+fixed((t.cost_usd||0)/t.sessions,2)+' a session';
   if((t.est_cost_usd||0)>0)sub+=' \u00b7 $'+fixed(t.est_cost_usd,2)+' at list';
   html+=tile('Cost','$'+fixed(t.cost_usd||0,2),sub,null);
  }else if((t.est_cost_usd||0)>0){
   // Nothing measured this window, so the estimate IS the cost figure — and it
   // says so in its own label rather than in small print underneath.
   html+=tile('Cost, estimated','$'+fixed(t.est_cost_usd,2),'at '+PRICEDAT+' rates',null);
  }
  // The prompt cache is the biggest lever on a bill, and it means nothing
  // except against the tokens it is a share of — so it rides on the input
  // count rather than taking a plate of its own. The two sources differ:
  // tokens come from the transcript and the cache from the status line, so
  // where only one reported, the survivor still gets said.
  // Two sources say something about the cache and they answer different
  // questions. The transcript splits the TOKENS, which is what the bill is
  // made of; the status line counts the REQUESTS that hit, which is how often.
  // Where both exist the tokens win the caption, because a member deciding
  // whether to keep a session warm is deciding about tokens.
  var cached='',split=cacheSplit(w);
  if(split){
   cached=pct(split.read,split.in)+' of it read from cache';
  }else if(hasCache(w)){
   var reqs=t.cache_requests||0;
   cached=pct(reqs-(t.cache_misses||0),reqs)+' of '+one(reqs,'request')+' hit the cache';
  }
  if(w.tokens_reported){
   html+=tile('Tokens in',fmtCount(t.in_tokens||0),cached,null)+
    tile('Tokens out',fmtCount(t.out_tokens||0),'',true);
  }else if(cached){
   var creqs=t.cache_requests||0;
   html+=tile('Cache hits',pct(creqs-(t.cache_misses||0),creqs),'of '+one(creqs,'model request'),null);
  }
  // The fullest the context got, not a total of anything — the caption says
  // so, because a figure in a row of sums will otherwise be read as one.
  if(w.context_reported){
   html+=tile('Peak context',fmtCount(w.peak_context||0),
    (w.peak_context_pct||0)>0?(w.peak_context_pct||0)+'% of the window, at its fullest'
     :'tokens, fullest single turn',null);
  }
  if(hasLines(w)){
   html+=tile('Lines written',fmtCount(t.lines_added||0),
    fmtCount(t.lines_removed||0)+' removed',null);
  }
  html+=tileLink('Models',(w.models||[]).length,drillHref('/usage/models'));
  html+='</div>';
  // The split, as the composition it is, between the plates and the breakdown.
  html+=cacheBar(w);
  html+=breakdownTable(w,'cost');
  return '<section class="panel"><h2>Cost</h2>'+
   html+sourceRow(w)+
   fine([(w.totals||{}).est_cost_usd>0
    ? 'An estimate prices your tokens at the rates published on '+PRICEDAT+'. A model with no '+
      'published rate is left out. A model that charges more above a '+
      'context size is priced at its base rate. It is never added to a measured cost.'
    : '',
    'Cost, lines written and cache figures come from your client\u2019s status line. '+
    '<code>tacit init</code> can connect it. Unavailable data appears as missing, not zero.',
    'Prompt-cache reads, writes, and fresh input have different prices.'])+
   '</section>';
 }

 // ---- Rhythm: when the work actually happens -----------------------------
 //
 // Everything else on this page is about the work. This is about the member:
 // which days they were at it, which hours, and how long a sitting lasts. It
 // is the first surface for "compare against your own past", which at a sample
 // of one is the whole substitute for a cohort.
 //
 // Two clocks, and the difference matters. The dates are UTC, because every
 // rollup in the log is; the hours are the member's own, counted at the turn.
 // An hour chart in UTC would tell somebody working at six in the evening that
 // they work at one in the morning, so the page says which clock it is using.
 // weekGrid lays the window's days on a calendar: one square a day, weeks
 // across, weekdays down. A quiet day is a square with nothing in it, which is
 // the whole point — the gaps are the reading, and a bar chart of only the days
 // that happened would close them up.
 // weekGrid lays the window's days on a calendar: one square a day, weeks
 // across, weekdays down, month labels over the week each one opens in, and a
 // key at the end. A quiet day is a square with nothing in it, which is the
 // whole point — the gaps are the reading, and a bar chart of only the days
 // that happened would close them up.
 var MONTHS_SHORT=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
 // A year, as GitHub draws one. The day rollups are kept longer than that, and
 // a calendar that grew with them would eventually be a strip nobody can read.
 var CAL_DAYS=371;
 // calSpan is the stretch the calendar covers, and the ONE place it is decided:
 // the streak line beside it reads the same answer, so the two can never come
 // to disagree about how long the window was. A record shorter than a year
 // starts where the record starts rather than padding a year of blanks.
 function calSpan(days,from,to){
  var seen=[];
  (days||[]).forEach(function(d){
   var ms=dayMS(d.date);
   if(!isNaN(ms))seen.push(ms);
  });
  var lo=dayMS(from),hi=dayMS(to);
  if(isNaN(hi))hi=seen.length?Math.max.apply(null,seen):NaN;
  if(isNaN(lo))lo=seen.length?Math.min.apply(null,seen):NaN;
  if(isNaN(lo)||isNaN(hi))return null;
  if(hi-lo>CAL_DAYS*DAY)lo=hi-CAL_DAYS*DAY;
  return {lo:lo,hi:hi};
 }
 function weekGrid(days,from,to){
  var by={},max=0,total=0;
  (days||[]).forEach(function(d){
   var ms=dayMS(d.date);
   if(isNaN(ms))return;
   by[ms]=(by[ms]||0)+(d.sessions||0);
   if(by[ms]>max)max=by[ms];
  });
  var sp=calSpan(days,from,to);
  if(!sp)return '';
  var lo=sp.lo,hi=sp.hi;
  // Weeks begin on the Monday before the window opens, so a row is a weekday
  // rather than whichever day the window happened to start on. The days before
  // it are padding: they hold the rows in line and are not quiet days.
  var start=lo-((new Date(lo).getUTCDay()+6)%7)*DAY;
  var cells='',months='',lastMonth=-1,lastLabel=-9,col=0;
  for(var ms=start;ms<=hi;ms+=7*DAY){
   col++;
   // The month a week mostly belongs to, read off its middle: labelling by the
   // Monday would put a heading over a column that owns one day of it. Labels
   // stay three columns apart so two short months cannot collide.
   var mid=new Date(ms+3*DAY).getUTCMonth();
   if(mid!==lastMonth&&col-lastLabel>=3){
    months+='<span style="grid-column:'+col+'">'+esc(MONTHS_SHORT[mid])+'</span>';
    lastMonth=mid;lastLabel=col;
   }
   for(var d=0;d<7;d++){
    var day=ms+d*DAY;
    if(day<lo||day>hi){cells+='<i class="pad"></i>';continue;}
    var n=by[day]||0;
    total+=n;
    // A day with anything on it is never the ground colour, however busy the
    // busiest day was: the reading is "did I work", and the shade is how much.
    var lvl=n>0?'l'+Math.max(1,Math.min(4,Math.ceil(n/Math.max(1,max)*4))):'';
    cells+='<i class="'+lvl+'" title="'+esc(dayLabel(day)+': '+one(n,'session'))+'"></i>';
   }
  }
  // The key stays at its own size whatever the squares do: it is a legend
  // rather than data, and a legend that grew with the chart would read as five
  // more days.
  var key='<div class="cal-key"><span>Less</span><i></i><i class="l1"></i>'+
   '<i class="l2"></i><i class="l3"></i><i class="l4"></i><span>More</span></div>';
  return '<div class="calwrap"><div class="cal" style="--cols:'+col+'">'+
   '<span></span><div class="cal-months">'+months+'</div>'+
   '<div class="cal-days"><span style="grid-row:1">Mon</span>'+
   '<span style="grid-row:3">Wed</span><span style="grid-row:5">Fri</span></div>'+
   '<div class="weeks" role="img" aria-label="'+
   esc(one(total,'session')+' across the window, one square a day')+'">'+cells+'</div>'+
   '</div>'+key+'</div>';
 }
 // The longest run of consecutive days with a session, and how many days had
 // one at all. Both are read off the same series the grid draws, so the two
 // can never disagree.
 function streak(days,from,to){
  var on={};
  (days||[]).forEach(function(d){
   var ms=dayMS(d.date);
   if(!isNaN(ms)&&(d.sessions||0)>0)on[ms]=true;
  });
  // The same stretch the calendar draws, decided in one place.
  var sp=calSpan(days,from,to);
  if(!sp)return null;
  var lo=sp.lo,hi=sp.hi,best=0,run=0,worked=0,span=0;
  for(var ms=lo;ms<=hi;ms+=DAY){
   span++;
   if(on[ms]){worked++;run++;if(run>best)best=run;}else{run=0;}
  }
  return {worked:worked,span:span,best:best};
 }
 // Rhythm, as its own panel on the summary: the calendar, the day, and how long
 // a sitting runs. Silent where the window holds no days at all rather than
 // drawing an empty grid.
 function rhythmPanel(w,hist){
  var days=workDays(w&&w.days);
  if(!days.length)return '';
  // The calendar reads the whole record and not the period, because the
  // question it answers — which days am I at this, how long a run — is a
  // longer one than any period control should decide, and five columns is not
  // a rhythm. Everything below it stays inside the period, and each of those
  // blocks already names the n it was computed over.
  var span=(hist&&(hist.days||[]).length)?hist:w;
  var calDays=workDays(span.days);
  var st=streak(calDays,span.from,span.to);
  var grid=weekGrid(calDays,span.from,span.to);
  if(!grid&&!(w.hours||[]).length)return '';
  var scope=span===w?'this period'
   :(st&&st.span>=CAL_DAYS?'the last year':'your whole record');
  var html='<section class="panel"><h2>Work schedule</h2>'+
   // tiles-lead: these two introduce the calendar under them rather than
   // summarising the panel, so they keep their natural width and stay together
   // above it instead of splitting the row in half.
   (st?'<div class="tiles tiles-lead">'+
     tile('Days worked',fmtCount(st.worked),'of '+fmtCount(st.span),null)+
     tile('Longest run',one(st.best,'day'),scope,null)+
    '</div>':'')+grid;
  // The day, as lengths. Twenty-four bars would be a chart of the clock rather
  // than of the work, so the hours that saw nothing stay out and each one
  // carries its share.
  var hours=(w&&w.hours)||[];
  if(hours.length){
   var turns=0;hours.forEach(function(h){turns+=h.count||0;});
   html+=sub3('Hour of the day','over '+fmtCount(turns)+' turns, your clock')+
    '<div class="bars-tight-label">'+
    bars(hours.map(function(h){
     return {label:h.key+':00',value:h.count||0,sub:pct(h.count||0,turns)+' of turns'};
    }),'')+'</div>';
  }
  // How long a sitting runs. The same component the turn times use, one level
  // up: that one is a turn, this one is the whole session around it.
  var lengths=(w&&w.lengths)||[];
  if(lengths.length){
   var sess=0;lengths.forEach(function(l){sess+=l.count||0;});
   html+=sub3('How long a session runs','over '+fmtCount(sess)+' sessions')+
    '<div class="bars-tight-label">'+bars(lengths.map(function(l){
     return {label:l.key,value:l.count||0,sub:pct(l.count||0,sess)+' of sessions'};
    }),'')+'</div>';
  }
  return html+fine([
   'The calendar covers '+esc(scope)+'; everything under it is the period in the '+
   'control above. Days use UTC. Hours use this machine\u2019s clock.',
   'Session length is first event to last, so a session you left open counts the gap.'])+
   '</section>';
 }

 // Stopped doing — a technique adopted in the previous window and not once in
 // this one. Either it decayed or the member forgot it, and only they can say
 // which.
 var DORMANT_FLOOR=3;
 function dormantPanel(techniques){
  var gone=(techniques||[]).filter(function(c){
   return (c.adopted||0)===0&&(c.prev_adopted||0)>=DORMANT_FLOOR;
  });
  if(!gone.length)return '';
  gone.sort(function(a,b){return (b.prev_adopted||0)-(a.prev_adopted||0);});
  var rows='';
  gone.forEach(function(c){
   var label=esc(c.name||c.cap);
   if(KNOWN[c.cap])label='<a href="'+esc(techniqueHref(c.cap))+'">'+label+'</a>';
   var last=String(c.last_adopted||'').slice(0,10);
   rows+='<tr><td>'+label+'</td>'+num(c.prev_adopted||0)+
    '<td class="num">'+esc(last||'—')+'</td></tr>';
  });
  return '<section class="panel"><h2>Techniques no longer adopted</h2>'+
   '<div class="table-wrap"><table class="list data-table"><thead><tr>'+
   '<th>Technique</th>'+uh('Adopted','window before')+uh('Last used','')+
   '</tr></thead><tbody>'+rows+'</tbody></table></div>'+
   fine(['These techniques had at least three adoptions in the prior window and none in this window.'])+
   '</section>';
 }

 // WHICH LAYOUT the page is in is decided by what there is to show. A note —
 // "your numbers are on your own machine", or a failure — sits BESIDE the
 // picture, because neither of them needs a page. The dashboard takes the whole
 // width and the picture goes back above it, because a stat tile row and two
 // charts in half a window is a dashboard nobody can read.
 //
 // The band is the DEFAULT, in the markup, so a page whose script never runs
 // gets it — and a page whose script never runs is a page with a note on it.
 var band=document.querySelector('.usg-band');
 function wide(on){if(band)band.classList.toggle('is-wide',!!on);}
 // head holds the window's headline figures in the column beside the picture.
 // Passing '' empties it and drops the class, which returns the band to the two
 // cells the local-only explainer uses.
 function setHead(html){
  var el=document.getElementById('usage-head');
  if(!el)return;
  el.innerHTML=html||'';
  if(band)band.classList.toggle('has-head',!!html);
 }
 // ---- Trends: the same data, asked a different question --------------------
 //
 // The summary panels describe a window ("what did I do"); these ask whether it
 // is moving ("is this changing"). Two questions, so two views — but one page
 // and one payload, because on the sealed-ledger path a second route would mean
 // another fetch and another decrypt of the same blob, and the member would
 // carry their key across pages to read one they had already opened.
 //
 // Counts per day, never rates. A rate over a daily bucket divides by whatever
 // that day happened to hold, so one turn with one retry reads as 100% and sits
 // on the chart next to a real week. The window rate already lives on the
 // summary, computed over an n worth dividing by.

 // workDays collapses the per-(date, model) series to one row per date, for the
 // plots that are about volume rather than about which model did it. The model
 // stays split on disk so the model-change report can read across a switch, and
 // the per-model small multiples below use that split directly.
 function workDays(rows){
  var by={},out=[];
  (rows||[]).forEach(function(r){
   var d=by[r.date];
   if(!d){d={date:r.date,turns:0,sessions:0,retries:0,corrections:0,in_tokens:0,out_tokens:0,
    peak_context:0,lines_added:0,lines_removed:0};by[r.date]=d;out.push(d);}
   d.turns+=r.turns||0; d.sessions+=r.sessions||0;
   d.retries+=r.retries||0; d.corrections+=r.corrections||0;
   d.lines_added+=r.lines_added||0; d.lines_removed+=r.lines_removed||0;
   // In and out stay apart so each can be plotted on a scale that fits it: the
   // input is hundreds of times the output, and one axis holding both leaves
   // the output on the baseline.
   d.in_tokens+=r.in_tokens||0; d.out_tokens+=r.out_tokens||0;
   // A level, so the day takes the fullest any session on it got.
   if((r.peak_context||0)>d.peak_context)d.peak_context=r.peak_context||0;
  });
  out.sort(function(a,b){return a.date<b.date?-1:1;});
  return out;
 }
 var WORK_FIELDS=['turns','sessions','retries','corrections','in_tokens','out_tokens','lines_added','lines_removed'];
 // A single-series plot needs no legend: its own heading names it, so colour
 // carries no identity there and every one of them can wear the same series
 // key. Colour only has to tell things apart inside a plot that holds more than
 // one, which is why the pairs below are the only ones that had to be chosen.
 function plot(title,bks,series,h,aria,showX,legend,note,gap,full,unit){
  return sub3(title,unit||'')+
   (note?'<p class="hint">'+note+'</p>':'')+
   (legend||'')+vchart(bks,series,h,aria,showX,gap,full);
 }
 // A sub-heading that carries its own unit or sample. This is the h3 twin of
 // the column-header unit (uh): the paragraph under a sub-heading was almost
 // always saying what the figures beneath it were of, and that belongs on the
 // heading.
 function sub3(title,unit){
  // Same rule as ui.Sub: a unit with no title of its own is a caption, because
  // an <h3> holding only a muted unit puts a near-empty entry in the outline a
  // screen reader navigates by.
  if(!title)return unit?'<p class="chart-sub unit-only"><span class="u">'+esc(unit)+'</span></p>':'';
  return '<h3 class="chart-sub">'+esc(title)+
   (unit?'<span class="u">'+esc(unit)+'</span>':'')+'</h3>';
 }
 // Everything a panel must not be read as, in one closed disclosure.
 //
 // Closed, because the marks above it carry the meaning: the headings name
 // their units, the legends name their categories, the denominators precede
 // their rates. A reader who never opens one of these is not misled by the
 // default view, which is the test this whole page is now held to. Nothing was
 // deleted to get here — the claims the product must not overstate all still
 // stand, one click from the figure they qualify.
 function fine(items){
  items=items.filter(Boolean);
  if(!items.length)return '';
  return '<details class="fineprint"><summary>How this is measured</summary><ul><li>'+
   items.join('</li><li>')+'</li></ul></details>';
 }
 function legendFor(pairs){
  return '<div class="legend">'+pairs.map(function(p){
   return '<span class="lg"><span class="lg-swatch '+p[1]+'"></span>'+esc(p[0])+'</span>';
  }).join('')+'</div>';
 }
 // ---- Tools: what the member's agents actually reach for ------------------
 //
 // The registry sees tools org-wide and identity-free, so it can say which tools
 // an organization leans on and never which ones YOU do, under which model, on
 // which project, beside which other tool. That is the whole of this view: the
 // cross-tabs nothing else can hold.
 //
 // The mix across kinds is a labelled bar list rather than a pie. Two reasons,
 // and the second is the binding one: a length is read more accurately than an
 // angle, and this palette cannot carry five categories — checked, and s5
 // against s1 is ΔE 12.4 in NORMAL vision, below the floor where a reader can
 // tell two colours apart at all. So the kinds are named beside their bars and
 // the colour carries nothing.
 var KINDLABEL={search:'Searching',read:'Reading',edit:'Editing',run:'Running',
  research:'Looking outside',delegate:'Delegating',steer:'Steering',mcp:'Your own tools',other:'Other'};
 var KINDORDER=['search','read','edit','run','research','delegate','steer','mcp','other'];
 var PKLABEL={vcs:'Version control',build:'Building',test:'Testing',package:'Packages',
  search:'Searching',files:'Files',network:'Network',container:'Containers & services',
  data:'Data & scripting',other:'Other'};
 var PKORDER=['vcs','build','test','package','search','files','network','container','data','other'];
 // bars() with the short-label track: for a band, a kind, a program, a pair --
 // anything whose label is a word rather than a sentence. The wide track is for
 // a leaderboard of technique names, and everything else looked stranded in it.
 function tbars(rows,empty){
  return '<div class="bars-tight-label">'+bars(rows,empty)+'</div>';
 }
 function bars(rows,empty){
  if(!rows.length)return '<p class="empty">'+esc(empty)+'</p>';
  var max=0;rows.forEach(function(r){if(r.value>max)max=r.value;});
  return '<div class="bars">'+rows.map(function(r){
   var w=max>0?(r.value/max*100):0;
   var shown=fmtCount(r.value)+(r.unit||'');
   return '<div class="bar-row" data-tip="'+esc(r.label+': '+shown+' '+(r.sub||''))+'">'+
    '<span class="bar-label">'+esc(r.label)+'</span>'+
    '<span class="bar-track"><span class="bar-fill '+esc(r.key||'s5')+'" style="width:'+w.toFixed(1)+'%"></span></span>'+
    '<span class="bar-val">'+esc(shown)+'</span>'+
    '<span class="bar-sub">'+esc(r.sub||'')+'</span></div>';
  }).join('')+'</div>';
 }
 // A matrix of tool against some dimension. Dense on purpose: the question it
 // answers — does this tool belong to one harness or all of them — is invisible
 // in any number of separate lists.
 // order, where a dimension has one: turn times are a vocabulary that reads
 // "under 30s" before "over 10m", and sorting those columns by size puts the
 // longest turns in the middle of the table.
 function matrix(tools,field,title,note,limit,rowHead,order){
  var cols={},colOrder=[];
  tools.forEach(function(t){
   Object.keys(t[field]||{}).forEach(function(c){if(!cols[c]){cols[c]=0;colOrder.push(c);}cols[c]+=t[field][c];});
  });
  if(colOrder.length<2)return ''; // one column is the totals column again
  if(order&&order.length){
   var rank={};order.forEach(function(k,i){rank[k]=i;});
   colOrder.sort(function(a,b){
    var ra=rank[a]===undefined?order.length:rank[a],rb=rank[b]===undefined?order.length:rank[b];
    return ra-rb||cols[b]-cols[a]||(a<b?-1:1);
   });
  }else{
   colOrder.sort(function(a,b){return cols[b]-cols[a]||(a<b?-1:1);});
  }
  var rows=tools.filter(function(t){return Object.keys(t[field]||{}).length;}).slice(0,limit||12);
  if(!rows.length)return '';
  var head='<tr><th>'+esc(rowHead||'Tool')+'</th>'+colOrder.map(function(c){
   return '<th class="num">'+esc(c)+'</th>';}).join('')+'</tr>';
  var body=rows.map(function(t){
   return '<tr><td>'+(t.html||esc(t.name))+'</td>'+colOrder.map(function(c){
    var n=(t[field]||{})[c]||0;
    // A zero is a real answer here — this tool never appeared there — and it
    // reads better as a dash than as a number competing with the ones that count.
    return '<td class="num">'+(n?esc(fmtCount(n)):'<span class="muted">—</span>')+'</td>';
   }).join('')+'</tr>';
  }).join('');
  return '<h3 class="chart-sub">'+esc(title)+'</h3>'+
   (note?'<p class="hint">'+note+'</p>':'')+
   '<div class="table-wrap"><table class="list data-table"><thead>'+head+'</thead><tbody>'+body+'</tbody></table></div>';
 }
 // What a tool keeps behind its name, in the member's words. The key comes
 // from capture.ToolDetailKind, so the page never guesses from the shape of a
 // detail key what the log already knows.
 var DETAILNOUN={program:['program','programs'],filetype:['file type','file types'],
  agent:['agent','agents'],skill:['skill','skills']};
 // detailCell is the column that says WHICH tools open, and what is behind
 // them. Before it, the only signal was that some tool names happened to be
 // links and the rest were not — a difference a member could only find by
 // clicking, and the question this column exists to answer without one.
 //
 // The cell is the link, in the colour every other link on the dashboard
 // wears. The name beside it stays plain: the whole row already goes there,
 // and two accents in one row would read as two destinations.
 function detailCell(t){
  var n=(t.detail||[]).length;
  if(!n)return '<span class="muted">—</span>';
  var noun=DETAILNOUN[t.detail_kind]||['level','levels'];
  return '<a href="'+esc(toolHref(t.name))+'">'+esc(n+' '+(n===1?noun[0]:noun[1]))+'</a>';
 }
 // toolHref opens one tool's own page. URL state, like every other view here, so
 // a member can keep the one they were reading.
 function toolHref(name){
  return drillHref('/usage/tools')+'&tool='+encodeURIComponent(name);
 }
 // One tool, in as much detail as it has. A shell tool resolves into the
 // programs it ran and what each of those was doing; a file tool into the kinds
 // of file it touched. Everything else keeps the cross-tabs it already had.
 function toolDetailView(w,name){
  var tools=(w&&w.tools)||[];
  var t=null;
  tools.forEach(function(x){if(x.name===name)t=x;});
  if(!t){
   return '<section class="panel"><h2>'+esc(name)+'</h2>'+
    '<p class="empty">No calls to this tool in the window. <a href="'+esc(APP+'/usage/tools?w='+encodeURIComponent(WINDOW))+'">Back to all tools</a>.</p></section>';
  }
  var detail=t.detail||[],dcalls=0;
  detail.forEach(function(d){dcalls+=d.count||0;});
  var html='<section class="panel"><h2>'+esc(t.name)+'</h2>'+
   '<p class="hint"><a href="'+esc(APP+'/usage/tools?w='+encodeURIComponent(WINDOW))+'">All tools</a> · '+
   esc(KINDLABEL[t.kind]||'Other')+
   (t.detail_kind?' · keeps '+esc((DETAILNOUN[t.detail_kind]||['level','levels'])[1]):'')+'</p>'+
   '<div class="tiles">'+
    tile('Calls',fmtCount(t.calls||0),
     (t.offers||0)>0?pct(t.calls_offered||0,t.offers)+' of '+fmtCount(t.offers)+' offered':'',true)+
    tile('Sessions',t.sessions||0,'',true)+
    tile('Per session',(t.sessions||0)>0?fixed((t.calls||0)/t.sessions,1):'—','',null)+
    tile('Last used',agoLabel(t.last),dated(t.last),null)+
   '</div>';
  var note='';
  if(detail.length){
   // The vocabulary comes from the record, not from the shape of a key. Older
   // records — a machine on a build before detail_kind, read through the
   // ledger — fall back to the shape: a "program subcommand" key has a space
   // in it and nothing else does.
   var dk=t.detail_kind||(detail.some(function(d){return d.key.indexOf(' ')>0;})?'program':'filetype');
   // Every kind but the programs records one key per call, so what the second
   // level does NOT account for is exactly the calls that named nothing. A
   // shell chain makes more keys than calls, so that arithmetic is meaningless
   // there and the line stays away.
   var missed=(dk!=='program'&&(t.calls||0)>dcalls)
    ? '<p class="hint">Named on '+esc(fmtCount(dcalls))+' of '+esc(fmtCount(t.calls||0))+
      ' calls. The rest named none, and this level cannot speak for them.</p>' : '';
   if(dk==='program'){
    var byProg={};
    detail.forEach(function(d){
     var p=d.key.split(' ')[0];
     if(!byProg[p])byProg[p]={total:0,subs:[]};
     byProg[p].total+=d.count;
     if(d.key.indexOf(' ')>0)byProg[p].subs.push({label:d.key.slice(d.key.indexOf(' ')+1),value:d.count});
    });
    var progs=Object.keys(byProg).sort(function(a,b){return byProg[b].total-byProg[a].total;});
    html+=sub3('What it ran',fmtCount(dcalls)+' runs across '+
      one(progs.length,'program'))+
     tbars(progs.map(function(p){return {label:p,value:byProg[p].total,sub:pct(byProg[p].total,dcalls)+' of runs'};}),'');
    progs.forEach(function(p){
     var subs=byProg[p].subs.sort(function(a,b){return b.value-a.value;});
     if(!subs.length)return;
     html+='<h4 class="chart-sub">'+esc(p)+'<span class="u">subcommands</span></h4>'+
      tbars(subs.map(function(sb){return {label:sb.label,value:sb.value,sub:pct(sb.value,byProg[p].total)+' of '+p};}),'');
    });
   }else{
    // One bar list, three vocabularies: the heading says which, and the note
    // under it says what was kept and what was deliberately not.
    var head='What it touched';
    note='The kind of file, which is all that is kept: the extension is a vocabulary and the path it came from is not.';
    if(dk==='filetype'&&t.kind==='search'){
     head='What it searched';
     note='The kind of file you pointed it at. The pattern you searched for is your own text and is never kept, which is why this counts only the calls that named a glob.';
    }else if(dk==='agent'){
     head='Who it handed to';
     note='The agent, which is a vocabulary. What you asked it to do is not, and is not kept.';
    }else if(dk==='skill'){
     head='Which skills it ran';
     note='The name only. Anything you typed after it is your own text.';
    }
    html+=sub3(head,'')+missed+
     tbars(detail.map(function(d){
      // The page's own rate idiom: a share never travels without the
      // denominator it was computed over, which here is the calls that named
      // one rather than every call.
      return {label:d.key,value:d.count,sub:rate(d.count,dcalls)};
     }),'');
   }
  }else if(t.detail_kind){
   // A tool that CAN open and did not: the member asked "why is this flat",
   // and the honest answer is that every call named nothing, not that the
   // second level was never built.
   html+='<p class="empty">Nothing behind this one in the window: none of its '+
    esc(one(t.calls||0,'call'))+' named a '+
    esc((DETAILNOUN[t.detail_kind]||['level'])[0])+'.</p>';
  }
  html+=matrix([t],'harness','By client')+matrix([t],'model','By model')+
   matrix([t],'task','By kind of session')+matrix([t],'project','By project');
  return html+'</section>';
 }
 // ranCell is how much of what this tool was asked to do actually happened.
 // Silent where the tool recorded no offers — an older session knows its calls
 // and not its offers, and a dash says so where "0%" would lie.
 // dated renders a date the record actually holds. A row that was only ever
 // offered never ran, so it has no last use, and Go's zero time would print
 // itself as the year 1 — a confident answer to a question nothing measured.
 function dated(v){
  return isNaN(dayMS(v))?'—':String(v).slice(0,10);
 }
 // How long ago something happened, in the coarsest form that answers "can I
 // still trust this": today, a count of days, or a count of weeks. A date set
 // at 40px is a poor hero; its age is the reading.
 function agoLabel(day){
  var ms=dayMS(day);
  if(isNaN(ms))return '\u2014';
  var days=Math.round((Date.now()-ms)/DAY);
  if(days<=0)return 'today';
  if(days<14)return one(days,'day')+' ago';
  return one(Math.round(days/7),'week')+' ago';
 }
 function ranCell(t){
  var offers=t.offers||0;
  if(!offers)return '—';
  return pct(t.calls_offered||0,offers);
 }
 // What broke, by kind and never by message. A tool's output is the member's
 // own work — file contents, a diff, somebody's customer list — so the kind of
 // failure is the only thing kept about it (capture.ToolFailure).
 //
 // The panel says so when it has nothing, rather than drawing an empty grid: a
 // harness that does not report failures and a month where nothing went wrong
 // look identical, and one of those is the most flattering lie this page could
 // tell.
 function failuresPanel(w){
  var rows=(w&&w.failures)||[];
  var offers=(w&&w.totals||{}).tool_offers||0;
  if(!rows.length){
   if(!hasOffers(w))return '';
   return '<h3 class="chart-sub">What broke</h3>'+
    '<p class="empty">No failed tool calls were reported in this window. Not every harness reports failures. The count of offered calls without a result is shown above.</p>';
  }
  var total=0;rows.forEach(function(r){total+=r.count||0;});
  // The lead is named only where it leads. "Going looking for something that
  // is not there is the commonest" is true of this market and not necessarily
  // of this member, and the page does not tell a reader about themselves from
  // somebody else's sample.
  var note='';
  return sub3('What broke',fmtCount(total)+' of '+fmtCount(offers||total)+' calls')+
   (note?'<p class="hint">'+esc(note.trim())+'</p>':'')+
   '<div class="bars-tight-label">'+bars(rows.map(function(r){
    return {label:r.key,value:r.count||0,sub:pct(r.count||0,total)+' of failures'};
   }),'')+'</div>';
 }
 function toolsView(w){
  var tools=(w&&w.tools)||[];
  if(!tools.length){
   return '<section class="panel"><h2>Tools</h2><p class="empty">No tool calls recorded in this window yet. '+
    'Tool names come from the full session records, which reach back '+
    (w&&w.detail_from?esc(String(w.detail_from).slice(0,10)):'thirty days')+
    '. Older sessions keep counts but not tool names.</p></section>';
  }
  var calls=0,sessions=(w.totals||{}).sessions||0;
  tools.forEach(function(t){calls+=t.calls||0;});
  var busiest=tools[0];
  var html='<section class="panel"><h2>Tools</h2>'+
   '<div class="tiles">'+
    tile('Tools used',tools.length,'',true)+
    tile('Calls',fmtCount(calls),'',true)+
    tile('Per session',sessions>0?fixed(calls/sessions,1):'—','',null)+
    tile('Busiest',busiest.name,pct(busiest.calls||0,calls)+' of calls',null)+
    (ranRate(w)?tile('Came back clean',pct(ranRate(w).ran,ranRate(w).offers),
      fmtCount(ranRate(w).ran)+' / '+fmtCount(ranRate(w).offers)+' offered',null):'')+
   '</div>';
  // The window's own acceptance. It was a sentence under the plate row and is
  // now a plate in it, which is where a figure with a denominator belongs.
  var rate=ranRate(w);

  // What the tools were FOR.
  var byKind={};
  tools.forEach(function(t){byKind[t.kind||'other']=(byKind[t.kind||'other']||0)+(t.calls||0);});
  var kindRows=KINDORDER.filter(function(k){return byKind[k];}).map(function(k){
   return {label:KINDLABEL[k]||k,value:byKind[k],sub:pct(byKind[k],calls)+' of calls'};
  }).sort(function(a,b){return b.value-a.value;});
  html+='<h3 class="chart-sub">What the tools were for</h3>'+

   '<div class="bars-tight-label">'+bars(kindRows,'Nothing classified yet.')+'</div>';

  // Every tool, with its own account of itself.
  html+='<h3 class="chart-sub">Every tool</h3>'+
   '<div class="table-wrap">'+
   // Inside sits second, right after the name it belongs to. On a phone the
   // table scrolls sideways and only the first columns survive the fold, and
   // the column that says whether a row opens is worth more there than the
   // label for what the tool is — which the bars above have already given.
   '<table class="list data-table"><thead><tr><th>Tool</th><th class="num">Inside</th><th>Kind</th><th class="num">Calls</th>'+
   '<th class="num">Share</th>'+(hasOffers(w)?'<th class="num">Ran</th>':'')+
   '<th class="num">Sessions</th><th class="num">Per session</th><th class="num">Last used</th>'+
   '</tr></thead><tbody>'+
   tools.map(function(t){
    // A tool with a second level opens it. The row idiom the other tables use,
    // so a drill-down looks like every other drill-down on the dashboard.
    var name=esc(t.name);
    var deep=(t.detail||[]).length>0;
    if(deep)name='<a class="row-link" href="'+esc(toolHref(t.name))+'">'+name+'</a>';
    return (deep?'<tr data-href="'+esc(toolHref(t.name))+'">':'<tr>')+
     '<td>'+name+'</td><td class="num">'+detailCell(t)+'</td>'+
     '<td>'+esc(KINDLABEL[t.kind]||t.kind||'Other')+'</td>'+
     '<td class="num">'+esc(fmtCount(t.calls||0))+'</td>'+
     '<td class="num">'+esc(pct(t.calls||0,calls))+'</td>'+
     // Ran out of offered. A column, not a headline: an acceptance rate is the
     // number this market most often misreads as productivity, and it means
     // nothing except beside the tool it belongs to.
     (hasOffers(w)?'<td class="num">'+esc(ranCell(t))+'</td>':'')+
     '<td class="num">'+(t.sessions||0)+'</td>'+
     '<td class="num">'+esc((t.sessions||0)>0?fixed((t.calls||0)/t.sessions,1):'—')+'</td>'+
     '<td class="num">'+esc(dated(t.last))+'</td></tr>';
   }).join('')+'</tbody></table></div>';

  // Inside the shell. Bash is one bar and the busiest one; these are the dozen
  // things it was. Names only — the programs, never the command lines, which is
  // what lets this exist at all.
  var cmds=(w&&w.commands)||[];
  if(cmds.length){
   var ccalls=0;cmds.forEach(function(c){ccalls+=c.calls||0;});
   var byPK={};
   cmds.forEach(function(c){byPK[c.kind||'other']=(byPK[c.kind||'other']||0)+(c.calls||0);});
   var pkRows=PKORDER.filter(function(k){return byPK[k];}).map(function(k){
    return {label:PKLABEL[k]||k,value:byPK[k],sub:pct(byPK[k],ccalls)+' of runs'};
   }).sort(function(a,b){return b.value-a.value;});
   html+='<h3 class="chart-sub">Inside the shell</h3>'+

    tbars(pkRows,'')+
    '<h4 class="chart-sub">By program</h4>'+
    tbars(cmds.slice(0,16).map(function(c){
     return {label:c.name,value:c.calls,sub:PKLABEL[c.kind]||'Other'};
    }),'');
  }
  html+=matrix(tools,'harness','By client',0);
  html+=matrix(tools,'model','By model',0);
  html+=matrix(tools,'task','By kind of session',0);
  html+=matrix(tools,'project','By project',0,8);

  // What travels together.
  var pairs=(w&&w.tool_pairs)||[];
  if(pairs.length){
   html+='<h3 class="chart-sub">Tools that travel together</h3>'+
 
    tbars(pairs.slice(0,10).map(function(p){
     return {label:p.a+' + '+p.b,value:p.sessions,sub:'sessions'};
    }),'');
  }

  // What broke, before what was missed: one is a fact the harness reported and
  // the other is a judgement a model made, and they read in that order.
  html+=failuresPanel(w);

  // What a fit-check said was missing.
  var absent=(w&&w.absent_tools)||[];
  if(absent.length){
   html+='<h3 class="chart-sub">Tools that would have helped</h3>'+

    tbars(absent.map(function(a){return {label:a.key,value:a.count,sub:'sessions'};}),'');
  }
  if(w&&w.detail_from){
   html+=fine(['Tool names reach back to '+esc(String(w.detail_from).slice(0,10))+
    '. Older sessions keep their counts but not their tool names, so named tools may cover only part of the selected period.',
    'A tool\u2019s second level is kept only where it is a vocabulary: the programs a shell ran, '+
    'the kinds of file a reader touched, the agents and skills a session reached for. A dash means '+
    'the tool keeps none, because its input is what you typed.',
    'Programs are kept and command lines are not, which is the only reason the shell breakdown '+
    'can exist at all.',
    'Missed tools are named by the fit-check on sessions where they were not used.'])+
    '';
  }
  return html+'</section>';
 }

 // ---- Models: the one thing on this page the member chooses ---------------
 //
 // The registry counts model cohorts across the whole organization and can
 // never say which models YOU ran, on which projects, with which tools, or what
 // happened to your own work when you switched. The Models tile on the summary
 // is the count; everything here is what the count is made of.
 //
 // Cross-tabs count SESSIONS, because a session runs under one model, one
 // client, one project and one kind of work — a call count would say the same
 // thing louder.
 function modelHref(key){
  return drillHref('/usage/models')+'&model='+encodeURIComponent(key);
 }
 // A cross-tab map as the labelled bar list the tools view already uses,
 // busiest first. Named rather than coloured, for the reason bars() gives.
 function countBars(map,total,unit,limit){
  var rows=Object.keys(map||{}).map(function(k){
   return {label:k,value:map[k],sub:total>0?pct(map[k],total)+' of '+unit:''};
  });
  rows.sort(function(a,b){return b.value-a.value||(a.label<b.label?-1:1);});
  return rows.slice(0,limit||12);
 }
 // Turn times keep the order the summary sent them in — "under 30s" before
 // "over 10m" is the only order that reads — so a model's own distribution is
 // walked in the same order rather than sorted by size.
 function turnTimeBars(map,order,turns){
  var seen={},rows=[];
  (order||[]).forEach(function(b){
   if(!map[b.key])return;
   seen[b.key]=true;
   rows.push({label:b.key,value:map[b.key],sub:pct(map[b.key],turns)+' of turns'});
  });
  Object.keys(map||{}).forEach(function(k){
   if(!seen[k])rows.push({label:k,value:map[k],sub:pct(map[k],turns)+' of turns'});
  });
  return rows;
 }
 // The day rows for one model, as the plot the trends view draws for all of
 // them: the same bucketer, so a member reading one model and reading the whole
 // window never sees two answers about where a week starts.
 function modelPlot(w,key,title,h,showX){
  var rows=((w&&w.days)||[]).filter(function(r){return (r.model||'unattributed')===key;});
  if(!rows.length)return '';
  var bks=buckets(workDays(rows),w.from,w.to,WORK_FIELDS,['peak_context']);
  if(!bks.length)return '';
  var unit=bks[0].label.indexOf('wk ')===0?'week':'day';
  return '<p class="chart-sub" style="margin-top:.7rem">'+(title||mono(key))+'</p>'+
   vchart(bks,[{name:'Turns',key:'s5',f:'turns'}],h||90,'Turns per '+unit+' under '+key,showX!==false);
 }
 function modelsView(d,w){
  var models=(w&&w.models)||[];
  if(!models.length){
   return '<section class="panel"><h2>Models</h2><p class="empty">No sessions in this window include a model name. '+
    'The harness records the model for each new session. '+
    '<a href="'+esc(APP+'/usage?w='+encodeURIComponent(WINDOW))+'">Back to the summary</a>.</p></section>';
  }
  var turns=0,sessions=0,calls=0;
  models.forEach(function(m){turns+=m.turns||0;sessions+=m.sessions||0;calls+=m.tool_calls||0;});
  var byTurns=models.slice().sort(function(a,b){return (b.turns||0)-(a.turns||0);});
  var lead=byTurns[0];
  var html='<section class="panel"><h2>Models</h2>'+
   // The figure is a figure and the name is the caption beside it. A model
   // label set at 40px wraps onto three lines and stops being a hero, which is
   // why the share leads and the model it belongs to rides underneath.
   '<div class="tiles">'+tile('Models used',models.length,'',true)+
    tile('Leading model',pct(lead.turns||0,turns),modelName(lead.key)+' of your turns',null);
  // Most spent, where anything was. A window of clients that report no money
  // would otherwise crown the cheapest-looking model at $0.00, which is a
  // ranking of nothing.
  var dear=models.slice().sort(function(a,b){return (b.cost_usd||0)-(a.cost_usd||0);})[0];
  if((dear.cost_usd||0)>0){
   html+=tile('Most spent','$'+fixed(dear.cost_usd,2),modelName(dear.key),null);
  }else{
   var est=models.slice().sort(function(a,b){return (b.est_cost_usd||0)-(a.est_cost_usd||0);})[0];
   if((est.est_cost_usd||0)>0){
    html+=tile('Most spent, estimated','$'+fixed(est.est_cost_usd,2),modelName(est.key),null);
   }
  }
  html+='</div>';

  // The mix. A length, not an angle and not a hue: this palette cannot separate
  // five categories, and the names are what the reader is after anyway. One
  // model is not a mix — a single bar at full width is the totals row again.
  if(models.length>1){
   html+='<h3 class="chart-sub">Share of your turns</h3>'+
    tbars(byTurns.map(function(m){
     return {label:modelName(m.key),value:m.turns||0,
      sub:one(m.sessions||0,'session')};
    }),'');
  }

  // Every model, with everything countable it has. Sortable, like every other
  // table on the dashboard, and a row opens the model.
  html+=sub3('Every model','')+'<div class="table-wrap">'+
   '<table class="list data-table"><thead><tr><th>Model</th><th class="num">First&nbsp;seen</th>'+
   '<th class="num">Sessions</th><th class="num">Turns</th><th class="num">Per&nbsp;session</th>'+
   '<th class="num">Tool&nbsp;calls</th><th class="num">Retries</th><th class="num">Corrections</th>'+
   (hasLines(w)?'<th class="num">Lines</th>':'')+
   (hasMoney(w,models)?'<th class="num">Cost</th>':'')+
   (w.context_reported?'<th class="num">Peak&nbsp;context</th>':'')+
   '<th class="num">Last&nbsp;used</th></tr></thead><tbody>'+
   models.map(function(m){
    return '<tr data-href="'+esc(modelHref(m.key))+'">'+
     '<td><a class="row-link" href="'+esc(modelHref(m.key))+'">'+mono(m.key)+'</a></td>'+
     '<td class="num">'+esc(String(m.first||'').slice(0,10)||'—')+'</td>'+
     num(m.sessions||0)+num(m.turns||0)+
     '<td class="num">'+esc((m.sessions||0)>0?fixed(m.turns/m.sessions,1):'—')+'</td>'+
     num(fmtCount(m.tool_calls||0))+num(m.retries||0)+num(m.corrections||0)+
     (hasLines(w)?'<td class="num">'+esc((m.lines_added||0)>0?fmtCount(m.lines_added):'—')+'</td>':'')+
     (hasMoney(w,models)?moneyCell(m):'')+
     (w.context_reported?'<td class="num">'+esc((m.peak_context||0)>0?fmtCount(m.peak_context):'—')+'</td>':'')+
     '<td class="num">'+esc(String(m.last||'').slice(0,10)||'—')+'</td></tr>';
   }).join('')+'</tbody></table></div>';

  // A doorway to the outcome rows, not a copy of them. The totals above are
  // model-only and stay that way: a model does not choose its tools, so the
  // comparison that carries a pass rate is keyed by model AND client, and it
  // lives on the view that asks what came of the work.
  if(((w&&w.model_clients)||[]).length){
   html+='<p class="hint"><a href="'+
    esc(APP+'/usage/results?w='+encodeURIComponent(WINDOW)+'#checked')+
    '">Was it checked, by model and client \u2192</a></p>';
  }
  html+='</section>';
  // The switch, where there was one. It reads here rather than on the trends,
  // because a before and an after either side of a model change is a fact about
  // the models and about nothing else on that page.
  html+=changePanel(w);

  // Over time. One plot per model rather than one plot with a colour per model:
  // a colour assigned by rank is repainted whenever the ranking moves, and a
  // switch reads as one series stopping where another starts.
  if(models.length>1){
   var plots='',ordered=models.slice().sort(function(a,b){return a.key<b.key?-1:1;});
   ordered.forEach(function(m,i){
    plots+=modelPlot(w,m.key,0,0,i===ordered.length-1);
   });
   if(plots){
    html+='<section class="panel chart-panel"><h2>Over time</h2>'+
  
     plots+'</section>';
   }
  }

  // The cross-tabs, which is the half of this the registry could never hold.
  var rows=models.map(function(m){
   return {name:modelName(m.key),html:mono(m.key),
    harness:m.harness,task:m.task,project:m.project,turn_times:m.turn_times,
    effort:m.effort};
  });
  var cross=matrix(rows,'harness','By client',0,12,'Model')+
   matrix(rows,'task','By kind of session',0,12,'Model')+
   matrix(rows,'project','By project',0,8,'Model')+
   // The reasoning level is the other half of the same choice: the model, and
   // how hard you asked it to think. It reads here rather than anywhere else
   // because those two are changed together or not at all.
   matrix(rows,'effort','By reasoning level',0,12,'Model',
    ['none','low','medium','high','xhigh','max'])+
   // Every other matrix on this page counts sessions; this one counts turns,
   // which the heading says so the note does not have to.
   matrix(rows,'turn_times','By turn time (turns)',0,
    12,'Model',((w&&w.turn_times)||[]).map(function(b){return b.key;}));
  if(cross){
   html+='<section class="panel"><h2>Under each model</h2>'+
 
    cross+'</section>';
  }

  // What Tacit itself did under each model — the other log, joined on the same
  // cohort key, which is why it can be read beside the work above.
  html+=modelsPanel(d&&d.models);
  // Only where the horizon actually falls inside the window: on a short period
  // every session is a full record, and the caveat would be noise about a line
  // the reader is nowhere near.
  if(w&&w.detail_from&&dayMS(w.detail_from)>dayMS(w.from)){
   html+=fine(['Model labels reach back to '+esc(String(w.detail_from).slice(0,10))+
    ' in full. Older sessions keep their cohort and lose the exact label the harness said, so a '+
    'variant list is shorter than the sessions behind it.']);
  }
  return html;
 }
 // One model, in as much detail as it has: what it was called, where it ran,
 // what it worked on, how long its turns took, which tools it reached for, and
 // how its own days went.
 function modelDetailView(d,w,key){
  var models=(w&&w.models)||[],m=null;
  models.forEach(function(x){if(x.key===key||modelName(x.key)===key)m=x;});
  var back='<a href="'+esc(APP+'/usage/models?w='+encodeURIComponent(WINDOW))+'">All models</a>';
  if(!m){
   // The key as given, not the family: a member who followed a stale link
   // needs to see which label found nothing.
   return '<section class="panel"><h2>'+esc(key)+'</h2>'+
    '<p class="empty">No sessions under this model in this period. A longer one may reach back to it. '+back+'.</p></section>';
  }
  var allTurns=0;models.forEach(function(x){allTurns+=x.turns||0;});
  var html='<section class="panel"><h2>'+esc(modelName(m.key))+'</h2>'+
   '<p class="hint">'+back+' · '+esc(m.key)+' · first seen '+esc(String(m.first||'').slice(0,10))+
   ', last '+esc(String(m.last||'').slice(0,10))+'</p>'+
   '<div class="tiles">'+
    tile('Sessions',m.sessions||0,pct(m.turns||0,allTurns)+' of your turns',null)+
    tile('Turns',fmtCount(m.turns||0),(m.sessions||0)>0?fixed(m.turns/m.sessions,1)+' per session':'',null)+
    tile('Tool calls',fmtCount(m.tool_calls||0),(m.sessions||0)>0?fixed((m.tool_calls||0)/m.sessions,0)+' per session':'',null)+
    tile('Retries',m.retries||0,(m.tool_calls||0)>0?rate(m.retries||0,m.tool_calls||0)+' calls':'',null)+
    tile('Corrections',m.corrections||0,(m.turns||0)>0?rate(m.corrections||0,m.turns||0)+' turns':'',null)+
    ((m.cost_usd||0)>0?tile('Cost','$'+fixed(m.cost_usd,2),'',null)
      :((m.est_cost_usd||0)>0?tile('Cost, estimated','$'+fixed(m.est_cost_usd,2),'at '+PRICEDAT+' rates',null):''))+
    ((m.in_tokens||0)+(m.out_tokens||0)>0?
      tile('Tokens in',fmtCount(m.in_tokens||0),'',null)+
      tile('Tokens out',fmtCount(m.out_tokens||0),'',null):'')+
    ((m.peak_context||0)>0?tile('Peak context',fmtCount(m.peak_context),'tokens, fullest single turn',null):'')+
    ((m.lines_added||0)+(m.lines_removed||0)>0?
      tile('Lines written',fmtCount(m.lines_added||0),fmtCount(m.lines_removed||0)+' removed',null):'')+
   '</div>';

  // What the harness actually called it. The cohort is what every rate here is
  // computed over; these are the labels folded into it, which is the only place
  // on the dashboard a pinned date stamp or a gateway route is visible.
  var variants=Object.keys(m.variants||{});
  if(variants.length){
   html+='<h3 class="chart-sub">What your harness called it</h3>'+
    tbars(countBars(m.variants,m.sessions||0,'sessions'),'');
  }
  var groups=[['harness','Where it ran',''],
   ['task','What kind of work',''],
   ['project','What it worked on',''],
   ['effort','How hard you asked it to think','']];
  groups.forEach(function(g){
   var map=m[g[0]]||{};
   if(!Object.keys(map).length)return;
   html+=sub3(g[1],'')+tbars(countBars(map,m.sessions||0,'sessions'),'');
  });
  if(m.turn_times&&Object.keys(m.turn_times).length){
   html+='<h3 class="chart-sub">Turn times</h3>'+
    '<div class="bars-tight-label">'+
     bars(turnTimeBars(m.turn_times,(w&&w.turn_times)||[],m.turns||0),'')+'</div>';
  }

  // The tools this model reached for, from the tool cross-tab that already
  // holds them: one place counts a tool call, and two views read it.
  var tools=((w&&w.tools)||[]).map(function(t){
   return {name:t.name,kind:t.kind,calls:(t.model||{})[m.key]||0,detail:(t.detail||[]).length>0};
  }).filter(function(t){return t.calls>0;})
    .sort(function(a,b){return b.calls-a.calls||(a.name<b.name?-1:1);});
  if(tools.length){
   var tcalls=0;tools.forEach(function(t){tcalls+=t.calls;});
   html+='<h3 class="chart-sub">What it reached for</h3>'+
    '<div class="table-wrap"><table class="list data-table"><thead><tr><th>Tool</th><th>Kind</th>'+
    '<th class="num">Calls</th><th class="num">Share</th></tr></thead><tbody>'+
    tools.slice(0,12).map(function(t){
     var name=esc(t.name);
     if(t.detail)name='<a class="row-link" href="'+esc(toolHref(t.name))+'">'+name+'</a>';
     return '<tr>'+'<td>'+name+'</td><td>'+esc(KINDLABEL[t.kind]||t.kind||'Other')+'</td>'+
      '<td class="num">'+esc(fmtCount(t.calls))+'</td>'+
      '<td class="num">'+esc(pct(t.calls,tcalls))+'</td></tr>';
    }).join('')+'</tbody></table></div>';
  }
  html+='</section>';

  var plot=modelPlot(w,m.key,'Turns',120);
  if(plot){
   html+='<section class="panel chart-panel"><h2>Over time</h2>'+
    plot+'</section>';
  }

  // And what Tacit did under it, from the other log on the same cohort key.
  var u=null;
  ((d&&d.models)||[]).forEach(function(x){if(x.model===m.key)u=x;});
  if(u){
   html+='<section class="panel"><h2>Suggestions under this model</h2>'+
    '<div class="tiles">'+
    tile('Queries',u.queries||0,'',true)+
    tile('Shown',u.shown||0,'',true)+
    tile('Adopted',u.adopted||0,rate(u.adopted||0,u.shown||0)+' shown',true)+
    tile('Helped',u.helped||0,rate(u.helped||0,u.adopted||0)+' adopted',true)+
    tile('Dismissed',u.dismissed||0,'',null)+
    '</div></section>';
  }
  return html;
 }

 // Steering: the corrections a member has made more than once.
 //
 // The ledger keeps hashes and counts and no wording at all, which is the
 // point — what somebody said to put their agent right is the closest thing on
 // this machine to a transcript. So the panel says how often rather than what,
 // and the useful half is that a correction repeated often enough is already
 // being drafted as a technique.
 function repeatsPanel(w){
  var rows=(w&&w.repeats)||[];
  if(!rows.length)return '';
  var top=rows.slice().sort(function(a,b){return (b.count||0)-(a.count||0);})[0];
  var raised=rows.filter(function(r){return r.raised;}).length;
  return '<section class="panel" id="repeats"><h2>Repeated corrections</h2>'+
   '<div class="tiles">'+
    tile('Repeated corrections',fmtCount(rows.length),'',null)+
    tile('Most repeated',one(top.count||0,'time'),
     top.first?String(top.first).slice(0,10)+' to '+String(top.last).slice(0,10):'',null)+
    tileLink('Drafted from these',fmtCount(raised),'/review')+
   '</div>'+
   fine(['The wording is not kept. Only the repeat count is stored.',
    'A repeated correction can create a draft technique for review.'])+'</section>';
 }

 // ---- Now: the state, rather than the record ------------------------------
 //
 // The one view whose question has a half-life in hours. What is left of the
 // allowance, whether today is a usual day, and what is running right now —
 // and then the window's headline figures, because a member who came here for
 // the state usually stays for the shape.
 // The state is two PLATES, beside the allowance whose half-life it shares.
 //
 // It was a panel — a heading, a hint paragraph and two sentences — and a panel
 // is what you give something with a paragraph's worth to say. This had a figure
 // and a comparison, which is a plate and a plate, and it took a screen's width
 // to say them in prose while the row above had room for four more.
 //
 // What is running, from the agent's own head rather than from the log: the log
 // knows what has been recorded and not what is open. Absent on the
 // sealed-ledger path, where there is no live agent to ask, and absent is right
 // — an unknown is not a zero, and a plate reading 0 would claim a machine is
 // idle when nothing asked it.
 function liveTile(w){
  if(typeof w.live_sessions!=='number')return '';
  return tile('Open right now',w.live_sessions,'on this machine',null);
 }
 // Whether the last day worked was a usual one. Labelled with the day itself,
 // because it is not always today: a member reading Monday's page is owed the
 // date rather than a word that has quietly stopped being true.
 function todayTile(w){
  var days=workDays(w&&w.days);
  if(days.length<3)return ''; // two days is not a usual day to compare against
  var last=days[days.length-1];
  var rest=days.slice(0,-1);
  var mean=0;rest.forEach(function(d){mean+=d.turns||0;});
  mean=mean/rest.length;
  if(mean<=0)return '';
  var ratio=(last.turns||0)/mean;
  var how=ratio>=1.35?'a busier day than usual':ratio<=0.65?'a quieter day than usual':'about a usual day';
  return tile(dayLabel(dayMS(last.date)),fmtCount(last.turns||0),
   'turns · '+how+', '+fixed(mean,1)+' on the days before',null);
 }
 // ---- The allowance as a climb, rather than as a level -------------------
 //
 // The plate says how much is gone. This says whether it will last, which is
 // the question at two in the afternoon, and the only thing that answers it is
 // the shape of the climb — sampled by the same status line that draws the
 // plate (hooks/quota.go).
 //
 // The machine's own turns run under it on the same buckets. They are NOT an
 // explanation of the climb and the page says so: an allowance is spent from
 // every machine and every surface signed into that account, and this log is
 // one machine. Two pictures side by side, and the reader draws the line.
 var FIVE_STEP=15*60000, WEEK_STEP=6*3600000, SPEND_STEP=24*3600000;
 function allowanceView(w){
  var hist=(w&&w.quota_history)||[],now=(w&&w.quotas)||[];
  if(!hist.length){
   return '<section class="panel"><h2>Allowance</h2>'+
    '<p class="empty">No allowance readings yet. Usage readings appear after the status line '+
    'reports them. A few minutes of work can produce the first reading.</p></section>';
  }
  return hist.map(function(h){
   var live=null;
   now.forEach(function(q){if((q.source||'')===(h.source||''))live=q;});
   return allowancePanel(w,h,live,hist.length>1);
  }).join('');
 }
 function allowancePanel(w,h,live,named){
  var title=named&&h.source_label?h.source_label+' allowance':'Allowance';
  var body='<section class="panel" id="'+esc(sourceAnchor(h.source))+'"><h2>'+esc(title)+'</h2>';
  if(live)body+='<div class="tiles tiles-tight">'+quotaTile(live,named)+'</div>';
  var end=Date.now();
  // The WHOLE window, not the part of it that has happened: five hours back
  // from the reset to the reset itself. The blank to the right of the climb is
  // the time left, which is half of what a member is here to see — a chart that
  // stopped at now would show the same picture at ten past as at half past.
  var fiveFrom=end-6*3600000,fiveTo=end;
  if(live&&live.five_hour){
   fiveTo=Date.parse(live.five_hour.resets_at);
   fiveFrom=fiveTo-5*3600000;
  }
  if(h.five&&h.five.length){
   var bks=levelBuckets(h.five,fiveFrom,fiveTo,FIVE_STEP,clockOf);
   body+=plot('The five hours',bks,[{name:'Used',key:'s9',f:'pct'}],118,
    'Share of the five-hour allowance used, quarter hour by quarter hour',true,'',
    'Shows allowance used and remaining time in the current window. '+
    'Sampled from the status line: a level that did not move between readings is drawn '+
    'as the level it held.'+burnLine(h.five,fiveFrom,fiveTo),2,100);
   // This machine's own work, on the same buckets and from its own series: it
   // is machine-wide, so it is drawn once under whichever allowance is being
   // read rather than split between them.
   var wbks=levelBuckets(w.work_history||[],fiveFrom,fiveTo,FIVE_STEP,clockOf);
   if(wbks.some(function(b){return b.turns>0;})){
    body+=plot('Your turns in the same hours',wbks,[{name:'Turns',key:'s5',f:'turns'}],100,
     'Turns per quarter hour on this machine',true,'',
     'This machine only. The allowance above is the account\u2019s, and it is spent from every '+
     'machine and every surface signed into it \u2014 so these two are worth reading side by side '+
     'and not as one explaining the other.',2);
   }
  }
  if(h.week&&h.week.length){
   body+=plot('The week',levelBuckets(h.week,end-7*24*3600000,end,WEEK_STEP,dayOf),
    [{name:'Used',key:'s9',f:'pct'}],118,
    'Share of the weekly allowance used, six hours at a time',true,'',
    'The same reading against the longer window.',2,100);
  }
  // The spend limit, where a gateway reports one. A day at a time over the last
  // month, and the axis still stops at 100 even though the reading need not:
  // the wall is what the picture is about, and a bar through it says so.
  if(h.spend&&h.spend.length){
   body+=plot('The spend limit',levelBuckets(h.spend,end-30*24*3600000,end,SPEND_STEP,dayOf),
    [{name:'Used',key:'s9',f:'pct'}],118,
    'Share of the spend limit used, a day at a time',true,'',
    'Your gateway reports spend against its money limit. The usage windows above do not supply this figure.',2,100);
  }
  return body+'</section>';
 }
 // Where the climb is heading, if it keeps its slope.
 //
 // This is the one question the allowance page cannot answer from a level alone:
 // 40% at ten past is comfortable or alarming depending entirely on how fast it
 // got there, and the reader is the only one who knows which. The history the
 // status line has been filling in says it, so the page says it.
 //
 // It is an EXTRAPOLATION and is written as one — the rate, the span it was
 // measured over, and the hour it lands on. It stays silent where a projection
 // would be a guess dressed as a reading:
 //
 //   - fewer than two readings, or under twenty minutes between the first and
 //     the last: a slope from a short sample is noise with a decimal point;
 //   - a level that is flat or falling: there is no rate to run out at, and the
 //     good news needs no arithmetic;
 //   - already at the cap: the page is not going to predict the past.
 //
 // Landing after the reset is worth saying rather than swallowing — it is the
 // answer to the question, and it is the answer a member wants.
 function burnLine(points,from,to){
  var rows=(points||[]).filter(function(p){return Date.parse(p.at)>=from;})
   .sort(function(a,b){return a.at<b.at?-1:1;});
  if(rows.length<2)return '';
  var first=rows[0],last=rows[rows.length-1];
  var spanMs=Date.parse(last.at)-Date.parse(first.at);
  if(spanMs<20*60000)return '';
  var climbed=(last.pct||0)-(first.pct||0);
  if(climbed<=0||(last.pct||0)>=100)return '';
  var perHour=climbed/(spanMs/3600000);
  var left=100-(last.pct||0);
  var outAt=Date.parse(last.at)+(left/perHour)*3600000;
  var basis=' Usage increased by '+esc(fixed(perHour,1))+'% an hour over the last '+
   esc(fmtSpan(Math.round(spanMs/1000)))+'. At that rate, ';
  if(outAt>=to){
   return basis+'it resets before it runs out.';
  }
  return basis+'it runs out around '+esc(clockOf(outAt))+', '+
   esc(fmtSpan(Math.round((to-outAt)/1000)))+' short of the reset.';
 }
 // A level does not disappear between readings: a bucket with no reading in it
 // carries the level the last one held, because that is what the allowance was
 // while nobody was looking. Counts sum; the level takes the highest the bucket
 // saw.
 //
 // Carried forward only as far as the last READING. The window runs on past it
 // — that blank is the hours left — and drawing the level across them would be
 // claiming a measurement of a time that has not happened.
 function levelBuckets(points,from,to,step,label){
  var rows=(points||[]).slice().sort(function(a,b){return a.at<b.at?-1:1;});
  var last=rows.length?Date.parse(rows[rows.length-1].at):0;
  var out=[],i=0,held=0,seen=false;
  while(i<rows.length&&Date.parse(rows[i].at)<from){
   held=rows[i].pct||0;seen=true;i++;
  }
  for(var t=from;t<to;t+=step){
   var b={label:label(t),pct:0,turns:0,tool_calls:0};
   var any=false;
   while(i<rows.length&&Date.parse(rows[i].at)<t+step){
    var p=rows[i];
    if((p.pct||0)>b.pct)b.pct=p.pct;
    b.turns+=p.turns||0;b.tool_calls+=p.tool_calls||0;
    held=p.pct||0;seen=true;any=true;i++;
   }
   if(!any&&seen&&t<=last)b.pct=held;
   out.push(b);
  }
  return out;
 }
 // Two machines reading one account write the same instant twice. In time
 // order, and one row per instant: a doubled point would double the turns
 // hanging off it.
 function dedupePoints(points){
  var seen={},out=[];
  (points||[]).slice().sort(function(a,b){return a.at<b.at?-1:1;}).forEach(function(p){
   if(seen[p.at])return;
   seen[p.at]=1;out.push(p);
  });
  return out;
 }
 function clockOf(ms){return clock(new Date(ms).toISOString());}
 function dayOf(ms){return dayLabel(ms);}
 // Each view carries the series it already draws. Trends was an axis pretending
 // to be a destination: a page whose only content is "the same numbers again,
 // over time" is one the member has to leave to ask anything else.
 // Each plot names its own series. A single-series plot needs no legend — its
 // heading names it, so colour carries no identity and every one of them wears
 // the same key — but two series on one plot do, because identity is never
 // colour alone. Which measures share a plot is a decision about the measures
 // and not about the layout: turns and tokens run hundreds of times their
 // neighbours and get their own scales, while retries and corrections are the
 // same size and read as one question.
 function seriesPanel(w,title,note,plots){
  var days=workDays(w&&w.days);
  if(!days.length)return '';
  var bks=buckets(days,w.from,w.to,WORK_FIELDS,['peak_context']);
  if(!bks.length)return '';
  var unit=bks[0].label.indexOf('wk ')===0?'week':'day';
  var body='',drawn=0;
  plots.forEach(function(p){
   if(p.when&&!p.when(w))return;
   var any=0;
   bks.forEach(function(b){p.series.forEach(function(sr){any+=b[sr.f]||0;});});
   if(!any)return;
   drawn++;
   body+=plot(p.title,bks,p.series,p.h||118,p.title+' per '+unit,false,
    p.legend||'',p.note?p.note(w,unit):'',p.gap,0,
    p.unit?p.unit(w,unit):'per '+unit);
  });
  if(!drawn)return '';
  // The last plot carries the dates; without it they belong to whatever ran
  // last, which on a view with one plot is nothing at all.

  return '<section class="panel chart-panel"><h2>'+esc(title)+'</h2>'+
   (note?'<p class="hint">'+note+'</p>':'')+body+'</section>';
 }
 function render(d,w,hist){
  // Two columns at the top: the picture at half width beside the window's
  // headline figures, with everything after it spanning both (the has-head rule
  // in app.css). Only Now has a picture, so every other view takes the width.
  wide(VIEW!=='now');
  // The drill-downs first: they are reached from a figure rather than from the
  // menu, and they answer a narrower question than any of the four.
  if(VIEW==='models'){
   setHead('');
   var m=new URLSearchParams(location.search).get('model');
   root.innerHTML=m?modelDetailView(d,w,m):modelsView(d,w);
   wireUp();
   return;
  }
  if(VIEW==='tools'){
   setHead('');
   var one=new URLSearchParams(location.search).get('tool');
   root.innerHTML=one?toolDetailView(w,one):toolsView(w);
   wireUp();
   return;
  }
  if(VIEW==='allowance'){
   setHead('');
   root.innerHTML=allowanceView(w);
   wireUp();
   return;
  }
  if(VIEW==='work'){
   setHead('');
   root.innerHTML=workPanel(w)+rhythmPanel(w,hist)+repeatsPanel(w)+
    seriesPanel(w,'Over time','',
     [{title:'Turns',series:[{name:'Turns',key:'s5',f:'turns'}]},
      // Retries and corrections share a plot: they are the same size and they
      // are the same question. Independent counts, so they keep the gap the
      // shown/adopted pair closes.
      {title:'Doing it twice',h:150,
       series:[{name:'Retries',key:'s3',f:'retries'},{name:'Corrections',key:'s7',f:'corrections'}],
       legend:legendFor([['Retries','s3'],['Corrections','s7']]),
       unit:function(){return 'per day';}}]);
   wireUp();
   return;
  }
  if(VIEW==='cost'){
   setHead('');
   root.innerHTML=costPanel(w)+delegationsPanel(w)+
    seriesPanel(w,'Over time','',
     // In runs hundreds of times out, so they are separate plots with their own
     // scales rather than one stack: at that ratio a stacked out segment is
     // below a pixel and pins to its minimum on every column, where its height
     // stops meaning anything at all.
     [{title:'Tokens in',series:[{name:'In',key:'s8',f:'in_tokens'}],
       when:function(x){return x.tokens_reported;},
       unit:function(x){
        var ti=(x.totals||{}).in_tokens||0,to=(x.totals||{}).out_tokens||0;
        return to>0?fmtCount(Math.round(ti/to))+'\u00d7 what it produced':'';
       }},
      {title:'Tokens out',series:[{name:'Out',key:'s9',f:'out_tokens'}],
       when:function(x){return x.tokens_reported;}},
      {title:'Lines written',series:[{name:'Lines',key:'s5',f:'lines_added'}],when:hasLines,
       unit:function(x){
        return fmtCount((x.totals||{}).lines_added||0)+' added, '+
         fmtCount((x.totals||{}).lines_removed||0)+' removed';
       }}]);
   wireUp();
   return;
  }
  if(VIEW==='results'){
   setHead('');
   root.innerHTML=resultsView(d,w);
   wireUp();
   return;
  }
  // Now. The picture keeps its height at half the width, and the window's
  // headline figures take the column beside it.
  var t=d.totals||{},html='';
  setHead('<div class="tiles">'+workTiles(w)+'</div>');
  if(!(w&&w.totals&&w.totals.sessions)){
   html+='<p class="empty">No sessions recorded on this machine in this window yet. '+
    'New sessions and reported costs will appear here. '+
    'See <a href="/docs/user-guide/20-sessions/04-work-with-suggestions.md">how suggestions arrive in a session</a>, '+
    'or browse <a href="/techniques">the playbook</a>.</p>';
  }
  html+=seriesPanel(w,'Over time','',
   [{title:'Sessions',series:[{name:'Sessions',key:'s5',f:'sessions'}]},
    {title:'Turns',series:[{name:'Turns',key:'s5',f:'turns'}]}]);
  root.innerHTML=html;
  wireUp();
 }
 // What landed: of the lines an agent wrote, how many are still in the branch.
 //
 // This page cannot go and measure it. The session log keeps a project basename
 // and never a path, so nothing here can find the repository — the line that
 // keeps the log safe to hold, and not a gap to be plugged. The question is
 // asked from inside the repository instead, where the working directory IS the
 // path and the member supplies it by standing there.
 //
 // What CAN happen is that the answer gets filed. A reading is a basename, a
 // branch and counts, which is what the command already prints, so
 // usage --landed --publish puts it where this panel can read it, without
 // anything learning where the code is.
 //
 // Every reading carries the day it was taken, and nothing refreshes it. That
 // date is the panel's whole defence: a survival figure measured three weeks ago
 // describes a branch that has moved, and a page showing it undated would be
 // telling a lie it was told honestly.
 function landedPanel(d,w){
  var rows=(w&&w.landed)||[];
  // The command IS the empty state: there is no figure to carry the meaning
  // until somebody runs it, so this is one of the three places on the page
  // where a sentence is all there is.
  var ask='<p class="hint">Ask inside a repository: <code>tacit usage --landed --publish</code></p>';
  if(!rows.length){
   return '<section class="panel"><h2>Code still present</h2>'+
    '<p class="empty">No repository has been measured yet.</p>'+ask+
    fine(['This page stores project names, not paths. Run the command inside the repository you want to check. '+
     'It reads that directory, writes nothing to it, and compares the branch\u2019s own '+
     'history against the lines your sessions recorded for that project.'])+'</section>';
  }
  // The headline is the same shape the check panel uses: the denominator, then
  // the rate over it. A "Survived" column beside "Written" and "Still there"
  // needs no paragraph — the three read left to right as the subtraction they
  // are.
  var written=0,stillThere=0,oldest='';
  rows.forEach(function(r){
   written+=r.added||0;stillThere+=r.survived||0;
   var at=String(r.asked_at||'').slice(0,10);
   if(at&&(!oldest||at<oldest))oldest=at;
  });
  var body='<table class="list data-table"><thead><tr><th>Project</th>'+
   uh('Written','lines')+uh('Still there','lines')+uh('Survived','of written')+
   uh('Measured over','')+uh('Asked','')+'</tr></thead><tbody>';
  rows.forEach(function(r){
   var added=r.added||0,survived=r.survived||0;
   body+='<tr><td>'+esc(r.project||'')+
    (r.branch?' <span class="muted">'+esc(r.branch)+'</span>':'')+'</td>'+
    num(fmtCount(added))+num(fmtCount(survived))+
    // The rate needs its denominator, and a window with no commits in it has
    // none: "0%" of nothing written is not a bad month, it is no measurement.
    '<td class="num">'+esc(added>0?pct(survived,added):'\u2014')+'</td>'+
    '<td class="num">'+esc((r.window_days||0)>0?one(r.window_days,'day'):'\u2014')+'</td>'+
    '<td class="num">'+esc(String(r.asked_at||'').slice(0,10))+'</td></tr>';
  });
  body+='</tbody></table>';
  return '<section class="panel"><h2>Code still present</h2>'+
   '<div class="tiles">'+
    tile('Lines written',fmtCount(written),'by your agents',null)+
    tile('Still in the branch',pct(stillThere,written),
     fmtCount(stillThere)+' / '+fmtCount(written),null)+
    // The staleness is the reading, and a date set at 40px is not it: nothing
    // refreshes these, so what a member needs at a glance is how old the
    // oldest one is. The date itself rides underneath, and in the table.
    tile('Oldest reading',agoLabel(oldest),oldest?oldest+' \u00b7 '+
     one(rows.length,'repository'):one(rows.length,'repository'),null)+
   '</div>'+
   '<div class="table-wrap">'+body+'</div>'+ask+
   fine(['Measured with blame at the branch tip. Reformatted, moved, or rewritten lines count as gone. Renames follow the file; rebases do not.',
    'Run the command again to refresh a reading. Readings show their date and expire after three months.'])+
   '</section>';
 }
 // Was it checked: of the sessions where your agent changed something, how many
 // ran a recognised check, and what the latest one said.
 //
 // WHY THIS PANEL HAS ALMOST NO PROSE, having shipped with four paragraphs of
 // it. Every paragraph was there because the design left a question open, and a
 // member reading a figure does not read the paragraph above it — so a panel
 // that needs the paragraph is a panel that does not work. The questions moved
 // into the marks that raise them:
 //
 //   what are these numbers of   -> the denominator is the first tile, before
 //                                 the two rates that divide by it, and each
 //                                 rate shows its own fraction underneath
 //   what do passed/stale/unknown mean
 //                              -> a segmented bar and a legend, so the four
 //                                 states are named where they are drawn
 //   per what are cost and turns -> the column header says "per session"
 //   what does this NOT prove    -> one closed disclosure, because the claim
 //                                 must stand and does not have to be read to
 //                                 be honoured by the labels above it
 //
 // What the disclosure holds is not decoration. A benchmark owns the task, runs
 // it eight times in a sandbox and injects a verifier; Tacit sees live work
 // once. Nothing here may be called resolved, ranked, or given a confidence
 // band, and CHECKSTATES plus the headers are what carry that in the default
 // view (docs/design/real-work-usage-analysis-plan.md).

 // The four states a check can leave a changed session in, and the fifth thing
 // a changed session can be: unchecked. Semantic and fixed, like every other
 // series key here. Three hues, because only three of the five are an ANSWER:
 // green passed, red failed, amber edited-after. A check that ran and said
 // nothing is grey and a session nothing checked is an empty outline, because
 // the absence of a verdict is not a category and must not be given a colour
 // that competes with one. Green-amber-red is also the one encoding every reader
 // already holds, which is why this legend needs no sentence under it.
 var CHECKSTATES=[{f:'passed',label:'Passed',key:'s2'},
                  {f:'failed',label:'Failed',key:'s6'},
                  {f:'stale',label:'Edited after',key:'s3'},
                  {f:'unknown',label:'No verdict',key:'nil'}];
 function checkPanel(w){
  var rows=(w&&w.model_clients)||[];
  if(!rows.length){
   return '<section class="panel" id="checked"><h2>Was It Checked</h2>'+
    '<p class="empty">No check data in this window. A session enters this measure after an edit tool runs.</p>'+
    '</section>';
  }
  // The pair rows are the sum of their task rows: one session has one model,
  // one client and one task type, so a pair's figures are its rows added up
  // and nothing is counted twice.
  var pairs=foldPairs(rows),all=foldOutcome(rows);
  // The denominator FIRST, then the two rates that divide by it. The order is
  // the explanation: read left to right, there is no question about what the
  // percentages are of, and that is what retired "of the same 110".
  var html='<section class="panel" id="checked"><h2>Was It Checked</h2>'+
   '<div class="tiles">'+
    tile('Changed sessions',fmtCount(all.changed),
     (w.totals||{}).sessions?'of '+fmtCount((w.totals||{}).sessions)+' sessions':'',null)+
    tile('Ran a check',pct(checked(all),all.changed),
     fmtCount(checked(all))+' / '+fmtCount(all.changed),null)+
    tile('Latest check passed',pct(all.passed,all.changed),
     fmtCount(all.passed)+' / '+fmtCount(all.changed),null)+
   '</div>'+
   // The whole window's composition, drawn once, with the legend that names the
   // states. The per-row bars below are the same mark broken down, so the
   // legend is read once and carries for the table.
   stateBar(all,'mix-hero')+checkLegend(all)+
   pairTable(pairs,w)+
   taskSplit(rows)+
   sourceChips(w,all)+
   checkFineprint(all);
  return html+'</section>';
 }
 // checked is derived rather than stored, so it cannot come to disagree with
 // the four states it is made of.
 function checked(c){return (c.passed||0)+(c.failed||0)+(c.stale||0)+(c.unknown||0);}
 // One bar: the four states, then the changed sessions nothing looked at. The
 // widths ARE the rates, which is why the numeric state columns could go.
 //
 // cls carries mix-cell inside a table row, where the bar is the cell's whole
 // content and does not want the standalone margins.
 function stateBar(c,cls){
  var n=c.changed||0;
  if(!n)return '';
  var out='<div class="mix '+esc(cls)+'" role="img" aria-label="'+esc(stateWords(c))+'">';
  CHECKSTATES.forEach(function(st){
   var v=c[st.f]||0;
   if(!v)return;
   out+='<span class="mix-seg '+st.key+'" style="flex-grow:'+v+
    '" data-tip="'+esc(st.label+': '+fmtCount(v)+' of '+fmtCount(n))+'"></span>';
  });
  var unchecked=n-checked(c);
  if(unchecked>0){
   out+='<span class="mix-seg none" style="flex-grow:'+unchecked+
    '" data-tip="'+esc('No check ran: '+fmtCount(unchecked)+' of '+fmtCount(n))+'"></span>';
  }
  return out+'</div>';
 }
 // The bar's own reading in words, for a screen reader and for forced-colors,
 // where two colours cannot separate four states.
 function stateWords(c){
  var n=c.changed||0,parts=[];
  CHECKSTATES.forEach(function(st){
   if(c[st.f])parts.push(fmtCount(c[st.f])+' '+st.label.toLowerCase());
  });
  var unchecked=n-checked(c);
  if(unchecked>0)parts.push(fmtCount(unchecked)+' with no check');
  return 'Of '+fmtCount(n)+' changed sessions: '+parts.join(', ')+'.';
 }
 // The legend, with each state's count on it. This is the only place the four
 // words appear, and it is beside the mark that uses them.
 function checkLegend(all){
  var out='<div class="legend">';
  // Every state stays on the legend whatever its count, so the vocabulary does
  // not change shape between windows and an absent state is never mistaken for
  // an unmeasured one. A zero is dimmed instead, so the eye goes to what
  // happened.
  CHECKSTATES.forEach(function(st){
   var v=all[st.f]||0;
   out+='<span class="lg'+(v?'':' zero')+'"><span class="lg-swatch '+st.key+'"></span>'+
    esc(st.label)+' <b>'+fmtCount(v)+'</b></span>';
  });
  var unchecked=(all.changed||0)-checked(all);
  out+='<span class="lg'+(unchecked?'':' zero')+'"><span class="lg-swatch none"></span>'+
   'No check <b>'+fmtCount(unchecked>0?unchecked:0)+'</b></span>';
  return out+'</div>';
 }
 function foldOutcome(rows){
  var out={};
  CHECK_TOTALS.forEach(function(f){out[f]=0;});
  (rows||[]).forEach(function(r){CHECK_TOTALS.forEach(function(f){out[f]+=r[f]||0;});});
  return out;
 }
 // The pair rows: the task dimension folded away, which is what the comparison
 // is keyed by.
 function foldPairs(rows){
  var by={},order=[];
  (rows||[]).forEach(function(r){
   var k=(r.model||'')+SEP+(r.client||'');
   if(!by[k]){by[k]=foldOutcome([]);by[k].model=r.model;by[k].client=r.client;order.push(k);}
   CHECK_TOTALS.forEach(function(f){by[k][f]+=r[f]||0;});
  });
  return order.map(function(k){return by[k];})
   .sort(function(a,b){return (b.changed||0)-(a.changed||0)||
    (String(a.model)<String(b.model)?-1:1);});
 }
 // A column header with its unit under it (th .u in app.css). The unit is what
 // a paragraph over the table used to carry, and it belongs here: the question
 // "per what" is asked at the column, so it is answered at the column.
 // The name is wrapped where there is a unit under it, so the sort caret can
 // attach to the NAME line rather than to the end of the cell -- which on a
 // two-line header is under the unit (app.css, th.has-u).
 function uh(name,unit){
  if(!unit)return '<th class="num">'+esc(name)+'</th>';
  return '<th class="num has-u"><span class="hd">'+esc(name)+'</span>'+
   '<span class="u">'+esc(unit)+'</span></th>';
 }
 // One row per model and client. Both halves are named on every row because a
 // model does not choose its tools, write its prompts or decide when to stop —
 // naming only the model would credit or blame the wrong half, and the two
 // columns say so without a sentence saying it.
 function pairTable(pairs,w){
  var money=pairs.some(function(p){return (p.cost_usd||0)>0||(p.est_cost_usd||0)>0;});
  var status=pairs.some(function(p){return (p.status_sessions||0)>0;});
  // Two header rows. The top one groups: everything left of the divide is what
  // happened, everything right of it is per changed session — said once, for
  // four or five columns, where it used to be a sentence above the table.
  var effort=['Tokens out','Calls','Turns'];
  if(money)effort.unshift('Cost');
  if(status)effort.splice(effort.length-1,0,'Working');
  var head='<tr class="grouprow"><th colspan="5" class="span">What happened</th>'+
   '<th colspan="'+effort.length+'" class="span num">Per changed session</th></tr>'+
   '<tr><th>Model</th><th>Client</th>'+uh('Changed','sessions')+
   '<th class="nosort">Checks</th>'+uh('Passed','of changed')+
   effort.map(function(n,i){
    return '<th class="num'+(i===0?' groupstart':'')+'">'+esc(n)+'</th>';
   }).join('')+'</tr>';
  var body=pairs.map(function(p){
   var n=p.changed||0,per=function(v){return n>0?v/n:0;};
   // A thin row keeps its counts and loses its rate: a percentage off nine
   // sessions reads as a finding and moves next week. The marker is one muted
   // word beside the count that earns it, which is all it needs to be guessable.
   var lean=n<MINSAMPLE;
   // The marker qualifies the ROW, so it sits where the row is named. Inline in
   // the Changed cell it pushed a two-digit count left of a three-digit one and
   // the column stopped lining up.
   var row='<tr><td>'+mono(p.model)+'</td>'+
    '<td><code>'+esc(p.client||'—')+'</code>'+
     (lean?'<span class="thin-mark" title="under '+MINSAMPLE+
      ' changed sessions: too few to read a rate from">thin</span>':'')+'</td>'+
    '<td class="num">'+esc(fmtCount(n))+'</td>'+
    '<td>'+stateBar(p,'mix-cell')+'</td>'+
    '<td class="num">'+(lean
      ? '<span class="muted">'+esc(fmtCount(p.passed||0)+' / '+fmtCount(n))+'</span>'
      : esc(pct(p.passed||0,n)))+'</td>';
   // The first per-session cell carries the divide the group header draws, so a
   // reader tracking a row never loses which side of it a figure is on.
   var first=' groupstart';
   if(money){
    row+=moneyCell({cost_usd:per(p.cost_usd||0),est_cost_usd:per(p.est_cost_usd||0)},first);
    first='';
   }
   row+='<td class="num'+first+'">'+esc(fmtCount(Math.round(per(p.out_tokens||0))))+'</td>'+
    num(fixed(per(p.tool_calls||0),1));
   if(status){
    var sn=p.status_sessions||0;
    row+='<td class="num">'+(sn>0?esc(fmtSpan((p.active_seconds||0)/sn)):'<span class="muted">—</span>')+'</td>';
   }
   row+=num(fixed(per(p.turns||0),1));
   return row+'</tr>';
  }).join('');
  return '<h3 class="chart-sub">By model and client</h3>'+
   '<div class="table-wrap"><table class="list data-table"><thead>'+head+
   '</thead><tbody>'+body+'</tbody></table></div>';
 }
 // The observed task type, which describes what a session's TOOLS were and not
 // what you asked for — which is why the column is headed "Kind of session"
 // rather than anything that sounds like a subject. Shown only where a row
 // clears the sample rule, and never pooled to make one that does.
 function taskSplit(rows){
  var by={},order=[];
  (rows||[]).forEach(function(r){
   var k=r.task||'';
   if(!k)return;
   if(!by[k]){by[k]=foldOutcome([]);by[k].task=k;order.push(k);}
   CHECK_TOTALS.forEach(function(f){by[k][f]+=r[f]||0;});
  });
  var keep=order.map(function(k){return by[k];})
   .filter(function(t){return (t.changed||0)>=MINSAMPLE;})
   .sort(function(a,b){return (b.changed||0)-(a.changed||0);});
  if(!keep.length)return '';
  return '<h3 class="chart-sub">By kind of session</h3>'+
   '<div class="table-wrap"><table class="list data-table tight"><thead><tr><th>Kind</th>'+
   uh('Changed','sessions')+'<th class="nosort">Checks</th>'+uh('Passed','of changed')+
   '</tr></thead><tbody>'+keep.map(function(t){
    return '<tr><td>'+esc(t.task)+'</td>'+num(fmtCount(t.changed))+
     '<td>'+stateBar(t,'mix-cell')+'</td>'+
     '<td class="num">'+esc(pct(t.passed||0,t.changed))+'</td></tr>';
   }).join('')+'</tbody></table></div>';
 }
 // The panel's own chip row: the shared clients and sources, plus the status
 // line's share over the CHANGED sessions rather than over every session, since
 // that is the set these figures are drawn from.
 function sourceChips(w,all){
  var out=clientChips(w);
  if(!out)return '';
  return '<div class="chips">'+out+
   chip('Cost',!!(w&&w.cost_reported),w&&w.cost_reported?'':'at list')+
   chip('Tokens',!!(w&&w.tokens_reported),'')+
   chip('Status line',(all.status_sessions||0)>0,
    (all.status_sessions||0)>0?pct(all.status_sessions,all.changed):'')+
   chip('Tool failures',!!(w&&w.failures_reported),'')+
   '</div>';
 }
 // Everything the panel must not be read as, in one closed disclosure.
 //
 // It is closed because moves above already carry it: the tiles establish the
 // denominator, the legend names the states, the headers name the units, and
 // nothing on the page is ranked or given a band. A reader who never opens this
 // is not misled by the default view — which is the whole test.
 function checkFineprint(all){
  var items='<li>A passing check means a check ran, its output read as a pass, and nothing was '+
   'edited after it. It does not show whether the work met your request because request text is not stored.</li>'+
   '<li>Rows are ordered by session count. The data does not control for task differences, so it does not rank models or clients.</li>'+
   '<li>No confidence band, at any sample size. These sessions are self-selected and their tasks '+
   'vary, so a confidence interval would require assumptions this measure does not meet.</li>'+
   '<li>A session counts as changed when an edit tool ran or your status line reported changed lines. '+
   'Shell-only edits are not detected, so those sessions are omitted.</li>'+
   '<li>The command and the result are read while the session is happening and neither is kept.</li>';
  if((all.changed||0)<MINSAMPLE){
   items+='<li>Under '+MINSAMPLE+' changed sessions every row is marked thin and shows counts '+
    'instead of a rate.</li>';
  }
  return '<details class="fineprint"><summary>How this is measured</summary><ul>'+items+'</ul></details>';
 }
 // Outcomes — the funnel, what you stopped doing, and the per-technique
 // drill-down. The half of this page that is about the playbook rather than
 // about the machine.
 // The member's own shown → adopted → helped, drawn as the flow the Outcomes
 // page draws for the organization.
 //
 // WHY THIS IS THE SECOND COPY OF A DRAWING AND ALLOWED TO BE. The registry
 // renders the org funnel in Go (outcomesviz.go FunnelFlow) because it owns
 // those numbers. It does not own these: on a shared registry the member's
 // usage arrives sealed and is opened in this browser, so a server that drew
 // this funnel would first have to be told what is in it — which is the one
 // thing this whole section promises never happens. The geometry is therefore
 // here, for the same reason plot() is: the data arrives by fetch.
 //
 // What is NOT copied is the component. Every class below — flow-svg, flow-band,
 // flow-bar, flow-name, flow-count, flow-conv, flow-drop, and the o1→o3 funnel
 // ramp — is the one already in app.css, so both themes, forced-colors and the
 // bloom filter follow for free, and one edit to the token block moves both
 // drawings.
 function funnelFlow(t){
  var stages=[{name:'Shown',tint:'o1',val:t.shown||0},
              {name:'Adopted',tint:'o2',val:t.adopted||0},
              {name:'Helped',tint:'o3',val:t.helped||0}];
  var conv=['adopted','helped'],drop=['not adopted','not helped'];
  var W=760,barW=58,maxH=132,botPad=12,topBand=56,H=topBand+maxH+botPad;
  var shown=stages[0].val;
  function height(v){
   if(v<=0)return 0;
   var hh=maxH*v/shown;
   return hh>8?hh:8;
  }
  var midY=topBand+maxH/2,barX=[10,(W-barW)/2,W-10-barW];
  var out='<svg class="flow-svg" viewBox="0 0 '+W+' '+H+'" role="img" aria-label="'+
   esc('Your funnel: '+fmtCount(stages[0].val)+' shown; '+fmtCount(stages[1].val)+
       ' adopted; '+fmtCount(stages[2].val)+' measurably helped.')+'">'+BLOOMDEFS;
  // Bands first, so the bars sit over their ends.
  for(var i=0;i<2;i++){
   var h1=height(stages[i].val),h2=height(stages[i+1].val);
   var x1=barX[i]+barW,x2=barX[i+1],mx=(x1+x2)/2;
   var t1=midY-h1/2,b1=midY+h1/2,t2=midY-h2/2,b2=midY+h2/2;
   out+='<path class="flow-band '+stages[i+1].tint+'" d="M'+x1+','+t1+' C'+mx+','+t1+' '+mx+','+t2+
    ' '+x2+','+t2+' L'+x2+','+b2+' C'+mx+','+b2+' '+mx+','+b1+' '+x1+','+b1+' Z"/>';
  }
  stages.forEach(function(st,i){
   var hh=height(st.val);
   out+='<rect class="flow-bar '+st.tint+'" x="'+barX[i]+'" y="'+(midY-hh/2)+'" width="'+barW+
    '" height="'+hh+'" rx="6"/>';
  });
  var anchors=['start','middle','end'],textX=[barX[0],W/2,barX[2]+barW];
  stages.forEach(function(st,i){
   out+='<text class="flow-name" x="'+textX[i]+'" y="18" text-anchor="'+anchors[i]+'">'+
    esc(st.name.toUpperCase())+'</text>'+
    '<text class="flow-count" x="'+textX[i]+'" y="44" text-anchor="'+anchors[i]+'">'+
    esc(fmtCount(st.val))+'</text>';
  });
  // Each band carries its own conversion, and — where the registry has one —
  // the same rate for everybody beside the loss. That second figure is what
  // makes the first legible: 88% adopted means one thing where everybody
  // adopts 68% and another where everybody adopts 90%.
  //
  // A stage that never happened has no rate to convert FROM, and its band stays
  // silent rather than printing 0/0.
  for(var j=0;j<2;j++){
   if(stages[j].val<=0)continue;
   var mid=(barX[j]+barW+barX[j+1])/2;
   var rate=Math.round(stages[j+1].val/stages[j].val*100);
   var lost=fmtCount(stages[j].val-stages[j+1].val)+' '+drop[j];
   if(ORG&&ORG.shown>0){
    var oFrom=j===0?ORG.shown:ORG.adopted,oTo=j===0?ORG.adopted:ORG.helped;
    if(oFrom>0)lost+=' \u00b7 registry '+Math.round(oTo/oFrom*100)+'%';
   }
   out+='<text class="flow-conv" x="'+mid+'" y="'+(midY-4)+'" text-anchor="middle">'+
    esc(rate+'% '+conv[j])+'</text>'+
    '<text class="flow-drop" x="'+mid+'" y="'+(midY+14)+'" text-anchor="middle">'+esc(lost)+'</text>';
  }
  out+='</svg>';
  // The phone rendering of the same journey. A 760-unit viewBox in a phone
  // column halves every label past legibility, so app.css swaps the flow for
  // this below 640px — the same swap the Outcomes funnel makes, and the reason
  // this markup has to be here: without a .flow-phone sibling the rule that
  // hides .flow-svg would leave the panel with nothing in it.
  //
  // Three bars rather than a second drawing. The stages keep their own ramp
  // (o1 → o3), so the phone reading is the same picture stood on its end.
  out+='<div class="flow-phone">'+bars([
   {label:'Shown',value:stages[0].val,key:'o1'},
   {label:'Adopted',value:stages[1].val,key:'o2',
    sub:stages[0].val>0?pct(stages[1].val,stages[0].val)+' of shown':''},
   {label:'Helped',value:stages[2].val,key:'o3',
    sub:stages[1].val>0?pct(stages[2].val,stages[1].val)+' of adopted':''}
  ],'')+'</div>';
  return out;
 }
 function resultsView(d,w){
  var t=(d&&d.totals)||{};
  var funnel=(t.shown||0)+(t.adopted||0)+(t.helped||0)+(t.dismissed||0)+(t.offered||0);
  var shownAny=(t.shown||0)+(t.adopted||0)+(t.helped||0)>0;
  var html='<section class="panel"><h2>Outcomes</h2>';
  // shown > 0, not shownAny: the flow's every height is a share of shown, and
  // a funnel drawn from a zero denominator is not a narrower funnel — it is
  // arithmetic nobody can do.
  if((t.shown||0)>0){
   // The same shape the Outcomes hero uses: the one rate worth leading with in
   // its own column, the journey beside it. Five flat plates said the same
   // thing and left the reader to work out that three of them were one story.
   var rate=pct(t.helped||0,t.shown||0);
   html+='<div class="hero-grid"><div class="hero-fig">'+
    '<div class="hero-value">'+esc(rate)+'</div>'+
    '<p class="hero-what">of what you were shown <b>measurably helped</b></p>';
   // The org rate rides under the figure rather than beside it in the flow:
   // this is the headline, and a headline with nothing to compare it to is
   // where a member most often invents a comparison of their own.
   if(ORG&&ORG.shown>0){
    html+='<p class="hero-sub"><span class="muted">'+esc(pct(ORG.helped,ORG.shown))+
     ' across the registry</span></p>';
   }
   html+='</div><div class="hero-flow">'+funnelFlow(t)+'</div></div>';
  }
  // Queries and questions are not funnel stages — one is what you asked, the
  // other what the agent asked you — so they stay plates beneath the journey
  // rather than being drawn into it.
  html+='<div class="tiles">'+
   tile('Queries',t.queries||0,'',true)+
   tile('Questions',t.offered||0,'',true)+
   ((t.shown||0)>0?tile('Dismissed',t.dismissed||0,
     (t.shown||0)>0?pct(t.dismissed||0,t.shown||0)+' of shown':'',null):'')+
   '</div>';
  if(((t.queries||0)+funnel)===0){
   html+='<p class="empty">No '+esc(PRODUCT)+' activity recorded on this machine in this window yet. '+
    'New usage will appear here. '+
    'See <a href="/docs/user-guide/20-sessions/04-work-with-suggestions.md">how suggestions arrive in a session</a>, '+
    'or browse <a href="/techniques">the playbook</a>.</p>';
  }else if(!shownAny){
   html+='<p class="empty">'+(t.queries||0)+' quer'+((t.queries||0)===1?'y':'ies')+
    ', and no suggestions shown yet in this window. '+
    'The shown \u2192 adopted \u2192 helped funnel fills once you\u2019re connected to a registry that delivers '+
    '<a href="/techniques">techniques</a>.</p>';
  }
  html+='</section>';
  // Ahead of "Code still present", because it asks the earlier question: whether the
  // work was checked at all comes before whether it survived the branch, and
  // the two are separate measurements over separate sets. One is sessions; the
  // other is a project over a window somebody asked about by hand.
  html+=checkPanel(w);
  html+=landedPanel(d,w);
  html+=dormantPanel(d.techniques);
  // The funnel's own series comes from the usage log rather than the session
  // log, so it is drawn from its own days.
  var ubks=buckets((d&&d.series)||[],d&&d.from,d&&d.to);
  if(ubks.length){
   var uunit=ubks[0].label.indexOf('wk ')===0?'week':'day';
   html+='<section class="panel chart-panel"><h2>Over time</h2>'+
    plot('Queries',ubks,[{name:'Queries',key:'s5',f:'queries'}],118,'Queries per '+uunit,!shownAny);
   if(shownAny){
    html+=plot('Suggestions',ubks,
     [{name:'Shown',key:'s1',f:'shown'},{name:'Adopted',key:'s2',f:'adopted'}],
     168,'Suggestions shown and adopted per '+uunit,true,
     legendFor([['Shown','s1'],['Adopted','s2']]),'',0);
   }
   html+='</section>';
  }
  if(shownAny)html+=techniquesTable(d.techniques);
  return html;
 }
 // The shell wires its charts and tables at load; everything this page builds
 // arrives afterwards, so it is enhanced by hand with the shell's own
 // implementations — which is where the numeric columns, the stable secondary
 // order, the aria-sort states and the chart tooltips all come from. Events does
 // the same with its aggregate table.
 var jumped=0;
 function wireUp(){
  if(window.tacitWireViz)root.querySelectorAll('svg.viz').forEach(window.tacitWireViz);
  if(window.tacitSortableTable){
   root.querySelectorAll('table.data-table').forEach(window.tacitSortableTable);
  }
  wireTableMore();
  // A drill-down here can name a panel rather than a page. The browser cannot
  // honour the fragment on its own: nothing on this page exists until the
  // ledger has been read and rendered, and by then the jump has been and gone.
  // So it happens here, when the target finally does — once, so that changing
  // the breakdown or the sort does not drag the reader back down the page.
  // Landing clear of the sticky bar is the [id] scroll-margin-top in app.css.
  if(!jumped&&/^#[\w-]+$/.test(location.hash)){
   var want=root.querySelector(location.hash);
   if(want){jumped=1;want.scrollIntoView();}
  }
 }
 function panel(title,body){
  root.innerHTML='<section class="panel"><h2>'+esc(title)+'</h2>'+body+'</section>';
 }
 // The sealed ledger (ledger.go). The key lives in this browser and nowhere
 // else: it is never sent to the registry, which is what lets the registry hold
 // the numbers at all. LKEY is the AES key, LID the address its machines file
 // under; neither can be derived from the other, so the member pastes both as
 // one token and we keep them together.
 var LSTORE='tacit.usage.key';
 function loadToken(){
  try{
   var raw=localStorage.getItem(LSTORE)||'';
   var dot=raw.indexOf('.');
   if(dot<1)return null;
   return {key:raw.slice(0,dot),id:raw.slice(dot+1)};
  }catch(e){return null;} // a browser refusing storage is a no-key browser
 }
 function saveToken(raw){try{localStorage.setItem(LSTORE,raw);}catch(e){}}
 function forgetToken(){try{localStorage.removeItem(LSTORE);}catch(e){}}
 function b64urlBytes(s){
  s=s.replace(/-/g,'+').replace(/_/g,'/');
  while(s.length%4)s+='=';
  var bin=atob(s),out=new Uint8Array(bin.length);
  for(var i=0;i<bin.length;i++)out[i]=bin.charCodeAt(i);
  return out;
 }
 // The other half of internal/ledger's Seal: a 12-byte nonce, then AES-256-GCM.
 // If this and the Go side ever disagree the member sees "cannot open", which
 // is the correct failure — better a refusal than a half-read record.
 function openSealed(keyBytes,b64){
  var blob=b64urlBytes(b64.replace(/\+/g,'-').replace(/\//g,'_'));
  return crypto.subtle.importKey('raw',keyBytes,{name:'AES-GCM'},false,['decrypt'])
   .then(function(k){
     return crypto.subtle.decrypt({name:'AES-GCM',iv:blob.slice(0,12)},k,blob.slice(12));
   })
   .then(function(buf){return JSON.parse(new TextDecoder().decode(buf));});
 }
 // Merging one member's machines.
 //
 // This is a SECOND implementation of an aggregation Go already does, which is
 // normally the thing to refuse — but the plaintext exists only in this browser,
 // so the registry cannot add up what it cannot read. Three rules keep it from
 // drifting into a different answer than the one machine it agrees with today:
 // it sums only named count fields, it never averages a rate (every rate on the
 // page is recomputed from summed parts by the renderer), and anything it
 // cannot combine honestly is dropped rather than guessed.
 //
 // Summing across machines does not double count: a session happens on one
 // machine and is recorded once, in that machine's log.
 var USAGE_TOTALS=['queries','shown','adopted','helped','dismissed','offered'];
 var USAGE_DAY=['queries','shown','adopted','helped'];
 var USAGE_TECH=['shown','adopted','helped','dismissed','prev_adopted'];
 var USAGE_MODEL=['queries','shown','adopted','helped','dismissed'];
 var WORK_TOTALS=['sessions','turns','tool_calls','retries','corrections','seconds','in_tokens','out_tokens','cost_usd',
  'status_sessions','lines_added','lines_removed','active_seconds','cache_requests','cache_misses',
  'cache_write_tokens','cache_read_tokens','est_cost_usd','tool_offers','tool_calls_offered'];
 // A delegation row is counts plus the totals of the sessions that used it, and
 // all of them add across machines: a session happens on one machine and is
 // recorded once, so two machines' rows for the same subagent are two disjoint
 // sets of sessions.
 var DELEGATION_TOTALS=['calls','sessions','session_cost','session_tokens','session_turns'];
 // An outcome row is check evidence plus the effort of the same changed
 // sessions, and every field of it adds across machines: a session happens on
 // one machine and is recorded once, so two machines' rows for the same
 // (model, client, task) are two disjoint sets of sessions. Nothing here is a
 // rate — every rate on the panel is recomputed from these sums by the
 // renderer — so there is nothing for the merge to average wrongly.
 // The separator every composite key here uses, as the merge does: a byte no
 // model label, client name or task type can contain.
 var SEP='\u0000';
 var CHECK_TOTALS=['changed','passed','failed','stale','unknown','attempted',
  'turns','tool_calls','out_tokens','seconds','active_seconds','lines_added','lines_removed',
  'status_sessions','cost_usd','est_cost_usd'];
 function addFields(dst,src,fields){
  for(var i=0;i<fields.length;i++){var f=fields[i];dst[f]=(dst[f]||0)+(src[f]||0);}
 }
 function minTime(a,b){if(!a)return b;if(!b)return a;return a<b?a:b;}
 function maxTime(a,b){if(!a)return b;if(!b)return a;return a>b?a:b;}
 // Combine keyed rows, summing counts and widening the first/last span. carry
 // names the string fields that identify a row rather than measure it, so a
 // technique keeps its name and a model its label.
 // maxes names the fields that are LEVELS: two machines that each half-filled a
 // context did not between them fill it once, so those take the larger rather
 // than the sum. Everything not named here is a count and is added.
 function mergeRows(lists,keyOf,fields,carry,maxes){
  maxes=maxes||[];
  var by={},order=[];
  for(var i=0;i<lists.length;i++){
   var rows=lists[i]||[];
   for(var j=0;j<rows.length;j++){
    var row=rows[j],k=keyOf(row);
    if(!by[k]){by[k]={};order.push(k);for(var c=0;c<carry.length;c++)by[k][carry[c]]=row[carry[c]];}
    var into=by[k];
    for(var c2=0;c2<carry.length;c2++)if(!into[carry[c2]]&&row[carry[c2]])into[carry[c2]]=row[carry[c2]];
    for(var m=0;m<maxes.length;m++){
     if((row[maxes[m]]||0)>(into[maxes[m]]||0))into[maxes[m]]=row[maxes[m]];
    }
    addFields(into,row,fields);
    into.first=minTime(into.first,row.first);
    into.last=maxTime(into.last,row.last);
    into.last_adopted=maxTime(into.last_adopted,row.last_adopted);
   }
  }
  return order.map(function(k){
   var r=by[k];
   // Drop the span fields again where no row carried them, so an absent date
   // stays absent rather than arriving as an empty string the renderer has to
   // second-guess.
   if(!r.first)delete r.first;
   if(!r.last)delete r.last;
   if(!r.last_adopted)delete r.last_adopted;
   return r;
  });
 }
 // mergeTools folds each machine's tool accounts into one. Calls and sessions
 // add; the cross-tabs add per key; "last used" takes the later of the two,
 // because it is a date and not a count.
 var TOOL_TABS=['harness','model','task','project','days'];
 function mergeTools(lists){
  var by={},order=[];
  (lists||[]).forEach(function(rows){
   (rows||[]).forEach(function(t){
    var into=by[t.name];
    if(!into){
     into={name:t.name,kind:t.kind,detail_kind:t.detail_kind,calls:0,offers:0,calls_offered:0,sessions:0,last:'',_detail:{}};
     TOOL_TABS.forEach(function(f){into[f]={};});
     by[t.name]=into;order.push(t.name);
    }
    if(!into.detail_kind&&t.detail_kind)into.detail_kind=t.detail_kind;
    into.calls+=t.calls||0;
    into.offers+=t.offers||0;
    into.calls_offered+=t.calls_offered||0;
    into.sessions+=t.sessions||0;
    (t.detail||[]).forEach(function(d){into._detail[d.key]=(into._detail[d.key]||0)+(d.count||0);});
    into.last=maxTime(into.last,t.last);
    TOOL_TABS.forEach(function(f){
     var src=t[f]||{};
     Object.keys(src).forEach(function(k){into[f][k]=(into[f][k]||0)+src[k];});
    });
   });
  });
  return order.map(function(n){
    var t=by[n];
    t.detail=Object.keys(t._detail).map(function(k){return {key:k,count:t._detail[k]};})
     .sort(function(x,y){return y.count-x.count||(x.key<y.key?-1:1);});
    delete t._detail;
    return t;
   }).sort(function(a,b){return (b.calls||0)-(a.calls||0)||(a.name<b.name?-1:1);});
 }
 // Models carry nested cross-tabs, exactly as tools do, so they need a merge of
 // their own rather than the keyed-row one: the counts inside harness, task,
 // project, turn_times and variants have to add per key, or a member with two
 // machines reads one machine's breakdown under both machines' totals.
 var MODEL_TABS=['harness','task','project','turn_times','variants','effort'];
 function mergeModels(lists){
  var by={},order=[];
  (lists||[]).forEach(function(rows){
   (rows||[]).forEach(function(m){
    var into=by[m.key];
    if(!into){
     into={key:m.key};
     MODEL_TABS.forEach(function(f){into[f]={};});
     by[m.key]=into;order.push(m.key);
    }
    addFields(into,m,WORK_TOTALS);
    into.first=minTime(into.first,m.first);
    into.last=maxTime(into.last,m.last);
    // A level, not a total: two machines that each half-filled a context did
    // not between them fill it once.
    if((m.peak_context||0)>(into.peak_context||0))into.peak_context=m.peak_context;
    MODEL_TABS.forEach(function(f){
     var src=m[f]||{};
     Object.keys(src).forEach(function(k){into[f][k]=(into[f][k]||0)+src[k];});
    });
   });
  });
  return order.map(function(k){return by[k];})
   .sort(function(a,b){return (b.sessions||0)-(a.sessions||0)||(a.key<b.key?-1:1);});
 }
 function mergeUsage(parts){
  if(parts.length===1)return parts[0];
  var out={window:parts[0].window,totals:{},merged:true};
  for(var i=0;i<parts.length;i++){
   addFields(out.totals,parts[i].totals||{},USAGE_TOTALS);
   out.from=minTime(out.from,parts[i].from);
   out.to=maxTime(out.to,parts[i].to);
  }
  out.series=mergeRows(parts.map(function(p){return p.series;}),
   function(r){return r.date;},USAGE_DAY,['date']);
  out.series.sort(function(a,b){return a.date<b.date?-1:1;});
  out.techniques=mergeRows(parts.map(function(p){return p.techniques;}),
   function(r){return r.cap;},USAGE_TECH,['cap','name']);
  var models=mergeRows(parts.map(function(p){return p.models;}),
   function(r){return r.model;},USAGE_MODEL,['model']);
  // Same rule the local summary follows: one row restating the totals above it
  // is a grid pretending to be a comparison.
  if(models.length>1)out.models=models;
  return out;
 }
 function mergeWork(parts){
  parts=parts.filter(function(p){return p;});
  if(!parts.length)return null;
  if(parts.length===1)return parts[0];
  var out={window:parts[0].window,totals:{},merged:true,
   tokens_reported:false,cost_reported:false,context_reported:false,
   peak_context:0,peak_context_pct:0,quotas:{},quota_history:{},work_history:[]};
  for(var i=0;i<parts.length;i++){
   var p=parts[i];
   addFields(out.totals,p.totals||{},WORK_TOTALS);
   out.from=minTime(out.from,p.from);
   out.to=maxTime(out.to,p.to);
   out.tokens_reported=out.tokens_reported||!!p.tokens_reported;
   out.cost_reported=out.cost_reported||!!p.cost_reported;
   // Context is a LEVEL, so it maxes where everything else sums. Two machines
   // that each half-filled a window did not between them fill it once, and a
   // summed peak would be the one figure on this page that cannot happen.
   out.context_reported=out.context_reported||!!p.context_reported;
   if((p.peak_context||0)>(out.peak_context||0))out.peak_context=p.peak_context;
   if((p.peak_context_pct||0)>(out.peak_context_pct||0))out.peak_context_pct=p.peak_context_pct;
   // An allowance belongs to the ACCOUNT, not to a machine: two machines
   // spending one are spending the same one, so the later reading is the whole
   // truth and adding or maxing them would both be wrong. Per provider, though
   // — Claude's allowance and OpenAI's are two, and latest-wins across them
   // would let one machine's Codex reading hide another's Claude reading.
   (p.quotas||[]).forEach(function(q){
    var have=out.quotas[q.source||''];
    if(!have||String(q.at)>String(have.at))out.quotas[q.source||'']=q;
   });
   // The climb merges rather than picking a winner: two machines sampling one
   // account saw the same allowance at different moments, and between them they
   // have more of the curve than either. Same instant, same reading — the
   // duplicate goes.
   out.work_history=out.work_history.concat(p.work_history||[]);
   (p.quota_history||[]).forEach(function(h){
    var into=out.quota_history[h.source||''];
    if(!into){into={source:h.source,source_label:h.source_label,five:[],week:[],spend:[]};
     out.quota_history[h.source||'']=into;}
    into.five=into.five.concat(h.five||[]);
    into.week=into.week.concat(h.week||[]);
    into.spend=into.spend.concat(h.spend||[]);
   });
   // The LATEST of the horizons, not the earliest: full records only reach
   // back as far as the machine that keeps the least, and claiming otherwise
   // would read a compacted stretch as a quiet one.
   out.detail_from=maxTime(out.detail_from,p.detail_from);
  }
  // Back to the ordered list the summary sends, so the plates do not shuffle
  // between renders and the single-allowance case reads as it always did.
  out.quotas=Object.keys(out.quotas).sort().map(function(k){return out.quotas[k];});
  // Two machines each did their own work: the points interleave, and a point
  // from one is never a duplicate of a point from the other even at the same
  // instant. Sorted, and left alone.
  out.work_history=(out.work_history||[]).slice().sort(function(a,b){return a.at<b.at?-1:1;});
  out.quota_history=Object.keys(out.quota_history).sort().map(function(k){
   var h=out.quota_history[k];
   h.five=dedupePoints(h.five);h.week=dedupePoints(h.week);h.spend=dedupePoints(h.spend);
   return h;
  });
  out.models=mergeModels(parts.map(function(p){return p.models;}));
  out.projects=mergeRows(parts.map(function(p){return p.projects;}),
   function(r){return r.key;},WORK_TOTALS,['key']);
  out.task_types=mergeRows(parts.map(function(p){return p.task_types;}),
   function(r){return r.key;},WORK_TOTALS,['key']);
  // Clients merge like every other group: a session runs on one machine under
  // one client and is recorded once, so two machines' rows for Claude Code are
  // two disjoint sets of sessions. The coverage sentence then names the union,
  // which is the honest answer for a member reading two laptops together.
  out.clients=mergeRows(parts.map(function(p){return p.clients;}),
   function(r){return r.key;},WORK_TOTALS,['key']);
  // The outcome rows, keyed on all three of model, client and task so a pair
  // one machine never ran stays present rather than arriving as a zero row —
  // and so the pair totals the panel derives are the sum of real rows.
  out.model_clients=mergeRows(parts.map(function(p){return p.model_clients;}),
   function(r){return (r.model||'')+SEP+(r.client||'')+SEP+(r.task||'');},
   CHECK_TOTALS,['model','client','task'])
   .sort(function(a,b){return (b.changed||0)-(a.changed||0)||
    (String(a.model)<String(b.model)?-1:1);});
  out.days=mergeRows(parts.map(function(p){return p.days;}),
   function(r){return r.date+'\u0000'+(r.model||'');},WORK_TOTALS,['date','model'],['peak_context']);
  out.days.sort(function(a,b){return a.date<b.date?-1:1;});
  out.repeats=mergeRows(parts.map(function(p){return p.repeats;}),
   function(r){return r.hash;},['count'],['hash','raised']);
  // Tools carry nested cross-tabs, so they need a merge of their own rather
  // than the keyed-row one: the counts inside harness/model/task/project/days
  // have to add per key, or a member with two machines sees one machine's
  // breakdown under both machines' totals.
  out.tools=mergeTools(parts.map(function(p){return p.tools;}));
  out.commands=mergeRows(parts.map(function(p){return p.commands;}),
   function(r){return r.name;},['calls'],['name','kind'])
   .sort(function(x,y){return (y.calls||0)-(x.calls||0)||(x.name<y.name?-1:1);});
  out.delegations=mergeRows(parts.map(function(p){return p.delegations;}),
   function(r){return r.kind+'\u0000'+r.name;},DELEGATION_TOTALS,['name','kind'])
   .sort(function(x,y){return (y.session_cost||0)-(x.session_cost||0)||(y.calls||0)-(x.calls||0);});
  out.tool_pairs=mergeRows(parts.map(function(p){return p.tool_pairs;}),
   function(r){return r.a+'\u0000'+r.b;},['sessions'],['a','b'])
   .sort(function(x,y){return (y.sessions||0)-(x.sessions||0);});
  // Turn times keep the bucket order the summary sent them in; absent tools go
  // back to busiest-first, because merging two machines can reorder them.
  out.turn_times=mergeRows(parts.map(function(p){return p.turn_times;}),
   function(r){return r.key;},['count'],['key']);
  // Failure kinds keep the vocabulary's order, like the turn buckets: the
  // summary already sent them in it, so the merge preserves rather than sorts.
  // Hours and lengths keep the order the summary sent them in — round the clock
  // and short to long — so the merge preserves rather than sorts.
  out.hours=mergeRows(parts.map(function(p){return p.hours;}),
   function(r){return r.key;},['count'],['key']);
  out.hours.sort(function(a,b){return a.key<b.key?-1:1;});
  out.lengths=mergeRows(parts.map(function(p){return p.lengths;}),
   function(r){return r.key;},['count'],['key']);
  out.failures=mergeRows(parts.map(function(p){return p.failures;}),
   function(r){return r.key;},['count'],['key']);
  out.failures_reported=parts.some(function(p){return !!p.failures_reported;});
  out.absent_tools=mergeRows(parts.map(function(p){return p.absent_tools;}),
   function(r){return r.key;},['count'],['key'])
   .sort(function(a,b){return (b.count||0)-(a.count||0)||(a.key<b.key?-1:1);});
  // "Model change" is a claim about one machine's history. Two machines
  // switching at different times is not a switch, and there is no honest way to
  // combine them — so the merged view drops it and the per-machine view keeps
  // it.
  return out;
 }
 var LMACHINES=null,LCHOSEN=null;
 function ledgerRender(){
  if(!LMACHINES||!LMACHINES.length)return;
  // Default to every machine together, because "my usage" is the member's
  // question and they do not think of themselves as two laptops.
  var parts=LMACHINES,one=null;
  if(LCHOSEN){
   for(var i=0;i<LMACHINES.length;i++)if(LMACHINES[i].machine===LCHOSEN)one=LMACHINES[i];
   if(one)parts=[one];
  }
  var windows=[];
  for(var p=0;p<parts.length;p++){
   var w=(parts[p].payload.windows||{})[WINDOW];
   if(w)windows.push(w);
  }
  if(!windows.length){
   panel('No data for this period',
    '<p class="hint">'+esc(one?one.machine:'Your machines')+' published no data for this window. Select a longer window.</p>');
   ledgerNote(parts,one);
   return;
  }
  // The whole record, for the calendar alone. The payload holds every window
  // already, so this costs a lookup rather than a second decrypt.
  var history=[];
  for(var h=0;h<parts.length;h++){
   var all=(parts[h].payload.windows||{})['all'];
   if(all&&all.work)history.push(all.work);
  }
  render(mergeUsage(windows.map(function(w){return w.usage||{};})),
         mergeWork(windows.map(function(w){return w.work||null;})),
         history.length?mergeWork(history):null);
  ledgerNote(parts,one);
 }
 // Provenance. A page that did not say which machines these numbers came from,
 // and when they last reported, would be inviting the reader to assume it had
 // them all and had them now.
 function ledgerNote(parts,one){
  var last='';
  for(var i=0;i<parts.length;i++)last=(!last||parts[i].updated>last)?parts[i].updated:last;
  var names=parts.map(function(p){return p.machine;}).join(', ');
  var lead=one
   ? 'Read from <b>'+esc(one.machine)+'</b> alone'
   : (parts.length>1
      ? 'Read from <b>'+esc(String(parts.length))+' machines</b>: '+esc(names)
      : 'Read from <b>'+esc(names)+'</b>');
  var pick='';
  if(LMACHINES.length>1){
   pick=' <label class="hint">Show <select id="lmach"><option value=""'+(LCHOSEN?'':' selected')+'>all machines</option>';
   for(var j=0;j<LMACHINES.length;j++){
    var m=LMACHINES[j].machine;
    pick+='<option value="'+esc(m)+'"'+(m===LCHOSEN?' selected':'')+'>'+esc(m)+'</option>';
   }
   pick+='</select></label>';
  }
  var note=document.createElement('section');
  note.className='panel';
  note.innerHTML='<p class="hint">'+lead+', sealed on each machine and last published '+esc(last)+
   '. The registry stores it and cannot read it.'+pick+
   ' <a href="#" id="lforget">Forget this key</a></p>';
  root.appendChild(note);
  var sel=note.querySelector('#lmach');
  if(sel)sel.onchange=function(){LCHOSEN=sel.value;ledgerRender();};
  note.querySelector('#lforget').onclick=function(e){
   e.preventDefault();forgetToken();LMACHINES=null;LCHOSEN=null;load();
  };
 }
 // The panel a member sees before they have pasted anything. It says what is
 // there, because "your machines are publishing" and "nothing has ever been
 // published" need different next steps from the reader.
 function ledgerPrompt(hasLedger,err){
  wide(false);setHead('');
  var lead=hasLedger
   ? '<p class="hint">Your machines publish encrypted usage here. Enter your key to decrypt it in this browser.</p>'
   : '<p class="hint">The registry has no personal usage for this account.</p>';
  panel('Personal usage is stored on your machines',
   lead+
   (err?'<p class="hint"><b>'+esc(err)+'</b></p>':'')+
   '<p class="hint">Run <code>tacit usage --key</code> on a machine you work on and paste what it prints:</p>'+
   '<p class="inline-form"><input id="lkey" type="password" autocomplete="off" spellcheck="false" '+
   'placeholder="paste your usage key" aria-label="Your usage key">'+
   '<button type="button" id="lgo">Open</button></p>'+
   '<p class="hint">The key stays in this browser and is not sent to the registry. Anyone with the key can read your usage.</p>'+
   '<p class="hint">You can also run <code>tacit usage</code> on that machine for text output (<a href="/docs/user-guide/50-reference/16-command-reference.md">command reference</a>) or open <a href="'+AGENTURL+'">'+AGENTURL+'</a> on that machine. <a href="/outcomes">Outcomes</a> shows aggregate organization data without individual data.</p>');
  var box=root.querySelector('#lkey');
  root.querySelector('#lgo').onclick=function(){
   var raw=(box.value||'').trim();
   if(raw.indexOf('.')<1){ledgerPrompt(hasLedger,'That does not look like a usage key.');return;}
   saveToken(raw);
   load();
  };
  box.onkeydown=function(e){if(e.key==='Enter')root.querySelector('#lgo').click();};
 }
 // The local proxy said no. Try the sealed ledger before giving up: this is the
 // path every member whose registry runs somewhere else takes.
 function localOnly(){
  var tok=loadToken();
  if(!tok){
   // Ask whether anything is filed for this reader, so the prompt can tell
   // them which situation they are in. A registry without the binding, or a
   // reader who never bound, simply gets the colder copy.
   fetch(APP+'/usage/ledger/mine',{headers:{'Accept':'application/json'},credentials:'same-origin'})
    .then(function(r){return r.json();})
    .then(function(d){ledgerPrompt(!!(d&&d.id),null);})
    .catch(function(){ledgerPrompt(false,null);});
   return;
  }
  root.innerHTML='<p class="hint">Decrypting usage…</p>';
  // Remember the address against this signed-in reader, so their next device
  // knows a ledger exists before they paste anything. The key is not sent.
  fetch(APP+'/usage/ledger/bind',{method:'POST',headers:{'Content-Type':'application/json'},
   credentials:'same-origin',body:JSON.stringify({id:tok.id})}).catch(function(){});
  fetch(APP+'/v1/ledger/'+encodeURIComponent(tok.id),
   {headers:{'Accept':'application/json'},credentials:'same-origin'})
   .then(function(r){return r.json();})
   .then(function(d){
     var slots=(d&&d.machines)||[];
     if(!slots.length){ledgerPrompt(false,'This ledger has no sessions yet. A machine publishes when its session ends, or within ten minutes of starting.');return;}
     var keyBytes=b64urlBytes(tok.key);
     return Promise.all(slots.map(function(sl){
       return openSealed(keyBytes,sl.sealed)
        .then(function(p){return {machine:sl.machine,updated:sl.updated,payload:p};})
        .catch(function(){return null;});
     })).then(function(all){
       LMACHINES=all.filter(function(x){return x&&x.payload&&x.payload.schema===1;});
       if(!LMACHINES.length){ledgerPrompt(true,'That key did not open anything filed here. Check you pasted the whole line.');return;}
       ledgerRender();
     });
   })
   .catch(function(e){ledgerPrompt(true,e.message);});
 }
 function unreachable(detail){
  wide(false);setHead('');
  // A literal apostrophe, not an entity: panel() escapes the title, so an
  // entity here shows through to the reader as text.
  panel('Couldn’t load usage',
   '<p class="hint">'+esc(detail)+'</p>'+
   '<p class="hint">The registry reads usage from this host&rsquo;s hook agent. Start a '+esc(PRODUCT)+' session on the host if the agent has stopped, then reload. '+
   'If it keeps failing, see <a href="/docs/user-guide/50-reference/15-troubleshooting.md">Troubleshooting</a>.</p>');
 }
 function get(path,window){
  return fetch(APP+path+'?window='+encodeURIComponent(window||WINDOW),
   {headers:{'Accept':'application/json'},credentials:'same-origin'})
   .then(function(r){return r.json().then(function(d){return {ok:r.ok,d:d};});});
 }
 function load(){
  root.innerHTML='<p class="hint">Loading usage…</p>';
  // Three feeds, one page: how you use the agent, how you work over the
  // period, and the whole record — which only the calendar reads, because a
  // calendar of five columns is not a rhythm and the question it answers is a
  // longer one than any period control should decide. The second and third are
  // each allowed to fail alone: a hook agent from an older build serves no
  // session feed, and the rest of the page should not go dark over it.
  var quiet=function(){return {ok:false,d:{}};};
  Promise.all([get('/usage/data'),get('/usage/sessions').catch(quiet),
   get('/usage/sessions','all').catch(quiet)])
   .then(function(r){
     var x=r[0],y=r[1],z=r[2];
     if(x.d&&x.d.local_only){localOnly();return;}
     if(!x.ok||(x.d&&x.d.error)){unreachable((x.d&&x.d.error)||('HTTP '+0));return;}
     var usable=function(f){return (f.ok&&f.d&&!f.d.local_only&&!f.d.error)?f.d:null;};
     render(x.d,usable(y),usable(z));
   })
   .catch(function(e){unreachable(e.message);});
 }
 load();
})();
