// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit init — the operator bootstrap (docs/distribution/install-plan.md):
// turns an installed binary into a running, configured registry. Idempotent
// by construction: every step fills only what is missing — an existing
// registry.env value, techniques directory, downloaded artifact, or service unit
// is never overwritten — so re-running after a partial failure completes the
// remainder and re-running on a healthy install changes nothing.

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit"
	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/ingress"
	"github.com/opentacit/tacit/internal/merge"
	"github.com/opentacit/tacit/internal/onnxassets"
	"github.com/opentacit/tacit/internal/product"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/web"
	"github.com/opentacit/tacit/internal/servicectl"
)

// The semantic-retrieval artifacts (docs/design/embedder-onnx.md) live in
// internal/onnxassets — one pinned table shared with `tacit onnx-fetch`
// (the Docker image build), so the two install paths can never drift.

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	embeddings := fs.String("embeddings", "auto", "on|off|auto: download the semantic-retrieval model (~90MB; auto = on where supported)")
	// None by default, and not for want of a detector.
	//
	// A registry with one member is a thing its owner starts when they want it:
	// this command runs it here, in front of them, and stopping it is Ctrl-C.
	// A registry with an identity provider is an organization's — it has to be
	// up when somebody else signs in, and to come back after a reboot nobody
	// was watching. That is a different registry, and the moment it becomes one
	// is `tacit secure`, which installs the service then. Guessing here installed a
	// unit for a registry nobody had decided to keep.
	//
	// The flag still takes an answer, because a server operator standing one up
	// by hand knows theirs is the second kind from the start.
	verbose := fs.Bool("verbose", false, "print every step as it happens; without it, init says what it did once, at the end")
	service := fs.String("service", "none", "systemd|launchd|none|auto: install a service manager to keep the registry up (default none: `tacit secure` installs one when you turn on sign-in)")
	port := fs.Int("port", 0, "registry port (default 8080)")
	// Both default to auto rather than on: an existing registry that already has
	// an identity provider, or an operator running this again to change a port,
	// must not acquire a second gate or a public address by re-running a setup
	// command.
	// Auto: a registry with neither a provider nor a mode gets an owner, and
	// one that has either is left exactly as it is. It was off by default while
	// `tacit personal` existed, so that command could tell which registries it
	// was allowed to adopt. There is one command now, and a fresh registry that
	// nobody can sign into is a registry nobody can administer.
	owner := fs.String("owner", "auto", "on|off|auto: sign this machine's operator in as the registry's owner, with no identity provider (auto: on for a registry that has neither)")
	globalAccess := fs.String("global-access", "auto", "on|off|auto: give this registry an address on the internet through the shared ingress (auto: on for a registry that has a sign-in and no address of its own). Contributing techniques to the public commons is a separate yes, in Settings.")
	startOver := fs.Bool("start-over", false, "set up a NEW registry where a merged one was: its data is moved aside and this machine keeps its settings")
	fromRepo := fs.String("from-repo", "auto", "read this repository's written conventions (CLAUDE.md, AGENTS.md, skills, rules) into the review queue as drafts; auto = the working directory when it is a checkout, off = skip, or a path")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}
	// Before anything prints: the early steps run long before the flag block
	// below, and a --verbose that arrives after them silences the half the
	// reader asked for.
	initVerbose = *verbose
	printInitBanner()

	envPath := registryconfig.RegistryEnvPath()
	if envPath == "" {
		fmt.Fprintln(os.Stderr, "no home directory; set TACIT_REGISTRY_ENV")
		return 1
	}
	vals := auditorconfig.ReadEnvFile(envPath)
	dataHome := registryconfig.DataHome()

	// A merged registry is finished, and this is the one command that can end
	// that (internal/merge/retire.go). Re-running setup over it must not
	// quietly bring it back: the files here are the instance that contributed
	// its playbook to an organization, and a registry started from them would
	// be that instance, serving work that has already moved.
	if dest, retired := merge.RetiredInto(filepath.Dir(envPath)); retired {
		if !*startOver {
			fmt.Fprintln(os.Stderr, "this registry was merged and is finished; it does not start again")
			if dest != "" {
				fmt.Fprintf(os.Stderr, "your playbook is at %s, and this machine's tools already point at it\n", dest)
			}
			fmt.Fprintf(os.Stderr, "to set up a NEW registry here:  %s init --start-over\n", selfCommand())
			return 1
		}
		// Now, while nothing is serving and no file is open. What follows is a
		// new instance: new keys, a new owner, and an address of its own, with
		// the old registry's evidence kept beside it under `.merged-…`.
		moved, err := merge.SweepMerged(filepath.Dir(envPath), vals["TACIT_DATA"],
			vals["TACIT_TECHNIQUES_DIR"], dataHome)
		if err != nil {
			fmt.Fprintf(os.Stderr, "the merged registry's files could not be moved aside: %v\n", err)
			return 1
		}
		for _, p := range moved {
			fmt.Printf("the merged registry is kept at %s\n", p)
		}
		// Minted again below, because a new instance that reuses the old one's
		// credentials is the old one to everything holding them.
		for _, k := range []string{"TACIT_API_KEY", "TACIT_OWNER_SECRET", "TACIT_AUTH_MODE"} {
			delete(vals, k)
		}
	} else if *startOver {
		fmt.Println("--start-over: this registry has not been merged, so there is nothing to move aside")
	}

	setIfMissing(vals, "TACIT_DATA", filepath.Join(dataHome, "data"))
	setIfMissing(vals, "TACIT_TECHNIQUES_DIR", filepath.Join(dataHome, "techniques"))
	newKey := vals["TACIT_API_KEY"] == ""
	if newKey {
		vals["TACIT_API_KEY"] = web.NewAPIKey()
	}
	if *port != 0 {
		vals["TACIT_PORT"] = strconv.Itoa(*port)
	}

	if err := os.MkdirAll(vals["TACIT_DATA"], 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", vals["TACIT_DATA"], err)
		return 1
	}
	if n, err := tacit.MaterializeSeedTechniques(vals["TACIT_TECHNIQUES_DIR"]); err != nil {
		fmt.Fprintf(os.Stderr, "seed techniques: %v\n", err)
		return 1
	} else if n > 0 {
		step("seeded %s with %d starter techniques\n", vals["TACIT_TECHNIQUES_DIR"], n)
	}

	switch *embeddings {
	case "off":
		// explicit opt-out; whatever is configured stays
	case "on", "auto":
		// auto respects an operator's existing embedder choice; on overrides it.
		if *embeddings == "on" || vals["TACIT_EMBED_MODEL"] == "" {
			if err := setupEmbeddings(vals, dataHome); err != nil {
				if *embeddings == "on" {
					fmt.Fprintf(os.Stderr, "embeddings: %v\n", err)
					return 1
				}
				fmt.Printf("semantic retrieval not configured (%v); the registry keeps the built-in lexical embedder\n", err)
			}
		}
	default:
		fmt.Fprintf(os.Stderr, "--embeddings must be on, off, or auto (got %q)\n", *embeddings)
		return 2
	}

	cfgDir := filepath.Dir(envPath)
	ownerOn, code := setupOwner(vals, *owner)
	if code != 0 {
		return code
	}
	if code := setupGlobalAccess(vals, *globalAccess, ownerOn); code != 0 {
		return code
	}

	if err := writeSortedEnvFile(envPath, "# written by `tacit init`; environment variables override these", vals); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", envPath, err)
		return 1
	}
	step("wrote %s\n", envPath)

	// The org's own conventions, filed as drafts, before anything starts
	// serving (originate.go). Last of the setup steps because it wants the data
	// directory to exist and the settings to be written, and because its report
	// is the one an operator should read on their way to the dashboard.
	// Carried to the summary: a count of drafts is worth one line there, where
	// the paragraph explaining them was worth none.
	drafted := 0
	if from := repoToOriginate(*fromRepo); from != "" {
		filed, already, err := originateFromRepo(vals["TACIT_DATA"], from, embedSettings{
			model:    orElseStr(vals["TACIT_EMBED_MODEL"], registryconfig.DefaultEmbedModel),
			dim:      embedDimOf(vals),
			onnxLib:  vals["TACIT_ONNX_LIB"],
			modelDir: vals["TACIT_ONNX_MODEL_DIR"],
		})
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "could not read this repository's conventions: %v\n", err)
		case filed > 0:
			drafted = filed
			step("\nread %s and filed %d draft technique(s) from its written conventions.\n", from, filed)
			step("nothing serves until you promote them — that queue is the Review page.\n")
		case already > 0:
			drafted = already
			step("\nthis repository's conventions are already in the review queue (%d).\n", already)
		}
	}

	svc := *service
	// Nobody asked for the background service, so almost nobody got one. The
	// flag defaulted to none and pointed at `tacit secure`, which made a
	// registry that survives a closed terminal a side effect of configuring an
	// identity provider — two needs that have nothing to do with each other
	// (docs/distribution/first-adoption-plan.md). Ask instead, once, and say
	// what the answer installs. A script that named the flag is obeyed; a
	// script that did not is left alone, because it has no one to ask.
	if svc == "none" && !flagWasSet(fs, "service") && stdinIsTerminal() {
		if askAboutService() {
			svc = "auto"
		}
	}
	if svc == "auto" {
		switch {
		case runtime.GOOS == "linux" && haveExec("systemctl"):
			svc = "systemd"
		case runtime.GOOS == "darwin":
			svc = "launchd"
		default:
			svc = "none"
		}
	}
	started := false
	switch svc {
	case "systemd":
		started = setupSystemd()
	case "launchd":
		started = setupLaunchd()
	case "none":
	default:
		fmt.Fprintf(os.Stderr, "--service must be systemd, launchd, none, or auto (got %q)\n", svc)
		return 2
	}

	// Whether this run finishes by serving the registry itself.
	//
	// A service manager makes it unnecessary. What is left is the case the
	// single-member tier was written for: somebody at a keyboard on a machine
	// with no unit manager, who asked for a registry and should get a running
	// one rather than a second command to type. Off where nobody is watching,
	// because a setup step in a script must return.
	foreground := !started && stdinIsTerminal()

	portNum := 8080
	if v, err := strconv.Atoi(vals["TACIT_PORT"]); err == nil && v > 0 {
		portNum = v
	}
	if started {
		if pollHealth(portNum, 15*time.Second) {
			fmt.Println("registry healthy")
		} else {
			fmt.Fprintf(os.Stderr, "registry did not answer /v1/health within 15s — check the service log\n")
			return 1
		}
	} else if !foreground {
		fmt.Printf("\nnothing is running this registry yet:\n")
		fmt.Printf("  %s serve                     run it here\n", selfCommand())
		fmt.Printf("  %s init --service auto       keep it running in the background\n", selfCommand())
	}
	if started {
		fmt.Printf("stop it with:  %s\n", serviceStopCommand())
	}

	// The instance key is this registry's identity to the shared proxy, made
	// here so it exists before anyone needs it and so it is part of what a
	// backup of the config directory captures. Nothing uses it until an
	// operator turns on "Access through the OpenTacit proxy"; generating it now
	// means that switch has nothing to set up when they do.
	if _, err := ingress.LoadOrCreateKey(cfgDir); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create this registry's instance key: %v\n", err)
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	// What it did, once. Everything here is either something to open or
	// something to find later; the rest of what this command knows about
	// itself is behind --verbose, or in `tacit doctor`.
	fmt.Printf("\nregistry:  http://%s:%d\n", host, portNum)
	fmt.Printf("settings:  %s  (holds the API key — treat it as a secret)\n", envPath)
	if drafted > 0 {
		fmt.Printf("drafts:    %d read from this repository, waiting on Review\n", drafted)
	}
	if publicAddressNoted {
		fmt.Printf("address:   public through %s — its proxy terminates TLS and can read what\n",
			registryconfig.DefaultIngress)
		fmt.Printf("           passes through. Off:  %s init --global-access off\n", selfCommand())
	}
	if !reportModelKeyBrief() {
		fmt.Printf("model:     none — suggestions are ranked but not fit-checked. `%s ask` still works.\n",
			selfCommand())
	}
	step("identity:  %s  (in %s — this registry's name on the shared proxy depends on it)\n",
		instanceFingerprint(cfgDir), ingress.KeyPath(cfgDir))
	if ownerOn && !foreground {
		publishing := vals["TACIT_GLOBAL_ACCESS"] == "1"
		base, where := ownerBase(registryconfig.Load(), portNum, publishing, started)
		fmt.Printf("\nsign in as the owner — %s, and the link works for 15 minutes:\n  %s\n",
			where, web.OwnerLink(base, vals["TACIT_OWNER_SECRET"]))
		fmt.Printf("(a fresh one any time, from this machine:  %s dashboard)\n", selfCommand())
		// The public address, when there is one coming, follows the link rather
		// than delaying it. Nobody should watch a running registry for thirty
		// seconds with no way to open it.
		if publishing && started {
			if url := awaitPublicURLAt(fmt.Sprintf("http://127.0.0.1:%d", portNum), 30*time.Second); url != "" {
				fmt.Printf("\nthe ingress allocated a public address — this link opens from anywhere:\n  %s\n",
					web.OwnerLink(url, vals["TACIT_OWNER_SECRET"]))
			} else {
				fmt.Printf("\n%s has not allocated a public address yet; the link above still works.\n",
					orElseStr(vals["TACIT_PUBLISH_INGRESS"], registryconfig.DefaultIngress))
				fmt.Printf("`%s dashboard` prints a public one once the tunnel is up.\n", selfCommand())
			}
		}
	}
	reportNextSteps(drafted, ownerOn && !foreground)
	if foreground {
		// The blank line that closes this report comes from the announcement
		// beside the running server (announceOwnerLink), which prints after
		// everything here.
		return serveHere(portNum)
	}
	fmt.Println()
	return 0
}

