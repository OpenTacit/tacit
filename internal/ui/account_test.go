// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// Identity is one circle in the top bar, not a line of text — the same control
// on the registry dashboard and the ingress console, from one implementation.
func TestAccountRendersAnAvatarNotAnAddress(t *testing.T) {
	user := map[string]any{
		"name": "Ada Lovelace", "email": "ada@example.com",
		"picture": "https://lh3.googleusercontent.com/a/abc123",
	}
	got := Account(user, true, "")

	for _, want := range []string{
		`id="account-btn"`, `class="avatar-btn"`,
		`<img class="avatar-img" src="https://lh3.googleusercontent.com/a/abc123"`,
		`referrerpolicy="no-referrer"`,    // don't leak the console's URL to the provider
		`<span class="avatar-initials"`,   // fallback underneath the picture
		`Ada Lovelace`, `ada@example.com`, // identity lives in the menu
		`href="/auth/logout">Sign out<`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("account markup missing %q:\n%s", want, got)
		}
	}
	// The address must not appear outside the menu — that was the bug this
	// replaced: a full email address widening the bar and wrapping on mobile.
	bar, _, _ := strings.Cut(got, `<div id="account-menu"`)
	if strings.Contains(bar, "@") {
		t.Errorf("the top bar still spells out an address: %s", bar)
	}
}

// A picture claim lands in an <img src>, so it is a place an identity provider
// could hand us something that is not a URL.
func TestAvatarRejectsNonHTTPPictures(t *testing.T) {
	for _, pic := range []string{"javascript:alert(1)", "data:image/svg+xml,<svg/>", "", "/relative"} {
		got := Account(map[string]any{"name": "Ada Lovelace", "picture": pic}, true, "")
		if strings.Contains(got, "<img") {
			t.Errorf("picture %q was rendered as an image", pic)
		}
		if !strings.Contains(got, ">AL<") {
			t.Errorf("picture %q: no initials fallback", pic)
		}
	}
}

func TestAccountSignedOutAndProviderless(t *testing.T) {
	if got := Account(nil, true, ""); got != `<a class="signin" href="/auth/login">Sign in</a>` {
		t.Errorf("signed out = %q, want a Sign in link", got)
	}
	// No identity provider: still a menu, but nothing to sign out of.
	open := Account(nil, false, `<a class="account-item" href="/settings">Settings</a>`)
	if !strings.Contains(open, `href="/settings"`) || strings.Contains(open, "Sign out") {
		t.Errorf("providerless menu wrong: %s", open)
	}
}

func TestInitials(t *testing.T) {
	for name, want := range map[string]string{
		"Ada Lovelace":       "AL",
		"ada.lv@example.com": "AL",
		"ada@example.com":    "A",
		"u_9931":             "U9",
		"":                   "?",
	} {
		if got := Initials(name); got != want {
			t.Errorf("Initials(%q) = %q, want %q", name, got, want)
		}
	}
}
