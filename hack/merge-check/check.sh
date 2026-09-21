#!/usr/bin/env bash
# Copyright 2026 The OpenTacit Authors
# SPDX-License-Identifier: Apache-2.0

# Validate `tacit merge` end to end: one member's registry folded into an
# organization's, against a real ingress and two real registries.
#
# What it proves, in the order the command does it:
#   1. merge refuses anything that is not a single-member registry
#   2. the owner starts it from the Team page with the join link alone, and the
#      link buys a preview to pick from rather than sending anything
#   3. --dry-run says what would go and changes nothing
#   4. techniques land at the organization as CONTRIBUTED DRAFTS
#   5. no outcome figure crosses; the standing travels as prose, disclaimed
#   6. the evidence archive is written before anything is retired
#   7. the ledger makes a second run send nothing
#   8. the harnesses are rewired to the organization
#   9. the member's playbook shows what became of each contributed technique
#  10. it refuses to release the address of a registry it could not stop
#  11. stopped, it finishes: the hostname goes back to the ingress
#  12. a merged registry does not start again, and `init --start-over` is the
#      deliberate way to a new one
#
# It touches nothing of yours. Its own ingress, its own HOME, its own ports,
# its own data, all removed on exit — including on failure or Ctrl-C.
#
#   ./hack/merge-check/check.sh
#   PERSONAL_PORT=19121 ORG_PORT=19122 ./check.sh   # if those ports are busy
set -uo pipefail

PERSONAL_PORT=${PERSONAL_PORT:-19121}
ORG_PORT=${ORG_PORT:-19122}
INGRESS_PORT=${INGRESS_PORT:-19453}
TUNNEL_PORT=${TUNNEL_PORT:-19454}
ZONE=${ZONE:-localtest.me}

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK=$(mktemp -d)
PASS=0
FAIL=0

cleanup() {
  for p in $PERSONAL_PORT $ORG_PORT $INGRESS_PORT $TUNNEL_PORT; do fuser -k "$p"/tcp >/dev/null 2>&1; done
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

ok()   { printf '  \033[32mok\033[0m    %s\n' "$1"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL+1)); }
step() { printf '\n\033[1m%s\033[0m\n' "$1"; }

for p in $PERSONAL_PORT $ORG_PORT $INGRESS_PORT $TUNNEL_PORT; do
  if fuser "$p"/tcp >/dev/null 2>&1; then
    echo "port $p is in use — set PERSONAL_PORT / ORG_PORT / INGRESS_PORT / TUNNEL_PORT" >&2
    exit 2
  fi
done

step "Building"
( cd "$REPO" && go build -o "$WORK/tacit" ./cmd/tacit && go build -o "$WORK/tacit-ingress" ./cmd/tacit-ingress ) \
  || { echo "build failed" >&2; exit 1; }
ok "built from $(cd "$REPO" && git rev-parse --short HEAD)"

step "Starting an ingress on 127.0.0.1:$INGRESS_PORT (zone $ZONE)"
"$WORK/tacit-ingress" serve --addr "127.0.0.1:$INGRESS_PORT" --tunnel-addr "127.0.0.1:$TUNNEL_PORT" \
  --zone "$ZONE" --data "$WORK/ingress-data" >"$WORK/ingress.log" 2>&1 &
disown
for _ in $(seq 20); do curl -sf --max-time 1 "http://127.0.0.1:$INGRESS_PORT/" -o /dev/null && break; sleep 1; done
curl -sf --max-time 2 "http://127.0.0.1:$INGRESS_PORT/" -o /dev/null \
  && ok "ingress answering" || { bad "ingress did not start (see $WORK/ingress.log)"; exit 1; }

export HOME="$WORK/home"
mkdir -p "$HOME"
export TACIT_PUBLISH_INGRESS="http://127.0.0.1:$INGRESS_PORT"
ORG_ENV="$WORK/org/registry.env"
ORG="http://127.0.0.1:$ORG_PORT"
PERSONAL="http://127.0.0.1:$PERSONAL_PORT"

