// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
)

func TestSlackTextConvertsHeadersAndBold(t *testing.T) {
	md := "# Tacit digest: Jul 9 to Jul 16\n\n## Headlines\n\n- **12 suggestions shown**, **4 adopted**\n\n---\n_footer_\n"
	out := SlackText(md)
	if strings.Contains(out, "#") || strings.Contains(out, "**") {
		t.Fatalf("markdown survived conversion:\n%s", out)
	}
	if !strings.Contains(out, "*Tacit digest: Jul 9 to Jul 16*") || !strings.Contains(out, "*12 suggestions shown*") {
		t.Fatalf("mrkdwn bolding missing:\n%s", out)
	}
}

func TestWeeklyPostStampsAndNeverDoublePosts(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Text == "" {
			t.Error("empty webhook payload")
		}
		posts++
	}))
	defer srv.Close()

	stamp := filepath.Join(t.TempDir(), "digest-week.stamp")
	load := func() ([]models.Technique, []models.FeedbackEvent, error) { return nil, nil, nil }
	now := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)

	maybePost(load, srv.URL, stamp, now)
	maybePost(load, srv.URL, stamp, now.Add(time.Hour)) // same ISO week: stamped, skipped
	if posts != 1 {
		t.Fatalf("posts = %d; want exactly 1 per week", posts)
	}
	maybePost(load, srv.URL, stamp, now.AddDate(0, 0, 7)) // next week posts again
	if posts != 2 {
		t.Fatalf("posts = %d; want 2 after a week passed", posts)
	}
	if raw, err := os.ReadFile(stamp); err != nil || !strings.Contains(string(raw), "-W") {
		t.Fatalf("stamp file: %q, %v", raw, err)
	}
}

func TestWeeklyPostRetriesAfterWebhookFailure(t *testing.T) {
	fail := true
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(500)
			return
		}
		posts++
	}))
	defer srv.Close()
	stamp := filepath.Join(t.TempDir(), "stamp")
	load := func() ([]models.Technique, []models.FeedbackEvent, error) { return nil, nil, nil }
	now := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)

	maybePost(load, srv.URL, stamp, now)
	if _, err := os.Stat(stamp); err == nil {
		t.Fatal("a failed post must not stamp the week")
	}
	fail = false
	maybePost(load, srv.URL, stamp, now.Add(30*time.Minute))
	if posts != 1 {
		t.Fatalf("posts = %d; want the retry to succeed", posts)
	}
}
