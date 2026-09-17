// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit doctor — install-plan Phase C: one command that says whether this
// machine's OpenTacit pieces are actually working, in both roles. Registry checks
// run when registry.env exists (this host operates a registry); member checks
// always run (every member has an agent.env or needs telling so).
// `--harness <name>` additionally fires a synthetic event through the real
// relay → agent → registry path and names the failing hop. Read-only by
// default; the one mutation is opt-in and interactive (`--fix` re-keys after
// a rejected member key, via the same setup flow connect uses).

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/hooks"
	"github.com/opentacit/tacit/internal/product"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	pkgclient "github.com/opentacit/tacit/pkg/client"
)

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	harness := fs.String("harness", "", "one harness: "+harnessNames()+" — checks its hook path end to end, or targets --deliver")
	fix := fs.Bool("fix", false, "offer interactive repairs (currently: re-key after a rejected member key)")
	probe := fs.Bool("probe", false, "liveness only: one request to the local registry's /v1/health, exit 0 or 1 (for container health checks and probes)")
	probeURL := fs.String("url", "", "the health endpoint --probe checks (default: this machine's registry)")
	deliver := fs.Bool("deliver", false, "arm the next turn to deliver one ◆ "+product.Name()+" block, so you can see whether suggestions RENDER where you sit")
	deliverStatus := fs.Bool("deliver-status", false, "report what became of the last armed delivery, and arm nothing")
	ready := fs.Bool("ready", false, "one answer: can this machine's AI tool reach the playbook? Quiet when it can; the full report only when it cannot")
	deliverContext := fs.Bool("context", false, "with --deliver: test the model-facing channel instead (the next prompt carries a diagnostic additionalContext)")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	// The two narrow modes answer their own question and return; everything
	// below is the report. A probe in particular must stay one request — a
	// supervisor runs it every 30 seconds to decide whether to kill a process.
	if *probe {
		return probeHealth(orElseStr(*probeURL, defaultProbeURL()))
	}
	if *deliver || *deliverStatus {
		return runDelivery(*harness, *deliverStatus, *deliverContext)
	}
	if *ready {
		return runReady(*harness)
	}

	c := &checker{}
	hc := &http.Client{Timeout: 5 * time.Second}

	fmt.Printf("tacit %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
	if latest := latestReleaseTag(2 * time.Second); latest != "" && latest != version {
		c.info("release %s is available (tacit upgrade)", latest)
	}

	// --- registry role ------------------------------------------------------
	regEnv := registryconfig.RegistryEnvPath()
	if st, err := os.Stat(regEnv); err == nil {
		fmt.Println("\nregistry (this host has a registry.env):")
		if perm := st.Mode().Perm(); perm != 0o600 {
			c.warn("%s is mode %04o — must be 0600 (it holds the API key)", regEnv, perm)
		} else {
			c.ok("registry.env present, mode 0600")
		}
		cfg := registryconfig.Load()
		health, err := fetchHealth(hc, fmt.Sprintf("http://127.0.0.1:%d", cfg.Port))
		liveCallback := ""
		if err == nil {
			liveCallback = callbackFor(health.ExternalURL, cfg.BasePath)
		}
		if err != nil {
			c.fail("registry does not answer on :%d (%v)%s", cfg.Port, err, systemdHint())
		} else {
			ver := health.Version
			if ver == "" {
				ver = "pre-version build"
			}
			c.ok("registry healthy on :%d (%s, %d techniques, %d events)", cfg.Port, ver, health.Techniques, health.Events)
			// The silent-fallback trap: configured for onnx, serving lexical.
			// embed_degraded is the registry's own admission (works from any
			// machine); the local-config comparison stays as the fallback for
			// registries that predate the field.
			switch {
			case health.EmbedDegraded:
				c.fail("configured for %s but serves %s — the onnx fallback fired; check the service log for 'embedder ... unavailable'", health.EmbedWanted, health.EmbedModel)
			case health.EmbedModel == "":
				c.info("registry predates the embed_model health field — %s cannot check the embedder remotely", product.Name())
			case health.EmbedModel == "hashing-v1" && cfg.EmbedModel != "hashing-v1":
				c.fail("configured for %s but serves hashing-v1 — the onnx fallback fired; check the service log for 'embedder ... unavailable'", cfg.EmbedModel)
			case health.EmbedModel == "hashing-v1":
				c.warn("retrieval is lexical (hashing-v1) — `tacit init --embeddings on` enables semantic ranking")
			default:
				c.ok("semantic retrieval live (%s)", health.EmbedModel)
			}
			if health.Drafts > 0 {
				c.info("%d draft(s) wait for review", health.Drafts)
			}
			checkKey(c, hc, fmt.Sprintf("http://127.0.0.1:%d", cfg.Port), cfg.APIKey, "registry.env key")
		}
		// What this registry exposes. It runs whether or not the service
		// answers: an open dashboard is a property of the configuration, and
		// the operator should hear it on the run where they are already
		// looking, not on the day someone else finds it.
		registrySecurityChecks(c, cfg, liveCallback)
	}

	// --- member role --------------------------------------------------------
	fmt.Println("\nmember:")
	home, _ := os.UserHomeDir()
	agentEnv := auditorconfig.AgentEnvPath(home)
	acfg := auditorconfig.Load()
	if _, err := os.Stat(agentEnv); err == nil {
		c.ok("agent.env present (%s)", agentEnv)
	} else {
		c.warn("no agent.env — run: tacit connect --registry <url> --key <key>")
	}
	if acfg.RegistryKey == "dev-key" {
		c.warn("API key is the compiled-in dev-key default")
	}
	if health, err := fetchHealth(hc, acfg.RegistryURL); err != nil {
		c.fail("cannot reach the registry at %s (%v)", acfg.RegistryURL, err)
	} else {
		c.ok("the registry answers at %s", acfg.RegistryURL)
		if health.EmbedDegraded {
			c.warn("the registry reports degraded retrieval (it wants %s, it serves %s) — tell its operator", health.EmbedWanted, health.EmbedModel)
		}
		if rejected := checkKey(c, hc, acfg.RegistryURL, acfg.RegistryKey, "agent.env key"); rejected {
			if *fix {
				fixRejectedKey(acfg.RegistryURL)
			} else {
				c.info("to repair a rotated key, run: tacit doctor --fix")
			}
		}
	}
	// The hook agent idle-exits by design; absence is not a fault.
	agentBase := fmt.Sprintf("http://%s:%d", acfg.HooksHost, acfg.HooksPort)
	if stats, err := fetchAgentStats(hc, agentBase); err != nil {
		c.info("hook agent not up on :%d (it starts on demand — normal when idle)", acfg.HooksPort)
	} else {
		c.ok("hook agent up on :%d (%d session(s), %d shown, %d adopted)",
			acfg.HooksPort, stats.Sessions, stats.Shown, stats.Adopted)
		// The model gets its own line whether or not it is healthy. A broken
		// model does not break retrieval or recording — it stops SUGGESTIONS,
		// and the resulting silence is the same silence as a quiet day, which
		// is why it has to be stated rather than inferred from a zero.
		reportModel(c, stats)
	}

	if *harness != "" {
		doctorHarness(c, hc, *harness, acfg)
	} else {
		doctorAllWiring(c)
	}

	fmt.Println()
	switch {
	case c.fails > 0:
		fmt.Printf("%d check(s) FAILED, %d warning(s)\n", c.fails, c.warns)
		return 1
	case c.warns > 0:
		fmt.Printf("no failures, %d warning(s)\n", c.warns)
	default:
		fmt.Println("all checks passed")
	}
	return 0
}

