// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit doctor --probe — exit 0 when the registry's /v1/health answers ok, 1
// otherwise. Exists mostly for containers (docs/distribution/docker-plan.md):
// the distroless image has no shell or curl, so Docker HEALTHCHECK and
// Kubernetes probes exec this binary instead.
//
// It stays one GET, deliberately, while the rest of doctor grew into a report
// of a dozen checks across both roles. A liveness probe runs every 30 seconds
// and decides whether to kill the container: it must be cheap, and it must
// answer the one question a supervisor asked. So --probe shares doctor's name
// (one verb for "is this working?") and none of its body.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

// defaultProbeURL is the local registry's health endpoint, from this machine's
// own configuration.
func defaultProbeURL() string {
	cfg := registryconfig.Load()
	return fmt.Sprintf("http://127.0.0.1:%d%s/v1/health", cfg.Port, cfg.BasePath)
}

func probeHealth(url string) int {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unhealthy: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	switch {
	case resp.StatusCode == http.StatusOK && body.OK:
		fmt.Println("ok")
		return 0
	case resp.StatusCode == http.StatusServiceUnavailable && strings.Contains(body.Error, "setup required"):
		// The first-run gate (web/setup.go): the server is up and serving the
		// claim form — healthy for a probe's purposes, just not claimed yet.
		fmt.Println("ok (first-run setup pending)")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unhealthy: %s -> %s\n", url, resp.Status)
		return 1
	}
}