// reportNextSteps closes `tacit init` with what to do, ordered by who gets
// something out of it.
//
// The order is the point. This command used to end by offering `tacit invite`
// beside the registry address, which asks a person who has not yet had one
// useful moment to go and recruit colleagues. Worse, it never mentioned
// `tacit connect`, so the one step that makes the next coding session better —
// wiring this machine's harnesses — was left for the reader to discover in the
// command list. A registry nothing is wired to is a dashboard, not a playbook.
//
// So: wire yourself, then read what your own repository already knew, then ask
// it something. Colleagues come after, under a heading that says they are a
// later choice rather than an outstanding chore. Nothing here is a new
// capability; it is the same three commands in the order that pays.
func reportNextSteps(drafted int, linkPrinted bool) {
	fmt.Printf("\nnext, for you:\n")
	n := 0
	next := func(format string, args ...any) {
		n++
		fmt.Printf("  %d. "+format, append([]any{n}, args...)...)
	}
	next("%s connect\n     wire this machine's coding tools, so the playbook reaches your next session\n",
		selfCommand())
	// Only offered when this repository actually wrote some. An empty review
	// queue named as a next step is a chore that leads to an empty page.
	if drafted > 0 {
		where := "open the dashboard"
		if linkPrinted {
			where = "follow the link above"
		}
		next("%s — Review holds the %s read from this repository.\n"+
			"     Promoting one puts it in front of you in your own sessions\n",
			where, plural(drafted, "draft"))
	}
	next("%s ask \"<whatever you are working on>\"\n"+
		"     the playbook's answer for a real task, with the evidence behind it\n",
		selfCommand())
	fmt.Printf("\nwhen you want colleagues on it:\n")
	fmt.Printf("  %s invite\n     a join link they can run — their outcomes start ranking what you both see\n",
		selfCommand())
}

