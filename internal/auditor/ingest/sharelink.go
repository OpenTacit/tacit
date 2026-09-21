// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Share-link extraction (the v0 consumer-era ingestion, retained for
// out-of-runtime imports). The two providers embed shared conversations very
// differently, which dictates the strategy:
//
//	ChatGPT (chatgpt.com/share/<id>): the FULL conversation is embedded in the
//	share page's HTML as an interned RSC payload (escaped string literals in a
//	<script>). Fetch the raw HTML and parse it — no headless browser.
//
//	Claude (claude.ai/share/<id>): the share page is a thin SPA shell; the
//	conversation loads from claude.ai/api/share/<id>, which sits behind a
//	Cloudflare bot challenge — retrieving it needs a real browser session
//	cookie. Once you have the JSON, this normalizes it.

package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

var shareIDRe = regexp.MustCompile(`/share/([0-9a-fA-F-]{6,})`)

// ErrBlocked is returned when a fetch is stopped by a bot challenge / auth wall.
var ErrBlocked = errors.New("blocked")

func isCloudflareChallenge(text string) bool {
	return strings.Contains(text, "Just a moment") ||
		strings.Contains(text, "challenges.cloudflare.com") ||
		strings.Contains(text, "cf-browser-verification")
}

func fetch(url, cookie, accept, referer string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: network error: %v", ErrBlocked, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if resp.StatusCode >= 400 {
		if resp.StatusCode == 403 || resp.StatusCode == 503 || isCloudflareChallenge(text) {
			return "", fmt.Errorf("%w: HTTP %d (likely a Cloudflare bot challenge)", ErrBlocked, resp.StatusCode)
		}
		return "", fmt.Errorf("%w: HTTP %d", ErrBlocked, resp.StatusCode)
	}
	return text, nil
}

// --- ChatGPT: parse the embedded RSC payload from the HTML -----------------

var (
	// private-use entity-annotation control chars (U+2060–U+2064, U+FEFF)
	entityMarkRe = regexp.MustCompile(`[\x{2060}-\x{2064}\x{FEFF}]`)
	entityRefRe  = regexp.MustCompile(`entity\[(.*?)\]`)
	litRe        = regexp.MustCompile(`\\"((?:[^\\]|\\.)*?)\\"`)
)

func decodeChatGPT(s string) string {
	r := strings.NewReplacer(
		"\\\\n", "\n", "\\n", "\n",
		"\\\\\"", "\"", "\\\"", "\"",
		"\\\\", "\\",
		"\\u0060", "`",
		"\\u003e", ">", "\\u003c", "<",
	)
	s = r.Replace(s)
	s = entityMarkRe.ReplaceAllString(s, "")
	s = entityRefRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := entityRefRe.FindStringSubmatch(m)[1]
		if idx := strings.Index(inner, ","); idx >= 0 {
			return strings.Trim(strings.TrimSpace(inner[idx+1:]), `"`)
		}
		return inner
	})
	return strings.TrimSpace(s)
}

// ParseChatGPTHTML extracts turns from a ChatGPT share page.
func ParseChatGPTHTML(html string) []Turn {
	start := strings.Index(html, `\"mapping\"`)
	if start == -1 {
		return nil
	}
	from := start - 2000
	if from < 0 {
		from = 0
	}
	region := html[from:]
	noise := map[string]bool{
		"check out this chat": true, "our latest and most advanced model": true,
		"here's a chat someone thought you'd want to see.": true,
		"finished_successfully":                            true, "absolute": true,
		"assistant": true, "user": true, "system": true,
	}
	var turns []Turn
	for _, m := range litRe.FindAllStringSubmatch(region, -1) {
		d := decodeChatGPT(m[1])
		if len(d) < 12 || !strings.Contains(d, " ") || !hasAlpha(d) {
			continue
		}
		low := strings.ToLower(d)
		if noise[low] || strings.HasPrefix(d, "_") || strings.HasPrefix(d, "http") ||
			strings.HasPrefix(d, "application/") || strings.HasPrefix(d, "text/") {
			continue
		}
		role := "user"
		if len(d) > 200 {
			role = "assistant"
		}
		turns = append(turns, Turn{role, d})
	}
	return turns
}

func hasAlpha(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// --- Claude: normalize the share-API JSON ----------------------------------

func claudeText(msg map[string]any) string {
	if t, _ := msg["text"].(string); strings.TrimSpace(t) != "" {
		return strings.TrimSpace(t)
	}
	blocks, _ := msg["content"].([]any)
	var parts []string
	for _, raw := range blocks {
		switch block := raw.(type) {
		case map[string]any:
			bt, hasType := block["type"].(string)
			if !hasType || bt == "text" {
				if t, _ := block["text"].(string); t != "" {
					parts = append(parts, t)
				}
			}
		case string:
			parts = append(parts, block)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// ParseClaudeJSON extracts turns from a saved Claude share-API response.
func ParseClaudeJSON(data map[string]any) []Turn {
	msgs, _ := data["chat_messages"].([]any)
	if msgs == nil {
		msgs, _ = data["messages"].([]any)
	}
	if msgs == nil {
		if conv, ok := data["conversation"].(map[string]any); ok {
			msgs, _ = conv["chat_messages"].([]any)
		}
	}
	var turns []Turn
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["sender"].(string)
		if role == "" {
			role, _ = m["role"].(string)
		}
		if role == "" {
			role = "unknown"
		}
		if role == "human" {
			role = "user"
		}
		if text := claudeText(m); text != "" {
			turns = append(turns, Turn{role, text})
		}
	}
	return turns
}

func fetchClaude(shareID, cookie string) ([]Turn, error) {
	body, err := fetch("https://claude.ai/api/share/"+shareID, cookie,
		"application/json", "https://claude.ai/share/"+shareID)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return nil, err
	}
	return ParseClaudeJSON(data), nil
}

// --- dispatch ----------------------------------------------------------------

// FromURL extracts turns from a share link.
func FromURL(url, cookie string) ([]Turn, error) {
	switch {
	case strings.Contains(url, "chatgpt.com") || strings.Contains(url, "chat.openai.com"):
		html, err := fetch(url, cookie, "text/html,*/*", "")
		if err != nil {
			return nil, err
		}
		return ParseChatGPTHTML(html), nil
	case strings.Contains(url, "claude.ai") || strings.Contains(url, "claude.com"):
		m := shareIDRe.FindStringSubmatch(url)
		if m == nil {
			return nil, errors.New("could not find a share id in the Claude URL")
		}
		return fetchClaude(m[1], cookie)
	}
	return nil, fmt.Errorf("unrecognised share host: %s", url)
}

// FromFile extracts turns from a saved share page or API response.
func FromFile(path string) ([]Turn, error) {
	text, err := ReadFileText(path)
	if err != nil {
		return nil, err
	}
	stripped := strings.TrimLeft(text, " \t\r\n")
	if strings.HasPrefix(stripped, "{") || strings.HasPrefix(stripped, "[") {
		var data map[string]any
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			return nil, err
		}
		return ParseClaudeJSON(data), nil // a saved Claude API response
	}
	if strings.Contains(text, `\"mapping\"`) {
		return ParseChatGPTHTML(text), nil // a saved ChatGPT share page
	}
	if isCloudflareChallenge(text) {
		return nil, fmt.Errorf("%w: saved file is a Cloudflare challenge page, not content", ErrBlocked)
	}
	return nil, errors.New("unrecognised file: not a ChatGPT share page or Claude JSON")
}
