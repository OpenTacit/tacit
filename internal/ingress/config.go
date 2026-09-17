// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package ingress is the shared proxy that gives a self-hosted registry a
// public name — the service behind the registry's Global Access setting
// (docs/distribution/ingress.md).
//
// The shape in one paragraph: a registry dials OUT to the ingress and holds a
// control connection plus a handful of idle data connections. A request
// arriving at the ingress for that registry's hostname is written down one of
// those idle connections, and the registry answers it on its own loopback
// listener. Nothing listens on a public port at the member's end, nothing needs
// a firewall change, and NAT is irrelevant. When the tunnel drops, remote
// members get a 502 and local ones carry on exactly as before.
//
// What this package deliberately does NOT do: hold technique text or raw
// events. Besides its route and operations files, it can keep signed import
// receipts and privacy-thresholded outcome totals that registries opt to send.
package ingress

import (
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/envcfg"
)

// Config is the ingress's runtime configuration, read from the environment so a
// systemd unit or a container can set it without a config file.
type Config struct {
	// Zone is the DNS suffix instance hostnames sit under. `tacit.zone` is the
	// project's own, so a published instance answers at cedar-hollow.tacit.zone;
	// anyone running their own ingress points this at a domain they hold.
	//
	// The default is "localhost", which works with no DNS setup at all —
	// *.localhost resolves to loopback in every current browser — because the
	// common case for this binary today is a laptop, and a default naming a
	// domain the operator does not control would fail in a confusing way.
	Zone string
	// SiteHost is the product's own domain — where the project page, the install
	// address and anything else that belongs to the PRODUCT answer, as opposed to
	// the zone above, which is where somebody else's REGISTRY answers.
	//
	// The split is the point. A tenant hostname and a marketing page have nothing
	// in common except a binary: one is a name allocated to a stranger's data, the
	// other is the first thing a stranger reads. Keeping them on separate domains
	// means the zone can be handed out freely without the product's own address
	// living inside somebody else's namespace, and the product can be renamed,
	// moved or fronted by a static host without touching a single tenant's
	// address.
	//
	// Empty means the page answers at the zone's apex, which is what a
	// single-domain deployment wants and what a laptop gets. When it is set, the
	// zone's apex redirects here rather than serving a second copy — one page, one
	// address, and the old one still works for anybody who wrote it down.
	SiteHost string
	// AdminHost is the hostname serving the console. Defaults to
	// "ingress.<zone>". It must not collide with an instance name. It stays under
	// the zone even when SiteHost is set: the console operates the proxy, so it
	// belongs with the proxy's own domain rather than with the product's.
	AdminHost string
	// SitePublish turns the project page on at the zone's apex — the page a
	// stranger gets at https://<zone>/ (internal/ui/site.go).
	//
	// Off by default, and the default is the point: the apex is the one address
	// on this zone that anybody may reach without an invitation, so publishing a
	// page there is a decision somebody makes rather than a consequence of
	// deploying a binary. While it is off the apex redirects to the console's
	// gated preview, so the page can be reviewed at the address it will be
	// served from.
	SitePublish bool
	// PublicAddr is the listener taking proxied traffic and the console.
	PublicAddr string
	// PublicPort is the port the world reaches this ingress on, when that is
	// not the port it listens on. Behind a TLS terminator — Apache owning 443
	// and proxying to a loopback listener — the two differ, and it is the
	// outside one that belongs in a published address. Empty means they are the
	// same, which is the direct case.
	PublicPort string
	// TunnelAddr is the listener registries dial to. Separate from PublicAddr
	// because the two speak different protocols and, in a real deployment, sit
	// behind different firewall rules.
	TunnelAddr string
	// DataDir holds instances.json (the route table), ops.jsonl (the traffic
	// record), and intelligence.json (opted-in aggregate import outcomes).
	DataDir string
	// Scheme is what published URLs are built with. Defaults to https when a
	// certificate is configured and http when one is not, so the addresses the
	// ingress hands out match what it actually serves.
	Scheme string

	// TLSCert and TLSKey are the PEM pair this ingress serves. Both set means
	// HTTPS; neither means plain HTTP, which is right for a laptop and wrong for
	// anything reachable. The files are re-read when they change, so a renewal
	// does not need a restart and does not drop every tunnel
	// (docs/distribution/ingress.md).
	TLSCert string
	TLSKey  string

	// OIDC signs the console in. With no issuer configured the console is open,
	// which is correct for a laptop and stated loudly in the UI.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURI  string
	OIDCScopes       string
	SessionSecret    string
	SessionTTL       time.Duration
	CookieSecure     bool
	// AdminEmails, when non-empty, is the allowlist of addresses that may reach
	// the console. Empty means any address the identity provider accepts.
	AdminEmails []string

	// IdleConns is how many spare data connections the ingress asks each
	// registry to keep parked. It is the ceiling on concurrent in-flight
	// requests to that instance before one has to wait for a connection.
	IdleConns int
	// DialWait is how long a request will wait for a free connection before
	// giving up with a 503.
	DialWait time.Duration
	// ConnMaxIdle is how long a parked connection may sit unused before it is
	// retired and replaced. It must be comfortably under the idle timeout of
	// anything proxying for this ingress — Apache's ProxyTimeout defaults to
	// 120 seconds — or requests pay that timeout instead.
	ConnMaxIdle time.Duration
	// RequestTimeout bounds a single proxied request.
	RequestTimeout time.Duration
	// MaxRequestBytes caps a proxied request body.
	MaxRequestBytes int64
	// RatePerMinute caps requests per instance per minute; 0 disables.
	RatePerMinute int

	// Enrolment. A registry enrols itself by presenting the key it generated,
	// with nobody asked to approve it, so the guards here are the whole of the
	// admission policy.
	//
	// EnrollOpen turns self-service on; with it off, only registries already
	// enrolled can connect, which is how an ingress is closed to newcomers
	// without disturbing the ones it carries.
	EnrollOpen bool
	// MaxInstances is the ceiling on names this ingress will carry; 0 is
	// unlimited.
	MaxInstances int
	// EnrollPerHour caps NEW enrolments per source address per hour. Existing
	// instances reconnecting are never counted — an outage must not lock a
	// registry out of coming back.
	EnrollPerHour int

	// OpLogMaxBytes rotates the operations log at this size (one generation is
	// kept). OpLogRetentionDays prunes the rotated generation.
	OpLogMaxBytes      int64
	OpLogRetentionDays int
	// FullClientIP keeps whole client addresses in the operations log. The
	// default truncates to a /24 (or /64), which is enough to tell one office
	// from another and not enough to follow a person.
	FullClientIP bool

	// TrustedProxies names front proxies — as IPs or CIDRs — whose forwarding
	// headers this ingress will believe when deciding who a connection is from
	// (clientaddr.go). Loopback is always trusted, which covers the usual
	// arrangement of a web server on the same host; this is for a proxy that
	// sits somewhere else. Anything not named here is attributed to the socket
	// it actually arrived on, so a client cannot claim to be someone else.
	TrustedProxies []string

	// AllowPrefixes is the set of path prefixes the proxy will forward. The
	// point is not access control — the registry does its own auth — but to
	// stop a published tunnel being used as a general-purpose proxy.
	AllowPrefixes []string
}

