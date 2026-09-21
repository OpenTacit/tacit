// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// tacit upgrade — install-plan Phase C: fetch the latest release, verify it
// against the published checksums, and swap the running binary in place.
// The download is verified BEFORE anything is touched; the swap is a rename,
// so a failure at any point leaves the current binary exactly as it was.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	registryconfig "github.com/opentacit/tacit/internal/registry/config"
)

// tarMember streams a gzipped tar until the named member and returns a reader
// positioned at its content.
func tarMember(r io.Reader, member string) (io.Reader, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("member %s not found in archive", member)
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == member && hdr.Typeflag == tar.TypeReg {
			return tr, nil
		}
	}
}

// auditorAgentEnv is the member-settings marker: its presence means this
// machine has harness wiring worth refreshing after a binary swap.
func auditorAgentEnv() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return auditorconfig.AgentEnvPath(home)
}

// releaseRepo is the GitHub project releases are published from; TACIT_REPO
// overrides it (same knob install.sh honors).
const releaseRepo = "opentacit/tacit"

func repoSlug() string {
	if r := os.Getenv("TACIT_REPO"); r != "" {
		return r
	}
	return releaseRepo
}

// latestReleaseTag asks the GitHub API for the newest release tag; "" on any
// failure — callers treat this as "unknown", never as an error.
func latestReleaseTag(timeout time.Duration) string {
	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Get("https://api.github.com/repos/" + repoSlug() + "/releases/latest")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if json.NewDecoder(resp.Body).Decode(&rel) != nil {
		return ""
	}
	return rel.TagName
}

func cmdUpgrade(args []string) int {
	if inContainer() {
		fmt.Fprintln(os.Stderr, "this registry runs from a container image; to upgrade, pull a newer tag:")
		fmt.Fprintln(os.Stderr, "  docker pull ghcr.io/opentacit/tacit:latest   (then recreate the container)")
		return 1
	}
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	target := fs.String("version", "", "release tag to install (default: latest)")
	noRestart := fs.Bool("no-restart", false, "swap the binary but do not restart the registry service")
	if _, ok := parseFlags(fs, args); !ok {
		return exitUsage
	}

	tag := *target
	if tag == "" {
		if tag = latestReleaseTag(10 * time.Second); tag == "" {
			fmt.Fprintf(os.Stderr, "cannot find the latest release of %s\n", repoSlug())
			return 1
		}
	}
	if tag == version {
		fmt.Printf("already on %s\n", version)
		return 0
	}
	if version == "dev" || strings.Contains(version, "-") {
		fmt.Fprintf(os.Stderr, "note: this binary (%s) looks source-built; the release is possibly not newer\n", version)
	}

	bin, err := os.Executable()
	if err == nil {
		bin, err = filepath.EvalSymlinks(bin)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve current binary: %v\n", err)
		return 1
	}

	base := fmt.Sprintf("https://github.com/%s/releases/download/%s", repoSlug(), tag)
	name := fmt.Sprintf("tacit_%s_%s_%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("download started: %s\n", name)

	sums, err := httpGetAll(base + "/SHA256SUMS")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch SHA256SUMS: %v\n", err)
		return 1
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		fmt.Fprintf(os.Stderr, "%s is not in the release checksums — release %s has no %s/%s build\n", name, tag, runtime.GOOS, runtime.GOARCH)
		return 1
	}

	tmp, err := os.MkdirTemp(filepath.Dir(bin), ".tacit-upgrade-*")
	if err != nil {
		// The binary's directory may not be writable at all — surface that now.
		fmt.Fprintf(os.Stderr, "%v (make sure %s is writable)\n", err, filepath.Dir(bin))
		return 1
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, name)
	if err := downloadChecked(base+"/"+name, archive, want); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}

	f, err := os.Open(archive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer f.Close()
	member, err := tarMember(f, "tacit")
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract: %v\n", err)
		return 1
	}
	next := filepath.Join(tmp, "tacit")
	out, err := os.OpenFile(next, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if _, err := io.Copy(out, member); err != nil {
		out.Close()
		fmt.Fprintf(os.Stderr, "extract: %v\n", err)
		return 1
	}
	out.Close()
	if err := os.Rename(next, bin); err != nil {
		fmt.Fprintf(os.Stderr, "swap %s: %v\n", bin, err)
		return 1
	}
	fmt.Printf("upgraded %s: %s -> %s\n", bin, version, tag)

	// Plugin payloads ship inside the binary, so an upgrade that stops here
	// leaves every harness running the OLD plugin tree until someone
	// remembers `tacit connect`. Nobody remembers — chain it. It must be the
	// NEW binary's connect (this process still holds the old embedded tree),
	// and it is idempotent by design. Wired harnesses refresh; a failure here
	// is a wiring problem, not a failed upgrade.
	if _, err := os.Stat(auditorAgentEnv()); err == nil {
		fmt.Println("\nrefresh of harness wiring from the new binary (tacit connect):")
		cmd := exec.Command(bin, "connect")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "connect after upgrade: %v — run `%s connect` yourself\n", err, bin)
		}
	}

	// An operator upgrading a registry host expects the service to pick the
	// new binary up — mirror `make deploy`. Members have no unit; skipped.
	if !*noRestart && runtime.GOOS == "linux" {
		if err := exec.Command("systemctl", "--user", "is-active", "--quiet", "tacit-registry.service").Run(); err == nil {
			if out, err := exec.Command("systemctl", "--user", "restart", "tacit-registry.service").CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "restart tacit-registry: %v\n%s", err, out)
				return 1
			}
			if pollHealth(registryconfig.Load().Port, 15*time.Second) {
				fmt.Println("registry restarted and healthy")
			} else {
				fmt.Fprintln(os.Stderr, "registry restarted but did not answer /v1/health after 15s")
				return 1
			}
		}
	}
	return 0
}

func httpGetAll(url string) ([]byte, error) {
	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// downloadChecked streams url to dst and verifies the digest before returning.
func downloadChecked(url, dst, wantSHA string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("%s: digest mismatch (got %s, want %s)", url, got, wantSHA)
	}
	return f.Close()
}
