// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Command tacit is the single binary for the whole system.
//
//	tacit init                           bootstrap a registry on this machine, and serve it where nothing else will
//	tacit serve                          run the registry service
//	tacit connect [flags]                wire this machine's AI harnesses
//	tacit audit <source> [flags]         audit a shared conversation
//	tacit feedback <technique-id>        record feedback on a suggestion
//	tacit eval retrieval --set <json>    compare dense and hybrid retrieval
//	tacit serve-hooks                    run the local in-harness hook agent
//	tacit hook-relay <harness>           forward one hook event (auto-starts the agent)
//	tacit doctor [--fix]                 check this machine's wiring
//
// Audit picks the real LLM when TACIT_LLM_API_KEY is set, else the offline
// heuristic.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentacit/tacit"
	"github.com/opentacit/tacit/internal/auditor/audit"
	"github.com/opentacit/tacit/internal/auditor/capture"
	"github.com/opentacit/tacit/internal/auditor/client"
	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/hooks"
	"github.com/opentacit/tacit/internal/auditor/ingest"
	"github.com/opentacit/tacit/internal/auditor/llm"
	"github.com/opentacit/tacit/internal/auditor/mcp"
	"github.com/opentacit/tacit/internal/auditor/sessionhash"
	"github.com/opentacit/tacit/internal/ledger"
	"github.com/opentacit/tacit/internal/merge"
	"github.com/opentacit/tacit/internal/product"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/digest"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/feedback"
	"github.com/opentacit/tacit/internal/registry/jobs"
	"github.com/opentacit/tacit/internal/registry/migrate"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/observe"
	"github.com/opentacit/tacit/internal/registry/sqlitestore"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/internal/registry/store"
	"github.com/opentacit/tacit/internal/registry/suggest"
	"github.com/opentacit/tacit/internal/registry/web"
	pkgclient "github.com/opentacit/tacit/pkg/client"
)

// version is stamped by the build (-ldflags "-X main.version=...") — the
// Makefile and the release workflow both set it; a bare `go build` stays "dev".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitUsage
	}
	switch args[0] {
	case "init":
		return cmdInit(args[1:])
	case "connect":
		return cmdConnect(args[1:])
	case "pause":
		return cmdPause(args[1:])
	case "resume":
		return cmdResume(args[1:])
	case "disconnect":
		return cmdDisconnect(args[1:])
	case "invite":
		return cmdInvite(args[1:])
	case "join":
		return cmdJoin(args[1:])
	case "merge":
		return cmdMerge(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "dashboard":
		return cmdDashboard(args[1:])
	case "secure":
		return cmdSecure(args[1:])
	case "upgrade":
		return cmdUpgrade(args[1:])
	case "serve":
		return cmdServe(args[1:])
	case "audit":
		return cmdAudit(args[1:])
	case "feedback":
		return cmdFeedback(args[1:])
	case "revise":
		return cmdRevise(args[1:])
	case "suggest":
		return cmdSuggest(args[1:])
	case "ask":
		return cmdAsk(args[1:])
	case "cohorts":
		return cmdCohorts(args[1:])
	case "serve-hooks":
		return cmdServeHooks(args[1:])
	case "hook-relay":
		return cmdHookRelay(args[1:])
	case "statusline":
		return cmdStatusline()
	case "env":
		return cmdEnv(args[1:])
	case "mcp":
		return cmdMCP()
	case "usage":
		return cmdUsage(args[1:])
	case "conventions":
		return cmdConventions(args[1:])
	case "feed":
		return cmdFeed(args[1:])
	case "digest":
		return cmdDigest(args[1:])
	case "migrate-store":
		return cmdMigrateStore(args[1:])
	case "demo":
		return cmdDemo(args[1:])
	case "eval":
		return cmdEval(args[1:])
	case "onnx-fetch": // hidden: the Docker image build's fetch stage
		return cmdOnnxFetch(args[1:])
	case "version", "-v", "--version":
		fmt.Println(version)
		return 0
	case "help", "-h", "--help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", args[0])
		usage()
		return 2
	}
}

// inContainer reports whether this binary runs inside the official image
// (which bakes TACIT_CONTAINER=1). Containers are immutable and have no
// service manager: upgrade means pulling a new image tag, and doctor's
// systemd hints would mislead.
func inContainer() bool { return os.Getenv("TACIT_CONTAINER") != "" }

// Exit codes. They are a wire surface: scripts and container health checks
// branch on them, and doctor's 3 in particular means "the registry did not
// answer", which is retryable, where 1 means "it answered and something is
// wrong", which is not.
const (
	exitOK          = 0
	exitFail        = 1
	exitUsage       = 2
	exitUnreachable = 3
)

// parseFlags parses flags interspersed with positionals (argparse-style, so
// `tacit audit - --text` works), returning the positionals in order and whether
// the flags parsed.
//
// Every flag set is ContinueOnError, so a bad flag returns through run() and
// main's single os.Exit rather than calling os.Exit from inside the flag
// package. That is what makes a command testable: under ExitOnError a test for
// "a bad flag reports usage" would take the test binary down with it.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, bool) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, false // flag has already printed the error and the usage
		}
		args = fs.Args()
		if len(args) == 0 {
			return positionals, true
		}
		positionals = append(positionals, args[0])
		args = args[1:]
	}
}

// The help text is authored prose, so it goes out through product.Rename
// rather than being interleaved with Name() calls — that is the split this
// package draws: compose with Name, rewrite prose with Rename. Every `tacit`
// in it is the command and survives untouched (product.Rename, rename_test).
func usage() {
	fmt.Fprint(os.Stderr, product.Rename(`tacit: registry, auditor, and hook agent

this machine
  tacit connect [--registry URL --code C] [--harness NAME]  point this machine at a registry and wire its AI tools;
                                       with no flags, report where it points. --code takes the handoff code from
                                       an invitation page (--key still takes a member key); --segment sets your
                                       cohort; --settings-only skips the wiring (a CI box with no AI tool on it)
  tacit join <join-url>                join a registry from an invite link, then connect
  tacit merge <join-url> [--keep-address --dry-run --yes]   fold this machine's personal registry into an
                                       organization's: contribute its techniques, archive its evidence, point
                                       the harnesses there, then stop the registry and release its address
  tacit disconnect [--registry --yes]  unwire this machine (inverse of connect/join; --registry also removes the service)
  tacit doctor [flags]                 is Tacit working here? --ready answers in one line and prints the full
                                       report only when something is wrong. --harness <name> checks one hook path end to end,
                                       --deliver shows whether suggestions RENDER for you (--deliver-status reads it back),
                                       --fix repairs a rejected key, --probe is a bare liveness check for container probes
  tacit upgrade [--version vX.Y.Z]     replace this binary with a verified release build

day to day
  tacit ask "<task>"                   one technique from the playbook for the task in hand, with the evidence
                                       behind it — or an honest "nothing matches that yet"
  tacit pause / tacit resume           stop being observed on this machine, and start again. Pause unwires
                                       nothing and takes hold on your next turn; asking still works
  tacit audit <source> [flags]         audit a conversation: a share URL, a transcript or captured-session file,
                                       - with --text, or --live <session-id> for a session as it runs
  tacit feedback <technique-id>        record feedback
  tacit revise <technique-id> [flags]  propose an update as a revision draft
  tacit cohorts [--json] [--skip]      list the cohorts that colleagues use, so you can join one and not make a
                                       near-duplicate; --skip stops this machine being asked for one
  tacit usage [--window 30d] [--json|--html]  show your OWN Tacit usage from this machine's local log (text, JSON, or a self-contained panel)
  tacit conventions [--root DIR] [--write]    report where your projects' CLAUDE.md / AGENTS.md / .cursorrules have drifted apart, and write the playbook back into them
  tacit dashboard                      print a fresh owner sign-in link for this registry (single-member mode, no identity provider)

the registry
  tacit init                           set up a registry on this machine (config, techniques, embeddings, service) and, with no service manager, serve it
  tacit serve                          run the registry service
  tacit secure [--off] [--dry-run]     turn dashboard sign-in on with your identity provider (--off opens it again)
  tacit invite [--ttl 24h] [--repo]    make a join link that a teammate can curl | sh; --repo also writes the .tacit/registry.toml marker
  tacit suggest [--n 10]               research practices matched to observed usage and create drafts
  tacit digest [--window 7d] [--out file | --slack <webhook>]   make the team digest markdown (A2 surface); TACIT_DIGEST_WEBHOOK posts it each week from serve
  tacit feed export --channel <ch> --out <dir> [--base-url URL]   static federation export
  tacit migrate-store                  copy file-store state into Postgres or SQLite (cutover)
  tacit demo load [--scenario <key>]   fill a registry with one month of demonstration usage
  tacit eval retrieval --set <json>    compare dense retrieval with a hybrid challenger; --model and --dim override the configured embedder

plumbing (harnesses and skills invoke these)
  tacit serve-hooks                    run the local in-harness hook agent
  tacit hook-relay <harness>           forward one hook event (for plugin hooks)
  tacit statusline                     status-line segment (add to the statusLine settings)
  tacit env                            print resolved REG/KEY/DASH for skills (eval "$(tacit env)")
  tacit mcp                            run the MCP stdio server for tacit_search and tacit_insights
  tacit version                        print the stamped build version
`))
}

