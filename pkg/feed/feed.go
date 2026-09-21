// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package feed implements the OpenTacit technique-feed protocol
// (docs/federation/federation-design.md; version "tacit-feed/v1") — the RSS/Atom-
// shaped mechanism by which technique knowledge crosses an OpenTacit
// boundary: provider descriptors at a well-known URL, channel feeds of technique
// entries with aggregate-only attestations, retractions, Ed25519 entry
// signatures, and paged archives.
//
// The package holds the wire types, the canonical signing scheme, and
// helpers shared by every publisher and consumer (the registry, the miner,
// static exporters, third-party providers). Normative schemas:
// schemas/feed.schema.json and schemas/provider-descriptor.schema.json.
package feed

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentacit/tacit/pkg/contracts"
)

// Version is the protocol version string carried by every feed document.
const Version = "tacit-feed/v1"

// DescriptorVersion is the provider-descriptor version.
const DescriptorVersion = "v1"

// WellKnownPath is where a provider serves its descriptor.
const WellKnownPath = "/.well-known/tacit.json"

// Entry kinds.
const (
	KindTechnique  = "technique"
	KindRetraction = "retraction"
)

// Descriptor is the provider document served at WellKnownPath — one request
// answers "who is this, how do I verify them, what do they publish?".
type Descriptor struct {
	Federation string        `json:"tacit_federation"` // DescriptorVersion
	Provider   ProviderInfo  `json:"provider"`
	PublicKey  string        `json:"public_key"` // "ed25519:BASE64"
	Channels   []ChannelInfo `json:"channels"`
}

// ProviderInfo identifies a publisher. ID is a URI and namespaces every
// entry id the provider emits.
type ProviderInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ChannelInfo is one published channel (a named, curated subset of techniques).
type ChannelInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	FeedURL string `json:"feed_url"`
}

// Feed is one channel's feed document.
type Feed struct {
	Version  string      `json:"version"` // Version
	Channel  ChannelMeta `json:"channel"`
	Updated  string      `json:"updated"`
	NextPage string      `json:"next_page,omitempty"` // archived-page cursor
	Entries  []Entry     `json:"entries"`
}

// ChannelMeta identifies the channel inside a feed document.
type ChannelMeta struct {
	ID       string `json:"id"`
	Provider string `json:"provider"` // ProviderInfo.ID
	Title    string `json:"title,omitempty"`
}

// Entry is one feed item: a technique publication/update or a retraction.
type Entry struct {
	ID          string               `json:"id"`   // URI, provider-namespaced
	Kind        string               `json:"kind"` // technique|retraction
	Updated     string               `json:"updated"`
	Title       string               `json:"title,omitempty"`
	ContentURL  string               `json:"content_url,omitempty"`
	ContentHash string               `json:"content_hash,omitempty"` // "sha256:HEX" of the canonical technique JSON
	Technique   *contracts.Technique `json:"technique,omitempty"`
	Attestation *Attestation         `json:"attestation,omitempty"`
	Reason      string               `json:"reason,omitempty"` // retractions
	Signature   string               `json:"signature,omitempty"`
}

// Attestation is aggregate-only outcome evidence a provider MAY attach.
// Never member-level, never events — rounded rates and k-thresholded sample
// sizes at org (or coarser) granularity. Consumers display it and may seed
// cold-start priors with it; local outcomes always dominate ranking.
type Attestation struct {
	Outcomes    AttestedOutcomes `json:"outcomes"`
	Granularity string           `json:"granularity"` // "org"
}

// AttestedOutcomes is the rounded outcome summary inside an attestation.
type AttestedOutcomes struct {
	HelpedRate float64 `json:"helped_rate"`
	SampleSize int     `json:"sample_size"`
	WindowDays int     `json:"window_days,omitempty"`
}

// --- canonical hashing & signing --------------------------------------------

