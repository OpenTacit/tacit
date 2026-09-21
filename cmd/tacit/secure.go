// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit secure — turn dashboard sign-in on (or back off) from the console.
//
// It exists because an open registry has no way to close itself from the web.
// Admins are signed-in members on TACIT_ADMIN_EMAILS, so a registry with no
// OIDC has no admins at all, and the Settings page says so out loud
// ("Read-only: web edits need a configured sign-in"). The first-run form can
// configure OIDC once, behind the console claim code; after that the only
// supported path was hand-editing registry.env. The console is the one channel
// an open registry can still trust — the same reasoning that put a claim code
// on first-run setup — so this is where the switch belongs.
//
// The work it does that a documented procedure cannot: it derives the callback
// URL from what the registry already knows (external URL + base path), proves
// the issuer by fetching its discovery document BEFORE writing anything,
// refuses a partial set of the four settings (which registry.env otherwise
// accepts in silence, leaving the dashboard open), and mints the session secret
// nobody remembers. `--off` is the way back in when a wrong config locks the
// operator out of their own dashboard.

import (
	"bufio"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/web"
	"github.com/opentacit/tacit/internal/servicectl"
)

// oidcKeys are the four settings that must move together. The registry enables
// sign-in only when all four are present (config.OIDCEnabled), so they are
// written as a set and cleared as a set.
var oidcKeys = []string{
	"TACIT_OIDC_ISSUER",
	"TACIT_OIDC_CLIENT_ID",
	"TACIT_OIDC_CLIENT_SECRET",
	"TACIT_OIDC_REDIRECT_URI",
}

func cmdSecure(args []string) int {
	fs := flag.NewFlagSet("secure", flag.ContinueOnError)
	off := fs.Bool("off", false, "remove the OIDC settings and restart — the way back in if a sign-in configuration locks you out")
	dryRun := fs.Bool("dry-run", false, "print the settings that would change and write nothing")
	issuerFlag := fs.String("issuer", "", "OIDC issuer URL (prompted when absent)")
	clientIDFlag := fs.String("client-id", "", "OIDC client ID (prompted when absent)")
	secretFlag := fs.String("client-secret", "", "OIDC client secret (prefer the prompt: a flag lands in your shell history)")
	callbackFlag := fs.String("callback", "", "redirect URI to register (default: derived from the registry's external URL)")
	scopesFlag := fs.String("scopes", "", "override the requested scopes (default: openid email profile)")
	adminsFlag := fs.String("admins", "", "comma-separated emails that may administer the dashboard")
	yes := fs.Bool("yes", false, "do not stop for confirmation (headless setup)")
	noRestart := fs.Bool("no-restart", false, "write the settings and start nothing: no service installed, no restart, you apply them yourself")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	envPath := registryconfig.RegistryEnvPath()
	if envPath == "" {
		fmt.Fprintln(os.Stderr, "no home directory; set TACIT_REGISTRY_ENV")
		return 1
	}
	if _, err := os.Stat(envPath); err != nil {
		fmt.Fprintf(os.Stderr, "no registry on this machine (%s does not exist)\nrun `tacit init` first\n", envPath)
		return 1
	}
	cfg := registryconfig.Load()
	fileVals := registryconfig.ReadEnv(envPath)
	live := liveCallback(cfg)

	fmt.Printf("registry settings: %s\n", envPath)
	c := &checker{}
	fmt.Println("\nexposure:")
	registrySecurityChecks(c, cfg, live)

	p := &prompter{sc: bufio.NewScanner(os.Stdin), interactive: stdinIsTerminal()}
	if *off {
		return secureOff(cfg, envPath, p, *yes, *dryRun, *noRestart)
	}
	return secureOn(cfg, fileVals, envPath, p, secureOpts{
		issuer: *issuerFlag, clientID: *clientIDFlag, clientSecret: *secretFlag,
		callback: *callbackFlag, scopes: *scopesFlag, admins: *adminsFlag,
		live: live, yes: *yes, dryRun: *dryRun, noRestart: *noRestart,
	})
}

