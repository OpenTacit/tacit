// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"regexp"
	"strings"
	"testing"
)

// A LABEL IS TYPE, NOT GEOMETRY.
//
// It used to be a fraction of the scene's own unit, which meant a picture that
// shrank to fit the space it was given took the only words it has down with it:
// measured on the pages, the Learning caption came out at 7.7px on a 1440 window
// and 5.1px at 390. Every diagram kept the document from scrolling sideways, and
// did it by making its text unreadable.
//
// So the size is stated in px and the drawing is not. A narrow window moves the
// parts — every scene carries a reflow — rather than shrinking the caption.
func TestDiagramLabelsAreStatedInReadingSizes(t *testing.T) {
	tag := setRule(t, ".dgm-tag")
	if regexp.MustCompile(`font-size:calc\(`).MatchString(tag) {
		t.Error("the label's size is computed from the scene's unit again; it shrinks with the drawing")
	}
	if !strings.Contains(tag, "font:13px/18px var(--font-ui)") {
		t.Errorf("the label is not 13px Mona Sans: %s", tag)
	}
	if second := setRule(t, ".dgm-tag i"); !strings.Contains(second, "font-size:12px") {
		t.Errorf("the label's second line is not 12px: %s", second)
	}
	if strings.Contains(appCSS, "--tag-fs") {
		t.Error("--tag-fs survives; two ways to size a label is one too many")
	}

	// Plex Mono is machine text. A caption is the interface talking, so the
	// secondary line reads in the interface face and only an actual address,
	// command or id inside one asks for the other.
	if second := setRule(t, ".dgm-tag i"); strings.Contains(second, "var(--font-mono)") {
		t.Error("the label's second line is set in the machine face; it is ordinary prose")
	}
	if code := setRule(t, ".dgm-tag code"); !strings.Contains(code, "var(--font-mono)") {
		t.Errorf("a machine address inside a label has no way to say so: %s", code)
	}
	if !strings.Contains(setupScene("host:8080", "a file store"), "<code>host:8080</code>") {
		t.Error("the setup picture's address is not marked as machine text")
	}
}

// AND IT HANGS FROM AN EDGE WHERE IT HAS TO. A label centred on a point near the
// frame puts half of itself outside, and the frame clips — at reading size that
// took the end off a capability's state and the whole of the exit caption. The
// two knobs are --ax and --ay, so a scene states the corner with the rest of its
// coordinates and the primitive stays one rule.
func TestDiagramLabelsCanHangFromAnEdge(t *testing.T) {
	tag := setRule(t, ".dgm-tag")
	for _, want := range []string{"--ax:-50%", "--ay:-50%", "transform:translate(var(--ax),var(--ay))"} {
		if !strings.Contains(tag, want) {
			t.Errorf("the label cannot be hung from an edge (%s missing): %s", want, tag)
		}
	}
	// Width is still stated in the scene's unit: a label wraps against the
	// drawing it belongs to, not against a guess in px.
	if !strings.Contains(tag, "max-width:calc(var(--tag-w,7)*var(--u))") {
		t.Errorf("the label's width is no longer measured in the scene's own unit: %s", tag)
	}
	if !strings.Contains(setRule(t, ".dgm-scene"), "--u:min(") {
		t.Error("the scene no longer names its own unit, so nothing outside it can ask for a width in it")
	}
}
