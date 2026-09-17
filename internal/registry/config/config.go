// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package config holds the registry's tunables — env-overridable runtime
// settings plus the ranking constants from docs/design/design.md.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentacit/tacit/internal/product"

	"github.com/opentacit/tacit/internal/envcfg"
)

// Config carries the registry's runtime settings, loaded from the environment
// (TACIT_*).
type Config struct {
	DataDir       string // file-store directory (techniques.json + events.jsonl)
	DBURL         string // when set, use the Postgres backend instead of the file store
	TechniquesDir string // curated *.md techniques
	Host          string
	Port          int
	APIKey        string // X-Tacit-Key for the /v1 JSON API

	// ProductName is what the registry calls itself on every surface it
	// renders — set with PRODUCT_NAME, which is the one setting deliberately
	// without a TACIT_ prefix (internal/product explains why). Load
	// materializes it back into the process environment so the display code,
	// which reaches for it far from any Config, resolves the same value.
	ProductName string

	// ExternalURL is the externally-reachable base URL the service is served
	// from — e.g. behind a reverse proxy or Tailscale Funnel at
	// https://tacit.example.com. When set it is the canonical origin
	// for all outward-facing URLs (OAuth issuer/metadata, federation provider
	// ID), overriding the derived local http://host:port. Leave empty for
	// purely local deployments.
	ExternalURL string

	// BasePath mounts the whole service under a sub-path — e.g. "/apps/tacit"
	// when a reverse proxy serves it at https://host/apps/tacit. Normalized by
	// Load: "/" (the default) becomes "", any other value keeps its leading
	// slash and drops trailing ones. Internal routes stay root-relative; the
	// handler strips the prefix on the way in and re-adds it to every emitted
	// URL on the way out. When set, ExternalURL should include the same path.
	BasePath string

	// Federation publishing (docs/federation/federation-design.md). ProviderID is the
	// URI that namespaces every entry this registry publishes — set it to the
	// externally reachable base URL; it defaults to the local address, which
	// is fine inside one network but should be pinned before sharing feeds
	// beyond it. The signing key persists at DataDir/feed_key.
	FeedProviderID   string
	FeedProviderName string

	// OIDC sign-in for the HTML view (all four must be set to enable).
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURI  string
	OIDCScopes       string
	SessionSecret    string
	SessionTTLSecs   int
	CookieSecure     bool

	// AdminEmails names the operators: signed-in members whose verified OIDC
	// email is on this list may edit the hot-appliable settings from the
	// dashboard. Deployment configuration, not registry data — the privacy
	// line (no member identity in registry state) is untouched. Empty list =
	// no admin concept: web settings stay read-only.
	AdminEmails []string

	// AuthMode is how a viewer becomes signed in: open, owner or oidc
	// (docs/design/registry-first-personal-tier.md). It defaults to open,
	// which is what every registry has been, and a published one may not stay
	// there.
	AuthMode string
	// OwnerSecret signs this registry's owner sessions and the one-time links
	// that mint them. Generated at `tacit init`, kept beside the instance key,
	// and never sent anywhere.
	OwnerSecret string

	// Global Access (docs/distribution/global-access-plan.md, formerly the
	// "Public access" setting). One switch with two consequences: the registry
	// dials PublishIngress from inside `tacit serve` and is given a public
	// address, AND it contributes its top-ranked general techniques to the commons
	// through the computed `public` channel. There is nothing to claim and no name
	// to pick: identity is the instance key this registry generated for itself,
	// and the hostname is allocated by the ingress.
	//
	// The two are deliberately not separable. A technique feed pays for the proxy
	// instead of an interaction feed, and a dial every rational operator turns to
	// zero is not a commons.
	GlobalAccess   bool
	PublishIngress string
	PublishTLS     bool

	// GlobalAccessConfirmed records that a person accepted the technique-sharing half.
	//
	// An instance that was already publishing when Global Access shipped consented
	// to a proxy, not to a feed of its techniques. The Global Access design requires that
	// an admin must not be able to opt a member's work into a vendor feed
	// silently. So such a registry keeps its public address and enters the STAGED
	// state: the channel is computed and shown, and not served, until an admin
	// confirms here.
	//
	// Staged began as a migration state. It is now where every registry starts:
	// `tacit init` turns the switch on and leaves this unset, because a registry
	// created ninety seconds ago clears no evidence floor and so contributes
	// nothing whatever it answers — consent taken then would buy the commons
	// nothing and cost the operator the address they need to open the dashboard
	// at all. The confirmation is asked for in Settings, when a technique is
	// actually eligible, and only a person ticking the box sets it. No flag and
	// no default writes it.
	GlobalAccessConfirmed bool

	// OpenFeedChannels are channels served WITHOUT a token, besides the computed
	// `public` one which is always open.
	//
	// It exists because gating every other channel is a breaking change for
	// registries that already federate: `general` is the example channel in
	// federation-design.md, it was world-readable, and subscribers polling it
	// would start receiving 404s on upgrade with nothing to tell them why. The
	// default is empty, so the safe posture is the one you get by doing nothing —
	// and an org that genuinely wants an open channel (an OSS community
	// publishing to anyone) says so explicitly rather than being unable to.
	//
	// Naming a channel here makes it readable by the entire internet whenever this
	// registry has a public address. That is the whole point and it is why it is
	// not a default.
	OpenFeedChannels []string

	// PublicMinN is the sample-size floor for entering the Public channel,
	// defaulting to federation.AttestationMinN. Raisable by operators whose
	// evidence is deep enough to afford it; see federation.PublicPolicy for why
	// the default is not higher.
	PublicMinN int

	EmbedModel string
	EmbedDim   int

	// Evidence-gated autonomy (docs/delivery/agent-delivery-plan.md Phase C):
	// when enabled, a stable technique whose measured record clears the bar below
	// is marked autonomy-eligible in evidence responses, and hook agents in
	// autonomous (consumer:agent) sessions apply it silently instead of
	// surfacing it. Off by default — autonomy is an operator decision.
	AutonomyEnabled       bool
	AutonomyMinHelpedRate float64 // minimum measured helped rate (0..1)
	AutonomyMinN          int     // minimum sample size behind that rate

	// Automated review (docs/learning/validation-without-review.md): the closed
	// loop that promotes techniques to serving with no human. AutoShadow routes
	// screened machine-provenance techniques into shadow evaluation instead of the
	// draft/review lane; AutoPromote graduates a shadow technique to stable once its
	// fit-check evidence clears the bar. Both off by default — automating the
	// review decision is an explicit operator choice. Served techniques remain subject
	// to decay + shadow auto-retire, so the loop is self-correcting on both ends.
	AutoShadow           bool
	AutoPromoteEnabled   bool
	AutoPromoteMinFit    float64 // shadow fit-rate bar for auto-promotion (0..1)
	AutoPromoteMinJudged int     // min shadow fit-check verdicts behind that rate
	// AutoDiscover runs the observed-technique discovery pass (cluster technique-less
	// worked-move sketches into candidates) on the recompute cycle.
	AutoDiscover bool

	RecomputeIntervalSecs int

	// DigestWebhook, when set, posts the weekly team digest to this Slack
	// incoming-webhook URL — the A2 delivery surface with zero recurring
	// operator effort (docs/distribution/growth-plan.md mechanism 3).
	DigestWebhook string

	// DemoDir is the whole of the demonstration-mode switch, and it is unset
	// everywhere but the instance that demonstrates the product. Set, every
	// *.json dataset in the directory (internal/demo portable format, written
	// by `tacit demo generate`) becomes a switchable scenario in the
	// dashboard's Demo menu, served from its own isolated file store under
	// DataDir/demo/<scenario>; production data is never touched. Unset, a
	// registry carries no trace of the scenarios: no menu, no switch route,
	// and a tacit_demo cookie that routes nothing and changes nothing.
	DemoDir string

	// FirstRun is true when no operator has ever configured this registry:
	// there is no registry.env file and no API key in the environment, so the
	// key would be the compiled-in "dev-key" default on 0.0.0.0 — a known key
	// on an open port. cmd/tacit uses this to start in SETUP MODE instead of
	// serving with defaults nobody chose (web/setup.go).
	FirstRun bool
}