// serveHere runs the registry this command has just configured, in this
// terminal.
//
// The sign-in link is printed from beside the server rather than before it:
// there is nothing to sign into until the listener is up, and where Global
// Access is on there is no address to name until the ingress has answered.
// That is the whole point of the tier and it cannot be known any earlier.
func serveHere(port int) int {
	cfg := registryconfig.Load()
	if !portFree(cfg.Host, port) {
		if registryRunning(fmt.Sprintf("http://127.0.0.1:%d", port)) {
			fmt.Printf("\na registry is already answering on port %d — that is this one, already being served.\n", port)
			fmt.Printf("for a sign-in link to it:  %s dashboard\n", selfCommand())
			return 0
		}
		if !portFreeWithin(cfg.Host, port, 5*time.Second) {
			fmt.Fprintf(os.Stderr, "\nport %d is in use by something that is not a registry.\n", port)
			fmt.Fprintf(os.Stderr, "move this registry once:  %s init --port <free port>\n", selfCommand())
			return 1
		}
	}
	fmt.Printf("\nserving the registry here — Ctrl-C stops it.\n")
	// This process has just said all of it in the report above, so the
	// registry's own startup log would only repeat it in a service log's voice
	// (main.go, startupQuiet). Trouble still prints.
	startupQuiet = true
	// Whether this registry HAS an owner, not whether this run gave it one.
	// The second and every later run is the one where somebody needs the link:
	// the first prints it beside a registry they are already looking at, and
	// the ones after that are how they get back in.
	if cfg.OwnerEnabled() {
		go announceOwnerLink(cfg, port)
	}
	return cmdServe([]string{"--port", strconv.Itoa(port)})
}