step "The organization's registry"
mkdir -p "$WORK/org"
TACIT_REGISTRY_ENV="$ORG_ENV" TACIT_DATA="$WORK/org/data" TACIT_TECHNIQUES_DIR="$WORK/org/techniques" \
  "$WORK/tacit" init --embeddings off --service none --port "$ORG_PORT" \
  --owner off --global-access off >"$WORK/org-init.log" 2>&1
ORG_KEY=$(grep '^TACIT_API_KEY=' "$ORG_ENV" | cut -d= -f2-)
TACIT_REGISTRY_ENV="$ORG_ENV" "$WORK/tacit" serve --port "$ORG_PORT" >"$WORK/org-serve.log" 2>&1 &
disown
for _ in $(seq 30); do curl -sf --max-time 1 "$ORG/v1/health" -o /dev/null && break; sleep 1; done
curl -sf --max-time 2 "$ORG/v1/health" -o /dev/null && ok "organization registry serving on $ORG_PORT" \
  || { bad "the org registry did not start (see $WORK/org-serve.log)"; exit 1; }

step "The member's own registry"
# init configures and returns here — nothing is at this keyboard, so it does not
# take the terminal — and serve runs it.
"$WORK/tacit" init --port "$PERSONAL_PORT" --embeddings off --global-access on --service none \
  >"$WORK/personal.log" 2>&1
PERSONAL_ENV="$HOME/.config/tacit/registry.env"
"$WORK/tacit" serve --port "$PERSONAL_PORT" >>"$WORK/personal.log" 2>&1 &
disown
for _ in $(seq 30); do curl -sf --max-time 1 "$PERSONAL/v1/health" -o /dev/null && break; sleep 1; done
curl -sf --max-time 2 "$PERSONAL/v1/health" -o /dev/null && ok "the member's registry is serving on $PERSONAL_PORT" \
  || { bad "the registry did not start (see $WORK/personal.log)"; exit 1; }
grep -q '^TACIT_AUTH_MODE=owner' "$PERSONAL_ENV" && ok "it is in single-member mode" || bad "not owner mode"
PERSONAL_KEY=$(grep '^TACIT_API_KEY=' "$PERSONAL_ENV" | cut -d= -f2-)

PUBLIC=""
for _ in $(seq 20); do
  PUBLIC=$(curl -s --max-time 2 "$PERSONAL/v1/health" \
    | python3 -c 'import json,sys; h=json.load(sys.stdin); print(h.get("external_url") or "" if h.get("access")=="global" else "")' 2>/dev/null)
  [ -n "$PUBLIC" ] && break
  sleep 1
done
PUBLIC=${PUBLIC%/}
[ -n "$PUBLIC" ] && ok "it holds the public address $PUBLIC" || bad "no public address allocated"

step "A technique of the member's own, with a history behind it"
curl -s --max-time 5 -X POST "$PERSONAL/v1/contribute" -H "X-Tacit-Key: $PERSONAL_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Render the page before believing the CSS","description":"Look at the rendered output, not the stylesheet.","recipe":"After a visual change, render the page headlessly and look at the result.","scope":"general","tags":["verification"]}' \
  >"$WORK/seed.json"
SEED_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("id",""))' "$WORK/seed.json")
[ -n "$SEED_ID" ] && ok "seeded $SEED_ID" || { bad "could not seed a technique"; cat "$WORK/seed.json"; }
# Promote it: a draft is something the member has not decided about, and merge
# contributes what their registry actually serves.
ROOT_KEY=$(grep '^TACIT_ROOT_KEY=' "$PERSONAL_ENV" | cut -d= -f2-)
[ -z "$ROOT_KEY" ] && ROOT_KEY="$PERSONAL_KEY"
curl -s --max-time 5 -X POST "$PERSONAL/v1/admin/promote" -H "X-Tacit-Key: $ROOT_KEY" \
  -H 'Content-Type: application/json' -d "{\"id\":\"$SEED_ID\",\"status\":\"stable\"}" -o "$WORK/promote.json"
