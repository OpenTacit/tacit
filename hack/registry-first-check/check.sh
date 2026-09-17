#!/usr/bin/env bash
# Copyright 2026 The OpenTacit Authors
# SPDX-License-Identifier: Apache-2.0

# Validate the registry-first single-member tier, end to end, against a real ingress.
#
# What it proves, in the order a member meets it:
#   1. `tacit init --owner on --global-access on` configures single-member auth
#   2. the registry dials the ingress and is allocated a public hostname
#   3. `tacit dashboard` prints a sign-in link AT THAT HOSTNAME, not localhost
#   4. anonymous requests through the public hostname get the front door only
#   5. the link is exchanged for a session, and the dashboard renders behind it
#   6. a registry with NO sign-in refuses to publish at all
#
# It touches nothing of yours. Its own ingress, its own HOME, its own ports,
# its own data, all removed on exit — including on failure or Ctrl-C.
#
#   ./hack/registry-first-check/check.sh            # run everything
#   PORT=19111 INGRESS_PORT=19443 ./check.sh        # if those ports are busy
set -uo pipefail

PORT=${PORT:-19111}
INGRESS_PORT=${INGRESS_PORT:-19443}
TUNNEL_PORT=${TUNNEL_PORT:-19444}
ZONE=${ZONE:-localtest.me}          # *.localtest.me resolves to 127.0.0.1

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK=$(mktemp -d)
PASS=0
FAIL=0

cleanup() {
  for p in $PORT $INGRESS_PORT $TUNNEL_PORT; do fuser -k "$p"/tcp >/dev/null 2>&1; done
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

ok()   { printf '  \033[32mok\033[0m    %s\n' "$1"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL+1)); }
step() { printf '\n\033[1m%s\033[0m\n' "$1"; }

# A busy port would silently test somebody else's server.
for p in $PORT $INGRESS_PORT $TUNNEL_PORT; do
  if fuser "$p"/tcp >/dev/null 2>&1; then
    echo "port $p is in use — set PORT / INGRESS_PORT / TUNNEL_PORT and retry" >&2
    exit 2
  fi
done

step "Building"
( cd "$REPO" && go build -o "$WORK/tacit" ./cmd/tacit && go build -o "$WORK/tacit-ingress" ./cmd/tacit-ingress ) \
  || { echo "build failed" >&2; exit 1; }
ok "tacit and tacit-ingress built from $(cd "$REPO" && git rev-parse --short HEAD)"

# Its own ingress, so nothing enrols on the project's real one.
step "Starting an ingress on 127.0.0.1:$INGRESS_PORT (zone $ZONE)"
TACIT_INGRESS_DATA="$WORK/ingress-data" "$WORK/tacit-ingress" serve \
  --addr "127.0.0.1:$INGRESS_PORT" --tunnel-addr "127.0.0.1:$TUNNEL_PORT" \
  --zone "$ZONE" --data "$WORK/ingress-data" >"$WORK/ingress.log" 2>&1 &
disown   # so the shell does not report "Killed" when cleanup stops it
for _ in $(seq 20); do curl -sf --max-time 1 "http://127.0.0.1:$INGRESS_PORT/" -o /dev/null && break; sleep 1; done
curl -sf --max-time 2 "http://127.0.0.1:$INGRESS_PORT/" -o /dev/null \
  && ok "ingress answering" || { bad "ingress did not start (see $WORK/ingress.log)"; exit 1; }

export HOME="$WORK/home"
export TACIT_REGISTRY_ENV="$WORK/home/registry.env"
export TACIT_PUBLISH_INGRESS="http://127.0.0.1:$INGRESS_PORT"
mkdir -p "$HOME"

step "1. init: single-member auth, global access on"
"$WORK/tacit" init --embeddings off --service none --port "$PORT" \
  --owner on --global-access on >"$WORK/init.log" 2>&1
grep -q '^TACIT_AUTH_MODE=owner' "$TACIT_REGISTRY_ENV" \
  && ok "owner mode written to registry.env" || bad "owner mode not configured"
grep -q '^TACIT_OWNER_SECRET=.\+' "$TACIT_REGISTRY_ENV" \
  && ok "an owner secret was generated" || bad "no owner secret"
grep -q '^TACIT_GLOBAL_ACCESS=1' "$TACIT_REGISTRY_ENV" \
  && ok "global access on" || bad "global access not configured"
# init did not start anything (--service none), so it must say so rather than
# waiting for an address nothing will allocate.
grep -q 'start the registry' "$WORK/init.log" \
  && ok "init said what to do next instead of polling" || bad "init did not name the next step"

