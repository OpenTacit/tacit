// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package federation implements the registry's ends of the technique-feed
// protocol (docs/federation/federation-design.md; wire types in tacit/pkg/feed):
// publishing channels of explicitly-externalized techniques, and subscribing to
// external providers whose entries land in the drafts lane.
//
// Publication is always an explicit act — a technique with no channels never
// leaves the registry. Attestations are aggregate-only and k-thresholded.
package federation

import (
	"crypto/ed25519"
	"fmt"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/pkg/contracts"
	"github.com/opentacit/tacit/pkg/feed"
)

// AttestationMinN is the minimum adopted sample size before a published
// entry carries outcome evidence — the same aggregate-only discipline the
// product applies internally, restated at the federation boundary.
const AttestationMinN = 5

// PageSize is the number of entries in the head feed document; older
// entries page via next_page.
const PageSize = 50

// Publisher renders this registry's federation surface.
type Publisher struct {
	Store        storage.Store
	Key          ed25519.PrivateKey
	ProviderID   string // stable identity: the URI namespace for entry ids
	ProviderName string
	BaseURL      string // where feeds/techniques are served; follows the current address
	// PublicMembers, when set, supplies the computed Public channel's membership
	// (public.go). It is a function rather than a slice because the publisher is
	// rebuilt per request while membership is recomputed on the scheduler tick,
	// and nil means the channel is not being served — which is how the staged
	// upgrade state serves everything EXCEPT the commons.
	PublicMembers func() []PublicMember
}

// publicServed is the set of technique ids the Public channel currently carries.
func (p *Publisher) publicServed() map[string]bool {
	if p.PublicMembers == nil {
		return nil
	}
	return ServedIDs(p.PublicMembers())
}