// Ranking / retrieval constants (docs/design/design.md).
const (
	// RelevanceK is a top-K TRUNCATION, not a relevance test: it keeps the K
	// most-similar candidates however weakly they match. With a corpus in the
	// dozens it admits a large fraction of the playbook on every turn, which is
	// why the fit-check — not retrieval — ends up being the only thing standing
	// between a member and an irrelevant suggestion. MinSimilarity is the
	// actual gate; see it for why it is still 0.
	RelevanceK = 20 // keep this many most-similar candidates
	// MinSimilarity is the relevance floor: a candidate scoring below it is not
	// proposed at all, so a turn that matches nothing produces no suggestion
	// rather than a fit-check burst that rejects four techniques.
	//
	// It is 0 (disabled) DELIBERATELY, and must not be raised by guess. The
	// offline calibration in hack/calibrate_floor/ cannot set it: audit facts
	// are aggregate-only and do not retain ch.SummaryText, which is the leading
	// term of embed.QueryText, so a replayed query is not the query that ran
	// and accepted/rejected candidates do not separate under it. The honest
	// path is the live one — FeedbackEvent.Similarity now records the score on
	// every shown and declined event, so the real distribution accumulates in
	// the event log. Set this once that data shows a threshold that cuts
	// declines materially faster than it cuts shown techniques.
	MinSimilarity     = 0.0
	EvidenceN         = 4   // candidates returned in the evidence block
	ShadowK           = 2   // shadow candidates returned for judge-only evaluation, never surfaced (docs/learning/validation-without-review.md); D1 piggybacks these onto an existing fit-check, so keep the burst small
	ExploreFloorShown = 10  // exploration floor (docs/learning/validation-without-review.md, D4): a technique shown fewer than this many times is "under-explored" and gets one guaranteed evidence slot so measured techniques can't starve a cold technique forever; 0 disables
	Prior             = 0.5 // prior helped_rate for shrinkage (curated default)
	ShrinkK           = 20  // shrinkage strength: small samples regress toward Prior
	MinRankSample     = 10  // min adopted before measured impact reorders a technique above similarity (below this, a lucky 1/1 must not leapfrog more-relevant unmeasured techniques)
	MinSample         = 30  // min adopted in a segment before trusting its stats
	// SupportRowMinAdopted is the floor for PROPOSING a support_matrix row from
	// measurement, and it is deliberately far below MinSample. MinSample governs
	// whether a RATE may be trusted; a support row is not a rate. The claim is
	// "this was used here and somebody said it worked", and thirty adoptions is
	// not what makes that true — it is what would stop the claim ever being made
	// on a registry with one member in it.
	SupportRowMinAdopted = 3
	MinDismissSample     = 5  // min dismissals before rejection alone reorders a technique — dismissing is deliberate member effort and accumulates faster than adoption, so its floor is lower
	EvidenceThinBelow    = 2  // fewer than this many candidates -> meta.thin
	CohortTop            = 8  // size of the "common in segment" set
	DecayMinSample       = 20 // need this many recent adoptions to judge decay
	DecayDrop            = 0.25
	DecayWindowDays      = 14
	OverallKey           = "__overall__"

	// Shadow auto-retire (docs/learning/validation-without-review.md, D7): a
	// shadow technique judged enough times that fits too rarely is not worth more
	// fit-check budget — auto-exit shadow with no human, the symmetric negative
	// end of the shadow loop. Needs both a floor of verdicts (so a couple of
	// early non-fits can't retire a technique) and a fit rate below the bar.
	ShadowRetireMinSample = 8    // min shadow fit-check verdicts before auto-retire
	ShadowRetireFloor     = 0.15 // fit rate at/below which a well-judged shadow technique retires

	// Observed-technique discovery (docs/learning/observed-technique-discovery.md): cluster
	// the org's own technique-less "worked move" sketches into candidates. K sessions
	// across M cohorts is repetition AND the k-anonymity floor; a technique exists
	// only if several independent people, in more than one cohort, did the move.
	DupThreshold         = 0.90 // cosine at/above which a proposed technique duplicates an existing one
	ObserveMinSessions   = 3    // k: distinct sessions a cluster needs to qualify
	ObserveMinCohorts    = 2    // m: distinct cohort values a cluster needs
	ObserveSim           = 0.6  // cosine at/above which two sketches are grouped as the same move
	ObserveMaxTechniques = 5    // techniques filed per discovery pass

	// InferredWeight scales member-verdict events (adopted/helped/dismissed)
	// whose confidence is "inferred" when rolling up rates — the guardrail
	// docs/delivery/low-intrusion-plan.md promised: uncalibrated behavioral
	// inference must not drive helped_rate (and so ranking) at the same weight
	// as a member saying so. 0.5 is a placeholder stance, not a measurement;
	// the E1 calibration experiment owns the real per-signal weights. Shown and
	// declined events are machine-observed facts, never scaled.
	InferredWeight = 0.5
)

