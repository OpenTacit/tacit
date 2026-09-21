// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// `tacit merge`: fold this machine's registry into an organization's.
//
// The design is docs/design/registry-first-personal-tier.md (M3). Linking
// is three separable acts — point, contribute, reconcile — and that
// decomposition is right about the mechanism and wrong about the surface. A
// member who has decided to go all-in on their team should not have to run
// three commands and decide about each. So this is one command that performs
// all three in order and then finishes the job: it rewires this machine, stops
// this machine's registry, and gives its hostname back to the ingress.
//
// The order is the design, not the presentation:
//
//	exchange -> contribute -> confirm -> archive -> rewire -> stop -> release
//
// Nothing irreversible may happen before the destination has confirmed what it
// accepted. And because the destination's UniqueID never overwrites, the
// command is made REPEATABLE rather than atomic — it cannot be atomic, since it
// POSTs across a network to a service that has already stored what it took.
// Every step is safe to run again, and the ledger is what makes that true.

package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/merge"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	pkgclient "github.com/opentacit/tacit/pkg/client"
	"github.com/opentacit/tacit/pkg/contracts"
	"github.com/opentacit/tacit/pkg/ingress"
)

func cmdMerge(args []string) int {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	keepAddress := fs.Bool("keep-address", false, "leave this machine's registry running and keep its public address (contribute and rewire only)")
	archivePath := fs.String("archive", "", "where to write the evidence archive (default: the home directory)")
	dryRun := fs.Bool("dry-run", false, "say what would be sent and change nothing")
	yes := fs.Bool("yes", false, "do not ask before stopping the registry and releasing its address")
	positionals, ok := parseFlags(fs, args)
	if !ok {
		return exitUsage
	}

	c := &report{}
	m, rc := openMerge()
	if rc != 0 {
		return rc
	}

	link := ""
	if len(positionals) > 0 {
		link = strings.TrimSpace(positionals[0])
	}

	// A dry run redeems nothing. The exchange SPENDS a join token, so a member
	// who looked before leaping would find their invitation dead when they
	// leapt — which is the opposite of what looking first is for.
	if *dryRun {
		if rc := m.aim(link); rc != 0 {
			return rc
		}
		if rc := m.readLocal(c); rc != 0 {
			return rc
		}
		return m.explain(c)
	}

	// The join link is the credential. A bare registry URL cannot authenticate
	// anybody, which is why this takes the same link `tacit join` does — and
	// why a resumed run needs no link at all: the ledger kept the key the first
	// exchange produced, and the token that produced it is already spent.
	if link != "" {
		if rc := m.exchange(c, link); rc != 0 {
			return rc
		}
	} else if m.ledger.Key == "" {
		fmt.Fprintln(os.Stderr, "usage: tacit merge <join-url>   (get one from a colleague: tacit invite)")
		fmt.Fprintln(os.Stderr, "a run that was interrupted resumes with no link: the key it obtained is remembered")
		return exitUsage
	} else {
		m.dest = m.ledger.Destination
		c.info("Resuming the merge into %s.", m.dest)
	}

	if rc := m.readLocal(c); rc != 0 {
		return rc
	}

	sent, failed := 0, 0
	if m.readable {
		sent, failed = m.contribute(c)
	}
	if sent+failed == 0 && len(m.techniques) > 0 {
		c.ok("Everything here had already been contributed to %s.", m.dest)
	}
	// A safety-screen rejection is per technique and not a reason to abandon
	// the merge; anything else is, because it means the destination is not
	// reliably taking work and the steps after this one destroy things.
	if failed > 0 && sent == 0 {
		c.fail("Nothing was accepted. The registry is untouched.")
		return 1
	}

	if rc := m.archive(c, *archivePath); rc != 0 {
		return rc
	}
	if rc := m.rewire(c); rc != 0 {
		return rc
	}
	if *keepAddress {
		c.info("--keep-address: this registry keeps running at %s.", m.localURL)
		c.info("When the drafts have landed, finish with:  %s merge --yes", selfCommand())
		return 0
	}
	if !*yes && !confirmRetire(m) {
		c.info("Stopped before the irreversible half. Your techniques are contributed and")
		c.info("this machine is pointed at %s; the registry is still running.", m.dest)
		return 0
	}
	// Release only once the registry is actually down. Handing back the
	// hostname of a process that is still running would leave it serving on an
	// address nothing resolves, and its tunnel client would enrol all over
	// again under a name nobody asked for.
	if m.stop(c) > 0 {
		return 1
	}
	if m.release(c) > 0 {
		return 1
	}
	m.farewell(c)
	return 0
}