// doctorAllWiring is hop 1 for every harness on this machine.
//
// It reported "all checks passed" on a machine where the key was good, the
// agent was up, and not one harness was wired — which is every machine where
// `tacit connect` printed a todo, and Claude Code's plugin install is a todo
// whenever the claude CLI is not on PATH. The member's whole verification step
// said yes while nothing could deliver anything.
//
// The deep probe stays behind --harness: it spawns an agent and posts synthetic
// events, which is too much to do to nine harnesses on every run. What belongs
// in the default report is the cheap half — does this harness's own config
// invoke the relay, and does the binary it names still exist — because that is
// the half that answers "did connect finish?".
func doctorAllWiring(c *checker) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	pluginsDir := filepath.Join(registryconfig.DataHome(), "plugins")
	found := false
	for _, def := range harnesses {
		if !def.installed(home) {
			continue
		}
		if !found {
			fmt.Println("\nharness wiring:")
			found = true
		}
		def.wiring(c, home, pluginsDir)
	}
	if !found {
		fmt.Println("\nharness wiring:")
		c.info("no supported AI tool found on this machine (%s)", harnessNamesProse())
	} else {
		c.info("hooks end to end for one of them: tacit doctor --harness <name>")
	}
}

// agentStats is the running hook agent's own account of itself: how it is
// finding the registry and the model, and what it has delivered. Parsed in one
// place because two callers need it — the member section of every run, and the
// per-harness hop check.
type agentStats struct {
	Sessions int `json:"sessions"`
	Shown    int `json:"shown"`
	Adopted  int `json:"adopted"`
	Registry *struct {
		State   string `json:"state"`
		Remedy  string `json:"remedy"`
		LastOK  string `json:"last_ok_at"`
		LastErr string `json:"last_error_at"`
	} `json:"registry"`
	LLM *struct {
		State         string `json:"state"`
		NeedsOperator bool   `json:"needs_operator"`
		Remedy        string `json:"remedy"`
	} `json:"llm"`
}