type secureOpts struct {
	issuer, clientID, clientSecret string
	callback, scopes, admins       string
	live                           string // the callback the running registry uses now, when it differs
	yes, dryRun, noRestart         bool
}

// secureOn walks the operator through the one part no program can do for them
// — registering a client at their identity provider — and does everything
// around it: derive the callback, prove the issuer, write the set, restart.
func secureOn(cfg registryconfig.Config, fileVals map[string]string, envPath string, p *prompter, o secureOpts) int {
	// 1. The callback. This is the value that has to match, character for
	// character, at the provider; deriving it from what the registry already
	// knows is the single biggest thing this command does for the operator.
	callback := o.callback
	if callback == "" {
		callback = defaultCallback(cfg)
	}
	if p.interactive && o.callback == "" && !o.yes {
		// Name the value for what it IS. Published, this setting is not the URI
		// the provider sees — the public address supersedes it — so calling it
		// "the callback to register" would send the operator to their provider
		// with the one address that is currently inert. It is still the right
		// thing to STORE: it is what the registry falls back to when the public
		// address goes away, and storing the proxy URL here would break sign-in
		// at exactly that moment.
		question := "callback URL to register with your provider"
		if o.live != "" && !strings.EqualFold(o.live, callback) {
			question = "callback URL to store (this registry's own address, for when it is not published)"
		}
		callback = p.ask(question, callback)
	}
	if problem := checkCallback(callback); problem != "" {
		fmt.Fprintf(os.Stderr, "\n%s\n", problem)
		return 2
	}

	// 2. The issuer, proved before anything is written. A wrong issuer spelling
	// (Okta's custom authorization server, the Entra tenant path, the Keycloak
	// realm) is otherwise discovered at the first sign-in, by which time the
	// dashboard is already closed.
	issuer := strings.TrimRight(orPrompt(p, o.issuer, "issuer URL", cfg.OIDCIssuer, o.yes), "/")
	if issuer == "" {
		fmt.Fprintln(os.Stderr, "\nan issuer is required (--issuer, e.g. https://accounts.google.com)")
		return 2
	}
	doc, err := fetchDiscovery(issuer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n%v\n", err)
		fmt.Fprintln(os.Stderr, "the issuer is the base URL that serves /.well-known/openid-configuration:")
		fmt.Fprintln(os.Stderr, "  Google     https://accounts.google.com")
		fmt.Fprintln(os.Stderr, "  Okta       https://<org>.okta.com  (or .../oauth2/default)")
		fmt.Fprintln(os.Stderr, "  Entra ID   https://login.microsoftonline.com/<tenant-id>/v2.0")
		fmt.Fprintln(os.Stderr, "  Keycloak   https://<host>/realms/<realm>")
		return 1
	}
	fmt.Printf("\n   ok  %s serves a discovery document\n", issuer)
	for _, ep := range []string{"authorization_endpoint", "token_endpoint", "userinfo_endpoint"} {
		fmt.Printf("    -  %s: %s\n", strings.TrimSuffix(ep, "_endpoint"), doc[ep])
	}

	// 3. The manual step. No provider hands out a client without a human in its
	// console, and the admin APIs that could are a per-provider credential
	// problem of their own — so the command's job is to make the paste exact.
	scopes := o.scopes
	if scopes == "" {
		scopes = orElseStr(cfg.OIDCScopes, "openid email profile")
	}
	fmt.Println("\nat your identity provider, create a web application (a confidential client) with:")
	if o.live != "" && !strings.EqualFold(o.live, callback) {
		// The registry answers on a public address that supersedes the
		// configured one, so the URI it will send is NOT the one about to be
		// written. Both belong at the provider: the live one for sign-in now,
		// the stored one for when that address goes away.
		fmt.Printf("  redirect URI   %s\n", o.live)
		fmt.Printf("                 (this registry's public address — the URI sign-in sends today)\n")
		fmt.Printf("  and also       %s\n", callback)
		fmt.Printf("                 (the value written below, used if the public address goes away)\n")
	} else {
		fmt.Printf("  redirect URI   %s\n", callback)
	}
	fmt.Printf("  scopes         %s\n", scopes)
	fmt.Println("  client type    web / server-side (it keeps a client secret)")
	if p.interactive && !o.yes {
		p.ask("press enter when the client exists", "")
	}

	clientID := orPrompt(p, o.clientID, "client ID", cfg.OIDCClientID, o.yes)
	clientSecret := o.clientSecret
	if clientSecret == "" && p.interactive {
		// Read from the terminal rather than a flag, for the same reason
		// doctor --fix reads a member key there: it stays out of the shell
		// history and out of the process list. Unechoed, so it stays out of
		// the scrollback too.
		clientSecret = p.askSecret("client secret")
	}
	if clientID == "" || clientSecret == "" {
		fmt.Fprintln(os.Stderr, "\nboth a client ID and a client secret are required — the registry exchanges the code from the server")
		return 2
	}

	// 4. The set to write. Everything here is a setting an operator forgets and
	// then debugs: the session secret whose absence signs everyone out at each
	// restart, and the secure-cookie flag that an https callback implies.
	updates := map[string]string{
		"TACIT_OIDC_ISSUER":        issuer,
		"TACIT_OIDC_CLIENT_ID":     clientID,
		"TACIT_OIDC_CLIENT_SECRET": clientSecret,
		"TACIT_OIDC_REDIRECT_URI":  callback,
		// The mode, because the four values only open the gate; they do not say
		// what this registry now IS. Secured out of owner mode without this,
		// AuthMode stayed "owner": SingleMember() went on reporting true, so an
		// organization's registry kept presenting itself as one person's — the
		// team transition rather than the members list — and the owner link
		// kept signing its holder in past the provider they had just
		// configured. Writing it here closes both.
		"TACIT_AUTH_MODE": registryconfig.AuthOIDC,
	}
	if o.scopes != "" {
		updates["TACIT_OIDC_SCOPES"] = o.scopes
	}
	if fileVals["TACIT_SESSION_SECRET"] == "" {
		updates["TACIT_SESSION_SECRET"] = web.NewAPIKey()
	}
	if strings.HasPrefix(callback, "https://") && !cfg.CookieSecure {
		updates["TACIT_COOKIE_SECURE"] = "1"
	}
	admins := o.admins
	if admins == "" && len(cfg.AdminEmails) == 0 && p.interactive && !o.yes {
		fmt.Println("\nadmin emails decide who may change settings and curate the playbook.")
		fmt.Println("left empty, every member who signs in can do both — a pilot posture.")
		admins = p.ask("admin emails (comma-separated, optional)", "")
	}
	if admins != "" {
		for _, e := range strings.Split(admins, ",") {
			if e = strings.TrimSpace(e); e != "" && !strings.Contains(e, "@") {
				fmt.Fprintf(os.Stderr, "\n%q is not an email address\n", e)
				return 2
			}
		}
		updates["TACIT_ADMIN_EMAILS"] = admins
	}

	fmt.Printf("\nto write to %s:\n", envPath)
	printUpdates(updates)
	if o.dryRun {
		fmt.Println("\n--dry-run: nothing written")
		return 0
	}
	if !o.yes && !p.confirm("write these settings and restart the registry?") {
		fmt.Println("nothing changed")
		return 0
	}
	if err := registryconfig.PatchEnv(envPath, updates); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", envPath, err)
		return 1
	}
	fmt.Printf("wrote %s\n", envPath)

	code := finishOn(cfg, envPath, o.noRestart)
	fmt.Println("\nsign-in is configured. Open the dashboard and sign in once, with your own account,")
	fmt.Println("before you tell anyone else it is on.")
	fmt.Println("if the provider answers `redirect_uri mismatch`, the URI registered there is not")
	fmt.Printf("character-for-character %s\n", callback)
	fmt.Println("if you cannot get in at all, this console can open the dashboard again:")
	fmt.Println("  tacit secure --off")
	if cfg.OwnerSecret != "" {
		fmt.Println("\nthe owner secret is kept and is now inert: while sign-in is on, the provider is")
		fmt.Println("the only way in. `--off` above is what makes the owner link work again.")
	}
	return code
}