// --- registry --------------------------------------------------------------

// resolveDocsDir is where the dashboard reads the user guide from.
//
// A checkout or the container image has the real tree, and it is more current
// than anything compiled into a binary built from it, so it wins. An installed
// binary has neither — the release tarball is one file — and used to serve
// "Document not found" as a single-member registry's home page. It gets the
// embedded copy, written once per build under the data home.
//
// A failure here is not worth refusing to serve over: the registry's job is the
// playbook, and the operator gets the same missing-document page they had
// before, with the reason in the log.
func resolveDocsDir(configured string) string {
	if tacit.GuideOnDisk(configured) {
		return configured
	}
	root, err := tacit.MaterializeGuide(registryconfig.DataHome(), version)
	if err != nil {
		log.Printf("[tacit] cannot write the user guide to %s: %v", registryconfig.DataHome(), err)
		return configured
	}
	return root
}

// startupQuiet suppresses the registry's informational startup log.
//
// `tacit init` at a console prints a full report — where the store is, the
// address, the API key, how members join, the sign-in link — and then serves in
// the same process. The registry's own startup lines then said the same facts
// again, timestamped and tagged by subsystem, to a person who has just met the
// product and can do nothing with any of them. Two voices, one of them a
// service log, on the screen a new operator reads most carefully.
//
// Only the informational ones. Everything that reports trouble keeps its
// log.Printf, and the first-run claim-code banner is louder than either,
// because those are the lines somebody has to act on.
var startupQuiet bool

// logStartup reports a startup fact that a foreground `init` has already said
// in its own words.
func logStartup(format string, args ...any) {
	if startupQuiet {
		return
	}
	log.Printf(format, args...)
}

