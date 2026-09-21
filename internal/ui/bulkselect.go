// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

// BulkSelectJS wires every bulk-action form on the page to the checkboxes that
// reference it by id. The table stays OUTSIDE the form — a row that navigates
// cannot be wrapped in one — so the boxes join it with the form attribute, and
// this is what makes the pair behave like a single control: the buttons stay
// disabled until something is ticked, the header box ticks the rows on screen,
// the whole cell is the target rather than the small square, and a destructive
// button asks first with the count in the question.
//
// It runs in the shell beside the table sort, so a page adds a selection column
// and a .bulk-actions form and writes no script of its own. The markup:
//
//	<form id="bulk-x" class="bulk-actions" method="post" action="…">
//	  <button name="do" value="suspend" disabled>Suspend</button>
//	  <button class="danger" name="do" value="release" disabled
//	          data-confirm="Release {n} name{s}? Anyone can claim them afterwards.">Release</button>
//	  <label data-all-for="bulk-x" data-all-count="812" hidden>
//	    <input type="checkbox" form="bulk-x" name="all" value="1" data-widen> Apply to all 812 matches</label>
//	</form>
//	<span class="sub" data-count-for="bulk-x"></span>
//	<th class="sel"><input type="checkbox" form="bulk-x" data-select-all aria-label="Select all"></th>
//	<td class="sel"><input type="checkbox" form="bulk-x" name="name" value="…"></td>
//
// The optional [data-all-for] label is what keeps a bulk control useful once a
// table pages: the boxes can only ever reach the rows on screen, so ticking the
// whole page reveals the offer to act on the whole match instead, and the
// count in a confirmation follows the wider scope.
const BulkSelectJS = `
 document.querySelectorAll('form.bulk-actions[id]').forEach(function(form){
   var owned='input[type="checkbox"][form="'+form.id+'"]';
   var all=document.querySelector(owned+'[data-select-all]');
   // Everything the form owns that names a row. The header box carries no
   // name, and the widening box is marked out of it: counting the control that
   // widens a selection as a member of it makes the page read as fully ticked
   // the moment anyone widens it.
   var boxes=[].slice.call(document.querySelectorAll(owned+'[name]:not([data-widen])'));
   if(!boxes.length)return;
   var buttons=[].slice.call(form.querySelectorAll('button'));
   var count=document.querySelector('[data-count-for="'+form.id+'"]');
   // The wider scope, on a table that pages: a whole-match offer that appears
   // only once the page itself is fully ticked.
   var scope=document.querySelector('[data-all-for="'+form.id+'"]');
   var scopeBox=scope&&scope.querySelector('input[type="checkbox"]');
   var scopeCount=scope&&scope.getAttribute('data-all-count');
   function ticked(){return boxes.filter(function(b){return b.checked;}).length;}
   function widened(){return !!(scopeBox&&scopeBox.checked);}
   function selected(){return widened()?scopeCount:ticked();}
   function sync(){
     var n=ticked(), full=n===boxes.length;
     // Offering "all 812" beside three ticked rows is an invitation to act on
     // the fleet by accident, so the offer goes away with the full page — and
     // takes any widening with it.
     if(scope){scope.hidden=!full;if(!full&&scopeBox)scopeBox.checked=false;}
     buttons.forEach(function(b){b.disabled=!n;});
     // Silent once the selection is widened: the ticked offer beside it
     // already says "all 163 matches", and a count repeating that is the same
     // sentence twice.
     if(count)count.textContent=(!n||widened())?'':n+' of '+boxes.length+' selected';
     // The header box reports the rows rather than leading them: half a
     // selection is neither ticked nor empty, and saying so is the difference
     // between a control and a lie.
     if(all){all.checked=full;all.indeterminate=n>0&&n<boxes.length;}
   }
   boxes.forEach(function(box){
     box.addEventListener('change',sync);
     // The whole cell is the target. A 2rem cell with a small square in it is
     // otherwise a row-navigation trap right beside the box.
     var cell=box.closest('td');
     if(cell)cell.addEventListener('click',function(e){
       if(e.target!==box){box.checked=!box.checked;sync();}});
   });
   if(all)all.addEventListener('change',function(){
     boxes.forEach(function(b){b.checked=all.checked;});sync();});
   if(scopeBox)scopeBox.addEventListener('change',sync);
   form.addEventListener('submit',function(e){
     var n=ticked();
     if(!n){e.preventDefault();return;}
     var many=selected();
     var ask=e.submitter&&e.submitter.getAttribute('data-confirm');
     if(ask&&!confirm(ask.replace(/{n}/g,many).replace(/{s}/g,+many===1?'':'s')))e.preventDefault();
   });
   sync();
 });`
