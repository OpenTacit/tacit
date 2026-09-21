// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

// FilterMenuJS gives every URL-backed filter menu the same disclosure and
// find-within-menu behavior. Form submission still works without JavaScript.
const FilterMenuJS = `
 document.querySelectorAll('details.fmenu').forEach(function(menu){
   menu.addEventListener('toggle',function(){
     if(!menu.open)return;
     document.querySelectorAll('details.fmenu[open]').forEach(function(other){if(other!==menu)other.open=false;});
     var search=menu.querySelector('.fmenu-search');
     if(search)search.focus();
   });
 });
 document.addEventListener('click',function(event){
   if(event.target.closest('details.fmenu'))return;
   document.querySelectorAll('details.fmenu[open]').forEach(function(menu){menu.open=false;});
 });
 document.querySelectorAll('.fmenu-search').forEach(function(search){
   search.addEventListener('input',function(){
     var value=search.value.toLowerCase();
     search.closest('.fmenu-pop').querySelectorAll('.fmenu-opt').forEach(function(row){
       row.style.display=row.textContent.toLowerCase().includes(value)?'':'none';
     });
   });
 });`
