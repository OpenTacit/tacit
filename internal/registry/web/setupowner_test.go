// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"
)

// A registry claimed through the first-run form and a registry set up by
// `tacit init` used to finish in two different states. init gated the dashboard
// behind an owner link; the form left sign-in off entirely unless all four OIDC
// fields were filled, so the container path produced a registry anybody who
// could reach the port could administer — and nothing on the page said so.
func TestClaimingARegistryGatesItTheWayInitDoes(t *testing.T) {
	vals := []setupField{{"TACIT_API_KEY", "k"}, {"TACIT_PORT", "8080"}}
	got := withOwnerWhenNoIdP(vals)
	if !setupNames(got, "TACIT_OWNER_SECRET") {
		t.Fatal("a claimed registry with no identity provider got no owner sign-in")
	}
	if !setupNames(got, "TACIT_AUTH_MODE") {
		t.Fatal("auth mode not set")
	}
	// And the confirmation page has to say how to get in, or the operator meets
	// a sign-in page holding no credential for it.
	byKey := map[string]string{}
	for _, f := range got {
		byKey[f.Key] = f.Value
	}
	page := setupSuccess("/tmp/registry.env", got, nil)
	if !strings.Contains(page, "tacit dashboard") {
		t.Error("the confirmation page does not say how to sign in")
	}
	_ = byKey
}

// An operator who configured their identity provider here does not get a second
// way in behind their back.
func TestClaimingWithAnIdentityProviderAddsNoSecondGate(t *testing.T) {
	vals := []setupField{
		{"TACIT_API_KEY", "k"},
		{"TACIT_OIDC_ISSUER", "https://accounts.google.com"},
		{"TACIT_OIDC_CLIENT_ID", "cid"},
	}
	if setupNames(withOwnerWhenNoIdP(vals), "TACIT_OWNER_SECRET") {
		t.Fatal("an OIDC registry was given an owner secret as well")
	}
}
