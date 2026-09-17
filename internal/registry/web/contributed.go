// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

// What became of the work a member contributed to their organization.
//
// A member who has run `tacit merge` has techniques sitting in somebody else's
// review queue, and nothing on this machine can tell them what happened next:
// the decision is made over there, and no message comes back. This is the panel
// that asks — the third of the three acts linking is made of, and the answer to
// the only question a contributor actually has (M3b in
// docs/design/registry-first-personal-tier.md).
//
// It appears only on a single-member registry that has merged. An organization's
// registry has no ledger, contributes to nobody, and shows nothing.

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/merge"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/pkg/contracts"
)

// contributedTTL is how long a checked answer is reused.
//
// Long, because the thing being watched moves at the speed of a person reading
// a review queue. A shorter one would put a network round trip per contributed
// technique in front of a page load to learn nothing new.
const contributedTTL = 10 * time.Minute

// contributedCache holds the last answer the organization gave.
//
// The page never waits on the network. It renders what is known — including
// nothing, the first time — and refreshes behind the reader, because a
// playbook page that hangs because somebody else's registry is slow is worse
// than one that is briefly out of date about a review queue.
type contributedCache struct {
	mu        sync.Mutex
	checkedAt time.Time
	dest      string
	standings []merge.Standing
	busy      bool
}

// ledgerPath is where the merge ledger lives for THIS registry: beside its own
// settings file, so it belongs to the registry rather than to the machine.
func ledgerPath() string { return merge.LedgerPath(config.RegistryEnvPath()) }

// contributedPanel is the section: an invitation to merge on a registry that
// has not, what happened to the work on one that has, and progress in between.
// Empty on an organization's registry.
func (s *Server) contributedPanel(r *http.Request) string {
	// Only a single-member registry has an owner who merged. An organization's
	// registry reads no ledger, whatever happens to be on the disk beside it.
	if !s.cfg().SingleMember() {
		return ""
	}
	// A run in flight owns the panel, and a finished one owns it for exactly one
	// more render — the read receipt. After that the question changes from "is
	// it going" to "what became of it", and the standings answer that better
	// than a log of what was sent does.
	if running, lines, failed, unread := s.takeMergeReport(); running || unread {
		return mergeProgressPanel(running, lines, failed)
	}
	l, err := merge.OpenLedger(ledgerPath())
	if err != nil || l.Destination == "" || len(l.Sent) == 0 {
		return s.mergeInvitation(r)
	}
	sent := l.SentTo(l.Destination)
	if len(sent) == 0 {
		return ""
	}
	standings, checkedAt := s.contributedStandings(l, sent)

	var b strings.Builder
	// M9: what is true right now, before what became of anything. A member who
	// contributed and has not finished is not half way through a task — they are
	// in a state that is allowed to last, and the page says so rather than
	// presenting the irreversible step as the next thing to do.
	b.WriteString(s.stillYoursBanner(l))
	fmt.Fprintf(&b, `<section class="panel"><h2>Contributed to %s</h2>`,
		html.EscapeString(displayHost(l.Destination)))
	// The tally leads, and the list is folded away behind it. A member with
	// forty contributed techniques does not need forty lines above their own
	// playbook to learn that none has been read yet — the answer is the
	// counts, and which ones is a question they ask second.
	fmt.Fprintf(&b, `<p class="hint">You sent %d technique%s for review. The destination registry controls their review status.</p>`,
		len(sent), plural(len(sent)))
	fmt.Fprintf(&b, `<p class="contributed-tally">%s</p>`, standingTally(standings))
	// One span, not two: the two-span summary elsewhere on this page is spaced
	// by a class of its own, and borrowing the markup without it renders
	// "Which ones8".
	fmt.Fprintf(&b, `<details class="fineprint"><summary>Which ones (%d)</summary><ul class="feed">`, len(standings))
	for _, st := range standings {
		name := st.Name
		if name == "" {
			name = st.DraftID
		}
		fmt.Fprintf(&b, `<li><a href="/techniques/%s">%s</a>—<span class="muted">%s</span></li>`,
			html.EscapeString(st.LocalID), html.EscapeString(name), html.EscapeString(st.Describe()))
	}
	b.WriteString(`</ul></details>`)
	if refused := s.refusals(); len(refused) > 0 {
		fmt.Fprintf(&b, `<p class="hint">%d failed the destination registry’s safety checks:</p><ul class="feed">`, len(refused))
		for _, l := range refused {
			fmt.Fprintf(&b, `<li class="warn">%s</li>`, html.EscapeString(l.text))
		}
		b.WriteString(`</ul>`)
	}
	if checkedAt.IsZero() {
		b.WriteString(`<p class="hint">Checking the destination registry now. Reload in a moment.</p>`)
	} else {
		fmt.Fprintf(&b, `<p class="hint">Last asked %s.</p>`, html.EscapeString(agoWords(time.Since(checkedAt))))
	}
	b.WriteString(s.mergeFinishOffer(r, l))
	b.WriteString(`</section>`)
	return b.String()
}