grep -q stable "$WORK/promote.json" && ok "promoted to stable on the member's registry" || bad "promotion failed"

# Three shown, two adopted, one helped — the evidence that must NOT cross.
EVENTS=$(python3 - "$SEED_ID" <<'PY'
import json,sys,uuid
tid=sys.argv[1]
rows=[]
def e(stage,value=None):
    r={"event_id":str(uuid.uuid4()),"audit_id":str(uuid.uuid4()),"technique_id":tid,
       "stage":stage,"segment":{},"confidence":"explicit","created_at":"2026-06-12T09:00:00Z"}
    if value is not None: r["value"]=value
    rows.append(r)
for _ in range(3): e("shown")
for _ in range(2): e("adopted")
e("helped", True)
print(json.dumps(rows))
PY
)
curl -s --max-time 5 -X POST "$PERSONAL/v1/feedback" -H "X-Tacit-Key: $PERSONAL_KEY" \
  -H 'Content-Type: application/json' -d "$EVENTS" -o "$WORK/feedback.json"
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if d.get("accepted",0)==6 else 1)' "$WORK/feedback.json" \
  && ok "6 outcome events recorded on the member's registry" || bad "events not accepted: $(cat "$WORK/feedback.json")"

step "1. merge refuses what is not a single-member registry"
OUT=$(TACIT_REGISTRY_ENV="$ORG_ENV" "$WORK/tacit" merge 2>&1)
echo "$OUT" | grep -q 'not a single-member registry' \
  && ok "an organization's registry is not one member's to fold" || bad "merge did not refuse: $OUT"

step "2. an invitation from the organization"
INVITE=$(TACIT_REGISTRY_ENV="$ORG_ENV" "$WORK/tacit" invite --registry "$ORG" --key "$ORG_KEY" 2>&1 \
  | grep -o "$ORG/join/[A-Za-z0-9._-]*" | head -1)
[ -n "$INVITE" ] && ok "join link issued" || { bad "no join link"; exit 1; }

step "3. --dry-run says what would go, and changes nothing"
DRY=$("$WORK/tacit" merge "$INVITE" --dry-run 2>&1)
echo "$DRY" | grep -q 'Render the page' && ok "it names the technique" || bad "no technique listed: $DRY"
echo "$DRY" | grep -q '3 shown' && ok "and the standing it would claim" || bad "no standing sentence"
echo "$DRY" | grep -q 'Nothing was changed' && ok "and says it changed nothing" || bad "no such assurance"
DRAFTS=$(curl -s --max-time 5 "$ORG/v1/techniques?status=draft" -H "X-Tacit-Key: $ORG_KEY" \
  | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["techniques"]))')
[ "$DRAFTS" = "0" ] && ok "the organization has no drafts yet" || bad "--dry-run contributed $DRAFTS drafts"

step "4. the merge started from the Team page"
# The owner should not have to reach for a terminal to begin: the join link is
# the whole credential, and the page they are already looking at takes it.
LLINK=$("$WORK/tacit" dashboard --local 2>&1 | tee "$WORK/dashboard.log" | grep -o 'http://127[^ ]*auth/owner[^ ]*')
[ -n "$LLINK" ] || bad "tacit dashboard printed no sign-in link: $(tail -2 "$WORK/dashboard.log" | tr -d '\n')"
curl -s --max-time 8 -c "$WORK/cookies" -o /dev/null "$LLINK"
curl -s --max-time 8 -b "$WORK/cookies" "$PERSONAL/team" >"$WORK/offer.html"
grep -q 'action="/admin/merge"' "$WORK/offer.html" \
  && ok "the Team page offers the merge to its owner" || bad "no merge form on the page"
grep -q 'name="key"' "$WORK/offer.html" \
  && bad "the form asks for a credential beyond the join link" || ok "and asks for one thing: the join link"
