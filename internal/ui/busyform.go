// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

// BusyFormJS puts a form into a visibly working state when it is submitted, for
// the ones whose work takes long enough that a click with no answer reads as a
// click that did not land.
//
// It is for a PLAIN form post — one that navigates when the server answers.
// There is nothing to poll and no completion to handle: the page goes away when
// the work is done, which is why this needs none of the machinery the
// suggestion run carries (that one is asynchronous and has to find its way back
// to a result it did not wait for).
//
// The bar is indeterminate on purpose. The duration is knowable in the rough —
// it scales with how much there is to compare — but not to the second, and a
// bar that fills against an invented deadline is a claim the page cannot honour.
// The sentence beside it says what is being worked on and how many, which is
// the part a reader can actually use.
//
// With JavaScript off the form still posts; it just posts without saying so.
//
//	<form method="post" action="…" data-busy="Comparing 108 techniques…">
//	  <button type="submit" data-busy-label="Comparing…">Find duplicates</button>
//	</form>
const BusyFormJS = `
 document.querySelectorAll('form[data-busy]').forEach(function(form){
   form.addEventListener('submit',function(){
     var note=form.getAttribute('data-busy')||'Working…';
     form.querySelectorAll('button').forEach(function(b){
       var busy=b.getAttribute('data-busy-label');
       if(busy)b.textContent=busy;
       // After the label, not before: a disabled submitter is not sent with the
       // form, so disabling first would drop the button's own name and value.
       setTimeout(function(){b.disabled=true;},0);
     });
     if(form.querySelector('.suggest-status'))return; // already running
     var status=document.createElement('div');
     status.className='suggest-status';
     status.setAttribute('role','status');
     status.innerHTML='<div class="suggest-bar indeterminate"><div class="suggest-bar-fill"></div></div>'+
       '<span class="suggest-line"></span>';
     status.querySelector('.suggest-line').textContent=note;
     form.appendChild(status);
   });
 });`