// SegmentDimensions are the fixed cohort dimensions for the pilot;
// SegmentPreference is the specificity order for outcome lookup; team is
// usually the most predictive inside an org.
//
// MemberDimensions is the subset a member TYPES. harness, surface and model are
// derived from the session and are always spelled the same way; the other four
// are free text, which is why the cohort directory exists — a member who picks
// `payments` when their colleagues use `payments-eng` splits every rollup that
// keys on the value.
//
// model is LAST in the preference order on purpose. It is the least predictive
// dimension inside an org, where everyone runs whatever was bought — and moving
// it up would repartition every existing registry's outcome lookup for no gain.
// Its value is to one member running several models, where it is the only
// cohort they have (docs/design/single-user-value.md). The values are
// canonical keys from internal/modelid, never raw labels: one model reaches
// OpenTacit under four names and a dimension that split them would compute each
// rate over a fraction of the evidence.
var (
	SegmentDimensions = []string{"team", "role", "function", "domain", "harness", "surface", "model"}
	SegmentPreference = []string{"team", "role", "function", "domain", "harness", "surface", "model"}
	CohortPreference  = []string{"team", "role"}
	MemberDimensions  = []string{"team", "role", "function", "domain"}
)

// DefaultIngress is the proxy a registry publishes through unless it is pointed
// somewhere else. A hostname — the scheme and port are worked out from it, so
// what an operator types is what they were given: "ingress.tacit.zone". The
// tunnel then arrives as an HTTP upgrade on that host's ordinary port, needing
// no port of its own and passing through whatever web server owns 443 there.
//
// The default is localhost because a default naming a host that does not
// resolve is worse than no default — it fails slowly and blames the network.
// When pointing at a shared ingress, change this and DefaultPublishTLS
// together (docs/distribution/ingress.md).
// On the registry-first branch this names the ingress the project runs, because
// "single-user access through the default ingress server" is not a default that
// resolves to a laptop. The comment above is why it was localhost: a default
// naming a host that does not resolve fails slowly and blames the network. A
// host that does resolve settles that.
const DefaultIngress = "ingress.tacit.zone"