// DefaultAllowPrefixes covers what a registry actually serves: the JSON API,
// the MCP endpoint and its OAuth furniture, the dashboard and the forms that
// act on it, the join and federation surfaces, and the static assets. Everything
// else 404s at the ingress without ever reaching the tunnel.
//
// This list is a copy of another program's route table, so it drifts: /admin/
// and /demo/ were once missing, which silently made a published dashboard
// read-only. Adding a route to the registry that this list does not cover now
// fails a test rather than an operator's afternoon
// (TestEveryRegistryRouteReachesTheTunnel).
var DefaultAllowPrefixes = []string{
	"/v1/", "/mcp", "/oauth/", "/.well-known/", "/auth/", "/join/", "/install.sh", "/f/",
	"/assets/", "/apple-touch-icon", "/manifest.webmanifest", "/favicon.ico", "/robots.txt",
	"/docs", "/techniques", "/outcomes", "/review", "/drafts", "/usage", "/learning",
	"/members", "/team", "/federation", "/settings", "/setup", "/insights", "/health",
	"/admin/", "/demo/",
}

// look reads the TACIT_INGRESS_* variables. The ingress has no fallback file —
// it is configured by its unit — so only the environment is consulted, and a
// value is trimmed before it counts as set.
var look = envcfg.Lookup{Trim: true}

