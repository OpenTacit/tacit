// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Member-local correction ledger: how often THIS member has had to tell the
// agent the same thing again.
//
// It exists to give a single member the axis the miner's anonymity gate denies
// them. A mined technique needs ≥K distinct origins across ≥M distinct cohorts
// — a threshold one person can never clear, and rightly so, because it is what
// keeps a proposal from being traceable to whoever produced it. Swap the axis:
// K repetitions by one member over time. Nine sessions in three weeks that open
// with the same correction describe a technique, and the reason for the cohort
// threshold does not apply when the only origin is the person reading the
// result (docs/design/single-user-value.md).
//
// What is stored is a hash, a count and two timestamps. Never the text. The
// hash proves repetition, which is all the past occurrences are needed for; the
// occurrence that crosses the threshold is live, in hand, and is what a draft is
// written from. That ordering is deliberate — a ledger holding the wording of
// every correction a member ever typed would be a transcript by instalments,
// and no local file should be one.
package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
)

// correctionRetention matches the session log's long horizon: a repetition
// spread over a season is exactly the one worth noticing.
const correctionRetention = 400 * 24 * time.Hour

// CorrectionThreshold is how many times one correction must recur before it is
// worth raising. Low enough to catch a real habit inside a month, high enough
// that a phrase typed twice in one afternoon is not a finding.
const CorrectionThreshold = 4

// correctionCues open a turn that is putting the agent right. Conservative on
// purpose: a false positive costs a member a suggestion about something they
// never said twice, which is the most expensive kind of wrong this system can
// be. Matched against the start of the message, or as a whole phrase inside it.
var correctionOpeners = []string{
	"no,", "no.", "no -", "no —", "nope", "wrong", "that's wrong", "thats wrong",
	"stop", "don't", "dont", "do not", "actually,", "revert", "undo",
	"that's not", "thats not", "not like that",
}

var correctionPhrases = []string{
	"instead of", "i said", "i told you", "you were supposed to", "as i said",
	"like i said", "again -", "again,", "i already said", "i asked you to",
	"you keep", "stop doing", "don't use", "dont use", "never use",
	"that's not what", "thats not what",
}

var correctionSplit = regexp.MustCompile(`[^a-z0-9]+`)

// correctionTokens are dropped before hashing: the words that mark a message as
// a correction carry no information about WHICH correction it is, so keeping
// them would collide every correction the member ever makes into one bucket.
var correctionTokens = map[string]bool{
	"actually": true, "instead": true, "again": true, "said": true, "told": true,
	"supposed": true, "stop": true, "dont": true, "wrong": true, "never": true,
	"keep": true, "asked": true, "please": true, "should": true, "would": true,
	"could": true, "that": true, "this": true, "what": true, "when": true,
	"there": true, "here": true, "with": true, "from": true, "into": true,
	"your": true, "youre": true, "need": true, "want": true, "make": true,
	"just": true, "like": true, "have": true, "does": true, "doesnt": true,
}

// correctionSignatureTokens is how many words form a correction's fingerprint.
//
// It is a fixed-width signature rather than the whole distinctive vocabulary,
// and the width is what makes the clustering work at all. Nobody phrases a
// correction identically twice: "no, use fuser -k on the port, don't pkill the
// process" and "I said use fuser -k on the port instead of pkill" share three
// distinctive words and differ on a fourth, so a fingerprint over everything
// they contain would place them in different buckets and the ledger would count
// one repetition as two firsts. Taking the three lexicographically smallest
// tokens is the same trick a minhash signature uses: a deterministic sample of
// the set, stable under the words that vary.
//
// The cost is collisions — two unrelated corrections whose three smallest
// distinctive words agree land together. Three words is narrow enough to
// cluster real rewordings and wide enough that the collision is rare, and the
// threshold of four repetitions plus a draft written from the LIVE occurrence
// bounds what a collision can actually cause: a suggestion about the wrong
// thing, once.
const correctionSignatureTokens = 3

// correctionMinTokens is the floor below which a message has no fingerprint. A
// correction carrying one distinctive word cannot be told from any other, and
// tracking it would fill the ledger with a bucket that means nothing.
const correctionMinTokens = 2