# A stranger must not be offered a control that hands the playbook away.
curl -s --max-time 8 "$PERSONAL/team" | grep -q 'action="/admin/merge"' \
  && bad "the merge form rendered for a signed-out visitor" || ok "and shows it to nobody else"

CSRF=$(grep -o 'name="csrf" value="[a-f0-9]*"' "$WORK/offer.html" | head -1 | sed 's/.*value="\([a-f0-9]*\)".*/\1/')
[ -n "$CSRF" ] && ok "the form carries a CSRF token" || bad "no CSRF token on the merge form"
# Two posts, not one (M8): the link buys a PREVIEW of what would go, with a
# tick beside each technique, and the member decides before anything crosses.
POST_CODE=$(curl -s --max-time 10 -b "$WORK/cookies" -o "$WORK/preview.html" -w '%{http_code}' \
  -d "csrf=$CSRF" --data-urlencode "link=$INVITE" "$PERSONAL/admin/merge")
[ "$POST_CODE" = "200" ] \
  && ok "the link buys a preview" || bad "the form post was refused ($POST_CODE): $(head -c 200 "$WORK/preview.html")"
grep -q 'action="/admin/merge/contribute"' "$WORK/preview.html" \
  && ok "which asks what to send before sending it" || bad "no selection form in the preview"
PICKS=$(grep -o 'name="id" value="[^"]*"' "$WORK/preview.html" | sed 's/.*value="\([^"]*\)".*/\1/' | sort -u)
[ -n "$PICKS" ] && ok "with the member's own technique on it" || bad "the preview offered nothing to send"
CSRF_P=$(grep -o 'name="csrf" value="[a-f0-9]*"' "$WORK/preview.html" | head -1 | sed 's/.*value="\([a-f0-9]*\)".*/\1/')
PICK_ARGS=()
for id in $PICKS; do PICK_ARGS+=(-d "id=$id"); done
POST_CODE=$(curl -s --max-time 10 -b "$WORK/cookies" -o "$WORK/merge-post.txt" -w '%{http_code}' \
  -d "csrf=$CSRF_P" "${PICK_ARGS[@]}" "$PERSONAL/admin/merge/contribute")
[ "$POST_CODE" = "303" ] \
  && ok "the merge started" || bad "the selection was refused ($POST_CODE): $(head -c 200 "$WORK/merge-post.txt")"
for _ in $(seq 20); do
  curl -s --max-time 8 -b "$WORK/cookies" "$PERSONAL/team" >"$WORK/progress.html"
  grep -q 'archived to' "$WORK/progress.html" && break
  sleep 1
done
grep -q 'contributed as' "$WORK/progress.html" \
  && ok "the page reports each technique as it crosses" || bad "no progress shown: $(grep -o 'Contributing your playbook' "$WORK/progress.html")"
grep -q 'archived to' "$WORK/progress.html" \
  && ok "and says where the evidence was archived" || bad "the UI merge did not archive"

step "5. the command finishes what the page started"
"$WORK/tacit" merge --keep-address >"$WORK/merge1.log" 2>&1
grep -q 'already there as' "$WORK/merge1.log" \
  && ok "it found the work the page had already contributed" || bad "the command re-sent it: $(tail -5 "$WORK/merge1.log")"

curl -s --max-time 5 "$ORG/v1/techniques?status=draft" -H "X-Tacit-Key: $ORG_KEY" >"$WORK/drafts.json"
python3 - "$WORK/drafts.json" <<'PY' >"$WORK/draft-check.txt"
import json,sys
ts=json.load(open(sys.argv[1]))["techniques"]
d=[t for t in ts if "Render the page" in t.get("name","")]
print("count", len(d))
if d:
    t=d[0]
    print("status", t.get("status"))
    print("provenance", t.get("provenance"))
    print("version", t.get("version"))
    print("has_embedding", "embedding" in t)
    desc=t.get("description","")
    print("claims", "3 shown" in desc)
    print("disclaimed", "Not measured by this organization" in desc)
    print("outcome_fields", any(k in t for k in ("shown","adopted","helped","outcomes")))
