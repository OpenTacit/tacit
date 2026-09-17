// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"net/http"
	"time"

	"github.com/opentacit/tacit/internal/auditor/client"
	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/contracts"
	"github.com/opentacit/tacit/internal/auditor/hooks"
	"github.com/opentacit/tacit/internal/ledger"
	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/windows"
)

func cmdMCP() int {
	cfg := auditorconfig.Load()
	segment := contracts.Segment(auditorconfig.ParseSegment(cfg.HooksSegment))
	server := newMCPServer(cfg.RegistryURL, cfg.RegistryKey, segment)
	server.Usage = localUsageFunc(cfg.UsageLogPath)
	if err := server.Run(os.Stdin, os.Stdout); err != nil {
		return 1
	}
	return 0
}

func cmdUsage(args []string) int {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the usage summary as JSON")
	asHTML := fs.Bool("html", false, "print a self-contained HTML usage panel")
	window := fs.String("window", "30d", "time window: "+windows.Prose())
	showKey := fs.Bool("key", false, "print the key that opens your usage on the registry dashboard, and stop")
	publish := fs.Bool("publish", false, "seal this machine's usage and file it with the registry now, then stop")
	rebuild := fs.Bool("rebuild", false, "re-read this machine's transcripts and rebuild the session log's tool detail under the current capture rules, then stop")
	dryRun := fs.Bool("dry-run", false, "with --rebuild: report what would change and write nothing")
	landed := fs.Bool("landed", false, "of the lines written here, how many are still in the branch: run it inside the repository, then stop (add --publish to file the reading for the dashboard)")
	_ = fs.Parse(args)
	cfg := auditorconfig.Load()
	if *showKey {
		return printLedgerKey(cfg)
	}
	// --landed is tested before --publish, because together they mean "measure
	// this repository and file the reading", not "measure it and then seal the
	// session log". Flags that read as a sentence should behave like one.
	if *landed {
		return reportLanded(cfg, *window, *asJSON, *publish)
	}
	if *publish {
		return publishLedgerNow(cfg)
	}
	if *rebuild {
		return rebuildSessionDetail(cfg, *dryRun)
	}
	html, text, summary, err := hooks.RenderUsageApp(cfg.UsageLogPath, *window)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit usage:", err)
		return 1
	}
	switch {
	case *asHTML:
		fmt.Println(html)
	case *asJSON:
		body, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(body))
	default:
		fmt.Print(text)
	}
	return 0
}

// reportLanded answers "did it land" for the repository the member is standing
// in.
//
// It is a command rather than a panel because of the one constraint that shapes
// the whole measure: the session log keeps a project basename and never a path,
// so nothing stored can find the repository later. The working directory is the
// path, supplied by the member at the moment they ask, and it is not written
// down afterwards.
func reportLanded(cfg auditorconfig.Config, window string, asJSON, publish bool) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit usage --landed:", err)
		return 1
	}
	win := hooks.ParseUsageWindow(window)
	now := time.Now()
	rep, err := hooks.Landed(wd, win, now, cfg.StateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit usage --landed:", err)
		return 1
	}
	// --publish files the reading so the Usage page can show it. The path is
	// still not stored: what is filed is the basename, the branch and the
	// counts, which is what the command already prints and what the session log
	// was already allowed to hold.
	filed := false
	if publish {
		if err := hooks.SaveLanded(cfg.StateDir, rep, win, now); err != nil {
			fmt.Fprintln(os.Stderr, "tacit usage --landed --publish:", err)
			return 1
		}
		filed = true
	}
	if asJSON {
		body, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(body))
		return 0
	}
	fmt.Print(landedText(rep))
	if filed {
		fmt.Printf("\n  Filed for %s. It shows on You / Outcomes until you\n"+
			"  ask again, dated today, and the registry still cannot read it.\n", rep.Project)
	}
	return 0
}