// secureOff clears the four settings and leaves everything else — the session
// secret included, so a later `tacit secure` does not sign out the sessions it
// is about to start issuing.
func secureOff(cfg registryconfig.Config, envPath string, p *prompter, yes, dryRun, noRestart bool) int {
	if !cfg.OIDCEnabled() {
		fmt.Println("\nsign-in is already off; there is nothing to remove")
		return 0
	}
	updates := map[string]string{}
	for _, k := range oidcKeys {
		updates[k] = "" // an empty value drops the line
	}
	// The mode follows the gate back out. Left at "oidc" with no provider, the
	// registry would claim a way in it no longer has, and the owner secret this
	// command deliberately keeps would stay inert — which is the opposite of
	// what --off is for. With a secret it is one member's again; with none
	// there is no gate at all, and the line goes.
	//
	// A registry that had been opened to a team comes back as one member's:
	// nothing recorded that it was a team, and reopening it is one button on
	// the dashboard the owner can now reach.
	updates["TACIT_AUTH_MODE"] = registryconfig.AuthOwner
	if cfg.OwnerSecret == "" {
		updates["TACIT_AUTH_MODE"] = ""
	}
	fmt.Printf("\nto remove from %s:\n", envPath)
	for _, k := range oidcKeys {
		fmt.Printf("  %s\n", k)
	}
	fmt.Println("\nthe dashboard will be open to everyone that can reach it.")
	if dryRun {
		fmt.Println("--dry-run: nothing written")
		return 0
	}
	if !yes && !p.confirm("remove them and restart?") {
		fmt.Println("nothing changed")
		return 0
	}
	if err := registryconfig.PatchEnv(envPath, updates); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", envPath, err)
		return 1
	}
	fmt.Printf("wrote %s (TACIT_SESSION_SECRET kept, so sessions survive turning sign-in back on)\n", envPath)
	return finishRestart(cfg, envPath, noRestart)
}