// standingTally is the headline: how many are in each state, in the order a
// contributor cares about them — what landed first, what is still out, what was
// turned down.
func standingTally(standings []merge.Standing) string {
	count := map[string]int{}
	for _, st := range standings {
		count[st.State]++
	}
	var parts []string
	for _, st := range []string{merge.StatePromoted, merge.StateWaiting, merge.StateShadow,
		merge.StateDeclined, merge.StateGone, merge.StateChecking, merge.StateUnreachable} {
		if n := count[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("<strong>%d</strong> %s", n,
				html.EscapeString(merge.Standing{State: st}.Describe())))
		}
	}
	return strings.Join(parts, " · ")
}

// contributedStandings returns the cached answer, refreshing behind the reader
// when it is stale. The zero time means nothing has been read yet.
func (s *Server) contributedStandings(l *merge.Ledger, sent map[string]merge.Sent) ([]merge.Standing, time.Time) {
	c := &s.contributed
	c.mu.Lock()
	fresh := c.dest == l.Destination && time.Since(c.checkedAt) < contributedTTL
	standings, checkedAt := c.standings, c.checkedAt
	if c.dest != l.Destination {
		// A different organization is a different question; showing the old
		// one's answers against the new one's name would be a lie.
		standings, checkedAt = nil, time.Time{}
	}
	start := !fresh && !c.busy
	if start {
		c.busy = true
	}
	c.mu.Unlock()

	if start {
		go func() {
			got := merge.LookUp(l.Destination, l.Key, sent, &http.Client{Timeout: 15 * time.Second})
			c.mu.Lock()
			c.standings, c.checkedAt, c.dest, c.busy = got, time.Now(), l.Destination, false
			c.mu.Unlock()
		}()
	}
	if standings == nil {
		// Nothing read yet: list what was sent, so the panel says which
		// techniques are out there even before anyone has answered for them.
		for id, e := range sent {
			standings = append(standings, merge.Standing{LocalID: id, DraftID: e.DraftID,
				SentAt: e.SentAt, State: merge.StateChecking})
		}
		sortStandings(standings)
	}
	return standings, checkedAt
}

func sortStandings(in []merge.Standing) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].LocalID < in[j-1].LocalID; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// displayHost is a destination URL without its scheme — the organization as a
// member would say it out loud.
func displayHost(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return strings.TrimRight(u, "/")
}

// agoWords is a rough "how long since", for a line whose whole job is to say
// the answer is not live.
func agoWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int(d.Minutes())
		return fmt.Sprintf("%d minute%s ago", n, plural(n))
	case d < 24*time.Hour:
		n := int(d.Hours())
		return fmt.Sprintf("%d hour%s ago", n, plural(n))
	default:
		n := int(d.Hours() / 24)
		return fmt.Sprintf("%d day%s ago", n, plural(n))
	}
}