// ownerBase is the address to sign in at, and how to describe it.
//
// The public one when this registry has been given one, because that is the
// whole point of turning the ingress on: a link to 127.0.0.1 is no use from the
// phone in somebody's hand or the laptop they actually work on. The address is
// allocated by the ingress at the moment the tunnel comes up, so it cannot be
// known before the registry is running and has dialled — hence the wait.
func ownerBase(cfg registryconfig.Config, port int, published, restarted bool) (base, where string) {
	return ownerBaseAt(cfg, port, fmt.Sprintf("http://127.0.0.1:%d", port), published, restarted)
}

// ownerBaseAt is the same against a given probe address, so a test can stand in
// for a registry that is up and not publishing.
//
// Two addresses, on purpose. `local` is what this function TALKS to — loopback,
// because it is asking the process on this machine whether the tunnel came up.
// The base it returns is what a person OPENS, and loopback is the last choice
// there (ownerlink.go).
func ownerBaseAt(cfg registryconfig.Config, port int, local string, published, restarted bool) (base, where string) {
	// Not publishing is not the same as loopback-only. The token is signed and
	// carries no host (ownerlink.go), so an operator who set this up over SSH
	// gets the address their own network can follow rather than one only the
	// machine they are not sitting at can open.
	//
	// And this never waits. It used to hold the sign-in link back for up to
	// thirty seconds while the tunnel dialled, which is the operator staring at
	// a registry that is up and serving with nothing telling them how to get in
	// — the exact wall the public address exists to remove. The caller prints
	// the public link after this one, when it lands.
	offline, offlineWhere := ownerLinkBase(cfg, port, "")
	if !published {
		return offline, offlineWhere
	}
	// Only wait when this run actually (re)started the registry. Otherwise
	// there is nothing in flight to wait for, and polling a process that
	// cannot answer spends thirty seconds writing a health check per second
	// into the log the operator is watching.
	if !restarted {
		if running := registryRunning(local); running {
			fmt.Printf("\na registry is already running at %s and it is not publishing — it started\n", local)
			fmt.Printf("before this change, so it has not read it. Restart it, then:  %s dashboard\n", selfCommand())
		} else {
			fmt.Printf("\nstart the registry and it will dial the ingress:  %s serve\n", selfCommand())
			fmt.Printf("then, for the public link:  %s dashboard\n", selfCommand())
		}
		return offline, offlineWhere + " until then"
	}
	return offline, offlineWhere + " for now"
}

