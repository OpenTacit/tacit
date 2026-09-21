// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package hooks

import "os/exec"

// detach is a no-op on non-unix platforms (the spawned agent still runs; it
// just shares the relay's console session).
func detach(cmd *exec.Cmd) {}