PY
grep -q '^count 1' "$WORK/draft-check.txt" && ok "it is at the organization, once" || bad "draft count wrong: $(cat "$WORK/draft-check.txt")"
grep -q '^status draft' "$WORK/draft-check.txt" && ok "as a draft, held out of retrieval" || bad "status is not draft"
grep -q '^provenance contributed' "$WORK/draft-check.txt" && ok "labelled contributed" || bad "provenance wrong"
grep -q '^version 1' "$WORK/draft-check.txt" && ok "at version 1, not the source registry's" || bad "version carried over"
grep -q '^claims True' "$WORK/draft-check.txt" && ok "the standing rides in the description" || bad "no standing in the draft"
grep -q '^disclaimed True' "$WORK/draft-check.txt" && ok "and says it was not measured here" || bad "the claim is not disclaimed"
grep -q '^outcome_fields False' "$WORK/draft-check.txt" && ok "no outcome figure crossed as data" || bad "outcome data crossed"

ARCHIVE=$(ls "$HOME"/tacit-personal-archive-*.json 2>/dev/null | head -1)
[ -n "$ARCHIVE" ] && ok "evidence archived to $(basename "$ARCHIVE")" || bad "no archive written"
if [ -n "$ARCHIVE" ]; then
  python3 - "$ARCHIVE" <<'PY' >"$WORK/archive-check.txt"
import json,sys
a=json.load(open(sys.argv[1]))
print("events", len(a["events"]))
o=[x for x in a["outcomes"] if x["shown"]]
print("shown", o[0]["shown"] if o else 0)
print("adopted", o[0]["adopted"] if o else 0)
print("helped", o[0]["helped"] if o else 0)
print("says_why", "sample of one" in a["note"])
PY
  grep -q '^events 6' "$WORK/archive-check.txt" && ok "it holds all 6 events" || bad "archive events: $(cat "$WORK/archive-check.txt")"
  grep -q '^shown 3' "$WORK/archive-check.txt" && ok "folded to 3 shown, 2 adopted, 1 helped" || bad "archive outcomes wrong"
  grep -q '^says_why True' "$WORK/archive-check.txt" && ok "and says why it exists" || bad "the archive does not explain itself"
fi

grep -q "TACIT_REGISTRY_URL=$ORG" "$HOME/.config/tacit/agent.env" \
  && ok "this machine's agents now point at the organization" || bad "agent.env was not rewired"
curl -sf --max-time 2 "$PERSONAL/v1/health" -o /dev/null \
  && ok "--keep-address left the registry running" || bad "the registry was stopped despite --keep-address"

step "6. a second run sends nothing"
"$WORK/tacit" merge --keep-address >"$WORK/merge2.log" 2>&1
grep -q 'already there as' "$WORK/merge2.log" && ok "it recognised what had gone" || bad "no ledger recall: $(tail -5 "$WORK/merge2.log")"
grep -q 'Resuming the merge' "$WORK/merge2.log" && ok "and resumed with no new invitation" || bad "it needed a fresh link"
AGAIN=$(curl -s --max-time 5 "$ORG/v1/techniques?status=draft" -H "X-Tacit-Key: $ORG_KEY" \
  | python3 -c 'import json,sys; print(len([t for t in json.load(sys.stdin)["techniques"] if "Render the page" in t.get("name","")]))')
[ "$AGAIN" = "1" ] && ok "still one copy at the organization, not two" || bad "a second run made $AGAIN copies"

step "7. the Team page says what became of the work"
# The third act: the decision is made in the organization's queue and nothing
# comes back, so the registry has to ask.
LLINK=$("$WORK/tacit" dashboard --local 2>&1 | tee "$WORK/dashboard.log" | grep -o 'http://127[^ ]*auth/owner[^ ]*')
[ -n "$LLINK" ] || bad "tacit dashboard printed no sign-in link: $(tail -2 "$WORK/dashboard.log" | tr -d '\n')"
curl -s --max-time 8 -c "$WORK/cookies" -o /dev/null "$LLINK"
curl -s --max-time 8 -b "$WORK/cookies" "$PERSONAL/team" >"$WORK/playbook1.html"
grep -q 'Asking your organization now' "$WORK/playbook1.html" \
  && ok "the first render does not block on the network" || bad "no 'asking' line on the first render"
