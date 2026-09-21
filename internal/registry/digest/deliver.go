// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Delivery: the digest was "a file nobody is sent" (growth-plan.md mechanism
// 3) — this gives it somewhere to go. Slack first: an incoming webhook is the
// one chat integration nearly every org already has, needs no OAuth, and
// carries plain text. Email and other channels can follow the same seam.

package digest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
)

var (
	mdH1   = regexp.MustCompile(`(?m)^# (.+)$`)
	mdH2   = regexp.MustCompile(`(?m)^## (.+)$`)
	mdBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// SlackText converts the digest's markdown to Slack mrkdwn: headers become
// bold lines, ** becomes *, everything else is already plain enough.
func SlackText(md string) string {
	out := mdH1.ReplaceAllString(md, "*$1*")
	out = mdH2.ReplaceAllString(out, "*$1*")
	out = mdBold.ReplaceAllString(out, "*$1*")
	out = strings.ReplaceAll(out, "\n---\n", "\n")
	return strings.TrimSpace(out)
}

// PostSlack sends a rendered digest to a Slack incoming webhook.
func PostSlack(webhookURL, md string) error {
	body, err := json.Marshal(map[string]string{"text": SlackText(md)})
	if err != nil {
		return err
	}
	hc := &http.Client{Timeout: 15 * time.Second}
	resp, err := hc.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack webhook: %s", resp.Status)
	}
	return nil
}

// StartWeekly posts a 7d digest to the webhook once a week (Monday morning),
// entirely registry-side — the A2 surface with zero recurring operator
// effort. stampPath persists the last-sent date so restarts (deploys are
// frequent) never double-post; a missed Monday (host asleep) posts on the
// next check instead of never. Returns a stop func.
func StartWeekly(load func() ([]models.Technique, []models.FeedbackEvent, error),
	webhookURL, stampPath string, now func() time.Time) func() {

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				maybePost(load, webhookURL, stampPath, now())
			}
		}
	}()
	return func() { close(done) }
}

// maybePost sends when a new digest week has started (ISO week boundary,
// Monday) and this week's digest has not been stamped yet.
func maybePost(load func() ([]models.Technique, []models.FeedbackEvent, error),
	webhookURL, stampPath string, now time.Time) {

	year, week := now.ISOWeek()
	stamp := fmt.Sprintf("%d-W%02d", year, week)
	if raw, err := os.ReadFile(stampPath); err == nil && strings.TrimSpace(string(raw)) == stamp {
		return
	}
	techniques, events, err := load()
	if err != nil {
		return // registry hiccup: try again next tick, never crash the loop
	}
	if err := PostSlack(webhookURL, Build(techniques, events, now, "7d")); err != nil {
		return // webhook down: retry next tick (stamp only after success)
	}
	_ = os.WriteFile(stampPath, []byte(stamp), 0o644)
}