func env(key, def string) string        { return look.Str(def, key) }
func envInt(key string, def int) int    { return look.Int(def, key) }
func envBool(key string, def bool) bool { return look.Bool(def, key) }

// FromEnv builds a configuration from TACIT_INGRESS_* variables, with defaults
// that run on a laptop with nothing set.
func FromEnv() Config {
	c := Config{
		Zone:               env("TACIT_INGRESS_ZONE", "localhost"),
		SiteHost:           env("TACIT_INGRESS_SITE_HOST", ""),
		AdminHost:          env("TACIT_INGRESS_ADMIN_HOST", ""),
		SitePublish:        envBool("TACIT_INGRESS_SITE", false),
		PublicAddr:         env("TACIT_INGRESS_ADDR", ":8443"),
		PublicPort:         env("TACIT_INGRESS_PUBLIC_PORT", ""),
		TunnelAddr:         env("TACIT_INGRESS_TUNNEL_ADDR", ":8444"),
		DataDir:            env("TACIT_INGRESS_DATA", "./ingress-data"),
		Scheme:             env("TACIT_INGRESS_SCHEME", ""),
		TLSCert:            env("TACIT_INGRESS_TLS_CERT", ""),
		TLSKey:             env("TACIT_INGRESS_TLS_KEY", ""),
		OIDCIssuer:         env("TACIT_INGRESS_OIDC_ISSUER", ""),
		OIDCClientID:       env("TACIT_INGRESS_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:   env("TACIT_INGRESS_OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURI:    env("TACIT_INGRESS_OIDC_REDIRECT_URI", ""),
		OIDCScopes:         env("TACIT_INGRESS_OIDC_SCOPES", "openid email profile"),
		SessionSecret:      env("TACIT_INGRESS_SESSION_SECRET", ""),
		SessionTTL:         time.Duration(envInt("TACIT_INGRESS_SESSION_TTL_HOURS", 12)) * time.Hour,
		CookieSecure:       envBool("TACIT_INGRESS_COOKIE_SECURE", false),
		IdleConns:          envInt("TACIT_INGRESS_IDLE_CONNS", 8),
		DialWait:           time.Duration(envInt("TACIT_INGRESS_DIAL_WAIT_MS", 5000)) * time.Millisecond,
		ConnMaxIdle:        time.Duration(envInt("TACIT_INGRESS_CONN_MAX_IDLE_SECS", 45)) * time.Second,
		RequestTimeout:     time.Duration(envInt("TACIT_INGRESS_REQUEST_TIMEOUT_SECS", 60)) * time.Second,
		MaxRequestBytes:    int64(envInt("TACIT_INGRESS_MAX_REQUEST_KB", 8192)) * 1024,
		RatePerMinute:      envInt("TACIT_INGRESS_RATE_PER_MINUTE", 1200),
		EnrollOpen:         envBool("TACIT_INGRESS_ENROLL_OPEN", true),
		MaxInstances:       envInt("TACIT_INGRESS_MAX_INSTANCES", 1000),
		EnrollPerHour:      envInt("TACIT_INGRESS_ENROLL_PER_HOUR", 10),
		OpLogMaxBytes:      int64(envInt("TACIT_INGRESS_OPLOG_MAX_MB", 64)) * 1024 * 1024,
		OpLogRetentionDays: envInt("TACIT_INGRESS_OPLOG_RETENTION_DAYS", 90),
		FullClientIP:       envBool("TACIT_INGRESS_FULL_CLIENT_IP", false),
		AllowPrefixes:      DefaultAllowPrefixes,
	}
	if v := env("TACIT_INGRESS_ADMIN_EMAILS", ""); v != "" {
		for _, e := range strings.Split(v, ",") {
			if e = strings.TrimSpace(strings.ToLower(e)); e != "" {
				c.AdminEmails = append(c.AdminEmails, e)
			}
		}
	}
	if v := env("TACIT_INGRESS_ALLOW_PREFIXES", ""); v != "" {
		c.AllowPrefixes = nil
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.AllowPrefixes = append(c.AllowPrefixes, p)
			}
		}
	}
	if v := env("TACIT_INGRESS_TRUSTED_PROXIES", ""); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.TrustedProxies = append(c.TrustedProxies, p)
			}
		}
	}
	return c.withDefaults()
}