sleep 2
curl -s --max-time 8 -b "$WORK/cookies" "$PERSONAL/team" >"$WORK/playbook2.html"
grep -q 'Contributed to' "$WORK/playbook2.html" \
  && ok "the playbook names the organization it contributed to" || bad "no contributed panel"
grep -q 'waiting for review' "$WORK/playbook2.html" \
  && ok "and says the technique is waiting in its review queue" || bad "no standing shown"

# Promote it at the organization, and the answer must change.
DRAFT_ID=$(python3 -c 'import json,sys; ts=json.load(open(sys.argv[1]))["techniques"]; print([t["id"] for t in ts if "Render the page" in t["name"]][0])' "$WORK/drafts.json")
curl -s --max-time 5 -X POST "$ORG/v1/admin/promote" -H "X-Tacit-Key: $ORG_KEY" \
  -H 'Content-Type: application/json' -d "{\"id\":\"$DRAFT_ID\",\"status\":\"stable\"}" -o /dev/null
# The answer is cached for ten minutes on purpose, so restart to re-ask rather
# than pretending a review queue moves at page-load speed.
fuser -k "$PERSONAL_PORT"/tcp >/dev/null 2>&1
for _ in $(seq 10); do fuser "$PERSONAL_PORT"/tcp >/dev/null 2>&1 || break; sleep 1; done
"$WORK/tacit" serve --port "$PERSONAL_PORT" >>"$WORK/personal.log" 2>&1 &
disown
for _ in $(seq 30); do curl -sf --max-time 1 "$PERSONAL/v1/health" -o /dev/null && break; sleep 1; done
LLINK=$("$WORK/tacit" dashboard --local 2>&1 | tee "$WORK/dashboard.log" | grep -o 'http://127[^ ]*auth/owner[^ ]*')
[ -n "$LLINK" ] || bad "tacit dashboard printed no sign-in link: $(tail -2 "$WORK/dashboard.log" | tr -d '\n')"
curl -s --max-time 8 -c "$WORK/cookies2" -o /dev/null "$LLINK"
curl -s --max-time 8 -b "$WORK/cookies2" "$PERSONAL/team" -o /dev/null
sleep 2
curl -s --max-time 8 -b "$WORK/cookies2" "$PERSONAL/team" >"$WORK/playbook3.html"
grep -q 'in your organization' "$WORK/playbook3.html" \
  && ok "once promoted there, the member's own playbook says so" || bad "the promotion never showed up"

step "8. it will not release the address of a registry it cannot stop"
# A `tacit serve` in a terminal is not this command's to kill. Hunting for a
# process is not its business; naming the one that has to go is.
"$WORK/tacit" merge --yes >"$WORK/merge3.log" 2>&1
grep -q 'still answering' "$WORK/merge3.log" \
  && ok "it says which process has to stop" || bad "it did not name the running registry: $(tail -6 "$WORK/merge3.log")"
grep -q 'Released' "$WORK/merge3.log" \
  && bad "it released the address of a registry that is still running" || ok "and released nothing"
[ -f "$HOME/.config/tacit/instance.key" ] \
  && ok "the instance key is still there" || bad "the key went despite the failed stop"

step "9. the page finishes what it started"
# The registry serving this page can stop itself, which is the most reliable
# stop there is — no unit file to find, no foreign terminal to negotiate with.
curl -s --max-time 8 -b "$WORK/cookies2" "$PERSONAL/team" >"$WORK/finish.html"
grep -q 'action="/admin/merge/finish"' "$WORK/finish.html" \
  && ok "the Team page offers to finish the merge" || bad "no finish control on the page"
