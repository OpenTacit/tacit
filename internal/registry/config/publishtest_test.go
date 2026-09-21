// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

// DefaultIngress names the proxy the project actually runs, so a test that
// enables publishing and forgets to point the address somewhere harmless
// enrols against production. It gets one hostname, never comes back, and the
// operator is left with a name nobody can account for — which is how a fleet
// fills up with instances no one can explain.
//
// So a registry built by `go test` declares itself a test by default. Nobody
// has to remember, which is the point: the ones that caused the problem were
// written by people who did not know there was anything to remember.
func TestARegistryBuiltByGoTestDeclaresItselfATest(t *testing.T) {
	t.Setenv("TACIT_PUBLISH_TEST", "")
	if got := Load(); !got.PublishTest {
		t.Error("a registry loaded inside a test does not declare itself one, " +
			"so a forgotten ingress address enrols against production silently")
	}

	// The override is the half that keeps this honest. Something has to be able
	// to prove what a NON-test registry declares, and that cannot be done if
	// the answer is forced.
	t.Setenv("TACIT_PUBLISH_TEST", "0")
	if got := Load(); got.PublishTest {
		t.Error("TACIT_PUBLISH_TEST=0 did not turn the declaration off")
	}

	t.Setenv("TACIT_PUBLISH_TEST", "1")
	if got := Load(); !got.PublishTest {
		t.Error("TACIT_PUBLISH_TEST=1 did not turn the declaration on")
	}
}

// The default this guards is the dangerous one, so it is worth stating: the
// address a registry publishes through, unconfigured, is the real proxy.
func TestTheDefaultIngressIsTheRealProxy(t *testing.T) {
	if DefaultIngress == "localhost" || DefaultIngress == "" {
		t.Skip("the default no longer names a shared proxy; the test-flag default is then belt and braces")
	}
	t.Setenv("TACIT_PUBLISH_INGRESS", "")
	cfg := Load()
	if cfg.PublishIngress != DefaultIngress {
		t.Fatalf("PublishIngress = %q, want the default %q", cfg.PublishIngress, DefaultIngress)
	}
	if !cfg.PublishTest {
		t.Errorf("an unconfigured registry under test would reach %s without declaring itself a test",
			DefaultIngress)
	}
}
