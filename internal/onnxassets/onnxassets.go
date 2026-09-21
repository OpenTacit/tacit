// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package onnxassets is the single source of truth for the semantic-retrieval
// artifacts (docs/design/embedder-onnx.md): the pinned ONNX Runtime library
// per platform and the sentence-transformer model files, each with the digest
// it must match. Both consumers — `tacit init` on an operator's machine and
// `tacit onnx-fetch` inside the Docker image build (docs/distribution/
// docker-plan.md) — fetch through here, so the pins can never drift between
// the install paths. The registry dlopens the library and feeds it the model:
// a swapped download must fail loudly, not load.
package onnxassets

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// Version is the pinned ONNX Runtime version: the release whose libraries
	// are fetched and whose digests are checked below.
	//
	// It is not a free choice. onnxruntime_go asks whatever library it loads
	// for a C API version — the ORT_API_VERSION in the header it bundles — and
	// a runtime older than that refuses to initialize. The registry survives
	// that refusal by falling back to hashing-v1, so the symptom is not a
	// crash but retrieval quietly answering from the wrong vector space. ONNX
	// Runtime 1.N serves API version N, so this pin has to stay at or above
	// the binding's. TestPinnedRuntimeServesTheBindingsAPIVersion checks the
	// pair, because a bump to the binding alone is what breaks it.
	Version = "1.30.0"
	base    = "https://github.com/microsoft/onnxruntime/releases/download/v" + Version + "/"
	// srcBase is the same release, as source: where the runtime's own licence
	// and third-party notices live. They are byte-identical to the copies
	// inside each platform tarball (checked for 1.30.0), so one small download
	// serves every platform instead of three large ones.
	srcBase = "https://raw.githubusercontent.com/microsoft/onnxruntime/v" + Version + "/"
	// ModelName is the sentence-transformer embedding model the registry runs.
	ModelName = "all-MiniLM-L6-v2"
	modelBase = "https://huggingface.co/sentence-transformers/" + ModelName + "/resolve/main/"

	// EmbedModelID/EmbedDim are what TACIT_EMBED_MODEL/TACIT_EMBED_DIM must be
	// set to for the fetched artifacts to be used.
	EmbedModelID = "onnx/" + ModelName
	EmbedDim     = "384"
)

// Artifact is one pinned download: URL, required digest, the member to
// extract when the source is a tarball ("" = plain file), and the filename to
// write.
type Artifact struct {
	URL    string
	SHA256 string
	Member string
	Out    string
}

// runtimeLibs maps GOOS/GOARCH to the pinned runtime library artifact.
// darwin/amd64 is absent: Microsoft stopped shipping Intel-mac builds, so
// init leaves those on hashing-v1 and says so.
var runtimeLibs = map[string]Artifact{
	"linux/amd64": {
		URL:    base + "onnxruntime-linux-x64-" + Version + ".tgz",
		SHA256: "245a6f8c38127551057a1cd1ffd59f0a186a227ade4f3492dea2494eb565542e",
		Member: "onnxruntime-linux-x64-" + Version + "/lib/libonnxruntime.so." + Version,
		Out:    "libonnxruntime.so." + Version,
	},
	"linux/arm64": {
		URL:    base + "onnxruntime-linux-aarch64-" + Version + ".tgz",
		SHA256: "64e903a43a041240fd6bcffe0ac6d4fea47ef87bf24b9d097801bd00a9612a4b",
		Member: "onnxruntime-linux-aarch64-" + Version + "/lib/libonnxruntime.so." + Version,
		Out:    "libonnxruntime.so." + Version,
	},
	"darwin/arm64": {
		URL:    base + "onnxruntime-osx-arm64-" + Version + ".tgz",
		SHA256: "bcc9110f9d638a119de2db7afb3ba9a1da8085f0cb3401e1c48ae1caf450b6fa",
		Member: "onnxruntime-osx-arm64-" + Version + "/lib/libonnxruntime." + Version + ".dylib",
		Out:    "libonnxruntime." + Version + ".dylib",
	},
}

// RuntimeLib returns the pinned runtime library for a platform, or ok=false
// where none is published.
func RuntimeLib(goos, goarch string) (Artifact, bool) {
	a, ok := runtimeLibs[goos+"/"+goarch]
	return a, ok
}

// RuntimeNotices are ONNX Runtime's own licence and the notices for everything
// its binary statically links — protobuf, Eigen, abseil, re2, and some thirty
// more, enumerated across six thousand lines.
//
// They are fetched because they must be redistributed. The MIT licence on the
// runtime requires its notice to accompany the binary, and the bundled
// components carry the same requirement; shipping libonnxruntime.so without
// them would be a licence violation in every image we publish. Eigen is
// MPL-2.0, which is why THIRD-PARTY-NOTICES.md does not claim the dependency
// tree is free of copyleft.
//
// Fetched alongside the library rather than vendored into this repository:
// they belong to a pinned version, and a copy committed here would drift the
// moment Version changed.
var RuntimeNotices = []Artifact{
	{
		URL:    srcBase + "LICENSE",
		SHA256: "2f07c72751aed99790b8a4869cf2311df85a860b22ded05fa22803587a48922c",
		Out:    "ONNXRUNTIME-LICENSE.txt",
	},
	{
		URL:    srcBase + "ThirdPartyNotices.txt",
		SHA256: "143764b952fdb1a7c69ce653bfba74a7744d6a8a573bfb73e235fba356c83de3",
		Out:    "ONNXRUNTIME-ThirdPartyNotices.txt",
	},
}

// ModelFiles are the model artifacts, fetched next to each other into one
// directory (TACIT_ONNX_MODEL_DIR).
var ModelFiles = []Artifact{
	{URL: modelBase + "onnx/model.onnx", SHA256: "6fd5d72fe4589f189f8ebc006442dbb529bb7ce38f8082112682524616046452", Out: "model.onnx"},
	{URL: modelBase + "vocab.txt", SHA256: "07eced375cec144d27c900241f3e339478dec958f92fddbc551f295c992038a3", Out: "vocab.txt"},
}

// Fetch ensures dst exists with the artifact's digest: verify if present,
// otherwise download (extracting a.Member when the source is a tarball) and
// verify before moving into place. Progress goes to stdout — both callers are
// CLI paths.
func Fetch(a Artifact, dst string) error {
	if sum, err := fileSHA256(dst); err == nil {
		if sum == a.SHA256 {
			return nil
		}
		return fmt.Errorf("%s exists but does not match its pinned digest — delete it to re-download", dst)
	}
	fmt.Printf("downloading %s ...\n", a.URL)
	resp, err := http.Get(a.URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", a.URL, resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tacit-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	var src io.Reader = resp.Body
	if a.Member != "" {
		f, err := tarMember(resp.Body, a.Member)
		if err != nil {
			return fmt.Errorf("%s: %w", a.URL, err)
		}
		src = f
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), src); err != nil {
		return err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != a.SHA256 {
		return fmt.Errorf("%s: digest mismatch (got %s, want %s)", a.URL, sum, a.SHA256)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// tarMember streams a gzipped tar until the named member and returns a reader
// positioned at its content. The tgz digest isn't pinned separately — the
// extracted member is what gets verified, because it is what gets loaded.
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
		// A leading "./" is packaging, not path: upstream writes the mac
		// tarball with it and the Linux ones without, and 1.30.0 changed which
		// way the mac one goes. Match the member either way so the next change
		// of mind is not a failed fetch.
		if strings.TrimPrefix(hdr.Name, "./") == member && hdr.Typeflag == tar.TypeReg {
			return tr, nil
		}
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