grep -q 'cannot be undone' "$WORK/finish.html" \
  && ok "and says the address does not come back" || bad "the finish control does not warn"
curl -s --max-time 8 "$PERSONAL/team" | grep -q '/admin/merge/finish' \
  && bad "the finish control rendered for a signed-out visitor" || ok "and offers it to nobody else"

CSRF2=$(grep -o 'name="csrf" value="[a-f0-9]*"' "$WORK/finish.html" | head -1 | sed 's/.*value="\([a-f0-9]*\)".*/\1/')
# Point this machine somewhere else first, so what the finish does to the
# harnesses is its own work and not what step 5 already did.
sed -i 's|^TACIT_REGISTRY_URL=.*|TACIT_REGISTRY_URL=http://127.0.0.1:1|' "$HOME/.config/tacit/agent.env"
# Unconfirmed does none of it.
curl -s --max-time 8 -b "$WORK/cookies2" -o /dev/null -w '%{http_code}' \
  -d "csrf=$CSRF2" "$PERSONAL/admin/merge/finish" | grep -q 303 \
  && ok "an unconfirmed post changes nothing" || bad "the unconfirmed post was not turned back"
curl -sf --max-time 3 "$PERSONAL/v1/health" -o /dev/null \
  && ok "and the registry is still running" || bad "an unconfirmed finish stopped the registry"

curl -s --max-time 20 -b "$WORK/cookies2" -d "csrf=$CSRF2" -d "confirm=yes" \
  "$PERSONAL/admin/merge/finish" >"$WORK/farewell.html"
grep -q 'Merged into' "$WORK/farewell.html" \
  && ok "the last page it serves says what happened" || bad "no farewell page: $(head -c 300 "$WORK/farewell.html")"
grep -q 'Released' "$WORK/farewell.html" \
  && ok "and reports the address released, because the request came in locally" || bad "the local finish did not report the release"

step "10. and then it is gone"
for _ in $(seq 20); do curl -sf --max-time 1 "$PERSONAL/v1/health" -o /dev/null || break; sleep 1; done
curl -sf --max-time 3 "$PERSONAL/v1/health" -o /dev/null \
  && bad "the registry is still serving after finishing" || ok "the registry stopped itself"
grep -q "TACIT_REGISTRY_URL=$ORG" "$HOME/.config/tacit/agent.env" \
  && ok "it wired this machine's tools to the organization itself" || bad "the harnesses were not rewired by the finish"
[ -f "$HOME/.config/tacit/instance.key" ] \
  && bad "the instance key survived a release" || ok "the instance key is gone with it"
curl -sf --max-time 2 "$PERSONAL/v1/health" -o /dev/null \
  && bad "the registry is still serving locally" || ok "it serves nothing locally either"
sleep 1
curl -sf --max-time 3 "$PUBLIC/v1/health" -o /dev/null \
  && bad "the public address still answers" || ok "the public address answers nothing"
python3 - "$WORK/ingress-data/instances.json" <<'PY' && ok "the route table forgot it" || bad "the ingress still lists the instance"
import json,os,sys
p=sys.argv[1]
if not os.path.exists(p): sys.exit(0)
raw=open(p).read().strip()
sys.exit(0 if not raw or raw in ("[]","{}") or len(json.loads(raw))==0 else 1)
PY

step "11. a merged registry does not start again"
# The elimination, stated as a refusal. Starting from the files a merged
# registry left behind would BE that registry: the same data, serving a playbook
# that has already moved, under a ledger reporting on an instance that is gone.
OLD_DATA=$(grep '^TACIT_DATA=' "$PERSONAL_ENV" 2>/dev/null | cut -d= -f2-)
timeout 20 "$WORK/tacit" serve --port "$PERSONAL_PORT" >"$WORK/restart.log" 2>&1
grep -q 'merged and is finished' "$WORK/restart.log" \
  && ok "serve refuses the registry that was merged" || bad "it started a merged registry: $(tail -3 "$WORK/restart.log")"
