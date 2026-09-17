// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

// usagePageSource is everything the Usage page is made of: the shell the server
// renders, and the renderer it loads.
//
// The two used to be one string in usage.go. They were split so the renderer
// could be a cached asset, and the tests that read across both — a claim about
// the markup and the script that fills it — say so by asking for this rather
// than by happening to match either. A test about one alone should name that
// one: usagePageHTML or usageJS.
func usagePageSource() string { return usagePageHTML + "\n" + usageJS }
