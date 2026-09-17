// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/opentacit/tacit/internal/ingress"
)

// ErrNoAddress reports a registry that never had a public address, which is not
// a failure to release one.
var ErrNoAddress = errors.New("this registry has no public address to release")

// ReleaseAddress hands a registry's hostname back to the ingress and deletes
// the instance key that named it.
//
// The key goes only once the ingress has confirmed. Keeping it would leave a
// credential for a name that no longer exists, and the next thing to present it
// would silently enrol as somebody new — under a fresh name, since the old one
// is already back in circulation.
//
// name is what was released, empty when the ingress had no record of this key.
// That case is a success: the state the caller asked for already holds, which
// is what makes a merge safe to run again after an interrupted release.
func ReleaseAddress(configDir, ingressAddr string, tls bool, version string) (name string, err error) {
	key, err := ingress.LoadKey(configDir)
	if err != nil {
		return "", ErrNoAddress
	}
	// Normalized, because the configured form is a bare hostname and a bare
	// hostname cannot be dialled: no scheme, no port.
	client := &ingress.Client{Addr: ingress.NormalizeAddr(ingressAddr), Token: key, Version: version, TLS: tls}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	name, err = client.Retire(ctx)
	if err != nil {
		return "", err
	}
	if err := os.Remove(ingress.KeyPath(configDir)); err != nil && !os.IsNotExist(err) {
		// The address is gone either way; say so rather than reporting a
		// failure that would send somebody looking for an instance that no
		// longer exists.
		return name, nil
	}
	return name, nil
}
