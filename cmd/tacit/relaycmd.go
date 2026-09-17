// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/hooks"
)

func cmdHookRelay(args []string) int {
	cfg := auditorconfig.Load()
	harness := "claude-code"
	if len(args) > 0 {
		harness = args[0]
	}
	event := ""
	if len(args) > 1 {
		event = args[1]
	}
	return hooks.RunRelay(hooks.RelayConfig{
		BaseURL:  fmt.Sprintf("http://%s:%d", cfg.HooksHost, cfg.HooksPort),
		APIKey:   cfg.HooksAPIKey,
		SpawnLog: cfg.HooksSpawnLog,
		Consumer: hooks.DetectConsumer(os.Getenv),
		Client:   hooks.DetectClient(os.Getenv),
	}, harness, event)
}