// mergeRun is one invocation's state: which registry is being folded, into
// what, and what has already gone.
type mergeRun struct {
	envPath  string // this machine's registry.env
	cfgDir   string
	cfg      registryconfig.Config
	localURL string
	local    *pkgclient.Registry
	ledger   *merge.Ledger
	// dest is the organization being merged into. It is the ledger's
	// destination once a link has been exchanged; a dry run fills it from the
	// link's own base, because it has redeemed nothing and has no key.
	dest string

	publicURL  string // the address the ingress allocated, while it still answers
	readable   bool   // the registry answered, so there is work to read
	techniques []contracts.Technique
	events     []contracts.FeedbackEvent
	outcomes   map[string]contracts.Outcome
	since      string
}

// openMerge resolves this machine's registry and refuses everything that is not
// one. The refusals are the whole of the "owner only" rule: whoever can read
// this profile IS the owner, because the owner secret never leaves the machine
// — the same argument as the claim code and `tacit dashboard`, and no second
// authentication system.
func openMerge() (*mergeRun, int) {
	base := registryconfig.RegistryEnvPath()
	if base == "" {
		fmt.Fprintln(os.Stderr, "no home directory; nothing to merge")
		return nil, 1
	}
	// RegistryEnvPath already honours TACIT_REGISTRY_ENV, so naming a settings
	// file explicitly still merges that one.
	envPath := base
	if !fileExists(envPath) {
		fmt.Fprintf(os.Stderr, "no registry on this machine (%s)\n", envPath)
		fmt.Fprintf(os.Stderr, "`tacit merge` folds a single-member registry into an organization's.\nTo point this machine at one without contributing anything:  %s join <join-url>\n", selfCommand())
		return nil, 1
	}
	os.Setenv("TACIT_REGISTRY_ENV", envPath)
	cfg := registryconfig.Load()
	if !cfg.SingleMember() {
		fmt.Fprintf(os.Stderr, "%s is not a single-member registry.\n", envPath)
		if cfg.TeamEnabled() {
			// The specific case, named: this registry was one member's and its
			// owner opened it to their team. Folding it into somebody else's
			// would take their colleagues' playbook with it.
			fmt.Fprintln(os.Stderr, "You opened it to your team, so it is your colleagues' playbook now.")
			fmt.Fprintln(os.Stderr, "Contributing it somewhere else is not one member's decision to make.")
			return nil, 1
		}
		fmt.Fprintln(os.Stderr, "`tacit merge` contributes one member's work and then retires their instance.")
		fmt.Fprintln(os.Stderr, "Doing that to an organization's registry is not one member's decision to make.")
		return nil, 1
	}
	m := &mergeRun{
		envPath:  envPath,
		cfgDir:   filepath.Dir(envPath),
		cfg:      cfg,
		localURL: fmt.Sprintf("http://127.0.0.1:%d", cfg.Port),
	}
	m.local = &pkgclient.Registry{BaseURL: m.localURL, APIKey: cfg.APIKey,
		HTTP: &http.Client{Timeout: 30 * time.Second}}

	l, err := merge.OpenLedger(merge.LedgerPath(envPath))
	if err != nil {
		// Never "start fresh" here: an unreadable ledger read as empty would
		// re-send everything it recorded, and UniqueID would duplicate all of
		// it in the reviewer's queue.
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", merge.LedgerPath(envPath), err)
		fmt.Fprintln(os.Stderr, "It records what has already been contributed, so this stops rather than sending it all again.")
		return nil, 1
	}
	m.ledger = l
	return m, 0
}

// aim works out where a dry run WOULD contribute, without redeeming anything.
func (m *mergeRun) aim(link string) int {
	if m.ledger.Destination != "" {
		m.dest = m.ledger.Destination
		return 0
	}
	if link == "" {
		fmt.Fprintln(os.Stderr, "usage: tacit merge <join-url> --dry-run")
		return exitUsage
	}
	base, _, ok := merge.SplitJoinLink(link)
	if !ok {
		fmt.Fprintf(os.Stderr, "this is not a join link (the format is .../join/<token>): %s\n", link)
		return 2
	}
	m.dest = base
	return 0
}