// DefaultPublishTLS applies only to the bare host:port form, where there is no
// scheme to read it from. A URL says https or http and settles the question.
const DefaultPublishTLS = true

// ConfigDir is where operator settings live — registry.env, and the instance
// key that is this registry's identity to the ingress.
func ConfigDir() string { return filepath.Dir(RegistryEnvPath()) }

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// DefaultPort is the port a registry serves on when nothing says otherwise.
const DefaultPort = 8080

// The retrieval space a registry uses before an operator configures one: the
// dependency-free lexical embedder, at the width every stored vector is
// compared in. Named so a command writing a technique outside `serve` lands it
// in the same space the server will read it from.
const (
	DefaultEmbedModel = "hashing-v1"
	DefaultEmbedDim   = 256
)

// DataHome is the per-user tacit state directory ($XDG_DATA_HOME/tacit or
// ~/.local/share/tacit) — where an installed binary keeps its store, techniques,
// and models when no repo checkout provides them.
func DataHome() string { return envcfg.DataHome() }

// defaultDir resolves a working-directory-relative default ("data", "techniques"):
// if the directory exists next to the process — the repo layout the dev loop
// and the original deployment run from — keep it; otherwise fall back to the
// data home, so an installed binary started from an arbitrary directory is
// self-contained instead of scattering state across whatever CWD it got.
func defaultDir(rel string) string {
	if _, err := os.Stat(rel); err == nil {
		return rel
	}
	return filepath.Join(DataHome(), rel)
}

// RegistryEnvPath is the operator settings file — the same file the systemd
// unit reads via EnvironmentFile=, written by the first-run setup UI, and read
// here as a fallback so a bare `tacit serve` honors it too. Override with
// TACIT_REGISTRY_ENV (tests).
func RegistryEnvPath() string {
	if p := os.Getenv("TACIT_REGISTRY_ENV"); p != "" {
		return p
	}
	return DefaultRegistryEnvPath()
}

