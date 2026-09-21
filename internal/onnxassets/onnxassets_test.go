// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package onnxassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func tgz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchPlainAndTarball(t *testing.T) {
	lib := []byte("fake shared library")
	model := []byte("fake model weights")
	archive := tgz(t, "onnxruntime/lib/libonnxruntime.so", lib)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/runtime.tgz":
			_, _ = w.Write(archive)
		case "/model.onnx":
			_, _ = w.Write(model)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	dir := t.TempDir()

	libDst := filepath.Join(dir, "libonnxruntime.so")
	rt := Artifact{URL: ts.URL + "/runtime.tgz", SHA256: sum(lib),
		Member: "onnxruntime/lib/libonnxruntime.so", Out: "libonnxruntime.so"}
	if err := Fetch(rt, libDst); err != nil {
		t.Fatalf("tarball fetch: %v", err)
	}
	if got, _ := os.ReadFile(libDst); !bytes.Equal(got, lib) {
		t.Fatalf("extracted member = %q, want %q", got, lib)
	}

	modelDst := filepath.Join(dir, "model.onnx")
	if err := Fetch(Artifact{URL: ts.URL + "/model.onnx", SHA256: sum(model), Out: "model.onnx"}, modelDst); err != nil {
		t.Fatalf("plain fetch: %v", err)
	}

	// Re-fetch verifies the existing file instead of downloading: point the
	// URL somewhere dead to prove no network happens.
	if err := Fetch(Artifact{URL: "http://127.0.0.1:1/nope", SHA256: sum(model), Out: "model.onnx"}, modelDst); err != nil {
		t.Fatalf("re-fetch of verified file should be a no-op, got %v", err)
	}
}

func TestFetchRejectsDigestMismatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered content"))
	}))
	defer ts.Close()
	dst := filepath.Join(t.TempDir(), "model.onnx")
	err := Fetch(Artifact{URL: ts.URL + "/model.onnx", SHA256: sum([]byte("expected content")), Out: "model.onnx"}, dst)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("want digest mismatch error, got %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatal("a failed download must not leave a file at the destination")
	}
}

func TestFetchRejectsExistingWrongFile(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "model.onnx")
	if err := os.WriteFile(dst, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Fetch(Artifact{URL: "http://127.0.0.1:1/nope", SHA256: sum([]byte("other")), Out: "model.onnx"}, dst)
	if err == nil || !strings.Contains(err.Error(), "does not match its pinned digest") {
		t.Fatalf("want pinned-digest refusal, got %v", err)
	}
}

func TestRuntimeLibPlatforms(t *testing.T) {
	for _, p := range []struct {
		goos, goarch string
		want         bool
	}{
		{"linux", "amd64", true}, {"linux", "arm64", true},
		{"darwin", "arm64", true}, {"darwin", "amd64", false}, {"windows", "amd64", false},
	} {
		if _, ok := RuntimeLib(p.goos, p.goarch); ok != p.want {
			t.Errorf("RuntimeLib(%s/%s) ok = %v, want %v", p.goos, p.goarch, ok, p.want)
		}
	}
}

// The pin and the binding have to agree. onnxruntime_go calls
// GetApi(ORT_API_VERSION) with the constant from the header it vendors, and a
// runtime older than that constant refuses to initialize; the registry treats
// the refusal as a missing embedder and serves hashing-v1, so nothing fails
// loudly and retrieval answers from the wrong vector space. ONNX Runtime 1.N
// serves API version N, which makes the pin's minor the number to compare.
//
// This is what a grouped dependency bump walks past: onnxruntime_go v1.36.0
// moved the constant from 26 to 29 while Version sat at 1.27.1, both build
// configurations still compiled, and the first symptom was suggestions that
// stopped matching.
func TestPinnedRuntimeServesTheBindingsAPIVersion(t *testing.T) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/yalue/onnxruntime_go").Output()
	if err != nil {
		t.Skipf("cannot locate onnxruntime_go in the module cache: %v", err)
	}
	header := filepath.Join(strings.TrimSpace(string(out)), "onnxruntime_c_api.h")
	b, err := os.ReadFile(header)
	if err != nil {
		t.Fatalf("read %s: %v", header, err)
	}
	m := regexp.MustCompile(`(?m)^#define ORT_API_VERSION\s+(\d+)`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("no ORT_API_VERSION in %s", header)
	}
	wants, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("ORT_API_VERSION %q: %v", m[1], err)
	}
	parts := strings.Split(Version, ".")
	if len(parts) != 3 {
		t.Fatalf("Version = %q, want major.minor.patch", Version)
	}
	serves, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("Version = %q: %v", Version, err)
	}
	if serves < wants {
		t.Fatalf("pinned ONNX Runtime %s serves C API version %d, but onnxruntime_go asks for %d: "+
			"the runtime refuses to initialize and retrieval falls back to hashing-v1. "+
			"Pin Version at 1.%d or newer and re-derive every digest above.", Version, serves, wants, wants)
	}
}

// Upstream packs the mac tarball with "./" in front of every member and the
// Linux ones without, and 1.30.0 flipped the mac one. The pinned member paths
// stay canonical and the match tolerates the prefix.
func TestTarMemberMatchesThroughLeadingDotSlash(t *testing.T) {
	lib := []byte("fake shared library")
	archive := tgz(t, "./onnxruntime-osx-arm64/lib/libonnxruntime.dylib", lib)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer ts.Close()
	dst := filepath.Join(t.TempDir(), "libonnxruntime.dylib")
	a := Artifact{URL: ts.URL + "/runtime.tgz", SHA256: sum(lib),
		Member: "onnxruntime-osx-arm64/lib/libonnxruntime.dylib", Out: "libonnxruntime.dylib"}
	if err := Fetch(a, dst); err != nil {
		t.Fatalf("fetch through a ./-prefixed tarball: %v", err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, lib) {
		t.Fatalf("extracted member = %q, want %q", got, lib)
	}
}
