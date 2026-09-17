// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package servicectl drives the unit that runs this machine's registry.
//
// It exists because turning sign-in on now happens in two places — `tacit
// secure` on the console and the Settings page in the browser — and both have
// to answer the same question the same way: is this registry run by a service
// unit that I may restart, and is it the registry these settings belong to?
// Two copies of that answer would drift, and the failure mode of the drift is
// restarting somebody else's registry, or reporting a restart that never
// happened.
//
// What it deliberately does NOT do is install a unit. Installing is the
// console's business (`tacit init`, `tacit secure`): it writes files, enables
// lingering and chooses how this machine will run the registry from now on.
// Restarting one that already exists is a smaller act, and the only one a web
// request needs.
package servicectl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/opentacit/tacit/internal/registry/config"
)

// Unit and Label name the registry's service to the two managers this project
// installs it under.
const (
	Unit  = "tacit-registry.service"
	Label = "com.tacit.registry"
)

// InContainer reports a registry running under an init system that is not the
// operator's to enrol in. cmd/tacit sets TACIT_CONTAINER in its own images.
func InContainer() bool { return os.Getenv("TACIT_CONTAINER") != "" }

// CanManage reports whether this machine has a service manager this package
// knows how to drive.
func CanManage() bool {
	switch {
	case InContainer():
		return false
	case runtime.GOOS == "linux" && haveExec("systemctl"):
		return true
	case runtime.GOOS == "darwin" && haveExec("launchctl"):
		return true
	}
	return false
}

// Active reports a managed registry that is running now.
func Active() bool {
	switch {
	case InContainer():
		return false
	case runtime.GOOS == "linux" && haveExec("systemctl"):
		return exec.Command("systemctl", "--user", "is-active", "--quiet", Unit).Run() == nil
	case runtime.GOOS == "darwin" && haveExec("launchctl"):
		return exec.Command("launchctl", "print", target()).Run() == nil
	}
	return false
}

// Restart restarts the managed registry, reporting whether there was one to
// restart. A registry run in the foreground is the operator's to restart, and
// saying so beats pretending nothing happened.
func Restart() (bool, error) {
	switch {
	case InContainer():
		return false, nil
	case runtime.GOOS == "linux" && haveExec("systemctl"):
		if err := exec.Command("systemctl", "--user", "is-active", "--quiet", Unit).Run(); err != nil {
			return false, nil
		}
		if out, err := exec.Command("systemctl", "--user", "restart", Unit).CombinedOutput(); err != nil {
			return false, fmt.Errorf("%v\n%s", err, out)
		}
		return true, nil
	case runtime.GOOS == "darwin" && haveExec("launchctl"):
		if err := exec.Command("launchctl", "print", target()).Run(); err != nil {
			return false, nil
		}
		if out, err := exec.Command("launchctl", "kickstart", "-k", target()).CombinedOutput(); err != nil {
			return false, fmt.Errorf("%v\n%s", err, out)
		}
		return true, nil
	}
	return false, nil
}

// ReadsEnv reports whether the settings at envPath are the ones the service
// would read. The unit's EnvironmentFile names this machine's registry, so a
// process pointed somewhere else by TACIT_REGISTRY_ENV must not restart it:
// that would reconfigure a different registry from the one being configured,
// and say it had done the right thing.
//
// It is also what keeps a test suite — which always points TACIT_REGISTRY_ENV
// at a temporary file — from restarting the developer's own registry.
func ReadsEnv(envPath string) bool {
	def := config.DefaultRegistryEnvPath()
	if def == "" || envPath == "" {
		return false
	}
	return samePath(envPath, def)
}

func samePath(a, b string) bool {
	clean := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		return filepath.Clean(p)
	}
	return clean(a) == clean(b)
}

func target() string { return fmt.Sprintf("gui/%d/%s", os.Getuid(), Label) }

func haveExec(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