step "2. serve: the registry dials the ingress"
"$WORK/tacit" serve --port "$PORT" >"$WORK/serve.log" 2>&1 &
disown
for _ in $(seq 30); do curl -sf --max-time 1 "http://127.0.0.1:$PORT/v1/health" -o /dev/null && break; sleep 1; done
PUBLIC=""
for _ in $(seq 20); do
  PUBLIC=$(curl -s --max-time 2 "http://127.0.0.1:$PORT/v1/health" \
    | python3 -c 'import json,sys; h=json.load(sys.stdin); print(h.get("external_url") or "" if h.get("access")=="global" else "")' 2>/dev/null)
  [ -n "$PUBLIC" ] && break
  sleep 1
done
PUBLIC=${PUBLIC%/}
[ -n "$PUBLIC" ] && ok "allocated $PUBLIC" || { bad "no public address (see $WORK/serve.log)"; exit 1; }

# The address is allocated a moment before the tunnel is carrying traffic. Wait
# for one request to get through, so the checks below assert what they mean
# rather than how quickly a fresh tunnel settles.
READY=""
for _ in $(seq 15); do
  [ "$(curl -s --max-time 3 -o /dev/null -w '%{http_code}' "$PUBLIC/v1/health")" = "200" ] && { READY=yes; break; }
  sleep 1
done
[ -n "$READY" ] && ok "and it answers through the tunnel" || bad "the public address never answered"

step "3. dashboard: the link is at the public address"
LINK=$("$WORK/tacit" dashboard 2>/dev/null | grep -o 'http[s]*://[^ ]*auth/owner[^ ]*')
case "$LINK" in
  "$PUBLIC"/*) ok "sign-in link is at the ingress address" ;;
  *127.0.0.1*) bad "sign-in link is on the loopback: $LINK" ;;
  *)           bad "no sign-in link printed" ;;
esac

step "4. anonymous through the public address sees nothing"
ANON=$(curl -s --max-time 8 "$PUBLIC/settings")
echo "$ANON" | grep -q 'tacit dashboard' \
  && ok "front door tells a visitor how to sign in" || bad "no front door"
# "/" is the address somebody is handed: a visitor with no session gets the
# front door there, not the guide.
curl -s --max-time 8 "$PUBLIC/" | grep -q 'tacit dashboard' \
  && ok "and / answers a stranger with it, not the guide" || bad "/ served the guide to a stranger"
echo "$ANON" | grep -q 'Global Access' \
  && bad "the settings page leaked to an anonymous visitor" || ok "settings did not render"

step "5. the link signs in, and the dashboard renders"
CODE=$(curl -s --max-time 8 -c "$WORK/cookies" -o /dev/null -w '%{http_code}' "$LINK")
[ "$CODE" = "302" ] && ok "link exchanged for a session ($CODE)" || bad "sign-in returned $CODE"
grep -q 'tacit_session' "$WORK/cookies" && ok "session cookie set" || bad "no session cookie"
curl -s --max-time 8 -L -b "$WORK/cookies" "$PUBLIC/" | grep -q 'Outcomes' \
  && ok "the dashboard is reachable for the owner, through the public address" || bad "dashboard did not render"

# A single-member registry opens on the guide: its owner set the place up
# minutes ago and a funnel with nothing in it answers no question of theirs.
curl -s --max-time 8 -b "$WORK/cookies" -o /dev/null -w '%{redirect_url}' "$PUBLIC/" \
  | grep -q '/docs/user-guide' && ok "and it opens on the User Guide" || bad "/ did not open on the guide"
curl -s --max-time 8 -L -b "$WORK/cookies" "$PUBLIC/docs/user-guide" | grep -q '>User Guide<' \
  && ok "which is a tab of its own, before Outcomes" || bad "no User Guide tab"

# The owner is the operator: the settings they can see, they can change.
#
# Against the LOCAL address, not the public one. A browser submits the whole
# settings form; curl posts the fields named here and every unposted setting
# reads as empty — including Global Access, which would drop the tunnel this
# section has just finished testing.
# A session minted at the ingress hostname is a cookie for that host, which curl
# will not send to 127.0.0.1 — so sign in again locally rather than reusing it.
LOCAL="http://127.0.0.1:$PORT"
LLINK=$("$WORK/tacit" dashboard --local 2>/dev/null | grep -o 'http://127[^ ]*auth/owner[^ ]*')
curl -s --max-time 8 -c "$WORK/lcookies" -o /dev/null "$LLINK"
curl -s --max-time 8 -b "$WORK/lcookies" "$LOCAL/settings" >"$WORK/settings.html"
grep -q 'name="llm_key"' "$WORK/settings.html" \
  && ok "settings are editable by the owner, not read-only" || bad "the owner cannot edit settings"
CSRF=$(grep -o 'name="csrf" value="[a-f0-9]*"' "$WORK/settings.html" | head -1 | sed 's/.*value="\([a-f0-9]*\)".*/\1/')
[ -n "$CSRF" ] && ok "forms carry a CSRF token without an identity provider" || bad "no CSRF token"
curl -s --max-time 8 -b "$WORK/lcookies" -o /dev/null -w '%{http_code}' \
  -d "csrf=$CSRF" -d "llm_key=sk-set-through-the-ui" -d "admin_emails=" \
  -d "publish=on" -d "global_access_confirm=1" -d "publish_ingress=$TACIT_PUBLISH_INGRESS" \
  "$LOCAL/settings" | grep -q 303 \
  && ok "a setting saved through the UI" || bad "the save was refused"