// mergeInvitation is the offer, shown on a single-member registry that has not
// merged: paste the link a colleague sent you.
//
// The link is the WHOLE credential, and that is the point of the form having
// one field. A member who has been invited to an organization already holds the
// one thing this needs; asking them for a registry URL and an API key as well
// would invent a second credential for something the first already proves.
func (s *Server) mergeInvitation(r *http.Request) string {
	// Only to somebody who could act on it. A signed-out visitor on a
	// single-member registry sees the front door, but a viewer who is not the
	// owner must not be shown a control that hands this registry's whole
	// playbook to an address they choose.
	if !s.isAdmin(s.sessionUser(r)) {
		return ""
	}
	return `<section class="panel"><h2>Contribute this playbook to an organization</h2>` +
		`<p class="hint">If a colleague has sent you a join link, this shows what they already have ` +
		`near each of your techniques, and lets you pick which ones to send for review. The registry ` +
		`archives your outcomes on this machine and does not add them to the organization’s figures.</p>` +
		`<form method="post" action="/admin/merge" class="merge-form">` +
		fmt.Sprintf(`<input type="hidden" name="csrf" value="%s">`, html.EscapeString(s.csrfToken(r))) +
		`<input type="text" name="link" placeholder="https://your-org.example/join/…" size="48" required>` +
		` <button type="submit">See what you would add</button></form>` +
		`<p class="hint">Nothing is contributed and nothing is retired by this—the next page is a ` +
		`list you select from. Redeeming the link does use it up, so it is spent whether or not you send anything.</p>` +
		`</section>`
}

// mergeProgress is one merge, running in the background of the process that
// serves the page that started it.
//
// In the background because contributing forty techniques is forty round trips
// to somebody else's registry, and a form POST that holds a connection open for
// all of them fails in the least useful way: a timeout at a proxy, with the
// work half done and nothing said about which half. The ledger is written as it
// goes, so a member who reloads sees the same truth the next run would.
type mergeProgress struct {
	mu      sync.Mutex
	running bool
	done    bool
	// reported marks the completion report as shown. A finished run holds the
	// panel for one render and then hands it back to the standings.
	reported bool
	lines    []progressLine
	failed   int
}

type progressLine struct {
	ok   bool
	text string
}

// takeMergeReport reads the run's state and consumes the one unread completion
// report, so a finished run is shown once and then gives the panel back.
func (s *Server) takeMergeReport() (running bool, lines []progressLine, failed int, unread bool) {
	m := &s.merging
	m.mu.Lock()
	defer m.mu.Unlock()
	unread = m.done && !m.reported
	if unread {
		m.reported = true
	}
	return m.running, append([]progressLine(nil), m.lines...), m.failed, unread
}

// mergeRunning reports a run in flight, for callers that only need to know
// whether to start another.
func (s *Server) mergeRunning() bool {
	s.merging.mu.Lock()
	defer s.merging.mu.Unlock()
	return s.merging.running
}

// refusals are the techniques the destination would not take. They never reach
// the ledger, so they would vanish with the completion report if the panel did
// not keep them: "we sent forty and they have thirty-eight" is the sort of
// thing a member should not have to notice for themselves.
func (s *Server) refusals() []progressLine {
	s.merging.mu.Lock()
	defer s.merging.mu.Unlock()
	var out []progressLine
	for _, l := range s.merging.lines {
		if !l.ok {
			out = append(out, l)
		}
	}
	return out
}

