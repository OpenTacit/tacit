// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package scrub

import (
	"strings"
	"testing"
)

func kinds(fs []Finding) map[string]int {
	out := map[string]int{}
	for _, f := range fs {
		out[f.Kind]++
	}
	return out
}

func TestDetectsCredentialFamilies(t *testing.T) {
	text := `deploy with AWS_KEY=AKIAIOSFODNN7EXAMPLE and header Authorization: Bearer abcdef0123456789TOKEN
github: ghp_AbCdEfGhIjKlMnOpQrStUvWxYz012345
anthropic: sk-ant-api03-xxxxxxxxxxxx
curl https://user:hunter2secret@internal.example.com/path
api_key = "9f8e7d6c5b4a39281706fivefour"
mail me at jane.doe@example.com`
	got := kinds(Scan(text))
	for _, want := range []string{"aws-access-key", "bearer-token", "github-token",
		"anthropic-key", "basic-auth-url", "secret-assignment", "email"} {
		if got[want] == 0 {
			t.Fatalf("missed %s in %v", want, got)
		}
	}
}

func TestPrivateKeyBlock(t *testing.T) {
	text := "config:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow…\n-----END RSA PRIVATE KEY-----\ndone"
	clean, fs := Redact(text)
	if kinds(fs)["private-key"] == 0 {
		t.Fatal("private key block missed")
	}
	if strings.Contains(clean, "MIIEow") {
		t.Fatalf("key material survived: %s", clean)
	}
	if !strings.Contains(clean, "[redacted:private-key]") {
		t.Fatalf("marker missing: %s", clean)
	}
}

func TestHighEntropyStrings(t *testing.T) {
	secret := "q7Rt9xK2mWpLc4vNbY8sD3fJhG6aZe5uT1oQiXnM0rkP"
	if kinds(Scan("token is " + secret))["high-entropy-string"] == 0 {
		t.Fatal("high-entropy secret missed")
	}
	// ordinary long identifiers and repeated padding must NOT flag
	for _, benign := range []string{
		"internal_registry_storage_conformance_suite_test",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"TestSlowSynthesisParksAndDeliversNextPrompt",
	} {
		if got := kinds(Scan(benign)); got["high-entropy-string"] > 0 {
			t.Fatalf("false positive on %q", benign)
		}
	}
}

func TestCleanProseUntouched(t *testing.T) {
	text := "Use the @warehouse connector: query revenue.pipeline where quarter = 'q3', then summarize."
	clean, fs := Redact(text)
	if len(fs) != 0 || clean != text {
		t.Fatalf("clean prose modified: %v %s", fs, clean)
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	text := "key AKIAIOSFODNN7EXAMPLE done"
	once, _ := Redact(text)
	twice, fs := Redact(once)
	if once != twice || len(fs) != 0 {
		t.Fatalf("not idempotent: %q vs %q (%v)", once, twice, fs)
	}
}
