// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package llmprovider

import "testing"

func TestLookupValidityAndTable(t *testing.T) {
	if !Valid("openrouter") || !Valid("groq") || !Valid("anthropic") {
		t.Fatal("known providers must be valid")
	}
	if Valid("nope") || Valid("") {
		t.Fatal("unknown/empty must be invalid")
	}
	if Lookup("groq").Wire != WireOpenAI {
		t.Fatalf("groq wire = %q", Lookup("groq").Wire)
	}
	if Lookup("anthropic").Wire != WireAnthropic {
		t.Fatalf("anthropic wire = %q", Lookup("anthropic").Wire)
	}
	if Lookup("").Key != Default || Lookup("unknown").Key != Default {
		t.Fatal("empty/unknown must fall back to the default provider")
	}

	// Every entry is complete: a missing base or default model would break
	// zero-config operation for that provider.
	for _, p := range Known {
		if p.Key == "" || p.Label == "" || p.BaseURL == "" || p.AuditModel == "" || p.ResearchModel == "" {
			t.Fatalf("incomplete provider: %+v", p)
		}
		if p.Wire != WireOpenAI && p.Wire != WireAnthropic {
			t.Fatalf("provider %q has unknown wire %q", p.Key, p.Wire)
		}
	}
	// A recognizable set is offered.
	for _, key := range []string{"anthropic", "openai", "openrouter", "google", "groq", "mistral", "deepseek"} {
		if !Valid(key) {
			t.Fatalf("expected %q in the provider list", key)
		}
	}
}

// The base URL in force: an override when the operator set one, and otherwise
// the SELECTED provider's own default — never the previous provider's. This is
// the one answer the settings page shows and every caller uses, so the address
// in the field is the address that gets called.
func TestBaseURLForResolvesTheAddressInForce(t *testing.T) {
	anthropic := Lookup("anthropic").BaseURL
	openai := Lookup("openai").BaseURL

	// Nothing configured: the provider's own default, per provider.
	if got := BaseURLFor("anthropic", ""); got != anthropic {
		t.Errorf("BaseURLFor(anthropic, \"\") = %q, want %q", got, anthropic)
	}
	if got := BaseURLFor("openai", "   "); got != openai {
		t.Errorf("blank-but-spaces must resolve to the provider default, got %q", got)
	}
	// An unknown or empty provider still resolves, via the default provider.
	if got := BaseURLFor("", ""); got != anthropic {
		t.Errorf("BaseURLFor(\"\", \"\") = %q, want the default provider's %q", got, anthropic)
	}
	// An override is the operator's own gateway and always wins: this field is
	// what it is for.
	if got := BaseURLFor("anthropic", "https://gateway.internal/v1"); got != "https://gateway.internal/v1" {
		t.Errorf("a configured base must win, got %q", got)
	}
}