// finishOn brings the new settings into force, and leaves the registry running
// the way an organization's registry has to run.
//
// This is where the service is installed, rather than at `tacit init`. Sign-in
// is the moment a registry stops being one person's — somebody else has to be
// able to reach it, at an hour when nobody is watching the terminal it was
// started from — so it is the moment a unit that survives a reboot is worth
// having. Before that, guessing meant installing a service for a registry
// nobody had decided to keep.
func finishOn(cfg registryconfig.Config, envPath string, noRestart bool) int {
	// The operator's own instruction first, then the question of whether this
	// command may touch the service at all.
	if noRestart {
		fmt.Println("\nnot started (--no-restart): the registry reads sign-in settings at startup only,")
		fmt.Println("and no service was installed. Restart it yourself to bring them into force.")
		return 0
	}
	if !serviceReads(envPath) {
		return reportNotThisMachinesRegistry(envPath)
	}
	local := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	switch serviceActionFor(serviceIsActive(), registryRunning(local), canManageService()) {
	case serviceRestart:
		restarted, err := restartRegistryService()
		return reportRestart(cfg, restarted, err)
	case serviceInstall:
		fmt.Println("\nsign-in belongs to a registry that is up when somebody signs in, so this")
		fmt.Println("installs a service for it:")
		if !installRegistryService() {
			fmt.Fprintln(os.Stderr, "the service could not be installed — start the registry yourself to apply the settings")
			return 1
		}
		if pollHealth(cfg.Port, 15*time.Second) {
			fmt.Println("the registry is running as a service, and healthy")
			return 0
		}
		fmt.Fprintln(os.Stderr, "the service was installed but the registry did not answer /v1/health after 15s — check the service log")
		return 1
	case serviceBlocked:
		// The one case this command must not paper over: something is serving
		// this registry and it is not a service. Hunting for a process to kill
		// is not its business; naming what has to happen is.
		fmt.Printf("\na registry is answering on %s and this command did not start it.\n", local)
		fmt.Println("sign-in is read at startup, so it has to be restarted before this takes effect.")
		fmt.Println("stop it (Ctrl-C in that terminal), then either:")
		fmt.Printf("  %s init --service auto     keep it running in the background\n", selfCommand())
		fmt.Printf("  %s serve                   start it here again\n", selfCommand())
		return 0
	default:
		fmt.Println("\nno service manager on this machine — restart the registry yourself to apply it")
		return 0
	}
}