// selfCommand names this binary the way the reader can actually run it.
//
// "tacit" when that is what PATH resolves to, and the full path when it is not.
// Both cases happen on a first run: install.sh puts the binary in
// ~/.local/bin, which is often not on PATH until the next shell, and anyone
// testing a build runs it from wherever they built it. Printing "tacit
// dashboard" to somebody whose PATH holds a different tacit — or none — is
// advice that fails, and it fails in the one place where a member has nothing
// else to go on.
func selfCommand() string {
	self, err := os.Executable()
	if err != nil {
		return "tacit"
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	onPath, err := exec.LookPath("tacit")
	if err != nil {
		return self
	}
	if resolved, err := filepath.EvalSymlinks(onPath); err == nil {
		onPath = resolved
	}
	a, errA := os.Stat(self)
	b, errB := os.Stat(onPath)
	if errA == nil && errB == nil && os.SameFile(a, b) {
		return "tacit"
	}
	return self
}

// registryRunning reports whether anything answers on this port, so the advice
// can name the actual next step rather than offering both.
func registryRunning(base string) bool {
	_, err := fetchHealth(&http.Client{Timeout: 2 * time.Second}, base)
	return err == nil
}

// awaitPublicURLAt polls a registry for the address the ingress gave it. Taking
// a base rather than a port lets a test stand in for one that has not finished
// dialling.
//
// It returns the address, or the reason there will not be one. Waiting out the
// whole budget and then saying "not yet" is wrong when the registry already
// knows it was refused — an enrolment ceiling does not clear in forty-five
// seconds, and the operator watching the poll is owed the sentence the ingress
// actually sent.
func awaitPublicURLAt(base string, budget time.Duration) string {
	url, _ := awaitPublicURLReason(base, budget)
	return url
}

func awaitPublicURLReason(base string, budget time.Duration) (url, refusal string) {
	deadline := time.Now().Add(budget)
	hc := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		health, err := fetchHealth(hc, base)
		// A refusal is terminal for this run: stop polling and carry it back.
		if err == nil && health.TunnelError != "" {
			return "", health.TunnelError
		}
		// "global" and "staged" both mean the tunnel is up and the address is
		// live; they differ only in whether the technique feed is being served
		// (web/public.go). Waiting for "global" alone was right when publishing
		// implied both, and wrong the moment `tacit init` started leaving the
		// commons unconfirmed — a new registry got its address, reported
		// "staged", and this polled past it for forty-five seconds and gave up,
		// so the one link a new operator actually wants never printed.
		if err == nil && health.ExternalURL != "" &&
			(health.Access == "global" || health.Access == "staged") {
			return strings.TrimRight(health.ExternalURL, "/"), ""
		}
		// Every poll is a line in the log somebody may be watching. Two
		// seconds is still prompt for a tunnel that takes one or two.
		time.Sleep(2 * time.Second)
	}
	return "", ""
}

// setupOwner puts a registry into single-member mode, or leaves it alone.
//
// auto means "a fresh registry with nobody else's sign-in configured". An
// operator re-running init to change a port keeps whatever they had, and one
// who has already configured an identity provider is not given a second way in
// behind their back — two gates on one dashboard is a question about which one
// is authoritative that nobody wants to answer at three in the morning.
func setupOwner(vals map[string]string, mode string) (bool, int) {
	hasIdP := vals["TACIT_OIDC_ISSUER"] != "" && vals["TACIT_OIDC_CLIENT_ID"] != ""
	switch mode {
	case "off":
		return false, 0
	case "auto":
		if hasIdP || vals["TACIT_AUTH_MODE"] != "" {
			return false, 0
		}
	case "on":
		if hasIdP {
			fmt.Fprintf(os.Stderr, "--owner: this registry already signs members in with an identity provider\n")
			return false, 2
		}
	default:
		fmt.Fprintf(os.Stderr, "--owner must be on, off, or auto (got %q)\n", mode)
		return false, 2
	}
	setIfMissing(vals, "TACIT_OWNER_SECRET", web.NewAPIKey())
	vals["TACIT_AUTH_MODE"] = registryconfig.AuthOwner
	step("sign-in:   owner (this machine's console, no identity provider)\n")
	return true, 0
}

