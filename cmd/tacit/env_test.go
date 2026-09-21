// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestCmdEnv checks the shell-sourceable output resolves REG/KEY/DASH from the
// environment. HOME is redirected to a temp dir so the developer's real
// ~/.config/tacit/agent.env can never leak into the test (the hermetic-config
// discipline in docs/dev/09-testing.md). --no-probe keeps it offline, so DASH
// falls back to REG with no network.
func TestCmdEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TACIT_REGISTRY_URL", "http://reg.example:8080")
	t.Setenv("TACIT_API_KEY", "k-secret")

	out := captureStdout(t, func() {
		if code := cmdEnv([]string{"--no-probe"}); code != 0 {
			t.Fatalf("cmdEnv exit = %d", code)
		}
	})

	for _, want := range []string{
		`REG='http://reg.example:8080'`,
		`KEY='k-secret'`,
		`DASH='http://reg.example:8080'`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("tacit env output missing %q\ngot:\n%s", want, out)
		}
	}

	// --no-key must withhold the key for link-only contexts.
	out = captureStdout(t, func() { cmdEnv([]string{"--no-probe", "--no-key"}) })
	if strings.Contains(out, "KEY=") {
		t.Fatalf("tacit env --no-key still printed the key:\n%s", out)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}
