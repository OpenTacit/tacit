// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package cli is the body of the tacit-ingress command: the shared proxy behind the registry's "Access
// through the OpenTacit proxy" setting: it gives a self-hosted registry a public
// name without the member owning a domain, terminating TLS, or opening a port
// (docs/distribution/ingress.md).
//
// Two listeners. Registries dial the tunnel port and hold connections open;
// browsers, harnesses and MCP clients arrive at the public port and are routed
// to a registry by hostname. The console rides the same public port on its own
// hostname.
//
// Run it on a laptop with nothing configured at all:
//
//	tacit-ingress serve     # console on http://localhost:8443, tunnel on :8444
//
// Then, on the registry: turn on "Access through the OpenTacit proxy" in Settings,
// pointed at localhost:8444. It enrols itself and is told its address.
//
// It is a package rather than a main so that two binaries can be the same
// program. The open one mounts nothing and answers on its own hostname with the
// status page; the one that runs the project's instance mounts the operator
// console through Options.Mount. Everything else about them — the flags, the
// listeners, the startup report — is here, once.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/opentacit/tacit/pkg/ingress"
)

// Options is what a binary supplies: the version its build stamped in, and the
// one seam — a chance to mount handlers on the server before it starts serving.
type Options struct {
	// Version is what `tacit-ingress version` prints.
	Version string
	// Mount runs after the server is built and before it listens. It is where a
	// console is attached (Server.Console, Server.Site). Nil means a proxy with
	// no console, which is the published configuration.
	Mount func(*ingress.Server)
}

// version is what this process reports. Set from Options.Version on the way in.
var version = "dev"

// mount is Options.Mount, held here because serve is reached through a switch.
var mount func(*ingress.Server)