func cmdServe(args []string) int {
	// registry.env IS the environment for a registry started by hand. A service
	// gets that from systemd's EnvironmentFile= or Docker's --env-file; nothing
	// was doing it here, so settings saved through the dashboard that are read
	// with os.Getenv — the model API key, most visibly — were written to the
	// file and then invisible on the next start.
	registryconfig.ExportFileEnv()
	cfg := registryconfig.Load()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&cfg.Host, "host", cfg.Host, "bind host")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "bind port")
	fs.StringVar(&cfg.DataDir, "data", cfg.DataDir, "file-store directory")
	fs.StringVar(&cfg.DBURL, "db", cfg.DBURL, "Postgres URL (empty = embedded file store)")
	fs.StringVar(&cfg.TechniquesDir, "techniques", cfg.TechniquesDir, "curated techniques directory")
	docsDir := fs.String("docs", "docs", "docs directory for the dashboard viewer")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	// A registry that has been merged does not start again. Its work is in the
	// organization's playbook, its address was handed back, and what is on this
	// disk is the instance that contributed it — starting it would serve a
	// playbook that has already moved, under a ledger reporting on a registry
	// that no longer exists (internal/merge/retire.go).
	if dest, retired := merge.RetiredInto(filepath.Dir(registryconfig.RegistryEnvPath())); retired {
		log.Printf("this registry was merged and is finished; it does not start again")
		if dest != "" {
			log.Printf("your playbook is at %s, and this machine's tools already point at it", dest)
		}
		log.Printf("to set up a new registry here:  %s init --start-over", selfCommand())
		return 1
	}

	// The port, before anything opens. The listener is the last thing this
	// command reaches: a busy port used to be a bind error printed after a cold
	// start had loaded an embedder and synced the corpus, and it named the port
	// and nothing else. The two ways a port can be busy also want different
	// advice — one of them is this same registry, already being served.
	if !portFree(cfg.Host, cfg.Port) {
		if registryRunning(fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)) {
			log.Printf("a registry is already answering on port %d; that is this one, already being served", cfg.Port)
			log.Printf("for a sign-in link to it:  %s dashboard", selfCommand())
			return 1
		}
		if !portFreeWithin(cfg.Host, cfg.Port, 5*time.Second) {
			log.Printf("port %d is in use by something that is not a registry", cfg.Port)
			log.Printf("serve this one elsewhere with:  %s serve --port <free port>", selfCommand())
			return 1
		}
	}

	// A fresh install has no techniques directory at all — seed it with the
	// embedded starter set so retrieval has something to serve. An existing
	// directory is operator state and is never touched (see seedtechniques.go).
	if n, err := tacit.MaterializeSeedTechniques(cfg.TechniquesDir); err != nil {
		log.Printf("seed techniques: %v", err)
	} else if n > 0 {
		log.Printf("[tacit] seeded %s with %d starter techniques", cfg.TechniquesDir, n)
	}

	var st storage.Store
	var err error
	if path, ok := sqlitestore.PathFromURL(cfg.DBURL); ok {
		_, statErr := os.Stat(path)
		fresh := os.IsNotExist(statErr)
		st, err = sqlitestore.Open(path)
		logStartup("[tacit] storage backend: sqlite (%s)", path)
		if err == nil && fresh {
			if cutErr := sqliteAutoCutover(st, cfg.DataDir); cutErr != nil {
				// Serving an empty database beside unmigrated file-store data
				// would read as data loss — refuse to start instead.
				log.Printf("file-store cutover into %s failed: %v", path, cutErr)
				st.Close()
				return 1
			}
		}
	} else if cfg.DBURL != "" {
		st, err = postgresStore(cfg.DBURL)
		logStartup("[tacit] storage backend: postgres")
	} else {
		st, err = store.Open(cfg.DataDir)
		logStartup("[tacit] storage backend: embedded file store (%s)", cfg.DataDir)
	}
	if err != nil {
		log.Printf("open store: %v", err)
		return 1
	}
	defer st.Close()
	embedder, err := embed.New(cfg.EmbedModel, cfg.EmbedDim)
	if err != nil {
		// A misconfigured optional embedder (e.g. onnx artifacts missing) must
		// not brick the registry: degrade to the built-in lexical default. Only
		// a broken hashing default is fatal.
		if cfg.EmbedModel == "hashing-v1" {
			log.Print(err)
			return 1
		}
		log.Printf("embedder %q unavailable (%v); using hashing-v1", cfg.EmbedModel, err)
		if embedder, err = embed.New("hashing-v1", 256); err != nil {
			log.Print(err)
			return 1
		}
	}
	synced, embedded, err := jobs.Startup(st, cfg.TechniquesDir, embedder)
	if err != nil {
		log.Printf("startup: %v", err)
		return 1
	}
	// discover runs the observed-technique discovery pass each cycle when enabled: it
	// clusters the org's own technique-less "worked move" sketches into candidates and
	// files them as provenance=observed (docs/learning/observed-technique-discovery.md).
	// It needs a plain LLM (Complete), built from env here so jobs stays LLM-free;
	// a missing key simply skips the pass. Entry follows AutoShadow, like suggest.
	var discover func()
	if cfg.AutoDiscover {
		discover = func() {
			model, err := suggest.FromEnv()
			if err != nil {
				return // no model configured: nothing to distill with
			}
			if _, err := observe.Run(st, embedder, model, observe.Config{
				MinSessions:   registryconfig.ObserveMinSessions,
				MinCohorts:    registryconfig.ObserveMinCohorts,
				SimThreshold:  registryconfig.ObserveSim,
				MaxTechniques: registryconfig.ObserveMaxTechniques,
				EnterShadow:   cfg.AutoShadow,
			}); err != nil {
				log.Printf("[discover] %v", err)
			}
		}
	}
	// Weekly digest to chat, registry-side: the digest used to be a file
	// nobody was sent. One env var (TACIT_DIGEST_WEBHOOK) and it travels.
	if cfg.DigestWebhook != "" {
		stopDigest := digest.StartWeekly(func() ([]models.Technique, []models.FeedbackEvent, error) {
			techniques, err := st.ListTechniques(nil, 0)
			if err != nil {
				return nil, nil, err
			}
			events, err := st.AllEvents("")
			if err != nil {
				return nil, nil, err
			}
			return techniques, events, nil
		}, cfg.DigestWebhook, filepath.Join(cfg.DataDir, "digest-week.stamp"), time.Now)
		defer stopDigest()
		logStartup("[tacit] weekly digest delivery: on (Slack webhook configured)")
	}

	srv := web.New(cfg, st, embedder, resolveDocsDir(*docsDir))
	srv.QuietStartup = startupQuiet
	srv.Version = version

	// The rollup scheduler starts after the server exists, because the Public
	// channel's re-rank is a method on it: ranking reads live config (the
	// evidence floor, whether the commons is confirmed) that only the server
	// holds. Recompute once now so a restart serves the channel it had rather
	// than an empty one until the first tick.
	srv.RecomputePublicChannel()
	stop := jobs.StartScheduler(st, time.Duration(cfg.RecomputeIntervalSecs)*time.Second,
		feedback.AutoPromoteGate{
			Enabled:   cfg.AutoPromoteEnabled,
			MinFit:    cfg.AutoPromoteMinFit,
			MinJudged: cfg.AutoPromoteMinJudged,
		}, discover, srv.RecomputePublicChannel)
	defer stop()
	if embedder.ModelID() != cfg.EmbedModel {
		// The startup fallback fired (log line above): advertise the mismatch
		// on /v1/health so `tacit doctor` sees it from any machine.
		srv.EmbedWanted = cfg.EmbedModel
	}

	// First run: no registry.env, no key in the environment. Refuse to serve
	// with the compiled-in defaults (a KNOWN key on an open port) — park
	// everything behind the setup UI and print the claim code where only the
	// operator can read it (web/setup.go).
	if cfg.FirstRun {
		code := web.NewSetupCode()
		srv.EnableSetup(code)
		log.Printf("[tacit] ──────────────────────────────────────────────────")
		log.Printf("[tacit] FIRST RUN: this registry is awaiting configuration.")
		log.Printf("[tacit] Open http://localhost:%d/setup and enter the claim code:", cfg.Port)
		log.Printf("[tacit]")
		log.Printf("[tacit]     %s", code)
		log.Printf("[tacit]")
		log.Printf("[tacit] Until then, pages redirect to /setup and the API answers 503.")
		log.Printf("[tacit] ──────────────────────────────────────────────────")
	}

	// Remote MCP endpoint: expose the same tools the stdio server does, wired
	// back into this registry over the loopback so key + translation stay
	// identical to /v1. Segment is unknown for a shared network endpoint (the
	// caller's cohort isn't carried yet), so events record without one.
	loopback := fmt.Sprintf("http://%s:%d", mcpLoopbackHost(cfg.Host), cfg.Port)
	mcpServer := newMCPServer(loopback, cfg.APIKey, nil)
	// tacit_usage renders as an MCP App. This shared endpoint has no member
	// identity, so it can only ever show the machine it runs on: TACIT_LOCAL_USAGE
	// delegates to the co-located log (self-hosted, single-member), else a pointer.
	mcpServer.Usage = remoteUsageFunc(auditorconfig.Load().UsageLogPath)
	srv.MCP = mcpServer

	// federation: poll subscribed feeds on their own cadence
	pollStop := make(chan struct{})
	defer close(pollStop)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-pollStop:
				return
			case <-ticker.C:
				for _, res := range srv.Poller().PollDue() {
					if res.Error != "" {
						log.Printf("[tacit] feed poll %s: %s", res.SubscriptionID, res.Error)
					} else if res.Imported+res.Updated+res.Retracted > 0 {
						log.Printf("[tacit] feed poll %s: %d imported, %d updated, %d retractions",
							res.SubscriptionID, res.Imported, res.Updated, res.Retracted)
					}
				}
			}
		}
	}()
	// Demonstration mode (demoserve.go): isolated per-scenario instances plus
	// the dataset cookie router in front. Skipped on first run — a registry
	// nobody has configured yet has no business showing demo data.
	handler := srv.Handler()
	if cfg.DemoDir != "" && !cfg.FirstRun {
		routed, cleanup, err := enableDemoMode(cfg, embedder, *docsDir, srv, handler)
		if err != nil {
			log.Printf("[tacit] demo mode disabled: %v", err)
		} else {
			handler = routed
			defer cleanup()
		}
	}

	// Publishing rides inside this process rather than beside it: the tunnel
	// serves the same handler the listener does, so a registry reachable
	// locally is reachable publicly with nothing else running
	// (docs/distribution/global-access-plan.md).
	srv.PublishHandler = handler
	if cfg.GlobalAccess {
		if ok, why := publishAllowed(cfg); !ok {
			log.Printf("[tacit] %s", why)
		} else {
			srv.StartPublishing(handler)
			logStartup("[tacit] publishing through %s", cfg.PublishIngress)
		}
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	logStartup("[tacit] startup synced=%d embedded=%d; serving on http://%s", synced, embedded, addr)

	// A server value rather than ListenAndServe's implicit one, so the registry
	// can be asked to stop from inside itself. The only thing that asks is the
	// last step of a merge finished from the dashboard: a member who has
	// contributed their playbook and told this registry to retire is asking the
	// process serving that page to be the thing that goes. Stopping itself is
	// the most reliable stop there is — no unit file to find, no foreign
	// terminal to negotiate with.
	httpSrv := &http.Server{Addr: addr, Handler: handler} //nolint:gosec // no timeouts, as before: this serves long reads
	srv.RequestStop = func(why string) {
		log.Printf("[tacit] stopping: %s", why)
		// Off the request, and Shutdown waits for it: the member's browser gets
		// the page that told them this was happening.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(ctx)
		}()
	}
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
		return 1
	}
	return 0
}