// serviceAction is what turning sign-in on has to do about whatever is serving
// this registry.
type serviceAction int

const (
	serviceRestart serviceAction = iota // a unit is running it: restart, so it reads the new settings
	serviceInstall                      // nothing is: install one and start it
	serviceBlocked                      // something else is, and this command cannot stop that
	serviceManual                       // nothing here can manage a service (a container, another OS)
)

func serviceActionFor(managed, answering, canManage bool) serviceAction {
	switch {
	case !canManage:
		return serviceManual
	case managed:
		return serviceRestart
	case answering:
		return serviceBlocked
	default:
		return serviceInstall
	}
}

// canManageService reports whether this machine has a service manager this
// command knows how to drive.
func canManageService() bool { return servicectl.CanManage() }

// serviceIsActive reports a managed registry that is running now.
func serviceIsActive() bool { return servicectl.Active() }

// installRegistryService writes the unit and starts it, with `tacit init`'s own
// installers so there is one description of this registry's service.
func installRegistryService() bool {
	switch {
	case runtime.GOOS == "linux" && haveExec("systemctl"):
		return setupSystemd()
	case runtime.GOOS == "darwin" && haveExec("launchctl"):
		return setupLaunchd()
	}
	return false
}

// reportRestart says what became of the restart, and whether the registry came
// back. Shared by turning sign-in on and off.
func reportRestart(cfg registryconfig.Config, restarted bool, err error) int {
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "restart the registry: %v\n", err)
		return 1
	case !restarted:
		fmt.Println("\nno registry service on this machine — restart `tacit serve` yourself to apply it")
		return 0
	case pollHealth(cfg.Port, 15*time.Second):
		fmt.Println("registry restarted and healthy")
		return 0
	default:
		fmt.Fprintln(os.Stderr, "registry restarted but did not answer /v1/health after 15s — check the service log")
		return 1
	}
}

// serviceReads reports whether the settings this command wrote are the ones the
// service would read. The unit's EnvironmentFile names this machine's registry
// (init.go, systemdUnit), so a run pointed somewhere else by TACIT_REGISTRY_ENV
// must not install or restart it: that would start, stop or reconfigure a
// different registry from the one being configured, and say it had done the
// right thing.
func serviceReads(envPath string) bool { return servicectl.ReadsEnv(envPath) }

func reportNotThisMachinesRegistry(envPath string) int {
	fmt.Printf("\nthese settings are %s, and the registry service reads %s.\n",
		envPath, registryconfig.DefaultRegistryEnvPath())
	fmt.Println("nothing was installed or restarted — restart that registry yourself to apply them.")
	return 0
}

// finishRestart applies the change the registry only reads at startup.
func finishRestart(cfg registryconfig.Config, envPath string, noRestart bool) int {
	if noRestart {
		fmt.Println("\nnot restarted (--no-restart): the registry reads sign-in settings at startup only")
		return 0
	}
	if !serviceReads(envPath) {
		return reportNotThisMachinesRegistry(envPath)
	}
	restarted, err := restartRegistryService()
	return reportRestart(cfg, restarted, err)
}

// restartRegistryService restarts the managed registry, reporting whether there
// was one to restart.
func restartRegistryService() (bool, error) { return servicectl.Restart() }

// --- what the registry exposes ----------------------------------------------