// isCorrection reports whether a member's message is putting the agent right.
func isCorrection(msg string) bool {
	text := strings.ToLower(strings.TrimSpace(msg))
	if text == "" {
		return false
	}
	for _, opener := range correctionOpeners {
		if strings.HasPrefix(text, opener) {
			return true
		}
	}
	for _, phrase := range correctionPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// correctionShape reduces a message to the fingerprint two sayings of the same
// correction share: its distinctive words, deduplicated, sorted so word order
// cannot separate them, and capped. Returns "" when nothing distinctive
// survives — a bare "no" repeats constantly and means nothing on its own.
func correctionShape(msg string) string {
	seen := map[string]bool{}
	var toks []string
	for _, t := range correctionSplit.Split(strings.ToLower(msg), -1) {
		if len(t) < 4 || stopwords[t] || correctionTokens[t] || seen[t] {
			continue
		}
		seen[t] = true
		toks = append(toks, t)
	}
	if len(toks) < correctionMinTokens {
		return ""
	}
	sort.Strings(toks)
	if len(toks) > correctionSignatureTokens {
		toks = toks[:correctionSignatureTokens]
	}
	return strings.Join(toks, " ")
}

// correctionEntry is one repeated correction, as counts.
type correctionEntry struct {
	Hash  string    `json:"hash"`
	Count int       `json:"count"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
	// Raised records that a draft has already been written from this
	// correction, so crossing the threshold raises it once rather than every
	// time the member says it again.
	Raised bool `json:"raised,omitempty"`
}

// correctionLedger is the on-disk file and its in-memory state.
type correctionLedger struct {
	mu      sync.Mutex
	path    string // "" -> in-memory only
	salt    string
	now     func() time.Time
	entries map[string]*correctionEntry
	dirty   bool
}

// correctionLedgerPath sits in the member's state directory, beside the session
// log — one place a member can inspect or delete in one act, and not their home
// directory (config.StateDir).
func correctionLedgerPath(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "corrections.jsonl")
}

func loadCorrectionLedger(path, salt string, now func() time.Time) *correctionLedger {
	if now == nil {
		now = time.Now
	}
	l := &correctionLedger{path: path, salt: salt, now: now,
		entries: map[string]*correctionEntry{}}
	if path == "" {
		return l
	}
	cutoff := now().Add(-correctionRetention)
	for _, line := range readLines(path) {
		var e correctionEntry
		if json.Unmarshal([]byte(line), &e) != nil || e.Hash == "" {
			l.dirty = true
			continue
		}
		if e.Last.Before(cutoff) {
			l.dirty = true
			continue
		}
		if prev := l.entries[e.Hash]; prev != nil {
			l.dirty = true
			if e.Count > prev.Count {
				*prev = e // last write for a hash wins
			}
			continue
		}
		copied := e
		l.entries[e.Hash] = &copied
	}
	if l.dirty {
		l.rewriteLocked()
	}
	return l
}

// note records one correction and returns its running count, or 0 when the
// message is not a correction or carries nothing distinctive. A nil receiver
// is a no-op so callers need not guard.
func (l *correctionLedger) note(msg string) (hash string, count int) {
	if l == nil {
		return "", 0
	}
	if !isCorrection(msg) {
		return "", 0
	}
	shape := correctionShape(msg)
	if shape == "" {
		return "", 0
	}
	sum := sha256.Sum256([]byte(l.salt + "\x00" + shape))
	hash = hex.EncodeToString(sum[:16])

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UTC()
	e := l.entries[hash]
	if e == nil {
		e = &correctionEntry{Hash: hash, First: now}
		l.entries[hash] = e
	}
	e.Count++
	e.Last = now
	l.dirty = true
	l.persistLocked(e)
	return hash, e.Count
}

// markRaised records that a draft has been written from this correction.
func (l *correctionLedger) markRaised(hash string) {
	if l == nil || hash == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.entries[hash]; e != nil && !e.Raised {
		e.Raised = true
		l.persistLocked(e)
	}
}

// alreadyRaised reports whether a draft has been written from this correction.
func (l *correctionLedger) alreadyRaised(hash string) bool {
	if l == nil || hash == "" {
		return true // no ledger, nothing to raise from
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[hash]
	return e == nil || e.Raised
}

// repeated returns the corrections at or over the threshold, most-repeated
// first — the read side of the ledger.
func (l *correctionLedger) repeated(threshold int) []correctionEntry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []correctionEntry
	for _, e := range l.entries {
		if e.Count >= threshold {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Last.After(out[j].Last)
	})
	return out
}

// persistLocked appends the entry's new state. The file is a log of states, not
// of events, so a reload takes the last line for each hash and a rewrite
// collapses it. Caller holds l.mu.
func (l *correctionLedger) persistLocked(e *correctionEntry) {
	if l.path == "" {
		return
	}
	if line, err := json.Marshal(e); err == nil {
		appendLine(l.path, line)
	}
}

func (l *correctionLedger) rewriteLocked() {
	if l.path == "" {
		return
	}
	hashes := make([]string, 0, len(l.entries))
	for h := range l.entries {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	var b strings.Builder
	for _, h := range hashes {
		if line, err := json.Marshal(l.entries[h]); err == nil {
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	if err := fsx.WriteFileAtomic(l.path, []byte(b.String()), 0o600); err != nil {
		log.Printf("[tacit-hooks] correction ledger not compacted into %s: %v", l.path, err)
		return
	}
	l.dirty = false
}

// flush compacts the ledger. Called when the agent idles out, so the file
// records one line per correction rather than one per saying of it.
func (l *correctionLedger) flush() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dirty {
		l.rewriteLocked()
	}
}