// publishAllowed decides whether a registry with Global Access on may actually
// dial the ingress, and says why not when it may not.
//
// A public address in front of an ungated dashboard is the one configuration
// this must never assemble on somebody's behalf: the two settings are set in
// different places, months apart, by people who each did something reasonable.
// Refusing to dial is the safe half of the failure — the registry goes on
// serving locally and nothing is exposed — and the message names both ways out
// rather than only the one that needs an identity provider.
func publishAllowed(cfg registryconfig.Config) (bool, string) {
	if cfg.AuthConfigured() {
		return true, ""
	}
	return false, "NOT publishing: Global Access is on and this registry has no sign-in. " +
		"Set one up with `tacit secure` (your identity provider) or `tacit init --owner` " +
		"(one member, no provider), or turn Global Access off in Settings."
}

// sqliteAutoCutover imports an existing file store into a JUST-CREATED SQLite
// database (docs/design/sqlite-plan.md). The case it exists for: the official
// container image defaults to SQLite, and a /data volume that predates that
// default carries file-store state — booting an empty database beside it
// would look exactly like data loss. It runs only when the database file did
// not exist before this start AND the data dir actually holds state: a fresh
// install has neither, an already-cutover deployment has the database. The
// copy is migrate-store's — idempotent, count-verified — and the source dir
// is left in place as the pre-cutover archive.
func sqliteAutoCutover(dst storage.Store, dataDir string) error {
	seeded := false
	for _, f := range []string{"techniques.json", "events.jsonl"} {
		if _, err := os.Stat(filepath.Join(dataDir, f)); err == nil {
			seeded = true
		}
	}
	if !seeded {
		return nil
	}
	src, err := store.Open(dataDir)
	if err != nil {
		return fmt.Errorf("open file store %s: %w", dataDir, err)
	}
	defer src.Close()
	stats, err := migrate.Copy(src, dst)
	if err != nil {
		return err
	}
	if err := migrate.Verify(src, dst); err != nil {
		return err
	}
	log.Printf("[tacit] imported the existing file store into sqlite: %d techniques, %d events (%s stays as the pre-cutover archive)",
		stats.Techniques, stats.Events, dataDir)
	return nil
}

// cmdMigrateStore is the file-store -> database cutover
// (docs/design/postgres-plan.md phase 2; docs/design/sqlite-plan.md phase 2).
// The destination opens by the same scheme dispatch as `tacit serve`.
// Idempotent: safe to re-run after a partial failure.
func cmdMigrateStore(args []string) int {
	cfg := registryconfig.Load()
	fs := flag.NewFlagSet("migrate-store", flag.ContinueOnError)
	dataDir := fs.String("data", cfg.DataDir, "source file-store directory")
	dbURL := fs.String("db", cfg.DBURL, "destination URL (postgres://... or sqlite:...)")
	dryRun := fs.Bool("dry-run", false, "report the migration counts and write nothing")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}
	if *dbURL == "" {
		fmt.Fprintln(os.Stderr, "usage: tacit migrate-store --data ./data --db postgres://...|sqlite:... [--dry-run]")
		return 2
	}

	src, err := store.Open(*dataDir)
	if err != nil {
		log.Printf("open source file store: %v", err)
		return 1
	}
	if *dryRun {
		techniques, events, err := migrate.Plan(src)
		if err != nil {
			log.Print(err)
			return 1
		}
		fmt.Printf("dry run: %d techniques and %d events to migrate from %s\n", techniques, events, *dataDir)
		return 0
	}

	var dst storage.Store
	if path, ok := sqlitestore.PathFromURL(*dbURL); ok {
		dst, err = sqlitestore.Open(path)
	} else {
		dst, err = postgresStore(*dbURL)
	}
	if err != nil {
		log.Printf("open destination: %v", err)
		return 1
	}
	defer dst.Close()
	stats, err := migrate.Copy(src, dst)
	if err != nil {
		log.Printf("migration failed (a re-run is safe): %v", err)
		return 1
	}
	if err := migrate.Verify(src, dst); err != nil {
		log.Printf("post-migration check failed: %v", err)
		return 1
	}
	fmt.Printf("migrated %d techniques (%d archived versions) and %d events (%d already present); recomputed %d rollup rows; the counts match\n",
		stats.Techniques, stats.Versions, stats.Events, stats.DuplicateEvents, stats.OutcomeRows)
	return 0
}

// --- auditor ----------------------------------------------------------------

func pickLLM(cfg auditorconfig.Config, offline bool) llm.Client {
	if offline {
		return llm.Heuristic{}
	}
	key := llm.ResolveKey(cfg.LLMKeyFile)
	if key == "" {
		fmt.Fprintf(os.Stderr, "[auditor] %s uses the offline heuristic LLM. Set TACIT_LLM_API_KEY to enable a real model.\n", product.Name())
		return llm.Heuristic{}
	}
	a, err := llm.New(llmConfig(cfg, key))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[auditor] %v; %s uses the offline heuristic LLM.\n", err, product.Name())
		return llm.Heuristic{}
	}
	return a
}

func llmConfig(cfg auditorconfig.Config, key string) llm.Config {
	return llm.Config{
		Provider: cfg.LLMProvider,
		APIKey:   key, BaseURL: cfg.LLMBaseURL, Version: cfg.AnthropicVersion,
		ExtraHeaders: llmExtraHeaders(),
		CharModel:    cfg.CharModel, SynthModel: cfg.SynthModel, MaxTokens: cfg.MaxTokens,
		PromptsDir: promptsDir(),
	}
}

// llmExtraHeaders carries optional OpenRouter request-attribution headers
// (HTTP-Referer / X-Title). Both are optional and empty for other providers.
func llmExtraHeaders() map[string]string {
	h := map[string]string{}
	if v := strings.TrimSpace(os.Getenv("TACIT_LLM_REFERER")); v != "" {
		h["HTTP-Referer"] = v
	}
	if v := strings.TrimSpace(os.Getenv("TACIT_LLM_TITLE")); v != "" {
		h["X-Title"] = v
	}
	if len(h) == 0 {
		return nil
	}
	return h
}

// promptsDir finds prompts/ next to the working directory (repo layout).
// TACIT_PROMPTS_DIR overrides it.
func promptsDir() string {
	if p := os.Getenv("TACIT_PROMPTS_DIR"); p != "" {
		return p
	}
	return "prompts"
}