// registrySecurityChecks reports how this registry is exposed and whether its
// sign-in is coherent. Shared with `tacit doctor`, which runs it read-only on
// every registry host: the checks that matter most are the ones an operator
// never thinks to run.
//
// The partial-OIDC case is a FAIL rather than a warning because it is the one
// state that lies: the operator has set three of four values, believes sign-in
// is on, and the dashboard is open with nothing anywhere saying so.
func registrySecurityChecks(c *checker, cfg registryconfig.Config, live string) {
	set := 0
	for _, v := range []string{cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.OIDCRedirectURI} {
		if strings.TrimSpace(v) != "" {
			set++
		}
	}
	switch {
	case set == 4:
		c.ok("sign-in enabled (%s)", cfg.OIDCIssuer)
	case set > 0:
		c.fail("sign-in is OFF: %d of the 4 TACIT_OIDC_* settings are set, and the registry needs all four — `tacit secure` completes them", set)
	case loopbackOnly(cfg.Host):
		c.ok("no sign-in, and the dashboard is bound to %s only", cfg.Host)
	default:
		c.warn("the dashboard is open to everyone that can reach this machine on :%d — `tacit secure` turns sign-in on", cfg.Port)
	}
	if set != 4 {
		return
	}

	if !strings.HasSuffix(cfg.OIDCRedirectURI, "/auth/callback") {
		c.fail("TACIT_OIDC_REDIRECT_URI must end in /auth/callback (it is %q)", cfg.OIDCRedirectURI)
	}
	if cfg.SessionSecret == "" {
		c.warn("TACIT_SESSION_SECRET is unset — the signing key is new at each start, so every restart signs every member out")
	}
	if strings.HasPrefix(cfg.OIDCRedirectURI, "https://") && !cfg.CookieSecure {
		c.warn("TACIT_COOKIE_SECURE is off while the callback is https — set it to 1 so the session cookie never travels in the clear")
	}
	if len(cfg.AdminEmails) == 0 {
		c.warn("TACIT_ADMIN_EMAILS is empty — every member who signs in can change settings and curate the playbook")
	}
	if base := cfg.ExternalBase(); base != "" && !strings.EqualFold(cfg.OIDCRedirectURI, base+"/auth/callback") {
		c.warn("the callback %s is not on the external URL %s — your provider returns members only to the URI registered with it", cfg.OIDCRedirectURI, base)
	}

	// Which callback the provider actually sees. A published registry's public
	// address SUPERSEDES the configured one (Server.OIDCRedirectURI), so the
	// value in registry.env is not the URI being sent — it is the fallback for
	// when that address goes away. Both belong at the provider, and saying only
	// "register the proxy address too" hides which of them is live today.
	switch {
	case live != "" && !strings.EqualFold(live, cfg.OIDCRedirectURI):
		c.info("sign-in redirects to %s — this registry's public address supersedes the configured callback, so that is the URI your provider must have", live)
		c.info("%s is the fallback for when that address goes away; register it too and the switch works both ways", cfg.OIDCRedirectURI)
	case cfg.GlobalAccess && live == "":
		c.info("Global Access is on and the registry is not answering here, so its public address is unknown — while it is published, that address is the callback, not %s (the Settings page shows it)", cfg.OIDCRedirectURI)
	}
}

// liveCallback asks the running registry what address it answers on and turns
// it into the callback it will actually send. "" when there is no registry to
// ask — the configuration alone cannot know, because the public address is
// allocated at runtime by the proxy.
func liveCallback(cfg registryconfig.Config) string {
	hc := &http.Client{Timeout: 3 * time.Second}
	health, err := fetchHealth(hc, fmt.Sprintf("http://127.0.0.1:%d", cfg.Port))
	if err != nil {
		return ""
	}
	return callbackFor(health.ExternalURL, cfg.BasePath)
}

// callbackFor is an external base URL's sign-in callback. The base path is
// appended only when the base does not already carry it: a configured external
// URL includes the sub-path, while a proxy-allocated address is an origin.
func callbackFor(base, basePath string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return ""
	}
	if basePath != "" && !strings.HasSuffix(base, basePath) {
		base += basePath
	}
	return base + "/auth/callback"
}