// landedText says what was measured, what it means, and where it stops being
// true. The last part is not a footnote: a survival figure over a window that
// has been rebased is measuring a history that was rewritten, and a member who
// does not know that will read a real drop into a housekeeping commit.
func landedText(r hooks.LandedReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — did it land\n", r.Project)
	fmt.Fprintf(&b, "  %s to %s", r.From.Format("2006-01-02"), r.To.Format("2006-01-02"))
	if r.Branch != "" {
		fmt.Fprintf(&b, " on %s", r.Branch)
	}
	b.WriteString("\n\n")
	if r.Commits == 0 {
		b.WriteString("  No commits in this window, so there is nothing to have landed.\n" +
			"  A longer --window may reach back to some.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "  %s across %s, touching %s\n",
		grouped(r.Commits, "commit"), grouped(r.Files, "file"), grouped(r.Added, "added line"))
	fmt.Fprintf(&b, "  %s removed over the same days\n\n", grouped(r.Removed, "line"))
	fmt.Fprintf(&b, "  Still in the branch:  %s of %s  (%.0f%%)\n",
		fmtInt(r.Survived), fmtInt(r.Added), 100*r.SurvivalRate())
	fmt.Fprintf(&b, "  Gone again:           %s\n\n", fmtInt(r.Added-r.Survived))

	switch {
	// Something to compare against, or a reason there is not. A status line
	// that reported and counted no lines is not a comparison — it is a session
	// that read rather than wrote — and the sentence about the difference
	// between the two would be about nothing.
	case r.RecordedReported && r.Recorded+r.RecordedRemoved > 0:
		fmt.Fprintf(&b, "  Your sessions recorded %s added and %s removed in this project\n"+
			"  over %s. The difference between that and the branch is work that\n"+
			"  never became a commit, or became one outside this window.\n\n",
			fmtInt(r.Recorded), fmtInt(r.RecordedRemoved), grouped(r.RecordedSessions, "session"))
	case r.RecordedSessions > 0 && r.RecordedReported:
		fmt.Fprintf(&b, "  %s recorded in this project, and none of them wrote a line, so\n"+
			"  there is nothing of yours to set against the branch.\n\n",
			grouped(r.RecordedSessions, "session"))
	case r.RecordedSessions > 0:
		fmt.Fprintf(&b, "  %s recorded in this project, with no line counts: the status line\n"+
			"  is not wired here, so there is nothing to compare the branch against.\n"+
			"  `tacit connect` offers to wire it.\n\n", grouped(r.RecordedSessions, "session"))
	default:
		b.WriteString("  No sessions recorded in this project in this window, so there is\n" +
			"  nothing of your own to compare the branch against.\n\n")
	}

	b.WriteString("  What this measures, and where it stops:\n" +
		"    Survival is blame at the tip — a line still credited to a commit in\n" +
		"    this window. A line that was reformatted, moved, or rewritten counts\n" +
		"    as gone, because by this measure it is.\n" +
		"    A rename follows the file. A rebase does not: it rewrites the commits\n" +
		"    and their dates, so a rebased window measures the history you have\n" +
		"    now rather than the one you worked in.\n" +
		"    Nothing outside this directory was read, and nothing was written.\n")
	return b.String()
}

// grouped is plural() with the thousands separated. Line counts run to six
// figures and this is the one command that produces them.
func grouped(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmtInt(n) + " " + unit + "s"
}

// fmtInt groups thousands, because a six-figure line count is unreadable
// otherwise and this is the one command that produces them.
func fmtInt(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + fmtInt(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// rebuildSessionDetail re-derives what is behind each tool from the transcripts
// this machine still has.
//
// It exists because the log keeps no text: when a capture rule was wrong — and
// one was, reading the body of a here-doc as if each line had run — what it
// recorded cannot be repaired from the log itself. The transcript is the only
// place the inputs survive, and the rebuild applies the CURRENT rules to it, so
// the result is what would have been recorded had the rule been right. It also
// backfills a vocabulary that was added later: the agents a delegation handed
// to, the skills invoked, the file types a search named.
func rebuildSessionDetail(cfg auditorconfig.Config, dry bool) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit usage --rebuild:", err)
		return 1
	}
	rep, err := hooks.RebuildDetail(cfg.StateDir, home, cfg.SessionSalt, dry, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit usage --rebuild:", err)
		return 1
	}
	verb := "rebuilt"
	if dry {
		verb = "would rebuild"
	}
	// The hook agent holds the log in memory and writes what it remembers at
	// the next Stop, so a rebuild under a running agent is overwritten for the
	// session that agent is recording. Saying which command fixes it beats
	// leaving the member to find out from the numbers.
	if agentIsUp(cfg) {
		fmt.Printf("the hook agent is up on :%d and holds the session you are in. It writes what it remembers\n"+
			"at the next turn, so stop it first — `fuser -k %d/tcp` — and it restarts on the next hook,\n"+
			"resuming from what this rebuild wrote.\n", cfg.HooksPort, cfg.HooksPort)
	}
	fmt.Printf("%s %d of %d session(s) from %d transcript(s) on this machine.\n",
		verb, rep.Rebuilt, rep.Records, rep.Transcripts)
	if rep.NoTranscript > 0 {
		fmt.Printf("%d session(s) have no transcript left; the program detail of %d was dropped rather than left wrong.\n",
			rep.NoTranscript, rep.Cleared)
	}
	if rep.AmpFilled > 0 {
		fmt.Printf("%d Amp session(s) took the model and the tokens their own thread accounts for; "+
			"Amp tells a hook neither.\n", rep.AmpFilled)
	}
	fmt.Printf("detail keys: %d before, %d after.\n", rep.KeysBefore, rep.KeysAfter)
	// Naming what went and what arrived is the whole report: a count alone
	// cannot be checked, and these are the keys the member saw on the page.
	printKeys("dropped", rep.Dropped)
	printKeys("added", rep.Added)
	if dry {
		fmt.Println("nothing was written. Run it again without --dry-run to keep this.")
	}
	return 0
}

// agentIsUp reports whether this machine's hook agent answers on loopback. It
// is the same probe doctor makes, and absence is not a fault: the agent
// idle-exits by design.
func agentIsUp(cfg auditorconfig.Config) bool {
	c := &http.Client{Timeout: time.Second}
	base := fmt.Sprintf("http://%s:%d", cfg.HooksHost, cfg.HooksPort)
	if _, err := fetchAgentStats(c, base); err != nil {
		return false
	}
	return true
}

func printKeys(label string, keys []string) {
	if len(keys) == 0 {
		return
	}
	shown := keys
	if len(shown) > 20 {
		shown = shown[:20]
	}
	fmt.Printf("  %s: %s", label, strings.Join(shown, ", "))
	if len(keys) > len(shown) {
		fmt.Printf(" and %d more", len(keys)-len(shown))
	}
	fmt.Println()
}

// printLedgerKey hands the member the one secret the dashboard needs to open
// their own numbers, and says plainly what it is.
//
// The key is derived from this machine's member credential rather than stored,
// so it is the same on every machine the member joined and there is nothing new
// to keep safe. It is printed rather than sent: the registry must never see it,
// which is the property that lets it hold the ledger at all.
func printLedgerKey(cfg auditorconfig.Config) int {
	secret := cfg.RegistryKey
	key, id, err := ledger.Derive(secret)
	if err != nil || secret == auditorconfig.DefaultRegistryKey {
		fmt.Fprintln(os.Stderr, "this machine is not connected to a registry, so it has no usage ledger.")
		fmt.Fprintln(os.Stderr, "run: tacit connect --registry <url> --key <key>")
		return 1
	}
	// One token, two halves, because the browser needs both and they cannot be
	// derived from each other — which is exactly the property that lets the
	// address be published in a URL while the key never is.
	fmt.Println("Your " + product.Name() + " usage key:")
	fmt.Println()
	fmt.Println("  " + base64.RawURLEncoding.EncodeToString(key) + "." + id)
	fmt.Println()
	fmt.Println("Paste it into the Usage page on your dashboard. It stays in that browser,")
	fmt.Println("and every machine you have connected reports into the same place.")
	fmt.Println()
	fmt.Println("The registry never receives this and cannot read your usage without it.")
	fmt.Println("Anyone who has it can read your usage, so treat it like a password.")
	return 0
}

// publishLedgerNow files this machine's usage with the registry immediately,
// instead of waiting for the running agent's next timer.
//
// It exists so a change can be LOOKED AT. The dashboard reads a sealed blob, and
// after a deploy that blob is whatever the previous binary published — so a new
// field is absent from the page for up to ten minutes, and absent looks exactly
// like broken. Once in the deploy, the page a member opens is the page the code
// they just built produces.
//
// It reads the logs from disk rather than talking to the agent, so it works with
// no daemon running and cannot disturb one that is.
func publishLedgerNow(cfg auditorconfig.Config) int {
	if cfg.RegistryKey == "" || cfg.RegistryKey == auditorconfig.DefaultRegistryKey {
		fmt.Fprintln(os.Stderr, "this machine is not connected to a registry, so it has no usage ledger to publish to.")
		return 1
	}
	key, id, err := ledger.Derive(cfg.RegistryKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit usage --publish:", err)
		return 1
	}
	body, err := hooks.LedgerPayload(hooks.FileSummarizer{
		UsageLogPath: cfg.UsageLogPath, StateDir: cfg.StateDir, Now: time.Now,
	}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit usage --publish:", err)
		return 1
	}
	sealed, err := ledger.Seal(key, body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tacit usage --publish:", err)
		return 1
	}
	registry := &client.Registry{BaseURL: cfg.RegistryURL, APIKey: cfg.RegistryKey,
		HTTP: &http.Client{Timeout: 10 * time.Second}}
	machine := hooks.MachineLabel()
	if err := registry.PublishLedger(id, machine, sealed); err != nil {
		if client.LedgerUnavailable(err) {
			fmt.Fprintln(os.Stderr, "this registry has no usage ledger — it predates one. Nothing to publish to.")
			return 1
		}
		fmt.Fprintln(os.Stderr, "tacit usage --publish:", err)
		return 1
	}
	fmt.Printf("published %s (%d bytes, sealed) to %s\n", machine, len(sealed), cfg.RegistryURL)
	return 0
}
