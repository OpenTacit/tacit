#!/bin/sh
# Copyright 2026 The OpenTacit Authors
# SPDX-License-Identifier: Apache-2.0

# first_run.sh — meet Tacit the way a stranger does.
#
# Running `tacit init` on your own machine does not show you a first run. It
# shows you a first run wearing your settings: the default data directory is
# ~/.local/share/tacit/data, so a "new" registry opens on a store with thousands
# of events in it — the Outcomes page draws its full dashboard instead of the
# cold-start panel, the review queue already has drafts, and the funnel has
# numbers. Your systemd service is also holding port 8080, and your harnesses
# are already wired.
#
# So this overrides HOME. Everything `tacit init` writes — registry.env, the
# instance key, the data store, the techniques directory, the downloaded model —
# lands under a scratch directory and nowhere else. Your registry, your service,
# your store and your harness wiring are untouched, and there is nothing to undo
# afterwards but an rm.
#
# The registry runs in the foreground, exactly as it does for a new operator,
# and it dials ingress.tacit.zone for a public name exactly as a new operator's
# does — that address IS the first-run experience, and a script that quietly
# turned it off would be showing you a different product. Ctrl-C stops it, and
# it keeps ONE ingress identity across runs, so repeated first runs reuse a
# single hostname rather than spending the proxy's per-address enrolment budget.
# Run the script again for another clean first run; --clean hands the name back.
#
#   hack/first_run.sh                  a first run, on port 8099
#   hack/first_run.sh --port 9000      somewhere else
#   hack/first_run.sh --embeddings on  include the 90MB model download
#   hack/first_run.sh --local          skip the ingress; a machine-local address
#   hack/first_run.sh --clean          delete previous scratch runs and exit
#
set -eu

PORT=8099
EMBEDDINGS=off
GLOBAL_ACCESS=auto
BUILD=1
SCRATCH_ROOT="${TMPDIR:-/tmp}/tacit-first-run"

usage() {
  sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --embeddings) EMBEDDINGS="$2"; shift 2 ;;
    # For when you are looking at something else and do not want the tunnel:
    # the registry keeps a machine-local address and nothing is enrolled.
    --local) GLOBAL_ACCESS=off; shift ;;
    --no-build) BUILD=0; shift ;;
    --clean)
      # Hand the hostname back before deleting the key that names it, or it
      # stays allocated on the shared proxy with nothing able to claim it.
      if [ -f "$SCRATCH_ROOT/instance.key" ] && command -v tacit >/dev/null 2>&1; then
        C=$(mktemp -d)
        mkdir -p "$C/.config/tacit"
        cp "$SCRATCH_ROOT/instance.key" "$C/.config/tacit/instance.key"
        printf 'TACIT_API_KEY=x\nTACIT_GLOBAL_ACCESS=1\n' > "$C/.config/tacit/registry.env"
        chmod 600 "$C/.config/tacit/"*
        HOME="$C" tacit disconnect --registry --yes 2>&1 | grep -iE "released|no record" || true
        rm -rf "$C"
      fi
      rm -rf "$SCRATCH_ROOT"
      echo "removed $SCRATCH_ROOT"
      exit 0 ;;
    -h|--help) usage 0 ;;
    *) echo "first_run.sh: unknown option $1" >&2; usage 2 ;;
  esac
done

REPO=$(git rev-parse --show-toplevel 2>/dev/null) || {
  echo "first_run.sh: run this from inside the tacit checkout" >&2
  exit 1
}
cd "$REPO"

# Is the port free? A first run that collides with your own service reports a
# confusing "a registry is already answering" and tells you nothing about what a
# stranger would see. Named rather than killed: the thing on that port may be
# something you want.
if command -v ss >/dev/null 2>&1 && ss -ltn 2>/dev/null | grep -q ":$PORT[[:space:]]"; then
  echo "first_run.sh: something is already listening on port $PORT." >&2
  echo "  free it:            fuser -k $PORT/tcp" >&2
  echo "  or pick another:    hack/first_run.sh --port 9000" >&2
  exit 1
fi

SCRATCH="$SCRATCH_ROOT/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$SCRATCH/.config/tacit"

# ONE ingress identity across runs, kept beside the scratch directories.
#
# The instance key is what the shared proxy knows a registry by, and `tacit init`
# mints a new one per run. So every first run enrolled as a brand-new registry,
# and the ingress's per-source ceiling — new enrolments per address per hour, the
# thing that stops one script claiming every name — refused the seventh with "too
# many new registries from this address". Releasing the name on exit frees the
# NAME; it does not give the hour back.
#
# Reusing one key makes every run the same registry to the proxy: one enrolment,
# one hostname, and the same address each time, which is better for looking at
# anyway. --clean drops it, and the next run enrols once more.
KEYSTORE="$SCRATCH_ROOT/instance.key"
if [ -f "$KEYSTORE" ]; then
  cp "$KEYSTORE" "$SCRATCH/.config/tacit/instance.key"
  chmod 600 "$SCRATCH/.config/tacit/instance.key"
  echo "reusing this machine's scratch ingress identity ($KEYSTORE)"
fi

# The working-tree binary, not whatever is installed, so this shows the code you
# are editing. -tags onnx because that is what a release build carries, and
# without it semantic retrieval silently falls back to the lexical embedder.
#
# Installed to ~/.local/bin inside the scratch home, which is where install.sh
# puts it, and put on PATH. That is not tidiness: the CLI prints the command a
# reader can actually run (selfCommand), so a binary somewhere else makes every
# suggested command in the output a temp path. A first run is the one place the
# words have to be the words.
BIN="$SCRATCH/.local/bin/tacit"
mkdir -p "$SCRATCH/.local/bin"
if [ "$BUILD" = 1 ]; then
  echo "building the working tree (-tags onnx)…"
  go build -tags onnx -o "$BIN" ./cmd/tacit
else
  command -v tacit >/dev/null 2>&1 || { echo "first_run.sh: --no-build, but no tacit on PATH" >&2; exit 1; }
  cp "$(command -v tacit)" "$BIN"
fi

# The guard that makes the rest of this safe to run repeatedly. If HOME is not
# the scratch directory then everything below writes into the real one, and the
# whole point of the script is gone.
[ -n "$SCRATCH" ] && [ "$SCRATCH" != "$HOME" ] || {
  echo "first_run.sh: refusing to run with HOME unchanged" >&2
  exit 1
}

# Hand the ingress name back when this run ends, however it ends.
#
# Without this, every first run would leave a hostname allocated on the shared
# proxy — a name nobody can reclaim, for a registry that lived four minutes in
# /tmp. `tacit disconnect --registry` is the operator's own teardown and now
# releases the address as part of it, so the cleanup here is the command a real
# user would run, not a special path for this script.
#
# No exec, then: the script has to outlive the registry to do this.
# Keep the identity for the next run rather than releasing it: the name is this
# scratch machine's, the enrolment is already spent, and handing it back only to
# ask for another one is what ran the ceiling down. `--clean` is where it goes.
cleanup() {
  [ "$GLOBAL_ACCESS" = off ] && return 0
  if [ -f "$SCRATCH/.config/tacit/instance.key" ]; then
    cp "$SCRATCH/.config/tacit/instance.key" "$KEYSTORE"
    chmod 600 "$KEYSTORE"
  fi
}
trap cleanup EXIT INT TERM

env HOME="$SCRATCH" PATH="$SCRATCH/.local/bin:$PATH" "$BIN" init \
  --port "$PORT" \
  --embeddings "$EMBEDDINGS" \
  --global-access "$GLOBAL_ACCESS" \
  --service none
