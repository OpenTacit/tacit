// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Saving any setting used to stop and re-dial the tunnel. When the save arrived
// through that tunnel — which is what the public address is for — the response
// went back over a connection that had just been closed: a hang, and then the
// ingress answering "this registry has no spare connection right now".
func TestASaveThatChangesNothingAboutTheTunnelLeavesItAlone(t *testing.T) {
	got := publishAction(true, true)
	if got != publishNothing {
		t.Errorf("action = %d, want the tunnel untouched", got)
	}
}

func TestTheTunnelIsRestartedOnlyWhenItsOwnSettingsChange(t *testing.T) {
	cases := []struct {
		name                  string
		wasServing, publishOn bool
		want                  int
	}{
		{"turned on", false, true, publishRestart},
		{"turned off", true, false, publishStop},
		{"already off", false, false, publishNothing},
		{"unchanged and serving", true, true, publishNothing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := publishAction(c.wasServing, c.publishOn); got != c.want {
				t.Errorf("action = %d, want %d", got, c.want)
			}
		})
	}
}

// The proxy address is deployment, not a setting: the page neither offers it nor
// writes it. A save that omitted it used to read back as "" and clear the
// operator's configured ingress.
func TestASaveLeavesTheProxyAddressAlone(t *testing.T) {
	srv, ts := newServer(t)
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte("TACIT_PUBLISH_INGRESS=ingress.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Cfg.PublishIngress = "ingress.example"
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")

	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)
	if strings.Contains(page, `name="publish_ingress"`) {
		t.Error("the settings page still offers the proxy address as a control")
	}

	post, err := admin.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {valueOf(t, page, "csrf")}, "admin_emails": {"ops@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = post.Body.Close()

	if srv.Cfg.PublishIngress != "ingress.example" {
		t.Errorf("PublishIngress = %q, want the configured address left alone", srv.Cfg.PublishIngress)
	}
	env, _ := os.ReadFile(envPath)
	if !strings.Contains(string(env), "TACIT_PUBLISH_INGRESS=ingress.example") {
		t.Errorf("registry.env no longer carries the operator's ingress:\n%s", env)
	}
}
