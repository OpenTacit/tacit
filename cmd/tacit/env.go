// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	pkgclient "github.com/opentacit/tacit/pkg/client"
)

// cmdEnv resolves the member's registry wiring and prints it in a
// shell-sourceable form, so a skill can replace the eight-line "resolve the
// registry address" bash preamble (source agent.env, default the URL and key,
// probe /v1/health for the operator's external dashboard URL) with a single
// call: `eval "$(tacit env)"`.
//
// This is the one place that resolution lives now. Every harness's skills used
// to inline the same block; centralising it here means a change to how the
// address resolves is one edit, not one per skill per harness.
//
//	REG  — the registry API base (TACIT_REGISTRY_URL, else agent.env, else the
//	       loopback default). Where curl posts /v1/evidence etc.
//	KEY  — the X-Tacit-Key (TACIT_API_KEY, else agent.env, else dev-key).
//	DASH — the member-facing dashboard origin: the operator's external_url from
//	       /v1/health when the registry advertises one, else REG. This is the
//	       address that belongs in links shown to a member, since REG may be a
//	       loopback that only resolves on the registry's own host.
func cmdEnv(args []string) int {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print as a JSON object instead of shell assignments")
	noKey := fs.Bool("no-key", false, "omit KEY (for contexts that only need the URLs)")
	noProbe := fs.Bool("no-probe", false, "skip the /v1/health probe; DASH falls back to REG")
	_ = fs.Parse(args)

	cfg := auditorconfig.Load()
	reg := strings.TrimRight(cfg.RegistryURL, "/")
	key := cfg.RegistryKey
	dash := reg
	if !*noProbe {
		if ext := probeExternalURL(reg); ext != "" {
			dash = strings.TrimRight(ext, "/")
		}
	}

	if *asJSON {
		out := map[string]string{"REG": reg, "DASH": dash}
		if !*noKey {
			out["KEY"] = key
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return 0
	}

	fmt.Printf("REG=%s\n", shellQuote(reg))
	if !*noKey {
		fmt.Printf("KEY=%s\n", shellQuote(key))
	}
	fmt.Printf("DASH=%s\n", shellQuote(dash))
	return 0
}

// probeExternalURL asks the registry for the operator's advertised external
// URL, so member-facing links point at the address a browser anywhere can open
// rather than a loopback. Best-effort with a short timeout: an unreachable or
// silent registry just yields "", and the caller falls back to REG.
func probeExternalURL(reg string) string {
	if reg == "" {
		return ""
	}
	h, err := (&pkgclient.Registry{BaseURL: reg, HTTP: &http.Client{Timeout: 2 * time.Second}}).GetHealth()
	if err != nil {
		return ""
	}
	return h.ExternalURL
}

// shellQuote single-quotes a value so `eval "$(tacit env)"` is safe even if a
// URL or key somehow carries shell metacharacters. Empty stays empty-quoted.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