// loopbackOnly reports whether the bind host reaches this machine alone. An
// empty host or a wildcard is every interface — the case that matters.
func loopbackOnly(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// --- derivation and validation ----------------------------------------------

// defaultCallback is the callback URL this registry's own configuration
// implies. The external URL already carries the base path when one is
// configured, so this is exactly the address members reach plus the route.
func defaultCallback(cfg registryconfig.Config) string { return cfg.CallbackURL() }

// checkCallback returns why a callback cannot work, or "". The http rule is the
// providers' rule, not ours: they refuse a plaintext redirect URI outright,
// with localhost the usual exception.
// checkCallback refuses a redirect URI the provider will not accept — shared
// with the Settings page for the same reason as the issuer check.
func checkCallback(raw string) string { return oidc.CheckCallback(raw) }

// fetchDiscovery proves an issuer before the operator is committed to it: the
// document must exist, parse, and name the three endpoints the flow uses.
// fetchDiscovery proves the issuer. The rule is shared with the Settings page's
// sign-in form, so both refuse the same URLs for the same reasons.
func fetchDiscovery(issuer string) (map[string]any, error) { return oidc.CheckIssuer(issuer) }

// --- console ----------------------------------------------------------------

type prompter struct {
	sc          *bufio.Scanner
	interactive bool
}

// ask reads one line, returning def when the operator just presses enter.
//
// Every prompt opens with a blank line. What the command has to REPORT and what
// it needs the operator to ANSWER are different kinds of line, and a question
// butted against the end of a findings list reads as one more finding.
func (p *prompter) ask(question, def string) string {
	fmt.Println()
	if def != "" {
		fmt.Printf("%s [%s]: ", question, def)
	} else {
		fmt.Printf("%s: ", question)
	}
	if !p.sc.Scan() {
		return def
	}
	if v := strings.TrimSpace(p.sc.Text()); v != "" {
		return v
	}
	return def
}

// askSecret reads one line without echoing it, so the answer does not stay in
// the terminal scrollback for the next person at the keyboard to read. A pipe
// has no echo to switch off, so it falls back to the same scanner ask uses.
func (p *prompter) askSecret(question string) string {
	fmt.Println()
	fmt.Printf("%s: ", question)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		if !p.sc.Scan() {
			return ""
		}
		return strings.TrimSpace(p.sc.Text())
	}
	v, err := term.ReadPassword(fd)
	// The unechoed enter leaves the cursor at the end of the question, so
	// print the line break the terminal did not.
	fmt.Println()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(v))
}

// confirmDefaultYes is confirm with the other default: enter means yes.
//
// Used where declining is the surprising answer and the question is asked only
// to keep the member's consent, not because we are undecided.
func (p *prompter) confirmDefaultYes(question string) bool {
	if !p.interactive {
		return true
	}
	switch strings.ToLower(p.ask(question+" [Y/n]", "")) {
	case "n", "no":
		return false
	}
	return true
}

func (p *prompter) confirm(question string) bool {
	if !p.interactive {
		return false
	}
	switch strings.ToLower(p.ask(question+" [y/N]", "")) {
	case "y", "yes":
		return true
	}
	return false
}

// orPrompt takes the flag when given, asks when there is a terminal, and
// otherwise leaves the value empty for the caller to reject — a headless run
// must fail loudly rather than block on a prompt nobody can answer.
func orPrompt(p *prompter, flagVal, question, def string, yes bool) string {
	if flagVal != "" {
		return flagVal
	}
	if p.interactive && !yes {
		return p.ask(question, def)
	}
	return def
}

func orElseStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// stdinIsTerminal reports whether there is a person to prompt.
//
// A terminal, not a character device. `/dev/null` is a character device, and
// the mode test this used to make called it a person: every script, unit file
// and container that redirects stdin from /dev/null — which is most of them —
// looked interactive. Harmless where the answer only decided whether to ask a
// question nobody would answer; not harmless once it decides whether `tacit
// init` takes the terminal and serves, because a setup step in a script then
// never returns.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// printUpdates shows the pending change with secrets masked: an operator should
// be able to read this back to themselves before it is written, and a terminal
// scrollback is not a safe place for a client secret.
func printUpdates(updates map[string]string) {
	for _, k := range registryconfig.SortedKeys(updates) {
		v := updates[k]
		if strings.Contains(k, "SECRET") {
			v = maskSecret(v)
		}
		fmt.Printf("  %s=%s\n", k, v)
	}
}

func maskSecret(v string) string {
	if len(v) <= 6 {
		return "……"
	}
	return v[:4] + "…… (" + fmt.Sprint(len(v)) + " chars)"
}