func printAudit(result audit.Result, registry *client.Registry, record bool, seg contracts.Segment) {
	fmt.Println(result.AuditText)
	fmt.Fprintf(os.Stderr, "\n[evidence: %d candidate(s); audit_id=%s]\n",
		len(result.ShownTechniqueIDs), result.AuditID)
	if record && len(result.ShownTechniqueIDs) > 0 {
		if _, err := registry.PostFeedback(audit.ShownEvents(result, seg, contracts.SourcePull)); err != nil {
			fmt.Fprintf(os.Stderr, "[cannot record shown events: %v]\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "[recorded %d 'shown' events]\n", len(result.ShownTechniqueIDs))
	}
}

// cmdAudit audits one conversation, whatever shape it arrives in: a share URL,
// a transcript file, raw text on stdin, a captured meta-harness session, or a
// live session streamed turn by turn.
//
// Those last two were `tacit omnigent` and `tacit omnigent-live`. They are the
// same act — audit a session against the playbook — differing only in where the
// transcript comes from, which is exactly what the source argument already
// says. Three verbs for one act made the choice look consequential; it is not.
func cmdAudit(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	text := fs.Bool("text", false, "treat source as raw transcript text")
	segment := fs.String("segment", "", "comma list, e.g. role=developer,domain=data-engineering")
	registryURL := fs.String("registry", cfg.RegistryURL, "registry URL")
	key := fs.String("key", cfg.RegistryKey, "registry API key")
	cookie := fs.String("cookie", "", "Cookie header for Claude share links")
	offline := fs.Bool("offline", false, "force the heuristic LLM")
	record := fs.Bool("record", false, "post 'shown' feedback events")
	live := fs.Bool("live", false, "audit a live captured session turn by turn as it runs; the source is the session id")
	baseURL := fs.String("base-url", cfg.OmnigentBase, "with --live: the capture server's URL")
	token := fs.String("token", cfg.OmnigentToken, "with --live: bearer token")
	email := fs.String("email", cfg.OmnigentEmail, "with --live: X-Forwarded-Email auth")
	maxTurns := fs.Int("max-turns", 0, "with --live: stop after N turns (default: run until the stream ends)")
	positionals, ok := parseFlags(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positionals) < 1 {
		fmt.Fprintln(os.Stderr, "usage: tacit audit <source> [flags]\n"+
			"       tacit audit --live <session-id> [flags]\n"+
			"source: a share URL, a transcript or captured-session file, or - with --text")
		return 2
	}
	source := positionals[0]
	seg := contracts.Segment(auditorconfig.ParseSegment(*segment))
	registry := &client.Registry{BaseURL: *registryURL, APIKey: *key}

	if *live {
		return auditLive(cfg, registry, seg, source, liveOpts{
			baseURL: *baseURL, token: *token, email: *email,
			offline: *offline, record: *record, maxTurns: *maxTurns,
		})
	}
	// A captured session carries its own structured characterization, so it is
	// audited from the record rather than from text the model must characterize
	// again. Recognized by shape, not by a flag: a file either is one or isn't.
	if !*text {
		if rec, ok := readCapturedSession(source); ok {
			return auditCaptured(cfg, registry, seg, rec, *offline, *record)
		}
	}

	if *text && source == "-" {
		raw, _ := readAllStdin()
		source = raw
	}
	transcript, err := ingest.LoadTranscript(source, *text, *cookie)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingestion failed: %v\n", err)
		return 2
	}
	result, err := audit.Run(transcript, pickLLM(cfg, *offline), registry.GetEvidence, seg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach the registry at %s: %v\n", *registryURL, err)
		return exitUnreachable
	}
	printAudit(result, registry, *record, seg)
	return 0
}

// readCapturedSession reports whether the source is a captured meta-harness
// session file, and returns it. The reader itself accepts any JSON object, so
// the test is whether the parse produced an actual session: a source and turns.
// Anything less falls through to the ordinary transcript path, where a JSON
// file is still perfectly loadable.
func readCapturedSession(source string) (contracts.CanonicalRecord, bool) {
	if st, err := os.Stat(source); err != nil || st.IsDir() {
		return contracts.CanonicalRecord{}, false
	}
	rec, err := capture.ReadOmnigentFile(source)
	if err != nil || rec.Source == "" || len(rec.Messages) == 0 {
		return contracts.CanonicalRecord{}, false
	}
	return rec, true
}

// auditCaptured audits one captured session record. Capture gives a structured
// characterization directly — no LLM guess.
func auditCaptured(cfg auditorconfig.Config, registry *client.Registry, seg contracts.Segment,
	rec contracts.CanonicalRecord, offline, record bool) int {
	char := capture.Characterize(rec)
	char.SessionHash = sessionhash.Hash(cfg.SessionSalt, "omnigent:"+rec.SessionID)
	result, err := audit.Run(capture.ToTranscriptText(rec), pickLLM(cfg, offline), registry.GetEvidence, seg, &char)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach the registry at %s: %v\n", registry.BaseURL, err)
		return exitUnreachable
	}
	fmt.Println(result.AuditText)
	fmt.Fprintf(os.Stderr, "\n[source=%s session=%s segment=%v; %d candidate(s); audit_id=%s]\n",
		rec.Source, rec.SessionID, result.Characterization.Segment, len(result.ShownTechniqueIDs), result.AuditID)
	if record && len(result.ShownTechniqueIDs) > 0 {
		if _, err := registry.PostFeedback(audit.ShownEvents(result, result.Characterization.Segment, contracts.SourcePull)); err == nil {
			fmt.Fprintf(os.Stderr, "[recorded %d 'shown' events]\n", len(result.ShownTechniqueIDs))
		}
	}
	return 0
}

type liveOpts struct {
	baseURL, token, email string
	offline, record       bool
	maxTurns              int
}

// auditLive attaches to a running captured session and audits each turn as it
// arrives.
func auditLive(cfg auditorconfig.Config, registry *client.Registry, seg contracts.Segment,
	sessionID string, o liveOpts) int {
	model := pickLLM(cfg, o.offline)
	n := 0
	onTurn := func(rec contracts.CanonicalRecord, _ *capture.LiveCapture) {
		n++
		char := capture.Characterize(rec)
		char.SessionHash = sessionhash.Hash(cfg.SessionSalt, "omnigent:"+rec.SessionID)
		result, err := audit.Run(capture.ToTranscriptText(rec), model, registry.GetEvidence, seg, &char)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[turn %d] cannot reach the registry: %v\n", n, err)
			return
		}
		fmt.Printf("\n======== live audit · turn %d · session %s ========\n", n, rec.SessionID)
		fmt.Println(result.AuditText)
		if o.record && len(result.ShownTechniqueIDs) > 0 {
			if _, err := registry.PostFeedback(audit.ShownEvents(result, result.Characterization.Segment, contracts.SourcePull)); err == nil {
				fmt.Fprintf(os.Stderr, "[recorded %d 'shown' events]\n", len(result.ShownTechniqueIDs))
			}
		}
	}
	fmt.Fprintf(os.Stderr, "[auditor] monitor of live session %s at %s started (press Ctrl-C to stop)\n", sessionID, o.baseURL)
	omni := &capture.OmnigentClient{BaseURL: o.baseURL, Token: o.token, Email: o.email}
	if _, err := omni.Monitor(sessionID, onTurn, nil, nil, nil, o.maxTurns); err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach the capture server at %s: %v\n", o.baseURL, err)
		return exitUnreachable
	}
	fmt.Fprintf(os.Stderr, "[auditor] the stream ended after %d turn(s).\n", n)
	return 0
}

func readAllStdin() (string, error) {
	raw := make([]byte, 0, 64*1024)
	buf := make([]byte, 32*1024)
	for {
		n, err := os.Stdin.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			return string(raw), nil
		}
	}
}

// cmdRevise proposes a reviewed update to an existing technique — the change
// lands as a revision draft (docs/design/revision-design.md), never directly on the
// serving technique. Only flags the member actually passed go in the body, so an
// empty flag can't blank a field.
func cmdRevise(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("revise", flag.ContinueOnError)
	fieldFlags := map[string]*string{ // flag name -> value; JSON key below
		"name":         fs.String("name", "", "new name"),
		"description":  fs.String("description", "", "new description"),
		"recipe":       fs.String("recipe", "", "new recipe"),
		"applies-when": fs.String("applies-when", "", "new applies-when fit condition"),
		"not-when":     fs.String("not-when", "", "new not-when fit condition"),
		"before-after": fs.String("before-after", "", "new before/after"),
		"tags":         fs.String("tags", "", "comma-separated tags (replaces the set)"),
		"note":         fs.String("note", "", "reason shown to the reviewer"),
	}
	jsonKey := map[string]string{"applies-when": "applies_when", "not-when": "not_when", "before-after": "before_after"}
	registryURL := fs.String("registry", cfg.RegistryURL, "registry URL")
	key := fs.String("key", cfg.RegistryKey, "registry API key")
	positionals, ok := parseFlags(fs, args)
	if !ok {
		return exitUsage
	}
	body := map[string]any{}
	fs.Visit(func(f *flag.Flag) {
		if v, ok := fieldFlags[f.Name]; ok {
			k := f.Name
			if mapped, ok := jsonKey[k]; ok {
				k = mapped
			}
			body[k] = *v
		}
	})
	if len(positionals) < 1 || len(body) == 0 || (len(body) == 1 && body["note"] != nil) {
		fmt.Fprintln(os.Stderr, "usage: tacit revise <technique-id> --<field> <value> [--note why] [flags]\n"+
			"fields: --name --description --recipe --applies-when --not-when --before-after --tags")
		return 2
	}
	registry := &client.Registry{BaseURL: *registryURL, APIKey: *key}
	resp, err := registry.Revise(positionals[0], body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "revise failed: %v\n", err)
		return exitUnreachable
	}
	fmt.Printf("created revision draft %v (supersedes %v at v%v); it waits for review in the drafts lane\n",
		resp["id"], resp["supersedes"], resp["base_version"])
	return 0
}

