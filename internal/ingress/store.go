// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
)

// Instance is one published registry: a name, the hash of the token that
// proves ownership of it, and the little the ingress knows about the thing at
// the other end. There is no technique here, no event, no member — the route table
// is the whole of the ingress's memory.
type Instance struct {
	Name    string    `json:"name"`            // the subdomain label
	Owner   string    `json:"owner,omitempty"` // set only if an operator recorded one
	Note    string    `json:"note,omitempty"`
	Created time.Time `json:"created"`

	// KeyHash is sha256 of the registry's instance key. The key itself is
	// generated on the registry and never sent anywhere but here, so this file
	// holds nothing that can be replayed as a registry — and the ingress
	// operator cannot mint an identity even if they wanted to.
	KeyHash string `json:"key_hash"`

	// Fingerprint is the short public form of the key, so a console can name an
	// instance's identity without being able to use it.
	Fingerprint string `json:"fingerprint,omitempty"`

	// Enrolled records that this instance registered itself rather than being
	// created by an operator. Everything self-enrols now; the flag stays
	// because an operator-created record is still possible and the distinction
	// belongs in the audit trail.
	Enrolled bool `json:"enrolled,omitempty"`

	// Disabled is the per-instance kill switch
	// (docs/distribution/ingress.md): the route stops answering and the
	// tunnel is refused, without deleting the name.
	Disabled bool `json:"disabled,omitempty"`

	// Observed at handshake, kept for the console.
	LastSeen time.Time `json:"last_seen,omitempty"`
	Version  string    `json:"version,omitempty"`
	// Source is where the registry last connected FROM, resolved through any
	// front proxy (clientaddr.go). Persisted rather than read from the live
	// tunnel, so the instances table can still say where an offline registry
	// lives — which is exactly when an operator is looking.
	Source string `json:"source,omitempty"`

	// Members and the three beside it are what the registry last reported about
	// its own size: member machines seen in its last day, week, month and year
	// (protocol.go, Stats). Kept with the time they arrived, because figures
	// from a registry that has been offline for a month are history rather than
	// news, and the console says which they are.
	//
	// Reported, never derived. The proxy does not count anyone.
	Members      int       `json:"members,omitempty"`
	MembersDay   int       `json:"members_day,omitempty"`
	MembersMonth int       `json:"members_month,omitempty"`
	MembersYear  int       `json:"members_year,omitempty"`
	MembersAt    time.Time `json:"members_at,omitempty"`
	// MembersWindows says the last report filled in all four windows, so a zero
	// in one of them means none rather than not-asked. Records written before
	// the windows existed carry only the week, and say so by leaving this false.
	MembersWindows bool `json:"members_windows,omitempty"`

	// Days is one sample per day of what this instance reported, oldest first.
	//
	// It exists because the registry cannot answer for the past. A member key
	// holds a single last-seen timestamp, overwritten on every use, so nobody —
	// not the registry, not the proxy — can reconstruct how many machines were
	// active on a day that has gone. A trend can only be accumulated, and it can
	// only start when somebody starts keeping it, which is why this is written
	// from the first report rather than added when a chart is wanted.
	//
	// One row a day, a year deep: small enough to sit in the route table, long
	// enough to answer the question an operator asks a year in.
	Days []DaySample `json:"days,omitempty"`
}

// Store is the instance registry, persisted as one JSON file. It is small by
// construction — a route table, not a database — so the whole thing is held in
// memory and rewritten atomically on change.
//
// Losing it is the worst thing that can happen to this process. An instance that
// cannot be resolved from its key enrols again and is given a NEW hostname, so a
// lost table renames every tenant at once and every address anybody wrote down
// stops working. Hence the three things here that a route table would not
// otherwise need: every save is flushed to disk and copied to a spare file, an
// unreadable table is moved aside rather than allowed to stop the ingress, and
// the two writes that fire on every handshake are coalesced instead of taking
// the write lock for a whole-file rewrite each.
type Store struct {
	path string
	mu   sync.RWMutex
	byID map[string]*Instance

	// warning is what OpenStore had to do to a table it could not read. It is
	// held rather than logged because the store is opened before there is a
	// logger to say it to, and it is never written again after OpenStore returns.
	warning string

	// dirty says an observation is owed to the file: something changed that is
	// not worth a save of its own but must not be lost. The flusher goroutine
	// clears it on its own clock.
	dirty  bool
	closed bool
	stop   chan struct{}
	done   chan struct{}
}

// backupSuffix names the spare copy of the route table, written after every
// successful save. It is what an unreadable primary recovers from.
const backupSuffix = ".bak"

