// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package installaddr holds the two addresses the installer is reached at.
//
// They are public rather than internal because the short address is not served
// by this program: it is a redirect on the project's DNS zone, configured
// somewhere else entirely, and whatever configures it has to agree with what
// the README, the user guide, the console and the script's own header all
// quote. Two copies of an address is how one of them goes stale — which it did,
// once, and the symptom was the first command a stranger runs answering 404.
package installaddr

// URL is what a person types. It is on the project's own zone because a command
// somebody reads off a screen and retypes should be short, and because the
// address survives the repository moving, being renamed, or the script one day
// being served rather than redirected to.
const URL = "https://opentacit.com/install.sh"

// ScriptURL is where the bytes are, and it is what URL redirects to. Nothing
// quotes it at a reader.
//
// The redirect is why the command still carries -L, and why -L was never
// optional: raw.githubusercontent.com redirects on its own account too.
const ScriptURL = "https://raw.githubusercontent.com/opentacit/tacit/main/install.sh"

// Command is the line a first-time reader is given to run.
const Command = "curl -fsSL " + URL + " | sh"