func mergeProgressPanel(running bool, lines []progressLine, failed int) string {
	var b strings.Builder
	b.WriteString(`<section class="panel"><h2>Contributing your playbook</h2>`)
	if running {
		b.WriteString(`<p class="hint">In progress. Reload to see how far it has got.</p>`)
	} else if failed > 0 {
		fmt.Fprintf(&b, `<p class="hint">Finished. %d techniques failed the organization’s safety checks; the rest were sent.</p>`, failed)
	} else {
		b.WriteString(`<p class="hint">Finished. Reload to see each technique’s review status.</p>`)
	}
	b.WriteString(`<ul class="feed">`)
	for _, l := range lines {
		class := "muted"
		if !l.ok {
			class = "warn"
		}
		fmt.Fprintf(&b, `<li class="%s">%s</li>`, class, html.EscapeString(l.text))
	}
	b.WriteString(`</ul></section>`)
	return b.String()
}

// handleMergeStart takes the join link and begins the reversible half of a
// merge: contribute, record, archive.
//
// It deliberately stops there. The rest of `tacit merge` stops this registry
// and unwires this machine's harnesses, and neither is a thing to do from
// inside a request being served by the process that would have to die doing it.
// The panel says which command finishes the job.
func (s *Server) handleMergeStart(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/techniques") {
		return
	}
	if !s.cfg().SingleMember() {
		http.Error(w, "only a single-member registry merges into an organization; an organization’s registry is not one member’s to contribute", http.StatusForbidden)
		return
	}
	if !s.isAdmin(s.sessionUser(r)) || !s.checkCSRF(r) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	link := r.PostFormValue("link")
	if _, _, ok := merge.SplitJoinLink(link); !ok {
		http.Error(w, "that is not a join link—the format is https://…/join/<token>", http.StatusBadRequest)
		return
	}
	if s.mergeRunning() {
		http.Redirect(w, r, "/team", http.StatusSeeOther)
		return
	}
	// The invitation is redeemed HERE, in the request, and the key is written to
	// the ledger before anything else happens (M3's order, unchanged): a join
	// token is spent by the exchange that redeems it, so a member who looks at
	// the preview and closes the tab must still be able to come back.
	//
	// One round trip, not forty, which is why it may happen in front of a form
	// POST when the contribution itself may not.
	hc := &http.Client{Timeout: 20 * time.Second}
	memberURL, memberKey, err := merge.ExchangeLink(hc, link, machineLabel())
	if err != nil {
		http.Error(w, "the invitation was refused: "+err.Error()+
			" (an expired link is a frequent cause)", http.StatusBadGateway)
		return
	}
	l, err := merge.OpenLedger(ledgerPath())
	if err != nil {
		http.Error(w, "the record of what has already been contributed could not be read: "+err.Error(),
			http.StatusInternalServerError)
		return
	}
	l.Destination, l.Key = memberURL, memberKey
	if l.StartedAt == "" {
		l.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := l.Save(); err != nil {
		http.Error(w, "the merge could not be recorded: "+err.Error(), http.StatusInternalServerError)
		return
	}
	techniques, events := s.mergeSource()
	rows := buildPreview(memberURL, memberKey, techniques, merge.OutcomeIndex(events), l)
	user := s.sessionUser(r)
	s.sendHTML(w, 200, s.renderShell(s.mergePreviewPage(r, memberURL, rows), user))
}

