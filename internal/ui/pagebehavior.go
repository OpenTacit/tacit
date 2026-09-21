// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

// FindRowsJS narrows rows already on screen. URL-backed filters remain a
// separate control and still scope the data on the server.
const FindRowsJS = `
 var findInput=document.getElementById('q');
 if(findInput&&!document.querySelector('main [data-search]'))findInput.closest('.search').style.display='none';
 else if(findInput)findInput.addEventListener('input',function(event){
   var value=event.target.value.toLowerCase();
   document.querySelectorAll('main [data-search]').forEach(function(row){
     row.style.display=row.dataset.search.includes(value)?'':'none';
   });
 });`

// HeatmapEdgesJS marks hidden columns on either side of a wide heatmap.
const HeatmapEdgesJS = `
 document.querySelectorAll('.org-heatmap').forEach(function(heatmap){
   function edges(){
     heatmap.classList.toggle('can-left',heatmap.scrollLeft>2);
     heatmap.classList.toggle('can-right',heatmap.scrollLeft<heatmap.scrollWidth-heatmap.clientWidth-2);
   }
   heatmap.addEventListener('scroll',edges,{passive:true});
   window.addEventListener('resize',edges,{passive:true});
   edges();
 });`

// PageControlsJS moves period and filter controls onto the breadcrumb line.
// They stay in the page body and remain usable when JavaScript is off.
const PageControlsJS = `
 var windowNav=document.querySelector('.window-nav'),crumbs=document.querySelector('nav.crumbs');
 var outcomesControls=document.querySelector('.outcomes-controls');
 if(crumbs){
   if(outcomesControls){crumbs.appendChild(outcomesControls);if(windowNav)outcomesControls.appendChild(windowNav);}
   else if(windowNav){crumbs.appendChild(windowNav);}
 }
 var windowSelect=document.querySelector('.window-select');
 if(windowSelect)windowSelect.addEventListener('change',function(){location.href=this.value;});`
