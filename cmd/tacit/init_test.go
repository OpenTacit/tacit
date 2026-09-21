// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/merge"
	"github.com/opentacit/tacit/internal/product"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

// initEnv points init at a settings file and a data home of its own, so the
// command runs for real without touching the machine it runs on — and with
// nobody at the keyboard, so it configures and returns rather than serving.
func initEnv(t *testing.T, content string) string {
	t.Helper()
	nobodyWatching(t)
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "share"))
	path := filepath.Join(dir, "registry.env")
	if content != "" {
		writeFileForInit(t, path, content)
	}
	t.Setenv("TACIT_REGISTRY_ENV", path)
	return path
}

func writeFileForInit(t *testing.T, path, content string) {
	t.Helper()
	if err := writeSortedEnvFile(path, "# test", parseEnvContent(content)); err != nil {
		t.Fatal(err)
	}
}

func parseEnvContent(content string) map[string]string {
	vals := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k != "" {
			vals[k] = v
		}
	}
	return vals
}

// The default that makes `tacit init` the only way in: a registry nobody can
// sign into is a registry nobody can administer, so a fresh one gets an owner
// without being asked for a flag.
func TestInitGivesAFreshRegistryAnOwner(t *testing.T) {
	path := initEnv(t, "")
	if code := cmdInit([]string{"--service", "none", "--embeddings", "off", "--global-access", "off"}); code != 0 {
		t.Fatalf("tacit init exited %d", code)
	}
	got := readEnv(t, path)
	if !strings.Contains(got, "TACIT_AUTH_MODE=owner") || !strings.Contains(got, "TACIT_OWNER_SECRET=") {
		t.Errorf("a fresh registry came up with no way to sign in:\n%s", got)
	}
	if !registryconfig.Load().SingleMember() {
		t.Error("the registry does not report as one member's")
	}
}

// And the other half of auto: a registry that already has an identity provider
// must not acquire a second way in because somebody re-ran setup.
func TestInitLeavesARegistryWithAProviderAlone(t *testing.T) {
	path := initEnv(t, strings.Join([]string{
		"TACIT_AUTH_MODE=oidc",
		"TACIT_OIDC_ISSUER=https://accounts.google.com",
		"TACIT_OIDC_CLIENT_ID=id",
		"TACIT_OIDC_CLIENT_SECRET=sec",
		"TACIT_OIDC_REDIRECT_URI=https://tacit.example.com/auth/callback",
	}, "\n"))
	if code := cmdInit([]string{"--service", "none", "--embeddings", "off", "--global-access", "off"}); code != 0 {
		t.Fatalf("tacit init exited %d", code)
	}
	got := readEnv(t, path)
	if strings.Contains(got, "TACIT_OWNER_SECRET") {
		t.Errorf("init minted an owner secret for a registry with a provider:\n%s", got)
	}
	if !strings.Contains(got, "TACIT_AUTH_MODE=oidc") {
		t.Errorf("init rewrote the mode of a registry it did not set up:\n%s", got)
	}
}

