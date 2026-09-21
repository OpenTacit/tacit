// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package merge

// The two calls a merge makes to the destination, over plain net/http.
//
// Not through pkg/client, and the reason is structural rather than a
// preference: the registry's web package renders the merge UI, and pkg/client's
// own tests exercise the web package. A dependency from here to the typed
// client closes a loop the compiler refuses. Both the command and the web
// handler use these, so there is still one implementation of each call.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIError is a destination's refusal, with the status it refused under. The
// status matters to a caller: a safety-screen rejection (400) is one technique's
// problem and a merge carries on past it, while a rejected key (401) means
// nothing will be accepted and there is no point continuing.
type APIError struct {
	Status int
	Msg    string
	Op     string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %d %s: %s", e.Op, e.Status, http.StatusText(e.Status), e.Msg)
}

// Message is the destination's own words, for a report that already says which
// technique and which status.
func (e *APIError) Message() string { return e.Msg }

// SplitJoinLink separates a join link into the registry it names and the token
// it carries. ok is false for anything that is not one.
//
// The link is the WHOLE credential a merge needs. That is the point of taking
// this form rather than a registry URL and a key: the member already has one
// from a colleague, and asking them to find an API key as well would invent a
// second credential for a thing the first one already proves.
func SplitJoinLink(link string) (base, token string, ok bool) {
	base, token, ok = strings.Cut(strings.TrimSpace(link), "/join/")
	if !ok || strings.TrimSpace(token) == "" || strings.TrimSpace(base) == "" {
		return "", "", false
	}
	return strings.TrimRight(base, "/"), strings.TrimSpace(token), true
}

// ExchangeLink redeems a whole join link, which is the form a member actually
// holds: they were handed one line by a colleague, and neither the CLI nor the
// merge form should ask them to take it apart.
func ExchangeLink(hc *http.Client, link, label string) (registryURL, apiKey string, err error) {
	base, token, ok := SplitJoinLink(link)
	if !ok {
		return "", "", fmt.Errorf("not a join link (the format is .../join/<token>)")
	}
	return Exchange(hc, base, token, label)
}

// Exchange redeems a join token for a member key. The token is the credential,
// so the call carries none of its own.
func Exchange(hc *http.Client, base, token, label string) (registryURL, apiKey string, err error) {
	var out struct {
		RegistryURL string `json:"registry_url"`
		APIKey      string `json:"api_key"`
	}
	if err := post(hc, base+"/v1/join/exchange", "",
		map[string]string{"token": token, "label": label}, &out, "join"); err != nil {
		return "", "", err
	}
	return out.RegistryURL, out.APIKey, nil
}

// RedeemCode trades a handoff code for the member key it stands for. Like
// Exchange, the code is the credential, so the call carries none of its own.
func RedeemCode(hc *http.Client, base, code string) (registryURL, apiKey string, err error) {
	var out struct {
		RegistryURL string `json:"registry_url"`
		APIKey      string `json:"api_key"`
	}
	if err := post(hc, strings.TrimRight(base, "/")+"/v1/connect/redeem", "",
		map[string]string{"code": code}, &out, "connect"); err != nil {
		return "", "", err
	}
	return out.RegistryURL, out.APIKey, nil
}

// Send posts one contribution and returns the draft id it became.
func Send(hc *http.Client, destination, key string, body map[string]any) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := post(hc, strings.TrimRight(destination, "/")+"/v1/contribute", key, body, &out, "contribute"); err != nil {
		return "", err
	}
	return out.ID, nil
}

func post(hc *http.Client, url, key string, payload, into any, op string) error {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Tacit-Key", key)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(body))
		}
		return &APIError{Status: resp.StatusCode, Msg: e.Error, Op: op}
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(body, into)
}