// memberSettings is what this machine points at.
//
// It travels as values. Three callers — connect, join and doctor --fix — used
// to build an argv of "--key", v and hand it back to a flag set to be parsed
// again, which meant every one of them had to know the flag spellings and a
// value could only reach here by surviving a round trip through a parser it
// never needed to enter.
type memberSettings struct {
	registryURL string
	key         string
	segment     string
	sessionSalt string
}

// applyMemberSettings writes the member's registry settings to agent.env — the
// config file every tacit process (hooks, relay, MCP, CLI) falls back to when
// the environment doesn't set TACIT_* — then verifies reachability and the key.
// With no flags it reports the currently resolved settings.
//
// It saves and verifies this machine's member settings —
// registry URL, key, cohort, session salt — or prints them when given nothing
// to change. It was `tacit setup`, which asked members to choose between two
// commands for one act: settings without wiring, or settings with it. It is a
// STEP of `tacit connect` now, and of `join` and `doctor --fix`, which all
// called it anyway.
func applyMemberSettings(set memberSettings) int {
	registryURL, key := &set.registryURL, &set.key
	segment, sessionSalt := &set.segment, &set.sessionSalt
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "no home directory: %v\n", err)
		return 1
	}
	path := auditorconfig.AgentEnvPath(home)

	if *registryURL == "" && *key == "" && *segment == "" && *sessionSalt == "" {
		cfg := auditorconfig.Load()
		keyState := "default (dev-key)"
		if cfg.RegistryKey != "dev-key" {
			keyState = "set"
		}
		saltState := "not set"
		if cfg.SessionSalt != "" {
			saltState = "set"
		}
		fmt.Printf("registry: %s\napi key:  %s\ncohort:   %s\nsession salt: %s\nsettings file: %s\n(environment variables override the file)\n",
			cfg.RegistryURL, keyState, formatSegment(auditorconfig.ParseSegment(cfg.HooksSegment)), saltState, path)
		if cfg.HooksSegment == "" {
			fmt.Println("\nNo cohort is set: the outcomes of this machine reach the org totals, but not a team's.\nBefore you name one, see the cohorts that colleagues already use:  tacit cohorts")
		}
		return 0
	}

	vals := auditorconfig.ReadEnvFile(path)
	if *registryURL != "" {
		vals["TACIT_REGISTRY_URL"] = strings.TrimRight(strings.TrimSpace(*registryURL), "/")
	}
	if *key != "" {
		vals["TACIT_API_KEY"] = strings.TrimSpace(*key)
	}
	if *segment != "" {
		vals["TACIT_HOOKS_SEGMENT"] = strings.TrimSpace(*segment)
	}
	if *sessionSalt != "" {
		vals["TACIT_SESSION_SALT"] = strings.TrimSpace(*sessionSalt)
	}
	// Default the adoption-sketch sink to the member's own registry — the
	// zero-effort org knowledge inflow (docs/mining/mining-design.md source 2;
	// the intake is /v1/sketches). Consent semantics are preserved: the sink
	// is a visible line in agent.env, announced below, and an explicitly
	// EMPTY `TACIT_SKETCH_URL=` opts out permanently (the key being present
	// means the member decided; setup never overrides a decision).
	sketchDefaulted := false
	if vals["TACIT_REGISTRY_URL"] != "" && vals["TACIT_API_KEY"] != "" {
		if _, decided := vals["TACIT_SKETCH_URL"]; !decided {
			vals["TACIT_SKETCH_URL"] = vals["TACIT_REGISTRY_URL"] + "/v1/sketches"
			vals["TACIT_SKETCH_TOKEN"] = vals["TACIT_API_KEY"]
			sketchDefaulted = true
		}
	}
	if err := writeSortedEnvFile(path, "# written by `tacit connect`; environment variables override these", vals); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
		return 1
	}
	fmt.Printf("saved %s\n", path)
	if sketchDefaulted {
		fmt.Println("adoption sketches: ON — when you adopt a suggestion, a scrubbed one-line" +
			" {situation, move} pair goes to your org registry as evidence on that technique." +
			" Transcripts never leave this machine. To opt out, set TACIT_SKETCH_URL= (empty) in the file above.")
	}

	// Verify against what was just saved: reachability, then the key.
	url := vals["TACIT_REGISTRY_URL"]
	if url == "" {
		url = auditorconfig.Load().RegistryURL
	}
	reg := &pkgclient.Registry{BaseURL: url, APIKey: vals["TACIT_API_KEY"],
		HTTP: &http.Client{Timeout: 5 * time.Second}}
	// Reachability first, and any answer counts — an error status still means
	// something is listening at that address, which is what this line reports.
	var reachErr *pkgclient.APIError
	if err := reg.Do("GET", "/v1/health", nil, nil); err != nil && !errors.As(err, &reachErr) {
		fmt.Fprintf(os.Stderr, "cannot reach the registry at %s: %v\n", url, err)
		return exitUnreachable
	}
	fmt.Printf("the registry answers at %s\n", url)
	var keyErr *pkgclient.APIError
	if err := reg.Do("GET", "/v1/techniques", nil, nil); err != nil && !errors.As(err, &keyErr) {
		fmt.Fprintf(os.Stderr, "key check failed: %v\n", err)
		return exitUnreachable
	}
	if keyErr != nil && keyErr.Status == 401 {
		fmt.Fprintf(os.Stderr, "The registry rejected the API key (401). Check the key with your %s admin.\n", product.Name())
		return exitUnreachable
	}
	fmt.Println("The registry accepted the API key. Run /tacit:status in a session to check the connection.")
	if *segment != "" {
		// The one moment a mistyped cohort is still cheap to fix: the member
		// is looking at the command they just ran. Silent when the registry
		// cannot answer — a cohort report must never turn a good save bad.
		reportCohortFit(url, vals["TACIT_API_KEY"], auditorconfig.ParseSegment(*segment))
	}
	return 0
}

// cmdSuggest triggers one registry-side suggestion pass (research needs
// TACIT_LLM_API_KEY on the registry service; the /tacit:suggest skill has a
// harness-side fallback when it is absent).
func cmdSuggest(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("suggest", flag.ContinueOnError)
	n := fs.Int("n", 10, "how many drafts to request (max 10)")
	registryURL := fs.String("registry", cfg.RegistryURL, "registry URL")
	key := fs.String("key", cfg.RegistryKey, "registry API key")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}
	registry := &client.Registry{BaseURL: *registryURL, APIKey: *key,
		HTTP: &http.Client{Timeout: 6 * time.Minute}} // research runs web searches
	resp, err := registry.Suggest(*n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "suggest failed: %v\n", err)
		return exitUnreachable
	}
	created, _ := resp["created"].([]any)
	if len(created) == 0 {
		fmt.Println("no new drafts: all the researched practices exist already")
		return 0
	}
	fmt.Printf("created %d draft(s). Review them at <registry>/drafts:\n", len(created))
	for _, id := range created {
		fmt.Printf("  %v\n", id)
	}
	return 0
}

