// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

// SortableTableJS enhances each data table and exposes the same hook for views
// that add a table after load. It runs inside the shell's existing script.
const SortableTableJS = `
 function sortableTable(table){
   var body=table.tBodies[0], head=table.tHead;
   if(!body||!head||!head.rows.length||!body.rows.length)return;
   var rows=Array.prototype.slice.call(body.rows);
   rows.forEach(function(r,i){r.dataset.sortidx=i;});
   // The LAST header row is the column row. A table may carry a group row above
   // it — one label spanning the five columns that are all per session, which is
   // what retired the paragraph over the table saying so — and the group row has
   // no column to sort.
   var ths=Array.prototype.slice.call(head.rows[head.rows.length-1].cells);
   var active=-1, dir=0;
   function text(r,i){var c=r.cells[i];return c?c.textContent.trim():'';}
   function numOf(r,i){var m=text(r,i).replace(/,/g,'').match(/-?\d+(\.\d+)?/);return m?parseFloat(m[0]):null;}
   function render(){
     ths.forEach(function(th){th.classList.remove('sort-asc','sort-desc');th.setAttribute('aria-sort','none');});
     var ordered=rows.slice();
     if(active>=0&&dir!==0){
       var numeric=ths[active].classList.contains('num');
       ordered.sort(function(a,b){
         var c;
         if(numeric){
           var an=numOf(a,active), bn=numOf(b,active);
           if(an===null&&bn===null)c=0; else if(an===null)return 1; else if(bn===null)return -1; else c=an-bn;
         }else{var at=text(a,active).toLowerCase(),bt=text(b,active).toLowerCase();c=at<bt?-1:at>bt?1:0;}
         return c!==0?c*dir:(+a.dataset.sortidx)-(+b.dataset.sortidx);
       });
       ths[active].classList.add(dir>0?'sort-asc':'sort-desc');
       ths[active].setAttribute('aria-sort',dir>0?'ascending':'descending');
     }
     ordered.forEach(function(r){body.appendChild(r);});
   }
   ths.forEach(function(th,ci){
     // A column with nothing orderable in it does not get a caret. A cell whose
     // content is a bar has no text to compare, and offering to sort by it is an
     // affordance that lies.
     if(th.querySelector('input')||th.classList.contains('nosort'))return;
     th.classList.add('sortable');th.tabIndex=0;th.setAttribute('role','button');th.setAttribute('aria-sort','none');
     function cycle(){
       if(ci===active){if(dir>0){dir=-1;}else{dir=0;active=-1;}}else{active=ci;dir=1;}
       render();
     }
     th.addEventListener('click',cycle);
     th.addEventListener('keydown',function(e){if(e.key==='Enter'||e.key===' '){e.preventDefault();cycle();}});
   });
 }
 window.tacitSortableTable=sortableTable;
 document.querySelectorAll('table.data-table').forEach(sortableTable);`