// setupGlobalAccess gives this registry an address on the internet.
//
// On by default, and that is a change. The switch used to be off, and before
// that it asked — a five-line question in the first ninety seconds a person had
// ever spent with the product, about a public address AND a feed of their
// techniques to a public commons, put to somebody whose registry had one member
// and nothing in it.
//
// The reason to default it on is what "off" actually costs. Without an address
// the only sign-in link is loopback, so a registry set up on a server — over
// SSH, which is how servers are set up — cannot be opened at all until somebody
// forwards a port or configures a proxy. That is the first wall a new operator
// hits, and it is in front of the dashboard, which is the product.
//
// What made this hard is that the switch has two halves and config.go is
// explicit that they are not separable: the technique feed is what pays for the
// proxy. It stays not-separable. What moves is WHEN the second half is asked
// for. A registry created ninety seconds ago clears none of the evidence
// floors, so it contributes nothing whatever it answers here, and consent taken
// now buys the commons nothing at the moment it is taken. So this sets the
// switch and NOT the confirmation — the staged state (web/public.go), built for
// the upgrade migration and exactly the right shape for a new registry: the
// address is live, the techniques have not moved, and Settings asks when there
// is something to ask about.
//
// Nothing here can assemble a public address in front of an open dashboard:
// `serve` refuses to dial without a sign-in (publishAllowed), and auto declines
// for the same reason rather than writing a setting that will be refused later.
func setupGlobalAccess(vals map[string]string, mode string, gated bool) int {
	on := false
	switch mode {
	case "off":
		return 0
	case "on":
		on = true
	case "auto":
		switch {
		case vals["TACIT_GLOBAL_ACCESS"] == "1":
			return 0 // already published; nothing to decide
		case vals["TACIT_EXTERNAL_URL"] != "":
			// This operator has an address of their own. Routing them through a
			// shared proxy they did not ask for would be the wrong default and
			// a second answer to a question they have already answered.
			return 0
		case !gated && vals["TACIT_OIDC_ISSUER"] == "":
			return 0
		case !ingressReachable(vals["TACIT_PUBLISH_INGRESS"]):
			// An install with no route to the ingress is a real deployment —
			// an air-gapped network, a lab, a laptop on a plane — and turning
			// the switch on there buys nothing and costs a dial failure in the
			// log every thirty seconds for as long as the registry runs
			// (internal/ingress/client.go caps the backoff there). Decline
			// quietly; Settings has the switch when the network changes.
			fmt.Printf("address:   %s is not reachable from here, so this registry keeps a local\n",
				orElseStr(vals["TACIT_PUBLISH_INGRESS"], registryconfig.DefaultIngress))
			fmt.Printf("           address only. Turn it on later in Settings.\n")
			return 0
		default:
			on = true
		}
	default:
		fmt.Fprintf(os.Stderr, "--global-access must be on, off, or auto (got %q)\n", mode)
		return 2
	}
	if !on {
		return 0
	}
	if !gated && vals["TACIT_OIDC_ISSUER"] == "" {
		// The same refusal `serve` makes, made earlier and in one breath rather
		// than in a log line nobody reads.
		fmt.Fprintf(os.Stderr, "not turning Global Access on: this registry has no sign-in, "+
			"and an open dashboard must not have a public address.\n")
		fmt.Fprintf(os.Stderr, "run again with --owner on, or configure an identity provider with `tacit secure`.\n")
		return 2
	}
	// The switch, and deliberately not the confirmation. Setting both here would
	// be the silent opt-in the Global Access design forbids: a flag is not a person
	// reading what would be published and saying yes.
	vals["TACIT_GLOBAL_ACCESS"] = "1"
	// Consent, so it is never behind --verbose: this is the sentence that tells
	// somebody their registry is about to be reachable from the internet
	// through a proxy that can read the traffic. Held for the summary rather
	// than printed here, where it landed on its own above a gap and read as a
	// warning about something that had already happened.
	publicAddressNoted = true
	return 0
}

// embedDimOf is the vector width this registry is configured for, so drafts
// filed here land in the same space as everything else.
func embedDimOf(vals map[string]string) int {
	if n, err := strconv.Atoi(vals["TACIT_EMBED_DIM"]); err == nil && n > 0 {
		return n
	}
	return registryconfig.DefaultEmbedDim
}