func cmdFeedback(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("feedback", flag.ContinueOnError)
	stage := fs.String("stage", "", "shown|adopted|helped|dismissed (required; helped also records adopted, so do not send both)")
	reason := fs.String("reason", "", "for --stage dismissed: not-relevant|already-knew|didnt-work")
	segment := fs.String("segment", "", "comma list, e.g. role=developer,domain=...")
	auditID := fs.String("audit-id", "", "link this to an earlier audit")
	rank := fs.Int("rank", 0, "rank the suggestion held in that audit")
	taskType := fs.String("task-type", "", "task type")
	registryURL := fs.String("registry", cfg.RegistryURL, "registry URL")
	key := fs.String("key", cfg.RegistryKey, "registry API key")
	noRecompute := fs.Bool("no-recompute", false, "skip the rollup recompute after the post")
	positionals, ok := parseFlags(fs, args)
	if !ok {
		return exitUsage
	}
	if len(positionals) < 1 || *stage == "" {
		fmt.Fprintln(os.Stderr, "usage: tacit feedback <technique-id> --stage <stage> [flags]")
		return 2
	}
	events := audit.ManualFeedbackEvents(positionals[0], *stage,
		contracts.Segment(auditorconfig.ParseSegment(*segment)), *reason, *rank, *auditID, *taskType, "explicit")
	registry := &client.Registry{BaseURL: *registryURL, APIKey: *key}
	resp, err := registry.PostFeedback(events)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach the registry at %s: %v\n", *registryURL, err)
		return exitUnreachable
	}
	if !*noRecompute {
		_, _ = registry.Recompute()
	}
	stages := ""
	for i, e := range events {
		if i > 0 {
			stages += ", "
		}
		stages += e.Stage
	}
	accepted := len(events)
	if v, ok := resp["accepted"].(float64); ok {
		accepted = int(v)
	}
	suffix := "; recomputed the rollups"
	if *noRecompute {
		suffix = ""
	}
	fmt.Printf("recorded %d event(s) [%s] for %s%s\n", accepted, stages, positionals[0], suffix)
	return 0
}

// --- hooks --------------------------------------------------------------------

func cmdServeHooks(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("serve-hooks", flag.ContinueOnError)
	host := fs.String("host", cfg.HooksHost, "bind host")
	port := fs.Int("port", cfg.HooksPort, "bind port")
	hooksKey := fs.String("hooks-key", cfg.HooksAPIKey, "X-Tacit-Key the hook config must send")
	segment := fs.String("segment", cfg.HooksSegment, "cohort stamped on this member, e.g. team=revops,role=analyst")
	registryURL := fs.String("registry", cfg.RegistryURL, "registry URL")
	key := fs.String("key", cfg.RegistryKey, "registry API key")
	offline := fs.Bool("offline", false, "force the heuristic LLM")
	idleExit := fs.Int("idle-exit", cfg.HooksIdleExitSecs, "stop after this many idle seconds (0 = stay up)")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	// Auto (not pickLLM): the agent is long-running, so it resolves the key
	// live — a key added to the key file after startup upgrades synthesis
	// with no restart.
	model := &llm.Auto{
		Resolve: func() string { return llm.ResolveKey(cfg.LLMKeyFile) },
		Offline: *offline,
		KeyFile: cfg.LLMKeyFile,
		Base:    llmConfig(cfg, ""),
	}
	if !*offline && llm.ResolveKey(cfg.LLMKeyFile) == "" {
		fmt.Fprintf(os.Stderr, "[auditor] "+product.Name()+" uses the offline heuristic. Add TACIT_LLM_API_KEY=... to %s; the agent detects it automatically.\n",
			cfg.LLMKeyFile)
	}

	// A tighter client than the package default (30s): every registry call the
	// agent makes is off the hook path, but retrieval holds the session's
	// one-suggestion-in-flight reservation while it runs, so a hung registry
	// should release it in seconds, not half a minute. Member-visible wait is
	// bounded separately by SynthBudget (agent.auditAtStop).
	registry := &client.Registry{BaseURL: *registryURL, APIKey: *key,
		HTTP: &http.Client{Timeout: 10 * time.Second}}
	agent := hooks.NewAgent(registry.GetEvidence, model,
		func(events []contracts.FeedbackEventDraft) error {
			_, err := registry.PostFeedback(events)
			return err
		},
		registry.Contribute,
		hooks.Options{
			MaxPerWindow:  cfg.HooksMaxPerWindow,
			Window:        time.Duration(cfg.HooksWindowHours * float64(time.Hour)),
			CooldownTurns: cfg.HooksSuggestionCooldown,
			CooldownFor:   time.Duration(cfg.HooksCooldownMinutes * float64(time.Minute)),
			MaxFitChecks:  cfg.HooksMaxFitChecks,
			SynthBudget:   time.Duration(cfg.HooksSynthBudgetSecs * float64(time.Second)),
			Segment:       contracts.Segment(auditorconfig.ParseSegment(*segment)),
			Sketch:        sketchSink(cfg),
			SketchSalt:    cfg.SketchSalt,
			SessionSalt:   cfg.SessionSalt,
			Drafts:        registry.DraftsCount,
			Access: func() (string, string, error) {
				h, err := registry.Health()
				return h.Access, h.Tunnel, err
			},
			Cohorts:             cohortDims(registry),
			Enrich:              registry.EnrichAuditFact,
			TechniqueMemoryPath: cfg.TechniqueMemoryPath,
			Paused:              func() bool { return auditorconfig.Paused(cfg.PausePath) },
			UsageLogPath:        cfg.UsageLogPath,
			StateDir:            cfg.StateDir,
			// "dev-key" is the compiled-in default: it means nobody ever ran
			// setup/join on this machine, which is exactly who the repo-marker
			// join nudge exists for.
			RegistryConfigured: *key != "dev-key",
			RegistryURL:        *registryURL,
			PublishLedger:      ledgerPublisher(registry, *key),
			AskBudget:          time.Duration(cfg.HooksAskBudgetSecs * float64(time.Second)),
			MentionBlock:       cfg.HooksMentionBlock,
		})
	if err := hooks.Serve(context.Background(), agent, *host, *port, *hooksKey, *idleExit); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

// ledgerPublisher builds the sink that files this machine's summaries in the
// member's sealed ledger, or nil when there is nothing to file them under.
//
// "dev-key" is the compiled-in default, which means nobody ever ran connect or
// join here. Deriving an address from it would put every unconfigured machine
// in the world at the SAME address, all of them able to open each other's
// blobs — so an unjoined machine publishes nothing, and that is a correctness
// rule rather than a nicety.
//
// The derivation happens once, here, and the key stays in this closure: the
// agent never holds it, the client never sees it, and neither could open what
// gets posted.
func ledgerPublisher(registry *client.Registry, memberKey string) func([]byte) error {
	if memberKey == "" || memberKey == "dev-key" {
		return nil
	}
	key, id, err := ledger.Derive(memberKey)
	if err != nil {
		return nil
	}
	machine := hooks.MachineLabel()
	return func(payload []byte) error {
		sealed, err := ledger.Seal(key, payload)
		if err != nil {
			return err
		}
		err = registry.PublishLedger(id, machine, sealed)
		if client.LedgerUnavailable(err) {
			// A registry too old to have a ledger is not a fault on this
			// machine, and saying so every ten minutes would be noise a member
			// can do nothing about.
			return nil
		}
		return err
	}
}

// sketchSink builds the opt-in sketch poster: nil (consent off) unless a
// miner intake URL is configured. Fire-and-forget with a short timeout — a
// slow miner can never matter to a session.
func sketchSink(cfg auditorconfig.Config) hooks.SketchSink {
	if cfg.SketchURL == "" {
		return nil
	}
	httpClient := &http.Client{Timeout: 5 * time.Second}
	return func(sk contracts.Sketch) {
		raw, err := json.Marshal(sk)
		if err != nil {
			return
		}
		req, err := http.NewRequest("POST", cfg.SketchURL, bytes.NewReader(raw))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.SketchToken != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.SketchToken)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return // best-effort by design
		}
		_ = resp.Body.Close()
	}
}