func fetchAgentStats(hc *http.Client, base string) (agentStats, error) {
	var stats agentStats
	resp, err := hc.Get(base + "/v1/hooks/stats")
	if err != nil {
		return stats, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return stats, fmt.Errorf("unparseable: %w", err)
	}
	return stats, nil
}

// reportModel states the model's condition in the member's terms. Faults that
// clear on their own (a rate limit, an overload) are said once and not raised
// as alarms; faults that need a person get the remedy the agent diagnosed,
// quoted rather than paraphrased.
func reportModel(c *checker, stats agentStats) {
	switch {
	case stats.LLM == nil || stats.LLM.State == "" || stats.LLM.State == "unknown":
		// The agent reports "unknown" until its first fit-check, and it
		// idle-exits often enough that a fresh one is the common case. Not a
		// fault, and not evidence of health either.
		c.info("this agent has not used the model yet (the first fit-check runs at the end of a turn)")
	case stats.LLM.State == "ok":
		c.ok("model answers — suggestions get a fit-check")
	case stats.LLM.NeedsOperator:
		c.fail("model: %s — %s (%s will deliver no suggestions until you fix this)",
			stats.LLM.State, stats.LLM.Remedy, product.Name())
	default:
		c.info("model: %s — clears on its own", stats.LLM.State)
	}
}

// healthInfo is the shared typed /v1/health. Named locally because doctor and
// secure read it under this name.
type healthInfo = pkgclient.HealthInfo

func fetchHealth(hc *http.Client, base string) (healthInfo, error) {
	h, err := (&pkgclient.Registry{BaseURL: base, HTTP: hc}).GetHealth()
	var api *pkgclient.APIError
	if errors.As(err, &api) {
		// The same sentence a raw response gave: "health returned 503 Service
		// Unavailable". Members grep doctor's lines, so the wording stays.
		return healthInfo{}, fmt.Errorf("health returned %d %s", api.Status, http.StatusText(api.Status))
	}
	return h, err
}

// checkKey verifies a key against /v1/techniques; reports rejected=true on a 401
// so callers can offer recovery (doctor --fix).
func checkKey(c *checker, hc *http.Client, base, key, label string) (rejected bool) {
	// The body is not read into anything: this asks whether the registry
	// accepts the key, and a registry with a large library should not be
	// downloaded to answer it.
	err := (&pkgclient.Registry{BaseURL: base, APIKey: key, HTTP: hc}).Do("GET", "/v1/techniques", nil, nil)
	var api *pkgclient.APIError
	if err != nil && !errors.As(err, &api) {
		c.fail("%s: key check failed (%v)", label, err)
		return false
	}
	if api != nil && api.Status == http.StatusUnauthorized {
		c.fail("the registry rejected the %s (401) — check it with the registry operator", label)
		return true
	}
	// Any other answer, including a 5xx, counts as accepted — this check is
	// about the key, and only a 401 is the registry saying no to it.
	c.ok("the registry accepted the %s", label)
	return false
}

