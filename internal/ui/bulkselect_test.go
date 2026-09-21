// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// One selection behaviour, carried by the shell, for every page that has one.
// The registry's review lane and the ingress console's fleet board both mark up
// a bulk form and write no script of their own; a second copy is how the two
// drift apart on the first edit.
func TestBulkSelectRidesInTheShell(t *testing.T) {
	page := Shell{Brand: "Test"}.Render(Page{Content: "<p>hi</p>"})
	if !strings.Contains(page, "form.bulk-actions[id]") {
		t.Error("a page built by this shell carries no bulk-selection behaviour, " +
			"so every board that wants one has to ship its own")
	}
}

// What the script promises the markup. Each of these is a contract a page
// depends on, and a rename here is silent everywhere else.
func TestBulkSelectContract(t *testing.T) {
	for _, hook := range []string{
		"form.bulk-actions[id]", // the form a page declares
		"[data-select-all]",     // the header box
		"data-count-for",        // where the live count is written
		"data-confirm",          // a destructive button asks first
		"indeterminate",         // half a selection says so
		"data-all-for",          // widening past the page, on a table that pages
		"data-all-count",        // and how many that would be
	} {
		if !strings.Contains(BulkSelectJS, hook) {
			t.Errorf("the bulk-selection script no longer honours %s", hook)
		}
	}
	// The count goes in the question, so an operator confirming a release sees
	// how many names they are freeing.
	if !strings.Contains(BulkSelectJS, "{n}") || !strings.Contains(BulkSelectJS, "{s}") {
		t.Error("a confirmation cannot name the size of the selection")
	}
	// Nothing submits an empty selection. A bulk action with no rows behind it
	// is a request the server has to defend itself against for no reason.
	if !strings.Contains(BulkSelectJS, "if(!n){e.preventDefault();return;}") {
		t.Error("an empty selection can still be submitted")
	}
	// The widening goes away with the full page it was offered under. Left
	// standing beside three ticked rows it is an invitation to act on a whole
	// fleet by accident.
	if !strings.Contains(BulkSelectJS, "if(!full&&scopeBox)scopeBox.checked=false;") {
		t.Error("a widened selection survives the page being un-ticked")
	}
}