// handleMergeContribute starts the contribution the member ticked.
//
// Selection is the whole of what this adds over the old one-shot form, and the
// unticked half is recorded rather than forgotten: a technique left behind is
// not the same as one that has not gone yet, and the table would otherwise offer
// it again on every visit.
func (s *Server) handleMergeContribute(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/team") {
		return
	}
	if !s.cfg().SingleMember() {
		http.Error(w, "only a single-member registry contributes to an organization", http.StatusForbidden)
		return
	}
	if !s.isAdmin(s.sessionUser(r)) || !s.checkCSRF(r) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	if s.mergeRunning() {
		http.Redirect(w, r, "/team", http.StatusSeeOther)
		return
	}
	l, err := merge.OpenLedger(ledgerPath())
	if err != nil || l.Destination == "" || l.Key == "" {
		http.Error(w, "there is no invitation to contribute to; paste the join link again",
			http.StatusConflict)
		return
	}
	chosen := map[string]bool{}
	for _, id := range r.PostForm["id"] {
		chosen[id] = true
	}
	// Everything offered and not ticked is held. Ticking one that was held
	// before releases it, so a change of mind costs nothing.
	techniques, _ := s.mergeSource()
	var held []string
	var release []string
	for _, t := range techniques {
		if _, sent := l.AlreadySent(t.ID, l.Destination); sent {
			continue
		}
		if chosen[t.ID] {
			release = append(release, t.ID)
			continue
		}
		held = append(held, t.ID)
	}
	l.Release(release)
	if err := l.Hold(held); err != nil {
		http.Error(w, "your choices could not be recorded: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(chosen) == 0 {
		// Nothing to send is a legitimate answer — a member may have opened the
		// table to look. The holds are saved; there is no run to start.
		http.Redirect(w, r, "/team", http.StatusSeeOther)
		return
	}
	s.merging.mu.Lock()
	s.merging.running, s.merging.done, s.merging.reported = true, false, false
	s.merging.lines, s.merging.failed = nil, 0
	s.merging.mu.Unlock()
	go s.runMerge(sortedIDs(chosen))
	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

// runMerge is the contribute-and-archive half, off the request.
//
// Off the request because contributing forty techniques is forty round trips to
// somebody else's registry, and a form POST holding a connection open for all of
// them fails in the least useful way: a timeout at a proxy, the work half done,
// and nothing said about which half. The ledger is written as it goes, so a
// member who reloads sees the same truth the next run would.
//
// The exchange has already happened in the request that produced the preview, so
// what arrives here is the member's selection.
func (s *Server) runMerge(chosen []string) {
	m := &s.merging
	note := func(ok bool, format string, a ...any) {
		m.mu.Lock()
		m.lines = append(m.lines, progressLine{ok: ok, text: fmt.Sprintf(format, a...)})
		if !ok {
			m.failed++
		}
		m.mu.Unlock()
	}
	defer func() {
		m.mu.Lock()
		m.running, m.done = false, true
		m.mu.Unlock()
	}()

	hc := &http.Client{Timeout: 60 * time.Second}
	l, err := merge.OpenLedger(ledgerPath())
	if err != nil {
		note(false, "the record of what has already been contributed could not be read: %v", err)
		return
	}
	memberURL := l.Destination
	note(true, "joined %s", memberURL)

	all, events := s.mergeSource()
	techniques := onlyChosen(all, chosen)
	sent, failed, fatal := merge.ContributeAll(hc, l, techniques,
		merge.OutcomeIndex(events), merge.FirstSeen(events), note)
	if fatal != nil {
		note(false, "%v", fatal)
		return
	}
	note(true, "%d contributed, %d refused", sent, failed)

	// The archive before anything can destroy it — which here means before the
	// member runs the command that retires this registry.
	path := merge.ArchivePath(config.DataHome(), l.ArchivePath)
	// Everything, not just what was contributed: the archive is this registry's
	// own evidence before anything can destroy it, and a technique the member
	// kept back is exactly the one whose history nothing else will hold.
	archive := merge.NewArchive(s.PublishedBase(), memberURL, all, events, l.SentTo(memberURL))
	if err := archive.Write(path); err != nil {
		note(false, "your outcomes could not be archived to %s: %v", path, err)
		return
	}
	l.ArchivePath = path
	_ = l.Save()
	note(true, "your own measurements archived to %s—they are not contributed", path)
}

// mergeSource reads what this registry would contribute: the techniques it
// actually serves, and its whole event log. A draft is something its owner has
// not decided about, and a retired one is something they decided against;
// neither is work to hand a reviewer.
func (s *Server) mergeSource() ([]contracts.Technique, []contracts.FeedbackEvent) {
	var techniques []contracts.Technique
	stored, err := s.Store.ListTechniques([]string{"stable", "mined"}, 0)
	if err == nil {
		for _, t := range stored {
			techniques = append(techniques, contracts.Technique(t))
		}
	}
	var events []contracts.FeedbackEvent
	if rows, err := s.Store.AllEvents(""); err == nil {
		for _, e := range rows {
			events = append(events, contracts.FeedbackEvent(e))
		}
	}
	return techniques, events
}

// Finishing a merge from the page that started it.
//
// The other half of `tacit merge`: point this machine's tools at the
// organization, stop this registry, and hand its hostname back. All three are
// available to the process serving the page — stopping itself is in fact the
// most reliable stop there is, with no unit file to find and no foreign
// terminal to negotiate with — so the panel offers a button rather than a
// command to go and type.
//
// What decides the shape of this handler is the release. Retiring an instance
// drops its tunnel (Server.retire sends bye and closes), so a member looking at
// this page THROUGH the ingress address is watching over the very connection
// the release destroys. Hence two orderings, chosen by how the request arrived:
//
//	locally      rewire, release, report what happened, then stop
//	over the tunnel  rewire, report, then release and stop
//
// The first can tell the member whether the address came back. The second
// cannot — they have already been sent to their organization by then — so it
// says what it is about to do, and the release's outcome goes to the log with
// `tacit merge --yes` as the retry.

// handleMergeFinish performs the irreversible half.
func (s *Server) handleMergeFinish(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/techniques") {
		return
	}
	if !s.cfg().SingleMember() {
		http.Error(w, "only a single-member registry retires itself", http.StatusForbidden)
		return
	}
	if !s.isAdmin(s.sessionUser(r)) || !s.checkCSRF(r) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	// The confirmation is a field rather than a second page, but it is not
	// decoration: everything below this line is one-way.
	if r.PostFormValue("confirm") != "yes" {
		http.Redirect(w, r, "/techniques", http.StatusSeeOther)
		return
	}
	// A registry that cannot stop itself must not release its address either.
	// The alternative is a live process serving on a name that resolves to
	// nothing, whose tunnel client would enrol again under a name nobody asked
	// for — the same failure the command refuses when it cannot stop a
	// registry somebody else started.
	if s.RequestStop == nil {
		http.Error(w, "this registry cannot stop itself, so it will not release its address either. "+
			"Finish from the machine it runs on:  tacit merge --yes", http.StatusConflict)
		return
	}
	l, err := merge.OpenLedger(ledgerPath())
	if err != nil || l.Destination == "" || len(l.Sent) == 0 {
		http.Error(w, "nothing has been contributed from this registry yet", http.StatusBadRequest)
		return
	}

	wired := s.rewireHarnesses(l)
	viaTunnel := s.viaIngress(r)
	released := ""
	if !viaTunnel {
		released = s.releaseAddress()
		s.retireProfile(l)
	}
	s.sendHTML(w, 200, s.renderShell(mergeFarewellPage(l, wired, released, viaTunnel), s.sessionUser(r)))

	// After the answer. Shutdown waits for this request to finish, so the page
	// above reaches the browser either way; the pause is for the tunnel, whose
	// last bytes are still in flight when the handler returns.
	go func() {
		time.Sleep(2 * time.Second)
		if viaTunnel {
			log.Printf("[merge] releasing the public address")
			if note := s.releaseAddress(); note != "" {
				log.Printf("[merge] %s", note)
			}
			s.retireProfile(l)
		}
		s.RequestStop("the owner finished merging into " + l.Destination)
	}()
}

