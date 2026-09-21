// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/digest"
	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/internal/registry/store"
	"github.com/opentacit/tacit/internal/windows"
	pkgclient "github.com/opentacit/tacit/pkg/client"
	feedpkg "github.com/opentacit/tacit/pkg/feed"
)

func cmdDigest(args []string) int {
	cfg := auditorconfig.Load()
	fs := flag.NewFlagSet("digest", flag.ContinueOnError)
	window := fs.String("window", "7d", windows.Help())
	registryURL := fs.String("registry", cfg.RegistryURL, "registry base URL")
	key := fs.String("key", cfg.RegistryKey, "registry API key")
	out := fs.String("out", "-", "output file (- for stdout)")
	slack := fs.String("slack", "", "post to this Slack incoming-webhook URL and write no file")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	reg := &pkgclient.Registry{BaseURL: *registryURL, APIKey: *key}
	techniques, err := reg.Techniques()
	if err != nil {
		log.Printf("digest: %v", err)
		return 1
	}
	events, err := reg.AllEvents("")
	if err != nil {
		log.Printf("digest: %v", err)
		return 1
	}
	md := digest.Build(techniques, events, time.Now().UTC(), *window)
	if *slack != "" {
		if err := digest.PostSlack(*slack, md); err != nil {
			log.Printf("digest: %v", err)
			return 1
		}
		fmt.Println("posted the digest to Slack")
		return 0
	}
	if *out == "-" {
		fmt.Print(md)
		return 0
	}
	if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
		log.Print(err)
		return 1
	}
	fmt.Printf("wrote the digest to %s\n", *out)
	return 0
}

func cmdFeed(args []string) int {
	if len(args) == 0 || args[0] != "export" {
		fmt.Fprintln(os.Stderr, "usage: tacit feed export --channel <ch> --out <dir> [--base-url URL] [--data dir]")
		return 2
	}
	cfg := registryconfig.Load()
	fs := flag.NewFlagSet("feed export", flag.ContinueOnError)
	channel := fs.String("channel", "", "channel to export")
	out := fs.String("out", "", "output directory")
	baseURL := fs.String("base-url", "", "external base URL for feed/technique links (defaults to the provider id)")
	dataDir := fs.String("data", cfg.DataDir, "file-store directory")
	if _, ok := parseFlags(fs, args[1:]); !ok {
		return exitUsage
	}
	if *channel == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: tacit feed export --channel <ch> --out <dir> [--base-url URL] [--data dir]")
		return 2
	}
	st, err := store.Open(*dataDir)
	if err != nil {
		log.Printf("open store: %v", err)
		return 1
	}
	defer st.Close()
	key, err := feedpkg.LoadOrCreateKey(filepath.Join(*dataDir, "feed_key"))
	if err != nil {
		log.Print(err)
		return 1
	}
	base := cfg.FeedProviderID
	if *baseURL != "" {
		base = *baseURL
	}
	if base == "" {
		base = cfg.ExternalBase()
	}
	if base == "" {
		fmt.Fprintln(os.Stderr, "set --base-url (or TACIT_EXTERNAL_URL / TACIT_FEED_PROVIDER_ID): static exports need a stable external URL")
		return 2
	}
	providerID := cfg.FeedProviderID
	if providerID == "" {
		providerID = feedpkg.PinnedProviderID(*dataDir)
	}
	if providerID == "" {
		providerID = base
	}
	pub := &federation.Publisher{Store: st, Key: key,
		ProviderID: providerID, ProviderName: cfg.FeedProviderName, BaseURL: base}
	n, err := pub.Export(*channel, *out)
	if err != nil {
		log.Print(err)
		return 1
	}
	fmt.Printf("exported channel %q: %d entries -> %s\n", *channel, n, *out)
	return 0
}