grep -q '^TACIT_LLM_API_KEY=sk-set-through-the-ui' "$TACIT_REGISTRY_ENV" 2>/dev/null \
  && ok "and reached the registry's settings file" || bad "the saved setting did not persist"
curl -s --max-time 8 -o /dev/null -w '%{http_code}' \
  -d "csrf=$CSRF" -d "llm_key=sk-anonymous" -d "publish=on" "$LOCAL/settings" | grep -q 403 \
  && ok "and an anonymous visitor still cannot save one" || bad "an anonymous visitor changed settings"

# A saved setting has to survive a restart. Settings writes registry.env, and
# a registry started by hand had nothing loading that file into its
# environment, so a key read with os.Getenv came back empty on the next start.
fuser -k "$PORT"/tcp >/dev/null 2>&1
# Wait for the port to be released before binding it again: a kill returns
# before the socket does, and a restart that failed to bind reads exactly like
# a setting that did not persist.
for _ in $(seq 10); do fuser "$PORT"/tcp >/dev/null 2>&1 || break; sleep 1; done
"$WORK/tacit" serve --port "$PORT" >"$WORK/serve2.log" 2>&1 &
disown
BACK=""
for _ in $(seq 30); do
  curl -sf --max-time 1 "$LOCAL/v1/health" -o /dev/null && { BACK=yes; break; }
  sleep 1
done
[ -n "$BACK" ] || bad "the registry did not come back after a restart (see $WORK/serve2.log)"
LLINK2=$("$WORK/tacit" dashboard --local 2>/dev/null | grep -o 'http://127[^ ]*auth/owner[^ ]*')
curl -s --max-time 8 -c "$WORK/lcookies2" -o /dev/null "$LLINK2"
curl -s --max-time 8 -b "$WORK/lcookies2" "$LOCAL/settings" \
  | grep -q 'Leave blank to keep the current key' \
  && ok "a saved key survives a restart" || bad "the saved key was gone after a restart"

# The reported bug: saving anything THROUGH the public address stopped and
# re-dialled the tunnel, so the response went back over a connection that had
# just been closed — a hang, then "this registry has no spare connection right
# now". The whole form is posted here, the way a browser does.
PCSRF=$(curl -s --max-time 8 -b "$WORK/cookies" "$PUBLIC/settings" \
  | grep -o 'name="csrf" value="[a-f0-9]*"' | head -1 | sed 's/.*value="\([a-f0-9]*\)".*/\1/')
SAVE=$(curl -s --max-time 30 -b "$WORK/cookies" -o /dev/null -w '%{http_code}' \
  -d "csrf=$PCSRF" -d "llm_key=sk-saved-through-the-tunnel" -d "publish=on" \
  -d "global_access_confirm=1" -d "publish_ingress=$TACIT_PUBLISH_INGRESS" -d "admin_emails=" \
  "$PUBLIC/settings")
[ "$SAVE" = "303" ] && ok "a save through the public address answers ($SAVE)" \
  || bad "a save through the public address answered $SAVE"
# Give it a few seconds rather than one: the assertion is that the tunnel
# SURVIVES, and a fixed sleep turns a slow moment into a failure.
UP=""
for _ in $(seq 8); do
  [ "$(curl -s --max-time 5 -o /dev/null -w '%{http_code}' "$PUBLIC/settings")" = "200" ] && { UP=yes; break; }
  sleep 1
done
[ -n "$UP" ] && ok "and the tunnel is still up afterwards" || bad "the save took the tunnel down"