// rewireHarnesses points this machine's tools at the organization by running
// the command that already knows how: `tacit connect --registry --key`.
//
// A subprocess rather than a shared package, and deliberately. The wiring
// touches a dozen harnesses' settings files and plugin registrations, all of it
// living in the CLI; reaching it from here would mean lifting that whole
// surface into a library so that two callers could do exactly the same thing.
// Running the command is the same work through the same code, and `tacit
// upgrade` already re-runs connect this way.
func (s *Server) rewireHarnesses(l *merge.Ledger) string {
	if s.WireHarnesses != nil {
		if err := s.WireHarnesses(l.Destination, l.Key); err != nil {
			return "Automatic tool setup failed. Run: tacit connect --registry " + l.Destination
		}
		return ""
	}
	self, err := os.Executable()
	if err != nil {
		return "could not find this binary to wire the harnesses: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "connect", "--registry", l.Destination, "--key", l.Key)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[merge] connect failed: %v\n%s", err, out)
		return "Automatic tool setup failed. Run: tacit connect --registry " + l.Destination
	}
	return ""
}

// releaseAddress hands the hostname back and returns the sentence to show.
func (s *Server) releaseAddress() string {
	name, err := merge.ReleaseAddress(config.ConfigDir(), s.cfg().PublishIngress, s.cfg().PublishTLS, s.Version)
	switch {
	case errors.Is(err, merge.ErrNoAddress):
		return "This registry had no public address to release."
	case err != nil:
		log.Printf("[merge] could not release the public address: %v", err)
		return "The public address could not be released (" + err.Error() + "). Run `tacit merge --yes` to retry it."
	case name == "":
		return "The ingress had no record of this registry; there was nothing to release."
	}
	return "Released " + name + ". The name is free again, and the enrollment slot with it."
}

