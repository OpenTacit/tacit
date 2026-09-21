// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// The busy state rides in the shell, so a slow form declares one attribute and
// writes no script of its own.
func TestBusyFormRidesInTheShell(t *testing.T) {
	page := Shell{Brand: "Test"}.Render(Page{Content: "<p>hi</p>"})
	if !strings.Contains(page, "form[data-busy]") {
		t.Error("a page built by this shell cannot put a slow form into a working state")
	}
}

// What the script promises the markup, and the one ordering bug that would make
// it silently break the form it is decorating.
func TestBusyFormContract(t *testing.T) {
	for _, hook := range []string{
		"form[data-busy]", // the form opts in
		"data-busy-label", // the button's working label
		"suggest-bar",     // reuses the progress bar that already exists
		"indeterminate",   // no invented deadline
		`role`,            // announced to a screen reader
	} {
		if !strings.Contains(BusyFormJS, hook) {
			t.Errorf("the busy-form script no longer honours %s", hook)
		}
	}
	// A disabled submitter is not sent with the form, so disabling has to happen
	// after the submit event is done with the button — otherwise a form whose
	// action depends on which button was pressed silently loses that.
	if !strings.Contains(BusyFormJS, "setTimeout(function(){b.disabled=true;},0)") {
		t.Error("the button is disabled during submit, which drops its name and value")
	}
	// Re-submitting must not stack a second bar on the form.
	if !strings.Contains(BusyFormJS, "already running") {
		t.Error("a second submit adds a second progress bar")
	}
}
