// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
)

func accessMarker(mode, tunnel string) string {
	switch mode {
	case "private":
		return "⌂ private"
	case "global":
		if tunnel == "down" {
			return "⇄ proxied·down"
		}
		return "⇄ proxied"
	case "staged":
		return "⇄ proxied (commons unconfirmed)"
	default:
		return ""
	}
}

// postStatusline hands the payload back to the agent, which folds the
// countable parts of it into the session record (capture.ReadStatusLine) and
// writes the account's allowance to its own file.
//
// The harness gives the status line the whole session several times a second —
// what it has cost, how long it has been working, how many lines it has
// written, how full the context and the prompt cache are, how much of the
// allowance is gone — and this command used to decode the session id out of it
// and drop the rest. It is the cheapest source of the four things the Usage
// page could not otherwise say.
//
// Fire and forget, on the same tight client as the read below and running
// beside it rather than before it: a member's prompt must never wait on this.
// A payload lost to a slow agent costs one render's worth of a figure that
// arrives again on the next one.
func postStatusline(client *http.Client, cfg auditorconfig.Config, raw []byte) {
	if len(raw) == 0 {
		return
	}
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://%s:%d/v1/hooks/statusline",
			cfg.HooksHost, cfg.HooksPort), bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tacit-Key", cfg.HooksAPIKey)
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	_ = resp.Body.Close()
}

func cmdStatusline() int {
	raw, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	var in struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &in)
	cfg := auditorconfig.Load()
	client := &http.Client{Timeout: 400 * time.Millisecond}
	// Both calls at once, so the write costs the render nothing: the line is
	// printed on the read's budget, which is what it has always been.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		postStatusline(client, cfg, raw)
	}()
	defer wg.Wait()
	resp, err := client.Get(fmt.Sprintf("http://%s:%d/v1/hooks/stats?session_id=%s",
		cfg.HooksHost, cfg.HooksPort, url.QueryEscape(in.SessionID)))
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var stats struct {
		Shown   int  `json:"shown"`
		Adopted int  `json:"adopted"`
		Drafts  *int `json:"drafts"`
		Session *struct {
			Shown   int `json:"shown"`
			Adopted int `json:"adopted"`
		} `json:"session"`
		LLM *struct {
			State         string `json:"state"`
			NeedsOperator bool   `json:"needs_operator"`
		} `json:"llm"`
		Registry *struct {
			State         string `json:"state"`
			NeedsOperator bool   `json:"needs_operator"`
			Access        string `json:"access"`
			Tunnel        string `json:"tunnel"`
		} `json:"registry"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return 0
	}
	shown, adopted := stats.Shown, stats.Adopted
	if stats.Session != nil {
		shown, adopted = stats.Session.Shown, stats.Session.Adopted
	}
	line := fmt.Sprintf("◆ tacit %d⚡ %d✓", shown, adopted)
	if stats.Drafts != nil && *stats.Drafts > 0 {
		line += fmt.Sprintf(" %d⚑", *stats.Drafts)
	}
	if stats.Registry != nil {
		if marker := accessMarker(stats.Registry.Access, stats.Registry.Tunnel); marker != "" {
			line += " " + marker
		}
	}
	switch {
	case stats.Registry != nil && stats.Registry.NeedsOperator:
		line += fmt.Sprintf(" ⚠ registry:%s (tacit doctor)", stats.Registry.State)
	case stats.LLM != nil && stats.LLM.NeedsOperator:
		line += fmt.Sprintf(" ⚠ %s (/tacit:status)", stats.LLM.State)
	}
	fmt.Print(line)
	return 0
}
