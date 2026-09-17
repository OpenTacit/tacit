// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"
)

// The wizard runs before anything else exists, so the picture is drawn from the
// values the form is ABOUT to write — this registry, not the idea of one.
func TestSetupSceneShowsTheRegistryTheFormWillWrite(t *testing.T) {
	s := setupScene("127.0.0.1:8080", "a file store on this machine")
	if !strings.Contains(s, "127.0.0.1:8080") {
		t.Error("the picture does not carry the address the form will write")
	}
	if !strings.Contains(s, "a file store on this machine") {
		t.Error("the picture does not carry the store the form will write")
	}
	// Three things and no more: the wizard has enough on its hands.
	if n := strings.Count(s, "dgm-tag"); n != 3 {
		t.Errorf("%d labels; the picture says who reaches it, what it is, and where its data goes", n)
	}
	if strings.Contains(s, "<") && strings.Contains(setupScene("<script>", "x"), "<script>") {
		t.Error("the address is not escaped; it comes from a form field")
	}
}