// mcpLoopbackHost maps the registry bind host to an address the process can
// dial itself on: a wildcard bind (0.0.0.0 / ::) isn't a connectable target, so
// fall back to loopback.
func mcpLoopbackHost(host string) string {
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return host
	}
}

// localUsageFunc renders the member's usage app straight from the co-located
// log — the same renderer the `tacit usage` CLI uses. Wired on the stdio plugin
// (always the member's own machine) and on a self-hosted registry endpoint.
func localUsageFunc(logPath string) mcp.UsageFunc {
	return func(window string) (string, string, map[string]any, error) {
		return hooks.RenderUsageApp(logPath, window)
	}
}

// remoteUsageFunc is the registry /mcp endpoint's tacit_usage source. Usage is
// member-local and this endpoint carries no member identity, so it can only
// serve the machine it runs on: opt in (TACIT_LOCAL_USAGE=1) on a self-hosted,
// single-member registry — where that machine IS the member's — to render the
// co-located log as the app; otherwise return a "usage is local" pointer app, so
// a shared registry shows the panel affordance without leaking the host's usage.
func remoteUsageFunc(logPath string) mcp.UsageFunc {
	if os.Getenv("TACIT_LOCAL_USAGE") == "1" {
		return localUsageFunc(logPath)
	}
	return func(window string) (string, string, map[string]any, error) {
		return remoteUsagePointerHTML(), remoteUsagePointerText(),
			map[string]any{"local_only": true}, nil
	}
}

// Functions rather than constants: the product name is a runtime setting
// (internal/product), and a const would freeze whatever it said at build time.
func remoteUsagePointerText() string {
	return "Your " + product.Name() + " usage is personal. It stays on your own machine — " +
		"the registry never collects it, so this shared endpoint cannot show it here. To see it, use the " + product.Name() + " " +
		"plugin on your own machine (its local tacit_usage renders it from your log), or run `tacit usage` there."
}

func remoteUsagePointerHTML() string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"><title>Your ` + product.Name() + ` usage</title>` +
		`<style>body{margin:0;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;color:#0b0b0b;background:#f9f9f7}` +
		`@media(prefers-color-scheme:dark){body{color:#f2f1ee;background:#191917}}` +
		`.panel{max-width:560px;margin:24px auto;padding:20px 22px;border:1px solid rgba(128,128,128,.28);border-radius:12px}` +
		`h1{font-size:17px;margin:0 0 .5rem}p{margin:.45rem 0;opacity:.85}code{font-size:.9em;padding:.05rem .3rem;border-radius:4px;background:rgba(128,128,128,.16)}</style></head>` +
		`<body><div class="panel"><h1>Your usage stays on your machine</h1>` +
		`<p>` + product.Name() + ` keeps your personal usage on the computer where your agent runs — the registry never collects it, so this shared endpoint cannot show it.</p>` +
		`<p>To see it, use the ` + product.Name() + ` plugin on your own machine: its local <code>tacit_usage</code> renders your usage from the local log. Or run <code>tacit usage</code> there.</p></div></body></html>`
}

// newMCPServer wires an mcp.Server to a registry (over HTTP), exposing
// tacit_search + the tacit_insights app. Used both by the stdio server
// (cmdMCP) and the registry's own remote endpoint (POST /mcp), which points it
// at the local loopback so a networked harness gets identical behavior without
// the local plugin.
func newMCPServer(registryURL, registryKey string, segment contracts.Segment) *mcp.Server {
	registry := &client.Registry{BaseURL: registryURL, APIKey: registryKey}
	return &mcp.Server{
		Search: registry.GetEvidence,
		// A tacit_search PULL emits no funnel event — `shown` is reserved for what
		// a member actually sees (see mcp.Server.search). So the MCP server needs
		// no feedback writer.
		Insights: func(window string) (string, string, map[string]any, error) {
			app, err := registry.GetInsightsApp(window)
			if err != nil {
				return "", "", nil, err
			}
			return app.HTML, app.Text, app.Summary, nil
		},
		Org: func(window string) (string, map[string]any, error) {
			app, err := registry.GetOrganizationApp(window)
			if err != nil {
				return "", nil, err
			}
			return app.Text, app.Summary, nil
		},
		Map: func() (string, string, map[string]any, error) {
			app, err := registry.GetMapApp()
			if err != nil {
				return "", "", nil, err
			}
			return app.HTML, app.Text, app.Summary, nil
		},
		Drafts: func() (string, string, map[string]any, error) {
			app, err := registry.GetReviewApp()
			if err != nil {
				return "", "", nil, err
			}
			return app.HTML, app.Text, app.Summary, nil
		},
		DraftAction: func(id, action string) (string, error) {
			status := "stable"
			if action == "reject" {
				status = "retired"
			}
			resp, err := registry.Promote(id, status)
			if err != nil {
				var se *client.StatusError
				if errors.As(err, &se) && (se.Code == 401 || se.Code == 403) {
					return "", fmt.Errorf("the registry refused this key for draft decisions (%d) — draft decisions need the admin key; ask your %s admin or use the dashboard", se.Code, product.Name())
				}
				return "", err
			}
			if action == "reject" {
				return fmt.Sprintf("Rejected %s — %s keeps it and does not erase it; it no longer waits in the queue.", id, product.Name()), nil
			}
			return fmt.Sprintf("Promoted %s to %v — it serves members from now on and starts to gather evidence.", id, resp["status"]), nil
		},
		// The write half of the connector tier (mcp-first-plan.md phase 1). A
		// member who reached this registry by adding one URL to their chat tool
		// could draw on the org's evidence and produce none: search and metrics
		// were the whole surface, so an MCP-only population consumed a corpus it
		// could not feed. Both land through the same endpoints a wired harness
		// uses, so the drafts queue and the ranking see one kind of record.
		Contribute: func(c mcp.Contribution) (string, error) {
			body := map[string]any{
				"name": c.Name, "description": c.Description, "recipe": c.Recipe,
				"applies_when": c.AppliesWhen, "not_when": c.NotWhen,
				"tags": c.Tags, "task_types": c.TaskTypes,
				"segment": segment,
			}
			if c.Scope == "org" || c.Scope == "general" {
				body["scope"] = c.Scope
			}
			resp, err := registry.Contribute(body)
			if err != nil {
				return "", err
			}
			id, _ := resp["id"].(string)
			return fmt.Sprintf("Filed %q as a draft (%s). It reaches nobody until a reviewer promotes it, "+
				"on the %s Review page.", c.Name, id, product.Name()), nil
		},
		// Explicit, and marked as such. In a coding harness these stages are
		// inferred from what the member does; here the member said it, which is
		// a different class of signal and the signal-trust view calibrates the
		// two separately.
		Feedback: func(techniqueID, stage, reason string) (string, error) {
			ev := contracts.FeedbackEventDraft{
				TechniqueID: techniqueID, Stage: stage, Segment: segment,
				Confidence: "explicit", Source: "pull",
			}
			if reason != "" {
				ev.Value = reason
			}
			if _, err := registry.PostFeedback([]contracts.FeedbackEventDraft{ev}); err != nil {
				return "", err
			}
			return fmt.Sprintf("Recorded: %s %s. It counts toward what your colleagues see ranked, in aggregate — "+
				"never against you.", techniqueID, stage), nil
		},
		Segment: segment,
	}
}