// DefaultRegistryEnvPath is where this machine's registry keeps its settings,
// whatever TACIT_REGISTRY_ENV says. The service unit names this file, so a
// command that installs or restarts the service has to know whether the
// settings it just wrote are the ones that service will read.
func DefaultRegistryEnvPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "tacit", "registry.env")
}

// ExportFileEnv puts registry.env into this process's environment, for every key
// the environment does not already set.
//
// Load reads the file itself, so anything that reaches configuration through it
// was always fine. What was not fine is the handful of values read straight
// from the environment with os.Getenv — TACIT_LLM_API_KEY above all, because
// Settings writes it to the file and the running process sees it only because
// the save also calls os.Setenv. Restart, and it was gone: nothing had ever put
// the file into the environment.
//
// A service does this already: systemd's EnvironmentFile= and Docker's
// --env-file both load registry.env before the process starts, which is why a
// registry run by hand behaved differently from the same registry run as a
// unit — and why the difference went unnoticed.
//
// The environment still wins, so an operator who sets a variable explicitly
// keeps overriding the file, exactly as the settings page says.
func ExportFileEnv() {
	path := RegistryEnvPath()
	if path == "" {
		return
	}
	for k, v := range readEnvFile(path) {
		if v == "" || os.Getenv(k) != "" {
			continue
		}
		_ = os.Setenv(k, v)
	}
}

// fileVals holds registry.env KEY=VALUE pairs, loaded once per Load call.
// Environment variables always win; the file fills what the environment
// leaves unset — the same posture as the agent's agent.env.
func readEnvFile(path string) map[string]string { return envcfg.ParseFile(path) }