func (c Config) withDefaults() Config {
	// The scheme follows the certificate unless it was set explicitly. An
	// ingress serving TLS that advertised http:// addresses would hand every
	// registry a URL that redirects or fails, and the operator would have had to
	// remember a second setting to prevent it.
	if c.Scheme == "" {
		c.Scheme = "http"
		if c.TLSCert != "" && c.TLSKey != "" {
			c.Scheme = "https"
		}
	}
	if c.AdminHost == "" {
		c.AdminHost = "ingress." + c.Zone
	}
	if c.IdleConns <= 0 {
		c.IdleConns = 8
	}
	if c.DialWait <= 0 {
		c.DialWait = 5 * time.Second
	}
	if c.ConnMaxIdle <= 0 {
		c.ConnMaxIdle = 45 * time.Second
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = 12 * time.Hour
	}
	if len(c.AllowPrefixes) == 0 {
		c.AllowPrefixes = DefaultAllowPrefixes
	}
	return c
}

// OIDCOn reports whether the console requires a sign-in.
func (c Config) OIDCOn() bool { return c.OIDCIssuer != "" && c.OIDCClientID != "" }

// PublicURL is the address an instance is reachable at — from outside, which is
// not always where this process is listening.
func (c Config) PublicURL(name string) string {
	return c.Scheme + "://" + c.HostFor(name) + c.publicPortSuffix()
}

// publicPortSuffix is the ":port" a published URL carries, empty when the port
// is the scheme's default and there is therefore nothing to say.
func (c Config) publicPortSuffix() string {
	if c.PublicPort != "" {
		if (c.Scheme == "https" && c.PublicPort == "443") || (c.Scheme == "http" && c.PublicPort == "80") {
			return ""
		}
		return ":" + c.PublicPort
	}
	return hostPort(c.PublicAddr, c.Scheme)
}

// SiteHostname is where the project page answers: the configured product domain
// when there is one, else the zone's own apex.
func (c Config) SiteHostname() string {
	if h := normalHost(c.SiteHost); h != "" {
		return h
	}
	return normalHost(c.Zone)
}

// SiteElsewhere reports whether the product has a domain of its own, which is the
// difference between the zone's apex serving the page and redirecting to it.
func (c Config) SiteElsewhere() bool {
	return normalHost(c.SiteHost) != "" && normalHost(c.SiteHost) != normalHost(c.Zone)
}

// IsAdminHost, IsSiteHost and IsZoneApex are the three questions routing asks of
// a Host header before it looks for an instance (proxy.go). They are mutually
// exclusive in any sane configuration and checked in that order regardless.
func (c Config) IsAdminHost(host string) bool { return normalHost(host) == normalHost(c.AdminHost) }
func (c Config) IsSiteHost(host string) bool  { return normalHost(host) == c.SiteHostname() }
func (c Config) IsZoneApex(host string) bool  { return normalHost(host) == normalHost(c.Zone) }

// SiteURL is where the project page is served and the address it is quoted at.
// With a product domain configured that is the product domain; without one it is
// the zone's apex, which is what a single-domain deployment wants.
func (c Config) SiteURL() string {
	return c.Scheme + "://" + c.SiteHostname() + c.publicPortSuffix()
}