// exchange redeems the join link and remembers what it got.
//
// The key is persisted before anything is contributed, and that is deliberate:
// a join token is spent by the exchange that redeems it, so a run interrupted
// after this point could not otherwise resume without asking a colleague for a
// second invitation.
func (m *mergeRun) exchange(c *report, joinURL string) int {
	base, _, ok := merge.SplitJoinLink(joinURL)
	if !ok {
		fmt.Fprintf(os.Stderr, "this is not a join link (the format is .../join/<token>): %s\n", joinURL)
		return 2
	}
	memberURL, memberKey, err := merge.ExchangeLink(&http.Client{Timeout: 10 * time.Second}, joinURL, machineLabel())
	var api *merge.APIError
	switch {
	case errors.As(err, &api):
		fmt.Fprintf(os.Stderr, "join failed (%s): %s — an expired link is a frequent cause; ask for a new one\n", statusLine(api.Status), api.Message())
		return exitUnreachable
	case err != nil:
		fmt.Fprintf(os.Stderr, "cannot reach the registry at %s: %v\n", base, err)
		return exitUnreachable
	case memberKey == "":
		fmt.Fprintln(os.Stderr, "join failed: the registry answered without a key")
		return exitUnreachable
	}
	if m.ledger.Destination != "" && m.ledger.Destination != memberURL {
		// Merging into a second organization is a contribution, not a
		// re-contribution — but it is also almost certainly a mistake, and the
		// ledger is per registry rather than per destination.
		c.warn("This registry has already merged into %s.", m.ledger.Destination)
		c.info("Contributing it to %s as well will send everything again, to them.", memberURL)
	}
	m.ledger.Destination = memberURL
	m.ledger.Key = memberKey
	m.dest = memberURL
	if m.ledger.StartedAt == "" {
		m.ledger.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := m.ledger.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot record the merge: %v\n", err)
		return 1
	}
	c.ok("Joined %s.", memberURL)
	return 0
}

// readLocal loads this registry's techniques and its event log.
//
// Over HTTP rather than out of the store, because the registry is running and a
// second process over one file store is exactly what `tacit init` refuses
// to start. It also means a registry that is NOT running is caught here, before
// anything has been sent, with a message that says how to start it.
func (m *mergeRun) readLocal(c *report) int {
	techniques, err := m.local.TechniquesUpTo(5000)
	if err != nil {
		// A registry that has already been contributed and is now stopped is
		// the ORDINARY way this command is finished: the registry running
		// in a terminal cannot be stopped by this process, so the member is
		// told to stop it and run again — and on that second run there is
		// nothing left to read and nothing left to send.
		if len(m.ledger.Sent) > 0 {
			c.info("The registry is not answering; its work has already been contributed.")
			c.info("Finishing the teardown.")
			return 0
		}
		fmt.Fprintf(os.Stderr, "cannot read the registry at %s: %v\n", m.localURL, err)
		fmt.Fprintf(os.Stderr, "It has to be running to be merged. Start it with:  %s serve\n", selfCommand())
		return exitUnreachable
	}
	m.readable = true
	// Only what this member's registry actually serves. A draft is something
	// they have not decided about themselves, and a retired technique is one
	// they decided against — neither is work to hand a reviewer.
	for _, t := range techniques {
		if t.Status == "stable" || t.Status == "mined" {
			m.techniques = append(m.techniques, t)
		}
	}
	events, err := m.allEvents()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read the event log: %v\n", err)
		return exitUnreachable
	}
	m.events = events
	// Read while the registry is still up: the address it holds is only
	// knowable from the running process, and by the time the confirmation
	// prompt needs to name it the answer would be gone.
	m.publicURL = livePublicBase(m.cfg)
	m.outcomes = merge.OutcomeIndex(events)
	m.since = merge.FirstSeen(events)
	c.info("%d techniques to contribute, %d recorded events.", len(m.techniques), len(m.events))
	return 0
}

// allEvents pages the whole log. The archive is only worth writing if it is
// complete, so this follows the cursor rather than taking the first page.
func (m *mergeRun) allEvents() ([]contracts.FeedbackEvent, error) {
	var all []contracts.FeedbackEvent
	seen := map[string]bool{}
	since := ""
	for {
		page, err := m.local.Events(since, 1000)
		if err != nil {
			return nil, err
		}
		added := 0
		for _, e := range page.Events {
			if e.EventID != "" && seen[e.EventID] {
				continue
			}
			seen[e.EventID] = true
			all = append(all, e)
			added++
		}
		// Pages overlap at the cursor timestamp, so a page that adds nothing
		// new is the end whatever the cursor says.
		if page.NextSince == "" || added == 0 {
			return all, nil
		}
		since = page.NextSince
	}
}

