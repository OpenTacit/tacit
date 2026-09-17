// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"os"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/ui"
)

// Only one overlay menu may be open at a time.
//
// The breadcrumb carries two dropdowns since the trail started showing what
// sits under a page, so a second popover opening over the first became reachable
// in one move — and two popovers on one line are unreadable. The top bar's menus
// already closed each other through a registry private to the script that built
// them; the behaviour is now one closer every overlay in the chrome shares.
//
// The browser is where this is actually verified — a script that closes the
// wrong thing looks identical in a diff. What this file holds is the part a diff
// CAN protect: that the one closer exists, that every shape of menu is in its
// set, and that the exemptions which took a live browser to find are still
// there.
func TestOneMenuAtATime(t *testing.T) {
	js := ui.MenuScript
	if !strings.Contains(js, "window.tacitCloseMenus=close;") {
		t.Fatal("there is no shared closer, so the next menu will need a mechanism of its own")
	}

	// All three shapes the chrome comes in. A menu missing from this set is a
	// menu that can sit open under another one.
	for _, want := range []struct{ sel, shape string }{
		{"details.crumb-menu", "the breadcrumb levels"},
		{"details.fmenu", "the filter menus"},
		{".account-menu", "the account and demo panels"},
		{"nav-toggle", "the phone's nav"},
	} {
		if !strings.Contains(js, want.sel) {
			t.Errorf("%s (%s) is not in the set", want.sel, want.shape)
		}
	}

	// `toggle` does not bubble and a control may stop propagation on its own
	// button, so both listeners have to be on the capture phase. Without the
	// third argument the closer silently never fires.
	if strings.Count(js, "},true);") < 2 {
		t.Error("a listener is not on the capture phase, so it will not see the event")
	}

	// The exemptions. Each of these cost a live browser to find, and each is a
	// gesture that closes the very menu it was opening.
	for _, want := range []struct{ sel, why string }{
		{"t.closest(POPS)", "a click inside a menu, or on the summary that opens it"},
		{"t.closest('[aria-controls]')", "the button that opens a panel"},
		{"t.closest('.nav-burger')", "the phone's burger"},
		// The one that is easy to miss: activating a <label for> makes the
		// browser forward a SECOND click to the control itself, so exempting
		// only the label left that click looking like a click on the page — and
		// opening the nav closed it again in the same gesture.
		{"t.closest('.nav-toggle')", "the checkbox the burger forwards its click to"},
	} {
		if !strings.Contains(js, want.sel) {
			t.Errorf("%s is not exempt from the outside-click close: %s", want.sel, want.why)
		}
	}

	// A click anywhere else, and Escape, dismiss whatever is open — a menu left
	// standing over the page is the other half of the same complaint.
	if !strings.Contains(js, "if(e.key==='Escape')close(null);") {
		t.Error("Escape does not dismiss an open menu")
	}

	// One closer, not two: the bar menus go through it rather than keeping a
	// list of their own.
	shell := registryShellSource(t)
	if strings.Contains(shell, "barMenus") {
		t.Error("the top bar still keeps its own registry of menus")
	}
	if !strings.Contains(shell, "if(open&&window.tacitCloseMenus)window.tacitCloseMenus(menu);") {
		t.Error("a bar menu opens without closing the rest of the chrome")
	}
	if !strings.Contains(shell, "ui.MenuScript") {
		t.Error("the shell does not ship the closer")
	}

	// An inline disclosure is NOT in the set. It expands under the page rather
	// than over it, so it overlaps nothing — and closing somebody's expanded
	// fine print because they opened a nav menu would lose them their place.
	if strings.Contains(js, "fineprint") {
		t.Error("an inline disclosure is treated as an overlay")
	}
}

// registryShellSource reads this package's own shell.go, for the assertions
// that are about the script it ships rather than about the shared one.
func registryShellSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("shell.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