// Main runs the command and returns the process's exit code.
func Main(args []string, opts Options) int {
	if opts.Version != "" {
		version = opts.Version
	}
	mount = opts.Mount
	if len(args) < 1 {
		usage()
		return 2
	}
	var err error
	switch args[0] {
	case "serve":
		err = serve(args[1:])
	case "instances":
		err = instances(args[1:])
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		usage()
	default:
		usage()
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit-ingress: "+err.Error())
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprint(os.Stderr, `tacit-ingress — the shared proxy that gives a self-hosted registry a public name

  tacit-ingress serve                 run the proxy, the tunnel listener and the console
  tacit-ingress instances list        show the route table
  tacit-ingress instances rm <name>   release a name
  tacit-ingress version

Registries enrol themselves: one generates its own key, presents it, and is
given a hostname. Nothing here issues a token.

Configuration is environment-only (TACIT_INGRESS_*); `+"`serve --print-config`"+` lists it.
`)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "", "public listener (default $TACIT_INGRESS_ADDR or :8443)")
	tunnelAddr := fs.String("tunnel-addr", "", "tunnel listener (default $TACIT_INGRESS_TUNNEL_ADDR or :8444)")
	zone := fs.String("zone", "", "DNS suffix instances answer under (default $TACIT_INGRESS_ZONE or localhost)")
	siteHost := fs.String("site-host", "", "the product's own domain, where the project page answers (default $TACIT_INGRESS_SITE_HOST, else the zone's apex)")
	data := fs.String("data", "", "data directory (default $TACIT_INGRESS_DATA or ./ingress-data)")
	tlsCert := fs.String("tls-cert", "", "PEM certificate (with chain) to serve; enables HTTPS")
	tlsKey := fs.String("tls-key", "", "PEM private key for --tls-cert")
	printConfig := fs.Bool("print-config", false, "print the resolved configuration and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := ingress.FromEnv()
	if *addr != "" {
		cfg.PublicAddr = *addr
	}
	if *tunnelAddr != "" {
		cfg.TunnelAddr = *tunnelAddr
	}
	if *zone != "" {
		cfg.Zone = *zone
		cfg.AdminHost = ""
	}
	if *siteHost != "" {
		cfg.SiteHost = *siteHost
	}
	if *data != "" {
		cfg.DataDir = *data
	}
	if *tlsCert != "" {
		cfg.TLSCert = *tlsCert
	}
	if *tlsKey != "" {
		cfg.TLSKey = *tlsKey
	}
	if *printConfig {
		return printResolved(cfg)
	}

	srv, err := ingress.New(cfg)
	if err != nil {
		return err
	}
	defer srv.Close()
	if mount != nil {
		mount(srv)
	}

	tunnelLn, err := net.Listen("tcp", srv.Cfg.TunnelAddr)
	if err != nil {
		return fmt.Errorf("tunnel listener: %w", err)
	}
	publicLn, err := net.Listen("tcp", srv.Cfg.PublicAddr)
	if err != nil {
		return fmt.Errorf("public listener: %w", err)
	}

	tlsCfg, err := srv.TLSConfig()
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	public := &http.Server{
		Handler:           srv.PublicHandler(),
		ReadHeaderTimeout: 30 * time.Second,
		TLSConfig:         tlsCfg,
	}

	go func() {
		if err := srv.ServeTunnels(tunnelLn); err != nil && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintln(os.Stderr, "tunnel listener stopped: "+err.Error())
		}
	}()
	go func() {
		// ServeTLS with empty paths uses TLSConfig.GetCertificate, which is what
		// reloads the pair after a renewal.
		serve := public.Serve
		if tlsCfg != nil {
			serve = func(ln net.Listener) error { return public.ServeTLS(ln, "", "") }
		}
		if err := serve(publicLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "public listener stopped: "+err.Error())
		}
	}()

	// Retention is a promise the console makes to whoever's requests are in the
	// log, so something has to enforce it while the process runs rather than
	// only when it restarts. The same clock sweeps expired test names, which is
	// the other thing this process has promised to let go of.
	stop := make(chan struct{})
	sweep := func() {
		if dropped, err := srv.Ops.Prune(); err != nil {
			fmt.Fprintln(os.Stderr, "operations log prune: "+err.Error())
		} else if dropped > 0 {
			fmt.Printf("pruned %d expired operations\n", dropped)
		}
		expireTestInstances(srv)
	}
	// Once at startup as well as on the clock: a proxy restarted every few days
	// would otherwise never reach the first tick.
	sweep()
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				sweep()
			case <-stop:
				return
			}
		}
	}()

	// What this binary answers on its own hostname depends on whether anything
	// was mounted, and the report says which — an operator reading "console" and
	// finding a status page has been told the wrong thing about their build.
	fmt.Printf("tacit-ingress %s\n", version)
	if srv.Console != nil {
		fmt.Printf("  console   %s://%s%s\n", cfg.Scheme, srv.Cfg.AdminHost, portOf(srv.Cfg.PublicAddr))
	} else {
		fmt.Printf("  status    %s://%s%s (no console in this build; route table: tacit-ingress instances list)\n",
			cfg.Scheme, srv.Cfg.AdminHost, portOf(srv.Cfg.PublicAddr))
	}
	fmt.Printf("  public    %s (routing *.%s — registries only)\n", srv.Cfg.PublicAddr, srv.Cfg.Zone)
	fmt.Printf("  tunnel    %s\n", srv.Cfg.TunnelAddr)
	fmt.Printf("  data      %s\n", srv.Cfg.DataDir)
	if exp := srv.CertNotAfter(); !exp.IsZero() {
		fmt.Printf("  tls       %s (expires %s, reloaded from disk on renewal)\n",
			srv.Cfg.TLSCert, exp.Local().Format("2006-01-02"))
	} else {
		fmt.Println("  tls       none — serving plain HTTP; set --tls-cert/--tls-key before exposing this")
	}
	switch {
	case !cfg.OIDCOn() && srv.Console != nil:
		fmt.Println("  auth      OPEN — no identity provider configured; do not expose this console")
	case !cfg.OIDCOn():
		fmt.Println("  auth      none — no identity provider configured, and nothing here asks for one")
	case srv.EphemeralSessions():
		fmt.Println("  auth      OIDC, sessions reset on restart (set TACIT_INGRESS_SESSION_SECRET to persist them)")
	default:
		fmt.Printf("  auth      OIDC via %s\n", cfg.OIDCIssuer)
	}
	// The project page is served by whatever is mounted on Server.Site, so with
	// nothing mounted there is no page to report the state of.
	if srv.Site != nil {
		if cfg.SitePublish {
			fmt.Printf("  site      %s — PUBLISHED to anybody who asks\n", cfg.SiteURL())
		} else {
			fmt.Printf("  site      %s — gated; unset TACIT_INGRESS_SITE publishes nothing\n", cfg.SiteURL())
		}
		if cfg.SiteElsewhere() {
			fmt.Printf("            %s://%s%s redirects here, path and all\n",
				cfg.Scheme, cfg.Zone, portOf(srv.Cfg.PublicAddr))
		}
	}
	// Every host this ingress signs somebody in on needs its own callback
	// registered. Printed rather than left to be worked out: a missing redirect
	// URI fails at the provider, with an error page that names it and an operator
	// who has to go and find where it came from.
	if cfg.OIDCOn() && srv.Console != nil {
		fmt.Println("  callbacks register every one of these at the identity provider:")
		for _, uri := range sortedRedirectURIs(cfg) {
			fmt.Printf("            %s\n", uri)
		}
		for _, problem := range ingress.SiteRedirectProblems(cfg) {
			fmt.Printf("            WARNING  %s\n", problem)
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	close(stop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = public.Shutdown(ctx)
	_ = tunnelLn.Close()
	fmt.Println("stopped")
	return nil
}

// sortedRedirectURIs is the callback list in a stable order, so a restart does not
// reshuffle it and leave an operator wondering what changed.
func sortedRedirectURIs(cfg ingress.Config) []string {
	byHost := cfg.RedirectURIs()
	out := make([]string, 0, len(byHost))
	for _, uri := range byHost {
		if uri != "" {
			out = append(out, uri)
		}
	}
	sort.Strings(out)
	return out
}

func printResolved(cfg ingress.Config) error {
	for _, line := range [][2]string{
		{"zone", cfg.Zone},
		{"site host", cfg.SiteHostname()},
		{"admin host", cfg.AdminHost},
		{"public addr", cfg.PublicAddr},
		{"tunnel addr", cfg.TunnelAddr},
		{"data dir", cfg.DataDir},
		{"scheme", cfg.Scheme},
		{"oidc issuer", cfg.OIDCIssuer},
		{"admin emails", strings.Join(cfg.AdminEmails, ",")},
		{"idle conns", fmt.Sprint(cfg.IdleConns)},
		{"rate/minute", fmt.Sprint(cfg.RatePerMinute)},
		{"enrolment", map[bool]string{true: "open", false: "closed"}[cfg.EnrollOpen]},
		{"max instances", fmt.Sprint(cfg.MaxInstances)},
		{"enrol/hour/addr", fmt.Sprint(cfg.EnrollPerHour)},
		{"oplog retention", fmt.Sprint(cfg.OpLogRetentionDays) + "d"},
	} {
		value := line[1]
		if value == "" {
			value = "—"
		}
		fmt.Printf("%-16s %s\n", line[0], value)
	}
	return nil
}

func instances(args []string) error {
	if len(args) == 0 {
		return errors.New("instances list | rm <name>")
	}
	cfg := ingress.FromEnv()
	store, err := ingress.OpenStore(cfg.DataDir)
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		list := store.List()
		if len(list) == 0 {
			fmt.Println("no registries have enrolled yet")
			return nil
		}
		fmt.Printf("%-24s %-10s %-10s %s\n", "NAME", "KEY", "STATE", "LAST SEEN")
		for _, in := range list {
			state := "active"
			if in.Disabled {
				state = "suspended"
			}
			seen := "never"
			if !in.LastSeen.IsZero() {
				seen = in.LastSeen.Local().Format("2006-01-02 15:04")
			}
			fmt.Printf("%-24s %-10s %-10s %s\n", in.Name, in.Fingerprint, state, seen)
		}
		return nil

	case "rm":
		if len(args) < 2 {
			return errors.New("instances rm <name>")
		}
		if err := store.Delete(args[1]); err != nil {
			return err
		}
		fmt.Printf("released %s\n", args[1])
		return nil
	}
	return fmt.Errorf("unknown instances command %q", args[0])
}

func portOf(addr string) string {
	_, port, ok := strings.Cut(addr, ":")
	if !ok || port == "" {
		return ""
	}
	return ":" + port
}