// retireProfile marks this instance finished, so that nothing starts it again
// from the files it leaves behind — same data, same owner secret, and a ledger
// still reporting on an instance that no longer exists. `tacit serve` refuses a
// retired registry; `tacit init --start-over` is how a member deliberately gets
// a new one (internal/merge/retire.go).
//
// After the release, always: a profile swept aside before its hostname was
// handed back would take the instance key with it, and nothing could retry.
func (s *Server) retireProfile(l *merge.Ledger) {
	if err := l.MarkRetired(); err != nil {
		log.Printf("[merge] could not mark this registry retired: %v", err)
	}
}

// viaIngress reports that this request arrived over the tunnel rather than the
// local listener — which is to say, over the connection a release destroys.
func (s *Server) viaIngress(r *http.Request) bool {
	return sameHost(r.Host, s.PublishedBase())
}

// sameHost compares a request's Host with a base URL's, ignoring ports and
// case. Ports are ignored because the two are not written the same way: a
// request carries whatever the browser dialled, and the published base carries
// the ingress's scheme-default port or none at all.
func sameHost(requestHost, base string) bool {
	if strings.TrimSpace(base) == "" {
		return false
	}
	trim := func(h string) string {
		h = strings.ToLower(strings.TrimSpace(h))
		if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
			h = h[:i]
		}
		return h
	}
	pub := trim(displayHost(base))
	return pub != "" && trim(requestHost) == pub
}

