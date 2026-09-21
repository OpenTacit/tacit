// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package config holds the audit layer's tunables. The LLM is the one
// external dependency here (by design — it lives outside the registry); with
// no API key the layer falls back to the offline heuristic.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/opentacit/tacit/internal/llmprovider"

	"github.com/opentacit/tacit/internal/envcfg"
)

// Config carries the auditor settings, loaded from the environment, falling
// back to the member's agent.env file. Model selection is a single set of
// TACIT_LLM_* keys, whatever the provider.
type Config struct {
	// LLMProvider selects the model backend: "anthropic" (default) or "openai"
	// (which covers OpenRouter and any OpenAI-compatible /chat/completions base).
	LLMProvider      string
	LLMBaseURL       string // resolved per provider in Load
	AnthropicVersion string // anthropic-version header; used only when provider is anthropic
	LLMKeyFile       string // KEY=value env file (TACIT_LLM_API_KEY) read live by the hook agent
	CharModel        string
	SynthModel       string
	MaxTokens        int

	RegistryURL string
	RegistryKey string

	OmnigentBase  string
	OmnigentToken string
	OmnigentEmail string

	HooksHost           string
	HooksPort           int
	HooksAPIKey         string
	HooksSegment        string // e.g. "team=revops,role=analyst"
	TechniqueMemoryPath string // member-local adopted/dismissed memory (hooks/techniquememory.go)
	UsageLogPath        string // member-local activity history (hooks/usagelog.go)
	// StateDir is where member-local state that is not one of the named paths
	// above lives: the session log, its daily rollups, the correction ledger
	// (hooks/sessionlog.go, hooks/corrections.go), and the merge archives.
	//
	// It exists because the alternative kept happening. Each new piece of
	// member-local state picked a name in $HOME — the session log derived its
	// own path from the usage log's DIRECTORY, which is $HOME, so it landed
	// there as sessions.jsonl with no dot and no warning. A home directory
	// ending up with a hundred files nobody chose to put there is not one bad
	// decision; it is the absence of a place to put things. This is the place.
	StateDir string
	// The unsolicited-suggestion budget (hooks/throttle.go). A suggestion needs
	// both cooldowns cleared AND room in the rolling window; it is the member's
	// budget, not a session's, and it is rebuilt from the usage log at startup.
	HooksMaxPerWindow       int
	HooksWindowHours        float64
	HooksSuggestionCooldown int // turns
	HooksCooldownMinutes    float64
	HooksMaxFitChecks       int // cap LLM fit-checks fanned out per suggestion
	HooksSynthBudgetSecs    float64
	HooksAskBudgetSecs      float64 // @tacit mention answer budget (mention.go)
	HooksMentionBlock       bool    // pure @tacit prompts may answer via decision:block (validated harnesses)
	HooksIdleExitSecs       int
	HooksSpawnLog           string

	// Sketch consent (docs/mining/mining-design.md source 2): setting SketchURL IS
	// the opt-in — no URL, no sketches, and the transcript never leaves the
	// machine either way. Salt must be org-stable so occurrence counting
	// works across members without identity.
	SketchURL   string
	SketchToken string
	SketchSalt  string
	SessionSalt string // optional org salt for audit session hashes; no default

	// PausePath is the marker whose EXISTENCE means "watch nothing". Read live
	// on every hook event rather than at startup, so pausing takes hold on the
	// next turn instead of the next restart — the same reason the model key is
	// resolved live.
	PausePath string
}

// DefaultPausePath is the pause marker, beside the other member-local files.
func DefaultPausePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tacit-paused")
}

// Paused reports whether this machine is paused. A missing home, an unreadable
// marker or any other doubt answers NO: failing closed here would silence the
// product for a reason nobody could see, which is the failure mode the whole
// plan is about.
func Paused(pausePath string) bool {
	if pausePath == "" {
		return false
	}
	_, err := os.Stat(pausePath)
	return err == nil
}

// defaultTechniqueMemoryPath is the member-local technique memory (hooks/techniquememory.go).
func defaultTechniqueMemoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "" // no home -> in-memory only; memory is a nicety, not a need
	}
	return filepath.Join(home, ".tacit-technique-memory.json")
}

// defaultUsageLogPath is the member-local usage history (hooks/usagelog.go).
//
// It stays at the top of $HOME, dotted, where it has always been. Moving it
// would be tidier and would orphan every member's history for the sake of a
// file `ls` does not show — the state directory below exists so nothing NEW
// has to make this choice, not to relitigate the ones already made.
func defaultUsageLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "" // no home -> in-memory only; the Usage view just shows nothing
	}
	return filepath.Join(home, ".tacit-usage-log.jsonl")
}

