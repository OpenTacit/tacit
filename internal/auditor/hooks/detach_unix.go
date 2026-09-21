// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package hooks

import (
	"os/exec"
	"syscall"
)

// detach puts the spawned agent in its own session so it outlives the relay.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
