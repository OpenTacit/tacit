// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
)

// PublishLedger files one machine's sealed summaries at the member's ledger
// address (docs/delivery/out-of-band-plan.md).
//
// It does not go through the shared JSON transport, because the body is
// ciphertext rather than a document: JSON-encoding it would base64 the bytes
// into a string field and pay a third for nothing. The one thing it borrows is
// the header — X-Tacit-Key, the same member credential every other call
// presents, which is what proves this machine belongs to this registry.
//
// The address and the sealing are the caller's business. This client never sees
// a derivation key and could not open what it is posting.
func (r *Registry) PublishLedger(id, machine string, sealed []byte) error {
	url := strings.TrimRight(r.BaseURL, "/") + "/v1/ledger/" + id + "/" + machine
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(sealed))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if r.APIKey != "" {
		req.Header.Set("X-Tacit-Key", r.APIKey)
	}
	httpClient := r.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// Bounded: a refusal body is a short JSON error, and an unbounded read
		// of whatever answered this URL is not something a background timer
		// should do.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return &StatusError{Path: "/v1/ledger", Code: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return nil
}

// LedgerUnavailable reports whether an error from PublishLedger means the
// registry does not offer a ledger at all — one that predates this endpoint. A
// member on such a registry should see nothing and hear nothing about it: their
// machine is not the thing that is wrong.
func LedgerUnavailable(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Code == http.StatusNotFound || se.Code == http.StatusMethodNotAllowed
}
