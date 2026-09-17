// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"fmt"
	"html"
	"time"
)

// Absolute times are rendered in the VIEWER's timezone, not the server's and
// not UTC. A registry is read by members in whatever zones they are in, and a
// column headed "last seen" that silently means "UTC" is a column that is
// wrong for almost everyone reading it — quietly, and by up to a day.
//
// The server cannot know that zone at render time (it has no request header
// that carries it, and a cookie would still be absent on the first view), so
// the instant is what travels: every absolute time goes out as a <time> whose
// datetime attribute is the RFC3339 instant, and LocalTimeScript re-formats it
// against the browser's own clock on load. Text rendered server-side into the
// element is the no-JS fallback, in UTC and labelled as such by the format
// where there is room for a zone at all.
//
// Only ABSOLUTE times need this. An age ("3d ago") is a duration between two
// instants and reads the same in every zone, so those stay server-rendered.
const (
	LTDate   = "d"   // Jul 28
	LTWeek   = "w"   // wk Jul 28
	LTYMD    = "ymd" // 2026-07-28 — sortable as text, which the table sort relies on
	LTStamp  = "ts"  // Jul 28, 15:04 PDT
	LTDateHM = "dhm" // Jul 28 15:04
	LTHM     = "hm"  // 15:04
	LTHMS    = "hms" // 15:04:05
)

// LTFallback renders the same shape the browser will, in UTC, for a reader
// whose script never runs. Exported for callers that build an Axis by hand and
// want its fallback labels to match what the browser would produce.
func LTFallback(t time.Time, kind string) string {
	t = t.UTC()
	switch kind {
	case LTYMD:
		return t.Format("2006-01-02")
	case LTStamp:
		return t.Format("Jan 2, 15:04 MST")
	case LTDateHM:
		return t.Format("Jan 2 15:04")
	case LTHM:
		return t.Format("15:04")
	case LTHMS:
		return t.Format("15:04:05")
	case LTWeek:
		return "wk " + t.Format("Jan 2")
	default:
		return t.Format("Jan 2")
	}
}

// LocalTime renders an instant as a <time> element that LocalTimeScript
// rewrites into the viewer's zone. A zero time renders nothing: it is the
// absence of a timestamp, not an instant in year one.
func LocalTime(t time.Time, kind string) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf(`<time datetime="%s" data-lt="%s">%s</time>`,
		html.EscapeString(t.UTC().Format(time.RFC3339)), kind, html.EscapeString(LTFallback(t, kind)))
}

// LocalTimeISO is LocalTime for a timestamp that arrives as the RFC3339 string
// it was stored as. Anything unparseable is passed through as escaped text
// rather than dropped — a stored value in an unexpected shape is still what
// the record says, and hiding it would hide the problem too.
func LocalTimeISO(rfc3339, kind string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return html.EscapeString(rfc3339)
	}
	return LocalTime(t, kind)
}

// LocalTimeScript rewrites every absolute time on the page into the viewer's
// timezone. Two passes, because absolute times reach the page in two shapes:
// as <time> elements in the document, and — for chart hover labels — as an
// attribute the tooltip reads at hover time, where there is no text node to
// rewrite.
//
// Both are idempotent and cheap enough to run on every page: a document with
// no times matches nothing and does nothing. It is exposed on window as
// tacitLocalTime(root) for the same reason tacitWireViz is — markup a page
// fetches and swaps in after load (the ingress rate chart) has never been
// through this pass, and would otherwise be the one part of the console still
// showing UTC.
const LocalTimeScript = `<script>(function(){
function p2(n){return n<10?'0'+n:''+n;}
// Built from the local getters rather than toLocaleDateString for ymd: this
// one is sorted and searched as text, so it has to stay ISO-shaped whatever
// the reader's locale would otherwise do with a date.
function fmt(d,k){
 if(k==='ymd')return d.getFullYear()+'-'+p2(d.getMonth()+1)+'-'+p2(d.getDate());
 if(k==='ts')return d.toLocaleString([],{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit',timeZoneName:'short'});
 // Chart axes stay on the 24-hour clock whatever the locale would prefer: the
 // ticks sit under their gridlines in a fixed gutter, and an am/pm suffix is
 // width the axis does not have.
 var hm=p2(d.getHours())+':'+p2(d.getMinutes());
 if(k==='hms')return hm+':'+p2(d.getSeconds());
 if(k==='hm')return hm;
 var s=d.toLocaleDateString([],{month:'short',day:'numeric'});
 if(k==='dhm')return s+' '+hm;
 return k==='w'?'wk '+s:s;
}
function at(iso){var d=new Date(iso);return isNaN(d.getTime())?null:d;}
function run(root){
 root=root||document;
 root.querySelectorAll('time[data-lt]').forEach(function(el){
  var d=at(el.getAttribute('datetime'));if(!d)return;
  var s=fmt(d,el.getAttribute('data-lt'));
  if(s===el.textContent)return;
  el.textContent=s;
  // Find-on-page tests a row's data-search, which the server built from the UTC
  // text. Add the local text so searching for what is actually on screen still
  // finds the row — a zone that shifts the date across midnight would otherwise
  // make the visible date unsearchable.
  var row=el.closest&&el.closest('[data-search]');
  if(row)row.dataset.search+=' '+s.toLowerCase();
 });
 root.querySelectorAll('[data-label-iso]').forEach(function(el){
  var d=at(el.getAttribute('data-label-iso'));if(!d)return;
  el.setAttribute('data-label',fmt(d,el.getAttribute('data-lt')||'d'));
 });
}
window.tacitLocalTime=run;
run(document);
})();</script>`
