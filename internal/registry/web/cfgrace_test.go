// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"github.com/opentacit/tacit/internal/registry/config"
	"net/url"
	"os"
	"sync"
	"testing"
)

// A save must not tear the settings out from under the requests reading them.
//
// Thirteen settings hot-apply, which means a handler writes them while every
// other handler is reading — the admin list the auth check consults, the auth
// mode, the Global Access pair. They were written straight onto s.Cfg with
// nothing holding the two sides apart, and the struct already carried eight
// mutexes for smaller things.
//
// Run the package with -race for this to mean anything; without it the test
// only proves the two paths still work concurrently.
func TestSavingSettingsDoesNotRaceTheRequestsReadingThem(t *testing.T) {
	srv, ts := newServer(t)
	envPath := t.TempDir() + "/registry.env"
	if err := os.WriteFile(envPath, []byte("TACIT_API_KEY=k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	srv.mutateCfg(func(c *config.Config) { c.AdminEmails = []string{"ops@example.com"} })
	srv.RestartRegistry = func() (bool, error) { return false, nil }
	admin := signIn(t, srv, "ops@example.com")

	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 1<<20)
	n, _ := resp.Body.Read(body)
	_ = resp.Body.Close()
	csrf := extractCSRF(t, string(body[:n]))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			r, err := admin.PostForm(ts.URL+"/settings", url.Values{
				"csrf": {csrf}, "section": {"automation"}, "autonomy": {"on"},
			})
			if err == nil {
				_ = r.Body.Close()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			// Any page will do: the shell reads the admin list on every render.
			r, err := admin.Get(ts.URL + "/members")
			if err == nil {
				_ = r.Body.Close()
			}
		}
	}()
	wg.Wait()
}