// Channels lists every channel any technique is published to, with counts.
func (p *Publisher) Channels() (map[string]int, error) {
	techniques, err := p.Store.ListTechniques(nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, c := range techniques {
		for _, ch := range c.Channels {
			out[ch]++
		}
	}
	// The Public channel is computed, so it appears here on the strength of its
	// membership rather than of any field on a technique.
	if served := p.publicServed(); len(served) > 0 {
		out[PublicChannel] = len(served)
	}
	return out, nil
}

// Descriptor builds the provider document for /.well-known/tacit.json.
func (p *Publisher) Descriptor() (feed.Descriptor, error) {
	channels, err := p.Channels()
	if err != nil {
		return feed.Descriptor{}, err
	}
	d := feed.Descriptor{
		Federation: feed.DescriptorVersion,
		Provider:   feed.ProviderInfo{ID: p.ProviderID, Name: p.ProviderName},
		PublicKey:  feed.PublicKeyString(p.Key.Public().(ed25519.PublicKey)),
	}
	names := make([]string, 0, len(channels))
	for ch := range channels {
		names = append(names, ch)
	}
	sort.Strings(names)
	for _, ch := range names {
		d.Channels = append(d.Channels, feed.ChannelInfo{
			ID: ch, Title: channelTitle(ch),
			FeedURL: strings.TrimRight(p.BaseURL, "/") + "/f/" + ch + "/feed.json",
		})
	}
	if d.Channels == nil {
		d.Channels = []feed.ChannelInfo{}
	}
	return d, nil
}

// Feed builds one channel's feed document. pageBefore (an RFC3339 string,
// optional) returns the archive page of entries strictly older than it.
func (p *Publisher) Feed(channel, pageBefore string) (feed.Feed, error) {
	techniques, err := p.Store.ListTechniques(nil, 0)
	if err != nil {
		return feed.Feed{}, err
	}
	// The Public channel's membership comes from the ranking, not from a field on
	// the technique. Everything downstream — signing, attestation, retraction, paging —
	// is identical; only the question "is this technique in this channel?" differs.
	inChannel := func(c models.Technique) bool { return hasChannel(c, channel) }
	if channel == PublicChannel {
		served := p.publicServed()
		if served == nil {
			return feed.Feed{}, fmt.Errorf("channel %q is not being served", channel)
		}
		inChannel = func(c models.Technique) bool { return served[c.ID] }
	}
	var published []models.Technique
	for _, c := range techniques {
		if inChannel(c) {
			published = append(published, c)
		}
	}
	if len(published) == 0 {
		return feed.Feed{}, fmt.Errorf("channel %q has no published techniques", channel)
	}
	// newest first; entry timestamps come from the technique's updated_at
	sort.Slice(published, func(i, j int) bool {
		return published[i].UpdatedAt > published[j].UpdatedAt
	})

	doc := feed.Feed{
		Version: feed.Version,
		Channel: feed.ChannelMeta{ID: channel, Provider: p.ProviderID, Title: channelTitle(channel)},
		Updated: published[0].UpdatedAt,
	}
	count := 0
	for _, c := range published {
		if pageBefore != "" && c.UpdatedAt >= pageBefore {
			continue
		}
		if count == PageSize {
			doc.NextPage = strings.TrimRight(p.BaseURL, "/") + "/f/" + channel +
				"/feed.json?page=" + doc.Entries[len(doc.Entries)-1].Updated
			break
		}
		entry := p.entryFor(c)
		doc.Entries = append(doc.Entries, entry)
		count++
	}
	if doc.Entries == nil {
		doc.Entries = []feed.Entry{}
	}
	return doc, nil
}

// entryFor renders and signs one technique's entry — a retraction when the technique has
// left service (retired/decayed/rejected, or pulled back to draft), a technique entry
// otherwise.
func (p *Publisher) entryFor(c models.Technique) feed.Entry {
	e := feed.Entry{
		ID:      feed.EntryID(p.ProviderID, c.ID),
		Updated: c.UpdatedAt,
	}
	if c.Status == "retired" || c.Status == "decayed" || c.Status == "rejected" || c.Status == "draft" {
		e.Kind = feed.KindRetraction
		e.Reason = "status: " + c.Status
		feed.Sign(&e, p.Key)
		return e
	}
	public := c
	public.Embedding = nil // embeddings are local, never federated
	public.EmbeddingModel = ""
	public.EmbeddingDim = 0
	e.Kind = feed.KindTechnique
	e.Title = c.Name
	e.ContentURL = strings.TrimRight(p.BaseURL, "/") + "/f/techniques/" + c.ID + ".md"
	e.ContentHash = feed.TechniqueHash(public)
	e.Technique = &public
	e.Attestation = p.attestationFor(c.ID)
	feed.Sign(&e, p.Key)
	return e
}

// attestationFor attaches aggregate outcome evidence when the overall rollup
// clears the k-threshold; rates are rounded to two decimals (aggregate-only,
// docs/federation/federation-design.md).
func (p *Publisher) attestationFor(techniqueID string) *feed.Attestation {
	outcome, ok, err := p.Store.GetOutcome(techniqueID, "__overall__")
	if err != nil || !ok || outcome.HelpedRate == nil || outcome.SampleSize < AttestationMinN {
		return nil
	}
	return &feed.Attestation{
		Outcomes: feed.AttestedOutcomes{
			HelpedRate: float64(int(*outcome.HelpedRate*100+0.5)) / 100,
			SampleSize: outcome.SampleSize,
		},
		Granularity: "org",
	}
}

// CanonicalTechnique renders a published technique's canonical markdown+frontmatter
// document (the content_url payload; also what `feed export` writes).
func (p *Publisher) CanonicalTechnique(id string) (string, bool, error) {
	c, ok, err := p.Store.GetTechnique(id)
	if err != nil || !ok {
		return "", false, err
	}
	// Unpublished techniques are never served — and membership in the computed channel
	// counts as published, or the Public feed would advertise content_urls that
	// 404.
	if len(c.Channels) == 0 && !p.publicServed()[id] {
		return "", false, nil
	}
	return RenderTechniqueMarkdown(c), true, nil
}

// TechniqueChannels is every channel a technique is currently in, including the computed
// one. It is what the token gate authorizes against for the technique-content path,
// which carries no channel of its own (see web/federation.go).
func (p *Publisher) TechniqueChannels(c models.Technique) []string {
	out := append([]string(nil), c.Channels...)
	if p.publicServed()[c.ID] {
		out = append(out, PublicChannel)
	}
	return out
}

// SetChannels updates a technique's publication channels (the admin publish act).
// It reads and writes one technique, so it takes the technique role rather
// than the whole store.
func SetChannels(st storage.TechniqueStore, techniqueID string, channels []string) (models.Technique, error) {
	c, ok, err := st.GetTechnique(techniqueID)
	if err != nil {
		return models.Technique{}, err
	}
	if !ok {
		return models.Technique{}, fmt.Errorf("no technique %q", techniqueID)
	}
	for _, ch := range channels {
		if !validChannel(ch) {
			return models.Technique{}, fmt.Errorf("invalid channel name %q", ch)
		}
		// The Public channel is computed from measured evidence. Letting a technique be
		// placed in it by hand would make the ranking a claim rather than a
		// measurement, and there would be no way for a reader of the feed to tell
		// which entries earned their place.
		if ch == PublicChannel {
			return models.Technique{}, fmt.Errorf(
				"channel %q is computed from evidence and cannot be assigned; a technique enters it by ranking",
				PublicChannel)
		}
	}
	c.Channels = channels
	c.UpdatedAt = models.Now()
	if err := st.UpsertTechnique(c); err != nil {
		return models.Technique{}, err
	}
	return c, nil
}

func hasChannel(c contracts.Technique, channel string) bool {
	for _, ch := range c.Channels {
		if ch == channel {
			return true
		}
	}
	return false
}

func validChannel(ch string) bool {
	if ch == "" {
		return false
	}
	for i, r := range ch {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

func channelTitle(ch string) string {
	words := strings.Split(strings.ReplaceAll(ch, "-", " "), " ")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// RenderTechniqueMarkdown is the inverse of the techniques parser for the fields techniques
// carry over the wire: YAML-subset frontmatter plus the before/after body.
func RenderTechniqueMarkdown(c models.Technique) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeScalar := func(k, v string) {
		if v == "" {
			return
		}
		// any value the plain-scalar form can't carry verbatim goes through a
		// literal block — the parser subset reads those exactly (no escaping)
		if strings.ContainsAny(v, "\n:#{}[]\"'") || strings.TrimSpace(v) != v {
			b.WriteString(k + ": |\n")
			for _, line := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
				b.WriteString("  " + line + "\n")
			}
			return
		}
		b.WriteString(k + ": " + v + "\n")
	}
	writeList := func(k string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString(k + ": [" + strings.Join(items, ", ") + "]\n")
	}
	writeScalar("id", c.ID)
	writeScalar("name", c.Name)
	writeScalar("description", c.Description)
	writeScalar("scope", c.Scope)
	writeScalar("status", c.Status)
	writeScalar("provenance", c.Provenance)
	b.WriteString(fmt.Sprintf("version: %d\n", c.Version))
	writeScalar("recipe", c.Recipe)
	writeList("tags", c.Tags)
	writeList("task_types", c.TaskTypes)
	writeScalar("applies_when", c.AppliesWhen)
	writeScalar("not_when", c.NotWhen)
	writeScalar("shipped", c.Shipped)
	b.WriteString("---\n")
	if c.BeforeAfter != "" {
		b.WriteString("\n" + strings.TrimSpace(c.BeforeAfter) + "\n")
	}
	return b.String()
}