// Load reads the registry configuration: environment first, then the
// registry.env file for anything unset, then compiled defaults.
func Load() Config {
	envFile := RegistryEnvPath()
	look := envcfg.Lookup{File: readEnvFile(envFile)}
	env := func(keys []string, def string) string { return look.Str(def, keys...) }
	envInt := func(keys []string, def int) int { return look.Int(def, keys...) }
	envFloat := func(keys []string, def float64) float64 { return look.Float(def, keys...) }
	fileExists := false
	if envFile != "" {
		if _, err := os.Stat(envFile); err == nil {
			fileExists = true
		}
	}
	// pkg/embed reads TACIT_ONNX_* from the process environment directly (it
	// must stay importable without this package), so the env-file fallback has
	// to be materialized for it. The systemd path gets this via
	// EnvironmentFile=; this covers a bare `tacit serve`.
	for k, v := range look.File {
		if strings.HasPrefix(k, "TACIT_ONNX_") && os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	// Same reason, one setting further: the display name is read by
	// internal/product, which stays importable without this package (config
	// imports it, not the other way round), so it sees only the process
	// environment. Materializing the resolved value means a PRODUCT_NAME set in
	// registry.env reaches every rendered surface, not just the ones holding a
	// Config. Set unconditionally, so the environment agrees with this struct
	// even when both are the compiled default.
	productName := env([]string{product.EnvKey}, product.Default)
	os.Setenv(product.EnvKey, productName)
	return Config{
		FirstRun:              !fileExists && os.Getenv("TACIT_API_KEY") == "",
		ProductName:           productName,
		DigestWebhook:         env([]string{"TACIT_DIGEST_WEBHOOK"}, ""),
		DataDir:               env([]string{"TACIT_DATA"}, defaultDir("data")),
		DBURL:                 env([]string{"TACIT_DB_URL", "DATABASE_URL"}, ""),
		TechniquesDir:         env([]string{"TACIT_TECHNIQUES_DIR"}, defaultDir("techniques")),
		Host:                  env([]string{"TACIT_HOST"}, "0.0.0.0"),
		Port:                  envInt([]string{"TACIT_PORT"}, DefaultPort),
		APIKey:                env([]string{"TACIT_API_KEY"}, "dev-key"),
		ExternalURL:           env([]string{"TACIT_EXTERNAL_URL"}, ""),
		BasePath:              NormalizeBasePath(env([]string{"TACIT_BASE_PATH"}, "/")),
		FeedProviderID:        env([]string{"TACIT_FEED_PROVIDER_ID"}, ""),
		FeedProviderName:      env([]string{"TACIT_FEED_PROVIDER_NAME"}, productName+" registry"),
		OIDCIssuer:            env([]string{"TACIT_OIDC_ISSUER"}, ""),
		OIDCClientID:          env([]string{"TACIT_OIDC_CLIENT_ID"}, ""),
		OIDCClientSecret:      env([]string{"TACIT_OIDC_CLIENT_SECRET"}, ""),
		OIDCRedirectURI:       env([]string{"TACIT_OIDC_REDIRECT_URI"}, ""),
		OIDCScopes:            env([]string{"TACIT_OIDC_SCOPES"}, "openid email profile"),
		SessionSecret:         env([]string{"TACIT_SESSION_SECRET"}, ""),
		SessionTTLSecs:        envInt([]string{"TACIT_SESSION_TTL_SECS"}, 8*60*60),
		CookieSecure:          env([]string{"TACIT_COOKIE_SECURE"}, "0") == "1",
		AdminEmails:           splitEmails(env([]string{"TACIT_ADMIN_EMAILS"}, "")),
		AuthMode:              env([]string{"TACIT_AUTH_MODE"}, AuthOpen),
		OwnerSecret:           env([]string{"TACIT_OWNER_SECRET"}, ""),
		GlobalAccess:          env([]string{"TACIT_GLOBAL_ACCESS"}, "0") == "1",
		GlobalAccessConfirmed: env([]string{"TACIT_GLOBAL_ACCESS_CONFIRMED"}, "0") == "1",
		PublicMinN:            envInt([]string{"TACIT_PUBLIC_MIN_N"}, 0),
		OpenFeedChannels:      splitCSV(env([]string{"TACIT_OPEN_FEED_CHANNELS"}, "")),
		PublishIngress:        env([]string{"TACIT_PUBLISH_INGRESS"}, DefaultIngress),
		PublishTLS:            env([]string{"TACIT_PUBLISH_TLS"}, boolStr(DefaultPublishTLS)) == "1",
		AutonomyEnabled:       env([]string{"TACIT_AUTONOMY"}, "0") == "1",
		AutonomyMinHelpedRate: envFloat([]string{"TACIT_AUTONOMY_MIN_HELPED_RATE"}, 0.8),
		AutonomyMinN:          envInt([]string{"TACIT_AUTONOMY_MIN_N"}, 20),
		AutoShadow:            env([]string{"TACIT_AUTO_SHADOW"}, "0") == "1",
		AutoPromoteEnabled:    env([]string{"TACIT_AUTO_PROMOTE"}, "0") == "1",
		AutoPromoteMinFit:     envFloat([]string{"TACIT_AUTO_PROMOTE_MIN_FIT"}, 0.6),
		AutoPromoteMinJudged:  envInt([]string{"TACIT_AUTO_PROMOTE_MIN_JUDGED"}, 12),
		AutoDiscover:          env([]string{"TACIT_AUTO_DISCOVER"}, "0") == "1",
		EmbedModel:            env([]string{"TACIT_EMBED_MODEL"}, DefaultEmbedModel),
		EmbedDim:              envInt([]string{"TACIT_EMBED_DIM"}, DefaultEmbedDim),
		RecomputeIntervalSecs: envInt([]string{"TACIT_RECOMPUTE_SECS"}, 300),
		DemoDir:               env([]string{"TACIT_DEMO_DIR"}, ""),
	}
}

// OIDCEnabled reports whether all four OIDC settings are present.
// Auth modes. A registry is one of these three, and which one it is decides
// whether the dashboard has a gate at all.
const (
	// AuthOpen is the pilot default and what every install has had until now:
	// no gate. Correct on a loopback address, and the reason a published
	// registry may not be in this mode.
	AuthOpen = "open"
	// AuthOwner is one member and no identity provider. Sessions are minted
	// from the console that started the process, the way the setup claim code
	// proves an operator.
	AuthOwner = "owner"
	// AuthTeam is what a single-member registry becomes when its owner invites
	// somebody (docs/design/browser-led-team-transition.md, M7). The owner
	// secret still signs the owner in; the member keys minted by an invitation
	// now sign their holders in too, so a colleague can open the dashboard
	// rather than only reaching the API. It is deliberately a THIRD mode rather
	// than a flag on AuthOwner: `tacit merge` refuses to act on a registry that
	// is not one member's, and that refusal has to keep working the moment a
	// registry stops being one.
	AuthTeam = "team"
	// AuthOIDC is an organization's identity provider.
	AuthOIDC = "oidc"
)

// OwnerEnabled reports single-member authentication: a secret and the mode that
// uses it. Both, because a secret left in a file by an earlier run must not
// silently turn a gate on, and a mode with no secret cannot let anyone in.
func (c Config) OwnerEnabled() bool {
	return (c.AuthMode == AuthOwner || c.AuthMode == AuthTeam) && c.OwnerSecret != ""
}

// SingleMember reports whether this registry is one person's — the personal
// tier, before anybody was invited.
//
// The distinction OwnerEnabled cannot make. Owner sessions keep working after a
// registry opens to a team, so every gate that asks "can the owner sign in?"
// must stay true across that change; every question about whether this is still
// ONE member's registry — whether `tacit merge` may fold it into somebody
// else's, whether the dashboard should lead with the guide — must ask this
// instead.
func (c Config) SingleMember() bool {
	return c.AuthMode == AuthOwner && c.OwnerSecret != ""
}

// TeamEnabled reports whether member keys sign their holders into the
// dashboard. True only after an owner has opened the registry to a team: on an
// organization's OIDC registry the provider is the way in, and on a
// single-member one there is nobody to let in.
func (c Config) TeamEnabled() bool {
	return c.AuthMode == AuthTeam && c.OwnerSecret != ""
}

// AuthConfigured reports whether the settings describe a gate — either an
// identity provider or an owner secret.
//
// It answers a question about CONFIGURATION, which is what `serve` needs before
// it dials the ingress. The dashboard's gates ask a different question, of the
// running server, because a Server can be handed a provider directly; see
// Server.authRequired.
func (c Config) AuthConfigured() bool { return c.OIDCEnabled() || c.OwnerEnabled() }

func (c Config) OIDCEnabled() bool {
	return c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCClientSecret != "" && c.OIDCRedirectURI != ""
}

// ExternalBase returns the configured external base URL with any trailing slash
// trimmed, or "" if none is set.
func (c Config) ExternalBase() string {
	return strings.TrimRight(c.ExternalURL, "/")
}

// CallbackURL is the OIDC redirect URI to STORE for this registry: its own
// address, derived from what the registry already knows.
//
// Deriving it is the single biggest thing the two sign-in flows (`tacit secure`
// and the Settings page's sign-in form) do for an operator — it is the value
// that has to match, character for character, at the provider. It is
// deliberately the registry's OWN address even when a published address is
// serving today: the public address supersedes it at runtime
// (web.Server.OIDCRedirectURI), and storing the proxy's URL here would break
// sign-in at exactly the moment that address went away.
func (c Config) CallbackURL() string {
	if base := c.ExternalBase(); base != "" {
		return base + "/auth/callback"
	}
	if c.OIDCRedirectURI != "" {
		return c.OIDCRedirectURI
	}
	host := c.Host
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "", "0.0.0.0", "::", "127.0.0.1", "::1", "localhost":
		// A bind address is not a name a provider can return a member to.
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%d%s/auth/callback", host, c.Port, c.BasePath)
}

// ExternalOrigin returns just the scheme://host origin of the configured
// external URL (dropping any path/query), or "" if none is set or it does not
// parse to an absolute URL. Used where only the origin is meaningful, such as
// the OAuth issuer and metadata endpoints.
func (c Config) ExternalOrigin() string {
	if c.ExternalURL == "" {
		return ""
	}
	u, err := url.Parse(c.ExternalURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// NormalizeBasePath maps the operator's TACIT_BASE_PATH spelling onto the
// internal convention: "" for a root mount, otherwise "/sub/path" — leading
// slash, no trailing slash — so callers can always write base + "/route".
func NormalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// splitCSV parses a comma-separated list: trimmed, blanks dropped, case kept
// (channel names are lowercase by validation, not by normalization here).
func splitCSV(v string) []string {
	var out []string
	for _, e := range strings.Split(v, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// splitEmails parses a comma-separated email list: trimmed, lowercased,
// blanks dropped — so " Ada@Example.com, " and "ada@example.com" compare equal.
func splitEmails(v string) []string {
	var out []string
	for _, e := range strings.Split(v, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}
