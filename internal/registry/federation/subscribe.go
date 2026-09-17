// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/pkg/feed"
	"github.com/opentacit/tacit/pkg/scrub"
)

// Trust levels for a subscription.
const (
	TrustReview     = "review"      // entries land in the drafts lane (default)
	TrustAutoAccept = "auto-accept" // entries go live, still decay-subject
)

// MaxBackfillPages bounds an initial archive walk.
const MaxBackfillPages = 20

// maxBackoffDoublings caps the failure backoff at 32 poll intervals.
const maxBackoffDoublings = 5

// Subscription is one feed the registry follows. State fields (pinned key,
// etag, seen map, poll status) are maintained by the poller.
type Subscription struct {
	ID          string `json:"id"`             // derived from the feed URL host+path
	Name        string `json:"name,omitempty"` // human label for the feed (falls back to DisplayName)
	FeedURL     string `json:"feed_url"`
	AuthToken   string `json:"auth_token,omitempty"` // sent as Bearer
	Trust       string `json:"trust"`
	Prefix      string `json:"prefix"` // local technique id namespace
	PollSeconds int    `json:"poll_seconds"`

	// poller state
	PinnedKey   string            `json:"pinned_key,omitempty"` // TOFU
	ProviderID  string            `json:"provider_id,omitempty"`
	ETag        string            `json:"etag,omitempty"`
	Seen        map[string]string `json:"seen,omitempty"` // entry id -> updated
	LastPoll    string            `json:"last_poll,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
	Failures    int               `json:"failures,omitempty"` // consecutive failed polls; drives the backoff
	Imported    int               `json:"imported"`
	Retractions []string          `json:"retractions,omitempty"` // pending review
	// Attestations remembers imported evidence for display only — it never
	// enters local outcomes (local evidence always dominates ranking).
	Attestations map[string]*feed.Attestation `json:"attestations,omitempty"`
}

// Subscriptions is the file-backed subscription set (node-local operational
// config, like the docs dir — not shared store state).
type Subscriptions struct {
	mu   *sync.Mutex
	path string
}

// locks holds one mutex per named resource, keyed by string. Callers build a
// fresh Subscriptions and a fresh Poller for every request (see
// web.Server.subscriptions and web.Server.Poller), so a mutex stored in the
// struct value would let two of them read, edit and write the same file at the
// same time. Keying the lock on the resource serializes them.
var locks sync.Map // string -> *sync.Mutex

func lockFor(name string) *sync.Mutex {
	mu, _ := locks.LoadOrStore(name, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// OpenSubscriptions binds the manager to DataDir/subscriptions.json.
func OpenSubscriptions(dataDir string) *Subscriptions {
	path := filepath.Join(dataDir, "subscriptions.json")
	return &Subscriptions{mu: lockFor("file:" + path), path: path}
}

func (s *Subscriptions) load() ([]Subscription, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var subs []Subscription
	if err := json.Unmarshal(raw, &subs); err != nil {
		return nil, fmt.Errorf("subscriptions file corrupt: %w", err)
	}
	return subs, nil
}

func (s *Subscriptions) save(subs []Subscription) error {
	sort.Slice(subs, func(i, j int) bool { return subs[i].ID < subs[j].ID })
	raw, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(s.path, raw, 0o600)
}

// List returns all subscriptions.
func (s *Subscriptions) List() ([]Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Put adds or replaces a subscription (matched by ID), normalizing defaults.
func (s *Subscriptions) Put(sub Subscription) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sub.FeedURL == "" {
		return sub, errors.New("feed_url required")
	}
	if sub.ID == "" {
		sub.ID = SubscriptionID(sub.FeedURL)
	}
	if sub.Name == "" {
		sub.Name = defaultFeedName(sub.FeedURL)
	}
	if sub.Trust == "" {
		sub.Trust = TrustReview
	}
	if sub.Trust != TrustReview && sub.Trust != TrustAutoAccept {
		return sub, fmt.Errorf("trust must be %q or %q", TrustReview, TrustAutoAccept)
	}
	if sub.Prefix == "" {
		sub.Prefix = defaultPrefix(sub.FeedURL)
	}
	if sub.PollSeconds < 60 {
		sub.PollSeconds = 3600
	}
	subs, err := s.load()
	if err != nil {
		return sub, err
	}
	replaced := false
	for i := range subs {
		if subs[i].ID == sub.ID {
			// keep poller state across config edits
			sub.PinnedKey = subs[i].PinnedKey
			sub.ProviderID = subs[i].ProviderID
			sub.ETag = subs[i].ETag
			sub.Seen = subs[i].Seen
			sub.Imported = subs[i].Imported
			sub.Attestations = subs[i].Attestations
			sub.Retractions = subs[i].Retractions
			subs[i] = sub
			replaced = true
		}
	}
	if !replaced {
		subs = append(subs, sub)
	}
	return sub, s.save(subs)
}

// Delete removes a subscription; imported techniques remain (flagged by their
// federated provenance) — removal is never a silent mass-delete.
func (s *Subscriptions) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs, err := s.load()
	if err != nil {
		return false, err
	}
	kept := subs[:0]
	found := false
	for _, sub := range subs {
		if sub.ID == id {
			found = true
			continue
		}
		kept = append(kept, sub)
	}
	if !found {
		return false, nil
	}
	return true, s.save(kept)
}

// update writes back the poller-state fields of sub and nothing else. A poll
// runs for as long as the feed walk takes, so the record it started from may be
// stale by the time it finishes: re-loading here and merging only the fields
// the poller owns keeps a config edit made during the poll (the mirror image of
// Put, which keeps poller state across a config edit). A subscription deleted
// during the poll stays deleted.
func (s *Subscriptions) update(sub Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs, err := s.load()
	if err != nil {
		return err
	}
	for i := range subs {
		if subs[i].ID != sub.ID {
			continue
		}
		subs[i].PinnedKey = sub.PinnedKey
		subs[i].ProviderID = sub.ProviderID
		subs[i].ETag = sub.ETag
		subs[i].Seen = sub.Seen
		subs[i].LastPoll = sub.LastPoll
		subs[i].LastError = sub.LastError
		subs[i].Failures = sub.Failures
		subs[i].Imported = sub.Imported
		subs[i].Retractions = sub.Retractions
		subs[i].Attestations = sub.Attestations
	}
	return s.save(subs)
}

// SubscriptionID derives a stable id from a feed URL.
func SubscriptionID(feedURL string) string {
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return models.Slugify(feedURL)
	}
	return models.Slugify(u.Host + u.Path)
}

func defaultPrefix(feedURL string) string {
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return "ext/" + models.Slugify(feedURL)
	}
	return "ext/" + models.Slugify(u.Hostname())
}

// DisplayName is the feed's human label: its configured Name, else a readable
// default derived from the URL (the channel segment of a /f/<channel>/feed.json
// path, else the host) — never the raw URL, so links read as names.
func (s Subscription) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	return defaultFeedName(s.FeedURL)
}

func defaultFeedName(feedURL string) string {
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return feedURL
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "f" {
		return parts[1] // /f/<channel>/feed.json -> channel
	}
	return u.Host
}

// PollResult reports one subscription's poll.
type PollResult struct {
	SubscriptionID string `json:"subscription_id"`
	Imported       int    `json:"imported"`
	Updated        int    `json:"updated"`
	Retracted      int    `json:"retracted"`
	Skipped        bool   `json:"skipped,omitempty"` // 304 not modified
	Error          string `json:"error,omitempty"`
}

// Poller fetches subscribed feeds and lands entries in the store.
type Poller struct {
	Store storage.Store
	Subs  *Subscriptions
	HTTP  *http.Client
	Now   func() time.Time // tests override
}

func (p *Poller) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now().UTC()
}

// PollAll polls every subscription (used by the admin endpoint and tests).
func (p *Poller) PollAll() []PollResult {
	subs, err := p.Subs.List()
	if err != nil {
		return []PollResult{{Error: err.Error()}}
	}
	out := make([]PollResult, 0, len(subs))
	for _, sub := range subs {
		out = append(out, p.pollOne(sub))
	}
	return out
}

// PollDue polls only subscriptions whose interval has elapsed (the scheduler
// entrypoint).
func (p *Poller) PollDue() []PollResult {
	subs, err := p.Subs.List()
	if err != nil {
		return []PollResult{{Error: err.Error()}}
	}
	var out []PollResult
	for _, sub := range subs {
		if !p.due(sub) {
			continue
		}
		out = append(out, p.pollOne(sub))
	}
	return out
}

// due reports whether a subscription's poll interval has elapsed. Consecutive
// failures double the wait, up to 32 intervals, so a feed that is down or
// misconfigured is retried at a falling rate rather than every interval. One
// success clears the count.
func (p *Poller) due(sub Subscription) bool {
	if sub.LastPoll == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339Nano, sub.LastPoll)
	if err != nil {
		return true
	}
	wait := time.Duration(sub.PollSeconds) * time.Second
	if n := sub.Failures; n > 0 {
		wait <<= min(n, maxBackoffDoublings)
	}
	return p.now().Sub(last) >= wait
}

// pollOne polls one subscription. It holds that subscription's poll lock for
// the whole walk, so the scheduler tick and the admin "poll now" button cannot
// run the same feed twice at once and overwrite each other's progress.
func (p *Poller) pollOne(sub Subscription) PollResult {
	mu := lockFor("poll:" + p.Subs.path + "|" + sub.ID)
	mu.Lock()
	defer mu.Unlock()

	res := PollResult{SubscriptionID: sub.ID}
	fail := func(err error) PollResult {
		res.Error = err.Error()
		sub.LastError = err.Error()
		sub.LastPoll = p.now().Format(time.RFC3339Nano)
		sub.Failures++
		sub.Imported += res.Imported // entries landed before the failure still count
		_ = p.Subs.update(sub)
		return res
	}

	doc, etag, notModified, err := p.fetchFeed(&sub, sub.FeedURL)
	if err != nil {
		return fail(err)
	}
	if notModified {
		res.Skipped = true
		sub.LastError = ""
		sub.Failures = 0
		sub.LastPoll = p.now().Format(time.RFC3339Nano)
		_ = p.Subs.update(sub)
		return res
	}

	// TOFU identity and key discovery/pinning via the provider descriptor.
	descriptor, err := p.discoverDescriptor(sub.FeedURL)
	if err != nil {
		return fail(fmt.Errorf("provider discovery: %w", err))
	}
	if sub.PinnedKey == "" {
		key := descriptor.PublicKey
		sub.PinnedKey = key
	} else if descriptor.PublicKey != sub.PinnedKey {
		return fail(fmt.Errorf("provider key changed (pinned %s..., current %s...). Re-add the subscription to accept the new key",
			head(sub.PinnedKey), head(descriptor.PublicKey)))
	}
	if sub.ProviderID != "" && sub.ProviderID != descriptor.Provider.ID {
		return fail(fmt.Errorf("provider id changed from %q to %q", sub.ProviderID, descriptor.Provider.ID))
	}
	sub.ProviderID = descriptor.Provider.ID
	channelID := doc.Channel.ID
	if err := validateFeedIdentity(doc, descriptor, channelID); err != nil {
		return fail(err)
	}

	if sub.Seen == nil {
		sub.Seen = map[string]string{}
	}
	if sub.Attestations == nil {
		sub.Attestations = map[string]*feed.Attestation{}
	}

	// walk pages: head first, then archives until exhausted or bounded
	pages := 0
	var unverified []string
	for {
		for _, entry := range doc.Entries {
			if !strings.HasPrefix(entry.ID, strings.TrimRight(sub.ProviderID, "/")+"/techniques/") {
				return fail(fmt.Errorf("entry id %q is outside provider namespace %q", entry.ID, sub.ProviderID))
			}
			// dedupe by CONTENT when the entry carries a hash: a provider
			// that merely re-stamps updated must not re-trigger review of an
			// already-imported technique (the flip-back loop); retractions have
			// no hash and key on updated
			if sub.Seen[entry.ID] == seenKey(entry) {
				continue
			}
			if err := feed.Verify(entry, sub.PinnedKey); err != nil {
				// One entry that will not verify must not wedge the feed: the
				// entries after it, retractions among them, still have to
				// land. Skip it and leave it unseen, so a corrected republish
				// is picked up rather than dropped.
				unverified = append(unverified, entry.ID)
				continue
			}
			switch entry.Kind {
			case feed.KindTechnique:
				updated, err := p.importTechnique(&sub, entry, channelID)
				if err != nil {
					return fail(err)
				}
				if updated {
					res.Updated++
				} else {
					res.Imported++
				}
			case feed.KindRetraction:
				if p.flagRetraction(&sub, entry) {
					res.Retracted++
				}
			}
			sub.Seen[entry.ID] = seenKey(entry)
		}
		if doc.NextPage == "" || pages >= MaxBackfillPages {
			break
		}
		pages++
		next, _, _, err := p.fetchFeed(&sub, doc.NextPage)
		if err != nil {
			break // archives are best-effort once the head succeeded
		}
		if err := validateFeedIdentity(next, descriptor, channelID); err != nil {
			return fail(err)
		}
		doc = next
	}

	sub.ETag = etag
	sub.LastError = unverifiedSummary(unverified)
	sub.Failures = 0 // the feed answered; a bad entry in it is not a fetch failure
	sub.LastPoll = p.now().Format(time.RFC3339Nano)
	sub.Imported += res.Imported
	res.Error = sub.LastError
	if err := p.Subs.update(sub); err != nil {
		res.Error = err.Error()
	}
	return res
}

// unverifiedSummary reports the entries this poll could not verify, naming the
// first few so an operator can go and look at them.
func unverifiedSummary(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	named := ids
	extra := ""
	if len(named) > 3 {
		extra = fmt.Sprintf(" and %d more", len(named)-3)
		named = named[:3]
	}
	word := "entries"
	if len(ids) == 1 {
		word = "entry"
	}
	return fmt.Sprintf("skipped %d %s that failed the signature check: %s%s",
		len(ids), word, strings.Join(named, ", "), extra)
}

func (p *Poller) fetchFeed(sub *Subscription, feedURL string) (feed.Feed, string, bool, error) {
	req, err := http.NewRequest("GET", feedURL, nil)
	if err != nil {
		return feed.Feed{}, "", false, err
	}
	if sub.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+sub.AuthToken)
	}
	if sub.ETag != "" && feedURL == sub.FeedURL {
		req.Header.Set("If-None-Match", sub.ETag)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return feed.Feed{}, "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return feed.Feed{}, sub.ETag, true, nil
	}
	if resp.StatusCode != 200 {
		return feed.Feed{}, "", false, fmt.Errorf("feed %s: HTTP %d", feedURL, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return feed.Feed{}, "", false, err
	}
	var doc feed.Feed
	if err := json.Unmarshal(raw, &doc); err != nil {
		return feed.Feed{}, "", false, fmt.Errorf("feed %s: not a feed document: %w", feedURL, err)
	}
	if doc.Version != feed.Version {
		return feed.Feed{}, "", false, fmt.Errorf("feed %s: unsupported version %q", feedURL, doc.Version)
	}
	return doc, resp.Header.Get("ETag"), false, nil
}

// discoverDescriptor fetches the provider descriptor derived from the feed URL
// (<base>/f/... -> <base>/.well-known/tacit.json).
func (p *Poller) discoverDescriptor(feedURL string) (feed.Descriptor, error) {
	base := feedURL
	if i := strings.Index(base, "/f/"); i >= 0 {
		base = base[:i]
	}
	req, err := http.NewRequest("GET", strings.TrimRight(base, "/")+feed.WellKnownPath, nil)
	if err != nil {
		return feed.Descriptor{}, err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return feed.Descriptor{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return feed.Descriptor{}, fmt.Errorf("descriptor: HTTP %d", resp.StatusCode)
	}
	var d feed.Descriptor
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return feed.Descriptor{}, err
	}
	if _, err := feed.ParsePublicKey(d.PublicKey); err != nil {
		return feed.Descriptor{}, err
	}
	if d.Provider.ID == "" {
		return feed.Descriptor{}, errors.New("descriptor provider id is empty")
	}
	return d, nil
}

func validateFeedIdentity(doc feed.Feed, descriptor feed.Descriptor, channelID string) error {
	if doc.Channel.Provider != descriptor.Provider.ID {
		return fmt.Errorf("feed provider %q does not match descriptor %q", doc.Channel.Provider, descriptor.Provider.ID)
	}
	if doc.Channel.ID != channelID {
		return fmt.Errorf("feed channel %q does not match head channel %q", doc.Channel.ID, channelID)
	}
	for _, channel := range descriptor.Channels {
		if channel.ID == channelID {
			return nil
		}
	}
	return fmt.Errorf("feed channel %q is not in provider descriptor", channelID)
}

// importTechnique lands one verified technique entry locally: prefixed id, federated
// provenance, drafts lane under review trust, and a scrub pass — a foreign
// feed is a trust boundary like any other. An UPDATE to a technique already in
// service lands as a revision draft (docs/design/revision-design.md) rather than
// overwriting: the serving technique keeps serving while the change is reviewed.
func (p *Poller) importTechnique(sub *Subscription, entry feed.Entry, channelID string) (updated bool, err error) {
	if entry.Technique == nil {
		return false, fmt.Errorf("entry %s: technique entry without technique", entry.ID)
	}
	src := *entry.Technique
	localID := sub.Prefix + "/" + src.ID

	base, exists, err := p.Store.GetTechnique(localID)
	if err != nil {
		return false, err
	}

	c := src
	c.ID = localID
	c.Provenance = "federated"
	c.Source = entry.ID
	c.Channels = nil // never re-publish an import by default
	c.Embedding = nil
	c.EmbeddingModel = ""
	c.EmbeddingDim = 0
	c.Recipe, _ = scrubText(c.Recipe)
	c.Description, _ = scrubText(c.Description)
	c.BeforeAfter, _ = scrubText(c.BeforeAfter)
	now := models.Now()
	importedAt := now
	if exists && base.Origin != nil {
		importedAt = base.Origin.ImportedAt
	}
	c.Origin = &models.FederationOrigin{ProviderID: sub.ProviderID, ProviderKey: sub.PinnedKey,
		EntryID: entry.ID, ChannelID: channelID, ContentHash: entry.ContentHash, ImportedAt: importedAt}
	c.UpdatedAt = now
	if !exists {
		c.CreatedAt = now
	}
	switch {
	case sub.Trust == TrustAutoAccept:
		if c.Status == "" || c.Status == "draft" {
			c.Status = "stable"
		}
		// Auto-accept overwrites in place; keep the outgoing version viewable.
		if exists {
			if err := p.Store.ArchiveTechniqueVersion(base); err != nil {
				return false, err
			}
		}
	case exists && base.Status != "draft":
		// Reviewed update of a technique in service: a revision draft in a fixed
		// "@upstream" slot — one pending upstream proposal per technique, newer
		// polls replace it, and it can never clobber a member's own "@N"
		// revision drafts. The base technique is not touched.
		c.ID = localID + "@upstream"
		c.Supersedes = localID
		c.BaseVersion = base.Version
		c.RevisionNote = "federated update from " + entry.ID
		c.Status = "draft"
		c.CreatedAt = now
	default: // new import (or re-import while still a draft): the drafts lane
		c.Status = "draft"
	}
	if err := p.Store.UpsertTechnique(c); err != nil {
		return false, err
	}
	if entry.Attestation != nil {
		sub.Attestations[localID] = entry.Attestation
	} else {
		delete(sub.Attestations, localID)
	}
	return exists, nil
}

// flagRetraction records an upstream retraction for review — never a silent
// delete (the org may have adopted and localized the move).
func (p *Poller) flagRetraction(sub *Subscription, entry feed.Entry) bool {
	techniqueID := entry.ID
	if i := strings.LastIndex(techniqueID, "/techniques/"); i >= 0 {
		techniqueID = techniqueID[i+len("/techniques/"):]
	}
	localID := sub.Prefix + "/" + techniqueID
	if _, exists, _ := p.Store.GetTechnique(localID); !exists {
		return false
	}
	note := fmt.Sprintf("%s: upstream retraction: %s", localID, entry.Reason)
	for _, existing := range sub.Retractions {
		if existing == note {
			return false
		}
	}
	sub.Retractions = append(sub.Retractions, note)
	return true
}

// seenKey is the change-detection key for an entry: content hash when
// present (techniques), updated timestamp otherwise (retractions).
func seenKey(e feed.Entry) string {
	if e.ContentHash != "" {
		return e.ContentHash
	}
	return e.Updated
}

func scrubText(text string) (string, []scrub.Finding) {
	if text == "" {
		return "", nil
	}
	return scrub.Redact(text)
}

func head(s string) string {
	if len(s) > 24 {
		return s[:24]
	}
	return s
}