// fixRejectedKey is doctor --fix's one recovery action: a rejected member key
// (rotation, revocation) becomes a paste-one-line repair instead of a support
// thread. The key is read from the terminal, unechoed — never an argument,
// never a log line, never scrollback — and handed to the same setup flow
// connect uses.
func fixRejectedKey(registryURL string) {
	p := &prompter{sc: bufio.NewScanner(os.Stdin), interactive: stdinIsTerminal()}
	key := p.askSecret(fmt.Sprintf("paste a new member key for %s (from its /members page)", registryURL))
	if key == "" {
		fmt.Println("you entered no key; nothing changed")
		return
	}
	_ = applyMemberSettings(memberSettings{registryURL: registryURL, key: key})
}

// doctorHarness verifies one harness's hook path end to end, in hop order —
// wiring → relay/agent → event round-trip → the agent's own view of the
// registry — so a member who "stopped getting suggestions" learns the exact
// hop that died instead of re-checking everything by hand
// (docs/distribution/connect-plan.md). The synthetic events are a
// SessionStart/SessionEnd pair: a real round-trip through the same endpoint
// the harness posts to, with nothing audited and no state left behind.
func doctorHarness(c *checker, hc *http.Client, name string, acfg auditorconfig.Config) {
	if _, known := findHarness(name); !known {
		c.fail("unknown harness %q (%s)", name, harnessNames())
		return
	}
	c.header("\nhook path (%s): wiring → relay → agent → registry", name)
	home, _ := os.UserHomeDir()
	pluginsDir := filepath.Join(registryconfig.DataHome(), "plugins")

	// Hop 1 — wiring: the harness's config must invoke the relay, and the
	// binary path baked into it must still exist (a moved binary breaks hooks
	// with no error anywhere — this is the only place that notices).
	doctorWiring(c, name, home, pluginsDir)

	// Hop 2 — relay → agent: the same connect-or-spawn a real hook performs.
	base := fmt.Sprintf("http://%s:%d", acfg.HooksHost, acfg.HooksPort)
	check := func(b string) bool {
		resp, err := hc.Get(b + "/v1/hooks/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	relayCfg := hooks.RelayConfig{BaseURL: base, APIKey: acfg.HooksAPIKey, SpawnLog: acfg.HooksSpawnLog}
	if !hooks.EnsureUp(base, check, func() error { return hooks.SpawnAgent(relayCfg) }, time.Sleep, 8) {
		c.fail("hook agent did not come up on %s — check the spawn log (%s)", base, acfg.HooksSpawnLog)
		return
	}
	c.ok("hook agent up on %s", base)

	// Hop 3 — a real event round-trip through the harness endpoint.
	sid := fmt.Sprintf("tacit-doctor-%d", os.Getpid())
	send := func(event string) (int, error) {
		body := fmt.Sprintf(`{"hook_event_name":%q,"session_id":%q}`, event, sid)
		req, err := http.NewRequest("POST", base+"/v1/hooks/"+name, strings.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		if acfg.HooksAPIKey != "" {
			req.Header.Set("X-Tacit-Key", acfg.HooksAPIKey)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}
	code, err := send("SessionStart")
	switch {
	case err != nil:
		c.fail("event POST to the agent failed (%v)", err)
		return
	case code == http.StatusUnauthorized:
		c.fail("agent rejected the event (401) — its --hooks-key does not match this machine's TACIT_HOOKS_API_KEY")
		return
	case code != http.StatusOK:
		c.fail("event POST returned %d", code)
		return
	}
	_, _ = send("SessionEnd") // clean up the synthetic session
	c.ok("agent accepted the synthetic SessionStart (POST /v1/hooks/%s)", name)

	// Hop 4 — the RUNNING agent's view of the registry. A long-lived agent
	// spawned before a key rotation fails here while the direct checks above
	// pass — exactly the divergence this hop exists to catch.
	stats, err := fetchAgentStats(hc, base)
	if err != nil {
		c.fail("agent stats unreadable (%v)", err)
		return
	}
	switch {
	case stats.Registry == nil || stats.Registry.State == "unknown":
		c.info("agent has not retrieved yet this run (the first retrieval fires at the end of a turn); the direct registry checks above stand")
	case stats.Registry.State == "ok":
		c.ok("agent ↔ registry healthy (last ok %s)", stats.Registry.LastOK)
	default:
		c.fail("agent ↔ registry: %s (last error %s) — %s", stats.Registry.State, stats.Registry.LastErr, stats.Registry.Remedy)
	}
	if stats.LLM != nil && stats.LLM.NeedsOperator {
		c.warn("agent LLM: %s — %s", stats.LLM.State, stats.LLM.Remedy)
	}
}

// doctorWiring checks hop 1 per harness: the harness config references the
// relay and the baked-in binary path exists. Each check mirrors what `tacit
// connect` writes for that harness, and lives beside it in the harness table
// (harnesses.go). A name no harness claims checks nothing — doctorHarness
// rejects it before this point.
func doctorWiring(c *checker, name, home, pluginsDir string) {
	if def, known := findHarness(name); known {
		def.wiring(c, home, pluginsDir)
	}
}

// relayCmdRe pulls the binary path out of a wired hook command, e.g.
// "/home/x/.local/bin/tacit hook-relay claude-code" -> "/home/x/.local/bin/tacit".
//
// The backtick in the exclusion class is load-bearing. The TypeScript plugins
// describe themselves in prose — "fall back to spawning `tacit hook-relay
// opencode`" — and without it the capture ran back through the opening
// backtick and reported a binary called "`tacit" that had gone missing.
var relayCmdRe = regexp.MustCompile("([^\\s\"'`\\\\]+)\\s+hook-relay")

// checkRelayBinaryIn verifies the binary a config file's relay command points
// at can still be found — the moved-binary failure is silent everywhere else.
//
// A bare name is a PATH lookup, not a path. The opencode and pi plugins
// resolve the binary at run time (`env(["TACIT_BIN"], "tacit")`), so what
// settles the question for them is whether a shell would find `tacit`, not
// whether a file happens to sit at that name beside doctor's working
// directory — which it never does, and which reported a healthy plugin as
// broken.
func checkRelayBinaryIn(c *checker, path, content, marker string) {
	if !strings.Contains(content, marker) {
		return // caller already judged wiring presence; nothing to extract
	}
	m := relayCmdRe.FindStringSubmatch(content)
	if m == nil {
		return
	}
	bin := m[1]
	if !strings.ContainsRune(bin, filepath.Separator) {
		resolved, err := exec.LookPath(bin)
		if err != nil {
			c.fail("%s runs %s, which is not on PATH — run `tacit connect` again, or set TACIT_BIN to the binary", path, bin)
			return
		}
		c.ok("relay binary on PATH (%s → %s)", bin, resolved)
		return
	}
	if _, err := os.Stat(bin); err != nil {
		c.fail("%s runs %s, which no longer exists — run `tacit connect` again", path, bin)
		return
	}
	c.ok("relay binary exists (%s)", bin)
}

// checkRelayBinary walks a plugin tree for the hooks file carrying the marker
// (Claude Code's installed copy lives wherever its plugin system put it).
func checkRelayBinary(c *checker, root, marker string) {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found || d.IsDir() || d.Name() != "hooks.json" {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil || !strings.Contains(string(raw), marker) {
			return nil
		}
		found = true
		checkRelayBinaryIn(c, path, string(raw), marker)
		return nil
	})
	if !found {
		c.warn("no installed hooks.json under %s contains %q — a hook can fail to fire; install the plugin again", root, marker)
	}
}

func systemdHint() string {
	if inContainer() {
		return " — this runs in a container; check `docker logs` on the host"
	}
	if runtime.GOOS != "linux" {
		return ""
	}
	out, err := exec.Command("systemctl", "--user", "is-active", "tacit-registry.service").Output()
	if err != nil {
		return " — service inactive; try: systemctl --user start tacit-registry"
	}
	return fmt.Sprintf(" — systemd reports the unit %s", strings.TrimSpace(string(out)))
}