// SiteRedirectURI is the callback the project page's own sign-in uses. It is
// derived rather than configured, because it MUST be the host serving the page: a
// mismatch is a sign-in that lands on the wrong hostname and a session cookie set
// where it will never be sent back. It has to be registered at the identity
// provider alongside the console's, and `serve` logs it at startup for exactly
// that reason.
func (c Config) SiteRedirectURI() string { return c.SiteURL() + "/auth/callback" }

// RedirectURIs maps every hostname this ingress signs somebody in on to the
// callback that host must use — the console's and the project page's. Both have
// to be registered at the identity provider, which is why `serve` prints them
// rather than leaving an operator to derive one of them.
//
// Moving the page to a product domain moves its callback with it, and forgetting
// that is the failure the derived address already had: the ingress starts, the
// console works, and a sign-in on the page fails at the provider naming an
// address nobody typed anywhere.
func (c Config) RedirectURIs() map[string]string {
	out := map[string]string{
		// The console keeps its configured callback verbatim: it is the one an
		// operator registered by hand, and it may name a host this would not derive.
		normalHost(c.AdminHost): c.OIDCRedirectURI,
	}
	out[c.SiteHostname()] = c.SiteRedirectURI()
	return out
}

// normalHost is a hostname as routing compares them: lowercase, no port.
func normalHost(h string) string { return strings.ToLower(hostOnly(strings.TrimSpace(h))) }

// SiteRedirectProblems reports why the apex's derived callback address will not be
// accepted by an identity provider, in words an operator can act on. Empty means
// it looks registrable.
//
// It exists because the address is DERIVED, and the two settings it is derived
// from are the two an ingress behind a TLS terminator gets wrong. Neither mistake
// shows up here: the ingress starts, the console works, and the apex's sign-in
// fails at Google with a redirect_uri_mismatch naming an address the operator
// never typed anywhere.
//
//   - Scheme follows the certificate, so an ingress serving plain HTTP behind
//     Apache or Cloudflare derives http:// unless TACIT_INGRESS_SCHEME says
//     otherwise — and no provider accepts http for a public host.
//   - The port comes from the listener, so a loopback :8443 behind a terminator on
//     443 derives a callback with :8443 in it unless TACIT_INGRESS_PUBLIC_PORT
//     says what the world actually reaches.
func SiteRedirectProblems(c Config) []string {
	var problems []string
	local := c.Zone == "localhost" || strings.HasSuffix(c.Zone, ".localhost") || c.Zone == "127.0.0.1"
	if c.Scheme != "https" && !local {
		problems = append(problems, "this is http:// on a public hostname, which no identity "+
			"provider will accept — set TACIT_INGRESS_SCHEME=https if TLS is terminated in front")
	}
	// The port is judged as it will be once the scheme is right, not as it is now.
	// publicPortSuffix omits a port only when it is the scheme's default, so an
	// otherwise-correct PublicPort=443 under a wrong http:// scheme looks like a
	// second fault and is not one. Reporting it would send an operator to fix the
	// setting that was already fine.
	corrected := c
	corrected.Scheme = "https"
	if port := corrected.publicPortSuffix(); port != "" && !local {
		problems = append(problems, "this carries the port "+strings.TrimPrefix(port, ":")+
			" — if the world reaches this ingress on 443, set TACIT_INGRESS_PUBLIC_PORT=443")
	}
	return problems
}

// HostFor is an instance's hostname, without a port.
func (c Config) HostFor(name string) string { return name + "." + c.Zone }

// hostPort renders the listener's port for a URL, omitting it when it is the
// scheme's default — a published address should read as a hostname, not as a
// hostname with :443 stuck on the end.
func hostPort(addr, scheme string) string {
	_, port, ok := strings.Cut(addr, ":")
	if !ok || port == "" {
		return ""
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return ""
	}
	return ":" + port
}

// PublicPortSuffix exposes the published ":port" for callers building their own
// URLs against this ingress.
func (c Config) PublicPortSuffix() string { return c.publicPortSuffix() }