func (m *mergeRun) explain(c *report) int {
	fmt.Println()
	pending := 0
	for _, t := range m.techniques {
		if _, done := m.ledger.AlreadySent(t.ID, m.dest); done {
			continue
		}
		pending++
		standing := merge.StandingNote(m.outcomes[t.ID], m.since)
		fmt.Printf("  %s\n", t.Name)
		if standing != "" {
			fmt.Printf("      %s\n", standing)
		}
	}
	fmt.Println()
	c.info("%d would be contributed to %s as drafts for review.", pending, m.dest)
	c.info("No outcome figure crosses; the sentences above travel as prose in the description.")
	c.info("Nothing was changed.")
	return 0
}

// contribute POSTs what has not gone, recording each success before the next
// attempt. Per technique, and carrying on: contributions are the
// adversary-plausible lane, so the destination's safety screen hard-rejects a
// High finding at the door, and one refused technique must not abandon a merge.
func (m *mergeRun) contribute(c *report) (sent, failed int) {
	note := func(ok bool, format string, a ...any) {
		if ok {
			c.ok(format, a...)
			return
		}
		c.fail(format, a...)
	}
	sent, failed, fatal := merge.ContributeAll(&http.Client{Timeout: 60 * time.Second},
		m.ledger, m.techniques, m.outcomes, m.since, note)
	if fatal != nil {
		c.fail("%v", fatal)
	}
	if sent > 0 {
		c.ok("%d contributed to %s, waiting for review there.", sent, m.dest)
	}
	return sent, failed
}

// archive writes the evidence that does not travel, before anything can
// destroy it.
func (m *mergeRun) archive(c *report, path string) int {
	if !m.readable {
		// Nothing was read, so there is nothing new to write. Refusing to go on
		// without an archive at all is the point of this step, so a run that
		// has never written one stops here rather than retiring a registry
		// whose measurements have never been saved.
		if m.ledger.ArchivePath == "" {
			c.fail("This registry's evidence has never been archived, and it cannot be read now.")
			c.info("Start it (%s serve) and run this again.", selfCommand())
			return 1
		}
		c.ok("Evidence was already archived to %s.", m.ledger.ArchivePath)
		return 0
	}
	if path == "" {
		path = merge.ArchivePath(auditorconfig.Load().StateDir, m.ledger.ArchivePath)
	}
	a := merge.NewArchive(m.localURL, m.dest, m.techniques, m.events,
		m.ledger.SentTo(m.dest))
	if err := a.Write(path); err != nil {
		// Refusing to continue is the point. The steps after this one retire
		// the registry, and the archive is the only copy of its measurements
		// that will survive that.
		c.fail("cannot write the archive to %s: %v", path, err)
		c.info("The merge stops here rather than retiring a registry whose evidence is not saved.")
		return 1
	}
	m.ledger.ArchivePath = path
	_ = m.ledger.Save()
	c.ok("Evidence archived to %s (%d events).", path, len(m.events))
	c.info("It is not contributed: an organization's floors are computed over cohorts,")
	c.info("and one member's outcomes entering them would be a sample of one.")
	return 0
}

// rewire points this machine's agents at the organization. It is the reversible
// half, and it runs only once the destination has confirmed what it accepted.
func (m *mergeRun) rewire(c *report) int {
	fmt.Println()
	if rc := applyMemberSettings(memberSettings{registryURL: m.dest, key: m.ledger.Key}); rc != 0 {
		return rc
	}
	if rc := cmdConnect(nil); rc != 0 {
		return rc
	}
	offerCohorts(m.dest, m.ledger.Key)
	return 0
}

// confirmRetire asks once, and says the thing the member has to know: the
// address does not come back.
func confirmRetire(m *mergeRun) bool {
	fmt.Println()
	fmt.Println("The rest cannot be undone:")
	fmt.Printf("  - the registry at %s stops, and stays stopped — it does not start again\n", m.localURL)
	if m.publicURL != "" {
		fmt.Printf("  - %s is released, and the same key will never resolve to it again\n", m.publicURL)
		fmt.Println("    (anything still pointing at that address — a bookmark, another machine — stops working)")
	} else if _, err := ingress.LoadKey(m.cfgDir); err == nil {
		fmt.Println("  - its public address is released, and the same key will never resolve to it again")
	}
	fmt.Printf("  - its data stays on disk at %s\n", m.cfg.DataDir)

	// Non-interactive means no: a pipe cannot answer, and taking silence for
	// consent here retires somebody's registry because they ran the command
	// from a script.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Println()
		fmt.Println("Nothing is reading this prompt. Re-run with --yes to retire it.")
		return false
	}
	p := &prompter{sc: bufio.NewScanner(os.Stdin), interactive: true}
	return strings.EqualFold(p.ask("Retire this registry? (yes/no)", "no"), "yes")
}

