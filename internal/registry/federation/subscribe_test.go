// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
	"github.com/opentacit/tacit/pkg/feed"
)

// provider spins up a REAL publisher (its own store + key) behind httptest —
// the subscriber is exercised against the actual protocol surface.
type provider struct {
	pub *Publisher
	st  *store.Store
	ts  *httptest.Server
}

func newProvider(t *testing.T) *provider {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key, err := feed.LoadOrCreateKey(filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatal(err)
	}
	pub := &Publisher{Store: st, Key: key, ProviderName: "Provider Org"}
	mux := http.NewServeMux()
	mux.HandleFunc(feed.WellKnownPath, func(w http.ResponseWriter, r *http.Request) {
		d, err := pub.Descriptor()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("GET /f/{channel}/feed.json", func(w http.ResponseWriter, r *http.Request) {
		doc, err := pub.Feed(r.PathValue("channel"), r.URL.Query().Get("page"))
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("ETag", doc.Updated) // real conditional-GET semantics
		if r.Header.Get("If-None-Match") == doc.Updated {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	pub.ProviderID = ts.URL
	pub.BaseURL = ts.URL
	return &provider{pub: pub, st: st, ts: ts}
}

func (pr *provider) publish(t *testing.T, id string, status string) models.Technique {
	t.Helper()
	c := models.Technique{
		ID: id, Name: "Name " + id, Description: "desc", Scope: "org", Status: status,
		Provenance: "curated", Version: 1, Recipe: "recipe " + id,
		Channels:  []string{"general"},
		CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}
	if err := pr.st.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func newSubscriber(t *testing.T, feedURL, trust string) (*Poller, *store.Store, Subscription) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	subs := OpenSubscriptions(t.TempDir())
	sub, err := subs.Put(Subscription{FeedURL: feedURL, Trust: trust, Prefix: "ext/provider"})
	if err != nil {
		t.Fatal(err)
	}
	return &Poller{Store: st, Subs: subs}, st, sub
}

func TestImportLandsInTheDraftsLane(t *testing.T) {
	pr := newProvider(t)
	pr.publish(t, "warehouse-move", "stable")
	poller, st, _ := newSubscriber(t, pr.ts.URL+"/f/general/feed.json", TrustReview)

	results := poller.PollAll()
	if len(results) != 1 || results[0].Error != "" || results[0].Imported != 1 {
		t.Fatalf("poll: %+v", results)
	}
	got, ok, _ := st.GetTechnique("ext/provider/warehouse-move")
	if !ok {
		t.Fatal("imported technique missing")
	}
	if got.Status != "draft" {
		t.Fatalf("review trust must land as draft, got %q", got.Status)
	}
	if got.Provenance != "federated" || !strings.Contains(got.Source, "/techniques/warehouse-move") {
		t.Fatalf("provenance: %+v", got)
	}
	if len(got.Channels) != 0 {
		t.Fatal("import must not be re-published by default")
	}
	if got.Origin == nil || got.Origin.ProviderID != pr.ts.URL || got.Origin.ProviderKey == "" ||
		got.Origin.ChannelID != "general" || got.Origin.EntryID != got.Source || got.Origin.ContentHash == "" || got.Origin.ImportedAt == "" {
		t.Fatalf("federation origin missing: %+v", got.Origin)
	}

	// second poll: conditional GET short-circuits, nothing re-imported
	results = poller.PollAll()
	if !results[0].Skipped || results[0].Imported != 0 {
		t.Fatalf("second poll: %+v", results)
	}
}

func TestAutoAcceptGoesLiveAndUpdatesReenterOnReviewTrust(t *testing.T) {
	pr := newProvider(t)
	c := pr.publish(t, "move", "stable")

	auto, autoStore, _ := newSubscriber(t, pr.ts.URL+"/f/general/feed.json", TrustAutoAccept)
	if res := auto.PollAll(); res[0].Error != "" {
		t.Fatal(res[0].Error)
	}
	got, _, _ := autoStore.GetTechnique("ext/provider/move")
	if got.Status != "stable" {
		t.Fatalf("auto-accept status: %q", got.Status)
	}

	review, reviewStore, _ := newSubscriber(t, pr.ts.URL+"/f/general/feed.json", TrustReview)
	review.PollAll()
	// reviewer promotes the draft locally
	local, _, _ := reviewStore.GetTechnique("ext/provider/move")
	local.Status = "stable"
	if err := reviewStore.UpsertTechnique(local); err != nil {
		t.Fatal(err)
	}
	// upstream edits the technique -> the update must re-enter review
	c.Recipe = "recipe v2"
	c.Version = 2
	c.UpdatedAt = models.Now()
	if err := pr.st.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	res := review.PollAll()
	if res[0].Error != "" || res[0].Updated != 1 {
		t.Fatalf("update poll: %+v", res)
	}
	// The update re-enters review as a revision draft; the promoted technique
	// keeps serving its reviewed text (docs/design/revision-design.md) — review
	// must not mean an availability gap.
	local, _, _ = reviewStore.GetTechnique("ext/provider/move")
	if local.Status != "stable" || local.Recipe == "recipe v2" {
		t.Fatalf("upstream update clobbered the serving technique: %+v", local)
	}
	rev, ok, _ := reviewStore.GetTechnique("ext/provider/move@upstream")
	if !ok || rev.Status != "draft" || rev.Recipe != "recipe v2" ||
		rev.Supersedes != "ext/provider/move" || rev.BaseVersion != local.Version {
		t.Fatalf("upstream update must land as a pinned revision draft: %+v", rev)
	}
	if rev.Origin == nil || local.Origin == nil || rev.Origin.ImportedAt != local.Origin.ImportedAt ||
		rev.Origin.ContentHash == local.Origin.ContentHash {
		t.Fatalf("upstream revision lineage did not keep import time and update hash: base=%+v revision=%+v", local.Origin, rev.Origin)
	}
}

func TestRetractionFlagsNeverDeletes(t *testing.T) {
	pr := newProvider(t)
	c := pr.publish(t, "fading-move", "stable")
	poller, st, sub := newSubscriber(t, pr.ts.URL+"/f/general/feed.json", TrustAutoAccept)
	poller.PollAll()

	c.Status = "retired"
	c.UpdatedAt = models.Now()
	_ = pr.st.UpsertTechnique(c)
	res := poller.PollAll()
	if res[0].Retracted != 1 {
		t.Fatalf("retraction not counted: %+v", res)
	}
	if _, ok, _ := st.GetTechnique("ext/provider/fading-move"); !ok {
		t.Fatal("retraction deleted the local technique")
	}
	subs, _ := poller.Subs.List()
	if len(subs) != 1 || len(subs[0].Retractions) != 1 {
		t.Fatalf("retraction not recorded for review: %+v", subs)
	}
	_ = sub
}

func TestKeyChangeRefusesImport(t *testing.T) {
	pr := newProvider(t)
	pr.publish(t, "move", "stable")
	poller, _, _ := newSubscriber(t, pr.ts.URL+"/f/general/feed.json", TrustReview)
	if res := poller.PollAll(); res[0].Error != "" {
		t.Fatal(res[0].Error)
	}

	// provider key rotates (or is replaced by an attacker)
	newKey, _ := feed.LoadOrCreateKey(filepath.Join(t.TempDir(), "k2"))
	pr.pub.Key = newKey
	pr.publish(t, "evil-move", "stable")

	res := poller.PollAll()
	if res[0].Error == "" || !strings.Contains(res[0].Error, "key changed") {
		t.Fatalf("key change accepted: %+v", res)
	}
	if _, ok, _ := poller.Store.GetTechnique("ext/provider/evil-move"); ok {
		t.Fatal("technique imported under a changed key")
	}
}

func TestImportScrubsSecrets(t *testing.T) {
	pr := newProvider(t)
	c := pr.publish(t, "leaky-move", "stable")
	c.Recipe = "use api_key = \"9f8e7d6c5b4a39281706aabb\" to call the service"
	c.UpdatedAt = models.Now()
	_ = pr.st.UpsertTechnique(c)

	poller, st, _ := newSubscriber(t, pr.ts.URL+"/f/general/feed.json", TrustReview)
	if res := poller.PollAll(); res[0].Error != "" {
		t.Fatal(res[0].Error)
	}
	got, _, _ := st.GetTechnique("ext/provider/leaky-move")
	if strings.Contains(got.Recipe, "9f8e7d6c") {
		t.Fatalf("secret survived import: %s", got.Recipe)
	}
	if !strings.Contains(got.Recipe, "[redacted:") {
		t.Fatalf("no redaction marker: %s", got.Recipe)
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	subs := OpenSubscriptions(t.TempDir())
	sub, err := subs.Put(Subscription{FeedURL: "https://x.example.org/f/general/feed.json"})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Trust != TrustReview || sub.Prefix != "ext/x-example-org" || sub.PollSeconds != 3600 {
		t.Fatalf("defaults: %+v", sub)
	}
	sub.PinnedKey, sub.ProviderID = "ed25519:pinned", "https://provider.example"
	if err := subs.update(sub); err != nil {
		t.Fatal(err)
	}
	edited, err := subs.Put(Subscription{ID: sub.ID, FeedURL: sub.FeedURL, Name: "New name"})
	if err != nil {
		t.Fatal(err)
	}
	if edited.PinnedKey != sub.PinnedKey || edited.ProviderID != sub.ProviderID {
		t.Fatalf("config edit lost pinned identity: %+v", edited)
	}
	if _, err := subs.Put(Subscription{FeedURL: "u", Trust: "yolo"}); err == nil {
		t.Fatal("bad trust accepted")
	}
	list, _ := subs.List()
	if len(list) != 1 {
		t.Fatalf("list: %d", len(list))
	}
	ok, err := subs.Delete(sub.ID)
	if err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if ok, _ := subs.Delete(sub.ID); ok {
		t.Fatal("double delete reported success")
	}
}

func TestDeletingSubscriptionKeepsImportedOrigin(t *testing.T) {
	pr := newProvider(t)
	pr.publish(t, "kept-move", "stable")
	poller, st, sub := newSubscriber(t, pr.ts.URL+"/f/general/feed.json", TrustReview)
	if res := poller.PollAll()[0]; res.Error != "" {
		t.Fatal(res.Error)
	}
	before, _, _ := st.GetTechnique("ext/provider/kept-move")
	if ok, err := poller.Subs.Delete(sub.ID); err != nil || !ok {
		t.Fatalf("delete subscription: ok=%v err=%v", ok, err)
	}
	after, ok, err := st.GetTechnique(before.ID)
	if err != nil || !ok || after.Origin == nil || *after.Origin != *before.Origin {
		t.Fatalf("subscription deletion lost imported lineage: %+v ok=%v err=%v", after.Origin, ok, err)
	}
}

func TestTimestampOnlyRefreshNeverRevertsAPromotion(t *testing.T) {
	pr := newProvider(t)
	c := pr.publish(t, "steady-move", "stable")
	poller, st, _ := newSubscriber(t, pr.ts.URL+"/f/general/feed.json", TrustReview)
	poller.PollAll()

	// the reviewer promotes the imported draft
	local, _, _ := st.GetTechnique("ext/provider/steady-move")
	local.Status = "stable"
	if err := st.UpsertTechnique(local); err != nil {
		t.Fatal(err)
	}

	// upstream re-stamps updated_at WITHOUT changing content (a no-op pass)
	c.UpdatedAt = models.Now()
	if err := pr.st.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	res := poller.PollAll()
	if res[0].Error != "" || res[0].Updated != 0 {
		t.Fatalf("timestamp-only refresh imported: %+v", res)
	}
	local, _, _ = st.GetTechnique("ext/provider/steady-move")
	if local.Status != "stable" {
		t.Fatalf("promotion reverted by a no-op refresh: %s", local.Status)
	}

	// a REAL content change still re-enters review — as a revision draft,
	// while the promoted technique keeps serving (docs/design/revision-design.md)
	c.Recipe = "genuinely new recipe"
	c.UpdatedAt = models.Now()
	_ = pr.st.UpsertTechnique(c)
	res = poller.PollAll()
	if res[0].Updated != 1 {
		t.Fatalf("real change not imported: %+v", res)
	}
	local, _, _ = st.GetTechnique("ext/provider/steady-move")
	if local.Status != "stable" {
		t.Fatalf("real change took the serving technique out of retrieval: %s", local.Status)
	}
	rev, ok, _ := st.GetTechnique("ext/provider/steady-move@upstream")
	if !ok || rev.Status != "draft" || rev.Recipe != "genuinely new recipe" {
		t.Fatalf("real change must land as a revision draft: %+v", rev)
	}
}

// gateTransport holds the first feed fetch open until the test releases it, so
// a test can act while a poll is in flight.
type gateTransport struct {
	rt      http.RoundTripper
	once    sync.Once
	hit     chan struct{}
	release chan struct{}
}

func newGate() *gateTransport {
	return &gateTransport{rt: http.DefaultTransport, hit: make(chan struct{}), release: make(chan struct{})}
}

func (g *gateTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/feed.json") {
		g.once.Do(func() {
			close(g.hit)
			<-g.release
		})
	}
	return g.rt.RoundTrip(r)
}

func TestConfigEditDuringAPollSurvives(t *testing.T) {
	pr := newProvider(t)
	pr.publish(t, "move", "stable")
	poller, _, sub := newSubscriber(t, pr.ts.URL+"/f/general/feed.json", TrustReview)
	gate := newGate()
	poller.HTTP = &http.Client{Transport: gate}

	done := make(chan PollResult, 1)
	go func() { done <- poller.PollAll()[0] }()

	// the operator edits the subscription while the poll is walking the feed
	<-gate.hit
	edit := sub
	edit.Trust = TrustAutoAccept
	edit.PollSeconds = 600
	edit.Name = "Renamed feed"
	if _, err := poller.Subs.Put(edit); err != nil {
		t.Fatal(err)
	}
	close(gate.release)

	if res := <-done; res.Error != "" {
		t.Fatal(res.Error)
	}
	list, err := poller.Subs.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if list[0].Trust != TrustAutoAccept || list[0].PollSeconds != 600 || list[0].Name != "Renamed feed" {
		t.Fatalf("the poll reverted the config edit: %+v", list[0])
	}
	if list[0].ETag == "" || len(list[0].Seen) == 0 {
		t.Fatalf("the config edit lost poller state: %+v", list[0])
	}
}

// poisonProvider serves the real provider's descriptor and feed with one
// entry's signature corrupted and moved to the front of the entry list.
func poisonProvider(t *testing.T, pr *provider, poisonID string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(feed.WellKnownPath, func(w http.ResponseWriter, r *http.Request) {
		d, err := pr.pub.Descriptor()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("GET /f/{channel}/feed.json", func(w http.ResponseWriter, r *http.Request) {
		doc, err := pr.pub.Feed(r.PathValue("channel"), r.URL.Query().Get("page"))
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		var bad, rest []feed.Entry
		for _, e := range doc.Entries {
			if strings.HasSuffix(e.ID, "/"+poisonID) {
				e.Signature = "ed25519:" + base64.StdEncoding.EncodeToString(make([]byte, 64))
				bad = append(bad, e)
				continue
			}
			rest = append(rest, e)
		}
		doc.Entries = append(bad, rest...)
		w.Header().Set("ETag", doc.Updated)
		_ = json.NewEncoder(w).Encode(doc)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestBadEntrySkippedAndLaterEntriesStillImport(t *testing.T) {
	pr := newProvider(t)
	pr.publish(t, "poison-move", "stable")
	pr.publish(t, "good-move", "stable")
	ts := poisonProvider(t, pr, "poison-move")
	poller, st, _ := newSubscriber(t, ts.URL+"/f/general/feed.json", TrustReview)

	res := poller.PollAll()[0]
	if res.Imported != 1 {
		t.Fatalf("the entry after the bad one did not import: %+v", res)
	}
	if _, ok, _ := st.GetTechnique("ext/provider/good-move"); !ok {
		t.Fatal("good technique missing")
	}
	if _, ok, _ := st.GetTechnique("ext/provider/poison-move"); ok {
		t.Fatal("unverifiable technique imported")
	}
	if res.Error == "" || !strings.Contains(res.Error, "signature") {
		t.Fatalf("the bad entry was not reported: %+v", res)
	}
	// the bad entry stays unseen, so a corrected republish still lands
	list, _ := poller.Subs.List()
	for id := range list[0].Seen {
		if strings.HasSuffix(id, "/poison-move") {
			t.Fatal("the bad entry was marked seen")
		}
	}
}

func TestFailedPollsBackOff(t *testing.T) {
	pr := newProvider(t)
	pr.publish(t, "move", "stable")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	subs := OpenSubscriptions(t.TempDir())
	sub, err := subs.Put(Subscription{
		FeedURL: pr.ts.URL + "/f/general/feed.json", Prefix: "ext/provider", PollSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	clock := base
	poller := &Poller{Store: st, Subs: subs, Now: func() time.Time { return clock }}

	// three failed polls behind us: the wait is 60s << 3 = 8 minutes
	if err := subs.update(Subscription{ID: sub.ID, LastPoll: base.Format(time.RFC3339Nano), Failures: 3}); err != nil {
		t.Fatal(err)
	}
	clock = base.Add(2 * time.Minute)
	if res := poller.PollDue(); len(res) != 0 {
		t.Fatalf("polled inside the backoff window: %+v", res)
	}
	clock = base.Add(10 * time.Minute)
	res := poller.PollDue()
	if len(res) != 1 || res[0].Error != "" {
		t.Fatalf("backoff never expired: %+v", res)
	}
	list, _ := subs.List()
	if list[0].Failures != 0 {
		t.Fatalf("a good poll must clear the failure count: %+v", list[0])
	}

	// a failing feed counts up again
	pr.ts.Close()
	clock = base.Add(20 * time.Minute)
	if res := poller.PollDue(); len(res) != 1 || res[0].Error == "" {
		t.Fatalf("dead feed polled clean: %+v", res)
	}
	if list, _ := subs.List(); list[0].Failures != 1 {
		t.Fatalf("failure not counted: %+v", list[0])
	}
	clock = base.Add(21 * time.Minute)
	if res := poller.PollDue(); len(res) != 0 {
		t.Fatalf("polled inside the backoff window after one failure: %+v", res)
	}
}

func TestSubscriptionDisplayName(t *testing.T) {
	// An explicit name wins.
	if got := (Subscription{Name: "My Feed", FeedURL: "https://x/f/general/feed.json"}).DisplayName(); got != "My Feed" {
		t.Fatalf("explicit name = %q", got)
	}
	// The default reads the channel from a /f/<channel>/feed.json path.
	if got := (Subscription{FeedURL: "https://acme.example.com/f/platform/feed.json"}).DisplayName(); got != "platform" {
		t.Fatalf("channel default = %q; want platform", got)
	}
	// Otherwise it falls back to the host — never the raw URL.
	if got := (Subscription{FeedURL: "https://acme.example.com/other/path"}).DisplayName(); got != "acme.example.com" {
		t.Fatalf("host default = %q; want acme.example.com", got)
	}
}
