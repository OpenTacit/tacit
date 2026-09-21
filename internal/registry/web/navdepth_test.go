// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"
)

// A trail used to say where you were and never that there was anything
// underneath. A member on You / Now had no way to learn that You / Now /
// Allowance existed: the only menu on the line offered the four destinations,
// and nothing said there was a level below at all.
//
// So the caret is the trail's vocabulary for depth, and it reads both ways — a
// step with one is a level holding more than one page, a step without one is a
// level holding exactly one. These tests walk every section and check that the
// levels the router serves are the levels the line shows.

// nav is the breadcrumb of one page, reduced to what a reader can act on: the
// steps, which of them open, and what they open onto.
type nav struct {
	trail string
	steps int
	menus []string
}

func navOf(t *testing.T, body string) nav {
	t.Helper()
	i := strings.Index(body, `<nav class="crumbs"`)
	if i < 0 {
		t.Fatal("the page has no breadcrumb trail")
	}
	trail := body[i:][:strings.Index(body[i:], "</nav>")]
	out := nav{trail: trail, steps: strings.Count(trail, `crumb-sep`) + 1}
	for _, pop := range strings.Split(trail, `<div class="crumb-menu-pop">`)[1:] {
		out.menus = append(out.menus, pop[:strings.Index(pop, "</div>")])
	}
	return out
}

// Every route the router serves under a destination has to be reachable from
// the trail of that destination — which is the whole of the complaint this
// answers. The sets are spelled out rather than derived: a new page under a
// view is exactly the change that should fail here until its trail knows about
// it.
func TestEveryLevelIsReachableFromTheTrail(t *testing.T) {
	_, ts := newServer(t)
	for _, tc := range []struct {
		path  string
		steps int      // how many steps the line has
		opens []string // hrefs the trail must offer, across all its carets
		shut  []string // and what must NOT be on it
	}{
		// You: four destinations, three of which have a page under them.
		{path: "/usage?w=30d", steps: 3,
			opens: []string{"/usage/work?w=30d", "/usage/allowance?w=30d"}},
		{path: "/usage/work?w=30d", steps: 3,
			opens: []string{"/usage?w=30d", "/usage/tools?w=30d"}},
		{path: "/usage/cost?w=30d", steps: 3,
			opens: []string{"/usage/models?w=30d"}},
		// Outcomes holds nothing under it, so the line stops at two and offers
		// no third caret. The absence is the other half of the reading.
		{path: "/usage/results?w=30d", steps: 2,
			opens: []string{"/usage?w=30d"},
			shut:  []string{"/usage/allowance", "/usage/tools", "/usage/models"}},
		// A page at the level below names itself there, beside the Overview it
		// shares the level with.
		{path: "/usage/allowance?w=30d", steps: 3,
			opens: []string{"/usage?w=30d", "/usage/allowance?w=30d"}},
		{path: "/usage/tools?w=30d", steps: 3,
			opens: []string{"/usage/work?w=30d", "/usage/tools?w=30d"}},
		// A leaf: four steps, and the list it hangs off is one click up.
		{path: "/usage/tools?w=30d&tool=Bash", steps: 4,
			opens: []string{"/usage/work?w=30d", "/usage/tools?w=30d"}},

		// Outcomes: five views at one level, and per-item pages under them. The
		// item's trail names the VIEW whose list it was on, so the reader can
		// move sideways without going home first.
		{path: "/outcomes/tag/x?w=all", steps: 3,
			opens: []string{"/outcomes?w=all", "/outcomes/cohorts?w=all", "/outcomes/events?w=all"}},
		{path: "/outcomes/dismissals/already-knew?w=all", steps: 3,
			opens: []string{"/outcomes/signal-trust?w=all"}},

		// Registry: Learning readiness has a page under it, and the workflows
		// page used to render a trail identical to the readiness page's.
		{path: "/learning", steps: 3,
			opens: []string{"/learning", "/learning/workflows", "/settings"}},
		{path: "/learning/workflows", steps: 3,
			opens: []string{"/learning", "/learning/workflows"}},
	} {
		_, body := fetchHTML(t, ts.URL+tc.path)
		n := navOf(t, body)
		if n.steps != tc.steps {
			t.Errorf("%s: trail has %d steps, want %d", tc.path, n.steps, tc.steps)
		}
		all := strings.Join(n.menus, " ")
		for _, href := range tc.opens {
			if !strings.Contains(all, `href="`+href+`"`) {
				t.Errorf("%s: no caret on the trail opens %s", tc.path, href)
			}
		}
		for _, href := range tc.shut {
			if strings.Contains(all, `href="`+href) {
				t.Errorf("%s: the trail offers %s, which does not live under it", tc.path, href)
			}
		}
	}
}

// A page that is a page of its own keeps its link when it is an ancestor: "up
// one level" is the commonest move on a trail, and folding the label into a
// <summary> would cost it a click. A step that is only a CATEGORY — a level
// with a name and no page of its own, like the destination above Overview —
// has no link, and its caret is all it is for.
func TestAnAncestorKeepsItsLinkAndGainsACaret(t *testing.T) {
	_, ts := newServer(t)
	// A leaf's parent is a real page: label links, caret beside it.
	_, body := fetchHTML(t, ts.URL+"/usage/tools?w=30d&tool=Bash")
	n := navOf(t, body)
	if !strings.Contains(n.trail, `<span class="crumb-step"><a href="/usage/tools?w=30d">Tools</a>`) {
		t.Error("the list a leaf hangs off is not one click up")
	}
	if !strings.Contains(n.trail, `class="crumb-menu caret-only"`) {
		t.Error("that step has no caret, so its level is invisible again")
	}
	// The destination above it is a category: its own page is Overview one step
	// along, so the name carries no href and makes no claim to be the page.
	if strings.Contains(n.trail, `<a href="/usage/work?w=30d">Work</a>`) {
		t.Error("the category step links to a page it is not")
	}
	if !strings.Contains(n.trail, `<summary>Work`) {
		t.Error("the category step is not a dropdown")
	}
	// Exactly one step claims to be the page being read.
	if c := strings.Count(n.trail, `aria-current="page">`); c != 1 {
		t.Errorf("%d steps claim to be the current page, want 1", c)
	}
}

// A per-item family is a list's worth of pages, not a menu's: a caret holding
// every tag would be a table in a popover. So the item's step gets no caret,
// and the list stays one step up.
func TestAPerItemFamilyStaysInItsList(t *testing.T) {
	_, ts := newServer(t)
	for _, path := range []string{"/outcomes/tag/x?w=all", "/usage/tools?w=30d&tool=Bash"} {
		_, body := fetchHTML(t, ts.URL+path)
		n := navOf(t, body)
		// The last step is plain text: no caret, nothing to open.
		tail := n.trail[strings.LastIndex(n.trail, "crumb-sep"):]
		if strings.Contains(tail, "crumb-menu") {
			t.Errorf("%s: the item step opens a menu of its siblings", path)
		}
	}
}