// saveInterval is how long a coalesced change may sit unwritten. Short enough
// that a crash loses a last-seen timestamp rather than anything anyone chose,
// long enough that a hundred registries reconnecting at once cost one save.
const saveInterval = 2 * time.Second

// OpenStore loads (or creates) the instance store under dir.
func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "instances.json"), byID: map[string]*Instance{}}
	list, err := readInstances(s.path)
	if err != nil {
		list = s.recoverTable(err)
	}
	for _, in := range list {
		s.byID[in.Name] = in
	}
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go s.flushEvery(saveInterval)
	return s, nil
}

// readInstances parses one route-table file. A file that is not there is an
// empty table, which is what a first run has; a file that is there and will not
// parse is the caller's problem to recover from.
func readInstances(path string) ([]*Instance, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Instance
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return list, nil
}

// recoverTable deals with a route table that would not load. Refusing to start
// was the old answer, and it takes every published registry down over one bad
// file with no way back short of an operator finding the problem by hand. So:
// move the bad file aside, keep it for whoever wants to look at it, try the
// spare copy, and record what happened for the server to log.
func (s *Store) recoverTable(cause error) []*Instance {
	aside := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().Unix())
	s.warning = fmt.Sprintf("the route table would not load (%v)", cause)
	if err := os.Rename(s.path, aside); err != nil {
		s.warning += fmt.Sprintf(" and could not be moved aside (%v)", err)
	} else {
		s.warning += fmt.Sprintf("; the bad file is kept at %s", aside)
	}
	list, err := readInstances(s.path + backupSuffix)
	switch {
	case err != nil:
		s.warning += fmt.Sprintf("; the spare copy will not load either (%v), so this ingress "+
			"starts with no routes and every registry that reconnects gets a new hostname", err)
		return nil
	case len(list) == 0:
		s.warning += "; there is no spare copy, so this ingress starts with no routes and " +
			"every registry that reconnects gets a new hostname"
		return nil
	default:
		s.warning += fmt.Sprintf("; recovered %d instances from the spare copy", len(list))
		return list
	}
}

// Warning is what OpenStore had to do to a route table it could not read, in
// words an operator can act on. Empty when there was nothing to say.
func (s *Store) Warning() string { return s.warning }