// stateDir resolves the member-local state directory: what the member set, or
// the default. Configured wins and is used as given — a member who names a
// directory has decided where their state goes, and this must not go creating
// anything anywhere else on the strength of it.
func stateDir(configured string) string {
	if configured != "" {
		return configured
	}
	return defaultStateDir()
}

// defaultStateDir is the member-local state directory, and it migrates anything
// an earlier build scattered in $HOME into it.
//
// The migration is one rename per file, once, on the same precedent as the
// technique-memory rename above: a member who already has a session history is
// not made to start over for a decision they had no part in. A rename that
// fails is dropped — the worst case is a file left where it was, which is
// exactly the state we are already in.
func defaultStateDir() string {
	dir := envcfg.DataHome()
	if dir == "." {
		return "" // no home -> in-memory only, like the paths above
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}
	for _, name := range strayStateFiles {
		dst := filepath.Join(dir, name)
		if _, err := os.Stat(dst); err == nil {
			continue // the new copy wins; never overwrite a live file
		}
		src := filepath.Join(home, name)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}
	return dir
}

// strayStateFiles are the undotted files an earlier build wrote straight into
// $HOME, because the session log took its directory from the usage log and the
// usage log lives at the top of the home directory.
var strayStateFiles = []string{"sessions.jsonl", "session-days.jsonl", "corrections.jsonl"}

// DefaultRegistryURL and DefaultRegistryKey are the compiled-in fallbacks Load
// returns when neither the environment nor agent.env names a registry. They are
// named so a caller can tell "the member configured this" from "nothing
// configured anything" — the difference `tacit invite` needs before it decides
// whether this host's own registry.env is the better answer (cmd/tacit/localadmin.go).
const (
	DefaultRegistryURL = "http://127.0.0.1:8080"
	DefaultRegistryKey = "dev-key"
)

// AgentEnvPath is the per-member settings file written by `tacit connect` and
// read by Load as a fallback for any TACIT_* variable absent from the
// environment. It automates member onboarding (registry URL + key) without
// touching shell profiles: hooks, the relay, the MCP server, and the CLI all
// pick it up because they all load config here. Environment always wins.
func AgentEnvPath(home string) string {
	return filepath.Join(home, ".config", "tacit", "agent.env")
}

// ReadEnvFile parses a KEY=VALUE file (comments and blanks skipped, quotes
// trimmed). Missing file -> empty map. It delegates to internal/envcfg, which
// is where the parser lives now; this name stays because the command calls it.
func ReadEnvFile(path string) map[string]string { return envcfg.ParseFile(path) }

var look envcfg.Lookup // agent.env fallback, loaded once per Load

func env(keys []string, def string) string        { return look.Str(def, keys...) }
func envInt(keys []string, def int) int           { return look.Int(def, keys...) }
func envFloat(keys []string, def float64) float64 { return look.Float(def, keys...) }

