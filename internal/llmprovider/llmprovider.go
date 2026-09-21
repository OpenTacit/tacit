// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package llmprovider is the single source of truth for the model providers
// OpenTacit can target: the friendly label, the wire format (Anthropic Messages vs
// OpenAI /chat/completions), the default base URL, and starting default models.
// One table so the auditor's model client, the registry researcher, config
// defaults, and the settings dropdown all agree on what "groq" or "openrouter"
// means.
//
// Almost every provider speaks the OpenAI /chat/completions wire format, so the
// list is mostly base-URL entries against that one adapter; only Anthropic uses
// the Messages wire. The default model ids are a convenience for zero-config
// operation and WILL drift — they are overridable per setting, and the settings
// combo box shows each provider's live catalog to pick from.
package llmprovider

import "strings"

// Wire formats. These are the only two request/response shapes OpenTacit speaks.
const (
	WireAnthropic = "anthropic"
	WireOpenAI    = "openai"
)

// Provider describes one selectable model backend.
type Provider struct {
	Key     string // stored in TACIT_LLM_PROVIDER
	Label   string // shown in the settings dropdown
	Wire    string // WireAnthropic | WireOpenAI
	BaseURL string // default endpoint root (TACIT_LLM_BASE_URL overrides)
	// AuditModel is the cheap default for the per-turn audit layer (char/synth);
	// ResearchModel is the more capable default for suggestion research + tag-merge.
	AuditModel    string
	ResearchModel string
}

// Known lists the providers offered in the settings dropdown, in display order.
// Anthropic stays first (the default). The rest are OpenAI-compatible bases.
var Known = []Provider{
	{Key: "anthropic", Label: "Anthropic (Claude)", Wire: WireAnthropic, BaseURL: "https://api.anthropic.com",
		AuditModel: "claude-haiku-4-5", ResearchModel: "claude-sonnet-5"},
	{Key: "openai", Label: "OpenAI", Wire: WireOpenAI, BaseURL: "https://api.openai.com/v1",
		AuditModel: "gpt-4o-mini", ResearchModel: "gpt-4o"},
	{Key: "openrouter", Label: "OpenRouter", Wire: WireOpenAI, BaseURL: "https://openrouter.ai/api/v1",
		AuditModel: "openai/gpt-4o-mini", ResearchModel: "openai/gpt-4o"},
	{Key: "google", Label: "Google Gemini", Wire: WireOpenAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		AuditModel: "gemini-2.0-flash", ResearchModel: "gemini-2.5-pro"},
	{Key: "groq", Label: "Groq", Wire: WireOpenAI, BaseURL: "https://api.groq.com/openai/v1",
		AuditModel: "llama-3.3-70b-versatile", ResearchModel: "llama-3.3-70b-versatile"},
	{Key: "mistral", Label: "Mistral", Wire: WireOpenAI, BaseURL: "https://api.mistral.ai/v1",
		AuditModel: "mistral-small-latest", ResearchModel: "mistral-large-latest"},
	{Key: "deepseek", Label: "DeepSeek", Wire: WireOpenAI, BaseURL: "https://api.deepseek.com",
		AuditModel: "deepseek-chat", ResearchModel: "deepseek-chat"},
	{Key: "together", Label: "Together", Wire: WireOpenAI, BaseURL: "https://api.together.xyz/v1",
		AuditModel: "meta-llama/Llama-3.3-70B-Instruct-Turbo", ResearchModel: "meta-llama/Llama-3.3-70B-Instruct-Turbo"},
	{Key: "xai", Label: "xAI (Grok)", Wire: WireOpenAI, BaseURL: "https://api.x.ai/v1",
		AuditModel: "grok-2-latest", ResearchModel: "grok-2-latest"},
	{Key: "fireworks", Label: "Fireworks", Wire: WireOpenAI, BaseURL: "https://api.fireworks.ai/inference/v1",
		AuditModel: "accounts/fireworks/models/llama-v3p3-70b-instruct", ResearchModel: "accounts/fireworks/models/llama-v3p3-70b-instruct"},
}

var byKey = func() map[string]Provider {
	m := make(map[string]Provider, len(Known))
	for _, p := range Known {
		m[p.Key] = p
	}
	return m
}()

// Default is the provider used for an empty or unknown key — Anthropic, the
// historical default.
const Default = "anthropic"

// Lookup returns the provider for a key, falling back to the default for an
// empty or unknown key.
func Lookup(key string) Provider {
	if p, ok := byKey[strings.TrimSpace(key)]; ok {
		return p
	}
	return byKey[Default]
}

// Valid reports whether key names a known provider.
func Valid(key string) bool {
	_, ok := byKey[strings.TrimSpace(key)]
	return ok
}

// BaseURLFor is the endpoint root IN FORCE for a provider: the operator's
// configured override when there is one, and otherwise this provider's own
// default from the table above.
//
// It exists because that sentence was being written out longhand in every place
// that needed it — the researcher, the auditor's configuration — while the
// settings page, which is where an operator READS the answer, did not resolve it
// at all: it put the provider's default in the field's placeholder and left the
// real resolution to whoever made the call. A default that is only a placeholder
// is a promise the page cannot keep, and the page and the caller drifting apart
// is how a registry ends up sending a provider's traffic to another provider's
// address. One resolver, used by the page that shows the value and by every
// caller that uses it.
func BaseURLFor(provider, configured string) string {
	if v := strings.TrimSpace(configured); v != "" {
		return v
	}
	return Lookup(provider).BaseURL
}
