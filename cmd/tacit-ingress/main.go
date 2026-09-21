// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Command tacit-ingress is the shared proxy that gives a self-hosted registry a
// public name without the member owning a domain, terminating TLS, or opening a
// port. The program itself is pkg/ingress/cli; this binary mounts no console, so
// its own hostname answers with the status page (pkg/ingress/status.go) and the
// route table is read here on the host, with `tacit-ingress instances`.
package main

import (
	"os"

	"github.com/opentacit/tacit/pkg/ingress/cli"
)

// version is stamped by the build (-X main.version=...).
var version = "dev"

func main() { os.Exit(cli.Main(os.Args[1:], cli.Options{Version: version})) }