// mergeFarewellPage is the last thing this registry serves.
func mergeFarewellPage(l *merge.Ledger, wired, released string, viaTunnel bool) page {
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="page-head"><h1>Merged into %s</h1></div>`,
		html.EscapeString(displayHost(l.Destination)))
	b.WriteString(`<section class="panel">`)
	fmt.Fprintf(&b, `<p>%d of your techniques are in their review queue.</p>`, len(l.SentTo(l.Destination)))
	if wired == "" {
		fmt.Fprintf(&b, `<p>This machine’s AI tools now retrieve from <a href="%s">%s</a>.</p>`,
			html.EscapeString(l.Destination), html.EscapeString(displayHost(l.Destination)))
	} else {
		fmt.Fprintf(&b, `<p class="warn">%s</p>`, html.EscapeString(wired))
	}
	if l.ArchivePath != "" {
		fmt.Fprintf(&b, `<p>Your own measurements are kept at <code>%s</code>. They were not contributed.</p>`,
			html.EscapeString(l.ArchivePath))
	}
	if released != "" {
		fmt.Fprintf(&b, `<p>%s</p>`, html.EscapeString(released))
	} else if viaTunnel {
		// Said in the future tense because it is: this page is travelling over
		// the connection the release is about to close.
		b.WriteString(`<p>This registry is stopping and releasing its public address. If the address still responds after a minute, run <code>tacit merge --yes</code>.</p>`)
	}
	b.WriteString(`<p>This registry is stopping and will not start again. This machine’s tools now use your organization’s playbook. The settings and data remain on disk.</p><p>To create a new registry on this machine, run <code>tacit init --start-over</code>. The command moves this registry’s data to a path ending in <code>.merged-…</code>, then creates an empty registry with a new address.</p>`)
	if startedByAService() {
		// Stopping is not enough when something is watching to start it again.
		// A graceful exit is a clean one, so systemd's Restart=on-failure will
		// not fire — but the next boot will, and what comes back has no
		// instance key, so it enrols itself as a stranger under a name nobody
		// chose. The unit's name is not something this process is told, so this
		// says what to look for rather than pretending to know.
		b.WriteString(`<p class="warn">A service manager started this registry and may create a new instance at the next boot. Disable the unit: <code>systemctl --user disable --now &lt;its unit&gt;</code>.</p>`)
	}
	fmt.Fprintf(&b, `<p><a class="btn" href="%s">Go to %s</a></p>`,
		html.EscapeString(l.Destination), html.EscapeString(displayHost(l.Destination)))
	b.WriteString(`</section>`)
	return page{active: "techniques", crumbs: []crumb{{label: "Playbook", href: playbookHome}, {label: "Merged", href: ""}},
		content: b.String()}
}

// startedByAService reports that this process was launched by systemd or
// launchd rather than by a person. systemd sets INVOCATION_ID for every service
// it starts; launchd sets XPC_SERVICE_NAME to something other than the shell's
// placeholder. Neither tells us the unit's name, which is why the page asks the
// member to look rather than naming it.
func startedByAService() bool {
	if os.Getenv("INVOCATION_ID") != "" {
		return true
	}
	name := os.Getenv("XPC_SERVICE_NAME")
	return name != "" && name != "0"
}

// mergeFinishOffer is the button, shown under the standings once there is
// something contributed to finish for.
func (s *Server) mergeFinishOffer(r *http.Request, l *merge.Ledger) string {
	if !s.isAdmin(s.sessionUser(r)) {
		return ""
	}
	if s.RequestStop == nil {
		// Honest about what this instance can do rather than offering a button
		// that would refuse.
		return `<p class="hint">Run <code>tacit merge --yes</code> on this machine to point its tools at the organization, stop this registry, and release its address.</p>`
	}
	return `<details class="fineprint"><summary>Finish the merge</summary>` +
		`<p class="sub">This points this machine’s AI tools at your organization, stops this registry, and gives its public address back. Your data stays on disk. The address does not come back: the same key will never resolve to it again, so anything still pointing there—a bookmark, another machine—stops working.</p>` +
		`<form method="post" action="/admin/merge/finish">` +
		fmt.Sprintf(`<input type="hidden" name="csrf" value="%s">`, html.EscapeString(s.csrfToken(r))) +
		`<label><input type="checkbox" name="confirm" value="yes" required> I understand this cannot be undone</label> ` +
		`<button class="danger" type="submit">Finish and retire this registry</button></form></details>`
}

// onlyChosen filters the contribution set to what the member ticked, keeping
// the source order so the progress lines read the way the table did.
func onlyChosen(all []contracts.Technique, chosen []string) []contracts.Technique {
	want := make(map[string]bool, len(chosen))
	for _, id := range chosen {
		want[id] = true
	}
	out := make([]contracts.Technique, 0, len(chosen))
	for _, t := range all {
		if want[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// machineLabel names this machine in the organization's member list, so the
// admin over there sees which laptop joined rather than an anonymous key.
func machineLabel() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "the owner’s registry"
}