func (m *mergeRun) stop(c *report) (fails int) {
	fmt.Println()
	fmt.Println("registry:")
	stopped := false
	if runtime.GOOS == "linux" && haveExec("systemctl") {
		home, _ := os.UserHomeDir()
		unit := filepath.Join(home, ".config", "systemd", "user", "tacit-registry.service")
		if fileExists(unit) {
			_ = exec.Command("systemctl", "--user", "disable", "--now", "tacit-registry.service").Run()
			c.ok("service stopped and disabled")
			stopped = true
		}
	}
	if !stopped && registryRunning(m.localURL) {
		// Something is serving it that this command did not install — a
		// terminal running `tacit serve`, most often. Hunting for a process
		// to kill is not this command's business; naming it is.
		c.fail("The registry is still answering on %s and this command did not start it.", m.localURL)
		c.info("Stop it where it is running (Ctrl-C in that terminal), then run this again:")
		c.info("  %s merge --yes", selfCommand())
		return 1
	}
	if !stopped {
		c.ok("nothing was serving %s", m.localURL)
	}
	// The settings are left in place on purpose. Retiring the profile is one
	// act, performed by the next run when nothing is holding a file open
	// (internal/merge/retire.go) — and until the address has actually been
	// released, this profile still holds the instance key a retry needs.
	return 0
}

// release gives the hostname back. Last, because it is the one step that
// cannot be taken back and the one whose failure costs least: a name that was
// not released is a slot the ingress carries, not work that was lost.
func (m *mergeRun) release(c *report) (fails int) {
	name, err := merge.ReleaseAddress(m.cfgDir, m.cfg.PublishIngress, m.cfg.PublishTLS, version)
	switch {
	case errors.Is(err, merge.ErrNoAddress):
		c.info("This registry had no public address to release.")
		return 0
	case err != nil:
		c.fail("could not release the address with %s: %v", m.cfg.PublishIngress, err)
		c.info("The instance key is at %s; retrying this command releases it.", ingress.KeyPath(m.cfgDir))
		return 1
	}
	if name == "" {
		c.ok("The ingress had no record of this registry; nothing to release.")
		m.retire(c)
		return 0
	}
	c.ok("Released %s. The name is free again, and the slot with it.", name)
	m.retire(c)
	return 0
}

// retire marks this registry finished. It does not start again: `tacit serve`
// refuses a retired registry, and the unit that would have restarted it was
// disabled by the stop above (internal/merge/retire.go).
//
// Last, after the address is gone: a registry marked finished before its
// hostname was released would still hold the instance key a retry needs, and
// nothing would be able to use it.
func (m *mergeRun) retire(c *report) {
	if err := m.ledger.MarkRetired(); err != nil {
		c.warn("could not mark this registry retired: %v", err)
		c.info("It would start again if something served it; stop it and run this command again.")
		return
	}
	c.info("Its settings and data stay at %s and %s, and it will not start again.", m.cfgDir, m.cfg.DataDir)
	c.info("For a new registry on this machine:  %s init --start-over", selfCommand())
}

func (m *mergeRun) farewell(c *report) {
	fmt.Println()
	fmt.Printf("Merged into %s.\n\n", m.dest)
	fmt.Printf("  Your agents now retrieve from your organization's playbook.\n")
	fmt.Printf("  %d techniques are in its review queue.\n", len(m.ledger.SentTo(m.dest)))
	if m.ledger.ArchivePath != "" {
		fmt.Printf("  Your own measurements are at %s.\n", m.ledger.ArchivePath)
	}
	fmt.Printf("\nWhat happened to your drafts:  %s/review\n", m.dest)
	fmt.Printf("This registry is finished, and does not start again. For a new one on this\nmachine — its data moved aside, your settings kept:  %s init --start-over\n", selfCommand())
}

func machineLabel() string {
	label := ""
	if u := os.Getenv("USER"); u != "" {
		label = u
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		if label != "" {
			label += "@"
		}
		label += host
	}
	return label
}
