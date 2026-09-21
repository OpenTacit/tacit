// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package onnxassets

import (
	"strings"
	"testing"
)

// The ONNX Runtime binary statically links around thirty third-party
// components, and their licences require the notices to accompany it. Before
// this existed, onnx-fetch pulled only libonnxruntime.so and discarded both
// the runtime's own LICENSE and its 6,000-line ThirdPartyNotices.txt — so
// every image we published redistributed all of that code with none of the
// required notices.
//
// This test is cheap insurance against the same thing happening again: a
// future tidy-up that trims the artifact list would make the build a licence
// violation while every test still passed and the registry still worked.
func TestRuntimeNoticesAreFetchedWithTheLibrary(t *testing.T) {
	if len(RuntimeNotices) == 0 {
		t.Fatal("no runtime notices declared; the image would ship libonnxruntime.so without its licences")
	}
	var gotLicence, gotThirdParty bool
	for _, n := range RuntimeNotices {
		if n.SHA256 == "" {
			t.Errorf("%s has no pinned digest; every fetched artifact is verified", n.Out)
		}
		if n.Out == "" {
			t.Errorf("%s has no output name", n.URL)
		}
		// The Dockerfile copies these by name. A rename here without one there
		// fails the image build, which is the right failure — but say so.
		if !strings.HasPrefix(n.Out, "ONNXRUNTIME-") {
			t.Errorf("%q does not start with ONNXRUNTIME-; the Dockerfile copies it by that name", n.Out)
		}
		if !strings.Contains(n.URL, Version) {
			t.Errorf("%s is not pinned to the runtime version %s", n.URL, Version)
		}
		switch {
		case strings.Contains(n.Out, "LICENSE"):
			gotLicence = true
		case strings.Contains(n.Out, "ThirdPartyNotices"):
			gotThirdParty = true
		}
	}
	if !gotLicence {
		t.Error("ONNX Runtime's own MIT licence is not fetched")
	}
	if !gotThirdParty {
		t.Error("the third-party notices for what the binary links are not fetched")
	}
}

// The notices belong to a pinned version. If Version moves and the digests do
// not, Fetch will refuse the download rather than ship notices describing a
// different build — but the failure is clearer stated here.
func TestRuntimeNoticesTrackThePinnedVersion(t *testing.T) {
	for _, n := range RuntimeNotices {
		if !strings.Contains(n.URL, "/v"+Version+"/") {
			t.Errorf("%s does not reference the pinned tag v%s; bump the digests when bumping Version", n.URL, Version)
		}
	}
}
