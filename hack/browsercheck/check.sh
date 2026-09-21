#!/usr/bin/env bash
# Copyright 2026 The OpenTacit Authors
# SPDX-License-Identifier: Apache-2.0

# Boot a scratch registry with the demo month in it and run check.js against it.
# Touches nothing of yours: its own HOME, data, port; all removed on exit.
# See README.md — Playwright is not vendored, so NODE_PATH must find it.
set -uo pipefail

PORT=${PORT:-9099}
REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK=$(mktemp -d)

cleanup() { fuser -k "$PORT"/tcp >/dev/null 2>&1; rm -rf "$WORK"; }
trap cleanup EXIT INT TERM

if fuser "$PORT"/tcp >/dev/null 2>&1; then
  echo "port $PORT is in use — set PORT and retry" >&2; exit 2
fi
if ! node -e 'require("playwright")' 2>/dev/null; then
  echo "playwright not on NODE_PATH — see hack/browsercheck/README.md" >&2; exit 2
fi

echo "Building"
go build -o "$WORK/tacit" "$REPO/cmd/tacit" || exit 1

echo "Serving on 127.0.0.1:$PORT"
mkdir -p "$WORK/home/data"
HOME="$WORK/home" "$WORK/tacit" serve -port "$PORT" -data "$WORK/home/data" \
  -docs "$REPO/docs" -techniques "$REPO/techniques" >"$WORK/serve.log" 2>&1 &
disown
for _ in $(seq 60); do
  curl -sf --max-time 1 "http://127.0.0.1:$PORT/v1/health" -o /dev/null && break; sleep 0.5
done

# The first-run gate: claim with the code the console printed, taking the rest
# of the wizard's own defaults.
CLAIM=$(grep -oE '[A-Z0-9]{4}-[A-Z0-9]{4}' "$WORK/serve.log" | head -1)
if [ -z "$CLAIM" ]; then echo "no claim code in the serve log" >&2; exit 1; fi
curl -sf "http://127.0.0.1:$PORT/setup" -o "$WORK/setup.html" || exit 1
KEY=$(python3 - "$WORK/setup.html" "$CLAIM" "http://127.0.0.1:$PORT" <<'PY'
import re, sys, urllib.parse, urllib.request
html = open(sys.argv[1]).read()
f = {}
for m in re.finditer(r'<(input|select)\b([^>]*)>', html):
    a = dict(re.findall(r'(\w+)="([^"]*)"', m.group(2)))
    if a.get("name"): f[a["name"]] = a.get("value", "")
f["claim_code"] = sys.argv[2]
req = urllib.request.Request(sys.argv[3] + "/setup", data=urllib.parse.urlencode(f).encode())
try: urllib.request.urlopen(req)
except urllib.error.HTTPError as e:
    print("setup failed:", e.read().decode()[:400], file=sys.stderr); raise SystemExit(1)
print(f["api_key"])
PY
) || exit 1

echo "Loading the demo month"
"$WORK/tacit" demo load -registry "http://127.0.0.1:$PORT" -key "$KEY" >/dev/null || exit 1

echo
BASE="http://127.0.0.1:$PORT" node "$REPO/hack/browsercheck/check.js"