// ingressReachable is one short dial, to decide whether defaulting Global
// Access on would produce an address or a retry loop.
//
// A TCP connect rather than a DNS lookup: a network that resolves everything to
// a captive portal answers the lookup and refuses the connection, and it is the
// connection this needs. Two seconds, because this runs in the middle of a setup
// command somebody is watching, and a slow answer here is worth less than a
// prompt local registry.
func ingressReachable(addr string) bool {
	target := ingressDialAddr(orElseStr(addr, registryconfig.DefaultIngress))
	conn, err := net.DialTimeout("tcp", target, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ingressDialAddr turns the configured ingress into a host:port to dial. The
// setting takes either form — a URL or a bare host, optionally with a port
// (internal/ingress/client.go parses the same two) — so a naive "does it
// contain a colon" test reads the scheme's colon in "https://…" and dials
// nonsense.
func ingressDialAddr(addr string) string {
	scheme, rest, hasScheme := strings.Cut(addr, "://")
	if !hasScheme {
		scheme, rest = "", addr
	}
	rest = strings.TrimSuffix(strings.SplitN(rest, "/", 2)[0], "/")
	if _, _, err := net.SplitHostPort(rest); err == nil {
		return rest
	}
	if scheme == "http" {
		return rest + ":80"
	}
	return rest + ":443"
}

func setIfMissing(vals map[string]string, key, val string) {
	if vals[key] == "" {
		vals[key] = val
	}
}

// setupEmbeddings fetches the pinned runtime library + model files into
// dataHome and points the embed vars at them. Existing files that match
// their digest are kept, so re-runs cost one hash pass, not a download.
func setupEmbeddings(vals map[string]string, dataHome string) error {
	rt, ok := onnxassets.RuntimeLib(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("no onnxruntime build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	libDir := filepath.Join(dataHome, "onnxruntime-"+onnxassets.Version)
	modelDir := filepath.Join(dataHome, "models", onnxassets.ModelName)
	for _, d := range []string{libDir, modelDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	libPath := filepath.Join(libDir, rt.Out)
	if err := onnxassets.Fetch(rt, libPath); err != nil {
		return err
	}
	for _, m := range onnxassets.ModelFiles {
		if err := onnxassets.Fetch(m, filepath.Join(modelDir, m.Out)); err != nil {
			return err
		}
	}
	vals["TACIT_EMBED_MODEL"] = onnxassets.EmbedModelID
	vals["TACIT_EMBED_DIM"] = onnxassets.EmbedDim
	vals["TACIT_ONNX_LIB"] = libPath
	vals["TACIT_ONNX_MODEL_DIR"] = modelDir
	fmt.Printf("configured semantic retrieval (%s, %s)\n", onnxassets.EmbedModelID, rt.Out)
	return nil
}

// writeSortedEnvFile writes one KEY=value line per entry, keys in order, under
// the header the command owns — `tacit init` names registry.env, `tacit connect`
// names agent.env, and each says so in its own words at the top of the file it
// wrote. The rest is the same file both times, so the writing is one function.
//
// header is the comment line, without its "\n". The file is 0600: registry.env
// carries the API key, agent.env carries the member's copy of it.
func writeSortedEnvFile(path, header string, vals map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(header + "\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, vals[k])
	}
	// Atomic: these two files are the whole of a member's and an operator's
	// configuration, and a plain write truncates first. A crash or a full disk
	// between the truncate and the write left an empty registry.env, taking the
	// API key with it.
	return fsx.WriteFileAtomic(path, []byte(b.String()), 0o600)
}

func haveExec(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

const systemdUnit = `# tacit registry as a systemd user service — written by tacit init.
[Unit]
Description=tacit registry service (playbook techniques, evidence, feedback)
After=network.target

[Service]
Type=simple
EnvironmentFile=-%%h/.config/tacit/registry.env
ExecStart=%s serve
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`

// setupSystemd installs and starts the user unit. An existing unit file is
// operator configuration (the original deployment has a hand-tuned one) and
// is left exactly as it is — init only ensures the service is enabled and up.
func setupSystemd() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "tacit-registry.service")
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		bin, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve binary path: %v\n", err)
			return false
		}
		if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "create %s: %v\n", filepath.Dir(unitPath), err)
			return false
		}
		if err := os.WriteFile(unitPath, fmt.Appendf(nil, systemdUnit, bin), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", unitPath, err)
			return false
		}
		fmt.Printf("wrote %s\n", unitPath)
	} else {
		fmt.Printf("kept the existing %s\n", unitPath)
	}
	// restart, not just enable --now: on a machine where the unit is already
	// running, `enable --now` is a no-op, so the settings this command just
	// wrote would not be in force until somebody restarted it by hand — and
	// nothing would say so. Turning on Global Access and then watching a
	// registry report no public address is exactly that failure.
	for _, cmd := range [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "tacit-registry.service"},
		{"systemctl", "--user", "restart", "tacit-registry.service"},
	} {
		if out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n%s", strings.Join(cmd, " "), err, out)
			return false
		}
	}
	fmt.Println("enabled the service (tip: `loginctl enable-linger` keeps it up after logout)")
	return true
}

// publicAddressNoted records that this run turned the shared proxy on, so the
// summary can say so where somebody is reading rather than mid-run.
var publicAddressNoted bool

// initVerbose turns the running commentary back on.
//
// `tacit init` narrated itself: every file it wrote, every setting it chose,
// the count of starter techniques, the proxy's terms in four lines, the model
// key's absence in six. Thirty-five lines of it, and the one thing a new
// operator can act on — the sign-in link — was at the bottom. Somebody meeting
// the product reads the first screen most carefully and it was mostly paths.
//
// So the default says what it did once, at the end, and this flag restores the
// step-by-step for anybody debugging a setup rather than doing one.
var initVerbose bool