// TechniqueHash computes the canonical content hash of a technique: sha256 over the
// technique's JSON with volatile/local fields (embedding, timestamps, decay,
// channels) removed, so the same knowledge hashes identically everywhere.
func TechniqueHash(technique contracts.Technique) string {
	c := technique
	c.Embedding = nil
	c.EmbeddingModel = ""
	c.EmbeddingDim = 0
	c.DecaySignal = 0
	c.DecayChecked = ""
	c.Channels = nil
	c.Origin = nil
	c.CreatedAt = ""
	c.UpdatedAt = ""
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// signingBase is the byte string an entry signature covers. Field order is
// fixed and versioned by Version; all fields are already immutable strings.
func signingBase(e Entry) []byte {
	return []byte(strings.Join([]string{
		Version, e.Kind, e.ID, e.Updated, e.ContentHash, e.Reason,
	}, "\n"))
}

// Sign signs an entry in place with the provider's private key.
func Sign(e *Entry, key ed25519.PrivateKey) {
	sig := ed25519.Sign(key, signingBase(*e))
	e.Signature = "ed25519:" + base64.StdEncoding.EncodeToString(sig)
}

// Verify checks an entry's signature against a provider public key string
// ("ed25519:BASE64"). An entry with a Technique must also match its ContentHash.
func Verify(e Entry, publicKey string) error {
	pub, err := ParsePublicKey(publicKey)
	if err != nil {
		return err
	}
	raw, ok := strings.CutPrefix(e.Signature, "ed25519:")
	if !ok {
		return errors.New("entry signature missing or not ed25519")
	}
	sig, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("entry signature not base64: %w", err)
	}
	if !ed25519.Verify(pub, signingBase(e), sig) {
		return errors.New("entry signature invalid")
	}
	if e.Technique != nil && e.ContentHash != "" && TechniqueHash(*e.Technique) != e.ContentHash {
		return errors.New("technique does not match content_hash")
	}
	return nil
}

// ParsePublicKey parses "ed25519:BASE64".
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	raw, ok := strings.CutPrefix(s, "ed25519:")
	if !ok {
		return nil, errors.New("public key must be 'ed25519:BASE64'")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("public key malformed")
	}
	return ed25519.PublicKey(b), nil
}

// PublicKeyString renders a public key in descriptor form.
func PublicKeyString(pub ed25519.PublicKey) string {
	return "ed25519:" + base64.StdEncoding.EncodeToString(pub)
}

// LoadOrCreateKey loads an Ed25519 seed (hex, one line) from path, creating
// it with a fresh key (0600) when absent — a provider's identity persists
// across restarts without any ceremony.
func LoadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	if raw, err := os.ReadFile(path); err == nil {
		seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("feed key at %s is malformed", path)
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(seed)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// ProviderIDFile is the name, inside the data dir beside the signing key, of the
// file holding this registry's pinned provider id.
const ProviderIDFile = "feed_provider_id"

// PinnedProviderID reads the provider id pinned in dir, or "" if none is.
//
// A provider id is pinned rather than derived because EntryID names every entry
// after it and a subscriber keeps it as an imported technique's provenance. Derive it
// from the current address and it moves whenever the address does — a new proxy
// name, a moved domain — at which point every subscriber sees the whole channel
// as new techniques rather than updates, and the old copies are orphaned under an id
// nothing will ever retract. Location may move; identity may not. What
// authenticates a publisher is the signing key next to this file, so pinning the
// id costs nothing in trust (docs/distribution/global-access-plan.md).
func PinnedProviderID(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, ProviderIDFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// PinProviderID records id as this registry's permanent provider id. Callers pin
// only a durable address: a registry named after http://127.0.0.1:8080 for the
// rest of its life would be worse off than one with no pin at all.
func PinProviderID(dir, id string) error {
	return os.WriteFile(filepath.Join(dir, ProviderIDFile), []byte(id+"\n"), 0o600)
}

// EntryID builds a provider-namespaced entry URI for a technique id.
func EntryID(providerID, techniqueID string) string {
	return strings.TrimRight(providerID, "/") + "/techniques/" + techniqueID
}