// Load reads the auditor configuration from the environment, falling back to
// the member's agent.env file (see AgentEnvPath) for anything unset.
func Load() Config {
	home, _ := os.UserHomeDir()
	look = envcfg.Lookup{File: ReadEnvFile(AgentEnvPath(home))}
	// Provider selects the backend by name (see internal/llmprovider). The base
	// URL and the default models come from that table, so an unset TACIT_LLM_*
	// still yields a working configuration for the chosen provider. The audit
	// layer runs on EVERY turn, so it defaults to the provider's cheap
	// AuditModel; ids drift and are overridable via TACIT_CHAR_MODEL /
	// TACIT_SYNTH_MODEL.
	provider := env([]string{"TACIT_LLM_PROVIDER"}, llmprovider.Default)
	p := llmprovider.Lookup(provider)
	return Config{
		LLMProvider:      provider,
		LLMBaseURL:       llmprovider.BaseURLFor(provider, env([]string{"TACIT_LLM_BASE_URL"}, "")),
		AnthropicVersion: "2023-06-01",
		LLMKeyFile: env([]string{"TACIT_KEY_FILE"},
			defaultKeyFile(home)),
		CharModel:  env([]string{"TACIT_CHAR_MODEL"}, p.AuditModel),
		SynthModel: env([]string{"TACIT_SYNTH_MODEL"}, p.AuditModel),
		MaxTokens:  envInt([]string{"TACIT_MAX_TOKENS"}, 2048),

		RegistryURL: env([]string{"TACIT_REGISTRY_URL"}, DefaultRegistryURL),
		RegistryKey: env([]string{"TACIT_API_KEY"}, DefaultRegistryKey),

		OmnigentBase:  env([]string{"OMNIGENT_BASE_URL"}, "http://127.0.0.1:6767"),
		OmnigentToken: env([]string{"OMNIGENT_TOKEN"}, ""),
		OmnigentEmail: env([]string{"OMNIGENT_EMAIL"}, ""),

		// Loopback-bound by default: per-developer-machine, not a shared
		// service — the loopback bind is the security boundary. The key is
		// optional defense in depth, read by both the agent and the relay.
		HooksHost:           env([]string{"TACIT_HOOKS_HOST"}, "127.0.0.1"),
		HooksPort:           envInt([]string{"TACIT_HOOKS_PORT"}, 8787),
		HooksAPIKey:         env([]string{"TACIT_HOOKS_KEY"}, "dev-hooks-key"),
		HooksSegment:        env([]string{"TACIT_HOOKS_SEGMENT"}, ""),
		TechniqueMemoryPath: env([]string{"TACIT_TECHNIQUE_MEMORY", "TACIT_CARD_MEMORY"}, defaultTechniqueMemoryPath()),
		PausePath:           env([]string{"TACIT_PAUSE_FILE"}, DefaultPausePath()),
		UsageLogPath:        env([]string{"TACIT_USAGE_LOG"}, defaultUsageLogPath()),
		StateDir:            stateDir(env([]string{"TACIT_STATE_DIR"}, "")),
		// TACIT_HOOKS_MAX_SUGGESTIONS is honoured as a fallback: it used to mean
		// "per session" and now means "per window", but an operator who set it
		// was asking for less unsolicited attention and the new reading gives
		// them that. Zero here means "unset" — hooks.NewAgent applies the
		// documented defaults (throttle.go).
		HooksMaxPerWindow:       envInt([]string{"TACIT_HOOKS_MAX_PER_WINDOW", "TACIT_HOOKS_MAX_SUGGESTIONS"}, 0),
		HooksWindowHours:        envFloat([]string{"TACIT_HOOKS_WINDOW_HOURS"}, 0),
		HooksSuggestionCooldown: envInt([]string{"TACIT_HOOKS_COOLDOWN_TURNS"}, 0),
		HooksCooldownMinutes:    envFloat([]string{"TACIT_HOOKS_COOLDOWN_MINUTES"}, 0),
		HooksMaxFitChecks:       envInt([]string{"TACIT_HOOKS_MAX_FIT_CHECKS"}, 4),
		HooksSynthBudgetSecs:    envFloat([]string{"TACIT_HOOKS_SYNTH_BUDGET_SECS"}, 4.0),
		HooksAskBudgetSecs:      envFloat([]string{"TACIT_HOOKS_ASK_BUDGET_SECS"}, 6.0),
		// Off by default: block reasons render in the CLI but Claude Code's
		// web/mobile clients show no hook output, and the agent cannot tell
		// which client is attached (advisor-mention-plan.md A0 findings).
		HooksMentionBlock: env([]string{"TACIT_HOOKS_MENTION_BLOCK"}, "0") == "1",
		HooksIdleExitSecs: envInt([]string{"TACIT_HOOKS_IDLE_EXIT_SECS"}, 900),
		HooksSpawnLog: env([]string{"TACIT_HOOKS_SPAWN_LOG"},
			filepath.Join(home, ".tacit-hooks.log")),
		SketchURL:   env([]string{"TACIT_SKETCH_URL"}, ""),
		SketchToken: env([]string{"TACIT_SKETCH_TOKEN"}, ""),
		SketchSalt:  env([]string{"TACIT_SESSION_SALT", "TACIT_SKETCH_SALT"}, "tacit"),
		SessionSalt: env([]string{"TACIT_SESSION_SALT", "TACIT_SKETCH_SALT"}, ""),
	}
}

// ParseSegment parses "team=revops,role=analyst" into a map.
func ParseSegment(s string) map[string]string {
	seg := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(pair), "="); ok {
			seg[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return seg
}

// defaultKeyFile is the member-writable Anthropic key file location.
func defaultKeyFile(home string) string {
	return filepath.Join(home, ".tacit-key.env")
}