// initBanner is the wordmark, and it spells ONE word: the name in
// product.Default. Everything else the registry displays asks internal/product
// for the name, and lettering cannot — so where an operator has renamed the
// product this prints nothing rather than the old name six lines high. The
// summary below it names things correctly either way.
//
// Only PRODUCT_NAME already in the environment can be read here: the banner is
// the first thing out, and registry.env has not been materialized yet. That is
// the right trade for a decoration — a rename set in the settings file loses a
// banner, not a correct name.
//
// No leading newline: this is the first thing the command writes, so a blank
// line above it is a blank line at the top of the terminal.
const initBanner = `   ____                 ______           _ __
  / __ \____  ___  ____/_  __/___ ______(_) /_
 / / / / __ \/ _ \/ __ \/ / / __ ` + "`" + `/ ___/ / __/
/ /_/ / /_/ /  __/ / / / / / /_/ / /__/ / /_
\____/ .___/\___/_/ /_/_/  \__,_/\___/_/\__/
    /_/
`

func printInitBanner() {
	if product.Name() != product.Default {
		return
	}
	// Print, not Println: the const already ends in a newline and the section
	// after it opens with one, so a third left two blank lines under the mark.
	fmt.Print(initBanner)
}

// step prints a line of the running commentary.
func step(format string, a ...any) {
	if !initVerbose {
		return
	}
	fmt.Printf(format, a...)
}

// flagWasSet reports whether the caller named this flag, as against it holding
// its default. The difference decides whether there is a question to ask.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			seen = true
		}
	})
	return seen
}

// askAboutService puts the background service to the person running init.
//
// Consent, not convenience: this installs something that runs when they are
// not looking, so it says what it starts and how to end it before it asks. The
// default is yes because a registry that stops when the terminal closes is the
// more surprising outcome of the two.
func askAboutService() bool {
	fmt.Println()
	fmt.Println("keep this registry running in the background?")
	fmt.Printf("  it installs a service for your user only, which starts `%s serve` at login\n", selfCommand())
	fmt.Printf("  it serves your own machine; nothing is exposed to the network by this answer\n")
	fmt.Printf("  stop it any time with:  %s\n", serviceStopCommand())
	p := &prompter{sc: bufio.NewScanner(os.Stdin), interactive: true}
	return p.confirmDefaultYes("keep it running")
}

// serviceStopCommand names the exact way off this platform's service.
func serviceStopCommand() string {
	switch {
	case runtime.GOOS == "linux" && haveExec("systemctl"):
		return "systemctl --user disable --now " + servicectl.Unit
	case runtime.GOOS == "darwin":
		return fmt.Sprintf("launchctl bootout gui/%d/%s", os.Getuid(), servicectl.Label)
	}
	return selfCommand() + " disconnect"
}

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>com.tacit.registry</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>serve</string></array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
	<key>StandardOutPath</key><string>%s</string>
	<key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

// setupLaunchd is the macOS equivalent. launchd has no EnvironmentFile, and
// needs none: registry config.Load falls back to ~/.config/tacit/registry.env
// on its own.
func setupLaunchd() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.tacit.registry.plist")
	logPath := filepath.Join(home, "Library", "Logs", "tacit-registry.log")
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		bin, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve binary path: %v\n", err)
			return false
		}
		if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "create %s: %v\n", filepath.Dir(plistPath), err)
			return false
		}
		if err := os.WriteFile(plistPath, fmt.Appendf(nil, launchdPlist, bin, logPath, logPath), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", plistPath, err)
			return false
		}
		fmt.Printf("wrote %s\n", plistPath)
	} else {
		fmt.Printf("kept the existing %s\n", plistPath)
	}
	// bootstrap is the modern verb; fall back to load -w for older macOS.
	uid := os.Getuid()
	if out, err := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath).CombinedOutput(); err != nil {
		if strings.Contains(string(out), "already bootstrapped") || strings.Contains(string(out), "Bootstrap failed: 5: Input/output error") {
			return true // already loaded
		}
		if out2, err2 := exec.Command("launchctl", "load", "-w", plistPath).CombinedOutput(); err2 != nil {
			fmt.Fprintf(os.Stderr, "launchctl: %v\n%s%s", err2, out, out2)
			return false
		}
	}
	return true
}

func pollHealth(port int, budget time.Duration) bool {
	hc := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		resp, err := hc.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/health", port))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

// instanceFingerprint is the short public form of this registry's instance key,
// for the line `tacit init` prints. An unreadable key is not worth failing the
// install over — publishing is off by default and the switch will make one.
func instanceFingerprint(configDir string) string {
	key, err := ingress.LoadKey(configDir)
	if err != nil {
		return "—"
	}
	return ingress.FingerprintOf(key)
}