// flushEvery writes what the handshake path coalesced, until Close stops it.
func (s *Store) flushEvery(every time.Duration) {
	defer close(s.done)
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			s.mu.Lock()
			if s.dirty {
				_ = s.saveLocked()
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

// Close stops the background save and writes anything still owed. It is called
// before the operations log closes, so a shutdown keeps what a reconnect storm
// coalesced.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	close(s.stop)
	<-s.done

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.saveLocked()
}

// List returns every instance, newest first.
func (s *Store) List() []Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Instance, 0, len(s.byID))
	for _, in := range s.byID {
		out = append(out, *in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Get returns one instance by name.
func (s *Store) Get(name string) (Instance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.byID[name]
	if !ok {
		return Instance{}, false
	}
	return *in, true
}

// ErrFull reports that the ingress has reached its instance ceiling.
var ErrFull = errors.New("this ingress is not accepting new registries")

// Resolve returns the instance a key belongs to, if it is already enrolled.
func (s *Store) Resolve(key string) (Instance, bool) {
	want := hashKey(key)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, in := range s.byID {
		// The hash is a fixed-length hex string of a random 32-byte secret, so
		// a plain comparison leaks nothing an attacker can use.
		if in.KeyHash == want {
			return *in, true
		}
	}
	return Instance{}, false
}

// Enroll resolves a key to its instance, registering it the first time.
//
// This is the whole of enrolment: a registry presents the key it generated for
// itself and gets a hostname back. Nobody approves it, and the second return
// value reports whether this call is what created it — the ingress logs a new
// enrolment, and says nothing about the thousandth reconnection.
func (s *Store) Enroll(key string, maxInstances int) (Instance, bool, error) {
	if in, ok := s.Resolve(key); ok {
		return in, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check under the write lock: two connections from one registry can race
	// here, and enrolling it twice would give one registry two names.
	want := hashKey(key)
	for _, in := range s.byID {
		if in.KeyHash == want {
			return *in, false, nil
		}
	}
	if maxInstances > 0 && len(s.byID) >= maxInstances {
		return Instance{}, false, ErrFull
	}
	name := s.freeNameLocked()
	if name == "" {
		return Instance{}, false, errors.New("could not allocate a hostname")
	}
	in := &Instance{
		Name:        name,
		Created:     time.Now().UTC(),
		KeyHash:     want,
		Fingerprint: FingerprintOf(key),
		Enrolled:    true,
	}
	s.byID[name] = in
	if err := s.saveLocked(); err != nil {
		delete(s.byID, name)
		return Instance{}, false, err
	}
	return *in, true, nil
}

// nameRe is a DNS label: what a hostname can actually be.
var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$`)

// reservedNames are labels an instance may not take, because the ingress itself
// answers on them or is likely to.
var reservedNames = map[string]bool{
	"ingress": true, "www": true, "admin": true, "api": true, "console": true,
	"mail": true, "ns1": true, "ns2": true, "_acme-challenge": true,
}

// ErrBadName reports a label that could not be a hostname.
var ErrBadName = errors.New("a name is 3–32 characters of lowercase letters, digits and hyphens")

// ErrTaken reports a name already in use.
var ErrTaken = errors.New("that name is taken")

// ErrReserved reports a name the ingress keeps for itself.
var ErrReserved = errors.New("that name is reserved by the ingress")

// ErrNoInstance reports a name that is not in the route table.
var ErrNoInstance = errors.New("no such instance")

// Rename moves an instance to a chosen hostname.
//
// This is the answer to "I want to choose my own name" without reopening
// enrolment to name requests. Enrolment stays anonymous and allocates from the
// vocabulary; choosing a name is an operator act, performed in a console that
// already sits behind an identity provider and an address allowlist. A stranger
// publishing a laptop gets cedar-hollow and cannot ask for anything else.
//
// The instance keeps its key, so the registry at the other end is unchanged —
// it simply learns a new address when it next connects, which is why the caller
// drops the tunnel afterwards.
func (s *Store) Rename(from, to string) error {
	to = strings.ToLower(strings.TrimSpace(to))
	if !nameRe.MatchString(to) {
		return ErrBadName
	}
	if reservedNames[to] {
		return ErrReserved
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.byID[from]
	if !ok {
		return ErrNoInstance
	}
	if from == to {
		return nil
	}
	if _, clash := s.byID[to]; clash {
		return ErrTaken
	}
	delete(s.byID, from)
	in.Name = to
	s.byID[to] = in
	if err := s.saveLocked(); err != nil {
		// Put it back rather than leave the table disagreeing with the file.
		delete(s.byID, to)
		in.Name = from
		s.byID[from] = in
		return err
	}
	return nil
}

// SetOwnerNote records an operator's annotation against an instance. The
// registry never sends either; they exist so a person running the ingress can
// write down who a name belongs to once they know.
func (s *Store) SetOwnerNote(name, owner, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.byID[name]
	if !ok {
		return ErrNoInstance
	}
	oldOwner, oldNote := in.Owner, in.Note
	in.Owner, in.Note = strings.ToLower(strings.TrimSpace(owner)), note
	if err := s.saveLocked(); err != nil {
		// Put them back rather than leave the table disagreeing with the file, the
		// same way Rename does. An operator told the change failed and then shown
		// it applied has been told two different things.
		in.Owner, in.Note = oldOwner, oldNote
		return err
	}
	return nil
}

// SetDisabled flips the per-instance kill switch.
func (s *Store) SetDisabled(name string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.byID[name]
	if !ok {
		return ErrNoInstance
	}
	in.Disabled = disabled
	return s.saveLocked()
}

// Delete drops a name. There is no hold before reissue: a deleted name is
// immediately claimable by the next enrolment, and the console says so.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, name)
	return s.saveLocked()
}

// Touch records that an instance handshook, with the version it reported.
func (s *Store) Touch(name, version, source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.byID[name]
	if !ok {
		return
	}
	in.LastSeen = time.Now().UTC()
	if version != "" {
		in.Version = version
	}
	if source != "" {
		in.Source = source
	}
	s.markDirtyLocked()
}

// DaySample is one day's figures, as reported on that day.
type DaySample struct {
	// Day is the UTC date, YYYY-MM-DD. A date rather than a timestamp because
	// that is the resolution the sample has: it is whatever the registry last
	// said that day, not a measurement taken at an instant.
	Day   string `json:"day"`
	Day1  int    `json:"d,omitempty"` // machines seen in the 24h before the report
	Week  int    `json:"w,omitempty"`
	Month int    `json:"m,omitempty"`
	Year  int    `json:"y,omitempty"`
}

// maxDaySamples is how far back the trend is kept: a year and a little, so a
// year-long view is always complete at the edges.
const maxDaySamples = 400

// setStats records what an instance reported about itself, and keeps one sample
// a day so a trend accumulates. windows is the sender's own account of whether
// it filled in every window (protocol.go, Stats).
//
// A registry that says nothing keeps whatever it said last: silence is not a
// claim that it has no members, and overwriting a real figure with a zero would
// say it was. Within a day the newest report wins — a day's sample is the last
// thing the registry said that day, not the first, so a registry that starts
// quiet and gets busy is recorded busy.
func (s *Store) setStats(name string, st Stats, windows bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.byID[name]
	if !ok {
		return
	}
	now := time.Now().UTC()
	in.Members, in.MembersDay = st.Week, st.Day
	in.MembersMonth, in.MembersYear = st.Month, st.Year
	in.MembersAt, in.MembersWindows = now, windows

	today := now.Format("2006-01-02")
	sample := DaySample{Day: today, Day1: st.Day, Week: st.Week, Month: st.Month, Year: st.Year}
	if n := len(in.Days); n > 0 && in.Days[n-1].Day == today {
		in.Days[n-1] = sample
	} else {
		in.Days = append(in.Days, sample)
	}
	if n := len(in.Days); n > maxDaySamples {
		in.Days = append([]DaySample{}, in.Days[n-maxDaySamples:]...)
	}
	s.markDirtyLocked()
}

// markDirtyLocked leaves a save to the flusher. It is what the two writes on the
// tunnel's own path use — Touch on every handshake, setStats on every report —
// because neither is worth a whole-file rewrite under the write lock, and a
// hundred registries reconnecting at once would otherwise serialize a hundred of
// them. Both record an observation, and the newest observation wins, so a save
// two seconds late loses nothing anybody chose.
//
// Once the store is closed there is no flusher left to owe it to, so the save
// happens now. Best effort: the caller is a handshake with nowhere to put an
// error.
func (s *Store) markDirtyLocked() {
	s.dirty = true
	if s.closed {
		_ = s.saveLocked()
	}
}

func (s *Store) saveLocked() error {
	list := make([]*Instance, 0, len(s.byID))
	for _, in := range s.byID {
		list = append(list, in)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	// Flushed to disk: a rename says nothing about whether what it renamed ever
	// arrived, and losing this file renames every published registry.
	if err := fsx.WriteFileAtomicSync(s.path, b, 0o600); err != nil {
		return err
	}
	s.dirty = false
	// The spare copy, written second on purpose: a failure here leaves a good
	// primary and a stale spare, which is the harmless way round, so it is not
	// worth failing an operator's action over. It is not flushed, because it is a
	// copy of bytes already on the disk and it is only ever read when the primary
	// will not parse — a cause a power cut does not share.
	_ = fsx.WriteFileAtomic(s.path+backupSuffix, b, 0o600)
	return nil
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// The generated-name vocabulary. Two concrete words that sound like a place,
// because the result is a hostname a person will read aloud on a call, type
// from memory, and see in a browser bar for years. "olive-harbor" is a name;
// "i-04f2c9a1" is an inventory number.
//
// The pair space is deliberately much larger than any plausible instance count,
// so a first pick almost always lands free and names stay two words. A
// collision takes a numeric suffix rather than random characters, which keeps
// the second olive-harbor readable too.
var (
	nameFirst = []string{
		"olive", "amber", "cobalt", "slate", "cedar", "quartz", "willow", "ember",
		"indigo", "basalt", "marble", "copper", "hazel", "juniper", "onyx", "pewter",
		"saffron", "teal", "umber", "azure", "cinder", "clover", "dusk", "flint",
		"garnet", "ivory", "jasper", "linen", "mica", "nutmeg", "opal", "pine",
		"russet", "sable", "thistle", "verdant", "wheat", "zinc", "coral", "fern",
	}
	nameSecond = []string{
		"harbor", "meadow", "summit", "ridge", "delta", "haven", "bay", "grove",
		"field", "point", "reach", "vale", "bluff", "cove", "range", "shore",
		"basin", "canyon", "ford", "glade", "hollow", "island", "landing", "mesa",
		"narrows", "orchard", "prairie", "quarry", "river", "spire", "terrace", "wharf",
	}
)

// freeNameLocked allocates an unused hostname. The caller holds the write lock.
func (s *Store) freeNameLocked() string {
	b := make([]byte, 2)
	for range 40 {
		_, _ = rand.Read(b)
		base := nameFirst[int(b[0])%len(nameFirst)] + "-" + nameSecond[int(b[1])%len(nameSecond)]
		if _, clash := s.byID[base]; !clash {
			return base
		}
		// That pair is taken: try it with a suffix before giving up on it, so a
		// busy ingress degrades to olive-harbor-2 rather than to noise.
		for n := 2; n <= 9; n++ {
			candidate := fmt.Sprintf("%s-%d", base, n)
			if _, clash := s.byID[candidate]; !clash {
				return candidate
			}
		}
	}
	return ""
}