// The guard `tacit personal` carried, on the run that now serves: something
// else on the port is not a registry to sign into, and the bind error alone
// says nothing about what to do next.
func TestServeHereRefusesAPortSomethingElseHolds(t *testing.T) {
	initEnv(t, "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close() // answers nothing recognisable, which is the case under test
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	if code := serveHere(port); code != 1 {
		t.Errorf("serveHere exited %d on a busy port, want 1", code)
	}
}

// retiredRegistry writes a registry that has been merged into an organization:
// settings, data, and the ledger that says it is finished.
func retiredRegistry(t *testing.T) (envPath, dataDir string) {
	t.Helper()
	nobodyWatching(t)
	dir := t.TempDir()
	dataHome := filepath.Join(dir, "share")
	t.Setenv("XDG_DATA_HOME", dataHome)
	envPath = filepath.Join(dir, "registry.env")
	dataDir = filepath.Join(dataHome, "tacit", "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "techniques.json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFileForInit(t, envPath, strings.Join([]string{
		"TACIT_API_KEY=the-old-key",
		"TACIT_AUTH_MODE=owner",
		"TACIT_OWNER_SECRET=the-old-owner",
		"TACIT_DATA=" + dataDir,
	}, "\n"))
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	l, err := merge.OpenLedger(merge.LedgerPath(envPath))
	if err != nil {
		t.Fatal(err)
	}
	l.Destination = "https://org.example"
	if err := l.MarkRetired(); err != nil {
		t.Fatal(err)
	}
	return envPath, dataDir
}

// A merged registry is finished. Setting up over it would bring back the
// instance that contributed its playbook, serving work that has already moved.
func TestInitRefusesAMergedRegistry(t *testing.T) {
	envPath, _ := retiredRegistry(t)
	if code := cmdInit([]string{"--service", "none", "--embeddings", "off", "--global-access", "off"}); code != 1 {
		t.Fatalf("tacit init exited %d over a merged registry, want 1", code)
	}
	if got := readEnv(t, envPath); !strings.Contains(got, "TACIT_API_KEY=the-old-key") {
		t.Errorf("the refusal changed the settings:\n%s", got)
	}
}

// And --start-over is the deliberate way past it: a NEW registry, with the old
// one's evidence kept beside it and this machine's settings file still here.
func TestInitStartOverMakesANewRegistryAndKeepsTheEvidence(t *testing.T) {
	envPath, dataDir := retiredRegistry(t)
	if code := cmdInit([]string{"--start-over", "--service", "none", "--embeddings", "off", "--global-access", "off"}); code != 0 {
		t.Fatalf("tacit init --start-over exited %d", code)
	}
	got := readEnv(t, envPath)
	for _, stale := range []string{"the-old-key", "the-old-owner"} {
		if strings.Contains(got, stale) {
			t.Errorf("the new registry reuses %q, so it is the old one to anything holding it:\n%s", stale, got)
		}
	}
	if !strings.Contains(got, "TACIT_AUTH_MODE=owner") || !strings.Contains(got, "TACIT_OWNER_SECRET=") {
		t.Errorf("the new registry has no owner to sign in as:\n%s", got)
	}
	if _, retired := merge.RetiredInto(filepath.Dir(envPath)); retired {
		t.Error("the new registry still reads as merged; it would refuse to start")
	}
	// Moved, never deleted.
	aside, err := filepath.Glob(dataDir + ".merged-*")
	if err != nil || len(aside) != 1 {
		t.Fatalf("the merged registry's data is not beside it: %v %v", aside, err)
	}
	if _, err := os.Stat(filepath.Join(aside[0], "techniques.json")); err != nil {
		t.Errorf("the member's own measurements did not survive: %v", err)
	}
}

// The refusal that makes retirement mean something: nothing starts a merged
// registry again, including a unit that outlived the merge.
func TestServeRefusesAMergedRegistry(t *testing.T) {
	keepEnvironment(t)
	retiredRegistry(t)
	if code := cmdServe([]string{}); code != 1 {
		t.Fatalf("tacit serve exited %d for a merged registry, want 1", code)
	}
}

// nobodyWatching puts /dev/null on stdin, which is what a script, a unit file
// and a container all do. It is a regression test as much as a fixture: this
// was read as "a person is here" once, and init served instead of returning.
func nobodyWatching(t *testing.T) {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
	if stdinIsTerminal() {
		t.Fatal("/dev/null reads as a terminal; init would take the terminal and serve in every script")
	}
}

// keepEnvironment restores this process's environment afterwards. `tacit serve`
// puts registry.env INTO the environment before reading it (config.ExportFileEnv),
// which is right for a registry and wrong for the tests that run after this one:
// a fixture's owner secret left behind makes the next test's open registry look
// gated.
func keepEnvironment(t *testing.T) {
	t.Helper()
	saved := os.Environ()
	t.Cleanup(func() {
		os.Clearenv()
		for _, kv := range saved {
			if k, v, ok := strings.Cut(kv, "="); ok {
				os.Setenv(k, v)
			}
		}
	})
}

// `tacit init` decides nothing about services. A registry with one member is
// started by its owner and stopped with Ctrl-C; the unit that outlives a reboot
// belongs to the registry an identity provider makes of it, and `tacit secure`
// installs it there.
func TestInitInstallsNoServiceByDefault(t *testing.T) {
	path := initEnv(t, "")
	home := filepath.Dir(path)
	t.Setenv("HOME", home)
	if code := cmdInit([]string{"--embeddings", "off", "--global-access", "off"}); code != 0 {
		t.Fatalf("tacit init exited %d", code)
	}
	for _, unit := range []string{
		filepath.Join(home, ".config", "systemd", "user", "tacit-registry.service"),
		filepath.Join(home, "Library", "LaunchAgents", "com.tacit.registry.plist"),
	} {
		if _, err := os.Stat(unit); err == nil {
			t.Errorf("init installed %s for a registry nobody has decided to keep", unit)
		}
	}
}

// A kill returns before the socket does. The guard that refuses a busy port
// has to allow for that, or "stop it and start it again" becomes a race that
// the old code won only by being slow to reach its listener.
func TestPortGuardWaitsForASocketThatIsBeingReleased(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		time.Sleep(700 * time.Millisecond)
		ln.Close()
	}()
	if !portFreeWithin("127.0.0.1", port, 5*time.Second) {
		t.Error("the guard refused a port that was released while it waited")
	}

	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if portFreeWithin("127.0.0.1", held.Addr().(*net.TCPAddr).Port, 500*time.Millisecond) {
		t.Error("the guard reported a port free while something held it throughout")
	}
}

// A registry set up on a server — over SSH, which is how servers are set up —
// used to end with a sign-in link to that machine's own loopback, which is the
// one address the operator could not open. `tacit init` now gives it an address
// on the shared ingress by default.
func TestInitGivesAFreshRegistryAnAddressYouCanOpen(t *testing.T) {
	vals := map[string]string{}
	if code := setupGlobalAccess(vals, "auto", true); code != 0 {
		t.Fatalf("auto returned %d", code)
	}
	if vals["TACIT_GLOBAL_ACCESS"] != "1" {
		t.Error("a gated registry with no address of its own did not get one")
	}
	// The switch, and NOT the confirmation. Both would be the silent opt-in the
	// Global Access design forbids: a default is not a person reading what would be
	// published and saying yes.
	if vals["TACIT_GLOBAL_ACCESS_CONFIRMED"] != "" {
		t.Error("setup confirmed the technique-sharing half on somebody's behalf")
	}
}

// An operator who configured their own address has answered this question.
// Routing them through a shared proxy would be a second answer to it.
func TestInitLeavesAnOperatorsOwnAddressAlone(t *testing.T) {
	vals := map[string]string{"TACIT_EXTERNAL_URL": "https://tacit.example.com"}
	if code := setupGlobalAccess(vals, "auto", true); code != 0 {
		t.Fatalf("auto returned %d", code)
	}
	if vals["TACIT_GLOBAL_ACCESS"] == "1" {
		t.Error("published a registry that already had an address")
	}
}

// A public address in front of a dashboard anybody can read is the one
// configuration this must never assemble. `serve` refuses to dial without a
// sign-in; auto declines earlier rather than writing a setting that gets refused.
func TestInitDoesNotPublishAnUngatedRegistry(t *testing.T) {
	vals := map[string]string{}
	if code := setupGlobalAccess(vals, "auto", false); code != 0 {
		t.Fatalf("auto returned %d", code)
	}
	if vals["TACIT_GLOBAL_ACCESS"] == "1" {
		t.Error("gave a registry with no sign-in a public address")
	}
	// Asking for it explicitly is still refused, and says both ways out.
	if code := setupGlobalAccess(map[string]string{}, "on", false); code == 0 {
		t.Error("--global-access on was accepted on a registry with no sign-in")
	}
}

// Off stays off, and an unknown answer is a usage error rather than a guess.
func TestInitGlobalAccessAnswers(t *testing.T) {
	vals := map[string]string{}
	if code := setupGlobalAccess(vals, "off", true); code != 0 || vals["TACIT_GLOBAL_ACCESS"] == "1" {
		t.Errorf("off: code %d, vals %v", code, vals)
	}
	if code := setupGlobalAccess(map[string]string{}, "maybe", true); code != 2 {
		t.Errorf("an unknown answer returned %d, want a usage error", code)
	}
}

// The ingress setting takes either form — a URL or a bare host, with or without
// a port — so the probe has to parse it the way the tunnel client does. A naive
// "does it contain a colon" test reads the scheme's colon and dials nonsense.
func TestIngressDialAddr(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ingress.tacit.zone", "ingress.tacit.zone:443"},
		{"ingress.tacit.zone:9443", "ingress.tacit.zone:9443"},
		{"https://ingress.tacit.zone", "ingress.tacit.zone:443"},
		{"https://ingress.tacit.zone/", "ingress.tacit.zone:443"},
		{"http://ingress.local", "ingress.local:80"},
		{"http://ingress.local:8080/tunnel", "ingress.local:8080"},
		{"127.0.0.1:9000", "127.0.0.1:9000"},
	} {
		if got := ingressDialAddr(tc.in); got != tc.want {
			t.Errorf("ingressDialAddr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// An install with no route to the ingress is a real deployment — an air-gapped
// network, a lab, a laptop on a plane. Turning the switch on there buys nothing
// and costs a dial failure in the log every thirty seconds for as long as the
// registry runs.
func TestInitDeclinesAnUnreachableIngress(t *testing.T) {
	vals := map[string]string{"TACIT_PUBLISH_INGRESS": "ingress.invalid"}
	if code := setupGlobalAccess(vals, "auto", true); code != 0 {
		t.Fatalf("auto returned %d", code)
	}
	if vals["TACIT_GLOBAL_ACCESS"] == "1" {
		t.Error("published against an ingress this machine cannot reach")
	}
	// Asked for explicitly, it is still written: an operator who knows the
	// network will be there tomorrow is not overruled by one dial today.
	explicit := map[string]string{"TACIT_PUBLISH_INGRESS": "ingress.invalid"}
	if code := setupGlobalAccess(explicit, "on", true); code != 0 {
		t.Fatalf("on returned %d", code)
	}
	if explicit["TACIT_GLOBAL_ACCESS"] != "1" {
		t.Error("--global-access on was overruled by a reachability probe")
	}
}

// THE LETTERING SPELLS ONE WORD. Every other displayed name asks
// internal/product; six-line letterforms cannot, so a renamed product gets no
// banner rather than the old name across the top of its first run.
func TestInitBannerOnlyPrintsTheNameItSpells(t *testing.T) {
	if !strings.Contains(initBanner, "_ __") {
		t.Fatal("the banner is not the wordmark")
	}
	t.Setenv(product.EnvKey, "Sagesse")
	if out := captureStdout(t, printInitBanner); out != "" {
		t.Errorf("a renamed product still gets the OpenTacit wordmark:\n%s", out)
	}
	t.Setenv(product.EnvKey, product.Default)
	if out := captureStdout(t, printInitBanner); !strings.Contains(out, "/ __ \\/ _ \\/ __ \\") {
		t.Errorf("the wordmark is not printed under its own name:\n%s", out)
	}
}
