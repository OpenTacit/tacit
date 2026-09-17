#!/bin/sh
# Copyright 2026 The OpenTacit Authors
# SPDX-License-Identifier: Apache-2.0

# Tacit installer — the `curl | sh` path:
#
#   curl -fsSL https://opentacit.com/install.sh | sh
#
# That address is a redirect to this file on the code host, served by the ingress
# at the zone's apex (internal/ingress/site.go). It is the address to quote:
# -L follows the hop, and the command survives the repository moving.
#
# Downloads the latest release binary for this platform, verifies its
# checksum, and installs it to ~/.local/bin. The script only fetches and
# places the binary; everything that configures or starts a registry lives in
# the binary itself (`tacit init`), where it is testable.
#
# Overrides: TACIT_VERSION (tag, default latest), TACIT_INSTALL_DIR
# (default ~/.local/bin), TACIT_REPO (default opentacit/tacit).
set -eu

REPO="${TACIT_REPO:-opentacit/tacit}"
VERSION="${TACIT_VERSION:-}"
INSTALL_DIR="${TACIT_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*" >&2; }
fail() { say "install.sh: $*"; exit 1; }

command -v curl >/dev/null 2>&1 || fail "this script needs curl"
command -v tar >/dev/null 2>&1 || fail "this script needs tar"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux | darwin) ;;
  *) fail "unsupported OS: $os (build from source: go install -tags onnx ./cmd/tacit)" ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported architecture: $arch" ;;
esac

if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$VERSION" ] || fail "cannot find the latest release of $REPO"
fi

base="https://github.com/$REPO/releases/download/$VERSION"
file="tacit_${VERSION}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "download started: tacit $VERSION ($os/$arch)"
curl -fsSL -o "$tmp/$file" "$base/$file" || fail "no release artifact $file under $base"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "cannot fetch SHA256SUMS"

# sha256sum is coreutils (linux); macOS ships shasum instead.
(
  cd "$tmp"
  grep " $file\$" SHA256SUMS >checksum.expected || fail "$file missing from SHA256SUMS"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c checksum.expected >/dev/null
  else
    shasum -a 256 -c checksum.expected >/dev/null
  fi
) || fail "checksum check failed for $file"

tar -xzf "$tmp/$file" -C "$tmp" tacit
mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/tacit" "$INSTALL_DIR/tacit"

say "installed tacit $("$INSTALL_DIR/tacit" version) to $INSTALL_DIR/tacit"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) say "note: $INSTALL_DIR is not on your PATH — add it to your shell profile" ;;
esac
# The registry sets TACIT_JOINING when it serves this script from a join link:
# that member is about to join somebody else's registry, and "start a registry
# for your org" is the one thing they must not do — undoing it later takes
# `tacit merge`. So the footer is for people who ran the plain installer.
if [ -z "${TACIT_JOINING:-}" ]; then
  say ""
  say "next steps:"
  say "  start a registry for your org:  tacit init"
  say "  join an existing registry:      tacit connect --registry <url> --key <key>"
fi