grep -q "$ORG" "$WORK/restart.log" \
  && ok "and says where the playbook went" || bad "the refusal does not name the organization"
curl -sf --max-time 2 "$PERSONAL/v1/health" -o /dev/null \
  && bad "something is serving it anyway" || ok "nothing is serving it"
timeout 20 "$WORK/tacit" init --port "$PERSONAL_PORT" --embeddings off --service none \
  >"$WORK/reinit.log" 2>&1
grep -q 'merged and is finished' "$WORK/reinit.log" \
  && ok "and setting up over it is refused too" || bad "init brought the merged registry back"

step "12. starting over stands up a new registry, and keeps the old one's evidence"
"$WORK/tacit" init --start-over --port "$PERSONAL_PORT" --embeddings off --global-access on \
  --service none >"$WORK/startover.log" 2>&1
grep -q 'the merged registry is kept at' "$WORK/startover.log" \
  && ok "it says where the merged one was put" || bad "it did not report moving the old data aside"
[ -f "$PERSONAL_ENV" ] \
  && ok "this machine's settings stayed where they are" || bad "the sweep took the machine's settings"
[ -n "$OLD_DATA" ] && ls -d "$OLD_DATA.merged-"* >/dev/null 2>&1 \
  && ok "and the merged registry's evidence is kept, not deleted" || bad "the data was not preserved"
[ -f "$HOME/.config/tacit/merged.json" ] \
  && bad "the ledger that ended it is still in force" || ok "the ledger was retired with it"
NEW_KEY=$(grep '^TACIT_API_KEY=' "$PERSONAL_ENV" | cut -d= -f2-)
[ -n "$NEW_KEY" ] && [ "$NEW_KEY" != "$PERSONAL_KEY" ] \
  && ok "the new registry has credentials of its own" || bad "it reuses the merged registry's API key"
"$WORK/tacit" serve --port "$PERSONAL_PORT" >>"$WORK/startover.log" 2>&1 &
disown
for _ in $(seq 40); do curl -sf --max-time 1 "$PERSONAL/v1/health" -o /dev/null && break; sleep 1; done
curl -sf --max-time 2 "$PERSONAL/v1/health" -o /dev/null \
  && ok "and it serves" || bad "the new registry did not come up (see $WORK/startover.log)"

# The new instance must be new: no ledger, so no report about somebody else's
# merge, and a different address from the one that was handed back.
LLINK3=$("$WORK/tacit" dashboard --local 2>&1 | tee "$WORK/dashboard.log" | grep -o 'http://127[^ ]*auth/owner[^ ]*')
[ -n "$LLINK3" ] || bad "tacit dashboard printed no sign-in link: $(tail -2 "$WORK/dashboard.log" | tr -d '\n')"
curl -s --max-time 8 -c "$WORK/cookies3" -o /dev/null "$LLINK3"
curl -s --max-time 8 -b "$WORK/cookies3" "$PERSONAL/team" >"$WORK/fresh.html"
grep -q 'Contributed to' "$WORK/fresh.html" \
  && bad "the new registry reports the old one's merge" || ok "it reports no merge of its own"
grep -q 'action="/admin/merge"' "$WORK/fresh.html" \
  && ok "and offers to merge, like any unmerged registry" || bad "the new registry cannot be merged"

NEWPUB=""
for _ in $(seq 20); do
  NEWPUB=$(curl -s --max-time 2 "$PERSONAL/v1/health" | python3 -c 'import json,sys; h=json.load(sys.stdin); print(h.get("external_url") or "" if h.get("access")=="global" else "")' 2>/dev/null)
  [ -n "$NEWPUB" ] && break
  sleep 1
done
NEWPUB=${NEWPUB%/}
[ -n "$NEWPUB" ] && [ "$NEWPUB" != "$PUBLIC" ] \
  && ok "and enrolled at a new address ($NEWPUB, was ${PUBLIC##*//})" || bad "address: got '$NEWPUB', old was '$PUBLIC'"

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