step "6. tacit init: one command, whether or not it has been set up"
fuser -k "$PORT"/tcp >/dev/null 2>&1; sleep 1
export HOME="$WORK/one" TACIT_REGISTRY_ENV="$WORK/one/registry.env"
mkdir -p "$HOME"
# Under `script`, because serving in the foreground is what init does for
# somebody at a keyboard: in a pipe it configures and returns, which is what a
# setup step in a script has to do.
script -qc "$WORK/tacit init --port $PORT --embeddings off --service none --global-access on" /dev/null \
  >"$WORK/init1.log" 2>&1 &
disown
for _ in $(seq 40); do grep -q 'auth/owner' "$WORK/init1.log" && break; sleep 1; done
grep -q 'starter techniques' "$WORK/init1.log" \
  && ok "first run configured a registry" || bad "first run did not set anything up"
grep -q 'API key:   unchanged' "$WORK/init1.log" \
  && bad "first run reported an API key it did not make" || ok "and made its key"
# tr -d '\r': the log came through a pty, so every line ends CRLF and the
# carriage return would travel inside the URL.
P_LINK=$(grep -o 'http://[^ ]*auth/owner[^ ]*' "$WORK/init1.log" | head -1 | tr -d '\r')
case "$P_LINK" in
  *localhost*|*127.0.0.1*) bad "init printed a loopback link: $P_LINK" ;;
  http*auth/owner*)        ok  "printed a sign-in link at the ingress address" ;;
  *)                       bad "init printed no sign-in link" ;;
esac
P_BASE=${P_LINK%%/auth/owner*}
curl -s --max-time 8 -c "$WORK/pcookies" -o /dev/null -w '%{http_code}' "$P_LINK" | grep -q 302 \
  && ok "that link signs the owner in" || bad "the link did not sign in"
# -L: in owner mode "/" opens on the guide, and every destination is one click
# away from there.
curl -s --max-time 8 -L -b "$WORK/pcookies" "$P_BASE/" | grep -q 'Outcomes' \
  && ok "the dashboard is reachable through it" || bad "dashboard did not render"

# Second run: nothing left to configure, so it just serves — and the address
# survives, because the hostname follows the instance key.
fuser -k "$PORT"/tcp >/dev/null 2>&1; sleep 1
script -qc "$WORK/tacit init --port $PORT --embeddings off --service none" /dev/null >"$WORK/init2.log" 2>&1 &
disown
for _ in $(seq 40); do grep -q 'auth/owner' "$WORK/init2.log" && break; sleep 1; done
grep -q 'API key:   unchanged' "$WORK/init2.log" \
  && ok "second run only served" || bad "second run set things up again"
grep -q "${P_BASE#http://}" "$WORK/init2.log" \
  && ok "same address across restarts" || bad "the address moved between runs"

step "7. one registry per machine, and it says so"
# A second process over one file store is the conflict this refuses to cause.
# The first is still serving from the run above.
timeout 10 script -qc "$WORK/tacit init --port $PORT --embeddings off --service none" /dev/null \
  >"$WORK/second.log" 2>&1
grep -q 'already answering on port' "$WORK/second.log" \
  && ok "refuses to start a second process over one file store" || bad "it started a second process"
fuser -k "$PORT"/tcp >/dev/null 2>&1; sleep 1

# A port held by something that is not a registry at all — on one machine this
# was code-server. The bind error serve would produce names the port and
# nothing else.
export HOME="$WORK/busy" TACIT_REGISTRY_ENV="$WORK/busy/registry.env"
mkdir -p "$HOME"
python3 -c "
import socket, time
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', $((PORT+3)))); s.listen(1); time.sleep(12)
" &
BLOCKER=$!
sleep 1
timeout 10 script -qc "$WORK/tacit init --port $((PORT+3)) --embeddings off --service none --global-access off" /dev/null \
  >"$WORK/busy.log" 2>&1
grep -q 'is in use by something that is not a registry' "$WORK/busy.log" \
  && ok "names a port held by something else, rather than a bind error" \
  || bad "a busy port produced no useful message"
kill "$BLOCKER" >/dev/null 2>&1

step "8. a registry with no sign-in refuses to publish"
fuser -k "$PORT"/tcp >/dev/null 2>&1
export HOME="$WORK/open" TACIT_REGISTRY_ENV="$WORK/open/registry.env"
mkdir -p "$HOME"
"$WORK/tacit" init --embeddings off --service none --owner off --port $((PORT+1)) \
  --global-access on >"$WORK/open-init.log" 2>&1
grep -q 'not turning Global Access on' "$WORK/open-init.log" \
  && ok "init refused to publish an ungated registry" \
  || bad "init let an open registry take a public address"

printf '\n\033[1m%d passed, %d failed\033[0m\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
